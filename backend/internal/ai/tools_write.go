package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"superset/internal/ai/registry"
	"superset/internal/models"
)

// ErrWriteConfirmRequired is returned when AI_WRITE_CONFIRM_REQUIRED is on: the
// write is refused rather than executed, pending user confirmation (fail-closed).
var ErrWriteConfirmRequired = errors.New("write confirmation required")

// wireWriteExecutors attaches the write tool executors. Kept separate from the
// read wiring so the (higher-risk) write path is easy to audit at a glance.
func wireWriteExecutors(s *Service) {
	if t, ok := s.Tools.Get("chart_writer"); ok {
		t.Execute = chartWriter(s)
	}
	if t, ok := s.Tools.Get("dashboard_writer"); ok {
		t.Execute = dashboardWriter(s)
	}
	if t, ok := s.Tools.Get("dashboard_compose_writer"); ok {
		t.Execute = dashboardComposeWriter(s)
	}
	if sk, ok := s.Skills.Get("dashboard_compose"); ok {
		sk.Executor = dashboardComposeSkill(s)
	}
}

// chartWriter creates or updates a chart, capturing a pre-image for rollback.
func chartWriter(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			ChartID *int           `json:"chart_id"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		if writeConfirmRequired(ctx) {
			return nil, ErrWriteConfirmRequired
		}
		userID := registry.UserIDFrom(ctx)
		db := s.DB.WithContext(ctx)
		cols := selectColumns(in.Payload, sliceWritableCols)

		// Create
		if in.ChartID == nil || *in.ChartID == 0 {
			name := strOf(cols["slice_name"])
			if name == "" || intOf(cols["datasource_id"]) == 0 {
				return nil, errors.New("payload.slice_name and payload.datasource_id are required")
			}
			sl := models.Slice{
				UUID:           uuid.NewString(),
				SliceName:      name,
				VizType:        strOf(cols["viz_type"]),
				DatasourceID:   intOf(cols["datasource_id"]),
				DatasourceType: orDefault(strOf(cols["datasource_type"]), "table"),
				DatasourceName: strOf(cols["datasource_name"]),
				Params:         datatypes.JSON(mustJSONDefault(cols["params"], "{}")),
				Description:    strOf(cols["description"]),
			}
			if err := db.Create(&sl).Error; err != nil {
				return nil, err
			}
			if userID > 0 {
				_ = db.Create(&models.SliceUser{UserID: userID, SliceID: sl.ID}).Error
			}
			return &registry.ToolResult{
				Output:     map[string]any{"chart_id": sl.ID, "uuid": sl.UUID, "op": "create"},
				SideEffect: registry.Write,
				TargetIDs:  []int{sl.ID},
				Snapshot:   writeSnapshot{Entity: "chart", Op: "create", TargetID: sl.ID},
			}, nil
		}

		// Update
		var existing models.Slice
		if err := db.First(&existing, *in.ChartID).Error; err != nil {
			return nil, fmt.Errorf("chart %d not found: %w", *in.ChartID, err)
		}
		if len(cols) == 0 {
			return nil, errors.New("payload has no writable fields")
		}
		before := map[string]any{}
		for col := range cols {
			before[col] = sliceColumn(&existing, col)
		}
		if err := db.Model(&models.Slice{}).Where("id = ?", existing.ID).Updates(cols).Error; err != nil {
			return nil, err
		}
		return &registry.ToolResult{
			Output:     map[string]any{"chart_id": existing.ID, "op": "update", "updated": keysOf(cols)},
			SideEffect: registry.Write,
			TargetIDs:  []int{existing.ID},
			Snapshot:   writeSnapshot{Entity: "chart", Op: "update", TargetID: existing.ID, Before: before},
		}, nil
	}
}

// dashboardWriter creates or updates a dashboard's columns and layout.
func dashboardWriter(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			DashboardID *int           `json:"dashboard_id"`
			Payload     map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		if writeConfirmRequired(ctx) {
			return nil, ErrWriteConfirmRequired
		}
		userID := registry.UserIDFrom(ctx)
		db := s.DB.WithContext(ctx)
		cols := selectColumns(in.Payload, dashboardWritableCols)

		if in.DashboardID == nil || *in.DashboardID == 0 {
			title := strOf(cols["dashboard_title"])
			if title == "" {
				return nil, errors.New("payload.dashboard_title is required")
			}
			dm := models.Dashboard{
				UUID:           uuid.NewString(),
				DashboardTitle: title,
				Slug:           strOf(cols["slug"]),
				Published:      boolOf(cols["published"]),
				PositionJSON:   datatypes.JSON(mustJSONDefault(cols["position_json"], "{}")),
				JSONMetadata:   datatypes.JSON(mustJSONDefault(cols["json_metadata"], "{}")),
				CSS:            strOf(cols["css"]),
				Description:    strOf(cols["description"]),
			}
			if err := db.Create(&dm).Error; err != nil {
				return nil, err
			}
			if userID > 0 {
				_ = db.Create(&models.DashboardUser{UserID: userID, DashboardID: dm.ID}).Error
			}
			return &registry.ToolResult{
				Output:     map[string]any{"dashboard_id": dm.ID, "uuid": dm.UUID, "op": "create"},
				SideEffect: registry.Write,
				TargetIDs:  []int{dm.ID},
				Snapshot:   writeSnapshot{Entity: "dashboard", Op: "create", TargetID: dm.ID},
			}, nil
		}

		var existing models.Dashboard
		if err := db.First(&existing, *in.DashboardID).Error; err != nil {
			return nil, fmt.Errorf("dashboard %d not found: %w", *in.DashboardID, err)
		}
		if len(cols) == 0 {
			return nil, errors.New("payload has no writable fields")
		}
		before := map[string]any{}
		for col := range cols {
			before[col] = dashboardColumn(&existing, col)
		}
		if err := db.Model(&models.Dashboard{}).Where("id = ?", existing.ID).Updates(cols).Error; err != nil {
			return nil, err
		}
		return &registry.ToolResult{
			Output:     map[string]any{"dashboard_id": existing.ID, "op": "update", "updated": keysOf(cols)},
			SideEffect: registry.Write,
			TargetIDs:  []int{existing.ID},
			Snapshot:   writeSnapshot{Entity: "dashboard", Op: "update", TargetID: existing.ID, Before: before},
		}, nil
	}
}

// dashboardComposeWriter creates charts from a page_spec and lays them out on a
// dashboard (existing or new), capturing a pre-image for rollback.
func dashboardComposeWriter(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			PageSpec    map[string]any `json:"page_spec"`
			DashboardID int            `json:"dashboard_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if len(in.PageSpec) == 0 {
			return nil, errors.New("page_spec is required")
		}

		res, snap, err := s.ComposeDashboard(ctx, registry.UserIDFrom(ctx), in.DashboardID, in.PageSpec)
		if err != nil {
			return nil, err
		}
		targets := append([]int{res.DashboardID}, res.ChartIDs...)
		return &registry.ToolResult{
			Output:     map[string]any{"dashboard_id": res.DashboardID, "chart_ids": res.ChartIDs, "op": res.Op},
			SideEffect: registry.Write,
			TargetIDs:  targets,
			Snapshot:   *snap,
		}, nil
	}
}

