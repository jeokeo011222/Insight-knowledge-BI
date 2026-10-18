/**
 * RFC-4180 CSV export used by the list pages and chart tiles. Keeping it in one
 * place means quoting/encoding (Excel + UTF-8 BOM) stays consistent everywhere.
 */

export interface CsvColumn<T> {
  key: string;
  title: string;
  value: (row: T) => unknown;
}

/** Escapes one cell: quotes doubled, wrapped when it contains a delimiter. */
function cell(value: unknown): string {
  if (value === null || value === undefined) return '';
  let text: string;
  if (value instanceof Date) {
    text = value.toISOString();
  } else if (typeof value === 'object') {
    try {
      text = JSON.stringify(value);
    } catch {
      text = String(value);
    }
  } else {
    text = String(value);
  }
  if (/[",\n\r]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`;
  }
  return text;
}

/** Builds the CSV body (header + rows). */
export function toCsv<T>(rows: T[], columns: CsvColumn<T>[]): string {
  const header = columns.map(column => cell(column.title)).join(',');
  const body = rows.map(row => columns.map(column => cell(column.value(row))).join(','));
  return [header, ...body].join('\r\n');
}

/** Triggers a browser download of `text` as `filename`. */
export function downloadText(filename: string, text: string, mime = 'text/csv;charset=utf-8'): void {
  // The BOM makes Excel read the file as UTF-8 instead of the ANSI codepage.
  const blob = new Blob(['\uFEFF', text], { type: mime });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  // Revoke on the next tick so Safari has time to start the download.
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** Convenience: build and download in one call. */
export function downloadCsv<T>(filename: string, rows: T[], columns: CsvColumn<T>[]): void {
  downloadText(filename, toCsv(rows, columns));
}
