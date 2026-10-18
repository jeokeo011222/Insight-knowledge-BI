package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/screens"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterScreens mounts /api/v1/screen/* (the data-visualisation wall) and
// the unauthenticated /api/v1/public/screen/* read path.
//
// A screen is deliberately *not* a dashboard row: it is an absolutely
// positioned 1920×1080 canvas that scales to whatever the TV/iframe gives it.
// Keeping the two models apart means neither layout engine has to compromise.
func RegisterScreens(r chi.Router, deps *Deps) {
	a := &screenAPI{deps: deps}

	r.Route("/screen", func(sr chi.Router) {
		sr.Use(security.RequireUser)

		// Static paths first: chi would otherwise route "templates" into {pk}.
		sr.Get("/templates", a.listTemplates)
		sr.Get("/templates/categories", a.templateCategories)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).
			Post("/templates/{key}", a.createFromTemplate)

		sr.With(security.RequirePermission(security.ActionRead, security.ResScreen)).Get("/", a.list)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).Post("/", a.create)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).Post("/import", a.importScreen)

		sr.With(security.RequirePermission(security.ActionRead, security.ResScreen)).Get("/{pk}", a.get)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).Put("/{pk}", a.update)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).Put("/{pk}/config", a.saveConfig)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).Post("/{pk}/duplicate", a.duplicate)
		sr.With(security.RequirePermission(security.ActionDelete, security.ResScreen)).Delete("/{pk}", a.remove)

		sr.With(security.RequirePermission(security.ActionShare, security.ResScreen)).Post("/{pk}/publish", a.publish)
		sr.With(security.RequirePermission(security.ActionShare, security.ResScreen)).Delete("/{pk}/publish", a.unpublish)
		sr.With(security.RequirePermission(security.ActionRead, security.ResScreen)).Get("/{pk}/embed", a.embedSnippet)

		sr.With(security.RequirePermission(security.ActionRead, security.ResScreen)).Get("/{pk}/export", a.export)
		sr.With(security.RequirePermission(security.ActionRead, security.ResScreen)).Get("/{pk}/revisions", a.listRevisions)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResScreen)).
			Post("/{pk}/revisions/{rev}/restore", a.restoreRevision)
	})

	// Public read path: a TV, a kiosk or a customer's iframe has no account.
	r.Route("/public/screen", func(pr chi.Router) {
		pr.Get("/{token}", a.publicGet)
		pr.Post("/{token}/unlock", a.publicUnlock)
		pr.Post("/{token}/view", a.publicView)
	})
}

type screenAPI struct{ deps *Deps }

// Payloads -----------------------------------------------------------------

type screenPayload struct {
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	ScaleMode   string          `json:"scale_mode"`
	Theme       string          `json:"theme"`
	Background  string          `json:"background"`
	Config      json.RawMessage `json:"config"`
	RefreshSec  int             `json:"refresh_sec"`
	Xinchuang   *bool           `json:"xinchuang"`
	Published   *bool           `json:"published"`
}

type screenUpdatePayload struct {
	Name        *string         `json:"name"`
	Slug        *string         `json:"slug"`
	Description *string         `json:"description"`
	Category    *string         `json:"category"`
	ScaleMode   *string         `json:"scale_mode"`
	Theme       *string         `json:"theme"`
	Background  *string         `json:"background"`
	Config      json.RawMessage `json:"config"`
	RefreshSec  *int            `json:"refresh_sec"`
	Xinchuang   *bool           `json:"xinchuang"`
	Published   *bool           `json:"published"`
}

type screenPublishPayload struct {
	Enabled        bool   `json:"enabled"`
	Password       string `json:"password"`
	ClearPassword  bool   `json:"clear_password"`
	ExpiresInHours int    `json:"expires_in_hours"`
	AllowExport    *bool  `json:"allow_export"`
	Regenerate     bool   `json:"regenerate"`
}

// JSON shaping -------------------------------------------------------------

