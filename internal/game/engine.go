package game

import (
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aitown/internal/config"
	"aitown/internal/llm"
	"aitown/internal/sim"
	"aitown/internal/workflow"
	"aitown/internal/worldgen"
	"aitown/internal/xlog"
)

// Hub 事件出口（server 包的 SSE hub 实现）。
type Hub interface {
	Publish(v any)
}

// Engine 游戏引擎：世界线程（唯一的物理权威）+ 一组村民 agent goroutine。
//
// 并发模型（actor 变体）：
//   - 世界线程（loop goroutine）独占 sim.World/jobs/convo 的变更，10Hz tick：
//     邮箱回流 → 物理结算（移动/劳作/时间/再生）→ 事件投递到村民信箱 → 快照广播
//   - 每个村民一条 agent goroutine：信箱等事件 + 1s 心跳自主决策（独立思考），
//     对世界做短临界区快操作（拿 e.mu），对 LLM 做阻塞调用（只占自己）
//   - HTTP/LLM 回调不直接改世界：HTTP 走锁内公共 API；LLM 回调经 mail 回流世界线程
type Engine struct {
	// mu 世界大锁：保护本结构全部字段与 sim.World 的短临界区快操作。
	// 使用者：世界线程（每 tick 持锁结算）、HTTP 公共 API、村民 agent 的快操作。
	// 村民的慢等待（走到/聊完/LLM）不持锁——通过各自信箱 select 完成。
	mu sync.Mutex
	// world 仿真世界的权威状态（地形/实体/库存/逻辑时钟）；nil 表示"还没有世界"。
	// 只被世界线程（loop goroutine）在锁内变更；村民经 agentHost 的快操作在锁内触碰。
	world *sim.World
	// cits 村民的大脑侧身份数据表（姓名/职业/性格/家/记忆/关系），key = 居民 ID。
	// 静态字段（ID/Name/Role/性格）创建后不变；动态字段（心情/记忆/关系）由各自
	// agent goroutine 私有读写，其他人须经 BrainView 原子视图读取。
	cits map[string]*Citizen
	// ord 居民 ID 注册顺序：快照输出、对话找邻居等按此顺序遍历（确定性）。
	ord []string
	// agents 村民执行体表：key = 居民 ID。每个条目是一条独立 goroutine（actor），
	// 拥有信箱/心跳/私有决策状态。只在 setupWorld（持锁）写入。
	agents map[string]*agent

	// pacing 节奏参数（设置页热更）：原子指针，供村民 goroutine 无锁读取冷却等参数。
	pacing atomic.Pointer[config.PacingConfig]

	// gw BYOK 的 LLM 网关：所有 AI 调用（世界初始化/规划/对话/决策/反思/旁白）的唯一出口。
	// 村民 goroutine 用它的阻塞版 Complete（只占自己）；世界线程用 CompleteAsync + mail 回流。
	gw *llm.Gateway
	// hub 事件出口（server 的 SSE Hub）：world/event/chat/bubble/edict/快照全从这里广播。
	hub Hub

	// saver 存档后端（main 注入；nil = 纯内存运行，不落盘）。
	saver Saver
	// saveCh 异步存档队列（容量 1：上一份没写完就跳过本次，日界每天才一次）。
	saveCh chan saveJob
	// lastSaveDay 上次自动存档的游戏天数：新的一天第一次推进时触发异步存档。
	lastSaveDay int

	// wf workflow 解释器：内置模板注册表。模板启动后只读，可被任意 agent 并发查询；
	// Execute 由村民 goroutine 调用（阻塞式跑完一个模板实例）。
	wf *workflow.Engine

	// jobs 工作单列表（领主指令分解的产物）：pending/claimed/done/failed 生命周期。
	// 只被世界线程变更；村民经 tryClaim（锁内）认领、jobFinished（锁内）核销。
	jobs []*Job
	// jobSeq 工作单 ID 单调计数器（不随修剪回退，避免 ID 复用）。
	jobSeq int
	// convos 当前进行中的对话（世界线程编排轮转）；nil = 空闲。
	// 同时场数上限见 convCap（设置页 conv_cap，默认 2；LLM 风暴另有网关限流兜底）。
	convos map[string]*Conversation
	// convCap 同时进行的对话场数上限（<=0 视为 2）。
	convCap int
	// edicts 最近的领主指令文本（保留 5 条）：村民闲聊时作为话题素材。
	edicts []string

	// mail 世界线程的延迟工作队列：LLM 异步回调（规划结果/对话轮转）投递闭包，
	// 每 tick 开头 drainMail 串行执行——异步结果回到世界线程的唯一通道。
	// 村民 agent 不使用它（他们有自己的信箱，且阻塞调用天然在自身线程完成）。
	mail chan func()
	// stop 世界线程的关闭信号：close 后 loop 退出（Stop() 触发，v0 与进程同生命周期）。
	stop chan struct{}
	// running 世界线程是否已启动（Start 幂等的依据；测试置 true 以手动逐 tick 驱动世界）。
	running bool
	// generating 世界正在生成中（防重复创建；setupWorld 完成后置 false）。
	generating bool

	// lastSnap 上次快照广播的真实时刻：节流到 250ms 一帧，避免 SSE 被刷爆。
	lastSnap time.Time
	// lastFullWarn 仓库满导致的交付失败提示节流（30 秒最多一条玩家事件）。
	lastFullWarn time.Time
	// lastFoodWarnDay 已发"粮仓见底"预警的天数：一天最多提醒一次。
	lastFoodWarnDay int
	// lastEventHour 事件调度：上次掷骰的小时键（tick/150），避免一小时内重复掷骰。
	lastEventHour int64
	// eventLastDay 各事件的最近触发天数（冷却用）。
	eventLastDay map[string]int
	// forceEventID 测试钩子：强制下一个调度周期触发指定事件。
	forceEventID string

	// testTimeFactor 仅供测试放大时间流速（同时作用于逻辑时钟与移动速度）。
	testTimeFactor float64
}

