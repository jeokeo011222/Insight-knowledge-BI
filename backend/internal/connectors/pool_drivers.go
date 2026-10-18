package connectors

// Driver registration for every engine whose Go driver is compiled into this
// build. Engine specs refer to these by the names registered in each driver's
// init function:
//
//	pgx        -> PostgreSQL protocol family (pgx stdlib)
//	mysql      -> MySQL protocol family (go-sql-driver/mysql, imported by engines_mysql.go)
//	sqlite     -> modernc.org/sqlite (pure Go)
//	clickhouse -> ClickHouse/clickhouse-go/v2
//	sqlserver  -> microsoft/go-mssqldb
//	oracle     -> sijms/go-ora/v2
//	trino      -> trinodb/trino-go-client (also used for Presto)
//	vertica    -> vertica/vertica-sql-go
//	exasol     -> exasol/exasol-driver-go (imported by engines_warehouse.go)
//	hdb        -> SAP/go-hdb
//	httpsql    -> internal HTTP JSON driver (httpsqldriver.go)
import (
	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/SAP/go-hdb/driver"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "github.com/trinodb/trino-go-client/trino"
	_ "github.com/vertica/vertica-sql-go"
	_ "modernc.org/sqlite"
)
