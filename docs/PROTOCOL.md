# 协议参考

> **本文件由 `cmd/docgen` 自动生成**——修改协议结构体（internal/game/events.go 等）后运行 `go run ./cmd/docgen` 重新生成，勿手改。

传输层两种：
- **REST**：请求-响应（世界初始化 / 指令 / 变速 / 配置）
- **SSE**：`GET /api/events` 单向事件流，帧格式 `data: {json}\n\n`，15s 心跳，慢消费者丢帧（前端靠 250ms 快照流自愈）

## SSE 事件流分派表

前端按每帧 JSON 的 `t` 字段分派（`web/src/net.ts`）：

| t | Go 类型 | 频率 | 说明 |
|---|---|---|---|
| snapshot | SnapshotMsg | 250ms | 时间/库存/居民位置状态/资源量/建筑进度/工作单/token 统计 |
| world | WorldMsg | 世界创建、圈工地、建筑落成 | 全量世界（前端重建静态渲染层） |
| event | EventMsg | 事件驱动 | 日志行（cat: system/world/edict/plan/job/build/chat/reflect） |
| chat | ChatMsg | 对话每句 | 一句对话（前端同时用于气泡与日志） |
| bubble | BubbleMsg | workflow say 节点 | 居民头顶气泡 |
| edict | EdictMsg | 下令时 | 领主指令广播 |

## 帧字段明细

### SnapshotMsg

SnapshotMsg 高频状态流：世界线程每 250ms 广播一次，前端据此做插值渲染。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "snapshot"。 |
| Tick | `tick` | `int64` | 世界累计逻辑 tick 数。 |
| Day | `day` | `int` | 当前游戏天数（从 1 起）。 |
| Hour | `hour` | `float64` | 当前时刻 0-24（浮点，如 8.5 = 08:30）。 |
| Paused | `paused` | `bool` | 是否暂停（时间与居民全部冻结）。 |
| Season | `season` | `string` | 当前季节：春/夏/秋/冬。 |
| Domain | `domain` | `map[string]int` | 领地库存：wood/food/stone 三键。 |
| Agents | `agents` | `[][AgentView](#agentview)` | 全体居民的运动与行为状态。 |
| Resources | `resources` | `[][ResState](#resstate)` | 资源节点的数量/枯竭变化。 |
| Buildings | `buildings` | `[][BldState](#bldstate)` | 建筑的进度/完工变化。 |
| Jobs | `jobs` | `[][JobView](#jobview)` | 进行中的工作单（done 的不再推送）。 |
| Stats | `stats` | `[Stats](#stats)` | LLM 网关累计统计（前端 token 仪表）。 |

### AgentView

AgentView 单个居民的运动/行为状态。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` | 居民 ID（与 world.agents[].id 对应）。 |
| X | `x` | `float64` | 居民横坐标（瓦片单位，浮点）。 |
| Y | `y` | `float64` | 居民纵坐标（瓦片单位，浮点）。 |
| Facing | `facing` | `int` | 朝向：0 下 / 1 上 / 2 左 / 3 右。 |
| State | `state` | `string` | 行为状态：idle/moving/working/talking/sleeping。 |
| Action | `action` | `string` | 当前动作文本（workflow 标题，如"伐木"）；空闲为空。 |
| Carrying | `carrying` | `int` | 背包内物资总件数。 |
| Hidden | `hidden` | `bool` | 是否已进入建筑（睡觉时在自家屋内，前端隐藏精灵与铭牌）。 |

### ResState

ResState 单个资源节点的数量变化。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` | 资源节点 ID。 |
| Amount | `amount` | `int` | 当前剩余可采数量。 |
| Depleted | `depleted` | `bool` | 是否已枯竭（等待再生）。 |

### BldState

BldState 单个建筑的进度变化。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` | 建筑 ID。 |
| Progress | `progress` | `int` | 施工进度 0-100。 |
| Complete | `complete` | `bool` | 是否已落成（false = 工地蓝图）。 |

### JobView

JobView 工作单状态（快照内只含未完成项）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` | 工作单 ID。 |
| Type | `type` | `string` | 任务类型（= workflow 模板 ID）。 |
| Title | `title` | `string` | 显示标题（如"伐木"）。 |
| Priority | `priority` | `int` | 优先级 1-5，5 最紧急。 |
| Status | `status` | `string` | 状态：pending/claimed/done/failed。 |
| ClaimedBy | `claimed_by` | `string` | 认领居民的 ID；空 = 无人认领。 |
| Note | `note` | `string` | 规划器附加的简短说明。 |
| IssuedBy | `issued_by` | `string` | 发布者：居民 ID / "lord"（领主）/ ""（领地系统事件）。 |
| IssuedByName | `issued_by_name` | `string` | 发布者显示名（快照时解析好，方便界面直接展示"谁发布的工作"）。 |

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

### WorldMsg

WorldMsg 全量世界快照（世界创建、建筑落成等结构变化时全量推送）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "world"，前端据此分派。 |
| World | `world` | `*[WorldJSON](#worldjson)` | 世界全量数据（瓦片/资源/建筑/居民/库存）。 |

### WorldJSON



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Name | `name` | `string` |  |
| Lore | `lore` | `string` |  |
| W | `w` | `int` |  |
| H | `h` | `int` |  |
| Tiles | `tiles` | `[]int` |  |
| Resources | `resources` | `[][ResJSON](#resjson)` |  |
| Buildings | `buildings` | `[][BldJSON](#bldjson)` |  |
| Agents | `agents` | `[][AgentJSON](#agentjson)` |  |
| Inventory | `inventory` | `map[string]int` |  |
| Day | `day` | `int` |  |
| Hour | `hour` | `float64` |  |

