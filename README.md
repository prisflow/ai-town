# AI 领地 · ai-town

[![CI](https://github.com/prisflow/ai-town/actions/workflows/ci.yml/badge.svg)](https://github.com/prisflow/ai-town/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/prisflow/ai-town?display_name=tag)](https://github.com/prisflow/ai-town/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

一个**能用自己的 API Key（BYOK）畅玩的 2D 像素模拟领主游戏**：你扮演领主，用自然语言下达指令；一群由大模型驱动的智能体村民感知环境、互相交流、认领工作单，并发地完成"伐木 / 采集 / 建造 / 农耕"等任务。世界初始化、指令规划、村民决策、对话、旁白全部由 AI 承接；寻路、经济结算、时间等走确定性规则，保证稳定可复现。

> 后端 Go 单二进制（内嵌前端与纯 Go SQLite）；前端 PixiJS 8 程序化像素美术，零素材依赖。
> 没有 API Key 也能玩：内置 **mock 演示模式**，离线跑通全部玩法。

---

## 📺 实机演示

<video controls src="https://github.com/user-attachments/assets/1b9ca570-6e01-4186-9821-1d10d342238c" width="720"></video>

（若视频未能内嵌播放，[点此直接观看](https://github.com/user-attachments/assets/1b9ca570-6e01-4186-9821-1d10d342238c)；想亲自体验：下载下面的桌面版，双击即可开玩。）

## 核心特性

- **多智能体并发（goroutine-per-agent）**：每个村民是一条独立的 goroutine——自己的信箱、自己的心跳、自己的记忆；空闲即思考，对世界的一切动作经世界单写者线程仲裁，真并发且无数据竞争
- **官员 / 平民分层**：官员拥有完整 LLM 大脑（决策、社交、发布工作单），平民零 token 机械劳作——人口可以扩，token 只随官员数线性增长
- **Agent 与 Workflow 严格分离**：智能体只做决策；行为封装为 JSON workflow 模板（`goto → harvest → deposit → say` …），内置 15 个模板 / 16 种步骤原语，可自定义扩展
- **世界初始化**：一句话描述 → LLM 生成世界传说与村民阵容 → 确定性算法铺开 48×48 地图（湖、森林、浆果、岩石、领主堡、民居、农田、粮仓、磨坊）
- **领主指令**：自然语言经"总管规划器"分解为工作单；官员也会根据村情自主发单，村民按职业/材料/优先级认领
- **社交与关系**：官员偶遇闲聊（LLM 对话），好感随谈话氛围升降，跨阈值成为朋友/挚友/关系恶化，事件写入各自记忆
- **BYOK 直连**：OpenAI 兼容 / Anthropic / mock 三类提供商；6 个内部角色可分别路由到不同模型；密钥只存本地，带并发 / RPM / 超时限额；创意度（temperature）与游戏节奏均可在设置里调
- **生存循环**：季节系统、食物三级链（小麦→面粉→面包+磨坊）、粮仓独立容量、资源再生、随机事件（商队/暴雨/野狗，频率可调）
- **SQLite 存档**：单存档槽，每天日界自动存 + 退出自动存 + 顶栏手动存；启动时"继续 / 新世界"选择页
- **两种运行形态**：Wails 桌面版（单个 exe + 窗口）与浏览器版（本地 HTTP 服务），同一套代码、同一个二进制

## 下载与安装

前往 [Releases](https://github.com/prisflow/ai-town/releases) 下载对应压缩包：

### 桌面版（推荐）

- 下载 `ai-town_desktop_v*_windows_amd64.zip`，解压后双击 `aitown-desktop.exe`
- 环境要求：Windows 10/11（依赖系统自带的 WebView2 运行时，绝大多数机器已内置；缺失时按系统提示安装即可）
- 数据目录：`%AppData%\aitown`（`config.json` 配置、`saves.db` 存档、`logs` 日志、`webview` 缓存）

### 浏览器版

- 下载 `ai-town_browser_v*_windows_amd64.zip`，解压后运行 `aitown.exe`，浏览器打开 <http://127.0.0.1:8080>
- 数据目录：exe 同级的 `data/`（可用 `-data` 指定其它目录）

### 首次进入

1. **继续上次 / 开新世界**：有存档时可选择继续或开新世界（开新世界会覆盖旧档）
2. 点击 **⚙ 设置** 填入你的 API Key（或保持 mock 演示模式直接开玩）
3. 右下角**领主控制台**输入指令，例如 `多储备一些木材`、`建造一座粮仓`

## 快速开始（源码构建）

前置：Go 1.27+、Node 20+。

### 浏览器版

```powershell
cd web
npm ci
npm run build      # tsc + vite，产物 web/dist 会被 go:embed 内嵌
cd ..
go build -o aitown.exe .
.\aitown.exe       # 默认 http://127.0.0.1:8080（-addr / -data 可调）
```

### 桌面版（Wails v2）

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
cd web && npm ci && npm run build && cd ..
wails build -s -clean          # 输出 build/bin/aitown-desktop.exe
```

### 开发模式（前端热更）

```powershell
.\aitown.exe                # 终端 1：后端 :8080
cd web && npm run dev       # 终端 2：Vite :5173（/api 已代理到 8080）
```

## BYOK 配置

点击右上角 **⚙ 设置**：

- **类型 openai**：一切 OpenAI 兼容接口。常用 Base URL：

  | 提供商 | Base URL | 模型示例 |
  |---|---|---|
  | OpenAI | `https://api.openai.com/v1` | `gpt-4o-mini` |
  | DeepSeek | `https://api.deepseek.com/v1` | `deepseek-chat` |
  | Moonshot | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` |
  | 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | `qwen-plus` |
  | 智谱 | `https://open.bigmodel.cn/api/paas/v4` | `glm-4-flash` |
  | OpenRouter | `https://openrouter.ai/api/v1` | 任意 |
  | Ollama | `http://127.0.0.1:11434/v1` | `qwen2.5:7b` |
  | LM Studio | `http://127.0.0.1:1234/v1` | 本地加载的模型 |

- **类型 anthropic**：Base URL 填 `https://api.anthropic.com`
- **类型 mock**：离线演示，不消耗 token

**角色路由**（6 个角色可分别指到不同提供商）：`worldgen`（世界初始化）、`planner`（领主指令规划）建议用强模型；`brain`（村民自主决策，高频小请求）、`dialogue`（村民对话）、`choice`（分支）、`narrate`（旁白）用便宜模型即可。所有请求从本机直接发往提供商，密钥只保存在本地配置文件。

**设置页还有**：创意度（各角色 temperature）、节奏（思考冷却 / 社交冷却 / 对话轮数 / 随机事件频率 / 开局官员数 / 入夜与天亮时刻）、额度（并发 / RPM / 请求超时 / 官员上限 / 对话场数上限）。

## 玩法说明

| 环节 | 说明 |
|---|---|
| 指令 | 控制台输入自然语言；总管规划器分解为工作单（采集/建造），建造自动选址圈工地 |
| 官员与平民 | 官员自主决策、社交、向公告栏发布工作单；平民零 token 按职业劳作、自动认领工作单 |
| 认领 | 按模板允许的职业 + 材料就绪情况认领；失败会反馈原因（如"料没备齐""不是你的职业能干的活"） |
| 日常 | 无单时村民按职业本能自发采集/劳作、散步、闲聊（官员之间）；情绪会随事件起伏 |
| 节律 | 默认 21:30 后回家睡觉、6:00 天亮、7:00 为日界；夜里全员入睡后自动快进到清晨（均可在设置-节奏调整） |
| 存档 | 每天日界自动存、关闭窗口/ Ctrl+C 退出自动存、顶栏 💾 手动存；启动时选择继续或开新世界 |
| 观察 | 左侧面板看居民状态/性格/背包（再点一下收起详情），也可以直接点画布上的村民 |
| 人事 | 面板内可任命 / 罢免官员、放逐村民 |
| 顶栏 | 暂停（空格）/ 存档 / 工作单看板 / 诊断面板 / 设置 |

## 存档

- 位置：桌面版 `%AppData%\aitown\saves.db`、浏览器版 `<data>/saves.db`（SQLite，WAL）
- 单槽覆盖；字段级约定、归一化规则与排障方法见 [SAVE-SPEC](docs/SAVE-SPEC.md)

## 目录结构

```
ai-town/
├── main.go / main_wails.go / boot.go   # 双入口：浏览器版 / Wails 桌面版 + 共用启动装配
├── webfs.go                            # go:embed 内嵌前端产物
├── wails.json                          # Wails 打包配置（含 build:tags）
├── build/                              # Wails 打包资源（图标 / Windows 资源 / NSIS 模板）
├── internal/
│   ├── config/    # BYOK 配置（providers / roles / limits / pacing）
│   ├── llm/       # LLM 网关：openai 兼容 + anthropic + mock、限流、统计
│   ├── sim/       # 确定性仿真：瓦片 / 实体 / A* 寻路 / 劳作结算 / 时间与季节
│   ├── workflow/  # 行为封装：JSON 模板 + 解释器（16 种步骤原语）
│   ├── worldgen/  # 世界初始化：LLM 规格 → 确定性建图
│   ├── game/      # 编排层：agent 循环 / 对话 / 规划器 / 工作单 / 存档 / SSE 消息
│   ├── server/    # HTTP API（REST + SSE）+ 静态 SPA
│   ├── store/     # 存档持久化（SQLite，单槽 gzip 快照）
│   └── xlog/      # 诊断日志（异步落盘 + 环形 tail）
├── web/           # Vite + React 19 + Tailwind v4 + PixiJS 8
└── docs/          # 架构 / 设计 / 协议 / 数据模型 / 存档规范 / 路线图
```

## 开发与测试

```powershell
go vet ./...
go test ./...              # 后端全量测试（含引擎集成测试，全部离线 mock）
cd web && npm run build    # 前端 tsc + vite
```

**协议/模型文档是生成物**：SSE 帧与领域模型的唯一事实源是 Go 结构体（及其注释）。
改了协议或模型后运行 `go run ./cmd/docgen`，自动再生：

- `docs/PROTOCOL.md`（SSE 帧字段 + REST 端点）
- `docs/DATA-MODEL.md`（领域模型字段表）
- `docs/schemas/*.json`（JSON Schema，机器可校验）
- `web/src/api.gen.ts`（前端 TS 类型——前端构建会立即暴露前后端协议漂移）

发布：推 `v*` tag 触发 [release workflow](.github/workflows/release.yml)，自动打包桌面版 / 浏览器版并创建 Release（含 SHA256SUMS）。

更多文档：[游戏设计](docs/GAME-DESIGN.md) · [架构与并发模型](docs/ARCHITECTURE.md) · [Workflow 模板规范](docs/WORKFLOW-SPEC.md) · [存档规范](docs/SAVE-SPEC.md) · [协议参考](docs/PROTOCOL.md) · [数据模型](docs/DATA-MODEL.md) · [路线图](docs/ROADMAP.md)。

## 已知限制（v0.1）

- **单存档槽**：启动可选"继续 / 新世界"，但还没有多槽选档界面
- **预编译包只有 Windows x64**（桌面版依赖 WebView2；源码在其它平台可自行构建）
- 只有官员调用 LLM（平民纯机械是设计取舍，不是缺陷）——想让更多人"有脑子"，任命他们当官员即可
- 记忆检索是最近窗口截断，不是向量召回（路线图内）
- LLM 费用自理；界面为中文，暂无多语言

## License

[MIT](LICENSE) © 2026 prisflow
