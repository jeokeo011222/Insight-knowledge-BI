package ai

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/models"
)

// NL→SQL memory (范例记忆).
//
// WrenAI keeps `knowledge/sql/*.md` — human-confirmed question→SQL pairs — and
// recalls the top 3 into every planning prompt. It is the single highest
// return-on-effort idea in the whole reference project: no model change, no new
// infrastructure, and repeated questions get more accurate as the team uses the
// product.
//
// This implementation keeps the same three rules:
//
//	write back   confirmed by a human (button) or auto-captured for review;
//	recall       approved rows only, ranked by question similarity, top-k;
//	stay clean   normalised hash dedupes, rejected rows disappear from recall,
//	             and the export is markdown so examples can live in git.
//
// Recall is lexical (see textmatch.go) because the deployment has no embedding
// provider by default. That is a documented degradation, not a claim of vector
// search.

// Memory sources and statuses.
const (
	MemorySourceSeed      = "seed"
	MemorySourceConfirmed = "confirmed"
	MemorySourceAuto      = "auto"

	MemoryStatusApproved = "approved"
	MemoryStatusPending  = "pending"
	MemoryStatusRejected = "rejected"
)

// SQLMemoryInput is one example to persist.
type SQLMemoryInput struct {
	DatasetID int
	Question  string
	SQL       string
	SpecJSON  string
	Source    string
	Status    string
	RunID     *int
}

// normalizeQuestion folds a question into a comparable form: lower case, no
// punctuation, and whitespace only where it separates two ASCII words. Chinese
// does not need the spaces users sprinkle in ("上个月 华东的 GMV" and
// "上个月华东的gmv" must hash to the same example), while English does.
func normalizeQuestion(q string) string {
	runes := []rune(strings.ToLower(strings.TrimSpace(q)))
	isCJK := func(r rune) bool { return r >= 0x2E80 }
	isSpace := func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
	isPunct := func(r rune) bool {
		return strings.ContainsRune("，。、；：？！,.;:?!\"'`()（）[]【】<>《》-—_/\\|@#$%^&*+=~", r)
	}
	var b strings.Builder
	for i, r := range runes {
		switch {
		case isSpace(r):
			// Look at the nearest non-space neighbours on both sides.
			prev, next := rune(0), rune(0)
			for j := i - 1; j >= 0; j-- {
				if !isSpace(runes[j]) {
					prev = runes[j]
					break
				}
			}
			for j := i + 1; j < len(runes); j++ {
				if !isSpace(runes[j]) {
					next = runes[j]
					break
				}
			}
			if prev == 0 || next == 0 || isCJK(prev) || isCJK(next) {
				continue
			}
			b.WriteRune(' ')
		case isPunct(r):
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// questionFingerprint is the dedupe key of an example.
func questionFingerprint(q string) string { return hashText(normalizeQuestion(q)) }

// SaveSQLMemory persists (or refreshes) one example. A confirmed row is
// immediately recallable; an auto capture waits in the review queue.
func (s *Service) SaveSQLMemory(ctx context.Context, userID int, in SQLMemoryInput) (*models.AISQLMemory, error) {
	question := strings.TrimSpace(in.Question)
	sql := strings.TrimSpace(strings.TrimRight(in.SQL, ";"))
	if in.DatasetID <= 0 {
		return nil, fmt.Errorf("dataset_id is required")
	}
	if question == "" || sql == "" {
		return nil, fmt.Errorf("question and sql are required")
	}
	source := in.Source
	if source == "" {
		source = MemorySourceConfirmed
	}
	switch source {
	case MemorySourceSeed, MemorySourceConfirmed, MemorySourceAuto:
	default:
		return nil, fmt.Errorf("invalid source %q", source)
	}
	status := in.Status
	if status == "" {
		status = MemoryStatusApproved
		if source == MemorySourceAuto {
			status = MemoryStatusPending
		}
	}
	if status != MemoryStatusApproved && status != MemoryStatusPending && status != MemoryStatusRejected {
		return nil, fmt.Errorf("invalid status %q", status)
	}

	row := &models.AISQLMemory{
		DatasetID:    in.DatasetID,
		Question:     question,
		QuestionHash: questionFingerprint(question),
		SQLQuery:     sql,
		SpecJSON:     in.SpecJSON,
		Source:       source,
		Status:       status,
		Quality:      1,
		RunID:        in.RunID,
		CreatedBy:    userID,
	}
	// Upsert on (dataset_id, question_hash): saving the same question twice must
	// refine the example, not create a second one that competes with it.
	err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "dataset_id"}, {Name: "question_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"sql_query", "spec_json", "source", "status", "quality", "run_id", "changed_on",
		}),
	}).Create(row).Error
	if err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Where("dataset_id = ? AND question_hash = ?", in.DatasetID, row.QuestionHash).
		First(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

// RecallSQLMemory returns the approved examples most similar to the question.
// A hit counter is bumped best-effort so stale examples can be retired later.
func (s *Service) RecallSQLMemory(ctx context.Context, datasetID int, question string, k int) []models.AISQLMemory {
	if s.DB == nil || datasetID <= 0 || strings.TrimSpace(question) == "" {
		return nil
	}
	if k <= 0 {
		k = 3
	}
	var rows []models.AISQLMemory
	if err := s.DB.WithContext(ctx).
		Where("status = ? AND dataset_id IN (0, ?)", MemoryStatusApproved, datasetID).
		Order("hits desc, id desc").Limit(500).Find(&rows).Error; err != nil {
		return nil
	}
	ranked := rankByText(question, rows, func(m models.AISQLMemory) string {
		return m.Question + " " + m.SQLQuery
	}, k, 0.3)
	out := make([]models.AISQLMemory, 0, len(ranked))
	ids := make([]int, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Item)
		ids = append(ids, item.Item.ID)
	}
	if len(ids) > 0 {
		// Best effort: a failed counter update must never break the run.
		now := time.Now()
		s.DB.WithContext(ctx).Model(&models.AISQLMemory{}).Where("id IN ?", ids).
			Updates(map[string]any{"hits": gorm.Expr("hits + 1"), "last_hit_on": now})
	}
	return out
}

