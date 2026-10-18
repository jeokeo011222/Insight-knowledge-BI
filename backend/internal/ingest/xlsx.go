package ingest

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

// SheetNames lists the worksheets of an Excel payload of either flavour. The
// zipped .xlsx and the legacy binary .xls are told apart by their signature.
func SheetNames(data []byte) ([]string, error) {
	if looksLikeXLS(data) {
		return XLSSheetNames(data)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not open the workbook: %w", err)
	}
	defer func() { _ = f.Close() }()
	return f.GetSheetList(), nil
}

// ReadXLSX parses the first worksheet of an .xlsx workbook into a Table. The
// first row is treated as the header. Legacy binary .xls workbooks are
// detected by their OLE2 signature and transparently routed to ReadXLS.
func ReadXLSX(data []byte) (*Table, error) {
	return ReadXLSXSheet(data, "")
}

// ReadXLSXSheet parses the named worksheet ("" selects the first) of an .xlsx
// workbook into a Table.
func ReadXLSXSheet(data []byte, sheet string) (*Table, error) {
	if looksLikeXLS(data) {
		return ReadXLSSheet(data, sheet)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not open the workbook: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("the workbook has no worksheets")
	}
	target := sheets[0]
	if name := strings.TrimSpace(sheet); name != "" {
		found := false
		for _, candidate := range sheets {
			if candidate == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("the workbook has no worksheet named %q", name)
		}
		target = name
	}

	rows, err := f.GetRows(target)
	if err != nil {
		return nil, fmt.Errorf("could not read worksheet %q: %w", target, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("worksheet %q is empty", target)
	}
	header := normalizeHeader(rows[0])
	return buildTable(header, rows[1:]), nil
}
