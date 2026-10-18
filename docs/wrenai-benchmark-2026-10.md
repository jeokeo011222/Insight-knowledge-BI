# WrenAI 对标研究：把「上下文层」装进智观 BI

> 研究对象：`D:\Code\WrenAI-main`（wrenai 0.16.0，Apache-2.0，2026-10 快照）
> 规模：Python 56,367 行 / Rust 31,890 行 / 85 篇文档 / 105 个测试文件
> 对照对象：本仓库 `backend/internal/ai`（7,557 行）、`backend/internal/queries`、`frontend/src/pages/datasets`
> 方法：读源码 + 读概念文档 + 逐项对照我们的实现（所有结论都可按文中路径复核）

---

## 0. 结论摘要（先看这十条）

1. **WrenAI 这一版已经不是「聊天式 BI」**：仓库里没有前端、没有 LLM 调用，`wren ask` 只负责把问题包成 prompt（`core/wren/src/wren/ask.py`）。它把自己降级成 **agent 的上下文层 + 受治理的 SQL 执行面**，通过 CLI / MCP / Skills 被 Claude Code、Cursor 这类外部 agent 调用。
2. **它的准确率不是模型给的，是六个原语协同给的**：语义契约（MDL）、定向检索、规划预览（dry-plan）、策略守卫、结构化报错重试、评测。单点 prompt 优化在它的设计里几乎不存在。
3. **我们最缺的不是 Agent 循环**。我们的 plan→act→observe、错误回传、重复调用检测、预算护栏、回滚都已经建成（`agent_runtime.go`），真正缺的是**被 agent 消费的上下文资产**：模型、关系、计算字段、业务规则、枚举/单位、历史成功示例。
4. **我们有三个「有表无实现」的空壳**，比「没有」更危险，因为它们制造能力假象：
   - `semantic_layers` / `semantic_views`：模型已迁移（`models/semantic.go`），除 `semantic_reader` 外无人读写；
   - `ai_meta_embedding`：建了 `vector(1536)` 列（`models/ai.go:162`），全仓库没有任何写入/检索代码；
   - `semantic_reader` 工具：永远读一张空表，却出现在工具清单里。
5. **两个真实的安全缺口**（不是体验问题）：
   - **列脱敏只在图表路径生效**：`api/charts.go:730` 调 `applyMasking`，而 Agent 走的 `queries/readonly.go:43 ExecuteReadOnlySQL` 完全不脱敏 → 问数会把图表里打了码的手机号/身份证原样吐出来；
   - **SQL 守卫是正则黑名单**（`ai/sql_safety.go:24`），且执行连接不是只读会话（`queries/engine.go:533`）→ 跨表读、`pg_read_file`、`dblink`、`FOR UPDATE` 这类风险拦不住。
6. **我们真正的优势是闭环**：图表/看板/大屏、调度告警、审计、写操作快照与回滚，WrenAI 全都没有。正确路线是「用 Wren 的上下文层强化我们的闭环」，不是转去做 CLI 基础设施。
7. **不要复刻它的重工程**：56k 行 Python CLI + 32k 行 Rust 引擎 + DataFusion + 22+ 数据源适配。我们用 Postgres + 现有 `queries/sql_builder.go` + 一个轻量语义模型，能拿到 80% 的收益。
8. **投入产出比最高的单点是「NL→SQL 记忆」**：一张表、一次 top-3 召回、一个「保存为范例」按钮。重复问题会随使用变准，这是 Wren 全篇最值得抄的一件事（`memory/store.py`）。
9. **评测必须换代**：我们现在是「SQL 文本正则断言」（`docs/eval_nl2sql.py`），Wren 是「20 题 + A/B 工作流 + 评分 rubric + agent 无关」（`evals/spodbtify_ab/`）。不升级评测，后面所有优化都无法证明有效。
10. **产品话术要跟着变**：从「能聊天」变成「口径唯一、过程可追溯、结果可复现」。Wren 的文档（`docs/core/concepts/correctness.md`）本身就是最好的销售材料模板。

---

## 1. WrenAI 全景：它到底在卖什么

### 1.1 定位

README 第一句：*"open-source GenBI for AI agents"*。它不替代数仓、不替代 ETL、不替代 BI 工具，只做**数仓与 agent 之间的一层**：把「表结构」翻译成「业务含义 + 使用规则 + 历史经验」，并把 agent 写的「语义 SQL」规划成可执行 SQL。

```
Agent（Claude Code / Cursor / 自研）
        │  问问题、取上下文、存经验
        ▼
Wren：MDL · Memory · Skills · 受治理执行
        │  planned, governed SQL
        ▼
数仓（Postgres/BigQuery/Snowflake/DuckDB…）
```

### 1.2 仓库结构（这一版没有 UI）

| 目录 | 作用 | 关键文件 |
|---|---|---|
| `core/wren` | CLI + Python SDK（56k 行） | `context.py`(2,213) / `memory/*` / `mdl/cte_rewriter.py`(1,145) / `policy.py`(560) / `skills_content/*` |
| `core/wren-core*` | Rust 语义引擎（DataFusion） | 31,890 行，负责 MDL → 物理 SQL 展开 |
| `core/wren-mdl` | MDL 的 Rust 模型定义 | — |
| `docs/core` | 85 篇概念/参考文档 | `concepts/correctness.md`、`concepts/memory_system.md`、`reference/mdl.md` |
| `skills` | 只装一个 ~50 行的发现桩 | 真正的技能内容放在 CLI 里按需拉取 |
| `sdk` | `wren-langchain` / `wren-pydantic` | 给外部 agent 框架接入 |
| `evals` | Agent 无关的 A/B 评测 | `spodbtify_ab/`（20 题 + 评分 rubric） |

**关键信号**：`core/wren` 里搜不到 openai/anthropic/litellm 任何调用。它把「谁来做推理」完全交给外部 agent，自己只提供**确定性原语**。这是一个非常克制的产品选择，也是它能同时服务 50+ agent 的原因。

### 1.3 一次问数的真实路径（`skills_content/usage/SKILL.md`）

```
1. memory recall  -q "<问题>"      → 相似的历史 NL→SQL（top 3）
2. memory fetch   -q "<关键词>"    → 相关的模型/字段/关系/规则（不是全量 schema）
3. 用模型名写 SQL（不是物理表名）
4. dry-plan                        → 展开成真正会执行的 SQL，先验证
5. query                           → 执行
6. 失败则分层修复（MDL 层 / DB 层）
7. memory store                    → 把确认过的问答对写回
```

