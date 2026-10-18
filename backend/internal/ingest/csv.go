package ingest

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// ReadCSV parses a delimited text file (CSV/TSV/…) into a Table. The delimiter
// is sniffed from the first line and the first record is treated as the header.
func ReadCSV(r io.Reader) (*Table, error) {
	br := bufio.NewReaderSize(r, 1<<16)
	delim := sniffDelimiter(br)

	reader := csv.NewReader(br)
	reader.Comma = delim
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not parse the delimited file: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("the file is empty")
	}

	header := normalizeHeader(records[0])
	return buildTable(header, records[1:]), nil
}

// sniffDelimiter guesses the field separator from the first line by counting the
// usual suspects; commas win when nothing else appears.
func sniffDelimiter(br *bufio.Reader) rune {
	peek, _ := br.Peek(1 << 16)
	line := peek
	if idx := bytes.IndexByte(peek, '\n'); idx >= 0 {
		line = peek[:idx]
	}
	line = bytes.TrimPrefix(line, []byte{0xEF, 0xBB, 0xBF})

	best := ','
	bestCount := 0
	for _, candidate := range []rune{',', '\t', ';', '|'} {
		if count := bytes.Count(line, []byte(string(candidate))); count > bestCount {
			best, bestCount = candidate, count
		}
	}
	return best
}

// normalizeHeader trims the header cells, replaces blanks and de-duplicates the
// resulting names so every column is addressable.
func normalizeHeader(record []string) []string {
	names := make([]string, len(record))
	for i, cell := range record {
		name := strings.TrimSpace(strings.TrimPrefix(cell, "\ufeff"))
		if name == "" {
			name = fmt.Sprintf("column_%d", i+1)
		}
		names[i] = name
	}
	return UniqueNames(names)
}

// buildTable turns string records into a typed Table. Columns are inferred from
// the whole column, then each cell is coerced into the column's type.
func buildTable(header []string, body [][]string) *Table {
	n := len(header)
	if n == 0 {
		return &Table{}
	}
	raw := make([][]any, len(body))
	for i, record := range body {
		row := make([]any, n)
		for j := 0; j < n; j++ {
			if j < len(record) {
				row[j] = strings.TrimSpace(record[j])
			}
		}
		raw[i] = row
	}
	return typedTable(header, raw)
}

// typedTable infers a column type from the raw column values and coerces every
// cell accordingly. Shared by the CSV and XLSX readers.
func typedTable(header []string, raw [][]any) *Table {
	n := len(header)
	columns := make([]Column, n)
	for j := 0; j < n; j++ {
		values := make([]any, len(raw))
		for i := range raw {
			values[i] = raw[i][j]
		}
		typ, isDttm := InferType(values)
		columns[j] = Column{Name: header[j], Type: typ, IsDttm: isDttm}
	}
	rows := make([][]any, len(raw))
	for i := range raw {
		row := make([]any, n)
		for j := 0; j < n; j++ {
			row[j] = Coerce(columns[j].Type, raw[i][j])
		}
		rows[i] = row
	}
	return &Table{Columns: columns, Rows: rows}
}
