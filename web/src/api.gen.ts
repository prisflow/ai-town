// 本文件由 cmd/docgen 自动生成——协议变更后运行 go run ./cmd/docgen 重新生成，勿手改。
// 字段说明来自后端 Go 结构体注释；与 server 端 JSON 严格对应。
/** AgentView 单个居民的运动/行为状态。 */
export interface AgentView {
  /** 居民 ID（与 world.agents[].id 对应）。 */
  id: string;
  /** 居民横坐标（瓦片单位，浮点）。 */
  x: number;
  /** 居民纵坐标（瓦片单位，浮点）。 */
  y: number;
  /** 朝向：0 下 / 1 上 / 2 左 / 3 右。 */
  facing: number;
  /** 行为状态：idle/moving/working/talking/sleeping。 */
  state: string;
  /** 当前动作文本（workflow 标题，如"伐木"）；空闲为空。 */
  action: string;
  /** 背包内物资总件数。 */
  carrying: number;
  /** 是否已进入建筑（睡觉时在自家屋内，前端隐藏精灵与铭牌）。 */
  hidden: boolean;
}

/** ResState 单个资源节点的数量变化。 */
export interface ResState {
  /** 资源节点 ID。 */
  id: string;
  /** 当前剩余可采数量。 */
  amount: number;
  /** 是否已枯竭（等待再生）。 */
  depleted: boolean;
}

/** BldState 单个建筑的进度变化。 */
export interface BldState {
  /** 建筑 ID。 */
  id: string;
  /** 施工进度 0-100。 */
  progress: number;
  /** 是否已落成（false = 工地蓝图）。 */
  complete: boolean;
}

/** JobView 工作单状态（快照内只含未完成项）。 */
export interface JobView {
  /** 工作单 ID。 */
  id: string;
  /** 任务类型（= workflow 模板 ID）。 */
  type: string;
  /** 显示标题（如"伐木"）。 */
  title: string;
  /** 优先级 1-5，5 最紧急。 */
  priority: number;
  /** 状态：pending/claimed/done/failed。 */
  status: string;
  /** 认领居民的 ID；空 = 无人认领。 */
  claimed_by: string;
  /** 规划器附加的简短说明。 */
  note: string;
  /** 发布者：居民 ID / "lord"（领主）/ ""（领地系统事件）。 */
  issued_by: string;
  /** 发布者显示名（快照时解析好，方便界面直接展示"谁发布的工作"）。 */
  issued_by_name: string;
}

/** Stats 网关累计统计（界面上的 token 消耗仪表）。 */
export interface Stats {
  /**  */
  llm_calls: number;
  /**  */
  llm_errors: number;
  /**  */
  tokens_in: number;
  /**  */
  tokens_out: number;
  /**  */
  last_error?: string;
  /**  */
  last_latency_ms: number;
}

/** SnapshotMsg 高频状态流：世界线程每 250ms 广播一次，前端据此做插值渲染。 */
export interface Snapshot {
  /** 消息类型标识。 */
  t: 'snapshot';
  /** 世界累计逻辑 tick 数。 */
  tick: number;
  /** 当前游戏天数（从 1 起）。 */
  day: number;
  /** 当前时刻 0-24（浮点，如 8.5 = 08:30）。 */
  hour: number;
  /** 是否暂停（时间与居民全部冻结）。 */
  paused: boolean;
  /** 当前季节：春/夏/秋/冬。 */
  season: string;
  /** 领地库存：wood/food/stone 三键。 */
  domain: Record<string, number>;
  /** 全体居民的运动与行为状态。 */
  agents: AgentView[];
  /** 资源节点的数量/枯竭变化。 */
  resources: ResState[];
  /** 建筑的进度/完工变化。 */
  buildings: BldState[];
  /** 进行中的工作单（done 的不再推送）。 */
  jobs: JobView[];
  /** LLM 网关累计统计（前端 token 仪表）。 */
  stats: Stats;
}

/**  */
export interface ResJSON {
  /**  */
  id: string;
  /**  */
  kind: string;
  /**  */
  x: number;
  /**  */
  y: number;
  /**  */
  amount: number;
  /**  */
  max: number;
}

/**  */
export interface BldJSON {
  /**  */
  id: string;
  /**  */
  kind: string;
  /**  */
  name: string;
  /**  */
  x: number;
  /**  */
  y: number;
  /**  */
  w: number;
  /**  */
  h: number;
  /**  */
  progress: number;
  /**  */
  complete: boolean;
}

/**  */
export interface AgentJSON {
  /**  */
  id: string;
  /**  */
  name: string;
  /**  */
  role: string;
  /**  */
  tier: number;
  /**  */
  personality: string;
  /**  */
  traits?: string[];
  /**  */
  x: number;
  /**  */
  y: number;
  /**  */
  home_id: string;
}

/**  */
export interface WorldJSON {
  /**  */
  name: string;
  /**  */
  lore: string;
  /**  */
  w: number;
  /**  */
  h: number;
  /**  */
  tiles: number[];
  /**  */
  resources: ResJSON[];
  /**  */
  buildings: BldJSON[];
  /**  */
  agents: AgentJSON[];
  /**  */
  inventory: Record<string, number>;
  /**  */
  day: number;
  /**  */
  hour: number;
}

