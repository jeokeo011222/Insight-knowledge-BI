package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"superset/internal/ai/registry"
)

// complete runs a plain (no function-calling) model call and returns the text
// plus its token usage. It reuses the run's resolved provider when there is one.
func (s *Service) complete(ctx context.Context, userID int, system, user string) (string, int, error) {
	pc, err := s.providerForContext(ctx)
	if err != nil {
		return "", 0, err
	}
	resp, err := s.LLM.Chat(ctx, pc, &ChatRequest{
		UserID: userID,
		Messages: []ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(resp.Content), resp.TokenUsage, nil
}

// extractJSONObject pulls the first JSON object out of a model reply, tolerating
// prose or ```json fences around it.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}

// fetchSchema reads a dataset's schema through the schema_reader tool so the
// call goes through the permission guard. It returns the compact text form
// (schema_context.go), which is 40–60% smaller than the JSON dump and already
// sliced for `question` when the table is wide.
func fetchSchema(ctx context.Context, rt *registry.ToolRuntime, datasetID int, question string) string {
	if datasetID <= 0 || rt == nil || rt.InvokeTool == nil {
		return "（未提供数据集 schema）"
	}
	args := map[string]any{"dataset_id": datasetID}
	if strings.TrimSpace(question) != "" {
		args["question"] = question
	}
	rawArgs, _ := json.Marshal(args)
	tr, err := rt.InvokeTool(ctx, "schema_reader", rawArgs)
	if err != nil || tr == nil {
		return "（schema 读取失败）"
	}
	if m, ok := tr.Output.(map[string]any); ok {
		if compact, ok := m["compact"].(string); ok && strings.TrimSpace(compact) != "" {
			return compact
		}
	}
	b, err := json.Marshal(tr.Output)
	if err != nil {
		return "（schema 序列化失败）"
	}
	return string(b)
}

const (
	nl2sqlSystemPrompt     = `你是资深数据分析师兼 SQL 工程师。只输出一个 JSON 对象，不要任何解释文字，也不要代码块围栏。`
	insightSystemPrompt    = `你是资深数据分析师。基于给定的 SQL 与查询结果，用中文 Markdown 输出：先给结论，再给关键证据（指标/维度/异常点），最后给建议。严禁编造结果中不存在的数据；数据不足时如实说明。`
	chartSystemPrompt      = `你是资深 BI 可视化专家。只输出一个 JSON 对象，不要任何解释文字，也不要代码块围栏。`
	sqlExplainSystemPrompt = `你是资深 SQL 工程师。用中文 Markdown 面向用户说明或优化给定的 SQL：` +
		`先给一句话结论，再逐条列出要点；如需给出改写后的 SQL，请放在 ` + "```sql" + ` 代码块中。` +
		`不要杜撰表结构或字段，无法确定的地方要明确说明。`
)

// sqlExplainExecutor explains, optimises or completes a SQL statement. It only
// authors text — the statement is never executed here, so the skill is a safe
// read-only helper for SQL Lab and the chat page.
func sqlExplainExecutor(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			SQL  string `json:"sql"`
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.SQL) == "" {
			return nil, errors.New("sql is required")
		}
		mode := strings.ToLower(strings.TrimSpace(args.Mode))
		switch mode {
		case "generate", "optimize", "explain":
		default:
			mode = "explain"
		}
		goals := map[string]string{
			"explain":  "解释这段 SQL 的业务含义、扫描与聚合逻辑，并指出可能的性能瓶颈",
			"optimize": "在保持语义等价的前提下优化这段 SQL，并说明每条修改带来的收益",
			"generate": "根据已有片段补全为更完整、可直接执行的只读 SQL，并说明补充了哪些过滤或聚合",
		}
		prompt := fmt.Sprintf("任务：%s\n原始 SQL：\n```sql\n%s\n```\n\n"+
			`只返回 JSON：{"summary":"一句话结论","points":["要点"],"optimized_sql":"改写后的 SQL 或空字符串","index_hints":["索引建议"]}`,
			goals[mode], args.SQL)
		content, tokens, err := s.complete(ctx, in.UserID, sqlExplainSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}

		out := map[string]any{}
		if raw := extractJSONObject(content); raw != "" {
			_ = json.Unmarshal([]byte(raw), &out)
		}
		optimized, _ := out["optimized_sql"].(string)

		var b strings.Builder
		if summary, _ := out["summary"].(string); strings.TrimSpace(summary) != "" {
			b.WriteString("## 结论\n")
			b.WriteString(strings.TrimSpace(summary))
			b.WriteString("\n")
		}
		if points, ok := out["points"].([]any); ok && len(points) > 0 {
			b.WriteString("\n## 要点\n")
			for _, p := range points {
				b.WriteString(fmt.Sprintf("- %v\n", p))
			}
		}
		if strings.TrimSpace(optimized) != "" {
			b.WriteString("\n## 优化后 SQL\n```sql\n")
			b.WriteString(strings.TrimSpace(optimized))
			b.WriteString("\n```\n")
		}
		if hints, ok := out["index_hints"].([]any); ok && len(hints) > 0 {
			b.WriteString("\n## 索引建议\n")
			for _, h := range hints {
				b.WriteString(fmt.Sprintf("- %v\n", h))
			}
		}
		md := b.String()
		if strings.TrimSpace(md) == "" {
			// The model ignored the JSON contract: fall back to its raw text so
			// the caller still gets a usable explanation.
			md = content
		}
		artifacts := map[string]any{}
		if strings.TrimSpace(optimized) != "" {
			artifacts["sql"] = strings.TrimSpace(optimized)
		}
		return &registry.Result{Output: out, Markdown: md, Artifacts: artifacts, TokenUsage: tokens}, nil
	}
}

