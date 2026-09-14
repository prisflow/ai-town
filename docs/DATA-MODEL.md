# 数据模型参考

> **本文件由 `cmd/docgen` 自动生成**——修改领域结构体后运行 `go run ./cmd/docgen` 重新生成，勿手改。

领域分层速览：`sim`（确定性世界物理）→ `workflow`（行为模板）→ `game`（智能体编排）→ `llm`（BYOK 网关）；`worldgen` 只在开局造世界，`config` 管 BYOK 设置。

## 世界物理（sim）

### Resource

Resource 世界上的资源节点。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Kind | `kind` | `[ResourceKind](#resourcekind)` |  |
| X | `x` | `int` |  |
| Y | `y` | `int` |  |
| Amount | `amount` | `int` |  |
| Max | `max` | `int` |  |
| Depleted | `depleted` | `bool` |  |
| RegrowAt | `regrow_at` | `int64` | 耗尽后恢复的世界 tick；0 表示未排程 |

### Building

Building 建筑（或工地）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Kind | `kind` | `[BuildingKind](#buildingkind)` |  |
| Name | `name` | `string` |  |
| X | `x` | `int` |  |
| Y | `y` | `int` |  |
| W | `w` | `int` |  |
| H | `h` | `int` |  |
| Progress | `progress` | `int` | 0-100，<100 视为工地 |
| Complete | `complete` | `bool` |  |

### Actor

Actor 居民的物理存在：位置、朝向、移动/劳作状态、背包。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `ID` | `string` |  |
| X | `X` | `float64` | 以瓦片为单位的连续坐标（tile+0.5 为格心） |
| Y | `Y` | `float64` | 以瓦片为单位的连续坐标（tile+0.5 为格心） |
| Facing | `Facing` | `int` | 0下 1上 2左 3右 |
| Speed | `Speed` | `float64` | 格/秒 |
| Path | `Path` | `[][Pt](#pt)` |  |
| Busy | `Busy` | `[BusyKind](#busykind)` |  |
| Work | `Work` | `*[WorkState](#workstate)` |  |
| Carrying | `Carrying` | `map[string]int` |  |
| Hidden | `Hidden` | `bool` | 村民已进入建筑（前端隐藏精灵）。 |

### Pt



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| X | `x` | `int` |  |
| Y | `y` | `int` |  |

### WorkState

WorkState 进行中的劳作。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Kind | `Kind` | `string` |  |
| TargetID | `TargetID` | `string` |  |
| NextUnitTick | `NextUnitTick` | `int64` |  |
| Units | `Units` | `int` |  |
| MaxUnits | `MaxUnits` | `int` |  |

## 行为模板（workflow）

### Template

Template 行为模板：一次工作/一段日常的可复用封装。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Title | `title` | `string` |  |
| Roles | `roles` | `[]string` | 允许执行的居民角色，空 = 不限 |
| Requires | `requires` | `map[string]int` | 启动所需领地库存（如建造材料） |
| Params | `params` | `map[string]string` | 参数默认值 |
| Steps | `steps` | `[]map[string]interface {}` |  |

### CheckSpec

CheckSpec 结构化条件：key 支持 domain.wood/food/stone、carrying、hour。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Key | `key` | `string` |  |
| Op | `op` | `string` |  |
| Value | `value` | `float64` |  |

## 村民与工作单（game）

### Citizen

Citizen 村民的"大脑"侧数据：身份、记忆、关系、工作履历。 数据由该村民的 agent goroutine 独占读写（快照经 BrainView 原子发布）， 物理存在（位置/移动/背包）在 sim.Actor，二者以 ID 关联。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `ID` | `string` |  |
| Name | `Name` | `string` |  |
| Role | `Role` | `string` | woodcutter/farmer/builder/forager/villager |
| Tier | `Tier` | `int` | TierCommoner 平民 / TierOfficial 官员 |
| Personality | `Personality` | `string` |  |
| Traits | `Traits` | `[]string` | 特质标签（勤劳/懒散/健谈/胆小/爱吃…），LLM 生成 |
| HomeID | `HomeID` | `string` |  |
| Mood | `Mood` | `int` | 心情 -100~100，>0 高效 <0 低效；事件加减、缓慢回归 0 |
| Mem | `Mem` | `[][Memory](#memory)` |  |
| Rel | `Rel` | `map[string]int` | 关系值（对话对象ID→好感） |
| WorkLog | `WorkLog` | `[][WorkEntry](#workentry)` |  |

