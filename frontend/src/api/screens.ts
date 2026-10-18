/**
 * Data-visualisation wall (大屏) API client.
 *
 * A screen is a fixed-resolution, absolutely positioned canvas — not a
 * responsive dashboard — so its whole document travels in one `config` object.
 */
import { client } from './client';

/* ------------------------------------------------------------------ types */

export type ScreenCategory = 'gov' | 'park' | 'energy' | 'manufacture' | 'general' | string;

export type ScreenComponentType =
  | 'kpi'
  | 'chart'
  | 'table'
  | 'text'
  | 'marquee'
  | 'clock'
  | 'image'
  | 'video'
  | 'iframe'
  | 'border'
  | 'decoration'
  | 'progress'
  | 'shape'
  | 'richtext'
  | 'filter'
  | 'group'
  | 'list';

export interface ScreenCanvas {
  width: number;
  height: number;
  background: string;
  gridSize: number;
  showGrid: boolean;
  scaleMode: 'fit' | 'width' | 'actual';
  theme: string;
  padding?: number;
}

export interface ScreenTheme {
  primary?: string;
  accent?: string;
  text?: string;
  muted?: string;
  panel?: string;
  border?: string;
}

/** Snapshot of a saved chart bound to a wall tile. */
export interface ScreenChartRef {
  id?: number;
  name?: string;
  vizType?: string;
  datasourceId?: number;
  formData?: Record<string, unknown>;
}

/** A dataset bound directly to a wall tile (no saved chart required). */
export interface ScreenDatasetRef {
  id: number;
  name: string;
  vizType: string;
  formData: Record<string, unknown>;
}

export interface ScreenComponent {
  id: string;
  type: ScreenComponentType;
  name: string;
  x: number;
  y: number;
  w: number;
  h: number;
  rotate?: number;
  z: number;
  locked?: boolean;
  visible?: boolean;
  props: Record<string, unknown>;
  style?: Record<string, unknown>;
  chart?: ScreenChartRef | null;
  dataset?: ScreenDatasetRef | null;
  children?: ScreenComponent[];
}

export interface ScreenFilter {
  id: string;
  name: string;
  kind: 'select' | 'time' | 'range';
  datasetId?: number;
  column?: string;
  defaultValue?: string;
  options?: string[];
}

export interface ScreenInteraction {
  refreshSec?: number;
  watermark?: boolean;
  showToolbar?: boolean;
  autoRotate?: boolean;
  rotateSec?: number;
}

export interface ScreenConfig {
  version: number;
  canvas: ScreenCanvas;
  theme?: ScreenTheme;
  components: ScreenComponent[];
  filters?: ScreenFilter[];
  interaction?: ScreenInteraction;
  meta?: Record<string, unknown>;
}

export interface ScreenSummary {
  id: number;
  uuid: string;
  name: string;
  slug: string;
  description: string;
  category: ScreenCategory;
  width: number;
  height: number;
  scale_mode: string;
  theme: string;
  background: string;
  thumbnail: string;
  published: boolean;
  template_key: string;
  owner_id: number;
  refresh_sec: number;
  config_rev: number;
  xinchuang: boolean;
  public_enabled: boolean;
  public_token: string;
  public_expires_at: string | null;
  allow_export: boolean;
  has_password: boolean;
  view_count: number;
  created_on?: string | null;
  changed_on?: string | null;
}

export interface ScreenDetail extends ScreenSummary {
  config: ScreenConfig;
}

export interface ScreenTemplate {
  key: string;
  name: string;
  category: ScreenCategory;
  category_cn: string;
  description: string;
  cover: string;
  tags: string[];
  width: number;
  height: number;
  theme: string;
  background: string;
  config: ScreenConfig;
}

export interface ScreenRevision {
  id: number;
  rev: number;
  note: string;
  saved_by: number;
  created_on?: string | null;
  size: number;
}

export interface PublishOptions {
  enabled: boolean;
  password?: string;
  clear_password?: boolean;
  expires_in_hours?: number;
  allow_export?: boolean;
  regenerate?: boolean;
}

export interface PublishResult {
  result: ScreenSummary;
  url: string;
}

export interface EmbedInfo {
  url: string;
  iframe: string;
  expires_at: string | null;
  has_password: boolean;
}

/** Payload of the unauthenticated read path. */
export interface PublicScreen {
  requires_password?: boolean;
  name: string;
  description?: string;
  cover?: string;
  expires_at?: string | null;
  id?: number;
  width?: number;
  height?: number;
  scale_mode?: string;
  theme?: string;
  background?: string;
  config?: ScreenConfig;
  refresh_sec?: number;
  allow_export?: boolean;
}

/* --------------------------------------------------------------- endpoints */

