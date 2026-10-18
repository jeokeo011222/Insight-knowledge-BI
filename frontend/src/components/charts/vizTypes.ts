import { hasTranslation } from '../../i18n';
import type { TranslationKey } from '../../i18n';
import { REGISTRY, type VizCategory } from './registry';

export { ADVANCED_TAG, FEATURED_TAG } from './registry';

export interface VizTypeOption {
  value: string;
  label: string;
  category: VizCategory;
  color: string;
  /** Gallery metadata, mirrored from the renderer registry. */
  descriptionEn?: string;
  descriptionZh?: string;
  tags?: string[];
  badge?: string;
  thumbnail?: string;
}

/**
 * Explore dropdown options, derived from the renderer registry so that the
 * picker can never advertise a viz type that has no renderer.
 */
export const VIZ_TYPES: VizTypeOption[] = Object.entries(REGISTRY).map(([value, definition]) => ({
  value,
  label: definition.label,
  category: definition.category,
  color: definition.color,
  descriptionEn: definition.descriptionEn,
  descriptionZh: definition.descriptionZh,
  tags: definition.tags,
  badge: definition.badge,
  thumbnail: definition.thumbnail,
}));

export const VIZ_CATEGORIES: VizCategory[] = Array.from(
  new Set(VIZ_TYPES.map(option => option.category)),
);

export function findVizType(value: string): VizTypeOption | undefined {
  return VIZ_TYPES.find(option => option.value === value);
}

/**
 * Localised display label for a viz type. Falls back to the renderer's own
 * English label when the message pack has no `chart.type.<value>` entry.
 */
export function vizLabel(
  option: VizTypeOption,
  t: (key: TranslationKey) => string,
): string {
  const key = `chart.type.${option.value}`;
  return hasTranslation(key) ? t(key as TranslationKey) : option.label;
}

export type { VizCategory };

/**
 * Category and tag labels are English identifiers in the renderer registry
 * (`'Time series'`, `'Part to Whole'`). They are stable keys, not display text:
 * the gallery must render them in the active locale, otherwise the Chinese UI
 * shows a mixed-language sidebar.
 */
const CATEGORY_KEYS: Record<string, string> = {
  'Time series': 'viz.category.time_series',
  Categorical: 'viz.category.categorical',
  Numeric: 'viz.category.numeric',
  Matrix: 'viz.category.matrix',
  'Flow & relation': 'viz.category.flow',
  Geo: 'viz.category.geo',
  Text: 'viz.category.text',
};

const TAG_KEYS: Record<string, string> = {
  Featured: 'viz.tag.featured',
  'Advanced-Analytics': 'viz.tag.advanced',
  'Time Series': 'viz.tag.time_series',
  Comparison: 'viz.tag.comparison',
  Distribution: 'viz.tag.distribution',
  Hierarchy: 'viz.tag.hierarchy',
  'Part to Whole': 'viz.tag.part_to_whole',
  Relationship: 'viz.tag.relationship',
  Geo: 'viz.tag.geo',
  Tabular: 'viz.tag.tabular',
  Text: 'viz.tag.text',
  KPI: 'viz.tag.kpi',
};

/** Localised category name, falling back to the registry value. */
export function vizCategoryLabel(
  category: string,
  t: (key: TranslationKey) => string,
): string {
  const key = CATEGORY_KEYS[category];
  return key && hasTranslation(key) ? t(key as TranslationKey) : category;
}

/** Localised tag name, falling back to the registry value. */
export function vizTagLabel(tag: string, t: (key: TranslationKey) => string): string {
  const key = TAG_KEYS[tag];
  return key && hasTranslation(key) ? t(key as TranslationKey) : tag;
}
