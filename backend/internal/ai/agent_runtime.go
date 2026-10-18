package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"superset/internal/ai/registry"
	"superset/internal/models"
)

// Runtime drives a single Agent run: plan → act → observe → answer, bounded by
// the cost/step guardrails in the design (§9.5). It is stateless; every call
// builds its own audit trail.
type Runtime struct {
	svc *Service
}

// Runtime returns the Agent runtime owned by this service.
func (s *Service) Runtime() *Runtime { return &Runtime{svc: s} }

// RunResult summarises a finished run (also delivered as the answer event).
type RunResult struct {
	RunID      int
	SessionID  string
	Status     string // done|stopped|failed
	Markdown   string
	Artifacts  map[string]any
	StepCount  int
	TokenUsage int
	Error      string
	// Observability counters (design §9.7).
	FirstTokenMS     int
	ToolErrorCount   int
	EmptyResultCount int
	SelfCheckCount   int
	SQLHash          string
}

// RunInput is one Agent invocation. It is a struct (rather than the positional
// arguments `Run` grew with) because a resumed run needs extra context: the
// transcript it already produced before the connection dropped.
type RunInput struct {
	Question  string
	SessionID string
	AgentID   int
	Context   registry.AIContext
	Emit      func(Event)
	// Resume, when set, continues a previous run instead of starting cold.
	Resume *ResumeSeed
}

// ResumeSeed carries everything a continuation run must not recompute: the
// plan it already committed to and the observations it already gathered.
type ResumeSeed struct {
	FromRunID int
	Plan      *PlanEvent
	Steps     []models.AIToolCallLog
}

// Run executes one Agent run. emit, when non-nil, receives SSE events in order.
// agentID selects the configured agent; 0 uses the default agent. sessionID ties
// consecutive runs into one analysis session so follow-ups inherit the prior
// turns' conclusions; an empty id starts a fresh session.
func (r *Runtime) Run(ctx context.Context, userID int, sessionID, question string, agentID int, actx registry.AIContext, emit func(Event)) *RunResult {
	return r.execute(ctx, userID, RunInput{
		Question: question, SessionID: sessionID, AgentID: agentID, Context: actx, Emit: emit,
	})
}

// Resume continues a run whose stream was interrupted. The persisted plan is
// replayed (so the user sees the same route, not a second opinion) and the
// already-completed observations are replayed into the model context, which is
// what makes "reconnect and carry on" cheaper than re-asking the question.
func (r *Runtime) Resume(ctx context.Context, userID int, in RunInput) *RunResult {
	return r.execute(ctx, userID, in)
}

