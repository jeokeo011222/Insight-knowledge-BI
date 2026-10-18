package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"superset/internal/ai/registry"
	"superset/internal/models"
	"superset/internal/queries"
)

// Read tools that were registered in P1 but only got their executors later.
// Keeping them here (instead of growing tools_read.go) makes the "registered
// but unwired" gap obvious: every tool listed in builtin.go must have a
// matching executor, otherwise the catalogue marks it unavailable.

// chartReader returns a chart definition: the Agent needs viz_type/metrics/
// dimensions to answer "what does this chart show" or to clone it.
func chartReader(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			ChartID int `json:"chart_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.ChartID <= 0 {
			return nil, errors.New("chart_id is required")
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		var row models.Slice
		if err := s.DB.WithContext(ctx).
			Where("id = ? AND deleted_at IS NULL", in.ChartID).
			First(&row).Error; err != nil {
			return nil, fmt.Errorf("chart %d not found: %w", in.ChartID, err)
		}
		return &registry.ToolResult{
			Output: map[string]any{
				"chart_id":        row.ID,
				"slice_name":      row.SliceName,
				"viz_type":        row.VizType,
				"datasource_id":   row.DatasourceID,
				"datasource_type": row.DatasourceType,
				"description":     row.Description,
				"params":          json.RawMessage(row.Params),
			},
			SideEffect: registry.Read,
		}, nil
	}
}

// dashboardReader returns a dashboard plus the charts it contains, so the
// Agent can describe a board without N round-trips.
func dashboardReader(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			DashboardID int `json:"dashboard_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.DashboardID <= 0 {
			return nil, errors.New("dashboard_id is required")
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		var board models.Dashboard
		if err := s.DB.WithContext(ctx).First(&board, in.DashboardID).Error; err != nil {
			return nil, fmt.Errorf("dashboard %d not found: %w", in.DashboardID, err)
		}
		var links []models.DashboardSlices
		if err := s.DB.WithContext(ctx).
			Where("dashboard_id = ?", board.ID).Find(&links).Error; err != nil {
			return nil, fmt.Errorf("failed to load dashboard charts: %w", err)
		}
		ids := make([]int, 0, len(links))
		for _, link := range links {
			ids = append(ids, link.SliceID)
		}
		charts := make([]map[string]any, 0, len(ids))
		if len(ids) > 0 {
			var rows []models.Slice
			if err := s.DB.WithContext(ctx).
				Where("id IN ? AND deleted_at IS NULL", ids).Find(&rows).Error; err != nil {
				return nil, fmt.Errorf("failed to load dashboard charts: %w", err)
			}
			for _, row := range rows {
				charts = append(charts, map[string]any{
					"chart_id":   row.ID,
					"slice_name": row.SliceName,
					"viz_type":   row.VizType,
				})
			}
		}
		return &registry.ToolResult{
			Output: map[string]any{
				"dashboard_id":    board.ID,
				"dashboard_title": board.DashboardTitle,
				"published":       board.Published,
				"charts":          charts,
			},
			SideEffect: registry.Read,
		}, nil
	}
}

