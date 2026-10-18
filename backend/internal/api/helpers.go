package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	apperrors "superset/internal/pkg/errors"
	"superset/internal/pkg/i18n"
	"superset/internal/security"
	"superset/internal/server"
)

// listParams captures the FAB-style list query string.
type listParams struct {
	Page     int
	Size     int
	OrderCol string
	OrderDir string
	Filters  []server.Filter
	// ParseErr keeps a malformed `filters` parameter visible instead of
	// silently degrading to "no filter" (which made users believe a filter
	// was applied while the full result set came back).
	ParseErr error
	Search   string
	All      bool
}

// localize resolves a user-facing message for the request language. `def` is
// the English fallback so a missing translation never leaks a bare key.
func localize(r *http.Request, key, def string, args ...any) string {
	return i18n.Error(i18n.Request(r), key, def, args...)
}

// parseListParams reads page/page_size/order_column/order_direction/filters.
func parseListParams(r *http.Request) listParams {
	page := server.QueryInt(r, "page", 1)
	size := server.QueryInt(r, "page_size", 25)
	if size <= 0 {
		size = 25
	}
	if size > 100 {
		size = 100
	}
	dir := strings.ToLower(server.QueryStr(r, "order_direction"))
	if dir != "desc" {
		dir = "asc"
	}
	filters, ferr := server.ParseFilters(r)
	return listParams{
		Page:     page,
		Size:     size,
		OrderCol: server.QueryStr(r, "order_column"),
		OrderDir: dir,
		Filters:  filters,
		ParseErr: ferr,
		Search:   server.QueryStr(r, "q"),
		All:      server.QueryBool(r, "all", false),
	}
}

// filterColumnPattern matches a plain SQL identifier. Column names inside the
// `filters` query parameter are attacker-controlled, so anything that is not a
// bare identifier is rejected instead of being concatenated into the WHERE
// clause (SQL injection guard).
var filterColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// quoteFilterColumn validates an API-supplied column name and returns it
// double-quoted for interpolation.
func quoteFilterColumn(col string) (string, bool) {
	if !filterColumnPattern.MatchString(col) {
		return "", false
	}
	return `"` + col + `"`, true
}

// applyFilters validates and translates FAB filters into GORM conditions.
// Column names come from the query string, so each one must be a bare
// identifier AND exist on the model being listed; anything else is rejected
// instead of producing a 500 that echoes the database's SQLSTATE text.
func applyFilters(q *gorm.DB, filters []server.Filter) (*gorm.DB, error) {
	cols := filterableColumns(q)
	for _, f := range filters {
		if f.Col == "" {
			continue
		}
		col, ok := quoteFilterColumn(f.Col)
		if !ok {
			return q, apperrors.BadRequest("Invalid filter column %q", f.Col)
		}
		if cols != nil {
			if _, exists := cols[f.Col]; !exists {
				return q, apperrors.BadRequest("Unknown filter column %q", f.Col)
			}
		}
		switch strings.ToLower(f.Opr) {
		case "eq":
			q = q.Where(col+" = ?", f.Value)
		case "ne":
			q = q.Where(col+" <> ?", f.Value)
		case "gt":
			q = q.Where(col+" > ?", f.Value)
		case "ge":
			q = q.Where(col+" >= ?", f.Value)
		case "lt":
			q = q.Where(col+" < ?", f.Value)
		case "le":
			q = q.Where(col+" <= ?", f.Value)
		case "in":
			q = q.Where(col+" IN ?", toSlice(f.Value))
		case "not in":
			q = q.Where(col+" NOT IN ?", toSlice(f.Value))
		case "ct":
			q = q.Where(col+"::text ILIKE ?", "%"+fmt.Sprint(f.Value)+"%")
		case "nct":
			q = q.Where(col+"::text NOT ILIKE ?", "%"+fmt.Sprint(f.Value)+"%")
		case "sw":
			q = q.Where(col+"::text ILIKE ?", fmt.Sprint(f.Value)+"%")
		case "is_null":
			q = q.Where(col + " IS NULL")
		case "is_not_null":
			q = q.Where(col + " IS NOT NULL")
		}
	}
	return q, nil
}

