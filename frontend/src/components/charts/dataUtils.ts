/**
 * Data-shaping helpers: column heuristics, series resolution, pivoting,
 * distribution statistics and geo column detection.
 */
import type { Row, VizContext } from './utils';
import { asText, formatCompact, parseTime, readList, readString, toNumber } from './utils';
import { resolveChinaRegion } from './chinaMap';

/* --------------------------------------------------------------- metadata */

export function coltypeOf(ctx: VizContext, key: string): string | undefined {
  const index = ctx.colnames.indexOf(key);
  return index >= 0 ? ctx.coltypes[index] : undefined;
}

const TIME_TEXT = /^\d{4}[-/]\d{1,2}([-/]\d{1,2})?([ T]\d{1,2}:\d{2}(:\d{2})?)?/;

export function isTimeColumn(rows: Row[], key: string, coltype?: string): boolean {
  if (coltype && /date|time/i.test(coltype)) return true;
  const sample = rows
    .slice(0, 20)
    .map(row => row[key])
    .filter(value => value !== null && value !== undefined && value !== '');
  if (!sample.length) return false;
  if (sample.every(value => value instanceof Date)) return true;
  return sample.every(value => typeof value === 'string' && TIME_TEXT.test(value.trim()));
}

export function isNumericColumn(rows: Row[], key: string): boolean {
  const sample = rows
    .slice(0, 20)
    .map(row => row[key])
    .filter(value => value !== null && value !== undefined && value !== '');
  if (!sample.length) return false;
  return sample.every(
    value => toNumber(value) !== null && !(typeof value === 'string' && TIME_TEXT.test(value.trim())),
  );
}

export function numericColumns(ctx: VizContext): string[] {
  return ctx.colnames.filter(
    key => isNumericColumn(ctx.rows, key) && !isTimeColumn(ctx.rows, key, coltypeOf(ctx, key)),
  );
}

export function textColumns(ctx: VizContext): string[] {
  return ctx.colnames.filter(
    key => !isNumericColumn(ctx.rows, key) || isTimeColumn(ctx.rows, key, coltypeOf(ctx, key)),
  );
}

/** First metric column, honouring the explore `metrics` control. */
export function metricColumn(ctx: VizContext): string | null {
  const declared = readList(ctx.formData, 'metrics').find(key => ctx.colnames.includes(key));
  if (declared) return declared;
  const excluded = readString(ctx.formData, ['granularity_sqla', 'x_axis', 'time_column']);
  const numeric = numericColumns(ctx).filter(key => key !== excluded);
  if (numeric.length) return numeric[0];
  return ctx.colnames.find(key => key !== excluded) ?? null;
}

/** First categorical column, honouring the explore `groupby` control. */
export function dimensionColumn(ctx: VizContext): string | null {
  const declared = readList(ctx.formData, 'groupby').find(key => ctx.colnames.includes(key));
  if (declared) return declared;
  const texts = textColumns(ctx).filter(
    key => !isTimeColumn(ctx.rows, key, coltypeOf(ctx, key)),
  );
  if (texts.length) return texts[0];
  return ctx.colnames[0] ?? null;
}

export function countDistinct(rows: Row[], key: string): number {
  const seen = new Set<string>();
  rows.forEach(row => seen.add(asText(row[key])));
  return seen.size;
}

export function dimensionTotals(
  rows: Row[],
  dimKey: string,
  valueKey: string | null,
): Array<{ name: string; value: number }> {
  const totals = new Map<string, number>();
  rows.forEach(row => {
    const name = asText(row[dimKey]) || '(null)';
    const value = valueKey ? toNumber(row[valueKey]) : 1;
    if (value === null) return;
    totals.set(name, (totals.get(name) ?? 0) + value);
  });
  return Array.from(totals, ([name, value]) => ({ name, value }));
}

/* ---------------------------------------------------------- series resolve */

export interface SeriesSpec {
  name: string;
  values: Array<number | null>;
}

export interface ResolvedSeries {
  xKey: string;
  groupKey: string | null;
  metricKeys: string[];
  xValues: string[];
  series: SeriesSpec[];
  isTime: boolean;
}

/**
 * Resolves an explore-shaped result into x values plus one or more series.
 * Handles wide results (one numeric column per series) as well as long results
 * (`groupby` column becomes the series split).
 */
