package game

// brain.go 村民的决策规划器：
// 组装处境快照（段落按"稳定→易变"排序提升 prompt 缓存命中）→ LLM 选意图 → 校验 → 转成 workflow。
// 官员的同一次调用还会产出 orders（发布工作单，可为空、无硬约束）——官员发单只走这一条通道。
// LLM 不可用/非法意图：不兜底、不强制——村民这一轮就发呆，稍后重新思考（自治语义）。
//
// 【阅读顺序】decideFor（入口）→ intentToRun（意图落地）→ buildPlannerPrompt（拼提示词）
// → salvageIntent / vibe / seasonWord（兜底与文案）→ 词表与数据表（文件尾部附录）。
// 聊天资格的唯一权威在 chat.go 的 chatGateLocked；工作单认领把关在 jobs.go 的 jobClaimReason，
// 官员发单落地在 jobs.go 的 applyOrders。

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"aitown/internal/llm"
	"aitown/internal/sim"
	"aitown/internal/workflow"
	"aitown/internal/xlog"
)

// Decision brain 一次完整决策的产物：
// 自己接下来做什么（Intent/Params）+ 要不要给领地发布工作单（Orders，可为空——不是必填项）。
type Decision struct {
	Intent string
	Params map[string]string
	Reason string
	Orders []JobOrder
}

