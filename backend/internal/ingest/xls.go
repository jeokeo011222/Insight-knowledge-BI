package ingest

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/shakinm/xlsReader/xls"
)

// xlsOLE2Magic is the compound-file signature shared by every legacy binary
// Excel workbook (.xls / BIFF8).
var xlsOLE2Magic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// looksLikeXLS reports whether the payload starts with the OLE2 magic bytes,
// which is how legacy .xls files are distinguished from zipped .xlsx files.
func looksLikeXLS(data []byte) bool {
	return len(data) >= len(xlsOLE2Magic) && bytes.Equal(data[:len(xlsOLE2Magic)], xlsOLE2Magic)
}

// xlsCell is the subset of the reader's cell interface used for conversion.
type xlsCell interface {
	GetString() string
	GetFloat64() float64
	GetType() string
	GetXFIndex() int
}

// XLSSheetNames lists the worksheet names of a legacy binary .xls workbook.
func XLSSheetNames(data []byte) ([]string, error) {
	wb, err := xls.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not open the .xls workbook: %w", err)
	}
	names := make([]string, 0, wb.GetNumberSheets())
	for i := 0; i < wb.GetNumberSheets(); i++ {
		sheet, err := wb.GetSheet(i)
		if err != nil {
			continue
		}
		names = append(names, sheet.GetName())
	}
	return names, nil
}

// ReadXLS parses the first worksheet of a legacy binary .xls workbook into a
// Table. The first row is treated as the header.
func ReadXLS(data []byte) (*Table, error) {
	return ReadXLSSheet(data, "")
}

// ReadXLSSheet parses the named worksheet ("" selects the first) of a legacy
// binary .xls workbook into a Table.
func ReadXLSSheet(data []byte, sheetName string) (*Table, error) {
	wb, err := xls.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("could not open the .xls workbook: %w", err)
	}
	if wb.GetNumberSheets() == 0 {
		return nil, fmt.Errorf("the workbook has no worksheets")
	}

	index := 0
	if name := strings.TrimSpace(sheetName); name != "" {
		index = -1
		for i := 0; i < wb.GetNumberSheets(); i++ {
			candidate, err := wb.GetSheet(i)
			if err != nil {
				continue
			}
			if candidate.GetName() == name {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, fmt.Errorf("the workbook has no worksheet named %q", name)
		}
	}

	sheet, err := wb.GetSheet(index)
	if err != nil {
		return nil, fmt.Errorf("could not read the worksheet: %w", err)
	}

	rows := readXLSSheet(wb, sheet)
	if len(rows) == 0 {
		return nil, fmt.Errorf("worksheet %q is empty", sheet.GetName())
	}
	header := normalizeHeader(rows[0])
	return buildTable(header, rows[1:]), nil
}

// readXLSSheet materialises every row of a worksheet as string cells. Numeric
// cells are rendered from their raw value, and those whose display format is a
// date are rendered as dates.
func readXLSSheet(wb xls.Workbook, sheet *xls.Sheet) [][]string {
	rows := make([][]string, 0, sheet.GetNumberRows())
	for _, r := range sheet.GetRows() {
		cols := r.GetCols()
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = xlsCellString(wb, c)
		}
		rows = append(rows, cells)
	}
	return rows
}

// xlsCellString renders a single cell, converting date-formatted numbers into a
// readable timestamp so type inference picks them up as dttm columns.
func xlsCellString(wb xls.Workbook, c xlsCell) string {
	typ := c.GetType()
	if strings.HasSuffix(typ, ".Number") || strings.HasSuffix(typ, ".Rk") {
		if isXLSDateFormatted(wb, c) {
			return xlsTimeString(c.GetFloat64())
		}
	}
	return c.GetString()
}

// isXLSDateFormatted reports whether the cell's XF references a date/time
// display format (built-in ranges 14–22 and 45–47, or a custom date pattern).
func isXLSDateFormatted(wb xls.Workbook, c xlsCell) bool {
	xf := wb.GetXFbyIndex(c.GetXFIndex())
	idx := xf.GetFormatIndex()
	if (idx >= 14 && idx <= 22) || (idx >= 45 && idx <= 47) {
		return true
	}
	if idx >= 164 {
		f := wb.GetFormatByIndex(idx)
		return looksLikeXLSDateFormat(f.String())
	}
	return false
}

// looksLikeXLSDateFormat heuristically detects a date/time pattern, ignoring
// quoted literals and escaped characters.
func looksLikeXLSDateFormat(format string) bool {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '"':
			i++
			for i < len(format) && format[i] != '"' {
				i++
			}
		case '\\':
			i++
		default:
			b.WriteByte(format[i])
		}
	}
	s := strings.ToLower(b.String())
	if s == "" || s == "general" || s == "@" {
		return false
	}
	return strings.ContainsAny(s, "yd") || strings.Contains(s, "h:mm") || strings.Contains(s, "[h]")
}

// xlsTimeString converts an Excel serial number to a "date" or "date time"
// string; the fractional part is dropped when the time is exactly midnight.
func xlsTimeString(serial float64) string {
	base := time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)
	days := int(serial)
	frac := serial - float64(days)
	t := base.AddDate(0, 0, days).Add(time.Duration(frac * float64(24*time.Hour)))
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04:05")
}