// NewEngine 构造引擎并装载内置 workflow 模板。
func NewEngine(gw *llm.Gateway, hub Hub) *Engine {
	e := &Engine{
		gw:             gw,
		hub:            hub,
		cits:           map[string]*Citizen{},
		agents:         map[string]*agent{},
		eventLastDay:   map[string]int{},
		mail:           make(chan func(), 512),
		stop:           make(chan struct{}),
		testTimeFactor: 1,
		convCap:        2,
	}
	e.wf = workflow.NewEngine()
	if err := e.wf.LoadBuiltin(); err != nil {
		log.Fatalf("加载内置 workflow 模板失败: %v", err)
	}
	defaultPacing := config.DefaultPacing()
	e.pacing.Store(&defaultPacing)
	return e
}

// Start 启动世界线程（幂等）。
func (e *Engine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.startLocked()
}

func (e *Engine) startLocked() {
	if e.running {
		return
	}
	e.running = true
	go e.loop()
}

// Stop 优雅关停：退出存档（同步）→ 取消全部村民 goroutine → 停止世界线程。
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 退出存档：尽力同步落盘，失败只记日志（不阻塞关停）
	if e.saver != nil && e.world != nil {
		if b, err := e.marshalSaveLocked(); err != nil {
			xlog.Warn("退出存档序列化失败", "err", err)
		} else if err := e.saver.Save(b, e.world.Name, e.world.Day()); err != nil {
			xlog.Warn("退出存档失败", "err", err)
		}
	}
	for _, ag := range e.agents {
		ag.cancel()
	}
	if e.running {
		// 关闭stop通道，所有监听的goroutine都收到零值
		close(e.stop)
		e.running = false
	}
}

func (e *Engine) loop() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				e.tickOnce(0.1)
			}()
		}
	}
}

