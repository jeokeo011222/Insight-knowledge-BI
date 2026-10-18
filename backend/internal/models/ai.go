package models

import "time"

// ai.go holds the 6 AI Agent business tables. They follow the same audit
// convention as the rest of the schema (BaseModel: created_on/changed_on +
// deleted), which intentionally deviates from the design doc's created_at
// naming for consistency with the existing models.

// AIAgentRun records one Agent run (plan→act→observe→answer).
type AIAgentRun struct {
	BaseModel
	UserID        int    `gorm:"column:user_id;index" json:"user_id"`
	SessionID     string `gorm:"column:session_id;size:64;index" json:"session_id"` // analysis session: consecutive runs share one id for follow-ups
	Scope         string `gorm:"column:scope;size:32;index" json:"scope"`           // dashboard|sqllab|chart|explore
	DashboardID   *int   `gorm:"column:dashboard_id" json:"dashboard_id,omitempty"`
	ChartID       *int   `gorm:"column:chart_id" json:"chart_id,omitempty"`
	DatasetID     *int   `gorm:"column:dataset_id;index" json:"dataset_id,omitempty"`
	Question      string `gorm:"column:question;type:text" json:"question"`
	Status        string `gorm:"column:status;size:32;index" json:"status"` // running|done|stopped|failed
	StepCount     int    `gorm:"column:step_count" json:"step_count"`
	ToolCallCount int    `gorm:"column:tool_call_count" json:"tool_call_count"`
	TokenUsage    int    `gorm:"column:token_usage" json:"token_usage"`
	LatencyMS     int    `gorm:"column:latency_ms" json:"latency_ms"`
	// Observability (design §9.7): the numbers the Agent can only improve if it
	// measures them. FirstTokenMS is the perceived latency of a streaming run.
	FirstTokenMS     int     `gorm:"column:first_token_ms" json:"first_token_ms"`
	ToolErrorCount   int     `gorm:"column:tool_error_count" json:"tool_error_count"`
	EmptyResultCount int     `gorm:"column:empty_result_count" json:"empty_result_count"`
	SelfCheckCount   int     `gorm:"column:self_check_count" json:"self_check_count"`
	ModelName        string  `gorm:"column:model_name;size:128" json:"model_name"`
	Temperature      float64 `gorm:"column:temperature" json:"temperature"`
	Seed             int     `gorm:"column:seed" json:"seed"`
	// GeneratedSQL / SQLHash make "same question, same SQL" auditable: the SQL
	// actually executed plus its fingerprint for cross-run comparison.
	GeneratedSQL string `gorm:"column:generated_sql;type:text" json:"generated_sql"`
	SQLHash      string `gorm:"column:sql_hash;size:64;index" json:"sql_hash"`
	FinalAnswer  string `gorm:"column:final_answer;type:text" json:"final_answer"`
	ErrorMsg     string `gorm:"column:error_msg;type:text" json:"error_msg"`
	// PlanJSON is the structured plan the run committed to. Persisting it lets a
	// dropped connection replay the same plan on resume instead of inventing a
	// second one (audit §4-5).
	PlanJSON string `gorm:"column:plan_json;type:text" json:"plan_json"`
	// ResumedFrom links a continuation run back to the run it continued.
	ResumedFrom *int `gorm:"column:resumed_from;index" json:"resumed_from,omitempty"`
}

// TableName returns the original table name.
func (AIAgentRun) TableName() string { return "ai_agent_run" }

// AIToolCallLog records every skill/tool invocation, including write
// snapshots used for auditing and rollback.
type AIToolCallLog struct {
	BaseModel
	RunID      int    `gorm:"column:run_id;index" json:"run_id"`
	StepNo     int    `gorm:"column:step_no" json:"step_no"`
	Kind       string `gorm:"column:kind;size:16;index" json:"kind"`               // skill|tool
	Name       string `gorm:"column:name;size:64;index" json:"name"`               // dupont|query_executor|chart_writer
	SideEffect string `gorm:"column:side_effect;size:16;index" json:"side_effect"` // read|write|destructive
	InputArgs  string `gorm:"column:input_args;type:jsonb" json:"input_args"`
	Result     string `gorm:"column:result;type:jsonb" json:"result"`
	Snapshot   string `gorm:"column:snapshot;type:jsonb" json:"snapshot"` // pre-write snapshot (rollback)
	TargetIDs  string `gorm:"column:target_ids;type:jsonb" json:"target_ids"`
	QueryID    *int   `gorm:"column:query_id;index" json:"query_id,omitempty"` // links to the query audit row when the tool ran SQL
	Status     string `gorm:"column:status;size:16" json:"status"`             // ok|error|denied
	ErrorMsg   string `gorm:"column:error_msg;type:text" json:"error_msg"`
	DurationMS int    `gorm:"column:duration_ms" json:"duration_ms"`
}