func screenJSON(s *models.Screen, withConfig bool) map[string]any {
	out := map[string]any{
		"id": s.ID, "uuid": s.UUID, "name": s.Name, "slug": s.Slug,
		"description": s.Description, "category": s.Category,
		"width": s.Width, "height": s.Height, "scale_mode": s.ScaleMode,
		"theme": s.Theme, "background": s.Background,
		"thumbnail": s.Thumbnail, "published": s.Published,
		"template_key": s.TemplateKey, "owner_id": s.OwnerID,
		"refresh_sec": s.RefreshSec, "config_rev": s.ConfigRev,
		"xinchuang":      s.Xinchuang,
		"public_enabled": s.PublicEnabled, "public_token": s.PublicToken,
		"public_expires_at": s.PublicExpiresAt, "allow_export": s.AllowExport,
		"has_password": s.PublicPassword != "",
		"view_count":   s.ViewCount,
		"created_on":   s.CreatedOn, "changed_on": s.ChangedOn,
	}
	if withConfig {
		out["config"] = jsonField(s.ConfigJSON)
	}
	return out
}

// Config validation --------------------------------------------------------

// screenComponentTypes is the palette the client may store. A closed set keeps
// a hand-crafted payload from smuggling an unknown renderer into a public page.
var screenComponentTypes = map[string]bool{
	"kpi": true, "chart": true, "table": true, "text": true, "marquee": true,
	"clock": true, "image": true, "video": true, "iframe": true, "border": true,
	"decoration": true, "progress": true, "shape": true, "richtext": true,
	"filter": true, "group": true, "qrcode": true, "list": true,
}

const (
	screenMaxConfigBytes  = 4 << 20 // 4 MiB
	screenMaxComponents   = 600
	screenMinCanvasWidth  = 320
	screenMaxCanvasWidth  = 7680
	screenMinCanvasHeight = 180
	screenMaxCanvasHeight = 4320
)

type screenConfigDoc struct {
	Version     int                     `json:"version"`
	Canvas      map[string]any          `json:"canvas"`
	Components  []screenConfigComponent `json:"components"`
	Filters     []map[string]any        `json:"filters"`
	Interaction map[string]any          `json:"interaction"`
}

type screenConfigComponent struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	X        float64                 `json:"x"`
	Y        float64                 `json:"y"`
	W        float64                 `json:"w"`
	H        float64                 `json:"h"`
	Z        float64                 `json:"z"`
	Locked   bool                    `json:"locked"`
	Visible  *bool                   `json:"visible"`
	Props    map[string]any          `json:"props"`
	Style    map[string]any          `json:"style"`
	Children []screenConfigComponent `json:"children"`
}

// validateScreenConfig enforces shape, palette membership and URL safety.
// The canvas is user-authored HTML-adjacent content that a *public* page may
// render, so the checks happen on write, not on read.
func validateScreenConfig(raw []byte) (screenConfigDoc, error) {
	var doc screenConfigDoc
	if len(raw) == 0 {
		return doc, apperrors.BadRequest("config is required")
	}
	if len(raw) > screenMaxConfigBytes {
		return doc, apperrors.BadRequest("config exceeds %d bytes", screenMaxConfigBytes)
	}
	lower := bytes.ToLower(raw)
	if bytes.Contains(lower, []byte("<script")) ||
		bytes.Contains(lower, []byte("javascript:")) ||
		bytes.Contains(lower, []byte("vbscript:")) {
		return doc, apperrors.BadRequest("config contains a forbidden script payload")
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, apperrors.BadRequest("config is not a valid JSON object")
	}
	if doc.Version <= 0 {
		doc.Version = 1
	}
	if w, ok := numberFrom(doc.Canvas["width"]); ok && (w < screenMinCanvasWidth || w > screenMaxCanvasWidth) {
		return doc, apperrors.BadRequest("canvas width must be between %d and %d", screenMinCanvasWidth, screenMaxCanvasWidth)
	}
	if h, ok := numberFrom(doc.Canvas["height"]); ok && (h < screenMinCanvasHeight || h > screenMaxCanvasHeight) {
		return doc, apperrors.BadRequest("canvas height must be between %d and %d", screenMinCanvasHeight, screenMaxCanvasHeight)
	}
	if len(doc.Components) > screenMaxComponents {
		return doc, apperrors.BadRequest("a screen may hold at most %d components", screenMaxComponents)
	}
	seen := make(map[string]bool, len(doc.Components))
	for i := range doc.Components {
		if err := validateComponent(&doc.Components[i], seen); err != nil {
			return doc, err
		}
	}
	return doc, nil
}

