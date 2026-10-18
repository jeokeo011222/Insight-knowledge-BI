package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// service.go owns persistence and the cold-start path for the semantic layer.

// Service is the semantic layer's entry point. It is stateless apart from the
// shared DB handle, so callers (HTTP API, Agent tools, gap scanner) can each
// hold their own instance without coordination.
type Service struct {
	DB  *gorm.DB
	Log *zap.Logger
}

// New builds a service.
func New(db *gorm.DB, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{DB: db, Log: log}
}

// ModelView is a model plus everything compiled around it. The UI needs all of
// it in one response: showing fields without the validation result is how the
// old semantic layer became a write-only form.
type ModelView struct {
	Model         models.SemanticModel          `json:"model"`
	Fields        []models.SemanticField        `json:"fields"`
	Relationships []models.SemanticRelationship `json:"relationships"`
	Rules         []models.SemanticRule         `json:"rules"`
	Manifest      *ModelSpec                    `json:"manifest,omitempty"`
	Issues        []Issue                       `json:"issues"`
	Publishable   bool                          `json:"publishable"`
}

// ---------------------------------------------------------------- queries --

// ListModels returns the models of a dataset ordered by name.
func (s *Service) ListModels(ctx context.Context, datasetID int) ([]models.SemanticModel, error) {
	var out []models.SemanticModel
	err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted = ?", datasetID, false).
		Order("name").Find(&out).Error
	return out, err
}

// GetModel loads a model with its fields, relationships, rules and validation.
func (s *Service) GetModel(ctx context.Context, id int) (*ModelView, error) {
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted = ?", id, false).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义模型 %d 不存在", id)
		}
		return nil, err
	}
	return s.view(ctx, m)
}

// Resolve finds a published model by name within a dataset.
func (s *Service) Resolve(ctx context.Context, datasetID int, name string) (*ModelView, error) {
	var m models.SemanticModel
	q := s.DB.WithContext(ctx).Where("dataset_id = ? AND deleted = ?", datasetID, false)
	if strings.TrimSpace(name) != "" {
		q = q.Where("name = ?", strings.TrimSpace(name))
	} else {
		q = q.Where("status = ?", "published")
	}
	if err := q.Order("id").First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			if strings.TrimSpace(name) != "" {
				return nil, apperrors.NotFound("数据集 %d 下没有名为 %q 的语义模型", datasetID, name)
			}
			return nil, apperrors.NotFound("数据集 %d 还没有发布语义模型", datasetID)
		}
		return nil, err
	}
	return s.view(ctx, m)
}

func (s *Service) view(ctx context.Context, m models.SemanticModel) (*ModelView, error) {
	var fields []models.SemanticField
	if err := s.DB.WithContext(ctx).Where("model_id = ? AND deleted = ?", m.ID, false).
		Order("sort_order, name").Find(&fields).Error; err != nil {
		return nil, err
	}
	var rels []models.SemanticRelationship
	if err := s.DB.WithContext(ctx).Where("(from_model_id = ? OR to_model_id = ?) AND deleted = ?", m.ID, m.ID, false).
		Order("id").Find(&rels).Error; err != nil {
		return nil, err
	}
	var rules []models.SemanticRule
	if err := s.DB.WithContext(ctx).
		Where("model_id = ? AND deleted = ? AND status <> ?", m.ID, false, "disabled").
		Order("sort_order, id").Find(&rules).Error; err != nil {
		return nil, err
	}
	// Dataset-scoped rules apply to every model of the dataset.
	var scoped []models.SemanticRule
	if err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND scope = ? AND deleted = ? AND status <> ?", m.DatasetID, models.RuleScopeDataset, false, "disabled").
		Order("sort_order, id").Find(&scoped).Error; err != nil {
		return nil, err
	}
	rules = append(scoped, rules...)

	physical, err := s.physicalColumns(ctx, m.DatasetID)
	if err != nil {
		return nil, err
	}
	names, err := s.modelNames(ctx, m.DatasetID)
	if err != nil {
		return nil, err
	}
	spec, issues := Compile(CompileInput{
		Model: m, Fields: fields, Relationships: rels, Rules: rules,
		PhysicalColumns: physical, ModelNames: names,
	})
	if issues == nil {
		issues = []Issue{}
	}
	return &ModelView{
		Model: m, Fields: fields, Relationships: rels, Rules: rules,
		Manifest: spec, Issues: issues, Publishable: !HasErrors(issues),
	}, nil
}

