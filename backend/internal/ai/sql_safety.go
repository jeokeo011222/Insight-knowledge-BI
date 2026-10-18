package ai

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrUnsafeSQL marks a statement rejected by the read-only guard.
var ErrUnsafeSQL = errors.New("unsafe sql rejected")

// sqlKeywordBlocklist covers DDL, DML and statement-level side effects that a
// read-only query must never contain. Word boundaries keep identifiers such as
// "created_at" or "deleted" from tripping the guard. REPLACE is deliberately
// absent: as a statement it cannot follow a leading SELECT/WITH, while
// REPLACE(col, a, b) is a common scalar function in read-only SQL.
var sqlKeywordBlocklist = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|merge|call|execute|exec|copy|vacuum|analyze|refresh|rename|attach|detach|into|outfile|dumpfile|load_file|kill|shutdown|reindex|cluster|pragma)\b`)

var (
	sqlBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	sqlLineComment  = regexp.MustCompile(`--[^\n]*`)
	sqlSingleQuote  = regexp.MustCompile(`'(?:[^']|'')*'`)
	sqlDoubleQuote  = regexp.MustCompile(`"(?:[^"]|"")*"`)
)

// ValidateReadOnlySQL accepts only a single read-only SELECT/WITH statement.
// It strips comments and string literals first so keyword checks cannot be
// bypassed by quoting, then rejects multi-statement input and write keywords.
//
// This is a conservative keyword guard rather than a full SQL AST parse; it is
// deliberately strict (any write keyword anywhere fails the statement).
func ValidateReadOnlySQL(statement string) error {
	s := strings.TrimSpace(statement)
	if s == "" {
		return fmt.Errorf("%w: empty statement", ErrUnsafeSQL)
	}
	trimmed := strings.TrimRight(s, "; \t\r\n")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("%w: multiple statements are not allowed", ErrUnsafeSQL)
	}

	clean := stripSQLNoise(trimmed)
	if !startsWithReadKeyword(clean) {
		return fmt.Errorf("%w: only SELECT/WITH queries are allowed", ErrUnsafeSQL)
	}
	if m := sqlKeywordBlocklist.FindStringSubmatch(clean); m != nil {
		return fmt.Errorf("%w: keyword %q is not allowed", ErrUnsafeSQL, strings.ToLower(m[1]))
	}
	return nil
}

// stripSQLNoise removes comments and string literals so keyword scanning sees
// only the structural SQL text.
func stripSQLNoise(s string) string {
	s = sqlBlockComment.ReplaceAllString(s, " ")
	s = sqlLineComment.ReplaceAllString(s, " ")
	s = sqlSingleQuote.ReplaceAllString(s, "''")
	s = sqlDoubleQuote.ReplaceAllString(s, `""`)
	return s
}

// startsWithReadKeyword reports whether the statement begins with SELECT or
// WITH, ignoring leading parentheses and whitespace.
func startsWithReadKeyword(s string) bool {
	t := strings.ToUpper(strings.TrimLeft(s, " \t\r\n("))
	return strings.HasPrefix(t, "SELECT") || strings.HasPrefix(t, "WITH")
}
