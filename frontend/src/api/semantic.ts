import { client } from './client';

/**
 * Semantic model (MDL-lite) client — benchmark W-05/W-06/W-07/W-08.
 *
 * Everything is dataset-scoped: the model is a view *of* a dataset, not a
 * separate object that can drift away from it.
 */

const BASE = '/api/v1';

export const semanticEndpoints = {
  models: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/models`,
  coldStart: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/cold-start`,
  rules: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/rules`,
  export: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/export`,
  import: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/import`,
  gaps: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/gaps`,
  applyGap: (datasetID: number | string) => `${BASE}/dataset/${datasetID}/semantic/gaps/apply`,
  model: (id: number | string) => `${BASE}/semantic/model/${id}`,
  publish: (id: number | string) => `${BASE}/semantic/model/${id}/publish`,
  relationships: (id: number | string) => `${BASE}/semantic/model/${id}/relationships`,
  relationship: (id: number | string) => `${BASE}/semantic/relationship/${id}`,
  rule: (id: number | string) => `${BASE}/semantic/rule/${id}`,
} as const;

/** Field kinds. `column` is a passthrough of a physical column. */
export type SemanticFieldKind = 'column' | 'metric' | 'dimension' | 'time';

export interface SemanticModelRow {
  id: number;
  dataset_id: number;
  name: string;
  description?: string;
  primary_key?: string;
  status: string;
  version: number;
  manifest_hash?: string;
  source: string;
  published_on?: string | null;
}

export interface SemanticFieldRow {
  id: number;
  model_id: number;
  name: string;
  kind: SemanticFieldKind;
  expression?: string;
  type?: string;
  description?: string;
  unit?: string;
  enum_values?: string[] | null;
  synonyms?: string[] | null;
  hidden: boolean;
  pii_level: string;
  default_agg?: string;
  is_dttm?: boolean;
  sort_order: number;
}

export interface SemanticRelationshipRow {
  id: number;
  name?: string;
  from_model_id: number;
  to_model_id: number;
  join_type: string;
  condition: string;
}

export interface SemanticRuleRow {
  id: number;
  scope: string;
  dataset_id: number;
  model_id: number;
  category: string;
  title: string;
  body: string;
  spec?: unknown;
  status: string;
  evidence?: string;
  version: number;
}

export interface SemanticIssue {
  severity: 'error' | 'warning';
  code: string;
  field?: string;
  message: string;
  hint?: string;
}

export interface SemanticManifest {
  name: string;
  description?: string;
  primary_key?: string;
  dataset_id: number;
  version: number;
  status: string;
  fields: Array<{
    name: string;
    kind: string;
    expression: string;
    type?: string;
    description?: string;
    unit?: string;
    enum_values?: string[];
    hidden?: boolean;
    pii_level?: string;
  }>;
  relationships?: Array<{ name?: string; from: string; to: string; join_type: string; condition: string }>;
  default_filters?: Array<{ field: string; op: string; value?: unknown; reason?: string }>;
  rules?: Array<{ category: string; title?: string; body: string }>;
  manifest_hash?: string;
}

export interface SemanticModelView {
  model: SemanticModelRow;
  fields: SemanticFieldRow[];
  relationships: SemanticRelationshipRow[];
  rules: SemanticRuleRow[];
  manifest?: SemanticManifest;
  issues: SemanticIssue[];
  publishable: boolean;
}

export interface SemanticFieldInput {
  name: string;
  kind: SemanticFieldKind;
  expression?: string;
  type?: string;
  description?: string;
  unit?: string;
  enum_values?: string[];
  hidden?: boolean;
  pii_level?: string;
  default_agg?: string;
  is_dttm?: boolean;
}

export interface SemanticGap {
  id: string;
  kind: string;
  severity: 'high' | 'medium' | 'low';
  target: string;
  question: string;
  draft: string;
  confidence: string;
  evidence: string;
  apply_mode: string;
}

export interface SemanticGapReport {
  dataset_id: number;
  score: number;
  coverage: Record<string, unknown>;
  counts: Record<string, number>;
  gaps: SemanticGap[];
  sampled: boolean;
  note?: string;
}

/* ------------------------------------------------------------------- models */

