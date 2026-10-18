package connectors

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Errors returned by engines whose native driver is not linked into this
// build. They are deliberately distinct so callers can tell "the engine is
// known but cannot be opened here" apart from a malformed connection.
var (
	// ErrDriverNotBundled marks an engine that is registered with a complete
	// dialect spec but has no Go driver compiled into the binary.
	ErrDriverNotBundled = errors.New("driver not bundled in this build")
	// ErrDBFileNotFound marks a file-backed connection whose database file is
	// missing. The API translates it, because a silent "connection failed" made
	// a typo in the path look like a credentials problem.
	ErrDBFileNotFound = errors.New("database file not found")
	// ErrDriverUnavailable marks a connection URI that cannot be converted
	// into a driver DSN for the same reason.
	ErrDriverUnavailable = errors.New("no Go driver is available for this engine")
)

// timeGrainFunc renders a dialect specific time-grain expression.
type timeGrainFunc func(col, grain, tz string) (string, error)

// limitFunc renders a dialect specific row limit clause.
type limitFunc func(n int) string

// notBundled builds the honest error returned by spec-only engines.
func notBundled(engine string) error {
	return fmt.Errorf("driver for engine %q is not bundled in this build: %w", engine, ErrDriverNotBundled)
}

// emptySchemasQuery is the metadata default for engines without a catalogue.
const emptySchemasQuery = `SELECT '' AS schema_name WHERE 1 = 0`

// emptyTablesQuery is the metadata default for engines without a catalogue.
const emptyTablesQuery = `SELECT '' AS table_name, '' AS table_type WHERE 1 = 0`

// emptyColumnsQuery is the metadata default for engines without a catalogue.
const emptyColumnsQuery = `SELECT '' AS column_name, '' AS data_type, '' AS is_nullable WHERE 1 = 0`

// specOnlySpec is a fully specified dialect without a bundled Go driver.
type specOnlySpec struct {
	BaseSpec
	grain   timeGrainFunc
	schemas string
	tables  string
	columns string
	limit   limitFunc
	epoch   func(string) string
	convert func(string) string
}

// engineDef is the declarative description used to build spec-only engines.
type engineDef struct {
	engine        string
	name          string
	driver        string
	port          int
	quote         string
	quoteEnd      string
	aliasInSelect bool
	fileUpload    bool
	noJoins       bool
	maxName       int
	defaultSchema string
	grain         timeGrainFunc
	schemas       string
	tables        string
	columns       string
	limit         limitFunc
	epoch         func(string) string
	convert       func(string) string
	info          map[string]any
}

// newSpecOnly materialises an engineDef.
func newSpecOnly(def engineDef) *specOnlySpec {
	if def.maxName == 0 {
		def.maxName = 128
	}
	if def.schemas == "" {
		def.schemas = emptySchemasQuery
	}
	if def.tables == "" {
		def.tables = emptyTablesQuery
	}
	if def.columns == "" {
		def.columns = emptyColumnsQuery
	}
	s := &specOnlySpec{
		BaseSpec: BaseSpec{
			EngineValue:      def.engine,
			EngineNameValue:  def.name,
			DriverValue:      def.driver,
			PortValue:        def.port,
			QuoteChar:        def.quote,
			QuoteEnd:         def.quoteEnd,
			HasJoins:         !def.noJoins,
			HasSubqueries:    !def.noJoins,
			AliasInSelect:    def.aliasInSelect,
			FileUpload:       def.fileUpload,
			MaxNameLen:       def.maxName,
			DefaultSchemaVal: def.defaultSchema,
			Info:             def.info,
		},
		grain:   def.grain,
		schemas: def.schemas,
		tables:  def.tables,
		columns: def.columns,
		limit:   def.limit,
		epoch:   def.epoch,
		convert: def.convert,
	}
	return s
}

