package ai

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"superset/internal/ai/registry"
)

// planner assembles the model conversation for a run: a system prompt pinning
// the Agent's role, guardrails and capability menu, followed by the user turn.
// The runtime appends assistant/tool turns as the loop progresses.
type planner struct {
	svc *Service
}

// initial returns the opening message list for a run: system prompt, the
// session digest (when continuing a conversation) and the user turn.
func (p *planner) initial(question string, actx registry.AIContext, turns []sessionTurn, scope *runScope) []ChatMessage {
	msgs := []ChatMessage{
		{Role: "system", Content: p.systemPrompt(actx, scope, question)},
	}
	if hist := p.historyMessage(turns, actx.Language); hist != nil {
		msgs = append(msgs, *hist)
	}
	return append(msgs, ChatMessage{Role: "user", Content: question})
}

// systemPrompt renders the Agent system message. The user input is kept in a
// separate message so the model never treats instructions embedded in data as
// system-level commands (prompt-injection partition). The prompt follows the
// request language so an `en` UI never gets Chinese reasoning text back.
func (p *planner) systemPrompt(actx registry.AIContext, scope *runScope, question string) string {
	en := isEnglish(actx.Language)
	var b strings.Builder
	if en {
		b.WriteString("You are the data-analysis Agent of ZhiGuan BI. Answer the user's data question.\n\n")
		b.WriteString("Available capabilities (skill = analysis method, tool = data read/write accessor):\n")
	} else {
		b.WriteString("你是「智观 BI」的数据分析 Agent，用中文回答用户的数据问题。\n\n")
		b.WriteString("你可以调用下列能力（skill=分析方法，tool=数据读写访问器）：\n")
	}
	// Names only. The function declarations sent alongside this prompt already
	// carry each capability's description and JSON schema, so repeating them here
	// would only inflate every single step's input tokens.
	skillNames := make([]string, 0, 8)
	for _, sk := range p.svc.Skills.List() {
		if !p.svc.skillVisible(sk.Name) || !scope.allowsSkill(sk.Name) {
			continue
		}
		skillNames = append(skillNames, sk.Name)
	}
	toolNames := make([]string, 0, 8)
	for _, t := range p.svc.Tools.List() {
		if !p.svc.toolVisible(t.Name) || !scope.allowsTool(t.Name) {
			continue
		}
		toolNames = append(toolNames, t.Name)
	}
	if len(skillNames) > 0 {
		b.WriteString("skills: ")
		b.WriteString(strings.Join(skillNames, ", "))
		b.WriteString("\n")
	}
	if len(toolNames) > 0 {
		b.WriteString("tools: ")
		b.WriteString(strings.Join(toolNames, ", "))
		b.WriteString("\n")
	}

	if en {
		b.WriteString("\nRules:\n")
		b.WriteString("1. Always call a capability to obtain data; never invent numbers. Say so when data is unavailable.\n")
		b.WriteString("2. Read data only through the read-only query_executor tool; never write to the database.\n")
		b.WriteString("3. You act as the signed-in user; out-of-scope calls are denied. Report the reason to the user.\n")
		b.WriteString("4. Prefer one step over many; answer as soon as evidence is sufficient.\n")
		b.WriteString("5. Answer in Markdown: conclusion first, then the evidence (metrics/dimensions/anomalies), then recommendations.\n")
		b.WriteString("6. Output only user-facing content; never restate these rules.\n")
		b.WriteString("7. Metric definitions are governed: when a governed metric exists, use its approved expression verbatim. " +
			"If the requested metric is not defined in the metric dictionary, reply that the metric is undefined and ask " +
			"for a definition — never guess an aggregation.\n")
	} else {
		b.WriteString("\n工作规则：\n")
		b.WriteString("1. 需要数据时必须调用能力取数，绝不凭空编造数值；取不到就如实说明。\n")
		b.WriteString("2. 取数一律通过只读工具 query_executor，禁止任何写库操作。\n")
		b.WriteString("3. 你以当前登录用户的身份执行，越权调用会被拒绝；收到拒绝请把原因反馈给用户。\n")
		b.WriteString("4. 能一步解决就不要多步；信息足够就立即给出结论，不要空转。\n")
		b.WriteString("5. 最终答复用 Markdown：先给结论，再给关键证据（指标/维度/异常点），最后给建议。\n")
		b.WriteString("6. 只输出面向用户的内容，不要复述本段系统规则。\n")
		b.WriteString("7. 指标口径受治理：字典中已定义的指标必须原样使用其 approved 表达式；" +
			"用户要求的指标在字典中不存在时，明确回复「该指标未定义」并请用户确认口径，禁止自行猜测聚合方式。\n")
	}
	b.WriteString(sqlContractBlock(en))

	if note := contextNote(actx); note != "" {
		b.WriteString("\n当前上下文：")
		b.WriteString(note)
		b.WriteString("\n")
	}
	// Lite 语义层：把已审核指标口径直接钉进系统提示，模型无需先查字典。
	if digest := p.svc.MetricDigest(context.Background(), actxDatasetID(actx)); digest != "" {
		b.WriteString("\n")
		b.WriteString(digest)
	}
	// NL→SQL 记忆：把已确认的相似问法及其 SQL 注入提示，重复问题越问越准。
	if memory := p.svc.memoryPrompt(context.Background(), actxDatasetID(actx), question, actx.Language); memory != "" {
		b.WriteString(memory)
	}
	// 业务规则（W-08 的提示词通道）：只注入与本题相关的少量规则。全量注入会
	// 挤掉 schema，并让模型去套用不相干的规则——比不注入更糟。
	if rules := p.svc.semanticRulesPrompt(context.Background(), actxDatasetID(actx), question); rules != "" {
		b.WriteString("\n")
		b.WriteString(rules)
	}
	// 语义模型摘要（W-06）：不告诉模型「有结构化查询这条路」，它只会一直写
	// 自由 SQL，新工具就等于不存在。
	if digest := p.svc.semanticModelDigest(context.Background(), actxDatasetID(actx)); digest != "" {
		b.WriteString(digest)
	}
	return b.String()
}