/** WorldMsg 全量世界快照（世界创建、建筑落成等结构变化时全量推送）。 */
export interface WorldMsg {
  /** 消息类型标识。 */
  t: 'world';
  /** 世界全量数据（瓦片/资源/建筑/居民/库存）。 */
  world: WorldJSON;
}

/** EventMsg 追加式日志事件（前端日志流的一行）。 */
export interface EventMsg {
  /** 消息类型标识。 */
  t: 'event';
  /** 日志类别：system/world/edict/plan/job/build/chat。 */
  cat: string;
  /** 日志文本。 */
  text: string;
  /** 事件发生的游戏天数。 */
  day: number;
  /** 事件发生的时刻 0-24。 */
  hour: number;
}

/** ChatMsg 一句对话（前端同时用于气泡与日志）。 */
export interface ChatMsg {
  /** 消息类型标识。 */
  t: 'chat';
  /** 说话居民 ID。 */
  from: string;
  /** 说话居民名。 */
  from_name: string;
  /** 倾听居民 ID。 */
  to: string;
  /** 倾听居民名。 */
  to_name: string;
  /** 台词内容。 */
  text: string;
  /** 对话发生的游戏天数。 */
  day: number;
  /** 对话发生的时刻。 */
  hour: number;
}

/** BubbleMsg 居民头顶气泡（自言自语/系统提示台词）。 */
export interface BubbleMsg {
  /** 消息类型标识。 */
  t: 'bubble';
  /** 气泡所属居民 ID。 */
  actor: string;
  /** 气泡文本。 */
  text: string;
  /** 建议显示秒数。 */
  secs: number;
}

/** EdictMsg 领主指令广播。 */
export interface EdictMsg {
  /** 消息类型标识。 */
  t: 'edict';
  /** 指令原文。 */
  text: string;
}

/**  */
export interface SaveMeta {
  /**  */
  exists: boolean;
  /**  */
  world_name: string;
  /**  */
  day: number;
  /**  */
  saved_at: string;
}

/** StateData GET /api/state 的载荷（响应另含 config 掩码字段，由 server 层拼装）。 */
export interface StateData {
  /** 是否已存在世界。 */
  has_world: boolean;
  /** 世界全量数据；未创建时为 null。 */
  world: WorldJSON;
  /** 当前快照；未创建时为 null。 */
  snapshot: Snapshot;
  /** 世界是否正在生成中。 */
  generating: boolean;
  /** 存档概况（启动"继续/新世界"选择页用）。 */
  save: SaveMeta;
}

/** ProviderConfig 描述一个 LLM 提供商的接入方式。 */
export interface ProviderConfig {
  /** openai | anthropic | mock */
  type: string;
  /** openai 形如 https://api.deepseek.com/v1；anthropic 形如 https://api.anthropic.com */
  base_url: string;
  /**  */
  api_key: string;
  /**  */
  model: string;
}

/** LimitsConfig LLM 全局限额，保护玩家的钱包。 */
export interface LimitsConfig {
  /** 同时在途的 LLM 请求数 */
  max_concurrent: number;
  /** 每分钟最大请求数 */
  rpm: number;
  /** 官员人数上限（官员有完整 LLM 大脑，数量决定 token 消耗） */
  official_cap: number;
  /** 同时进行的对话场数上限（每场对话持续消耗 dialogue token） */
  conv_cap: number;
  /** 单次 LLM 请求超时（秒）：本地慢模型（Ollama 大模型）可调大。 */
  llm_timeout_sec: number;
  /** 各角色温度覆盖（角色名→值，0/缺省 = 用内置默认）。 */
  temperatures?: Record<string, number>;
}

/** PacingConfig 世界节奏与玩法参数（设置页"节奏"分组）。 */
export interface PacingConfig {
  /** 官员两次思考的最小间隔（秒）：省 token 的总闸，调大省钱、调小吃戏。 */
  think_cooldown_sec: number;
  /** 一次对话后的社交冷却（秒）。 */
  chat_cooldown_sec: number;
  /** 每场对话的轮数（1-4）。 */
  chat_rounds: number;
  /** 每游戏小时触发世界事件的概率（%），0 = 关闭事件。 */
  event_chance: number;
  /** 新世界预任命官员数（0 = 全平民；对话需要至少 2 名官员）。 */
  startup_officials: number;
  /** 入夜时刻（24 小时制，如 21.5）。 */
  night_start: number;
  /** 天亮时刻（如 6）。 */
  night_end: number;
  /** 日界/跳夜目标时刻（如 7）。 */
  day_break: number;
}

/** Config 顶层配置。Roles 把内部角色路由到某个 provider： worldgen(世界初始化) / planner(指令规划) / dialogue(对话) / choice(决策) / narrate(旁白)。 */
export interface Config {
  /**  */
  providers: Record<string, ProviderConfig>;
  /**  */
  roles: Record<string, string>;
  /**  */
  limits: LimitsConfig;
  /** 世界节奏与玩法参数（设置页"节奏"分组） */
  pacing: PacingConfig;
  /** 开启 DEBUG 日志（完整 prompt/原始响应），设置页开关 */
  debug: boolean;
}