这条链路里，**每一步都是模型可以调用的显式原语**，不是藏在产品里的魔法；agent 的推理轨迹里能看到完整证据。

---

## 2. 六个正确性支柱（Wren 的自述，也是我们的检查表）

`docs/core/reference/architecture.md` 把「不幻觉」拆成六件事。逐条对照我们：

| 支柱 | Wren 的实现 | 我们的现状 | 差距 |
|---|---|---|---|
| **Schema linking** | MDL + `memory fetch` 定向召回 | `schema_reader` 全量 dump 一个数据集；`meta_search` 是 `LIKE` 关键词（`tools_read_extra.go:162`） | 大 |
| **Value profiling** | enrich 工作流采样 `LIMIT 30`，≤30 个不同值判为枚举候选（`enrich-context/SKILL.md:173`） | 列描述自由文本，无枚举/单位/哨兵值结构化字段 | 大 |
| **Ambiguity detection** | 技能强制「先问清楚再写 SQL」，由外部 agent 执行追问 | `nl2sql` 会返回 `missing` 字段，但运行时没有「暂停并提问」的能力，模型通常硬答 | 中 |
| **Generation trace** | `dry-plan` 输出展开后的 SQL；错误带 `phase`/`available columns` | 只回显最终 SQL；`EXPLAIN` 只做代价拦截 | 中 |
| **Retry & repair** | 错误按 MDL 层 / DB 层分流，一次只修一个 | 工具错误会回传模型 + 空结果 self-check（`agent_runtime.go:295`/`596`） | 中 |
| **Eval** | 20 题、A/B 工作流、rubric、agent 无关 | 9 类 SQL 文本断言 + dry/live 两模式 | 大 |

---

## 3. 八个机制拆解（带源码证据）

### 3.1 MDL：可审查的语义契约

MDL 不是「表结构缓存」，而是**声明式契约**，四类对象：

```yaml
# models/orders/metadata.yml
name: orders
table_reference: {catalog: jaffle_shop, schema: main, table: orders}
primary_key: order_id
columns:
  - name: amount
    type: DECIMAL
    properties: {description: "含税金额，USD"}
  - name: net_amount
    is_calculated: true
    expression: "amount * 1.1"
  - name: customer
    type: customers           # 关系句柄
    relationship: orders_customers

# relationships.yml
relationships:
  - name: orders_customers
    models: [orders, customers]
    join_type: MANY_TO_ONE
    condition: orders.customer_id = customers.customer_id

# cubes/revenue/metadata.yml
name: revenue
base_object: orders
measures:
  - {name: total, expression: SUM(amount), type: DOUBLE}
dimensions:
  - {name: status, expression: status, type: VARCHAR}
time_dimensions:
  - {name: order_date, expression: order_date, type: DATE}
hierarchies: {time: [order_date]}
```

关键设计（`docs/core/reference/mdl.md`）：

- **未暴露的列不存在**。模型里没写的列，agent 在任何 SQL 里都引用不到——这是「列级访问控制」最干净的实现方式（比结果脱敏更彻底）。
- **关系是白名单**。JOIN 路径只有声明过的，agent 不能自己发明。
- **计算字段/指标只定义一次**，所有查询复用。
- **Cube 是结构化聚合对象**，查询走 `wren cube query --measures ... --dimensions ...`，**不写 GROUP BY**（`cube_cli.py`），官方说法是「对弱模型尤其能降错误率」。

> 对照我们：`table_columns` 有 `expression`（计算列雏形）、`sql_metrics` 有表达式，但**没有模型、没有关系、没有业务规则、没有隐藏列**。指标字典（`ai_metric`）只进提示词，不约束执行，所以用户觉得「没啥用」是合理的。

### 3.2 dry-plan：先展开、再执行

`engine.py:97` 的流程：

```
用户 SQL（目标方言）
 → sqlglot 解析
 → qualify_tables / normalize_identifiers / qualify_columns
 → 识别引用的 model 与 column
 → 每个 model 用 wren-core 展开成 Wren 方言 SQL
 → 解析回目标方言，作为 CTE 注入
 → 输出最终 SQL
```

三个收益：

1. **错误发生在 DB 之前**：列名写错时直接返回 `Available columns: [...]`（`mdl/cte_rewriter.py:756`），而不是等数据库报 `column does not exist`；
2. **可审查**：agent 与用户都能看到「你写的模型 SQL 最终变成了什么」；
3. **可 dry-run**：`dry_run()` 只校验不取数（`engine.py:124`），适合写操作前的预检。

> 对照我们：`ExecuteReadOnlySQL` 有 `EXPLAIN` 行数代价闸门（`readonly.go`，很聪明的设计），但没有语义展开，也没有「可用列」提示；模型只能靠自我纠正。

### 3.3 SQL 策略守卫：AST + 严格模式

`policy.py` 三层：

| 层 | 规则 | 我们的对应 |
|---|---|---|
| **只读（永远开）** | 只接受 SELECT 家族 AST；拒绝 DDL/DML/多语句 | 正则黑名单（可绕：`FOR UPDATE`、`setval()`、`pg_read_file`） |
| **严格模式（可选）** | FROM/JOIN 里出现的表必须来自 MDL；数据读取函数（`read_csv`/`dblink`/`postgres_scan`/`iceberg_scan`…）在**任意 AST 位置**一律拒绝 | 无 |
| **函数黑名单** | 按方言把函数名 canonicalize 后比对，避免被 `Anonymous` 绕过 | 无 |

两个工程细节值得抄：

- 数据读取函数不是只查 FROM 位置，而是**遍历整棵 AST**（防止 `SELECT read_csv('/etc/passwd')` 藏在投影里）；
- 对 `allowed_source_functions` 做成显式 opt-in，默认拒绝生成器函数（防 DoS）。

> 对照我们最致命的一点：`ExecuteReadOnlySQL` 只校验「数据集属于这个库、用户有权限」，**不校验 SQL 里查的是不是这个数据集**。agent 完全可以 `SELECT * FROM 同库其他表`。再加上连接不是只读会话，实际安全边界只有正则。

### 3.4 Memory：可重建的检索层，文件是真理

`memory/` 的结构：

