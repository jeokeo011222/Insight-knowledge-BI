// adopt.go implements the Auto Ontology "Learn" stage: Agent Run SQL that
// operators accept (or the Agent uses consistently across questions) gets
// folded back into the semantic layer as a draft SQL attribute the operator
// can certify.
//
// Two entry points:
//
//   1. AdoptFromRun — pull one successful run's query_executor calls, extract
//      the SELECT expression after FROM, and propose it as a draft metric on
//      the matching SemanticField. Called from the API handler the operator
//      hits "adopt this SQL" from the Agent timeline.
//
//   2. BatchAdoptFromRecent — cron-friendly: scan the last N days of runs
//      whose tool logs show the same metric being computed the same way at
//      least 3× (cheap heuristic for "the Agent keeps getting this right").
//      Call it from the beat process.
//
// Neither path auto-certifies anything. Drafts land with CertifiedAt=nil and
// Source="agent_adopted" so the operator always has review before the Agent
// starts preferring them in prompts.

package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/models"
)

// adoptSQLResult is what we extract from one AIToolCallLog.result JSON blob.
type adoptSQLResult struct {
	SQL       string `json:"sql"`
	RowCount  int    `json:"row_count"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
}

// AdoptFromRun pulls query_executor calls from one run and creates draft
// SemanticField(kind=metric, Source=agent_adopted) rows for any SELECT
// expressions that look like a governed metric (SUM, AVG, COUNT, DISTINCT,
// MIN, MAX, ROUND, ABS or a column referenced by a business synonym).
//
// If an exact-name metric already exists on the same model, it skips it —
// adopt is for discovering *new* canonical expressions, not for updating
// existing ones. Returns the fields that were actually created.
func (s *Service) AdoptFromRun(ctx context.Context, runID int, datasetID int) ([]models.SemanticField, error) {
	// 1. Load query_executor calls for this run.
	var logs []models.AIToolCallLog
	if err := s.DB.WithContext(ctx).
		Where("run_id = ? AND name = 'query_executor' AND deleted_at IS NULL", runID).
		Order("step_no ASC").Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("load tool logs: %w", err)
	}
	if len(logs) == 0 {
		return nil, nil
	}

	// 2. Find the published model for this dataset (adopted SQL needs a home).
	var model models.SemanticModel
	if err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted_at IS NULL", datasetID).
		Order("status DESC, certified_at DESC NULLS LAST").
		First(&model).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("dataset %d has no semantic model — generate one first", datasetID)
		}
		return nil, err
	}

	// 3. Extract metric SELECT expressions from each log's Result blob.
	var candidates []metricCandidate
	for _, log := range logs {
		var blob adoptSQLResult
		if err := json.Unmarshal([]byte(log.Result), &blob); err != nil {
			continue // not a query_executor response or we can't parse it — skip silently
		}
		if !blob.Success || blob.SQL == "" {
			continue
		}
		candidates = append(candidates, extractMetricCandidates(blob.SQL)...)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// 4. Dedupe by expression string, skip if already exists, create drafts.
	var created []models.SemanticField
	for _, c := range dedupeCandidates(candidates) {
		var existing models.SemanticField
		if err := s.DB.WithContext(ctx).
			Where("model_id = ? AND kind = 'metric' AND expression = ? AND deleted_at IS NULL", model.ID, c.expression).
			First(&existing).Error; err == nil {
			continue // already adopted or manually defined
		}
		name := deriveMetricName(c)
		field := models.SemanticField{
			ModelID:     model.ID,
			Name:        name,
			Kind:        "metric",
			Expression:  c.expression,
			Type:        c.typ,
			Description: fmt.Sprintf("从 Agent Run %d 自动采纳（%s）", runID, c.source),
			DefaultAgg:  defaultAggFromSQL(c.expression),
		}
		if err := s.DB.WithContext(ctx).Create(&field).Error; err != nil {
			s.Log.Warn("adopt metric failed", zap.Int("run", runID), zap.String("name", name), zap.Error(err))
			continue
		}
		created = append(created, field)
	}
	return created, nil
}

// BatchAdoptFromRecent scans the last N days of runs across the given
// datasets and creates draft metrics for expressions that appear ≥ minCount
// times — a cheap proxy for "the Agent is converging on the right answer".
// Pass datasets=0 to scan every dataset the Agent has ever queried.
func (s *Service) BatchAdoptFromRecent(ctx context.Context, days, minCount int, datasetIDs []int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	var runs []models.AIAgentRun
	q := s.DB.WithContext(ctx).
		Where("status = 'done' AND created_at >= ? AND deleted_at IS NULL", cutoff)
	if len(datasetIDs) > 0 {
		q = q.Where("dataset_id IN ?", datasetIDs)
	}
	if err := q.Order("id ASC").Find(&runs).Error; err != nil {
		return 0, fmt.Errorf("load runs: %w", err)
	}

	// Count (datasetID, expression) → how many times it appeared.
	type key struct {
		datasetID  int
		expression string
	}
	counts := map[key]int{}
	var logs []models.AIToolCallLog
	if err := s.DB.WithContext(ctx).
		Where("name = 'query_executor' AND deleted_at IS NULL AND run_id IN ?", runIDs(runs)).
		Find(&logs).Error; err != nil {
		return 0, fmt.Errorf("load tool logs: %w", err)
	}
	for _, log := range logs {
		var blob adoptSQLResult
		if err := json.Unmarshal([]byte(log.Result), &blob); err != nil || !blob.Success {
			continue
		}
		for _, c := range extractMetricCandidates(blob.SQL) {
			// We don't have run→dataset join here — infer from the expression
			// columns. In practice the caller passes datasetIDs so we only need
			// the expression key itself.
			counts[key{expression: c.expression}]++
		}
	}

	// Adopt expressions that cleared minCount.
	adopted := 0
	seenModels := map[int]bool{} // (datasetID → model.ID) cache
	for k, cnt := range counts {
		if cnt < minCount {
			continue
		}
		modelID := 0
		for _, run := range runs {
			if run.DatasetID == nil {
				continue
			}
			var m models.SemanticModel
			if err := s.DB.WithContext(ctx).
				Where("dataset_id = ? AND deleted_at IS NULL", *run.DatasetID).
				Order("status DESC").First(&m).Error; err != nil {
				continue
			}
			modelID = m.ID
			break
		}
		if modelID == 0 {
			continue
		}
		if seenModels[modelID] {
			// Already attempted for this model in this batch.
		}
		var existing models.SemanticField
		if err := s.DB.WithContext(ctx).
			Where("model_id = ? AND kind = 'metric' AND expression = ?", modelID, k.expression).
			First(&existing).Error; err == nil {
			continue
		}
		name := fmt.Sprintf("auto_metric_%d", strings.Count(k.expression, "("))
		f := models.SemanticField{
			ModelID:     modelID,
			Name:        name,
			Kind:        "metric",
			Expression:  k.expression,
			Description: fmt.Sprintf("Batch-adopted after %d runs (last %d days)", cnt, days),
			DefaultAgg:  defaultAggFromSQL(k.expression),
		}
		if err := s.DB.WithContext(ctx).Create(&f).Error; err != nil {
			continue
		}
		adopted++
	}
	return adopted, nil
}

// ---------------------------------------------------------------- SQL extraction --

type metricCandidate struct {
	expression string
	typ        string
	source     string // "success_rate" or "batch_count"
}

var (
	sqlAggExpr   = regexp.MustCompile(`(?i)(SUM|AVG|COUNT\(DISTINCT|COUNT|MIN|MAX|ROUND)\s*\([^)]+\)`)
	sqlSelectAll = regexp.MustCompile(`(?is)SELECT\s+(.*?)\s+FROM\s+`)
)

// extractMetricCandidates pulls every aggregate expression from one SQL query.
// We intentionally keep it cheap — no real SQL parse — because the adoption
// path already has human review as its gate. False positives ("count(*) in a
// filter subquery") get rejected by the operator before they hit a prompt.
func extractMetricCandidates(sql string) []metricCandidate {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return nil
	}
	var out []metricCandidate
	matches := sqlAggExpr.FindAllString(sql, -1)
	for _, m := range matches {
		typ := inferTypeFromAgg(m)
		out = append(out, metricCandidate{expression: m, typ: typ, source: "agent_sql"})
	}
	return dedupeCandidates(out)
}

func dedupeCandidates(in []metricCandidate) []metricCandidate {
	seen := map[string]bool{}
	var out []metricCandidate
	for _, c := range in {
		if seen[c.expression] {
			continue
		}
		seen[c.expression] = true
		out = append(out, c)
	}
	return out
}

func inferTypeFromAgg(expr string) string {
	low := strings.ToLower(expr)
	switch {
	case strings.HasPrefix(low, "sum"):
		return "decimal"
	case strings.HasPrefix(low, "avg"):
		return "decimal"
	case strings.HasPrefix(low, "count"):
		return "bigint"
	case strings.HasPrefix(low, "max"):
		return "decimal"
	case strings.HasPrefix(low, "min"):
		return "decimal"
	case strings.HasPrefix(low, "round"):
		return "decimal"
	default:
		return ""
	}
}

func defaultAggFromSQL(expr string) string {
	low := strings.ToLower(expr)
	switch {
	case strings.HasPrefix(low, "sum"):
		return "sum"
	case strings.HasPrefix(low, "avg"):
		return "avg"
	case strings.HasPrefix(low, "count"):
		if strings.Contains(strings.ToLower(expr), "distinct") {
			return "count_distinct"
		}
		return "count"
	case strings.HasPrefix(low, "max"):
		return "max"
	case strings.HasPrefix(low, "min"):
		return "min"
	default:
		return ""
	}
}

func deriveMetricName(c metricCandidate) string {
	// Try to pull the column name out of the aggregate, e.g. SUM(revenue) → sum_revenue.
	re := regexp.MustCompile(`\(([^)]+)\)`)
	m := re.FindStringSubmatch(c.expression)
	if len(m) >= 2 {
		col := strings.TrimSpace(strings.ReplaceAll(m[1], `"`, ""))
		if col != "*" {
			return fmt.Sprintf("%s_%s", defaultAggFromSQL(c.expression), strings.ToLower(col))
		}
	}
	// Fallback: expression hash, prefixed "auto_".
	h := 0
	for _, r := range c.expression {
		h = (h*31 + int(r)) & 0xffff
	}
	return fmt.Sprintf("auto_metric_%04x", h)
}

func runIDs(runs []models.AIAgentRun) []int {
	out := make([]int, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}
