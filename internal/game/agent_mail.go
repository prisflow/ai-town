package game

import (
	"context"
	"errors"
	"runtime/debug"
	"time"

	"aitown/internal/sim"
	"aitown/internal/xlog"
)

// errWaitTimeout 等待无进展/信号超时：waitWorld 无进展判死、waitConvDone 事件迟到等
// 异常场景的自愈出口（超时路径会捕获完整调用栈供事后定位）。
var errWaitTimeout = errors.New("等待信号超时")

// 等待上限：
//   - waitTimeout：waitWorld 的无进展判死窗口（有进展会重置、世界暂停会冻结）
//   - chatWaitTimeout：waitConvDone 对话等待的总超时（多轮 LLM 较慢，故放宽）
const (
	waitTimeout     = 30 * time.Second
	chatWaitTimeout = 90 * time.Second
)

// onMail 信箱事件入口（仅村民自己的 goroutine 调用）。
// 停泊位（mainLoop）与等待位（waitWorld/waitConvDone）共用本入口——两处分发语义在此统一。
//
// 分发规则——
//
//	EvInvite   → 被邀请聊天：打断手头工作（若在干活）、标记对话态、物理位设为"交谈"
//	EvChatDone → 对话结束：清对话态、物理位恢复空闲
//	             （发起者正阻塞在 waitConvDone 等这个事件，收到后解除）
//	EvEdict    → 领主指令原文：与便签同路（post：通知板 + 记忆），是否理会由思考决定
//	EvArrived / EvWorkDone → 过期的机械信号：直接丢弃（等待方已按世界事实完成，
//	             信号只剩回声，记进通知板只会污染 prompt）
//	其余       → post()：入通知板 + 写一条私有记忆
//
// 为何物理位（Actor.Busy）在这里写：对话状态与 workflow 都在村民自己的
// goroutine 内串行变更，与 BeginMove/BeginWork 无交叉竞态
// （曾因世界线程直接写 Busy 导致状态互踩、村民永久卡死，故收敛到本 goroutine）。
func (a *agent) onMail(ev Event) {
	setBusy := func(b sim.BusyKind) {
		// 世界线程要并发读
		a.e.mu.Lock()
		if act := a.e.world.Actors[a.cit.ID]; act != nil {
			act.Busy = b
		}
		a.e.mu.Unlock()
	}
	switch ev.Kind {
	case EvArrived, EvWorkDone:
		return // 过期机械信号：静默丢弃
	case EvInvite:
		// 引擎已把双方登记进对话：打断在途工作并标记进入对话态（心跳不再思考）
		if a.run != nil {
			a.cancelWorkflow("被拉去聊天")
		}
		a.talkingWith = ev.From
		a.setView(func(v *BrainView) { v.State = "talking" })
		setBusy(sim.BusyTalk)
	case EvChatDone:
		a.talkingWith = ""
		a.setView(func(v *BrainView) { v.State = "" })
		setBusy(sim.BusyNone)
	}
	a.post(ev)
}

// post 事件记账（仅村民自己的 goroutine 调用）：写入"未读通知板"并留一条私有记忆。
//
//	通知板：有界 32 条，思考时拼进 prompt 的【未读通知】段
//	记忆：  kind 按事件类型归类（对话类=chat / 其余=event），importance=2，48 条环形
//
// 空文本事件（无 Outcome 的机械/存根信号）直接丢弃：不入板、不写记忆，避免污染
// （历史上每日报时事件曾以空文本灌满通知板与记忆池）。
func (a *agent) post(ev Event) {
	if ev.Outcome == "" {
		return
	}
	a.notifications = append(a.notifications, ev)
	if len(a.notifications) > 32 {
		a.notifications = a.notifications[len(a.notifications)-32:]
	}
	kind := "event"
	switch ev.Kind {
	case EvMemo, EvInvite:
		kind = "chat"
	}
	a.cit.AddMemoryAt(ev.Day, ev.Hour, kind, ev.Outcome, 2)
}

// cancelWorkflow 打断当前 workflow 并记录原因（由信箱循环/维护调用）。
// 顺序关键：先取消上下文（等待方 waitWorld/waitConvDone 优先看到打断，不会被
// "物理已清空"误判成正常完成），再清理 sim 物理残留（移动路径/劳作状态）。
func (a *agent) cancelWorkflow(reason string) {
	a.wfInterrupted = reason
	if a.wfCancel != nil {
		a.wfCancel()
	}
	a.e.mu.Lock()
	if a.e.world != nil {
		a.e.world.CancelActor(a.cit.ID)
	}
	a.e.mu.Unlock()
}