| 内容 | 存储 | 检索 |
|---|---|---|
| Schema 条目（model/column/relationship/view/cube/measure/dimension） | MDL 编译产物 | LanceDB 向量；无扩展时 grep |
| 业务规则 `knowledge/rules/*.md` | Markdown（提交到 git） | 同上，按 `##` 切块 |
| NL→SQL 确认对 `knowledge/sql/*.md` | Markdown + YAML frontmatter | 语义召回 top-3，可按 datasource 过滤；grep 后端退化为词重叠 |

三个非常聪明的取舍：

1. **索引是派生物**：`.wren/memory/` 被 gitignore，可随时从 markdown 重建；团队协作面是 `knowledge/`，不是二进制索引。
2. **小 schema 直接全量，大 schema 才检索**：`SCHEMA_DESCRIBE_THRESHOLD = 30_000` 字符（`memory/schema_indexer.py:70`）。小项目不做 embedding，省钱且没有召回损失。
3. **写回有启发式**：`sql_classify.py:is_exploratory()` 判断「裸 SELECT 无 WHERE/GROUP/聚合」的探索性查询，不当作值得记忆的范例。

> 对照我们：没有范例库。`ai_meta_embedding` 表建了但没人用。`meta_search` 的注释老实承认「keyword scan (no pgvector dependency)」，而工具描述却写着 pgvector——描述与实现不一致，模型会高估这个工具。

### 3.5 Skills：渐进披露 + 决策树 + 错误分层

Wren 的技能不是「工具函数」，而是**给 agent 读的工作流文档**：

- 只安装一个 ~50 行的发现桩，正文由 `wren skills get <name>` 按需拉取，内容跟随 CLI 版本，避免文档漂移；
- `usage` 技能里有完整的「命令决策树」和「Step 2.5 复杂度评估」：先判断这题是单步查数还是多步分析，再决定要不要拆子问题；
- 错误恢复分两层：`dry-plan` 失败 → MDL/语义层问题（补关系、改列名）；`dry-plan` 成功但执行失败 → 方言/权限/数据问题。**一次只修一个问题，每修一次重跑 dry-plan**。

> 对照我们：10 个 skill 都是「带 schema 的一段 prompt + 一个 executor」，没有工作流文档、没有复杂度分诊、没有错误分层。模型拿到的是函数签名，不是「先做什么、后做什么、什么情况停下来问」。

### 3.6 上下文增强：一份 10 类缺口清单

`enrich-context/SKILL.md` + `references/gap_catalog.md` 是整份研究里**最能直接产品化**的东西。它把「schema 表达不了、但 agent 必须知道」的东西分成 10 类：

1. 枚举值语义（`status=4` 是什么）
2. 单位/量纲（分 vs 元、毫秒 vs 秒、bp）
3. NULL 语义（未登录 vs 无过期）
4. 软删除/默认过滤
5. 魔法哨兵（-1=未知、9999=永久）
6. 业务同义词（"客户"→customers，不是 customers_v3）
7. 时间约定（UTC、事件时间 vs 写入时间、自然月 vs 财月）
8. 跨系统标识（stripe_customer_id）
9. 币种/地区
10. 规范表选择（lookalike 表里选哪张）

每种都有：**触发条件、写入位置（列描述 / knowledge/rules）、标签格式（`[enum]`/`[unit]`/`[null]`…）、为什么重要**。工作方式有两种：

- **grill**：一次只问一个问题，先给草稿再让用户确认；
- **auto-pilot**：自己推断并落盘，只在「与现有定义冲突」和「高影响面变更（新 cube/view/relationship）」时打断。

三条硬规则尤其值得学：**只增不改**、**每次改完必须 validate、失败回滚这一处**、**每个推断都要标 confidence 和来源**。

### 3.7 互操作：OSI / dbt / MCP / SDK

- **OSI 导入**：`osi.py` 把 Open Semantic Interchange 的 YAML 转成 MDL（`wren context build --from-osi`），语义模型可跨工具迁移；
- **dbt 导入**：`dbt.py` 读 `manifest.json`/`catalog.json`/`run_results.json`，把 dbt 模型 + 测试状态映射进 MDL；
- **MCP server**：`mcp_server.py` 暴露 `run_sql` / `dry_run` / `dry_plan` / `query_cube` / `get_mdl` / `list_models` / `describe_model` / `list_cubes` / `describe_cube`，任何 MCP 客户端可直接用；
- **SDK**：`wren-langchain`、`wren-pydantic`。

> 对照我们：没有互操作层。我们的元数据只服务自家 Agent。

### 3.8 评测：agent 无关的 A/B

`evals/spodbtify_ab/`：

- 20 道真实分析题（"top 5 artists by playlist count…"）；
- 同一批题跑两种工作流（`dbt_integrated` vs 基线），对比「建语义层」到底带来多少提升；
- `scoring` rubric + `agent_output.schema.json`（结构化输出）；
- `run_eval.py validate|prompt|run|summarize`，可接任意 agent 命令模板。

> 对照我们：`docs/eval_nl2sql.py` 的断言是 `groupby_contains` / `metric_keywords` / `forbid` 这类**文本特征**，无法回答「结果对不对」「两次是否一致」「成本多少」。

---

## 4. 我们的现状盘点（先承认，再改进）

### 4.1 已经很强、不要推倒的部分

| 能力 | 证据 | 说明 |
|---|---|---|
| Agent 运行时 | `agent_runtime.go` | plan/act/observe、SSE、步数/token/超时护栏、重复调用检测、工具错误回传、空结果 self-check、断流 resume |
| 写操作安全 | `snapshot.go` + 写工具 | 写前快照、按 run 回滚，业界少见 |
| 治理执行 | `queries/engine.go` | 数据集级授权 + RLS 注入 + 行数上限 + 审计 + EXPLAIN 代价闸门 |
| 产品闭环 | 图表/看板/大屏/调度/告警 | WrenAI 完全没有 |
| 多引擎 | 67 个引擎（26 个真实驱动） | Wren 22+ |
| 会话 | `session_id` + 摘要 | 已有，但只带 3 轮结论摘要 |

### 4.2 三个「有表无实现」的空壳（重点）

| 空壳 | 现状 | 风险 |
|---|---|---|
| `semantic_layers` / `semantic_views` | `models/semantic.go` 完整映射 Superset 6.x 两张表；除 `skills_domain.go:20 semanticReader` 外无任何读写、无 API、无 UI、无引擎 | agent 调 `semantic_reader` 永远拿到空数组，会误判「这个库没有语义层」 |
| `ai_meta_embedding` | `models/ai.go:162`，`vector(1536)`，register 里最后迁移；全仓库无写入/检索 | 工具描述承诺 pgvector，实际是 LIKE |
| `metric_dictionary` 工具 + `ai_metric` 表 | 页面已下线，后端仍注入 prompt | 没有维护入口，数据只会腐烂 |

