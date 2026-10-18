/**
 * Native filter (dashboard filter bar) model helpers:
 *  - parsing `json_metadata.native_filter_configuration`
 *  - turning a UI value into a Superset dataMask
 *  - URL <-> filter value serialisation
 *  - DISTINCT value query used to populate select controls
 */
import { v4 as uuidv4 } from 'uuid';
import type {
  Dataset,
  JsonObject,
  NativeFilterConfig,
  NativeFilterDataMask,
  NativeFilterType,
  QueryContext,
  QueryObject,
} from '../../api/types';
import type { ActiveDataMask } from './dataMask';
import { parseJsonObject, resolveTimeRange } from './dataMask';

export type FilterValue =
  | string
  | number
  | string[]
  | [number | null, number | null]
  | { from: string; to: string }
  | null;

export type FilterValueMap = Record<string, FilterValue | undefined>;

export interface TimePreset {
  value: string;
  label: string;
}

export const TIME_PRESETS: TimePreset[] = [
  { value: 'No filter', label: 'No filter' },
  { value: 'Last day', label: 'Last day' },
  { value: 'Last week', label: 'Last week' },
  { value: 'Last month', label: 'Last month' },
  { value: 'Last quarter', label: 'Last quarter' },
  { value: 'Last year', label: 'Last year' },
  { value: 'Custom', label: 'Custom range' },
];

export const FILTER_TYPE_LABELS: Record<NativeFilterType, string> = {
  filter_select: 'Single select',
  filter_multi_select: 'Multi select',
  filter_range: 'Numeric range',
  filter_time: 'Time range',
  filter_timecolumn: 'Time column',
  filter_value: 'Value',
  filter_divider: 'Divider',
};

export function isFilterValueEmpty(value: FilterValue | undefined): boolean {
  if (value === null || value === undefined) return true;
  if (typeof value === 'string') return value.trim() === '';
  if (Array.isArray(value)) return !value.length || value.every(item => item === null || item === '');
  if (typeof value === 'object') return !value.from && !value.to;
  return false;
}

/* ------------------------------------------------------------- parsing */

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function isFilterType(value: unknown): value is NativeFilterType {
  return typeof value === 'string' && value in FILTER_TYPE_LABELS;
}

/** Reads and normalises `native_filter_configuration` from json_metadata. */
export function parseNativeFilters(
  metadata: unknown,
): NativeFilterConfig[] {
  const meta =
    typeof metadata === 'string'
      ? parseJsonObject(metadata)
      : metadata && typeof metadata === 'object' && !Array.isArray(metadata)
        ? (metadata as JsonObject)
        : {};
  const raw = meta.native_filter_configuration;
  if (!Array.isArray(raw)) return [];

  const out: NativeFilterConfig[] = [];
  raw.forEach(item => {
    if (!item || typeof item !== 'object') return;
    const record = item as Record<string, unknown>;
    const id = asString(record.id);
    const name = asString(record.name);
    if (!id || !name) return;
    const filterType = isFilterType(record.filterType)
      ? record.filterType
      : 'filter_select';
    const targets = (Array.isArray(record.targets) ? record.targets : [])
      .map(target => {
        if (!target || typeof target !== 'object') return null;
        const entry = target as Record<string, unknown>;
        const datasetId = Number(entry.datasetId);
        const column = entry.column;
        const name2 =
          column && typeof column === 'object'
            ? asString((column as Record<string, unknown>).name)
            : '';
        if (!Number.isFinite(datasetId) || datasetId <= 0) return null;
        return { datasetId, column: { name: name2 } };
      })
      .filter((target): target is NativeFilterConfig['targets'][number] => target !== null);

    out.push({
      id,
      name,
      filterType,
      targets,
      defaultDataMask:
        record.defaultDataMask && typeof record.defaultDataMask === 'object'
          ? (record.defaultDataMask as NativeFilterDataMask)
          : undefined,
      controlValues:
        record.controlValues && typeof record.controlValues === 'object'
          ? (record.controlValues as NativeFilterConfig['controlValues'])
          : {},
      requiredFirst: Boolean(record.requiredFirst),
      description: asString(record.description) || undefined,
    });
  });
  return out;
}

export function createFilterId(): string {
  return `NATIVE_FILTER-${uuidv4()}`;
}