func (s *Service) physicalColumns(ctx context.Context, datasetID int) (map[string]bool, error) {
	var cols []models.TableColumn
	if err := s.DB.WithContext(ctx).
		Where("table_id = ? AND is_active = ? AND deleted = ?", datasetID, true, false).
		Find(&cols).Error; err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, nil // virtual dataset without synced columns: lenient
	}
	out := make(map[string]bool, len(cols))
	for _, c := range cols {
		out[c.ColumnName] = true
	}
	return out, nil
}

func (s *Service) modelNames(ctx context.Context, datasetID int) (map[int]string, error) {
	var rows []models.SemanticModel
	if err := s.DB.WithContext(ctx).Where("dataset_id = ? AND deleted = ?", datasetID, false).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out, nil
}

// --------------------------------------------------------------- mutation --

// ModelInput is the create/update payload.
type ModelInput struct {
	DatasetID      int            `json:"dataset_id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	PrimaryKey     string         `json:"primary_key"`
	Status         string         `json:"status"`
	Fields         []FieldInput   `json:"fields"`
	DefaultFilters datatypes.JSON `json:"default_filters"`
}

// FieldInput is one field in a create/update payload.
type FieldInput struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Expression  string   `json:"expression"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Unit        string   `json:"unit"`
	EnumValues  []string `json:"enum_values"`
	Synonyms    []string `json:"synonyms"`
	Hidden      bool     `json:"hidden"`
	PIILevel    string   `json:"pii_level"`
	DefaultAgg  string   `json:"default_agg"`
	IsDttm      bool     `json:"is_dttm"`
	SortOrder   int      `json:"sort_order"`
}

var modelNameRE = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// NormalizeModelName produces an identifier the Agent can use in text without
// quoting: lowercase, [a-z0-9_].
func NormalizeModelName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = modelNameRE.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "model"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "m_" + s
	}
	return s
}

// CreateModel persists a model and its fields atomically.
func (s *Service) CreateModel(ctx context.Context, in ModelInput) (*ModelView, error) {
	if in.DatasetID <= 0 {
		return nil, apperrors.BadRequest("dataset_id 必填")
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&models.SqlaTable{}).
		Where("id = ? AND deleted_at IS NULL", in.DatasetID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, apperrors.NotFound("数据集 %d 不存在", in.DatasetID)
	}
	name := NormalizeModelName(in.Name)
	if name == "" {
		return nil, apperrors.BadRequest("模型名必填")
	}
	m := models.SemanticModel{
		DatasetID: in.DatasetID, Name: name, Description: in.Description,
		PrimaryKey: in.PrimaryKey, Status: firstNonEmpty(in.Status, "draft"),
		Version: 1, Source: "manual", DefaultFilters: in.DefaultFilters,
	}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			if isDuplicate(err) {
				return apperrors.Conflict("数据集下已存在名为 %q 的语义模型", name)
			}
			return err
		}
		return insertFields(tx, m.ID, in.Fields)
	})
	if err != nil {
		return nil, err
	}
	return s.GetModel(ctx, m.ID)
}

// UpdateModel rewrites the header and, when Fields is non-nil, replaces the
// whole field list. A full replace is intentional: partial field edits make
// "which version is deployed" unanswerable, and the UI always submits all rows.
func (s *Service) UpdateModel(ctx context.Context, id int, in ModelInput) (*ModelView, error) {
	m, err := s.loadModel(ctx, id)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{}
	if strings.TrimSpace(in.Name) != "" {
		updates["name"] = NormalizeModelName(in.Name)
	}
	if in.Description != "" {
		updates["description"] = in.Description
	}
	if in.PrimaryKey != "" {
		updates["primary_key"] = in.PrimaryKey
	}
	if in.Status != "" {
		if in.Status != "draft" && in.Status != "published" {
			return nil, apperrors.BadRequest("status 只能是 draft 或 published")
		}
		updates["status"] = in.Status
	}
	if in.DefaultFilters != nil {
		updates["default_filters"] = in.DefaultFilters
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			updates["version"] = m.Version + 1
			if err := tx.Model(&models.SemanticModel{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				if isDuplicate(err) {
					return apperrors.Conflict("数据集下已存在同名语义模型")
				}
				return err
			}
		}
		if in.Fields != nil {
			if err := tx.Model(&models.SemanticField{}).
				Where("model_id = ?", id).Update("deleted", true).Error; err != nil {
				return err
			}
			if err := insertFields(tx, id, in.Fields); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetModel(ctx, id)
}

// DeleteModel soft-deletes a model and its fields. Relationships are removed
// too, otherwise the other end would keep a dangling reference that only shows
// up at query time.
func (s *Service) DeleteModel(ctx context.Context, id int) error {
	if _, err := s.loadModel(ctx, id); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.SemanticField{}).Where("model_id = ?", id).Update("deleted", true).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.SemanticRelationship{}).
			Where("from_model_id = ? OR to_model_id = ?", id, id).Update("deleted", true).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.SemanticRule{}).Where("model_id = ?", id).Update("deleted", true).Error; err != nil {
			return err
		}
		return tx.Model(&models.SemanticModel{}).Where("id = ?", id).Update("deleted", true).Error
	})
}

