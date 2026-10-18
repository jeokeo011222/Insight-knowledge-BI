package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/connectors"
	"superset/internal/models"
)

// ExternalDatabaseName is the fixed name of the managed database that stores
// every file/API dataset. Keeping it stable lets EnsureExternalDatabase find
// the same row again after a restart.
const ExternalDatabaseName = "外部数据 (External Data)"

// identifierUnsafe matches everything that cannot appear in a SQL identifier.
// Letters and digits of any script are kept, so an imported Chinese header
// survives as 科目 instead of collapsing to a positional placeholder; spaces,
// punctuation and symbols collapse into a single underscore.
var identifierUnsafe = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

// maxIdentifierRunes bounds a generated identifier so it stays well within the
// engine limits even after the sheet suffix is appended.
const maxIdentifierRunes = 50

// UploadDir resolves (and creates) the directory holding uploaded source files
// and the managed SQLite database. DataDir (SUPERSET_HOME) may start with ~.
func UploadDir(cfg *config.Config) (string, error) {
	return subDir(cfg, "uploads")
}

// SourceDir resolves (and creates) the directory that keeps a copy of every
// uploaded source file so a file dataset can be re-parsed on refresh.
func SourceDir(cfg *config.Config) (string, error) {
	return subDir(cfg, filepath.Join("uploads", "source"))
}

func subDir(cfg *config.Config, sub string) (string, error) {
	base := expandHome(cfg.DataDir)
	if base == "" {
		base = "."
	}
	dir := filepath.Join(base, filepath.FromSlash(sub))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create the directory %q: %w", dir, err)
	}
	return dir, nil
}

// expandHome resolves a leading ~ to the current user's home directory.
func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// EnsureExternalDatabase returns the managed database row, creating the row and
// its SQLite file on first use. It inserts directly (mirroring the governance
// seed) so no Database write permission is required.
func EnsureExternalDatabase(ctx context.Context, db *gorm.DB, cfg *config.Config) (*models.Database, error) {
	var existing models.Database
	err := db.WithContext(ctx).Where("database_name = ?", ExternalDatabaseName).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	dir, err := UploadDir(cfg)
	if err != nil {
		return nil, err
	}
	file := filepath.ToSlash(filepath.Join(dir, "external.db"))

	created := models.Database{
		UUID:                uuid.NewString(),
		DatabaseName:        ExternalDatabaseName,
		VerboseName:         ExternalDatabaseName,
		SQLAlchemyURI:       "sqlite:///" + file,
		ConfigurationMethod: "sqlalchemy_form",
		ExposeInSQLlab:      true,
		ExploreDatabase:     true,
	}
	if err := db.WithContext(ctx).Create(&created).Error; err != nil {
		return nil, err
	}
	return &created, nil
}

// WriteTable (re)creates the given table in the managed SQLite store and inserts
// every row. The table is dropped first so a re-pull replaces stale data.
func WriteTable(ctx context.Context, uri, tableName string, tbl *Table) error {
	if tbl == nil || len(tbl.Columns) == 0 {
		return fmt.Errorf("there is nothing to write: no columns were found")
	}
	db, spec, err := connectors.Open(uri)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	quoted := spec.QuoteIdentifier(tableName)
	colNames := make([]string, len(tbl.Columns))
	defs := make([]string, len(tbl.Columns))
	for i, c := range tbl.Columns {
		colNames[i] = c.Name
		defs[i] = spec.QuoteIdentifier(c.Name) + " " + sqliteType(c.Type)
	}

	for _, stmt := range []string{
		"DROP TABLE IF EXISTS " + quoted,
		"CREATE TABLE " + quoted + " (" + strings.Join(defs, ", ") + ")",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("could not prepare table %q: %w", tableName, err)
		}
	}
	if len(tbl.Rows) == 0 {
		return nil
	}

	placeholders := "(" + strings.TrimSuffix(strings.Repeat("?,", len(colNames)), ",") + ")"
	prefix := "INSERT INTO " + quoted + " (" + joinIdentifiers(spec, colNames) + ") VALUES "

	const batchSize = 200
	for start := 0; start < len(tbl.Rows); start += batchSize {
		end := start + batchSize
		if end > len(tbl.Rows) {
			end = len(tbl.Rows)
		}
		values := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*len(colNames))
		for _, row := range tbl.Rows[start:end] {
			values = append(values, placeholders)
			for j := range colNames {
				if j < len(row) {
					args = append(args, row[j])
				} else {
					args = append(args, nil)
				}
			}
		}
		if _, err := db.ExecContext(ctx, prefix+strings.Join(values, ","), args...); err != nil {
			return fmt.Errorf("could not insert rows into %q: %w", tableName, err)
		}
	}
	return nil
}

func joinIdentifiers(spec connectors.EngineSpec, names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = spec.QuoteIdentifier(n)
	}
	return strings.Join(quoted, ", ")
}

// sqliteType maps a generic column type onto SQLite's storage classes.
func sqliteType(generic string) string {
	switch generic {
	case TypeInteger, TypeBoolean:
		return "INTEGER"
	case TypeFloat:
		return "REAL"
	default:
		return "TEXT"
	}
}

// SanitizeIdentifier turns an arbitrary label into a safe SQL identifier,
// returning "" when nothing usable remains so callers can supply a fallback.
// Letters and digits are kept in any script, so Chinese labels stay readable.
func SanitizeIdentifier(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = identifierUnsafe.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return ""
	}
	// Counting in runes keeps a multi-byte script from splitting mid-character.
	runes := []rune(name)
	if unicode.IsDigit(runes[0]) {
		name = "t_" + name
		runes = []rune(name)
	}
	if len(runes) > maxIdentifierRunes {
		name = strings.Trim(string(runes[:maxIdentifierRunes]), "_")
	}
	return name
}

// UniqueNames sanitizes a list of labels, de-duplicating collisions and filling
// blanks with a positional default.
func UniqueNames(names []string) []string {
	used := map[string]int{}
	out := make([]string, len(names))
	for i, raw := range names {
		base := SanitizeIdentifier(raw)
		if base == "" {
			base = fmt.Sprintf("column_%d", i+1)
		}
		count := used[base]
		used[base] = count + 1
		if count == 0 {
			out[i] = base
		} else {
			out[i] = fmt.Sprintf("%s_%d", base, count+1)
		}
	}
	return out
}
