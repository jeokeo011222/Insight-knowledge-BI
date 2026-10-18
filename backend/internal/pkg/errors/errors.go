// Package errors defines the unified application error type and error type
// enumeration, mirroring superset/errors.py.
package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrorType enumerates the superset error types (SupersetErrorType).
type ErrorType string

const (
	// Generic
	GenericError           ErrorType = "GENERIC_COMMAND_ERROR"
	GenericValidationError ErrorType = "VALIDATION_ERROR"
	ModelMissingError      ErrorType = "MODEL_MISSING_ERROR"
	ModelNotFoundError     ErrorType = "MODEL_NOT_FOUND_ERROR"
	InternalError          ErrorType = "INTERNAL_ERROR"
	ConflictError          ErrorType = "CONFLICT_ERROR"
	UnavailableError       ErrorType = "UNAVAILABLE_ERROR"

	// Auth / security
	AccessDeniedError             ErrorType = "ACCESS_DENIED_ERROR"
	AuthenticationError           ErrorType = "AUTHENTICATION_ERROR"
	UnauthorizedError             ErrorType = "UNAUTHORIZED_ERROR"
	DatabaseSecurityAccessError   ErrorType = "DATABASE_SECURITY_ACCESS_ERROR"
	DatasourceSecurityAccessError ErrorType = "DATASOURCE_SECURITY_ACCESS_ERROR"
	DashboardSecurityAccessError  ErrorType = "DASHBOARD_SECURITY_ACCESS_ERROR"
	ChartSecurityAccessError      ErrorType = "CHART_SECURITY_ACCESS_ERROR"
	TableSecurityAccessError      ErrorType = "TABLE_SECURITY_ACCESS_ERROR"
	QuerySecurityAccessError      ErrorType = "QUERY_SECURITY_ACCESS_ERROR"
	MissingOwnershipError         ErrorType = "MISSING_OWNERSHIP_ERROR"

	// Query / datasource
	QueryObjectValidationError ErrorType = "QUERY_OBJECT_VALIDATION_ERROR"
	QueryContextError          ErrorType = "QUERY_CONTEXT_ERROR"
	InvalidPostProcessingError ErrorType = "INVALID_POST_PROCESSING_ERROR"
	ColumnNotFoundException    ErrorType = "COLUMN_NOT_FOUND_ERROR"
	DatasourceTypeError        ErrorType = "DATASOURCE_TYPE_ERROR"
	SerializationError         ErrorType = "SERIALIZATION_ERROR"
	ResultsBackendError        ErrorType = "RESULTS_BACKEND_ERROR"

	// Database
	GenericDBError          ErrorType = "GENERIC_DB_ENGINE_ERROR"
	DBAPIDatabaseError      ErrorType = "GENERIC_DB_API_ERROR"
	DBConnectionError       ErrorType = "DB_CONNECTION_ERROR"
	InvalidParametersError  ErrorType = "INVALID_PARAMETERS_ERROR"
	UnsupportedFeatureError ErrorType = "UNSUPPORTED_FEATURE_ERROR"

	// Import / export
	ImportExportError ErrorType = "IMPORT_EXPORT_ERROR"
	AssetImportError  ErrorType = "ASSET_IMPORT_ERROR"
	AssetExportError  ErrorType = "ASSET_EXPORT_ERROR"

	// Alerts / reports
	AlertReportError             ErrorType = "ALERT_REPORT_ERROR"
	AlertReportValidationError   ErrorType = "ALERT_REPORT_VALIDATION_ERROR"
	AlertReportNotificationError ErrorType = "ALERT_REPORT_NOTIFICATION_ERROR"
	AlertReportScheduleError     ErrorType = "ALERT_REPORT_SCHEDULE_ERROR"

	// Embedded / guest token
	EmbeddedDashboardNotFoundError     ErrorType = "EMBEDDED_DASHBOARD_NOT_FOUND_ERROR"
	EmbeddedDashboardAccessDeniedError ErrorType = "EMBEDDED_DASHBOARD_ACCESS_DENIED_ERROR"

	// RLS
	RLSDatasourceForbiddenError       ErrorType = "RLS_DATASOURCE_FORBIDDEN_ERROR"
	SubjectsNotFoundValidationError   ErrorType = "SUBJECTS_NOT_FOUND_VALIDATION_ERROR"
	DatasourceNotFoundValidationError ErrorType = "DATASOURCE_NOT_FOUND_VALIDATION_ERROR"

	// Time
	TimeRangeParseFailError ErrorType = "TIME_RANGE_PARSE_FAIL_ERROR"
	TimeDeltaAmbiguousError ErrorType = "TIME_DELTA_AMBIGUOUS_ERROR"

	// Password
	PasswordComplexityValidationError ErrorType = "PASSWORD_COMPLEXITY_VALIDATION_ERROR"

	// Jinja
	TemplateException ErrorType = "TEMPLATE_EXCEPTION"

	// 6.x
	SemanticLayerError ErrorType = "SEMANTIC_LAYER_ERROR"
	TaskError          ErrorType = "TASK_ERROR"
	ExtensionError     ErrorType = "EXTENSION_ERROR"
)

