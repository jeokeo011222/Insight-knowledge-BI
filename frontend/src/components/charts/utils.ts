/**
 * Core helpers for the visualisation layer: value coercion, formatting,
 * axis theming and explore form-data access.
 */
import dayjs from 'dayjs';
import type { Dayjs } from 'dayjs';
import type { ReactElement } from 'react';
import type { GridComponentOption, XAXisComponentOption, YAXisComponentOption } from 'echarts';
import type { ChartAnnotation } from '../../api/types';

export type Row = Record<string, unknown>;

export interface VizContext {
  rows: Row[];
  colnames: string[];
  coltypes: string[];
  /** Registry key of the visualisation, so a renderer can adapt to its type. */
  vizType: string;
  formData: Record<string, unknown>;
  height: number;
  compact: boolean;
  /** Events to overlay on a time axis (annotation layers selected in Explore). */
  annotations?: ChartAnnotation[];
  onClickData?: (params: unknown) => void;
}

export type VizRenderer = (props: { ctx: VizContext }) => ReactElement | null;

/* ------------------------------------------------------------------ theme */

export const CHART_COLORS = [
  '#1fa8c9',
  '#454e7c',
  '#5ac189',
  '#ff7f44',
  '#666666',
  '#e04355',
  '#fcc700',
  '#a868b7',
  '#3cc7c5',
  '#d1c6bc',
];

export const PRIMARY = '#1b5fd9';
export const FONT_SIZE = 11;

/**
 * Axis / grid / tooltip palette. These are module-level `let` bindings so the
 * shared renderers can pick up theme changes without threading a theme prop
 * through every one of them; `syncChartTheme` (called by the app ThemeProvider
 * before the tree renders) swaps them between light and dark.
 */
export let AXIS_COLOR = '#879399';
export let SPLIT_COLOR = '#f0f0f0';
export let AXIS_LINE_COLOR = '#e0e0e0';
/** Strong body text (labels drawn inside marks, table-like figures). */
export let TEXT_STRONG = '#333333';
/** Secondary text (legend labels, captions, muted annotations). */
export let TEXT_MUTED = '#666666';
/** Neutral hairline (graph edges, progress tracks, dividers). */
export let BORDER_COLOR = '#d9d9d9';
/** Subtle panel fill (KPI cards, progress gauges, empty regions). */
export let SUBTLE_BG = '#f4f6f7';
/** Track colour behind a progress/gauge value. */
export let TRACK_COLOR = '#e6eaed';
/** Low end of a heat/calendar gradient (must track the surface colour). */
export let HEAT_LOW_COLOR = '#f1f9fb';
/** Mid stop of a heat/calendar gradient. */
export let HEAT_MID_COLOR = '#9ed4e1';
/** Positive delta (kept distinct from the brand palette). */
export let POSITIVE_COLOR = '#5ac189';
/** Negative delta. */
export let NEGATIVE_COLOR = '#e04355';
/** Accent used for "attention"/warning marks (targets, deltas, alerts). */
export let ACCENT_COLOR = '#ff7f44';
/** Colour used to separate adjacent marks (bars / pie slices) from the backdrop. */
export let MARK_BORDER_COLOR = '#ffffff';
/** Fill of regions with no data on a map visualisation. */
export let MAP_AREA_COLOR = '#eef2f7';
/** Region boundaries on a map visualisation. */
export let MAP_BORDER_COLOR = '#cfd8e3';
/** Low → high gradient of a map's `visualMap`. */
export let MAP_SCALE_COLORS: [string, string] = ['#dbe6f7', '#1b5fd9'];

export interface ChartTooltipStyle {
  backgroundColor: string;
  borderColor: string;
  borderWidth: number;
  padding: [number, number];
  textStyle: { color: string; fontSize: number };
}

export let TOOLTIP_STYLE: ChartTooltipStyle = {
  backgroundColor: 'rgba(255,255,255,0.96)',
  borderColor: '#e0e0e0',
  borderWidth: 1,
  padding: [6, 8],
  textStyle: { color: '#333333', fontSize: FONT_SIZE },
};

