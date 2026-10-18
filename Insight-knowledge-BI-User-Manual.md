# ZG BI User Manual

> Insight Knowledge BI · AI-native BI platform
> Repository: <https://github.com/jeokeo011222/Insight-knowledge-BI>

ZG BI (English name ZG BI / Insight Knowledge BI) is an open-source, AI-native BI platform. Its goal is simple: **make connecting a database, pulling data, building charts, assembling dashboards, writing SQL, and asking AI all happen in one interface — instead of being scattered across five or six tools**. The backend is a single Go binary; the frontend is React + TypeScript + Vite + Ant Design v5 + Apache ECharts. The metadata database is PostgreSQL (85 tables), and the task queue is Redis + asynq.

This manual walks through everything in the left sidebar, in the order "left to right, top to bottom". You can read it cover to cover, or treat it as a dictionary — jump to whichever module you need.

![Overall architecture](docs/images/architecture.svg)

---

## Applicable Version

| Item | Value |
| --- | --- |
| Product name | ZG BI (Insight Knowledge BI) |
| Backend | Go single binary, three subcommands: `run` / `worker` / `beat` |
| Frontend | React + TypeScript + Vite + Ant Design v5 + Apache ECharts |
| Metadata DB | PostgreSQL (85 tables) |
| Cache / queue | Redis + asynq |
| Local dependencies | Docker Compose (PostgreSQL + Redis) |
| Backend API port | `8088` (health check `/health`) |
| Frontend dev port | `3000` |
| Default login | `admin` / `admin` (created by first-run bootstrap) |
| UI language | 中文 / English (toggle in the header), plus a light/dark theme toggle |

> Note: This manual reflects the current version. If your UI differs slightly, the actual UI wins.

---

## Table of Contents