export const screenEndpoints = {
  list: '/screen/',
  create: '/screen/',
  templates: '/screen/templates',
  templateCategories: '/screen/templates/categories',
  fromTemplate: (key: string) => `/screen/templates/${key}`,
  screen: (id: number | string) => `/screen/${id}`,
  config: (id: number | string) => `/screen/${id}/config`,
  duplicate: (id: number | string) => `/screen/${id}/duplicate`,
  publish: (id: number | string) => `/screen/${id}/publish`,
  embed: (id: number | string) => `/screen/${id}/embed`,
  export: (id: number | string) => `/screen/${id}/export`,
  import: '/screen/import',
  revisions: (id: number | string) => `/screen/${id}/revisions`,
  restore: (id: number | string, rev: number) => `/screen/${id}/revisions/${rev}/restore`,
  publicGet: (token: string) => `/public/screen/${token}`,
  publicUnlock: (token: string) => `/public/screen/${token}/unlock`,
};

/* -------------------------------------------------------------------- calls */

interface Paginated<T> {
  result: T[];
  count: number;
  ids?: number[];
}

export async function listScreens(params: {
  page?: number;
  pageSize?: number;
  q?: string;
  category?: string;
} = {}): Promise<Paginated<ScreenSummary>> {
  return client.get<Paginated<ScreenSummary>>(screenEndpoints.list, {
    page: params.page ?? 0,
    page_size: params.pageSize ?? 24,
    q: params.q,
    category: params.category,
  });
}

export async function getScreen(id: number | string): Promise<ScreenDetail> {
  const response = await client.get<{ result: ScreenDetail }>(screenEndpoints.screen(id));
  return response.result;
}

export async function createScreen(body: {
  name: string;
  description?: string;
  category?: string;
  width?: number;
  height?: number;
  theme?: string;
  background?: string;
  config?: ScreenConfig;
}): Promise<ScreenDetail> {
  const response = await client.post<{ result: ScreenDetail }>(screenEndpoints.create, body);
  return response.result;
}

export async function updateScreen(
  id: number | string,
  body: Partial<{
    name: string;
    description: string;
    category: string;
    scale_mode: string;
    theme: string;
    background: string;
    refresh_sec: number;
    xinchuang: boolean;
    published: boolean;
    config: ScreenConfig;
  }>,
): Promise<ScreenDetail> {
  const response = await client.put<{ result: ScreenDetail }>(screenEndpoints.screen(id), body);
  return response.result;
}

export async function saveScreenConfig(
  id: number | string,
  config: ScreenConfig,
  note?: string,
): Promise<{ result: ScreenSummary; config_rev: number }> {
  return client.put<{ result: ScreenSummary; config_rev: number }>(screenEndpoints.config(id), {
    config,
    note,
  });
}

export async function deleteScreen(id: number | string): Promise<void> {
  await client.delete(screenEndpoints.screen(id));
}

export async function duplicateScreen(
  id: number | string,
  name?: string,
): Promise<ScreenDetail> {
  const response = await client.post<{ result: ScreenDetail }>(screenEndpoints.duplicate(id), {
    name,
  });
  return response.result;
}

export async function listScreenTemplates(category?: string): Promise<{
  result: ScreenTemplate[];
  count: number;
  categories: Array<{ key: string; name: string; name_en: string }>;
}> {
  return client.get(screenEndpoints.templates, { category });
}

export async function createScreenFromTemplate(
  key: string,
  body: { name?: string } = {},
): Promise<ScreenDetail> {
  const response = await client.post<{ result: ScreenDetail }>(
    screenEndpoints.fromTemplate(key),
    body,
  );
  return response.result;
}

export async function publishScreen(
  id: number | string,
  options: PublishOptions,
): Promise<PublishResult> {
  return client.post<PublishResult>(screenEndpoints.publish(id), options);
}

export async function unpublishScreen(id: number | string): Promise<void> {
  await client.delete(screenEndpoints.publish(id));
}

export async function getScreenEmbed(id: number | string): Promise<EmbedInfo> {
  return client.get<EmbedInfo>(screenEndpoints.embed(id));
}

export async function listScreenRevisions(
  id: number | string,
): Promise<{ result: ScreenRevision[]; count: number }> {
  return client.get(screenEndpoints.revisions(id));
}

export async function restoreScreenRevision(
  id: number | string,
  rev: number,
): Promise<ScreenDetail> {
  const response = await client.post<{ result: ScreenDetail }>(
    screenEndpoints.restore(id, rev),
  );
  return response.result;
}

export async function importScreen(bundle: unknown): Promise<ScreenDetail> {
  const response = await client.post<{ result: ScreenDetail }>(screenEndpoints.import, bundle);
  return response.result;
}

export function screenExportUrl(id: number | string): string {
  return `/api/v1${screenEndpoints.export(id)}`;
}

/* ------------------------------------------------------- public read path */

export async function getPublicScreen(token: string): Promise<PublicScreen> {
  const response = await client.get<{ result: PublicScreen }>(
    screenEndpoints.publicGet(token),
    undefined,
    { anonymous: true },
  );
  return response.result;
}

export async function unlockPublicScreen(
  token: string,
  password: string,
): Promise<PublicScreen> {
  const response = await client.post<{ result: PublicScreen }>(
    screenEndpoints.publicUnlock(token),
    { password },
    { anonymous: true },
  );
  return response.result;
}
