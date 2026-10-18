/**
 * Shared formatting for dataset refresh state. The list column and the detail
 * panel must agree on how an interval and a status are rendered, otherwise the
 * same dataset reads differently in two places on one screen.
 */
import type { TranslationKey } from '../../i18n';

export type IntervalUnit = 'minute' | 'hour' | 'day';

export const UNIT_SECONDS: Record<IntervalUnit, number> = {
  minute: 60,
  hour: 3600,
  day: 86400,
};

/** Upper bound per unit, all equivalent to 30 days. */
export const UNIT_MAX: Record<IntervalUnit, number> = {
  minute: 60 * 24 * 30,
  hour: 24 * 30,
  day: 30,
};

/**
 * Split a stored interval into a value + unit, preferring the largest unit that
 * divides evenly so 3600 shows as "1 hour" rather than "60 minutes".
 */
export function splitInterval(seconds: number): { value: number; unit: IntervalUnit } {
  if (seconds <= 0) return { value: 1, unit: 'minute' };
  if (seconds % UNIT_SECONDS.day === 0) return { value: seconds / UNIT_SECONDS.day, unit: 'day' };
  if (seconds % UNIT_SECONDS.hour === 0) return { value: seconds / UNIT_SECONDS.hour, unit: 'hour' };
  return { value: Math.max(1, Math.round(seconds / 60)), unit: 'minute' };
}

type Translate = (key: TranslationKey, vars?: Record<string, string | number>) => string;

/** Human-readable period, e.g. "每 30 分钟" / "Every 2 hours". */
export function formatRefreshInterval(seconds: number, t: Translate): string {
  if (!seconds || seconds <= 0) return t('dataset.refresh.manual_only');
  const split = splitInterval(seconds);
  if (split.unit === 'day') return t('dataset.refresh.every_days', { count: split.value });
  if (split.unit === 'hour') return t('dataset.refresh.every_hours', { count: split.value });
  return t('dataset.refresh.every_minutes', { count: split.value });
}
