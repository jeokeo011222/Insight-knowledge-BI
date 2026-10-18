package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"superset/internal/models"
)

// manifest.go compiles a semantic model into a deterministic manifest and
// validates it. Two rules drive every decision here:
//
//  1. Anything the manifest cannot resolve is an error, not a warning. A model
//     that half-compiles produces SQL that fails at the engine, which costs a
//     round trip and teaches the Agent nothing.
//  2. The hash is computed over a canonical form (sorted keys, no timestamps
//     inside the hashed body) so "did the Agent's vocabulary change?" is a
//     diff, not an eyeball exercise.

// Issue is one validation finding. Severity "error" blocks publish; "warning"
// does not, but is surfaced so the operator knows what is still missing.
type Issue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// Validation issue codes. Stable because the UI groups by them.
const (
	IssueEmptyModel          = "EMPTY_MODEL"
	IssueDuplicateField      = "DUPLICATE_FIELD"
	IssueUnknownFieldKind    = "UNKNOWN_FIELD_KIND"
	IssueMissingExpression   = "MISSING_EXPRESSION"
	IssueUnknownColumn       = "UNKNOWN_COLUMN"
	IssueUnknownPrimaryKey   = "UNKNOWN_PRIMARY_KEY"
	IssueMissingPrimaryKey   = "MISSING_PRIMARY_KEY"
	IssueManyToManyNoPK      = "MANY_TO_MANY_WITHOUT_PK"
	IssueDanglingJoin        = "DANGLING_JOIN"
	IssueInvalidPII          = "INVALID_PII_LEVEL"
	IssueInvalidEnum         = "INVALID_ENUM_VALUES"
	IssueNoMetric            = "NO_METRIC"
	IssueNoRelationship      = "NO_RELATIONSHIP"
	IssueDefaultFilterBroken = "DEFAULT_FILTER_BROKEN"
	// IssueFilterUnknownColumn is distinct from IssueDefaultFilterBroken on
	// purpose: "the spec is not JSON" and "the spec points at a column that does
	// not exist" need different fixes, and the old code conflated them.
	IssueFilterUnknownColumn = "DEFAULT_FILTER_UNKNOWN_COLUMN"
	IssueFieldNoDescription  = "FIELD_WITHOUT_DESCRIPTION"
	IssueHiddenPrimaryKey    = "HIDDEN_PRIMARY_KEY"
)

// ModelSpec is the manifest view of a model.
type ModelSpec struct {
	Name        string         `json:"name" yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	PrimaryKey  string         `json:"primary_key,omitempty" yaml:"primary_key,omitempty"`
	DatasetID   int            `json:"dataset_id" yaml:"dataset_id"`
	Version     int            `json:"version" yaml:"version"`
	Status      string         `json:"status" yaml:"status"`
	Fields      []FieldSpec    `json:"fields" yaml:"fields"`
	Joins       []JoinSpec     `json:"relationships,omitempty" yaml:"relationships,omitempty"`
	Filters     []FilterSpec   `json:"default_filters,omitempty" yaml:"default_filters,omitempty"`
	Rules       []RuleSpec     `json:"rules,omitempty" yaml:"rules,omitempty"`
	Extra       map[string]any `json:"-" yaml:"-"`
	ManifestSHA string         `json:"manifest_hash,omitempty" yaml:"manifest_hash,omitempty"`
}

// FieldSpec is the manifest view of a field. Expression is already resolved
// (column fields carry their physical column name).
type FieldSpec struct {
	Name        string   `json:"name" yaml:"name"`
	Kind        string   `json:"kind" yaml:"kind"`
	Expression  string   `json:"expression" yaml:"expression"`
	Type        string   `json:"type,omitempty" yaml:"type,omitempty"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Unit        string   `json:"unit,omitempty" yaml:"unit,omitempty"`
	Enum        []string `json:"enum_values,omitempty" yaml:"enum_values,omitempty"`
	Synonyms    []string `json:"synonyms,omitempty" yaml:"synonyms,omitempty"`
	Hidden      bool     `json:"hidden,omitempty" yaml:"hidden,omitempty"`
	PIILevel    string   `json:"pii_level,omitempty" yaml:"pii_level,omitempty"`
	DefaultAgg  string   `json:"default_agg,omitempty" yaml:"default_agg,omitempty"`
	IsDttm      bool     `json:"is_dttm,omitempty" yaml:"is_dttm,omitempty"`
}

