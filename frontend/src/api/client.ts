import { tGlobal } from '../i18n';
import type { ApiErrorBody, ApiErrorShape, TokenPair } from './types';

const API_BASE = '/api/v1';
const ACCESS_TOKEN_KEY = 'access_token';
const REFRESH_TOKEN_KEY = 'refresh_token';

export type QueryValue = string | number | boolean | null | undefined;
export type QueryParams = Record<string, QueryValue>;

export interface RequestOptions {
  query?: QueryParams;
  body?: unknown;
  /** Multipart payload; takes precedence over `body` and skips the JSON header. */
  formData?: FormData;
  /**
   * Verbatim request body (YAML, CSV, ...). Takes precedence over `body` so a
   * file upload is not re-encoded as a JSON string. `contentType` defaults to
   * text/plain.
   */
  rawText?: string;
  contentType?: string;
  signal?: AbortSignal;
  /** Skip attaching the bearer token (used by the login call). */
  anonymous?: boolean;
  /** Internal: prevents an endless refresh loop. */
  retried?: boolean;
}

/** Normalised error thrown by every client call. */
export class ApiError extends Error implements ApiErrorShape {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

export const tokenStore = {
  get access(): string | null {
    return localStorage.getItem(ACCESS_TOKEN_KEY);
  },
  get refresh(): string | null {
    return localStorage.getItem(REFRESH_TOKEN_KEY);
  },
  set(pair: TokenPair): void {
    localStorage.setItem(ACCESS_TOKEN_KEY, pair.access_token);
    if (pair.refresh_token) {
      localStorage.setItem(REFRESH_TOKEN_KEY, pair.refresh_token);
    }
  },
  clear(): void {
    localStorage.removeItem(ACCESS_TOKEN_KEY);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
  },
};

/**
 * Guest token used by the public-share and embedded views.
 *
 * It is kept in memory only (never in localStorage) so closing the tab drops
 * the credential, and it is sent on every request as the header the backend's
 * authenticator looks for. The backend decides the scope; the SPA only carries
 * the token.
 */
export const guestTokenStore = {
  current: null as string | null,
  set(token: string | null): void {
    this.current = token;
  },
  clear(): void {
    this.current = null;
  },
};

function buildUrl(path: string, query?: QueryParams): string {
  const url = `${API_BASE}${path}`;
  if (!query) return url;
  const search = new URLSearchParams();
  Object.entries(query).forEach(([key, value]) => {
    if (value === undefined || value === null || value === '') return;
    search.append(key, String(value));
  });
  const qs = search.toString();
  return qs ? `${url}?${qs}` : url;
}

async function parseError(response: Response): Promise<ApiError> {
  let message = `${response.status} ${response.statusText}`.trim();
  try {
    const body = (await response.json()) as ApiErrorBody;
    if (body?.message) {
      message = body.message;
    } else if (body?.errors?.length) {
      message = body.errors.map(e => e.message).filter(Boolean).join('; ') || message;
    }
  } catch {
    // Non JSON body: keep the HTTP status text.
  }
  return new ApiError(message, response.status);
}

function redirectToLogin(): void {
  if (typeof window === 'undefined') return;
  if (window.location.pathname === '/login') return;
  window.location.assign('/login');
}

class SupersetClient {
  private refreshInFlight: Promise<boolean> | null = null;

  private async fetchWithAuth(
    method: string,
    path: string,
    opts: RequestOptions,
  ): Promise<Response> {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (opts.rawText !== undefined) {
      headers['Content-Type'] = opts.contentType ?? 'text/plain';
    } else if (!opts.formData && opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
    }
    const token = tokenStore.access;
    if (token && !opts.anonymous) {
      headers.Authorization = `Bearer ${token}`;
    }
    if (guestTokenStore.current) {
      headers['X-GuestToken'] = guestTokenStore.current;
    }
    return fetch(buildUrl(path, opts.query), {
      method,
      headers,
      credentials: 'include',
      // FormData must be sent as-is so the browser sets the multipart boundary.
      body:
        opts.formData ??
        opts.rawText ??
        (opts.body === undefined ? undefined : JSON.stringify(opts.body)),
      signal: opts.signal,
    });
  }

