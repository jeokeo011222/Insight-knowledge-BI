package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// osi.go implements the OSI (Open Semantic Interchange, 0.2.0) exchange format.
//
// Why bother: our own bundle format is good for "diff two environments", but it
// is ours. OSI is the format an enterprise already has when it owns Looker,
// Cube or dbt semantic assets, so supporting it is the difference between "you
// must rebuild your metrics here" and "bring them along". It is also the only
// vendor-neutral place to put things OSI has no field for: type, unit, enum,
// synonyms, pii level and the column-vs-metric distinction all travel in a
// custom_extensions block under our own vendor name, which is exactly the
// escape hatch the spec sanctions.
//
// One deliberate asymmetry: export writes every model of a dataset into ONE
// OSI semantic_model with N datasets (that is how OSI expresses joins), while
// import slices a document back to a single dataset. Cross-dataset joins cannot
// round-trip because our engine has no federated query path; they are skipped
// and reported instead of being silently dropped.

// OSIVersion is the OSI spec version we emit and accept.
const OSIVersion = "0.2.0"

// OSIVendorName is the custom_extensions vendor we write. Wren writes "WREN";
// we read that block for the type hint so their exports import cleanly here.
const OSIVendorName = "SUPERSET"

// Additional issue codes for the interchange path.
const (
	IssueOSINoDataset      = "OSI_NO_DATASET"
	IssueOSIDatasetUnknown = "OSI_DATASET_NOT_FOUND"
	IssueOSICrossDataset   = "OSI_CROSS_DATASET_UNSUPPORTED"
	IssueOSIAmbiguous      = "OSI_AMBIGUOUS_DOCUMENT"
)

// ---------------------------------------------------------------- document --

type osiDoc struct {
	Version       string     `yaml:"version" json:"version"`
	SemanticModel []osiModel `yaml:"semantic_model" json:"semantic_model"`
}

type osiModel struct {
	Name        string      `yaml:"name" json:"name"`
	Description string      `yaml:"description,omitempty" json:"description,omitempty"`
	AIContext   *osiAI      `yaml:"ai_context,omitempty" json:"ai_context,omitempty"`
	Datasets    []osiTable  `yaml:"datasets" json:"datasets"`
	Relations   []osiRel    `yaml:"relationships,omitempty" json:"relationships,omitempty"`
	Metrics     []osiMetric `yaml:"metrics,omitempty" json:"metrics,omitempty"`
}

type osiAI struct {
	Instructions string   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	Synonyms     []string `yaml:"synonyms,omitempty" json:"synonyms,omitempty"`
}

type osiTable struct {
	Name        string     `yaml:"name" json:"name"`
	Source      string     `yaml:"source,omitempty" json:"source,omitempty"`
	PrimaryKey  any        `yaml:"primary_key,omitempty" json:"primary_key,omitempty"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Fields      []osiField `yaml:"fields" json:"fields"`
	Extensions  []osiExt   `yaml:"custom_extensions,omitempty" json:"custom_extensions,omitempty"`
}

type osiField struct {
	Name        string   `yaml:"name" json:"name"`
	Expression  osiExpr  `yaml:"expression" json:"expression"`
	Type        string   `yaml:"type,omitempty" json:"type,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Dimension   *osiDim  `yaml:"dimension,omitempty" json:"dimension,omitempty"`
	AIContext   *osiAI   `yaml:"ai_context,omitempty" json:"ai_context,omitempty"`
	Extensions  []osiExt `yaml:"custom_extensions,omitempty" json:"custom_extensions,omitempty"`
}

type osiDim struct {
	IsTime bool `yaml:"is_time" json:"is_time"`
}

type osiRel struct {
	Name        string   `yaml:"name" json:"name"`
	From        string   `yaml:"from" json:"from"`
	To          string   `yaml:"to" json:"to"`
	FromColumns []string `yaml:"from_columns,omitempty" json:"from_columns,omitempty"`
	ToColumns   []string `yaml:"to_columns,omitempty" json:"to_columns,omitempty"`
	Extensions  []osiExt `yaml:"custom_extensions,omitempty" json:"custom_extensions,omitempty"`
}

