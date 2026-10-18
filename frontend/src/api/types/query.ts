/* ------------------------------------------------------------- query ctx */

import type { JsonObject } from '../types';


export interface QueryFilter {
  col: string;
  op: string;
  val: unknown;
}

export interface QueryExtras {
  time_grain_sqla?: string;
  where?: string;
  having?: string;
}

export interface QueryObject {
  database_id?: number;
  schema?: string;
  sql?: string;
  metrics?: string[];
  groupby?: string[];
  columns?: string[];
  from_dttm?: string | null;
  to_dttm?: string | null;
  granularity?: string | null;
  filters?: QueryFilter[];
  extras?: QueryExtras;
  row_limit?: number;
  row_offset?: number;
  orderby?: Array<[string, boolean]>;
  post_processing?: JsonObject[];
  is_timeseries?: boolean;
  /** Annotation layer ids to overlay on the chart's time axis. */
  annotation_layers?: number[];
}

export interface QueryContext {
  datasource: { id: number; type: string };
  force?: boolean;
  queries: QueryObject[];
  form_data?: JsonObject;
  result_type?: string;
  result_format?: string;
}

/** One annotation event as returned with chart data. */
export interface ChartAnnotation {
  id: number;
  layer_id: number;
  layer: string;
  short_descr: string;
  long_descr?: string;
  start_dttm?: string | null;
  end_dttm?: string | null;
}

export interface QueryResultItem {
  data: Array<Record<string, unknown>>;
  colnames: string[];
  coltypes: string[];
  sql: string;
  query?: string;
  rowcount: number;
  cache_key?: string;
  status?: string;
  applied_filters?: JsonObject[];
  /** Overlay events for the chart's time axis; only present when layers are selected. */
  annotation_data?: ChartAnnotation[];
}

export interface ChartDataResponse {
  result: QueryResultItem[];
  query_context?: QueryContext;
  errors: Array<{ message?: string }>;
}

/* --------------------------------------------------------------- sqllab */

export interface SqlLabColumn {
  column_name?: string;
  name: string;
  type: string;
}

export interface Query {
  id: number;
  client_id?: string;
  database_id: number;
  database_name?: string;
  schema?: string | null;
  sql: string;
  executed_sql?: string | null;
  status: 'running' | 'success' | 'failed' | string;
  start_time?: number | null;
  end_time?: number | null;
  rows?: number | null;
  error_message?: string | null;
  limit?: number | null;
}

export interface SqlLabResult {
  id: number;
  status: 'success' | 'failed' | string;
  data: Array<Record<string, unknown>>;
  columns: SqlLabColumn[];
  query?: Query;
  errors: Array<{ message?: string }>;
}

export interface QueryHistoryItem {
  id: number;
  sql: string;
  status: string;
  start_time?: number | null;
  end_time?: number | null;
  rows?: number | null;
  database_name?: string;
  schema?: string | null;
  tab_name?: string | null;
  error_message?: string | null;
}

export interface SavedQuery {
  id: number;
  uuid?: string;
  label: string;
  sql: string;
  db_id?: number;
  schema?: string | null;
  description?: string | null;
  changed_on?: string | null;
  created_on?: string | null;
}

