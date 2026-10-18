package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/ai/registry"
	"superset/internal/models"
)

// Audit persists the three audit layers the design mandates (§11.2): the run,
// every capability call, and every model call. Writes are best-effort — the AI
// plane must never break the BI main path — so failures only warn.
type Audit struct {
	db  *gorm.DB
	log *zap.Logger
}

// NewAudit builds the audit writer.
func NewAudit(db *gorm.DB, log *zap.Logger) *Audit { return &Audit{db: db, log: log} }

// StartRun opens an ai_agent_run row. The returned run always has a usable
// in-memory value; ID stays 0 when the insert fails and the run still proceeds.
func (a *Audit) StartRun(ctx context.Context, userID int, sessionID string, actx registry.AIContext, question string) *models.AIAgentRun {
	run := &models.AIAgentRun{
		UserID:      userID,
		SessionID:   sessionID,
		Scope:       actx.Scope,
		DashboardID: actx.DashboardID,
		ChartID:     actx.ChartID,
		DatasetID:   actx.DatasetID,
		Question:    question,
		Status:      "running",
	}
	if a == nil || a.db == nil {
		return run
	}
	if err := a.db.WithContext(ctx).Create(run).Error; err != nil {
		a.warn("ai run create failed", err)
		run.ID = 0
	}
	return run
}

// FinishRun closes an ai_agent_run row with its final status, counters and answer.
func (a *Audit) FinishRun(ctx context.Context, run *models.AIAgentRun, status string, tokenUsage, steps, toolCalls int, finalAnswer, errMsg string, latencyMS int) {
	if run == nil {
		return
	}
	run.Status = status
	run.TokenUsage = tokenUsage
	run.StepCount = steps
	run.ToolCallCount = toolCalls
	run.FinalAnswer = finalAnswer
	run.ErrorMsg = errMsg
	run.LatencyMS = latencyMS
	if a == nil || a.db == nil || run.ID == 0 {
		return
	}
	if err := a.db.WithContext(ctx).Save(run).Error; err != nil {
		a.warn("ai run update failed", err)
	}
}

// SavePlan persists the structured plan a run committed to. Keeping it on the
// run row means a reconnect replays the same plan instead of planning again,
// and post-hoc review can answer "why did it take this route" (audit §4-1/§4-5).
func (a *Audit) SavePlan(ctx context.Context, run *models.AIAgentRun, plan PlanEvent) {
	if run == nil || a == nil || a.db == nil || run.ID == 0 {
		return
	}
	b, err := json.Marshal(plan)
	if err != nil {
		a.warn("ai run plan marshal failed", err)
		return
	}
	run.PlanJSON = string(b)
	if err := a.db.WithContext(ctx).Model(run).Update("plan_json", run.PlanJSON).Error; err != nil {
		a.warn("ai run plan update failed", err)
	}
}

// LogToolCall records one skill/tool invocation with its args, result, write
// snapshot and target ids (the rollback/责任认定 material).
func (a *Audit) LogToolCall(ctx context.Context, runID, stepNo int, kind, name, sideEffect string, args json.RawMessage, result, snapshot any, targetIDs []int, status, errMsg string, durationMS int) {
	if a == nil || a.db == nil || runID == 0 {
		return
	}
	row := &models.AIToolCallLog{
		RunID:      runID,
		StepNo:     stepNo,
		Kind:       kind,
		Name:       name,
		SideEffect: sideEffect,
		InputArgs:  marshalCapped(args, toolLogMaxBytes),
		Result:     marshalCapped(result, toolLogMaxBytes),
		// Snapshots are never truncated: rollback depends on the full pre-image.
		Snapshot:   marshalJSON(snapshot),
		TargetIDs:  marshalJSON(targetIDs),
		QueryID:    queryIDOf(result),
		Status:     status,
		ErrorMsg:   errMsg,
		DurationMS: durationMS,
	}
	if err := a.db.WithContext(ctx).Create(row).Error; err != nil {
		a.warn("ai tool call log failed", err)
	}
}

// LLMCall is one model invocation as recorded by the audit layer.
type LLMCall struct {
	RunID       int
	UserID      int
	Operation   string
	Prompt      string
	Model       string
	TokenUsage  int
	Temperature float64
	MaxTokens   int
	Seed        int
	LatencyMS   int
	CacheHit    bool
	ErrMsg      string
}