// TimeGrainExpr delegates to the dialect expression.
func (s *specOnlySpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	if s.grain == nil {
		return "", fmt.Errorf("time grains are not supported for engine %q", s.Engine())
	}
	return s.grain(col, grain, tz)
}

// LimitClause delegates to the dialect limit syntax.
func (s *specOnlySpec) LimitClause(n int) string {
	if s.limit != nil {
		return s.limit(n)
	}
	return s.BaseSpec.LimitClause(n)
}

// EpochToDTTM delegates to the dialect conversion.
func (s *specOnlySpec) EpochToDTTM(col string) string {
	if s.epoch != nil {
		return s.epoch(col)
	}
	return s.BaseSpec.EpochToDTTM(col)
}

// ConvertToDateTimeExpr delegates to the dialect conversion.
func (s *specOnlySpec) ConvertToDateTimeExpr(col string) string {
	if s.convert != nil {
		return s.convert(col)
	}
	return s.BaseSpec.ConvertToDateTimeExpr(col)
}

// DSN reports that no Go driver is bundled.
func (s *specOnlySpec) DSN(uri string) (string, string, error) {
	return "", "", fmt.Errorf("%s: %w", s.Engine(), ErrDriverUnavailable)
}

// Open reports that no Go driver is bundled.
func (s *specOnlySpec) Open(dsn string) (*sql.DB, error) { return nil, notBundled(s.Engine()) }

// SchemasQuery returns the dialect schema listing.
func (s *specOnlySpec) SchemasQuery() string { return s.schemas }

// TablesQuery returns the dialect table listing.
func (s *specOnlySpec) TablesQuery() string { return s.tables }

// ColumnsQuery returns the dialect column listing.
func (s *specOnlySpec) ColumnsQuery() string { return s.columns }

// sqlLimit is the plain "LIMIT n" clause used by most dialects.
func sqlLimit(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("LIMIT %d", n)
}

// fetchFirstLimit renders the SQL:2008 row limiting clause (Oracle 12c+, Db2).
func fetchFirstLimit(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("FETCH FIRST %d ROWS ONLY", n)
}

// offsetFetchLimit renders the T-SQL row limiting clause.
func offsetFetchLimit(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("OFFSET 0 ROWS FETCH NEXT %d ROWS ONLY", n)
}

// quotedColumn wraps a timestamp column with a timezone conversion when the
// caller asked for a non-UTC zone and the engine supports the syntax.
func tzAwarePG(col, tz string) string {
	if tz == "" || strings.EqualFold(tz, "UTC") {
		return col
	}
	return fmt.Sprintf("%s AT TIME ZONE 'UTC' AT TIME ZONE '%s'", col, tz)
}

// pgTimeGrain mirrors PostgresEngineSpec._time_grain_expressions.
func pgTimeGrain(col, grain, tz, engine string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("%s: %w", engine, err)
	}
	if norm == "" {
		return col, nil
	}
	c := tzAwarePG(col, tz)
	switch norm {
	case "PT1S":
		return fmt.Sprintf("DATE_TRUNC('second', %s)", c), nil
	case "PT1M":
		return fmt.Sprintf("DATE_TRUNC('minute', %s)", c), nil
	case "PT5M":
		return fmt.Sprintf("DATE_TRUNC('hour', %[1]s) + INTERVAL '5 minutes' * FLOOR(EXTRACT(MINUTE FROM %[1]s) / 5)", c), nil
	case "PT10M":
		return fmt.Sprintf("DATE_TRUNC('hour', %[1]s) + INTERVAL '10 minutes' * FLOOR(EXTRACT(MINUTE FROM %[1]s) / 10)", c), nil
	case "PT15M":
		return fmt.Sprintf("DATE_TRUNC('hour', %[1]s) + INTERVAL '15 minutes' * FLOOR(EXTRACT(MINUTE FROM %[1]s) / 15)", c), nil
	case "PT30M":
		return fmt.Sprintf("DATE_TRUNC('hour', %[1]s) + INTERVAL '30 minutes' * FLOOR(EXTRACT(MINUTE FROM %[1]s) / 30)", c), nil
	case "PT1H":
		return fmt.Sprintf("DATE_TRUNC('hour', %s)", c), nil
	case "PT6H":
		return fmt.Sprintf("DATE_TRUNC('day', %[1]s) + INTERVAL '6 hours' * FLOOR(EXTRACT(HOUR FROM %[1]s) / 6)", c), nil
	case "P1D":
		return fmt.Sprintf("DATE_TRUNC('day', %s)", c), nil
	case "P1W":
		return fmt.Sprintf("DATE_TRUNC('week', %s)", c), nil
	case "P1M":
		return fmt.Sprintf("DATE_TRUNC('month', %s)", c), nil
	case "P3M":
		return fmt.Sprintf("DATE_TRUNC('quarter', %s)", c), nil
	case "P1Y":
		return fmt.Sprintf("DATE_TRUNC('year', %s)", c), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for %s", grain, engine)
}

