package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/textsearch"
)

// rules.go implements W-08: business rules with two consumers.
//
// The prompt channel is cheap and lossy (a model can ignore prose). The
// execution channel is narrow but reliable (a filter becomes part of the SQL).
// Every rule may use either or both; the point is that "ignore soft-deleted
// rows" does not depend on the model reading a paragraph.

// RuleInput is the create/update payload for a rule.
type RuleInput struct {
	Scope     string `json:"scope"`
	DatasetID int    `json:"dataset_id"`
	ModelID   int    `json:"model_id"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	// Spec is the machine-readable half. Accepts either an object or a list of
	// objects; a list creates one executable filter per entry.
	Spec     json.RawMessage `json:"spec"`
	Status   string          `json:"status"`
	Evidence string          `json:"evidence"`
}

// ListRules returns the rules that apply to a dataset (its own plus global).
func (s *Service) ListRules(ctx context.Context, datasetID int) ([]models.SemanticRule, error) {
	var out []models.SemanticRule
	q := s.DB.WithContext(ctx).Where("deleted = ?", false)
	if datasetID > 0 {
		q = q.Where("dataset_id = ? OR scope = ?", datasetID, models.RuleScopeGlobal)
	}
	err := q.Order("scope, category, sort_order, id").Find(&out).Error
	return out, err
}

// CreateRule validates the category and stores the rule.
func (s *Service) CreateRule(ctx context.Context, in RuleInput) (*models.SemanticRule, error) {
	rule := models.SemanticRule{
		Scope: firstNonEmpty(in.Scope, models.RuleScopeDataset),
		DatasetID: in.DatasetID, ModelID: in.ModelID,
		Category: strings.TrimSpace(in.Category),
		Title:    strings.TrimSpace(in.Title),
		Body:     strings.TrimSpace(in.Body),
		Status:   firstNonEmpty(in.Status, "active"),
		Evidence: in.Evidence, Version: 1,
	}
	if err := validateRule(rule); err != nil {
		return nil, err
	}
	if len(in.Spec) > 0 && string(in.Spec) != "null" {
		rule.Spec = normalizeSpec(in.Spec)
	}
	if rule.Category == models.RuleDefaultFilter && rule.Spec == nil {
		return nil, apperrors.BadRequest("default_filter 规则必须给出 spec，否则只是一段模型可能忽略的文字")
	}
	if rule.Scope == models.RuleScopeModel && rule.ModelID == 0 {
		return nil, apperrors.BadRequest("作用域为 model 的规则必须指定 model_id")
	}
	if rule.Scope == models.RuleScopeDataset && rule.DatasetID == 0 {
		return nil, apperrors.BadRequest("作用域为 dataset 的规则必须指定 dataset_id")
	}
	if err := s.DB.WithContext(ctx).Create(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// UpdateRule applies a partial update and bumps the version so a diff is
// meaningful.
func (s *Service) UpdateRule(ctx context.Context, id int, in RuleInput) (*models.SemanticRule, error) {
	var rule models.SemanticRule
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted = ?", id, false).First(&rule).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("规则 %d 不存在", id)
		}
		return nil, err
	}
	if in.Category != "" {
		rule.Category = strings.TrimSpace(in.Category)
	}
	if in.Title != "" {
		rule.Title = in.Title
	}
	if in.Body != "" {
		rule.Body = in.Body
	}
	if in.Status != "" {
		rule.Status = in.Status
	}
	if in.Evidence != "" {
		rule.Evidence = in.Evidence
	}
	if len(in.Spec) > 0 {
		rule.Spec = normalizeSpec(in.Spec)
	}
	if err := validateRule(rule); err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).Model(&models.SemanticRule{}).Where("id = ?", id).
		Updates(map[string]any{
			"category": rule.Category, "title": rule.Title, "body": rule.Body,
			"spec": rule.Spec, "status": rule.Status, "evidence": rule.Evidence,
			"version": gorm.Expr("version + 1"),
		}).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// DeleteRule soft-deletes a rule.
func (s *Service) DeleteRule(ctx context.Context, id int) error {
	res := s.DB.WithContext(ctx).Model(&models.SemanticRule{}).
		Where("id = ? AND deleted = ?", id, false).Update("deleted", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("规则 %d 不存在", id)
	}
	return nil
}

func validateRule(rule models.SemanticRule) error {
	valid := false
	for _, c := range models.RuleCategories {
		if rule.Category == c {
			valid = true
			break
		}
	}
	if !valid {
		return apperrors.BadRequest("category 必须是 %s 之一", strings.Join(models.RuleCategories, " / "))
	}
	if strings.TrimSpace(rule.Body) == "" && rule.Spec == nil {
		return apperrors.BadRequest("规则的 body 与 spec 不能同时为空")
	}
	switch rule.Scope {
	case models.RuleScopeGlobal, models.RuleScopeDataset, models.RuleScopeModel:
	default:
		return apperrors.BadRequest("scope 只能是 global / dataset / model")
	}
	if rule.Status != "active" && rule.Status != "disabled" {
		return apperrors.BadRequest("status 只能是 active / disabled")
	}
	return nil
}

// normalizeSpec accepts an object or an array and always stores an array. One
// shape in the column means one shape in every consumer.
func normalizeSpec(raw json.RawMessage) datatypes.JSON {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "["):
		var list []FilterSpec
		if err := json.Unmarshal(raw, &list); err == nil {
			blob, _ := json.Marshal(list)
			return datatypes.JSON(blob)
		}
	case strings.HasPrefix(trimmed, "{"):
		var one FilterSpec
		if err := json.Unmarshal(raw, &one); err == nil {
			blob, _ := json.Marshal([]FilterSpec{one})
			return datatypes.JSON(blob)
		}
	}
	return datatypes.JSON(trimmed)
}

// ruleSpecs decodes the stored array shape back into filters.
func ruleSpecs(raw datatypes.JSON) []FilterSpec {
	if len(raw) == 0 {
		return nil
	}
	var list []FilterSpec
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var one FilterSpec
	if err := json.Unmarshal(raw, &one); err == nil && one.Field != "" {
		return []FilterSpec{one}
	}
	return nil
}

// ExecutionFilters returns the filters that must be applied to every query
// against this model. Ordering is stable (model filters first, then the
// model's own DefaultFilters blob) so generated SQL is reproducible.
func (s *Service) ExecutionFilters(ctx context.Context, model *models.SemanticModel) ([]FilterSpec, error) {
	if model == nil {
		return nil, nil
	}
	var rules []models.SemanticRule
	if err := s.DB.WithContext(ctx).
		Where("deleted = ? AND status = ?", false, "active").
		Where("category = ?", models.RuleDefaultFilter).
		Where("(model_id = ?) OR (scope = ? AND dataset_id = ?)", model.ID, models.RuleScopeDataset, model.DatasetID).
		Order("sort_order, id").Find(&rules).Error; err != nil {
		return nil, err
	}
	out := make([]FilterSpec, 0, len(rules)+2)
	seen := map[string]bool{}
	push := func(fs FilterSpec) {
		key := fmt.Sprintf("%s|%s|%s", fs.Table, fs.Field, fs.Op)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, fs)
	}
	for _, r := range rules {
		for _, fs := range ruleSpecs(r.Spec) {
			fs.Reason = firstNonEmpty(fs.Reason, r.Title, r.Body)
			push(fs)
		}
	}
	// The model-level blob is a convenience for cold-started models; explicit
	// rules always win because they carry provenance.
	for _, fs := range ruleSpecs(model.DefaultFilters) {
		push(fs)
	}
	return out, nil
}

// PromptRules retrieves the rules most relevant to a question. The whole rule
// set is never injected: a long rulebook crowds out the schema and the model
// starts applying rules that do not apply.
func (s *Service) PromptRules(ctx context.Context, datasetID, modelID int, question string, k int) (string, error) {
	var rules []models.SemanticRule
	q := s.DB.WithContext(ctx).
		Where("deleted = ? AND status = ?", false, "active").
		Where("dataset_id = ? OR scope = ?", datasetID, models.RuleScopeGlobal)
	if err := q.Order("sort_order, id").Find(&rules).Error; err != nil {
		return "", err
	}
	if len(rules) == 0 {
		return "", nil
	}
	if k <= 0 {
		k = 3
	}
	// Model-scoped rules are always relevant to their own model; dataset and
	// global rules compete on relevance with the question.
	picked := make([]models.SemanticRule, 0, k)
	for _, r := range rules {
		if r.ModelID != 0 && r.ModelID == modelID {
			picked = append(picked, r)
		}
	}
	others := make([]models.SemanticRule, 0, len(rules))
	for _, r := range rules {
		if r.ModelID == 0 || r.ModelID != modelID {
			others = append(others, r)
		}
	}
	ranked := textsearch.Rank(question, others, func(r models.SemanticRule) string {
		return r.Title + " " + r.Body
	}, k, 0.15)
	for _, r := range ranked {
		picked = append(picked, r.Item)
	}
	if len(picked) == 0 {
		return "", nil
	}
	if len(picked) > k+2 {
		picked = picked[:k+2]
	}
	var b strings.Builder
	b.WriteString("## 业务规则（本问题相关，必须遵守）\n")
	for _, r := range picked {
		title := firstNonEmpty(r.Title, r.Category)
		fmt.Fprintf(&b, "- [%s] %s：%s\n", r.Category, title, strings.TrimSpace(r.Body))
	}
	return b.String(), nil
}
