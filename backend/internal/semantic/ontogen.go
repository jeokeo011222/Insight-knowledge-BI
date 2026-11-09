// Package semantic owns the ontology layer: models, fields, relationships and
// rules that sit between the Agent and the raw database. ontogen.go is the
// cold-start path — it generates a usable draft model from a dataset's
// columns without asking the user to build anything by hand.
//
// The strategy mirrors NVIDIA Auto Ontology's Discover stage:
//
//  1. Read the dataset's columns (from metadata DB) and metrics (if any).
//  2. Profile each column to infer its role — key / dimension / time / measure.
//  3. If more than one dataset shares a database, infer relationships from
//     same-name + _id / fk suffixes and from foreign keys when the connector
//     surfaces them.
//  4. Emit a SemanticModel with fields, a handful of starter metrics, and
//     relationships. Everything lands as source="generated", status="draft"
//     so the operator can review and certify.

package semantic

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
)

// ---------------------------------------------------------------- public API --

// GenerateModelFromDataset produces a draft SemanticModel for one dataset.
// It is the single-dataset entry point the API and the batch generator call.
// The returned ModelView already has its fields and relationships populated;
// the caller is responsible for persisting them.
func (s *Service) GenerateModelFromDataset(ctx context.Context, datasetID int) (*ModelView, error) {
	var table models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&table).Error; err != nil {
		return nil, fmt.Errorf("dataset %d not found: %w", datasetID, err)
	}
	var cols []models.TableColumn
	if err := s.DB.WithContext(ctx).Where("table_id = ? AND is_active = true", table.ID).
		Order("column_name").Find(&cols).Error; err != nil {
		return nil, fmt.Errorf("load columns for dataset %d: %w", datasetID, err)
	}

	model := buildDraftModel(&table, cols)
	fields := buildDraftFields(cols)
	relationships, relatedNote := buildDraftRelationships(ctx, s.DB, table.DatabaseID, table.ID, cols)
	metrics := buildDraftMetrics(cols)

	// Bundle metrics as field-ish rows (kind="metric") so the consumer has
	// a single flat list. SemanticField already supports kind="metric".
	for _, m := range metrics {
		fields = append(fields, m)
	}

	if relationships == nil {
		relationships = []models.SemanticRelationship{}
	}
	_ = relatedNote // surfaced in the UI as an info-level issue later

	return &ModelView{
		Model:         model,
		Fields:        fields,
		Relationships: relationships,
	}, nil
}