// applyListFilters applies the list filters and writes a 400 itself when the
// parameter is malformed or names a column the model does not have. The bool
// reports whether the caller should keep going.
func applyListFilters(w http.ResponseWriter, q *gorm.DB, p listParams) (*gorm.DB, bool) {
	if p.ParseErr != nil {
		server.WriteError(w, apperrors.BadRequest("Invalid filters parameter: %s", p.ParseErr.Error()))
		return q, false
	}
	next, err := applyFilters(q, p.Filters)
	if err != nil {
		server.WriteError(w, err)
		return q, false
	}
	return next, true
}

// filterSchemaCache memoises GORM model schemas so validating a filter column
// does not re-parse the struct on every request.
var filterSchemaCache sync.Map

// filterableColumns returns the database column names of the statement's model.
// It returns nil when the query is built on a raw table (schema unknown), in
// which case only the identifier check applies.
func filterableColumns(q *gorm.DB) map[string]struct{} {
	if q == nil || q.Statement == nil || q.Statement.Model == nil {
		return nil
	}
	parsed, err := schema.Parse(q.Statement.Model, &filterSchemaCache, schema.NamingStrategy{})
	if err != nil || parsed == nil {
		return nil
	}
	cols := make(map[string]struct{}, len(parsed.Fields))
	for _, field := range parsed.Fields {
		cols[field.DBName] = struct{}{}
	}
	return cols
}

func toSlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case string:
		parts := strings.Split(t, ",")
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimSpace(p))
		}
		return out
	default:
		return []any{v}
	}
}

// orderClause builds a safe ORDER BY clause from the whitelist.
func orderClause(p listParams, allowed map[string]string, fallback string) string {
	col := fallback
	if mapped, ok := allowed[strings.ToLower(p.OrderCol)]; ok {
		col = mapped
	}
	return col + " " + p.OrderDir
}

// listResultW is the ResponseWriter-aware variant used by all modules.
func listResultW[T any](w http.ResponseWriter, r *http.Request, db *gorm.DB, p listParams, order string, shaper func(*T) any) {
	var total int64
	if err := db.Count(&total).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	var rows []T
	q := db.Order(order)
	if p.All {
		if p.Size > 0 && p.Size < 1000 {
			q = q.Limit(p.Size)
		}
	} else {
		q = q.Offset((p.Page - 1) * p.Size).Limit(p.Size)
	}
	if err := q.Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]any, 0, len(rows))
	for i := range rows {
		if shaper != nil {
			out = append(out, shaper(&rows[i]))
		} else {
			out = append(out, rows[i])
		}
	}
	server.WritePaginated(w, total, out, nil, nil)
}

// jsonField decodes a datatypes.JSON column into a generic map.
func jsonField(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// jsonMap decodes a JSON column into a map, defaulting to an empty map.
func jsonMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return map[string]any{}
	}
	return v
}

// jsonArray decodes a JSON column into a slice, defaulting to an empty slice.
func jsonArray(raw []byte) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var v []any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return []any{}
	}
	return v
}

// mustJSON marshals a value, returning "null" on error.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// idsParam parses a "ids=1,2,3" query parameter.
func idsParam(r *http.Request) []int {
	raw := server.QueryStr(r, "ids")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// currentUser returns the caller, or an error when anonymous.
func currentUser(r *http.Request) (*security.AuthUser, error) {
	u := security.UserFrom(r.Context())
	if u == nil {
		return nil, apperrors.Unauthorized("Unauthorized")
	}
	return u, nil
}

// ownedBy reports whether the user owns the object or is an admin.
func ownedBy(r *http.Request, ownerID int) bool {
	u := security.UserFrom(r.Context())
	if u == nil {
		return false
	}
	if u.ID == ownerID {
		return true
	}
	perms, _ := security.PermissionsFrom(r.Context())
	return perms.Admin()
}

// userIDOf returns the caller id (0 for anonymous).
func userIDOf(r *http.Request) int {
	if u := security.UserFrom(r.Context()); u != nil {
		return u.ID
	}
	return 0
}

// intPtrNil returns nil for 0, else a pointer.
func intPtrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// derefInt returns the value or a default.
func derefInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// nowPtr returns a pointer to the current UTC time.
func nowPtr() *time.Time {
	t := time.Now().UTC()
	return &t
}

// parseCSVList splits a comma separated list.
func parseCSVList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