### Memory

Memory 一条记忆（事件/对话/洞察），用于 prompt 注入与界面回看。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Day | `day` | `int` |  |
| Hour | `hour` | `float64` |  |
| Text | `text` | `string` |  |
| Kind | `kind` | `string` | event | chat |
| Importance | `importance` | `int` |  |

### WorkEntry

WorkEntry 一条工作履历（planner 上下文："你最近做过什么"）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Type | `Type` | `string` | 任务类型（workflow 模板 ID）。 |
| Title | `Title` | `string` | 显示标题。 |
| Outcome | `Outcome` | `string` | 结果：done | failed。 |
| Day | `Day` | `int` | 完成日的游戏天数。 |

### Job

Job 工作单：领主指令经规划器分解后的最小可执行单元。 Type 即 workflow 模板 ID，Params 注入模板变量（如建造工地 $site）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `ID` | `string` | 工作单 ID（j1、j2…）。 |
| Type | `Type` | `string` | 任务类型（= workflow 模板 ID）。 |
| Title | `Title` | `string` | 显示标题（取自模板）。 |
| Params | `Params` | `map[string]string` | 注入模板的变量（如建造工地的 site）。 |
| Priority | `Priority` | `int` | 优先级 1-5，5 最紧急。 |
| Note | `Note` | `string` | 规划器附加的简短说明。 |
| IssuedBy | `IssuedBy` | `string` | 发布者："lord" = 领主/总管指令；居民 ID = 官员发布；"" = 领地系统事件。 |
| Status | `Status` | `string` | 生命周期：pending → claimed → done；失败/打断搁置回 pending，Fails≥3 才 failed。 |
| ClaimedBy | `ClaimedBy` | `string` | 认领居民的 ID；空 = 无人认领。 |
| Fails | `Fails` | `int` | 累计失败次数（搁置重领的循环保护）。 |

## 世界初始化（worldgen）

### Spec

Spec 世界规格（LLM 产物，可直接 JSON 序列化落盘）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Name | `name` | `string` |  |
| Lore | `lore` | `string` |  |
| Villagers | `villagers` | `[][Villager](#villager)` |  |

### Villager

Villager 村民规格。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Name | `name` | `string` |  |
| Role | `role` | `string` |  |
| Personality | `personality` | `string` |  |
| Traits | `traits` | `[]string` |  |

## 配置（config）

### Config

Config 顶层配置。Roles 把内部角色路由到某个 provider： worldgen(世界初始化) / planner(指令规划) / dialogue(对话) / choice(决策) / narrate(旁白)。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Providers | `providers` | `map[string]*[ProviderConfig](#providerconfig)` |  |
| Roles | `roles` | `map[string]string` |  |
| Limits | `limits` | `[LimitsConfig](#limitsconfig)` |  |
| Pacing | `pacing` | `[PacingConfig](#pacingconfig)` | 世界节奏与玩法参数（设置页"节奏"分组） |
| Debug | `debug` | `bool` | 开启 DEBUG 日志（完整 prompt/原始响应），设置页开关 |

### ProviderConfig

ProviderConfig 描述一个 LLM 提供商的接入方式。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Type | `type` | `string` | openai | anthropic | mock |
| BaseURL | `base_url` | `string` | openai 形如 https://api.deepseek.com/v1；anthropic 形如 https://api.anthropic.com |
| APIKey | `api_key` | `string` |  |
| Model | `model` | `string` |  |

