# 存档规范（SAVE-SPEC）

> 对象：单机存档的**存储结构、载荷字段、写入时机与兼容策略**。
> 实现：`internal/store/store.go`（SQLite 层）、`internal/game/save.go`（快照组装/读档）、`internal/sim/worldstate.go`（世界序列化）、`boot.go`（启动接线）。
> 本文档是字段级权威：**改动 schema 或 payload 结构必须同步本文档，并升 `schemaVersion`/`saveVersion`**。

## 1. 总览

- **文件**：`<dataDir>/saves.db`。数据目录：浏览器版默认 `./data`（`-data` 可改），桌面版默认 `%AppData%\aitown`。
- **引擎**：SQLite（`modernc.org/sqlite`，纯 Go 无 cgo，WAL 模式）。保证单文件、事务安全、崩溃不写坏。
- **模态**：**单槽覆盖**——`slot` 表恒只有 `id=1` 一行；保存即整行覆盖，没有"另存为"。
- **载荷**：gzip 压缩后的**全量 JSON**（世界 + 村民 + 工作单 + 少量运行元信息）。
  为什么不做关系化多表：当前需求是全量存/全量读，没有按字段查询的场景；冗余列（世界名/天数）单独提出只是为了不解压就能展示。等做事件日志/向量记忆（sqlite-vec）时再扩表。
- **双层版本**：
  - `meta.schema_version`：**存储结构**版本（表结构变化时升；不匹配直接拒绝打开，宁可报错不静默改档）；
  - payload 内 `version`（当前 1）：**载荷结构**版本（字段增删时升；不匹配拒绝加载，按"无档"继续并告警）。

## 2. 连接参数

```go
dsn := "file:" + filepath.ToSlash(path) +
    "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
db.SetMaxOpenConns(1)
```

- `WAL`：读写不互斥；进程崩溃后由 WAL 恢复，不会出现半写状态。
- `busy_timeout(5000)`：并发写等待 5 秒再报错（本进程单连接，主要防外部工具占用）。
- `synchronous(NORMAL)`：WAL 下兼顾安全与速度（存档频率都是"每天一次"级别，不做极致性能）。
- **单连接**：写者唯一化，避免多连接写冲突；读档只在启动装配时发生，无并发压力。

## 3. Schema v1

```sql
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS slot (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  saved_at   TEXT NOT NULL,
  world_name TEXT NOT NULL,
  day        INTEGER NOT NULL,
  payload    BLOB NOT NULL
);
```

### `meta` 键值表

| 列 | 类型 | 说明 |
|---|---|---|
| `key` | `TEXT PRIMARY KEY` | 键。当前只有一行：`schema_version` |
| `value` | `TEXT NOT NULL` | 值。`schema_version` 当前为 `"1"` |

- 开库流程：读 `schema_version` → 无则写入当前版本（首次运行）→ 有且不等于当前版本则**拒绝打开**（`Open` 返回错误，`boot()` 记录告警并退化为纯内存运行）。
- 留成 KV 是为了后续塞别的元信息（最近备份时间、迁移标记）不用动表结构。
- 注意：SQLite 的 `TEXT PRIMARY KEY` 历史原因**不隐含 NOT NULL**；本程序的写入路径恒赋值，无实害。

### `slot` 存档槽（恒一行）

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | `INTEGER PRIMARY KEY CHECK (id = 1)` | `INTEGER PRIMARY KEY` 即 rowid 别名（存取最快）；`CHECK (id=1)` 把"单槽"约束写进数据库层。将来要多槽：去掉 CHECK，`id` 当槽位号 |
| `saved_at` | `TEXT NOT NULL` | 保存时刻，RFC3339 字符串（SQLite 无原生时间类型，TEXT 可直接排序/比较） |
| `world_name` | `TEXT NOT NULL` | 世界名。冗余列：`Info()` 不解压 payload 就能展示/记日志 |
| `day` | `INTEGER NOT NULL` | 游戏天数。同上，供界面/诊断使用 |
| `payload` | `BLOB NOT NULL` | **真正的存档**：gzip 后的全量 JSON。用 BLOB 因为内容本就二进制 |

写入语句（`store.Save`）：