func validateComponent(c *screenConfigComponent, seen map[string]bool) error {
	if strings.TrimSpace(c.ID) == "" {
		return apperrors.BadRequest("every component needs an id")
	}
	if seen[c.ID] {
		return apperrors.BadRequest("duplicate component id %q", c.ID)
	}
	seen[c.ID] = true
	if !screenComponentTypes[c.Type] {
		return apperrors.BadRequest("unknown component type %q", c.Type)
	}
	if c.W < 0 || c.H < 0 {
		return apperrors.BadRequest("component %q has a negative size", c.ID)
	}
	for _, child := range c.Children {
		child := child
		if err := validateComponent(&child, seen); err != nil {
			return err
		}
	}
	return nil
}

func numberFrom(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// CRUD ---------------------------------------------------------------------

func (a *screenAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Screen{})
	if p.Search != "" {
		q = q.Where("name ILIKE ? OR description ILIKE ?", "%"+p.Search+"%", "%"+p.Search+"%")
	}
	if cat := server.QueryStr(r, "category"); cat != "" {
		q = q.Where("category = ?", cat)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "changed_on desc", func(s *models.Screen) any {
		return screenJSON(s, false)
	})
}

func (a *screenAPI) find(r *http.Request) (*models.Screen, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var s models.Screen
	if err := a.deps.DB.WithContext(r.Context()).First(&s, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Screen not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &s, nil
}

func (a *screenAPI) get(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": screenJSON(s, true)})
}

func (a *screenAPI) create(w http.ResponseWriter, r *http.Request) {
	var p screenPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		server.WriteError(w, apperrors.BadRequest("name is required"))
		return
	}
	cfg := p.Config
	if len(cfg) == 0 {
		cfg = defaultScreenConfig(p.Width, p.Height, p.Theme, p.Background)
	}
	doc, err := validateScreenConfig(cfg)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	s := models.Screen{
		UUID: uuid.NewString(), Name: p.Name, Slug: p.Slug,
		Description: p.Description, Category: p.Category,
		Width: firstPositive(p.Width, 1920), Height: firstPositive(p.Height, 1080),
		ScaleMode: orDefault(p.ScaleMode, "fit"), Theme: orDefault(p.Theme, "tech-blue"),
		Background: p.Background, ConfigJSON: datatypes.JSON(cfg),
		RefreshSec: p.RefreshSec, OwnerID: userIDOf(r),
		Published:   p.Published != nil && *p.Published,
		Xinchuang:   p.Xinchuang != nil && *p.Xinchuang,
		AllowExport: true, ConfigRev: 1,
	}
	applyCanvasMeta(&s, doc)
	if err := a.deps.DB.WithContext(r.Context()).Create(&s).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create screen: %s", err.Error()))
		return
	}
	a.saveRevision(r, &s, "create")
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": screenJSON(&s, true)})
}