**建议**：短期先让 `semantic_reader` 明确返回「未配置」，或在工具清单里摘掉；中期用真正的语义模型替代，而不是留着空壳撑门面。

### 4.3 两个安全缺口（需要在下一迭代修）

**（1）列脱敏不覆盖 Agent**

```
图表路径：charts.go:730  → applyMasking()  → 非管理员看到 ***
Agent 路径：tools_read.go:84 → ExecuteReadOnlySQL() → 原样返回
```

后果：数据集里给 `phone` 配了 `phone` 脱敏，图表里是 `138****0000`，问数里 agent 能看到 `13800000000`，还会把它写进回答和 `ai_tool_call_log`。

**（2）SQL 守卫是正则 + 连接非只读**

```go
// ai/sql_safety.go:24
var sqlKeywordBlocklist = regexp.MustCompile(`(?i)\b(insert|update|...)\b`)
```

能拦常见写关键字，但拦不住：

- 跨数据集/跨 schema 读（`SELECT * FROM other_table`）；
- 文件/网络读取函数（`pg_read_file`、`read_csv`、`dblink`）；
- 锁与副作用函数（`FOR UPDATE`、`setval()`、`pg_sleep()`）；
- 只读账户缺失时，任何漏网之鱼都是持久化风险。

### 4.4 差距矩阵

| 能力 | WrenAI | 智观 BI | 差距等级 |
|---|---|---|---|
| 语义模型（模型/字段/关系/视图/Cube） | 完整 MDL + 引擎 | 仅 dataset + columns + metrics | **大** |
| 计算字段复用 | 模型级 expression | 列级 expression，无复用 | 中 |
| JOIN 白名单 | relationships.yml | 无 | **大** |
| 业务规则（默认过滤/同义词/币种/时区） | knowledge/rules + 检索 | 无 | **大** |
| 枚举/单位/NULL 语义 | 结构化标签 + 缺口清单 | 自由文本描述 | **大** |
| 定向上下文检索 | 向量 + 30k 阈值 + 类型过滤 | 全量 dump + LIKE | **大** |
| NL→SQL 范例记忆 | knowledge/sql + 召回/写回 | 无 | **大** |
| 语义 SQL 规划 | dry-plan + CTE 展开 | 无 | **大** |
| SQL 守卫 | AST + 表白名单 + 函数黑名单 | 正则黑名单 | **大** |
| 列级治理 | 未暴露列不存在（CLAC） | 仅图表结果脱敏 | **大** |
| 结构化报错/修复 | error_code + phase + available columns | 原始错误 + 通用 hint | 中 |
| 工作流技能 | Markdown + 渐进披露 + 错误分层 | 函数式 skill + 单段 prompt | 中 |
| 评测 | 结果级/A-B/rubric | SQL 文本断言 | **大** |
| 互操作 | OSI/dbt/MCP/SDK | 无 | 中（战略） |

---

## 5. 十条优化建议（按投入产出比排序）

> 命名：W-01 ~ W-10。每条都给出「现状 → Wren 依据 → 方案 → 验收 → 成本」。成本按 1 人 1 天 = 1d 估。
>
> **实施状态（2026-10-18）**：W-01 ~ W-09 全部落地并通过自查（`go build`/`go vet`/`go test ./...`/`tsc --noEmit`/`vite build` 全绿），逐项证据见 `docs/verification.md` 第 28 节（P0）与第 29 节（W-05~W-08）；仅 W-10（OSI 导入 / MCP 暴露）未开始，原因见 §10。

### P0：两周内应该做完（安全与上下文地基）

#### W-01 统一字段暴露策略：hidden / masked 贯穿「提示词 → SQL → 结果」

**现状**：`mask_type` 只在图表结果层生效（`api/masking.go:161`）；`schema_reader` 把所有 `is_active` 列都交给 agent（`tools_read.go:145`），包括手机号/身份证列；Agent 取数不脱敏。

**Wren 依据**：MDL 里没声明的列在引擎里**不存在**（`reference/mdl.md`「Selective exposure for column-level access control」）；`is_hidden` 会从符号表里剥离。比结果脱敏更彻底，因为 agent 连列名都看不到，也不会把它写进 SQL。

**方案**：

1. `table_columns` 增 `agent_visible`（默认 true）与 `pii_level`（none/low/high），复用现有 `mask_type`；
2. `schema_reader`：`agent_visible=false` 直接不返回；`mask_type<>''` 的列返回 `{"masked": true, "note": "值将被脱敏，仅可用于计数/分组，不可用于明细"}`；
3. `ExecuteReadOnlySQL`：返回前按数据集 mask 规则脱敏（把 `maskRulesFor/applyMasking` 下沉到 `queries` 层，API 与 AI 共用一份）；
4. AST 守卫（W-02）拒绝引用 `agent_visible=false` 的列；
5. UI：数据集列编辑弹窗把「脱敏」升级成「Agent 可见性 + 脱敏规则」，并在数据集详情页显示「对 Agent 隐藏 N 列」。

**验收**：给 `birth_names` 的 `name` 配 `name` 脱敏 → 图表与问数输出一致为 `张*`；`SELECT name FROM ...` 被守卫拒绝（若设为 hidden）；`ai_tool_call_log` 中不出现原始值。

**成本**：3–4d。

---

#### W-02 SQL 守卫升级：AST + 只读会话 + 表/列白名单 + 结构化错误

**现状**：正则黑名单；`ExecuteReadOnlySQL` 不限制表；连接是非只读会话（`openPool` 直接 `connectors.Open`）。

**Wren 依据**：`policy.py` 三层守卫；`cte_rewriter.py:756` 的 `Available columns: [...]` 报错。

**方案**（渐进式，不追求一次支持 67 种方言）：

1. **兜底优先**：为 Agent 单独开只读连接池 —— Postgres `BEGIN READ ONLY` / MySQL `SET SESSION TRANSACTION READ ONLY` / 其他引擎尽量 `read-only` 连接参数；这是零解析成本的真防护；
2. **AST 层**：优先接入 `pg_query_go`（Postgres）与 `vitess sqlparser`（MySQL）；其余方言先用「词法扫描 + 表名提取」的白名单校验，不支持的方言走保守模式（只允许单条 SELECT + 禁止函数清单）；
3. **表白名单**：只允许当前 dataset 的物理表（含 schema）、CTE 名、以及 dataset 上声明的关联表（W-05 之后）；
4. **函数黑名单**：`pg_read_file/pg_ls_dir/lo_import/dblink/read_csv/read_parquet/glob/setval/pg_sleep` 等，按方言维护；
5. **结构化错误**：`{code:"COLUMN_NOT_FOUND", phase:"sql_parse", message, available_columns:[...], hint}`，随 tool observation 回传模型（我们已具备回传通道）。模型一次就能改对，而不是瞎猜。

