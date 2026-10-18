/**
 * Chart query-context construction and native-filter (dataMask) merging.
 *
 * Every dashboard tile re-queries through `POST /chart/data` so that filter
 * values can be injected. The original chart is stored as `params` /
 * `form_data`; both are normalised here into a QueryContext.
 *
 * Merge rules (see `mergeDataMasks`):
 *  - only filters whose `targets` contain the chart's datasource id apply;
 *  - `extraFormData.filters` are appended to `queries[0].filters`;
 *  - `from_dttm` / `to_dttm` from a time filter override the chart range;
 *  - `granularity_sqla` from a time-column filter overrides the granularity.
 */
import dayjs from 'dayjs';
import type {
  Chart,
  DashboardChart,
  JsonObject,
  NativeFilterDataMask,
  QueryContext,
  QueryFilter,
  QueryObject,
} from '../../api/types';

/** One active native filter, already aimed at its target datasets. */
export interface ActiveDataMask {
  filterId: string;
  datasetIds: number[];
  mask: NativeFilterDataMask;
  /**
   * Set by cross-filters: the tile that emitted the selection. It is skipped
   * when merging so a clicked chart does not collapse onto its own selection.
   */
  sourceChartId?: number;
}

export interface TimeBounds {
  from: string;
  to: string;
}

interface LooseQuery extends Omit<QueryObject, 'metrics' | 'groupby' | 'columns' | 'orderby'> {
  metrics?: unknown[];
  groupby?: unknown[];
  columns?: unknown[];
  orderby?: Array<[unknown, unknown]>;
}

const TIMESERIES_VIZ = new Set([
  'line',
  'area',
  'bar',
  'dist_bar',
  'scatter',
  'bubble',
  'big_number',
  'big_number_total',
  'compare',
  'time_pivot',
  'heatmap_v2',
  'cal_heatmap',
  'calendar_heatmap',
]);

const UNIT: Record<string, dayjs.ManipulateType> = {
  second: 'second',
  minute: 'minute',
  hour: 'hour',
  day: 'day',
  week: 'week',
  month: 'month',
  year: 'year',
};

/** dayjs has no "quarter" manipulate unit: map it to three months. */
function shiftBy(now: dayjs.Dayjs, amount: number, unit: string): dayjs.Dayjs {
  if (unit === 'quarter') return now.subtract(amount * 3, 'month');
  return now.subtract(amount, UNIT[unit] ?? 'day');
}

export function parseJsonObject(raw: JsonObject | string | null | undefined): JsonObject {
  if (!raw) return {};
  if (typeof raw === 'string') {
    try {
      const parsed: unknown = JSON.parse(raw);
      return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
        ? (parsed as JsonObject)
        : {};
    } catch {
      return {};
    }
  }
  return raw;
}

function asArray(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (value === null || value === undefined || value === '') return [];
  return [value];
}

function asStringArray(value: unknown): string[] {
  return asArray(value).filter((item): item is string => typeof item === 'string');
}

function asNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

/** Resolves a Superset time range token into absolute ISO bounds. */
export function resolveTimeRange(raw: string, now = dayjs()): TimeBounds | null {
  const value = raw.trim();
  if (!value || /^no\s*filter$/i.test(value)) return null;

  if (value.includes(' : ')) {
    const [from, to] = value.split(' : ');
    if (from && to) return { from: dayjs(from).toISOString(), to: dayjs(to).toISOString() };
  }

  const lower = value.toLowerCase();
  const simple: Record<string, [number, string]> = {
    'last day': [1, 'day'],
    'last week': [1, 'week'],
    'last month': [1, 'month'],
    'last quarter': [1, 'quarter'],
    'last year': [1, 'year'],
    'previous calendar week': [1, 'week'],
    'previous calendar month': [1, 'month'],
    'previous calendar quarter': [1, 'quarter'],
    'previous calendar year': [1, 'year'],
  };
  if (simple[lower]) {
    const [amount, unit] = simple[lower];
    return { from: shiftBy(now, amount, unit).toISOString(), to: now.toISOString() };
  }
  if (lower === 'today') {
    return { from: now.startOf('day').toISOString(), to: now.toISOString() };
  }
  if (lower === 'yesterday') {
    return {
      from: now.subtract(1, 'day').startOf('day').toISOString(),
      to: now.startOf('day').toISOString(),
    };
  }

  const relative = /^last\s+(\d+)\s+(second|minute|hour|day|week|month|quarter|year)s?$/.exec(lower);
  if (relative) {
    const amount = Number(relative[1]);
    return { from: shiftBy(now, amount, relative[2]).toISOString(), to: now.toISOString() };
  }

  // Unknown tokens are passed through: the backend parser also understands
  // expressions such as "90 days ago" or "start of month".
  return { from: value, to: dayjs().toISOString() };
}

function parseSimpleFilters(value: unknown): QueryFilter[] {
  const out: QueryFilter[] = [];
  asArray(value).forEach(item => {
    if (!item || typeof item !== 'object') return;
    const record = item as Record<string, unknown>;
    const expressionType = asString(record.expressionType);
    if (expressionType && expressionType !== 'SIMPLE') return;
    const col = asString(record.col) || asString(record.subject) || asString(record.column_name);
    const op = asString(record.op) || asString(record.operator);
    if (!col || !op) return;
    const val =
      record.val !== undefined
        ? record.val
        : record.comparator !== undefined
          ? record.comparator
          : record.value;
    out.push({ col, op, val });
  });
  return out;
}

