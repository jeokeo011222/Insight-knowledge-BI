package ai

import (
	"context"
	"fmt"
	"strings"

	"superset/internal/models"
	"superset/internal/queries"
)

// Dataset context rendering (上下文切片).
//
// nl2sql used to receive the dataset's whole schema as JSON. On a 120-column
// warehouse table that is several thousand tokens per call, repeated on every
// step of the loop, and most of those columns are irrelevant to the question.
// WrenAI's answer is the same shape as this file: render a compact textual
// description, keep it whole while it is small, and switch to retrieval once it
// crosses a threshold (SCHEMA_DESCRIBE_THRESHOLD = 30_000 there; we start lower
// because our budget is per model call inside a multi-step loop).
//
// The compact form is also where masked/hidden columns become explicit: the
// model is told "masked" before it writes SQL, instead of discovering asterisks
// in the result and treating them as bad data.

// DefaultSchemaCompactThreshold is the compact-schema size above which we stop
// dumping every column and switch to retrieval. Overridable by config.
const DefaultSchemaCompactThreshold = 8000

// retrievalTopK is how many columns survive when retrieval kicks in.
const retrievalTopK = 24

// datasetSchema is one dataset's renderable context.
type datasetSchema struct {
	Table   models.SqlaTable
	Columns []models.TableColumn // active AND agent-visible only
	Metrics []models.SqlMetric
	// HiddenCount counts columns withheld from the Agent, so the model can be
	// told "there are N more columns you cannot use" instead of assuming the
	// dataset is small.
	HiddenCount int
}

// loadDatasetSchema reads a dataset with only the columns the Agent may see.
func loadDatasetSchema(ctx context.Context, s *Service, datasetID int) (*datasetSchema, error) {
	if s.DB == nil {
		return nil, fmt.Errorf("metadata store is unavailable")
	}
	var table models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&table).Error; err != nil {
		return nil, fmt.Errorf("dataset %d not found: %w", datasetID, err)
	}
	var all []models.TableColumn
	if err := s.DB.WithContext(ctx).Where("table_id = ?", table.ID).
		Order("column_name").Find(&all).Error; err != nil {
		return nil, fmt.Errorf("failed to load dataset schema: %w", err)
	}
	var metrics []models.SqlMetric
	if err := s.DB.WithContext(ctx).Where("table_id = ?", table.ID).
		Order("metric_name").Find(&metrics).Error; err != nil {
		return nil, fmt.Errorf("failed to load dataset metrics: %w", err)
	}

	ds := &datasetSchema{Table: table, Metrics: metrics}
	for _, column := range all {
		if !column.IsActive {
			continue
		}
		if !column.AgentVisible {
			ds.HiddenCount++
			continue
		}
		ds.Columns = append(ds.Columns, column)
	}
	return ds, nil
}

// maskedNote renders the exposure tag appended to a column line.
func maskedNote(column models.TableColumn) string {
	rule := queries.EffectiveMaskRule(column)
	if rule.Type == "" {
		return ""
	}
	return " [已脱敏：仅可用于计数/分组]"
}

// columnLine renders one column as `name type — description [tags]`.
func columnLine(column models.TableColumn) string {
	var b strings.Builder
	b.WriteString(column.ColumnName)
	if column.Type != "" {
		b.WriteString(" ")
		b.WriteString(column.Type)
	}
	desc := strings.TrimSpace(column.Description)
	verbose := strings.TrimSpace(column.VerboseName)
	switch {
	case desc != "":
		b.WriteString(" — ")
		b.WriteString(desc)
	case verbose != "" && verbose != column.ColumnName:
		b.WriteString(" — ")
		b.WriteString(verbose)
	}
	if column.IsDttm {
		b.WriteString(" [时间]")
	}
	b.WriteString(maskedNote(column))
	return b.String()
}