// decideFor 村民的一次完整决策（think 调用的入口）：
// 处境快照（锁内采集）→ 词表过滤（社交冷却/满仓/官员不足）→ LLM 选意图 → 解析。
func (e *Engine) decideFor(a *agent, ctx context.Context) (Decision, error) {
	e.mu.Lock()
	worldNil := e.world == nil
	var in brainInput
	if e.world != nil {
		w := e.world
		in.Day = w.Day()
		in.Hour = w.Hour()
		in.Season = w.Season().Name()
		in.Night = w.IsNight()
		in.Pop = len(e.cits)
		in.Domain = map[string]int{
			"wood": w.Inventory["wood"], "food": w.Inventory["food"], "stone": w.Inventory["stone"],
			"wheat": w.Inventory["wheat"], "flour": w.Inventory["flour"], "bread": w.Inventory["bread"],
		}
		for _, id := range w.BldOrd {
			b := w.Bld[id]
			if !b.Complete {
				continue
			}
			switch b.Kind {
			case sim.BMill:
				in.Mills++
			case sim.BFarm:
				in.Farms++
			case sim.BHouse:
				in.Houses++
			case sim.BGranary:
				in.Granaries++
			}
		}
		for _, j := range e.jobs {
			if j.Status != "pending" {
				continue
			}
			// 料没备齐的单不进视野：LLM 看不见就不会反复认领（实测 50 秒 50+ 次失败风暴）；
			// 材料门槛在认领时还会再把关，这里只做"可见性"过滤
			tpl := e.wf.Template(j.Type)
			if tpl == nil || !e.requiresMet(tpl) {
				continue
			}
			// 产出满仓的单也不进视野（免得官员们围着满仓仓库白忙）
			if e.outputFullLocked(j.Type) {
				continue
			}
			// 职业不符的单不进视野：与 jobClaimReason 的 AllowsRole 同一把尺。
			// 曾漏了这道：农夫官员看得见石料单，连着认领失败"不是你的职业能干的活"
			if !tpl.AllowsRole(a.cit.Role) {
				continue
			}
			in.Jobs = append(in.Jobs, JobView{ID: j.ID, Title: j.Title, Priority: j.Priority})
		}
		// 进行中 + 劳力概况：让官员知道"活有没有人在干、还有几双手"。
		// 发单的节制靠这些信息自省，不靠系统硬拦——官员的管理水平留给领主评判。
		for _, j := range e.jobs {
			if j.Status != "claimed" || len(in.InProgress) >= 8 {
				continue
			}
			who := "有人"
			if c := e.cits[j.ClaimedBy]; c != nil {
				who = c.Name
			}
			in.InProgress = append(in.InProgress, j.Title+" · "+who)
		}
		for id, c := range e.cits {
			if c.Tier != TierCommoner {
				continue
			}
			in.Workers++
			if act := w.Actors[id]; act != nil && act.Busy != sim.BusyNone {
				in.WorkersBusy++
			}
		}
	}
	e.mu.Unlock()
	if worldNil {
		return Decision{}, errors.New("世界不存在")
	}

	// 伙伴近况：官员之间才社交（平民只劳作）。官员不足 2 人时不列伙伴，
	// 词表同步移除社交意图——独官无友，对话无从谈起。
	// 资格规则的唯一权威在 chatGateLocked（chat.go）。
	socialAllowed := e.officialCount() >= 2
	if socialAllowed {
		for _, id := range e.ord {
			if id == a.cit.ID {
				continue
			}
			c := e.cits[id]
			ag := e.agents[id]
			if c == nil || ag == nil || c.Tier != TierOfficial {
				continue // 平民不参与对话
			}
			if ag.sleeping || ag.talkingWith != "" {
				continue
			}
			tag := "（可交谈）"
			if ag.run != nil {
				tag = "（在忙自己的事）"
			}
			if v := ag.view.Load(); v != nil {
				in.Peers = append(in.Peers, c.Name+tag)
			}
		}
	}
	// 未读通知文本
	for _, ev := range a.notifications {
		in.Notifications = append(in.Notifications, ev.Outcome)
	}
	in.WorkLog = a.cit.RecentWork(5)
	in.Memories = a.cit.RecentMemories(5)

	// 词表过滤（三合一）：社交冷却 / 官员不足 2 人 / 产出满仓的需求门控——
	// LLM 看不见就不会白跑（曾因无门控出现"麦子爆仓还持续安排种田磨面"）。
	noSocial := a.chatCooldownUntil.After(time.Now()) || !socialAllowed
	e.mu.Lock()
	blocked := make(map[string]bool, len(brainIntents))
	for _, v := range brainIntents {
		blocked[v] = e.outputFullLocked(v)
	}
	e.mu.Unlock()
	vocab := make([]string, 0, len(brainIntents))
	for _, v := range brainIntents {
		if noSocial && (v == "socialize" || v == "chat_with") {
			continue
		}
		if blocked[v] {
			continue
		}
		vocab = append(vocab, v)
	}
	in.Vocab = vocab

	resp, err := e.gw.Complete(ctx, &llm.Request{
		Role:     llm.RoleBrain,
		System:   brainSystem,
		Messages: []llm.Message{{Role: "user", Content: buildPlannerPrompt(a, in)}},
		JSONMode: true, Temperature: e.gw.Temperature(llm.RoleBrain, 0.6),
	})
	if err != nil {
		return Decision{}, err
	}
	type outT struct {
		Intent string            `json:"intent"`
		Params map[string]string `json:"params"`
		Reason string            `json:"reason"`
		Orders []JobOrder        `json:"orders"`
	}
	out, perr := llm.ParseData[outT](resp.Text)
	if perr != nil {
		// 最后兜底：响应仍残缺时正则直接抠意图（params 丢失按空处理）——宁可发呆归发呆，能干活先干活
		if intent := salvageIntent(resp.Text); intent != "" {
			xlog.Warn("大脑响应解析降级：仅提取意图", "trace", xlog.TraceFrom(ctx),
				"intent", intent, "resp", xlog.Trunc(resp.Text, 120))
			return Decision{Intent: intent}, nil
		}
		xlog.Warn("大脑响应解析失败", "trace", xlog.TraceFrom(ctx),
			"resp", xlog.Trunc(resp.Text, 200), "err", perr)
		return Decision{}, perr
	}
	intent := strings.ToLower(strings.TrimSpace(out.Intent))
	if intent == "" {
		return Decision{}, errors.New("空意图")
	}
	return Decision{Intent: intent, Params: out.Params, Reason: out.Reason, Orders: out.Orders}, nil
}