export function resolveSeries(ctx: VizContext): ResolvedSeries | null {
  const { rows, colnames, formData } = ctx;
  if (!rows.length || !colnames.length) return null;

  const declaredX = readString(formData, [
    'granularity_sqla',
    'x_axis',
    'time_column',
    'all_columns_x',
  ]);
  const xKey =
    (declaredX && colnames.includes(declaredX) ? declaredX : null) ??
    colnames.find(key => isTimeColumn(rows, key, coltypeOf(ctx, key))) ??
    colnames[0];

  let metricKeys = numericColumns(ctx).filter(key => key !== xKey);
  const declaredMetrics = readList(formData, 'metrics').filter(
    key => colnames.includes(key) && key !== xKey,
  );
  if (declaredMetrics.length) metricKeys = declaredMetrics;
  const countMode = metricKeys.length === 0;
  if (countMode) metricKeys = ['count'];

  let groupKey: string | null = null;
  const declaredGroups = readList(formData, 'groupby').filter(
    key => key !== xKey && colnames.includes(key) && !metricKeys.includes(key),
  );
  if (declaredGroups.length) {
    groupKey = declaredGroups[0];
  } else if (metricKeys.length === 1) {
    const candidate = colnames.find(
      key =>
        key !== xKey &&
        key !== metricKeys[0] &&
        !isNumericColumn(rows, key) &&
        countDistinct(rows, key) > 1 &&
        countDistinct(rows, key) <= 24,
    );
    if (candidate) groupKey = candidate;
  }

  const xValues: string[] = [];
  const seenX = new Set<string>();
  const buckets = new Map<string, Map<string, number>>();
  rows.forEach(row => {
    const xLabel = asText(row[xKey]) || '(null)';
    if (!seenX.has(xLabel)) {
      seenX.add(xLabel);
      xValues.push(xLabel);
    }
    const groups = groupKey ? [asText(row[groupKey]) || '(null)'] : [''];
    groups.forEach(group => {
      metricKeys.forEach(metric => {
        const value = countMode ? 1 : toNumber(row[metric]);
        if (value === null) return;
        const name =
          groupKey && group
            ? metricKeys.length > 1
              ? `${group} · ${metric}`
              : group
            : metric;
        let bucket = buckets.get(name);
        if (!bucket) {
          bucket = new Map<string, number>();
          buckets.set(name, bucket);
        }
        bucket.set(xLabel, (bucket.get(xLabel) ?? 0) + value);
      });
    });
  });

  // A time axis always reads left to right. The SQL source is free to return
  // the buckets in any order (DESC is the usual default), which would
  // otherwise draw the series backwards.
  const timeAxis = isTimeColumn(rows, xKey, coltypeOf(ctx, xKey));
  if (timeAxis) {
    xValues.sort((a, b) => (parseTime(a)?.valueOf() ?? 0) - (parseTime(b)?.valueOf() ?? 0));
  }

  const series = Array.from(buckets, ([name, bucket]) => ({
    name,
    values: xValues.map(x => (bucket.has(x) ? (bucket.get(x) as number) : null)),
  }));

  return {
    xKey,
    groupKey,
    metricKeys,
    xValues,
    series,
    isTime: timeAxis,
  };
}

/* ---------------------------------------------------------- pivot & stats */

export function keyOf(values: string[]): string {
  return values.join('\u0001');
}

export interface PivotResult {
  rowOrder: string[];
  rowValues: Map<string, string[]>;
  colOrder: string[];
  cell: Map<string, number | null>;
  rowDims: string[];
  colKey: string | null;
  metric: string;
}

export function pivotResult(
  rows: Row[],
  rowDims: string[],
  colKey: string | null,
  metric: string,
): PivotResult {
  const rowOrder: string[] = [];
  const rowValues = new Map<string, string[]>();
  const colOrder: string[] = [];
  const cell = new Map<string, number | null>();

  rows.forEach(row => {
    const dims = rowDims.map(dim => asText(row[dim]) || '(null)');
    const rowKey = keyOf(dims);
    if (!rowValues.has(rowKey)) {
      rowValues.set(rowKey, dims);
      rowOrder.push(rowKey);
    }
    if (colKey) {
      const colValue = asText(row[colKey]) || '(null)';
      if (!colOrder.includes(colValue)) colOrder.push(colValue);
      cell.set(`${rowKey}\u0002${colValue}`, toNumber(row[metric]));
    } else {
      const previous = cell.get(rowKey);
      const value = toNumber(row[metric]);
      cell.set(rowKey, previous === undefined || previous === null ? value : previous + (value ?? 0));
    }
  });

  return { rowOrder, rowValues, colOrder, cell, rowDims, colKey, metric };
}

export function extent(values: number[]): { min: number; max: number } {
  let min = Number.POSITIVE_INFINITY;
  let max = Number.NEGATIVE_INFINITY;
  values.forEach(value => {
    if (!Number.isFinite(value)) return;
    if (value < min) min = value;
    if (value > max) max = value;
  });
  return Number.isFinite(min) ? { min, max } : { min: 0, max: 1 };
}

