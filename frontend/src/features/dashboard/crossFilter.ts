/**
 * Cross-filter (chart → chart linkage).
 *
 * Clicking a bar / slice / point pushes the clicked dimension value into a
 * dashboard-level list of "cross filters". They are merged into every *other*
 * tile bound to the same dataset, which turns a wall of charts into one
 * analysis surface — the single feature that most separates a dashboard from a
 * screenshot.
 *
 * The source tile is deliberately excluded: re-querying it with its own
 * selection would collapse it to the one bar the user just clicked.
 */
import type { DashboardChart, NativeFilterDataMask } from '../../api/types';
import type { ActiveDataMask } from './dataMask';
import { chartFormData } from './dataMask';

export interface CrossFilter {
  /** Stable identity: same chart + column + value replaces the previous one. */
  id: string;
  sourceChartId: number;
  datasetId: number;
  column: string;
  value: string | number;
  label: string;
}

/** The dimension a chart groups by, used as the click target column. */
export function crossFilterColumn(chart: DashboardChart): string | null {
  const form = chartFormData(chart) as Record<string, unknown>;
  const groupby = form.groupby;
  if (Array.isArray(groupby) && groupby.length) {
    const first = groupby[0];
    if (typeof first === 'string' && first.trim()) return first.trim();
    if (first && typeof first === 'object') {
      const name = (first as Record<string, unknown>).name;
      if (typeof name === 'string' && name.trim()) return name.trim();
    }
  }
  for (const key of ['granularity_sqla', 'granularity', 'time_grain_sqla']) {
    const value = form[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  const columns = form.columns;
  if (Array.isArray(columns) && columns.length) {
    const first = columns[0];
    if (typeof first === 'string' && first.trim()) return first.trim();
  }
  return null;
}

/** Builds the id used to de-duplicate repeated clicks on the same value. */
export function crossFilterId(chartId: number, column: string, value: string | number): string {
  return `CROSSFILTER-${chartId}-${column}-${String(value)}`;
}

/**
 * Extracts a cross filter from an ECharts click event.
 *
 * Returns null for clicks that carry no categorical dimension (legend toggles,
 * background clicks, charts without a groupby) so the dashboard never issues a
 * meaningless `WHERE 1 = 0`.
 */
export function crossFilterFromClick(params: unknown, chart: DashboardChart): CrossFilter | null {
  if (!params || typeof params !== 'object') return null;
  const event = params as Record<string, unknown>;
  // Legend / toolbox events must not filter the board.
  if (typeof event.componentType === 'string' && event.componentType !== 'series') return null;
  const column = crossFilterColumn(chart);
  if (!column) return null;

  const raw = event.name ?? event.value;
  let value: string | number | null = null;
  if (typeof raw === 'string' && raw.trim()) {
    value = raw.trim();
  } else if (typeof raw === 'number' && Number.isFinite(raw)) {
    value = raw;
  } else if (Array.isArray(raw) && raw.length) {
    // Scatter / heatmap pass an array; the category is the first scalar.
    const first = raw[0];
    if (typeof first === 'string' && first.trim()) value = first.trim();
    else if (typeof first === 'number') value = first;
  }
  if (value === null) return null;

  return {
    id: crossFilterId(chart.id, column, value),
    sourceChartId: chart.id,
    datasetId: chart.datasource_id,
    column,
    value,
    label: `${column}: ${value}`,
  };
}

/** Builds the dataMask a cross filter contributes. */
export function crossFilterMask(filter: CrossFilter): ActiveDataMask {
  const mask: NativeFilterDataMask = {
    extraFormData: { filters: [{ col: filter.column, op: '==', val: filter.value }] },
    filterState: { value: filter.value, label: filter.label },
  };
  return {
    filterId: filter.id,
    datasetIds: [filter.datasetId],
    // The dashboard excludes the emitting tile when it merges the masks.
    sourceChartId: filter.sourceChartId,
    mask,
  };
}

export function crossFilterMasks(filters: CrossFilter[]): ActiveDataMask[] {
  return filters.map(crossFilterMask);
}

/** Adds a filter, replacing an identical one so repeated clicks do not stack. */
export function toggleCrossFilter(list: CrossFilter[], next: CrossFilter): CrossFilter[] {
  const existing = list.find(item => item.id === next.id);
  if (existing) return list.filter(item => item.id !== next.id);
  // One filter per (chart, column): a new value replaces the old one.
  const kept = list.filter(
    item => !(item.sourceChartId === next.sourceChartId && item.column === next.column),
  );
  return [...kept, next];
}

export function removeCrossFilter(list: CrossFilter[], id: string): CrossFilter[] {
  return list.filter(item => item.id !== id);
}

/** True when the mask belongs to another tile than the one asking. */
export function appliesToChart(mask: ActiveDataMask, chartId: number): boolean {
  return mask.sourceChartId === undefined || mask.sourceChartId !== chartId;
}
