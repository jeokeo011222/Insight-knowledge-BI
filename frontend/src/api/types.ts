/**
 * Wire types for the Superset REST API (see backend/internal/api).
 *
 * Only fields consumed by the SPA are modelled; everything else stays
 * accessible through index signatures where the payload is free-form JSON.
 */

/** Generic JSON object used for params / position_json / metadata. */
export type JsonObject = Record<string, unknown>;

/** FAB style list envelope. */
export interface Paginated<T> {
  count: number;
  result: T[];
  ids_above?: number[];
  ids_below?: number[];
}

/** A single resource envelope. */
export interface ResultEnvelope<T> {
  result: T;
}

export interface ApiErrorBody {
  message?: string;
  errors?: Array<{ message?: string }>;
}

export interface ApiErrorShape {
  message: string;
  status: number;
}

/* ------------------------------------------------------------------ auth */

export interface LoginRequest {
  username: string;
  password: string;
  provider: 'db';
}

export interface TokenPair {
  access_token: string;
  refresh_token: string;
}

export interface Role {
  id: number;
  name: string;
}

export interface MeUser {
  id: number;
  username: string;
  first_name: string;
  last_name: string;
  email: string;
  is_active: boolean;
  is_anonymous: boolean;
  roles: Role[];
  permissions: string[];
}

/* ------------------------------------------------------------- databases */

export interface Database {
  id: number;
  uuid?: string;
  database_name: string;
  verbose_name?: string | null;
  backend: string;
  sqlalchemy_uri: string;
  configuration_method?: string;
  expose_in_sqllab: boolean;
  allow_ctas: boolean;
  allow_cvas: boolean;
  allow_dml: boolean;
  allow_run_async: boolean;
  allow_file_upload?: boolean;
  allow_multi_schema_metadata_fetch?: boolean;
  explore_database?: boolean;
  is_managed_externally?: boolean;
  external_url?: string | null;
  server_cert?: string | null;
  encrypted_extra?: unknown;
  encrypted_extra_configured?: boolean;
  cache_timeout?: number | null;
  force_ctas_schema?: string | null;
  impersonate_user?: boolean;
  extra?: string | null;
  /** Health probe (数据源健康检查). */
  last_probe_at?: string | null;
  last_probe_ok?: boolean | null;
  last_probe_ms?: number | null;
  last_probe_error?: string | null;
  created_on?: string | null;
  changed_on?: string | null;
}

export interface DatabasePayload {
  database_name: string;
  verbose_name?: string;
  sqlalchemy_uri?: string;
  expose_in_sqllab?: boolean;
  allow_ctas?: boolean;
  allow_cvas?: boolean;
  allow_dml?: boolean;
  allow_run_async?: boolean;
  allow_file_upload?: boolean;
  cache_timeout?: number | null;
}

export type ConnectionKind = 'host' | 'catalog' | 'file' | 'uri';

export interface ConnectionField {
  name: string;
  label: string;
  label_key: string;
  type: 'string' | 'password' | 'int' | 'select';
  required: boolean;
  default?: string | number;
  placeholder?: string;
  help?: string;
  help_key?: string;
  group: 'connection' | 'auth' | 'advanced';
}

export interface ConnectionForm {
  kind: ConnectionKind;
  driver: string;
  example?: string;
  fields: ConnectionField[];
}

export interface EngineInfo {
  engine: string;
  engine_name: string;
  driver: string;
  default_port: number;
  supports_file_upload: boolean;
  supports_joins?: boolean;
  /** Picker group: relational | analytics | cloud | file | nosql | other. */
  category?: string;
  connection?: ConnectionForm;
}

export interface ConnectionTestResult {
  message: string;
  result: { latency_ms: number } | null;
  errors?: Array<{ message: string }>;
}

export interface TableMetadataColumn {
  name: string;
  type: string;
  is_dttm: boolean;
  is_date?: boolean;
}

export interface TableMetadata {
  name: string;
  schema: string;
  columns: TableMetadataColumn[];
  indexes?: unknown[];
  select_star?: string;
}

export interface DatabaseOption {
  value: string;
  label: string;
  type?: string;
}

export interface CatalogNode {
  name: string;
  type: 'database' | 'schema' | 'table' | string;
  children?: CatalogNode[];
  extra?: { schema?: string };
}

/* -------------------------------------------------------------- datasets */

export interface Owner {
  id: number;
  username?: string;
  first_name?: string | null;
  last_name?: string | null;
  name?: string | null;
}