```sql
INSERT INTO slot(id, saved_at, world_name, day, payload) VALUES(1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  saved_at = excluded.saved_at, world_name = excluded.world_name,
  day = excluded.day, payload = excluded.payload
```

- gzip 在内存完成、再交给 SQLite；单条 upsert 自带事务原子性——**写失败不会弄坏上一份存档**。
- 读档（`store.Load`）：`SELECT payload FROM slot WHERE id=1` → `sql.ErrNoRows` 返回 `(nil, nil)`（"无档"不是错误）→ gunzip 返回 JSON。
- `store.Info()`：返回 `(world_name, day, saved_at, ok)`，驱动前端启动选择页与诊断（`GET /api/state` 的 `save` 字段）。

## 4. 载荷结构（`game.SaveData`，JSON）

| 字段 | JSON key | 类型 | 说明 |
|---|---|---|---|
| `Version` | `version` | int | 载荷版本，当前 `1`（`saveVersion`） |
| `SavedAt` | `saved_at` | string | RFC3339（与 `slot.saved_at` 冗余，便于脱离 DB 单独解析） |
| `World` | `world` | object | `sim.WorldState`（见 §5） |
| `Citizens` | `citizens` | map[id]Citizen | 村民大脑侧全量：姓名/职业/阶层/性格/特质/家/心情/记忆/关系/工作履历 |
| `Order` | `order` | []string | 居民 ID 的固定顺序（快照输出与遍历的确定性来源） |
| `Jobs` | `jobs` | []Job | 工作单：类型/参数/优先级/备注/发布者/状态；**存档副本里 claimed → pending、ClaimedBy 清空**（见 §6） |
| `JobSeq` | `job_seq` | int | 工作单 ID 发号器（不随修剪回退，防 ID 复用） |
| `Edicts` | `edicts` | []string | 最近 5 条领主指令（聊天话题素材） |
| `LastFoodWarnDay` | `last_food_warn_day` | int | "粮仓见底"预警去重 |
| `EventLastDay` | `event_last_day` | map[事件]天 | 随机事件冷却（同类麻烦 7 天内不重复） |

## 5. 世界序列化（`sim.WorldState`）

> 由 `(*sim.World).MarshalState()` 生成、`sim.UnmarshalState()` 还原。
> 包内实现，所以能访问 `World` 的未导出字段。