**验收**：20 条对抗用例（多语句、CTE 内 DML、`pg_read_file`、`dblink`、跨数据集表、`FOR UPDATE`、系统表）全部拒绝；正常问数 p95 开销 < 5ms；错误回传后模型第二次成功率 ≥ 80%。

**成本**：5–7d（含方言适配与用例）。

---

#### W-03 NL→SQL 记忆库：让重复问题越问越准

**现状**：没有任何范例库；`ai_chat_history` 只存对话，不存「问题→可用 SQL」。

**Wren 依据**：`memory/store.py:381 store_query` + `:414 recall_queries`；`knowledge/sql/*.md` 是真理，索引可重建；`sql_classify.py:is_exploratory` 过滤探索性查询。

**方案**：

1. 新表 `ai_sql_memory`：

```sql
id, dataset_id, model_id NULL, nl_query, sql_query, spec_json NULL,
source('seed'|'confirmed'|'auto'), quality smallint, run_id, created_by,
embedding vector(1536), created_on
```

2. **写回时机**（三档，先做前两档）：
   - 用户点「保存为图表」→ 保存该次 `generated_sql`；
   - 用户点「这个回答有用」→ 保存；
   - 自动：`rowcount>0` + 无错误 + 命中 approved 指标 + 非探索性查询 → `source='auto' && quality=1`，进入待审队列；
3. **召回**：planner 在 system prompt 后注入 `top-3` 相似范例（`dataset_id` 过滤 + 相似度阈值），格式：`问题 → SQL → 一句话口径`；
4. **去重/衰减**：`nl_query` 归一化后 hash 去重；90 天未命中的 auto 范例降权；
5. **导出**：`GET /ai/sql-memory/export` 输出 markdown，便于进 git / 跨环境迁移（学 Wren 的 `knowledge/sql`）。

**验收**：20 题里 8 题是同义改写，召回命中 ≥ 6；A/B（有/无记忆）SQL 执行正确率提升 ≥ 10pct；存储内容通过 W-01/W-02 的策略检查，无 PII 原文。

**成本**：5d + 0.5d 前端按钮。

---

#### W-04 上下文检索：用「定向切片」替代「全量 schema dump」

**现状**：`nl2sql` 把整个数据集 schema 序列化成 JSON 塞进 prompt（`skills_read.go:171`）；宽表（100+ 列）直接吃掉几千 token，且引入无关列干扰；`meta_search` 只是 `LIKE`。

**Wren 依据**：`schema_indexer.describe_schema()` 生成紧凑文本；`SCHEMA_DESCRIBE_THRESHOLD=30_000` 以下全量、以上向量检索 top-k；检索条目带 `item_type`/`model_name` 过滤。

**方案**：

1. 写一个 `describeDatasetCompact()`：`表名（说明）` → `列名 类型 — 描述 [枚举/单位/NULL]`，比 JSON 省 40–60% token；
2. 阈值：≤ 8k 字符全量注入；超过则 `ai_meta_embedding` top-k（k=8）+ 命中的 metric/rule；
3. 把 `ai_meta_embedding` 真正用起来：数据集保存/刷新时增量重建（`kind=table|column|metric|rule`，`text` 为紧凑描述）；
4. `meta_search` 工具描述改成「向量召回，缺 embedding 时退化为关键词」，避免模型误判能力。

**验收**：100 列宽表 prompt token 下降 ≥ 40%；20 题正确率不降；`meta_search` 在「同义改写」问题上命中率从 0 提升到 ≥ 60%。

**成本**：4–5d（复用 Postgres pgvector，无需新组件）。

---

#### W-09a 先测基线（评测换代的第一半，1d）

在改任何东西之前，把当前 20 题跑一遍，记录：生成 SQL、是否执行成功、结果行数、token、耗时、两次是否一致。没有基线，后面所有「提升」都是主观感受。详见 W-09。

---

### P1：一个月内（语义层与治理工作台）

#### W-05 语义模型（MDL-lite）：让「口径」变成可执行契约

**现状**：只有 dataset + columns + metrics；没有模型名、关系、业务规则；`semantic_layers` 是空壳。

**Wren 依据**：MDL 的 models/relationships/cubes；「未声明即不存在」。

**方案**（不引入跨方言 SQL 引擎，先做「数据集内语义模型」）：

1. 新表（或复用 `semantic_views.configuration`，推荐新表更清晰）：

```
semantic_models(id, dataset_id, name, description, primary_key, default_filters jsonb, status, version)
semantic_fields(id, model_id, name, kind[column|metric|dimension|time], expression, type,
                description, unit, enum_values jsonb, synonyms jsonb, hidden bool, pii_level)
semantic_relationships(id, from_model_id, to_model_id, join_type, condition)
semantic_rules(id, scope[global|dataset|model], category, body, status, version)
```

2. **冷启动**：从现有 dataset 自动生成一版模型（列 → fields；`sql_metrics` → metric fields；`expression` 非空 → calculated），人工只需补描述/枚举/关系；
3. **校验**：编译成 `manifest.json`（含 hash），校验悬空引用、重复名、无主键的多对多关系；失败不允许发布；
4. **UI**：放在数据集详情新增「语义模型」页签，而不是独立菜单页（避免重蹈「指标字典」孤岛）；
5. **导出/导入**：JSON/YAML，便于 git 与跨环境。

**验收**：一个数据集能声明 1 个模型 + 3 个指标 + 2 个关系；manifest 校验能拦住「关系引用不存在的列」；Agent 能用模型名而不是物理表名（W-06）。

**成本**：8–10d（含 UI）。

---

#### W-06 语义查询接口：能不用 SQL 就不用 SQL

**现状**：Agent 只能写自由 SQL；`query_executor` 是唯一取数通道。

**Wren 依据**：`wren cube query --measures --dimensions --time-dimension name:grain`，结构化查询，不写 GROUP BY；官方明确「对弱模型更稳」。

**方案**：

