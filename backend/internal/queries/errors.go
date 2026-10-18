package queries

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	apperrors "superset/internal/pkg/errors"
)

// classifyQueryError maps a raw driver error onto a stable, user-facing
// AppError. The database's own text (SQLSTATE, table/column names, driver
// chatter) never reaches the client: the caller logs it and the query audit
// keeps it for operators.
//
// The status codes are chosen so the UI can act on them:
//
//	400 unknown column / table  → "refresh, the field no longer exists"
//	403 insufficient privilege  → "ask your admin for access"
//	424 datasource unreachable  → "the source is down, retry later"
//	504 query timed out         → "narrow the range"
//	500 anything else           → "something broke, check the logs"
func classifyQueryError(err error, timeout time.Duration) *apperrors.AppError {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || isTimeout(err) {
		secs := int(timeout.Seconds())
		if secs <= 0 {
			secs = defaultQueryTimeout
		}
		return apperrors.GatewayTimeout(
			"Query timed out after %ds; narrow the time range or add filters", secs)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42703": // undefined_column
			return apperrors.ColumnNotFound()
		case "42P01": // undefined_table
			return apperrors.BadRequest("Query references a table that does not exist")
		case "42501": // insufficient_privilege
			return apperrors.Forbidden("Insufficient database privileges for this query")
		case "3D000", "28P01", "28000", "08001", "08006", "57P03":
			return apperrors.ServiceUnavailable("The data source is unavailable")
		}
	}

	if errors.Is(err, sql.ErrConnDone) {
		return apperrors.ServiceUnavailable("The data source is unavailable")
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "unknown column"),
		strings.Contains(lower, "no such column"),
		strings.Contains(lower, "invalid column name"),
		strings.Contains(lower, "column") && strings.Contains(lower, "does not exist"):
		return apperrors.ColumnNotFound()
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "dial tcp"),
		strings.Contains(lower, "broken pipe"),
		strings.Contains(lower, "server closed"),
		strings.Contains(lower, "failed to connect"),
		strings.Contains(lower, "could not connect"):
		return apperrors.ServiceUnavailable("The data source is unavailable")
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"):
		secs := int(timeout.Seconds())
		if secs <= 0 {
			secs = defaultQueryTimeout
		}
		return apperrors.GatewayTimeout(
			"Query timed out after %ds; narrow the time range or add filters", secs)
	}
	return apperrors.Internal("Query execution failed")
}

// isTimeout reports whether err is a net timeout (dial/TLS/read deadlines).
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ClassifyQueryError is the exported entry point for callers outside the query
// engine (SQL Lab opens its own pool).
func ClassifyQueryError(err error, timeout time.Duration) *apperrors.AppError {
	return classifyQueryError(err, timeout)
}

// StatementErrorMessage picks the message a SQL editor should show.
//
// Two audiences, two messages. A connectivity failure (datasource down, query
// timed out) is not the author's fault and its driver text is noise — show the
// classified sentence. A rejected statement (bad column, bad syntax) is the
// author's fault and the driver's text IS the fix ("syntax error at or near
// 'form'"), so keep it. Neither case leaks SQLSTATE to a chart request, because
// that path uses classifyQueryError directly.
func StatementErrorMessage(err error, timeout time.Duration) (int, string) {
	classified := classifyQueryError(err, timeout)
	if classified == nil {
		return 200, ""
	}
	switch classified.StatusCode {
	case 424, 504:
		return classified.StatusCode, classified.Message
	default:
		return classified.StatusCode, err.Error()
	}
}