| 字段 | 说明 |
|---|---|
| `Name` / `Lore` | 领地名与背景故事 |
| `W` / `H` / `Tiles` | 地图尺寸与地形数组（行优先）；读档校验 `len(Tiles)==W*H`，不符直接报错 |
| `Res` / `ResOrd` | 资源节点表与注册顺序（树/浆果/岩石的存量、再生排程） |
| `Bld` / `BldOrd` / `BGrid` | 建筑表、注册顺序、瓦片→建筑空间索引；`BGrid` 缺失/不完整时按 `Bld` 重建 |
| `Actors` / `ActOrd` | 村民**物理存在**：位置/朝向/速度/**背包**；路径/劳作/忙碌/隐藏被清空（见 §6） |
| `Inventory` / `ResCaps` | 领地库存与各资源独立容量（粮仓加成后的结果，读档 `RecalcInvCap` 再兜底重算） |
| `Tick` | 逻辑时钟（唯一的游戏时间基准；天数/季节/昼夜全由它推导） |
| `NightStart` / `NightEnd` | 夜间区间（创建世界时从节奏配置注入） |
| `Seed` | 世界随机源种子。**rng 不可序列化：读档用同一种子重建随机源**（随机序列从此重新开始，不追求逐次操作的严格可复现） |
| `NextID` | ID 发号器（`GenID` 的计数）。**必须存档，否则读档后新实体 ID 会与旧实体撞车** |
| `Claims` | 资源软认领表（`resID → actorID`，未导出字段，在这里显式搬运） |

**不序列化（读档重建）**：`Paused`（暂停是会话态，读档一律恢复为运行）。

## 6. 归一化规则（写入前在"存档副本"上做，内存不动）

世界侧（`MarshalState` 内）：

| 项 | 处理 | 原因 |
|---|---|---|
| `Actor.Path` / `Work` | 清空 | 移动/劳作的半截状态没有意义，读档后重新决策 |
| `Actor.Busy` | 置 `BusyNone` | 同上；避免读档后"卡在移动中" |
| `Actor.Hidden` | 置 false | 睡觉隐藏是渲染态，读档时精灵必须可见 |
| `Actor.Carrying` | **保留** | 背在身上的物资属于世界事实，不是运行态 |

工作单（`marshalSaveLocked` 内，副本上）：

| 项 | 处理 | 原因 |
|---|---|---|
| `Jobs[].Status == "claimed"` | 副本改为 `"pending"`、`ClaimedBy=""` | 读档后执行流全部重建，认领者已不存在；不改会让这些单永久卡死（"幽灵认领者"）。内存中的单不动——正在干活的人不受存档影响 |

**不存（明确不恢复）**：agent goroutine、进行中的 workflow、信箱、通知板、进行中的对话、村民的 `nextThink`/冷却。读档 = 全员从"站在原地的空闲状态"继续生活，由各自的心跳重新决策。

## 7. 生命周期（什么时候写、什么时候读）

| 时机 | 路径 | 同步/异步 |
|---|---|---|
| 游戏日界（天数变化） | `tickOnce → autoSaveIfNewDayLocked → saveCh` | **异步**：序列化在锁内，写盘交给后台单写者（`saveCh` 容量 1，上一份没写完就跳过本次） |
| 退出（桌面版关窗 / 浏览器版 Ctrl+C） | `wails OnShutdown` / 信号处理 → `appParts.close → Engine.Stop` | **同步**：`Stop` 在锁内序列化并落盘（失败只记日志） |
| 手动 | `POST /api/save` → `Engine.SaveNow` | **同步**：返回时已写完 |
| 启动 | `boot()`：开库 → `AttachSaver`（**不自动读档**） | 同步；存档概况进 `GET /api/state.save`，前端展示"继续 / 新世界"选择页 |
| 继续上次 | `POST /api/continue` → `Engine.LoadSave` | 同步；恢复世界并重建全部村民 goroutine，世界帧经 SSE 广播 |
| 开新世界 | `POST /api/world` → 建图完成后立即 `saveAsyncLocked` | 异步；**立即覆盖旧档**（不必等第一个日界） |

- 新世界建图时会把"自动存档基点"初始化为当天，跨过第一个日界就会自动存。
- 日界存档基点 `lastSaveDay==0` 的兜底分支只服务于手工构造的引擎（测试）：首个观测日只记录、不写盘。
- **强迫杀进程（任务管理器/直接关控制台窗口）没有退出存档**：由日界自动存档兜底（最多丢一天）。
- 失败语义：`Open` 失败 → 纯内存运行（日志告警）；`continue` 遇坏档/版本不符/IO 错误 → `POST /api/continue` 返回 400（前端提示错误，玩家可改选"开新世界"，新世界会覆盖坏档）；写盘失败 → 仅日志告警，游戏继续。

## 8. 人工排障

文件位置见 §1。查询（需自备 `sqlite3` 命令行；仓库不内置）：

```sql
.schema                                   -- 看表结构
SELECT world_name, day, saved_at FROM slot; -- 不解压看元信息
SELECT key, value FROM meta;               -- 看 schema_version
```

解压 payload 查看 JSON（PowerShell，需本机有 Python）：

```powershell
python -c "import sqlite3,gzip; b=sqlite3.connect('saves.db').execute('select payload from slot').fetchone()[0]; print(gzip.decompress(b).decode('utf-8')[:3000])"
```

**清档重开**：停止程序后删除 `saves.db`（连同 `-wal`/`-shm` 文件），下次启动即空档。

## 9. 已知取舍与演进

- **弱一致窗口**：Citizen 的记忆/关系/心情由各自 agent goroutine 写，存档在世界线程读取，可能相差一条记忆。日界存档时村民多在睡觉、窗口极小；单机单用户可接受（等 ROADMAP"快操作意图化"后收口）。
- **随机序列不跨档连续**：rng 按种子重建，读档后的随机序列与"不读档一直跑"不相同（玩法无感，但别拿它做严格回归复现）。
- **多槽**：去掉 `slot` 的 `CHECK (id=1)`，把 `id` 当槽位号；`Save/Load` 增加槽位参数即可。
- **事件日志 / 向量记忆**：届时在同一个库里扩表；`sqlite-vec`（modernc 自带 vtab）用于记忆检索，载荷结构不需要动。