// dateTruncTimeGrain renders DATE_TRUNC for engines with that function.
func dateTruncTimeGrain(col, grain, tz, engine string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("%s: %w", engine, err)
	}
	if norm == "" {
		return col, nil
	}
	unit, err := GrainToDateTrunc(norm)
	if err != nil {
		return "", fmt.Errorf("%s: %w", engine, err)
	}
	switch norm {
	case "PT5M", "PT10M", "PT15M", "PT30M", "PT6H":
		return "", fmt.Errorf("unsupported time grain %q for %s", grain, engine)
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", unit, col), nil
}

// mysqlTimeGrain mirrors MySQLEngineSpec._time_grain_expressions.
func mysqlTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("mysql: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		col = fmt.Sprintf("CONVERT_TZ(%s, 'UTC', '%s')", col, tz)
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:%%s')", col), nil
	case "PT1M":
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:00')", col), nil
	case "PT5M":
		return fmt.Sprintf("TIMESTAMP(DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:00')) - INTERVAL (MINUTE(%s) %% 5) MINUTE", col, col), nil
	case "PT10M":
		return fmt.Sprintf("TIMESTAMP(DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:00')) - INTERVAL (MINUTE(%s) %% 10) MINUTE", col, col), nil
	case "PT15M":
		return fmt.Sprintf("TIMESTAMP(DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:00')) - INTERVAL (MINUTE(%s) %% 15) MINUTE", col, col), nil
	case "PT30M":
		return fmt.Sprintf("TIMESTAMP(DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:%%i:00')) - INTERVAL (MINUTE(%s) %% 30) MINUTE", col, col), nil
	case "PT1H":
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:00:00')", col), nil
	case "PT6H":
		return fmt.Sprintf("TIMESTAMP(DATE_FORMAT(%s, '%%Y-%%m-%%d %%H:00:00')) - INTERVAL (HOUR(%s) %% 6) HOUR", col, col), nil
	case "P1D":
		return fmt.Sprintf("DATE(%s)", col), nil
	case "P1W":
		return fmt.Sprintf("DATE(%s) - INTERVAL WEEKDAY(%s) DAY", col, col), nil
	case "P1M":
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-%%m-01')", col), nil
	case "P3M":
		return fmt.Sprintf("CONCAT(YEAR(%s), '-', LPAD(QUARTER(%s) * 3 - 2, 2, '0'), '-01')", col, col), nil
	case "P1Y":
		return fmt.Sprintf("DATE_FORMAT(%s, '%%Y-01-01')", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for mysql", grain)
}

// mssqlTimeGrain mirrors MssqlEngineSpec._time_grain_expressions.
func mssqlTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("mssql: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("DATEADD(SECOND, DATEDIFF(SECOND, '2000-01-01', %s), '2000-01-01')", col), nil
	case "PT1M":
		return fmt.Sprintf("DATEADD(MINUTE, DATEDIFF(MINUTE, 0, %s), 0)", col), nil
	case "PT5M":
		return fmt.Sprintf("DATEADD(MINUTE, DATEDIFF(MINUTE, 0, %[1]s) / 5 * 5, 0)", col), nil
	case "PT10M":
		return fmt.Sprintf("DATEADD(MINUTE, DATEDIFF(MINUTE, 0, %[1]s) / 10 * 10, 0)", col), nil
	case "PT15M":
		return fmt.Sprintf("DATEADD(MINUTE, DATEDIFF(MINUTE, 0, %[1]s) / 15 * 15, 0)", col), nil
	case "PT30M":
		return fmt.Sprintf("DATEADD(MINUTE, DATEDIFF(MINUTE, 0, %[1]s) / 30 * 30, 0)", col), nil
	case "PT1H":
		return fmt.Sprintf("DATEADD(HOUR, DATEDIFF(HOUR, 0, %s), 0)", col), nil
	case "PT6H":
		return fmt.Sprintf("DATEADD(HOUR, DATEDIFF(HOUR, 0, %[1]s) / 6 * 6, 0)", col), nil
	case "P1D":
		return fmt.Sprintf("DATEADD(DAY, DATEDIFF(DAY, 0, %s), 0)", col), nil
	case "P1W":
		return fmt.Sprintf("DATEADD(DAY, 1 - DATEPART(WEEKDAY, %[1]s), DATEADD(DAY, DATEDIFF(DAY, 0, %[1]s), 0))", col), nil
	case "P1M":
		return fmt.Sprintf("DATEADD(MONTH, DATEDIFF(MONTH, 0, %s), 0)", col), nil
	case "P3M":
		return fmt.Sprintf("DATEADD(QUARTER, DATEDIFF(QUARTER, 0, %s), 0)", col), nil
	case "P1Y":
		return fmt.Sprintf("DATEADD(YEAR, DATEDIFF(YEAR, 0, %s), 0)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for mssql", grain)
}

// oracleTimeGrain mirrors OracleEngineSpec._time_grain_expressions.
func oracleTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("oracle: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("CAST(%s AS DATE)", col), nil
	case "PT1M":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'MI')", col), nil
	case "PT1H":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'HH')", col), nil
	case "P1D":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'DDD')", col), nil
	case "P1W":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'WW')", col), nil
	case "P1M":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'MONTH')", col), nil
	case "P3M":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'Q')", col), nil
	case "P1Y":
		return fmt.Sprintf("TRUNC(CAST(%s AS DATE), 'YEAR')", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for oracle", grain)
}