export interface DatasetColumn {
  /** Column-level masking (列级脱敏); "" means the raw value is shown. */
  mask_type?: string;
  mask_rule?: string;
  /** Agent exposure (W-01): false hides the column from the Agent entirely. */
  agent_visible?: boolean;
  /** Derived sensitivity class: none | low | high. */
  pii_level?: string;
  /** True when an effective mask rule (mask_type or high PII) applies. */
  masked?: boolean;
  id: number;
  uuid?: string;
  column_name: string;
  verbose_name?: string | null;
  type?: string | null;
  is_dttm?: boolean;
  is_date?: boolean;
  is_active?: boolean;
  groupby?: boolean;
  filterable?: boolean;
  description?: string | null;
  expression?: string | null;
  table_id?: number;
  python_date_format?: string | null;
  datetime_format?: string | null;
  extra?: JsonObject | null;
}

export interface DatasetMetric {
  id: number;
  uuid?: string;
  metric_name: string;
  verbose_name?: string | null;
  expression: string;
  metric_type?: string | null;
  description?: string | null;
  d3format?: string | null;
  warning_text?: string | null;
  currency?: string | null;
}

export interface Dataset {
  id: number;
  uuid?: string;
  table_name: string;
  schema?: string | null;
  catalog?: string | null;
  database: { id: number; database_name: string };
  description?: string | null;
  sql?: string | null;
  main_dttm_col?: string | null;
  columns: DatasetColumn[];
  metrics: DatasetMetric[];
  owners: Owner[];
  changed_on?: string | null;
  created_on?: string | null;
  cache_timeout?: number | null;
  default_endpoint?: string | null;
  is_sqllab_view?: boolean;
  kind?: string;
  type?: string;
  name?: string;
  source?: DatasetSourceInfo | null;
  /** Auto-refresh period in seconds; 0 means manual only. */
  refresh_interval_sec?: number;
  last_refreshed_at?: string | null;
  last_refresh_status?: 'running' | 'success' | 'failed' | '' | null;
  last_refresh_error?: string | null;
  last_refresh_duration_ms?: number | null;
  last_refresh_trigger?: 'manual' | 'schedule' | '' | null;
}

/** Where an external dataset's rows came from (file upload or REST API). */
export interface DatasetSourceInfo {
  type: 'file' | 'api';
  filename?: string;
  url?: string;
  method?: string;
  record_path?: string;
}

export type ApiAuthType = 'none' | 'bearer' | 'basic' | 'api_key';

export interface ApiAuthConfig {
  type: ApiAuthType;
  token?: string;
  username?: string;
  password?: string;
  key_name?: string;
  key_value?: string;
  in?: 'header' | 'query';
}

/** Configuration of a remote JSON-over-HTTP data source. */
export interface ApiSourceConfig {
  url: string;
  method?: string;
  headers?: Record<string, string>;
  params?: Record<string, string>;
  body?: string;
  auth?: ApiAuthConfig;
  record_path?: string;
  max_rows?: number;
  timeout_sec?: number;
}

/** Result of a dry-run API fetch used by the "test" button. */
export interface IngestPreview {
  columns: Array<{ column_name: string; type: string; is_dttm: boolean }>;
  data: unknown[][];
  rowcount: number;
  duration_ms: number;
}

export interface DatasetSamples {
  sql: string;
  columns: string[];
  coltypes: string[];
  data: Array<Record<string, unknown>>;
  rowcount: number;
  truncated: boolean;
  duration_ms: number;
  limit: number;
  table_name: string;
  schema?: string | null;
}

export interface DatasetColumnPayload {
  column_name: string;
  type?: string;
  is_dttm?: boolean;
  verbose_name?: string;
  description?: string;
  groupby?: boolean;
  filterable?: boolean;
}

export interface DatasetPayload {
  table_name: string;
  database_id: number;
  schema?: string;
  catalog?: string | null;
  sql?: string | null;
  description?: string;
  main_dttm_col?: string | null;
  columns?: DatasetColumnPayload[];
  owners?: number[];
  metrics?: Array<{ metric_name: string; expression: string }>;
}

export interface ValidateSqlResult {
  message: string;
  result: Array<{ column_name: string; type: string }>;
  errors: string[];
}

/* ---------------------------------------------------------------- charts */

export interface Chart {
  id: number;
  uuid?: string;
  slice_name: string;
  viz_type: string;
  datasource_id: number;
  datasource_type: string;
  datasource_name: string;
  datasource_url?: string;
  params?: JsonObject | string | null;
  description?: string | null;
  owners: Owner[];
  changed_on?: string | null;
  created_on?: string | null;
  thumbnail_url?: string | null;
  url?: string;
  cache_timeout?: number | null;
  certified_by?: string | null;
  certification_details?: string | null;
  tags?: Tag[];
}