1. 新工具 `semantic_query`：

```json
{
  "model": "orders",
  "metrics": ["net_revenue", "order_count"],
  "dimensions": ["channel"],
  "time": {"field": "order_date", "range": "last_30_days", "grain": "day"},
  "filters": [{"field": "status", "op": "not_in", "value": ["refunded"]}],
  "limit": 500
}
```

2. 后端用现有 `queries/sql_builder.go` 生成 SQL（它已处理方言、分组、过滤、RLS），并自动套用 `semantic_rules` 的默认过滤；
3. 返回值附带 `generated_sql`，与图表查询共用一条路径，保证「图表能出的数，问数也能出」；
4. 复杂 SQL 仍走 `query_executor`（受 W-02 守卫），形成「确定性优先、自由 SQL 兜底」。

**验收**：20 题里 ≥ 14 题可完全用 `semantic_query` 表达；同一问题两次生成 SQL 逐字符一致；token 下降 ≥ 50%；准确率不低于自由 SQL 基线。

**成本**：6–8d（复用现有 builder，主要是映射与测试）。

---

#### W-07 口径治理工作台：把「缺口清单」产品化

**现状**：数据集详情可编辑列描述、指标表达式（`DatasetColumnModal` / `DatasetMetricModal`），但**没有任何引导**：不知道缺什么、先补哪个、补了有什么收益。刚下线的指标字典页就是「一个空表格」，所以用户觉得没用。

**Wren 依据**：`enrich-context` 的 10 类缺口 + grill/auto-pilot + 「只增不改、每改必校验、推断标 confidence」。

**方案**：

1. **缺口扫描器**（后端定时 + 手动触发）：按 10 类规则给每个数据集打分：
   - 无描述的列占比；
   - 低基数（≤30 不同值）但无 `[enum]` 的列；
   - `*_amount/_price/_cost` 无单位的列；
   - 有 `deleted_at/is_deleted` 但无默认过滤规则的表；
   - 金额列但无币种规则；
   - 时间列无时区/事件时间说明；
   - 名称相似的表（`orders` vs `orders_v2`）无 canonical 规则；
   - 指标无 `approved`。
2. **逐题确认 UI**：一次只问一个缺口，给出**草稿**（从列名/采样值推断）+「采纳/改写/跳过」；采纳即写入 `description` 或 `semantic_rules`；
3. **采样需授权**：枚举候选用 `LIMIT 30` 采样，第一次运行前明确询问（学 Wren 的 grill 模式）；
4. **审计**：每次写回记录 who/when/why（证据来源：采样值/文档/人工），支持 diff 与回滚；
5. **覆盖率看板**：`已描述列 % / 已标枚举 % / 有规则的表 %`，作为「上下文健康度」指标。

**验收**：一个 30 列数据集从 0 到 90% 覆盖 ≤ 30 分钟；用户每轮只需回答一个问题；所有写回可回滚。

**成本**：10–12d（UI 为主）。

---

#### W-08 业务规则：默认过滤、同义词、币种、时区

**现状**：无。软删除表靠模型自觉；「客户」到底指哪张表靠模型猜。

**Wren 依据**：`knowledge/rules/*.md`，按 `##` 切块检索；`default_filters` 在执行期注入。

**方案**：

1. 规则模型（见 W-05 的 `semantic_rules`），类别固定为：`default_filter / naming / currency / time / external_id / canonical_table`；
2. **双通道消费**：
   - 提示词：按问题检索 top-3 规则片段注入（不要全量）；
   - 执行：`semantic_query` 自动拼默认过滤；`query_executor` 在 W-02 的 AST 改写里注入（对单表查询可行，多表 JOIN 先提示不自动改）；
3. UI 放在「语义模型」页签下的「规则」子页，Markdown 编辑器 + 预览 + 版本 diff。

**验收**：软删除表在两条取数路径都默认过滤；去掉规则后恢复；同义词「客户」能命中 canonical 表。

**成本**：5–6d。

---

### P2：季度内（评测与互操作）

#### W-09 评测换代：从「SQL 文本」到「结果正确 + 可复现 + 成本」

**现状**：`docs/eval_nl2sql.py` 用 9 类 SQL 文本断言；`golden_questions.json` 只有问题与期望关键词。

**Wren 依据**：20 题 + 两种工作流 A/B + rubric + 结构化输出（`evals/spodbtify_ab/`）。

**方案**：

1. 每个 case 增加：
   - `reference_sql`（可选）与 `expected`（结果级）：`row_count_range`、`columns`、关键指标的 `value_tolerance`；
   - `determinism: true`（同题跑两次，SQL 或 spec 必须一致）；
   - `context_variant`（有/无规则、有/无记忆），用于 A/B；
2. 指标：`execution_accuracy`（能跑通且结果匹配）、`semantic_accuracy`（口径正确）、`determinism_rate`、`p50_latency`、`token_cost`、`tool_error_rate`；
3. CI：`--dry` 继续做格式门禁；`--live` 每晚跑，回归超阈值阻断发布；
4. 评测集按域扩到 50 题（财务/运营/销售），并允许从「已确认的记忆」自动生成候选 case。

**验收**：任何一次 prompt/模型/上下文改动，都能给出上述 6 个指标的 before/after。

**成本**：6–8d（含 30 道新题）。

---

#### W-10 互操作：OSI 导入 + MCP 暴露（战略选项）

**现状**：无。

**Wren 依据**：`osi.py` 的 `--from-osi`；`mcp_server.py` 的 `run_sql/dry_plan/list_models/describe_model`。

**方案**（二选一或都做）：

1. **OSI 导入**：支持从 OSI YAML 生成 `semantic_models`，让已有 Looker/Cube/dbt 语义资产可迁移进来 —— 这是对企业客户最实在的「不锁定」承诺；
2. **MCP server**：把 `list_datasets/describe_dataset/semantic_query/run_sql/dry_plan` 暴露给外部 agent（Claude Code/Cursor），我们做「企业内 BI 的 MCP 后端」。需要：OAuth/PAT、按用户权限过滤、速率限制、审计。

**验收**：OSI 样例能导入并通过校验；MCP 客户端能列出数据集并跑一条受 RLS 约束的查询。

**成本**：8–12d（MCP 安全是大头）。

---

## 6. 不建议照搬的五件事

