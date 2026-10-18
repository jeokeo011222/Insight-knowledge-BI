package semantic

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// gaps.go implements W-07's backend: a scanner that turns "the model is vague"
// into a ranked list of specific questions.
//
// The retired metric-dictionary page failed because it was an empty form: the
// user had to already know what was missing. This scanner answers that instead
// of asking it, and every gap carries a draft so the cheapest action is "adopt".
//
// Two deliberate constraints:
//   - Metadata only by default. Sampling distinct values needs an explicit
//     SampleFunc, because it reads real rows and must be authorized first.
//   - Every gap is stable (ID derived from kind+target) so the UI can remember
//     "skipped" without a second table.

// Gap kinds. Fixed vocabulary: the UI groups by it and the apply endpoint
// dispatches on it.
const (
	GapMissingDescription = "missing_description"
	GapMissingEnum        = "missing_enum"
	GapMissingUnit        = "missing_unit"
	GapSoftDeleteNoRule   = "soft_delete_without_rule"
	GapCurrencyNoRule     = "currency_without_rule"
	GapTimeNoHint         = "time_without_hint"
	GapSimilarTables      = "similar_tables_without_canonical"
	GapMetricUnapproved   = "metric_not_approved"
	GapNoPublishedModel   = "no_published_model"
	GapNoRelationship     = "no_relationship"
	GapPIILevelMissing    = "pii_level_missing"
)

// Gap is one concrete thing to fix.
type Gap struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"` // high|medium|low
	Target   string `json:"target"`
	// Question is what the operator is asked. Exactly one question per gap.
	Question string `json:"question"`
	// Draft is the machine's guess, offered for adoption. Confidence says how
	// much to trust it; "low" means the user should rewrite rather than accept.
	Draft      string `json:"draft"`
	Confidence string `json:"confidence"`
	Evidence   string `json:"evidence"`
	// ApplyMode tells the UI which button to show: "column_description",
	// "dataset_description", "semantic_rule" or "" (manual only).
	ApplyMode string `json:"apply_mode"`
}

// GapReport is the scan result plus the coverage numbers the dashboard shows.
type GapReport struct {
	DatasetID int            `json:"dataset_id"`
	Score     int            `json:"score"` // 0-100, "context health"
	Coverage  map[string]any `json:"coverage"`
	Counts    map[string]int `json:"counts"`
	Gaps      []Gap          `json:"gaps"`
	Sampled   bool           `json:"sampled"`
	Note      string         `json:"note,omitempty"`
}

// Scanner computes gaps for one dataset.
type Scanner struct {
	DB  *gorm.DB
	Log *zap.Logger
	// Sample returns up to n distinct values of one column. It is only called
	// when the caller explicitly asked for sampling; a nil function means "not
	// authorized", which is reported instead of silently skipped.
	Sample func(ctx context.Context, datasetID int, column string, n int) ([]string, error)
}

// NewScanner builds a scanner.
func NewScanner(db *gorm.DB, log *zap.Logger) *Scanner {
	if log == nil {
		log = zap.NewNop()
	}
	return &Scanner{DB: db, Log: log}
}

var moneyHints = []string{"amount", "price", "cost", "revenue", "gmv", "fee", "salary", "金额", "收入", "价格", "费用"}
var timeHints = []string{"时区", "timezone", "utc", "事件时间", "业务日期"}