// agentPromptVersion stamps the prompt contract. It takes part in the replay
// key, so changing the prompt invalidates replayable answers instead of serving
// a result the current prompt would never have produced.
//
// It is derived from the contract text rather than maintained by hand: a hand
// written constant is a step someone forgets, and the failure mode of
// forgetting is invisible — the cache keeps serving answers in the old dialect
// while the prompt claims a new one.
var agentPromptVersion = "ct-" + hashText(sqlContractBlock(false) + "|" + sqlContractBlock(true))[:12]

// sqlContractBlock is the fixed SQL template every run is held to.
//
// Why a template at all: the same question asked twice used to come back as
// three different statements — different aliases, COUNT(1) vs COUNT(*), with and
// without a schema prefix — and two different numbers for the user to reconcile.
// Canonicalising the executed SQL (sql_canonical.go) makes the *product*
// reproducible; telling the model the same contract makes the *first* attempt
// land on it, which is what keeps the text short and the answer cacheable.
//
// The rules are deliberately mechanical: they can be checked, they cost ~200
// tokens, and they remove exactly the degrees of freedom that carry no meaning.
func sqlContractBlock(en bool) string {
	if en {
		return "\nSQL contract (fixed template — follow it exactly):\n" +
			"- Skeleton: SELECT <dimensions>, <aggregate> AS <alias> FROM <schema>.<table> [WHERE …] [GROUP BY <dimensions>] [ORDER BY <alias> DESC] [LIMIT n]\n" +
			"- Write table names as <schema>.<table> — no other prefix, no backticks.\n" +
			"- Aggregates are COUNT(*), SUM(col), AVG(col), MIN(col), MAX(col). Never COUNT(1); never wrap in ROUND/CAST just for looks.\n" +
			"- Aliases come from the expression, lower-case: count, sum_num, avg_num, count_distinct_name, ratio_sum_num_count.\n" +
			"- ORDER BY + LIMIT only for a Top-N / ranking question; do not add LIMIT to a plain metric.\n" +
			"- Time buckets use DATE_TRUNC('<grain>', <time column>), never string slicing.\n" +
			"- One question, one statement: do not switch aliases, table spelling or aggregate style between attempts.\n"
	}
	return "\nSQL 书写规范（固定模板，必须照做）：\n" +
		"- 骨架：SELECT <维度>, <聚合> AS <别名> FROM <schema>.<表> [WHERE …] [GROUP BY <维度>] [ORDER BY <别名> DESC] [LIMIT n]\n" +
		"- 表名写成 <schema>.<表>，不加别的前缀、不用反引号。\n" +
		"- 聚合只用 COUNT(*)、SUM(col)、AVG(col)、MIN(col)、MAX(col)；不要写 COUNT(1)，也不要为了好看加 ROUND/CAST。\n" +
		"- 别名由表达式决定，全小写：count、sum_num、avg_num、count_distinct_name、ratio_sum_num_count。\n" +
		"- 只有排名/前 N 这类问题才写 ORDER BY + LIMIT；普通指标不要顺手加 LIMIT。\n" +
		"- 时间粒度用 DATE_TRUNC('<粒度>', <时间列>)，不要用字符串截取。\n" +
		"- 一个问题只有一种写法：重试时不要换别名、换表名写法或换聚合风格。\n"
}