### LimitsConfig

LimitsConfig LLM 全局限额，保护玩家的钱包。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| MaxConcurrent | `max_concurrent` | `int` | 同时在途的 LLM 请求数 |
| RPM | `rpm` | `int` | 每分钟最大请求数 |
| OfficialCap | `official_cap` | `int` | 官员人数上限（官员有完整 LLM 大脑，数量决定 token 消耗） |
| ConvCap | `conv_cap` | `int` | 同时进行的对话场数上限（每场对话持续消耗 dialogue token） |
| LLMTimeoutSec | `llm_timeout_sec` | `int` | 单次 LLM 请求超时（秒）：本地慢模型（Ollama 大模型）可调大。 |
| Temperatures | `temperatures` | `map[string]float64` | 各角色温度覆盖（角色名→值，0/缺省 = 用内置默认）。 |

### PacingConfig

PacingConfig 世界节奏与玩法参数（设置页"节奏"分组）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ThinkCooldownSec | `think_cooldown_sec` | `int` | 官员两次思考的最小间隔（秒）：省 token 的总闸，调大省钱、调小吃戏。 |
| ChatCooldownSec | `chat_cooldown_sec` | `int` | 一次对话后的社交冷却（秒）。 |
| ChatRounds | `chat_rounds` | `int` | 每场对话的轮数（1-4）。 |
| EventChance | `event_chance` | `int` | 每游戏小时触发世界事件的概率（%），0 = 关闭事件。 |
| StartupOfficials | `startup_officials` | `int` | 新世界预任命官员数（0 = 全平民；对话需要至少 2 名官员）。 |
| NightStart | `night_start` | `float64` | 入夜时刻（24 小时制，如 21.5）。 |
| NightEnd | `night_end` | `float64` | 天亮时刻（如 6）。 |
| DayBreak | `day_break` | `float64` | 日界/跳夜目标时刻（如 7）。 |

## LLM 网关（llm）

### Stats

Stats 网关累计统计（界面上的 token 消耗仪表）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Calls | `llm_calls` | `int64` |  |
| Errors | `llm_errors` | `int64` |  |
| TokensIn | `tokens_in` | `int64` |  |
| TokensOut | `tokens_out` | `int64` |  |
| LastError | `last_error` | `string` |  |
| LastLatencyMS | `last_latency_ms` | `int64` |  |

### Message

Message 一条对话消息。Role: system/user/assistant。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Role | `role` | `string` |  |
| Content | `content` | `string` |  |

### Request

Request 一次补全请求。Role 仅用于网关路由与 mock 分支，不会发给提供商。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Role | `Role` | `string` |  |
| System | `System` | `string` |  |
| Messages | `Messages` | `[][Message](#message)` |  |
| JSONMode | `JSONMode` | `bool` | 要求返回纯 JSON |
| MaxTokens | `MaxTokens` | `int` |  |
| Temperature | `Temperature` | `float64` |  |

### Response

Response 补全结果与 token 用量。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Text | `Text` | `string` |  |
| TokensIn | `TokensIn` | `int` |  |
| TokensOut | `TokensOut` | `int` |  |

## 引擎编排（game）

### EvKind



| 常量 | 值 | 说明 |
|---|---|---|
| EvArrived | 0 | workflow 信号：移动到达 |
| EvWorkDone | 1 | workflow 信号：劳作完成（Outcome: full=仓满/empty/built/tended） |
| EvChatDone | 2 | workflow 信号：参与的对话结束（From=对方，Outcome=摘要） |
| EvInvite | 3 | 通知：被邀请聊天（From=邀请者）——可打断在途工作，触发重新思考 |
| EvMemo | 4 | 通知：记忆便签（Outcome=记忆文本） |
| EvEdict | 5 | 通知：领主指令原文广播（Outcome=指令文本）——全员进通知板/记忆 |
| EvReceipt | 6 | 通知：工作单完成回执（投给发单官员本人；只投完成，不投领取） |

