package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/ai/registry"
	"superset/internal/models"
	"superset/internal/security"
)

// Plugin validation errors. They are sentinels so the API layer can map them to
// a 400 with the operator's language instead of echoing a Go error string.
var (
	// ErrPluginNotFound is returned for a code with no row in the plugin table.
	ErrPluginNotFound = errors.New("capability plugin not found")
	// ErrPluginBuiltin guards built-in capabilities: their behaviour lives in
	// code, so built-ins are fully read-only — they cannot be edited or deleted.
	ErrPluginBuiltin = errors.New("内置能力不可修改或删除")
	// ErrPluginCodeTaken is returned when a create reuses an existing code.
	ErrPluginCodeTaken = errors.New("capability code already exists")
	// ErrPluginInvalid wraps every field-level validation failure.
	ErrPluginInvalid = errors.New("invalid capability plugin")
)

// codePattern is deliberately narrow: the code becomes an OpenAI function name
// and a key in agent allow-lists, so dots, dashes and spaces are not allowed.
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// SkillPlugin is the settings-page view of one skill definition. It merges the
// DB row with the in-memory registry so the UI can show built-ins and custom
// skills in one list.
type SkillPlugin struct {
	Code         string   `json:"code"`
	Title        string   `json:"title"`
	Domain       string   `json:"domain"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	InputSchema  string   `json:"input_schema"`
	ToolCodes    []string `json:"tool_codes"`
	CostTier     string   `json:"cost_tier"`
	Enabled      bool     `json:"enabled"`
	Builtin      bool     `json:"builtin"`
	// Available reports whether an executor is wired. A built-in skill whose
	// code is not registered anymore, or a custom skill without instructions,
	// stays visible but cannot be bound to an agent.
	Available bool `json:"available"`
}

// SkillPluginInput is the create/update body for a skill plugin.
type SkillPluginInput struct {
	Code         string   `json:"code"`
	Title        string   `json:"title"`
	Domain       string   `json:"domain"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	InputSchema  string   `json:"input_schema"`
	ToolCodes    []string `json:"tool_codes"`
	CostTier     string   `json:"cost_tier"`
	Enabled      *bool    `json:"enabled"`
}

// ToolPlugin is the settings-page view of one tool definition.
type ToolPlugin struct {
	Code         string `json:"code"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	InputSchema  string `json:"input_schema"`
	SideEffect   string `json:"side_effect"`
	Kind         string `json:"kind"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	Headers      string `json:"headers"`
	BodyTemplate string `json:"body_template"`
	TimeoutSec   int    `json:"timeout_sec"`
	Enabled      bool   `json:"enabled"`
	Builtin      bool   `json:"builtin"`
	Available    bool   `json:"available"`
}