type osiMetric struct {
	Name        string   `yaml:"name" json:"name"`
	Expression  osiExpr  `yaml:"expression" json:"expression"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Extensions  []osiExt `yaml:"custom_extensions,omitempty" json:"custom_extensions,omitempty"`
}

type osiExt struct {
	VendorName string `yaml:"vendor_name" json:"vendor_name"`
	Data       string `yaml:"data" json:"data"`
}

// osiExpr is OSI's dual-shaped expression: either a plain string or
// {dialects: [{dialect, expression}]}. Both shapes appear in the wild.
type osiExpr struct {
	Expression string
}

var bareIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

func (e osiExpr) MarshalYAML() (any, error) {
	if bareIdentRE.MatchString(strings.TrimSpace(e.Expression)) {
		return strings.TrimSpace(e.Expression), nil
	}
	return map[string]any{
		"dialects": []any{map[string]any{"dialect": "ANSI_SQL", "expression": e.Expression}},
	}, nil
}

func (e *osiExpr) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		e.Expression = value.Value
		return nil
	}
	var obj struct {
		Dialects []osiDialect `yaml:"dialects"`
	}
	if err := value.Decode(&obj); err != nil {
		return err
	}
	e.Expression = pickDialect(obj.Dialects, "ANSI_SQL")
	return nil
}

func (e osiExpr) MarshalJSON() ([]byte, error) {
	if bareIdentRE.MatchString(strings.TrimSpace(e.Expression)) {
		return json.Marshal(strings.TrimSpace(e.Expression))
	}
	return json.Marshal(map[string]any{
		"dialects": []map[string]string{{"dialect": "ANSI_SQL", "expression": e.Expression}},
	})
}

func (e *osiExpr) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		e.Expression = s
		return nil
	}
	var obj struct {
		Dialects []osiDialect `json:"dialects"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	e.Expression = pickDialect(obj.Dialects, "ANSI_SQL")
	return nil
}

// osiDialect is one entry of a dialect-tagged expression.
type osiDialect struct {
	Dialect    string `yaml:"dialect" json:"dialect"`
	Expression string `yaml:"expression" json:"expression"`
}

func pickDialect(all []osiDialect, want string) string {
	for _, d := range all {
		if strings.EqualFold(strings.TrimSpace(d.Dialect), want) && strings.TrimSpace(d.Expression) != "" {
			return d.Expression
		}
	}
	for _, d := range all {
		if strings.TrimSpace(d.Expression) != "" {
			return d.Expression
		}
	}
	return ""
}

// ---------------------------------------------------------------- vendor bag --

// osiFieldMeta is what our own custom_extensions block carries. Every field is
// optional so a Wren export (which only sends "type") still imports.
type osiFieldMeta struct {
	Kind       string   `json:"kind,omitempty"`
	Type       string   `json:"type,omitempty"`
	Unit       string   `json:"unit,omitempty"`
	Enum       []string `json:"enum,omitempty"`
	Synonyms   []string `json:"synonyms,omitempty"`
	PIILevel   string   `json:"pii,omitempty"`
	Hidden     bool     `json:"hidden,omitempty"`
	DefaultAgg string   `json:"default_agg,omitempty"`
	// Dataset pins a top-level OSI metric to the dataset it belongs to. OSI
	// metrics are document-level; ours are model-level, so the link has to be
	// written down or the round trip loses it.
	Dataset string `json:"dataset,omitempty"`
}

func extJSON(v any) string {
	blob, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(blob)
}

// readExt merges our vendor block and Wren's type hint. Our block wins: a file
// that came from us must round-trip exactly, even if it also carries a WREN
// block from an intermediate hop.
func readExt(exts []osiExt) (osiFieldMeta, string) {
	var meta osiFieldMeta
	var wrenType string
	for _, e := range exts {
		raw := strings.TrimSpace(e.Data)
		if raw == "" {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(e.VendorName)) {
		case OSIVendorName:
			_ = json.Unmarshal([]byte(raw), &meta)
		case "WREN":
			var v struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(raw), &v); err == nil && v.Type != "" {
				wrenType = v.Type
			}
		}
	}
	return meta, wrenType
}

// ------------------------------------------------------------------- export --