// Scan inspects metadata (and optionally samples) to produce the gap list.
func (sc *Scanner) Scan(ctx context.Context, datasetID int, allowSample bool) (*GapReport, error) {
	var ds models.SqlaTable
	if err := sc.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("数据集 %d 不存在", datasetID)
		}
		return nil, err
	}
	var cols []models.TableColumn
	if err := sc.DB.WithContext(ctx).
		Where("table_id = ? AND is_active = ? AND deleted = ?", datasetID, true, false).
		Order("id").Find(&cols).Error; err != nil {
		return nil, err
	}

	var modelsList []models.SemanticModel
	if err := sc.DB.WithContext(ctx).Where("dataset_id = ? AND deleted = ?", datasetID, false).
		Find(&modelsList).Error; err != nil {
		return nil, err
	}
	// Declared fields act as the ground truth for "already documented": a
	// description on the semantic field is as good as one on the column.
	declared := map[string]FieldSpec{}
	var modelIDs []int
	for _, m := range modelsList {
		modelIDs = append(modelIDs, m.ID)
	}
	if len(modelIDs) > 0 {
		var fields []models.SemanticField
		if err := sc.DB.WithContext(ctx).Where("model_id IN ? AND deleted = ?", modelIDs, false).
			Find(&fields).Error; err != nil {
			return nil, err
		}
		for _, f := range fields {
			var enum []string
			if vals, ok := stringSlice(f.EnumValues); ok {
				enum = vals
			}
			declared[f.Name] = FieldSpec{Name: f.Name, Description: f.Description, Unit: f.Unit, Enum: enum}
		}
	}

	var rules []models.SemanticRule
	if err := sc.DB.WithContext(ctx).
		Where("(dataset_id = ? OR scope = ?) AND deleted = ? AND status = ?", datasetID, models.RuleScopeGlobal, false, "active").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	ruleByCategory := map[string]int{}
	for _, r := range rules {
		ruleByCategory[r.Category]++
	}

	report := &GapReport{DatasetID: datasetID, Gaps: []Gap{}, Counts: map[string]int{}, Sampled: allowSample && sc.Sample != nil}
	add := func(g Gap) {
		report.Gaps = append(report.Gaps, g)
		report.Counts[g.Kind]++
	}

	described := 0
	enumReady := 0
	lowCardinality := 0
	var moneyCols, timeCols []string
	hasSoftDeleteColumn := ""
	for _, c := range cols {
		field, hasField := declared[c.ColumnName]
		desc := strings.TrimSpace(c.Description)
		if desc == "" && hasField {
			desc = strings.TrimSpace(field.Description)
		}
		unit := ""
		if hasField {
			unit = strings.TrimSpace(field.Unit)
		}
		if desc != "" {
			described++
		} else {
			add(Gap{
				ID:       GapMissingDescription + ":" + c.ColumnName,
				Kind:     GapMissingDescription, Severity: "high",
				Target:   c.ColumnName,
				Question: fmt.Sprintf("「%s」这一列存的是什么？（%s）", c.ColumnName, strings.TrimSpace(c.Type)),
				Draft:    draftDescription(c), Confidence: draftConfidence(c),
				Evidence: "column.description 为空",
				ApplyMode: "column_description",
			})
		}

		lower := strings.ToLower(c.ColumnName)
		isMoney := false
		for _, hint := range moneyHints {
			if strings.Contains(lower, hint) {
				isMoney = true
				break
			}
		}
		if isMoney {
			moneyCols = append(moneyCols, c.ColumnName)
			if unit == "" && !strings.Contains(desc, "元") && !strings.Contains(desc, "币") {
				add(Gap{
					ID:       GapMissingUnit + ":" + c.ColumnName,
					Kind:     GapMissingUnit, Severity: "medium",
					Target:   c.ColumnName,
					Question: fmt.Sprintf("「%s」的单位是什么？（元/万元/美元…）", c.ColumnName),
					Draft:    guessUnit(c.ColumnName), Confidence: "low",
					Evidence: "列名像金额但没有声明单位",
					ApplyMode: "column_description",
				})
			}
		}

		if c.IsDttm {
			timeCols = append(timeCols, c.ColumnName)
			hasTimeHint := false
			for _, hint := range timeHints {
				if strings.Contains(strings.ToLower(desc), hint) {
					hasTimeHint = true
					break
				}
			}
			if !hasTimeHint {
				add(Gap{
					ID:       GapTimeNoHint + ":" + c.ColumnName,
					Kind:     GapTimeNoHint, Severity: "low",
					Target:   c.ColumnName,
					Question: fmt.Sprintf("「%s」是哪个时区的业务时间？（例如 UTC 的事件时间 / 北京时间）", c.ColumnName),
					Draft:    "UTC 时间（待确认）", Confidence: "low",
					Evidence: "时间列未说明时区或事件/统计口径",
					ApplyMode: "column_description",
				})
			}
		}

		if strings.EqualFold(lower, "deleted_at") || strings.EqualFold(lower, "is_deleted") || strings.EqualFold(lower, "deleted") {
			hasSoftDeleteColumn = c.ColumnName
		}

		// Enum detection needs values, which needs authorization.
		enumDeclared := hasField && len(field.Enum) > 0
		if !enumDeclared && sc.Sample != nil && allowSample && isCategorical(c) {
			lowCardinality++
			values, err := sc.Sample(ctx, datasetID, c.ColumnName, 30)
			if err != nil {
				sc.Log.Debug("gap sampler failed", zap.String("column", c.ColumnName), zap.Error(err))
			} else if len(values) > 0 && len(values) <= 30 {
				add(Gap{
					ID:       GapMissingEnum + ":" + c.ColumnName,
					Kind:     GapMissingEnum, Severity: "medium",
					Target:   c.ColumnName,
					Question: fmt.Sprintf("「%s」的取值是固定枚举吗？", c.ColumnName),
					Draft:    strings.Join(values, " / "), Confidence: "high",
					Evidence: fmt.Sprintf("采样 %d 个不同值，全部落在此集合内", len(values)),
					ApplyMode: "column_description",
				})
			}
		} else if enumDeclared {
			enumReady++
		}
	}

	if hasSoftDeleteColumn != "" && ruleByCategory[models.RuleDefaultFilter] == 0 {
		add(Gap{
			ID:       GapSoftDeleteNoRule + ":" + hasSoftDeleteColumn,
			Kind:     GapSoftDeleteNoRule, Severity: "high",
			Target:   hasSoftDeleteColumn,
			Question: fmt.Sprintf("是否默认排除软删除记录（%s）？", hasSoftDeleteColumn),
			Draft:    fmt.Sprintf("默认过滤 %s IS NULL", hasSoftDeleteColumn),
			Confidence: "high",
			Evidence: "存在软删除列，但没有任何 default_filter 规则",
			ApplyMode: "semantic_rule",
		})
	}
	if len(moneyCols) > 0 && ruleByCategory[models.RuleCurrency] == 0 {
		add(Gap{
			ID:       GapCurrencyNoRule + ":" + strings.Join(moneyCols, ","),
			Kind:     GapCurrencyNoRule, Severity: "medium",
			Target:   moneyCols[0],
			Question: "这些金额列都是同一种币种吗？",
			Draft:    "金额字段统一为人民币（元），不做汇率换算",
			Confidence: "low",
			Evidence: fmt.Sprintf("检测到金额列 %s，但没有 currency 规则", strings.Join(moneyCols, ", ")),
			ApplyMode: "semantic_rule",
		})
	}

	// Similarly named sibling tables are a canonical-table question: the model
	// cannot know whether orders or orders_v2 is "the" orders.
	if siblings := sc.siblingTables(ctx, ds); len(siblings) > 0 && ruleByCategory[models.RuleCanonicalTable] == 0 {
		add(Gap{
			ID:       GapSimilarTables + ":" + ds.TableNameStr,
			Kind:     GapSimilarTables, Severity: "high",
			Target:   ds.TableNameStr,
			Question: fmt.Sprintf("%s 与 %s 哪张是权威表？", ds.TableNameStr, strings.Join(siblings, "、")),
			Draft:    fmt.Sprintf("以 %s 为准，%s 仅历史归档", ds.TableNameStr, strings.Join(siblings, "、")),
			Confidence: "low",
			Evidence: "存在名称相近的表且没有 canonical_table 规则",
			ApplyMode: "semantic_rule",
		})
	}

	// Unapproved metrics: the Agent only trusts approved ones.
	var metrics []models.AIMetric
	if err := sc.DB.WithContext(ctx).
		Where("dataset_id IN (0, ?) AND deleted = ?", datasetID, false).Find(&metrics).Error; err == nil {
		for _, m := range metrics {
			if m.Status != "approved" {
				add(Gap{
					ID:       GapMetricUnapproved + ":" + m.Name,
					Kind:     GapMetricUnapproved, Severity: "medium",
					Target:   m.Name,
					Question: fmt.Sprintf("指标「%s」的口径可以定稿吗？", m.Name),
					Draft:    m.Expression, Confidence: "high",
					Evidence: "状态为 " + firstNonEmpty(m.Status, "draft"),
					ApplyMode: "",
				})
			}
		}
	}

	published := false
	totalFields := 0
	hasRelationship := false
	for _, m := range modelsList {
		if m.Status == "published" {
			published = true
		}
	}
	if len(modelIDs) > 0 {
		var relCount int64
		sc.DB.WithContext(ctx).Model(&models.SemanticRelationship{}).
			Where("from_model_id IN ? AND deleted = ?", modelIDs, false).Count(&relCount)
		hasRelationship = relCount > 0
		var fieldCount int64
		sc.DB.WithContext(ctx).Model(&models.SemanticField{}).
			Where("model_id IN ? AND deleted = ?", modelIDs, false).Count(&fieldCount)
		totalFields = int(fieldCount)
	}
	if !published {
		add(Gap{
			ID:       GapNoPublishedModel + ":dataset",
			Kind:     GapNoPublishedModel, Severity: "high",
			Target:   ds.TableNameStr,
			Question: "语义模型还没有发布，Agent 现在无法使用结构化查询。",
			Draft:    "补完描述后发布",
			Confidence: "high",
			Evidence: "没有 status=published 的语义模型",
			ApplyMode: "",
		})
	}
	if published && !hasRelationship {
		add(Gap{
			ID:       GapNoRelationship + ":dataset",
			Kind:     GapNoRelationship, Severity: "low",
			Target:   ds.TableNameStr,
			Question: "这个数据集需要与其它表关联吗？",
			Draft:    "单表分析为主，暂不建立关系",
			Confidence: "low",
			Evidence: "已发布模型但没有任何关系",
			ApplyMode: "",
		})
	}
	for _, c := range cols {
		if c.PIILevel == "" || c.PIILevel == "none" {
			if looksPersonal(c) {
				add(Gap{
					ID:       GapPIILevelMissing + ":" + c.ColumnName,
					Kind:     GapPIILevelMissing, Severity: "high",
					Target:   c.ColumnName,
					Question: fmt.Sprintf("「%s」是否包含个人信息？", c.ColumnName),
					Draft:    "high（应默认脱敏）", Confidence: "medium",
					Evidence: "列名/类型像个人信息但 pii_level=none",
					ApplyMode: "",
				})
			}
		}
	}

	// Coverage score. Weights are explicit so the number can be argued with.
	descPct := 0
	if len(cols) > 0 {
		descPct = described * 100 / len(cols)
	}
	enumPct := 100
	if lowCardinality > 0 {
		enumPct = enumReady * 100 / lowCardinality
	}
	rulePct := 0
	if hasSoftDeleteColumn == "" || ruleByCategory[models.RuleDefaultFilter] > 0 {
		rulePct += 50
	}
	if len(moneyCols) == 0 || ruleByCategory[models.RuleCurrency] > 0 {
		rulePct += 50
	}
	modelPct := 0
	if published {
		modelPct = 50
	}
	if totalFields > 0 {
		modelPct += 50
	}
	report.Score = (descPct*40 + enumPct*20 + rulePct*20 + modelPct*20) / 100
	report.Coverage = map[string]any{
		"columns":              len(cols),
		"described_columns":    described,
		"described_pct":        descPct,
		"enum_ready_pct":       enumPct,
		"rules_present":        ruleByCategory,
		"published_model":      published,
		"semantic_fields":      totalFields,
	}
	if !allowSample {
		report.Note = "未采样：枚举检查需要读取真实数据，勾选「允许采样」后重新扫描"
	}

	// Highest severity first, then stable order so the UI does not reshuffle.
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(report.Gaps, func(i, j int) bool {
		if rank[report.Gaps[i].Severity] != rank[report.Gaps[j].Severity] {
			return rank[report.Gaps[i].Severity] < rank[report.Gaps[j].Severity]
		}
		return report.Gaps[i].ID < report.Gaps[j].ID
	})
	return report, nil
}