// execute is the shared plan→act→observe→answer loop.
func (r *Runtime) execute(ctx context.Context, userID int, in RunInput) *RunResult {
	s := r.svc
	res := &RunResult{Status: "done"}

	emit := in.Emit
	sessionID := in.SessionID
	question := in.Question
	actx := in.Context

	if !s.Enabled() {
		res.Status = "failed"
		res.Error = ErrLLMDisabled.Error()
		emitEvent(emit, EventError, ErrorEvent{Message: res.Error})
		return res
	}

	// Resolve the agent, its provider and its capability scope up front so the
	// whole run — prompt, function menu, gates and guardrails — follows one
	// plug-in configuration.
	agent, pc, scope, err := s.resolveRun(ctx, in.AgentID)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		emitEvent(emit, EventError, ErrorEvent{Message: res.Error})
		return res
	}

	audit := s.Audit
	run := audit.StartRun(ctx, userID, sessionID, actx, question)
	if in.Resume != nil && in.Resume.FromRunID > 0 {
		from := in.Resume.FromRunID
		run.ResumedFrom = &from
	}
	res.RunID = run.ID
	res.SessionID = sessionID

	runCtx := withRunScope(ctx, scope)
	if timeout := r.timeout(agent); timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, timeout)
		defer cancel()
	}

	started := time.Now()

	// Structured plan first: one cheap call that names the steps, so the user
	// sees a real plan instead of a fixed sentence and the run is auditable.
	// A resumed run replays its persisted plan rather than planning anew.
	var plan PlanEvent
	planTokens := 0
	if in.Resume != nil && in.Resume.Plan != nil {
		plan = *in.Resume.Plan
		plan.RunID = run.ID
		plan.SessionID = sessionID
		plan.Resumed = true
		emitEvent(emit, EventPlan, plan)
	} else {
		plan, planTokens = r.plan(runCtx, pc, userID, run.ID, question, actx, scope)
		plan.SessionID = sessionID
		emitEvent(emit, EventPlan, plan)
		audit.SavePlan(runCtx, run, plan)
	}
	tokenUsed := planTokens

	pl := &planner{svc: s}
	msgs := pl.initial(question, actx, s.sessionHistory(ctx, userID, sessionID), scope)
	tools := s.ToolDeclarations(scope)

	maxSteps := r.maxSteps(agent)
	maxToolCalls := r.maxToolCalls(agent)
	tokenBudget := r.tokenBudget(agent)

	var steps, toolCalls int
	var final string
	var failure error
	stopped := false
	runArtifacts := map[string]any{}

	// Observability (design §9.7): counters the panel surfaces per run.
	var firstTokenMS, toolErrors, emptyResults, selfChecks int
	var lastSQL string
	// lastRows is the row count of the last successful SQL the run produced. It
	// decides whether the run is worth remembering as an NL→SQL example.
	lastRows := 0
	// Duplicate detector: (tool, args) → step that already produced the result.
	seen := map[string]int{}

	// Reconnect: hand the model the work it already did, and replay those
	// steps to the client so the rebuilt transcript matches what it lost.
	//
	// A continuation leg gets its own configured budget: a run that spent its
	// allowance on the first leg would otherwise be "resumable" only in name.
	// The duplicate detector above still prevents the model from re-spending on
	// an identical (tool, args) pair, so the extra budget is not free money.
	baseToolCalls := 0
	if in.Resume != nil && len(in.Resume.Steps) > 0 {
		msgs = append(msgs, seedTranscript(in.Resume.Steps, actx.Language, emit, obsLimitsFor(tokenBudget-tokenUsed))...)
		for i := range in.Resume.Steps {
			c := &in.Resume.Steps[i]
			stepNo := i + 1
			toolCalls = stepNo
			seen[toolFingerprint(c.Name, json.RawMessage(c.InputArgs))] = stepNo
			if c.Status != "ok" {
				toolErrors++
			}
			if sql := sqlOf(decodeLogResult(c.Result)); sql != "" {
				lastSQL = sql
			}
		}
		baseToolCalls = toolCalls
	}

	for steps < maxSteps {
		if runCtx.Err() != nil {
			stopped = true
			break
		}
		steps++

		resp, err := s.LLM.Chat(runCtx, pc, &ChatRequest{Messages: msgs, Tools: tools, UserID: userID})
		if err != nil {
			failure = err
			break
		}
		if firstTokenMS == 0 {
			firstTokenMS = int(time.Since(started).Milliseconds())
		}
		// A cache hit reuses a response whose cost was already paid by the run
		// that populated the cache, so it must not consume this run's budget.
		if !resp.Cached {
			tokenUsed += resp.TokenUsage
		}
		audit.LogLLMCall(runCtx, LLMCall{
			RunID: run.ID, UserID: userID, Operation: "agent_step", Prompt: question,
			Model: resp.Model, TokenUsage: resp.TokenUsage, Temperature: resp.Temperature,
			MaxTokens: resp.MaxTokens, Seed: resp.Seed, LatencyMS: resp.LatencyMS, CacheHit: resp.Cached,
		})
		if run.ModelName == "" {
			run.ModelName, run.Temperature, run.Seed = resp.Model, resp.Temperature, resp.Seed
		}

		// No call requested: the model produced its final answer. This is checked
		// before the budget gate on purpose — an answer the model already paid
		// for must never be discarded just because it tipped the limit.
		if len(resp.ToolCalls) == 0 {
			final = strings.TrimSpace(resp.Content)
			break
		}

		// Early stop: a single turn may blow the whole budget.
		if tokenUsed > tokenBudget {
			stopped = true
			break
		}

		msgs = append(msgs, ChatMessage{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})

		fanoutStop := false
		for _, tc := range resp.ToolCalls {
			if toolCalls-baseToolCalls >= maxToolCalls {
				stopped = true
				fanoutStop = true
				break
			}
			kind := s.capabilityKind(tc.Function.Name)
			args := json.RawMessage(tc.Function.Arguments)

			// Same tool + same args second time: do not spend another call or
			// another row of audit; hand the model its own earlier result back.
			fingerprint := toolFingerprint(tc.Function.Name, args)
			if prevStep, dup := seen[fingerprint]; dup {
				dupObs := fmt.Sprintf(`{"status":"duplicate","message":"与第 %d 步完全相同的调用，结果已在上下文中，请直接使用该结果或更换策略"}`, prevStep)
				emitEvent(emit, EventObserve, ObserveEvent{StepNo: toolCalls, Status: "duplicate", Error: dupObs})
				msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: dupObs})
				continue
			}

			toolCalls++
			seen[fingerprint] = toolCalls
			emitEvent(emit, EventAct, ActEvent{StepNo: toolCalls, Kind: kind, Name: tc.Function.Name, Args: args})

			out, oerr := r.invoke(runCtx, audit, run.ID, userID, kind, tc.Function.Name, args, actx, toolCalls)

			// Skill artifacts (chart_spec, sql, page_spec …) flow back to the
			// host page through the answer event so "保存为图表" can act on them.
			if kind == "skill" {
				if res, ok := out.(*registry.Result); ok {
					if len(res.Artifacts) > 0 {
						for k, v := range res.Artifacts {
							runArtifacts[k] = v
						}
					}
					// A skill runs its own model calls (nl2sql/insight/chart_design
					// …). Their usage has to count toward the run budget, otherwise
					// skills silently blow past the cost guardrail the agent asked
					// for and the reported token total is misleadingly low.
					tokenUsed += res.TokenUsage
				}
			}
			if sql := sqlOf(out); sql != "" {
				lastSQL = sql
				lastRows = rowsOf(out)
			}

			obs := ObserveEvent{StepNo: toolCalls, Status: "ok", Result: out}
			if oerr != nil {
				obs.Status = observationStatus(oerr)
				obs.Error = oerr.Error()
				toolErrors++
			}
			emitEvent(emit, EventObserve, obs)

			hint := resultHint(tc.Function.Name, out, actx.Language)
			if hint != "" {
				selfChecks++
			}
			if isEmptyResult(out) {
				emptyResults++
			}
			msgs = append(msgs, ChatMessage{
				Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
				Content: observationText(out, oerr, hint, obsLimitsFor(tokenBudget-tokenUsed)),
			})
		}
		if fanoutStop {
			break
		}
	}

	status := "done"
	errMsg := ""
	switch {
	case failure != nil:
		status, errMsg = "failed", failure.Error()
	case stopped || final == "":
		status = "stopped"
	}
	if final == "" {
		final = fallbackAnswer(status, errMsg, actx.Language)
	}

	latency := int(time.Since(started).Milliseconds())
	run.FirstTokenMS = firstTokenMS
	run.ToolErrorCount = toolErrors
	run.EmptyResultCount = emptyResults
	run.SelfCheckCount = selfChecks
	run.GeneratedSQL = lastSQL
	if lastSQL != "" {
		run.SQLHash = hashText(lastSQL)
		// Publish the SQL that actually ran as an artifact. Without it the chat
		// page cannot offer "保存为范例" (or copy the SQL) for a run that queried
		// through query_executor instead of a skill.
		runArtifacts["sql"] = lastSQL
	}
	audit.FinishRun(runCtx, run, status, tokenUsed, steps, toolCalls, final, errMsg, latency)

	// NL→SQL memory (W-03): a successful, non-exploratory run is captured for
	// review. It stays out of recall until approved, so a wrong example cannot
	// teach the model a wrong join.
	if status == "done" {
		s.autoCaptureMemory(runCtx, run, lastSQL, lastRows)
	}

	res.Status = status
	res.Markdown = final
	res.StepCount = steps
	res.TokenUsage = tokenUsed
	res.Error = errMsg
	res.Artifacts = runArtifacts
	res.FirstTokenMS = firstTokenMS
	res.ToolErrorCount = toolErrors
	res.EmptyResultCount = emptyResults
	res.SelfCheckCount = selfChecks
	res.SQLHash = run.SQLHash

	if failure != nil {
		emitEvent(emit, EventError, ErrorEvent{RunID: run.ID, Message: errMsg})
	}
	emitEvent(emit, EventAnswer, AnswerEvent{
		RunID: run.ID, SessionID: sessionID, Markdown: final, Artifacts: res.Artifacts,
		TokenUsage: tokenUsed, StepCount: steps,
		FirstTokenMS: firstTokenMS, ToolErrorCount: toolErrors,
		EmptyResultCount: emptyResults, SelfCheckCount: selfChecks,
	})
	return res
}

