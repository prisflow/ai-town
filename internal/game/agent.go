package game

import (
	"context"
	// 提供对单个变量的原子读写、加减、交换、CAS 操作，用于无锁并发。适合计数器、标志位、无锁结构
	// 原子操作（不可被中断，不可被分割，要么全部完成，要么不发生）
	// CAS：原子操作，compare and swap
	"sync/atomic"
	"time"

	"aitown/internal/workflow"
)

// agent 一个村民的自主执行体：每个村民一条独立 goroutine。
//
// 【文件地图】（建议按此顺序阅读）
//
//	agent.go      结构体 / 构造 / 视图与记忆小工具（本文件）
//	agent_loop.go 生命循环与决策：mainLoop → think → 各分支（入口在上，故事顺序）
//	agent_run.go  一次 workflow 的执行与结算
//	agent_mail.go 信箱：收信分发 / 记账 / 打断 / 等待
//
// 【规则索引】见 doc.go（全包单一权威版本，避免两处漂移）。
//
// 【主循环速览】（细节见 mainLoop 与 mailbox 字段注释）
//
//	goroutine 只在两个位置消费信箱——
//	  ① 停泊位：mainLoop 的 select（等心跳/等信；冷却/对话/暂停期间都停在这里）
//	  ② 等待位：workflow 的 waitWorld/waitConvDone（等"走到了/干完了/聊完了"）
//	两个位置最终都走 onMail 分发，语义不分叉；LLM 调用与睡觉期间谁都不读信，
//	事件先堆在通道缓冲里，回到上述位置再补处理。
//
// think（思考）是同步阻塞调用：可能秒返回（暂停/夜间/冷却/对话中），
// 也可能一口气跑完"决策 + 整个 workflow"：
//
//	· 官员：LLM 决策（干活/吃饭/社交/发呆/提议建设）
//	· 平民：零 token 的机械决策（认领工作单 / 按职业劳作）
//
// 村民可以偷懒、可以无视领主指令——秩序不是系统强加的，是玩家管理出来的
// （放逐/罢免/任命，见 docs/GAME-DESIGN.md）。唯一的机械约束：夜里会强制回屋睡觉
// （休息是生理需求，不必问过大脑）。
//
// 【两条信息管道——理解本模块的关键】
//
//	mailbox（通道）：世界线程投来的原始事件，在"两个消费位置"被取走
//	notifications（板子）：把事件"记账"留存，思考时拼进 prompt 喂给 LLM（有界滚动）
//
// 【事件从哪来、到哪去】
//
//	世界线程 tick 结算
//	  ├─ 机械信号（走到了/干完了/聊完了）→ mailbox → 等待函数收到 → 唤醒等待中的 workflow
//	  └─ 社交/情报/号令事件（邀请/对话便签/领主指令）→ mailbox → 消费位置分发
//	                                              ├─ 邀请 → 打断工作、转入对话
//	                                              ├─ 洞察 → 直接写入自己的洞察记忆
//	                                              └─ 便签/指令 → post()：通知板 +1、记忆 +1
type agent struct {
	// e 引擎句柄：访问世界、发事件、调 LLM 的入口（村民不自己持状态，全是共享的）
	e *Engine
	// cit 大脑侧数据：姓名/职业/性格/阶层/记忆/关系/家
	// （物理侧数据——位置/移动/背包——在 sim.Actor，二者以 ID 关联）
	cit *Citizen

	// mailbox 信箱：世界线程 → 本村民 goroutine 的唯一传输通道
	// （带缓冲 channel，容量 128，在 newAgent 里 make）。
	//
	//  谁写：引擎 deliver()（世界线程持锁时调用）。
	//        投递永不阻塞——信箱满就丢弃，偶尔丢一封信无碍：影响界面的状态
	//        由 250ms 快照流兜底自愈。
	//  谁读：只有村民自己的 goroutine，仅两个读取点——
	//        ① 空闲时：mainLoop 的 select 收到 → onMail() 分发
	//        ② 干活时：workflow 的等待函数 waitWorld/waitConvDone 一边等信号一边代为消费
	//           （非匹配事件交给 onMail，与停泊位同一分发语义）
	//  装什么：两类事件（种类定义见 events.go 的 EvKind）——
	//        机械信号：EvArrived(走到了)/EvWorkDone(干完了)/EvChatDone(聊完了)
	//                  —— 用于"叫醒"等待中的 workflow（waitWorld 另有状态轮询兜底，
	//                     事件只是快速唤醒通道，丢了也不会卡死）
	//        通知类： EvInvite(被邀请聊天)/EvMemo(对话便签)/EvEdict(领主指令)
	//                  —— onMail 分发：邀请打断工作、洞察直写记忆、便签进通知板
	//
	// 【与 Engine.mail 的区别】Engine.mail 是"世界线程自己的延迟队列"（LLM 异步
	// 回调回流用）；mailbox 是"每个村民的私有收件箱"（世界线程→村民）。两者无关。
	mailbox chan Event

	// ctx/cancel 生命期开关：放逐或引擎关停时 cancel()，村民 goroutine 随之退出
	ctx    context.Context
	cancel context.CancelFunc

	// view 对外只读视图（状态/动作）：村民原子发布，世界线程拼快照时无锁读取
	view atomic.Pointer[BrainView]

	// notifications 未读通知板：mailbox 事件的"留言簿"（有界 32 条，最旧的被挤掉）。
	// 到达即写一条私有记忆（经历），之后是否理会由思考决定。
	// 通知不抢占思考——村民在下次心跳空闲时统一消化。
	//
	// 具体机制：post() 把事件记到板上（同时写一条记忆）；think→decideFor 时
	// 拼进 prompt 的【未读通知】段供 LLM 参考。
	// 没有"已读"概念：这是最近 32 条事件的滚动窗口，靠新事件挤掉旧的。
	notifications []Event

	// ---------- 行为状态（村民自己的 goroutine 写；世界线程只读） ----------
	sleeping      bool               // 睡觉中（nightRest 或 sleep 工作流：在家睡到天亮；期间不思考）
	run           *workflow.Run      // 正在执行的 workflow；nil = 空闲
	wfCancel      context.CancelFunc // 当前 workflow 的取消函数（打断 = 调它 + 清理物理残留）
	wfInterrupted string             // 非空 = workflow 被打断的原因（如"被拉去聊天""夜深了，收工回家"）
	paused        bool               // 测试钩子：暂停自主思考（仅测试使用）
	talkingWith   string             // 对话对端的居民 ID；"" = 没在聊天（对话期间心跳不思考）

	// ---------- 思考节流（防 LLM 请求风暴；村民私有计数） ----------
	// 思考链路追踪与失败退避（诊断：trace ID 贯穿 agent→brain→gateway 日志）
	thinkSeq   int           // 思考序号：每次 think 自增，拼 trace ID（如 a92-7）供全链路日志追踪
	failStreak int           // 连续思考失败次数（成功干成一件活才清零）——用于"仅首次失败提醒玩家"
	failDelay  time.Duration // 指数退避当前延迟：2s→4s→8s→…→30s 封顶
	nextThink  time.Time     // 到点之前不再思考（失败退避 与 "干完活歇口气" 共用此字段）
	// chatCooldownUntil 对话冷却截止：聊完一场后一段时间内不再发起社交（秒数见设置页"节奏"）
	chatCooldownUntil time.Time
}

