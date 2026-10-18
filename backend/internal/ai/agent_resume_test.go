package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"superset/internal/models"
)

// TestSeedTranscriptRebuildsToolPairs covers the invariant a continuation run
// depends on: every tool message must be preceded by the assistant message that
// announced its call, and the call ids must match.
func TestSeedTranscriptRebuildsToolPairs(t *testing.T) {
	steps := []models.AIToolCallLog{
		{StepNo: 1, Kind: "skill", Name: "metric_digest", Status: "ok", InputArgs: `{"dataset_id":3}`, Result: `{"rows":[{"avg":52}]}`},
		{StepNo: 2, Kind: "tool", Name: "query_executor", Status: "error", InputArgs: `{"sql":"select 1"}`, ErrorMsg: "boom"},
	}
	msgs := seedTranscript(steps, "zh", nil, obsLimitsFor(100000))
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages (call+result per step), got %d", len(msgs))
	}
	for i := 0; i < len(msgs); i += 2 {
		call := msgs[i]
		tool := msgs[i+1]
		if call.Role != "assistant" || len(call.ToolCalls) != 1 {
			t.Fatalf("message %d must be an assistant call, got %+v", i, call)
		}
		if tool.Role != "tool" {
			t.Fatalf("message %d must be a tool result, got %+v", i+1, tool)
		}
		if tool.ToolCallID != call.ToolCalls[0].ID {
			t.Fatalf("tool_call_id mismatch: %q vs %q", tool.ToolCallID, call.ToolCalls[0].ID)
		}
		if tool.Name != call.ToolCalls[0].Function.Name {
			t.Fatalf("tool name mismatch: %q vs %q", tool.Name, call.ToolCalls[0].Function.Name)
		}
	}
	// The failed step must be replayed as an error, not as an empty success.
	if !strings.Contains(msgs[3].Content, "boom") {
		t.Fatalf("failed step lost its error: %s", msgs[3].Content)
	}
}

// TestSeedTranscriptEmitsReplayFrames asserts the client can rebuild the
// transcript it lost: one observe frame per persisted step, flagged replayed.
func TestSeedTranscriptEmitsReplayFrames(t *testing.T) {
	steps := []models.AIToolCallLog{
		{StepNo: 1, Kind: "tool", Name: "query_executor", Status: "ok", InputArgs: `{}`,
			Result: `{"rows":[{"a":1}],"colnames":["a"],"rowcount":1}`},
	}
	var frames []Event
	seedTranscript(steps, "en", func(ev Event) { frames = append(frames, ev) }, obsLimitsFor(100000))

	if len(frames) != 1 {
		t.Fatalf("want 1 replay frame, got %d", len(frames))
	}
	obs, ok := frames[0].Data.(ObserveEvent)
	if !ok {
		t.Fatalf("frame payload is %T, want ObserveEvent", frames[0].Data)
	}
	if frames[0].Type != EventObserve || !obs.Replayed || obs.Status != "ok" {
		t.Fatalf("unexpected replay frame: %+v", obs)
	}
	// Rows must come back as []map[string]any so shrinkObservation and the
	// empty-result hint keep working on a resumed run.
	if !isEmptyResult(map[string]any{"rows": []map[string]any{}}) {
		t.Fatal("sanity: empty rows should read as an empty result")
	}
	decoded, ok := obs.Result.(map[string]any)
	if !ok {
		t.Fatalf("decoded result is %T, want map", obs.Result)
	}
	if _, ok := decoded["rows"].([]map[string]any); !ok {
		t.Fatalf("rows were not normalised: %T", decoded["rows"])
	}
}

// TestDecodePlanHandlesGarbage keeps a corrupt plan_json from killing a resume:
// the run simply re-plans.
func TestDecodePlanHandlesGarbage(t *testing.T) {
	if p := decodePlan(""); p != nil {
		t.Fatalf("empty plan should decode to nil, got %+v", p)
	}
	if p := decodePlan("null"); p != nil {
		t.Fatalf("null plan should decode to nil, got %+v", p)
	}
	if p := decodePlan("{not json"); p != nil {
		t.Fatalf("garbage plan should decode to nil, got %+v", p)
	}
	raw, err := json.Marshal(PlanEvent{
		Thought: "按城市看销售额",
		Steps:   []PlanStep{{Intent: "取数", Dataset: "orders", Metrics: []string{"gmv"}}},
		Source:  "model",
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	p := decodePlan(string(raw))
	if p == nil || len(p.Steps) != 1 || p.Steps[0].Dataset != "orders" || p.Source != "model" {
		t.Fatalf("plan round-trip lost data: %+v", p)
	}
}