// snowflakeTimeGrain mirrors SnowflakeEngineSpec._time_grain_expressions.
func snowflakeTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("snowflake: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	unit, err := GrainToDateTrunc(norm)
	if err != nil {
		return "", err
	}
	switch norm {
	case "PT5M", "PT10M", "PT15M", "PT30M", "PT6H":
		return "", fmt.Errorf("unsupported time grain %q for snowflake", grain)
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", strings.ToUpper(unit), col), nil
}

// bigqueryTimeGrain mirrors BigQueryEngineSpec._time_grain_expressions for the
// timestamp type.
func bigqueryTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("bigquery: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	if norm == "P1W" {
		return fmt.Sprintf("TIMESTAMP_TRUNC(%s, WEEK)", col), nil
	}
	unit, err := GrainToDateTrunc(norm)
	if err != nil {
		return "", err
	}
	switch norm {
	case "PT5M":
		return fmt.Sprintf("TIMESTAMP_SECONDS(5*60 * DIV(UNIX_SECONDS(CAST(%[1]s AS TIMESTAMP)), 5*60))", col), nil
	case "PT10M":
		return fmt.Sprintf("TIMESTAMP_SECONDS(10*60 * DIV(UNIX_SECONDS(CAST(%[1]s AS TIMESTAMP)), 10*60))", col), nil
	case "PT15M":
		return fmt.Sprintf("TIMESTAMP_SECONDS(15*60 * DIV(UNIX_SECONDS(CAST(%[1]s AS TIMESTAMP)), 15*60))", col), nil
	case "PT30M":
		return fmt.Sprintf("TIMESTAMP_SECONDS(30*60 * DIV(UNIX_SECONDS(CAST(%[1]s AS TIMESTAMP)), 30*60))", col), nil
	case "PT6H":
		return fmt.Sprintf("TIMESTAMP_SECONDS(6*3600 * DIV(UNIX_SECONDS(CAST(%[1]s AS TIMESTAMP)), 6*3600))", col), nil
	}
	return fmt.Sprintf("TIMESTAMP_TRUNC(%s, %s)", col, strings.ToUpper(unit)), nil
}

