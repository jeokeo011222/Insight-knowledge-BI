package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"superset/internal/ai/registry"
)

// dashboardComposeSkill turns a requested page_spec into a complete, storable
// dashboard draft and persists it through the dashboard_compose_writer tool
// (whose write is snapshotted and therefore rollback-able).
func dashboardComposeSkill(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			PageSpec    map[string]any `json:"page_spec"`
			DashboardID int            `json:"dashboard_id"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if len(args.PageSpec) == 0 {
			return nil, errors.New("dashboard_compose: page_spec is required")
		}
		if rt == nil || rt.InvokeTool == nil {
			return nil, errors.New("dashboard_compose: tool runtime is unavailable")
		}

		raw, _ := json.Marshal(args.PageSpec)
		prompt := fmt.Sprintf("看板编排需求（page_spec）：\n%s\n\n"+
			`请补全为可落库的结构，只返回 JSON：{"title":"看板标题","charts":[{"slice_name":"...","viz_type":"...","datasource_id":整数,"datasource_type":"table","params":{},"layout":{"w":6,"h":4}}]}`,
			string(raw))
		content, tokens, err := s.complete(ctx, in.UserID, chartSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}

		plan := map[string]any{}
		if obj := extractJSONObject(content); obj != "" {
			_ = json.Unmarshal([]byte(obj), &plan)
		}
		if len(plan) == 0 {
			plan = args.PageSpec
		}

		callArgs, _ := json.Marshal(map[string]any{"page_spec": plan, "dashboard_id": args.DashboardID})
		tr, err := rt.InvokeTool(ctx, "dashboard_compose_writer", callArgs)
		if err != nil {
			return nil, err
		}

		md := "看板已生成初稿（写入操作，可按运行回滚）。"
		return &registry.Result{
			Output:     tr.Output,
			Markdown:   md,
			Artifacts:  map[string]any{"page_spec": plan},
			TokenUsage: tokens,
		}, nil
	}
}
