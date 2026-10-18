# 智观 BI（Superset Go/React 复刻）程序审计与优化建议

> 审计视角：顶级程序员 / 架构评审。方法＝源码静态审查 + 实测 PoC + 单测回归，不做泛泛而谈。
> 范围：后端 114 个 Go 文件（30k+ 行）、前端 92 个 TS/TSX 文件、PostgreSQL 元数据库（68 表）。
> 时间：2026-10-17。

---

## 0. 结论摘要

| 级别 | 数量 | 说明 |
| --- | --- | --- |
| **P0 安全/可用性** | 5 | 3 项本轮已修复并验证（SQL 注入、Agent 上下文打爆、只读 SQL 误杀），2 项需产品决策（温度参数、错误语义） |
| **P1 正确性/体验** | 9 | 静默吞错、审计库膨胀、暗色主题硬编码色、图表不随容器 resize、响应式缺失、Agent 交互缺口等 |
| **P2 工程质量** | 6 | 无 Skeleton、可访问性、分页、i18n 混语言等 |
| **Agent 能力优化** | 8 条 | 对标 2026 主流产品（Copilot / Spotter / Pulse / Gemini / 小Q / 白泽）的可落地改造 |
| **UI 优化** | 7 条 | 图表设计语言、仪表盘编辑、表格、移动端、可访问性 |

**一句话判断**：功能复刻度已经很高（主题、TABS、67 引擎、AI Agent 都在），当前短板不在"有没有"，而在**可信度**——注入面、非确定性输出、静默失败、深色主题可读性。2026 年 BI 的竞争点已经从"能回答"变成"回答得可信、可溯源、可执行"（见 §7 对标），这恰好是本项目下一步该补的。

---

## 1. P0：安全与可用性缺陷

### P0-1（已修复）列表接口 SQL 注入 —— `filters[].col` 直拼 WHERE

- **位置**：`backend/internal/api/helpers.go` `applyFilters()`（20 个列表接口共用：chart/dashboard/dataset/database/theme/annotation/AI run…）
- **根因**：`col := f.Col` 来自 query string，随后 `q.Where(col + " = ?", ...)` 拼接；`server.ParseFilters` 只做 JSON 解析，无列名校验。
- **实测证据**（修复前，8088）：

  ```text
  baseline:                        time=0.019s
  filters=[{"col":"(SELECT pg_sleep(0.6) IS NULL)","opr":"is_not_null"}]
                                   time=0.622s     # 任意 SQL 表达式在元数据库执行
  filters=[{"col":"nonexistent_column",...}]
                                   → column "nonexistent_column" does not exist (SQLSTATE 42703)
  ```

  任意登录用户即可对元数据库做布尔盲注/时间盲注，进而读取 `ab_user`（密码哈希）、`key_value` 等。
- **修复**：新增 `quoteFilterColumn()`，仅接受 `^[A-Za-z_][A-Za-z0-9_]*$` 并双引号包裹；不合法直接忽略。
- **回归**：`internal/api/helpers_test.go` 覆盖 4 个合法标识符 + 9 个注入载荷；实测注入请求恢复 47ms、返回正常 6 条，正常筛选 `id=2` 仍 `count=1`。
- **残留建议**：未知但合法的列名仍会 500 并回显 `SQLSTATE 42703`。应改为**按模型列白名单**校验（参考 Superset `filterable_columns`），返回 400 且不泄露 SQL 错误。

### P0-2（已修复）Agent 观察量无上限：一次取数即可打爆上下文

- **位置**：`backend/internal/ai/agent_runtime.go` `observationText()`；行数默认值 `AI_QUERY_MAX_ROWS=5000`（`config.go:342`）。
- **根因**：工具结果 `rows` 全量 `json.Marshal` 后作为 tool message 回灌模型。5000 行 × 8 字段 ≈ 200–400KB ≈ 60–120k tokens，超过多数模型窗口 → 第一次取数就 4xx/截断，且日志里只表现为 "模型不可用"，极难定位。
- **修复**：模型侧行数截断 50 行 + 24KB 兜底摘要（保留 `rowcount`、`colnames`、5 行 `sample`），新增 `_rows_omitted` 字段告知模型被省略的行数；**UI 与审计仍拿全量结果**。
- **回归**：`internal/ai/agent_runtime_test.go`（500 行结果 → ≤24KB 且 `rowcount` 不变；小结果不截断）。

### P0-3（已修复）只读 SQL 守卫误杀 `REPLACE()` 函数