func (a *screenAPI) update(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p screenUpdatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.Name != nil {
		if strings.TrimSpace(*p.Name) == "" {
			server.WriteError(w, apperrors.BadRequest("name must not be empty"))
			return
		}
		updates["name"] = *p.Name
	}
	if p.Slug != nil {
		updates["slug"] = *p.Slug
	}
	if p.Description != nil {
		updates["description"] = *p.Description
	}
	if p.Category != nil {
		updates["category"] = *p.Category
	}
	if p.ScaleMode != nil {
		updates["scale_mode"] = *p.ScaleMode
	}
	if p.Theme != nil {
		updates["theme"] = *p.Theme
	}
	if p.Background != nil {
		updates["background"] = *p.Background
	}
	if p.RefreshSec != nil {
		updates["refresh_sec"] = *p.RefreshSec
	}
	if p.Xinchuang != nil {
		updates["xinchuang"] = *p.Xinchuang
	}
	if p.Published != nil {
		updates["published"] = *p.Published
	}
	if len(p.Config) > 0 {
		doc, err := validateScreenConfig(p.Config)
		if err != nil {
			server.WriteError(w, err)
			return
		}
		updates["config_json"] = datatypes.JSON(p.Config)
		applyUpdatesFromCanvas(updates, doc)
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(s).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update screen: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(s, s.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": screenJSON(s, true)})
}

// saveConfig is the editor's autosave target: one atomic write, one revision.
func (a *screenAPI) saveConfig(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		Config json.RawMessage `json:"config"`
		Note   string          `json:"note"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	doc, err := validateScreenConfig(body.Config)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{
		"config_json":   datatypes.JSON(body.Config),
		"config_rev":    s.ConfigRev + 1,
		"changed_by_fk": intPtrNil(userIDOf(r)),
	}
	applyUpdatesFromCanvas(updates, doc)
	if err := a.deps.DB.WithContext(r.Context()).Model(s).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not save screen: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(s, s.ID).Error
	a.saveRevision(r, s, orDefault(body.Note, "save"))
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": screenJSON(s, false), "config_rev": s.ConfigRev,
		"message": "Screen saved",
	})
}

func applyCanvasMeta(s *models.Screen, doc screenConfigDoc) {
	if w, ok := numberFrom(doc.Canvas["width"]); ok && w > 0 {
		s.Width = int(w)
	}
	if h, ok := numberFrom(doc.Canvas["height"]); ok && h > 0 {
		s.Height = int(h)
	}
	if v, ok := doc.Canvas["scaleMode"].(string); ok && v != "" {
		s.ScaleMode = v
	}
	if v, ok := doc.Canvas["theme"].(string); ok && v != "" {
		s.Theme = v
	}
	if v, ok := doc.Canvas["background"].(string); ok && v != "" {
		s.Background = v
	}
	if v, ok := doc.Interaction["refreshSec"]; ok {
		if n, ok := numberFrom(v); ok {
			s.RefreshSec = int(n)
		}
	}
}

func applyUpdatesFromCanvas(updates map[string]any, doc screenConfigDoc) {
	if w, ok := numberFrom(doc.Canvas["width"]); ok && w > 0 {
		updates["width"] = int(w)
	}
	if h, ok := numberFrom(doc.Canvas["height"]); ok && h > 0 {
		updates["height"] = int(h)
	}
	if v, ok := doc.Canvas["scaleMode"].(string); ok && v != "" {
		updates["scale_mode"] = v
	}
	if v, ok := doc.Canvas["theme"].(string); ok && v != "" {
		updates["theme"] = v
	}
	if v, ok := doc.Canvas["background"].(string); ok && v != "" {
		updates["background"] = v
	}
	if v, ok := doc.Interaction["refreshSec"]; ok {
		if n, ok := numberFrom(v); ok {
			updates["refresh_sec"] = int(n)
		}
	}
}

func (a *screenAPI) duplicate(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = security.DecodeBody(r, &body)
	copy := models.Screen{
		UUID: uuid.NewString(), Name: orDefault(body.Name, s.Name+" 副本"),
		Slug: "", Description: s.Description, Category: s.Category,
		Width: s.Width, Height: s.Height, ScaleMode: s.ScaleMode,
		Theme: s.Theme, Background: s.Background, ConfigJSON: s.ConfigJSON,
		Thumbnail: s.Thumbnail, TemplateKey: s.TemplateKey, OwnerID: userIDOf(r),
		RefreshSec: s.RefreshSec, Xinchuang: s.Xinchuang,
		AllowExport: s.AllowExport, ConfigRev: 1,
	}
	if err := a.deps.DB.WithContext(r.Context()).Create(&copy).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not duplicate screen: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": screenJSON(&copy, true)})
}

func (a *screenAPI) remove(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	if err := db.Delete(s).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	db.Where("screen_id = ?", s.ID).Delete(&models.ScreenRevision{})
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Templates ----------------------------------------------------------------

func (a *screenAPI) listTemplates(w http.ResponseWriter, r *http.Request) {
	items := screens.All()
	category := server.QueryStr(r, "category")
	out := make([]any, 0, len(items))
	for i := range items {
		if category != "" && items[i].Category != category {
			continue
		}
		out = append(out, items[i])
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": out, "count": len(out), "categories": screens.Categories(),
	})
}

func (a *screenAPI) templateCategories(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": screens.Categories()})
}

func (a *screenAPI) createFromTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	tpl, ok := screens.Get(key)
	if !ok {
		server.WriteError(w, apperrors.NotFound("Template not found"))
		return
	}
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	_ = security.DecodeBody(r, &body)
	raw, err := json.Marshal(tpl.Config)
	if err != nil {
		server.WriteError(w, apperrors.Internal("Could not encode template"))
		return
	}
	doc, err := validateScreenConfig(raw)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	s := models.Screen{
		UUID: uuid.NewString(), Name: orDefault(body.Name, tpl.Name), Slug: body.Slug,
		Description: tpl.Description, Category: tpl.Category,
		Width: tpl.Width, Height: tpl.Height, ScaleMode: "fit",
		Theme: tpl.Theme, Background: tpl.Background, ConfigJSON: datatypes.JSON(raw),
		TemplateKey: tpl.Key, OwnerID: userIDOf(r), AllowExport: true, ConfigRev: 1,
	}
	applyCanvasMeta(&s, doc)
	if err := a.deps.DB.WithContext(r.Context()).Create(&s).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create screen: %s", err.Error()))
		return
	}
	a.saveRevision(r, &s, "template:"+tpl.Key)
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": screenJSON(&s, true)})
}

// Publishing / sharing -----------------------------------------------------

func newPublicToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *screenAPI) publish(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p screenPublishPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"public_enabled": p.Enabled}
	if p.Enabled {
		if s.PublicToken == "" || p.Regenerate {
			token, err := newPublicToken()
			if err != nil {
				server.WriteError(w, apperrors.Internal("Could not create share token"))
				return
			}
			updates["public_token"] = token
			s.PublicToken = token
		}
		switch {
		case p.ClearPassword:
			updates["public_password"] = ""
		case p.Password != "":
			hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
			if err != nil {
				server.WriteError(w, apperrors.Internal("Could not hash password"))
				return
			}
			updates["public_password"] = string(hash)
		}
		if p.ExpiresInHours > 0 {
			exp := time.Now().Add(time.Duration(p.ExpiresInHours) * time.Hour)
			updates["public_expires_at"] = exp
		} else if p.ExpiresInHours == 0 {
			updates["public_expires_at"] = nil
		}
		if p.AllowExport != nil {
			updates["allow_export"] = *p.AllowExport
		}
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(s).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update sharing: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(s, s.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": screenJSON(s, false),
		"url":    "/screen/public/" + s.PublicToken,
	})
}

func (a *screenAPI) unpublish(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(s).Updates(map[string]any{
		"public_enabled": false, "public_token": "", "public_password": "",
		"public_expires_at": nil,
	}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (a *screenAPI) embedSnippet(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if !s.PublicEnabled || s.PublicToken == "" {
		server.WriteError(w, apperrors.BadRequest("Enable public sharing before embedding"))
		return
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	url := fmt.Sprintf("%s://%s/screen/public/%s?embed=1", scheme, host, s.PublicToken)
	snippet := fmt.Sprintf(
		`<iframe src="%s" width="1920" height="1080" frameborder="0" allowfullscreen style="border:0;width:100%%;height:100%%"></iframe>`,
		url,
	)
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"url": url, "iframe": snippet,
		"expires_at": s.PublicExpiresAt, "has_password": s.PublicPassword != "",
	})
}

// Export / import ----------------------------------------------------------

func (a *screenAPI) export(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	payload := map[string]any{
		"format": "superset-go/screen", "version": 1,
		"name": s.Name, "description": s.Description, "category": s.Category,
		"width": s.Width, "height": s.Height, "scale_mode": s.ScaleMode,
		"theme": s.Theme, "background": s.Background,
		"refresh_sec": s.RefreshSec, "xinchuang": s.Xinchuang,
		"config": jsonField(s.ConfigJSON),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", safeFilename(s.Name)+".screen.json"))
	_ = json.NewEncoder(w).Encode(payload)
}

func (a *screenAPI) importScreen(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Format      string          `json:"format"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Category    string          `json:"category"`
		Width       int             `json:"width"`
		Height      int             `json:"height"`
		ScaleMode   string          `json:"scale_mode"`
		Theme       string          `json:"theme"`
		Background  string          `json:"background"`
		RefreshSec  int             `json:"refresh_sec"`
		Xinchuang   bool            `json:"xinchuang"`
		Config      json.RawMessage `json:"config"`
	}
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.Format != "" && p.Format != "superset-go/screen" {
		server.WriteError(w, apperrors.BadRequest("Unsupported screen bundle format"))
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "Imported screen"
	}
	doc, err := validateScreenConfig(p.Config)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	s := models.Screen{
		UUID: uuid.NewString(), Name: p.Name, Description: p.Description,
		Category: p.Category, Width: firstPositive(p.Width, 1920), Height: firstPositive(p.Height, 1080),
		ScaleMode: orDefault(p.ScaleMode, "fit"), Theme: orDefault(p.Theme, "tech-blue"),
		Background: p.Background, ConfigJSON: datatypes.JSON(p.Config),
		RefreshSec: p.RefreshSec, Xinchuang: p.Xinchuang,
		OwnerID: userIDOf(r), AllowExport: true, ConfigRev: 1,
	}
	applyCanvasMeta(&s, doc)
	if err := a.deps.DB.WithContext(r.Context()).Create(&s).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not import screen: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": screenJSON(&s, true)})
}

// Revisions ----------------------------------------------------------------

const screenRevisionKeep = 20

func (a *screenAPI) saveRevision(r *http.Request, s *models.Screen, note string) {
	db := a.deps.DB.WithContext(r.Context())
	rev := models.ScreenRevision{
		ScreenID: s.ID, Rev: s.ConfigRev, ConfigJSON: s.ConfigJSON,
		SavedBy: userIDOf(r), Note: note,
	}
	if err := db.Create(&rev).Error; err != nil {
		return
	}
	var old []models.ScreenRevision
	if err := db.Where("screen_id = ?", s.ID).Order("rev desc").Offset(screenRevisionKeep).
		Find(&old).Error; err == nil && len(old) > 0 {
		ids := make([]int, 0, len(old))
		for i := range old {
			ids = append(ids, old[i].ID)
		}
		db.Delete(&models.ScreenRevision{}, ids)
	}
}

func (a *screenAPI) listRevisions(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var rows []models.ScreenRevision
	if err := a.deps.DB.WithContext(r.Context()).
		Where("screen_id = ?", s.ID).Order("rev desc").Limit(screenRevisionKeep).
		Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, map[string]any{
			"id": rows[i].ID, "rev": rows[i].Rev, "note": rows[i].Note,
			"saved_by": rows[i].SavedBy, "created_on": rows[i].CreatedOn,
			"size": len(rows[i].ConfigJSON),
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (a *screenAPI) restoreRevision(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	rev, err := server.URLParamInt(r, "rev")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var target models.ScreenRevision
	if err := a.deps.DB.WithContext(r.Context()).
		Where("screen_id = ? AND rev = ?", s.ID, rev).First(&target).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Revision not found"))
		return
	}
	doc, err := validateScreenConfig(target.ConfigJSON)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{
		"config_json": target.ConfigJSON, "config_rev": s.ConfigRev + 1,
		"changed_by_fk": intPtrNil(userIDOf(r)),
	}
	applyUpdatesFromCanvas(updates, doc)
	if err := a.deps.DB.WithContext(r.Context()).Model(s).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not restore revision: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(s, s.ID).Error
	a.saveRevision(r, s, fmt.Sprintf("restore:r%d", rev))
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": screenJSON(s, true)})
}

// Public read path ---------------------------------------------------------

func (a *screenAPI) findByToken(r *http.Request) (*models.Screen, error) {
	token := chi.URLParam(r, "token")
	if token == "" {
		return nil, apperrors.NotFound("Screen not found")
	}
	var s models.Screen
	if err := a.deps.DB.WithContext(r.Context()).
		Where("public_token = ? AND public_enabled = ?", token, true).First(&s).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Share link is invalid or has been disabled")
		}
		return nil, apperrors.Internal("Database error")
	}
	if s.PublicExpiresAt != nil && time.Now().After(*s.PublicExpiresAt) {
		return nil, apperrors.Forbidden("Share link has expired")
	}
	return &s, nil
}

