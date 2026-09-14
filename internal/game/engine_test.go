package game

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"aitown/internal/config"
	"aitown/internal/llm"
	"aitown/internal/sim"
	"aitown/internal/workflow"
)

// captureHub 收集事件（跳过高频快照）用于断言。多 goroutine 并发发布，需加锁。
type captureHub struct {
	mu     sync.Mutex
	events []EventMsg
	edicts []string
	worlds int
	chats  int
}

func (h *captureHub) Publish(v any) {
	switch m := v.(type) {
	case *EventMsg:
		h.mu.Lock()
		h.events = append(h.events, *m)
		h.mu.Unlock()
	case *EdictMsg:
		h.mu.Lock()
		h.edicts = append(h.edicts, m.Text)
		h.mu.Unlock()
	case *WorldMsg:
		h.mu.Lock()
		h.worlds++
		h.mu.Unlock()
	case *ChatMsg:
		h.mu.Lock()
		h.chats++
		h.mu.Unlock()
	}
}

func (h *captureHub) hasEvent(cat, substr string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.events {
		if e.Cat == cat && contains(e.Text, substr) {
			return true
		}
	}
	return false
}

func (h *captureHub) chatCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.chats
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// newTestEngine 造一个手动驱动的引擎：真实世界循环被伪装成已启动，
// 测试用 tickOnce 手动推帧；村民 agent goroutine 是真实的。
func newTestEngine(t *testing.T) (*Engine, *captureHub) {
	t.Helper()
	gw := llm.NewGateway(config.Defaults())
	hub := &captureHub{}
	e := NewEngine(gw, hub)
	e.running = true      // 阻止 Start() 起真实 ticker，由测试手动推帧
	e.testTimeFactor = 20 // 加速游戏时钟
	t.Cleanup(e.Stop)
	return e, hub
}

func pump(e *Engine, n int) {
	for i := 0; i < n; i++ {
		// 模拟生产环境：tickOnce 在持有世界锁的前提下执行（可捕获误加锁导致的死锁）
		func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.tickOnce(0.1)
		}()
	}
}

func pumpUntil(e *Engine, cond func() bool, maxN int) bool {
	for i := 0; i < maxN; i++ {
		func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.tickOnce(0.1)
		}()
		if cond() {
			return true
		}
		time.Sleep(500 * time.Microsecond) // 让村民 goroutine / LLM 回调得到调度
	}
	return false
}

func TestCreateWorldAndSimulate(t *testing.T) {
	e, _ := newTestEngine(t)

	if err := e.CreateWorld("一个安静的小山村，靠山临水"); err != nil {
		t.Fatal(err)
	}
	if !pumpUntil(e, func() bool { return e.HasWorld() }, 100) {
		t.Fatal("世界应在 100 tick 内生成（mock worldgen）")
	}
	w := e.world
	if w.Name == "" || len(e.ord) < 4 {
		t.Fatalf("世界数据不完整: %s, %d 居民", w.Name, len(e.ord))
	}
	if len(e.agents) != len(e.ord) {
		t.Fatalf("每名居民应有自己的 agent goroutine: %d/%d", len(e.agents), len(e.ord))
	}
	// 快照应可构建且包含全部居民
	if snap := e.snapshotLocked(); snap == nil || len(snap.Agents) != len(e.ord) {
		t.Fatal("快照构建失败")
	}
	if wj := e.worldJSONLocked(); wj == nil || len(wj.Tiles) != w.W*w.H {
		t.Fatal("世界 JSON 构建失败")
	}
}

// TestCreateDefaultWorldSkipsLLM 跳过 AI 入口：同步建默认世界、零 LLM 调用。
func TestCreateDefaultWorldSkipsLLM(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateDefaultWorld(); err != nil {
		t.Fatal(err)
	}
	if !e.HasWorld() {
		t.Fatal("默认世界应同步就绪")
	}
	if calls := e.gw.Stats().Calls; calls != 0 {
		t.Fatalf("跳过 AI 不应产生 LLM 调用，实际 %d 次", calls)
	}
	if !hub.hasEvent("world", "迷雾河谷") {
		t.Fatal("应有默认世界诞生事件")
	}
}

