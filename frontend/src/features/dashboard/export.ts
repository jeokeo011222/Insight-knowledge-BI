/**
 * Client-side export helpers for chart tiles.
 *
 * CSV comes straight from the query result the tile already holds, so an
 * export never costs another query. PNG is produced from the rendered ECharts
 * SVG (the whole app renders charts with `renderer: 'svg'`), which keeps the
 * export free of the canvas taint problems and works for every viz type that
 * draws through ECharts.
 */
import type { QueryResultItem } from '../../api/types';

/** Replaces characters that break download names on Windows. */
export function safeFilename(name: string, fallback = 'chart'): string {
  const cleaned = name
    .replace(/[\\/:*?"<>|\u0000-\u001f]+/g, '-')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^[-.]+|[-.]+$/g, '');
  return (cleaned || fallback).slice(0, 80);
}

function timestamp(): string {
  const now = new Date();
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}`;
}

/** Column names for a result, falling back to the first row's keys. */
export function resultColumns(result: QueryResultItem): string[] {
  if (result.colnames?.length) return result.colnames;
  const rows = Array.isArray(result.data) ? result.data : [];
  return rows.length ? Object.keys(rows[0]) : [];
}

function csvCell(value: unknown): string {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'object' ? JSON.stringify(value) : String(value);
  return /[",\n\r]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

/** RFC 4180 CSV with a UTF-8 BOM so Excel opens Chinese columns correctly. */
export function buildCsv(result: QueryResultItem): string {
  const columns = resultColumns(result);
  const rows = Array.isArray(result.data) ? result.data : [];
  const lines = [columns.map(csvCell).join(',')];
  rows.forEach(row => {
    lines.push(
      columns
        .map(column => csvCell((row as Record<string, unknown>)[column]))
        .join(','),
    );
  });
  return `\uFEFF${lines.join('\r\n')}`;
}

/** Triggers a browser download for an in-memory blob. */
export function downloadBlobFile(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  // Revoking immediately can cancel the download in some browsers.
  window.setTimeout(() => URL.revokeObjectURL(url), 4000);
}

export function exportCsv(result: QueryResultItem, name: string): void {
  const blob = new Blob([buildCsv(result)], { type: 'text/csv;charset=utf-8' });
  downloadBlobFile(blob, `${safeFilename(name)}-${timestamp()}.csv`);
}

/** Draws a serialised SVG onto a canvas and returns a PNG blob. */
async function svgToPng(svg: SVGSVGElement, scale = 2, background = '#ffffff'): Promise<Blob> {
  const rect = svg.getBoundingClientRect();
  const width = Math.max(1, Math.round(rect.width || svg.clientWidth || 640));
  const height = Math.max(1, Math.round(rect.height || svg.clientHeight || 360));

  const clone = svg.cloneNode(true) as SVGSVGElement;
  clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg');
  clone.setAttribute('width', String(width));
  clone.setAttribute('height', String(height));
  if (!clone.getAttribute('viewBox')) {
    clone.setAttribute('viewBox', `0 0 ${width} ${height}`);
  }

  const source = new XMLSerializer().serializeToString(clone);
  const svgUrl = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(source)}`;

  const image = new Image();
  image.decoding = 'sync';
  await new Promise<void>((resolve, reject) => {
    image.onload = () => resolve();
    image.onerror = () => reject(new Error('svg render failed'));
    image.src = svgUrl;
  });

  const canvas = document.createElement('canvas');
  canvas.width = width * scale;
  canvas.height = height * scale;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('canvas unavailable');
  ctx.fillStyle = background;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  ctx.drawImage(image, 0, 0, canvas.width, canvas.height);

  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(blob => {
      if (blob) resolve(blob);
      else reject(new Error('png encode failed'));
    }, 'image/png');
  });
}

/**
 * Exports the chart rendered inside `element` as PNG.
 *
 * Returns false when the tile holds no SVG (a plain table), so the caller can
 * disable the menu item instead of downloading an empty image.
 */
export async function exportElementPng(
  element: HTMLElement | null,
  name: string,
  background = '#ffffff',
): Promise<boolean> {
  const svg = element?.querySelector('svg');
  if (!svg) return false;
  const blob = await svgToPng(svg as SVGSVGElement, 2, background);
  downloadBlobFile(blob, `${safeFilename(name)}-${timestamp()}.png`);
  return true;
}

/** True when the tile renders through ECharts (so PNG export makes sense). */
export function hasExportableChart(element: HTMLElement | null): boolean {
  return Boolean(element?.querySelector('svg'));
}