export interface ChartPayload {
  slice_name: string;
  viz_type: string;
  datasource_id: number;
  datasource_type: string;
  params?: JsonObject;
  description?: string;
  owners?: number[];
}

/* ------------------------------------------------------------- dashboards */

export interface Dashboard {
  id: number;
  uuid?: string;
  dashboard_title: string;
  slug?: string | null;
  published: boolean;
  status: 'published' | 'draft' | string;
  description?: string | null;
  owners: Owner[];
  changed_by_name?: string | null;
  changed_on?: string | null;
  changed_on_delta_humanized?: string | null;
  created_on?: string | null;
  thumbnail_url?: string | null;
  url?: string;
  chart_count?: number;
  position_json?: JsonObject | string | null;
  json_metadata?: JsonObject | string | null;
  css?: string | null;
  tags?: Tag[];
}

export interface DashboardPayload {
  dashboard_title?: string;
  slug?: string;
  published?: boolean;
  position_json?: JsonObject;
  json_metadata?: JsonObject;
  css?: string;
  description?: string;
}

export interface DashboardChart extends Chart {
  slice_id?: number;
  form_data?: JsonObject;
  query_context?: JsonObject | null;
}

export interface DashboardChartsResponse {
  result: DashboardChart[];
  count: number;
  ids?: number[];
  metadata?: { dashboard_id: number; dashboard_title: string };
}

/* ------------------------------------------------------------ annotations */

export interface Tag {
  id: number;
  name: string;
  type?: number;
}

export interface AnnotationLayer {
  id: number;
  name: string;
  descr?: string | null;
  created_on?: string | null;
}

export interface Annotation {
  id: number;
  short_descr: string;
  long_descr?: string | null;
  start_dttm?: string | null;
  end_dttm?: string | null;
  layer?: { id: number; name: string } | null;
}

/* ------------------------------------------------------------------- rls */

export interface RlsFilter {
  id: number;
  name: string;
  description?: string | null;
  filter_type: string;
  group_key?: string | null;
  clause: string;
  groupby?: string | null;
  roles: Role[];
  tables: Array<{ id: number; table_name: string; schema?: string | null }>;
}

/* ---------------------------------------------------------------- system */

/** Safe, password-free view of the app's own metadata connection. */
export interface SystemDatabaseConnection {
  driver: string;
  host: string;
  port: string;
  database: string;
  username: string;
  password_configured: boolean;
  uri_masked: string;
  config_path: string;
  config_writable: boolean;
}

export interface SystemDatabaseTestResult {
  ok: boolean;
  message: string;
  latency_ms: number;
}

/* ------------------------------------------------- system overview (console) */

export type ModuleStatus = 'ok' | 'warn' | 'down' | 'off';

export interface SystemModule {
  key: string;
  name: string;
  category: string;
  status: ModuleStatus;
  detail: string;
  latency_ms: number;
  metric: number;
  unit: string;
}

export interface SystemDatabaseItem {
  id: number;
  name: string;
  engine: string;
  status: 'ok' | 'down' | 'unknown';
  latency_ms: number;
  checked_at: string;
  error: string;
}

export interface SystemOverview {
  app: {
    version: string;
    go_version: string;
    started_at: string;
    uptime_sec: number;
    pid: number;
    hostname: string;
    config_path: string;
  };
  modules: SystemModule[];
  databases: {
    total: number;
    ok: number;
    failing: number;
    unknown: number;
    items: SystemDatabaseItem[];
  };
  theme: {
    total: number;
    system_themes: number;
    default_name: string;
    dark_name: string;
  };
  counts: {
    users: number;
    roles: number;
    dashboards: number;
    charts: number;
    datasets: number;
    screens: number;
    queries_24h: number;
  };
  warnings: string[];
}

/* ------------------------------------------------------------ audit logs */

export interface AuditLogEntry {
  id: number;
  action: string;
  user_id?: number | null;
  dashboard_id?: number | null;
  slice_id?: number | null;
  json?: JsonObject | null;
  dttm?: string | null;
  duration_ms?: number | null;
  referrer?: string | null;
}

/* --------------------------------------------------------------- reports */

