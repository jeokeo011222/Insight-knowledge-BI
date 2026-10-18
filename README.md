<div align="center">

<img src="frontend/public/logo.svg" alt="智观 BI" width="88" />

# 智观 BI

**用对话，看懂你的数据。**

一个 AI 原生的开源 BI 平台 —— 自然语言问数、SQL 智能助写、指标分析、看板编排、数据大屏与行级安全，
全部装进**一个 Go 二进制**里。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white)](https://react.dev)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.7-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org)
[![Ant Design](https://img.shields.io/badge/Ant%20Design-5-0170FE?logo=antdesign&logoColor=white)](https://ant.design)
[![ECharts](https://img.shields.io/badge/ECharts-5.6-AA344D?logo=apacheecharts&logoColor=white)](https://echarts.apache.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-metadata-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/compose/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/jeokeo011222/Insight-knowledge-BI/pulls)

**[中文](#为什么要再做一个-bi)** ｜ **[English](#english-version)** ｜ **[用户手册](docs/manual/智观BI-用户操作手册.md)** ｜ **[User Manual](docs/manual/ZG-BI-User-Manual.md)**

<img src="docs/images/02-workspace.png" alt="智观 BI 工作台" width="100%" />

<sub>工作台：最近编辑的仪表盘、图表与数据集一览，左侧为按「数据资产 / 数据开发 / 系统设置」分组的导航。</sub>

</div>

---

## 为什么要再做一个 BI？

因为今天绝大多数 BI 工具都默认你**已经知道**该查哪张表、该用哪个字段、该写什么 SQL。

但真正每天在用数据的人 —— 运营、财务、供应链的同事 —— 往往卡在第一步：

> 「我想知道上个月华东区的退货率为什么涨了。」
> —— 这句话本身没毛病，难的是把它翻译成一条正确的 SQL。

智观 BI 的思路很简单：**在数据前面放一个会干活的 Agent。** 它自己去找表、看字段、对齐指标口径、
生成只读 SQL、把结果算清楚，然后给你结论与归因 —— 并且把**它每一步做了什么**都摊开给你看。

你负责提出好问题，它负责把它变成可信的答案。

## 七个亮点

| | 亮点 | 一句话说明 |
|---|---|---|
| 🤖 | **AI 分析，过程可审计** | 规划 → 调工具 → 观察 → 作答 的闭环推理，SSE 实时流式回放每一步；结论、SQL、取数行数全部留痕 |
| 🛡️ | **四层护栏 + 写前快照** | 最大步数 / 工具调用数 / Token 预算 / 运行超时；写工具先落快照，可按 run 逆向回滚 |
| 🔒 | **默认只读，越权即拒绝** | 只读 SQL 守卫（词法校验 + 表白名单 + 危险函数黑名单）、列级 `agent_visible`、结果脱敏、会话级只读三方言 |
| 📊 | **80+ 种可视化 + 图库** | 时间序列、分类、矩阵/透视、KPI/仪表、地理、以及 K 线 / 甘特 / 地平线 / 平行坐标 / 蝴蝶图等高级类型，内置可搜索图库 |
| 🧩 | **三种取数，一个出口** | 直连数据库（60+ 种引擎）、Excel/CSV 导入（含旧版 `.xls`、多 sheet 选择）、HTTP/API 接口 —— 最终都沉淀成同一种「数据集」 |
| 🖥️ | **大屏与看板都能绑数据集** | 仪表盘支持行/标签页布局、原生过滤器、模板导入导出；数据大屏每个数据组件都能二选一绑定「已保存图表」或「数据集」 |
| 🧭 | **指标口径统一** | 语义层指标字典注入提示词，模型不允许自造聚合口径；归因分解由程序计算（share / cum_share），杜绝口算误差 |

## 功能一览

### 1 · AI 分析：会解释自己的答案

<img src="docs/images/03-ai-chat.png" alt="AI 分析" width="100%" />

提问即取数。Agent 会先看有哪些表、哪些列、指标怎么定义，再决定用哪条 SQL；结果出来后按剩余
Token 预算动态截断（保留首尾），因此大表也能安全入模。答不下去时它不会硬编，而是回到规划继续补信息。

<div align="center">
<img src="docs/images/agent-runtime.svg" alt="Agent 运行时" width="100%" />
</div>

### 2 · 数据集：数据在哪，都从同一个门进来

<img src="docs/images/06-datasets.png" alt="数据集列表" width="100%" />

- **数据库直连** —— 查询时下推，不落库，方言引号 / 时间粒度 / `LIMIT` 语法自动适配；
- **Excel / CSV 导入** —— 支持新版 `.xlsx` 与旧版 `.xls`（OLE2 魔数识别，不靠扩展名猜）；工作簿有多个
  sheet 时会让你勾选，**默认只选第一个**，避免误建一堆数据集；
- **HTTP / API 接口** —— 仅允许 `http` / `https`，带超时、响应体大小、行数三重上限，凭据加密存储且**不回显**。

后两者物化成托管 SQLite 表（`external.db`）后再生成数据集，因此下游的查询、图表、探索、行级安全
**只有一份实现**。

<div align="center">
<img src="docs/images/data-ingestion.svg" alt="三种取数方式" width="100%" />
</div>

### 3 · 探索分析与可视化图库

<img src="docs/images/05-viz-gallery.png" alt="可视化图库" width="100%" />

左侧选数据集与指标，中间实时预览，底部看原始数据行 —— 这是 Explore 的日常。点开「查看全部图表」
会弹出可搜索、可按标签筛选（精选 / 高级分析）的**可视化图库**，80+ 种类型一屏挑完。

<img src="docs/images/04-charts.png" alt="图表列表" width="100%" />

### 4 · 仪表盘与数据大屏

<img src="docs/images/07-dashboard.png" alt="仪表盘" width="100%" />

行与标签页布局、拖拽调整、原生过滤器、自动刷新间隔选择器（**默认关闭** —— 我们不做默认刷屏的行为）、
一键导出/导入模板文件。

<img src="docs/images/08-screen.png" alt="数据大屏" width="100%" />

数据大屏支持图表、指标卡、表格、排行榜、进度条等组件，**每个数据组件都可以绑定数据集**，
而不仅仅是已保存的图表。

### 5 · 权限、AI 治理与可观测性

<img src="docs/images/09-access.png" alt="用户与角色" width="100%" />

用户管理、角色管理、行级安全（RLS）同一个页面的三个页签。内置五个角色 —— **管理员 / 数据分析师 /
数据查看者 / SQL 用户 / 公开用户** —— 内置角色不能改名或删除，但权限集合可自由调整。

<img src="docs/images/10-ai-settings.png" alt="AI 智能体设置" width="100%" />

AI 供应商、Agent、Skill、工具四张表分开治理。供应商的 API Key **只写不读**，接口只返回一个
"是否已配置" 的布尔值；内置 Agent / Skill / 工具是**只读**的，只有自建实例可改可删。

审计日志分「系统操作」与「AI 调用」两个页签，Agent 的每一次 run、每一次工具调用都有记录。

## 系统架构

<div align="center">
<img src="docs/images/architecture.svg" alt="系统架构" width="100%" />
</div>

后端是**单一二进制**，用三个子命令扮演三种角色：

```bash
./superset-dev run      # HTTP API + WebSocket + Agent 运行时
./superset-dev worker   # asynq 异步队列消费者（缩略图、报表、导出）
./superset-dev beat     # 定时调度（告警与定时报告）
```

元数据库 85 张表，全部由启动时的自动迁移建立（幂等）。

## 快速开始

**前置条件**：Docker、Go 1.22+、Node.js 18+。

```bash
git clone https://github.com/jeokeo011222/Insight-knowledge-BI.git
cd Insight-knowledge-BI/scripts
./start.sh              # Windows 用 start.bat 或 powershell -File start.ps1
```

脚本做五件事，而且**是幂等的** —— 已经在跑的部分会自动跳过：

1. 拉起 PostgreSQL（5533）与 Redis（6379）容器
2. 编译后端二进制
3. 迁移元数据库（建表 + 播种角色权限）
4. 启动 `run` / `worker` / `beat`
5. 启动前端 Vite dev server

打开 **http://localhost:3000**，用 **`admin` / `admin`** 登录即可。

### 生命周期脚本

| 操作 | Windows | Linux / macOS | 说明 |
|---|---|---|---|
| 启动 | `scripts\start.bat` \| `scripts\start.ps1` | `./scripts/start.sh` | 幂等，可重复执行 |
| 停止 | `scripts\stop.bat` \| `scripts\stop.ps1` | `./scripts/stop.sh` | 默认只停应用，保留数据库容器 |
| 重启 | `scripts\restart.bat` \| `scripts\restart.ps1` | `./scripts/restart.sh` | ≈ 先 stop 再 start |

常用参数（Windows 用 `-`，Linux 用 `--`）：

| 参数 | 作用 |
|---|---|
| `-SkipInfra` | 跳过容器，使用你自己的数据库 / Redis |
| `-SkipBuild` | 不重新编译，只重启进程（改前端或改配置时最常用） |
| `-SkipFrontend` | 不动前端 dev server |
| `-IncludeInfra` | （仅 stop）连数据库容器一起停 |

### 手工方式（不想用脚本）

```bash
docker compose -f docker/docker-compose.yml up -d     # 1. 基础设施

cd backend && go build -o superset-dev.exe ./cmd/superset
./superset-dev.exe db upgrade                          # 2. 建表 / 升级

./superset-dev.exe run &                               # 3. 三个角色分别起
./superset-dev.exe worker &
./superset-dev.exe beat &

cd frontend && pnpm install && pnpm run dev            # 4. 前端
```

## 技术栈

| 层 | 选型 |
|---|---|
| 后端 | Go 1.26 · Cobra CLI · `net/http` · JWT · SSE · asynq |
| 元数据 | PostgreSQL（85 张表，启动时自动迁移） |
| 缓存 / 队列 | Redis（缓存、事件总线、异步任务） |
| 前端 | React 18 · TypeScript 5.7 · Vite 6 · Redux Toolkit 2 · react-router 6 |
| UI | Ant Design 5 · Apache ECharts 5.6 |
| 部署 | Docker Compose（PostgreSQL + Redis） |
| 国际化 | 中文 / English 双语词条包完整对齐 |

## 目录结构

```
superset-go/
├── backend/                    Go 后端（单一二进制，三个子命令）
│   ├── cmd/superset/           入口：run / worker / beat / db
│   └── internal/
│       ├── ai/                 Agent 运行时、规划器、护栏、审计
│       ├── api/                HTTP 路由与处理器
│       ├── connectors/         60+ 数据库引擎规格与方言
│       ├── ingest/             文件导入与 HTTP 取数（物化到 external.db）
│       ├── queries/            查询引擎、后处理算子、缓存
│       ├── security/           RBAC 权限模型
│       └── models/             85 张表的模型层
├── frontend/                   React + TypeScript 前端
│   └── src/
│       ├── components/         图表渲染器、图库、大屏组件、AI 面板
│       ├── pages/              各业务页面
│       └── i18n/               中英双语词条
├── docker/                     docker-compose.yml（PostgreSQL + Redis）
├── docs/
│   ├── images/                 README 配图与架构图
│   └── manual/                 中英文用户操作手册
└── scripts/                    启动 / 停止 / 重启脚本
```

## 文档

- [智观 BI 用户操作手册](docs/manual/智观BI-用户操作手册.md) —— 中文，从登录到排障
- [ZG BI User Manual](docs/manual/ZG-BI-User-Manual.md) —— English, same structure

## 常见问题

<details>
<summary><b>登录后某个菜单项不见了？</b></summary>

菜单是按权限渲染的。默认只有 `管理员` 能看到系统设置下的全部入口。去「用户与角色 → 角色管理」
给自己的角色补权限即可。
</details>

<details>
<summary><b>后端改了代码，但接口返回 404？</b></summary>

旧进程还在跑。执行 `scripts\restart.bat`（或 `./scripts/restart.sh`）重新编译并重启即可。
</details>

<details>
<summary><b>导入旧版 .xls 报错？</b></summary>

旧版 `.xls` 是 OLE2 复合文档，解析路径与 `.xlsx` 完全不同。本项目通过魔数识别并按 XF 格式索引
判断日期类型 —— 如果仍失败，请把文件头部几行贴到 issue 里。
</details>

<details>
<summary><b>图表显示「暂无数据」？</b></summary>

多半是数据集本身没数据或时间范围被过滤掉了。先到「数据集」页点开该数据集预览原始行，
再去 Explore 放宽时间范围、清空筛选条件。
</details>

<details>
<summary><b>AI 分析报 Token 预算不足？</b></summary>

去「AI 智能体设置 → Agent 设置」把该 Agent 的护栏放宽。绑定数据集的场景下指标字典不做条数截断，
所以预算要留够；未绑定数据集时才会回落到 50 条的字典上限。
</details>

<details>
<summary><b>想彻底重置？</b></summary>

`scripts\stop.ps1 -IncludeInfra` 连容器一起停，再 `docker compose -f docker/docker-compose.yml down -v`
删掉数据卷，然后重新 `start`。注意这会清空所有元数据。
</details>

## 路线图

- [ ] 更多图表类型的交互式配置面板
- [ ] Agent 写操作的双向确认与更细粒度的回滚展示
- [ ] 数据集增量刷新与调度绑定
- [ ] 单点登录（OIDC / LDAP）
- [ ] 更完整的评测集与 NL2SQL 基准

## 参与贡献

Issue 与 PR 都欢迎。提交前请确保：

```bash
cd backend  && go build ./... && go vet ./...
cd frontend && npm run typecheck && npm run build
```

## 致谢与开源说明

本项目的元数据模型、API 语义与部分前端交互，**参照 Apache Superset 6.x 的设计重新实现**，
但代码是独立的 Go / TypeScript 实现，并非其分支。在此向上游社区致谢。

> 本项目**暂未附加开源许可证文件**。在补充 LICENSE 之前，默认保留所有权利 ——
> 如果你打算商用或二次分发，请先开 issue 沟通。若你的场景需要明确授权，
> 推荐参照上游采用 Apache-2.0，也欢迎直接提 PR 把 LICENSE 补上。

如果这个项目对你有帮助，欢迎点一个 ⭐ —— 它对开源项目真的很重要。

<br />

---

<div align="center">

# English Version

**Ask your data. Actually understand the answer.**

</div>

<img src="docs/images/02-workspace.png" alt="ZG BI workspace" width="100%" />

## Why another BI tool?

Because almost every BI tool silently assumes you already know *which* table to hit, *which* field to use,
and *what* SQL to write. The people who actually need answers every day — operations, finance, supply chain —
usually get stuck on the very first step:

> "I want to know why the return rate in East China jumped last month."

That sentence is fine. The hard part is translating it into correct SQL.

ZG BI takes a different approach: **put an agent in front of the data.** It finds the tables, reads the
schema, aligns with your metric definitions, generates a read-only query, computes the numbers, and hands
you a conclusion with the drivers behind it — while showing you **every step it took**.

You ask good questions. It makes them trustworthy answers.

## Highlights

| | Highlight | In one line |
|---|---|---|
| 🤖 | **Auditable AI analysis** | plan → act → observe → answer, streamed live; every conclusion, SQL statement and row count is on record |
| 🛡️ | **Four guardrails + snapshots** | max steps / max tool calls / token budget / run timeout; write tools snapshot first and roll back per run |
| 🔒 | **Read-only by default** | read-only SQL guard (lexical checks, table allowlist, dangerous-function blocklist), column-level visibility, result masking, session-level read-only |
| 📊 | **80+ visualizations + gallery** | time series, categorical, matrix/pivot, KPI/gauge, geo, plus candlestick / gantt / horizon / parallel coordinates / butterfly |
| 🧩 | **Three ways in, one way out** | direct DB (60+ engines), Excel/CSV import (incl. legacy `.xls`, multi-sheet), HTTP/API — all land as the same kind of *dataset* |
| 🖥️ | **Screens & dashboards** | dashboards with rows/tabs, native filters, template import/export; every screen widget can bind a saved chart **or** a dataset |
| 🧭 | **One metric definition, everywhere** | semantic-layer metric dictionary is injected into the prompt; attribution maths (`share` / `cum_share`) is computed in code, never by the model |

## Feature tour

### 1 · AI analysis that explains itself

<img src="docs/images/03-ai-chat.png" alt="AI analysis" width="100%" />

Ask in plain language. The agent first surveys available tables, columns and metric definitions, then decides
which query to run. Results are truncated against the remaining token budget (keeping head and tail), so large
tables still fit safely. If it cannot answer yet, it returns to planning instead of guessing.

<div align="center">
<img src="docs/images/agent-runtime.svg" alt="Agent runtime" width="100%" />
</div>

### 2 · Datasets: wherever the data lives, it enters through one door

<img src="docs/images/06-datasets.png" alt="Dataset list" width="100%" />

- **Direct connection** — pushdown at query time, nothing is copied; dialect quoting, time grains and `LIMIT` syntax adapted per engine.
- **Excel / CSV import** — both modern `.xlsx` and legacy `.xls` (detected by OLE2 magic number, not file extension). Multi-sheet workbooks prompt you to choose, and **only the first sheet is selected by default** so you never create a pile of datasets by accident.
- **HTTP / API** — `http` / `https` only, with timeouts plus response-size and row-count caps; credentials are encrypted and **never echoed back**.

The last two are materialised into a managed SQLite store (`external.db`) before becoming datasets, so querying,
charts, exploration and row-level security all share **a single implementation**.

<div align="center">
<img src="docs/images/data-ingestion.svg" alt="Three ingestion routes" width="100%" />
</div>

### 3 · Explore and the visualization gallery

<img src="docs/images/05-viz-gallery.png" alt="Visualization gallery" width="100%" />

Pick a dataset and metrics on the left, preview in the middle, inspect raw rows at the bottom. Click
"View all charts" and a searchable, tag-filtered (**Featured** / **Advanced**) gallery opens with 80+ types.

<img src="docs/images/04-charts.png" alt="Chart list" width="100%" />

### 4 · Dashboards and data screens

<img src="docs/images/07-dashboard.png" alt="Dashboard" width="100%" />

Rows and tabs, drag-and-drop resizing, native filters, an auto-refresh interval selector that is
**off by default** (we do not auto-refresh behind your back), and one-click template export/import.

<img src="docs/images/08-screen.png" alt="Data screen" width="100%" />

Screens support chart, KPI card, table, leaderboard and progress widgets. **Any data widget can bind a dataset
directly** — not only a saved chart.

### 5 · Access control, AI governance and observability

<img src="docs/images/09-access.png" alt="Users and roles" width="100%" />

Users, roles and row-level security are three tabs of one page. Five built-in roles ship out of the box —
**Admin / Analyst / Viewer / SQL User / Public** — built-in roles cannot be renamed or deleted, but their
permission set is fully editable.

<img src="docs/images/10-ai-settings.png" alt="AI agent settings" width="100%" />

Providers, Agents, Skills and Tools are governed in four separate tables. Provider API keys are
**write-only** — the API returns only a boolean "is configured". Built-in agents, skills and tools are
**read-only**; only your own instances can be edited or deleted.

The audit log splits into **system operations** and **AI calls**: every agent run and every tool invocation is recorded.

## Architecture

<div align="center">
<img src="docs/images/architecture.svg" alt="Architecture" width="100%" />
</div>

The backend is a **single binary** playing three roles:

```bash
./superset-dev run      # HTTP API + WebSocket + agent runtime
./superset-dev worker   # asynq queue consumers (thumbnails, reports, exports)
./superset-dev beat     # scheduled jobs (alerts and scheduled reports)
```

All 85 metadata tables are created by an idempotent migration on startup.

## Quick start

**Requirements**: Docker, Go 1.22+, Node.js 18+.

```bash
git clone https://github.com/jeokeo011222/Insight-knowledge-BI.git
cd Insight-knowledge-BI/scripts
./start.sh              # on Windows use start.bat or: powershell -File start.ps1
```

The script is **idempotent** — anything already running is skipped. It will:

1. start the PostgreSQL (5533) and Redis (6379) containers
2. build the backend binary
3. migrate the metadata database (tables + role/permission seeds)
4. launch `run` / `worker` / `beat`
5. start the frontend dev server

Then open **http://localhost:3000** and log in with **`admin` / `admin`**.

### Lifecycle scripts

| Action | Windows | Linux / macOS | Notes |
|---|---|---|---|
| Start | `scripts\start.bat` \| `scripts\start.ps1` | `./scripts/start.sh` | idempotent, safe to re-run |
| Stop | `scripts\stop.bat` \| `scripts\stop.ps1` | `./scripts/stop.sh` | stops the app, keeps DB containers |
| Restart | `scripts\restart.bat` \| `scripts\restart.ps1` | `./scripts/restart.sh` | ≈ stop then start |

| Flag | Effect |
|---|---|
| `-SkipInfra` | skip containers, use your own database / Redis |
| `-SkipBuild` | skip compilation, just restart processes |
| `-SkipFrontend` | leave the frontend dev server alone |
| `-IncludeInfra` | (stop only) also stop the database containers |

## Tech stack

| Layer | Choice |
|---|---|
| Backend | Go 1.26 · Cobra CLI · `net/http` · JWT · SSE · asynq |
| Metadata | PostgreSQL (85 tables, auto-migrated on startup) |
| Cache / queue | Redis (cache, event bus, background jobs) |
| Frontend | React 18 · TypeScript 5.7 · Vite 6 · Redux Toolkit 2 · react-router 6 |
| UI | Ant Design 5 · Apache ECharts 5.6 |
| Deployment | Docker Compose (PostgreSQL + Redis) |
| i18n | Fully aligned Chinese / English message packs |

## Documentation

- [ZG BI User Manual (EN)](docs/manual/ZG-BI-User-Manual.md)
- [智观 BI 用户操作手册 (中文)](docs/manual/智观BI-用户操作手册.md)

## Notes, licensing and acknowledgements

The metadata model, API semantics and parts of the frontend interaction are a **clean-room reimplementation
inspired by Apache Superset 6.x** — the code is an independent Go / TypeScript implementation, not a fork.
Thanks to the upstream community.

> This project **does not ship a LICENSE file yet**. Until one is added, all rights are reserved —
> please open an issue before commercial use or redistribution. If you need explicit terms,
> Apache-2.0 (matching upstream) is the recommended choice, and a PR adding it is very welcome.

If this project helps you, a ⭐ goes a long way.
