package queries

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Read-only sessions (只读会话).
//
// The SQL guard in the Agent plane rejects statements it can prove are unsafe.
// A session-level READ ONLY downgrade is the complement: it is the engine
// itself refusing a write, which catches every statement the parser could not
// classify (unknown dialects, exotic syntax, a bypass we have not thought of).
//
// Only engines with a real session-level switch are downgraded. For the rest
// the guard and the whitelist remain the boundary, and the caller is told so by
// ReadOnlyResult.SessionReadOnly so the UI never claims a protection it did not
// have.

// sqlQuerier is the subset of *sql.DB / *sql.Conn the executors need.
type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readOnlyDialect classifies an SQLAlchemy URI into the session-switch family.
func readOnlyDialect(uri string) string {
	u := strings.ToLower(strings.TrimSpace(uri))
	switch {
	case strings.HasPrefix(u, "postgres"), strings.HasPrefix(u, "postgresql"):
		return "postgres"
	case strings.HasPrefix(u, "mysql"), strings.HasPrefix(u, "mariadb"):
		return "mysql"
	case strings.HasPrefix(u, "sqlite"):
		return "sqlite"
	default:
		return ""
	}
}

// readOnlyStatement returns the statement that downgrades a session, and the one
// that restores it before the connection goes back to the pool.
func readOnlyStatement(dialect string, on bool) string {
	switch dialect {
	case "postgres":
		if on {
			return "SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY"
		}
		return "SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE"
	case "mysql":
		if on {
			return "SET SESSION TRANSACTION READ ONLY"
		}
		return "SET SESSION TRANSACTION READ WRITE"
	case "sqlite":
		if on {
			return "PRAGMA query_only = ON"
		}
		return "PRAGMA query_only = OFF"
	default:
		return ""
	}
}

// openReadOnlySession pins one pooled connection, downgrades it to read-only and
// returns a release function that restores the original session mode before the
// connection returns to the pool. Leaking a read-only connection into the pool
// would break the next chart write (dataset refresh, SQL Lab DML), so the reset
// is attempted even when the caller panics mid-query.
//
// When the engine has no session switch, or the downgrade fails, it degrades to
// the plain pool: the guard and whitelist still apply, and the caller learns the
// truth from the returned ok flag.
func openReadOnlySession(ctx context.Context, db *sql.DB, uri string, log *zap.Logger) (sqlQuerier, bool, func()) {
	dialect := readOnlyDialect(uri)
	if dialect == "" {
		return db, false, func() {}
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		if log != nil {
			log.Warn("read-only session unavailable, falling back to the pool", zap.Error(err))
		}
		return db, false, func() {}
	}
	if _, err := conn.ExecContext(ctx, readOnlyStatement(dialect, true)); err != nil {
		if log != nil {
			log.Warn("failed to downgrade the session to read-only",
				zap.String("dialect", dialect), zap.Error(err))
		}
		_ = conn.Close()
		return db, false, func() {}
	}
	release := func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(resetCtx, readOnlyStatement(dialect, false)); err != nil && log != nil {
			log.Warn("failed to restore the session mode; discarding the connection",
				zap.String("dialect", dialect), zap.Error(err))
			// Discard rather than return a poisoned connection to the pool.
			_ = conn.Raw(func(any) error { return sql.ErrConnDone })
			_ = conn.Close()
			return
		}
		_ = conn.Close()
	}
	return conn, true, release
}