// ComposeResult reports a compose operation.
type ComposeResult struct {
	DashboardID int    `json:"dashboard_id"`
	ChartIDs    []int  `json:"chart_ids"`
	Op          string `json:"op"` // create|compose
}

// ComposeDashboard materialises a page_spec: it creates the charts and links
// them onto the target dashboard (creating it when dashboardID is 0), writing a
// single grid layout into position_json. It returns the pre-image snapshot so
// the caller can attach it to the tool result for rollback.
func (s *Service) ComposeDashboard(ctx context.Context, userID, dashboardID int, pageSpec map[string]any) (*ComposeResult, *writeSnapshot, error) {
	if s.DB == nil {
		return nil, nil, errors.New("metadata store is unavailable")
	}
	if writeConfirmRequired(ctx) {
		return nil, nil, ErrWriteConfirmRequired
	}
	charts, _ := pageSpec["charts"].([]any)
	if len(charts) == 0 {
		return nil, nil, errors.New("page_spec.charts is empty")
	}
	db := s.DB.WithContext(ctx)
	res := &ComposeResult{ChartIDs: []int{}}
	snap := &writeSnapshot{Entity: "dashboard"}

	if dashboardID > 0 {
		var dash models.Dashboard
		if err := db.First(&dash, dashboardID).Error; err != nil {
			return nil, nil, fmt.Errorf("dashboard %d not found: %w", dashboardID, err)
		}
		res.Op = "compose"
		res.DashboardID = dash.ID
		snap.Op = "compose"
		snap.TargetID = dash.ID
		snap.Before = map[string]any{
			"position_json": jsonText(dash.PositionJSON),
			"json_metadata": jsonText(dash.JSONMetadata),
		}
	} else {
		title := strOf(pageSpec["title"])
		if title == "" {
			title = "AI 生成的看板"
		}
		dash := models.Dashboard{
			UUID:           uuid.NewString(),
			DashboardTitle: title,
			PositionJSON:   datatypes.JSON("{}"),
			JSONMetadata:   datatypes.JSON("{}"),
		}
		if err := db.Create(&dash).Error; err != nil {
			return nil, nil, err
		}
		if userID > 0 {
			_ = db.Create(&models.DashboardUser{UserID: userID, DashboardID: dash.ID}).Error
		}
		res.Op = "create"
		res.DashboardID = dash.ID
		snap.Op = "create"
		snap.TargetID = dash.ID
	}

	layout := map[string]any{}
	for i, raw := range charts {
		spec, _ := raw.(map[string]any)
		if spec == nil {
			continue
		}
		sl := models.Slice{
			UUID:           uuid.NewString(),
			SliceName:      orDefault(strOf(spec["slice_name"]), fmt.Sprintf("AI 图表 %d", i+1)),
			VizType:        strOf(spec["viz_type"]),
			DatasourceID:   intOf(spec["datasource_id"]),
			DatasourceType: orDefault(strOf(spec["datasource_type"]), "table"),
			Params:         datatypes.JSON(mustJSONDefault(spec["params"], "{}")),
		}
		if err := db.Create(&sl).Error; err != nil {
			return nil, nil, err
		}
		if userID > 0 {
			_ = db.Create(&models.SliceUser{UserID: userID, SliceID: sl.ID}).Error
		}
		if err := db.Create(&models.DashboardSlices{DashboardID: res.DashboardID, SliceID: sl.ID}).Error; err != nil {
			return nil, nil, err
		}
		res.ChartIDs = append(res.ChartIDs, sl.ID)
		layout[fmt.Sprintf("chart-%d", sl.ID)] = map[string]any{
			"slice_id": sl.ID, "slice_name": sl.SliceName, "layout": spec["layout"],
		}
	}
	snap.Created = res.ChartIDs

	blob, _ := json.Marshal(map[string]any{"charts": layout})
	if err := db.Model(&models.Dashboard{}).Where("id = ?", res.DashboardID).
		Updates(map[string]any{"position_json": string(blob)}).Error; err != nil {
		return nil, nil, err
	}
	return res, snap, nil
}

// ---- small typed accessors over decoded JSON payloads ----

func strOf(v any) string {
	s, _ := v.(string)
	return s
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// mustJSONDefault renders a value as a JSON string, falling back when empty.
func mustJSONDefault(v any, def string) string {
	if v == nil {
		return def
	}
	s := mustJSON(v)
	if s == "" {
		return def
	}
	return s
}

// keysOf returns the sorted-ish key list of a column map (for audit output).
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