- **位置**：`backend/internal/ai/sql_safety.go` 关键词黑名单含 `replace`。
- **现象**：`SELECT REPLACE(gender,'girl','f') FROM ...` 被判定为写操作拒绝。列清洗是 AI 生成 SQL 的高频动作，直接表现为"Agent 偶尔莫名其妙说 SQL 不安全"。
- **修复**：移除 `replace`。`REPLACE` 作为语句不可能出现在 `SELECT/WITH` 开头，且多语句已被拦截，守卫强度不变。
- **回归**：`internal/ai/sql_safety_test.go`（允许 `REPLACE()`/字面量含 `delete`；拒绝 DROP/DELETE/UPDATE/多语句/`SELECT INTO`）。

### P0-4（待决策）温度参数从未下发 → 输出不确定

- **位置**：`backend/internal/ai/llm_gateway.go:200` `Temperature float64 \`json:"temperature,omitempty"\``；全仓无任何赋值。
- **后果**：零值被 `omitempty` 吃掉，所有模型调用走供应商默认温度（通常 1.0）。同一问题两次运行 SQL 不同、结论不同，直接击穿产品北极星"可复现率（同问题同 SQL=100%）"。
- **建议**：`Temperature *float64`，默认 0.1–0.2；支持 `AI_TEMPERATURE` 配置；供应商支持时同时传 `seed`；把 `temperature/model/prompt_hash` 写进 `ai_llm_call_log`，为"同题同 SQL"提供证据链。另建议补 `max_tokens`（部分供应商默认 512，长结论会被截断）。

### P0-5（待决策）错误语义：500 + 数据库原文外泄

- **位置**：`helpers.go` `listResultW` 全量透传 `apperrors.Internal`；`queries` 层把 `Query failed: <DB 原文>` 直接回给前端。
- **后果**：泄露表名/列名/SQL 方言细节；前端只显示红色 toast，用户不知道是"筛选列不存在"还是"服务挂了"。
- **建议**：错误分类映射（`42703/42P01` → 400「筛选字段不存在」；连接失败 → 424「数据源不可用」；超时 → 504），原文只进日志与审计。

---

## 2. P1：正确性与体验缺陷

| # | 位置 | 问题 | 建议 |
| --- | --- | --- | --- |
| 1 | `internal/ai/tools_read.go:118-122` | `schemaReader` 用 `_ = ...Find()` 吞掉列/指标查询错误 → Agent 拿到空 schema 后编 SQL | 返回错误（`errors.Join`），让模型看到"schema 读取失败"而不是"没有列" |
| 2 | `internal/ai/audit.go:70-91` | 每次工具调用把**完整结果**写 `ai_tool_call_log.result(jsonb)`，5000 行结果 × 每次调用 → 元数据库膨胀、写放大 | 落库前截断 16KB + 记录 `query_id` 关联；大对象走对象存储 |
| 3 | `agent_runtime.go` 主循环 | 无重复调用检测：模型可用相同 `name+args` 反复刷预算 | 对 `(tool,args)` 做 hash，第二次相同调用直接回 `{"status":"duplicate"}` 并提示换策略 |
| 4 | `frontend/src/components/charts/renderers/*.tsx` | **47 处硬编码颜色**（`#333333/#666666/#e0e0e0/#f0f0f0/#ffffff`）。深色主题下轴标签、系族标签、日历格线几乎不可读 | 统一走 `utils.ts` 的 `AXIS_COLOR/SPLIT_COLOR`，新增 `TEXT_STRONG/TEXT_MUTED`；渲染器禁用裸 hex（ESLint 规则） |
| 5 | `frontend/src/components/charts/EChart.tsx` | 未监听**容器**尺寸；echarts-for-react 只处理 window resize。仪表盘拖拽改 tile 尺寸、侧栏折叠后图表不重排（留白/溢出） | 加 `ResizeObserver` → `chart.resize()`（debounce 80ms） |
| 6 | 全仓仅 **1 条 `@media`** | 无响应式：Agent Drawer 固定 460px，窄屏溢出；KPI/表格在 <768px 不可用 | 断点 768/1200；Drawer 窄屏全屏；表格横向滚动容器 |
| 7 | `components/ai/AgentPanel.tsx:96-99` | artifacts effect 以对象身份触发：流式期间每次 observe 都重建 artifacts → Dashboard 反复 `load()` + 多次成功 toast | effect 内按 `chart_ids/dashboard_id` 值去重；或仅在 `answer` 到达后 apply 一次 |
| 8 | `AgentPanel.tsx:101/121` | `if (!canUse) return null` 位于 `useState` 之前（hooks 顺序违规）。当前被 `RequireAuth` 掩盖，一旦权限异步刷新即 "Rendered more hooks" 崩溃 | 提前所有 hooks，条件返回放到最后 |
| 9 | `server.ParseFilters` + `helpers.go:52` | `filters, _ :=` 吞错：非法 filters 静默变"无过滤"，用户以为筛选生效 | 解析失败返回 400；`parseListParams` 改为返回 error |