// ExportOSI renders a dataset's models as one OSI semantic_model.
func (s *Service) ExportOSI(ctx context.Context, datasetID int, format string) (string, error) {
	var ds models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		return "", apperrors.NotFound("数据集 %d 不存在", datasetID)
	}
	list, err := s.ListModels(ctx, datasetID)
	if err != nil {
		return "", err
	}
	doc := osiDoc{Version: OSIVersion}
	sm := osiModel{
		Name:        firstNonEmpty(ds.TableNameStr, fmt.Sprintf("dataset_%d", datasetID)),
		Description: ds.Description,
	}
	if desc := strings.TrimSpace(ds.Description); desc != "" {
		sm.AIContext = &osiAI{Instructions: desc}
	}
	published := 0
	for _, m := range list {
		if m.Status == "published" {
			published++
		}
	}
	for _, m := range list {
		// Interchange is about what both sides agree on. Draft models are still
		// exported when nothing is published, otherwise an environment could
		// hand over an empty file and blame the format.
		if published > 0 && m.Status != "published" {
			continue
		}
		v, err := s.view(ctx, m)
		if err != nil {
			return "", err
		}
		if v.Manifest == nil {
			continue
		}
		table := osiTable{
			Name:        m.Name,
			Source:      osiSource(ds),
			Description: firstNonEmpty(m.Description, m.Description),
		}
		if strings.TrimSpace(m.PrimaryKey) != "" {
			table.PrimaryKey = []string{m.PrimaryKey}
		}
		for _, f := range v.Manifest.Fields {
			if f.Kind == models.FieldKindMetric {
				sm.Metrics = append(sm.Metrics, osiMetric{
					Name:        f.Name,
					Expression:  osiExpr{Expression: f.Expression},
					Description: f.Description,
					Extensions: []osiExt{{VendorName: OSIVendorName, Data: extJSON(osiFieldMeta{
						Kind: f.Kind, Type: f.Type, Unit: f.Unit, Enum: f.Enum,
						Synonyms: f.Synonyms, PIILevel: f.PIILevel, Hidden: f.Hidden,
						DefaultAgg: f.DefaultAgg, Dataset: m.Name,
					})}},
				})
				continue
			}
			field := osiField{
				Name:        f.Name,
				Expression:  osiExpr{Expression: f.Expression},
				Type:        f.Type,
				Description: f.Description,
				Dimension:   &osiDim{IsTime: f.IsDttm || f.Kind == models.FieldKindTime},
				Extensions: []osiExt{{VendorName: OSIVendorName, Data: extJSON(osiFieldMeta{
					Kind: f.Kind, Type: f.Type, Unit: f.Unit, Enum: f.Enum,
					Synonyms: f.Synonyms, PIILevel: f.PIILevel, Hidden: f.Hidden,
					DefaultAgg: f.DefaultAgg,
				})}},
			}
			if len(f.Synonyms) > 0 {
				field.AIContext = &osiAI{Synonyms: f.Synonyms}
			}
			table.Fields = append(table.Fields, field)
		}
		sm.Datasets = append(sm.Datasets, table)
		for _, j := range v.Manifest.Joins {
			fromCols, toCols := parseJoinColumns(j.Condition)
			sm.Relations = append(sm.Relations, osiRel{
				Name:        firstNonEmpty(j.Name, j.From+"_to_"+j.To),
				From:        j.From,
				To:          j.To,
				FromColumns: fromCols,
				ToColumns:   toCols,
				Extensions: []osiExt{{VendorName: OSIVendorName, Data: extJSON(map[string]string{
					"join_type": j.JoinType, "condition": j.Condition,
				})}},
			})
		}
	}
	doc.SemanticModel = []osiModel{sm}

	if strings.HasSuffix(strings.ToLower(format), "json") {
		blob, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(blob), nil
	}
	blob, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// osiSource builds the OSI `source` reference: a virtual dataset has no table,
// so its SQL is the source (which is also what OSI allows).
func osiSource(ds models.SqlaTable) string {
	if strings.TrimSpace(ds.SQL) != "" {
		return ds.SQL
	}
	parts := make([]string, 0, 3)
	for _, p := range []string{ds.Catalog, ds.Schema, ds.TableNameStr} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ".")
}

var joinPairRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_$]*)\.([A-Za-z_][A-Za-z0-9_$]*)\s*=\s*([A-Za-z_][A-Za-z0-9_$]*)\.([A-Za-z_][A-Za-z0-9_$]*)`)

// parseJoinColumns extracts the structured half of a join condition. The raw
// condition is still written into custom_extensions, so a condition the regex
// cannot parse (a function call, a cast) survives the round trip; OSI consumers
// simply see empty column lists.
func parseJoinColumns(condition string) ([]string, []string) {
	var from, to []string
	for _, m := range joinPairRE.FindAllStringSubmatch(condition, -1) {
		from = append(from, m[1]+"."+m[2])
		to = append(to, m[3]+"."+m[4])
	}
	return from, to
}

// ------------------------------------------------------------------- import --

// ImportOSI converts an OSI document into models of datasetID. Only the OSI
// dataset that corresponds to datasetID is imported; everything that spans
// datasets is reported rather than guessed.
func (s *Service) ImportOSI(ctx context.Context, datasetID int, payload, format string) (*ImportResult, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, apperrors.BadRequest("导入内容为空")
	}
	var doc osiDoc
	asJSON := strings.EqualFold(format, "osi-json") ||
		(strings.EqualFold(format, "osi") && strings.HasPrefix(payload, "{"))
	if asJSON {
		if err := json.Unmarshal([]byte(payload), &doc); err != nil {
			return nil, apperrors.BadRequest("OSI JSON 解析失败：%s", err.Error())
		}
	} else if err := yaml.Unmarshal([]byte(payload), &doc); err != nil {
		return nil, apperrors.BadRequest("OSI YAML 解析失败：%s", err.Error())
	}
	if len(doc.SemanticModel) == 0 {
		return nil, apperrors.BadRequest("OSI 文档里没有 semantic_model")
	}

	var ds models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		return nil, apperrors.NotFound("数据集 %d 不存在", datasetID)
	}
	sm, err := pickOSIModel(doc, ds.TableNameStr)
	if err != nil {
		return nil, err
	}
	table, rest, err := pickOSITable(sm, ds.TableNameStr)
	if err != nil {
		return nil, err
	}

	res := &ImportResult{}
	for _, other := range rest {
		res.Skipped = append(res.Skipped, fmt.Sprintf("dataset %q（不属于当前数据集）", other))
	}
	for _, other := range sm.Relations {
		if !(strings.EqualFold(other.From, table.Name) && strings.EqualFold(other.To, table.Name)) {
			res.Skipped = append(res.Skipped, fmt.Sprintf("relationship %q（跨数据集）", other.Name))
		}
	}

	spec := ModelSpec{
		Name:        NormalizeModelName(table.Name),
		Description: firstNonEmpty(table.Description, aiInstructions(sm)),
		PrimaryKey:  osiPrimaryKey(table.PrimaryKey, &res.Issues, table.Name),
		DatasetID:   datasetID,
		Status:      "draft",
	}
	for _, f := range table.Fields {
		meta, wrenType := readExt(f.Extensions)
		kind := firstNonEmpty(meta.Kind, defaultOSIKind(f))
		if kind == models.FieldKindMetric {
			// Some producers put metrics inline; keep them as metrics instead of
			// turning a SUM(...) into a dimension.
			spec.Fields = append(spec.Fields, FieldSpec{
				Name: f.Name, Kind: kind, Expression: f.Expression.Expression,
				Type: firstNonEmpty(meta.Type, f.Type, wrenType), Description: f.Description,
				Unit: meta.Unit, Enum: meta.Enum, Synonyms: mergeStrings(f, meta),
				Hidden: meta.Hidden, PIILevel: meta.PIILevel, DefaultAgg: meta.DefaultAgg,
			})
			continue
		}
		spec.Fields = append(spec.Fields, FieldSpec{
			Name: f.Name, Kind: kind, Expression: f.Expression.Expression,
			Type: firstNonEmpty(meta.Type, f.Type, wrenType), Description: f.Description,
			Unit: meta.Unit, Enum: meta.Enum, Synonyms: mergeStrings(f, meta),
			Hidden: meta.Hidden, PIILevel: meta.PIILevel, DefaultAgg: meta.DefaultAgg,
			IsDttm: kind == models.FieldKindTime,
		})
	}
	for _, m := range sm.Metrics {
		meta, _ := readExt(m.Extensions)
		owner := strings.TrimSpace(meta.Dataset)
		// A metric whose expression names another dataset cannot be evaluated
		// here: our models are per-dataset and there is no federated join. OSI
		// files from other tools carry no owner field, so the expression itself
		// is the only evidence — and importing it anyway would produce a metric
		// that fails at the engine.
		refs := metricDatasets(m.Expression.Expression, datasetNames(sm))
		if owner == "" && len(refs) > 0 {
			foreign := make([]string, 0, len(refs))
			for _, r := range refs {
				if !strings.EqualFold(r, table.Name) {
					foreign = append(foreign, r)
				}
			}
			if len(foreign) > 0 {
				res.Skipped = append(res.Skipped, fmt.Sprintf(
					"metric %q（表达式引用了 dataset %s）", m.Name, strings.Join(foreign, ", ")))
				continue
			}
		}
		if owner != "" && !strings.EqualFold(owner, table.Name) {
			res.Skipped = append(res.Skipped, fmt.Sprintf("metric %q（属于 dataset %q）", m.Name, owner))
			continue
		}
		spec.Fields = append(spec.Fields, FieldSpec{
			Name: m.Name, Kind: models.FieldKindMetric, Expression: m.Expression.Expression,
			Type: meta.Type, Description: m.Description, Unit: meta.Unit,
			Hidden: meta.Hidden, PIILevel: meta.PIILevel, DefaultAgg: meta.DefaultAgg,
		})
	}
	for _, r := range sm.Relations {
		if !(strings.EqualFold(r.From, table.Name) && strings.EqualFold(r.To, table.Name)) {
			continue
		}
		cond := joinCondition(r)
		if cond == "" {
			continue
		}
		spec.Joins = append(spec.Joins, JoinSpec{
			Name: r.Name, From: NormalizeModelName(r.From), To: NormalizeModelName(r.To),
			JoinType: osiJoinType(r), Condition: cond,
		})
	}
	if len(spec.Fields) == 0 {
		return nil, apperrors.BadRequest("OSI dataset %q 里没有可导入的字段", table.Name)
	}

	if err := s.importSpecs(ctx, datasetID, []ModelSpec{spec}, "imported", res); err != nil {
		return nil, err
	}
	return res, nil
}

// pickOSIModel selects the semantic_model to import. A name match against the
// target table is unambiguous; otherwise a single-model document is the only
// reasonable reading; anything else needs a human to say which one.
func pickOSIModel(doc osiDoc, tableName string) (osiModel, error) {
	if len(doc.SemanticModel) == 1 {
		return doc.SemanticModel[0], nil
	}
	for _, sm := range doc.SemanticModel {
		if strings.EqualFold(strings.TrimSpace(sm.Name), strings.TrimSpace(tableName)) {
			return sm, nil
		}
	}
	names := make([]string, 0, len(doc.SemanticModel))
	for _, sm := range doc.SemanticModel {
		names = append(names, sm.Name)
	}
	sort.Strings(names)
	return osiModel{}, apperrors.BadRequest(
		"OSI 文档里有多个 semantic_model，无法确定导入哪一个：%s（把目标语义模型命名成数据集表名 %q 可自动匹配）",
		strings.Join(names, ", "), tableName)
}

// pickOSITable finds the OSI dataset that describes our table, and returns the
// names of the ones that were left out.
func pickOSITable(sm osiModel, tableName string) (osiTable, []string, error) {
	if len(sm.Datasets) == 0 {
		return osiTable{}, nil, apperrors.BadRequest("OSI semantic_model %q 里没有 dataset", sm.Name)
	}
	match := func(t osiTable) bool {
		if strings.EqualFold(t.Name, tableName) {
			return true
		}
		return strings.EqualFold(lastSegment(t.Source), tableName)
	}
	var found *osiTable
	for i := range sm.Datasets {
		if match(sm.Datasets[i]) {
			if found != nil {
				names := []string{found.Name, sm.Datasets[i].Name}
				return osiTable{}, nil, apperrors.BadRequest(
					"OSI 文档里有多个 dataset 指向表 %q：%s", tableName, strings.Join(names, ", "))
			}
			found = &sm.Datasets[i]
		}
	}
	if found == nil {
		// A single-dataset document is unambiguous even when the producer named
		// it differently from our table; several candidates is not.
		if len(sm.Datasets) == 1 {
			found = &sm.Datasets[0]
		} else {
			names := make([]string, 0, len(sm.Datasets))
			for _, t := range sm.Datasets {
				names = append(names, t.Name)
			}
			sort.Strings(names)
			return osiTable{}, nil, apperrors.BadRequest(
				"OSI 文档里的 dataset（%s）都不指向表 %q", strings.Join(names, ", "), tableName)
		}
	}
	rest := make([]string, 0, len(sm.Datasets))
	for _, t := range sm.Datasets {
		if !strings.EqualFold(t.Name, found.Name) {
			rest = append(rest, t.Name)
		}
	}
	return *found, rest, nil
}

// qualifiedRefRE finds `alias.` prefixes, which is how OSI metrics name the
// dataset they aggregate.
var qualifiedRefRE = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_$]*)\s*\.\s*[A-Za-z_*]`)

