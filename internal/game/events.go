package game

import "aitown/internal/llm"

// ---------- SSE 消息协议（与前端一一对应） ----------

// WorldMsg 全量世界快照（世界创建、建筑落成等结构变化时全量推送）。
type WorldMsg struct {
	// T 消息类型标识，恒为 "world"，前端据此分派。
	T string `json:"t"`
	// World 世界全量数据（瓦片/资源/建筑/居民/库存）。
	World *WorldJSON `json:"world"`
}

// SnapshotMsg 高频状态流：世界线程每 250ms 广播一次，前端据此做插值渲染。
type SnapshotMsg struct {
	// T 消息类型标识，恒为 "snapshot"。
	T string `json:"t"`
	// Tick 世界累计逻辑 tick 数。
	Tick int64 `json:"tick"`
	// Day 当前游戏天数（从 1 起）。
	Day int `json:"day"`
	// Hour 当前时刻 0-24（浮点，如 8.5 = 08:30）。
	Hour float64 `json:"hour"`
	// Paused 是否暂停（时间与居民全部冻结）。
	Paused bool `json:"paused"`
	// Season 当前季节：春/夏/秋/冬。
	Season string `json:"season"`
	// Domain 领地库存：wood/food/stone 三键。
	Domain map[string]int `json:"domain"`
	// Agents 全体居民的运动与行为状态。
	Agents []AgentView `json:"agents"`
	// Resources 资源节点的数量/枯竭变化。
	Resources []ResState `json:"resources"`
	// Buildings 建筑的进度/完工变化。
	Buildings []BldState `json:"buildings"`
	// Jobs 进行中的工作单（done 的不再推送）。
	Jobs []JobView `json:"jobs"`
	// Stats LLM 网关累计统计（前端 token 仪表）。
	Stats llm.Stats `json:"stats"`
}

// AgentView 单个居民的运动/行为状态。
type AgentView struct {
	// ID 居民 ID（与 world.agents[].id 对应）。
	ID string `json:"id"`
	// X 居民横坐标（瓦片单位，浮点）。
	X float64 `json:"x"`
	// Y 居民纵坐标（瓦片单位，浮点）。
	Y float64 `json:"y"`
	// Facing 朝向：0 下 / 1 上 / 2 左 / 3 右。
	Facing int `json:"facing"`
	// State 行为状态：idle/moving/working/talking/sleeping。
	State string `json:"state"`
	// Action 当前动作文本（workflow 标题，如"伐木"）；空闲为空。
	Action string `json:"action"`
	// Carrying 背包内物资总件数。
	Carrying int `json:"carrying"`
	// Hidden 是否已进入建筑（睡觉时在自家屋内，前端隐藏精灵与铭牌）。
	Hidden bool `json:"hidden"`
}

// ResState 单个资源节点的数量变化。
type ResState struct {
	// ID 资源节点 ID。
	ID string `json:"id"`
	// Amount 当前剩余可采数量。
	Amount int `json:"amount"`
	// Depleted 是否已枯竭（等待再生）。
	Depleted bool `json:"depleted"`
}

// BldState 单个建筑的进度变化。
type BldState struct {
	// ID 建筑 ID。
	ID string `json:"id"`
	// Progress 施工进度 0-100。
	Progress int `json:"progress"`
	// Complete 是否已落成（false = 工地蓝图）。
	Complete bool `json:"complete"`
}

// JobView 工作单状态（快照内只含未完成项）。
type JobView struct {
	// ID 工作单 ID。
	ID string `json:"id"`
	// Type 任务类型（= workflow 模板 ID）。
	Type string `json:"type"`
	// Title 显示标题（如"伐木"）。
	Title string `json:"title"`
	// Priority 优先级 1-5，5 最紧急。
	Priority int `json:"priority"`
	// Status 状态：pending/claimed/done/failed。
	Status string `json:"status"`
	// ClaimedBy 认领居民的 ID；空 = 无人认领。
	ClaimedBy string `json:"claimed_by"`
	// Note 规划器附加的简短说明。
	Note string `json:"note"`
	// IssuedBy 发布者：居民 ID / "lord"（领主）/ ""（领地系统事件）。
	IssuedBy string `json:"issued_by"`
	// IssuedByName 发布者显示名（快照时解析好，方便界面直接展示"谁发布的工作"）。
	IssuedByName string `json:"issued_by_name"`
}