| 不抄 | 原因 |
|---|---|
| **agent-agnostic CLI 定位** | 它赌的是「agent 会普及，上下文层是刚需」；我们是产品公司，用户要的是看板、图表、协作。把 agent 能力外化成 MCP 可以作为附加能力，但不能替代产品。 |
| **Rust + DataFusion 语义引擎** | 31,890 行只换来「跨方言模型展开」；我们有 67 个引擎（26 个真实驱动）、现有 `sql_builder` 已经处理方言与 RLS。用「语义查询接口 + 受控自由 SQL」拿到 80% 收益，成本低一个数量级。 |
| **56k 行 Python CLI 的组织方式** | `context.py` 单文件 2,213 行、`cloud.py` 1,805 行，是典型的「先长出来再重构」。我们不必复刻这种技术债。 |
| **LanceDB + ONNX 本地向量** | 我们已有 Postgres，pgvector 是现成的；多引入一个嵌入式向量库只增加运维面。 |
| **Vercel/Cloudflare GenBI 部署** | 目标市场不同（它服务「让 agent 生成可分享站点」），我们的客户在私有化/内网。 |

---

## 7. 路线图

| 阶段 | 时间 | 内容 | 交付判断 |
|---|---|---|---|
| Sprint 1 | 第 1–2 周 | W-01 列暴露、W-02 守卫、W-04 检索、W-09a 基线 | 安全缺口清零；prompt token 明显下降；有基线数字 |
| Sprint 2 | 第 3–4 周 | W-03 记忆库、W-07 治理工作台（第一期：缺口扫描 + 逐题确认） | 重复问题准确率提升；覆盖率可量化 |
| Sprint 3 | 第 5–8 周 | W-05 语义模型、W-06 语义查询、W-08 规则 | 20 题里 ≥14 题走确定性路径；软删除/币种生效 |
| Quarter | 第 9–12 周 | W-09 评测换代、W-10 OSI/MCP、Cube 预聚合 | 每次改动有 before/after；互操作样例跑通 |

**排序逻辑**：先修「会出事」的（泄露、越权），再修「会变准」的（记忆、检索），最后修「会变强」的（语义层、评测、互操作）。语义层放在第 5 周不是因为它不重要，而是因为它依赖前面积累的元数据与规则，早做会变成第二个空壳。

---

## 8. 度量体系

| 层级 | 指标 | 现状 | 目标（季度） |
|---|---|---|---|
| 北极星 | 问数结果被采纳率（保存图表/继续追问/点赞 ÷ 总问数） | 未埋点 | 建立并 ≥ 45% |
| 正确性 | 金标题执行准确率 | 待测（W-09a） | ≥ 85% |
| 可复现 | 同题两次 SQL/spec 一致率 | 未测 | ≥ 95% |
| 安全 | 策略违规数（PII 泄露/跨表/写操作） | 未知 | 0 |
| 成本 | 单次问数 token / 延迟 P50 | 未统计 | 各降 ≥ 35% |
| 上下文 | 已描述列占比 / 已标枚举占比 / 有规则的表占比 | 未统计 | 80% / 60% / 50% |
| 体验 | 首答延迟、工具错误率、澄清命中率 | 部分有 | P50 < 8s；错误率 < 15% |

---

## 9. 风险与边界

1. **解析器方言覆盖**：67 个引擎不可能全 AST。策略是「只读会话兜底 + 主流方言 AST + 其余保守白名单」，并把不支持的方言显式标注（不要假装支持）。
2. **语义模型维护成本**：冷启动自动生成 + 只补关键字段（枚举/单位/关系），避免「建模工程」拖死项目；Wren 的 enrich 也是「先脚手架、再逐步丰富」。
3. **记忆污染**：auto 记忆必须可审、可删、可过期；错误范例比没有范例更糟。
4. **过度约束**：语义查询是主路径，自由 SQL 必须保留逃生门（受守卫与审计约束），否则复杂分析做不了。
5. **空壳清理的兼容性**：`semantic_layers/semantic_views` 是 Superset 兼容表，直接删会破坏导入导出；建议保留但标注「reserved」，同时移除误导性的 `semantic_reader` 暴露。

---

## 附录 A：WrenAI 关键源码索引

| 主题 | 路径 |
|---|---|
| 定位与架构 | `README.md`、`docs/core/reference/architecture.md` |
| MDL 规范 | `docs/core/reference/mdl.md`（models/columns/relationships/views/cubes/rules） |
| 规划 | `core/wren/src/wren/engine.py:97 dry_plan`、`mdl/cte_rewriter.py` |
| 守卫 | `core/wren/src/wren/policy.py:292 validate_sql_policy` |
| 记忆 | `core/wren/src/wren/memory/store.py`、`schema_indexer.py`、`sql_classify.py` |
| 技能 | `core/wren/src/wren/skills_content/{usage,enrich-context,generate-mdl}/SKILL.md` |
| 缺口清单 | `skills_content/enrich-context/references/gap_catalog.md` |
| 互操作 | `osi.py`、`dbt.py`、`mcp_server.py`、`sdk/` |
| 评测 | `evals/spodbtify_ab/{README.md,run_eval.py,spodbtify_ab_eval.json}` |
| 理念 | `docs/core/concepts/{correctness,memory_system,agent_learning,what_is_context}.md` |

## 附录 B：我们这边对应的代码位置

| 主题 | 路径 |
|---|---|
| Agent 提示 | `backend/internal/ai/agent_planner.go` |
| 运行时与错误回传 | `backend/internal/ai/agent_runtime.go:411`（工具调用）、`:295`（错误观测）、`:596`（空结果 hint） |
| SQL 守卫 | `backend/internal/ai/sql_safety.go:24` |
| 只读取数 | `backend/internal/queries/readonly.go:43`、`:203 wrapReadOnly` |
| 查询构建 | `backend/internal/queries/sql_builder.go` |
| 脱敏 | `backend/internal/api/masking.go:161`、`api/charts.go:730` |
| Schema 工具 | `backend/internal/ai/tools_read.go:108` |
| 元数据召回 | `backend/internal/ai/tools_read_extra.go:162` |
| 语义读取（空壳） | `backend/internal/ai/skills_domain.go:20`、`models/semantic.go` |
| 指标注入 | `backend/internal/ai/metrics.go:26`、`agent_planner.go:102` |
| 数据集编辑 UI | `frontend/src/pages/datasets/DatasetColumnModal.tsx`、`DatasetMetricModal.tsx` |
| 评测 | `docs/eval_nl2sql.py`、`docs/golden_questions.json` |
| 既有规划 | `docs/product-optimization.md`（P0-1 语义层 / P1-6 自检与评测） |