func insertFields(tx *gorm.DB, modelID int, fields []FieldInput) error {
	seen := map[string]int{}
	for i, f := range fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return apperrors.BadRequest("第 %d 行的字段名为空", i+1)
		}
		// Catch duplicates here: the partial unique index would reject them as a
		// 500, and the operator would see a database error instead of the row.
		if prev, ok := seen[name]; ok {
			return apperrors.BadRequest("字段 %q 在第 %d 行与第 %d 行重复", name, prev, i+1)
		}
		seen[name] = i + 1
		kind := f.Kind
		if kind == "" {
			kind = models.FieldKindColumn
		}
		order := f.SortOrder
		if order == 0 {
			order = i
		}
		row := models.SemanticField{
			ModelID: modelID, Name: name, Kind: kind,
			Expression: f.Expression, Type: f.Type, Description: f.Description,
			Unit: f.Unit, Hidden: f.Hidden,
			PIILevel:   firstNonEmpty(f.PIILevel, "none"),
			DefaultAgg: f.DefaultAgg, IsDttm: f.IsDttm || kind == models.FieldKindTime,
			SortOrder: order,
		}
		if f.EnumValues != nil {
			row.EnumValues = mustJSON(f.EnumValues)
		}
		if f.Synonyms != nil {
			row.Synonyms = mustJSON(f.Synonyms)
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// PublishModel validates and freezes the manifest hash. Publishing with errors
// is refused: a model that cannot compile would only fail later, inside a run.
func (s *Service) PublishModel(ctx context.Context, id int) (*ModelView, error) {
	v, err := s.GetModel(ctx, id)
	if err != nil {
		return nil, err
	}
	if HasErrors(v.Issues) {
		return v, apperrors.BadRequest("语义模型校验未通过，共 %d 个错误", countErrors(v.Issues)).WithExtra(map[string]any{
			"code": "SEMANTIC_MODEL_INVALID", "issues": v.Issues,
		})
	}
	now := time.Now()
	if err := s.DB.WithContext(ctx).Model(&models.SemanticModel{}).Where("id = ?", id).
		Updates(map[string]any{
			"status": "published", "manifest_hash": v.Manifest.ManifestSHA,
			"published_on": now, "version": gorm.Expr("version + 1"),
		}).Error; err != nil {
		return nil, err
	}
	return s.GetModel(ctx, id)
}

// ------------------------------------------------------------- cold start --

// ColdStart derives a model from the dataset. The goal is not perfection but a
// compiling starting point: the operator adds descriptions and relationships
// instead of typing forty field rows.
//
// Inference is deliberately conservative and every inferred value is marked in
// Evidence/Description so a human can tell "the machine guessed this" from
// "someone declared this".
func (s *Service) ColdStart(ctx context.Context, datasetID int, force bool) (*ModelView, error) {
	var ds models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("数据集 %d 不存在", datasetID)
		}
		return nil, err
	}
	var existing models.SemanticModel
	err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted = ?", datasetID, false).First(&existing).Error
	if err == nil && !force {
		return nil, apperrors.Conflict("数据集 %d 已有语义模型 %q，如需重建请传 force=true", datasetID, existing.Name)
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	var cols []models.TableColumn
	if err := s.DB.WithContext(ctx).
		Where("table_id = ? AND is_active = ? AND deleted = ?", datasetID, true, false).
		Order("id").Find(&cols).Error; err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, apperrors.BadRequest("数据集没有可用列，先同步列再生成语义模型")
	}
	var metrics []models.SqlMetric
	if err := s.DB.WithContext(ctx).
		Where("table_id = ? AND deleted = ?", datasetID, false).Order("id").Find(&metrics).Error; err != nil {
		return nil, err
	}

	fields := make([]FieldInput, 0, len(cols)+len(metrics))
	order := 0
	for _, c := range cols {
		kind := models.FieldKindColumn
		if c.IsDttm {
			kind = models.FieldKindTime
		}
		desc := strings.TrimSpace(c.Description)
		if desc == "" && c.VerboseName != "" {
			desc = c.VerboseName
		}
		fields = append(fields, FieldInput{
			Name: c.ColumnName, Kind: kind, Type: c.Type, Description: desc,
			Hidden: !c.AgentVisible, PIILevel: c.PIILevel,
			IsDttm: c.IsDttm, SortOrder: order,
		})
		order++
	}
	for _, mt := range metrics {
		fields = append(fields, FieldInput{
			Name: NormalizeModelName(mt.MetricName), Kind: models.FieldKindMetric,
			Expression: mt.Expression, Type: "number",
			Description: firstNonEmpty(mt.Description, mt.VerboseName),
			SortOrder:   order,
		})
		order++
	}

	name := NormalizeModelName(ds.TableNameStr)
	m := models.SemanticModel{
		DatasetID: datasetID, Name: name, Description: ds.Description,
		PrimaryKey: guessPrimaryKey(cols), Status: "draft", Version: 1, Source: "generated",
	}
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if existing.ID > 0 {
			m.ID = existing.ID
			if err := tx.Model(&models.SemanticField{}).Where("model_id = ?", existing.ID).Update("deleted", true).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.SemanticModel{}).Where("id = ?", existing.ID).
				Updates(map[string]any{
					"description": m.Description, "primary_key": m.PrimaryKey,
					"source": "generated", "version": gorm.Expr("version + 1"), "status": "draft",
				}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&m).Error; err != nil {
			if isDuplicate(err) {
				return apperrors.Conflict("数据集下已存在同名语义模型 %q", name)
			}
			return err
		}
		if err := insertFields(tx, m.ID, fields); err != nil {
			return err
		}
		// A soft-deleted table almost always means the operator wants it
		// filtered; recording it as a rule is cheaper than every future query
		// remembering to.
		if col, ok := softDeleteColumn(cols); ok {
			rule := models.SemanticRule{
				Scope: models.RuleScopeModel, DatasetID: datasetID, ModelID: m.ID,
				Category: models.RuleDefaultFilter, Title: "排除已删除记录",
				Body:   fmt.Sprintf("默认过滤 `%s IS NULL`，统计口径不含软删除记录。", col),
				Spec:   mustJSON(FilterSpec{Field: col, Op: "is_null", Reason: "默认排除软删除记录"}),
				Status: "active", Evidence: "cold_start: 检测到软删除列",
			}
			if err := tx.Create(&rule).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetModel(ctx, m.ID)
}

func guessPrimaryKey(cols []models.TableColumn) string {
	candidates := []string{"id", "uuid"}
	for _, want := range candidates {
		for _, c := range cols {
			if strings.EqualFold(c.ColumnName, want) {
				return c.ColumnName
			}
		}
	}
	return ""
}

func softDeleteColumn(cols []models.TableColumn) (string, bool) {
	for _, want := range []string{"deleted_at", "is_deleted", "deleted"} {
		for _, c := range cols {
			if strings.EqualFold(c.ColumnName, want) && !c.IsDttm {
				return c.ColumnName, true
			}
		}
	}
	return "", false
}

// ---------------------------------------------------------------- helpers --

func (s *Service) loadModel(ctx context.Context, id int) (*models.SemanticModel, error) {
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted = ?", id, false).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义模型 %d 不存在", id)
		}
		return nil, err
	}
	return &m, nil
}

func mustJSON(v any) datatypes.JSON {
	blob, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return datatypes.JSON(blob)
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}

func countErrors(issues []Issue) int {
	n := 0
	for _, i := range issues {
		if i.Severity == "error" {
			n++
		}
	}
	return n
}
