package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
)

// writeSnapshot is the pre-image captured before a write. It is serialised into
// ai_tool_call_log.snapshot so a run's mutations can be rolled back (design §11.2).
type writeSnapshot struct {
	Entity   string         `json:"entity"` // chart|dashboard
	Op       string         `json:"op"`     // create|update|compose
	TargetID int            `json:"target_id"`
	Before   map[string]any `json:"before,omitempty"`  // original columns (update/compose)
	Created  []int          `json:"created,omitempty"` // sibling ids created alongside (compose)
}

// RollbackResult reports what a rollback undid.
type RollbackResult struct {
	RunID      int      `json:"run_id"`
	RolledBack int      `json:"rolled_back"`
	Skipped    int      `json:"skipped"`
	Details    []string `json:"details"`
}

// sliceWritableCols / dashboardWritableCols are the writable column sets shared
// by the snapshot capture and the writer tools.
var sliceWritableCols = map[string]bool{
	"slice_name": true, "viz_type": true, "datasource_id": true, "datasource_type": true,
	"datasource_name": true, "params": true, "description": true, "cache_timeout": true,
	"certified_by": true, "certification_details": true,
}

var dashboardWritableCols = map[string]bool{
	"dashboard_title": true, "slug": true, "published": true, "position_json": true,
	"json_metadata": true, "css": true, "description": true,
	"certified_by": true, "certification_details": true,
}

// sliceColumn reads the current value of a writable chart column.
func sliceColumn(s *models.Slice, col string) any {
	switch col {
	case "slice_name":
		return s.SliceName
	case "viz_type":
		return s.VizType
	case "datasource_id":
		return s.DatasourceID
	case "datasource_type":
		return s.DatasourceType
	case "datasource_name":
		return s.DatasourceName
	case "params":
		return jsonText(s.Params)
	case "description":
		return s.Description
	case "cache_timeout":
		return s.CacheTimeout
	case "certified_by":
		return s.CertifiedBy
	case "certification_details":
		return s.CertificationDetails
	}
	return nil
}

// dashboardColumn reads the current value of a writable dashboard column.
func dashboardColumn(d *models.Dashboard, col string) any {
	switch col {
	case "dashboard_title":
		return d.DashboardTitle
	case "slug":
		return d.Slug
	case "published":
		return d.Published
	case "position_json":
		return jsonText(d.PositionJSON)
	case "json_metadata":
		return jsonText(d.JSONMetadata)
	case "css":
		return d.CSS
	case "description":
		return d.Description
	case "certified_by":
		return d.CertifiedBy
	case "certification_details":
		return d.CertificationDetails
	}
	return nil
}

// selectColumns keeps only writable columns, normalising JSON object values to
// JSON strings so they can be written into jsonb columns.
func selectColumns(payload map[string]any, allowed map[string]bool) map[string]any {
	out := map[string]any{}
	for k, v := range payload {
		if !allowed[k] || v == nil {
			continue
		}
		if k == "params" || k == "position_json" || k == "json_metadata" {
			out[k] = mustJSON(v)
			continue
		}
		out[k] = v
	}
	return out
}

// mustJSON renders a value as a JSON string, passing strings through unchanged.
func mustJSON(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func jsonText(v datatypes.JSON) string {
	if len(v) == 0 {
		return ""
	}
	return string(v)
}

// RollbackRun undoes the writes a run performed, in reverse order, using the
// pre-images stored in ai_tool_call_log.
func (s *Service) RollbackRun(ctx context.Context, runID int) (*RollbackResult, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	rows, err := s.Audit.WriteCalls(ctx, runID)
	if err != nil {
		return nil, err
	}
	res := &RollbackResult{RunID: runID, Details: []string{}}
	for _, row := range rows {
		var snap writeSnapshot
		if err := json.Unmarshal([]byte(row.Snapshot), &snap); err != nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("step %d: 快照解析失败", row.StepNo))
			continue
		}
		if err := s.applyRollback(ctx, snap); err != nil {
			res.Skipped++
			res.Details = append(res.Details, fmt.Sprintf("step %d %s#%d: %v", row.StepNo, snap.Entity, snap.TargetID, err))
			continue
		}
		res.RolledBack++
		res.Details = append(res.Details, fmt.Sprintf("step %d 已回滚 %s#%d(%s)", row.StepNo, snap.Entity, snap.TargetID, snap.Op))
	}
	return res, nil
}

// applyRollback restores one write to its pre-image.
func (s *Service) applyRollback(ctx context.Context, snap writeSnapshot) error {
	db := s.DB.WithContext(ctx)
	switch snap.Entity {
	case "chart":
		if snap.Op == "create" {
			return softDeleteSlice(db, snap.TargetID)
		}
		return updateColumns(db, &models.Slice{}, snap.TargetID, snap.Before)
	case "dashboard":
		if snap.Op == "create" {
			return softDeleteDashboard(db, snap.TargetID)
		}
		for _, id := range snap.Created {
			if err := softDeleteSlice(db, id); err != nil {
				return err
			}
		}
		return updateColumns(db, &models.Dashboard{}, snap.TargetID, snap.Before)
	}
	return fmt.Errorf("unknown entity %q", snap.Entity)
}

// updateColumns writes a pre-image back onto a row, skipping empty pre-images.
func updateColumns(db *gorm.DB, model any, id int, before map[string]any) error {
	if len(before) == 0 {
		return nil
	}
	return db.Model(model).Where("id = ?", id).Updates(before).Error
}

func softDeleteSlice(db *gorm.DB, id int) error {
	now := time.Now()
	if err := db.Where("slice_id = ?", id).Delete(&models.DashboardSlices{}).Error; err != nil {
		return err
	}
	if err := db.Where("slice_id = ?", id).Delete(&models.SliceUser{}).Error; err != nil {
		return err
	}
	return db.Model(&models.Slice{}).Where("id = ?", id).Updates(map[string]any{"deleted_at": &now}).Error
}

func softDeleteDashboard(db *gorm.DB, id int) error {
	now := time.Now()
	if err := db.Where("dashboard_id = ?", id).Delete(&models.DashboardSlices{}).Error; err != nil {
		return err
	}
	return db.Model(&models.Dashboard{}).Where("id = ?", id).Updates(map[string]any{"deleted_at": &now}).Error
}
