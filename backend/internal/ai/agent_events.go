package ai

import "encoding/json"

// SSE event names emitted by the Agent runtime, matching the contract in
// design §19.1: plan → act → observe → (loop) → answer, with error terminal.
const (
	EventPlan    = "plan"
	EventAct     = "act"
	EventObserve = "observe"
	EventAnswer  = "answer"
	EventError   = "error"
	// EventAttached is sent by the resume endpoint when the original run is
	// still executing: the client should poll the run instead of continuing it.
	EventAttached = "attached"
)

// Event is one SSE frame: a named type plus its JSON-serialisable payload.
type Event struct {
	Type string
	Data any
}

// PlanStep is one structured step of the run plan. It makes the plan auditable
// and editable instead of leaving it as a decorative sentence.
type PlanStep struct {
	Intent  string   `json:"intent"`
	Dataset string   `json:"dataset,omitempty"`
	Metrics []string `json:"metrics,omitempty"`
	Deps    []int    `json:"deps,omitempty"`
}

// PlanEvent announces a run and its high-level intent.
type PlanEvent struct {
	RunID     int    `json:"runId"`
	SessionID string `json:"sessionId,omitempty"`
	Thought   string `json:"thought"`
	// Steps is the structured plan; empty when the planner call failed and the
	// runtime fell back to the generic intent.
	Steps []PlanStep `json:"steps,omitempty"`
	// Source records where the plan came from: "model" | "fallback".
	Source string `json:"source,omitempty"`
	// Resumed marks a plan replayed from a persisted run on reconnect.
	Resumed bool `json:"resumed,omitempty"`
}

// ActEvent announces one capability invocation.
type ActEvent struct {
	StepNo int             `json:"stepNo"`
	Kind   string          `json:"kind"` // skill|tool
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// ObserveEvent carries a capability's outcome back to the client.
type ObserveEvent struct {
	StepNo int    `json:"stepNo"`
	Status string `json:"status"` // ok|error|denied
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	// Replayed marks an observation reconstructed from the audit log while
	// resuming a run, so the client can rebuild the transcript it lost.
	Replayed bool `json:"replayed,omitempty"`
}

// AnswerEvent is the terminal successful frame of a run.
type AnswerEvent struct {
	RunID      int            `json:"runId"`
	SessionID  string         `json:"sessionId,omitempty"`
	Markdown   string         `json:"markdown"`
	Artifacts  map[string]any `json:"artifacts,omitempty"`
	TokenUsage int            `json:"tokenUsage"`
	StepCount  int            `json:"stepCount"`
	// Observability: what this run actually cost and how healthy it was.
	FirstTokenMS     int `json:"firstTokenMs,omitempty"`
	ToolErrorCount   int `json:"toolErrorCount,omitempty"`
	EmptyResultCount int `json:"emptyResultCount,omitempty"`
	SelfCheckCount   int `json:"selfCheckCount,omitempty"`
	// Replayed marks an answer served from the replay store (same question,
	// same numbers) instead of a fresh model run.
	Replayed bool `json:"replayed,omitempty"`
}

// ErrorEvent reports a terminal failure while streaming.
type ErrorEvent struct {
	RunID   int    `json:"runId"`
	Message string `json:"message"`
}

// AttachedEvent is emitted by POST /agent/runs/{id}/resume when the target run
// is still executing on the server. The client should poll GET /agent/runs/{id}
// for its terminal state instead of starting a continuation run.
type AttachedEvent struct {
	RunID  int    `json:"runId"`
	Status string `json:"status"`
}

// emitEvent forwards an event when a sink is attached (it is nil for
// non-streaming callers such as the /skill/invoke and /tool/invoke endpoints).
func emitEvent(emit func(Event), typ string, data any) {
	if emit != nil {
		emit(Event{Type: typ, Data: data})
	}
}