// TableName returns the original table name.
func (AIToolCallLog) TableName() string { return "ai_tool_call_log" }

// AISkillRegistry is the DB-backed skill plugin definition. Built-in skills are
// seeded here so an operator can see and disable them; skills created on the
// settings page are prompt-defined ("declarative") and get an executor that
// calls the model with their instructions.
type AISkillRegistry struct {
	BaseModel
	Code        string `gorm:"column:code;size:64;uniqueIndex" json:"code"` // nl2sql|insight|dupont|...
	Title       string `gorm:"column:title;size:128" json:"title"`
	Domain      string `gorm:"column:domain;size:64;index" json:"domain"` // finance|sales|ops|general
	Description string `gorm:"column:description;type:text" json:"description"`
	Params      string `gorm:"column:params;type:jsonb" json:"params"` // parameter presets
	// Instructions is the prompt body of a declarative skill. Empty for
	// built-ins, whose behaviour lives in code.
	Instructions string `gorm:"column:instructions;type:text" json:"instructions"`
	// InputSchema is the JSON Schema advertised to the planner for a
	// declarative skill; built-ins carry theirs in code.
	InputSchema string `gorm:"column:input_schema;type:jsonb" json:"input_schema"`
	// ToolCodes is the JSON array of tools a declarative skill may call.
	ToolCodes string `gorm:"column:tool_codes;type:jsonb" json:"tool_codes"`
	CostTier  string `gorm:"column:cost_tier;size:16;default:standard" json:"cost_tier"` // cheap|standard|heavy
	Enabled   bool   `gorm:"column:enabled;default:true" json:"enabled"`
	IsBuiltin bool   `gorm:"column:is_builtin;default:false" json:"is_builtin"`
}

// TableName returns the original table name.
func (AISkillRegistry) TableName() string { return "ai_skill_registry" }

// AIToolRegistry is the DB-backed tool plugin definition. Built-in tools are
// seeded here so they can be disabled without a redeploy; tools created on the
// settings page are HTTP tools that call an external endpoint.
type AIToolRegistry struct {
	BaseModel
	Code         string `gorm:"column:code;size:64;uniqueIndex" json:"code"`
	Title        string `gorm:"column:title;size:128" json:"title"`
	Description  string `gorm:"column:description;type:text" json:"description"`
	InputSchema  string `gorm:"column:input_schema;type:jsonb" json:"input_schema"`
	SideEffect   string `gorm:"column:side_effect;size:16;default:read" json:"side_effect"` // read|write|destructive
	Kind         string `gorm:"column:kind;size:16;default:builtin" json:"kind"`            // builtin|http
	Method       string `gorm:"column:method;size:8" json:"method"`
	URL          string `gorm:"column:url;type:text" json:"url"`
	Headers      string `gorm:"column:headers;type:jsonb" json:"headers"`
	BodyTemplate string `gorm:"column:body_template;type:text" json:"body_template"`
	TimeoutSec   int    `gorm:"column:timeout_sec;default:30" json:"timeout_sec"`
	Enabled      bool   `gorm:"column:enabled;default:true" json:"enabled"`
	IsBuiltin    bool   `gorm:"column:is_builtin;default:false" json:"is_builtin"`
}

// TableName is the tool plugin table.
func (AIToolRegistry) TableName() string { return "ai_tool_registry" }

// AIChatHistory stores Q&A records (compat entrypoints such as /ai/chat).
type AIChatHistory struct {
	BaseModel
	UserID        int     `gorm:"column:user_id;index" json:"user_id"`
	RunID         *int    `gorm:"column:run_id;index" json:"run_id,omitempty"`
	Scope         string  `gorm:"column:scope;size:32;index" json:"scope"`
	DatasetID     *int    `gorm:"column:dataset_id;index" json:"dataset_id,omitempty"`
	Question      string  `gorm:"column:question;type:text" json:"question"`
	GeneratedSQL  string  `gorm:"column:generated_sql;type:text" json:"generated_sql"`
	LLMResponse   string  `gorm:"column:llm_response;type:text" json:"llm_response"`
	QueryStatus   string  `gorm:"column:query_status;size:32" json:"query_status"`
	ExecutionTime float64 `gorm:"column:execution_time" json:"execution_time"`
}