// toolFingerprint identifies a (tool, args) pair so an identical repeat inside
// the same run can be short-circuited instead of burning budget.
func toolFingerprint(name string, args json.RawMessage) string {
	return name + "\x00" + hashText(string(args))
}

// sqlOf extracts the SQL a tool ran, when the result carries one.
func sqlOf(out any) string {
	if res, ok := out.(*registry.Result); ok && res != nil {
		if s, ok := res.Artifacts["sql"].(string); ok {
			return s
		}
	}
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["sql"].(string); ok {
		return s
	}
	return ""
}

// isEmptyResult reports whether a data tool returned zero rows.
func isEmptyResult(out any) bool {
	m, ok := out.(map[string]any)
	if !ok {
		return false
	}
	rows, ok := m["rows"].([]map[string]any)
	return ok && len(rows) == 0
}

// rowsOf extracts the row count a data tool reported.
func rowsOf(out any) int {
	m, ok := out.(map[string]any)
	if !ok {
		return 0
	}
	switch v := m["rowcount"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	if rows, ok := m["rows"].([]map[string]any); ok {
		return len(rows)
	}
	return 0
}

// invoke runs one capability through the permission/audit guard and records an
// ai_tool_call_log row for it.
func (r *Runtime) invoke(ctx context.Context, audit *Audit, runID, userID int, kind, name string, args json.RawMessage, actx registry.AIContext, stepNo int) (any, error) {
	started := time.Now()
	var (
		out        any
		sideEffect = registry.Read.String()
		snapshot   any
		targetIDs  []int
		err        error
	)

	switch kind {
	case "skill":
		var res *registry.Result
		res, err = r.svc.InvokeSkill(ctx, userID, runID, name, args, actx)
		if res != nil {
			out = res
		}
	default:
		var res *registry.ToolResult
		res, err = r.svc.InvokeTool(ctx, userID, runID, name, args)
		if res != nil {
			sideEffect = res.SideEffect.String()
			snapshot = res.Snapshot
			targetIDs = res.TargetIDs
			out = res.Output
		}
	}

	status := "ok"
	errMsg := ""
	if err != nil {
		status, errMsg = observationStatus(err), err.Error()
	}
	audit.LogToolCall(ctx, runID, stepNo, kind, name, sideEffect, args, out, snapshot, targetIDs, status, errMsg, int(time.Since(started).Milliseconds()))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// capabilityKind classifies a function name as a skill or a tool.
func (s *Service) capabilityKind(name string) string {
	if _, ok := s.Skills.Get(name); ok {
		return "skill"
	}
	return "tool"
}

// observationStatus maps an invocation error onto the observe event status.
func observationStatus(err error) string {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return "denied"
	default:
		return "error"
	}
}

// Limits for the model-facing rendering of a tool observation. The UI and the
// audit log still receive the full result; only the text handed to the model is
// bounded, otherwise one wide query would blow the context window and fail the
// run on its very first data step.
//
// The ceilings are deliberately roomy. A financial analysis pulls whole
// statements (三大表) in a single shot, and a fixed 50-row window made them
// unreadable. The cap actually applied is derived from the tokens still
// available in the run (obsLimitsFor), so a large result passes through in full
// while there is room and is squeezed back to a sample only near the budget.
const (
	// obsMinRows keeps a usable sample even when the budget is nearly spent.
	obsMinRows = 50
	// obsMaxRows is the absolute ceiling for one observation.
	obsMaxRows = 5000
	// obsMinBytes is the floor byte allowance for one observation.
	obsMinBytes = 24 << 10
	// obsMaxBytes is the absolute ceiling for one observation.
	obsMaxBytes = 96 << 10
	// obsTokensPerRow converts remaining tokens into a row allowance.
	obsTokensPerRow = 40
	// obsBytesPerRow converts a row allowance into a byte allowance.
	obsBytesPerRow = 160
)

// obsLimits is the per-observation allowance handed to observationText.
type obsLimits struct{ rows, bytes int }

// obsLimitsFor scales the caps with the tokens still available in this run. A
// large table is submitted whole while there is room; once the budget is nearly
// spent the allowance falls back toward the sample floor, so the cost guardrail
// stays meaningful for big-table analysis without ever letting one observation
// overflow the provider's context window.
func obsLimitsFor(remainingTokens int) obsLimits {
	rows := remainingTokens / obsTokensPerRow
	if rows < obsMinRows {
		rows = obsMinRows
	}
	if rows > obsMaxRows {
		rows = obsMaxRows
	}
	bytes := rows * obsBytesPerRow
	if bytes < obsMinBytes {
		bytes = obsMinBytes
	}
	if bytes > obsMaxBytes {
		bytes = obsMaxBytes
	}
	return obsLimits{rows: rows, bytes: bytes}
}

// observationText renders a tool message for the next model turn.
func observationText(v any, err error, hint string, lim obsLimits) string {
	if err != nil {
		return fmt.Sprintf(`{"status":%q,"error":%q}`, observationStatus(err), err.Error())
	}
	v = shrinkObservation(v, lim.rows)
	b, mErr := json.Marshal(v)
	if mErr != nil || len(b) == 0 {
		b = []byte("{}")
	}
	if len(b) > lim.bytes {
		// Wide rows can still overflow after the row cap; degrade to a summary
		// that keeps the shape (columns + counts) but drops the payload.
		if m, ok := v.(map[string]any); ok {
			summary := map[string]any{
				"_truncated": true,
				"rowcount":   m["rowcount"],
				"colnames":   m["colnames"],
				"sql":        m["sql"],
			}
			if rows, ok := m["rows"].([]map[string]any); ok && len(rows) > 0 {
				summary["sample"] = rows[:5]
			}
			if sb, sErr := json.Marshal(summary); sErr == nil {
				b = sb
			}
		}
	}
	if len(b) > lim.bytes {
		b = truncateObservation(b, lim.bytes)
	}
	if hint == "" {
		return string(b)
	}
	// The self-check hint rides on the observation so the model gets one
	// bounded, grounded retry opportunity inside the existing step budget.
	return fmt.Sprintf(`{"result":%s,"self_check":%q}`, b, hint)
}

// truncateObservation keeps the head and the tail of an oversized observation,
// so period/trend tables do not lose the rows that carry the latest periods. It
// falls back to a plain note when even the two ends cannot fit.
func truncateObservation(b []byte, max int) []byte {
	half := max / 2
	if wrapped, err := json.Marshal(map[string]any{
		"_truncated": true,
		"note":       "观察结果过大，已保留首尾片段并截断中间",
		"head":       string(b[:half]),
		"tail":       string(b[len(b)-half:]),
	}); err == nil && len(wrapped) <= max {
		return wrapped
	}
	return []byte(`{"_truncated":true,"note":"观察结果过大，未能完整传入模型"}`)
}

// shrinkObservation caps the row array of a query result before it is sent to
// the model, keeping the true row count so the answer can still say "共 N 行".
// Both ends of the result are kept: truncating to the head alone silently drops
// the most recent periods of a time series.
func shrinkObservation(v any, maxRows int) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	rows, ok := m["rows"].([]map[string]any)
	if !ok || len(rows) <= maxRows || maxRows <= 0 {
		return v
	}
	head := maxRows / 2
	tail := maxRows - head
	kept := make([]map[string]any, 0, maxRows)
	kept = append(kept, rows[:head]...)
	kept = append(kept, rows[len(rows)-tail:]...)
	out := make(map[string]any, len(m)+2)
	for k, val := range m {
		out[k] = val
	}
	out["rows"] = kept
	out["_rows_omitted"] = len(rows) - maxRows
	return out
}