  /** Refreshes the access token once, sharing a single in-flight request. */
  private async refreshAccessToken(): Promise<boolean> {
    if (this.refreshInFlight) return this.refreshInFlight;
    const refreshToken = tokenStore.refresh;
    if (!refreshToken) return false;

    this.refreshInFlight = (async () => {
      try {
        const response = await fetch(buildUrl(endpoints.refresh), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ refresh_token: refreshToken }),
        });
        if (!response.ok) return false;
        const pair = (await response.json()) as TokenPair;
        if (!pair.access_token) return false;
        tokenStore.set(pair);
        return true;
      } catch {
        return false;
      } finally {
        this.refreshInFlight = null;
      }
    })();

    return this.refreshInFlight;
  }

  private async request<T>(
    method: string,
    path: string,
    opts: RequestOptions = {},
  ): Promise<T> {
    let response = await this.fetchWithAuth(method, path, opts);

    // A guest session has no refresh token and must never be bounced to the
    // login page: the public view reports the failure instead.
    const asGuest = Boolean(guestTokenStore.current);
    if (
      response.status === 401 &&
      !opts.anonymous &&
      !asGuest &&
      !opts.retried &&
      tokenStore.refresh
    ) {
      const refreshed = await this.refreshAccessToken();
      if (refreshed) {
        response = await this.fetchWithAuth(method, path, { ...opts, retried: true });
      }
    }

    if (response.status === 401 && !opts.anonymous && !asGuest) {
      tokenStore.clear();
      redirectToLogin();
      throw new ApiError(tGlobal('error.auth.session_expired'), 401);
    }
    if (!response.ok) {
      throw await parseError(response);
    }
    if (response.status === 204) {
      return undefined as unknown as T;
    }
    const text = await response.text();
    if (!text) return undefined as unknown as T;
    return JSON.parse(text) as T;
  }

  get<T>(path: string, query?: QueryParams, opts?: Omit<RequestOptions, 'query' | 'body'>): Promise<T> {
    return this.request<T>('GET', path, { ...opts, query });
  }

  post<T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'body'>): Promise<T> {
    return this.request<T>('POST', path, { ...opts, body });
  }

  /** POST a verbatim text body (importing a YAML/JSON file). */
  postText<T>(path: string, text: string, contentType?: string): Promise<T> {
    return this.request<T>('POST', path, { rawText: text, contentType });
  }

  /** POSTs a multipart form (file uploads). */
  postForm<T>(path: string, formData: FormData, opts?: Omit<RequestOptions, 'body' | 'formData'>): Promise<T> {
    return this.request<T>('POST', path, { ...opts, formData });
  }

  put<T>(path: string, body?: unknown, opts?: Omit<RequestOptions, 'body'>): Promise<T> {
    return this.request<T>('PUT', path, { ...opts, body });
  }

  delete<T>(path: string, query?: QueryParams): Promise<T> {
    return this.request<T>('DELETE', path, { query });
  }

  /** POSTs a payload and returns the raw blob (CSV exports). */
  async postBlob(path: string, body: unknown): Promise<Blob> {
    const response = await this.fetchWithAuth('POST', path, { body });
    if (!response.ok) {
      throw await parseError(response);
    }
    return response.blob();
  }

  /** GETs a binary response (CSV / image / PDF exports). */
  async getBlob(path: string, query?: QueryParams): Promise<Blob> {
    const response = await this.fetchWithAuth('GET', path, { query });
    if (!response.ok) {
      throw await parseError(response);
    }
    return response.blob();
  }
}

/** Downloads a server generated file through the authenticated client. */
export async function downloadBlob(blob: Blob, filename: string): Promise<void> {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

/** Extracts a human readable message from any thrown value. */
export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof Error) return error.message;
  return String(error);
}

/**
 * Central registry of REST paths. Keeping them in one place makes backend
 * route changes a single-file edit.
 */