// tickOnce 世界线程推进一步：邮箱回流 → 物理结算 → 事件投递 → 快照广播。
// 村民的大脑不在这一步里——他们是独立的 goroutine。
func (e *Engine) tickOnce(dt float64) {
	e.drainMail()
	if e.world == nil {
		return
	}
	w := e.world
	ticks := int64(0)
	// 世界暂停的时候什么都不做
	if !w.Paused {
		// 1个时间单位
		ticks = int64(dt * 10 * e.testTimeFactor)
		// 至少向前一步
		if ticks < 1 {
			ticks = 1
		}
	}
	adt := dt * e.testTimeFactor * w.SeasonMoveMul() // 移动随倍率与季节减速（冬季 ×0.9）

	if ticks > 0 {
		w.Tick += ticks
		for _, id := range w.ActOrd {
			a := w.Actors[id]
			switch a.Busy {
			case sim.BusyMove:
				if w.StepActorMove(a, adt) {
					e.deliver(id, Event{Kind: EvArrived})
				}
			case sim.BusyWork:
				if done, outcome := w.StepActorWork(a); done {
					target := ""
					if a.Work != nil {
						target = a.Work.TargetID
					}
					a.Busy = sim.BusyNone
					a.Work = nil
					if outcome == "built" { // 建筑落成：世界结构变化 → 全量推送 + 全员围观 + 公共账本
						if b := w.Bld[target]; b != nil {
							e.publishEventLocked("build", b.Name+" 建成了！")
							w.RecalcInvCap()
							e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
						}
					}
					e.deliver(id, Event{Kind: EvWorkDone, Outcome: outcome})
				}
			}
		}
		// 重新生成资源
		w.RegenResources()
		// 夜间全员入睡：黑屏快进到清晨（治愈节奏不留漫漫长夜的垃圾时间）
		if w.IsNight() && e.allAsleepLocked() {
			e.skipToMorning()
		}
		e.dayTick()
		e.autoSaveIfNewDayLocked()
		e.maybeWorldEvent()
	}
	if time.Since(e.lastSnap) >= 250*time.Millisecond {
		e.lastSnap = time.Now()
		e.publish(e.snapshotLocked())
	}
}

func (e *Engine) drainMail() {
	for {
		select {
		case f := <-e.mail:
			f()
		default:
			goto pending
		}
	}
pending:
}

// allAsleepLocked 夜间快进条件：全体村民都已入睡。需持锁。
// "睡着即算睡"：睡眠发生在哪个 workflow 里（nightRest 机械入睡 / LLM sleep 意图）
// 不影响判定——之前要求 run==nil 会把"带着睡眠 workflow 睡着的人"误判为清醒，
// 导致跳夜永不触发、长夜按真实时间空转（实测：全 7 人 sleeping 却整夜不快进）。
func (e *Engine) allAsleepLocked() bool {
	if len(e.ord) == 0 {
		return false
	}
	for _, id := range e.ord {
		ag := e.agents[id]
		if ag == nil {
			continue
		}
		if !ag.sleeping {
			return false
		}
	}
	return true
}

// skipToMorning 夜间快进：时钟跳到"严格晚于当前的最近日界时刻"（默认 7 点），
// 睡着的村民由各自轮询自然唤醒。
// 注意不能用 Day() 直接推算：夜晚横跨午夜，00:00-06:00 触发时 Day() 已加 1，
// 再按 Day()+1 推算会多跳一整天（实测：第 2 天凌晨触发直接跳到第 3 天 7 点）。需持锁。
func (e *Engine) skipToMorning() {
	w := e.world
	dayIdx := w.Tick / sim.TicksPerDay
	target := dayIdx*sim.TicksPerDay + int64(e.Pacing().DayBreak*sim.TicksPerHour)
	if target <= w.Tick {
		target += sim.TicksPerDay
	}
	w.Tick = target
	w.RegenResources()
	e.publishEventLocked("world", "夜色温柔，领地沉沉睡去……清晨的鸟鸣把大家唤醒了。")
}

// resLabels 库存资源的中文名（面向玩家的仓库提示用）。
var resLabels = map[string]string{
	"wood": "木材", "food": "浆果", "stone": "石料",
	"wheat": "小麦", "flour": "面粉", "bread": "面包",
}

// intentOutputRes 意图/工作类型 → 其产出资源键（用于满仓需求门控）。
var intentOutputRes = map[string]string{
	"gather_wood": "wood", "gather_food": "food", "gather_stone": "stone",
	"farm_tend": "wheat", "grind": "flour", "bake_bread": "bread",
}

// outputFullLocked 该意图（或工作单类型）的产出资源是否已满仓——满了就不该再去做。
// 需持锁。
func (e *Engine) outputFullLocked(name string) bool {
	res, ok := intentOutputRes[name]
	if !ok || e.world == nil {
		return false
	}
	return e.world.ResFull(res)
}

