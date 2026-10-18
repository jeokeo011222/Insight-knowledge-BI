/**
 * China basemap loader for map visualisations.
 *
 * The province boundaries ship as a static asset (`public/geo/china-provinces.json`)
 * rather than a bundle import so the ~570KB file stays out of the main chunk. It
 * is fetched and registered with ECharts once; afterwards `resolveChinaRegion`
 * maps data values (「广东」, 「广东省」) onto the boundary names used by the map.
 */
import { echarts } from './echartsSetup';

export const CHINA_MAP_NAME = 'china';

const MAP_URL = '/geo/china-provinces.json';

/** Longest first: 「广西壮族自治区」 must lose all three tokens to become 「广西」. */
const REGION_SUFFIXES = [
  '维吾尔自治区',
  '壮族自治区',
  '回族自治区',
  '特别行政区',
  '自治区',
  '自治州',
  '省',
  '市',
];

interface ChinaGeoJson {
  type: string;
  features: Array<{ properties?: { name?: string } }>;
}

/** Strips the administrative suffix so short and full spellings share a key. */
function normalizeRegion(value: string): string {
  const name = value.trim();
  for (const suffix of REGION_SUFFIXES) {
    if (name.length > suffix.length && name.endsWith(suffix)) {
      return name.slice(0, -suffix.length);
    }
  }
  return name;
}

let regionLookup: Map<string, string> | null = null;
let loadPromise: Promise<boolean> | null = null;

/** Whether the China basemap is already registered with ECharts. */
export function isChinaMapRegistered(): boolean {
  return Boolean(echarts.getMap(CHINA_MAP_NAME));
}

/**
 * Fetches + registers the China basemap exactly once. Resolves `false` when the
 * asset cannot be loaded, letting the caller show a placeholder instead.
 */
export function ensureChinaMap(): Promise<boolean> {
  if (regionLookup) return Promise.resolve(true);
  if (!loadPromise) {
    loadPromise = fetch(MAP_URL)
      .then(response =>
        response.ok ? response.json() : Promise.reject(new Error(String(response.status))),
      )
      .then((geoJson: ChinaGeoJson) => {
        echarts.registerMap(CHINA_MAP_NAME, geoJson as never);
        const lookup = new Map<string, string>();
        geoJson.features.forEach(feature => {
          const full = feature.properties?.name?.trim();
          if (!full) return;
          lookup.set(full, full);
          lookup.set(normalizeRegion(full), full);
        });
        regionLookup = lookup;
        return true;
      })
      .catch(() => false);
  }
  return loadPromise;
}

/** Resolves a data value to its boundary name, or `null` when it is not a region. */
export function resolveChinaRegion(value: string): string | null {
  if (!regionLookup) return null;
  const trimmed = value.trim();
  if (!trimmed) return null;
  return regionLookup.get(trimmed) ?? regionLookup.get(normalizeRegion(trimmed)) ?? null;
}
