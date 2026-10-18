package ai

import (
	"encoding/json"

	"superset/internal/ai/registry"
	"superset/internal/security"
)

// raw is a terse constructor for inline JSON schemas.
func raw(s string) json.RawMessage { return json.RawMessage(s) }

// registerBuiltins populates the skill and tool registries and then wires the
// executors implemented so far: read tools, P2 read skills, and the P4 write
// tools + dashboard_compose skill. The remaining skills stay registered but
// unwired for later phases.
func registerBuiltins(s *Service) {
	registerSkills(s)
	registerReadTools(s)
	registerWriteTools(s)
	wireExecutors(s)
	wireWriteExecutors(s)
}

func registerSkills(s *Service) {
	permRead := []registry.Permission{{Action: security.ActionRead, Resource: security.ResAIAgent}}

	s.Skills.Register(&registry.Skill{
		Name: "nl2sql", Title: "自然语言问数", Domain: "general",
		Description: "把自然语言问题转成候选 SQL（含置信度与缺失信息），不直接执行。",
		InputSchema: raw(`{"type":"object","properties":{"question":{"type":"string"},"dataset_id":{"type":"integer"},"dialect":{"type":"string"},"context":{"type":"object"}},"required":["question"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 1500, LatencyMS: 3000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "sql_explain", Title: "SQL 解释与优化", Domain: "general",
		Description: "对 SQL 做解释、优化或补全，返回 Markdown 说明与可选优化后 SQL。",
		InputSchema: raw(`{"type":"object","properties":{"sql":{"type":"string"},"mode":{"type":"string","enum":["generate","optimize","explain"]}},"required":["sql","mode"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "cheap", Tokens: 800, LatencyMS: 2000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "insight", Title: "数据洞察", Domain: "general",
		Description: "对指标做统计 + 归因 + 异常检测，输出结论 Markdown。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"metrics":{"type":"array","items":{"type":"string"}},"groupby":{"type":"array","items":{"type":"string"}},"filters":{"type":"array"}},"required":["dataset_id"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 2000, LatencyMS: 4000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "chart_design", Title: "图表推荐", Domain: "general",
		Description: "依据字段类型与分析意图推荐可视化类型，给出理由与替代方案。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"fields":{"type":"array"},"intent":{"type":"string"}},"required":["dataset_id"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "cheap", Tokens: 900, LatencyMS: 2000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "metric_compute", Title: "语义指标计算", Domain: "general",
		Description: "按语义层定义的指标口径计算指标值，返回指标值与所用 SQL。",
		InputSchema: raw(`{"type":"object","properties":{"metric_codes":{"type":"array","items":{"type":"string"}},"dataset_id":{"type":"integer"},"filters":{"type":"array"},"time_grain":{"type":"string"}},"required":["metric_codes","dataset_id"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 1500, LatencyMS: 3000},
	})

	// Domain analysis skills (finance / trend family).
	s.Skills.Register(&registry.Skill{
		Name: "dupont", Title: "杜邦分析", Domain: "finance",
		Description: "基于利润表与资产负债表做杜邦分解：ROE = 净利润率 × 总资产周转率 × 权益乘数。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_ids":{"type":"array","items":{"type":"integer"}},"period":{"type":"string"}},"required":["dataset_ids","period"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "heavy", Tokens: 4000, LatencyMS: 6000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "trend", Title: "趋势分析", Domain: "general",
		Description: "对单指标做时间序列趋势、同比环比与拐点识别。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"metric":{"type":"string"},"time_column":{"type":"string"},"window":{"type":"string"}},"required":["dataset_id","metric"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 2000, LatencyMS: 4000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "structure", Title: "结构分析", Domain: "general",
		Description: "对单指标做维度结构占比、帕累托(top_n)分析。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"metric":{"type":"string"},"dimension":{"type":"string"},"top_n":{"type":"integer"}},"required":["dataset_id","metric","dimension"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 2000, LatencyMS: 4000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "yoy_mom", Title: "同环比分析", Domain: "finance",
		Description: "对指标做同比(YoY)/环比(MoM)对比并输出瀑布分解。",
		InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"metric":{"type":"string"},"period":{"type":"string"}},"required":["dataset_id","metric","period"]}`),
		Permissions: permRead,
		CostHint:    registry.CostHint{Tier: "standard", Tokens: 2000, LatencyMS: 4000},
	})
	s.Skills.Register(&registry.Skill{
		Name: "dashboard_compose", Title: "看板编排", Domain: "general",
		Description: "依据 page_spec 生成看板页面初稿（写操作，经写 tool 落库，可回滚）。",
		InputSchema: raw(`{"type":"object","properties":{"page_spec":{"type":"object"},"dashboard_id":{"type":"integer"}},"required":["page_spec"]}`),
		Permissions: []registry.Permission{{Action: security.ActionWrite, Resource: security.ResDashboard}},
		CostHint:    registry.CostHint{Tier: "heavy", Tokens: 4000, LatencyMS: 8000},
	})
}

func registerReadTools(s *Service) {
	defs := []*registry.Tool{
		{
			Name: "query_executor", Title: "SQL 执行器",
			Description: "以当前用户身份执行只读 SQL（AST 校验 + 强制 LIMIT + 超时/行数闸）。",
			InputSchema: raw(`{"type":"object","properties":{"sql":{"type":"string"},"dataset_id":{"type":"integer"}},"required":["sql"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionQuery, Resource: security.ResDataset}},
		},
		{
			Name: "semantic_query", Title: "语义查询",
			Description: "用语义模型的结构化方式取数，不写 SQL：给出指标、维度、时间范围、过滤，后端生成与图表一致的 SQL。数据集已发布语义模型时优先使用；它表达不了的结构再退回 query_executor。",
			InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"model":{"type":"string","description":"语义模型名，省略则用该数据集已发布的模型"},"metrics":{"type":"array","items":{"type":"string"},"description":"指标字段名；省略表示 COUNT(*)"},"dimensions":{"type":"array","items":{"type":"string"},"description":"分组维度字段名"},"time":{"type":"object","properties":{"field":{"type":"string"},"range":{"type":"string","description":"today|yesterday|last_7_days|last_30_days|last_90_days|last_12_months|this_year|last_month|last_quarter 或 '2024-01-01 : 2024-12-31'"},"grain":{"type":"string","description":"hour|day|week|month|quarter|year"}}},"filters":{"type":"array","items":{"type":"object","properties":{"field":{"type":"string"},"op":{"type":"string","description":"eq|ne|gt|gte|lt|lte|in|not_in|like|is_null|is_not_null"},"value":{}},"required":["field","op"]}},"order":{"type":"array","items":{"type":"object","properties":{"field":{"type":"string"},"desc":{"type":"boolean"}}}},"limit":{"type":"integer"}},"required":["dataset_id"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionQuery, Resource: security.ResDataset}},
		},
		{
			Name: "schema_reader", Title: "Schema 读取器",
			Description: "读取数据集/表字段与类型元数据（只包含对 Agent 可见的列，脱敏列会标注 masked）。传 question 可只取与问题相关的列。",
			InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"database_id":{"type":"integer"},"schema":{"type":"string"},"table":{"type":"string"},"question":{"type":"string","description":"当前分析问题，用于宽表的相关列筛选"}},"required":[]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResDataset}},
		},
		{
			Name: "semantic_reader", Title: "语义层读取器",
			Description: "读取数据集的语义模型（已发布）：模型名、字段（名称/类型/表达式/描述/单位/枚举）、关系、默认过滤条件。这是「口径」的权威来源，回答业务指标前应先读它。未配置时返回 configured=false。",
			InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"model":{"type":"string","description":"语义模型名，省略则取已发布模型"},"codes":{"type":"array","items":{"type":"string"},"description":"外部语义资产名称（旧语义层），一般不需要"}},"required":["dataset_id"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResSemanticLayer}},
		},
		{
			Name: "metric_dictionary", Title: "指标字典",
			Description: "查询已审核的业务指标口径（名称、SQL 表达式、单位、可拆维度），涉及业务指标时必须先查此字典。",
			InputSchema: raw(`{"type":"object","properties":{"dataset_id":{"type":"integer"},"names":{"type":"array","items":{"type":"string"}}},"required":[]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResAIAgent}},
		},
		{
			Name: "meta_search", Title: "元数据召回",
			Description: "把用户口语映射到表/字段/指标。使用词法相关度排序（ASCII 词 + 中文二元组）；配置了 embedding 时可升级为向量召回。",
			InputSchema: raw(`{"type":"object","properties":{"query":{"type":"string"},"dataset_id":{"type":"integer"},"k":{"type":"integer"}},"required":["query"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResDataset}},
		},
		{
			Name: "chart_reader", Title: "图表读取器",
			Description: "读取图表定义（viz_type、指标、维度、过滤）。",
			InputSchema: raw(`{"type":"object","properties":{"chart_id":{"type":"integer"}},"required":["chart_id"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResChart}},
		},
		{
			Name: "dashboard_reader", Title: "看板读取器",
			Description: "读取看板布局与所含图表。",
			InputSchema: raw(`{"type":"object","properties":{"dashboard_id":{"type":"integer"}},"required":["dashboard_id"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResDashboard}},
		},
		{
			Name: "result_sampler", Title: "结果采样器",
			Description: "对查询结果做受限采样（受 AI_SAMPLES_ROW_LIMIT 约束），用于喂给 LLM。",
			InputSchema: raw(`{"type":"object","properties":{"sql":{"type":"string"},"dataset_id":{"type":"integer"},"limit":{"type":"integer"}},"required":["sql","dataset_id"]}`),
			SideEffect:  registry.Read,
			Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResDataset}},
		},
	}
	for _, d := range defs {
		s.Tools.Register(d)
	}
}

func registerWriteTools(s *Service) {
	defs := []*registry.Tool{
		{
			Name: "chart_writer", Title: "图表写入器",
			Description: "创建/更新图表（写前快照，支持按 run 回滚）。",
			InputSchema: raw(`{"type":"object","properties":{"chart_id":{"type":"integer"},"payload":{"type":"object"}},"required":["payload"]}`),
			SideEffect:  registry.Write,
			Permissions: []registry.Permission{{Action: security.ActionWrite, Resource: security.ResChart}},
		},
		{
			Name: "dashboard_writer", Title: "看板写入器",
			Description: "创建/更新看板布局（写前快照，支持按 run 回滚）。",
			InputSchema: raw(`{"type":"object","properties":{"dashboard_id":{"type":"integer"},"payload":{"type":"object"}},"required":["payload"]}`),
			SideEffect:  registry.Write,
			Permissions: []registry.Permission{{Action: security.ActionWrite, Resource: security.ResDashboard}},
		},
		{
			Name: "dashboard_compose_writer", Title: "看板编排写入器",
			Description: "按 page_spec 批量创建图表并编排到看板（写前快照，支持回滚）。",
			InputSchema: raw(`{"type":"object","properties":{"page_spec":{"type":"object"},"dashboard_id":{"type":"integer"}},"required":["page_spec"]}`),
			SideEffect:  registry.Write,
			Permissions: []registry.Permission{{Action: security.ActionWrite, Resource: security.ResDashboard}},
		},
	}
	for _, d := range defs {
		s.Tools.Register(d)
	}
}