// notifyStorageFull 仓库满导致的交付失败：节流 30 秒最多一条玩家事件（此前完全静默，
// 玩家无从知道"麦子爆仓饿死了浆果入库"）。
func (e *Engine) notifyStorageFull(villager string, full []string) {
	e.mu.Lock()
	if time.Since(e.lastFullWarn) < 30*time.Second {
		e.mu.Unlock()
		return
	}
	e.lastFullWarn = time.Now()
	e.mu.Unlock()
	names := make([]string, 0, len(full))
	for _, r := range full {
		if lb, ok := resLabels[r]; ok {
			names = append(names, lb)
		} else {
			names = append(names, r)
		}
	}
	e.publishEvent("system", villager+"背回来的物资交不进去："+strings.Join(names, "、")+"仓已满（消耗一些就能腾出位置）。")
}

// deliver 把事件投进村民信箱——**世界线程写入 mailbox 的唯一入口**。
//
//	谁在投：
//	  · tickOnce：EvArrived（移动完成）/ EvWorkDone（劳作完成）——机械信号
//	  · chat.go：EvInvite（邀请对方聊天）/ EvMemo（对话便签）/ EvChatDone（对话结束）
//	  · Edict：EvEdict（领主指令原文）——全村各投一条
//	特性：带缓冲、永不阻塞世界线程；信箱满（村民病态卡顿）就丢弃——快照流自愈。
//	必须在世界线程（或持锁上下文）中调用：投递前为事件盖章游戏时间，
//	村民写记忆从此无需读世界时钟（消除该路径的锁）。
func (e *Engine) deliver(actorID string, ev Event) {
	if ag := e.agents[actorID]; ag != nil {
		if e.world != nil { // 投递前盖章游戏时间（调用方均持锁）
			ev.Day = e.world.Day()
			ev.Hour = e.world.Hour()
		}
		ag.deliver(ev)
	}
}

// dayTick 世界线程的日节拍：粮仓预警 + 心情自然回归。
func (e *Engine) dayTick() {
	w := e.world
	if w.Hour() < e.Pacing().DayBreak {
		return
	}
	if w.Inventory["food"] < 6 && w.Day() != e.lastFoodWarnDay {
		e.lastFoodWarnDay = w.Day()
		e.publishEventLocked("system", "粮仓快见底了。领主该下令补充食物了（在控制台输入指令）。")
	}
	// 心情缓慢回归平淡：每天向 0 收敛 1 点（情绪有半衰，不是永久状态）
	for _, c := range e.cits {
		if c.Mood > 0 {
			c.AdjustMood(-1)
		} else if c.Mood < 0 {
			c.AdjustMood(1)
		}
	}
}

// ---------- 公共 API（server 调用，自行加锁） ----------

// HasWorld 是否已有世界。
func (e *Engine) HasWorld() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.world != nil
}

// CreateWorld 世界初始化 workflow：用户需求 → LLM 规格 → 确定性建图 → 村民 goroutine 诞生。
func (e *Engine) CreateWorld(prompt string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.world != nil {
		return errors.New("世界已存在（v0 版本请重启进程开新世界）")
	}
	if e.generating {
		return errors.New("世界正在生成中，请稍候")
	}
	e.generating = true
	e.publishEventLocked("system", "开始生成世界：正在构思土地与居民…")
	req := &llm.Request{
		Role:        llm.RoleWorldgen,
		System:      "",
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		JSONMode:    true,
		Temperature: e.gw.Temperature(llm.RoleWorldgen, 0.9),
	}
	e.gw.CompleteAsync(req, func(resp *llm.Response, err error) {
		e.mail <- func() { e.setupWorld(resp, err) }
	})
	e.startLocked()
	return nil
}