### Engine

Engine 游戏引擎：世界线程（唯一的物理权威）+ 一组村民 agent goroutine。 并发模型（actor 变体）： - 世界线程（loop goroutine）独占 sim.World/jobs/convo 的变更，10Hz tick： 邮箱回流 → 物理结算（移动/劳作/时间/再生）→ 事件投递到村民信箱 → 快照广播 - 每个村民一条 agent goroutine：信箱等事件 + 1s 心跳自主决策（独立思考）， 对世界做短临界区快操作（拿 e.mu），对 LLM 做阻塞调用（只占自己） - HTTP/LLM 回调不直接改世界：HTTP 走锁内公共 API；LLM 回调经 mail 回流世界线程

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| mu | `mu` | `sync.Mutex` | 世界大锁：保护本结构全部字段与 sim.World 的短临界区快操作。 使用者：世界线程（每 tick 持锁结算）、HTTP 公共 API、村民 agent 的快操作。 村民的慢等待（走到/聊完/LLM）不持锁——通过各自信箱 select 完成。 |
| world | `world` | `*[World](#world)` | 仿真世界的权威状态（地形/实体/库存/逻辑时钟）；nil 表示"还没有世界"。 只被世界线程（loop goroutine）在锁内变更；村民经 agentHost 的快操作在锁内触碰。 |
| cits | `cits` | `map[string]*[Citizen](#citizen)` | 村民的大脑侧身份数据表（姓名/职业/性格/家/记忆/关系），key = 居民 ID。 静态字段（ID/Name/Role/性格）创建后不变；动态字段（心情/记忆/关系）由各自 agent goroutine 私有读写，其他人须经 BrainView 原子视图读取。 |
| ord | `ord` | `[]string` | 居民 ID 注册顺序：快照输出、对话找邻居等按此顺序遍历（确定性）。 |
| agents | `agents` | `map[string]*[agent](#agent)` | 村民执行体表：key = 居民 ID。每个条目是一条独立 goroutine（actor）， 拥有信箱/心跳/私有决策状态。只在 setupWorld（持锁）写入。 |
| pacing | `pacing` | `atomic.Pointer[aitown/internal/config.PacingConfig]` | 节奏参数（设置页热更）：原子指针，供村民 goroutine 无锁读取冷却等参数。 |
| gw | `gw` | `*[Gateway](#gateway)` | BYOK 的 LLM 网关：所有 AI 调用（世界初始化/规划/对话/决策/反思/旁白）的唯一出口。 村民 goroutine 用它的阻塞版 Complete（只占自己）；世界线程用 CompleteAsync + mail 回流。 |
| hub | `hub` | `[Hub](#hub)` | 事件出口（server 的 SSE Hub）：world/event/chat/bubble/edict/快照全从这里广播。 |
| saver | `saver` | `[Saver](#saver)` | 存档后端（main 注入；nil = 纯内存运行，不落盘）。 |
| saveCh | `saveCh` | `chan game.saveJob` | 异步存档队列（容量 1：上一份没写完就跳过本次，日界每天才一次）。 |
| lastSaveDay | `lastSaveDay` | `int` | 上次自动存档的游戏天数：新的一天第一次推进时触发异步存档。 |
| wf | `wf` | `*[Engine](#engine)` | workflow 解释器：内置模板注册表。模板启动后只读，可被任意 agent 并发查询； Execute 由村民 goroutine 调用（阻塞式跑完一个模板实例）。 |
| jobs | `jobs` | `[]*[Job](#job)` | 工作单列表（领主指令分解的产物）：pending/claimed/done/failed 生命周期。 只被世界线程变更；村民经 tryClaim（锁内）认领、jobFinished（锁内）核销。 |
| jobSeq | `jobSeq` | `int` | 工作单 ID 单调计数器（不随修剪回退，避免 ID 复用）。 |
| convos | `convos` | `map[string]*[Conversation](#conversation)` | 当前进行中的对话（世界线程编排轮转）；nil = 空闲。 同时场数上限见 convCap（设置页 conv_cap，默认 2；LLM 风暴另有网关限流兜底）。 |
| convCap | `convCap` | `int` | 同时进行的对话场数上限（<=0 视为 2）。 |
| edicts | `edicts` | `[]string` | 最近的领主指令文本（保留 5 条）：村民闲聊时作为话题素材。 |
| mail | `mail` | `chan func()` | 世界线程的延迟工作队列：LLM 异步回调（规划结果/对话轮转）投递闭包， 每 tick 开头 drainMail 串行执行——异步结果回到世界线程的唯一通道。 村民 agent 不使用它（他们有自己的信箱，且阻塞调用天然在自身线程完成）。 |
| stop | `stop` | `chan struct {}` | 世界线程的关闭信号：close 后 loop 退出（Stop() 触发，v0 与进程同生命周期）。 |
| running | `running` | `bool` | 世界线程是否已启动（Start 幂等的依据；测试置 true 以手动逐 tick 驱动世界）。 |
| generating | `generating` | `bool` | 世界正在生成中（防重复创建；setupWorld 完成后置 false）。 |
| lastSnap | `lastSnap` | `time.Time` | 上次快照广播的真实时刻：节流到 250ms 一帧，避免 SSE 被刷爆。 |
| lastFullWarn | `lastFullWarn` | `time.Time` | 仓库满导致的交付失败提示节流（30 秒最多一条玩家事件）。 |
| lastFoodWarnDay | `lastFoodWarnDay` | `int` | 已发"粮仓见底"预警的天数：一天最多提醒一次。 |
| lastEventHour | `lastEventHour` | `int64` | 事件调度：上次掷骰的小时键（tick/150），避免一小时内重复掷骰。 |
| eventLastDay | `eventLastDay` | `map[string]int` | 各事件的最近触发天数（冷却用）。 |
| forceEventID | `forceEventID` | `string` | 测试钩子：强制下一个调度周期触发指定事件。 |
| testTimeFactor | `testTimeFactor` | `float64` | 仅供测试放大时间流速（同时作用于逻辑时钟与移动速度）。 |

