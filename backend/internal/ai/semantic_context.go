package ai

import (
	"context"
	"fmt"
	"strings"

	"superset/internal/models"
	"superset/internal/semantic"
)

// semantic_context.go wires the semantic model into the Agent's prompt.
//
// Two distinct jobs, deliberately kept apart:
//
//   - semanticModelDigest tells the planner that a structured query path
//     exists. Without it the model keeps writing free SQL, and W-06's whole
//     point (deterministic SQL for questions the model already understands) is
//     lost — a tool nobody calls is a tool that does not exist.
//   - semanticRulesPrompt injects the few business rules that bear on this
//     question. Never the whole rulebook: that pushes out the schema and makes
//     the model apply rules that do not apply.

// semanticModelDigest summarises the published model for the planner.
func (s *Service) semanticModelDigest(ctx context.Context, datasetID int) string {
	if datasetID <= 0 || s.DB == nil {
		return ""
	}
	var m models.SemanticModel
	err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND status = ? AND deleted = ?", datasetID, "published", false).
		Order("id").First(&m).Error
	if err != nil {
		return ""
	}
	var fieldCount int64
	s.DB.WithContext(ctx).Model(&models.SemanticField{}).
		Where("model_id = ? AND deleted = ?", m.ID, false).Count(&fieldCount)

	var b strings.Builder
	fmt.Fprintf(&b, "\n## 语义模型：%s（v%d，%d 个字段）\n", m.Name, m.Version, fieldCount)
	if m.Description != "" {
		fmt.Fprintf(&b, "%s\n", m.Description)
	}
	b.WriteString("本数据集已发布语义模型。能用它表达的问题请优先调用 semantic_query（给指标/维度/时间/过滤，不写 SQL），")
	b.WriteString("它生成的 SQL 与图表一致、可复现。只有它表达不了的（窗口函数、多表复杂 JOIN、子查询、自定义计算）才用 query_executor 写自由 SQL。\n")
	b.WriteString("不确定有哪些字段时先调 semantic_reader，不要猜字段名。\n")
	return b.String()
}

// semanticRulesPrompt returns the top-k rules relevant to the question.
func (s *Service) semanticRulesPrompt(ctx context.Context, datasetID int, question string) string {
	if datasetID <= 0 || s.DB == nil || strings.TrimSpace(question) == "" {
		return ""
	}
	modelID := 0
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted = ?", datasetID, false).
		Order("case when status = 'published' then 0 else 1 end, id").
		First(&m).Error; err == nil {
		modelID = m.ID
	}
	text, err := semantic.New(s.DB, s.Log).PromptRules(ctx, datasetID, modelID, question, s.Config.AISQLMemoryTopK)
	if err != nil || strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}