---

## 3. P2：工程质量与一致性

1. **感知性能**：0 处 `Skeleton`（antd 已内置），首屏表格/图表只有 spinner；列表切页闪白。
2. **可访问性**：92 个 tsx 仅 6 处 `aria-*/onKeyDown/tabIndex`；Drawer 无 Esc 关闭、发送无 `Cmd+Enter`、图表无文本替代。
3. **i18n 混语言**：Agent 用户可见文案在**后端硬编码中文**（`fallbackAnswer`、`resultHint`、`ValidateReadOnlySQL` 错误），`locale=en` 时会中英混排；系统提示也是中文写死（`agent_planner.go`）。
4. **历史分页**：`loadHistory` 固定 20 条且无"加载更多"；`/ai/agent/runs` 支持 page 但前端未用。
5. **超时不可配**：`executeSQL` 120s 为常量（`engine.go:477`），Superset 支持按 database/dataset 配置；长查询与慢库无法差异化。
6. **RLS × 聚合**：`wrapReadOnly` 把 RLS 条件套在外层子查询，聚合结果（单行、无用户列）无法被过滤 → 报错或漏过滤；应按 Superset 做法把 RLS 注入内层 WHERE/建安全视图。

---

## 4. Agent 智能分析：8 项可落地优化

> 现状已具备：SSE plan→act→observe→answer、只读 SQL 守卫、写工具+回滚、会话记忆（3 轮×400 字）、指标字典注入、自检重试、贡献度程序计算。短板集中在**规划真实性、成本控制、可验证性、失败恢复**。

1. **Plan 事件真实化**（当前是固定文案 "理解问题 → 规划取数与分析步骤"）。让模型先输出结构化 plan（`steps[{intent,dataset,metrics,deps}]`），前端可折叠/可编辑/可跳过；plan 与最终答案同时入库，便于复盘"为什么走了这条路"。
2. **上下文预算显式化**。已做 observation 截断；下一步：schema 只发相关列（按问题召回 Top-N）、大结果转**摘要统计**（min/max/sum/分位 + 前 20 行）、跨轮用摘要而非全文，并在 UI 显示 "本轮已用 12.3k/40k tokens"。
3. **SQL 质量闸门**。在只读守卫后加：① 方言校验（按 database 引擎注入方言提示）；② `EXPLAIN` 成本闸（全表扫描行数 > N 或笛卡尔积直接拒绝并提示加过滤）；③ 生成 SQL 与执行 SQL 双记录（含 params），支撑"同题同 SQL"复现。
4. **语义层强制引用**。MetricDigest 已有；进一步要求模型只能引用 approved `metric code` 或显式 `expr`，禁止自由发明聚合口径（Looker LookML / Quick BI 指标模型的思路），未命中就返回"该指标未定义"而不是猜。
5. **分析状态机 + 断点续跑**。现在是单次 run，SSE 断了只能重问（前端无重连）。建议：run 级 checkpoint（已完成的 step 落库），断线后 `GET /ai/agent/runs/{id}/resume` 续跑；长任务转异步 + 通知（复用现有 asynq/报告投递）。
6. **可观测性指标**。首字延迟、每步 token、工具成功率、空结果率、`self_check` 触发率、答案采纳率（"保存为图表/加入看板"点击）、SQL 复现率。没有这些指标，Agent 优化只能靠感觉。
7. **评估集进 CI**。`docs/golden_questions.json` 现 5 题，扩到 ≥50 题分层（单表聚合 / 时间对比 / 多表 / 归因 / 空结果 / 越权）；`--dry` 进 PR，`--live` 夜间跑，分数下降阻断合并。ThoughtSpot/白泽的"可信"都是靠这种回归守住的。
8. **成本分级路由**。按问题复杂度（是否多表、是否需归因）选小模型/大模型；相同 `(dataset, schema_hash, question)` 的 SQL 模板缓存；对失败 run 落"反例样本"，定期回灌提示词。

---

## 5. 页面 UI：7 条改进

1. **图表设计语言统一**：把颜色/字号/间距收敛到 `theme.ts` → 生成 echarts theme 注册，渲染器只取令牌（解决 §2-4 的 47 处硬编码）。
2. **图表默认可用性**：轴标签超长自动旋转/省略 + tooltip 全文；多系列共享 tooltip；legend 超 8 项折叠；数值格式化（千分位/百分比/万/亿）；空结果给"调整时间范围/去掉筛选"的引导按钮而不是一行灰字。
3. **仪表盘编辑体验**：拖拽把手可见化 + 对齐参考线、撤销/重做（当前只有 dirty 标记与离开确认）、多选批量对齐、保存状态用 "已保存 14:32" 而非瞬时 toast。
4. **表格**：列宽持久化（localStorage 按 dataset 维度）、首列冻结、密度切换、CSV 导出入口前置。
5. **Agent 面板交互**：自动滚动到底、答案复制为 Markdown、重新生成、失败一键重试、追问建议 chips（"按性别拆一下""看同比"）、运行前显示预计成本档位、窄屏全屏。
6. **移动端**：≥768 断点，KPI 卡片单列、筛选器抽屉化、图表高度自适应。
7. **可访问性**：focus ring 可见、Drawer Esc 关闭、图表 `aria-label` 摘要（"饼图：girl 52%、boy 48%"）、表格 `scope` 语义。