// TableName returns the original table name.
func (AIChatHistory) TableName() string { return "ai_chat_history" }

// AIAuditLog audits every LLM call (masked prompt, model, tokens, errors).
// The sampling columns are the evidence chain for reproducibility: without
// them a non-deterministic answer cannot be explained after the fact.
type AIAuditLog struct {
	BaseModel
	RunID      *int   `gorm:"column:run_id;index" json:"run_id,omitempty"`
	UserID     int    `gorm:"column:user_id;index" json:"user_id"`
	Operation  string `gorm:"column:operation;size:64;index" json:"operation"` // chat|nl2sql|insight|agent_step
	Prompt     string `gorm:"column:prompt;type:text" json:"prompt"`
	ModelName  string `gorm:"column:model_name;size:128" json:"model_name"`
	TokenUsage int    `gorm:"column:token_usage" json:"token_usage"`
	ErrorMsg   string `gorm:"column:error_msg;type:text" json:"error_msg"`

	Temperature float64 `gorm:"column:temperature" json:"temperature"`
	MaxTokens   int     `gorm:"column:max_tokens" json:"max_tokens"`
	Seed        int     `gorm:"column:seed" json:"seed"`
	PromptHash  string  `gorm:"column:prompt_hash;size:64;index" json:"prompt_hash"`
	LatencyMS   int     `gorm:"column:latency_ms" json:"latency_ms"`
	CacheHit    bool    `gorm:"column:cache_hit" json:"cache_hit"`
}

// TableName returns the original table name.
func (AIAuditLog) TableName() string { return "ai_audit_log" }

// AIMetaEmbedding stores metadata vectors (pgvector) for recall.
type AIMetaEmbedding struct {
	BaseModel
	DatasetID int    `gorm:"column:dataset_id;index" json:"dataset_id"`
	ColName   string `gorm:"column:col_name;size:255;index" json:"col_name"`
	Kind      string `gorm:"column:kind;size:32" json:"kind"` // column|metric|table|glossary|skill
	Text      string `gorm:"column:text;type:text" json:"text"`
	Embedding string `gorm:"column:embedding;type:vector(1536)" json:"embedding"`
}

// TableName returns the original table name.
func (AIMetaEmbedding) TableName() string { return "ai_meta_embedding" }

// AIProvider is one runtime-editable LLM endpoint (the "大模型供应商" table).
// It carries everything the gateway needs for a call, so the Agent can be
// repointed at another model from the UI without a restart.
type AIProvider struct {
	BaseModel
	Name         string `gorm:"column:name;size:128;uniqueIndex" json:"name"`
	ProviderType string `gorm:"column:provider_type;size:32" json:"provider_type"` // ollama|openai_compatible
	BaseURL      string `gorm:"column:base_url;size:512" json:"base_url"`
	Model        string `gorm:"column:model;size:128" json:"model"`
	// APIKey is stored server-side only; the API masks it behind a
	// `api_key_configured` flag, exactly like the global settings used to.
	APIKey      string `gorm:"column:api_key;type:text" json:"api_key"`
	TimeoutSec  int    `gorm:"column:timeout_sec" json:"timeout_sec"`
	Enabled     bool   `gorm:"column:enabled;default:true" json:"enabled"`
	Description string `gorm:"column:description;type:text" json:"description"`
}

// TableName returns the original table name.
func (AIProvider) TableName() string { return "ai_provider" }