export function quantile(sorted: number[], q: number): number {
  if (!sorted.length) return 0;
  const position = (sorted.length - 1) * q;
  const base = Math.floor(position);
  const rest = position - base;
  const next = sorted[base + 1] ?? sorted[base];
  return sorted[base] + rest * (next - sorted[base]);
}

export interface BoxStats {
  min: number;
  q1: number;
  median: number;
  q3: number;
  max: number;
}

export function boxStats(values: number[]): BoxStats {
  const sorted = [...values].sort((a, b) => a - b);
  const q1 = quantile(sorted, 0.25);
  const median = quantile(sorted, 0.5);
  const q3 = quantile(sorted, 0.75);
  const spread = q3 - q1;
  const inliers = sorted.filter(value => value >= q1 - 1.5 * spread && value <= q3 + 1.5 * spread);
  return {
    min: inliers[0] ?? sorted[0],
    q1,
    median,
    q3,
    max: inliers[inliers.length - 1] ?? sorted[sorted.length - 1],
  };
}

export function histogram(
  values: number[],
  binCount = 12,
): { labels: string[]; counts: number[] } {
  if (!values.length) return { labels: [], counts: [] };
  const { min, max } = extent(values);
  if (min === max) return { labels: [formatCompact(min)], counts: [values.length] };
  const bins = Math.max(1, Math.min(binCount, values.length));
  const step = (max - min) / bins;
  const counts = new Array<number>(bins).fill(0);
  values.forEach(value => {
    const index = Math.min(bins - 1, Math.floor((value - min) / step));
    counts[index] += 1;
  });
  const labels = counts.map(
    (_, index) => `${formatCompact(min + index * step)} – ${formatCompact(min + (index + 1) * step)}`,
  );
  return { labels, counts };
}

/* -------------------------------------------------------------------- geo */

const LAT_CANDIDATES = ['latitude', 'lat', 'y_lat', 'lat_col'];
const LNG_CANDIDATES = ['longitude', 'lon', 'lng', 'long', 'x_lon', 'lon_col'];

function matchColumn(colnames: string[], candidates: string[]): string | null {
  const lower = colnames.map(name => name.toLowerCase());
  for (const candidate of candidates) {
    const index = lower.indexOf(candidate);
    if (index >= 0) return colnames[index];
  }
  for (const candidate of candidates) {
    const index = lower.findIndex(name => name.includes(candidate));
    if (index >= 0) return colnames[index];
  }
  return null;
}

export function findGeoColumns(colnames: string[]): { lat: string | null; lng: string | null } {
  return {
    lat: matchColumn(colnames, LAT_CANDIDATES),
    lng: matchColumn(colnames, LNG_CANDIDATES),
  };
}

/** Column names that conventionally hold an administrative region. */
const REGION_CANDIDATES = [
  'province',
  'state',
  'region',
  'country',
  '省份',
  '省',
  '地区',
  '区域',
  'name',
];

/** Fraction of a column's distinct values that resolve to a China region. */
function regionHitRatio(rows: Row[], key: string): number {
  const values = new Set<string>();
  rows.slice(0, 200).forEach(row => {
    const value = asText(row[key]);
    if (value) values.add(value);
  });
  if (values.size < 2) return 0;
  let hits = 0;
  values.forEach(value => {
    if (resolveChinaRegion(value)) hits += 1;
  });
  return hits / values.size;
}

/**
 * Finds the column to render as a choropleth: an explicitly grouped-by column
 * first, then columns whose name looks like a region, then the rest — keeping
 * only columns whose values overwhelmingly name a Chinese province.
 */
export function findRegionColumn(ctx: VizContext): string | null {
  const declared = readList(ctx.formData, 'groupby').filter(key => ctx.colnames.includes(key));
  const looksLikeRegion = (key: string) =>
    REGION_CANDIDATES.some(candidate => key.toLowerCase().includes(candidate));
  const ordered = [
    ...declared,
    ...ctx.colnames.filter(looksLikeRegion),
    ...ctx.colnames.filter(key => !looksLikeRegion(key)),
  ];
  const seen = new Set<string>();
  for (const key of ordered) {
    if (seen.has(key)) continue;
    seen.add(key);
    if (regionHitRatio(ctx.rows, key) >= 0.6) return key;
  }
  return null;
}

/** Limits huge results so that a chart stays responsive. */
export function takeRows(rows: Row[], limit: number): Row[] {
  return rows.length > limit ? rows.slice(0, limit) : rows;
}