## 10. 实施状态（2026-10-18）

| 编号 | 内容 | 状态 | 落地位置 |
|---|---|---|---|
| W-01 | 字段暴露策略（hidden / masked 贯穿提示词→SQL→结果） | **已落地** | `models/dataset.go`、`queries/masking.go`、`queries/readonly.go`、`ai/tools_read.go`、`api/datasets.go`、`DatasetColumnModal.tsx` |
| W-02 | SQL 守卫升级（只读会话 + 表/列白名单 + 函数黑名单 + 结构化错误） | **已落地** | `ai/sql_guard.go`、`queries/readonly_session.go`、`ai/tools_read.go`、`ai/tools_read_extra.go` |
| W-03 | NL→SQL 记忆库 | **已落地** | `models/ai.go`（`AISQLMemory`）、`ai/memory.go`、`agent_planner.go`、`agent_runtime.go`、`api/ai.go`、`AgentPanel.tsx` |
| W-04 | 上下文检索（定向切片替代全量 dump） | **已落地** | `ai/schema_context.go`、`ai/textmatch.go`、`ai/tools_read_extra.go`、`ai/builtin.go` |
| W-09a | 先测基线 | **已落地**（5 题样本；全量待部署新构建后跑） | `docs/eval-baseline-20261018-1635.json` |
| W-05 | 语义模型 MDL-lite | **已落地** | `models/semantic_model.go`、`semantic/manifest.go`、`semantic/service.go`、`api/semantic.go`、`DatasetSemanticPanel.tsx` |
| W-06 | 语义查询接口 | **已落地**（Agent 实测走 `semantic_query`） | `semantic/tools_semantic.go`、`ai/semantic_context.go`、`queries/sql_builder.go` |
| W-07 | 口径治理工作台（第一期：缺口扫描 + 逐题确认） | **已落地**（数据集内页签，无独立菜单） | `semantic/gaps.go`、`DatasetSemanticPanel.tsx`（缺口页签） |
| W-08 | 业务规则 | **已落地** | `semantic/rules.go`、`agent_planner.go`（提示通道）、`tools_semantic.go`（执行通道） |
| W-09 | 评测换代 | **已落地**（结果级 + 确定性 + 成本指标） | `docs/eval_nl2sql.py` |
| W-10 | OSI/MCP 互操作 | 未开始 | 导出格式（YAML/JSON）已稳定，可在此基础上做 OSI 映射；MCP server 未动 |

**为什么 P0 先做、P1 后做**：W-01/W-02 是会出事的缺陷（PII 原文进回答与审计日志、跨表读）；W-03/W-04 是「会变准」且互不依赖的独立增量；W-05~W-08 相互依赖（语义查询依赖模型，规则的双通道消费依赖语义查询），而且都需要补 UI 才能被真实使用——按 §7 的排序逻辑，它们必须等前面积累的描述/枚举/规则数据，否则会变成第二个「有表无实现」的空壳。

**落地过程中的两处判断偏差（值得记住）**：

1. `pii_level` 没有做成独立的判断依据，而是与 `mask_type` 合并成 `EffectiveMaskRule`：显式 `mask_type` 优先，`pii_level=high` 只在没有显式规则时兜底为 `full`。两个平行的敏感度字段必然漂移。
2. 向量召回没有硬上：部署里没有 embedding provider，pgvector 也可能没装。Wren 自己也接受「无向量时退化为词重叠」，所以 `textmatch.go` 用 ASCII 词 + 中文二元组的覆盖率打分，`meta_search` 明确返回 `retriever: "lexical"`，不假装是向量检索。

**W-05~W-08 落地时暴露的四个真 bug（比功能本身更值得记）**：

1. **软删除 + 普通唯一索引 = 一次性资源**：`semantic_models`/`semantic_fields` 上的 `UNIQUE(dataset_id, name)`、`UNIQUE(model_id, name)` 不排除 `deleted=true`。后果有两层：保存字段（整表替换 = 软删旧行 + 插同名新行）每次都 500；删掉模型后重新冷启动永远 409，用户看不到任何冲突行。修法是把两个键改成 `WHERE deleted = false` 的部分唯一索引（`pkg/database.migrateSemanticIndexes`，AutoMigrate 表达不了），并把 GORM 的 `uniqueIndex` 标签去掉，否则下次启动又会建回普通唯一索引。
2. **规则的存储形状与解析形状不一致**：`normalizeSpec` 统一存成数组，`Compile` 却按单个对象反序列化。于是**每一条通过 API 建的 default_filter 规则**都会在清单里变成 `DEFAULT_FILTER_UNKNOWN_COLUMN` 错误，模型永远发布不了——功能「有表有接口有 UI」但完全不可用。现在 `filterSpecs` 同时接受对象与数组，并把「spec 坏了」和「过滤的列不存在」拆成两个错误码（前者是 JSON 问题，后者是 schema 漂移，修法不同）。
3. **校验了没解析的表达式**：未知列检查看的是 `f.Expression`，而 column/time 字段的透传形态表达式为空（等价于同名列）。结果是列改名/删列后，指向幽灵列的字段时间接通过校验，直到执行期才报 SQL 错。现在校验**解析后**的表达式，且只在它是裸标识符时校验（`SUM(x)`、`date_trunc(...)` 不能按列名判定）。
4. **构建器只认 JSON map，不认自己定义的强类型**：`columnItem` 支持 `map[string]any` 却不支持 `AdhocColumn`，而 `metricItem` 两者都支持。语义查询工具用 Go 结构体构造 `Groupby`，于是**每一次 semantic_query 都返回 `Unsupported column definition`**，Agent 只能退回自由 SQL——工具在目录里、能调用、但从不成功。补上 `case AdhocColumn` 并加了三条构建器单测（强类型 / map / 表达式不被引号包住）。

**这一批的边界（刻意不做）**：

- 语义模型只覆盖单数据集内的模型与模型间关系，不做跨数据集联邦；跨库 JOIN 仍走自由 SQL。
- 缺口扫描的采样是 opt-in（`?sample=1`），因为读真实行需要授权；个人信息类列永不采样（草稿会把原始值写进治理报告）。
- 治理台做成数据集详情里的页签，而不是独立菜单：指标字典页面的教训是「没有任务起点的治理页面 = 孤儿页面」。
- W-10 没动：导出（YAML/JSON）已经稳定，但 OSI 是外部规范，MCP server 是另一条进程边界，单独排期比塞进本轮更稳。