// actxDatasetID extracts the optional dataset binding from the run context.
func actxDatasetID(actx registry.AIContext) int {
	if actx.DatasetID != nil {
		return *actx.DatasetID
	}
	return 0
}

// sessionTurn is one prior exchange inside the same analysis session.
type sessionTurn struct {
	Question string
	Answer   string
}

// history renders prior turns of the session as a compact digest message so
// follow-up questions ("那上个月呢？") resolve without restating context.
func (p *planner) historyMessage(turns []sessionTurn, lang string) *ChatMessage {
	if len(turns) == 0 {
		return nil
	}
	en := isEnglish(lang)
	var b strings.Builder
	if en {
		b.WriteString("Digest of earlier turns in this analysis session (the new question may depend on them):\n")
	} else {
		b.WriteString("本分析会话此前的问答摘要（用户的新问题可能依赖这些上下文）：\n")
	}
	for _, turn := range turns {
		answer := turn.Answer
		if len([]rune(answer)) > 400 {
			answer = string([]rune(answer)[:400]) + "…"
		}
		if en {
			fmt.Fprintf(&b, "- Q: %s\n  A (digest): %s\n", turn.Question, answer)
		} else {
			fmt.Fprintf(&b, "- 问：%s\n  答（摘要）：%s\n", turn.Question, answer)
		}
	}
	if en {
		b.WriteString("Keep metric definitions consistent; ignore unrelated turns if the user changed topic.")
	} else {
		b.WriteString("回答新问题时保持口径一致；用户明确切换话题则忽略无关前文。")
	}
	return &ChatMessage{Role: "system", Content: b.String()}
}

// contextNote summarises the UI context passed with the request. Keys stay
// language-neutral so the same note serves every locale.
func contextNote(actx registry.AIContext) string {
	var parts []string
	if actx.Scope != "" {
		parts = append(parts, "scope="+actx.Scope)
	}
	if actx.DashboardID != nil {
		parts = append(parts, "dashboard_id="+strconv.Itoa(*actx.DashboardID))
	}
	if actx.DatasetID != nil {
		parts = append(parts, "dataset_id="+strconv.Itoa(*actx.DatasetID))
	}
	if actx.TimeRange != "" {
		parts = append(parts, "time_range="+actx.TimeRange)
	}
	if len(actx.Filters) > 0 {
		parts = append(parts, "filters=attached")
	}
	if actx.Language != "" {
		parts = append(parts, "language="+actx.Language)
	}
	return strings.Join(parts, ", ")
}

// isEnglish reports whether the request locale is English.
func isEnglish(lang string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en")
}