// JoinSpec is the manifest view of a relationship.
type JoinSpec struct {
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	From      string `json:"from" yaml:"from"`
	To        string `json:"to" yaml:"to"`
	JoinType  string `json:"join_type" yaml:"join_type"`
	Condition string `json:"condition" yaml:"condition"`
}

// FilterSpec is the executable form of a default_filter rule.
type FilterSpec struct {
	Table string `json:"table,omitempty" yaml:"table,omitempty"`
	Field string `json:"field" yaml:"field"`
	Op    string `json:"op" yaml:"op"`
	Value any    `json:"value,omitempty" yaml:"value,omitempty"`
	// Reason is shown to the user when a filter was applied automatically, so a
	// surprising number can be explained instead of merely observed.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// RuleSpec is the prompt-side view of a rule.
type RuleSpec struct {
	Category string `json:"category" yaml:"category"`
	Title    string `json:"title,omitempty" yaml:"title,omitempty"`
	Body     string `json:"body" yaml:"body"`
}

// CompileInput is everything Compile needs. Physical columns are passed in
// rather than fetched so the function stays pure and testable.
type CompileInput struct {
	Model         models.SemanticModel
	Fields        []models.SemanticField
	Relationships []models.SemanticRelationship
	Rules         []models.SemanticRule
	// PhysicalColumns is the set of column names that exist on the dataset's
	// physical table. Virtual datasets pass nil, which downgrades the
	// unknown-column check to a warning instead of silently passing.
	PhysicalColumns map[string]bool
	// ModelNames maps model id -> name so relationships can be rendered by name.
	ModelNames map[int]string
}

// Compile validates the model and returns the manifest plus every finding.
// The manifest is returned even when there are errors, because the UI shows the
// partial result while the operator fixes it.
func Compile(in CompileInput) (*ModelSpec, []Issue) {
	var issues []Issue
	add := func(sev, code, field, msg, hint string) {
		issues = append(issues, Issue{Severity: sev, Code: code, Field: field, Message: msg, Hint: hint})
	}

	spec := &ModelSpec{
		Name:        in.Model.Name,
		Description: in.Model.Description,
		PrimaryKey:  in.Model.PrimaryKey,
		DatasetID:   in.Model.DatasetID,
		Version:     in.Model.Version,
		Status:      in.Model.Status,
	}

	if strings.TrimSpace(in.Model.Name) == "" {
		add("error", IssueEmptyModel, "", "语义模型缺少名称", "给模型一个稳定的英文名，Agent 用它代替物理表名")
	}
	if len(in.Fields) == 0 {
		add("error", IssueEmptyModel, "", "语义模型没有任何字段", "用「从数据集生成」冷启动，或至少声明一个 column 字段")
	}

	declared := map[string]bool{}
	kindNames := map[string]bool{
		models.FieldKindColumn: true, models.FieldKindMetric: true,
		models.FieldKindDimension: true, models.FieldKindTime: true,
	}
	hasMetric := false

	for _, f := range in.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			add("error", IssueDuplicateField, "", "存在空字段名", "字段名不能为空")
			continue
		}
		if declared[name] {
			add("error", IssueDuplicateField, name, fmt.Sprintf("字段名 %q 重复声明", name), "字段名在模型内必须唯一")
		}
		declared[name] = true

		if !kindNames[f.Kind] {
			add("error", IssueUnknownFieldKind, name, fmt.Sprintf("字段 %q 的 kind=%q 不合法", name, f.Kind),
				"kind 只能是 column / metric / dimension / time")
		}
		if f.Kind == models.FieldKindMetric {
			hasMetric = true
		}

		expr := strings.TrimSpace(f.Expression)
		if expr == "" {
			switch f.Kind {
			case models.FieldKindColumn, models.FieldKindTime:
				// Passthrough: the physical column with the same name.
				expr = name
			default:
				add("error", IssueMissingExpression, name,
					fmt.Sprintf("%s 字段 %q 没有表达式", f.Kind, name),
					"metric/dimension 必须给出 SQL 表达式，例如 SUM(amount)")
			}
		}
		// Validate the *resolved* expression, not the raw one. A column field
		// with an empty expression is a passthrough to the physical column of the
		// same name, so checking f.Expression skipped exactly the case that
		// matters most (a stale field left behind by a column rename).
		if in.PhysicalColumns != nil && isBareIdentifier(expr) && !in.PhysicalColumns[expr] {
			add("error", IssueUnknownColumn, name,
				fmt.Sprintf("字段 %q 指向的列 %q 不在数据集里", name, expr),
				"改表达式或重新同步数据集列")
		}
		if p := f.PIILevel; p != "" && p != "none" && p != "low" && p != "high" {
			add("error", IssueInvalidPII, name, fmt.Sprintf("字段 %q 的 pii_level=%q 不合法", name, p),
				"pii_level 只能是 none / low / high")
		}
		if strings.TrimSpace(f.Description) == "" {
			add("warning", IssueFieldNoDescription, name,
				fmt.Sprintf("字段 %q 没有描述", name), "描述是 Agent 理解口径的主要来源，建议补齐")
		}

		fs := FieldSpec{
			Name:        name,
			Kind:        f.Kind,
			Expression:  expr,
			Type:        f.Type,
			Description: f.Description,
			Unit:        f.Unit,
			Hidden:      f.Hidden,
			PIILevel:    f.PIILevel,
			DefaultAgg:  f.DefaultAgg,
			IsDttm:      f.IsDttm || f.Kind == models.FieldKindTime,
		}
		if vals, ok := stringSlice(f.EnumValues); ok {
			fs.Enum = vals
		} else if f.EnumValues != nil {
			add("warning", IssueInvalidEnum, name,
				fmt.Sprintf("字段 %q 的 enum_values 不是字符串数组", name), "枚举值必须是 JSON 字符串数组")
		}
		if vals, ok := stringSlice(f.Synonyms); ok {
			fs.Synonyms = vals
		}
		spec.Fields = append(spec.Fields, fs)
	}

	// Sort by name so the hash does not change when the operator reorders rows.
	sort.SliceStable(spec.Fields, func(i, j int) bool { return spec.Fields[i].Name < spec.Fields[j].Name })

	if in.Model.PrimaryKey != "" {
		if !declared[in.Model.PrimaryKey] {
			add("error", IssueUnknownPrimaryKey, in.Model.PrimaryKey,
				fmt.Sprintf("主键 %q 不是已声明字段", in.Model.PrimaryKey), "先声明该字段，或清空主键")
		} else {
			for _, f := range in.Fields {
				if f.Name == in.Model.PrimaryKey && f.Hidden {
					add("warning", IssueHiddenPrimaryKey, f.Name,
						"主键被标记为 hidden，关系仍可使用但 Agent 看不到它", "这是常见配置，确认无误即可忽略")
				}
			}
		}
	}

	for _, rel := range in.Relationships {
		fromName, fromOK := in.ModelNames[rel.FromModelID]
		toName, toOK := in.ModelNames[rel.ToModelID]
		if !fromOK || !toOK {
			add("error", IssueDanglingJoin, rel.Name,
				fmt.Sprintf("关系 %q 引用了不存在的模型（from=%d to=%d）", rel.Name, rel.FromModelID, rel.ToModelID),
				"关系两端都必须是本数据集内的模型")
			continue
		}
		if strings.TrimSpace(rel.Condition) == "" {
			add("error", IssueDanglingJoin, rel.Name,
				fmt.Sprintf("关系 %s → %s 没有连接条件", fromName, toName), "例如 orders.customer_id = customers.id")
		}
		if rel.JoinType == models.JoinManyToMany {
			pk := ""
			if rel.FromModelID == in.Model.ID {
				pk = in.Model.PrimaryKey
			}
			if pk == "" {
				add("error", IssueManyToManyNoPK, rel.Name,
					fmt.Sprintf("多对多关系 %s → %s 的模型没有主键", fromName, toName),
					"多对多必须声明主键，否则无法消除重复行")
			}
		}
		spec.Joins = append(spec.Joins, JoinSpec{
			Name: rel.Name, From: fromName, To: toName,
			JoinType: rel.JoinType, Condition: rel.Condition,
		})
	}

	for _, rule := range in.Rules {
		if rule.Status == "disabled" {
			continue
		}
		if rule.Category == models.RuleDefaultFilter {
			// normalizeSpec stores a *list* of filters, so decoding into a single
			// object silently failed for every rule created through the API —
			// which turned every default filter into DEFAULT_FILTER_BROKEN and
			// blocked publishing. Accept both shapes.
			filters, ok := filterSpecs(rule.Spec)
			if !ok {
				add("error", IssueDefaultFilterBroken, rule.Title,
					fmt.Sprintf("规则 %q 的 spec 无法解析为默认过滤条件", firstNonEmpty(rule.Title, rule.Category)),
					`spec 形如 {"field":"deleted_at","op":"is_null"} 或该对象的数组`)
			}
			for _, fs := range filters {
				fs.Reason = firstNonEmpty(fs.Reason, rule.Title, rule.Body)
				if in.PhysicalColumns != nil && !declared[fs.Field] && !in.PhysicalColumns[fs.Field] {
					add("error", IssueFilterUnknownColumn, fs.Field,
						fmt.Sprintf("规则 %q 过滤的列 %q 不在数据集里", firstNonEmpty(rule.Title, rule.Category), fs.Field),
						"改成真实存在的列，或停用该规则")
				}
				spec.Filters = append(spec.Filters, fs)
			}
		}
		spec.Rules = append(spec.Rules, RuleSpec{Category: rule.Category, Title: rule.Title, Body: rule.Body})
	}

	if !hasMetric {
		add("warning", IssueNoMetric, "", "模型没有声明任何 metric 字段", "semantic_query 只能分组计数，无法回答「多少/总额」类问题")
	}
	if len(in.Relationships) == 0 {
		add("warning", IssueNoRelationship, "", "模型没有任何关系", "单表模型可以工作，但跨表问题会退化到自由 SQL")
	}

	sort.SliceStable(spec.Filters, func(i, j int) bool {
		if spec.Filters[i].Field != spec.Filters[j].Field {
			return spec.Filters[i].Field < spec.Filters[j].Field
		}
		return spec.Filters[i].Op < spec.Filters[j].Op
	})

	spec.ManifestSHA = hashManifest(spec)
	return spec, issues
}

