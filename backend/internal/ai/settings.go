package ai

import (
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"sync/atomic"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/config"
	"superset/internal/models"
)

// Settings is the runtime-editable global Agent switch exposed by
// /api/v1/ai/settings. Everything else (providers, agents, guardrails) lives in
// the ai_provider / ai_agent tables; only the master switch stays a single
// key-value so an operator can always reach the config page and turn the plane
// back on.
type Settings struct {
	Enabled bool `json:"enabled"`
}

// settingsFromConfig seeds the live settings from the startup configuration.
func settingsFromConfig(cfg *config.Config) *Settings {
	return &Settings{Enabled: cfg.AIAssistantEnabled}
}

// settingsStore holds the live settings behind an atomic pointer so the gateway
// and the Agent runtime read them without locking while the settings endpoint
// swaps in a new instance.
type settingsStore struct{ ptr atomic.Pointer[Settings] }

func newSettingsStore(initial *Settings) *settingsStore {
	store := &settingsStore{}
	store.ptr.Store(initial)
	return store
}

// Load returns the current settings; never nil.
func (s *settingsStore) Load() *Settings {
	if current := s.ptr.Load(); current != nil {
		return current
	}
	return &Settings{}
}

func (s *settingsStore) Store(next *Settings) { s.ptr.Store(next) }

/* ----------------------------------------------------------- persistence */

// aiSettingsKey identifies the settings row inside the shared `keyvalue` table,
// following the same convention as the dashboard filter-state store.
const aiSettingsKey = "ai_agent_settings"

// settingsEnvelope guards against a crc32 id collision landing on a row that
// belongs to another feature.
type settingsEnvelope struct {
	Key      string   `json:"key"`
	Settings Settings `json:"settings"`
}

func aiSettingsRowID() int {
	return int(crc32.ChecksumIEEE([]byte(aiSettingsKey)) & 0x7fffffff)
}

// EnabledForDB reports the Agent master switch as an operator would see it:
// the persisted override wins, otherwise the deploy-time default is used. It
// exists so the system console can render AI status without building a whole
// Service (and without touching the LLM gateway).
func EnabledForDB(ctx context.Context, db *gorm.DB, fallback bool) bool {
	if db == nil {
		return fallback
	}
	persisted, found, err := loadSettings(ctx, db)
	if err != nil || !found {
		return fallback
	}
	return persisted.Enabled
}

// loadSettings reads the persisted settings. The bool reports whether a row was
// found; errors are returned so the caller can log them and fall back to config.
func loadSettings(ctx context.Context, db *gorm.DB) (*Settings, bool, error) {
	if db == nil {
		return nil, false, nil
	}
	var row models.KeyValue
	err := db.WithContext(ctx).First(&row, aiSettingsRowID()).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var envelope settingsEnvelope
	if err := json.Unmarshal([]byte(row.Value), &envelope); err != nil {
		return nil, false, nil
	}
	if envelope.Key != aiSettingsKey {
		return nil, false, nil
	}
	return &envelope.Settings, true, nil
}

func saveSettings(ctx context.Context, db *gorm.DB, settings *Settings) error {
	if db == nil {
		return errors.New("database unavailable")
	}
	raw, err := json.Marshal(settingsEnvelope{Key: aiSettingsKey, Settings: *settings})
	if err != nil {
		return err
	}
	row := models.KeyValue{ID: aiSettingsRowID(), Value: string(raw)}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&row).Error
}
