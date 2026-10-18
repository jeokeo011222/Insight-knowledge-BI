package models

import (
	"time"

	"gorm.io/datatypes"
)

// semantic_model.go holds the MDL-lite layer (benchmark W-05/W-08). It is
// deliberately separate from semantic.go, which maps Superset's own
// semantic_layers / semantic_views tables: those are an empty shell in this
// deployment, and reusing their JSON blob would have hidden every validation
// error inside one opaque column.
//
// The core promise mirrors Wren's MDL: a field that is not declared here does
// not exist for the Agent, so the semantic model — not the physical schema — is
// what the Agent is allowed to talk about.

// SemanticModel is one queryable subject, scoped to a single dataset (the
// cross-dataset graph is deferred until there is a real caller for it).
type SemanticModel struct {
	BaseModel
	// Uniqueness is per (dataset, name) among *live* rows only. The partial
	// unique index lives in pkg/database.migrateSemanticIndexes: a GORM
	// uniqueIndex tag would create a plain one, and soft-deleted rows would then
	// occupy the key forever ("delete, then regenerate" → duplicate key).
	DatasetID   int    `gorm:"column:dataset_id;not null;index:ix_semantic_models_dataset" json:"dataset_id"`
	Name        string `gorm:"column:name;size:64;not null" json:"name"`
	Description string `gorm:"column:description;type:text" json:"description"`
	// PrimaryKey names a declared field (usually a column field). It is required
	// for many-to-many relationships to be valid — see Validate.
	PrimaryKey string `gorm:"column:primary_key;size:128" json:"primary_key"`
	// DefaultFilters is the structured form of the default_filter rules, kept on
	// the model so a cold-started model can be published without visiting the
	// rules tab. Rules remain the source of truth when both exist.
	DefaultFilters datatypes.JSON `gorm:"column:default_filters;type:jsonb" json:"default_filters"`
	Status         string         `gorm:"column:status;size:16;not null;default:draft" json:"status"` // draft|published
	Version        int            `gorm:"column:version;not null;default:1" json:"version"`
	// ManifestHash fingerprints the compiled manifest. Two deployments with the
	// same hash answer the same question the same way; a diff means the Agent's
	// vocabulary changed.
	ManifestHash string `gorm:"column:manifest_hash;size:64" json:"manifest_hash"`
	// Source records how the model came to exist: manual (hand built),
	// generated (cold start from the dataset), imported (JSON/YAML).
	Source string `gorm:"column:source;size:16;not null;default:manual" json:"source"`
	// PublishedOn is set the first time status flips to published, so "how long
	// has this been drifting" has an answer.
	PublishedOn *time.Time `gorm:"column:published_on" json:"published_on"`
}

// TableName returns the table name.
func (SemanticModel) TableName() string { return "semantic_models" }

// Field kinds. "column" and "dimension" are both groupable attributes; the
// distinction is intentional: column is a passthrough of a physical column,
// dimension is an expression over one or more columns (region_for_sales...).
const (
	FieldKindColumn    = "column"
	FieldKindMetric    = "metric"
	FieldKindDimension = "dimension"
	FieldKindTime      = "time"
)