// setupWorld 由邮箱回调（世界线程）：解析规格 → 建图（建图部分与默认世界共用）。
func (e *Engine) setupWorld(resp *llm.Response, err error) {
	spec := worldgen.DefaultSpec()
	if err != nil {
		e.publishEventLocked("system", "LLM 不可用（"+err.Error()+"），使用默认世界「"+spec.Name+"」")
	} else {
		parsed, perr := llm.ParseData[worldgen.Spec](resp.Text)
		if perr != nil {
			e.publishEventLocked("system", "世界规格解析失败，使用默认世界（"+perr.Error()+"）")
		} else {
			spec = worldgen.Normalize(&parsed)
		}
	}
	e.buildWorldFromSpecLocked(spec)
}

// CreateDefaultWorld 立即铺设内置默认世界（跳过 LLM）：同步完成，返回后 world 帧已广播。
// 用于前端"直接铺默认世界"入口——不产生任何 LLM 调用与等待。
func (e *Engine) CreateDefaultWorld() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.world != nil {
		return errors.New("世界已存在（v0 版本请重启进程开新世界）")
	}
	if e.generating {
		return errors.New("世界正在生成中，请稍候")
	}
	spec := worldgen.DefaultSpec()
	e.publishEventLocked("system", "正在铺设默认世界「"+spec.Name+"」…")
	e.buildWorldFromSpecLocked(spec)
	e.startLocked()
	return nil
}

// buildWorldFromSpecLocked 由世界规格建图并诞生村民 goroutine（AI 世界与默认世界共用）。
// 需持锁。
func (e *Engine) buildWorldFromSpecLocked(spec *worldgen.Spec) {
	seed := time.Now().UnixNano()
	w := worldgen.BuildWorld(spec, seed, 0)
	// 节奏参数注入世界：夜间区间 + 从日界时刻开局（设置页可调）
	pacing := e.Pacing()
	w.NightStart, w.NightEnd = pacing.NightStart, pacing.NightEnd
	w.Tick = int64(pacing.DayBreak * sim.TicksPerHour)
	e.world = w
	e.cits = map[string]*Citizen{}
	e.agents = map[string]*agent{}
	e.ord = nil
	e.jobs = nil
	e.convos = map[string]*Conversation{}

	owner := map[string]string{}
	for i, v := range spec.Villagers {
		if i >= len(w.ActOrd) {
			break
		}
		a := w.Actors[w.ActOrd[i]]
		home := w.FindFreeHome(owner, a.ID)
		if home != nil {
			owner[a.ID] = home.ID
		}
		homeID := ""
		if home != nil {
			homeID = home.ID
		}
		c := &Citizen{
			ID:          a.ID,
			Name:        v.Name,
			Role:        v.Role,
			Personality: v.Personality,
			Traits:      v.Traits,
			HomeID:      homeID,
			Rel:         map[string]int{},
		}
		e.cits[a.ID] = c
		e.ord = append(e.ord, a.ID)
		c.AddMemory(w, "event", "在「"+w.Name+"」安了家", 3)
	}
	// 诞生独立执行流：从这一刻起村民开始自主生活（与读档重建共用）
	e.spawnAgentsLocked()
	// 新世界预任命官员（设置页可调；对话至少需要两名官员，也可以开局全平民）
	startup := e.Pacing().StartupOfficials
	for i, id := range e.ord {
		if i >= startup {
			break
		}
		if c := e.cits[id]; c != nil {
			c.Tier = TierOfficial
		}
	}
	e.generating = false
	// 日界自动存档基点：新世界也从"今天"起算，跨过第一个日界就会自动存
	// （漏了这行会让前两次日界白跳：autoSaveIfNewDayLocked 把首个观测日只当基准）
	e.lastSaveDay = w.Day()
	e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
	e.publishEventLocked("world", "「"+w.Name+"」诞生了。"+strconv.Itoa(len(spec.Villagers))+" 名居民开始新的生活。")
	// 新世界立即落一份档：既给"今天"留底，也保证"开新世界"会覆盖旧档
	// （不必等第一个日界；玩家中途退出也不会让旧档阴魂不散）
	e.saveAsyncLocked()
}

// spawnAgentsLocked 为 e.cits 里每个村民建执行体 goroutine（新世界与读档共用）。
// 幂等：已有执行体的 ID 跳过。需持锁。
func (e *Engine) spawnAgentsLocked() {
	for _, id := range e.ord {
		c := e.cits[id]
		if c == nil {
			continue
		}
		if _, ok := e.agents[id]; ok {
			continue
		}
		ag := newAgent(e, c)
		e.agents[id] = ag
		go ag.mainLoop()
	}
}