export const endpoints = {
  /** Prefix shared by every REST path; used to build absolute WS URLs. */
  apiBase: API_BASE,
  // auth
  login: '/security/login',
  refresh: '/security/refresh',
  logout: '/security/logout',
  csrfToken: '/security/csrf_token',
  guestToken: '/security/guest_token',
  me: '/me/',

  // databases
  databases: '/database/',
  databaseInfo: '/database/_info',
  databaseAvailable: '/database/available',
  databaseTestConnection: '/database/test_connection',
  database: (id: number | string) => `/database/${id}`,
  databaseSchemas: (id: number | string) => `/database/${id}/schemas`,
  databaseConnection: (id: number | string) => `/database/${id}/connection`,
  databaseTables: (id: number | string) => `/database/${id}/tables`,
  databaseTableMetadata: (id: number | string) => `/database/${id}/table_metadata`,
  databaseCatalog: (id: number | string) => `/database/${id}/catalog`,
  databaseSelectStar: (id: number | string) => `/database/${id}/select_star`,
  databaseHealth: '/database/health',
  databaseProbe: (id: number | string) => `/database/${id}/probe`,
  databaseProbeAll: '/database/probe_all',

  // datasets
  datasets: '/dataset/',
  datasetInfo: '/dataset/_info',
  datasetValidateSql: '/dataset/validate_sql',
  datasetUpload: '/dataset/upload/',
  datasetUploadSheets: '/dataset/upload/sheets/',
  datasetApiSource: '/dataset/api_source/',
  datasetApiSourceTest: '/dataset/api_source/test/',
  dataset: (id: number | string) => `/dataset/${id}`,
  datasetSamples: (id: number | string) => `/dataset/${id}/samples`,
  datasetRefresh: (id: number | string) => `/dataset/${id}/refresh`,
  datasetRefreshConfig: (id: number | string) => `/dataset/${id}/refresh_config`,
  datasetRefreshSource: (id: number | string) => `/dataset/${id}/refresh_source/`,
  datasetColumns: (id: number | string) => `/dataset/${id}/column/`,
  datasetColumn: (id: number | string, columnId: number) => `/dataset/${id}/column/${columnId}`,
  datasetMetrics: (id: number | string) => `/dataset/${id}/metric/`,
  datasetMetric: (id: number | string, metricId: number) => `/dataset/${id}/metric/${metricId}`,

  // charts
  charts: '/chart/',
  chartInfo: '/chart/_info',
  chartData: '/chart/data',
  chart: (id: number | string) => `/chart/${id}`,
  chartDataById: (id: number | string) => `/chart/${id}/data`,

  // dashboards
  dashboards: '/dashboard/',
  dashboardInfo: '/dashboard/_info',
  dashboard: (id: number | string) => `/dashboard/${id}`,
  dashboardCharts: (id: number | string) => `/dashboard/${id}/charts`,
  dashboardDatasets: (id: number | string) => `/dashboard/${id}/datasets`,
  dashboardCopy: (id: number | string) => `/dashboard/${id}/copy/`,
  dashboardShare: (id: number | string) => `/dashboard/${id}/share`,
  dashboardExportImage: (id: number | string) => `/dashboard/${id}/export_image`,
  dashboardFilterState: (id: number | string) => `/dashboard/${id}/filter_state`,
  dashboardFilterStateKey: (id: number | string, key: string) =>
    `/dashboard/${id}/filter_state/${key}`,

  // public sharing (anonymous)
  publicDashboard: (token: string) => `/public/dashboard/${token}`,
  publicDashboardUnlock: (token: string) => `/public/dashboard/${token}/unlock`,
  publicScreen: (token: string) => `/public/screen/${token}`,
  publicScreenUnlock: (token: string) => `/public/screen/${token}/unlock`,

  // lineage
  lineage: (type: string, id: number | string) => `/lineage/${type}/${id}`,

  // sql lab
  sqllab: '/sqllab/',
  sqllabHistory: '/sqllab/history/',
  sqllabEstimate: '/sqllab/estimate/',
  sqllabFormat: '/sqllab/format/',
  sqllabExport: '/sqllab/export/',
  sqllabCancel: (id: number | string) => `/sqllab/${id}/cancel`,
  sqllabResults: (id: number | string) => `/sqllab/${id}/results`,
  sqllabQuery: (id: number | string) => `/sqllab/${id}`,
  sqllabSocket: '/ws',
  savedQueries: '/sqllab/saved_query/',
  savedQuery: (id: number | string) => `/sqllab/saved_query/${id}`,
  tabStates: '/sqllab/tab_state/',
  tabState: (id: number | string) => `/sqllab/tab_state/${id}`,

  // annotations & tags
  annotationLayers: '/annotation_layer/',
  annotationLayer: (id: number | string) => `/annotation_layer/${id}`,
  annotations: '/annotation/',
  annotation: (id: number | string) => `/annotation/${id}`,
  tagsForObject: (objectType: string, objectId: number | string) =>
    `/tag/${objectType}/${objectId}`,
  tags: '/tag/',

  // governance
  rowLevelSecurity: '/rowlevelsecurity/',
  rlsFilter: (id: number | string) => `/rowlevelsecurity/${id}`,
  reports: '/report/',
  report: (id: number | string) => `/report/${id}`,
  reportLogs: (id: number | string) => `/report/${id}/log/`,
  logs: '/log/',
  /** Compliance export: same filters as the list, whole window in one CSV. */
  logsExport: '/log/export/',
  /** Asset bundles: `export/{type}/{id}/` downloads, `/import/` restores. */
  importExport: '/importexport/import/',
  exportAsset: (type: string, id: number | string) => `/importexport/export/${type}/${id}/`,
  favorites: '/favstar/',
  // self metadata database (system management)
  systemDatabase: '/system/database',
  systemDatabaseTest: '/system/database/test',
  /** Console landing payload: module health + engine probes + theme slots. */
  systemOverview: '/system/overview',
  info: '/info/',
  menu: '/menu/',
  timeRange: '/time_range/',

  // themes
  themes: '/theme/',
  theme: (id: number | string) => `/theme/${id}`,
  cssTemplates: '/css_template/',
  cssTemplate: (id: number | string) => `/css_template/${id}`,

  // rbac
  users: '/user/',
  userInfo: '/user/_info',
  user: (id: number | string) => `/user/${id}`,
  roles: '/role/',
  roleInfo: '/role/_info',
  role: (id: number | string) => `/role/${id}`,
  permissions: '/permission/',
  groups: '/group/',
} as const;

export const client = new SupersetClient();
