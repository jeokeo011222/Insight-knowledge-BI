package ai

import (
	"context"
	"fmt"
	"strings"

	"superset/internal/ai/registry"
	"superset/internal/queries"
)

// dataset_sql.go is the one read path external callers (the REST endpoint
// /dataset/{pk}/query and the MCP server) are allowed to use.
//
// The guard lives in this package because it was written for the agent, and
// re-implementing a second, weaker check elsewhere is how a security boundary
// rots. Everything downstream is shared too: table/column whitelist, function
// blacklist, wildcard rejection, read-only session, RLS predicates, masking and
// the row cap all still apply. The only thing this adds is the plumbing that
// turns "SQL in" into "rows out" for a caller that is not an LLM.

// ValidateDatasetSQL runs only the SQL guard (whitelist, banned functions,
// wildcard projection) and returns no data. It exists so a caller can find out
// "would this be allowed?" without paying for a warehouse round trip — the
// guard is the part that rejects a statement, so validating it is the useful
// half of a dry run.
func ValidateDatasetSQL(ctx context.Context, s *Service, datasetID int, sqlText string) error {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return fmt.Errorf("sql is required")
	}
	if datasetID <= 0 {
		return fmt.Errorf("dataset_id is required")
	}
	return s.guardDatasetSQL(ctx, datasetID, sqlText)
}

// RunDatasetSQL executes read-only SQL against one dataset on behalf of the
// user in ctx. limit <= 0 falls back to the configured agent row cap.
func RunDatasetSQL(ctx context.Context, s *Service, datasetID int, sqlText string, limit int) (map[string]any, error) {
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return nil, fmt.Errorf("sql is required")
	}
	if strings.HasSuffix(sqlText, ";") {
		sqlText = strings.TrimSpace(strings.TrimSuffix(sqlText, ";"))
	}
	if datasetID <= 0 {
		return nil, fmt.Errorf("dataset_id is required")
	}
	if s.Queries == nil {
		return nil, fmt.Errorf("query engine is unavailable")
	}
	if err := s.guardDatasetSQL(ctx, datasetID, sqlText); err != nil {
		return nil, err
	}

	maxRows := 0
	if s.Config != nil {
		maxRows = s.Config.AIQueryMaxRows
	}
	rowLimit := limit
	if rowLimit <= 0 {
		rowLimit = maxRows
	}
	if maxRows > 0 && rowLimit > maxRows {
		rowLimit = maxRows
	}

	res, err := s.Queries.ExecuteReadOnlySQL(ctx, registry.UserIDFrom(ctx), sqlText, queries.ReadOnlyOptions{
		DatasetID:     datasetID,
		RowLimit:      rowLimit,
		ExemptMasking: isAdminFrom(ctx),
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"sql":         res.SQL,
		"rows":        res.Data,
		"colnames":    res.ColNames,
		"rowcount":    res.RowCount,
		"truncated":   res.Truncated,
		"query_id":    res.QueryID,
		"duration_ms": int64(res.DurationMS),
		"query_mode":  "sql",
	}
	if len(res.MaskedColumns) > 0 {
		out["masked_columns"] = res.MaskedColumns
		out["masking_note"] = "这些列的值已按合规规则脱敏，可用于计数/分组，不可用于识别个人"
	}
	return out, nil
}
