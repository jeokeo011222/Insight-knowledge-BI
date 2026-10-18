package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"superset/internal/connectors"
	"superset/internal/models"
)

// ObserveTimeout bounds a single alert SQL run.
const ObserveTimeout = 120 * time.Second

// Observation is the first row of an alert query.
type Observation struct {
	Columns []string
	Row     map[string]any
	// Value is the first column of the first row (nil when the query returned
	// no rows).
	Value any
	// FloatValue is Value coerced to a number, when possible.
	FloatValue *float64
	// RowJSON is the JSON encoded first row, stored in last_value_row_json.
	RowJSON []byte
}

// Observe executes the alert SQL and returns the first row.
func Observe(ctx context.Context, db *gorm.DB, s *models.ReportSchedule) (*Observation, error) {
	if s == nil {
		return nil, fmt.Errorf("reports: schedule is nil")
	}
	if s.DatabaseID == nil || *s.DatabaseID == 0 {
		return nil, fmt.Errorf("reports: alert %q has no database_id", s.Name)
	}
	sqlText := strings.TrimSpace(s.SQL)
	if sqlText == "" {
		return nil, fmt.Errorf("reports: alert %q has no SQL", s.Name)
	}

	var database models.Database
	if err := db.WithContext(ctx).First(&database, *s.DatabaseID).Error; err != nil {
		return nil, fmt.Errorf("reports: load database %d: %w", *s.DatabaseID, err)
	}
	if strings.TrimSpace(database.SQLAlchemyURI) == "" {
		return nil, fmt.Errorf("reports: database %q has no sqlalchemy_uri", database.DatabaseName)
	}

	pool, _, err := connectors.Open(database.SQLAlchemyURI)
	if err != nil {
		return nil, fmt.Errorf("reports: open database %q: %w", database.DatabaseName, err)
	}
	defer pool.Close()

	queryCtx, cancel := context.WithTimeout(ctx, ObserveTimeout)
	defer cancel()

	rows, err := pool.QueryContext(queryCtx, sqlText)
	if err != nil {
		return nil, fmt.Errorf("reports: alert query failed: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reports: read alert columns: %w", err)
	}
	obs := &Observation{Columns: cols, Row: map[string]any{}}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reports: alert query failed: %w", err)
		}
		obs.RowJSON = []byte("{}")
		return obs, nil
	}

	holders := make([]any, len(cols))
	scan := make([]any, len(cols))
	for i := range holders {
		scan[i] = &holders[i]
	}
	if err := rows.Scan(scan...); err != nil {
		return nil, fmt.Errorf("reports: scan alert row: %w", err)
	}
	for i, name := range cols {
		obs.Row[name] = normalizeValue(holders[i])
	}
	if len(cols) > 0 {
		obs.Value = obs.Row[cols[0]]
	}
	if f, ok := toFloat(obs.Value); ok {
		obs.FloatValue = &f
	}
	raw, err := json.Marshal(obs.Row)
	if err != nil {
		return nil, fmt.Errorf("reports: encode alert row: %w", err)
	}
	obs.RowJSON = raw
	return obs, nil
}

// normalizeValue converts driver specific types into JSON friendly values.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return t
	}
}
