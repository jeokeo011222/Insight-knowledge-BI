package api

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"go.uber.org/zap"

	"superset/internal/models"
	"superset/internal/queries"
	"superset/internal/security"
)

// A chart may only reference a handful of layers: the editor is a picker, not
// a query language. The cap keeps a hand-crafted form_data from fanning out.
const annotationLayerLimit = 12

// annotationRowLimit caps the events returned with one chart. A layer that
// accumulated years of daily markers must not turn every dashboard render into
// a megabyte of JSON; the newest events are kept.
const annotationRowLimit = 500

// chartAnnotation is one event as the chart renderer consumes it. The wire
// shape stays flat on purpose: the renderer only needs "what, when, which
// layer", and Superset's own annotation_data already speaks in these names.
type chartAnnotation struct {
	ID         int        `json:"id"`
	LayerID    int        `json:"layer_id"`
	Layer      string     `json:"layer"`
	ShortDescr string     `json:"short_descr"`
	LongDescr  string     `json:"long_descr"`
	StartDttm  *time.Time `json:"start_dttm"`
	EndDttm    *time.Time `json:"end_dttm"`
}

// annotationIDsFromContext collects the layer ids a chart asked for.
//
// Both sources matter: the editor writes them into form_data (so the selection
// is saved with the chart) and into the query object; a dashboard tile rebuilds
// the query context from the stored params, so form_data is the only place they
// survive. Reading both keeps editor and dashboard in agreement.
func annotationIDsFromContext(qc *queries.QueryContext) []int {
	if qc == nil {
		return nil
	}
	ids := make([]int, 0, 4)
	seen := make(map[int]struct{}, 4)
	add := func(list []any) {
		for _, raw := range list {
			id := annotationID(raw)
			if id <= 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
			if len(ids) >= annotationLayerLimit {
				return
			}
		}
	}
	for i := range qc.Queries {
		add(qc.Queries[i].AnnotationLayers)
		if len(ids) >= annotationLayerLimit {
			return ids
		}
	}
	if qc.FormData != nil {
		if list, ok := qc.FormData["annotation_layers"].([]any); ok {
			add(list)
		}
	}
	return ids
}

// annotationID accepts every shape the selection may arrive in: a JSON number,
// a numeric string (form inputs), or an object carrying id/value/layer_id.
func annotationID(raw any) int {
	switch value := raw.(type) {
	case int:
		if value > 0 {
			return value
		}
		return 0
	case int32:
		if value > 0 {
			return int(value)
		}
		return 0
	case int64:
		if value > 0 && value <= math.MaxInt32 {
			return int(value)
		}
		return 0
	case float64:
		if value <= 0 || value != math.Trunc(value) || value > math.MaxInt32 {
			return 0
		}
		return int(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed)
		}
		return 0
	case string:
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
		return 0
	case map[string]any:
		for _, key := range []string{"id", "value", "layer_id"} {
			if nested, ok := value[key]; ok {
				if id := annotationID(nested); id > 0 {
					return id
				}
			}
		}
	}
	return 0
}

// annotationWindow is the widest time range covered by the query context. It
// is used to drop events that cannot land on the x axis anyway.
type annotationWindow struct {
	From *time.Time
	To   *time.Time
}

func annotationWindowOf(qc *queries.QueryContext) annotationWindow {
	var window annotationWindow
	if qc == nil {
		return window
	}
	for i := range qc.Queries {
		from := annotationParseTime(qc.Queries[i].FromDttm)
		to := annotationParseTime(qc.Queries[i].ToDttm)
		if from != nil && (window.From == nil || from.Before(*window.From)) {
			window.From = from
		}
		if to != nil && (window.To == nil || to.After(*window.To)) {
			window.To = to
		}
	}
	return window
}

// canReadAnnotations reports whether the caller may see annotation events.
// Chart data is readable by anyone with query permission on the chart, so a
// viewer without annotation rights gets no events instead of a 403: the chart
// still renders, just without the overlays.
func canReadAnnotations(ctx context.Context) bool {
	set, ok := security.PermissionsFrom(ctx)
	if !ok {
		// No permission set on the context means the request was not wrapped by
		// the auth middleware (tests, internal calls). Stay permissive here and
		// let the route-level guard decide who reaches this handler at all.
		return true
	}
	if set.Admin() {
		return true
	}
	return set.Has(security.ActionRead + " on " + security.ResAnnotation)
}

// annotationData loads the events of the selected layers, newest first.
func (c *chartAPI) annotationData(ctx context.Context, qc *queries.QueryContext) []chartAnnotation {
	ids := annotationIDsFromContext(qc)
	if len(ids) == 0 || !canReadAnnotations(ctx) {
		return nil
	}

	var layers []models.AnnotationLayer
	if err := c.deps.DB.WithContext(ctx).
		Where("id IN ?", ids).
		Where("deleted = ?", false).
		Find(&layers).Error; err != nil {
		// A broken annotation lookup must never fail the chart itself.
		c.logAnnotationError(err)
		return nil
	}
	if len(layers) == 0 {
		return nil
	}
	names := make(map[int]string, len(layers))
	live := make([]int, 0, len(layers))
	for i := range layers {
		names[layers[i].ID] = layers[i].Name
		live = append(live, layers[i].ID)
	}

	window := annotationWindowOf(qc)
	db := c.deps.DB.WithContext(ctx).
		Model(&models.Annotation{}).
		Where("layer_id IN ?", live).
		Where("deleted = ?", false).
		Where("start_dttm IS NOT NULL OR end_dttm IS NOT NULL")
	if window.From != nil {
		// An event without an end is a point in time: it still counts when it
		// starts inside the window.
		db = db.Where("end_dttm IS NULL OR end_dttm >= ?", *window.From)
	}
	if window.To != nil {
		db = db.Where("start_dttm IS NULL OR start_dttm <= ?", *window.To)
	}

	var rows []models.Annotation
	if err := db.Order("start_dttm DESC NULLS LAST").Limit(annotationRowLimit).Find(&rows).Error; err != nil {
		c.logAnnotationError(err)
		return nil
	}

	out := make([]chartAnnotation, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		out = append(out, chartAnnotation{
			ID:         row.ID,
			LayerID:    row.LayerID,
			Layer:      names[row.LayerID],
			ShortDescr: row.ShortDescr,
			LongDescr:  row.LongDescr,
			StartDttm:  row.StartDttm,
			EndDttm:    row.EndDttm,
		})
	}
	return out
}

// logAnnotationError keeps the chart alive when annotations cannot be read.
func (c *chartAPI) logAnnotationError(err error) {
	if c.deps == nil || c.deps.Log == nil {
		return
	}
	c.deps.Log.Warn("failed to load annotation data", zap.Error(err))
}