// hiveTimeGrain mirrors HiveEngineSpec._time_grain_expressions.
func hiveTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("hive: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-MM-dd HH:mm:ss')", col), nil
	case "PT1M":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-MM-dd HH:mm:00')", col), nil
	case "PT1H":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-MM-dd HH:00:00')", col), nil
	case "P1D":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-MM-dd 00:00:00')", col), nil
	case "P1W":
		return fmt.Sprintf("date_format(date_sub(%[1]s, CAST(7-from_unixtime(unix_timestamp(%[1]s),'u') as int)), 'yyyy-MM-dd 00:00:00')", col), nil
	case "P1M":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-MM-01 00:00:00')", col), nil
	case "P3M":
		return fmt.Sprintf("date_format(add_months(trunc(%[1]s, 'MM'), -(month(%[1]s)-1)%%3), 'yyyy-MM-dd 00:00:00')", col), nil
	case "P1Y":
		return fmt.Sprintf("from_unixtime(unix_timestamp(%s), 'yyyy-01-01 00:00:00')", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for hive", grain)
}

// druidTimeGrain mirrors DruidEngineSpec._time_grain_expressions.
func druidTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("druid: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	periods := map[string]string{
		"PT1S": "PT1S", "PT1M": "PT1M", "PT5M": "PT5M", "PT10M": "PT10M",
		"PT15M": "PT15M", "PT30M": "PT30M", "PT1H": "PT1H", "PT6H": "PT6H",
		"P1D": "P1D", "P1W": "P1W", "P1M": "P1M", "P3M": "P3M", "P1Y": "P1Y",
	}
	period, ok := periods[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for druid", grain)
	}
	return fmt.Sprintf("TIME_FLOOR(CAST(%s AS TIMESTAMP), '%s')", col, period), nil
}

// pinotTimeGrain mirrors PinotEngineSpec._time_grain_expressions.
func pinotTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("pinot: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	unit, err := GrainToDateTrunc(norm)
	if err != nil {
		return "", err
	}
	switch norm {
	case "PT5M":
		return fmt.Sprintf("CAST(ROUND(DATE_TRUNC('minute', CAST(%[1]s AS TIMESTAMP)), 300000) AS TIMESTAMP)", col), nil
	case "PT10M":
		return fmt.Sprintf("CAST(ROUND(DATE_TRUNC('minute', CAST(%[1]s AS TIMESTAMP)), 600000) AS TIMESTAMP)", col), nil
	case "PT15M":
		return fmt.Sprintf("CAST(ROUND(DATE_TRUNC('minute', CAST(%[1]s AS TIMESTAMP)), 900000) AS TIMESTAMP)", col), nil
	case "PT30M":
		return fmt.Sprintf("CAST(ROUND(DATE_TRUNC('minute', CAST(%[1]s AS TIMESTAMP)), 1800000) AS TIMESTAMP)", col), nil
	case "PT6H":
		return fmt.Sprintf("CAST(ROUND(DATE_TRUNC('hour', CAST(%[1]s AS TIMESTAMP)), 21600000) AS TIMESTAMP)", col), nil
	}
	return fmt.Sprintf("CAST(DATE_TRUNC('%s', CAST(%s AS TIMESTAMP)) AS TIMESTAMP)", unit, col), nil
}