### World

World 世界状态：地图、实体、领地库存、时间。仅在游戏循环单线程内读写。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Name | `Name` | `string` | 领地名称（世界初始化时由 LLM 生成，失败回退默认"迷雾河谷"）。 |
| Lore | `Lore` | `string` | 世界传说/背景故事，纯展示用途（新建世界卡片、规划器上下文）。 |
| W | `W` | `int` | W, H 地图宽高（格数）。 |
| H | `H` | `int` | W, H 地图宽高（格数）。 |
| Tiles | `Tiles` | `[][Tile](#tile)` | 瓦片地形数组，行优先：idx = y*W+x。决定通行性与渲染底图。 |
| Res | `Res` | `map[string]*[Resource](#resource)` | 资源节点表（树/浆果丛/岩石），key = 资源 ID。 |
| ResOrd | `ResOrd` | `[]string` | 资源 ID 的注册顺序；map 遍历无序，靠它保证确定性与 JSON 输出稳定。 |
| Bld | `Bld` | `map[string]*[Building](#building)` | 建筑表（领主堡/民居/粮仓/农田，含未完工工地），key = 建筑 ID。 |
| BldOrd | `BldOrd` | `[]string` | 建筑 ID 的注册顺序（作用同 ResOrd）。 |
| BGrid | `BGrid` | `[]string` | 瓦片→建筑 ID 的空间索引：每格记录压在其上的建筑 ID，空串 = 无建筑。 O(1) 回答"这格能不能走 / 属于哪个建筑"，AddBuilding 时填充。 |
| Actors | `Actors` | `map[string]*[Actor](#actor)` | 居民实体表（位置/朝向/移动路径/劳作状态/背包），key = 居民 ID。 注意：这里只是"物理存在"，村民的记忆/性格等大脑状态在 game.Citizen。 |
| ActOrd | `ActOrd` | `[]string` | 居民 ID 的注册顺序；每 tick 按此顺序推进所有人（确定性）。 |
| Inventory | `Inventory` | `map[string]int` | 领地公共库存（wood/food/stone）：采集交付入库、建造/吃饭从此扣减。 |
| Tick | `Tick` | `int64` | 世界累计 tick 数，即逻辑时钟： 10 tick = 1 真实秒(1x)，150 tick = 1 游戏小时，3600 tick = 1 游戏天。 劳作耗时、资源再生、日夜判定全部以此为唯一时间基准。 |
| Paused | `Paused` | `bool` | 暂停标志：true 时 Tick 停走、居民与劳作全部冻结（SSE 快照仍照常发送）。 |
| NightStart | `NightStart` | `float64` | NightStart, NightEnd 夜间区间（24 小时制）：NightStart 后或 NightEnd 前视为夜里。 由 game 层在创建世界时从节奏配置注入；默认 21.5 / 6。 |
| NightEnd | `NightEnd` | `float64` | NightStart, NightEnd 夜间区间（24 小时制）：NightStart 后或 NightEnd 前视为夜里。 由 game 层在创建世界时从节奏配置注入；默认 21.5 / 6。 |
| ResCaps | `ResCaps` | `map[string]int` | 各资源的仓库独立容量（件）。共享总容量会被单一资源（如小麦）占满， 挤死其他资源的入库（实测：小麦爆仓后浆果 18 分钟交不进去）——因此分资源限额。 |
| Seed | `Seed` | `int64` | 世界随机源种子：rng 不可序列化，存档时带种子、读档按种子重建随机源。 |
| rng | `rng` | `*rand.Rand` | 世界级随机源（种子化）：地形生成、散步落点、日常骰子共用。 因只在游戏循环单线程内使用，无需加锁；同 seed 可完整复现世界。 |
| nextID | `nextID` | `int` | ID 发号器：GenID 据此生成 r1/b1/a1 等自增实体 ID。 |
| claims | `claims` | `map[string]string` | 资源软认领表：resID → actorID。引导村民分散采集、缓解"全员挤一棵树" 的竞态（无强制：全被认领时允许共用）。仅世界线程/持引擎锁环境读写。 |