// ToolPluginInput is the create/update body for a tool plugin.
type ToolPluginInput struct {
	Code         string `json:"code"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	InputSchema  string `json:"input_schema"`
	SideEffect   string `json:"side_effect"`
	Kind         string `json:"kind"`
	Method       string `json:"method"`
	URL          string `json:"url"`
	Headers      string `json:"headers"`
	BodyTemplate string `json:"body_template"`
	TimeoutSec   int    `json:"timeout_sec"`
	Enabled      *bool  `json:"enabled"`
}

// pluginState is the mutable half of the plugin layer: which codes came from the
// settings page (so they can be un-registered again) and which built-ins the
// operator switched off.
type pluginState struct {
	mu             sync.RWMutex
	customSkills   map[string]bool
	customTools    map[string]bool
	disabledSkills map[string]bool
	disabledTools  map[string]bool
}

func newPluginState() *pluginState {
	return &pluginState{
		customSkills:   map[string]bool{},
		customTools:    map[string]bool{},
		disabledSkills: map[string]bool{},
		disabledTools:  map[string]bool{},
	}
}

/* ------------------------------------------------------------------- reads */

// ListSkillPlugins returns every skill: DB rows merged with the registry, so an
// entry registered in code but not yet seeded still shows up.
func (s *Service) ListSkillPlugins(ctx context.Context) ([]SkillPlugin, error) {
	rows, err := s.skillRows(ctx)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]*models.AISkillRegistry, len(rows))
	out := make([]SkillPlugin, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		byCode[row.Code] = row
		out = append(out, s.skillPluginOf(row))
	}
	// Registry entries without a row (fresh deployment, seeding failed): show
	// them as built-ins so the list is never misleadingly short.
	for _, sk := range s.Skills.List() {
		if _, ok := byCode[sk.Name]; ok {
			continue
		}
		out = append(out, SkillPlugin{
			Code: sk.Name, Title: sk.Title, Domain: sk.Domain,
			Description: sk.Description, CostTier: orDefaultStr(sk.CostHint.Tier, "standard"),
			Enabled: s.skillVisible(sk.Name), Builtin: true, Available: sk.Executor != nil,
		})
	}
	sortSkills(out)
	return out, nil
}

// ListToolPlugins returns every tool, merged the same way.
func (s *Service) ListToolPlugins(ctx context.Context) ([]ToolPlugin, error) {
	rows, err := s.toolRows(ctx)
	if err != nil {
		return nil, err
	}
	byCode := make(map[string]*models.AIToolRegistry, len(rows))
	out := make([]ToolPlugin, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		byCode[row.Code] = row
		out = append(out, s.toolPluginOf(row))
	}
	for _, t := range s.Tools.List() {
		if _, ok := byCode[t.Name]; ok {
			continue
		}
		out = append(out, ToolPlugin{
			Code: t.Name, Title: t.Title, Description: t.Description,
			SideEffect: t.SideEffect.String(), Kind: "builtin",
			TimeoutSec: 30, Enabled: s.toolVisible(t.Name), Builtin: true,
			Available: t.Execute != nil,
		})
	}
	sortTools(out)
	return out, nil
}

// skillPluginOf renders one row plus its live wiring state.
func (s *Service) skillPluginOf(row *models.AISkillRegistry) SkillPlugin {
	sk, registered := s.Skills.Get(row.Code)
	available := false
	if registered {
		available = sk.Executor != nil
	}
	out := SkillPlugin{
		Code: row.Code, Title: row.Title, Domain: orDefaultStr(row.Domain, "general"),
		Description: row.Description, Instructions: row.Instructions,
		InputSchema: orDefaultStr(row.InputSchema, "{}"),
		ToolCodes:   DecodeCodes(row.ToolCodes),
		CostTier:    orDefaultStr(row.CostTier, "standard"),
		Enabled:     row.Enabled, Builtin: row.IsBuiltin, Available: available,
	}
	return out
}

// toolPluginOf renders one tool row plus its live wiring state.
func (s *Service) toolPluginOf(row *models.AIToolRegistry) ToolPlugin {
	t, registered := s.Tools.Get(row.Code)
	available := registered && t.Execute != nil
	return ToolPlugin{
		Code: row.Code, Title: row.Title, Description: row.Description,
		InputSchema: orDefaultStr(row.InputSchema, "{}"),
		SideEffect:  orDefaultStr(row.SideEffect, "read"),
		Kind:        orDefaultStr(row.Kind, "builtin"),
		Method:      row.Method, URL: row.URL, Headers: orDefaultStr(row.Headers, "{}"),
		BodyTemplate: row.BodyTemplate, TimeoutSec: row.TimeoutSec,
		Enabled: row.Enabled, Builtin: row.IsBuiltin, Available: available,
	}
}

/* -------------------------------------------------------------- mutations */

// CreateSkillPlugin inserts a declarative skill and applies it immediately.
func (s *Service) CreateSkillPlugin(ctx context.Context, in *SkillPluginInput) (*SkillPlugin, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	code := strings.TrimSpace(in.Code)
	if !codePattern.MatchString(code) {
		return nil, fmt.Errorf("%w: 编码只能是小写字母开头、由小写字母/数字/下划线组成，2-64 位", ErrPluginInvalid)
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&models.AISkillRegistry{}).Where("code = ?", code).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 || hasSkill(s, code) {
		return nil, fmt.Errorf("%w: %s", ErrPluginCodeTaken, code)
	}
	row, err := s.skillRowFromInput(nil, in)
	if err != nil {
		return nil, err
	}
	row.Code = code
	row.IsBuiltin = false
	if err := s.DB.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	if err := s.syncPlugins(ctx); err != nil {
		return nil, err
	}
	out := s.skillPluginOf(row)
	return &out, nil
}

// UpdateSkillPlugin edits a skill. A built-in only honours `enabled` (and the
// description fields) because its behaviour is compiled in.
func (s *Service) UpdateSkillPlugin(ctx context.Context, code string, in *SkillPluginInput) (*SkillPlugin, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	row, err := s.skillRow(ctx, code)
	if err != nil {
		return nil, err
	}
	updated, err := s.skillRowFromInput(row, in)
	if err != nil {
		return nil, err
	}
	if row.IsBuiltin {
		// Built-ins are code-owned and fully read-only: their behaviour lives in
		// the binary, so an edit here could only ever diverge from it.
		return nil, fmt.Errorf("%w: %s", ErrPluginBuiltin, code)
	}
	if err := s.DB.WithContext(ctx).Model(&models.AISkillRegistry{}).Where("id = ?", row.ID).
		Updates(map[string]any{
			"title":        updated.Title,
			"domain":       updated.Domain,
			"description":  updated.Description,
			"instructions": updated.Instructions,
			"input_schema": updated.InputSchema,
			"tool_codes":   updated.ToolCodes,
			"cost_tier":    updated.CostTier,
			"enabled":      updated.Enabled,
		}).Error; err != nil {
		return nil, err
	}
	if err := s.syncPlugins(ctx); err != nil {
		return nil, err
	}
	saved, err := s.skillRow(ctx, code)
	if err != nil {
		return nil, err
	}
	out := s.skillPluginOf(saved)
	return &out, nil
}

// DeleteSkillPlugin removes a custom skill. Built-ins are refused: deleting the
// row would resurrect it on the next seed and silently re-enable a capability an
// operator thought was gone.
func (s *Service) DeleteSkillPlugin(ctx context.Context, code string) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	row, err := s.skillRow(ctx, code)
	if err != nil {
		return err
	}
	if row.IsBuiltin {
		return fmt.Errorf("%w: %s", ErrPluginBuiltin, code)
	}
	if err := s.DB.WithContext(ctx).Delete(&models.AISkillRegistry{}, row.ID).Error; err != nil {
		return err
	}
	return s.syncPlugins(ctx)
}

// CreateToolPlugin inserts an HTTP tool and applies it immediately.
func (s *Service) CreateToolPlugin(ctx context.Context, in *ToolPluginInput) (*ToolPlugin, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	code := strings.TrimSpace(in.Code)
	if !codePattern.MatchString(code) {
		return nil, fmt.Errorf("%w: 编码只能是小写字母开头、由小写字母/数字/下划线组成，2-64 位", ErrPluginInvalid)
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&models.AIToolRegistry{}).Where("code = ?", code).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 || hasTool(s, code) {
		return nil, fmt.Errorf("%w: %s", ErrPluginCodeTaken, code)
	}
	row, err := s.toolRowFromInput(nil, in)
	if err != nil {
		return nil, err
	}
	row.Code = code
	row.IsBuiltin = false
	if err := s.DB.WithContext(ctx).Create(row).Error; err != nil {
		return nil, err
	}
	if err := s.syncPlugins(ctx); err != nil {
		return nil, err
	}
	out := s.toolPluginOf(row)
	return &out, nil
}

// UpdateToolPlugin edits an HTTP tool.
func (s *Service) UpdateToolPlugin(ctx context.Context, code string, in *ToolPluginInput) (*ToolPlugin, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	row, err := s.toolRow(ctx, code)
	if err != nil {
		return nil, err
	}
	updated, err := s.toolRowFromInput(row, in)
	if err != nil {
		return nil, err
	}
	if row.IsBuiltin {
		// Same rule as skills: a built-in tool is defined in code and must not be
		// edited through the settings page.
		return nil, fmt.Errorf("%w: %s", ErrPluginBuiltin, code)
	}
	if err := s.DB.WithContext(ctx).Model(&models.AIToolRegistry{}).Where("id = ?", row.ID).
		Updates(map[string]any{
			"title":         updated.Title,
			"description":   updated.Description,
			"input_schema":  updated.InputSchema,
			"side_effect":   updated.SideEffect,
			"kind":          updated.Kind,
			"method":        updated.Method,
			"url":           updated.URL,
			"headers":       updated.Headers,
			"body_template": updated.BodyTemplate,
			"timeout_sec":   updated.TimeoutSec,
			"enabled":       updated.Enabled,
		}).Error; err != nil {
		return nil, err
	}
	if err := s.syncPlugins(ctx); err != nil {
		return nil, err
	}
	saved, err := s.toolRow(ctx, code)
	if err != nil {
		return nil, err
	}
	out := s.toolPluginOf(saved)
	return &out, nil
}

// DeleteToolPlugin removes a custom tool.
func (s *Service) DeleteToolPlugin(ctx context.Context, code string) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	row, err := s.toolRow(ctx, code)
	if err != nil {
		return err
	}
	if row.IsBuiltin {
		return fmt.Errorf("%w: %s", ErrPluginBuiltin, code)
	}
	if err := s.DB.WithContext(ctx).Delete(&models.AIToolRegistry{}, row.ID).Error; err != nil {
		return err
	}
	return s.syncPlugins(ctx)
}

/* ------------------------------------------------------------- row helpers */

func (s *Service) skillRows(ctx context.Context) ([]models.AISkillRegistry, error) {
	if s.DB == nil {
		return nil, nil
	}
	var rows []models.AISkillRegistry
	if err := s.DB.WithContext(ctx).Order("code asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) toolRows(ctx context.Context) ([]models.AIToolRegistry, error) {
	if s.DB == nil {
		return nil, nil
	}
	var rows []models.AIToolRegistry
	if err := s.DB.WithContext(ctx).Order("code asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) skillRow(ctx context.Context, code string) (*models.AISkillRegistry, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var row models.AISkillRegistry
	err := s.DB.WithContext(ctx).Where("code = ?", code).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, code)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Service) toolRow(ctx context.Context, code string) (*models.AIToolRegistry, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var row models.AIToolRegistry
	err := s.DB.WithContext(ctx).Where("code = ?", code).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, code)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// skillRowFromInput validates an input and renders the row to persist. A nil
// `row` means "create": defaults are filled in.
func (s *Service) skillRowFromInput(row *models.AISkillRegistry, in *SkillPluginInput) (*models.AISkillRegistry, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: 请填写技能名称", ErrPluginInvalid)
	}
	instructions := strings.TrimSpace(in.Instructions)
	schema, err := normaliseSchema(in.InputSchema)
	if err != nil {
		return nil, err
	}
	tier := strings.TrimSpace(in.CostTier)
	switch tier {
	case "":
		tier = "standard"
	case "cheap", "standard", "heavy":
	default:
		return nil, fmt.Errorf("%w: 成本档位只能是 cheap/standard/heavy", ErrPluginInvalid)
	}
	builtin := row != nil && row.IsBuiltin
	if !builtin && instructions == "" {
		// A declarative skill without instructions would register an executor
		// that always fails; reject it at the door instead.
		return nil, fmt.Errorf("%w: 请填写技能指令（用于驱动模型的提示词）", ErrPluginInvalid)
	}
	out := &models.AISkillRegistry{}
	if row != nil {
		*out = *row
	}
	out.Title = title
	out.Domain = orDefaultStr(strings.TrimSpace(in.Domain), "general")
	out.Description = strings.TrimSpace(in.Description)
	out.Instructions = instructions
	out.InputSchema = schema
	// params is a legacy jsonb column; PostgreSQL rejects '' as json, so an
	// insert without an explicit value failed with 22P02.
	if strings.TrimSpace(out.Params) == "" {
		out.Params = "{}"
	}
	out.ToolCodes = EncodeCodes(in.ToolCodes)
	out.CostTier = tier
	if in.Enabled != nil {
		out.Enabled = *in.Enabled
	} else if row == nil {
		out.Enabled = true
	}
	return out, nil
}

// toolRowFromInput validates a tool input and renders the row to persist.
func (s *Service) toolRowFromInput(row *models.AIToolRegistry, in *ToolPluginInput) (*models.AIToolRegistry, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: 请填写工具名称", ErrPluginInvalid)
	}
	schema, err := normaliseSchema(in.InputSchema)
	if err != nil {
		return nil, err
	}
	side := strings.TrimSpace(in.SideEffect)
	switch side {
	case "":
		side = "read"
	case "read", "write", "destructive":
	default:
		return nil, fmt.Errorf("%w: 副作用只能是 read/write/destructive", ErrPluginInvalid)
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "http"
	}
	if kind != "http" && kind != "builtin" {
		return nil, fmt.Errorf("%w: 工具类型只能是 http 或 builtin", ErrPluginInvalid)
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	target := strings.TrimSpace(in.URL)
	if kind == "http" {
		if method == "" {
			method = http.MethodGet
		}
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return nil, fmt.Errorf("%w: 请求方法只能是 GET/POST/PUT/PATCH/DELETE", ErrPluginInvalid)
		}
		parsed, err := url.Parse(target)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("%w: 请填写合法的调用地址", ErrPluginInvalid)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("%w: 调用地址必须是 http(s)", ErrPluginInvalid)
		}
	}
	headers := strings.TrimSpace(in.Headers)
	if headers == "" {
		headers = "{}"
	}
	var headerMap map[string]string
	if err := json.Unmarshal([]byte(headers), &headerMap); err != nil {
		return nil, fmt.Errorf("%w: 请求头必须是 JSON 对象", ErrPluginInvalid)
	}
	timeout := in.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 300 {
		timeout = 300
	}
	out := &models.AIToolRegistry{}
	if row != nil {
		*out = *row
	}
	out.Title = title
	out.Description = strings.TrimSpace(in.Description)
	out.InputSchema = schema
	out.SideEffect = side
	out.Kind = kind
	out.Method = method
	out.URL = target
	out.Headers = headers
	out.BodyTemplate = strings.TrimSpace(in.BodyTemplate)
	out.TimeoutSec = timeout
	if in.Enabled != nil {
		out.Enabled = *in.Enabled
	} else if row == nil {
		out.Enabled = true
	}
	return out, nil
}

// normaliseSchema validates an optional JSON Schema and yields the stored form.
func normaliseSchema(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return `{"type":"object","properties":{}}`, nil
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return "", fmt.Errorf("%w: 参数 Schema 必须是合法的 JSON 对象", ErrPluginInvalid)
	}
	return text, nil
}

/* ------------------------------------------------------------------- sync */

// seedPlugins makes sure every capability compiled into the binary has a row, so
// the settings page can list and disable it. A row that already exists is kept,
// except that a built-in row seeded before its schema/description existed is
// repaired in place: the settings page renders those columns, and an empty
// placeholder makes a real capability look like it has no substance.
func (s *Service) seedPlugins(ctx context.Context) error {
	if s.DB == nil {
		return nil
	}
	skills, err := s.skillRows(ctx)
	if err != nil {
		return err
	}
	skillByCode := make(map[string]*models.AISkillRegistry, len(skills))
	for i := range skills {
		skillByCode[skills[i].Code] = &skills[i]
	}
	for _, sk := range s.Skills.List() {
		schema := schemaJSON(sk.InputSchema)
		if row, ok := skillByCode[sk.Name]; ok {
			// Custom rows are entirely operator-owned; only built-ins are code-owned.
			if !row.IsBuiltin {
				continue
			}
			patch := map[string]any{}
			if isPlaceholderSchema(row.InputSchema) {
				patch["input_schema"] = schema
			}
			if strings.TrimSpace(row.Description) == "" {
				patch["description"] = sk.Description
			}
			if strings.TrimSpace(row.Title) == "" {
				patch["title"] = sk.Title
			}
			if strings.TrimSpace(row.Domain) == "" {
				patch["domain"] = orDefaultStr(sk.Domain, "general")
			}
			if len(patch) > 0 {
				if err := s.DB.WithContext(ctx).Model(&models.AISkillRegistry{}).
					Where("id = ?", row.ID).Updates(patch).Error; err != nil {
					s.Log.Warn("failed to repair built-in skill row", zap.String("code", sk.Name))
				}
			}
			continue
		}
		row := &models.AISkillRegistry{
			Code: sk.Name, Title: sk.Title, Domain: orDefaultStr(sk.Domain, "general"),
			Description: sk.Description, CostTier: orDefaultStr(sk.CostHint.Tier, "standard"),
			// Every jsonb column needs valid JSON: PostgreSQL rejects '' with
			// 22P02, which is exactly how the first seeding attempt failed and
			// left the list looking populated (it was served from the registry).
			Params:      "{}",
			InputSchema: schema,
			ToolCodes:   "[]",
			Enabled:     true, IsBuiltin: true,
		}
		if err := s.DB.WithContext(ctx).Create(row).Error; err != nil {
			// A concurrent seed can lose the unique-index race; that is fine.
			s.Log.Warn("failed to seed built-in skill row", zap.String("code", sk.Name))
		}
	}

	tools, err := s.toolRows(ctx)
	if err != nil {
		return err
	}
	toolByCode := make(map[string]*models.AIToolRegistry, len(tools))
	for i := range tools {
		toolByCode[tools[i].Code] = &tools[i]
	}
	for _, t := range s.Tools.List() {
		schema := schemaJSON(t.InputSchema)
		if row, ok := toolByCode[t.Name]; ok {
			if !row.IsBuiltin {
				continue
			}
			patch := map[string]any{}
			if isPlaceholderSchema(row.InputSchema) {
				patch["input_schema"] = schema
			}
			if strings.TrimSpace(row.Description) == "" {
				patch["description"] = t.Description
			}
			if strings.TrimSpace(row.Title) == "" {
				patch["title"] = t.Title
			}
			if len(patch) > 0 {
				if err := s.DB.WithContext(ctx).Model(&models.AIToolRegistry{}).
					Where("id = ?", row.ID).Updates(patch).Error; err != nil {
					s.Log.Warn("failed to repair built-in tool row", zap.String("code", t.Name))
				}
			}
			continue
		}
		row := &models.AIToolRegistry{
			Code: t.Name, Title: t.Title, Description: t.Description,
			SideEffect: t.SideEffect.String(), Kind: "builtin",
			InputSchema: schema,
			Headers:     "{}", TimeoutSec: 30, Enabled: true, IsBuiltin: true,
		}
		if err := s.DB.WithContext(ctx).Create(row).Error; err != nil {
			s.Log.Warn("failed to seed built-in tool row", zap.String("code", t.Name))
		}
	}
	return nil
}

// schemaJSON renders a registry input schema into its stored string form,
// falling back to the empty object schema when a descriptor omits one.
func schemaJSON(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return emptyObjectSchema
	}
	return text
}

// emptyObjectSchema is the placeholder a capability without a declared schema
// gets. It is also the marker used to decide whether a seeded row needs repair.
const emptyObjectSchema = `{"type":"object","properties":{}}`

// isPlaceholderSchema reports whether a stored schema is missing or is only the
// empty placeholder, i.e. it carries no real parameter definition to show.
func isPlaceholderSchema(stored string) bool {
	text := strings.TrimSpace(stored)
	if text == "" || text == "{}" {
		return true
	}
	var probe struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		// Never overwrite something we cannot parse: an operator's hand-written
		// schema must survive even if it is not strictly valid JSON.
		return false
	}
	if probe.Type != "" && probe.Type != "object" {
		return false
	}
	return len(probe.Properties) == 0
}

// syncPlugins applies the DB rows to the live registries: built-ins get their
// enabled flag, custom rows get registered (or removed) with their executor.
func (s *Service) syncPlugins(ctx context.Context) error {
	if s.plugins == nil {
		return nil
	}
	skills, err := s.skillRows(ctx)
	if err != nil {
		return err
	}
	tools, err := s.toolRows(ctx)
	if err != nil {
		return err
	}

	s.plugins.mu.Lock()
	defer s.plugins.mu.Unlock()

	disabledSkills := map[string]bool{}
	desiredSkills := map[string]bool{}
	for i := range skills {
		row := &skills[i]
		if !row.Enabled {
			disabledSkills[row.Code] = true
		}
		if row.IsBuiltin {
			continue
		}
		desiredSkills[row.Code] = true
		s.Skills.Register(s.declarativeSkill(row))
	}
	// Drop custom skills that were deleted while the process was running.
	for code := range s.plugins.customSkills {
		if !desiredSkills[code] {
			s.Skills.Remove(code)
		}
	}
	s.plugins.customSkills = desiredSkills

	disabledTools := map[string]bool{}
	desiredTools := map[string]bool{}
	for i := range tools {
		row := &tools[i]
		if !row.Enabled {
			disabledTools[row.Code] = true
		}
		if row.IsBuiltin {
			continue
		}
		desiredTools[row.Code] = true
		s.Tools.Register(s.pluginTool(row))
	}
	for code := range s.plugins.customTools {
		if !desiredTools[code] {
			s.Tools.Remove(code)
		}
	}
	s.plugins.customTools = desiredTools

	s.plugins.disabledSkills = disabledSkills
	s.plugins.disabledTools = disabledTools
	return nil
}

// declarativeSkill turns a prompt-defined skill row into a registry entry whose
// executor asks the model to follow the stored instructions.
func (s *Service) declarativeSkill(row *models.AISkillRegistry) *registry.Skill {
	sk := &registry.Skill{
		Name:        row.Code,
		Title:       orDefaultStr(row.Title, row.Code),
		Description: orDefaultStr(row.Description, row.Title),
		Domain:      orDefaultStr(row.Domain, "general"),
		InputSchema: json.RawMessage(orDefaultStr(row.InputSchema, `{"type":"object","properties":{}}`)),
		Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResAIAgent}},
		CostHint:    registry.CostHint{Tier: orDefaultStr(row.CostTier, "standard"), Tokens: 1500, LatencyMS: 3000},
	}
	sk.Executor = s.declarativeSkillExecutor(row)
	return sk
}

// declarativeSkillExecutor renders the stored instructions as a system prompt.
func (s *Service) declarativeSkillExecutor(row *models.AISkillRegistry) registry.SkillExecutor {
	instructions := strings.TrimSpace(row.Instructions)
	toolCodes := DecodeCodes(row.ToolCodes)
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		if instructions == "" {
			return nil, fmt.Errorf("%w: 技能 %s 未配置指令", registry.ErrNotImplemented, row.Code)
		}
		args := strings.TrimSpace(string(in.Args))
		if args == "" {
			args = "{}"
		}
		var b strings.Builder
		b.WriteString("调用参数（JSON）：\n")
		b.WriteString(args)
		if in.Context.DatasetID != nil {
			b.WriteString(fmt.Sprintf("\n当前数据集 ID：%d", *in.Context.DatasetID))
		}
		if in.Context.Scope != "" {
			b.WriteString("\n调用场景：" + in.Context.Scope)
		}
		if len(toolCodes) > 0 {
			b.WriteString("\n可用工具（供参考，需要数据时请在结论中说明）：" + strings.Join(toolCodes, "、"))
		}
		content, tokens, err := s.complete(ctx, in.UserID, instructions, b.String())
		if err != nil {
			return nil, err
		}
		return &registry.Result{
			Output:     map[string]any{"markdown": content},
			Markdown:   content,
			TokenUsage: tokens,
		}, nil
	}
}

// pluginTool turns an HTTP tool row into a registry entry with a transport.
func (s *Service) pluginTool(row *models.AIToolRegistry) *registry.Tool {
	side := registry.Read
	switch row.SideEffect {
	case "write":
		side = registry.Write
	case "destructive":
		side = registry.Destructive
	}
	t := &registry.Tool{
		Name:        row.Code,
		Title:       orDefaultStr(row.Title, row.Code),
		Description: orDefaultStr(row.Description, row.Title),
		InputSchema: json.RawMessage(orDefaultStr(row.InputSchema, `{"type":"object","properties":{}}`)),
		SideEffect:  side,
		Permissions: []registry.Permission{{Action: security.ActionRead, Resource: security.ResAIAgent}},
	}
	if row.Kind == "http" && strings.TrimSpace(row.URL) != "" {
		t.Execute = httpToolExecutor(row)
	}
	return t
}

// httpToolExecutor calls the configured endpoint, substituting `{{name}}`
// placeholders in the URL and body with the call arguments.
func httpToolExecutor(row *models.AIToolRegistry) registry.ToolExecutor {
	timeout := time.Duration(orDefaultInt(row.TimeoutSec, 30)) * time.Second
	client := &http.Client{Timeout: timeout}
	method := orDefaultStr(strings.ToUpper(row.Method), http.MethodGet)
	rawURL := strings.TrimSpace(row.URL)
	bodyTemplate := strings.TrimSpace(row.BodyTemplate)
	var headers map[string]string
	if err := json.Unmarshal([]byte(orDefaultStr(row.Headers, "{}")), &headers); err != nil {
		headers = nil
	}
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		start := time.Now()
		vars := map[string]any{}
		if len(args) > 0 {
			_ = json.Unmarshal(args, &vars)
		}
		target := substituteVars(rawURL, vars, func(v any) string {
			return url.QueryEscape(inlineJSONBare(v))
		})
		body := substituteVars(bodyTemplate, vars, inlineJSON)

		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, reader)
		if err != nil {
			return nil, fmt.Errorf("构建请求失败: %w", err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for key, value := range headers {
			req.Header.Set(key, substituteVars(value, vars, inlineJSONBare))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("调用工具失败: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		if readErr != nil {
			return nil, fmt.Errorf("读取工具响应失败: %w", readErr)
		}
		var parsed any
		if json.Unmarshal(raw, &parsed) != nil {
			parsed = string(raw)
		}
		return &registry.ToolResult{
			Output:     map[string]any{"status": resp.StatusCode, "body": parsed},
			SideEffect: registry.Read,
			DurationMS: int(time.Since(start).Milliseconds()),
		}, nil
	}
}

// substituteVars replaces `{{name}}` placeholders using the supplied escaper.
// Unknown names are left untouched so a typo surfaces in the response instead
// of silently producing an empty URL.
func substituteVars(template string, vars map[string]any, escape func(any) string) string {
	if template == "" || len(vars) == 0 {
		return template
	}
	out := template
	for key, value := range vars {
		out = strings.ReplaceAll(out, "{{"+key+"}}", escape(value))
	}
	return out
}

// inlineJSON renders a value as a JSON token usable inside a quoted template.
func inlineJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	token := string(b)
	if len(token) >= 2 && token[0] == '"' {
		return token[1 : len(token)-1]
	}
	return token
}

// inlineJSONBare renders a value without JSON escaping (headers, plain text).
func inlineJSONBare(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

/* ----------------------------------------------------------------- helpers */

// skillVisible additionally honours the operator's disable switch stored in the
// plugin table; without this a disabled built-in would still be advertised.
func (s *Service) skillEnabledByPlugin(name string) bool {
	if s.plugins == nil {
		return true
	}
	s.plugins.mu.RLock()
	defer s.plugins.mu.RUnlock()
	return !s.plugins.disabledSkills[name]
}

// toolEnabledByPlugin reports whether a tool is switched on in the plugin table.
func (s *Service) toolEnabledByPlugin(name string) bool {
	if s.plugins == nil {
		return true
	}
	s.plugins.mu.RLock()
	defer s.plugins.mu.RUnlock()
	return !s.plugins.disabledTools[name]
}

func hasSkill(s *Service, code string) bool {
	_, ok := s.Skills.Get(code)
	return ok
}

func hasTool(s *Service, code string) bool {
	_, ok := s.Tools.Get(code)
	return ok
}

func orDefaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// sortPlugins orders skill/tool lists by code so both tabs stay stable.
func sortSkills(rows []SkillPlugin) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
}

func sortTools(rows []ToolPlugin) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
}