// waitConvDone 阻塞等待匹配事件（当前唯一调用方：对话等待 EvChatDone）。
// 本函数是信箱的"第二个读取点"：干活/对话期间 mainLoop 的 select 不在跑，
// 由这里代为消费信箱——匹配事件直接返回；其余事件走 onMail（与停泊位同一语义）。
// 超时（chatWaitTimeout）视为信号丢失：WARN 带 trace + 完整调用栈 → 返回
// errWaitTimeout 自愈，杜绝"workflow 开始后永久挂死"（Action 永远钉死、村民原地不动）。
func (a *agent) waitConvDone(ctx context.Context, timeout time.Duration, match func(Event) bool) (Event, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-deadline:
			// 世界暂停（手动）时事件天然不来：冻结计时，恢复后重新计满。
			// 否则暂停超过 30s 会把所有在途劳作集体判超时（实测一轮 8 连失败）。
			a.e.mu.Lock()
			paused := a.e.world != nil && a.e.world.Paused
			a.e.mu.Unlock()
			if paused {
				deadline = time.After(timeout)
				continue
			}
			// 卡死现场捕获：完整调用栈写入日志——信号丢失类竞态由此精确定位
			xlog.Warn("等待事件超时（信号丢失自愈）", "villager", a.cit.Name,
				"trace", xlog.TraceFrom(ctx), "waited", timeout.String(),
				"stack", xlog.Trunc(string(debug.Stack()), 1600))
			return Event{}, errWaitTimeout
		case ev := <-a.mailbox:
			if match(ev) {
				return ev, nil
			}
			a.onMail(ev) // 非匹配事件：同一分发入口（邀请会打断当前工作）
		}
	}
}

// waitWorld 条件等待（双通道）：世界状态轮询保证正确性，事件投递仅作快速唤醒。
// 与 waitConvDone 一样是信箱的"第二个读取点"：干活期间代 mainLoop 消费信箱，
// 非匹配事件统一走 onMail（与停泊位同一分发语义）。
//
// 背景：早期"纯事件等待"把"活干完了"这一持久事实转译成一条瞬时消息（EvWorkDone/
// EvArrived），消息迟到（暂停/长工期）或丢失（投递竞态）都会让等待方误判——
// 已为此打过四轮补丁。waitWorld 直接反复读取事实本身：完成状态与推进进度
// 每 200ms 在世界锁内求值一次，消息链路彻底退出正确性依赖。
//
//   - done        → 立即返回 nil（事件到达也会走这条快速路径）
//   - progressing → 重置无进展计时（build 3 分钟工期每 7.2s 必有进展，工期无关）
//   - 世界暂停    → 计时冻结（手动暂停多久都不会误判）
//   - 无进展超 timeout → 真异常：栈捕获 + errWaitTimeout 自愈
//   - 信箱事件照常处理（EvInvite 打断等）；ctx 取消优先于一切
func (a *agent) waitWorld(ctx context.Context, match func(Event) bool, check func(w *sim.World) (done, progressed bool)) (Event, error) {
	deadline := time.After(waitTimeout)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		// ctx 取消优先于一切：打断时 wfCancel 先于物理清理，必须先看到取消
		if err := ctx.Err(); err != nil {
			return Event{}, err
		}
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-deadline:
			xlog.Warn("等待无进展超时（自愈）", "villager", a.cit.Name,
				"trace", xlog.TraceFrom(ctx), "no_progress_for", waitTimeout.String(),
				"stack", xlog.Trunc(string(debug.Stack()), 1600))
			return Event{}, errWaitTimeout
		case <-poll.C:
			a.e.mu.Lock()
			w := a.e.world
			if w == nil {
				a.e.mu.Unlock()
				return Event{}, errWaitTimeout
			}
			if w.Paused {
				a.e.mu.Unlock()
				deadline = time.After(waitTimeout) // 暂停冻结计时
				continue
			}
			done, prog := check(w)
			a.e.mu.Unlock()
			if done {
				return Event{Kind: EvWorkDone}, nil
			}
			if prog {
				deadline = time.After(waitTimeout) // 有进展：重置无进展计时
			}
		case ev := <-a.mailbox:
			if match(ev) {
				return ev, nil // 事件快速唤醒（信号正常到达时的零延迟路径）
			}
			a.onMail(ev) // 非匹配事件：同一分发入口（邀请会打断当前工作）
		}
	}
}

// deliver 世界线程向该村民信箱投递事件（带缓冲，永不阻塞投递方）。
// 是 mailbox 的写入侧包装：投不进（信箱满 128）就丢弃——快照流会自愈界面状态。
func (a *agent) deliver(ev Event) {
	// 缓冲区无位置，自动放弃，有位置自动发送
	select {
	case a.mailbox <- ev:
	default: // 信箱满：村民病态卡顿，丢弃（快照流自愈）
	}
}