// elasticsearchTimeGrain mirrors ElasticsearchEngineSpec._time_grain_expressions.
func elasticsearchTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("elasticsearch: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	unit, err := GrainToDateTrunc(norm)
	if err != nil {
		return "", err
	}
	switch norm {
	case "PT5M", "PT10M", "PT15M", "PT30M", "PT6H":
		return "", fmt.Errorf("unsupported time grain %q for elasticsearch", grain)
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", unit, col), nil
}

// teradataTimeGrain mirrors TeradataEngineSpec._time_grain_expressions.
// Teradata has no second level truncation, matching upstream.
func teradataTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("teradatasql: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	units := map[string]string{
		"PT1M": "MI", "PT1H": "HH", "P1D": "DDD", "P1W": "WW",
		"P1M": "MONTH", "P3M": "Q", "P1Y": "YEAR",
	}
	unit, ok := units[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for teradatasql", grain)
	}
	return fmt.Sprintf("TRUNC(CAST(%s AS DATE), '%s')", col, unit), nil
}

// kylinTimeGrain mirrors KylinEngineSpec._time_grain_expressions.
func kylinTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("kylin: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("TIMESTAMPADD(SECOND, HOUR(%[1]s) * 3600 + MINUTE(%[1]s) * 60 + SECOND(%[1]s), CAST(CAST(%[1]s AS DATE) AS TIMESTAMP))", col), nil
	case "PT1M":
		return fmt.Sprintf("TIMESTAMPADD(MINUTE, HOUR(%[1]s) * 60 + MINUTE(%[1]s), CAST(CAST(%[1]s AS DATE) AS TIMESTAMP))", col), nil
	case "PT1H":
		return fmt.Sprintf("TIMESTAMPADD(HOUR, HOUR(%[1]s), CAST(CAST(%[1]s AS DATE) AS TIMESTAMP))", col), nil
	case "P1D":
		return fmt.Sprintf("CAST(%s AS DATE)", col), nil
	case "P1W":
		return fmt.Sprintf("TIMESTAMPADD(DAY, 1 - DAYOFWEEK(%[1]s), CAST(%[1]s AS DATE))", col), nil
	case "P1M":
		return fmt.Sprintf("TIMESTAMPADD(DAY, 1 - DAYOFMONTH(%[1]s), CAST(%[1]s AS DATE))", col), nil
	case "P3M":
		return fmt.Sprintf("TIMESTAMPADD(MONTH, 3 * QUARTER(%[1]s) - 3, TIMESTAMPADD(DAY, 1 - DAYOFYEAR(%[1]s), CAST(%[1]s AS DATE)))", col), nil
	case "P1Y":
		return fmt.Sprintf("TIMESTAMPADD(DAY, 1 - DAYOFYEAR(%[1]s), CAST(%[1]s AS DATE))", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for kylin", grain)
}

// sqliteTimeGrain mirrors SQLiteEngineSpec._time_grain_expressions.
func sqliteTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("sqlite: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		col = fmt.Sprintf("DATETIME(%s, '%s')", col, tzOffsetModifier(tz))
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:%%S', %s)", col), nil
	case "PT1M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:00', %s)", col), nil
	case "PT5M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:00', %s, 'start of minute', '-' || (CAST(STRFTIME('%%M', %s) AS INTEGER) %% 5) || ' minutes')", col, col), nil
	case "PT10M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:00', %s, 'start of minute', '-' || (CAST(STRFTIME('%%M', %s) AS INTEGER) %% 10) || ' minutes')", col, col), nil
	case "PT15M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:00', %s, 'start of minute', '-' || (CAST(STRFTIME('%%M', %s) AS INTEGER) %% 15) || ' minutes')", col, col), nil
	case "PT30M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:%%M:00', %s, 'start of minute', '-' || (CAST(STRFTIME('%%M', %s) AS INTEGER) %% 30) || ' minutes')", col, col), nil
	case "PT1H":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:00:00', %s)", col), nil
	case "PT6H":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d %%H:00:00', %s, 'start of hour', '-' || (CAST(STRFTIME('%%H', %s) AS INTEGER) %% 6) || ' hours')", col, col), nil
	case "P1D":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-%%d', %s)", col), nil
	case "P1W":
		return fmt.Sprintf("DATE(%s, 'weekday 0', '-6 days')", col), nil
	case "P1M":
		return fmt.Sprintf("STRFTIME('%%Y-%%m-01', %s)", col), nil
	case "P3M":
		return fmt.Sprintf("DATE(STRFTIME('%%Y-', %s) || SUBSTR('01040710', (CAST(STRFTIME('%%m', %s) AS INTEGER) - 1) / 3 * 2 + 1, 2) || '-01')", col, col), nil
	case "P1Y":
		return fmt.Sprintf("STRFTIME('%%Y-01-01', %s)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for sqlite", grain)
}