// publicGet returns either the canvas (no password) or just enough metadata to
// render the password gate. The config is never sent before authentication.
func (a *screenAPI) publicGet(w http.ResponseWriter, r *http.Request) {
	s, err := a.findByToken(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if s.PublicPassword != "" {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
			"requires_password": true, "name": s.Name,
			"description": s.Description, "cover": s.Background,
			"expires_at": s.PublicExpiresAt,
		}})
		return
	}
	a.publicPayload(w, s)
}

func (a *screenAPI) publicUnlock(w http.ResponseWriter, r *http.Request) {
	s, err := a.findByToken(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	if s.PublicPassword == "" {
		a.publicPayload(w, s)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(s.PublicPassword), []byte(body.Password)) != nil {
		server.WriteError(w, apperrors.Forbidden("Incorrect password"))
		return
	}
	a.publicPayload(w, s)
}

func (a *screenAPI) publicPayload(w http.ResponseWriter, s *models.Screen) {
	go func(id int) {
		_ = a.deps.DB.Model(&models.Screen{}).Where("id = ?", id).
			UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error
	}(s.ID)
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"id": s.ID, "name": s.Name, "description": s.Description,
		"width": s.Width, "height": s.Height, "scale_mode": s.ScaleMode,
		"theme": s.Theme, "background": s.Background,
		"config": jsonField(s.ConfigJSON), "refresh_sec": s.RefreshSec,
		"allow_export": s.AllowExport, "expires_at": s.PublicExpiresAt,
	}})
}