// memoryPrompt renders the recalled examples as a system-prompt section.
func (s *Service) memoryPrompt(ctx context.Context, datasetID int, question, lang string) string {
	if s.Config != nil && s.Config.AISQLMemoryTopK == 0 {
		return ""
	}
	k := 3
	if s.Config != nil && s.Config.AISQLMemoryTopK > 0 {
		k = s.Config.AISQLMemoryTopK
	}
	rows := s.RecallSQLMemory(ctx, datasetID, question, k)
	if len(rows) == 0 {
		return ""
	}
	en := isEnglish(lang)
	var b strings.Builder
	if en {
		b.WriteString("\nConfirmed examples for similar questions on this dataset ")
		b.WriteString("(validated SQL — reuse the joins/filters/definitions unless the question clearly differs):\n")
	} else {
		b.WriteString("\n本数据集上已确认的相似问法（下面是人工验证过的 SQL，除问题明显不同外，请沿用其中的关联/过滤/口径）：\n")
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "- Q: %s\n  SQL:\n```sql\n%s\n```\n", row.Question, row.SQLQuery)
	}
	return b.String()
}

// autoCaptureMemory records a successful run for review. It deliberately skips
// exploratory queries (a bare `SELECT * … LIMIT 10`) — remembering those teaches
// nothing and would drown the queue.
func (s *Service) autoCaptureMemory(ctx context.Context, run *models.AIAgentRun, sql string, rowCount int) {
	if s.DB == nil || run == nil || run.DatasetID == nil || *run.DatasetID <= 0 {
		return
	}
	if rowCount <= 0 || strings.TrimSpace(sql) == "" || isExploratorySQL(sql) {
		return
	}
	if len([]rune(run.Question)) < 4 {
		return
	}
	row := &models.AISQLMemory{
		DatasetID:    *run.DatasetID,
		Question:     run.Question,
		QuestionHash: questionFingerprint(run.Question),
		SQLQuery:     strings.TrimSpace(strings.TrimRight(sql, ";")),
		Source:       MemorySourceAuto,
		Status:       MemoryStatusPending,
		Quality:      1,
		RunID:        &run.ID,
		CreatedBy:    run.UserID,
	}
	// The run's context is cancelled when the request ends; capturing is a
	// side effect that must survive that, so it gets its own bounded context.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Never overwrite a confirmed example with an auto capture.
	if err := s.DB.WithContext(writeCtx).Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil && s.Log != nil {
		s.Log.Debug("auto memory capture skipped: " + err.Error())
	}
}

// exploratoryMarkers recognise a reusable analysis: filtering, grouping or
// aggregating. Word boundaries matter because the previous substring scan
// missed `SELECT ...\n  WHERE` and would have dropped a perfectly good example.
var exploratoryMarkers = regexp.MustCompile(`(?i)\b(where|group\s+by|having|over)\b|\b(count|sum|avg|min|max|stddev|variance|percentile_\w+)\s*\(`)

// isExploratorySQL reports whether a statement is a throwaway look rather than a
// reusable analysis.
func isExploratorySQL(sql string) bool {
	return !exploratoryMarkers.MatchString(sql)
}

// ExportSQLMemory renders the memory as markdown so it can live in git and move
// between environments — the property that makes Wren's `knowledge/sql` useful.
func (s *Service) ExportSQLMemory(ctx context.Context, datasetID int) (string, error) {
	q := s.DB.WithContext(ctx).Model(&models.AISQLMemory{}).
		Where("status <> ?", MemoryStatusRejected)
	if datasetID > 0 {
		q = q.Where("dataset_id = ?", datasetID)
	}
	var rows []models.AISQLMemory
	if err := q.Order("dataset_id, id").Find(&rows).Error; err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# NL→SQL memory\n\n")
	fmt.Fprintf(&b, "<!-- exported %s; %d examples -->\n\n", time.Now().Format(time.RFC3339), len(rows))
	current := -1
	for _, row := range rows {
		if row.DatasetID != current {
			fmt.Fprintf(&b, "\n## dataset %d\n\n", row.DatasetID)
			current = row.DatasetID
		}
		fmt.Fprintf(&b, "### %s\n\nsource: %s · status: %s · hits: %d\n\n```sql\n%s\n```\n\n",
			row.Question, row.Source, row.Status, row.Hits, row.SQLQuery)
	}
	return b.String(), nil
}
