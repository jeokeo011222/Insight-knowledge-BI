import { client, endpoints } from './client';

/** Owner-facing view of a dashboard share row. */
export interface DashboardShare {
  dashboard_id?: number;
  enabled: boolean;
  has_password: boolean;
  allow_export: boolean;
  view_count: number;
  expires_at?: string | null;
  expired?: boolean;
  created_on?: string;
  changed_on?: string;
  /** Only present while the link is live. */
  token: string;
  url: string;
  embed_url: string;
}

export interface SharePayload {
  enabled?: boolean;
  password?: string;
  clear_password?: boolean;
  regenerate?: boolean;
  expires_in_hours?: number;
  allow_export?: boolean;
}

/**
 * Payload returned to an anonymous visitor.
 *
 * With `requires_password` the response carries metadata only — the dashboard
 * document is never sent before the gate is passed.
 */
export interface PublicDashboard {
  dashboard_id: number;
  title: string;
  slug?: string;
  description?: string;
  allow_export: boolean;
  can_export?: boolean;
  expires_at?: string | null;
  requires_password: boolean;
  /** Minted by the backend on unlock; scoped to read-only access. */
  guest_token?: string;
  expires_in?: number;
}

export async function getDashboardShare(id: number | string): Promise<DashboardShare> {
  const response = await client.get<{ result: DashboardShare }>(endpoints.dashboardShare(id));
  return response.result;
}

export async function saveDashboardShare(
  id: number | string,
  payload: SharePayload,
): Promise<DashboardShare> {
  const response = await client.post<{ result: DashboardShare }>(
    endpoints.dashboardShare(id),
    payload,
  );
  return response.result;
}

export async function revokeDashboardShare(id: number | string): Promise<DashboardShare> {
  const response = await client.delete<{ result: DashboardShare }>(endpoints.dashboardShare(id));
  return response.result;
}

/** Opens a public link (no credentials). */
export async function getPublicDashboard(token: string): Promise<PublicDashboard> {
  const response = await client.get<{ result: PublicDashboard }>(
    endpoints.publicDashboard(token),
    undefined,
    { anonymous: true },
  );
  return response.result;
}

/** Exchanges the share password for a scoped guest token. */
export async function unlockPublicDashboard(
  token: string,
  password: string,
): Promise<PublicDashboard> {
  const response = await client.post<{ result: PublicDashboard }>(
    endpoints.publicDashboardUnlock(token),
    { password },
    { anonymous: true },
  );
  return response.result;
}

/** Absolute URL for a share path ("/dashboard/public/x" → https://host/...). */
export function absoluteUrl(path: string): string {
  if (!path) return '';
  if (/^https?:\/\//i.test(path)) return path;
  if (typeof window === 'undefined') return path;
  return `${window.location.origin}${path.startsWith('/') ? '' : '/'}${path}`;
}

/** The iframe snippet used by the share dialog and the embeddable page. */
export function embedSnippet(path: string, height = 720): string {
  const url = absoluteUrl(path);
  return `<iframe src="${url}" width="100%" height="${height}" frameborder="0" allowfullscreen></iframe>`;
}

// Lineage ------------------------------------------------------------------

export interface LineageNode {
  key: string;
  type: 'dataset' | 'chart' | 'dashboard' | string;
  id: number;
  name: string;
  subtitle?: string;
  detail?: string;
}

export interface LineageEdge {
  source: string;
  target: string;
}

export interface LineageGraph {
  nodes: LineageNode[];
  edges: LineageEdge[];
  upstream: LineageNode[];
  downstream: LineageNode[];
}

export async function getLineage(
  type: 'dataset' | 'chart' | 'dashboard',
  id: number | string,
): Promise<LineageGraph> {
  const response = await client.get<{ result: LineageGraph }>(endpoints.lineage(type, id));
  return response.result;
}