/** Creates a new filter pre-targeted at the first available dataset column. */
export function createNativeFilter(
  filterType: NativeFilterType,
  datasets: Dataset[],
): NativeFilterConfig {
  const dataset = datasets[0];
  const column =
    dataset?.columns.find(item => item.is_dttm)?.column_name ??
    dataset?.columns[0]?.column_name ??
    '';
  return {
    id: createFilterId(),
    name: FILTER_TYPE_LABELS[filterType],
    filterType,
    targets:
      filterType === 'filter_divider' || filterType === 'filter_time' || !dataset
        ? []
        : [{ datasetId: dataset.id, column: { name: column } }],
    controlValues: {
      multiSelection: filterType === 'filter_multi_select',
      defaultToFirstItem: false,
      sortAscending: true,
    },
    requiredFirst: false,
  };
}

export function filterDatasetId(config: NativeFilterConfig): number | null {
  return config.targets[0]?.datasetId ?? null;
}
export function filterColumnName(config: NativeFilterConfig): string {
  return config.targets[0]?.column.name ?? '';
}

/* ------------------------------------------------------------ dataMask */

function toScalar(value: string): string | number {
  return value.trim() !== '' && Number.isFinite(Number(value)) ? Number(value) : value;
}

/**
 * Builds the dataMask pushed to `filter_state` and merged into incoming
 * chart queries. Returns null when the filter has no active value.
 */
export function buildFilterDataMask(
  config: NativeFilterConfig,
  value: FilterValue | undefined,
): NativeFilterDataMask | null {
  const column = filterColumnName(config);

  if (config.filterType === 'filter_divider') return null;
  if (config.filterType === 'filter_timecolumn') {
    if (!column) return null;
    return {
      extraFormData: { granularity_sqla: column },
      filterState: { value: column, label: column },
    };
  }
  if (config.filterType === 'filter_time') {
    if (value === null || value === undefined) return null;
    let bounds: { from: string; to: string } | null = null;
    let label = '';
    if (typeof value === 'string') {
      if (value === 'No filter' || !value) return null;
      bounds = resolveTimeRange(value);
      label = value;
    } else if (!Array.isArray(value) && typeof value === 'object') {
      if (!value.from || !value.to) return null;
      bounds = {
        from: new Date(value.from).toISOString(),
        to: new Date(value.to).toISOString(),
      };
      label = `${value.from} → ${value.to}`;
    }
    if (!bounds) return null;
    return {
      extraFormData: { time_range: label, from_dttm: bounds.from, to_dttm: bounds.to },
      filterState: { value, label },
    };
  }
  if (!column || isFilterValueEmpty(value)) return null;

  if (config.filterType === 'filter_range') {
    const range = Array.isArray(value) ? value : [null, null];
    const min = typeof range[0] === 'number' ? range[0] : null;
    const max = typeof range[1] === 'number' ? range[1] : null;
    const filters: Array<{ col: string; op: string; val: number }> = [];
    if (min !== null) filters.push({ col: column, op: '>=', val: min });
    if (max !== null) filters.push({ col: column, op: '<=', val: max });
    if (!filters.length) return null;
    return {
      extraFormData: { filters },
      filterState: {
        value: [min, max],
        label: `${min ?? '…'} – ${max ?? '…'}`,
      },
    };
  }

  if (config.filterType === 'filter_value') {
    const scalar =
      typeof value === 'string' ? toScalar(value) : typeof value === 'number' ? value : '';
    if (scalar === '') return null;
    return {
      extraFormData: { filters: [{ col: column, op: '==', val: scalar }] },
      filterState: { value: scalar, label: String(scalar) },
    };
  }

  const values = (Array.isArray(value) ? value : [value])
    .map(item => (typeof item === 'number' ? item : String(item)))
    .filter(item => item !== '');
  if (!values.length) return null;
  const single =
    config.filterType === 'filter_select' && !config.controlValues?.multiSelection;
  const payload = single ? values[0] : values;
  return {
    extraFormData: {
      filters: [{ col: column, op: 'IN', val: single ? [payload] : payload }],
    },
    filterState: {
      value: single ? values[0] : values,
      label: values.join(', '),
    },
  };
}

