package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"superset/internal/models"
)

// Resume semantics (audit §4-5).
//
// A run is one SSE stream. When the browser tab is refreshed, the network drops
// or a proxy times the stream out, the user previously had exactly one option:
// re-ask the question and pay for the whole trajectory again. The audit's
// acceptance criterion is "reconnect and carry on", so three things persist:
//
//	plan      → ai_agent_run.plan_json   (replayed, never re-planned)
//	steps     → ai_tool_call_log         (replayed into model context + UI)
//	status    → ai_agent_run.status      (running|done|stopped|failed)
//
// ResumeState is the read model behind GET /agent/runs/{id}/resume. It answers
// one question for the client: is this run attachable (still executing), can it
// be continued (partial work worth keeping), or is it simply over?

// ResumeState describes whether and how a run can be resumed.
type ResumeState struct {
	RunID      int        `json:"runId"`
	SessionID  string     `json:"sessionId,omitempty"`
	Status     string     `json:"status"`
	Question   string     `json:"question"`
	Scope      string     `json:"scope,omitempty"`
	Plan       []PlanStep `json:"plan,omitempty"`
	PlanSource string     `json:"planSource,omitempty"`
	Answer     string     `json:"answer,omitempty"`
	SQLError   string     `json:"error,omitempty"`
	// Steps is the persisted trajectory, already trimmed for the wire.
	Steps      []ResumeStep `json:"steps,omitempty"`
	TokenUsage int          `json:"tokenUsage"`
	LatencyMS  int          `json:"latencyMs"`
	// Resumable: a continuation run can be started and will inherit Steps.
	Resumable bool `json:"resumable"`
	// Attached: the original run is still executing; poll instead of resuming.
	Attached bool `json:"attached"`
	// Reason explains a Resumable=false decision in user-facing terms.
	Reason string `json:"reason,omitempty"`
}

// ResumeStep is one persisted trajectory entry rendered for replay.
type ResumeStep struct {
	StepNo     int    `json:"stepNo"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	SideEffect string `json:"sideEffect,omitempty"`
	Status     string `json:"status"`
	Args       any    `json:"args,omitempty"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int    `json:"durationMs,omitempty"`
}

// MaxStepsReplayedToModel bounds how much of a long trajectory is re-fed to the
// model. The most recent steps are the ones that determine "where we were", and
// replaying fifty observations would cost the very budget resume is meant to
// save.
const MaxStepsReplayedToModel = 12

// BuildResumeState inspects a run and decides attach/continue/done.
func (s *Service) BuildResumeState(ctx context.Context, userID, runID int) (*ResumeState, error) {
	if s.DB == nil {
		return nil, errors.New("database unavailable")
	}
	var run models.AIAgentRun
	if err := s.DB.WithContext(ctx).
		Where("id = ? AND user_id = ?", runID, userID).First(&run).Error; err != nil {
		return nil, err
	}

	state := &ResumeState{
		RunID: run.ID, SessionID: run.SessionID, Status: run.Status,
		Question: run.Question, Scope: run.Scope, Answer: run.FinalAnswer,
		SQLError: run.ErrorMsg, TokenUsage: run.TokenUsage, LatencyMS: run.LatencyMS,
	}
	if plan := decodePlan(run.PlanJSON); plan != nil {
		state.Plan = plan.Steps
		state.PlanSource = plan.Source
	}

	var calls []models.AIToolCallLog
	if err := s.DB.WithContext(ctx).Where("run_id = ?", run.ID).
		Order("step_no asc").Find(&calls).Error; err != nil {
		return nil, err
	}
	state.Steps = make([]ResumeStep, 0, len(calls))
	for i := range calls {
		state.Steps = append(state.Steps, resumeStepJSON(&calls[i]))
	}

	// A run still marked "running" is either genuinely alive (another tab is
	// streaming it, or the SSE died but the goroutine did not) or abandoned
	// because the server restarted. Only the grace window can tell them apart.
	if run.Status == "running" {
		if s.runStale(run) {
			state.Status = "stopped"
			state.Resumable = len(calls) > 0
			if state.Resumable {
				state.Reason = "stale"
			} else {
				state.Reason = "no_steps"
			}
		} else {
			state.Attached = true
			state.Reason = "running"
		}
		return state, nil
	}

	switch run.Status {
	case "done":
		state.Reason = "done"
	case "failed", "stopped":
		state.Resumable = len(calls) > 0
		state.Reason = run.Status
	default:
		state.Resumable = len(calls) > 0
	}
	// "Nothing to continue from" is the actionable fact; the terminal status is
	// still reported separately, so do not let it mask the reason.
	if !state.Resumable && len(calls) == 0 {
		state.Reason = "no_steps"
	}
	return state, nil
}

