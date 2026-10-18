<div align="center">

<img src="frontend/public/logo.png" alt="智观 BI" width="88" />

# 智观 BI

**用对话，看懂你的数据。**

一个 AI 原生的源码可见 BI 平台 —— 自然语言问数、SQL 智能助写、指标分析、看板编排、数据大屏与行级安全，
全部装进**一个 Go 二进制**里。

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white)](https://react.dev)
[![TypeScript](https://img.shields.io/badge/TypeScript-5.7-3178C6?logo=typescript&logoColor=white)](https://www.typescriptlang.org)
[![Ant Design](https://img.shields.io/badge/Ant%20Design-5-0170FE?logo=antdesign&logoColor=white)](https://ant.design)
[![ECharts](https://img.shields.io/badge/ECharts-5.6-AA344D?logo=apacheecharts&logoColor=white)](https://echarts.apache.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-metadata-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://docs.docker.com/compose/)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](https://github.com/jeokeo011222/Insight-knowledge-BI/pulls)
[![License](https://img.shields.io/badge/License-PolyForm%20Noncommercial%201.0.0-orange.svg)](LICENSE)

**[中文](README.md)** ｜ **[English](README_EN.md)** ｜ **[用户手册](智观BI-用户操作手册.md)** ｜ **[User Manual](Insight-knowledge-BI-User-Manual.md)**

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

## 八个亮点

| | 亮点 | 一句话说明 |
|---|---|---|
| 🤖 | **AI 分析，过程可审计** | 规划 → 调工具 → 观察 → 作答 的闭环推理，SSE 实时流式回放每一步；结论、SQL、取数行数全部留痕 |
| 🛡️ | **四层护栏 + 写前快照** | 最大步数 / 工具调用数 / Token 预算 / 运行超时；写工具先落快照，可按 run 逆向回滚 |
| 🔒 | **默认只读，越权即拒绝** | 只读 SQL 守卫（词法校验 + 表白名单 + 危险函数黑名单）、列级 `agent_visible`、结果脱敏、会话级只读三方言 |
| 📊 | **80+ 种可视化 + 图库** | 时间序列、分类、矩阵/透视、KPI/仪表、地理、以及 K 线 / 甘特 / 地平线 / 平行坐标 / 蝴蝶图等高级类型，内置可搜索图库 |
| 🧩 | **三种取数，一个出口** | 直连数据库（60+ 种引擎）、Excel/CSV 导入（含旧版 `.xls`、多 sheet 选择）、HTTP/API 接口 —— 最终都沉淀成同一种「数据集」 |
| 🖥️ | **大屏与看板都能绑数据集** | 仪表盘支持行/标签页布局、原生过滤器、模板导入导出；数据大屏每个数据组件都能二选一绑定「已保存图表」或「数据集」 |
| 🧭 | **指标口径统一** | 语义层指标字典注入提示词，模型不允许自造聚合口径；归因分解由程序计算（share / cum_share），杜绝口算误差 |
| 🔁 | **同题同答案** | 低温 + 固定 SQL 模板 + 语句规范化 + 回答记忆：同一个问题问两遍得到同一条 SQL 的比率从 6.4% 提到 91.5%（几乎全靠回答记忆；关掉它只靠提示词的对照是 8.7%） |

## 功能一览

### 1 · AI 分析：会解释自己的答案

<img src="docs/images/03-ai-chat.png" alt="AI 分析" width="100%" />

提问即取数。Agent 会先看有哪些表、哪些列、指标怎么定义，再决定用哪条 SQL；结果出来后按剩余
Token 预算动态截断（保留首尾），因此大表也能安全入模。答不下去时它不会硬编，而是回到规划继续补信息。

**同一个问题问两遍，应该是同一个答案。** 这靠四层而不是靠运气：规划温度压到 0；系统提示里给出
固定 SQL 模板（骨架、聚合白名单、别名规则、schema 限定）；执行前所有语句过一遍规范化
（引号、大小写、schema 前缀、`COUNT(1)→COUNT(*)`、投影别名按表达式重写）；重复提问命中回答记忆——
**重新执行同一条 SQL**，结果指纹（行数 + 排序后的行集合）一致才复用答案，数据变了就重新生成。
50 题 × 2 次的评测里，「同题两跑得到同一 SQL」的比率从 6.4% 提到 91.5%，重复提问 0 token。
诚实地说：这条提升几乎全部来自回答记忆——关掉回放、只靠降温 + 模板 + 规范化，实测只有 8.7%。所以我们的说法是「重复提问这一层可复现」，
不是「模型每次都能写出同一条 SQL」；后者要靠确定性编译器（问题 → spec → SQL），还没做。

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

## 把 BI 接到别的 Agent 上（MCP）

除了内置的 Agent，取数能力也可以挂到一个外部的 MCP 客户端（Claude Code、Cursor、任何支持
Model Context Protocol 的宿主）上，让它把「问数」当成一个工具用：

```bash
./superset-dev mcp            # stdio JSON-RPC，由 MCP 宿主启动
```

客户端配置里给三个环境变量即可：

```json
{
  "mcpServers": {
    "superset-bi": {
      "command": "/path/to/superset-dev",
      "args": ["mcp"],
      "env": {
        "SUPERSET_MCP_URL": "http://localhost:8088",
        "SUPERSET_MCP_USERNAME": "admin",
        "SUPERSET_MCP_PASSWORD": "admin"
      }
    }
  }
}
```

也可以用 `SUPERSET_MCP_TOKEN` 传一个长效 token。暴露的工具是**只读**的五个：

| 工具 | 做什么 |
|---|---|
| `list_datasets` | 列出可读数据集 |
| `describe_dataset` | 列、指标、语义模型字段与校验问题 |
| `dry_plan` | 只出 SQL 与语义层自动加的过滤条件，不执行 |
| `semantic_query` | 结构化取数（指标 + 维度 + 时间 + 过滤），不写 SQL |
| `run_sql` | 数据集作用域的只读 SQL，过守卫、RLS 与脱敏 |

关键点：MCP 进程只是后端 API 的一个瘦客户端，**每个调用都以真实用户身份执行** ——
权限、行级安全、列级脱敏、SQL 守卫、行数上限都照常生效，不存在「换个入口就能绕过」的旁路。

语义模型还能按 [OSI](https://github.com/open-semantic-interchange/OSI) 0.2.0 导入导出
（数据集详情 → 语义模型 → `OSI` / `导入 OSI`），导出的文件可以带到别的 BI 工具里，
换工具不用重建指标。

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

- [智观 BI 用户操作手册](智观BI-用户操作手册.md) —— 中文，从登录到排障
- [ZG BI User Manual](Insight-knowledge-BI-User-Manual.md) —— English, same structure

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

## 致谢与许可说明

本项目的元数据模型、API 语义与部分前端交互，**参照 Apache Superset 6.x 的设计重新实现**，
但代码是独立的 Go / TypeScript 实现，并非其分支。在此向上游社区致谢。

> 本项目采用 **PolyForm Noncommercial License 1.0.0** —— 源码可见，但**仅限非商业用途**。
> 个人学习、科研实验、非营利教育等场景可自由使用、修改与分发；
> 若要用于 SaaS 服务、企业付费业务、外包项目、售卖或集成进商业产品对外分发，
> 须另行取得授权。完整条款见 [LICENSE](LICENSE)。

如果这个项目对你有帮助，欢迎点一个 ⭐ —— 它对开源项目真的很重要。