// HasErrors reports whether any finding blocks publishing.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == "error" {
			return true
		}
	}
	return false
}

// hashManifest hashes the canonical JSON of the manifest without the hash
// itself. encoding/json sorts map keys and the slices were sorted above, so the
// digest is stable across processes and restarts.
func hashManifest(spec *ModelSpec) string {
	clone := *spec
	clone.ManifestSHA = ""
	clone.Status = "" // status is a workflow flag, not part of the contract
	blob, err := json.Marshal(clone)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// filterSpecs parses the machine-readable half of a default_filter rule.
//
// Two shapes are legal: a single object (hand-written / imported manifests) and
// a list (what normalizeSpec stores from the API). A spec that is present but
// points at no field or no operator is a *broken* spec, not an empty one — the
// caller must be able to tell those apart, hence the bool.
func filterSpecs(raw []byte) ([]FilterSpec, bool) {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null", "{}", "[]":
		return nil, true
	}
	if strings.HasPrefix(trimmed, "[") {
		var list []FilterSpec
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, false
		}
		for _, fs := range list {
			if strings.TrimSpace(fs.Field) == "" || strings.TrimSpace(fs.Op) == "" {
				return nil, false
			}
		}
		return list, true
	}
	if strings.HasPrefix(trimmed, "{") {
		var one FilterSpec
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, false
		}
		if strings.TrimSpace(one.Field) == "" || strings.TrimSpace(one.Op) == "" {
			return nil, false
		}
		return []FilterSpec{one}, true
	}
	return nil, false
}

// bareIdentifier matches a plain column reference. Anything containing
// punctuation is treated as an expression and left unvalidated: `SUM(amount)` or
// `date_trunc('day', ds)` cannot be checked against the column list by name.
var bareIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isBareIdentifier(expr string) bool {
	return bareIdentifier.MatchString(strings.TrimSpace(expr))
}

func stringSlice(raw []byte) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
