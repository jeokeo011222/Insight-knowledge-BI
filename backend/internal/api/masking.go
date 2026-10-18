package api

import (
	"net/http"

	"superset/internal/models"
	"superset/internal/queries"
	"superset/internal/security"
)

// Column-level masking on the chart-data path.
//
// The implementation lives in internal/queries so that the Agent's read-only
// executor and the chart executor mask with exactly the same rules (see
// queries/masking.go for why that matters). This file keeps the HTTP-facing
// glue and the short names the rest of the API package uses.
//
// SQL Lab is deliberately excluded: analysts need raw values to debug a query,
// and access to SQL Lab is already gated by its own permission.

// Supported mask types (aliases of the shared definitions).
const (
	MaskPhone  = queries.MaskPhone
	MaskIDCard = queries.MaskIDCard
	MaskEmail  = queries.MaskEmail
	MaskName   = queries.MaskName
	MaskFull   = queries.MaskFull
	MaskCustom = queries.MaskCustom
)

// MaskTypes is the list accepted by the column editor.
func MaskTypes() []string { return queries.MaskTypes() }

// maskString applies one mask rule to a textual value.
func maskString(value, maskType, rule string) string {
	return queries.MaskString(value, maskType, rule)
}

// maskValue converts non-string values so a numeric phone column is masked too.
func maskValue(raw any, maskType, rule string) any {
	return queries.MaskValue(raw, maskType, rule)
}

// maskRulesFor loads the mask configuration of a dataset. A metadata error is
// swallowed: a dashboard must not fail because one column row is unreadable.
func (c *chartAPI) maskRulesFor(r *http.Request, datasetID int) map[string]queries.MaskRule {
	rules, err := queries.LoadMaskRules(r.Context(), c.deps.DB, datasetID)
	if err != nil {
		return nil
	}
	return rules
}

// applyMasking rewrites every result row for non-admin callers.
func (c *chartAPI) applyMasking(r *http.Request, qc *queries.QueryContext, result *queries.QueryResult) {
	if result == nil || len(result.Results) == 0 || qc == nil {
		return
	}
	// Admins are the compliance officers: they see the raw value.
	if perms, _ := security.PermissionsFrom(r.Context()); perms.Admin() {
		return
	}
	datasetID, _ := qc.DatasourceID()
	rules := c.maskRulesFor(r, datasetID)
	if len(rules) == 0 {
		return
	}
	for index := range result.Results {
		queries.ApplyMaskRules(result.Results[index].Data, rules)
	}
}

// maskExempt reports whether the caller is the compliance officer who may see
// raw values (admin). It is the single rule both the chart path and the sample
// preview consult.
func maskExempt(r *http.Request) bool {
	perms, _ := security.PermissionsFrom(r.Context())
	return perms != nil && perms.Admin()
}

// columnPIILevel derives the coarse PII class shown in the dataset UI from the
// stored configuration. It is computed rather than stored twice: mask_type is
// the source of truth for "how to mask", pii_level for "how sensitive".
func columnPIILevel(c *models.TableColumn) string {
	if c.PIILevel != "" && c.PIILevel != queries.PIILevelNone {
		return c.PIILevel
	}
	switch c.MaskType {
	case MaskPhone, MaskIDCard, MaskEmail, MaskName, MaskFull:
		return queries.PIILevelHigh
	case MaskCustom:
		return queries.PIILevelLow
	default:
		return queries.PIILevelNone
	}
}

// normalizePIILevel keeps the stored value inside the accepted set.
func normalizePIILevel(level string) string {
	switch level {
	case queries.PIILevelLow, queries.PIILevelHigh:
		return level
	default:
		return queries.PIILevelNone
	}
}