function parseOrderby(value: unknown): Array<[string, boolean]> {
  const out: Array<[string, boolean]> = [];
  asArray(value).forEach(item => {
    if (Array.isArray(item) && item.length) {
      const column = typeof item[0] === 'string' ? item[0] : '';
      if (column) out.push([column, Boolean(item[1])]);
      return;
    }
    if (item && typeof item === 'object') {
      const record = item as Record<string, unknown>;
      const column = asString(record.column) || asString(record.col);
      if (column) out.push([column, Boolean(record.desc)]);
    }
  });
  return out;
}

/** Normalises chart params into the form_data object used for querying. */
export function chartFormData(chart: DashboardChart): JsonObject {
  const fromForm = chart.form_data ? parseJsonObject(chart.form_data) : {};
  if (Object.keys(fromForm).length) return fromForm;
  return parseJsonObject(chart.params);
}

/** True when the chart carries enough information to be queried explicitly. */
export function hasQueryableFormData(chart: DashboardChart): boolean {
  const formData = chartFormData(chart);
  const metrics = asArray(formData.metrics);
  const groupby = asArray(formData.groupby);
  const columns = asArray(formData.columns);
  return metrics.length + groupby.length + columns.length > 0;
}

/**
 * Builds a QueryContext from stored chart form_data. Falls back to a plain
 * `SELECT *`-style query when the chart only has a datasource.
 */
export function buildChartQueryContext(chart: DashboardChart): QueryContext {
  const formData = chartFormData(chart);
  const metrics = asArray(formData.metrics);
  const groupby = asArray(formData.groupby);
  const columns = asArray(formData.columns);
  const filters = [
    ...parseSimpleFilters(formData.adhoc_filters),
    ...parseSimpleFilters(formData.filters),
  ];
  const granularity =
    asString(formData.granularity_sqla) ||
    asString(formData.granularity) ||
    asString(formData.time_column);
  const timeRange = asString(formData.time_range);
  const bounds = timeRange ? resolveTimeRange(timeRange) : null;
  const rowLimit = asNumber(formData.row_limit, 10000);
  const extras: JsonObject = {};
  const timeGrain = asString(formData.time_grain_sqla);
  if (timeGrain) extras.time_grain_sqla = timeGrain;
  const where = asString(formData.where);
  if (where) extras.where = where;
  const having = asString(formData.having);
  if (having) extras.having = having;

  const vizType = asString(formData.viz_type) || chart.viz_type;
  // The temporal column and grain only take part in the query for timeseries
  // visualisations; categorical tiles (heatmap, pivot, pie …) keep their axes.
  const isTimeseries =
    Boolean(formData.is_timeseries) ||
    vizType.includes('timeseries') ||
    TIMESERIES_VIZ.has(vizType);

  const query: LooseQuery = {
    database_id: asNumber(formData.database_id, 0) || undefined,
    schema: asString(formData.schema) || undefined,
    sql: asString(formData.sql) || undefined,
    metrics,
    groupby,
    columns,
    filters,
    granularity: granularity || undefined,
    from_dttm: bounds?.from,
    to_dttm: bounds?.to,
    extras,
    row_limit: rowLimit > 0 ? rowLimit : 10000,
    row_offset: 0,
    orderby: parseOrderby(formData.orderby),
    post_processing: [],
    is_timeseries: isTimeseries,
  };

  return {
    datasource: {
      id: chart.datasource_id,
      type: chart.datasource_type || 'table',
    },
    force: false,
    queries: [query as unknown as QueryObject],
    form_data: formData,
    result_type: 'full',
    result_format: 'json',
  };
}

function filterKey(filter: QueryFilter): string {
  return `${filter.col}|${filter.op}|${JSON.stringify(filter.val)}`;
}

/**
 * Appends every applicable native filter dataMask to the query context.
 * Returns the original object when nothing applies (cheap identity check).
 */
export function mergeDataMasks(
  context: QueryContext,
  datasetId: number,
  masks: ActiveDataMask[],
): QueryContext {
  const applicable = masks.filter(
    item => item.datasetIds.includes(datasetId) && item.mask.extraFormData,
  );
  if (!applicable.length || !context.queries.length) return context;

  const base = context.queries[0];
  const filters: QueryFilter[] = [...(base.filters ?? [])];
  const seen = new Set(filters.map(filterKey));
  let fromDttm = base.from_dttm;
  let toDttm = base.to_dttm;
  let granularity = base.granularity;

  applicable.forEach(({ mask }) => {
    const extra = mask.extraFormData ?? {};
    (extra.filters ?? []).forEach(filter => {
      const key = filterKey(filter);
      if (seen.has(key)) return;
      seen.add(key);
      filters.push(filter);
    });
    if (extra.from_dttm) fromDttm = extra.from_dttm;
    if (extra.to_dttm) toDttm = extra.to_dttm;
    if (extra.granularity_sqla) granularity = extra.granularity_sqla;
  });

  const queries = context.queries.map((query, index) =>
    index === 0
      ? { ...query, filters, from_dttm: fromDttm, to_dttm: toDttm, granularity }
      : query,
  );

  return { ...context, queries };
}

/** Stable serialisation used to memoise fetches. */
export function masksSignature(masks: ActiveDataMask[]): string {
  return JSON.stringify(masks);
}

/** Converts saved chart DTOs into dashboard tiles (form_data drives queries). */
export function toDashboardCharts(charts: Chart[]): DashboardChart[] {
  return charts.map(chart => ({
    ...chart,
    form_data: parseJsonObject(chart.params),
  }));
}