1. [Login and Workspace](#chapter-1-login-and-workspace)
2. [Database Connections](#chapter-2-database-connections)
3. [Datasets](#chapter-3-datasets)
4. [Explore](#chapter-4-explore)
5. [Visualization Gallery](#chapter-5-visualization-gallery)
6. [Chart List](#chapter-6-chart-list)
7. [Dashboards](#chapter-7-dashboards)
8. [Screens](#chapter-8-screens)
9. [SQL Lab](#chapter-9-sql-lab)
10. [AI Analysis](#chapter-10-ai-analysis)
11. [Alerts and Reports](#chapter-11-alerts-and-reports)
12. [Users and Roles](#chapter-12-users-and-roles)
13. [AI Agent Settings](#chapter-13-ai-agent-settings)
14. [Audit Logs](#chapter-14-audit-logs)
15. [System Management](#chapter-15-system-management)
16. [Lifecycle Scripts](#chapter-16-lifecycle-scripts)
17. [FAQ](#faq)
18. [Appendix](#appendix)

### Left Sidebar Map

The sidebar is grouped. Build a "mental map" first — every section below refers back to it:

| Position | Group | Menu items |
| --- | --- | --- |
| Top | — | Workspace |
| Upper group | Data Assets | AI Analysis, Dashboards, Screens, Charts |
| Middle group | Data Development | Datasets, Databases, SQL Lab, Annotation Layers |
| Lower group | System Settings | System Management, Users & Roles, AI Agents, Alerts & Reports, Audit Logs |

---

## Chapter 1. Login and Workspace

**What this step solves:** First prove "who you are", then decide "what you can see and change". In ZG BI all data is constrained by role permissions, so logging in isn't just a formality — it's the entry point to the permission system.

![Login page](docs/images/01-login.png)

### 1.1 Logging in

1. Open `http://localhost:3000` in your browser to reach the login page.
2. Enter your username and password (on first use, `admin` / `admin`).
3. Click "Login"; on success you land on the **Workspace**.
4. If login fails, it's usually wrong credentials, or the metadata DB being unreachable after a config change; see the [FAQ](#faq).

> Common misconception: `admin` / `admin` is a default account created by the **first-run bootstrap**, meant only for getting started locally. For anything exposed externally, change the password under "Users & Roles" immediately.

### 1.2 Workspace

**What this step solves:** When you start work each day, you don't want to search from scratch. The Workspace surfaces the dashboards, charts, and datasets you touched recently, so you can pick up where you left off.

![Workspace](docs/images/02-workspace.png)

The Workspace has several blocks:

| Block | Contents | What you can do |
| --- | --- | --- |
| Recent dashboards | Cards for dashboards you recently opened / edited | Click to open the dashboard |
| Recent charts | Recently saved / edited charts | Click to keep editing in Explore |
| Recent datasets | Recently used datasets | Click to open the dataset detail |
| Agent-count KPI card | The number of AI agents currently available | Click to open the "AI Agents" settings |

### 1.3 Header

From left to right, the header has: the sidebar collapse button, a breadcrumb (your current location), the language toggle (中文 / English), the light/dark theme toggle, and the account entry (personal settings / logout).

> Tip: The language and theme toggles only affect **your own** current browser — they don't change anyone else's UI and don't change the system default. To change the system default theme, go to "Theme Management" in Chapter 15.

---

## Chapter 2. Database Connections

**What this step solves:** The data lives in someone else's database, so the platform first needs to know "where to fetch from and with what credentials". A database connection is that "address sign + key"; all direct-connection datasets, SQL Lab, and AI question-answering build on top of it.

![Three ingestion routes overview](docs/images/data-ingestion.svg)

### 2.1 Adding a database connection

1. In the left sidebar, go to **Data Development → Databases**.
2. Click "Add" in the top-right corner.
3. Choose the database type from the engine dropdown. The platform supports **60+ engine types** (PostgreSQL, MySQL, ClickHouse, SQL Server, Oracle, SQLite, Trino, Presto, Druid, Elasticsearch, Kylin, and more).
4. Fill in the connection info, one of two ways:
   - Fill in fields: host / port / database name / username / password;
   - Or paste a connection URI directly.
5. Click "Test connection". Success shows a confirmation; failure gives a clear error (for example, when a driver isn't bundled with the binary, it returns `driver_not_bundled`).
6. Save.

### 2.2 Two switches to watch after connecting

| Switch | Meaning | Recommendation |
| --- | --- | --- |
| SQL Lab access | Whether SQL can be written against this database in SQL Lab | For production DBs, enable only for trusted roles |
| Write capability | Whether write operations are allowed | For analytics, strongly recommend keeping this **off** (read-only) |

> Common misconception: Filling in a connection doesn't mean everyone can use it. Actually seeing data is still governed by dataset / database / schema permissions, falling back level by level — see Chapter 12.

### 2.3 About drivers

The platform bundles some real drivers that connect directly; another set of engines has only dialect and metadata specifications registered. If a connection fails with `driver_not_bundled`, that engine's driver wasn't bundled with the binary — bring in the matching driver as indicated. The platform **never fakes a connection**.

---

## Chapter 3. Datasets

**What this step solves:** A database has dozens of tables, and business colleagues don't care about table names. A dataset turns data into "an object a business person can understand and a chart can consume directly". It's also the shared foundation for permissions, metric definitions, and AI question-answering.

![Dataset list](docs/images/06-datasets.png)

### 3.1 Three ingestion routes, one destination

Whichever route you take, you end up with the **same "dataset" object**. The only difference is where the data comes from and when it's fetched.

|  | ① Direct database | ② Excel / CSV file import | ③ HTTP / API interface |
| --- | --- | --- | --- |
| Data source | A table in your business DB | A spreadsheet file you have | An endpoint URL |
| Fetch timing | Query-time pushdown (no landing) | Landed once at import time | Landed when pulled |
| Where data lives | Still in the source DB | Platform-managed SQLite store (`external.db`) | Platform-managed SQLite store (`external.db`) |
| Best for | Stable production tables | One-off / periodic ledgers | Integrating external systems |
| Data freshness | Live (follows the source) | Depends on when you re-import | Depends on when you re-pull |

**Key point:** Data from file imports and API pulls is **materialised into the platform's own SQLite database** (the `external.db` file), and then becomes a dataset. This means — whether direct or imported — subsequent query, charting, explore, and row-level security (RLS) **reuse one and the same code path**, behaving consistently. You don't have to remember two sets of rules.

### 3.2 ① Direct database

1. Go to **Data Development → Datasets**, click "New dataset".
2. Choose "Direct database".
3. Pick database → schema → table.
4. Save to finish; the platform syncs the columns.
5. Afterwards, you can maintain columns and metrics (definitions) on the dataset.

### 3.3 ② Excel / CSV file import

1. When creating a dataset, choose "File import" and upload the file.
2. The platform supports both modern `.xlsx` and **legacy `.xls`**. Old `.xls` files are detected via the OLE2 magic number, so you don't need to convert the format first.
3. If the workbook has **more than one sheet**, the platform prompts you to choose. The **first sheet is selected by default**.
4. **Each sheet you select becomes its own independent dataset**.
5. When done, they appear in the dataset list.

> Common misconception: Assuming one Excel file can only produce one dataset. In fact, selecting multiple sheets yields multiple datasets, each maintained independently.

### 3.4 ③ HTTP / API interface

1. When creating a dataset, choose "API interface".
2. Enter the endpoint URL. **Only http / https is allowed**.
3. Configure as needed: timeout, response-size limit, row-count limit.
4. If it needs auth, enter credentials. Note: **credentials are never echoed back** — after saving, the API won't return them to you, and the UI won't display them in plain text.
5. Successfully pulled data lands in `external.db` and becomes a dataset.

### 3.5 What you can change inside a dataset

Opening a dataset detail lets you: view / edit columns, maintain metrics (definitions), re-sync columns, preview data, and connect it to Explore or a screen.

> Tip: Define a metric here once, and charts and AI question-answering will reuse that definition — this is what the "semantic layer" means, see Chapter 10.

---

## Chapter 4. Explore

**What this step solves:** A dataset is the "raw material"; Explore is the "workbench". It lets you pick dimensions, pick metrics, add filters, and switch chart types without writing SQL, seeing the result in real time.

### 4.1 Layout

Explore's layout is fixed in three parts:

- **Left**: the metric / column configuration panel (choose data, configure metrics, add filters).
- **Centre**: the chart preview area (change one thing, the chart updates).
- **Bottom**: the data preview table (so you can see exactly what data is behind the chart).

### 4.2 Typical flow for one chart

1. Go to **Data Assets → Charts**, or click "Explore" from a dataset, to enter Explore.
2. Pick a dataset.
3. Choose a visualization type (or open the visualization gallery to pick one, see Chapter 5).
4. On the left, configure **metrics** and **columns / dimensions**.
5. Add filters as needed.
6. Watch the chart preview update in the centre and the data detail at the bottom.
7. When satisfied, click "Save as chart", name it, and save.

> Common misconception: An empty chart is usually because no metric was selected, or the time range filtered everything out. Check the bottom data preview first — that tells you whether it's "the query returned nothing" or "the chart isn't configured".

---

## Chapter 5. Visualization Gallery

**What this step solves:** You may not know which chart you want. The gallery lays out a Superset-style collection of chart types so you can "pick by looking", instead of "remembering type names".

![Visualization gallery](docs/images/05-viz-gallery.png)

### 5.1 What it looks like

- A **gallery modal** styled like Superset's.
- Around **80 chart types**.
- A **search box** at the top, plus filters by tag: **精选 (Featured) / 高级分析 (Advanced Analytics)**.

The gallery covers these major families:

| Family | Examples |
| --- | --- |
| Time-series | Lines, areas, columns over time, etc. |
| Categorical | Columns, bars, pie, donut, etc. |
| Matrix / pivot / table | Pivot table, plain table |
| Numeric | KPI, gauge |
| Geo / map | Map-related charts |
| Advanced analytics | Candlestick, gantt, horizon, parallel coordinates, butterfly, tree |

### 5.2 Where to open the gallery

There are three entry points, any of which works:

1. The "查看全部图表" (View all charts) link in the **Explore left panel**;
2. The **chart list page**;
3. Inside a **dashboard's "Add chart" drawer**.

Once you pick a type, it carries that type back into the current context so you can keep configuring.

> Tip: Advanced analytics charts (candlestick, gantt, parallel coordinates, etc.) require specific data shapes — confirm your fields match before choosing one.

---

## Chapter 6. Chart List

**What this step solves:** Once you've made lots of charts, you need to find them, edit them, and delete them. The chart list is your "shelf of work".

![Chart list](docs/images/04-charts.png)

### 6.1 What you can do

| Action | How | Result |
| --- | --- | --- |
| Search | Type a keyword in the top search box | Filters by name |
| Keep editing | Click a chart's name | Returns to Explore to keep editing that chart |
| Delete | The delete action in the row | The chart is removed |

> Common misconception: Before deleting a chart, think about whether some dashboard references it. If the chart is gone, that tile on the dashboard goes empty.

---

## Chapter 7. Dashboards

**What this step solves:** A single chart answers one question; a set of charts answers "how is this business doing right now". A dashboard assembles several charts into one page, with filters and layout.

![Dashboard detail](docs/images/07-dashboard.png)

### 7.1 List and creation

1. Go to **Data Assets → Dashboards**.
2. The list supports switching between a **card view** and a **table view**.
3. Click "New" to create a dashboard, name it, and save.
4. Click a dashboard to open its detail.

### 7.2 Editing the layout

In edit mode:

1. The layout is made of **rows + tabs**.
2. Add charts: open the "Add chart" drawer and pick from existing charts; the drawer also includes the **visualization gallery** entry.
3. **Drag / resize** each chart tile to set its position and size.
4. Save, then switch to view mode.

### 7.3 Filters, refresh, and sharing

| Capability | Description |
| --- | --- |
| Native filters | Filters available on the page, applied to each chart's data |
| Auto-refresh | There is a refresh-interval selector, **default OFF** |
| Import / export | Export a dashboard as a template file, and import it |
| View / edit mode toggle | One-click switch to prevent accidental edits |
| Copy link | Generates a shareable link |

> **Important: by default, nothing auto-refreshes.** To make a dashboard refresh on a schedule, you must manually open the refresh-interval selector and set it. This is by design, to avoid hammering the database when you're not looking.

> Common misconception: Assuming "the content didn't change" means "the data didn't update". First confirm auto-refresh is on — it defaults to off.

---

## Chapter 8. Screens

**What this step solves:** A dashboard is for "analysis"; a screen is for "display" — the big-font, high-contrast kind on a meeting-room wall or in an ops room. It's another expression of the same data.

![Data screen](docs/images/08-screen.png)

### 8.1 List and two modes

1. Go to **Data Assets → Screens**.
2. The list shows all your screens.
3. Opening one gives you the **full-screen editor / viewer**, suited to throwing onto a big display.

### 8.2 Available widgets

| Widget | Purpose |
| --- | --- |
| Chart | Display a chart |
| KPI card | Highlight one key number |
| Table | Show detail / a list |
| Leaderboard | Ranked display |
| Progress bar | Completion / achievement rate |

### 8.3 How data binding works (key point)

**Every data widget can bind data in one of two ways:**

- **Bind a saved chart**; or
- **Bind a dataset directly**.

The two are **mutually exclusive** — a single widget can only use one at a time. Use the **property panel** on the right to configure the binding.

> How to choose: To reuse a chart you've already tuned, bind the "saved chart"; to swap the data source on the screen on the fly, bind the "dataset" directly.

> Common misconception: You bound a chart and now want to change its metric — you can't. When bound to a chart, the data is determined by that chart; to configure freely, switch to binding a dataset.

---

## Chapter 9. SQL Lab

**What this step solves:** Some questions can't be configured graphically and still need SQL. SQL Lab gives you a clean place to write SQL, and the results can be turned straight into charts.

### 9.1 Running a query

1. Go to **Data Development → SQL Lab**.
2. Pick a database on the left.
3. Write SQL in the editor.
4. Click "Run"; the results table appears below.
5. Rows in the result can be "explored" directly, or "promoted to a chart".

> Safety note: SQL Lab is **read-only by default**. This is both a safety design and one of the preconditions that let AI question-answering run safely.

> Common misconception: Assuming you can modify data in SQL Lab. It's read-only by default, so you can't — to actually write, an admin must enable write capability on the database connection.

---

## Chapter 10. AI Analysis

**What this step solves:** When you don't know SQL and don't want to configure charts, you just want to "ask directly". AI Analysis lets you ask in natural language, and the platform fetches the data, explains, and even generates a chart for you.

![AI Analysis conversation](docs/images/03-ai-chat.png)

### 10.1 How it works (in plain language)

AI Analysis doesn't "guess an answer once" — it works step by step, like an analyst:

![Agent runtime](docs/images/agent-runtime.svg)

**Plan → Execute tools → Observe results → Decide whether it's enough to answer → Produce a conclusion**

- **Plan**: first figure out what to query and how to break it down.
- **Execute tools**: call tools such as data fetching and metric lookup (possibly multiple steps).
- **Observe results**: check whether the returned data is correct and sufficient.
- **Decide whether it's enough**: if enough, conclude; if not, loop back to "Plan" for another round.
- **Produce a conclusion**: give the explanation and the answer.

The whole process **streams the steps live** — you can see what it's thinking and what it queried, rather than waiting for a final answer.

### 10.2 What you get

| Capability | Description |
| --- | --- |
| Explain its SQL | It can talk you through the SQL it wrote, so you can verify |
| Respect metric definitions | It goes through the **semantic layer**'s metric definitions and won't invent its own |
| Generate a chart | It can propose a **chart spec**, which you can confirm and "save as a chart" |

### 10.3 Guardrails (why it's safe to use)

The platform wraps AI in a ring of guardrails so it won't "run away" or exceed its authority:

| Guardrail | Effect |
| --- | --- |
| Max steps | Limits the number of reasoning loops |
| Max tool calls | Limits how many tool calls one question can make |
| Token budget | Limits the usage of one question |
| Run timeout | Stops when time is up, never running forever |
| Read-only SQL guard | Blocks any write operation |
| Column-level visibility | Some columns are invisible to the AI outright |
| Result masking | Sensitive values are masked in results |
| Snapshot + rollback for write tools | When a write is needed it snapshots first, and **can roll back per run** |

> Common misconception: Assuming AI can "conjure" the right answer every time. It relies on the datasets and metric definitions in your platform — **the better you build datasets and definitions, the more accurate it answers**.

---

## Chapter 11. Alerts and Reports

**What this step solves:** You can't stare at a screen all day. When a key metric should have risen but didn't, or a daily/weekly report should have gone out but didn't, you need someone (or the system) to watch and send for you.

Go to **System Settings → Alerts & Reports** — this is **two tabs on one page**:

| Tab | Purpose |
| --- | --- |
| Alert | Monitor metrics by rule and notify when triggered |
| Report | Generate and send reports on a schedule |

Configuration entries and lists are all here; scheduling is executed by the backend's `beat` process via cron (see Chapter 16).

---

## Chapter 12. Users and Roles

**What this step solves:** Who should see what and who should change what must be spelled out. This chapter is where you "hand out keys".

![Users and roles](docs/images/09-access.png)

Go to **System Settings → Users & Roles**. The page has **three tabs**: **Users / Roles / Row-Level Security**.

### 12.1 Users

Create / edit users here and assign roles. (The identity you log in with comes from here.)

### 12.2 Roles and built-in roles

A role is a set of permissions. The platform ships 5 built-in roles:

| Built-in role | Purpose |
| --- | --- |
| 管理员 (Admin) | All permissions |
| 数据分析师 (Data Analyst) | For people who model and analyze |
| 数据查看者 (Data Viewer) | View only, no changes |
| SQL 用户 (SQL User) | For people who write SQL |
| 公开用户 (Public) | Basic public-access role |

**Important rules:**

- Built-in roles **cannot be renamed and cannot be deleted**.
- But their **permission set is still editable**.

> This is often misread: "can't rename" ≠ "can't change permissions". You can adjust what a built-in role can do; you just can't rename or delete it.

### 12.3 Row-Level Security (RLS)

RLS lets you attach **row filters** to "a role + a table". That way the same table shows different rows to different roles.

1. Switch to the "Row-Level Security" tab.
2. Create a rule: choose a role, choose a table, write the row-filter condition.
3. After saving, users in that role see only the filtered rows when querying that table.

> Tip: RLS applies to both direct-connection datasets and imported datasets — because, as Chapter 3 said, they ultimately go through the same query path.

---

## Chapter 13. AI Agent Settings

**What this step solves:** For AI Analysis to be accurate, stable, and compliant, you must first configure "which model, what tools it may call, and where the boundaries are". This chapter is the AI "control console".

![AI agent settings](docs/images/10-ai-settings.png)

Go to **System Settings → AI Agents**. The page has **four tabs**:

| Tab | What it manages |
| --- | --- |
| 基础设置 (Basic Settings) | Provider and model configuration |
| Agent 设置 (Agent Settings) | Which agents exist and what each binds to |
| Skill 设置 (Skill Settings) | Which skills exist |
| 工具设置 (Tool Settings) | Which tools exist |

### 13.1 Basic Settings (Provider / model)

Configure model providers and models here. **API key storage has explicit rules:**

- Once a key is stored, the **API never returns it to you**;
- The API only returns a boolean indicating "is configured".

> This means: after you enter a key, what you see in the UI is "configured", not the key text. That's an intentional security design, not a bug.

### 13.2 Agent Settings

An agent is **bound to a provider** and may optionally **whitelist skills / tools**:

- **Empty whitelist = everything in that category is allowed**;
- Non-empty whitelist = only the listed ones are allowed.

### 13.3 Built-in vs user-created (key difference)

| Object | Editable / deletable |
| --- | --- |
| Built-in agents / skills / tools | **Read-only**, view only |
| Ones you created | Can be edited / deleted |

> Common misconception: You try to edit a built-in agent and find you can't. That's by design — built-ins guarantee a working baseline; to customize, **create a new one** and edit that.

### 13.4 Agent guardrails

Agent guardrails (max steps, max tool calls, token budget, timeout, etc.) are configurable in this chapter. Before adjusting them, think about why you're adjusting — see the guardrail table in Chapter 10.

---

## Chapter 14. Audit Logs

**What this step solves:** When something goes wrong you need to trace it: who changed what, what exactly the AI called, and how much it cost. Audit logs are that "running account".

Go to **System Settings → Audit Logs**. The page has **two tabs**:

| Tab | What it records |
| --- | --- |
| 系统操作 (System Operations) | The various system operations on the platform |
| AI 调用 (AI Calls) | Each AI call (including tool calls, etc.) |

> Tip: The AI-call audit records **masked** values, so it's safe to retain for compliance scenarios.

---

## Chapter 15. System Management

**What this step solves:** Some settings don't belong to a feature module but to "the platform itself" — which metadata DB to connect to, and what the default theme is. Those live here.

Go to **System Settings → System Management**. The page has **two tabs**:

### 15.1 Database Connection Info (metadata DB)

- This manages the **metadata DB (metadata DB)** connection, not your business databases (business DBs are configured under the "Databases" menu).
- You can **view / edit / save / test**.
- **Important: changes here take effect only after the backend restarts.**

> Common misconception: Assuming a change here takes effect immediately. It doesn't — the backend process must be restarted.

### 15.2 Theme Management

| Capability | Description |
| --- | --- |
| Light / dark presets | The platform's built-in theme presets |
| Set as system default / system dark | Make that theme the default for all users |
| Import / export theme files | Themes can be backed up and migrated |

> Keep it straight: The header theme toggle only affects **you**; "Set as system default" affects **everyone's** default.

---

## Chapter 16. Lifecycle Scripts

**What this step solves:** Starting containers, building the backend, migrating the DB, launching three processes, then starting the frontend by hand is easy to get wrong. The repo's `scripts/` folder packages this into one command that's **safe to re-run**.

### 16.1 Script list

| Platform | Start | Stop | Restart |
| --- | --- | --- | --- |
| Windows | `start.ps1` / `start.bat` | `stop.ps1` / `stop.bat` | `restart.ps1` / `restart.bat` |
| Linux / macOS | `start.sh` | `stop.sh` | `restart.sh` |

### 16.2 What the start script does

`start` is **idempotent** — parts already running are skipped automatically, and re-running won't report "port already in use". It does five steps in order:

1. Start containers (PostgreSQL + Redis)
2. Build the backend
3. Migrate the metadata DB
4. Start the backend's three processes: `run` / `worker` / `beat`
5. Start the frontend dev server

### 16.3 Available switches for the start script

| PowerShell | Shell | Effect |
| --- | --- | --- |
| `-SkipInfra` | `--skip-infra` | Skip containers (when using external PG / Redis) |
| `-SkipBuild` | `--skip-build` | Skip building the backend |
| `-SkipFrontend` | `--skip-frontend` | Skip the frontend dev server |

### 16.4 Available switches for the stop script

| PowerShell | Shell | Effect |
| --- | --- | --- |
| `-IncludeInfra` | `--include-infra` | Stop containers as well |
| `-KeepFrontend` | `--keep-frontend` | Keep the frontend dev server |

> Note: By default, `stop` does **not** stop containers — the metadata DB is inside them, they can be started again anytime, and stopping them only makes the next start slower.

> Common misconception: Assuming "stop" means "wipe". The stop script doesn't delete data; to truly start over, see the FAQ ["How do I reset everything"](#faq).

---

## FAQ

**Q1. I forgot my password, what do I do?**
The most direct route: if you have another admin account, use it to reset yours under "Users & Roles → Users". If even the admin password is lost, reset it according to your deployment (a local dev environment can usually rebuild `admin/admin` by re-running the bootstrap flow).

**Q2. I can't see a menu item — what's going on?**
Most likely permissions. As Chapter 1 said, what the sidebar shows is determined by role permissions. Ask an admin to check your role's permission set under "Users & Roles → Roles".

**Q3. The data in a dataset doesn't match the source / isn't updating — what do I do?**
First distinguish the dataset type: a **direct** dataset follows the source DB, so what you query is the source's current value; **file-import / API-pull** data is materialised at import time and won't update on its own — you need to **re-import / re-pull**.

**Q4. I have a very old `.xls` file — can I import it?**
Yes. The platform supports both modern `.xlsx` and legacy `.xls` (detected via the OLE2 magic number), so you don't need to convert first. See Section 3.3.

**Q5. Why is my chart empty?**
Check in order: ① did you select a metric; ② is the time range / filter filtering out all the data; ③ are there any rows in the bottom data preview? No rows at the bottom = the query returned nothing; rows at the bottom but an empty chart = a chart configuration issue.

**Q6. The backend restarted, but the page still shows 404 / errors?**
A backend restart briefly interrupts service. Confirm the backend is up (visit `http://localhost:8088/health`); then confirm the frontend dev server is still alive on port `3000`. The frontend proxies `/api` to `8088`, so both sides must be up.

**Q7. Where do the AI API keys I configure live? Will they be visible?**
They live in the platform's config (AI Agents → Basic Settings). The **API never returns the key text**, only a boolean for "is configured". This is an intentional security design.

**Q8. How do I completely reset the environment (start over)?**
The idea is to clear the state and bring it back up: stop all processes (the `stop` script, with `--include-infra` if needed to stop containers) → handle the metadata DB (rebuild / clear) → use the `start` script to run start + migrate + bootstrap again. Exact steps depend on your deployment; **always back up** before doing this.

**Q9. Why doesn't my dashboard auto-refresh?**
Because **auto-refresh defaults to OFF**. Turn on a refresh interval manually on the dashboard — see Section 7.3.

**Q10. Can a screen widget bind both a chart and a dataset?**
No. The two are **mutually exclusive**; one widget can only use one at a time, configured via the property panel — see Section 8.3.

---

## Appendix

### A. Default Roles and Permissions Quick Reference

| Role | Purpose | Renamable | Deletable | Permissions editable |
| --- | --- | --- | --- | --- |
| 管理员 (Admin) | All permissions | No | No | Yes |
| 数据分析师 (Data Analyst) | Analysis and modeling | No | No | Yes |
| 数据查看者 (Data Viewer) | View only | No | No | Yes |
| SQL 用户 (SQL User) | Write SQL | No | No | Yes |
| 公开用户 (Public) | Basic public access | No | No | Yes |

### B. Ports and Addresses

| Purpose | Address |
| --- | --- |
| Frontend UI | `http://localhost:3000` |
| Backend API | `http://localhost:8088` |
| Backend health check | `http://localhost:8088/health` |
| Default login | `admin` / `admin` |

### C. URL Quick Reference (Common Endpoints)

> These are common backend endpoints for deployment and troubleshooting; use them only when diagnosing. For daily operation, use the UI.

| Purpose | Method and path |
| --- | --- |
| Health check | `GET /health` |
| Log in for a token | `POST /api/v1/security/login` |
| Gallery / chart data | `POST /api/v1/chart/data` |
| Async SQL query | `POST /api/v1/sqllab/` |

### D. Shortcut Notes

The two most-used global toggles are in the header: **language (中文 / English)** and **theme (light / dark)**. The current version provides no keyboard shortcuts; perform actions through the UI menus.

### E. Related Resources

- Repository: <https://github.com/jeokeo011222/Insight-knowledge-BI>
- Architecture: `docs/images/architecture.svg`
- Agent runtime: `docs/images/agent-runtime.svg`
- Three ingestion routes: `docs/images/data-ingestion.svg`