func TestEdictDrivesGatherJobs(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	wood0 := e.world.Inventory["wood"]
	if err := e.Edict("多储备一些木材过冬"); err != nil {
		t.Fatal(err)
	}
	// 总管规划是异步回调（经邮箱回流）：显式等待规划事件（官员发单也会产单，不能只等多单数）
	if !pumpUntil(e, func() bool { return hub.hasEvent("plan", "总管") }, 200) {
		t.Fatal("应有总管规划事件")
	}
	if len(e.jobs) == 0 {
		t.Fatal("指令应产生工作单")
	}
	if !hub.hasEvent("edict", "木材") {
		t.Fatal("应广播领主指令")
	}
	if e.gw.Stats().Calls == 0 {
		t.Fatal("规划应调用过 LLM 网关（mock）")
	}
	// 村民 goroutine 认领 → 采集 → 交付：等待木材增长
	ok := pumpUntil(e, func() bool {
		return e.world.Inventory["wood"] > wood0
	}, 30000)
	if !ok {
		t.Fatalf("木材应增长: %d -> %d", wood0, e.world.Inventory["wood"])
	}
}

// TestEdictReachesVillagers 领主指令原文直达村民：全员收到 EvEdict → 通知板记一条。
// 这是"村民知道领主说了什么"的直接通道（工作单是落地通道，两者互补）。
func TestEdictReachesVillagers(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	freezeAll(e, true) // 冻结思考：只验证"信箱 → 通知板"这条链

	// 拨到上午：避免村民还停在夜间作息（睡眠轮询 500ms 才醒，等待真实时间即可）
	e.mu.Lock()
	e.world.Tick = 10 * sim.TicksPerHour
	e.mu.Unlock()

	if err := e.Edict("多储备一些木材过冬"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		e.mu.Lock()
		e.tickOnce(0.1)
		e.mu.Unlock()
		time.Sleep(time.Millisecond)
		for _, ag := range e.agents {
			for _, ev := range ag.notifications {
				if ev.Kind == EvEdict && strings.Contains(ev.Outcome, "多储备一些木材过冬") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("领主指令应进入村民通知板（全员感知）")
	}
}

func TestPauseResume(t *testing.T) {
	e, _ := newTestEngine(t)
	e.CreateWorld("测试")
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	e.SetPaused(true)
	t0 := e.world.Tick
	pump(e, 20)
	if e.world.Tick != t0 {
		t.Fatal("暂停时世界不应推进")
	}
	e.SetPaused(false)
	t1 := e.world.Tick
	pump(e, 10)
	if e.world.Tick-t1 <= 0 {
		t.Fatalf("恢复后世界应推进: %d -> %d", t1, e.world.Tick)
	}
}

func TestTimeFliesAndDayAdvances(t *testing.T) {
	e, _ := newTestEngine(t)
	e.CreateWorld("测试")
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	day0 := e.world.Day()
	pump(e, 2000) // factor=20 → 约 5.5 游戏天
	if e.world.Day() <= day0 {
		t.Fatalf("天数应前进: %d -> %d（paused=%v tick=%d）", day0, e.world.Day(), e.world.Paused, e.world.Tick)
	}
}

func TestInterruptSelfDrivenWorkflow(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	a := e.agents[e.ord[0]]
	// 暂停全体村民心跳并等待全部空闲：避免目标村民恰好被拉进对话（对话中不打断是产品语义）
	e.mu.Lock()
	for _, ag := range e.agents {
		ag.paused = true
	}
	e.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	e.wf.Register(&workflow.Template{ID: "t_long", Title: "长任务", Steps: []workflow.StepSpec{
		{"type": "wait", "seconds": 1000},
	}})

	// 存在竞态窗口（暂停前在途的 think 仍可能把目标选为对话参与者）→ 重试式打断
	lastErr := ""
	for attempt := 0; attempt < 30; attempt++ {
		pumpUntil(e, func() bool {
			if a.run != nil || a.talkingWith != "" || a.wfCancel != nil {
				return false
			}
			return len(e.convos) == 0
		}, 400)
		if len(e.convos) > 0 {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		run, _ := e.wf.Start(a.cit.ID, "t_long", nil)
		done := make(chan struct{})
		go func() { a.execWorkflow(run, "", "test-trace"); close(done) }()
		pumpUntil(e, func() bool { return a.wfCancel != nil }, 200)
		a.cancelWorkflow("测试打断") // 与生产路径一致（onMail/等待循环均调用它）
		select {
		case <-done:
			lastErr = ""
		case <-time.After(500 * time.Millisecond):
			lastErr = "打断后工作流未结束"
		}
		if lastErr == "" {
			break
		}
	}
	if lastErr != "" {
		t.Fatalf("%s（现场: run=%v talkingWith=%q convos=%d paused=%v）",
			lastErr, a.run != nil, a.talkingWith, len(e.convos), a.paused)
	}
	if a.wfInterrupted != "" {
		t.Fatal("打断原因应被清空")
	}
	found := false
	for _, m := range a.cit.Mem {
		if contains(m.Text, "放下") {
			found = true
		}
	}
	if !found {
		t.Fatal("记忆应记录被打断的事实")
	}
	_ = hub
}

func TestWorldEvents(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	// 断言事件本身（事件文本）而非库存差值：村民采集回库与事件结算并发，库存断言有竞态

	// 野狗偷粮
	e.mu.Lock()
	e.forceEventID = "dog_steal"
	e.mu.Unlock()
	if !pumpUntil(e, func() bool { return hub.hasEvent("world", "野狗") }, 400) {
		t.Fatal("野狗偷粮事件应触发")
	}

	// 暴雨损屋（食物受潮 + 维修工作单）
	e.mu.Lock()
	e.forceEventID = "rain_damage"
	e.mu.Unlock()
	if !pumpUntil(e, func() bool {
		return hub.hasEvent("world", "暴雨") && hub.hasEvent("job", "暴雨维修")
	}, 400) {
		t.Fatal("暴雨应造成损失并生成维修工作单")
	}

	// 旅行商人（石料换面粉）
	e.mu.Lock()
	e.world.Inventory["stone"] = 10
	e.forceEventID = "merchant"
	e.mu.Unlock()
	if !pumpUntil(e, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.world.Inventory["flour"] >= 4
	}, 400) {
		t.Fatal("商人应以石换面")
	}
}

// TestConversationsHappen 对话链路：直接发起一场官员对话，断言台词帧产生、
// 轮转正常收尾、双方回到非对话态。
// 注意不依赖"官员自己选中 socialize"的涌现时序——那受 goroutine 调度影响，
// CI 上曾在 20s 预算内偶发失败；涌现行为由 mock 的 intent 轮换覆盖，
// 本测试只锁定对话机制本身（邀请 → 逐句生成 → 关系结算 → EvChatDone 清理）。
func TestConversationsHappen(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	freezeAll(e, false) // 冻结思考：排除官员自主工作流抢位，纯粹测对话机制

	e.mu.Lock()
	var officials []string
	for _, id := range e.ord {
		if c := e.cits[id]; c != nil && c.Tier == TierOfficial {
			officials = append(officials, id)
		}
	}
	if len(officials) < 2 {
		e.mu.Unlock()
		t.Fatalf("需要至少两名官员才能对话，实际 %d", len(officials))
	}
	aID, bID := officials[0], officials[1]
	// 原地触发要求两人凑在一起（距离 ≤3）：直接摆到相邻格
	pa, pb := e.world.Actors[aID], e.world.Actors[bID]
	if pa == nil || pb == nil {
		e.mu.Unlock()
		t.Fatal("官员实体不存在")
	}
	pa.X, pa.Y = 10.5, 10.5
	pb.X, pb.Y = 11.5, 10.5
	_, err := e.startConversationBetween(aID, bID, "今年的收成", 2)
	e.mu.Unlock()
	if err != nil {
		t.Fatalf("应能发起对话: %v", err)
	}

	// 驱动世界直到：至少产生一句台词，且对话已收尾（convos 清空）
	if !pumpUntil(e, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return hub.chatCount() > 0 && len(e.convos) == 0
	}, 3000) {
		e.mu.Lock()
		n := len(e.convos)
		e.mu.Unlock()
		t.Fatalf("对话应产生台词并正常收尾（剩余在途 %d）", n)
	}

	// 收尾通知经信箱异步到达：等双方清除对话态
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		left := 0
		for _, ag := range e.agents {
			if ag.talkingWith != "" {
				left++
			}
		}
		e.mu.Unlock()
		if left == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("对话结束后双方应清除对话态")
}

// freezeAll 冻结全部村民并可令其入睡（测试钩子：避免真实心跳干扰时间线断言）。
func freezeAll(e *Engine, asleep bool) {
	for _, ag := range e.agents {
		ag.paused = true
		ag.sleeping = asleep
	}
}

func TestExileNoDeadlock(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	id := e.ord[0]
	done := make(chan error, 1)
	go func() { done <- e.Exile(id) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("放逐失败: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("放逐死锁：Exile 未在 3 秒内返回（锁重入回归）")
	}
	e.mu.Lock()
	_, still := e.cits[id]
	_, actorStill := e.world.Actors[id]
	e.mu.Unlock()
	if still || actorStill {
		t.Fatal("被放逐者应从领地移除")
	}
	if !hub.hasEvent("edict", "被领主放逐") {
		t.Fatal("放逐事件应发布")
	}
}

func TestExileTearsDownConversation(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	a, b := e.ord[0], e.ord[1]
	freezeAll(e, false) // 冻结思考：避免清除对话标记后又自然开启新对话（测试抖动）

	e.mu.Lock()
	cv := &Conversation{ID: a + "|" + b, aID: a, bID: b, topic: "测试话题", maxTurn: 4, aSpeaks: true}
	e.convos[cv.ID] = cv
	e.agents[a].talkingWith = b
	e.agents[b].talkingWith = a
	e.mu.Unlock()

	if err := e.Exile(a); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	n := len(e.convos)
	e.mu.Unlock()
	if n != 0 {
		t.Fatal("放逐后包含该村民的对话应被拆除")
	}
	pumpUntil(e, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.agents[b].talkingWith == ""
	}, 200)
	e.mu.Lock()
	tw := e.agents[b].talkingWith
	e.mu.Unlock()
	if tw != "" {
		t.Fatalf("在场一方应收到对话结束通知，talkingWith=%q", tw)
	}
}

func TestNightSkipTargets(t *testing.T) {
	e, hub := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	e.mu.Lock()
	freezeAll(e, true)

	// 情形 A：晚间 22:00（第 2 天）触发 → 快进到第 3 天 07:00
	e.world.Tick = sim.TicksPerDay + 22*sim.TicksPerHour
	e.mu.Unlock()
	func() { e.mu.Lock(); defer e.mu.Unlock(); e.tickOnce(0.1) }()
	e.mu.Lock()
	if e.world.Day() != 3 || e.world.Hour() != 7 {
		e.mu.Unlock()
		t.Fatalf("晚间跳夜应到第3天07:00，实际 day=%d hour=%.1f", e.world.Day(), e.world.Hour())
	}

	// 情形 B：凌晨 01:00（第 3 天）触发 → 快进到当天 07:00（不能多跳一天）
	e.world.Tick = 2*sim.TicksPerDay + 1*sim.TicksPerHour
	e.mu.Unlock()
	func() { e.mu.Lock(); defer e.mu.Unlock(); e.tickOnce(0.1) }()
	e.mu.Lock()
	day, hour := e.world.Day(), e.world.Hour()
	e.mu.Unlock()
	if day != 3 || hour != 7 {
		t.Fatalf("凌晨跳夜应到当天07:00，实际 day=%d hour=%.1f（多跳一天=公式回归）", day, hour)
	}
	_ = hub
}

func TestNightSkipDespiteSleepWorkflow(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	e.mu.Lock()
	freezeAll(e, true)
	// 模拟"LLM sleep 意图在 workflow 内入睡"：run != nil 但 sleeping=true
	run, err := e.wf.Start(e.ord[0], "sleep", nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	e.agents[e.ord[0]].run = run
	ok := e.allAsleepLocked()
	e.world.Tick = sim.TicksPerDay + 22*sim.TicksPerHour
	e.mu.Unlock()
	if !ok {
		t.Fatal("带睡眠 workflow 的睡着村民不应阻塞快进条件")
	}
	func() { e.mu.Lock(); defer e.mu.Unlock(); e.tickOnce(0.1) }()
	if d := e.world.Day(); d != 3 {
		t.Fatalf("睡眠 workflow 不应阻塞跳夜，期望第3天，实际第%d天", d)
	}
}

func TestBuildStopsAtNight(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	// 停掉全部执行流，避免心跳干扰（直接驱动 host 层）
	for _, ag := range e.agents {
		ag.paused = true
	}

	e.mu.Lock()
	id := e.ord[0]
	act := e.world.Actors[id]
	act.X, act.Y = 4.5, 5.5
	e.world.AddBuilding(&sim.Building{ID: "b_test", Kind: sim.BHouse, Name: "工地·民居", X: 5, Y: 5, W: 2, H: 2})
	e.world.Tick = sim.TicksPerDay + 22*sim.TicksPerHour // 夜里 22:00
	a := e.agents[id]
	e.mu.Unlock()

	ah := &agentHost{e: e, a: a}
	err := ah.BeginWork(context.Background(), id, sim.WorkBuild, "b_test", 0)
	if err == nil || !strings.Contains(err.Error(), "收工") {
		t.Fatalf("夜间建造应触发收工，实际 err=%v", err)
	}
	e.mu.Lock()
	busy := e.world.Actors[id].Busy
	work := e.world.Actors[id].Work
	e.mu.Unlock()
	if busy == sim.BusyWork || work != nil {
		t.Fatal("收工后劳作状态应被清理")
	}
}

// waitTestAgent 为"等待位单消费者"测试准备环境：时间拨到上午 10 点（避开夜间作息）、
// 冻结全部真实村民到空闲（消除在途 workflow / 对话对断言的干扰），
// 返回一个**未注册、未启动 mainLoop** 的独立执行体——它的信箱只有测试自己消费，
// 因此"投递事件 → 等待函数处理"完全确定。
func waitTestAgent(t *testing.T, e *Engine) *agent {
	t.Helper()
	e.mu.Lock()
	id := e.ord[0]
	e.world.Tick = 10 * sim.TicksPerHour
	e.mu.Unlock()
	freezeAll(e, true)
	if !pumpUntil(e, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		a := e.agents[id]
		return a.run == nil && a.talkingWith == ""
	}, 200) {
		t.Fatal("真实村民未能进入空闲（在途 workflow 未收尾）")
	}
	e.mu.Lock()
	cit := e.cits[id]
	e.mu.Unlock()
	ag := newAgent(e, cit)
	run, err := e.wf.Start(id, "idle_wander", nil)
	if err != nil {
		t.Fatal(err)
	}
	ag.run = run
	return ag
}

// TestInviteInterruptsWaitingPosition 等待位（waitWorld）收到邀请：正确打断当前工作、
// 标记对话态（talkingWith/视图/BusyTalk）、邀请进板记 chat 记忆——与停泊位同一语义。
// 旧行为：只记账+打断，不标对话（"边干活边聊"回归）。
func TestInviteInterruptsWaitingPosition(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	ag := waitTestAgent(t, e)
	id, inviter := ag.cit.ID, e.ord[1]

	wfCtx, wfCancel := context.WithCancel(context.Background())
	defer wfCancel()
	ag.wfCancel = wfCancel
	waitDone := make(chan error, 1)
	go func() {
		// 等一个永不满足的条件：只有事件（邀请）能把它唤醒
		_, err := ag.waitWorld(wfCtx, func(ev Event) bool { return ev.Kind == EvChatDone },
			func(w *sim.World) (bool, bool) { return false, false })
		waitDone <- err
	}()

	time.Sleep(100 * time.Millisecond) // 让等待进入 select
	ag.deliver(Event{Kind: EvInvite, From: inviter, Outcome: "拉你聊两句", Day: 1, Hour: 10})

	select {
	case err := <-waitDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("邀请应以 ctx 取消打断等待，实际 err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待位收到邀请后未唤醒")
	}

	if ag.talkingWith != inviter {
		t.Fatalf("应标记对话对端为 %s，实际 %q", inviter, ag.talkingWith)
	}
	if v := ag.view.Load(); v == nil || v.State != "talking" {
		t.Fatal("视图应显示 talking")
	}
	e.mu.Lock()
	busy := e.world.Actors[id].Busy
	e.mu.Unlock()
	if busy != sim.BusyTalk {
		t.Fatalf("物理位应为 BusyTalk，实际 %v", busy)
	}
	found := false
	for _, m := range ag.cit.Mem {
		if m.Kind == "chat" && strings.Contains(m.Text, "拉你聊两句") {
			found = true
		}
	}
	if !found {
		t.Fatal("邀请应记入村民 chat 记忆")
	}
	if len(ag.notifications) == 0 {
		t.Fatal("邀请应进通知板")
	}

	// 对话结束：清除对话态与物理位
	ag.onMail(Event{Kind: EvChatDone, From: inviter, Outcome: "聊完了"})
	if ag.talkingWith != "" {
		t.Fatal("对话结束后应清除 talkingWith")
	}
	e.mu.Lock()
	busy = e.world.Actors[id].Busy
	e.mu.Unlock()
	if busy != sim.BusyNone {
		t.Fatalf("对话结束后物理位应为空闲，实际 %v", busy)
	}
}

// TestClaimPathsShareValidation 自动挑单（claimJob）与 LLM 指名单（intentToRun
// claim_job）共用同一套把关：满仓拒绝、优先级、失效工地核销，两径行为一致。
// 手工挂一个无人世界，只驱动引擎层认领逻辑，排除真实村民执行流的干扰。
func TestClaimPathsShareValidation(t *testing.T) {
	e, _ := newTestEngine(t)

	e.mu.Lock()
	e.world = sim.NewWorld(24, 24, 7)
	cit := &Citizen{ID: "a1", Name: "测试", Role: "villager", Rel: map[string]int{}}
	e.cits = map[string]*Citizen{cit.ID: cit}
	e.ord = []string{cit.ID}
	ag := newAgent(e, cit)                              // 不入 e.agents、不起 mainLoop：纯引擎层测试
	e.world.Inventory["wood"] = e.world.ResCaps["wood"] // 木材满仓
	jWood := e.addJob("gather_wood", nil, 5, "", "")
	e.mu.Unlock()
	if jWood == nil {
		t.Fatal("伐木单应创建成功")
	}

	// ① 满仓：两条路径都拒绝
	if got := e.tryClaim(cit); got != nil {
		t.Fatalf("满仓时不应自动认领，实际领到 %s", got.ID)
	}
	if _, _, err := e.intentToRun(ag, "claim_job", map[string]string{"job": jWood.ID}); err == nil ||
		!strings.Contains(err.Error(), "产出仓已满") {
		t.Fatalf("满仓时指名单应给出满仓理由，实际 err=%v", err)
	}

	// ② 腾出空间：自动挑单按优先级选中 5 档的木单
	e.mu.Lock()
	e.world.Inventory["wood"] = 0
	jStone := e.addJob("gather_stone", nil, 2, "", "")
	e.mu.Unlock()
	got := e.tryClaim(cit)
	if got == nil || got.ID != jWood.ID {
		t.Fatalf("应按优先级认领 %s，实际 %v", jWood.ID, got)
	}
	if got.Status != "claimed" || got.ClaimedBy != cit.ID {
		t.Fatalf("认领后状态错误：status=%s claimedBy=%s", got.Status, got.ClaimedBy)
	}

	// ③ 指名单路径认领另一张单
	run, jobID, err := e.intentToRun(ag, "claim_job", map[string]string{"job": jStone.ID})
	if err != nil || run == nil || jobID != jStone.ID {
		t.Fatalf("指名单认领失败：run=%v jobID=%s err=%v", run, jobID, err)
	}
	e.mu.Lock()
	st := jStone.Status
	e.mu.Unlock()
	if st != "claimed" {
		t.Fatalf("指名单认领后状态应为 claimed，实际 %s", st)
	}
	// 已领走的单再指名单：带现场的失败
	if _, _, err := e.intentToRun(ag, "claim_job", map[string]string{"job": jStone.ID}); err == nil ||
		!strings.Contains(err.Error(), "已经不在了") {
		t.Fatalf("重复认领应给出带现场的失败，实际 err=%v", err)
	}

	// ④ 失效工地（已建完）：jobClaimReason 判核销；两条路径都把它标 done
	e.mu.Lock()
	b := &sim.Building{ID: "b_done", Kind: sim.BHouse, Name: "民居", X: 12, Y: 12, W: 2, H: 2, Complete: true}
	e.world.AddBuilding(b)
	jSite := &Job{ID: "j_site", Type: "build_house", Title: "建造民居",
		Params: map[string]string{"site": b.ID}, Status: "pending", Priority: 1}
	e.jobs = append(e.jobs, jSite)
	reason, dispose := e.jobClaimReason(cit, jSite)
	e.mu.Unlock()
	if !dispose || reason != "工地已被建完" {
		t.Fatalf("失效工地应判核销，实际 reason=%q dispose=%v", reason, dispose)
	}
	if _, _, err := e.intentToRun(ag, "claim_job", map[string]string{"job": jSite.ID}); err == nil {
		t.Fatal("失效工地不应可认领")
	}
	e.mu.Lock()
	st = jSite.Status
	e.mu.Unlock()
	if st != "done" {
		t.Fatalf("失效工地单应被核销为 done，实际 %s", st)
	}
	// 自动挑单同样核销：再放一张同款，tryClaim 应跳过并标 done（没有别的可领 → nil）
	e.mu.Lock()
	jSite2 := &Job{ID: "j_site2", Type: "build_house", Title: "建造民居",
		Params: map[string]string{"site": b.ID}, Status: "pending", Priority: 1}
	e.jobs = append(e.jobs, jSite2)
	e.mu.Unlock()
	if got := e.tryClaim(cit); got != nil {
		t.Fatalf("失效工地不应被自动认领，实际领到 %s", got.ID)
	}
	e.mu.Lock()
	st = jSite2.Status
	e.mu.Unlock()
	if st != "done" {
		t.Fatalf("自动挑单应核销失效工地单，实际 %s", st)
	}
}

// TestApplyOrdersLandsWithIssuer 官员 orders 落地：每条一张、照单全收（无硬约束，官员自由），
// 带发布者署名（前端"谁发布的工作"的数据依据）；未知类型被结构安全拦下。
func TestApplyOrdersLandsWithIssuer(t *testing.T) {
	e, _ := newTestEngine(t)

	e.mu.Lock()
	e.world = sim.NewWorld(24, 24, 7)
	cit := &Citizen{ID: "a1", Name: "测试", Role: "villager", Rel: map[string]int{}}
	e.cits = map[string]*Citizen{cit.ID: cit}
	e.jobs = nil
	e.mu.Unlock()

	n := e.applyOrders(cit, []JobOrder{
		{Type: "gather_wood", Priority: 5, Note: "备柴"},
		{Type: "gather_wood", Priority: 3, Note: "重复也照发"},
		{Type: "gather_wood", Priority: 3, Note: "发几张由官员自己掂量"},
		{Type: "不存在的类型", Priority: 3, Note: "应被拒绝"},
	})
	if n != 3 {
		t.Fatalf("无硬约束应上板 3 张（未知类型被拒），实际 %d", n)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.jobs) != 3 {
		t.Fatalf("工作单应为 3 张，实际 %d", len(e.jobs))
	}
	for _, j := range e.jobs {
		if j.IssuedBy != cit.ID {
			t.Fatalf("工作单应署名发布者 %s，实际 %q", cit.ID, j.IssuedBy)
		}
	}
	if got := e.issuerNameLocked(cit.ID); got != "测试" {
		t.Fatalf("发布者显示名应解析为居民姓名，实际 %q", got)
	}
	if got := e.issuerNameLocked("lord"); got != "领主" {
		t.Fatalf("lord 应显示为领主，实际 %q", got)
	}
}

// TestJobDoneReceiptToIssuer 发单回执：工作完成时，发单官员的信箱收到一条便签
// （只投完成、只投发布者本人——治官员"发完看不见下文"的重复发单）。
func TestJobDoneReceiptToIssuer(t *testing.T) {
	e, _ := newTestEngine(t)

	e.mu.Lock()
	e.world = sim.NewWorld(16, 16, 7)
	off := &Citizen{ID: "a1", Name: "老周", Role: "woodcutter", Tier: TierOfficial, Rel: map[string]int{}}
	e.cits = map[string]*Citizen{off.ID: off}
	ag := newAgent(e, off) // 不入 e.agents 的 mainLoop：只验证投递
	e.agents = map[string]*agent{off.ID: ag}
	j := e.addJob("gather_wood", nil, 3, "", off.ID)
	e.mu.Unlock()
	if j == nil {
		t.Fatal("工作单应创建成功")
	}

	e.mu.Lock()
	j.Status = "claimed"
	j.ClaimedBy = "" // 领单人可缺省：回执只关心发布者
	e.mu.Unlock()
	e.jobFinished(j.ID, "done", "") // 自行加锁，勿持锁调用

	select {
	case ev := <-ag.mailbox:
		if ev.Kind != EvReceipt || !strings.Contains(ev.Outcome, "你发布的「伐木」") {
			t.Fatalf("回执内容不对：kind=%v outcome=%q", ev.Kind, ev.Outcome)
		}
	default:
		t.Fatal("发单官员应收到完成回执")
	}
}
