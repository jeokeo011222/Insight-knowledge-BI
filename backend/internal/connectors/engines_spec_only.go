package connectors

import "fmt"

// This file registers every engine that Superset ships a spec for but that
// cannot be opened in this build: the dialect is complete (quoting, time
// grains, metadata queries, limit syntax) while DSN/Open return
// ErrDriverUnavailable / ErrDriverNotBundled instead of pretending to work.

func init() {
	Register(newSpecOnly(engineDef{
		engine: "monetdb", name: "MonetDB", driver: "pymonetdb", port: 50000,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "monetdb")
		},
		schemas: `SELECT name AS schema_name FROM sys.schemas ORDER BY name`,
		tables: `SELECT t.name AS table_name, 'TABLE' AS table_type
		         FROM sys.tables t JOIN sys.schemas s ON t.schema_id = s.id
		         WHERE s.name = '%s' ORDER BY t.name`,
		columns: `SELECT c.name AS column_name, c.type AS data_type, 'YES' AS is_nullable
		          FROM sys.columns c
		          JOIN sys.tables t ON c.table_id = t.id
		          JOIN sys.schemas s ON t.schema_id = s.id
		          WHERE s.name = '%s' AND t.name = '%s' ORDER BY c.number`,
		limit: sqlLimit,
	}))

	// TDengine speaks its own SQL dialect and defaults to the taosws driver.
	Register(newSpecOnly(engineDef{
		engine: "taosws", name: "TDengine", driver: "taosws", port: 6041,
		quote: "`", aliasInSelect: true,
		grain:   tdengineTimeGrain,
		schemas: `SELECT name AS schema_name FROM information_schema.ins_databases`,
		tables: `SELECT table_name, IFNULL(type, 'TABLE') AS table_type
		         FROM information_schema.ins_tables WHERE db_name = '%s' ORDER BY table_name`,
		columns: `SELECT col_name AS column_name, type AS data_type, '' AS is_nullable
		          FROM information_schema.ins_columns
		          WHERE db_name = '%s' AND table_name = '%s' ORDER BY col_idx`,
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("TO_TIMESTAMP(%s * 1000)", col) },
	}))

	Register(newSpecOnly(engineDef{
		engine: "sybase", name: "SAP Sybase", driver: "pyodbc", port: 5000,
		quote: `"`,
		grain: mssqlTimeGrain,
		schemas: `SELECT name AS schema_name FROM sysusers
		          WHERE name NOT IN ('dbo','guest','sys','public') ORDER BY name`,
		tables: `SELECT o.name AS table_name, 'TABLE' AS table_type
		         FROM sysobjects o JOIN sysusers u ON o.uid = u.uid
		         WHERE u.name = '%s' AND o.type = 'U' ORDER BY o.name`,
		columns: `SELECT c.name AS column_name, t.name AS data_type, 'YES' AS is_nullable
		          FROM syscolumns c
		          JOIN systypes t ON c.usertype = t.usertype
		          JOIN sysobjects o ON c.id = o.id
		          JOIN sysusers u ON o.uid = u.uid
		          WHERE u.name = '%s' AND o.name = '%s' ORDER BY c.colid`,
		limit: sqlLimit,
		epoch: func(col string) string {
			return fmt.Sprintf("DATEADD(SECOND, %s, '1970-01-01')", col)
		},
	}))

	Register(newSpecOnly(engineDef{
		engine: "db2", name: "IBM Db2", driver: "ibm_db_sa", port: 50000,
		quote: `"`, defaultSchema: "",
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "db2")
		},
		schemas: `SELECT SCHEMANAME AS schema_name FROM SYSCAT.SCHEMATA
		          WHERE SCHEMANAME NOT LIKE 'SYS%' ORDER BY SCHEMANAME`,
		tables: `SELECT TABNAME AS table_name, TYPE AS table_type FROM SYSCAT.TABLES
		         WHERE TABSCHEMA = '%s' ORDER BY TABNAME`,
		columns: `SELECT COLNAME AS column_name, TYPENAME AS data_type,
		                 NULLS AS is_nullable, DEFAULT AS column_default
		          FROM SYSCAT.COLUMNS WHERE TABSCHEMA = '%s' AND TABNAME = '%s'
		          ORDER BY COLNO`,
		limit: fetchFirstLimit,
		epoch: func(col string) string {
			return fmt.Sprintf("(TIMESTAMP('1970-01-01-00.00.00.000000') + %s SECONDS)", col)
		},
	}))

	Register(newSpecOnly(engineDef{
		engine: "ibmi", name: "IBM Db2 for i", driver: "ibm_db_sa", port: 446,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "ibmi")
		},
		schemas: `SELECT SCHEMA_NAME AS schema_name FROM QSYS2.SYSSCHEMAS
		          WHERE SCHEMA_NAME NOT LIKE 'Q%' ORDER BY SCHEMA_NAME`,
		tables: `SELECT TABLE_NAME AS table_name, TABLE_TYPE AS table_type
		         FROM QSYS2.SYSTABLES WHERE TABLE_SCHEMA = '%s' ORDER BY TABLE_NAME`,
		columns: `SELECT COLUMN_NAME AS column_name, DATA_TYPE AS data_type,
		                 IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default
		          FROM QSYS2.SYSCOLUMNS
		          WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
		          ORDER BY ORDINAL_POSITION`,
		limit: fetchFirstLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "netezza", name: "IBM Netezza", driver: "nzpy", port: 5480,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "netezza")
		},
		schemas: `SELECT SCHEMA AS schema_name FROM _V_SCHEMA ORDER BY SCHEMA`,
		tables: `SELECT TABLENAME AS table_name, 'TABLE' AS table_type
		         FROM _V_TABLE WHERE SCHEMA = '%s' ORDER BY TABLENAME`,
		columns: `SELECT ATTNAME AS column_name, FORMAT_TYPE AS data_type, '' AS is_nullable
		          FROM _V_RELATION_COLUMN
		          WHERE SCHEMA = '%s' AND RELATION = '%s' ORDER BY ATTNUM`,
		limit: sqlLimit,
		epoch: func(col string) string {
			return fmt.Sprintf("(TIMESTAMP 'epoch' + %s * INTERVAL '1 second')", col)
		},
	}))

	Register(newSpecOnly(engineDef{
		engine: "teradatasql", name: "Teradata", driver: "teradatasql", port: 1025,
		quote:   `"`,
		grain:   teradataTimeGrain,
		schemas: `SELECT DatabaseName AS schema_name FROM DBC.DatabasesV ORDER BY DatabaseName`,
		tables: `SELECT TableName AS table_name, 'TABLE' AS table_type FROM DBC.TablesV
		         WHERE DatabaseName = '%s' AND TableKind IN ('T','V') ORDER BY TableName`,
		columns: `SELECT ColumnName AS column_name, ColumnType AS data_type,
		                 Nullable AS is_nullable, DefaultValue AS column_default
		          FROM DBC.ColumnsV
		          WHERE DatabaseName = '%s' AND TableName = '%s' ORDER BY ColumnId`,
		limit: fetchFirstLimit,
		epoch: func(col string) string {
			return fmt.Sprintf("CAST(DATE '1970-01-01' + (%s / 86400) AS TIMESTAMP(0))", col)
		},
	}))

	Register(newSpecOnly(engineDef{
		engine: "databricks", name: "Databricks", driver: "databricks-sql-python", port: 443,
		quote: "`", aliasInSelect: true, maxName: 255,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "databricks")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("timestamp_seconds(%s)", col) },
		info:  map[string]any{"supports_dynamic_catalog": true},
	}))

	Register(newSpecOnly(engineDef{
		engine: "datafusion", name: "Apache DataFusion", driver: "flightsql", port: 50051,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "datafusion")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "dremio", name: "Dremio", driver: "flight", port: 9047,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "dremio")
		},
		schemas: `SELECT schema_name FROM INFORMATION_SCHEMA.SCHEMATA ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM INFORMATION_SCHEMA."TABLES"
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type,
		                 CASE WHEN is_nullable = 'YES' THEN 'YES' ELSE 'NO' END AS is_nullable,
		                 column_default
		          FROM INFORMATION_SCHEMA.COLUMNS
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
		info:  map[string]any{"supports_dynamic_catalog": true},
	}))

	Register(newSpecOnly(engineDef{
		engine: "drill", name: "Apache Drill", driver: "sadrill", port: 8047,
		quote: "`",
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "drill")
		},
		schemas: `SELECT schema_name FROM INFORMATION_SCHEMA.SCHEMATA ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM INFORMATION_SCHEMA."TABLES"
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM INFORMATION_SCHEMA.COLUMNS
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "hive", name: "Apache Hive", driver: "pyhive", port: 10000,
		quote: "`", aliasInSelect: true,
		grain: hiveTimeGrain,
		schemas: `SELECT schema_name FROM information_schema.schemata
		          WHERE schema_name <> 'information_schema' ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "impala", name: "Apache Impala", driver: "impala", port: 21050,
		quote: "`", aliasInSelect: true, maxName: 128,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "impala")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata
		          WHERE schema_name NOT IN ('information_schema','sys') ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	// Spark SQL has no portable INFORMATION_SCHEMA; metadata discovery is
	// reported as empty rather than faked.
	Register(newSpecOnly(engineDef{
		engine: "spark", name: "Apache Spark SQL", driver: "pyhive", port: 10000,
		quote: "`", aliasInSelect: true,
		grain: hiveTimeGrain,
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("timestamp_seconds(%s)", col) },
	}))

	Register(newSpecOnly(engineDef{
		engine: "phoenix", name: "Apache Phoenix", driver: "phoenixdb", port: 8765,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "phoenix")
		},
		schemas: `SELECT DISTINCT TABLE_SCHEM AS schema_name FROM SYSTEM.CATALOG
		          WHERE TABLE_SCHEM IS NOT NULL ORDER BY TABLE_SCHEM`,
		tables: `SELECT TABLE_NAME AS table_name, 'TABLE' AS table_type
		         FROM SYSTEM.CATALOG
		         WHERE TABLE_SCHEM = '%s' AND COLUMN_NAME IS NULL AND TABLE_TYPE IS NOT NULL
		         ORDER BY TABLE_NAME`,
		columns: `SELECT COLUMN_NAME AS column_name, DATA_TYPE AS data_type,
		                 NULLABLE AS is_nullable, COLUMN_DEF AS column_default
		          FROM SYSTEM.CATALOG
		          WHERE TABLE_SCHEM = '%s' AND TABLE_NAME = '%s'
		          ORDER BY ORDINAL_POSITION`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "kustosql", name: "Azure Data Explorer", driver: "kusto", port: 443,
		quote: `"`, aliasInSelect: false,
		grain: kustoTimeGrain,
		limit: func(n int) string {
			if n <= 0 {
				return ""
			}
			return fmt.Sprintf("| take %d", n)
		},
		epoch: func(col string) string {
			return fmt.Sprintf("unixtime_seconds_todatetime(%s)", col)
		},
	}))

	// Non-relational engines: they are registered so the connection form can
	// list them, but SQL-only behaviour (time grains) is explicitly refused.
	Register(newSpecOnly(engineDef{
		engine: "solr", name: "Apache Solr", driver: "sqlalchemy_solr", port: 8983,
		quote: `"`, noJoins: true,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "mongodb", name: "MongoDB", driver: "pymongo", port: 27017,
		quote: `"`, noJoins: true,
		limit: func(int) string { return "" },
	}))

	Register(newSpecOnly(engineDef{
		engine: "dynamodb", name: "Amazon DynamoDB", driver: "boto3", port: 443,
		quote: `"`, noJoins: true,
		limit: func(int) string { return "" },
	}))

	Register(newSpecOnly(engineDef{
		engine: "couchbase", name: "Couchbase", driver: "couchbase", port: 8091,
		quote: "`", noJoins: true,
		grain: couchbaseTimeGrain,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "datastore", name: "Google Datastore", driver: "datastore", port: 443,
		quote: `"`, noJoins: true,
		limit: func(int) string { return "" },
	}))

	// Cloudflare D1 and Google Sheets both speak SQLite SQL.
	Register(newSpecOnly(engineDef{
		engine: "d1", name: "Cloudflare D1", driver: "httpx", port: 443,
		quote:   `"`,
		grain:   sqliteTimeGrain,
		schemas: `SELECT 'main' AS schema_name`,
		tables: `SELECT name AS table_name, 'table' AS table_type FROM sqlite_master
		         WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`,
		columns: `PRAGMA table_info('%s.%s')`,
		limit:   sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "gsheets", name: "Google Sheets", driver: "apsw", port: 443,
		quote:   `"`,
		grain:   sqliteTimeGrain,
		schemas: `SELECT 'main' AS schema_name`,
		tables: `SELECT name AS table_name, 'table' AS table_type FROM sqlite_master
		         WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`,
		columns: `PRAGMA table_info('%s.%s')`,
		limit:   sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "firebird", name: "Firebird", driver: "fdb", port: 3050,
		quote: `"`,
		grain: firebirdTimeGrain,
		schemas: `SELECT DISTINCT TRIM(RDB$OWNER_NAME) AS schema_name
		          FROM RDB$RELATIONS WHERE RDB$SYSTEM_FLAG = 0 ORDER BY 1`,
		tables: `SELECT TRIM(RDB$RELATION_NAME) AS table_name, 'TABLE' AS table_type
		         FROM RDB$RELATIONS
		         WHERE RDB$SYSTEM_FLAG = 0 AND TRIM(RDB$OWNER_NAME) = '%s' ORDER BY 1`,
		columns: `SELECT TRIM(rf.RDB$FIELD_NAME) AS column_name, '' AS data_type,
		                 'YES' AS is_nullable
		          FROM RDB$RELATION_FIELDS rf
		          WHERE TRIM(rf.RDB$RELATION_NAME) = '%s'
		            AND EXISTS (
		                SELECT 1 FROM RDB$RELATIONS r
		                WHERE r.RDB$RELATION_NAME = rf.RDB$RELATION_NAME
		                  AND TRIM(r.RDB$OWNER_NAME) = '%s')
		          ORDER BY rf.RDB$FIELD_POSITION`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "firebolt", name: "Firebolt", driver: "firebolt", port: 443,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "firebolt")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata
		          WHERE schema_name NOT IN ('information_schema','pg_catalog') ORDER BY 1`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY 1`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s' ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "arc", name: "Arc", driver: "arrow", port: 443,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "arc")
		},
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "ascend", name: "Ascend", driver: "ascend", port: 443,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "ascend")
		},
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "ocient", name: "Ocient", driver: "ocient", port: 443,
		quote: `"`,
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "ocient")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata ORDER BY 1`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY 1`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s' ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "odps", name: "ODPS (MaxCompute)", driver: "odps", port: 443,
		quote: "`", aliasInSelect: true,
		grain:   odpsTimeGrain,
		schemas: `SELECT name AS schema_name FROM information_schema.schemata ORDER BY name`,
		tables: `SELECT table_name, 'TABLE' AS table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s' ORDER BY ordinal_position`,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "iotdb", name: "Apache IoTDB", driver: "iotdb", port: 6667,
		quote: `"`,
		grain: iotdbTimeGrain,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "yql", name: "YDB", driver: "ydb", port: 2135,
		quote: "`",
		grain: yqlTimeGrain,
		limit: sqlLimit,
	}))

	Register(newSpecOnly(engineDef{
		engine: "shillelagh", name: "Shillelagh", driver: "apsw", port: 0,
		quote:   `"`,
		grain:   sqliteTimeGrain,
		schemas: `SELECT 'main' AS schema_name`,
		tables: `SELECT name AS table_name, 'table' AS table_type FROM sqlite_master
		         WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`,
		columns: `PRAGMA table_info('%s.%s')`,
		limit:   sqlLimit,
	}))

	// The Superset metadata database itself is a Postgres instance.
	Register(newSpecOnly(engineDef{
		engine: "superset", name: "Superset meta database", driver: "psycopg2", port: 5432,
		quote: `"`, aliasInSelect: true, defaultSchema: "public",
		grain: func(col, grain, tz string) (string, error) {
			return pgTimeGrain(col, grain, tz, "superset")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata
		          WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast')
		          ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
		info:  map[string]any{"supports_dynamic_catalog": true},
	}))
}
