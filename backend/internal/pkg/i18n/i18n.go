// Package i18n provides JSON language-pack based translations, mirroring the
// superset/translations/*/LC_MESSAGES structure for the backend.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Translator resolves messages for a locale.
type Translator struct {
	mu       sync.RWMutex
	packs    map[string]map[string]string
	fallback string
}

// New builds a translator from embedded locale packs.
func New(fs embed.FS, fallback string) (*Translator, error) {
	t := &Translator{packs: map[string]map[string]string{}, fallback: fallback}
	entries, err := fs.ReadDir("locales")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		locale := e.Name()
		if len(locale) > 5 {
			locale = locale[:len(locale)-5]
		}
		raw, err := fs.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, err
		}
		var pack map[string]string
		if err := json.Unmarshal(raw, &pack); err != nil {
			return nil, err
		}
		t.packs[locale] = pack
	}
	return t, nil
}

// Translate returns the localized message for key, falling back to the
// fallback locale and finally to the key itself.
func (t *Translator) Translate(locale, key string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if pack, ok := t.packs[normalize(locale)]; ok {
		if msg, ok := pack[key]; ok {
			return msg
		}
	}
	if pack, ok := t.packs[t.fallback]; ok {
		if msg, ok := pack[key]; ok {
			return msg
		}
	}
	return key
}

// Lookup reports whether key exists for locale (or its fallback locale).
func (t *Translator) Lookup(locale, key string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if pack, ok := t.packs[normalize(locale)]; ok {
		if msg, ok := pack[key]; ok {
			return msg, true
		}
	}
	if pack, ok := t.packs[t.fallback]; ok {
		if msg, ok := pack[key]; ok {
			return msg, true
		}
	}
	return "", false
}

// Keys returns the sorted key set of a locale pack, used by tests and tooling.
func (t *Translator) Keys(locale string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	pack, ok := t.packs[normalize(locale)]
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(pack))
	for k := range pack {
		keys = append(keys, k)
	}
	return keys
}

// defaultTranslator is set once at bootstrap so handlers can translate without
// threading the translator through every call site.
var defaultTranslator atomic.Pointer[Translator]

// SetDefault registers the process-wide translator.
func SetDefault(t *Translator) { defaultTranslator.Store(t) }

// Default returns the process-wide translator (may be nil).
func Default() *Translator { return defaultTranslator.Load() }

// T translates key for lang, formatting with args when they are provided.
// Unknown keys are returned verbatim so callers can detect missing entries.
func T(lang, key string, args ...any) string {
	if tr := Default(); tr != nil {
		if msg, ok := tr.Lookup(lang, key); ok {
			return format(msg, args)
		}
	}
	return format(key, args)
}

// Error translates key for lang and falls back to def when the key is missing,
// so clients never receive a bare key in an error message.
func Error(lang, key, def string, args ...any) string {
	if tr := Default(); tr != nil {
		if msg, ok := tr.Lookup(lang, key); ok {
			return format(msg, args)
		}
	}
	return format(def, args)
}

func format(msg string, args []any) string {
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// normalize maps BCP-47-ish tags ("zh-CN", "en_US") to pack names ("zh", "en").
func normalize(tag string) string {
	tag = strings.TrimSpace(strings.ToLower(tag))
	if tag == "" {
		return ""
	}
	tag = strings.NewReplacer("_", "-").Replace(tag)
	if idx := strings.IndexByte(tag, '-'); idx > 0 {
		return tag[:idx]
	}
	return tag
}

// ---------------------------------------------------------------------------
// Request language plumbing
// ---------------------------------------------------------------------------

type langKey struct{}

// Middleware resolves the request language from `?lang=` or `Accept-Language`
// and stores it in the request context.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := resolveLang(r)
		ctx := context.WithValue(r.Context(), langKey{}, lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext returns the language stored by Middleware, defaulting to English.
func FromContext(ctx context.Context) string {
	if lang, ok := ctx.Value(langKey{}).(string); ok && lang != "" {
		return lang
	}
	return "en"
}

// Request returns the language for r, honouring `?lang=` first (handy for
// clients that cannot set headers, e.g. iframe embeds).
func Request(r *http.Request) string {
	if r == nil {
		return "en"
	}
	if lang := normalize(r.URL.Query().Get("lang")); lang != "" {
		return lang
	}
	return FromContext(r.Context())
}

func resolveLang(r *http.Request) string {
	if lang := normalize(r.URL.Query().Get("lang")); lang != "" {
		return lang
	}
	header := r.Header.Get("Accept-Language")
	if header == "" {
		return "en"
	}
	// Accept-Language: zh-CN,zh;q=0.9,en;q=0.8 -> first supported tag wins.
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		switch lang := normalize(tag); lang {
		case "zh", "en":
			return lang
		}
	}
	return "en"
}