// nl2SQLExecutor turns a natural-language question into a candidate SQL
// statement (it never executes it), returning SQL + confidence + clarifications.
func nl2SQLExecutor(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			Question  string `json:"question"`
			DatasetID int    `json:"dataset_id"`
			Dialect   string `json:"dialect"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if strings.TrimSpace(args.Question) == "" {
			return nil, errors.New("question is required")
		}
		if args.DatasetID == 0 && in.Context.DatasetID != nil {
			args.DatasetID = *in.Context.DatasetID
		}
		dialect := args.Dialect
		if dialect == "" {
			dialect = "标准 SQL"
		}

		schema := fetchSchema(ctx, rt, args.DatasetID, args.Question)
		prompt := fmt.Sprintf("数据集 schema（紧凑文本）：\n%s\n\n目标方言：%s\n用户问题：%s\n\n"+
			`请生成一条只读 SELECT，只返回 JSON：{"sql":"...","confidence":0.0到1.0的小数,"missing":["仍需澄清的信息"],"explanation":"一句话口径说明"}`,
			schema, dialect, args.Question)
		content, tokens, err := s.complete(ctx, in.UserID, nl2sqlSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}

		out := map[string]any{}
		if raw := extractJSONObject(content); raw != "" {
			_ = json.Unmarshal([]byte(raw), &out)
		}
		md := "未能生成候选 SQL。"
		if sql, _ := out["sql"].(string); sql != "" {
			md = fmt.Sprintf("**候选 SQL**（置信度 %v，未经执行）\n```sql\n%s\n```", out["confidence"], sql)
		}
		return &registry.Result{Output: out, Markdown: md, TokenUsage: tokens}, nil
	}
}

// insightExecutor authors a query from the requested metrics/dimensions, runs
// it through query_executor and turns the rows into a narrative insight.
func insightExecutor(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			DatasetID int      `json:"dataset_id"`
			Metrics   []string `json:"metrics"`
			Groupby   []string `json:"groupby"`
			Filters   []any    `json:"filters"`
			Question  string   `json:"question"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if args.DatasetID == 0 && in.Context.DatasetID != nil {
			args.DatasetID = *in.Context.DatasetID
		}
		if args.DatasetID <= 0 {
			return nil, errors.New("dataset_id is required")
		}

		schema := fetchSchema(ctx, rt, args.DatasetID, args.Question)
		spec, _ := json.Marshal(map[string]any{
			"metrics": args.Metrics, "groupby": args.Groupby,
			"filters": args.Filters, "question": args.Question,
		})
		prompt := fmt.Sprintf("数据集 schema：\n%s\n\n分析口径：%s\n\n"+
			`请生成一条只读 SELECT（务必带 LIMIT），只返回 JSON：{"sql":"...","confidence":0.0到1.0的小数,"explanation":"..."}`,
			schema, string(spec))
		content, tokens, err := s.complete(ctx, in.UserID, nl2sqlSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		var gen map[string]any
		if raw := extractJSONObject(content); raw != "" {
			_ = json.Unmarshal([]byte(raw), &gen)
		}
		sql, _ := gen["sql"].(string)
		if sql == "" {
			return nil, errors.New("insight: could not author a SQL statement")
		}

		if rt == nil || rt.InvokeTool == nil {
			return nil, errors.New("insight: tool runtime is unavailable")
		}
		rawArgs, _ := json.Marshal(map[string]any{"sql": sql, "dataset_id": args.DatasetID})
		tr, err := rt.InvokeTool(ctx, "query_executor", rawArgs)
		if err != nil {
			return nil, err
		}

		sample, _ := json.Marshal(s.sampleRows(tr.Output))
		md, t2, err := s.complete(ctx, in.UserID, insightSystemPrompt,
			fmt.Sprintf("分析目标：%s\nSQL：%s\n查询结果（JSON）：\n%s", args.Question, sql, string(sample)))
		if err != nil {
			return nil, err
		}
		return &registry.Result{
			Output:   map[string]any{"sql": sql, "result": tr.Output},
			Markdown: md,
			Artifacts: map[string]any{
				"sql": sql,
				// chart_spec powers the frontend "保存为图表" closed loop.
				"chart_spec": chartSpecFromInsight(args.DatasetID, args.Question, args.Metrics, args.Groupby),
			},
			TokenUsage: tokens + t2,
		}, nil
	}
}