/** Swaps the chart palette to match the active app theme. Idempotent. */
export function syncChartTheme(mode: 'light' | 'dark'): void {
  if (mode === 'dark') {
    AXIS_COLOR = '#8ba3b8';
    SPLIT_COLOR = 'rgba(255,255,255,0.09)';
    AXIS_LINE_COLOR = 'rgba(255,255,255,0.16)';
    TEXT_STRONG = '#e8f0f6';
    TEXT_MUTED = '#9fb3c6';
    BORDER_COLOR = 'rgba(255,255,255,0.18)';
    SUBTLE_BG = 'rgba(255,255,255,0.06)';
    TRACK_COLOR = 'rgba(255,255,255,0.14)';
    HEAT_LOW_COLOR = '#16293e';
    HEAT_MID_COLOR = '#1d5c78';
    POSITIVE_COLOR = '#4ecb8d';
    NEGATIVE_COLOR = '#ff6b7a';
    ACCENT_COLOR = '#ffa06b';
    MARK_BORDER_COLOR = '#16293e';
    MAP_AREA_COLOR = '#16293e';
    MAP_BORDER_COLOR = '#2c4666';
    MAP_SCALE_COLORS = ['#1d3a5c', '#22c3e6'];
    TOOLTIP_STYLE = {
      backgroundColor: 'rgba(22,41,62,0.96)',
      borderColor: '#2c4666',
      borderWidth: 1,
      padding: [6, 8],
      textStyle: { color: '#e8f0f6', fontSize: FONT_SIZE },
    };
    return;
  }
  AXIS_COLOR = '#879399';
  SPLIT_COLOR = '#f0f0f0';
  AXIS_LINE_COLOR = '#e0e0e0';
  TEXT_STRONG = '#333333';
  TEXT_MUTED = '#666666';
  BORDER_COLOR = '#d9d9d9';
  SUBTLE_BG = '#f4f6f7';
  TRACK_COLOR = '#e6eaed';
  HEAT_LOW_COLOR = '#f1f9fb';
  HEAT_MID_COLOR = '#9ed4e1';
  POSITIVE_COLOR = '#5ac189';
  NEGATIVE_COLOR = '#e04355';
  ACCENT_COLOR = '#ff7f44';
  MARK_BORDER_COLOR = '#ffffff';
  MAP_AREA_COLOR = '#eef2f7';
  MAP_BORDER_COLOR = '#cfd8e3';
  MAP_SCALE_COLORS = ['#dbe6f7', '#1b5fd9'];
  TOOLTIP_STYLE = {
    backgroundColor: 'rgba(255,255,255,0.96)',
    borderColor: '#e0e0e0',
    borderWidth: 1,
    padding: [6, 8],
    textStyle: { color: '#333333', fontSize: FONT_SIZE },
  };
}

export function colorAt(index: number): string {
  return CHART_COLORS[index % CHART_COLORS.length];
}

/**
 * Deterministic palette colour for a dimension value (FNV-1a). The same value
 * maps to the same colour on every chart of a dashboard, so a category keeps
 * its identity across charts instead of depending on per-chart row order.
 */
export function stableColor(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return CHART_COLORS[(hash >>> 0) % CHART_COLORS.length];
}

/**
 * Colour for a named series: single-series charts keep the canonical first
 * colour (unchanged look), multi-series charts key off the series name so
 * cross-chart colouring stays consistent (P0-1 主题令牌/稳定着色).
 */
export function seriesColor(name: string, index: number, total: number): string {
  if (total <= 1) return colorAt(0);
  return stableColor(name || String(index));
}

/* ------------------------------------------------------------- conversion */

const PLAIN_NUMBER = /^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$/;

/** Coerces numbers, numeric strings, dates and ISO strings to a finite number. */
export function toNumber(value: unknown): number | null {
  if (value === null || value === undefined) return null;
  if (typeof value === 'number') return Number.isFinite(value) ? value : null;
  if (typeof value === 'bigint') return Number(value);
  if (typeof value === 'boolean') return value ? 1 : 0;
  if (value instanceof Date) {
    const time = value.getTime();
    return Number.isFinite(time) ? time : null;
  }
  if (typeof value !== 'string') return null;
  const text = value.trim();
  if (!text) return null;
  const cleaned = text.replace(/[,\s$€¥]/g, '').replace(/%$/, '');
  if (PLAIN_NUMBER.test(cleaned)) {
    const parsed = Number(cleaned);
    return Number.isFinite(parsed) ? parsed : null;
  }
  const parsedDate = dayjs(text);
  return parsedDate.isValid() ? parsedDate.valueOf() : null;
}

