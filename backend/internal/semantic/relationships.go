package semantic

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// relationships.go owns the join declarations. They are kept out of the field
// editor because a relationship is a statement about two models, not about one
// row in a table — mixing them is how join conditions end up half-edited.

// RelationshipInput is the create/update payload.
type RelationshipInput struct {
	Name        string `json:"name"`
	FromModelID int    `json:"from_model_id"`
	ToModelID   int    `json:"to_model_id"`
	JoinType    string `json:"join_type"`
	Condition   string `json:"condition"`
}

// CreateRelationship validates both endpoints and stores the join.
func (s *Service) CreateRelationship(ctx context.Context, in RelationshipInput) (*models.SemanticRelationship, error) {
	from, err := s.loadModel(ctx, in.FromModelID)
	if err != nil {
		return nil, err
	}
	to, err := s.loadModel(ctx, in.ToModelID)
	if err != nil {
		return nil, err
	}
	if from.DatasetID != to.DatasetID {
		return nil, apperrors.BadRequest("关系两端必须属于同一数据集（%d vs %d）", from.DatasetID, to.DatasetID)
	}
	if from.ID == to.ID {
		return nil, apperrors.BadRequest("模型不能与自身建立关系")
	}
	joinType := firstNonEmpty(in.JoinType, models.JoinManyToOne)
	switch joinType {
	case models.JoinManyToOne, models.JoinOneToMany, models.JoinOneToOne, models.JoinManyToMany:
	default:
		return nil, apperrors.BadRequest("join_type 只能是 many_to_one / one_to_many / one_to_one / many_to_many")
	}
	if strings.TrimSpace(in.Condition) == "" {
		return nil, apperrors.BadRequest("连接条件必填，例如 orders.customer_id = customers.id")
	}
	rel := models.SemanticRelationship{
		Name: strings.TrimSpace(in.Name), FromModelID: from.ID, ToModelID: to.ID,
		JoinType: joinType, Condition: strings.TrimSpace(in.Condition),
	}
	if err := s.DB.WithContext(ctx).Create(&rel).Error; err != nil {
		return nil, err
	}
	return &rel, nil
}

// UpdateRelationship edits an existing join.
func (s *Service) UpdateRelationship(ctx context.Context, id int, in RelationshipInput) (*models.SemanticRelationship, error) {
	var rel models.SemanticRelationship
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted = ?", id, false).First(&rel).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("关系 %d 不存在", id)
		}
		return nil, err
	}
	if in.Name != "" {
		rel.Name = in.Name
	}
	if in.JoinType != "" {
		rel.JoinType = in.JoinType
	}
	if in.Condition != "" {
		rel.Condition = in.Condition
	}
	if err := s.DB.WithContext(ctx).Model(&models.SemanticRelationship{}).Where("id = ?", id).
		Updates(map[string]any{
			"name": rel.Name, "join_type": rel.JoinType, "condition": rel.Condition,
		}).Error; err != nil {
		return nil, err
	}
	return &rel, nil
}

// DeleteRelationship soft-deletes a join.
func (s *Service) DeleteRelationship(ctx context.Context, id int) error {
	res := s.DB.WithContext(ctx).Model(&models.SemanticRelationship{}).
		Where("id = ? AND deleted = ?", id, false).Update("deleted", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("关系 %d 不存在", id)
	}
	return nil
}