// SemanticField is one declared attribute of a model.
type SemanticField struct {
	BaseModel
	// Same partial-unique rule as SemanticModel: rewriting a model soft-deletes
	// every field row and re-inserts the same names, so a plain
	// UNIQUE(model_id, name) rejects the second save with a duplicate key.
	ModelID int    `gorm:"column:model_id;not null;index" json:"model_id"`
	Name    string `gorm:"column:name;size:64;not null" json:"name"`
	Kind    string `gorm:"column:kind;size:16;not null;default:column" json:"kind"`
	// Expression is the SQL fragment this field compiles to. Empty means "the
	// physical column with the same name" for column/time kinds, and is invalid
	// for metric/dimension.
	Expression  string         `gorm:"column:expression;type:text" json:"expression"`
	Type        string         `gorm:"column:type;size:32" json:"type"`
	Description string         `gorm:"column:description;type:text" json:"description"`
	Unit        string         `gorm:"column:unit;size:32" json:"unit"`
	EnumValues  datatypes.JSON `gorm:"column:enum_values;type:jsonb" json:"enum_values"`
	Synonyms    datatypes.JSON `gorm:"column:synonyms;type:jsonb" json:"synonyms"`
	// Hidden fields are addressable by the model but not offered to the Agent —
	// the join key case: the relationship needs customer_id, the Agent does not.
	Hidden     bool   `gorm:"column:hidden;not null;default:false" json:"hidden"`
	PIILevel   string `gorm:"column:pii_level;size:16;not null;default:none" json:"pii_level"`
	SortOrder  int    `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
	IsDttm     bool   `gorm:"column:is_dttm;not null;default:false" json:"is_dttm"`
	DefaultAgg string `gorm:"column:default_agg;size:16" json:"default_agg"`
}

// TableName returns the table name.
func (SemanticField) TableName() string { return "semantic_fields" }

// Relationship cardinalities.
const (
	JoinManyToOne  = "many_to_one"
	JoinOneToMany  = "one_to_many"
	JoinOneToOne   = "one_to_one"
	JoinManyToMany = "many_to_many"
)

// SemanticRelationship joins two models. Condition is a SQL predicate over the
// two models' physical tables (e.g. "orders.customer_id = customers.id").
type SemanticRelationship struct {
	BaseModel
	Name        string `gorm:"column:name;size:64" json:"name"`
	FromModelID int    `gorm:"column:from_model_id;not null;index" json:"from_model_id"`
	ToModelID   int    `gorm:"column:to_model_id;not null;index" json:"to_model_id"`
	JoinType    string `gorm:"column:join_type;size:16;not null;default:many_to_one" json:"join_type"`
	Condition   string `gorm:"column:condition;type:text;not null" json:"condition"`
}

// TableName returns the table name.
func (SemanticRelationship) TableName() string { return "semantic_relationships" }

// Rule scopes and categories.
const (
	RuleScopeGlobal  = "global"
	RuleScopeDataset = "dataset"
	RuleScopeModel   = "model"

	RuleDefaultFilter  = "default_filter"
	RuleNaming         = "naming"
	RuleCurrency       = "currency"
	RuleTime           = "time"
	RuleExternalID     = "external_id"
	RuleCanonicalTable = "canonical_table"
)

// RuleCategories is the fixed vocabulary. A new category needs a consumer in
// this package, otherwise the rule is just prose the Agent ignores — which is
// exactly the failure mode the benchmark called out.
var RuleCategories = []string{
	RuleDefaultFilter, RuleNaming, RuleCurrency, RuleTime, RuleExternalID, RuleCanonicalTable,
}

// SemanticRule carries business knowledge that is not expressible as a column
// comment. Two consumers, deliberately:
//
//   - Body (markdown) is retrieved into the prompt, top-k per question;
//   - Spec (json) is applied at execution time, so "ignore refunded orders"
//     survives a model that forgets to read the prompt.
//
// A rule with an empty Spec is prompt-only; that is allowed and honest.
type SemanticRule struct {
	BaseModel
	Scope     string `gorm:"column:scope;size:16;not null;default:dataset" json:"scope"`
	DatasetID int    `gorm:"column:dataset_id;index" json:"dataset_id"`
	ModelID   int    `gorm:"column:model_id;index" json:"model_id"`
	Category  string `gorm:"column:category;size:24;not null;index" json:"category"`
	Title     string `gorm:"column:title;size:128" json:"title"`
	Body      string `gorm:"column:body;type:text;not null" json:"body"`
	// Spec is the machine-readable half: {"table":"orders","field":"deleted_at",
	// "op":"is_null"} or {"table":"orders","field":"status","op":"not_in",
	// "value":["refunded"]}.
	Spec   datatypes.JSON `gorm:"column:spec;type:jsonb" json:"spec"`
	Status string         `gorm:"column:status;size:16;not null;default:active" json:"status"` // active|disabled
	// Evidence records where the rule came from (sample values, documentation,
	// human). Governance needs provenance to decide whether to trust a rule.
	Evidence  string `gorm:"column:evidence;size:255" json:"evidence"`
	Version   int    `gorm:"column:version;not null;default:1" json:"version"`
	SortOrder int    `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
}

// TableName returns the table name.
func (SemanticRule) TableName() string { return "semantic_rules" }