// resultHint implements the NL2SQL self-check loop: an empty result from the
// data tools is far more often an over-tight filter or wrong 口径 than a true
// negative, so nudge the model to re-examine before concluding.
func resultHint(name string, out any, lang string) string {
	if name != "query_executor" && name != "result_sampler" {
		return ""
	}
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}
	if rows, ok := m["rows"].([]map[string]any); ok && len(rows) == 0 {
		if isEnglish(lang) {
			return "The query returned 0 rows. Re-check the filters, time range and aggregation before concluding; retry once with a corrected SQL, and only report 'no data' when you are sure."
		}
		return "查询返回 0 行。请检查过滤条件、时间范围或聚合口径是否过严，修正 SQL 后重试一次；确认确实无数据时才如实说明。"
	}
	return ""
}

// sessionHistory loads the last finished turns of this analysis session so a
// follow-up question inherits its predecessors' conclusions (P0-2).
func (s *Service) sessionHistory(ctx context.Context, userID int, sessionID string) []sessionTurn {
	if sessionID == "" || s.DB == nil {
		return nil
	}
	var runs []models.AIAgentRun
	if err := s.DB.WithContext(ctx).
		Where("user_id = ? AND session_id = ? AND status = ? AND final_answer <> ''", userID, sessionID, "done").
		Order("created_on desc").Limit(3).Find(&runs).Error; err != nil {
		return nil
	}
	turns := make([]sessionTurn, 0, len(runs))
	// Oldest first so the digest reads chronologically.
	for i := len(runs) - 1; i >= 0; i-- {
		turns = append(turns, sessionTurn{Question: runs[i].Question, Answer: runs[i].FinalAnswer})
	}
	return turns
}