// kustoTimeGrain renders the KQL startof* functions.
func kustoTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("kustosql: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("startofsecond(%s)", col), nil
	case "PT1M":
		return fmt.Sprintf("startofminute(%s)", col), nil
	case "PT5M":
		return fmt.Sprintf("bin(%s, 5m)", col), nil
	case "PT10M":
		return fmt.Sprintf("bin(%s, 10m)", col), nil
	case "PT15M":
		return fmt.Sprintf("bin(%s, 15m)", col), nil
	case "PT30M":
		return fmt.Sprintf("bin(%s, 30m)", col), nil
	case "PT1H":
		return fmt.Sprintf("startofhour(%s)", col), nil
	case "PT6H":
		return fmt.Sprintf("bin(%s, 6h)", col), nil
	case "P1D":
		return fmt.Sprintf("startofday(%s)", col), nil
	case "P1W":
		return fmt.Sprintf("startofweek(%s)", col), nil
	case "P1M":
		return fmt.Sprintf("startofmonth(%s)", col), nil
	case "P3M":
		return fmt.Sprintf("startofquarter(%s)", col), nil
	case "P1Y":
		return fmt.Sprintf("startofyear(%s)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for kustosql", grain)
}

// tdengineTimeGrain renders TDengine's TIMETRUNCATE function.
func tdengineTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("taosws: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	units := map[string]string{
		"PT1S": "1s", "PT1M": "1m", "PT5M": "5m", "PT10M": "10m", "PT15M": "15m",
		"PT30M": "30m", "PT1H": "1h", "PT6H": "6h", "P1D": "1d", "P1W": "1w",
		"P1M": "1n", "P3M": "3n", "P1Y": "1y",
	}
	unit, ok := units[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for taosws", grain)
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		return fmt.Sprintf("TIMETRUNCATE(%s, %s, '%s')", col, unit, tz), nil
	}
	return fmt.Sprintf("TIMETRUNCATE(%s, %s)", col, unit), nil
}

// odpsTimeGrain renders MaxCompute's DATETRUNC function.
func odpsTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("odps: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	units := map[string]string{
		"PT1S": "second", "PT1M": "minute", "PT1H": "hour", "P1D": "day",
		"P1W": "week", "P1M": "month", "P3M": "quarter", "P1Y": "year",
	}
	unit, ok := units[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for odps", grain)
	}
	return fmt.Sprintf("DATETRUNC(%s, '%s')", col, unit), nil
}

// couchbaseTimeGrain renders N1QL's DATE_TRUNC_STR function.
func couchbaseTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("couchbase: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	units := map[string]string{
		"PT1S": "second", "PT1M": "minute", "PT1H": "hour", "P1D": "day",
		"P1W": "week", "P1M": "month", "P3M": "quarter", "P1Y": "year",
	}
	unit, ok := units[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for couchbase", grain)
	}
	return fmt.Sprintf("DATE_TRUNC_STR(%s, \"%s\")", col, unit), nil
}