// resultSampler runs a read-only statement and returns only a sample of rows —
// the cheap way for the model to eyeball value shapes before writing SQL.
// It shares the query engine so RBAC/RLS/limits/auditing still apply.
func resultSampler(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			SQL       string `json:"sql"`
			DatasetID int    `json:"dataset_id"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.DatasetID <= 0 {
			return nil, errors.New("dataset_id is required")
		}
		if s.Queries == nil {
			return nil, errors.New("query engine is unavailable")
		}
		statement, err := s.prepareAgentSQL(ctx, in.DatasetID, in.SQL)
		if err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit <= 0 || limit > s.Config.AISamplesRowLimit {
			limit = s.Config.AISamplesRowLimit
		}
		res, err := s.Queries.ExecuteReadOnlySQL(ctx, registry.UserIDFrom(ctx), statement, queries.ReadOnlyOptions{
			DatasetID:     in.DatasetID,
			RowLimit:      limit,
			ExemptMasking: isAdminFrom(ctx),
		})
		if err != nil {
			return nil, err
		}
		return &registry.ToolResult{
			Output: map[string]any{
				"sql":       res.SQL,
				"sample":    res.Data,
				"colnames":  res.ColNames,
				"rowcount":  res.RowCount,
				"truncated": res.Truncated,
				"limit":     limit,
			},
			SideEffect: registry.Read,
			DurationMS: res.DurationMS,
		}, nil
	}
}

// metaSearch maps a colloquial question onto tables/columns/metrics. Candidates
// are read once (bounded) and ranked by text relevance, so "客户" can find
// `customers.name` even though no column contains the literal string — a plain
// ILIKE cannot do that, which is why the old implementation returned nothing for
// paraphrase questions while the tool description promised vector recall.
func metaSearch(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			Query     string `json:"query"`
			DatasetID int    `json:"dataset_id"`
			K         int    `json:"k"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		needle := strings.TrimSpace(in.Query)
		if needle == "" {
			return nil, errors.New("query is required")
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}
		k := in.K
		if k <= 0 || k > 50 {
			k = 10
		}
		db := s.DB.WithContext(ctx)

		type candidate struct {
			score   float64
			payload map[string]any
		}
		cands := make([]candidate, 0, 64)

		var tables []models.SqlaTable
		tq := db.Where("deleted_at IS NULL")
		if in.DatasetID > 0 {
			tq = tq.Where("id = ?", in.DatasetID)
		}
		if err := tq.Order("id").Limit(300).Find(&tables).Error; err != nil {
			return nil, fmt.Errorf("meta search failed: %w", err)
		}
		for _, table := range tables {
			text := table.TableNameStr + " " + table.Description
			cands = append(cands, candidate{textScore(needle, text), map[string]any{
				"kind":       "dataset",
				"dataset_id": table.ID,
				"name":       table.TableNameStr,
				"schema":     table.Schema,
				"database":   table.DatabaseID,
			}})
		}

		var columns []models.TableColumn
		cq := db.Model(&models.TableColumn{})
		if in.DatasetID > 0 {
			cq = cq.Where("table_id = ?", in.DatasetID)
		}
		if err := cq.Order("table_id").Limit(800).Find(&columns).Error; err != nil {
			return nil, fmt.Errorf("meta search failed: %w", err)
		}
		for _, column := range columns {
			if !column.AgentVisible {
				continue // hidden columns must not be discoverable either
			}
			text := column.ColumnName + " " + column.VerboseName + " " + column.Description
			cands = append(cands, candidate{textScore(needle, text), map[string]any{
				"kind":       "column",
				"dataset_id": column.TableID,
				"name":       column.ColumnName,
				"verbose":    column.VerboseName,
				"type":       column.Type,
			}})
		}

		var metrics []models.AIMetric
		mq := db.Model(&models.AIMetric{})
		if in.DatasetID > 0 {
			mq = mq.Where("dataset_id IN (0, ?)", in.DatasetID)
		}
		if err := mq.Order("name").Limit(300).Find(&metrics).Error; err != nil {
			return nil, fmt.Errorf("meta search failed: %w", err)
		}
		for _, metric := range metrics {
			text := metric.Name + " " + metric.Description + " " + metric.Expression
			cands = append(cands, candidate{textScore(needle, text), map[string]any{
				"kind":       "metric",
				"name":       metric.Name,
				"expression": metric.Expression,
				"dataset_id": metric.DatasetID,
				"status":     metric.Status,
			}})
		}

		// Keep the best k, dropping candidates that matched nothing at all.
		ranked := make([]candidate, 0, k)
		for _, c := range cands {
			if c.score < 0.2 {
				continue
			}
			ranked = append(ranked, c)
		}
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
		hits := make([]map[string]any, 0, k)
		for _, c := range ranked {
			if len(hits) >= k {
				break
			}
			payload := c.payload
			payload["score"] = round2(c.score)
			hits = append(hits, payload)
		}
		return &registry.ToolResult{
			Output: map[string]any{
				"query":     needle,
				"hits":      hits,
				"count":     len(hits),
				"retriever": "lexical",
			},
			SideEffect: registry.Read,
		}, nil
	}
}