export function asText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (value instanceof Date) return dayjs(value).format('YYYY-MM-DD HH:mm:ss');
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value);
    } catch {
      return String(value);
    }
  }
  return String(value);
}

export function parseTime(value: unknown): Dayjs | null {
  if (value === null || value === undefined || value === '') return null;
  if (value instanceof Date) return dayjs(value);
  if (typeof value === 'number') return dayjs(value);
  if (typeof value !== 'string') return null;
  const parsed = dayjs(value.trim());
  return parsed.isValid() ? parsed : null;
}

/* -------------------------------------------------------------- formatting */

export function formatCompact(value: number): string {
  const abs = Math.abs(value);
  const sign = value < 0 ? '-' : '';
  const scaled = (divisor: number, suffix: string) => {
    const scaledValue = abs / divisor;
    const digits = scaledValue >= 100 ? 0 : scaledValue >= 10 ? 1 : 2;
    return `${sign}${Number(scaledValue.toFixed(digits))}${suffix}`;
  };
  if (!Number.isFinite(value)) return '—';
  if (abs >= 1e12) return scaled(1e12, 'T');
  if (abs >= 1e9) return scaled(1e9, 'B');
  if (abs >= 1e6) return scaled(1e6, 'M');
  if (abs >= 1e3) return scaled(1e3, 'K');
  if (Number.isInteger(value)) return String(value);
  return String(Number(value.toFixed(2)));
}

function digitsOf(format: string): number | undefined {
  const match = format.match(/\.(\d+)/);
  return match ? Number(match[1]) : undefined;
}

/** Builds an axis/tooltip formatter from a Superset `y_axis_format` value. */
export function makeNumberFormatter(format?: string | null): (value: number) => string {
  const spec = (format ?? '').toUpperCase();
  if (spec.includes('%') || spec === 'P') {
    const digits = digitsOf(spec) ?? 1;
    return value => `${value.toFixed(digits)}%`;
  }
  if (spec.includes('$') || spec.includes('CURRENCY')) {
    const digits = digitsOf(spec) ?? 2;
    return value =>
      `$${value.toLocaleString('en-US', {
        minimumFractionDigits: digits,
        maximumFractionDigits: digits,
      })}`;
  }
  if (spec.includes('SMART') || spec === 'S') {
    return value => formatCompact(value);
  }
  const digits = digitsOf(spec);
  return value =>
    value.toLocaleString('en-US', {
      maximumFractionDigits: digits ?? (Number.isInteger(value) ? 0 : 2),
    });
}

export function formatNumber(value: number | null, format?: string | null): string {
  if (value === null || !Number.isFinite(value)) return '—';
  return makeNumberFormatter(format)(value);
}

/* ----------------------------------------------------------------- layout */

export function gridOption(
  compact: boolean,
  extra: Partial<GridComponentOption> = {},
): GridComponentOption {
  return {
    left: compact ? 8 : 12,
    right: compact ? 8 : 16,
    top: compact ? 18 : 26,
    bottom: compact ? 18 : 30,
    containLabel: true,
    ...extra,
  };
}

export function categoryAxis(
  options: {
    data?: string[];
    formatter?: (value: string) => string;
    boundaryGap?: boolean;
  } = {},
): XAXisComponentOption {
  return {
    type: 'category',
    data: options.data,
    boundaryGap: options.boundaryGap ?? true,
    axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
    axisTick: { show: false },
    axisLabel: {
      color: AXIS_COLOR,
      fontSize: FONT_SIZE,
      hideOverlap: true,
      // Long labels are truncated in place (full text stays in the tooltip)
      // instead of overlapping into an unreadable smear.
      width: 96,
      overflow: 'truncate',
      formatter: options.formatter
        ? (value: unknown) => options.formatter?.(String(value)) ?? String(value)
        : undefined,
    },
  };
}