### agent

agent 一个村民的自主执行体：每个村民一条独立 goroutine。 【文件地图】（建议按此顺序阅读） agent.go 结构体 / 构造 / 视图与记忆小工具（本文件） agent_loop.go 生命循环与决策：mainLoop → think → 各分支（入口在上，故事顺序） agent_run.go 一次 workflow 的执行与结算 agent_mail.go 信箱：收信分发 / 记账 / 打断 / 等待 【规则索引】见 doc.go（全包单一权威版本，避免两处漂移）。 【主循环速览】（细节见 mainLoop 与 mailbox 字段注释） goroutine 只在两个位置消费信箱—— ① 停泊位：mainLoop 的 select（等心跳/等信；冷却/对话/暂停期间都停在这里） ② 等待位：workflow 的 waitWorld/waitConvDone（等"走到了/干完了/聊完了"） 两个位置最终都走 onMail 分发，语义不分叉；LLM 调用与睡觉期间谁都不读信， 事件先堆在通道缓冲里，回到上述位置再补处理。 think（思考）是同步阻塞调用：可能秒返回（暂停/夜间/冷却/对话中）， 也可能一口气跑完"决策 + 整个 workflow"： · 官员：LLM 决策（干活/吃饭/社交/发呆/提议建设） · 平民：零 token 的机械决策（认领工作单 / 按职业劳作） 村民可以偷懒、可以无视领主指令——秩序不是系统强加的，是玩家管理出来的 （放逐/罢免/任命，见 docs/GAME-DESIGN.md）。唯一的机械约束：夜里会强制回屋睡觉 （休息是生理需求，不必问过大脑）。 【两条信息管道——理解本模块的关键】 mailbox（通道）：世界线程投来的原始事件，在"两个消费位置"被取走 notifications（板子）：把事件"记账"留存，思考时拼进 prompt 喂给 LLM（有界滚动） 【事件从哪来、到哪去】 世界线程 tick 结算 ├─ 机械信号（走到了/干完了/聊完了）→ mailbox → 等待函数收到 → 唤醒等待中的 workflow └─ 社交/情报/号令事件（邀请/对话便签/领主指令）→ mailbox → 消费位置分发 ├─ 邀请 → 打断工作、转入对话 ├─ 洞察 → 直接写入自己的洞察记忆 └─ 便签/指令 → post()：通知板 +1、记忆 +1

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| e | `e` | `*[Engine](#engine)` | 引擎句柄：访问世界、发事件、调 LLM 的入口（村民不自己持状态，全是共享的） |
| cit | `cit` | `*[Citizen](#citizen)` | 大脑侧数据：姓名/职业/性格/阶层/记忆/关系/家 （物理侧数据——位置/移动/背包——在 sim.Actor，二者以 ID 关联） |
| mailbox | `mailbox` | `chan game.Event` | 信箱：世界线程 → 本村民 goroutine 的唯一传输通道 （带缓冲 channel，容量 128，在 newAgent 里 make）。 谁写：引擎 deliver()（世界线程持锁时调用）。 投递永不阻塞——信箱满就丢弃，偶尔丢一封信无碍：影响界面的状态 由 250ms 快照流兜底自愈。 谁读：只有村民自己的 goroutine，仅两个读取点—— ① 空闲时：mainLoop 的 select 收到 → onMail() 分发 ② 干活时：workflow 的等待函数 waitWorld/waitConvDone 一边等信号一边代为消费 （非匹配事件交给 onMail，与停泊位同一分发语义） 装什么：两类事件（种类定义见 events.go 的 EvKind）—— 机械信号：EvArrived(走到了)/EvWorkDone(干完了)/EvChatDone(聊完了) —— 用于"叫醒"等待中的 workflow（waitWorld 另有状态轮询兜底， 事件只是快速唤醒通道，丢了也不会卡死） 通知类： EvInvite(被邀请聊天)/EvMemo(对话便签)/EvEdict(领主指令) —— onMail 分发：邀请打断工作、洞察直写记忆、便签进通知板 【与 Engine.mail 的区别】Engine.mail 是"世界线程自己的延迟队列"（LLM 异步 回调回流用）；mailbox 是"每个村民的私有收件箱"（世界线程→村民）。两者无关。 |
| ctx | `ctx` | `context.Context` | ctx/cancel 生命期开关：放逐或引擎关停时 cancel()，村民 goroutine 随之退出 |
| cancel | `cancel` | `context.CancelFunc` |  |
| view | `view` | `atomic.Pointer[aitown/internal/game.BrainView]` | 对外只读视图（状态/动作）：村民原子发布，世界线程拼快照时无锁读取 |
| notifications | `notifications` | `[][Event](#event)` | 未读通知板：mailbox 事件的"留言簿"（有界 32 条，最旧的被挤掉）。 到达即写一条私有记忆（经历），之后是否理会由思考决定。 通知不抢占思考——村民在下次心跳空闲时统一消化。 具体机制：post() 把事件记到板上（同时写一条记忆）；think→decideFor 时 拼进 prompt 的【未读通知】段供 LLM 参考。 没有"已读"概念：这是最近 32 条事件的滚动窗口，靠新事件挤掉旧的。 |
| sleeping | `sleeping` | `bool` | ---------- 行为状态（村民自己的 goroutine 写；世界线程只读） ---------- |
| run | `run` | `*[Run](#run)` | 正在执行的 workflow；nil = 空闲 |
| wfCancel | `wfCancel` | `context.CancelFunc` | 当前 workflow 的取消函数（打断 = 调它 + 清理物理残留） |
| wfInterrupted | `wfInterrupted` | `string` | 非空 = workflow 被打断的原因（如"被拉去聊天""夜深了，收工回家"） |
| paused | `paused` | `bool` | 测试钩子：暂停自主思考（仅测试使用） |
| talkingWith | `talkingWith` | `string` | 对话对端的居民 ID；"" = 没在聊天（对话期间心跳不思考） |
| thinkSeq | `thinkSeq` | `int` | ---------- 思考节流（防 LLM 请求风暴；村民私有计数） ---------- 思考链路追踪与失败退避（诊断：trace ID 贯穿 agent→brain→gateway 日志） |
| failStreak | `failStreak` | `int` | 连续思考失败次数（成功干成一件活才清零）——用于"仅首次失败提醒玩家" |
| failDelay | `failDelay` | `time.Duration` | 指数退避当前延迟：2s→4s→8s→…→30s 封顶 |
| nextThink | `nextThink` | `time.Time` | 到点之前不再思考（失败退避 与 "干完活歇口气" 共用此字段） |
| chatCooldownUntil | `chatCooldownUntil` | `time.Time` | 对话冷却截止：聊完一场后一段时间内不再发起社交（秒数见设置页"节奏"） |