// ApplyGap writes the adopted draft back. Only the modes the scanner advertised
// are accepted: anything else would be a silent write to a field the user did
// not see.
func (sc *Scanner) ApplyGap(ctx context.Context, datasetID int, gapID, value string) (map[string]any, error) {
	if strings.TrimSpace(value) == "" {
		return nil, apperrors.BadRequest("采纳内容不能为空")
	}
	parts := strings.SplitN(gapID, ":", 2)
	if len(parts) != 2 {
		return nil, apperrors.BadRequest("gap id 格式错误")
	}
	kind, target := parts[0], parts[1]
	switch kind {
	case GapMissingDescription, GapMissingUnit, GapTimeNoHint, GapMissingEnum:
		res := sc.DB.WithContext(ctx).Model(&models.TableColumn{}).
			Where("table_id = ? AND column_name = ?", datasetID, target).
			Update("description", value)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, apperrors.NotFound("列 %q 不存在", target)
		}
		// Keep the semantic field in sync when one exists, otherwise the two
		// descriptions drift and the Agent sees the stale one.
		var modelIDs []int
		sc.DB.WithContext(ctx).Model(&models.SemanticModel{}).
			Where("dataset_id = ? AND deleted = ?", datasetID, false).Pluck("id", &modelIDs)
		if len(modelIDs) > 0 {
			sc.DB.WithContext(ctx).Model(&models.SemanticField{}).
				Where("name = ? AND model_id IN ?", target, modelIDs).
				Update("description", value)
		}
		return map[string]any{"applied": "column_description", "target": target, "value": value}, nil
	case GapSoftDeleteNoRule:
		rule := models.SemanticRule{
			Scope: models.RuleScopeDataset, DatasetID: datasetID,
			Category: models.RuleDefaultFilter, Title: "默认排除软删除记录",
			Body:     value,
			Spec:     mustJSON(FilterSpec{Field: target, Op: "is_null", Reason: "默认排除软删除记录"}),
			Status:   "active", Evidence: "gap_scanner: " + gapID,
		}
		if err := sc.DB.WithContext(ctx).Create(&rule).Error; err != nil {
			return nil, err
		}
		return map[string]any{"applied": "semantic_rule", "rule_id": rule.ID}, nil
	case GapCurrencyNoRule:
		rule := models.SemanticRule{
			Scope: models.RuleScopeDataset, DatasetID: datasetID,
			Category: models.RuleCurrency, Title: "金额币种",
			Body: value, Status: "active", Evidence: "gap_scanner: " + gapID,
		}
		if err := sc.DB.WithContext(ctx).Create(&rule).Error; err != nil {
			return nil, err
		}
		return map[string]any{"applied": "semantic_rule", "rule_id": rule.ID}, nil
	case GapSimilarTables:
		rule := models.SemanticRule{
			Scope: models.RuleScopeDataset, DatasetID: datasetID,
			Category: models.RuleCanonicalTable, Title: "权威表",
			Body: value, Status: "active", Evidence: "gap_scanner: " + gapID,
		}
		if err := sc.DB.WithContext(ctx).Create(&rule).Error; err != nil {
			return nil, err
		}
		return map[string]any{"applied": "semantic_rule", "rule_id": rule.ID}, nil
	}
	return nil, apperrors.BadRequest("缺口 %q 需要手动处理（apply_mode 为空）", kind)
}

