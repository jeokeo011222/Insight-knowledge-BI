package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// portable.go implements JSON/YAML export and import. The format is the
// compiled manifest, not the raw tables: exporting whatever happened to be in
// the database would make "did this environment drift?" impossible to answer.
// A bundle that imports cleanly produces the same manifest hash.

// BundleVersion is bumped when the wire shape changes incompatibly.
const BundleVersion = "mdl-lite/v1"

// Bundle is the portable representation of every model in one dataset.
type Bundle struct {
	Version string      `json:"version" yaml:"version"`
	Dataset DatasetRef  `json:"dataset" yaml:"dataset"`
	Models  []ModelSpec `json:"models" yaml:"models"`
}

// DatasetRef identifies the source dataset. Name is informational; import
// targets whatever dataset the caller names.
type DatasetRef struct {
	ID   int    `json:"id" yaml:"id"`
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

// Export renders a dataset's published-or-draft models.
func (s *Service) Export(ctx context.Context, datasetID int, format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "osi", "osi-yaml", "osi-json":
		return s.ExportOSI(ctx, datasetID, format)
	}
	var ds models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&ds).Error; err != nil {
		return "", apperrors.NotFound("数据集 %d 不存在", datasetID)
	}
	list, err := s.ListModels(ctx, datasetID)
	if err != nil {
		return "", err
	}
	bundle := Bundle{Version: BundleVersion, Dataset: DatasetRef{ID: datasetID, Name: ds.TableNameStr}}
	for _, m := range list {
		v, err := s.view(ctx, m)
		if err != nil {
			return "", err
		}
		if v.Manifest != nil {
			bundle.Models = append(bundle.Models, *v.Manifest)
		}
	}
	if strings.EqualFold(format, "yaml") || strings.EqualFold(format, "yml") {
		blob, err := yaml.Marshal(bundle)
		if err != nil {
			return "", err
		}
		return string(blob), nil
	}
	blob, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// ImportResult reports what an import changed.
type ImportResult struct {
	Created []string `json:"created"`
	Updated []string `json:"updated"`
	Skipped []string `json:"skipped"`
	Issues  []Issue  `json:"issues"`
}

// Import parses a bundle and upserts its models into datasetID. A model whose
// name already exists is replaced field-for-field; the previous fields are
// soft-deleted, never mutated, so a bad import can be diagnosed afterwards.
func (s *Service) Import(ctx context.Context, datasetID int, payload, format string) (*ImportResult, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "osi", "osi-yaml", "osi-json":
		return s.ImportOSI(ctx, datasetID, payload, format)
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, apperrors.BadRequest("导入内容为空")
	}
	var bundle Bundle
	if strings.EqualFold(format, "yaml") || strings.EqualFold(format, "yml") ||
		(format == "" && !strings.HasPrefix(payload, "{")) {
		if err := yaml.Unmarshal([]byte(payload), &bundle); err != nil {
			return nil, apperrors.BadRequest("YAML 解析失败：%s", err.Error())
		}
	} else if err := json.Unmarshal([]byte(payload), &bundle); err != nil {
		return nil, apperrors.BadRequest("JSON 解析失败：%s", err.Error())
	}
	if bundle.Version != "" && bundle.Version != BundleVersion {
		return nil, apperrors.BadRequest("不支持的 bundle 版本 %q，期望 %q", bundle.Version, BundleVersion)
	}
	if len(bundle.Models) == 0 {
		return nil, apperrors.BadRequest("bundle 里没有任何模型")
	}

	res := &ImportResult{}
	if err := s.importSpecs(ctx, datasetID, bundle.Models, "manual", res); err != nil {
		return nil, err
	}
	return res, nil
}

// importSpecs upserts compiled specs into a dataset and then wires the
// relationships. Shared by the bundle importer and the OSI importer so both
// paths get the same second pass: a relationship can only be written once both
// endpoints exist, and implementing that twice invites the two paths to drift.
func (s *Service) importSpecs(ctx context.Context, datasetID int, specs []ModelSpec, source string, res *ImportResult) error {
	for _, spec := range specs {
		name := NormalizeModelName(spec.Name)
		if name == "" {
			res.Skipped = append(res.Skipped, "(空名称)")
			continue
		}
		fields := make([]FieldInput, 0, len(spec.Fields))
		for _, f := range spec.Fields {
			fi := FieldInput{
				Name: f.Name, Kind: f.Kind, Type: f.Type, Description: f.Description,
				Unit: f.Unit, EnumValues: f.Enum, Synonyms: f.Synonyms,
				Hidden: f.Hidden, PIILevel: f.PIILevel, DefaultAgg: f.DefaultAgg,
				IsDttm: f.IsDttm,
			}
			// A column field's expression is the physical column; persisting it
			// would freeze a name that a future column rename should update.
			if f.Kind == models.FieldKindColumn || f.Kind == models.FieldKindTime {
				if f.Expression != f.Name {
					fi.Expression = f.Expression
				}
			} else {
				fi.Expression = f.Expression
			}
			fields = append(fields, fi)
		}

		var existing models.SemanticModel
		err := s.DB.WithContext(ctx).
			Where("dataset_id = ? AND name = ? AND deleted = ?", datasetID, name, false).
			First(&existing).Error
		switch {
		case err == nil:
			if _, uerr := s.UpdateModel(ctx, existing.ID, ModelInput{
				Name: name, Description: spec.Description, PrimaryKey: spec.PrimaryKey,
				Fields: fields,
			}); uerr != nil {
				return uerr
			}
			res.Updated = append(res.Updated, name)
		case isNotFound(err):
			if _, cerr := s.CreateModel(ctx, ModelInput{
				DatasetID: datasetID, Name: name, Description: spec.Description,
				PrimaryKey: spec.PrimaryKey, Fields: fields, Source: source,
			}); cerr != nil {
				return cerr
			}
			res.Created = append(res.Created, name)
		default:
			return err
		}
	}

	// Relationships are imported in a second pass: both endpoints must exist
	// first, which is why they live on the model spec but are written last.
	byName := map[string]int{}
	list, err := s.ListModels(ctx, datasetID)
	if err != nil {
		return err
	}
	for _, m := range list {
		byName[m.Name] = m.ID
	}
	for _, spec := range specs {
		fromID := byName[NormalizeModelName(spec.Name)]
		for _, j := range spec.Joins {
			toID, ok := byName[NormalizeModelName(j.To)]
			if !ok || fromID == 0 {
				res.Issues = append(res.Issues, Issue{
					Severity: "warning", Code: IssueDanglingJoin, Field: j.Name,
					Message: fmt.Sprintf("跳过关系 %q：端点模型不存在", j.Condition),
					Hint:    "先导入被引用的模型，再导入关系",
				})
				continue
			}
			var count int64
			s.DB.WithContext(ctx).Model(&models.SemanticRelationship{}).
				Where("from_model_id = ? AND to_model_id = ? AND deleted = ?", fromID, toID, false).
				Count(&count)
			if count > 0 {
				continue
			}
			rel := models.SemanticRelationship{
				Name: j.Name, FromModelID: fromID, ToModelID: toID,
				JoinType: firstNonEmpty(j.JoinType, models.JoinManyToOne), Condition: j.Condition,
			}
			if err := s.DB.WithContext(ctx).Create(&rel).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "record not found")
}