// newAgent 创建村民执行体（只构造，不启动；调用方 go a.mainLoop() 启动执行流）。
// mailbox 容量 128：正常流量远够（每村民每天几十封），积压只会出现在
// 村民长时间阻塞的异常场景（此时快照流仍能反映状态）。
func newAgent(e *Engine, c *Citizen) *agent {
	// 根 context + 取消开关：放逐/引擎关停时 cancel()，本村民所有监听 Done() 的 select 立即醒来退出
	ctx, cancel := context.WithCancel(context.Background())
	a := &agent{
		e:       e,
		cit:     c,
		mailbox: make(chan Event, 128),
		ctx:     ctx,
		cancel:  cancel,
	}
	// 原子指针（线程安全）：初始化占位保证 Load() 永不为 nil；
	// 更新永远"拷贝→改副本→换指针"（见 setView），读写不会撞见改了一半的状态
	a.view.Store(&BrainView{})
	return a
}

// setView 原子更新对外视图。
// CoW：copy on write
// 原子操作不防止逻辑竞态，只防止物理竞态
func (a *agent) setView(mut func(v *BrainView)) {
	v := *a.view.Load()
	mut(&v)
	a.view.Store(&v)
}

// rememberAt 追加一条自己的记忆（时间来自事件盖章，纯私有追加，无锁）。
func (a *agent) rememberAt(kind, text string, importance, day int, hour float64) {
	a.cit.AddMemoryAt(day, hour, kind, text, importance)
}