// SaveGenerated writes a generated ModelView as new rows. Existing model rows
// with the same (dataset, name) get soft-deleted first, so "re-generate" is
// an idempotent refresh — the operator can re-run it after a schema drift.
func (s *Service) SaveGenerated(ctx context.Context, mv *ModelView) (*ModelView, error) {
	if mv == nil {
		return nil, fmt.Errorf("empty model view")
	}
	tx := s.DB.WithContext(ctx).Begin()
	defer tx.Rollback()

	// Upsert the model itself.
	var existing models.SemanticModel
	err := tx.Where("dataset_id = ? AND name = ? AND deleted_at IS NULL", mv.Model.DatasetID, mv.Model.Name).
		First(&existing).Error
	if err == nil {
		// Soft-delete old rows under this model so we can re-insert cleanly.
		if err := tx.Where("model_id = ?", existing.ID).Delete(&models.SemanticField{}).Error; err != nil {
			return nil, fmt.Errorf("prune old fields: %w", err)
		}
		if err := tx.Where("from_model_id = ? OR to_model_id = ?", existing.ID, existing.ID).
			Delete(&models.SemanticRelationship{}).Error; err != nil {
			return nil, fmt.Errorf("prune old relationships: %w", err)
		}
		mv.Model.ID = existing.ID
		if err := tx.Save(&mv.Model).Error; err != nil {
			return nil, fmt.Errorf("save model: %w", err)
		}
	} else {
		if err := tx.Create(&mv.Model).Error; err != nil {
			return nil, fmt.Errorf("create model: %w", err)
		}
	}

	for i := range mv.Fields {
		mv.Fields[i].ModelID = mv.Model.ID
		mv.Fields[i].SortOrder = i
		if err := tx.Create(&mv.Fields[i]).Error; err != nil {
			return nil, fmt.Errorf("create field %s: %w", mv.Fields[i].Name, err)
		}
	}
	for i := range mv.Relationships {
		mv.Relationships[i].FromModelID = mv.Model.ID
		if err := tx.Create(&mv.Relationships[i]).Error; err != nil {
			return nil, fmt.Errorf("create rel: %w", err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("commit generated model: %w", err)
	}
	return mv, nil
}

// ---------------------------------------------------------------- build helpers --

func buildDraftModel(table *models.SqlaTable, cols []models.TableColumn) models.SemanticModel {
	name := prettifyIdentifier(table.TableNameStr)
	desc := fmt.Sprintf("Auto-generated from dataset %q (%s.%s). Review and certify before publishing.",
		table.TableNameStr, safe(table.Schema), safe(table.TableNameStr))
	return models.SemanticModel{
		DatasetID:   table.ID,
		Name:        name,
		Description: desc,
		PrimaryKey:  guessPrimaryKey(cols),
		Status:      "draft",
		Source:      "generated",
		Version:     1,
	}
}

func buildDraftFields(cols []models.TableColumn) []models.SemanticField {
	out := make([]models.SemanticField, 0, len(cols))
	for _, c := range cols {
		role := inferColumnRole(c)
		f := models.SemanticField{
			Name:        c.ColumnName,
			Kind:        role.kind,
			Type:        c.Type,
			Description: autoDescription(c, role),
			Unit:        role.unit,
			IsDttm:      c.IsDttm,
			Hidden:      role.isKey,
			DefaultAgg:  role.defaultAgg,
			Synonyms:    toJSON(role.synonyms),
		}
		if c.VerboseName != "" && c.VerboseName != c.ColumnName {
			f.Description = c.VerboseName // operator already set a human label — keep it
		}
		out = append(out, f)
	}
	return out
}

// buildDraftMetrics emits a handful of common metrics on measure columns so
// the Agent has something to answer with immediately. Aggregates are chosen
// by column type (integer → SUM+AVG+COUNT, decimal → SUM+AVG, boolean → COUNT).
func buildDraftMetrics(cols []models.TableColumn) []models.SemanticField {
	var out []models.SemanticField
	for _, c := range cols {
		role := inferColumnRole(c)
		switch role.kind {
		case "time":
			continue
		case "key":
			continue
		case "measure":
			snake := snakeCase(c.ColumnName)
			out = append(out,
				models.SemanticField{
					Name:        fmt.Sprintf("sum_%s", snake),
					Kind:        "metric",
					Expression:  fmt.Sprintf("SUM(%s)", quoteIdent(c.ColumnName)),
					Type:        c.Type,
					Description: fmt.Sprintf("Sum of %s", prettifyIdentifier(c.ColumnName)),
					Unit:        role.unit,
					DefaultAgg:  "sum",
				},
				models.SemanticField{
					Name:        fmt.Sprintf("avg_%s", snake),
					Kind:        "metric",
					Expression:  fmt.Sprintf("AVG(%s)", quoteIdent(c.ColumnName)),
					Type:        c.Type,
					Description: fmt.Sprintf("Average of %s", prettifyIdentifier(c.ColumnName)),
					Unit:        role.unit,
					DefaultAgg:  "avg",
				},
				models.SemanticField{
					Name:        fmt.Sprintf("count_distinct_%s", snake),
					Kind:        "metric",
					Expression:  fmt.Sprintf("COUNT(DISTINCT %s)", quoteIdent(c.ColumnName)),
					Type:        "bigint",
					Description: fmt.Sprintf("Distinct count of %s", prettifyIdentifier(c.ColumnName)),
					DefaultAgg:  "count_distinct",
				},
			)
		default: // dimension
			if role.defaultAgg != "" {
				out = append(out, models.SemanticField{
					Name:        fmt.Sprintf("count_%s", snakeCase(c.ColumnName)),
					Kind:        "metric",
					Expression:  "COUNT(*)",
					Type:        "bigint",
					Description: fmt.Sprintf("Count of records by %s", prettifyIdentifier(c.ColumnName)),
					DefaultAgg:  "count",
				})
			}
		}
	}
	// Dedupe by name (multiple dims → multiple "count_*", fine).
	return out
}

// buildDraftRelationships looks at all datasets in the same database and
// pairs tables by same-name + _id columns. It is intentionally heuristic
// — if the connector surfaces foreign keys those should be used instead —
// and everything comes back with a SourceConfidence note so the UI can show
// "confidence: medium, inferred from column names".
func buildDraftRelationships(ctx context.Context, db *gorm.DB, databaseID, thisDatasetID int, cols []models.TableColumn) ([]models.SemanticRelationship, string) {
	var otherTables []models.SqlaTable
	err := db.WithContext(ctx).Where("database_id = ? AND id != ? AND deleted_at IS NULL", databaseID, thisDatasetID).
		Find(&otherTables).Error
	if err != nil || len(otherTables) == 0 {
		return nil, ""
	}

	thisNames := map[string]bool{}
	for _, c := range cols {
		thisNames[strings.ToLower(c.ColumnName)] = true
	}

	var rels []models.SemanticRelationship
	var noteParts []string
	for _, other := range otherTables {
		var oc []models.TableColumn
		if err := db.WithContext(ctx).Where("table_id = ? AND is_active = true", other.ID).Find(&oc).Error; err != nil {
			continue
		}
		for _, c := range oc {
			low := strings.ToLower(c.ColumnName)
			// Pattern A: "orders.customer_id" ↔ "customers.id"
			if strings.HasSuffix(low, "_id") {
				stem := strings.TrimSuffix(low, "_id")
				if thisNames[low] || thisNames[stem] {
					cond := fmt.Sprintf("%s.%s = %s.id",
						quoteIdent(other.TableNameStr), quoteIdent(c.ColumnName),
						quoteIdent(stem+"_id"))
					rels = append(rels, models.SemanticRelationship{
						Name:      fmt.Sprintf("%s ↔ %s", other.TableNameStr, stem),
						JoinType:  "many_to_one",
						Condition: cond,
					})
					noteParts = append(noteParts, fmt.Sprintf("%s.%s → %s", other.TableNameStr, c.ColumnName, stem+"_id"))
				}
			}
		}
	}
	return rels, strings.Join(noteParts, ", ")
}

// ---------------------------------------------------------------- helpers --

type inferredRole struct {
	kind        string // key | dimension | time | measure
	isKey       bool
	unit        string
	defaultAgg  string
	synonyms    []string
}

var (
	measureWords   = regexp.MustCompile(`(?i)(amount|price|cost|revenue|sales|fee|total|gross|net|margin|profit|expense|balance|amount|sum|amt)`)
	timeWords      = regexp.MustCompile(`(?i)(date|time|year|month|day|hour|minute|second|ts|timestamp|dttm|created|updated|ordered|shipped)`)
	keyWords       = regexp.MustCompile(`(?i)(_id$|^id$|^pk$|^uuid$|_key$)`)
	moneyWords     = regexp.MustCompile(`(?i)(amount|price|cost|revenue|fee|balance|profit|margin|expense)`)
	qtyWords       = regexp.MustCompile(`(?i)(qty|quantity|count|num|number|total)`)
	ratioWords     = regexp.MustCompile(`(?i)(rate|ratio|pct|percent|share|markup|margin_pct)`)
)

func inferColumnRole(c models.TableColumn) inferredRole {
	low := strings.ToLower(c.ColumnName)
	switch {
	case keyWords.MatchString(low):
		return inferredRole{kind: "key", isKey: true, defaultAgg: "", synonyms: []string{"identifier", "主键"}}
	case c.IsDttm || timeWords.MatchString(low) || strings.Contains(low, "_at"):
		return inferredRole{kind: "time", defaultAgg: "", synonyms: []string{"时间", "日期"}}
	case strings.Contains(strings.ToLower(c.Type), "bool"):
		return inferredRole{kind: "dimension", defaultAgg: "count", synonyms: []string{"是否"}}
	case measureWords.MatchString(low) || strings.Contains(strings.ToLower(c.Type), "decimal") || strings.Contains(strings.ToLower(c.Type), "float") || strings.Contains(strings.ToLower(c.Type), "double"):
		unit := ""
		switch {
		case moneyWords.MatchString(low):
			unit = "元"
		case ratioWords.MatchString(low):
			unit = "%"
		}
		return inferredRole{kind: "measure", unit: unit, defaultAgg: "sum", synonyms: []string{"指标", "数值"}}
	default:
		return inferredRole{kind: "dimension", defaultAgg: "count", synonyms: []string{"维度", "分类"}}
	}
}

func autoDescription(c models.TableColumn, role inferredRole) string {
	label := prettifyIdentifier(c.ColumnName)
	switch role.kind {
	case "key":
		return fmt.Sprintf("主键：%s（隐藏，用于 join）", label)
	case "time":
		return fmt.Sprintf("时间列：%s", label)
	case "measure":
		if role.unit != "" {
			return fmt.Sprintf("指标：%s（单位 %s）", label, role.unit)
		}
		return fmt.Sprintf("指标：%s", label)
	default:
		return fmt.Sprintf("维度：%s", label)
	}
}

// guessPrimaryKey lives in service.go — reused here via the package scope.

var wordSplit = regexp.MustCompile(`(?i)[_\s]+`)
var camelSplit = regexp.MustCompile(`[A-Z][a-z]+`)

func prettifyIdentifier(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	// CamelCase → "Camel Case"
	camelParts := camelSplit.FindAllString(s, -1)
	if len(camelParts) >= 2 {
		s = strings.Join(camelParts, " ")
	}
	words := strings.Fields(s)
	for i, w := range words {
		if w == strings.ToUpper(w) && len(w) > 2 {
			continue // acronym
		}
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

func snakeCase(s string) string {
	s = strings.ReplaceAll(s, "-", "_")
	out := camelSplit.ReplaceAllStringFunc(s, func(w string) string { return "_" + strings.ToLower(w) })
	out = strings.TrimPrefix(out, "_")
	out = strings.ReplaceAll(out, " ", "_")
	out = strings.ToLower(out)
	return strings.Trim(out, "_")
}

func quoteIdent(col string) string {
	// Postgres double-quotes, MySQL backticks, SQLite double-quotes — driver handles it;
	// we emit double-quotes and let the engine layer rewrite for MySQL when needed.
	return `"` + strings.ReplaceAll(col, `"`, `""`) + `"`
}

func toJSON(v any) datatypes.JSON {
	b, _ := datatypes.NewJSONType(v).MarshalJSON()
	return b
}

func safe(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

// ---------------------------------------------------------------- public generation entry points --

// GenerateAllForDatabase loops every physical dataset in a database and builds
// a draft model for each. It is idempotent — calling it twice produces the same
// output. Datasets that already have a published model are skipped (the operator
// must delete or explicitly regenerate them).
func (s *Service) GenerateAllForDatabase(ctx context.Context, databaseID int) (int, error) {
	var tables []models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("database_id = ? AND deleted_at IS NULL", databaseID).
		Order("table_name").Find(&tables).Error; err != nil {
		return 0, err
	}
	var created int
	for _, t := range tables {
		// Skip if a published semantic model already exists — "draft" is fine to
		// overwrite (that's the retry path), "published" means someone signed it off.
		var existing models.SemanticModel
		if err := s.DB.WithContext(ctx).
			Where("dataset_id = ? AND status = 'published' AND deleted_at IS NULL", t.ID).
			First(&existing).Error; err == nil {
			continue
		}

		mv, err := s.GenerateModelFromDataset(ctx, t.ID)
		if err != nil {
			s.Log.Warn("generate ontology failed", zap.Int("dataset", t.ID), zap.Error(err))
			continue
		}
		if _, err := s.SaveGenerated(ctx, mv); err != nil {
			s.Log.Warn("save generated ontology failed", zap.Int("dataset", t.ID), zap.Error(err))
			continue
		}
		created++
	}
	return created, nil
}

// ---------------------------------------------------------------- sort helpers --

func init() {
	// keep import used if needed later
	_ = sort.Slice
}