// compact renders the whole dataset description.
func (ds *datasetSchema) compact() string {
	var b strings.Builder
	fmt.Fprintf(&b, "数据集：%s", ds.Table.TableNameStr)
	if ds.Table.Schema != "" {
		fmt.Fprintf(&b, "（schema=%s）", ds.Table.Schema)
	}
	if desc := strings.TrimSpace(ds.Table.Description); desc != "" {
		fmt.Fprintf(&b, " — %s", desc)
	}
	b.WriteString("\n")
	if ds.Table.MainDttmCol != "" {
		fmt.Fprintf(&b, "主时间列：%s\n", ds.Table.MainDttmCol)
	}
	if ds.Table.SQL != "" || ds.Table.IsSQLLabView {
		b.WriteString("类型：虚拟数据集（通过数据集 SQL 读取，不要直接引用物理表）\n")
	} else {
		fmt.Fprintf(&b, "物理表：%s\n", qualifiedTable(ds.Table))
	}
	b.WriteString("列：\n")
	for _, column := range ds.Columns {
		b.WriteString("  ")
		b.WriteString(columnLine(column))
		b.WriteString("\n")
	}
	if len(ds.Metrics) > 0 {
		b.WriteString("已定义指标（原样使用表达式，不要自行改写口径）：\n")
		for _, metric := range ds.Metrics {
			fmt.Fprintf(&b, "  %s = %s", metric.MetricName, metric.Expression)
			if desc := strings.TrimSpace(metric.Description); desc != "" {
				fmt.Fprintf(&b, " — %s", desc)
			}
			b.WriteString("\n")
		}
	}
	if ds.HiddenCount > 0 {
		fmt.Fprintf(&b, "另有 %d 个列对 Agent 隐藏（不可查询，提及它们会被告知无此列）。\n", ds.HiddenCount)
	}
	return b.String()
}

// compactFor renders the context for one question: the whole description while
// it stays under the threshold, otherwise a core header plus the columns that
// actually match the question.
func (ds *datasetSchema) compactFor(question string, threshold int) (string, bool) {
	if threshold <= 0 {
		threshold = DefaultSchemaCompactThreshold
	}
	full := ds.compact()
	if len(full) <= threshold {
		return full, false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "数据集：%s（列较多，以下仅列出与问题最相关的 %d 列，共 %d 列）\n",
		ds.Table.TableNameStr, retrievalTopK, len(ds.Columns))
	if ds.Table.Schema != "" {
		fmt.Fprintf(&b, "schema=%s\n", ds.Table.Schema)
	}
	if ds.Table.MainDttmCol != "" {
		fmt.Fprintf(&b, "主时间列：%s\n", ds.Table.MainDttmCol)
	}
	if ds.Table.SQL != "" || ds.Table.IsSQLLabView {
		b.WriteString("类型：虚拟数据集（通过数据集 SQL 读取，不要直接引用物理表）\n")
	} else {
		fmt.Fprintf(&b, "物理表：%s\n", qualifiedTable(ds.Table))
	}

	// A time column and the identifiers are almost always needed, so they are
	// pinned ahead of the relevance ranking.
	var pinned, rest []models.TableColumn
	for _, column := range ds.Columns {
		if column.IsDttm || strings.EqualFold(column.ColumnName, ds.Table.MainDttmCol) {
			pinned = append(pinned, column)
			continue
		}
		rest = append(rest, column)
	}
	ranked := rankByText(question, rest, func(c models.TableColumn) string {
		return c.ColumnName + " " + c.VerboseName + " " + c.Description
	}, retrievalTopK, 0)
	selected := pinned
	for _, item := range ranked {
		selected = append(selected, item.Item)
	}
	if len(selected) > retrievalTopK+4 {
		selected = selected[:retrievalTopK+4]
	}
	b.WriteString("列：\n")
	for _, column := range selected {
		b.WriteString("  ")
		b.WriteString(columnLine(column))
		b.WriteString("\n")
	}
	// Metrics and hidden-column count survive the cut: they are short and they
	// are exactly the context a model is most likely to get wrong.
	if len(ds.Metrics) > 0 {
		b.WriteString("已定义指标（原样使用表达式）：\n")
		for _, metric := range ds.Metrics {
			fmt.Fprintf(&b, "  %s = %s\n", metric.MetricName, metric.Expression)
		}
	}
	if ds.HiddenCount > 0 {
		fmt.Fprintf(&b, "另有 %d 个列对 Agent 隐藏（不可查询）。\n", ds.HiddenCount)
	}
	return b.String(), true
}

// qualifiedTable renders the physical table reference.
func qualifiedTable(table models.SqlaTable) string {
	if table.Schema != "" {
		return table.Schema + "." + table.TableNameStr
	}
	return table.TableNameStr
}
