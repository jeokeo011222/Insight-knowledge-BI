package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"superset/internal/ai/registry"
	"superset/internal/models"
)

// metrics.go implements the Lite metric dictionary (semantic layer): governed
// metric definitions that pin business 口径 so the Agent aligns to reviewed
// expressions instead of inventing aggregations. Only approved rows reach the
// planner prompt; the model can still query the full dictionary via the
// `metric_dictionary` tool.

// MetricDigest renders the approved metric dictionary as planner-prompt lines.
// datasetID > 0 keeps dataset-specific metrics and global ones; metrics bound
// to another dataset are excluded to avoid cross-dataset confusion.
func (s *Service) MetricDigest(ctx context.Context, datasetID int) string {
	if s.DB == nil {
		return ""
	}
	q := s.DB.WithContext(ctx).Model(&models.AIMetric{}).
		Where("status = ?", "approved")
	if datasetID > 0 {
		// Dataset-scoped analysis pins *every* approved 口径 of that dataset.
		// A financial run reads whole statements (三大表) and drops none of them:
		// a truncated dictionary is what makes the model invent an aggregation
		// instead of reusing a reviewed one.
		q = q.Where("dataset_id IN ?", []int{0, datasetID})
	} else {
		// No dataset attached, so the digest can only be a generic hint; keep it
		// bounded and let the model pull the rest via the metric_dictionary tool.
		q = q.Limit(50)
	}
	var rows []models.AIMetric
	if err := q.Order("name").Find(&rows).Error; err != nil {
		return ""
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("指标字典（已审核口径，涉及这些指标时必须按此口径取数）：\n")
	for _, m := range rows {
		line := fmt.Sprintf("- %s：SQL 口径 `%s`", m.Name, m.Expression)
		if m.Unit != "" {
			line += fmt.Sprintf("（单位：%s）", m.Unit)
		}
		if m.Description != "" {
			line += fmt.Sprintf(" — %s", m.Description)
		}
		if dims := strings.Trim(m.Dimensions, " []"); m.Dimensions != "" && dims != "" {
			line += fmt.Sprintf("；可拆维度：%s", dims)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// metricDictionaryExecutor backs the `metric_dictionary` read tool: it lets the
// model look up metric definitions (drafts included, flagged as such) on demand.
func metricDictionaryExecutor(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			DatasetID int      `json:"dataset_id"`
			Names     []string `json:"names"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		q := s.DB.WithContext(ctx).Model(&models.AIMetric{})
		if len(in.Names) > 0 {
			q = q.Where("name IN ?", in.Names)
		}
		if in.DatasetID > 0 {
			q = q.Where("dataset_id IN ?", []int{0, in.DatasetID})
		}
		var rows []models.AIMetric
		if err := q.Order("name").Limit(100).Find(&rows).Error; err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(rows))
		for _, m := range rows {
			out = append(out, map[string]any{
				"name":        m.Name,
				"expression":  m.Expression,
				"description": m.Description,
				"unit":        m.Unit,
				"dataset_id":  m.DatasetID,
				"status":      m.Status,
			})
		}
		return &registry.ToolResult{
			Output:     map[string]any{"metrics": out, "note": "draft 状态的口径尚未审核，仅供参考"},
			SideEffect: registry.Read,
		}, nil
	}
}

// SaveChartSpec is the payload of the one-click "save as chart" flow: it turns
// an Agent answer's chart_spec artifact into a persisted chart and optionally
// appends it to a dashboard's position_json.
type SaveChartSpec struct {
	DatasetID   int              `json:"dataset_id"`
	VizType     string           `json:"viz_type"`
	SliceName   string           `json:"slice_name"`
	Metrics     []any            `json:"metrics"`
	Groupby     []any            `json:"groupby"`
	Filters     []map[string]any `json:"filters"`
	DashboardID int              `json:"dashboard_id"`
}

// SaveChartResult reports what the one-click save produced.
type SaveChartResult struct {
	ChartID     int    `json:"chart_id"`
	SliceName   string `json:"slice_name"`
	DashboardID int    `json:"dashboard_id,omitempty"`
	AddedToDash bool   `json:"added_to_dashboard"`
	VizType     string `json:"viz_type"`
}

// SaveChart persists the spec as a chart (form_data params over the dataset)
// and, when a dashboard is given, appends it as a new row of the v2 layout.
// It is a user-initiated write (not an Agent run), so no rollback snapshot is
// taken; the standard chart/dashboard audit trail applies.
func (s *Service) SaveChart(ctx context.Context, userID int, spec SaveChartSpec) (*SaveChartResult, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	if spec.DatasetID <= 0 {
		return nil, errors.New("dataset_id is required")
	}
	if spec.VizType == "" {
		spec.VizType = "table"
	}
	name := strings.TrimSpace(spec.SliceName)
	if name == "" {
		name = "AI 图表"
	}

	var ds models.SqlaTable
	if err := s.DB.WithContext(ctx).First(&ds, spec.DatasetID).Error; err != nil {
		return nil, fmt.Errorf("dataset %d not found: %w", spec.DatasetID, err)
	}

	formData := map[string]any{
		"viz_type":   spec.VizType,
		"metrics":    spec.Metrics,
		"groupby":    spec.Groupby,
		"filters":    spec.Filters,
		"datasource": fmt.Sprintf("%d__table", ds.ID),
		"row_limit":  10000,
	}
	params, err := json.Marshal(formData)
	if err != nil {
		return nil, err
	}

	slice := models.Slice{
		UUID:           uuid.NewString(),
		SliceName:      name,
		VizType:        spec.VizType,
		DatasourceID:   ds.ID,
		DatasourceType: "table",
		DatasourceName: ds.TableNameStr,
		Params:         datatypes.JSON(params),
	}
	if err := s.DB.WithContext(ctx).Create(&slice).Error; err != nil {
		return nil, err
	}
	if userID > 0 {
		_ = s.DB.WithContext(ctx).Create(&models.SliceUser{UserID: userID, SliceID: slice.ID}).Error
	}

	res := &SaveChartResult{ChartID: slice.ID, SliceName: slice.SliceName, VizType: spec.VizType}
	if spec.DashboardID > 0 {
		added, err := s.appendChartToDashboard(ctx, userID, spec.DashboardID, slice)
		if err != nil {
			return res, nil // chart persisted; surface the dashboard problem separately
		}
		res.DashboardID = spec.DashboardID
		res.AddedToDash = added
	}
	return res, nil
}

// appendChartToDashboard adds one chart as a new full-width row at the end of a
// dashboard's v2 position_json, placing it under the default tab when the
// layout uses TABS. Reports false when the layout is not a v2 grid document.
func (s *Service) appendChartToDashboard(ctx context.Context, userID int, dashboardID int, slice models.Slice) (bool, error) {
	var dash models.Dashboard
	if err := s.DB.WithContext(ctx).First(&dash, dashboardID).Error; err != nil {
		return false, err
	}
	doc := map[string]any{}
	if len(dash.PositionJSON) > 0 {
		if err := json.Unmarshal(dash.PositionJSON, &doc); err != nil {
			return false, fmt.Errorf("position_json is not a JSON object: %w", err)
		}
	}
	grid, _ := doc["GRID_ID"].(map[string]any)
	if grid == nil {
		return false, nil
	}
	children, _ := grid["children"].([]any)

	chartNodeID := fmt.Sprintf("CHART-%d", slice.ID)
	rowNodeID := fmt.Sprintf("ROW-AI-%d", slice.ID)
	rowParents := []any{"ROOT_ID", "GRID_ID"}
	chartParents := []any{"ROOT_ID", "GRID_ID", rowNodeID}

	// TABS layouts keep rows under their owning TAB; land on the default tab.
	tabsID := "TABS_ID"
	if _, hasTabs := doc[tabsID]; hasTabs {
		if node, _ := doc[tabsID].(map[string]any); node != nil {
			owner := defaultTabID(node)
			var tabNode map[string]any
			if owner != "" {
				tabNode, _ = doc[owner].(map[string]any)
			}
			if tabNode == nil {
				return false, nil
			}
			tabChildren, _ := tabNode["children"].([]any)
			tabNode["children"] = append(tabChildren, rowNodeID)
			rowParents = []any{"ROOT_ID", "GRID_ID", tabsID, owner}
			chartParents = []any{"ROOT_ID", "GRID_ID", tabsID, owner, rowNodeID}
		}
	} else {
		grid["children"] = append(children, rowNodeID)
	}

	doc[chartNodeID] = map[string]any{
		"id": chartNodeID, "type": "CHART", "children": []any{},
		"parents": chartParents,
		"meta": map[string]any{
			"width": 12, "height": 50, "chartId": slice.ID, "sliceName": slice.SliceName,
		},
	}
	doc[rowNodeID] = map[string]any{
		"id": rowNodeID, "type": "ROW", "children": []any{chartNodeID},
		"parents": rowParents, "meta": map[string]any{"background": "BACKGROUND_TRANSPARENT"},
	}

	blob, err := json.Marshal(doc)
	if err != nil {
		return false, err
	}
	if err := s.DB.WithContext(ctx).Model(&models.Dashboard{}).Where("id = ?", dashboardID).
		Update("position_json", string(blob)).Error; err != nil {
		return false, err
	}
	if userID > 0 {
		_ = s.DB.WithContext(ctx).Create(&models.DashboardUser{UserID: userID, DashboardID: dashboardID}).Error
	}
	return true, nil
}

// defaultTabID picks the tab carrying meta.default, falling back to the first.
func defaultTabID(tabsNode map[string]any) string {
	children, _ := tabsNode["children"].([]any)
	first := ""
	for _, raw := range children {
		id, _ := raw.(string)
		if id == "" {
			continue
		}
		if first == "" {
			first = id
		}
		node, _ := tabsNode[id].(map[string]any)
		meta, _ := node["meta"].(map[string]any)
		if b, _ := meta["default"].(bool); b {
			return id
		}
	}
	return first
}