// runStale reports whether a "running" row has outlived the configured run
// timeout plus a grace period, i.e. no goroutine can still be working on it.
func (s *Service) runStale(run models.AIAgentRun) bool {
	limit := 120 * time.Second
	if s.Config != nil && s.Config.AIAgentRunTimeoutSec > 0 {
		limit = time.Duration(s.Config.AIAgentRunTimeoutSec) * time.Second
	}
	// One minute of slack: the timeout cancels the context, but the final
	// FinishRun write and the last SSE flush still have to happen.
	limit += 60 * time.Second
	start := run.CreatedOn
	if run.ChangedOn.After(start) {
		start = run.ChangedOn
	}
	return time.Since(start) > limit
}

// SeedFor builds the continuation seed for a run, or returns nil when the run
// cannot be continued.
func (s *Service) SeedFor(ctx context.Context, userID, runID int) (*ResumeSeed, *ResumeState, error) {
	state, err := s.BuildResumeState(ctx, userID, runID)
	if err != nil {
		return nil, nil, err
	}
	if state.Attached || !state.Resumable {
		return nil, state, nil
	}
	seed := &ResumeSeed{FromRunID: runID}
	var run models.AIAgentRun
	if err := s.DB.WithContext(ctx).Where("id = ?", runID).First(&run).Error; err == nil {
		seed.Plan = decodePlan(run.PlanJSON)
	}
	if err := s.DB.WithContext(ctx).Where("run_id = ?", runID).
		Order("step_no asc").Find(&seed.Steps).Error; err != nil {
		return nil, state, err
	}
	if len(seed.Steps) > MaxStepsReplayedToModel {
		seed.Steps = seed.Steps[len(seed.Steps)-MaxStepsReplayedToModel:]
	}
	return seed, state, nil
}

// decodePlan parses a persisted plan, returning nil when absent or unreadable.
func decodePlan(raw string) *PlanEvent {
	if raw == "" || raw == "null" {
		return nil
	}
	var plan PlanEvent
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil
	}
	return &plan
}

// seedTranscript turns persisted tool calls back into a chat prefix: an
// assistant message carrying the calls, then one tool message per call. The
// call ids are synthesised (`resume_<n>`) because the provider only requires
// them to be unique within the conversation, and this is a fresh one.
//
// Each step is also replayed to the client so the panel can restore the
// transcript it lost, which is what makes the reconnect feel continuous rather
// than like a fresh answer.
func seedTranscript(steps []models.AIToolCallLog, lang string, emit func(Event), lim obsLimits) []ChatMessage {
	out := make([]ChatMessage, 0, len(steps)*2)
	for i := range steps {
		c := &steps[i]
		stepNo := i + 1
		callID := fmt.Sprintf("resume_%d", stepNo)
		args := c.InputArgs
		if args == "" {
			args = "{}"
		}

		out = append(out, ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: callID, Type: "function",
				Function: FunctionCall{Name: c.Name, Arguments: args},
			}},
		})

		decoded := decodeLogResult(c.Result)
		var stepErr error
		if c.Status != "ok" {
			msg := c.ErrorMsg
			if msg == "" {
				msg = c.Status
			}
			stepErr = errors.New(msg)
		}
		hint := resultHint(c.Name, decoded, lang)
		out = append(out, ChatMessage{
			Role: "tool", ToolCallID: callID, Name: c.Name,
			Content: observationText(decoded, stepErr, hint, lim),
		})

		obs := ObserveEvent{StepNo: stepNo, Status: c.Status, Result: decoded, Replayed: true}
		if stepErr != nil {
			obs.Error = stepErr.Error()
		}
		emitEvent(emit, EventObserve, obs)
	}
	return out
}

// decodeLogResult rehydrates a stored tool result into the shape the model and
// the replay frame expect.
func decodeLogResult(raw string) any {
	if raw == "" || raw == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return map[string]any{"_raw": raw}
	}
	return normaliseJSON(v)
}

// normaliseJSON restores map[string]any rows JSON decoding produces: the
// observation pipeline looks for `[]map[string]any`, which only appears when
// the value was built in Go rather than decoded.
func normaliseJSON(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	rows, ok := m["rows"].([]any)
	if !ok {
		return v
	}
	typed := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if rm, ok := row.(map[string]any); ok {
			typed = append(typed, rm)
		}
	}
	m["rows"] = typed
	return m
}

// resumeStepJSON renders one persisted step for the resume snapshot.
func resumeStepJSON(c *models.AIToolCallLog) ResumeStep {
	step := ResumeStep{
		StepNo: c.StepNo, Kind: c.Kind, Name: c.Name, SideEffect: c.SideEffect,
		Status: c.Status, Args: decodeLogResult(c.InputArgs),
		Result: decodeLogResult(c.Result), Error: c.ErrorMsg, DurationMS: c.DurationMS,
	}
	return step
}