// intentToRun 把意图转成可执行的 workflow（或就地完成，如提议建设）。
// 返回 (run, jobID, err)；run == nil && err == nil 表示意图已就地完成。
func (e *Engine) intentToRun(a *agent, intent string, params map[string]string) (*workflow.Run, string, error) {
	if params == nil {
		params = map[string]string{}
	}
	if tid, ok := intentTemplates[intent]; ok {
		run, err := e.wf.Start(a.cit.ID, tid, params)
		if err != nil {
			return nil, "", err
		}
		return run, "", nil
	}
	switch intent {
	case "claim_job":
		id := params["job"]
		e.mu.Lock()
		var job *Job
		for _, j := range e.jobs {
			if j.ID == id {
				job = j
				break
			}
		}
		if job == nil || job.Status != "pending" {
			// 认领失败带现场：prompt 里的快照可能已过期，直接给出眼下可领的单，省得 LLM 再猜。
			// 只列前 5 张：完整列表可能二十几张，进事件流/日志会刷屏
			hint := ""
			pend := 0
			for _, j := range e.jobs {
				if j.Status != "pending" {
					continue
				}
				pend++
				if pend <= 5 {
					hint += fmt.Sprintf("%s %s、", j.ID, j.Title)
				}
			}
			e.mu.Unlock()
			if hint != "" {
				more := ""
				if pend > 5 {
					more = fmt.Sprintf("…等共 %d 张", pend)
				}
				return nil, "", fmt.Errorf("那张工作单已经不在了；现在可领：%s%s", strings.TrimSuffix(hint, "、"), more)
			}
			return nil, "", fmt.Errorf("那张工作单已经不在了，眼下也没有别的活")
		}
		// 与自动挑单共用同一套把关（角色/产出满仓/材料/工地失效），两条路径永远一致
		reason, dispose := e.jobClaimReason(a.cit, job)
		if dispose {
			job.Status = "done" // 已失去意义的单直接核销（如工地被建完）
		}
		if reason != "" {
			e.mu.Unlock()
			return nil, "", fmt.Errorf("没领到「%s」：%s", job.Title, reason)
		}
		e.claimJobLocked(a.cit, job)
		e.mu.Unlock()
		run, err := e.wf.Start(a.cit.ID, job.Type, job.Params)
		if err != nil {
			e.jobFinished(job.ID, "failed", err.Error())
			return nil, "", err
		}
		return run, job.ID, nil
	case "chat_with":
		targetID := e.resolveVillager(params["target"])
		// 聊天资格：与词表可见性同一套规则（唯一权威 chatGateLocked，chat.go）
		e.mu.Lock()
		reason := e.chatGateLocked(a.cit.ID, targetID)
		e.mu.Unlock()
		if reason != "" {
			return nil, "", fmt.Errorf("跟%s聊不到一块去：%s", params["target"], reason)
		}
		run, err := e.wf.Start(a.cit.ID, "chat_with", map[string]string{"target": targetID, "topic": params["topic"]})
		if err != nil {
			return nil, "", err
		}
		return run, "", nil
	}
	return nil, "", fmt.Errorf("不知道怎么%s", intent)
}