// fallbackAnswer is used when the loop ends without a model-authored answer.
func fallbackAnswer(status, errMsg, lang string) string {
	if isEnglish(lang) {
		if errMsg != "" {
			return fmt.Sprintf("This run could not finish: %s", errMsg)
		}
		return "This run hit its step or cost limit before producing a conclusion. Narrow the question and retry."
	}
	if errMsg != "" {
		return fmt.Sprintf("本次运行未能完成：%s", errMsg)
	}
	return "本次运行已达到步数或成本上限，未能生成最终结论。可以缩小问题范围后重试。"
}

func (r *Runtime) timeout(agent *models.AIAgent) time.Duration {
	sec := agent.RunTimeoutSec
	if sec <= 0 {
		sec = r.svc.Config.AIAgentRunTimeoutSec
	}
	if sec <= 0 {
		sec = 120
	}
	return time.Duration(sec) * time.Second
}

func (r *Runtime) maxSteps(agent *models.AIAgent) int {
	if n := agent.MaxSteps; n > 0 {
		return n
	}
	if n := r.svc.Config.AIAgentMaxSteps; n > 0 {
		return n
	}
	return 6
}

func (r *Runtime) maxToolCalls(agent *models.AIAgent) int {
	if n := agent.MaxToolCalls; n > 0 {
		return n
	}
	if n := r.svc.Config.AIAgentMaxToolCalls; n > 0 {
		return n
	}
	return 8
}