// Edict 领主下指令：广播给玩家与全村（人人记入通知板）→ 规划器（LLM）分解为工作单。
// 指令原文经 EvEdict 投递给每个村民：下一步思考时 LLM 能在【未读通知】里读到"领主说了什么"。
func (e *Engine) Edict(text string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.world == nil {
		return errors.New("请先创建世界")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("指令不能为空")
	}
	e.edicts = append(e.edicts, text)
	if len(e.edicts) > 5 {
		e.edicts = e.edicts[len(e.edicts)-5:]
	}
	e.publish(&EdictMsg{T: "edict", Text: text})
	e.publishEventLocked("edict", "领主下令："+text)
	// 全村感知：指令原文进每个人的通知板与记忆（是否理会、怎么行动仍由村民自己决定）
	for _, id := range e.ord {
		e.deliver(id, Event{Kind: EvEdict, Outcome: "领主下令：" + text})
	}
	e.requestPlan(text)
	return nil
}

// SetPaused 暂停/继续（领主手动控制）。
func (e *Engine) SetPaused(p bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.world != nil {
		e.world.Paused = p
	}
}

// SetConvCap 设置同时进行的对话场数上限（设置页 conv_cap 热生效；<=0 回退 2）。
func (e *Engine) SetConvCap(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n <= 0 {
		n = 2
	}
	e.convCap = n
}

// Pacing 当前节奏参数（原子读，任意 goroutine 可用）。
func (e *Engine) Pacing() config.PacingConfig {
	if p := e.pacing.Load(); p != nil {
		return *p
	}
	return config.DefaultPacing()
}

// SetPacing 热更新节奏参数（设置页保存时调用；钳位后生效）。
func (e *Engine) SetPacing(p config.PacingConfig) {
	p.Sanitize()
	e.pacing.Store(&p)
}

// StateData GET /api/state 的载荷（响应另含 config 掩码字段，由 server 层拼装）。
type StateData struct {
	// HasWorld 是否已存在世界。
	HasWorld bool `json:"has_world"`
	// World 世界全量数据；未创建时为 null。
	World *WorldJSON `json:"world"`
	// Snapshot 当前快照；未创建时为 null。
	Snapshot *SnapshotMsg `json:"snapshot"`
	// Generating 世界是否正在生成中。
	Generating bool `json:"generating"`
	// Save 存档概况（启动"继续/新世界"选择页用）。
	Save SaveMeta `json:"save"`
}

// State 组装完整状态（进页面时拉取）。
func (e *Engine) State() StateData {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := StateData{HasWorld: e.world != nil, Generating: e.generating, Save: e.saveMetaLocked()}
	if e.world != nil {
		st.World = e.worldJSONLocked()
		st.Snapshot = e.snapshotLocked()
	}
	return st
}

func (e *Engine) publish(v any) {
	if e.hub != nil {
		e.hub.Publish(v)
	}
}

// publishEventLocked 世界线程使用的日志事件（调用方已持锁）。
// 游戏事件全量镜像进诊断日志（INFO），与游戏内控制台逐条对应。
func (e *Engine) publishEventLocked(cat, text string) {
	day, hour := 0, 0.0
	if e.world != nil {
		day, hour = e.world.Day(), e.world.Hour()
	}
	xlog.Info("事件", "cat", cat, "text", text)
	e.publish(&EventMsg{T: "event", Cat: cat, Text: text, Day: day, Hour: hour})
}

// publishEvent 任意 goroutine 可用的日志事件（内部短暂拿锁读时间）。
// 游戏事件全量镜像进诊断日志（INFO），与游戏内控制台逐条对应。
func (e *Engine) publishEvent(cat, text string) {
	e.mu.Lock()
	day, hour := 0, 0.0
	if e.world != nil {
		day, hour = e.world.Day(), e.world.Hour()
	}
	e.mu.Unlock()
	xlog.Info("事件", "cat", cat, "text", text)
	e.publish(&EventMsg{T: "event", Cat: cat, Text: text, Day: day, Hour: hour})
}