export interface ReportSchedule {
  id: number;
  name: string;
  type: 'Alert' | 'Report' | string;
  description?: string | null;
  active: boolean;
  crontab: string;
  timezone?: string | null;
  chart_id?: number | null;
  dashboard_id?: number | null;
  database_id?: number | null;
  owners: Owner[];
  last_state?: string | null;
  last_eval_dttm?: string | null;
  changed_on?: string | null;
}

export interface ReportLog {
  id: number;
  state: string;
  scheduled_dttm?: string | null;
  start_dttm?: string | null;
  end_dttm?: string | null;
  error_message?: string | null;
}

/* ----------------------------------------------------------------- theme */

export interface Theme {
  id: number;
  uuid?: string;
  theme_name: string;
  json_data?: JsonObject | null;
  is_default: boolean;
  is_system?: boolean;
  is_system_default?: boolean;
  is_system_dark?: boolean;
  changed_on?: string | null;
}

export interface CssTemplate {
  id: number;
  uuid?: string;
  template_name: string;
  css: string;
  changed_on?: string | null;
}

/* ------------------------------------------------------------------ misc */

export interface UserEntity {
  id: number;
  username: string;
  first_name?: string | null;
  last_name?: string | null;
  email?: string | null;
  active: boolean;
  roles: Role[];
  groups?: Array<{ id: number; name: string }>;
  last_login?: string | null;
  login_count?: number | null;
}

export interface PermissionEntity {
  id: number;
  name: string;
  view_menu?: { id: number; name: string } | null;
}

/** Role list payload: roleJSON includes permissions and derived user count. */
export interface RoleDetail extends Role {
  permissions: Array<{
    id: number;
    name: string;
    view_menu_id?: number;
    view_menu_name?: string;
    permission_view_id?: number;
  }>;
  user_count?: number;
  /** Built-in (seeded) roles cannot be deleted or renamed. */
  is_builtin?: boolean;
}

export interface MenuItem {
  name: string;
  label: string;
  icon?: string;
  url?: string;
  children?: MenuItem[];
}

export interface TimeRangeOption {
  name: string;
  value: string;
  since: string;
  until: string;
}

export interface LogEntry {
  id: number;
  action: string;
  user_id?: number | null;
  dashboard_id?: number | null;
  slice_id?: number | null;
  json?: JsonObject | null;
  dttm?: string | null;
  duration_ms?: number | null;
  referrer?: string | null;
}

export interface InfoResponse {
  version: string;
  database_version: string;
  features: Record<string, boolean>;
}

export * from './types/query';

/* --------------------------------------- dashboard layout & native filters */

/** Row background presets used by `position_json` ROW nodes. */
export type RowBackground =
  | 'BACKGROUND_TRANSPARENT'
  | 'BACKGROUND_LIGHT'
  | 'BACKGROUND_DARK';

/** Minimal dataset projection returned by `GET /dashboard/{id}/datasets`. */
export interface DashboardDataset {
  id: number;
  table_name: string;
  schema?: string | null;
  database_id: number;
  database_name?: string;
  description?: string | null;
  datasource_type?: string;
}

export type NativeFilterType =
  | 'filter_select'
  | 'filter_multi_select'
  | 'filter_range'
  | 'filter_time'
  | 'filter_timecolumn'
  | 'filter_value'
  | 'filter_divider';

/** Dataset/column pair a native filter applies to. */
export interface NativeFilterTarget {
  datasetId: number;
  column: { name: string };
}

/** Free-form per-filter UI switches (multi selection, default first item...). */
export interface NativeFilterControlValues {
  enableEmptyFilter?: boolean;
  multiSelection?: boolean;
  defaultToFirstItem?: boolean;
  sortAscending?: boolean;
  inputMaxWidth?: number;
  [key: string]: unknown;
}

/** Payload pushed to charts and mirrored into `filter_state`. */
export interface NativeFilterDataMask {
  extraFormData?: {
    filters?: Array<{ col: string; op: string; val: unknown }>;
    time_range?: string;
    from_dttm?: string;
    to_dttm?: string;
    granularity_sqla?: string;
  };
  filterState?: { value?: unknown; label?: string };
}

/** One entry of `json_metadata.native_filter_configuration`. */
export interface NativeFilterConfig {
  id: string;
  name: string;
  filterType: NativeFilterType;
  targets: NativeFilterTarget[];
  defaultDataMask?: NativeFilterDataMask;
  controlValues?: NativeFilterControlValues;
  requiredFirst?: boolean;
  description?: string;
}

/** Loose shape of `GET /dashboard/{id}/filter_state` entries. */
export interface DashboardFilterStateEntry {
  id?: string;
  key?: string;
  label?: string;
  value?: unknown;
}