// AppError is the unified error type carried through handlers.
type AppError struct {
	Type       ErrorType      `json:"-"`
	Message    string         `json:"message"`
	StatusCode int            `json:"-"`
	Level      string         `json:"level,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// Error implements the error interface.
func (e *AppError) Error() string {
	return fmt.Sprintf("%s: %s", e.Type, e.Message)
}

// Status returns the HTTP status code.
func (e *AppError) Status() int { return e.StatusCode }

// WithExtra attaches extra context to the error.
func (e *AppError) WithExtra(extra map[string]any) *AppError {
	e.Extra = extra
	return e
}

// New builds an AppError.
func New(t ErrorType, message string, status int) *AppError {
	return &AppError{Type: t, Message: message, StatusCode: status, Level: "error"}
}

// Newf builds an AppError with a formatted message.
func Newf(t ErrorType, status int, format string, args ...any) *AppError {
	return New(t, fmt.Sprintf(format, args...), status)
}

// From maps a generic error to an AppError, defaulting to a 500.
func From(err error) *AppError {
	if err == nil {
		return nil
	}
	var ae *AppError
	if errors.As(err, &ae) {
		return ae
	}
	return New(InternalError, err.Error(), http.StatusInternalServerError)
}

// Constructors for common error cases ------------------------------------

func ValidationError(format string, args ...any) *AppError {
	return Newf(GenericValidationError, http.StatusUnprocessableEntity, format, args...)
}

func BadRequest(format string, args ...any) *AppError {
	return Newf(GenericError, http.StatusBadRequest, format, args...)
}

func Unauthorized(format string, args ...any) *AppError {
	return Newf(AuthenticationError, http.StatusUnauthorized, format, args...)
}

func Forbidden(format string, args ...any) *AppError {
	return Newf(AccessDeniedError, http.StatusForbidden, format, args...)
}

func NotFound(format string, args ...any) *AppError {
	return Newf(ModelMissingError, http.StatusNotFound, format, args...)
}

func Conflict(format string, args ...any) *AppError {
	return Newf(ConflictError, http.StatusConflict, format, args...)
}

func Internal(format string, args ...any) *AppError {
	return Newf(InternalError, http.StatusInternalServerError, format, args...)
}

// ColumnNotFound reports a filter/query referencing a column the datasource
// does not have. It is a client-side mistake (stale UI, typo) and must not
// leak the database's own SQLSTATE text.
func ColumnNotFound() *AppError {
	return New(ColumnNotFoundException, "Query references an unknown column", http.StatusBadRequest)
}

// ServiceUnavailable reports an unreachable datasource (DNS, refused, auth).
// 424 tells the UI "the source is down", not "your request is wrong".
func ServiceUnavailable(format string, args ...any) *AppError {
	return Newf(DBConnectionError, http.StatusFailedDependency, format, args...)
}

// GatewayTimeout reports a query that exceeded its execution budget.
func GatewayTimeout(format string, args ...any) *AppError {
	return Newf(UnavailableError, http.StatusGatewayTimeout, format, args...)
}

// Access error constructors with extra payloads (mirrors superset errors).

func DatabaseSecurityAccess(err error) *AppError {
	return New(DatabaseSecurityAccessError, err.Error(), http.StatusForbidden)
}

func DatasourceSecurityAccess(err error) *AppError {
	return New(DatasourceSecurityAccessError, err.Error(), http.StatusForbidden)
}

func DashboardSecurityAccess(err error) *AppError {
	return New(DashboardSecurityAccessError, err.Error(), http.StatusForbidden)
}

func ChartSecurityAccess(err error) *AppError {
	return New(ChartSecurityAccessError, err.Error(), http.StatusForbidden)
}

func QuerySecurityAccess(err error) *AppError {
	return New(QuerySecurityAccessError, err.Error(), http.StatusForbidden)
}

func TableSecurityAccess(err error) *AppError {
	return New(TableSecurityAccessError, err.Error(), http.StatusForbidden)
}

// ErrorResponse is the JSON error body shape.
type ErrorResponse struct {
	Message string         `json:"message"`
	Type    ErrorType      `json:"error_type,omitempty"`
	Level   string         `json:"level,omitempty"`
	Extra   map[string]any `json:"extra,omitempty"`
}

// ToResponse converts an error into the JSON response body.
func ToResponse(err error) ErrorResponse {
	ae := From(err)
	return ErrorResponse{Message: ae.Message, Type: ae.Type, Level: ae.Level, Extra: ae.Extra}
}

// MarshalJSON renders the superset-style error body {"message": ...}.
func (e *AppError) MarshalJSON() ([]byte, error) {
	return json.Marshal(ErrorResponse{Message: e.Message, Type: e.Type, Level: e.Level, Extra: e.Extra})
}
