/**
 * Tile interactions that go beyond linkage: drill-down and jump.
 *
 * A dashboard tile can react to a click in three ways, in this order:
 *
 *  1. **drill** — the tile is configured with `drill_levels` (a dimension
 *     hierarchy). Clicking a value keeps the tile but re-groups it by the next
 *     level, accumulating one equality filter per step, and shows a breadcrumb.
 *  2. **jump** — the tile is configured with `jump_config`: the click opens
 *     another dashboard (as external filter params) or an external URL with the
 *     clicked value substituted.
 *  3. **cross-filter** — the default (see `crossFilter.ts`).
 *
 * Both configurations live in the chart `params`, so nothing here needs the
 * backend: the drill rewrites the query context the same way native filters do.
 */
import type { DashboardChart, JsonObject, QueryContext, QueryFilter } from '../../api/types';
import { chartFormData } from './dataMask';

/** One drilled level: the value the user picked at `column`. */
export interface DrillStep {
  column: string;
  value: string | number;
}

/** Jump target stored in `params.jump_config`. */
export interface JumpConfig {
  type: 'url' | 'dashboard';
  /** `url` target; `{value}` / `{column}` placeholders are substituted. */
  url?: string;
  /** `dashboard` target. */
  dashboardId?: number;
  /** External filter name used for `dashboard` targets (`f_<param>`). */
  param?: string;
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string' && item.trim() !== '')
    : [];
}

/** The dimension hierarchy a tile drills through, outermost first. */
export function drillLevels(chart: DashboardChart): string[] {
  const form = chartFormData(chart) as Record<string, unknown>;
  return asStringArray(form.drill_levels);
}

/**
 * The level a click should drill *into*.
 *
 * The tile already groups by the first level, so after N steps the next level
 * is `levels[N + 1]`; null means the bottom of the hierarchy is reached and the
 * click falls through to jump / cross-filter.
 */
export function nextDrillLevel(levels: string[], steps: number): string | null {
  if (levels.length < 2) return null;
  const index = steps + 1;
  return index < levels.length ? levels[index] : null;
}

/**
 * Rewrites the query context for the current drill path.
 *
 * Every step becomes an equality filter; the grouping moves to `level`. The
 * time grain is kept so drilling inside a time series still plots over time.
 */
export function applyDrill(
  context: QueryContext,
  steps: DrillStep[],
  level: string | null,
): QueryContext {
  if (!steps.length || !level || !context.queries.length) return context;
  const base = context.queries[0];
  const filters: QueryFilter[] = [...(base.filters ?? [])];
  const seen = new Set(filters.map(filter => `${filter.col}|${filter.op}|${JSON.stringify(filter.val)}`));
  steps.forEach(step => {
    const key = `${step.column}|==|${JSON.stringify(step.value)}`;
    if (seen.has(key)) return;
    seen.add(key);
    filters.push({ col: step.column, op: '==', val: step.value });
  });

  const queries = context.queries.map((query, index) =>
    index === 0 ? { ...query, filters, groupby: [level] } : query,
  );
  const formData: JsonObject = { ...(context.form_data ?? {}), groupby: [level] };
  // `force` bypasses the query cache: the same chart id now asks a different
  // question, and a cached answer would show the pre-drill rows.
  return { ...context, force: true, queries, form_data: formData };
}

/** The configured jump target, or null when the tile does not jump. */
export function jumpConfig(chart: DashboardChart): JumpConfig | null {
  const form = chartFormData(chart) as Record<string, unknown>;
  const raw = form.jump_config;
  if (!raw || typeof raw !== 'object') return null;
  const record = raw as Record<string, unknown>;
  const type = asString(record.type);
  if (type === 'url') {
    const url = asString(record.url);
    return url ? { type: 'url', url } : null;
  }
  if (type === 'dashboard') {
    const id = Number(record.dashboard_id ?? record.dashboardId);
    if (!Number.isFinite(id) || id <= 0) return null;
    return { type: 'dashboard', dashboardId: id, param: asString(record.param) || undefined };
  }
  return null;
}

/**
 * Builds the destination URL for a click.
 *
 * URLs may embed `{value}` / `{column}`; when they do not, nothing is appended
 * (an external system usually has its own parameter names). Dashboard targets
 * reuse the documented external filter syntax, so the destination pre-filters
 * itself without any new endpoint.
 */
export function buildJumpUrl(
  config: JumpConfig,
  column: string,
  value: string | number,
): string | null {
  const text = String(value);
  if (config.type === 'url') {
    const template = config.url ?? '';
    if (!template) return null;
    return template
      .replace(/\{value\}/g, encodeURIComponent(text))
      .replace(/\{column\}/g, encodeURIComponent(column));
  }
  if (config.type === 'dashboard' && config.dashboardId) {
    const key = `f_${config.param || column}`;
    return `/dashboard/${config.dashboardId}/?${key}=${encodeURIComponent(text)}`;
  }
  return null;
}