func (r *Runtime) tokenBudget(agent *models.AIAgent) int {
	if n := agent.TokenBudget; n > 0 {
		return n
	}
	if n := r.svc.Config.AIAgentTokenBudget; n > 0 {
		return n
	}
	return 120000
}

// plan asks the model for a small structured plan before any tool runs. It is
// deliberately cheap (≤4 steps, 400 completion tokens, temperature 0.1) and
// degrades to the generic intent when the provider is unavailable, so planning
// never blocks a run.
func (r *Runtime) plan(ctx context.Context, pc *ProviderConfig, userID, runID int, question string, actx registry.AIContext, scope *runScope) (PlanEvent, int) {
	fallback := PlanEvent{
		RunID:   runID,
		Thought: "理解问题 → 规划取数与分析步骤",
		Source:  "fallback",
	}
	if pc == nil || strings.TrimSpace(question) == "" {
		return fallback, 0
	}

	var b strings.Builder
	if isEnglish(actx.Language) {
		b.WriteString("You are a BI analysis planner. Break the question into 1-4 executable steps. Output ONE JSON object only, no prose, no code fences.\n")
		b.WriteString(`Format: {"thought":"one-line approach","steps":[{"intent":"step intent","dataset":"dataset name or empty","metrics":["metric"],"deps":[step indexes]}]}`)
		b.WriteString("\nAvailable capabilities:")
	} else {
		b.WriteString("你是 BI 数据分析规划器。把用户问题拆成 1-4 个可执行步骤，只输出一个 JSON 对象，不要解释、不要 Markdown 代码块。\n")
		b.WriteString(`格式：{"thought":"一句话总体思路","steps":[{"intent":"步骤意图","dataset":"数据集名或空","metrics":["指标名"],"deps":[依赖的步骤序号]}]}`)
		b.WriteString("\n可用能力：")
	}
	for _, sk := range r.svc.Skills.List() {
		if !r.svc.skillVisible(sk.Name) || !scope.allowsSkill(sk.Name) {
			continue
		}
		b.WriteString(" " + sk.Name)
	}
	userTurn := question
	if note := contextNote(actx); note != "" {
		userTurn += "\n当前上下文：" + note
	}

	temp := 0.1
	resp, err := r.svc.LLM.Chat(ctx, pc, &ChatRequest{
		UserID:      userID,
		Messages:    []ChatMessage{{Role: "system", Content: b.String()}, {Role: "user", Content: userTurn}},
		Temperature: &temp,
		MaxTokens:   400,
		// Planning is a pure function of the question plus the agent's scope, so
		// re-asking the same question (retry / regenerate) reuses the cached plan
		// instead of paying for another model round-trip.
		CacheKey: planCacheKey(scope, question, actx.Language),
	})
	if err != nil {
		r.svc.Log.Debug("agent plan call failed, using fallback", zap.Error(err))
		return fallback, 0
	}
	r.svc.Audit.LogLLMCall(ctx, LLMCall{
		RunID: runID, UserID: userID, Operation: "agent_plan", Prompt: question,
		Model: resp.Model, TokenUsage: resp.TokenUsage, Temperature: resp.Temperature,
		MaxTokens: resp.MaxTokens, Seed: resp.Seed, LatencyMS: resp.LatencyMS, CacheHit: resp.Cached,
	})
	// A cache hit is free: the tokens were spent when the entry was written.
	planCost := 0
	if !resp.Cached {
		planCost = resp.TokenUsage
	}

	var parsed struct {
		Thought string     `json:"thought"`
		Steps   []PlanStep `json:"steps"`
	}
	raw := extractJSONObject(resp.Content)
	if raw == "" || json.Unmarshal([]byte(raw), &parsed) != nil || len(parsed.Steps) == 0 {
		return fallback, planCost
	}
	if len(parsed.Steps) > 4 {
		parsed.Steps = parsed.Steps[:4]
	}
	thought := strings.TrimSpace(parsed.Thought)
	if thought == "" {
		thought = "已生成分析计划"
	}
	return PlanEvent{RunID: runID, Thought: thought, Steps: parsed.Steps, Source: "model"}, planCost
}

// planCacheKey identifies a reusable plan: the same question asked of the same
// agent scope in the same language must yield the same plan, so it is safe to
// serve from the gateway's response cache.
func planCacheKey(scope *runScope, question, lang string) string {
	agentID := 0
	if scope != nil {
		agentID = scope.AgentID
	}
	return fmt.Sprintf("plan|a=%d|lang=%s|%s", agentID, lang, question)
}