### Event

Event 投递到村民信箱的事件。 语义（Phase R 起）：信箱 = 未读通知板，村民 goroutine 只暂存不响应； 是否理会、如何行动，由村民自己的思考（brain）决定。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Kind | `Kind` | `[EvKind](#evkind)` | 事件种类。 |
| From | `From` | `string` | 对话/邀请相关事件的另一方居民 ID。 |
| Outcome | `Outcome` | `string` | 事件附加文本（结果标记/记忆文本/摘要/洞察）。空文本事件会被 post 丢弃。 |
| Day | `Day` | `int` | 事件发生的游戏天数（世界线程投递时盖章，村民写记忆免读世界时钟）。 |
| Hour | `Hour` | `float64` | 事件发生的时刻 0-24（投递时盖章）。 |

### Run



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| tpl | `tpl` | `*[Template](#template)` |  |
| ActorID | `ActorID` | `string` |  |
| Vars | `Vars` | `map[string]string` |  |
| Queue | `Queue` | `[]map[string]interface {}` |  |
| failReason | `failReason` | `string` |  |
| Finished | `Finished` | `bool` |  |
| OK | `OK` | `bool` |  |

### Gateway

Gateway LLM 网关：并发信号量 + 滑动窗口 RPM 限制 + 统计。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| mu | `mu` | `sync.RWMutex` |  |
| cfg | `cfg` | `*[Config](#config)` |  |
| sem | `sem` | `chan struct {}` |  |
| rpm | `rpm` | `[]time.Time` |  |
| rpmM | `rpmM` | `sync.Mutex` |  |
| st | `st` | `[Stats](#stats)` |  |
| stM | `stM` | `sync.Mutex` |  |
| clientsM | `clientsM` | `sync.Mutex` |  |
| clients | `clients` | `map[*[ProviderConfig](#providerconfig)][Client](#client)` | 按提供商缓存客户端（mock 客户端携带确定性状态，不能每请求重建） |

### Engine

Engine 模板注册表。模板只读，可被任意 agent goroutine 并发查询；单线程使用注册。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| templates | `templates` | `map[string]*[Template](#template)` |  |

### Conversation



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `ID` | `string` |  |
| aID | `aID` | `string` |  |
| bID | `bID` | `string` |  |
| topic | `topic` | `string` |  |
| turn | `turn` | `int` |  |
| maxTurn | `maxTurn` | `int` |  |
| history | `history` | `[]string` |  |
| aSpeaks | `aSpeaks` | `bool` |  |