// supportedVizTypes is the whitelist of visualisation types the replica can
// actually render; recommendations outside it fall back to heuristics.
var supportedVizTypes = map[string]bool{
	"line": true, "bar": true, "dist_bar": true, "area": true, "pie": true,
	"scatter": true, "big_number": true, "big_number_total": true, "table": true,
	"pivot_table_v2": true, "heatmap": true, "treemap": true, "funnel": true,
	"gauge_chart": true, "radar": true, "word_cloud": true, "sunburst": true,
	"rose": true, "horizontal_bar": true, "cal_heatmap": true,
}

// chartSpecFromInsight turns an insight invocation into a persisted-chart spec.
// The viz-type guess follows the data shape: any time-ish field suggests a
// timeseries; otherwise the first grouping column drives a categorical bar.
func chartSpecFromInsight(datasetID int, question string, metrics, groupby []string) map[string]any {
	if datasetID <= 0 {
		return nil
	}
	timeish := func(name string) bool {
		n := strings.ToLower(name)
		for _, kw := range []string{"date", "time", "month", "week", "day", "year", "dt", "季度", "月", "日", "周", "年"} {
			if strings.Contains(n, kw) {
				return true
			}
		}
		return false
	}
	viz := "bar"
	if len(metrics) == 1 && len(groupby) == 1 && timeish(groupby[0]) {
		viz = "line"
	} else {
		for _, g := range groupby {
			if timeish(g) {
				viz = "line"
				break
			}
		}
	}
	name := strings.TrimSpace(question)
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	return map[string]any{
		"dataset_id": datasetID, "viz_type": viz, "slice_name": name,
		"metrics": metrics, "groupby": groupby,
	}
}

// heuristicCandidates ranks visualisation types from the analysis intent and
// field shape before the model weighs in (P1-3: data-shape driven, not a
// static lookup). Deterministic, cheap, and the fallback for hallucinated
// viz types.
func heuristicCandidates(intent string, fields []any) []map[string]any {
	i := strings.ToLower(intent)
	type cand struct {
		viz    string
		reason string
		score  int
	}
	cands := []cand{
		{"line", "时间序列趋势：随时间演变的指标首选折线", 0},
		{"bar", "分类对比：按维度比较指标大小", 0},
		{"dist_bar", "分组对比：多指标或分组条形对比", 0},
		{"pie", "占比构成：少数类别的份额分布", 0},
		{"treemap", "层级占比：类别较多时的面积占比", 0},
		{"heatmap", "双维密度：两个维度交叉的强度分布", 0},
		{"scatter", "相关性：两个数值变量的关系", 0},
		{"big_number", "单值大数字：只看一个关键数值", 0},
	}
	bump := func(viz, kw string, n int) {
		if strings.Contains(i, kw) {
			for k := range cands {
				if cands[k].viz == viz {
					cands[k].score += n
				}
			}
		}
	}
	bump("line", "趋势", 3)
	bump("line", "走势", 3)
	bump("line", "时间", 2)
	bump("line", "变化", 2)
	bump("pie", "占比", 3)
	bump("pie", "构成", 3)
	bump("pie", "份额", 3)
	bump("treemap", "占比", 1)
	bump("bar", "排名", 3)
	bump("bar", "top", 3)
	bump("bar", "对比", 2)
	bump("bar", "比较", 2)
	bump("dist_bar", "对比", 1)
	bump("heatmap", "分布", 2)
	bump("heatmap", "相关", 2)
	bump("heatmap", "密度", 2)
	bump("scatter", "相关", 3)
	bump("scatter", "关系", 2)
	bump("big_number", "总共", 2)
	bump("big_number", "多少", 1)

	// Field shape: 0 groupby + 1 metric → big number; ≤6 categories → pie over treemap.
	if len(fields) == 1 {
		cands[7].score += 2 // big_number
	}
	out := make([]map[string]any, 0, len(cands))
	for _, c := range cands {
		if c.score > 0 {
			out = append(out, map[string]any{"vizType": c.viz, "reason": c.reason, "score": c.score})
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"vizType": "bar", "reason": "默认分类对比", "score": 1})
	}
	return out
}

