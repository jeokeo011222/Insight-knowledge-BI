// Package ingest turns external data sources — uploaded CSV/Excel files and
// JSON-over-HTTP REST APIs — into tabular data that is materialised into the
// managed SQLite store and then exposed as ordinary Superset datasets.
//
// Every source funnels through the same Table representation so the API layer
// can persist it without caring where the rows came from.
package ingest

// Column describes one column of a parsed or fetched table. Type carries a
// generic SQL type (VARCHAR/INTEGER/FLOAT/BOOLEAN/TIMESTAMP) so the SQLite
// writer and the dataset metadata share one vocabulary.
type Column struct {
	Name   string
	Type   string
	IsDttm bool
}

// Table is the tabular result shared by every ingestion source.
type Table struct {
	Columns []Column
	Rows    [][]any
}

// RowCount returns the number of data rows.
func (t *Table) RowCount() int {
	if t == nil {
		return 0
	}
	return len(t.Rows)
}