// EventMsg 追加式日志事件（前端日志流的一行）。
type EventMsg struct {
	// T 消息类型标识，恒为 "event"。
	T string `json:"t"`
	// Cat 日志类别：system/world/edict/plan/job/build/chat。
	Cat string `json:"cat"`
	// Text 日志文本。
	Text string `json:"text"`
	// Day 事件发生的游戏天数。
	Day int `json:"day"`
	// Hour 事件发生的时刻 0-24。
	Hour float64 `json:"hour"`
}

// ChatMsg 一句对话（前端同时用于气泡与日志）。
type ChatMsg struct {
	// T 消息类型标识，恒为 "chat"。
	T string `json:"t"`
	// From 说话居民 ID。
	From string `json:"from"`
	// FromName 说话居民名。
	FromName string `json:"from_name"`
	// To 倾听居民 ID。
	To string `json:"to"`
	// ToName 倾听居民名。
	ToName string `json:"to_name"`
	// Text 台词内容。
	Text string `json:"text"`
	// Day 对话发生的游戏天数。
	Day int `json:"day"`
	// Hour 对话发生的时刻。
	Hour float64 `json:"hour"`
}

// BubbleMsg 居民头顶气泡（自言自语/系统提示台词）。
type BubbleMsg struct {
	// T 消息类型标识，恒为 "bubble"。
	T string `json:"t"`
	// Actor 气泡所属居民 ID。
	Actor string `json:"actor"`
	// Text 气泡文本。
	Text string `json:"text"`
	// Secs 建议显示秒数。
	Secs float64 `json:"secs"`
}

// EdictMsg 领主指令广播。
type EdictMsg struct {
	// T 消息类型标识，恒为 "edict"。
	T string `json:"t"`
	// Text 指令原文。
	Text string `json:"text"`
}

// BrainView 村民大脑的对外只读视图：由 agent goroutine 原子发布，
// 供世界线程拼装 250ms 快照时无锁读取。
type BrainView struct {
	// State 非物理状态："" / "talking" / "sleeping"（moving/working 由 sim.Actor 推导）。
	State string
	// Action 当前动作文本（workflow 标题）。
	Action string
}

// ---------- agent 信箱事件（世界线程 → 村民 goroutine） ----------

// EvKind 信箱事件种类。
type EvKind int

const (
	EvArrived  EvKind = iota // workflow 信号：移动到达
	EvWorkDone               // workflow 信号：劳作完成（Outcome: full=仓满/empty/built/tended）
	EvChatDone               // workflow 信号：参与的对话结束（From=对方，Outcome=摘要）
	EvInvite                 // 通知：被邀请聊天（From=邀请者）——可打断在途工作，触发重新思考
	EvMemo                   // 通知：记忆便签（Outcome=记忆文本）
	EvEdict                  // 通知：领主指令原文广播（Outcome=指令文本）——全员进通知板/记忆
	EvReceipt                // 通知：工作单完成回执（投给发单官员本人；只投完成，不投领取）
)

// Event 投递到村民信箱的事件。
// 语义（Phase R 起）：信箱 = 未读通知板，村民 goroutine 只暂存不响应；
// 是否理会、如何行动，由村民自己的思考（brain）决定。
type Event struct {
	// Kind 事件种类。
	Kind EvKind
	// From 对话/邀请相关事件的另一方居民 ID。
	From string
	// Outcome 事件附加文本（结果标记/记忆文本/摘要/洞察）。空文本事件会被 post 丢弃。
	Outcome string
	// Day 事件发生的游戏天数（世界线程投递时盖章，村民写记忆免读世界时钟）。
	Day int
	// Hour 事件发生的时刻 0-24（投递时盖章）。
	Hour float64
}