---

## 6. 与 2026 主流产品的差距（对标）

| 能力 | Power BI Copilot | Tableau Pulse | ThoughtSpot Spotter | Looker + Gemini | Quick BI 小Q / 白泽 | 本项目现状 |
| --- | --- | --- | --- | --- | --- | --- |
| 自然语言问数 | ✅ 生成 DAX/报告 | ✅ Ask/Agent | ✅ 搜索式（强项） | ✅ 基于 LookML | ✅ 全链路 | ✅ NL2SQL + 只读守卫 |
| 指标口径治理 | 语义模型 | 指标层 | 建模依赖 | **LookML（标杆）** | 指标模型/口径治理 | 🟡 指标字典（Lite，已审核注入） |
| 主动洞察/订阅 | Copilot 摘要 | **Pulse 定时摘要（标杆）** | SpotIQ 20+ 异常检测 | 有限 | 订阅/推送 | 🟡 报表+邮件/Slack 投递，缺指标订阅编排 |
| 归因/下钻 | Key Influencers（手动解读） | 有限 | 有限（不自主动因） | 有限 | **归因链路（强）** | 🟡 structure 贡献度，缺 driver 分解 |
| 可溯源/可复现 | 输出非确定（公认短板） | 中 | 中 | 高（语义层） | 高（指标+溯源） | 🔴 温度未固定、SQL 未双记录 |
| 行动闭环 | Teams/Flow | Salesforce | 工作流嵌入 | GCP | 钉钉/业务系统 | 🟡 写工具+回滚，缺 IM 分发 |
| 主题/品牌化 | 有限 | 有限 | 有限 | 有限 | 中 | ✅ 主题管理（default/dark 槽位 + 热应用） |

结论：**"能问"已达标，"可信/可执行"是差距**。优先级应放在复现性（温度/seed/SQL 留存）、口径强制、归因深度、主动推送四件事上，而不是继续堆图表类型。

---

## 7. 路线图与验收

**第 1 周（P0 收口）**
- 温度/seed/max_tokens 落地 + 审计字段（验收：同题连跑 3 次 SQL 文本一致率 ≥ 90%，记录可查）。
- 错误语义映射（验收：未知筛选列返回 400 且无 SQL 原文；数据源断连返回 424）。
- 列白名单（验收：注入载荷 100% 400/忽略；前端筛选无回归）。

**第 2–3 周（P1 体验）**
- 图表主题令牌化 + ResizeObserver + 暗色对比度巡检脚本（验收：dark 下轴标签对比度 ≥ 4.5:1；拖拽 resize 后无留白）。
- AgentPanel 交互补齐（滚动/复制/重试/去重 artifacts）+ hooks 修正（验收：流式 30 帧仅 1 次 dashboard 刷新）。
- schemaReader 错误上抛、审计截断（验收：工具失败可见；单 run 审计写入 ≤ 200KB）。

**第 1–2 月（能力）**
- 结构化 plan + 断点续跑 + 指标级可观测（验收：断网重连可续跑；面板可见首字延迟/token/成功率）。
- 语义层强制引用 + EXPLAIN 成本闸（验收：未定义指标不生成 SQL；全表扫描被拦截并给出建议）。
- 评估集 50 题进 CI（验收：PR 上 `--dry` 全绿，nightly 分数趋势可见）。

**北极星指标**：问数采纳率、同题 SQL 复现率、空结果自愈率、提问到入盘时长、主动订阅回访率。

---

## 8. 附：本轮验证命令

```bash
# 单元测试（新增 4 组断言集）
cd superset-go/backend && /d/Go/bin/go.exe test ./internal/api/ ./internal/ai/

# 注入回归（修复前后对比）
curl -sG --data-urlencode 'filters=[{"col":"(SELECT pg_sleep(0.6) IS NULL)","opr":"is_not_null"}]' \
  "localhost:8099/api/v1/chart/?page_size=1" -H "Authorization: Bearer $TOKEN" -w '\ntime=%{time_total}s\n'
# 修复前 0.622s / 修复后 0.047s，返回 count=6 正常数据
```
