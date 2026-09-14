# Workflow 模板规范

workflow 是"行为的封装"：一段 JSON 步骤图，由智能体（Agent）决定何时启动、注入什么参数。
模板文件放在 `internal/workflow/templates/*.json`（go:embed 内置），也可以照此格式自行编写后注册。

## 1. 模板结构

```json
{
  "id": "gather_wood",            // 模板 ID；工作单 Type 即此 ID
  "title": "伐木",                 // 界面动作文本
  "roles": ["woodcutter", ...],   // 允许执行的居民角色；省略 = 不限
  "requires": {"wood": 10},       // 启动所需领地库存（认领时校验，不满足则挂起）
  "params": {"site": ""},         // 参数默认值，可被工作单覆盖
  "steps": [ ... ]                // 步骤图
}
```

步骤是 `{"type": "...", ...参数}`；`$name` 引用运行期变量（`goto_nearest` 的 `set`、
工作单参数、`ai_choice` 分支等都会写入变量表）。

## 2. 步骤类型参考

### 移动

| type | 参数 | 说明 |
|---|---|---|
| `goto` | `target`: `keep` / `home` / 实体ID / `$var` | 走到目标旁；无路可达则步骤失败 |
| `goto_nearest` | `kind`: `tree`/`berry`/`rock`/`farm`，`set`: 变量名，`max_range`（默认 40） | 走向最近的资源/农田并记录 ID |
| `wander` | `radius`（默认 5） | 随机走向附近可通行格；失败不算错误 |

### 劳作

| type | 参数 | 说明 |
|---|---|---|
| `harvest` | `target`: 资源ID / `$var` | 采集到**资源枯竭**为止（单趟携回=资源点剩余量，无背包上限） |
| `tend` | `target`: 农田ID，`units`（默认 6） | 田间劳作，产物直接进领地库存 |
| `build` | `target`: 工地ID / `$var` | 施工至进度 100（材料需已 `withdraw`） |
| `deposit` | — | 在领主堡旁把背包全部入库；不在仓库旁则失败 |
| `withdraw` | `resource`, `amount` | 从领地库存取材料；不足则失败（带原因） |
| `eat` | — | 消耗 1 食物恢复精力；没食物会抱怨但不失败 |

### 等待/表达

| type | 参数 | 说明 |
|---|---|---|
| `wait` | `seconds` | 挂起等待 |
| `say` | `text`；或 `ai: true` + `prompt`（`text` 作为兜底台词），`secs` 显示时长 | 气泡+日志；`ai:true` 走 `narrate` 角色 LLM |
| `talk` | `topic`（可空，默认取最近领主指令），`rounds`（默认 3） | 与附近空闲居民开始一场 LLM 对话；无人则失败 |
| `sleep` | — | 回家睡觉到天亮（配合 `goto home`） |

### 控制流

| type | 参数 | 说明 |
|---|---|---|
| `condition` | `check: {key, op, value}`，`then: [...]`，`else: [...]` | 结构化条件分支 |
| `ai_choice` | `prompt`，`options: [{label, steps: [...]}]` | LLM（choice 角色）选分支；60s 超时走第一项 |
| `fail` | `reason` | 主动失败（原因会进日志与工作单状态） |

**condition 的 check**：`key ∈ domain.wood | domain.food | domain.stone | carrying | hour | energy`，
`op ∈ >= | <= | > | < | == | true`。

## 3. 内置模板

| id | 说明 | roles |
|---|---|---|
| `gather_wood` | 找树 → 采集 → 回仓 → 交付 → 台词 | 伐木/工匠/居民/采集/农夫 |
| `gather_food` | 找浆果丛 → 采集 → 回仓 → 交付 | 同上 |
| `gather_stone` | 找岩石 → 采集 → 回仓 → 交付 | 工匠/伐木/居民 |
| `farm_tend` | 去农田劳作 6 单位（产出直接入库） | 农夫/采集/居民/工匠 |
| `build_house` | 取木 10 → 去工地 → 施工 → AI 台词 | 工匠/居民/伐木/农夫/采集 |
| `build_granary` | 取木 10 + 石 6 → 施工 → AI 台词 | 工匠/居民/伐木/农夫 |
| `build_farm` | 取木 6 → 施工 → AI 台词 | 农夫/居民/工匠 |
| `eat_meal` | 去仓库 → 吃饭 → 台词 | 全员 |
| `idle_wander` | 散步 + 停留 | 全员 |
| `socialize` | `ai_choice` 选话题 → 说话 → 闲聊 3 轮 | 全员 |
| `sleep` | 回家 → 睡到天亮 → 早安 | 全员 |

## 4. 自定义示例：巡夜人

```json
{
  "id": "night_patrol",
  "title": "巡夜",
  "roles": ["villager", "builder"],
  "steps": [
    { "type": "condition", "check": { "key": "hour", "op": ">=", "value": 21 },
      "then": [
        { "type": "goto", "target": "keep" },
        { "type": "wander", "radius": 10 },
        { "type": "say", "ai": true, "prompt": "你是深夜巡夜的村民，说一句巡视时嘀咕的话", "text": "今晚也很平静。", "secs": 4 },
        { "type": "wait", "seconds": 3 },
        { "type": "condition", "check": { "key": "hour", "op": "<", "value": 6 },
          "then": [ { "type": "wander", "radius": 10 } ],
          "else": [ { "type": "say", "text": "天亮了，收工。" } ] }
      ],
      "else": [ { "type": "say", "text": "还不到巡夜的时候。" } ]
    }
  ]
}
```

> v0 模板为内置 embed；加载自定义目录模板的挂载点已预留（`Engine.Register`），roadmap 中开放。

## 5. 与规划器的约定

规划器（LLM）把领主指令分解为工作单时，`type` 只能取模板 ID；
因此**新增一个可被指令触发的模板 = 放入 templates 目录 + 在规划器 system prompt 的任务表里加一行**
（`internal/game/planner.go`）。