func (sc *Scanner) siblingTables(ctx context.Context, ds models.SqlaTable) []string {
	var rows []models.SqlaTable
	if err := sc.DB.WithContext(ctx).
		Where("database_id = ? AND id <> ? AND deleted_at IS NULL", ds.DatabaseID, ds.ID).
		Find(&rows).Error; err != nil {
		return nil
	}
	base := strings.ToLower(ds.TableNameStr)
	var out []string
	for _, r := range rows {
		other := strings.ToLower(r.TableNameStr)
		if other == base {
			continue
		}
		if strings.HasPrefix(other, base+"_") || strings.HasPrefix(base, other+"_") {
			out = append(out, r.TableNameStr)
		}
	}
	sort.Strings(out)
	return out
}

func draftDescription(c models.TableColumn) string {
	name := c.ColumnName
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "_id") || lower == "id":
		return "主键/外键标识"
	case strings.HasSuffix(lower, "_at") || strings.HasSuffix(lower, "_time") || strings.HasSuffix(lower, "_date"):
		return "时间戳"
	case strings.Contains(lower, "name"):
		return "名称"
	case strings.Contains(lower, "status") || strings.Contains(lower, "state"):
		return "状态"
	case strings.Contains(lower, "count") || strings.Contains(lower, "num") || strings.Contains(lower, "qty"):
		return "数量"
	}
	return ""
}

func draftConfidence(c models.TableColumn) string {
	if draftDescription(c) != "" {
		return "medium"
	}
	return "low"
}

func guessUnit(name string) string {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "usd") || strings.Contains(lower, "dollar") {
		return "美元"
	}
	return "元（待确认）"
}

func isCategorical(c models.TableColumn) bool {
	low := strings.ToLower(c.Type)
	return strings.Contains(low, "char") || strings.Contains(low, "text") || strings.Contains(low, "enum")
}

func looksPersonal(c models.TableColumn) bool {
	low := strings.ToLower(c.ColumnName)
	for _, hint := range []string{"phone", "mobile", "email", "idcard", "id_card", "ssn", "passport", "address", "姓名", "身份证", "手机"} {
		if strings.Contains(low, hint) {
			return true
		}
	}
	return false
}
