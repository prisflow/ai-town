package game

import (
	"fmt"
	"math/rand"
	"runtime/debug" // 调试、诊断运行时，堆栈、GC、内存、构建信息
	"strings"
	"time"

	"aitown/internal/xlog"
)

// mainLoop 村民的主循环（每个村民一条 goroutine，随 ctx 取消而退出）。
// 这是本文件的入口，以下函数按调用顺序排列。
//
// 结构：进入时先想一次 → 此后循环两件事——
//
//	收到信箱事件 → onMail（机械信号唤醒等待 / 通知类入板）
//	每秒心跳     → think（内部各道门自行判断：睡眠/夜间作息/冷却/在途工作）
//
// 注意：think 与 execWorkflow 都是**阻塞**调用——干活期间该 goroutine 就停在
// workflow 里，不在这个 select 上；彼时信箱由 waitWorld/waitConvDone 代为消费
// （见 mailbox 字段注释的"两个读取点"）。panic 时整个执行体停摆并落日志，
// 但世界线程与快照不受影响（村民表现为"失踪"，可由放逐清理）。
func (a *agent) mainLoop() {
	defer func() {
		if r := recover(); r != nil {
			xlog.Error("村民执行体已停止", "villager", a.cit.Name, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	hb := time.NewTicker(time.Second) // 物理节拍：夜间作息等时间敏感检查，每秒发布一次消息
	defer hb.Stop()

	a.think() // 醒来即思考

	for {
		select {
		case <-a.ctx.Done(): // 跟随上级 context 控制终止
			return
		case ev := <-a.mailbox:
			a.onMail(ev)
		case <-hb.C:
			a.think() // 到点重新思考（内部各门自行判断：睡眠/夜间/冷却/在途工作）
		}
	}
}

// think 一次完整决策分岔：官员走 LLM 决策，平民走机械劳作（零 token）。
//
// 按顺序穿过五道门，任何一道不放行就直接返回（等下一次心跳再试）：
//
//	① 暂停（测试钩子：冻结村民自主行为，仅测试设置）
//	② 睡觉中（nightRest 或 sleep 工作流进行时）
//	③ 夜间作息：空闲且天黑 → 回屋睡到天亮（机械流程，零 token）
//	④ 冷却/退避/在途：正在跑 workflow、正在聊天、或未到 nextThink 到点时间
//	⑤ 分岔：平民 → commonerThink（认领工作单/职业劳作）；
//	        官员 → LLM 决策（自己干什么 + 顺手发工作单 orders）→ 意向转 workflow
func (a *agent) think() {
	if a.paused || a.sleeping {
		return
	}

	// 夜间作息：空闲即回屋睡到天亮（机械流程，零 token）
	if a.nightRest() {
		return
	}

	// 冷却/退避期与在途工作：到点才思考
	if a.run != nil || a.talkingWith != "" || time.Now().Before(a.nextThink) {
		return
	}

	if a.cit.Tier == TierCommoner {
		a.commonerThink()
		return
	}

	a.thinkSeq++
	trace := fmt.Sprintf("%s-%d", a.cit.ID, a.thinkSeq)
	ctx := xlog.WithTrace(a.ctx, trace)

	dec, err := a.e.decideFor(a, ctx)
	if err != nil {
		a.onThinkFail(trace, err)
		return
	}
	a.failDelay = 0
	a.nextThink = time.Time{}
	xlog.Info("决策", "villager", a.cit.Name, "trace", trace, "intent", dec.Intent, "params", dec.Params)
	if dec.Reason != "" {
		a.e.publishEvent("brain", a.cit.Name+" 决定："+dec.Intent+"（"+dec.Reason+"）")
	}
	// 官员发单：orders 可空、无硬约束——发多发少、重复与否都是官员自己的选择（公告栏的秩序靠领主管理）
	if n := a.e.applyOrders(a.cit, dec.Orders); n > 0 {
		xlog.Info("发布工作单", "villager", a.cit.Name, "trace", trace, "count", n)
	}

	run, jobID, err := a.e.intentToRun(a, dec.Intent, dec.Params)
	if err != nil {
		a.onIntentFail(dec.Intent, err)
		return
	}
	if run == nil {
		return // 意图已就地完成（例如发布工作单之外的就地动作）
	}
	a.execWorkflow(run, jobID, trace)
}

// nightRest 夜间机械作息：空闲村民直接回屋睡觉到天亮，不走 LLM。
// 在途 workflow 不打断（workflow 执行期间 goroutine 不在主循环，根本走不到这里）；
// 无家可回就原地睡；"休息是生理需求，不必问过大脑"。
// 返回 true 表示已处理夜间睡眠（本次思考跳过）。
func (a *agent) nightRest() bool {
	e := a.e
	e.mu.Lock()
	night := e.world != nil && e.world.IsNight()
	homeID := a.cit.HomeID
	e.mu.Unlock()
	if !night {
		return false
	}
	// 用 agentHost 做两个物理动作：走到家（走不到就原地）→ 睡到天亮
	ah := &agentHost{e: e, a: a}
	if homeID != "" {
		_ = ah.PathToEntity(a.ctx, a.cit.ID, homeID) // 路断了就原地睡，不露宿街头也得睡
	}
	_ = ah.SleepUntilMorning(a.ctx, a.cit.ID) // 阻塞到天亮（内部管理 sleeping 标志与视图）
	return true
}

// commonerChatter 平民台词库：按职业的固定闲话（零 token 的"活着"感）。
var commonerChatter = map[string][]string{
	"woodcutter": {"这斧头越用越顺手了。", "北边的木头好，结实用。", "歇口气，再砍两棵。"},
	"farmer":     {"地里离不开人呐。", "这场雨下得正是时候。", "庄稼就是农人的命根。"},
	"builder":    {"石头要一块块垒，急不得。", "这墙砌得端正。", "图纸都在心里装着呢。"},
	"forager":    {"坡上的浆果红了。", "林子里的路我闭眼都认得。", "篮子又满了。"},
	"villager":   {"今天日头不错。", "领地的风都是甜的。", "日子平平淡淡才是真。"},
}

// commonerLabour 职业基础劳作表：无单时的谋生本能。
// 列表里**重复出现即权重**（出现 3 次 = 3/4 概率），idle_wander 提供"摸鱼"的人味。
var commonerLabour = map[string][]string{
	"woodcutter": {"gather_wood", "gather_wood", "gather_wood", "idle_wander"},
	"farmer":     {"farm_tend", "farm_tend", "gather_food", "idle_wander"},
	"builder":    {"gather_stone", "gather_wood", "gather_stone", "idle_wander"},
	"forager":    {"gather_food", "gather_food", "gather_food", "idle_wander"},
	"villager":   {"gather_wood", "gather_food", "idle_wander", "idle_wander"},
}

// commonerThink 平民的机械决策（零 LLM）：
//  1. 有可领工作单 → 认领执行（官员与领主的安排通过公告栏流转）
//  2. 无单 → 按职业做基础劳作（产出满仓的活计被需求门控剔除）；
//     劳作不可行时散步消磨 + 偶发闲话气泡（台词库抽卡，不加 token）
func (a *agent) commonerThink() {
	a.thinkSeq++
	trace := fmt.Sprintf("%s-c%d", a.cit.ID, a.thinkSeq)
	// 有活先领活（claimJob 按角色/材料把关）
	if job := a.e.tryClaim(a.cit); job != nil {
		run, err := a.e.wf.Start(a.cit.ID, job.Type, job.Params)
		if err == nil {
			a.execWorkflow(run, job.ID, trace)
			return
		}
		// 认领成功后起活失败（模板异常等）：把单子交回，否则永久卡在 claimed
		a.e.jobFinished(job.ID, "failed", err.Error())
	}
	// 职业基础劳作表（包级 var，列表重复项即权重）：无单时的谋生本能
	pool := commonerLabour[a.cit.Role]
	if len(pool) == 0 {
		pool = commonerLabour["villager"]
	}
	// 需求门控：产出资源已满仓的活计剔除（免得对着满仓仓库死做无用功——
	// 实测农夫在小麦爆仓后仍 77 次锄田）。全被剔除就散步。
	e := a.e
	e.mu.Lock()
	filtered := make([]string, 0, len(pool))
	for _, it := range pool {
		if !e.outputFullLocked(it) {
			filtered = append(filtered, it)
		}
	}
	e.mu.Unlock()
	if len(filtered) > 0 {
		pool = filtered
	} else {
		pool = []string{"idle_wander"}
	}
	intent := pool[int(time.Now().UnixNano())%len(pool)]
	if tid, ok := intentTemplates[intent]; ok {
		if run, err := a.e.wf.Start(a.cit.ID, tid, nil); err == nil {
			a.execWorkflow(run, "", trace)
			return
		}
	}
	// 劳作不可行（资源枯竭等）：散步消磨 + 偶发闲话
	a.nextThink = time.Now().Add(5 * time.Second)
	if time.Now().After(a.chatCooldownUntil) && rand.Intn(100) < 30 {
		lines := commonerChatter[a.cit.Role]
		if len(lines) > 0 {
			a.e.publishEvent("chat", a.cit.Name+"："+lines[rand.Intn(len(lines))])
			a.chatCooldownUntil = time.Now().Add(60 * time.Second)
		}
	}
}

// onIntentFail 意图不可行（没有农田/料没备齐等）：节流冷却 + 首次玩家事件。
// 不设冷却会导致"LLM 反复选同一个干不了的活"的请求风暴（实测 50 秒 50+ 次调用）。
func (a *agent) onIntentFail(intent string, err error) {
	a.failStreak++
	a.nextThink = time.Now().Add(3 * time.Second)
	xlog.WarnEvery("intent:"+a.cit.ID, 30*time.Second, "意图不可行",
		"villager", a.cit.Name, "intent", intent, "err", err)
	if a.failStreak == 1 {
		a.e.publishEvent("brain", a.cit.Name+" 想去"+intent+"，但"+err.Error())
	}
}

// onThinkFail LLM 思考失败：节流日志 + 首次桥接玩家事件 + 指数退避（2s→30s 封顶），
// 避免全员每秒打一次 LLM 的请求风暴。
func (a *agent) onThinkFail(trace string, err error) {
	xlog.WarnEvery("think:"+a.cit.ID, 15*time.Second, "村民思考失败",
		"villager", a.cit.Name, "trace", trace, "err", err)
	a.failStreak++
	a.failDelay = a.failDelay * 2
	if a.failDelay < 2*time.Second {
		a.failDelay = 2 * time.Second
	}
	if a.failDelay > 30*time.Second {
		a.failDelay = 30 * time.Second
	}
	a.nextThink = time.Now().Add(a.failDelay)
	if a.failStreak == 1 {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
			msg += "（请在设置里检查 API Key）"
		case strings.Contains(msg, "路由到不存在的提供商"):
			msg += "（请在设置里检查角色路由）"
		}
		a.e.publishEvent("system", a.cit.Name+" 的思绪被打断了："+msg)
	}
}
