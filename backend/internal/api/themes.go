package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterThemes mounts /api/v1/theme/* and /api/v1/css_template/*
// (superset/themes/api.py, superset/css_templates/api.py).
func RegisterThemes(r chi.Router, deps *Deps) {
	a := &themeAPI{deps: deps}

	r.Route("/theme", func(tr chi.Router) {
		tr.Use(security.RequireUser)

		// Static paths must be registered before /{pk} (chi gives them priority,
		// mirroring superset/themes/api.py: set_system_default, set_system_dark,
		// unset_system_default, unset_system_dark, system, export, import).
		tr.Get("/system", a.system)
		tr.With(security.RequirePermission(security.ActionRead, security.ResTheme)).Get("/export/", a.exportThemes)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTheme)).Post("/import/", a.importThemes)
		// The four system slots (default / dark) change the appearance of the
		// whole deployment, so they are ResSystem — administrator-only — rather
		// than ResTheme. ResTheme still governs the normal theme CRUD, which an
		// analyst may use to save a personal theme.
		tr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).Put("/{pk}/set_system_default", a.setSystemDefault)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).Put("/{pk}/set_system_dark", a.setSystemDark)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).Delete("/unset_system_default", a.unsetSystemDefault)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).Delete("/unset_system_dark", a.unsetSystemDark)

		tr.With(security.RequirePermission(security.ActionRead, security.ResTheme)).Get("/", a.list)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTheme)).Post("/", a.create)
		tr.With(security.RequirePermission(security.ActionRead, security.ResTheme)).Get("/{pk}", a.get)
		tr.With(security.RequirePermission(security.ActionWrite, security.ResTheme)).Put("/{pk}", a.update)
		tr.With(security.RequirePermission(security.ActionDelete, security.ResTheme)).Delete("/{pk}", a.remove)
		tr.With(security.RequirePermission(security.ActionDelete, security.ResTheme)).Delete("/", a.bulkDelete)
	})

	r.Route("/css_template", func(cr chi.Router) {
		cr.Use(security.RequireUser)

		cr.With(security.RequirePermission(security.ActionRead, security.ResCssTemplate)).Get("/", a.listCSS)
		cr.With(security.RequirePermission(security.ActionWrite, security.ResCssTemplate)).Post("/", a.createCSS)
		cr.With(security.RequirePermission(security.ActionRead, security.ResCssTemplate)).Get("/{pk}", a.getCSS)
		cr.With(security.RequirePermission(security.ActionWrite, security.ResCssTemplate)).Put("/{pk}", a.updateCSS)
		cr.With(security.RequirePermission(security.ActionDelete, security.ResCssTemplate)).Delete("/{pk}", a.removeCSS)
	})
}

type themeAPI struct{ deps *Deps }

type themePayload struct {
	UUID      string `json:"uuid"`
	ThemeName string `json:"theme_name"`
	JSONData  any    `json:"json_data"`
	IsDefault *bool  `json:"is_default"`
	IsDark    *bool  `json:"is_dark"`
}

type cssTemplatePayload struct {
	UUID         string `json:"uuid"`
	TemplateName string `json:"template_name"`
	CSS          string `json:"css"`
}

// themeJSONValue decodes the stored json_data string for the response.
func themeJSONValue(raw string) any {
	if raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

func themeJSON(t *models.Theme) map[string]any {
	return map[string]any{
		"id": t.ID, "uuid": t.UUID, "theme_name": t.ThemeName,
		"json_data":  themeJSONValue(t.JSONData),
		"is_default": t.IsSystemDefault, "is_system": t.IsSystem,
		"is_system_default": t.IsSystemDefault, "is_system_dark": t.IsSystemDark,
		"created_on": t.CreatedOn, "changed_on": t.ChangedOn,
	}
}

func cssTemplateJSON(c *models.CssTemplate) map[string]any {
	return map[string]any{
		"id": c.ID, "uuid": c.UUID, "template_name": c.TemplateName,
		"css": c.CSS, "created_on": c.CreatedOn, "changed_on": c.ChangedOn,
	}
}

// Themes ------------------------------------------------------------------

func (a *themeAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Theme{})
	if p.Search != "" {
		q = q.Where("theme_name ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "theme_name asc", func(t *models.Theme) any { return themeJSON(t) })
}

func (a *themeAPI) find(r *http.Request) (*models.Theme, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var t models.Theme
	if err := a.deps.DB.WithContext(r.Context()).First(&t, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Theme not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &t, nil
}

func (a *themeAPI) get(w http.ResponseWriter, r *http.Request) {
	t, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": themeJSON(t)})
}

func (a *themeAPI) create(w http.ResponseWriter, r *http.Request) {
	var p themePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.ThemeName == "" {
		server.WriteError(w, apperrors.BadRequest("theme_name is required"))
		return
	}
	data := jsonString(p.JSONData)
	if err := validateThemeJSON(data); err != nil {
		server.WriteError(w, err)
		return
	}
	t := models.Theme{
		UUID: orDefault(p.UUID, uuid.NewString()), ThemeName: p.ThemeName,
		JSONData: data,
	}
	if err := a.deps.DB.WithContext(r.Context()).Create(&t).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create theme: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": themeJSON(&t)})
}

func (a *themeAPI) update(w http.ResponseWriter, r *http.Request) {
	t, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p themePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.ThemeName != "" {
		updates["theme_name"] = p.ThemeName
	}
	if p.JSONData != nil {
		data := jsonString(p.JSONData)
		if err := validateThemeJSON(data); err != nil {
			server.WriteError(w, err)
			return
		}
		updates["json_data"] = data
	}
	// System slots are admin-only and must move atomically via
	// set_system_default / set_system_dark, so is_default/is_dark sent with a
	// regular create/update no longer write them directly.
	if err := a.deps.DB.WithContext(r.Context()).Model(t).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update theme: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": themeJSON(t)})
}

func (a *themeAPI) remove(w http.ResponseWriter, r *http.Request) {
	t, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if t.IsSystem {
		server.WriteError(w, apperrors.BadRequest("System themes cannot be deleted"))
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Delete(t).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// CSS templates -----------------------------------------------------------

func (a *themeAPI) listCSS(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.CssTemplate{})
	if p.Search != "" {
		q = q.Where("template_name ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "template_name asc", func(c *models.CssTemplate) any { return cssTemplateJSON(c) })
}

func (a *themeAPI) findCSS(r *http.Request) (*models.CssTemplate, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var c models.CssTemplate
	if err := a.deps.DB.WithContext(r.Context()).First(&c, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("CSS template not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &c, nil
}

func (a *themeAPI) getCSS(w http.ResponseWriter, r *http.Request) {
	c, err := a.findCSS(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": cssTemplateJSON(c)})
}

func (a *themeAPI) createCSS(w http.ResponseWriter, r *http.Request) {
	var p cssTemplatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.TemplateName == "" {
		server.WriteError(w, apperrors.BadRequest("template_name is required"))
		return
	}
	c := models.CssTemplate{
		UUID: orDefault(p.UUID, uuid.NewString()), TemplateName: p.TemplateName, CSS: p.CSS,
	}
	if err := a.deps.DB.WithContext(r.Context()).Create(&c).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create CSS template: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": cssTemplateJSON(&c)})
}

func (a *themeAPI) updateCSS(w http.ResponseWriter, r *http.Request) {
	c, err := a.findCSS(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p cssTemplatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.TemplateName != "" {
		updates["template_name"] = p.TemplateName
	}
	if p.CSS != "" {
		updates["css"] = p.CSS
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(c).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update CSS template: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(c, c.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": cssTemplateJSON(c)})
}

func (a *themeAPI) removeCSS(w http.ResponseWriter, r *http.Request) {
	c, err := a.findCSS(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Delete(c).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// System theme slots + validation (superset/themes/utils.py, api.py) -------

// themeModes are the algorithm values Ant Design understands; "system" is a
// mode rather than a mapping algorithm but is accepted like upstream.
var themeAlgorithmValues = map[string]bool{
	"default": true, "dark": true, "compact": true, "system": true,
}

// isValidAlgorithmValue mirrors _is_valid_algorithm: a string or a list of
// strings drawn from ThemeMode values.
func isValidAlgorithmValue(v any) bool {
	switch a := v.(type) {
	case string:
		return themeAlgorithmValues[a]
	case []any:
		for _, item := range a {
			s, ok := item.(string)
			if !ok || !themeAlgorithmValues[s] {
				return false
			}
		}
		return len(a) > 0
	default:
		return false
	}
}

// validateThemeJSON mirrors is_valid_theme: token/components must be objects,
// hashed/inherit booleans, algorithm a valid mode string or list. An empty
// object is valid.
func validateThemeJSON(raw string) error {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return apperrors.BadRequest("json_data is not a valid JSON object")
	}
	for field, want := range map[string]any{"token": map[string]any{}, "components": map[string]any{}} {
		if v, ok := m[field]; ok {
			if _, isObj := v.(map[string]any); !isObj {
				return apperrors.BadRequest("Theme json_data is not valid: %s must be an object", field)
			}
			_ = want
		}
	}
	for _, field := range []string{"hashed", "inherit"} {
		if v, ok := m[field]; ok {
			if _, isBool := v.(bool); !isBool {
				return apperrors.BadRequest("Theme json_data is not valid: %s must be a boolean", field)
			}
		}
	}
	if v, ok := m["algorithm"]; ok && !isValidAlgorithmValue(v) {
		return apperrors.BadRequest("Theme json_data is not valid: algorithm must be one of default/dark/compact/system")
	}
	sanitizeThemeTokens(m)
	return nil
}

// sanitizeThemeTokens mirrors sanitize_theme_tokens: block javascript:/data:
// URLs in brandSpinnerUrl / brandLogoHref token values.
func sanitizeThemeTokens(m map[string]any) {
	token, ok := m["token"].(map[string]any)
	if !ok {
		return
	}
	for _, key := range []string{"brandSpinnerUrl", "brandLogoHref"} {
		if s, ok := token[key].(string); ok && isUnsafeURL(s) {
			token[key] = nil
		}
	}
}

func isUnsafeURL(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:")
}

// slotAlgorithmFor mirrors enforce_theme_algorithm: a theme served in the
// default (light) or dark slot must carry the slot's algorithm, otherwise Ant
// Design derives a broken half-light half-dark token set. Modifiers such as
// "compact" are preserved.
func slotAlgorithmFor(slot string, jsonData any) any {
	wanted, conflicting := "default", "dark"
	if slot == "dark" {
		wanted, conflicting = "dark", "default"
	}
	obj, ok := jsonData.(map[string]any)
	if !ok {
		return wanted
	}
	raw, ok := obj["algorithm"]
	if !ok {
		return wanted
	}
	var list []string
	switch a := raw.(type) {
	case string:
		list = []string{a}
	case []any:
		for _, item := range a {
			if s, ok := item.(string); ok {
				list = append(list, s)
			}
		}
	default:
		return wanted
	}
	hasWanted, hasConflicting := false, false
	for _, s := range list {
		if s == wanted {
			hasWanted = true
		}
		if s == conflicting {
			hasConflicting = true
		}
	}
	if hasWanted && !hasConflicting {
		return raw
	}
	corrected := []string{wanted}
	for _, s := range list {
		if s != wanted && s != conflicting {
			corrected = append(corrected, s)
		}
	}
	if len(corrected) == 1 {
		return corrected[0]
	}
	return corrected
}

// requireThemeAdmin mirrors the "Only administrators can set system themes"
// guard on set_system_default/set_system_dark.
func requireThemeAdmin(r *http.Request) error {
	if perms, ok := security.PermissionsFrom(r.Context()); !ok || !perms.Admin() {
		return apperrors.Forbidden("Only administrators can set system themes")
	}
	return nil
}

func (a *themeAPI) setSystemDefault(w http.ResponseWriter, r *http.Request) {
	if err := requireThemeAdmin(r); err != nil {
		server.WriteError(w, err)
		return
	}
	a.setSystemSlot(w, r, "is_system_default")
}

func (a *themeAPI) setSystemDark(w http.ResponseWriter, r *http.Request) {
	if err := requireThemeAdmin(r); err != nil {
		server.WriteError(w, err)
		return
	}
	a.setSystemSlot(w, r, "is_system_dark")
}

// setSystemSlot atomically moves the slot flag to the target theme
// (SetSystemDefaultThemeCommand / SetSystemDarkThemeCommand).
func (a *themeAPI) setSystemSlot(w http.ResponseWriter, r *http.Request, column string) {
	t, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Theme{}).Where(column+" = ?", true).Update(column, false).Error; err != nil {
			return err
		}
		// The two slots are mutually exclusive per theme: a row cannot be both
		// the light default and the dark theme, otherwise the light slot would
		// ship dark tokens and the shell would render a dark page with light
		// components.
		cleared := "is_system_dark"
		if column == "is_system_dark" {
			cleared = "is_system_default"
		}
		if err := tx.Model(t).Update(cleared, false).Error; err != nil {
			return err
		}
		return tx.Model(t).Update(column, true).Error
	}); err != nil {
		server.WriteError(w, apperrors.Internal("Could not set system theme: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"id": t.ID, "result": "success"})
}

func (a *themeAPI) unsetSystemDefault(w http.ResponseWriter, r *http.Request) {
	if err := requireThemeAdmin(r); err != nil {
		server.WriteError(w, err)
		return
	}
	a.deps.DB.WithContext(r.Context()).Model(&models.Theme{}).
		Where("is_system_default = ?", true).Update("is_system_default", false)
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": "success"})
}

func (a *themeAPI) unsetSystemDark(w http.ResponseWriter, r *http.Request) {
	if err := requireThemeAdmin(r); err != nil {
		server.WriteError(w, err)
		return
	}
	a.deps.DB.WithContext(r.Context()).Model(&models.Theme{}).
		Where("is_system_dark = ?", true).Update("is_system_dark", false)
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": "success"})
}

// themeBaseTokens is the built-in fallback used when no system theme is set
// (THEME_DEFAULT / THEME_DARK in superset/config.py, aligned with the brand
// tokens of frontend/src/theme.ts).
func themeBaseTokens(dark bool) map[string]any {
	return map[string]any{
		"colorPrimary":   "#1b5fd9",
		"colorInfo":      "#22c3e6",
		"colorSuccess":   "#28a06b",
		"colorWarning":   "#e8a417",
		"colorError":     "#dc3f4e",
		"colorBgBase":    map[bool]string{true: "#0f1d2e", false: "#ffffff"}[dark],
		"colorBgLayout":  map[bool]string{true: "#0a1420", false: "#f4f5f7"}[dark],
		"colorText":      map[bool]string{true: "#e8f0f6", false: "#232629"}[dark],
		"colorBorder":    map[bool]string{true: "#1f3550", false: "#e6e8eb"}[dark],
		"borderRadius":   6,
		"fontSize":       13,
		"fontFamily":     "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", Roboto, sans-serif",
		"fontFamilyCode": "\"JetBrains Mono\", \"SFMono-Regular\", Consolas, monospace",
	}
}

// system returns the resolved system theme slice used to bootstrap the page
// (GET /theme/system, get_theme_bootstrap_data): the system default and dark
// themes with slot-consistent algorithms, plus the administration flag.
func (a *themeAPI) system(w http.ResponseWriter, r *http.Request) {
	var def, dark models.Theme
	db := a.deps.DB.WithContext(r.Context())
	// A theme flagged as the dark slot must never populate the light slot: its
	// token set is a dark palette and would paint a dark shell while antd keeps
	// the light algorithm. Legacy rows carrying both flags fall back to the
	// built-in base until an admin re-picks the default.
	_ = db.Where("is_system_default = ? AND is_system_dark = ?", true, false).First(&def).Error
	_ = db.Where("is_system_dark = ?", true).First(&dark).Error

	slot := func(t *models.Theme, name string, fallback algorithmFallback) map[string]any {
		if t.ID == 0 {
			// No admin-chosen theme in this slot: serve the built-in base.
			return map[string]any{
				"theme_name": name,
				"json_data": map[string]any{
					"token":     themeBaseTokens(fallback == algorithmDark),
					"algorithm": string(fallback),
				},
				"is_system": true,
			}
		}
		v := themeJSONValue(t.JSONData)
		if m, ok := v.(map[string]any); ok {
			m["algorithm"] = slotAlgorithmFor(string(fallback), m)
		}
		out := themeJSON(t)
		out["algorithm"] = slotAlgorithmFor(string(fallback), v)
		return out
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"default":                     slot(&def, "智观默认", algorithmLight),
		"dark":                        slot(&dark, "智观暗色", algorithmDark),
		"defaultMode":                 "light",
		"enableUiThemeAdministration": true,
	}})
}

type algorithmFallback string

const (
	algorithmLight algorithmFallback = "default"
	algorithmDark  algorithmFallback = "dark"
)

// exportThemes downloads the theme catalogue as JSON (superset exports YAML
// inside a zip; a single JSON document keeps the Go replica dependency-free
// while preserving round-tripping through import).
func (a *themeAPI) exportThemes(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Theme{})
	if p.Search != "" {
		q = q.Where("theme_name ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	var themes []models.Theme
	if err := q.Order("theme_name asc").Find(&themes).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	payload := make([]map[string]any, 0, len(themes))
	for i := range themes {
		payload = append(payload, map[string]any{
			"theme_name":        themes[i].ThemeName,
			"json_data":         themeJSONValue(themes[i].JSONData),
			"is_system":         themes[i].IsSystem,
			"is_system_default": themes[i].IsSystemDefault,
			"is_system_dark":    themes[i].IsSystemDark,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"themes": payload})
}

// importThemes upserts themes by name (ImportThemesCommand semantics).
func (a *themeAPI) importThemes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Themes    []themePayload `json:"themes"`
		Overwrite bool           `json:"overwrite"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	if len(body.Themes) == 0 {
		server.WriteError(w, apperrors.BadRequest("No valid theme files found in the uploaded zip"))
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	created, updated := 0, 0
	for _, item := range body.Themes {
		if item.ThemeName == "" {
			continue
		}
		data := jsonString(item.JSONData)
		if err := validateThemeJSON(data); err != nil {
			server.WriteError(w, err)
			return
		}
		var existing models.Theme
		err := db.Where("theme_name = ?", item.ThemeName).First(&existing).Error
		switch {
		case err == gorm.ErrRecordNotFound:
			t := models.Theme{
				UUID: uuid.NewString(), ThemeName: item.ThemeName, JSONData: data,
				IsSystem: item.IsDefault != nil && *item.IsDefault,
			}
			if err := db.Create(&t).Error; err != nil {
				server.WriteError(w, apperrors.Conflict("Could not create theme: %s", err.Error()))
				return
			}
			created++
		case err != nil:
			server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
			return
		case body.Overwrite:
			updates := map[string]any{"json_data": data}
			if item.IsDefault != nil {
				updates["is_system_default"] = *item.IsDefault
			}
			if item.IsDark != nil {
				updates["is_system_dark"] = *item.IsDark
			}
			if err := db.Model(&existing).Updates(updates).Error; err != nil {
				server.WriteError(w, apperrors.Conflict("Could not update theme: %s", err.Error()))
				return
			}
			updated++
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Theme imported successfully", "created": created, "updated": updated,
	})
}

// bulkDelete removes several themes at once (DELETE /api/v1/theme/ with ids).
func (a *themeAPI) bulkDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int `json:"ids"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	if len(body.IDs) == 0 {
		server.WriteError(w, apperrors.BadRequest("ids is required"))
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var count int64
	db.Model(&models.Theme{}).Where("is_system = ? AND id IN ?", false, body.IDs).Count(&count)
	if err := db.Where("is_system = ? AND id IN ?", false, body.IDs).Delete(&models.Theme{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK", "deleted": count})
}
