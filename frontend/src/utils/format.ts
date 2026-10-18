import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import { getLang } from '../i18n';

dayjs.extend(relativeTime);

type DateInput = string | number | Date | dayjs.Dayjs | null | undefined;

const LOCALE_TAG = { zh: 'zh-CN', en: 'en-US' } as const;

/** Empty-value placeholder shared by tables and detail panes. */
export const EMPTY_TEXT = '—';

/** `YYYY-MM-DD HH:mm:ss` in the current locale's timezone handling. */
export function formatDateTime(value: DateInput, pattern = 'YYYY-MM-DD HH:mm:ss'): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT;
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format(pattern) : EMPTY_TEXT;
}

/** `YYYY-MM-DD`, for date-only columns. */
export function formatDate(value: DateInput): string {
  return formatDateTime(value, 'YYYY-MM-DD');
}

/** `YYYY-MM-DD HH:mm`, the common table-cell granularity. */
export function formatDateTimeShort(value: DateInput): string {
  return formatDateTime(value, 'YYYY-MM-DD HH:mm');
}

export interface NumberFormatOptions {
  /** Compact notation (1.2K / 3.4M) for KPI tiles. */
  compact?: boolean;
  /** Fixed fraction digits; omitted means "natural" (max 2). */
  digits?: number;
  /** Append a `%` sign (value is treated as an already-scaled percentage). */
  percent?: boolean;
}

/**
 * Locale-aware number formatting: grouped by default, compact on request.
 * Non-numeric input falls back to the placeholder rather than `NaN`.
 */
export function formatNumber(value: number | string | null | undefined, options: NumberFormatOptions = {}): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT;
  const num = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(num)) return EMPTY_TEXT;

  const { compact = false, digits, percent = false } = options;
  const formatter = new Intl.NumberFormat(LOCALE_TAG[getLang()], {
    notation: compact ? 'compact' : 'standard',
    maximumFractionDigits: digits ?? (compact ? 1 : 2),
    minimumFractionDigits: digits ?? 0,
    style: percent ? 'percent' : 'decimal',
  });
  return formatter.format(percent ? num / 100 : num);
}

/** Relative time, e.g. "3 hours ago"; follows the active locale. */
export function fromNow(value: DateInput): string {
  if (value === null || value === undefined || value === '') return EMPTY_TEXT;
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.fromNow() : EMPTY_TEXT;
}

/** Compact timestamp used in exported file names (YYYYMMDD-HHmm). */
export function exportStamp(): string {
  return dayjs().format('YYYYMMDD-HHmm');
}