// yqlTimeGrain renders YQL's DateTime:: functions.
func yqlTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("yql: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("DateTime::Parse(\"%%Y-%%m-%%dT%%H:%%M:%%SZ\", DateTime::Format(\"%%Y-%%m-%%dT%%H:%%M:%%SZ\", %s))", col), nil
	case "PT1M":
		return fmt.Sprintf("DateTime::Parse(\"%%Y-%%m-%%dT%%H:%%M:00Z\", DateTime::Format(\"%%Y-%%m-%%dT%%H:%%M:00Z\", %s))", col), nil
	case "PT1H":
		return fmt.Sprintf("DateTime::Parse(\"%%Y-%%m-%%dT%%H:00:00Z\", DateTime::Format(\"%%Y-%%m-%%dT%%H:00:00Z\", %s))", col), nil
	case "P1D":
		return fmt.Sprintf("DateTime::StartOfDay(%s)", col), nil
	case "P1W":
		return fmt.Sprintf("DateTime::StartOfWeek(%s)", col), nil
	case "P1M":
		return fmt.Sprintf("DateTime::StartOfMonth(%s)", col), nil
	case "P3M":
		return fmt.Sprintf("DateTime::StartOfQuarter(%s)", col), nil
	case "P1Y":
		return fmt.Sprintf("DateTime::StartOfYear(%s)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for yql", grain)
}

// iotdbTimeGrain renders IoTDB's date_bin function.
func iotdbTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("iotdb: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	intervals := map[string]string{
		"PT1S": "1s", "PT1M": "1m", "PT5M": "5m", "PT10M": "10m", "PT15M": "15m",
		"PT30M": "30m", "PT1H": "1h", "PT6H": "6h", "P1D": "1d", "P1W": "7d",
		"P1M": "30d", "P3M": "90d", "P1Y": "365d",
	}
	interval, ok := intervals[norm]
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q for iotdb", grain)
	}
	return fmt.Sprintf("date_bin(interval '%s', %s, '1970-01-01T00:00:00')", interval, col), nil
}

// firebirdTimeGrain renders DATEADD based truncation (Firebird has no
// DATE_TRUNC).
func firebirdTimeGrain(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("firebird: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("DATEADD(MILLISECOND, -EXTRACT(MILLISECOND FROM %s), %s)", col, col), nil
	case "PT1M":
		return fmt.Sprintf("DATEADD(SECOND, -EXTRACT(SECOND FROM %[1]s), %[1]s)", col), nil
	case "PT1H":
		return fmt.Sprintf("DATEADD(MINUTE, -EXTRACT(MINUTE FROM %[1]s), DATEADD(SECOND, -EXTRACT(SECOND FROM %[1]s), %[1]s))", col), nil
	case "P1D":
		return fmt.Sprintf("CAST(%s AS DATE)", col), nil
	case "P1W":
		return fmt.Sprintf("DATEADD(DAY, 1 - EXTRACT(WEEKDAY FROM %[1]s), CAST(%[1]s AS DATE))", col), nil
	case "P1M":
		return fmt.Sprintf("CAST(%[1]s - (EXTRACT(DAY FROM %[1]s) - 1) AS DATE)", col), nil
	case "P3M":
		return fmt.Sprintf("CAST(EXTRACT(YEAR FROM %[1]s) || '-' || LPAD(CAST(3 * ((CAST(EXTRACT(MONTH FROM %[1]s) AS INTEGER) - 1) / 3) + 1 AS VARCHAR(2)), 2, '0') || '-01' AS DATE)", col), nil
	case "P1Y":
		return fmt.Sprintf("CAST(EXTRACT(YEAR FROM %[1]s) || '-01-01' AS DATE)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for firebird", grain)
}