// LogLLMCall records one model call (operation, prompt digest, model, tokens,
// sampling parameters, error).
func (a *Audit) LogLLMCall(ctx context.Context, call LLMCall) {
	if a == nil || a.db == nil {
		return
	}
	var rid *int
	if call.RunID != 0 {
		rid = &call.RunID
	}
	row := &models.AIAuditLog{
		RunID:       rid,
		UserID:      call.UserID,
		Operation:   call.Operation,
		Prompt:      call.Prompt,
		ModelName:   call.Model,
		TokenUsage:  call.TokenUsage,
		Temperature: call.Temperature,
		MaxTokens:   call.MaxTokens,
		Seed:        call.Seed,
		PromptHash:  hashText(call.Prompt),
		LatencyMS:   call.LatencyMS,
		CacheHit:    call.CacheHit,
		ErrorMsg:    call.ErrMsg,
	}
	if err := a.db.WithContext(ctx).Create(row).Error; err != nil {
		a.warn("ai audit log failed", err)
	}
}

func (a *Audit) warn(msg string, err error) {
	if a != nil && a.log != nil {
		a.log.Warn(msg, zap.Error(err))
	}
}

// WriteCalls returns a run's write tool calls that carry a pre-image snapshot,
// newest first, so a rollback can undo them in reverse order.
func (a *Audit) WriteCalls(ctx context.Context, runID int) ([]models.AIToolCallLog, error) {
	if a == nil || a.db == nil {
		return nil, nil
	}
	var rows []models.AIToolCallLog
	err := a.db.WithContext(ctx).
		Where("run_id = ? AND side_effect IN ? AND snapshot IS NOT NULL AND snapshot <> ''", runID, []string{"write", "destructive"}).
		Order("step_no DESC").Find(&rows).Error
	return rows, err
}

// marshalJSON renders a value as a JSON string for the jsonb columns, returning
// "" for nil so empty columns stay empty.
// marshalJSON renders v for a jsonb column. It must never return "" — an empty
// string is not valid jsonb and Postgres rejects the whole row (SQLSTATE
// 22P02), which silently drops the audit record. "null" is the honest
// representation of "no value".
func marshalJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return "null"
	}
	return string(b)
}

// toolLogMaxBytes caps one jsonb payload in ai_tool_call_log. A 5000-row query
// result is 200–400 KB; logging it verbatim on every call bloats the metadata
// database and write amplification grows with each Agent run. The full result
// stays available through the query audit (query_id) and the SSE stream.
const toolLogMaxBytes = 16 << 10

// marshalCapped renders v as JSON, degrading to a valid-JSON envelope that
// keeps a preview when the payload exceeds limit. Like marshalJSON it always
// returns valid JSON: an empty json.RawMessage (a tool invoked with no
// arguments) previously produced "" and the row was rejected by Postgres.
func marshalCapped(v any, limit int) string {
	if v == nil {
		return "null"
	}
	if raw, ok := v.(json.RawMessage); ok && len(bytes.TrimSpace(raw)) == 0 {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return "null"
	}
	if len(b) <= limit {
		return string(b)
	}
	preview := string(b[:limit])
	// Trim to a valid rune boundary so the preview survives json.Marshal.
	for len(preview) > 0 && !utf8.ValidString(preview) {
		preview = preview[:len(preview)-1]
	}
	envelope := map[string]any{
		"_truncated": true,
		"_bytes":     len(b),
		"_preview":   preview,
	}
	eb, err := json.Marshal(envelope)
	if err != nil {
		return `{"_truncated":true}`
	}
	return string(eb)
}

// queryIDOf pulls the query audit id out of a query_executor result so the tool
// log can be joined back to the query table without duplicating the rows.
func queryIDOf(result any) *int {
	m, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"query_id", "queryId"} {
		switch v := m[key].(type) {
		case int:
			if v > 0 {
				return &v
			}
		case float64:
			if v > 0 {
				id := int(v)
				return &id
			}
		case json.Number:
			if id, err := v.Int64(); err == nil && id > 0 {
				n := int(id)
				return &n
			}
		}
	}
	return nil
}

// hashText returns the SHA-256 fingerprint used for prompt/SQL equivalence.
func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