// publicView lets an embed keep counting views without refetching the canvas.
func (a *screenAPI) publicView(w http.ResponseWriter, r *http.Request) {
	s, err := a.findByToken(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	go func(id int) {
		_ = a.deps.DB.Model(&models.Screen{}).Where("id = ?", id).
			UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error
	}(s.ID)
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// helpers ------------------------------------------------------------------

func firstPositive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func defaultScreenConfig(width, height int, theme, background string) []byte {
	w, h := firstPositive(width, 1920), firstPositive(height, 1080)
	doc := map[string]any{
		"version": 1,
		"canvas": map[string]any{
			"width": w, "height": h, "gridSize": 8, "showGrid": true,
			"scaleMode": "fit", "theme": orDefault(theme, "tech-blue"),
			"background": orDefault(background,
				"radial-gradient(120% 90% at 50% 0%, #0d3563 0%, #071a33 52%, #030b17 100%)"),
		},
		"components": []any{},
		"filters":    []any{},
		"interaction": map[string]any{
			"refreshSec": 0, "watermark": false, "showToolbar": false,
		},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

// safeFilename strips characters that would break a Content-Disposition header.
func safeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", "\"", "_", "\n", "_", "\r", "_", ";", "_",
	)
	out := strings.TrimSpace(replacer.Replace(name))
	if out == "" {
		return "screen"
	}
	return out
}
