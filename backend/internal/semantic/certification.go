// certification.go owns the "signed-off" side of the semantic layer.
//
// When the operator clicks "certify" on a model, field or relationship, this
// is what gets invoked. Certify sets CertifiedAt to now; decertify clears it.
// The Agent surface then uses that timestamp to decorate its prompt with
// "✅ 已认证" vs. "AI 生成 · 未认证" so the answer shows who reviewed it.
//
// Auto Ontology's Certify stage is the inspiration — the key difference is
// they use an approval workflow, we use a single timestamp column because
// we have a flat role model (admin writes, others read) and the timestamp
// is enough to answer "when was this signed off?" and "have any of the
// fields drifted since?".

package semantic

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// CertifyModel signs off a SemanticModel and every one of its fields and
// relationships. We do *all* descendants at once because an operator never
// wants "the model is certified but half its fields are draft" — that
// silently drops fields from plan resolution because the Agent prompt
// deprioritises draft rows (see agent prompt builder).
func (s *Service) CertifyModel(ctx context.Context, modelID int) (*models.SemanticModel, error) {
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", modelID).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义模型 %d 不存在", modelID)
		}
		return nil, err
	}
	now := time.Now()
	m.CertifiedAt = &now
	if err := s.DB.WithContext(ctx).Save(&m).Error; err != nil {
		return nil, fmt.Errorf("certify model: %w", err)
	}
	if err := s.DB.WithContext(ctx).
		Model(&models.SemanticField{}).
		Where("model_id = ? AND deleted_at IS NULL", modelID).
		Update("certified_at", now).Error; err != nil {
		return nil, fmt.Errorf("certify fields: %w", err)
	}
	if err := s.DB.WithContext(ctx).
		Model(&models.SemanticRelationship{}).
		Where("(from_model_id = ? OR to_model_id = ?) AND deleted_at IS NULL", modelID, modelID).
		Update("certified_at", now).Error; err != nil {
		return nil, fmt.Errorf("certify relationships: %w", err)
	}
	s.Log.Info("certified semantic model", zap.Int("model", modelID), zap.Time("at", now))
	return &m, nil
}

// Decertify clears the CertifiedAt timestamp on a model and every descendant.
// Used when the operator discovers drift (columns changed, definitions are
// now wrong) and wants the Agent to stop trusting it until re-review.
func (s *Service) DecertifyModel(ctx context.Context, modelID int) (*models.SemanticModel, error) {
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", modelID).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义模型 %d 不存在", modelID)
		}
		return nil, err
	}
	m.CertifiedAt = nil
	if err := s.DB.WithContext(ctx).Save(&m).Error; err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).
		Model(&models.SemanticField{}).
		Where("model_id = ? AND deleted_at IS NULL", modelID).
		Update("certified_at", nil).Error; err != nil {
		return nil, err
	}
	if err := s.DB.WithContext(ctx).
		Model(&models.SemanticRelationship{}).
		Where("(from_model_id = ? OR to_model_id = ?) AND deleted_at IS NULL", modelID, modelID).
		Update("certified_at", nil).Error; err != nil {
		return nil, err
	}
	s.Log.Info("decertified semantic model", zap.Int("model", modelID))
	return &m, nil
}

// CertifyField / DecertifyField let the operator sign off one field at a
// time — used when fixing a drift incrementally ("GMV expression changed,
// only re-certify that metric").
func (s *Service) CertifyField(ctx context.Context, fieldID int) (*models.SemanticField, error) {
	return s.setFieldCertified(ctx, fieldID, true)
}
func (s *Service) DecertifyField(ctx context.Context, fieldID int) (*models.SemanticField, error) {
	return s.setFieldCertified(ctx, fieldID, false)
}
func (s *Service) setFieldCertified(ctx context.Context, fieldID int, certified bool) (*models.SemanticField, error) {
	var f models.SemanticField
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", fieldID).First(&f).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义字段 %d 不存在", fieldID)
		}
		return nil, err
	}
	if certified {
		now := time.Now()
		f.CertifiedAt = &now
	} else {
		f.CertifiedAt = nil
	}
	if err := s.DB.WithContext(ctx).Save(&f).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// CertifyRelationship / DecertifyRelationship let the operator sign off a
// single join — useful when ontogen.go inferred a join that only one side
// of a many-to-many bridge is known.
func (s *Service) CertifyRelationship(ctx context.Context, relID int) (*models.SemanticRelationship, error) {
	return s.setRelationshipCertified(ctx, relID, true)
}
func (s *Service) DecertifyRelationship(ctx context.Context, relID int) (*models.SemanticRelationship, error) {
	return s.setRelationshipCertified(ctx, relID, false)
}
func (s *Service) setRelationshipCertified(ctx context.Context, relID int, certified bool) (*models.SemanticRelationship, error) {
	var r models.SemanticRelationship
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", relID).First(&r).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("语义关系 %d 不存在", relID)
		}
		return nil, err
	}
	if certified {
		now := time.Now()
		r.CertifiedAt = &now
	} else {
		r.CertifiedAt = nil
	}
	if err := s.DB.WithContext(ctx).Save(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// ---------------------------------------------------------------- query helpers --

// CertificationSummary returns certified vs. draft counts for one model.
// Used by the API handler and by the Agent prompt builder to decide
// whether to mention "未认证字段" to the user.
func (s *Service) CertificationSummary(ctx context.Context, modelID int) (map[string]int, error) {
	out := map[string]int{
		"model_certified":   0,
		"model_uncertified": 0,
		"field_certified":   0,
		"field_uncertified": 0,
		"rel_certified":     0,
		"rel_uncertified":   0,
	}
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).Select("certified_at").
		Where("id = ? AND deleted_at IS NULL", modelID).First(&m).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return out, nil
		}
		return nil, err
	}
	if m.CertifiedAt != nil {
		out["model_certified"] = 1
	} else {
		out["model_uncertified"] = 1
	}
	var fields []models.SemanticField
	if err := s.DB.WithContext(ctx).Where("model_id = ? AND deleted_at IS NULL", modelID).
		Find(&fields).Error; err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f.CertifiedAt != nil {
			out["field_certified"]++
		} else {
			out["field_uncertified"]++
		}
	}
	var rels []models.SemanticRelationship
	if err := s.DB.WithContext(ctx).
		Where("(from_model_id = ? OR to_model_id = ?) AND deleted_at IS NULL", modelID, modelID).
		Find(&rels).Error; err != nil {
		return nil, err
	}
	for _, r := range rels {
		if r.CertifiedAt != nil {
			out["rel_certified"]++
		} else {
			out["rel_uncertified"]++
		}
	}
	return out, nil
}