/** Collects dataMasks for every filter with an active value. */
export function collectActiveMasks(
  configs: NativeFilterConfig[],
  values: FilterValueMap,
): ActiveDataMask[] {
  const out: ActiveDataMask[] = [];
  configs.forEach(config => {
    const mask = buildFilterDataMask(config, values[config.id]);
    if (!mask) return;
    out.push({
      filterId: config.id,
      datasetIds: config.targets.map(target => target.datasetId),
      mask,
    });
  });
  return out;
}

/* ----------------------------------------------------------- URL state */

export interface DashboardUrlState {
  filterValues: FilterValueMap;
  edit: boolean;
}

function toBase64(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = '';
  bytes.forEach(byte => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function fromBase64(value: string): string {
  const normalised = value.replace(/-/g, '+').replace(/_/g, '/');
  const padded = normalised + '='.repeat((4 - (normalised.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = Uint8Array.from(binary, char => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

/** Reads `?native_filters=<base64>&edit=1`; returns null when absent. */
export function readUrlState(search: string): DashboardUrlState | null {
  const params = new URLSearchParams(search);
  const encoded = params.get('native_filters');
  const edit = params.get('edit') === '1';
  if (!encoded) {
    return edit ? { filterValues: {}, edit } : null;
  }
  try {
    const parsed: unknown = JSON.parse(fromBase64(encoded));
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null;
    const filterValues = parsed as FilterValueMap;
    return { filterValues, edit };
  } catch {
    return { filterValues: {}, edit };
  }
}

/** Mirrors filter values and edit mode into the address bar (no history). */
export function syncUrlState(values: FilterValueMap, edit: boolean): void {
  if (typeof window === 'undefined') return;
  const url = new URL(window.location.href);
  const entries = Object.entries(values).filter(([, value]) => value !== undefined);
  if (entries.length) {
    url.searchParams.set(
      'native_filters',
      toBase64(JSON.stringify(Object.fromEntries(entries))),
    );
  } else {
    url.searchParams.delete('native_filters');
  }
  if (edit) {
    url.searchParams.set('edit', '1');
  } else {
    url.searchParams.delete('edit');
  }
  window.history.replaceState(null, '', url.toString());
}

/* ------------------------------------------------- external f_ params */

/**
 * Normalises a token so `Sales Region`, `sales-region` and `SALES_REGION` all
 * address the same filter (DataEase/Superset links are written by hand, by a
 * BI portal or by a wiki, so tolerance matters more than exactness).
 */
function paramSlug(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9\u4e00-\u9fa5]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

function coerceParamValue(raw: string): string | number {
  const text = raw.trim();
  if (text === '') return '';
  if (/^-?\d+(\.\d+)?$/.test(text)) return Number(text);
  return text;
}

export interface ExternalFilterResult {
  values: FilterValueMap;
  /** Parameters that matched no configured filter (surfaced to the user). */
  unmatched: string[];
}

/**
 * Reads `?f_<filter>` parameters into filter values.
 *
 * An external system can deep-link a dashboard with a pre-applied selection:
 *
 *   /dashboard/12/?f_region=华东,华南&f_time=Last%20quarter
 *
 * `<filter>` matches, in order: the native filter id, the filter name, or the
 * targeted column name. Multi-select filters accept comma separated lists;
 * single-value filters keep the first entry. Time filters accept both a preset
 * ("Last week") and an explicit ISO range ("2026-01-01~2026-03-31").
 */
export function readExternalFilters(
  search: string,
  configs: NativeFilterConfig[],
): ExternalFilterResult {
  const values: FilterValueMap = {};
  const unmatched: string[] = [];
  const params = new URLSearchParams(search);
  for (const [key, raw] of params.entries()) {
    if (!key.startsWith('f_') || key === 'f_') continue;
    const token = key.slice(2);
    const config = configs.find(
      item =>
        item.id === token ||
        paramSlug(item.name) === paramSlug(token) ||
        paramSlug(filterColumnName(item)) === paramSlug(token),
    );
    if (!config) {
      unmatched.push(key);
      continue;
    }
    if (raw === '') {
      values[config.id] = null;
      continue;
    }
    if (config.filterType === 'filter_time') {
      const range = raw.split('~');
      if (range.length === 2 && range[0] && range[1]) {
        values[config.id] = { from: range[0], to: range[1] };
      } else {
        values[config.id] = raw;
      }
      continue;
    }
    if (config.filterType === 'filter_range') {
      const range = raw.split(',').map(item => Number(item.trim()));
      values[config.id] = [
        Number.isFinite(range[0]) ? range[0] : null,
        Number.isFinite(range[1]) ? range[1] : null,
      ];
      continue;
    }
    const parts = raw
      .split(',')
      .map(item => coerceParamValue(item))
      .filter(item => item !== '');
    if (!parts.length) {
      values[config.id] = null;
      continue;
    }
    const multi =
      config.filterType === 'filter_multi_select' || config.controlValues?.multiSelection;
    values[config.id] = multi ? (parts as string[]) : (parts[0] as string | number);
  }
  return { values, unmatched };
}

/* -------------------------------------------------- candidate values */

/** QueryContext returning DISTINCT values for a column (max 1000 rows). */
export function buildDistinctValuesContext(
  datasetId: number,
  column: string,
  rowLimit = 1000,
): QueryContext {
  const query = {
    metrics: [],
    groupby: [column],
    columns: [],
    filters: [],
    granularity: undefined,
    from_dttm: null,
    to_dttm: null,
    extras: {},
    row_limit: rowLimit,
    row_offset: 0,
    orderby: [[column, true]],
    post_processing: [],
    is_timeseries: false,
  } as unknown as QueryObject;
  return {
    datasource: { id: datasetId, type: 'table' },
    force: false,
    queries: [query],
    result_type: 'full',
    result_format: 'json',
  };
}

/** Numeric bounds inferred from candidate values (range slider). */
export function numericBounds(values: Array<string | number>): [number, number] {
  const numbers = values
    .map(value => Number(value))
    .filter(value => Number.isFinite(value));
  if (!numbers.length) return [0, 100];
  const min = Math.min(...numbers);
  const max = Math.max(...numbers);
  if (min === max) return [Math.min(0, min), max + 1];
  return [min, max];
}

/** Best-effort default value for a filter, honouring defaultToFirstItem. */
export function defaultFilterValue(
  config: NativeFilterConfig,
  candidates: Array<string | number>,
): FilterValue | undefined {
  const configured = config.defaultDataMask?.filterState?.value;
  if (configured !== undefined && configured !== null) {
    if (Array.isArray(configured)) {
      return configured.map(item => String(item));
    }
    if (typeof configured === 'object') {
      const record = configured as Record<string, unknown>;
      if (typeof record.value === 'string') return record.value;
    }
    return String(configured);
  }
  if (config.controlValues?.defaultToFirstItem && candidates.length) {
    const sorted = config.controlValues.sortAscending === false
      ? [...candidates].sort((a, b) => String(b).localeCompare(String(a)))
      : candidates;
    const first = sorted[0];
    return config.filterType === 'filter_multi_select' ? [String(first)] : String(first);
  }
  return undefined;
}

/** Normalises filter_state responses (documented map and backend list shape). */
export function parseFilterState(payload: unknown): FilterValueMap {
  const out: FilterValueMap = {};
  if (!payload || typeof payload !== 'object') return out;

  const assign = (key: string, mask: unknown) => {
    if (!key || !mask || typeof mask !== 'object') return;
    const record = mask as Record<string, unknown>;
    const state = record.filterState;
    if (state && typeof state === 'object' && 'value' in state) {
      out[key] = (state as { value: FilterValue }).value;
    }
  };

  const result = (payload as Record<string, unknown>).result;
  if (Array.isArray(result)) {
    result.forEach(item => {
      if (!item || typeof item !== 'object') return;
      const body = (item as Record<string, unknown>).value;
      if (!body || typeof body !== 'object') return;
      const bodyRecord = body as Record<string, unknown>;
      const key = typeof bodyRecord.key === 'string' ? bodyRecord.key : '';
      assign(key, bodyRecord.value);
    });
    return out;
  }

  if (result && typeof result === 'object') {
    Object.entries(result as Record<string, unknown>).forEach(([key, entry]) => {
      if (!entry || typeof entry !== 'object') return;
      const record = entry as Record<string, unknown>;
      assign(key, 'value' in record ? record.value : record);
    });
  }
  return out;
}
