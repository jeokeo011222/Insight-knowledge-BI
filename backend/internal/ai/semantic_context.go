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

// certificationPrompt appends a one-line governance summary to the system
// prompt — "3/12 指标已认证" vs. "全部字段为草稿". The Agent uses this to
// decide whether to qualify its answer with 「口径未认证，仅供参考」.
//
// CertifiedAt is an operator-visible timestamp (see semantic/certification.go).
// When every field is certified, the model can assert metrics with confidence;
// when some or most are draft, we tell it to flag the gap. The summary is
// intentionally one line — we don't want certification noise pushing out
// actual schema or rules.
func (s *Service) certificationPrompt(ctx context.Context, datasetID int, lang string) string {
	if datasetID <= 0 || s.DB == nil {
		return ""
	}
	var m models.SemanticModel
	if err := s.DB.WithContext(ctx).
		Where("dataset_id = ? AND deleted = ?", datasetID, false).
		Order("case when status = 'published' then 0 else 1 end, id").
		First(&m).Error; err != nil {
		return ""
	}
	var fields []models.SemanticField
	if err := s.DB.WithContext(ctx).
		Where("model_id = ? AND deleted = ?", m.ID, false).Find(&fields).Error; err != nil {
		return ""
	}
	if len(fields) == 0 {
		return ""
	}
	var certMetrics, certOther, uncert int
	for _, f := range fields {
		if f.CertifiedAt != nil {
			if f.Kind == "metric" {
				certMetrics++
			} else {
				certOther++
			}
		} else {
			uncert++
		}
	}

	var b strings.Builder
	b.WriteString("\n")
	if strings.EqualFold(lang, "en") {
		b.WriteString("## Semantic governance summary\n")
		fmt.Fprintf(&b, "Dataset %d: ", datasetID)
		if m.CertifiedAt != nil {
			fmt.Fprintf(&b, "model certified ✅ (%s) ", m.CertifiedAt.Format("2006-01-02"))
		} else {
			b.WriteString("model DRAFT ")
		}
		fmt.Fprintf(&b, "| certified metrics: %d | certified other: %d | uncertified fields: %d\n",
			certMetrics, certOther, uncert)
		if certMetrics == 0 {
			b.WriteString("⚠️  No metric is certified. If you use a metric in your answer, say it is AI-generated and ask the user to confirm the definition.\n")
		} else if uncert > 0 {
			b.WriteString("Some fields are uncertified. Qualify answers that depend on them — e.g. 'this dimension is machine-inferred and has not been reviewed'.\n")
		} else {
			b.WriteString("All fields are certified — you may state metrics with confidence.\n")
		}
	} else {
		b.WriteString("## 语义治理摘要\n")
		fmt.Fprintf(&b, "数据集 %d：", datasetID)
		if m.CertifiedAt != nil {
			fmt.Fprintf(&b, "模型已认证 ✅（%s）", m.CertifiedAt.Format("2006-01-02"))
		} else {
			b.WriteString("模型为草稿")
		}
		fmt.Fprintf(&b, "｜已认证指标 %d｜已认证其他字段 %d｜未认证字段 %d\n",
			certMetrics, certOther, uncert)
		if certMetrics == 0 {
			b.WriteString("⚠️ 当前没有指标被认证。回答时涉及指标请注明「口径由 AI 生成，仅供参考」，并建议用户到语义层认证后再使用。\n")
		} else if uncert > 0 {
			b.WriteString("部分字段未认证。对答案涉及的这些字段请加限定 —— 例如「该维度由 AI 推断，未经审核」。\n")
		} else {
			b.WriteString("全部字段已认证，可直接引用。\n")
		}
	}
	return b.String()
}