export async function listSemanticModels(datasetID: number): Promise<SemanticModelRow[]> {
  const response = await client.get<{ result: SemanticModelRow[] }>(semanticEndpoints.models(datasetID));
  return response.result ?? [];
}

export async function getSemanticModel(id: number): Promise<SemanticModelView> {
  const response = await client.get<{ result: SemanticModelView }>(semanticEndpoints.model(id));
  return response.result;
}

/** Derives a model from the dataset's columns and metrics. */
export async function coldStartSemanticModel(datasetID: number, force = false): Promise<SemanticModelView> {
  const response = await client.post<{ result: SemanticModelView }>(
    `${semanticEndpoints.coldStart(datasetID)}${force ? '?force=true' : ''}`,
    {},
  );
  return response.result;
}

export async function createSemanticModel(
  datasetID: number,
  input: { name: string; description?: string; primary_key?: string; fields?: SemanticFieldInput[] },
): Promise<SemanticModelView> {
  const response = await client.post<{ result: SemanticModelView }>(semanticEndpoints.models(datasetID), input);
  return response.result;
}

export async function updateSemanticModel(
  id: number,
  input: { name?: string; description?: string; primary_key?: string; fields?: SemanticFieldInput[] },
): Promise<SemanticModelView> {
  const response = await client.put<{ result: SemanticModelView }>(semanticEndpoints.model(id), input);
  return response.result;
}

export async function publishSemanticModel(id: number): Promise<SemanticModelView> {
  const response = await client.post<{ result: SemanticModelView }>(semanticEndpoints.publish(id), {});
  return response.result;
}

export async function deleteSemanticModel(id: number): Promise<void> {
  await client.delete(semanticEndpoints.model(id));
}

/* ------------------------------------------------------------ relationships */

export async function createSemanticRelationship(
  modelID: number,
  input: { to_model_id: number; join_type: string; condition: string; name?: string },
): Promise<SemanticRelationshipRow> {
  const response = await client.post<{ result: SemanticRelationshipRow }>(
    semanticEndpoints.relationships(modelID),
    input,
  );
  return response.result;
}

export async function deleteSemanticRelationship(id: number): Promise<void> {
  await client.delete(semanticEndpoints.relationship(id));
}

/* -------------------------------------------------------------------- rules */

export async function listSemanticRules(datasetID: number): Promise<SemanticRuleRow[]> {
  const response = await client.get<{ result: SemanticRuleRow[] }>(semanticEndpoints.rules(datasetID));
  return response.result ?? [];
}

export async function createSemanticRule(
  datasetID: number,
  input: {
    category: string;
    title?: string;
    body: string;
    scope?: string;
    model_id?: number;
    spec?: unknown;
    evidence?: string;
  },
): Promise<SemanticRuleRow> {
  const response = await client.post<{ result: SemanticRuleRow }>(semanticEndpoints.rules(datasetID), input);
  return response.result;
}

export async function updateSemanticRule(
  id: number,
  input: { category?: string; title?: string; body?: string; status?: string; spec?: unknown },
): Promise<SemanticRuleRow> {
  const response = await client.put<{ result: SemanticRuleRow }>(semanticEndpoints.rule(id), input);
  return response.result;
}

export async function deleteSemanticRule(id: number): Promise<void> {
  await client.delete(semanticEndpoints.rule(id));
}

/* --------------------------------------------------------- gaps (W-07) */

/** `sample=true` reads real values to detect enums; it requires explicit consent. */
export async function scanSemanticGaps(datasetID: number, sample = false): Promise<SemanticGapReport> {
  const response = await client.get<{ result: SemanticGapReport }>(
    `${semanticEndpoints.gaps(datasetID)}${sample ? '?sample=1' : ''}`,
  );
  return response.result;
}

export async function applySemanticGap(
  datasetID: number,
  gapId: string,
  value: string,
): Promise<Record<string, unknown>> {
  const response = await client.post<{ result: Record<string, unknown> }>(
    semanticEndpoints.applyGap(datasetID),
    { gap_id: gapId, value },
  );
  return response.result;
}

/** Export URL for a direct download (the browser carries the session cookie). */
export function semanticExportUrl(datasetID: number, format: 'json' | 'yaml' = 'json'): string {
  return `${semanticEndpoints.export(datasetID)}?format=${format}`;
}