// buildPlannerPrompt 组装村民自主决策 prompt（独立函数便于单测）。
// 段落按"稳定 → 易变"排序：头部（世界静态/人设/词表）对所有村民高度一致，
// 是提供商前缀缓存的命中区（缓存价 ≈ 1/10）；易变的当下处境放尾部，
// 顺带让 LLM 最后读到最新状态（近因效应）。内容与旧版等价，只调整顺序与分组。
func buildPlannerPrompt(a *agent, in brainInput) string {
	var b strings.Builder

	// —— 头部稳定段（前缀缓存命中区）——
	fmt.Fprintf(&b, "【领地】%s季 · 农田%d 磨坊%d 粮仓%d 民居%d · 居民%d人\n\n",
		in.Season, in.Farms, in.Mills, in.Granaries, in.Houses, in.Pop)
	fmt.Fprintf(&b, "【你是】%s（%s）——%s · 心情%s\n", a.cit.Name, roleLabel(a.cit.Role), a.cit.Personality, moodLabel(a.cit.Mood))

	if len(in.Jobs) > 0 {
		// 工作单是公告栏模式：不进信箱，全靠这里让村民"看见"才能被 claim_job 认领
		fmt.Fprintf(&b, "【待领工作单】（共 %d 张）\n", len(in.Jobs))
		for _, j := range in.Jobs {
			line := "- " + j.ID + " " + j.Title
			if j.Priority >= 4 {
				line += " · 紧急"
			}
			if j.Note != "" {
				line += " · " + j.Note
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if len(in.WorkLog) > 0 {
		b.WriteString("【你最近做过】\n")
		for _, w := range in.WorkLog {
			fmt.Fprintf(&b, "- %s（%s，第%d天）\n", w.Title, w.Outcome, w.Day)
		}
		b.WriteString("\n")
	}

	b.WriteString("【可选行动】\n")
	for _, id := range in.Vocab {
		b.WriteString("- " + id + "：" + brainIntentHelp[id] + "\n")
	}

	// —— 尾部易变段（当下处境，近因呈现）——
	if in.Night {
		fmt.Fprintf(&b, "\n【状态】第%d天 %s %.0f点 · 木材%s 食物%s 石料%s 小麦%d 面粉%d 面包%d · 夜深了\n",
			in.Day, seasonWord(in.Season), in.Hour,
			vibe(in.Domain["wood"], 20, 8, 0),
			vibe(in.Domain["food"], 20, 6, 2),
			vibe(in.Domain["stone"], 10, 4, 0),
			in.Domain["wheat"], in.Domain["flour"], in.Domain["bread"])
	} else {
		fmt.Fprintf(&b, "\n【状态】第%d天 %s %.0f点 · 木材%s 食物%s 石料%s 小麦%d 面粉%d 面包%d\n",
			in.Day, seasonWord(in.Season), in.Hour,
			vibe(in.Domain["wood"], 20, 8, 0),
			vibe(in.Domain["food"], 20, 6, 2),
			vibe(in.Domain["stone"], 10, 4, 0),
			in.Domain["wheat"], in.Domain["flour"], in.Domain["bread"])
	}
	if len(in.InProgress) > 0 {
		fmt.Fprintf(&b, "\n【进行中】%d 件\n", len(in.InProgress))
		for _, s := range in.InProgress {
			b.WriteString("- " + s + "\n")
		}
	}
	if in.Workers > 0 {
		fmt.Fprintf(&b, "\n【劳力】平民%d人（干活中%d · 空闲%d）\n", in.Workers, in.WorkersBusy, in.Workers-in.WorkersBusy)
	}

	if len(in.Peers) > 0 {
		b.WriteString("\n【伙伴们的近况】（你看到的，未必是此刻）\n")
		for _, p := range in.Peers {
			b.WriteString("- " + p + "\n")
		}
	}
	if len(in.Notifications) > 0 {
		b.WriteString("\n【未读通知】\n")
		for _, n := range in.Notifications {
			b.WriteString("- " + n + "\n")
		}
	}
	if len(in.Memories) > 0 {
		b.WriteString("\n【你的记忆】\n")
		for _, m := range in.Memories {
			b.WriteString("- " + m + "\n")
		}
	}

	b.WriteString("\n结合你的性格、处境与通知，决定你接下来做什么。")
	return b.String()
}

// salvageIntent 从截断/残缺的响应中直接提取意图 ID；提取不到返回空串。
func salvageIntent(text string) string {
	m := intentRe.FindStringSubmatch(strings.ToLower(text))
	if m == nil {
		return ""
	}
	return m[1]
}

// vibe 资源档位文案（村民的"体感"，而非精确数字）。
func vibe(n, good, ok, low int) string {
	switch {
	case n >= good:
		return "充足"
	case n >= ok:
		return "尚可"
	case n > low:
		return "紧张"
	default:
		return "见底"
	}
}

// seasonWord 季节文案（预留：默认汉字符直接透传）。
func seasonWord(s string) string {
	switch s {
	case "春":
		return "春"
	case "夏":
		return "夏"
	case "秋":
		return "秋"
	case "冬":
		return "冬"
	}
	return s
}

// resolveVillager 按 ID 或名字解析村民 ID。
func (e *Engine) resolveVillager(nameOrID string) string {
	if nameOrID == "" {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.cits[nameOrID]; ok {
		return nameOrID
	}
	for _, c := range e.cits {
		if c.Name == nameOrID {
			return c.ID
		}
	}
	return ""
}

// ---------- 词表与提示词数据表（上方函数读取；改行为先看这里） ----------

// intentRe 从残缺响应里抠意图 ID 的兜底正则（salvageIntent 用）。
var intentRe = regexp.MustCompile(`"intent"\s*:\s*"([a-z_]+)"`)

// brainIntents 村民可选行动词表（顺序即 prompt 呈现顺序）。
// 刻意不按磨坊/农田/职业做存在性过滤：村民可以尝试任何行动，
// 失败会被记住，并可能推动"提议建造"之类的解决——这就是涌现。
var brainIntents = []string{
	"gather_wood", "gather_food", "gather_stone", "farm_tend",
	"grind", "bake_bread", "socialize", "claim_job", "chat_with",
	"eat", "sleep", "idle_wander",
}

// intentTemplates 需要模板支撑的意图 → 模板 ID。
var intentTemplates = map[string]string{
	"gather_wood":  "gather_wood",
	"gather_food":  "gather_food",
	"gather_stone": "gather_stone",
	"farm_tend":    "farm_tend",
	"grind":        "grind",
	"bake_bread":   "bake_bread",
	"eat":          "eat_meal",
	"sleep":        "sleep",
	"idle_wander":  "idle_wander",
	"socialize":    "socialize",
}

// brainIntentHelp 意图说明（prompt 呈现）。
var brainIntentHelp = map[string]string{
	"gather_wood":  "伐木获得木材",
	"gather_food":  "采集浆果获得食物",
	"gather_stone": "开采岩石获得石料",
	"farm_tend":    "在农田劳作获得小麦（冬季休耕）",
	"grind":        "在磨坊把小麦磨成面粉（需磨坊与小麦）",
	"bake_bread":   "烘焙面包（需磨坊、面粉与浆果）",
	"claim_job":    "认领一张领主工作单，params: {\"job\":\"工作单ID\"}",
	"chat_with":    "找某位村民聊天，params: {\"target\":\"对方名字\"}",
	"eat":          "去领主堡吃点东西",
	"sleep":        "回家睡觉",
	"idle_wander":  "在附近散步发呆",
}

const brainSystem = `你是领地村民的内心。根据你的处境、性格、记忆与村里发生的事，自主决定你接下来做什么。
你可以勤劳工作，也可以偷懒发呆；可以理会领主的指令，也可以随它去——但每个选择都有后果。
intent 必须从"可选行动"里选；params 按说明给出。

你还是领地的官员，可以顺手发布工作单（orders 数组），交给村民认领去干：
- orders 可以为空：没有要紧事就不发，不用硬凑；想发几张发几张，重复发也行——由你自己掂量；
- 每条 order 是一张单：{"type":"任务类型","priority":1-5,"note":"一句短说明"}；
- 任务类型：gather_wood / gather_food / gather_stone / farm_tend / grind / bake_bread / build_house / build_granary / build_farm / build_mill。

只输出 JSON：{"intent":"行动ID","params":{...},"reason":"第一人称，10字以内","orders":[...]}`

// brainInput 规划器 prompt 的输入快照（decideFor 采集 → buildPlannerPrompt 渲染）。
type brainInput struct {
	Day           int
	Hour          float64
	Season        string
	Night         bool
	Pop           int
	Domain        map[string]int
	Mills         int
	Farms         int
	Houses        int
	Granaries     int
	Jobs          []JobView
	InProgress    []string // 进行中的活（"伐木 · 小满"），让官员知道谁在干什么
	Workers       int      // 平民劳动力总数
	WorkersBusy   int      // 其中当下在忙（干活/赶路/对话）的人数
	Peers         []string
	Notifications []string
	WorkLog       []WorkEntry
	Memories      []string
	Vocab         []string
}