### ResJSON



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Kind | `kind` | `string` |  |
| X | `x` | `int` |  |
| Y | `y` | `int` |  |
| Amount | `amount` | `int` |  |
| Max | `max` | `int` |  |

### BldJSON



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Kind | `kind` | `string` |  |
| Name | `name` | `string` |  |
| X | `x` | `int` |  |
| Y | `y` | `int` |  |
| W | `w` | `int` |  |
| H | `h` | `int` |  |
| Progress | `progress` | `int` |  |
| Complete | `complete` | `bool` |  |

### AgentJSON



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| ID | `id` | `string` |  |
| Name | `name` | `string` |  |
| Role | `role` | `string` |  |
| Tier | `tier` | `int` |  |
| Personality | `personality` | `string` |  |
| Traits | `traits` | `[]string` |  |
| X | `x` | `float64` |  |
| Y | `y` | `float64` |  |
| HomeID | `home_id` | `string` |  |

### EventMsg

EventMsg 追加式日志事件（前端日志流的一行）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "event"。 |
| Cat | `cat` | `string` | 日志类别：system/world/edict/plan/job/build/chat。 |
| Text | `text` | `string` | 日志文本。 |
| Day | `day` | `int` | 事件发生的游戏天数。 |
| Hour | `hour` | `float64` | 事件发生的时刻 0-24。 |

### ChatMsg

ChatMsg 一句对话（前端同时用于气泡与日志）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "chat"。 |
| From | `from` | `string` | 说话居民 ID。 |
| FromName | `from_name` | `string` | 说话居民名。 |
| To | `to` | `string` | 倾听居民 ID。 |
| ToName | `to_name` | `string` | 倾听居民名。 |
| Text | `text` | `string` | 台词内容。 |
| Day | `day` | `int` | 对话发生的游戏天数。 |
| Hour | `hour` | `float64` | 对话发生的时刻。 |

### BubbleMsg

BubbleMsg 居民头顶气泡（自言自语/系统提示台词）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "bubble"。 |
| Actor | `actor` | `string` | 气泡所属居民 ID。 |
| Text | `text` | `string` | 气泡文本。 |
| Secs | `secs` | `float64` | 建议显示秒数。 |

### EdictMsg

EdictMsg 领主指令广播。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| T | `t` | `string` | 消息类型标识，恒为 "edict"。 |
| Text | `text` | `string` | 指令原文。 |

## REST 端点

| 方法 | 路径 | 请求体 | 响应 | 说明 |
|---|---|---|---|---|
| GET | /api/health | — | {"ok":true} | 存活探针 |
| GET | /api/state | — | StateData + `config`（掩码） | 页面初始全量状态 |
| POST | /api/world | {"prompt":"...","skip_ai":bool} | 202 {"ok":true} | 创建世界（异步，进度看 SSE system 事件）；skip_ai=true 直接铺内置默认世界（零 LLM） |
| POST | /api/edict | {"text":"..."} | 202 {"ok":true} | 领主指令（异步规划） |
| POST | /api/pause | {"paused":bool} | {"ok":true} | 暂停/继续 |
| POST | /api/save | — | {"ok":true} / 400 {"error"} | 手动存档（同步落盘；游戏每日与退出另有自动存档） |
| POST | /api/continue | — | {"ok":true} / 400 {"error"} | 继续上次存档（启动选择页用；载入后世界帧经 SSE 广播） |
| POST | /api/official | {"id":"居民ID","appoint":bool} | {"ok":true} | 任命/罢免官员 |
| POST | /api/exile | {"id":"居民ID"} | {"ok":true} | 放逐村民 |
| GET | /api/config | — | Config（掩码） | 读配置 |
| POST | /api/config | Config | Config（掩码） | 保存并热生效；掩码密钥自动保留旧值 |
| POST | /api/config/test | {"provider":"名"} | {"ok","latency_ms","error"} | 探测提供商连通性 |
| GET | /api/diag | — | {"stats","debug","tail"} | 诊断快照（LLM 统计 + 日志尾） |
| GET | /api/events | — | SSE 事件流 | 见上表 |
| GET | / | — | SPA | 前端静态资源（内嵌） |

## REST 载荷明细

### StateData

StateData GET /api/state 的载荷（响应另含 config 掩码字段，由 server 层拼装）。

| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| HasWorld | `has_world` | `bool` | 是否已存在世界。 |
| World | `world` | `*[WorldJSON](#worldjson)` | 世界全量数据；未创建时为 null。 |
| Snapshot | `snapshot` | `*[SnapshotMsg](#snapshotmsg)` | 当前快照；未创建时为 null。 |
| Generating | `generating` | `bool` | 世界是否正在生成中。 |
| Save | `save` | `[SaveMeta](#savemeta)` | 存档概况（启动"继续/新世界"选择页用）。 |

### SaveMeta



| 字段 | JSON | Go 类型 | 说明 |
|---|---|---|---|
| Exists | `exists` | `bool` |  |
| WorldName | `world_name` | `string` |  |
| Day | `day` | `int` |  |
| SavedAt | `saved_at` | `string` |  |

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

## 附：枚举与约定

**瓦片**（world.tiles 数组值，行优先 idx = y*w+x）：0 草地 · 1 泥土 · 2 道路 · 3 水（不可通行） · 4 沙滩 · 5 农田

**资源 kind**：tree（→木材）· berry（→食物）· rock（→石料）

**建筑 kind**：keep（领主堡/交付点）· house（民居）· granary（粮仓）· farm（农田）

**居民状态**（snapshot.agents[].state）：idle / moving / working / talking / sleeping

**工作单状态**：pending → claimed → done | failed

**时间刻度**：10 tick = 1 真实秒（1x）；150 tick = 1 游戏小时；3600 tick = 1 游戏天 = 6 真实分钟
