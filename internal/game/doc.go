// Package game —— 领地游戏的编排层：一个世界线程 + 每村民一条 goroutine。
//
// 本文件是全项目的阅读入口。想快速建立地图，只需三样：
// 本文件（导览）→ agent_loop.go（村民的一生）→ host_path.go / host_work.go（手脚）。
//
// # 文件地图（按业务线分组，共 21 个文件）
//
// 导览：
//
//	doc.go          本文件：阅读路线 / 判断索引 / 状态分区
//
// 村民线 —— 从一条命到一双手：
//
//	agent.go        执行体数据与构造（结构体 / 构造 / 视图与记忆小工具）
//	agent_loop.go   mainLoop → think → 各分支：生命循环与决策（入口在上）
//	agent_run.go    一次 workflow 的执行与结算
//	agent_mail.go   信箱：收信分发 / 记账 / 打断 / 等待
//	host.go         手脚总纲：锁包装 / 世界查询 / 条件求值 / LLM 步骤
//	host_path.go    走路：目标解析、寻路起程、到达等待
//	host_work.go    干活与生存：劳作 / 存料 / 取料 / 吃饭（含建造夜间收工）
//	host_life.go    对话与睡觉
//
// 大脑线 —— 想什么、怎么落地：
//
//	brain.go        官员决策：处境快照 → 词表过滤 → LLM 选意图（含发布工作单 orders）→ 落地
//	jobs.go         工作单：发布 / 认领（两条路径共用把关）/ 结算 / 撤销
//	chat.go         官员对话：聊天资格（chatGateLocked）/ 轮转 / 收尾
//	citizen.go      村民数据：记忆 / 关系 / 工作履历 / 心情
//
// 世界线 —— 时间、秩序与事件：
//
//	engine.go       世界线程：tickOnce / 昼夜 / 跳夜 / 事件发布 / 快照广播
//	official.go     官员制度：任命 / 罢免 / 放逐
//	planner.go      领主指令 → LLM 总规划 → 工作单（失败回退规则）
//	worldevents.go  世界随机事件（商队 / 暴雨 / 野狗）
//
// 接口面 —— 与前端及外部对话：
//
//	events.go       SSE 协议与事件类型（EvKind 等）
//	worldjson.go    快照序列化（UI 数据面）
//
// # 阅读路线（约 2,000 行覆盖 90% 业务）
//
//	第 1 站  agent.go        结构体 + 文件地图（本文件之上）
//	第 2 站  agent_loop.go   mainLoop → think 五道门 → 平民/官员分岔
//	第 3 站  agent_run.go    一次工作怎么跑、怎么结算
//	第 4 站  agent_mail.go   事件怎么进来、怎么等、怎么被打断
//	第 5 站  host.go         手脚层总纲
//	第 6 站  host_path.go    走路（寻路 + 到达等待）
//	第 7 站  host_work.go    干活 / 存料 / 吃饭 / 夜间收工
//	第 8 站  host_life.go    对话 / 睡觉
//	第 9 站  brain.go        官员怎么被 LLM 驱动
//	第 10 站 jobs.go         工作单的一生
//	第 11 站 chat.go         一场对话的一生
//
// # 三块状态与访问规则（改动前必看）
//
//	物理态（sim.Actor：位置/路径/Busy/Work/Hidden/库存）
//	    世界线程 tick 推进；村民经 host 快操作改写；两边都走 e.mu 大锁
//	大脑态（Citizen：记忆/关系/履历/心情）
//	    该村民 goroutine 独占；世界线程仅"对话收尾写双方记忆/关系"等受控例外
//	提示位（agent.sleeping / talkingWith / run / nextThink……）
//	    本 goroutine 写；世界线程与同僚仅读；无锁、容忍旧值（最多慢半拍）
//
// # 判断索引（同一规则只认一个权威函数）
//
//	什么时候能思考    agent_loop.go 的 think 五道门
//	夜里睡觉 / 醒     agent_loop.go nightRest + host_life.go SleepUntilMorning
//	建造到点收工      host_work.go BeginWork 的 nightStop
//	世界何时跳夜      engine.go allAsleepLocked / skipToMorning
//	谁能聊天          chat.go chatGateLocked（词表门 / 挑人门 / 意图门共用）
//	工作单能否认领    jobs.go jobClaimReason（自动挑单与 LLM 指名单共用）
//	官员怎么发工作单  brain.go 的 orders（同一次 LLM 调用产出，可空、无硬约束）
//	                 → jobs.go applyOrders；领主指令走 planner.go
//	领主指令怎么感知  engine.go Edict → EvEdict 投递（全村通知板+记忆，原文可见）；
//	                 落实为工作单走 planner.go
//	节奏参数怎么生效  config.Pacing → Engine.Pacing()/SetPacing（原子发布，任意 goroutine 可读）；
//	                 设置页"节奏"分组热更；夜间区间/开局时刻在建世界时注入 sim.World
//	产出是否满仓      engine.go outputFullLocked + intentOutputRes
//	世界暂停          world.Paused：agent_mail.go 的等待冻结计时，恢复后重新计满
//
// # 两条信息管道
//
//	mailbox（通道）：世界线程 → 村民 goroutine 的原始事件，在"停泊位/等待位"被取走
//	notifications（板子）：事件记账留存，思考时拼进 prompt（有界 32 条滚动）
//	完整数据流图见 agent.go 的 mailbox 字段注释。
//
// # 为什么全在一个包里（Go 常识）
//
//	Engine 调 agent 的方法、agent 也调 Engine 的方法（互相依赖）——
//	拆成两个包会循环导入，Go 编译器直接拒绝。所以本项目按
//	"包 = 内聚单元、文件 = 业务单元"组织；同类功能靠文件与命名聚合。
//
// # 跑测试
//
//	go test ./internal/... -count=1
//	（game 包集成测试在 engine_test.go，机制测试分散在同名 *_test.go）
package game