func datasetNames(sm osiModel) map[string]bool {
	names := make(map[string]bool, len(sm.Datasets))
	for _, d := range sm.Datasets {
		names[strings.ToLower(d.Name)] = true
	}
	return names
}

// metricDatasets returns which declared datasets an expression references.
// Matching against the document's own dataset names (rather than "anything
// before a dot") is what keeps `schema.table.col` and `amount * 1.0` from being
// mistaken for dataset references.
func metricDatasets(expr string, names map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range qualifiedRefRE.FindAllStringSubmatch(expr, -1) {
		key := strings.ToLower(m[1])
		if !names[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m[1])
	}
	return out
}

func lastSegment(source string) string {
	s := strings.TrimSpace(source)
	if s == "" || strings.ContainsAny(s, " \n\t") {
		return ""
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

func aiInstructions(sm osiModel) string {
	if sm.AIContext == nil {
		return ""
	}
	return sm.AIContext.Instructions
}

func defaultOSIKind(f osiField) string {
	if f.Dimension != nil && f.Dimension.IsTime {
		return models.FieldKindTime
	}
	return models.FieldKindColumn
}

func mergeStrings(f osiField, meta osiFieldMeta) []string {
	out := append([]string{}, meta.Synonyms...)
	if f.AIContext != nil {
		for _, s := range f.AIContext.Synonyms {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
	}
	seen := map[string]bool{}
	uniq := make([]string, 0, len(out))
	for _, s := range out {
		key := strings.ToLower(strings.TrimSpace(s))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, strings.TrimSpace(s))
	}
	return uniq
}

// osiPrimaryKey accepts "id" or ["a","b"]. A composite key cannot be expressed
// in our models, so the first column is used and the narrowing is reported —
// silently dropping the other columns would make every join cardinality wrong.
func osiPrimaryKey(raw any, issues *[]Issue, table string) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		cols := make([]string, 0, len(v))
		for _, c := range v {
			if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
				cols = append(cols, strings.TrimSpace(s))
			}
		}
		if len(cols) == 0 {
			return ""
		}
		if len(cols) > 1 {
			*issues = append(*issues, Issue{
				Severity: "warning", Code: IssueOSICrossDataset, Field: table,
				Message: fmt.Sprintf("dataset %q 的主键是复合键 %v，只保留了 %q", table, cols, cols[0]),
				Hint:    "复合主键需要多列唯一约束，当前模型只支持单列主键",
			})
		}
		return cols[0]
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func osiJoinType(r osiRel) string {
	for _, e := range r.Extensions {
		if !strings.EqualFold(strings.TrimSpace(e.VendorName), OSIVendorName) {
			continue
		}
		var meta struct {
			JoinType string `json:"join_type"`
		}
		if json.Unmarshal([]byte(e.Data), &meta) == nil && strings.TrimSpace(meta.JoinType) != "" {
			return meta.JoinType
		}
	}
	return models.JoinManyToOne
}

// joinCondition prefers the verbatim condition we wrote; otherwise it rebuilds
// one from from_columns/to_columns, which is all a foreign OSI file carries.
func joinCondition(r osiRel) string {
	for _, e := range r.Extensions {
		if !strings.EqualFold(strings.TrimSpace(e.VendorName), OSIVendorName) {
			continue
		}
		var meta struct {
			Condition string `json:"condition"`
		}
		if json.Unmarshal([]byte(e.Data), &meta) == nil && strings.TrimSpace(meta.Condition) != "" {
			return meta.Condition
		}
	}
	n := len(r.FromColumns)
	if n == 0 || n != len(r.ToColumns) {
		return ""
	}
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf("%s = %s", r.FromColumns[i], r.ToColumns[i]))
	}
	return strings.Join(parts, " AND ")
}