// AIAgent is a configured agent: a named, plugin-style bundle that binds one
// LLM provider plus the skills/tools it may use (empty = all) and its own
// guardrails. The Agent runtime resolves it per run.
type AIAgent struct {
	BaseModel
	Name        string `gorm:"column:name;size:128;uniqueIndex" json:"name"`
	Description string `gorm:"column:description;type:text" json:"description"`
	ProviderID  int    `gorm:"column:provider_id;index" json:"provider_id"`
	IsDefault   bool   `gorm:"column:is_default;index" json:"is_default"`
	Enabled     bool   `gorm:"column:enabled;default:true" json:"enabled"`
	// IsBuiltin marks an agent shipped with the product. Built-ins are seeded by
	// code on startup and are immutable: the API refuses to update or delete
	// them, so operators cannot silently diverge from the shipped presets.
	IsBuiltin bool `gorm:"column:is_builtin;index;default:false" json:"is_builtin"`
	// SkillCodes/ToolCodes are JSON arrays of registry names; empty means the
	// agent may use every registered capability (the historical behaviour).
	SkillCodes string `gorm:"column:skill_codes;type:jsonb" json:"skill_codes"`
	ToolCodes  string `gorm:"column:tool_codes;type:jsonb" json:"tool_codes"`

	MaxSteps             int  `gorm:"column:max_steps" json:"max_steps"`
	MaxToolCalls         int  `gorm:"column:max_tool_calls" json:"max_tool_calls"`
	TokenBudget          int  `gorm:"column:token_budget" json:"token_budget"`
	RunTimeoutSec        int  `gorm:"column:run_timeout_sec" json:"run_timeout_sec"`
	WriteToolsEnabled    bool `gorm:"column:write_tools_enabled" json:"write_tools_enabled"`
	WriteConfirmRequired bool `gorm:"column:write_confirm_required" json:"write_confirm_required"`
}

// TableName returns the original table name.
func (AIAgent) TableName() string { return "ai_agent" }

// AIMetric is one entry of the Lite metric dictionary (semantic layer): a
// governed business-metric definition the Agent must align to instead of
// inventing aggregation expressions. Only `approved` rows are injected into
// prompts; drafts stay editable until a reviewer signs them off.
type AIMetric struct {
	BaseModel
	Name        string `gorm:"column:name;size:128;uniqueIndex" json:"name"` // business name, e.g. 活跃用户
	Description string `gorm:"column:description;type:text" json:"description"`
	Expression  string `gorm:"column:expression;type:text" json:"expression"`  // canonical SQL fragment, e.g. COUNT(DISTINCT user_id)
	DatasetID   int    `gorm:"column:dataset_id;index" json:"dataset_id"`      // 0 = applies to any dataset
	Dimensions  string `gorm:"column:dimensions;type:jsonb" json:"dimensions"` // allowed split columns, JSON array
	Unit        string `gorm:"column:unit;size:32" json:"unit"`
	Status      string `gorm:"column:status;size:16;index;default:draft" json:"status"` // draft|approved
	OwnerID     int    `gorm:"column:owner_id" json:"owner_id"`
}

// TableName returns the original table name.
func (AIMetric) TableName() string { return "ai_metric" }

// AISQLMemory is one confirmed natural-language → SQL example (NL→SQL 范例记忆).
//
// It is the cheapest accuracy lever in the whole Agent plane: a repeated
// question ("上个月华东的 GMV") is answered with the SQL a human already
// validated, instead of being re-derived from scratch. Three rules keep it from
// becoming a liability:
//
//   - only `approved` rows are recalled into prompts, so a wrong auto-capture
//     can never teach the model a wrong join;
//   - the question is normalised and hashed, so the same question cannot
//     accumulate duplicates;
//   - a row records who saved it and from which run, so a bad example can be
//     traced and removed.
type AISQLMemory struct {
	BaseModel
	DatasetID    int    `gorm:"column:dataset_id;index;uniqueIndex:uq_ai_sql_memory_dataset_hash" json:"dataset_id"`
	Question     string `gorm:"column:question;type:text" json:"question"`
	QuestionHash string `gorm:"column:question_hash;size:64;uniqueIndex:uq_ai_sql_memory_dataset_hash" json:"question_hash"`
	SQLQuery     string `gorm:"column:sql_query;type:text" json:"sql_query"`
	// SpecJSON keeps the structured request (metrics/dimensions/filters) when the
	// example came from semantic_query, so it can be replayed without the model.
	SpecJSON string `gorm:"column:spec_json;type:jsonb" json:"spec_json"`
	// Source is seed|confirmed|auto. Auto captures wait for review.
	Source string `gorm:"column:source;size:16;index;default:confirmed" json:"source"`
	// Status is approved|pending|rejected. Only approved rows are recalled.
	Status    string     `gorm:"column:status;size:16;index;default:approved" json:"status"`
	Quality   int        `gorm:"column:quality;default:1" json:"quality"`
	RunID     *int       `gorm:"column:run_id;index" json:"run_id,omitempty"`
	CreatedBy int        `gorm:"column:created_by;index" json:"created_by"`
	Hits      int        `gorm:"column:hits;default:0" json:"hits"`
	LastHitOn *time.Time `gorm:"column:last_hit_on" json:"last_hit_on,omitempty"`
}

// TableName returns the original table name.
func (AISQLMemory) TableName() string { return "ai_sql_memory" }