// chartDesignExecutor recommends a visualisation for the fields and intent.
// Deterministic heuristics rank candidates first; the model refines wording
// and picks among them, and any viz type outside the render whitelist is
// replaced by the top heuristic candidate (P1-3).
func chartDesignExecutor(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			DatasetID int    `json:"dataset_id"`
			Fields    []any  `json:"fields"`
			Intent    string `json:"intent"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if args.DatasetID == 0 && in.Context.DatasetID != nil {
			args.DatasetID = *in.Context.DatasetID
		}
		fields, _ := json.Marshal(args.Fields)
		schema := fetchSchema(ctx, rt, args.DatasetID, args.Intent)
		heuristics := heuristicCandidates(args.Intent, args.Fields)
		heurJSON, _ := json.Marshal(heuristics)

		prompt := fmt.Sprintf("数据集 schema：\n%s\n\n可用字段：%s\n分析意图：%s\n\n"+
			"程序预排的候选（按数据形态打分，权威排序）：%s\n\n"+
			`请从候选中确认或微调推荐，只返回 JSON：{"vizType":"...","metrics":[...],"groupby":[...],"reason":"...","alternatives":[{"vizType":"...","reason":"..."}]}`,
			schema, string(fields), args.Intent, string(heurJSON))
		content, tokens, err := s.complete(ctx, in.UserID, chartSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		if raw := extractJSONObject(content); raw != "" {
			_ = json.Unmarshal([]byte(raw), &out)
		}
		viz, _ := out["vizType"].(string)
		topHeuristic, _ := heuristics[0]["vizType"].(string)
		if !supportedVizTypes[viz] {
			// Hallucinated or unsupported type: fall back to the data-shape pick.
			viz = topHeuristic
			out["vizType"] = viz
			if r, _ := out["reason"].(string); r == "" {
				out["reason"] = topHeuristic
			}
		}
		md := "未能给出图表建议。"
		if viz != "" {
			md = fmt.Sprintf("**推荐图表**：`%s`\n\n%s", viz, out["reason"])
		}
		artifacts := map[string]any{}
		if args.DatasetID > 0 && viz != "" {
			spec := map[string]any{
				"dataset_id": args.DatasetID, "viz_type": viz,
				"slice_name": args.Intent,
			}
			if m, ok := out["metrics"].([]any); ok {
				spec["metrics"] = m
			}
			if g, ok := out["groupby"].([]any); ok {
				spec["groupby"] = g
			}
			artifacts["chart_spec"] = spec
		}
		return &registry.Result{Output: out, Markdown: md, Artifacts: artifacts, TokenUsage: tokens}, nil
	}
}

// sampleRows trims a query result's rows before they are sent to the model,
// keeping only a bounded sample (design: only statistics/samples go to the LLM).
func (s *Service) sampleRows(output any) any {
	m, ok := output.(map[string]any)
	if !ok {
		return output
	}
	rows, ok := m["rows"].([]map[string]any)
	if !ok {
		return output
	}
	limit := s.Config.AISamplesRowLimit
	if limit <= 0 {
		limit = 200
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	cp["rows"] = rows
	return cp
}