export function valueAxis(
  options: {
    formatter?: (value: number) => string;
    log?: boolean;
    max?: number;
    min?: number;
    minorTick?: boolean;
    name?: string;
  } = {},
): YAXisComponentOption {
  return {
    type: options.log ? 'log' : 'value',
    name: options.name,
    max: options.max,
    min: options.min,
    nameTextStyle: { color: AXIS_COLOR, fontSize: FONT_SIZE },
    axisLine: { show: false },
    axisTick: { show: options.minorTick ?? false, lineStyle: { color: AXIS_LINE_COLOR } },
    minorTick: { show: options.minorTick ?? false },
    axisLabel: {
      color: AXIS_COLOR,
      fontSize: FONT_SIZE,
      formatter: options.formatter
        ? (value: number) => options.formatter?.(value) ?? String(value)
        : undefined,
    },
    splitLine: { lineStyle: { color: SPLIT_COLOR } },
  };
}

/** Value axis rendered horizontally (bullet charts). */
export function horizontalValueAxis(
  options: { formatter?: (value: number) => string; max?: number; min?: number } = {},
): XAXisComponentOption {
  return {
    type: 'value',
    max: options.max,
    min: options.min,
    axisLine: { show: false },
    axisTick: { show: false },
    axisLabel: {
      color: AXIS_COLOR,
      fontSize: FONT_SIZE,
      formatter: options.formatter
        ? (value: number) => options.formatter?.(value) ?? String(value)
        : undefined,
    },
    splitLine: { lineStyle: { color: SPLIT_COLOR } },
  };
}

/** Category axis rendered vertically (bullet charts). */
export function verticalCategoryAxis(data: string[]): YAXisComponentOption {
  return {
    type: 'category',
    data,
    axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
    axisTick: { show: false },
    axisLabel: { color: AXIS_COLOR, fontSize: FONT_SIZE, width: 96, overflow: 'truncate' },
    splitLine: { show: false },
  };
}

/** Legend defaults: scroll instead of overflowing once a chart has many series. */
export function legendOption(itemCount: number, compact: boolean): Record<string, unknown> | undefined {
  if (compact) return { show: false };
  if (itemCount <= 8) {
    return { textStyle: { color: TEXT_MUTED, fontSize: FONT_SIZE } };
  }
  return {
    type: 'scroll',
    bottom: 0,
    left: 'center',
    itemWidth: 10,
    itemHeight: 8,
    pageIconSize: 8,
    textStyle: { color: TEXT_MUTED, fontSize: FONT_SIZE },
  };
}

/* -------------------------------------------------------------- form data */

export function readList(formData: Record<string, unknown>, key: string): string[] {
  const raw = formData[key];
  if (!Array.isArray(raw)) {
    return typeof raw === 'string' ? [raw] : [];
  }
  const out: string[] = [];
  raw.forEach(item => {
    if (typeof item === 'string') {
      out.push(item);
      return;
    }
    if (item && typeof item === 'object') {
      const record = item as Record<string, unknown>;
      if (typeof record.label === 'string') out.push(record.label);
      else if (typeof record.value === 'string') out.push(record.value);
    }
  });
  return out.filter(Boolean);
}

export function readString(formData: Record<string, unknown>, keys: string[]): string | null {
  for (const key of keys) {
    const raw = formData[key];
    if (typeof raw === 'string' && raw.trim()) return raw;
    if (typeof raw === 'number' && Number.isFinite(raw)) return String(raw);
  }
  return null;
}

export function readBool(
  formData: Record<string, unknown>,
  key: string,
  fallback = false,
): boolean {
  const raw = formData[key];
  if (typeof raw === 'boolean') return raw;
  if (typeof raw === 'string') return raw === 'true' || raw === '1';
  if (typeof raw === 'number') return raw !== 0;
  return fallback;
}

export function readNumber(formData: Record<string, unknown>, keys: string[]): number | null {
  for (const key of keys) {
    const parsed = toNumber(formData[key]);
    if (parsed !== null) return parsed;
  }
  return null;
}
