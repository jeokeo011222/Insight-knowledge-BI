/**
 * Agent plane client: the SSE invocation stream plus the capability, history and
 * rollback endpoints mounted under `/api/v1/ai`.
 *
 * The SSE call needs its own `fetch` (the shared `SupersetClient` only speaks
 * JSON), so the frame parser lives here. Every other call reuses the client so
 * 401 refresh, error normalisation and base paths stay in one place.
 */
import { ApiError, client, endpoints, tokenStore } from './client';
import type { ApiErrorBody, JsonObject, Paginated } from './types';

/* ------------------------------------------------------------- AI context */

export type AgentScope = 'dashboard' | 'sqllab' | 'chart' | 'explore' | 'chat';

/** One UI filter forwarded to the Agent (mirrors `native filters` shape). */
export interface AIContextFilter {
  column: string;
  op: string;
  value: unknown;
}

/**
 * Caller context sent with every invocation. Field names are snake_case because
 * the backend decodes into `registry.AIContext` json tags.
 */
export interface AIContext {
  scope: AgentScope;
  dashboard_id?: number;
  chart_id?: number;
  dataset_id?: number;
  locale?: 'zh' | 'en';
  time_range?: string;
  filters?: AIContextFilter[];
  extra?: JsonObject;
}

/* --------------------------------------------------------- SSE event types */

export type AgentEventName =
  | 'plan'
  | 'act'
  | 'observe'
  | 'answer'
  | 'error'
  | 'attached';
export type AgentCapabilityKind = 'skill' | 'tool';
export type AgentObservationStatus = 'ok' | 'error' | 'denied';

export interface PlanStep {
  intent: string;
  dataset?: string;
  metrics?: string[];
  deps?: number[];
}

export interface PlanEvent {
  runId: number;
  sessionId?: string;
  thought: string;
  /** Structured plan produced by the planner call; absent on fallback. */
  steps?: PlanStep[];
  source?: 'model' | 'fallback';
  /** True when the plan is replayed from a persisted run on reconnect. */
  resumed?: boolean;
}

export interface ActEvent {
  stepNo: number;
  kind: AgentCapabilityKind;
  name: string;
  args?: unknown;
}

export interface ObserveEvent {
  stepNo: number;
  status: AgentObservationStatus;
  /** Skill invocations yield a `SkillResultPayload`; tool calls yield raw output. */
  result?: unknown;
  error?: string;
  /** True when the observation is replayed from the audit log on reconnect. */
  replayed?: boolean;
}

export interface AnswerEvent {
  runId: number;
  sessionId?: string;
  markdown: string;
  artifacts?: JsonObject;
  tokenUsage: number;
  stepCount: number;
  /** Observability counters surfaced under the answer. */
  firstTokenMs?: number;
  toolErrorCount?: number;
  emptyResultCount?: number;
  selfCheckCount?: number;
}

export interface ErrorEvent {
  runId: number;
  message: string;
}

export interface AgentStreamHandlers {
  onPlan?: (event: PlanEvent) => void;
  onAct?: (event: ActEvent) => void;
  onObserve?: (event: ObserveEvent) => void;
  onAnswer?: (event: AnswerEvent) => void;
  onError?: (event: ErrorEvent) => void;
  /** The target run is still executing: poll it instead of resuming. */
  onAttached?: (event: AttachedEvent) => void;
}

/** Mirrors the Go `AttachedEvent`: the original run is still live. */
export interface AttachedEvent {
  runId: number;
  status: string;
}

/** One persisted trajectory entry as replayed by the resume endpoint. */
export interface ResumeStep {
  stepNo: number;
  kind: string;
  name: string;
  sideEffect?: string;
  status: string;
  args?: unknown;
  result?: unknown;
  error?: string;
  durationMs?: number;
}

/**
 * Mirrors the Go `ResumeState`: whether a run can be attached to, continued,
 * or is simply over.
 */
export interface ResumeState {
  runId: number;
  sessionId?: string;
  status: string;
  question: string;
  scope?: string;
  plan?: PlanStep[];
  planSource?: string;
  answer?: string;
  error?: string;
  steps?: ResumeStep[];
  tokenUsage: number;
  latencyMs: number;
  resumable: boolean;
  attached: boolean;
  reason?: string;
}

/** Payload of `observe.result` when the invoked capability is a skill. */
export interface SkillResultPayload {
  output?: unknown;
  markdown?: string;
  artifacts?: JsonObject;
  token_usage?: number;
}

/* ------------------------------------------------------- capability types */

export interface CapabilityPermission {
  action: string;
  resource: string;
}

export interface CostHint {
  tier: string;
  tokens: number;
  latency_ms: number;
}

export interface PublicSkill {
  name: string;
  title: string;
  description: string;
  domain: string;
  input_schema?: unknown;
  output_schema?: unknown;
  permissions?: CapabilityPermission[];
  cost_hint?: CostHint;
  /** False = registered but the executor is not wired yet. */
  available?: boolean;
}

export interface PublicTool {
  name: string;
  title: string;
  description: string;
  input_schema?: unknown;
  side_effect: string;
  permissions?: CapabilityPermission[];
  /** False = registered but the executor is not wired yet. */
  available?: boolean;
}

export interface AgentFeatures {
  agent: boolean;
  domain_skills: boolean;
  write_tools: boolean;
  write_confirm_required: boolean;
}

export interface Capabilities {
  skills: PublicSkill[];
  tools: PublicTool[];
  features: AgentFeatures;
}

/* ---------------------------------------------------- run history / rollback */

export interface AgentRun {
  id: number;
  user_id: number;
  session_id?: string | null;
  scope: string;
  dashboard_id?: number | null;
  chart_id?: number | null;
  dataset_id?: number | null;
  question: string;
  status: string;
  step_count: number;
  tool_call_count: number;
  token_usage: number;
  latency_ms: number;
  final_answer?: string | null;
  error_msg?: string | null;
  created_on?: string | null;
  changed_on?: string | null;
  /** Observability counters (audit §4-6). */
  first_token_ms?: number;
  tool_error_count?: number;
  empty_result_count?: number;
  self_check_count?: number;
  /** Reproducibility: which model/params produced this run, and its SQL. */
  model_name?: string | null;
  temperature?: number;
  seed?: number;
  generated_sql?: string | null;
  sql_hash?: string | null;
  plan_json?: string | null;
  resumed_from?: number | null;
}

export interface AgentStep {
  id: number;
  run_id: number;
  step_no: number;
  kind: string;
  name: string;
  side_effect: string;
  input_args?: unknown;
  result?: unknown;
  snapshot?: unknown;
  target_ids?: number[] | null;
  status: string;
  error_msg?: string | null;
  duration_ms: number;
  created_on?: string | null;
}

export interface AgentRunDetail {
  run: AgentRun;
  steps: AgentStep[];
}

/** Mirrors the Go `RollbackResult` (snake_case, per `snapshot.go`). */
export interface RollbackResult {
  run_id: number;
  rolled_back: number;
  skipped: number;
  details: string[];
}

/* ---------------------------------------------------- save-as-chart loop */

/** The `chart_spec` artifact an Agent answer carries for the closed loop. */
export interface ChartSpec {
  dataset_id: number;
  viz_type: string;
  slice_name?: string;
  metrics?: unknown[];
  groupby?: unknown[];
  filters?: unknown[];
  dashboard_id?: number;
}

export interface SaveChartResult {
  chart_id: number;
  slice_name: string;
  dashboard_id?: number;
  added_to_dashboard: boolean;
  viz_type: string;
}

/* ------------------------------------------------------------- endpoints */

const AI_BASE = '/ai';

export const aiEndpoints = {
  invoke: `${AI_BASE}/agent/invoke`,
  skills: `${AI_BASE}/agent/skills`,
  runs: `${AI_BASE}/agent/runs`,
  run: (id: number | string) => `${AI_BASE}/agent/runs/${id}`,
  resume: (id: number | string) => `${AI_BASE}/agent/runs/${id}/resume`,
  rollback: (id: number | string) => `${AI_BASE}/agent/runs/${id}/rollback`,
  settings: `${AI_BASE}/settings`,
  capabilities: `${AI_BASE}/capabilities`,
  providers: `${AI_BASE}/providers`,
  provider: (id: number | string) => `${AI_BASE}/providers/${id}`,
  providerTest: `${AI_BASE}/providers/test`,
  agents: `${AI_BASE}/agents`,
  agent: (id: number | string) => `${AI_BASE}/agents/${id}`,
  skillPlugins: `${AI_BASE}/skills`,
  skillPlugin: (code: string) => `${AI_BASE}/skills/${encodeURIComponent(code)}`,
  toolPlugins: `${AI_BASE}/tools`,
  toolPlugin: (code: string) => `${AI_BASE}/tools/${encodeURIComponent(code)}`,
  saveChart: `${AI_BASE}/chart`,
  audit: `${AI_BASE}/audit`,
  sqlMemory: `${AI_BASE}/sql-memory`,
  sqlMemoryItem: (id: number | string) => `${AI_BASE}/sql-memory/${id}`,
  sqlMemoryApprove: (id: number | string) => `${AI_BASE}/sql-memory/${id}/approve`,
  sqlMemoryReject: (id: number | string) => `${AI_BASE}/sql-memory/${id}/reject`,
  sqlMemoryExport: `${AI_BASE}/sql-memory/export`,
} as const;

/* ------------------------------------------------------------ audit types */

/** One row of the LLM audit trail (ai_audit_log). */
export interface AIAuditEntry {
  id: number;
  run_id?: string | null;
  user_id?: number | null;
  operation?: string | null;
  model_name?: string | null;
  token_usage?: number | null;
  latency_ms?: number | null;
  cache_hit?: boolean | null;
  error_msg?: string | null;
  created_on?: string | null;
}

/** Paginated list of audit entries. */
export async function listAIAuditLogs(page = 1, pageSize = 25): Promise<Paginated<AIAuditEntry>> {
  return client.get<Paginated<AIAuditEntry>>(aiEndpoints.audit, {
    page,
    page_size: pageSize,
  });
}

/* ------------------------------------------------------------ SSE parsing */

/** Rebuilds an ApiError from a non-2xx streaming response. */
async function toApiError(response: Response): Promise<ApiError> {
  let message = `${response.status} ${response.statusText}`.trim();
  try {
    const body = (await response.json()) as ApiErrorBody;
    if (body?.message) {
      message = body.message;
    } else if (body?.errors?.length) {
      message = body.errors.map(item => item.message).filter(Boolean).join('; ') || message;
    }
  } catch {
    // Non JSON body: keep the HTTP status text.
  }
  return new ApiError(message, response.status);
}

/** Dispatches one parsed SSE frame to the matching handler. */
function emitFrame(frame: string, handlers: AgentStreamHandlers): void {
  let name = '';
  const dataLines: string[] = [];
  frame.split('\n').forEach(rawLine => {
    const line = rawLine.replace(/\r$/, '');
    if (line.startsWith('event:')) {
      name = line.slice(6).trim();
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).replace(/^ /, ''));
    }
  });
  if (!name) return;

  const raw = dataLines.join('\n');
  let payload: unknown = {};
  if (raw) {
    try {
      payload = JSON.parse(raw);
    } catch {
      payload = {};
    }
  }

  switch (name as AgentEventName) {
    case 'plan':
      handlers.onPlan?.(payload as PlanEvent);
      break;
    case 'act':
      handlers.onAct?.(payload as ActEvent);
      break;
    case 'observe':
      handlers.onObserve?.(payload as ObserveEvent);
      break;
    case 'answer':
      handlers.onAnswer?.(payload as AnswerEvent);
      break;
    case 'attached':
      handlers.onAttached?.(payload as AttachedEvent);
      break;
    case 'error':
      handlers.onError?.(payload as ErrorEvent);
      break;
    default:
      break;
  }
}

/**
 * POSTs a question and consumes the Agent's plan→act→observe→answer stream.
 *
 * Resolves once the stream ends (answer, error or disconnect); a non-2xx
 * response (e.g. 503 while `AI_ASSISTANT_ENABLED=false`) rejects with ApiError.
 */
export async function streamAgentInvoke(
  question: string,
  context: AIContext,
  handlers: AgentStreamHandlers,
  signal?: AbortSignal,
  sessionId?: string | null,
  agentId?: number | null,
): Promise<void> {
  const response = await postStream(aiEndpoints.invoke, {
    question,
    context,
    session_id: sessionId || undefined,
    // Omitted means the backend falls back to the default agent.
    agent_id: agentId || undefined,
  }, signal);
  await consumeStream(response, handlers);
}

/**
 * Continues a run whose stream was interrupted (audit §4-5). The server
 * replays the persisted plan and steps before emitting live frames, so the
 * caller rebuilds the transcript it lost and then carries on from there.
 *
 * When the original run is still executing the server emits one `attached`
 * frame and closes; the caller should poll `getAgentRun` instead.
 */
export async function streamAgentResume(
  runId: number,
  handlers: AgentStreamHandlers,
  signal?: AbortSignal,
  body?: { question?: string; context?: AIContext; agentId?: number },
): Promise<void> {
  const response = await postStream(aiEndpoints.resume(runId), {
    question: body?.question,
    context: body?.context,
    agent_id: body?.agentId,
  }, signal);
  await consumeStream(response, handlers);
}

/** POSTs a streaming body and returns the raw response (non-2xx rejects). */
async function postStream(
  path: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<Response> {
  const headers: Record<string, string> = {
    Accept: 'text/event-stream',
    'Content-Type': 'application/json',
  };
  const token = tokenStore.access;
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  const response = await fetch(`${endpoints.apiBase}${path}`, {
    method: 'POST',
    headers,
    credentials: 'include',
    body: JSON.stringify(body),
    signal,
  });
  if (!response.ok) {
    throw await toApiError(response);
  }
  return response;
}

/** Reads an SSE body frame by frame, dispatching each to its handler. */
async function consumeStream(
  response: Response,
  handlers: AgentStreamHandlers,
): Promise<void> {
  if (!response.body) {
    // Environments without streaming: parse the buffered body as one chunk.
    emitFrame(await response.text(), handlers);
    return;
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, '\n');
    let boundary = buffer.indexOf('\n\n');
    while (boundary !== -1) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      emitFrame(frame, handlers);
      boundary = buffer.indexOf('\n\n');
    }
  }

  buffer += decoder.decode();
  if (buffer.trim()) {
    emitFrame(buffer, handlers);
  }
}

/* ------------------------------------------------------- capability / history */

/** Fetches the public capability menu (skills + tools + feature switches). */
export async function listAgentSkills(): Promise<Capabilities> {
  const response = await client.get<{ result: Capabilities }>(aiEndpoints.skills);
  return response.result;
}

/** Fetches the current user's Agent run history, newest first. */
export async function listAgentRuns(page = 1, pageSize = 20): Promise<Paginated<AgentRun>> {
  return client.get<Paginated<AgentRun>>(aiEndpoints.runs, {
    page,
    page_size: pageSize,
    order_column: 'created_on',
    order_direction: 'desc',
  });
}

/** Fetches one run together with its step trajectory (tool call log). */
export async function getAgentRun(id: number | string): Promise<AgentRunDetail> {
  const response = await client.get<{ result: AgentRunDetail }>(aiEndpoints.run(id));
  return response.result;
}

/** Inspects whether a run can be attached to, continued, or is over. */
export async function getAgentResume(id: number | string): Promise<ResumeState> {
  const response = await client.get<{ result: ResumeState }>(aiEndpoints.resume(id));
  return response.result;
}

/** Reverts the write operations a run performed, using its pre-images. */
export async function rollbackAgentRun(id: number | string): Promise<RollbackResult> {
  const response = await client.post<{ result: RollbackResult }>(aiEndpoints.rollback(id));
  return response.result;
}

/* ------------------------------------------------------- NL→SQL memory */

/** One confirmed question→SQL example (ai_sql_memory). */
export interface SqlMemoryEntry {
  id: number;
  dataset_id: number;
  question: string;
  sql: string;
  spec?: unknown;
  source: 'seed' | 'confirmed' | 'auto';
  status: 'approved' | 'pending' | 'rejected';
  quality: number;
  run_id?: number | null;
  created_by: number;
  hits: number;
  last_hit_on?: string | null;
  created_on?: string;
  changed_on?: string;
}

/** Payload for "保存为范例". */
export interface SqlMemoryInput {
  dataset_id: number;
  question: string;
  sql: string;
  spec?: JsonObject;
}

/** Stores a human-confirmed example; it becomes recallable immediately. */
export async function saveSqlMemory(input: SqlMemoryInput): Promise<SqlMemoryEntry> {
  const response = await client.post<{ result: SqlMemoryEntry }>(aiEndpoints.sqlMemory, input);
  return response.result;
}

/** Lists the example library (optionally the review queue of auto captures). */
export async function listSqlMemory(params: {
  dataset_id?: number;
  status?: string;
  page?: number;
  page_size?: number;
} = {}): Promise<Paginated<SqlMemoryEntry>> {
  return client.get<Paginated<SqlMemoryEntry>>(aiEndpoints.sqlMemory, params);
}

/** Approves or rejects a captured example. */
export async function reviewSqlMemory(
  id: number | string,
  action: 'approve' | 'reject',
): Promise<SqlMemoryEntry> {
  const url = action === 'approve' ? aiEndpoints.sqlMemoryApprove(id) : aiEndpoints.sqlMemoryReject(id);
  const response = await client.post<{ result: SqlMemoryEntry }>(url);
  return response.result;
}

/** Deletes an example so it is never recalled again. */
export async function deleteSqlMemory(id: number | string): Promise<void> {
  await client.delete(aiEndpoints.sqlMemoryItem(id));
}

/** Persists an Agent answer's chart_spec as a chart (optionally on a dashboard). */
export async function saveAgentChart(spec: ChartSpec): Promise<SaveChartResult> {
  const response = await client.post<{ result: SaveChartResult }>(aiEndpoints.saveChart, spec);
  return response.result;
}

/* ------------------------------------------------------ agent / LLM settings */

/** Global Agent master switch (`PUT /ai/settings`). */
export interface AgentSettings {
  enabled: boolean;
}

/** Payload for saving the master switch. */
export type AgentSettingsInput = AgentSettings;

/** Reads the global Agent master switch. */
export async function getAgentSettings(): Promise<AgentSettings> {
  const response = await client.get<{ result: AgentSettings }>(aiEndpoints.settings);
  return response.result;
}

/** Saves the master switch; the server applies it immediately. */
export async function updateAgentSettings(input: AgentSettingsInput): Promise<AgentSettings> {
  const response = await client.put<{ result: AgentSettings }>(aiEndpoints.settings, input);
  return response.result;
}

/* ------------------------------------------------- LLM providers (供应商) */

/**
 * One configured LLM endpoint. The API key itself is never returned — the
 * server only reports whether one is stored (`api_key_configured`).
 */
export interface AIProvider {
  id: number;
  name: string;
  provider_type: string;
  base_url: string;
  model: string;
  timeout_sec: number;
  enabled: boolean;
  description: string;
  api_key_configured: boolean;
  created_on?: string | null;
  changed_on?: string | null;
}

/** Create/update payload. Omitting `api_key` keeps the stored key. */
export interface AIProviderInput {
  name: string;
  provider_type: string;
  base_url: string;
  model?: string;
  timeout_sec?: number;
  enabled?: boolean;
  description?: string;
  api_key?: string;
}

/** Lists every configured provider. */
export async function listProviders(): Promise<AIProvider[]> {
  const response = await client.get<{ result: AIProvider[] }>(aiEndpoints.providers);
  return response.result;
}

/** Creates a provider. */
export async function createProvider(payload: AIProviderInput): Promise<AIProvider> {
  const response = await client.post<{ result: AIProvider }>(aiEndpoints.providers, payload);
  return response.result;
}

/** Updates a provider. */
export async function updateProvider(id: number, payload: AIProviderInput): Promise<AIProvider> {
  const response = await client.put<{ result: AIProvider }>(aiEndpoints.provider(id), payload);
  return response.result;
}

/** Deletes a provider (refused while an agent still binds it). */
export async function deleteProvider(id: number): Promise<void> {
  await client.delete(aiEndpoints.provider(id));
}

/**
 * Probe body for the provider editor. `api_key` is optional on purpose: an edit
 * dialog never receives the stored secret, so leaving it blank makes the server
 * reuse the saved key instead of testing without authentication.
 */
export interface AIProviderTestInput {
  id?: number;
  provider_type?: string;
  base_url?: string;
  model?: string;
  timeout_sec?: number;
  api_key?: string;
}

/** Result of a connectivity probe (`/models`, or one minimal chat call). */
export interface AIProviderTestResult {
  ok: boolean;
  latency_ms: number;
  method: 'models' | 'chat' | string;
  models?: string[];
  model?: string;
  endpoint: string;
  message: string;
}

/** Tests a provider endpoint without saving it. */
export async function testProvider(payload: AIProviderTestInput): Promise<AIProviderTestResult> {
  const response = await client.post<{ result: AIProviderTestResult }>(
    aiEndpoints.providerTest,
    payload,
  );
  return response.result;
}

/* ---------------------------------------------------- agents (智能体插件) */

/**
 * A plugin-style agent: a bound provider plus the skills/tools it may use.
 * Empty `skill_codes`/`tool_codes` means "every registered capability".
 */
export interface AIAgentConfig {
  id: number;
  name: string;
  description: string;
  provider_id: number;
  provider_name?: string;
  is_default: boolean;
  enabled: boolean;
  /** 产品预置的内置智能体：不可修改、不可删除。 */
  builtin?: boolean;
  skill_codes: string[];
  tool_codes: string[];
  max_steps: number;
  max_tool_calls: number;
  token_budget: number;
  run_timeout_sec: number;
  write_tools_enabled: boolean;
  write_confirm_required: boolean;
  created_on?: string | null;
  changed_on?: string | null;
}

/** Create/update payload for an agent. */
export interface AIAgentInput {
  name: string;
  description?: string;
  provider_id?: number;
  is_default?: boolean;
  enabled?: boolean;
  skill_codes?: string[];
  tool_codes?: string[];
  max_steps?: number;
  max_tool_calls?: number;
  token_budget?: number;
  run_timeout_sec?: number;
  write_tools_enabled?: boolean;
  write_confirm_required?: boolean;
}

/** Lists every configured agent, default first. */
export async function listAgents(): Promise<AIAgentConfig[]> {
  const response = await client.get<{ result: AIAgentConfig[] }>(aiEndpoints.agents);
  return response.result;
}

/** Creates an agent. */
export async function createAgent(payload: AIAgentInput): Promise<AIAgentConfig> {
  const response = await client.post<{ result: AIAgentConfig }>(aiEndpoints.agents, payload);
  return response.result;
}

/** Updates an agent. */
export async function updateAgent(id: number, payload: AIAgentInput): Promise<AIAgentConfig> {
  const response = await client.put<{ result: AIAgentConfig }>(aiEndpoints.agent(id), payload);
  return response.result;
}

/** Deletes an agent. */
export async function deleteAgent(id: number): Promise<void> {
  await client.delete(aiEndpoints.agent(id));
}

/* --------------------------------------------- capability catalogue (插件菜单) */

/** The skill/tool catalogue offered by the agent editor. */
export interface CapabilityCatalog {
  skills: PublicSkill[];
  tools: PublicTool[];
}

/** Fetches every registered skill/tool (no DB access; available while disabled). */
export async function getCapabilityCatalog(): Promise<CapabilityCatalog> {
  const response = await client.get<{ result: CapabilityCatalog }>(aiEndpoints.capabilities);
  return response.result;
}

/* ------------------------------------------------- capability plugins (技能/工具) */

/**
 * One skill definition as the settings page sees it: the DB plugin row merged
 * with the live registry state. `builtin` rows came from code and can only be
 * disabled or re-described; `available` is false when no executor is wired.
 */
export interface SkillPlugin {
  code: string;
  title: string;
  domain: string;
  description: string;
  instructions: string;
  input_schema: string;
  tool_codes: string[];
  cost_tier: string;
  enabled: boolean;
  builtin: boolean;
  available: boolean;
}

/** Create/update body for a skill plugin. */
export interface SkillPluginInput {
  code?: string;
  title: string;
  domain?: string;
  description?: string;
  instructions?: string;
  input_schema?: string;
  tool_codes?: string[];
  cost_tier?: string;
  enabled?: boolean;
}

/** One tool definition as the settings page sees it. */
export interface ToolPlugin {
  code: string;
  title: string;
  description: string;
  input_schema: string;
  side_effect: 'read' | 'write' | 'destructive' | string;
  kind: 'builtin' | 'http' | string;
  method: string;
  url: string;
  headers: string;
  body_template: string;
  timeout_sec: number;
  enabled: boolean;
  builtin: boolean;
  available: boolean;
}

/** Create/update body for a tool plugin. */
export interface ToolPluginInput {
  code?: string;
  title: string;
  description?: string;
  input_schema?: string;
  side_effect?: string;
  kind?: string;
  method?: string;
  url?: string;
  headers?: string;
  body_template?: string;
  timeout_sec?: number;
  enabled?: boolean;
}

/** Lists every skill plugin (built-in and custom). */
export async function listSkillPlugins(): Promise<SkillPlugin[]> {
  const response = await client.get<{ result: SkillPlugin[] }>(aiEndpoints.skillPlugins);
  return response.result;
}

/** Creates a declarative skill. */
export async function createSkillPlugin(payload: SkillPluginInput): Promise<SkillPlugin> {
  const response = await client.post<{ result: SkillPlugin }>(aiEndpoints.skillPlugins, payload);
  return response.result;
}

/** Updates a skill (built-ins only honour enabled/title/description). */
export async function updateSkillPlugin(
  code: string,
  payload: SkillPluginInput,
): Promise<SkillPlugin> {
  const response = await client.put<{ result: SkillPlugin }>(
    aiEndpoints.skillPlugin(code),
    payload,
  );
  return response.result;
}

/** Deletes a custom skill (built-ins are refused by the server). */
export async function deleteSkillPlugin(code: string): Promise<void> {
  await client.delete(aiEndpoints.skillPlugin(code));
}

/** Lists every tool plugin (built-in and custom). */
export async function listToolPlugins(): Promise<ToolPlugin[]> {
  const response = await client.get<{ result: ToolPlugin[] }>(aiEndpoints.toolPlugins);
  return response.result;
}

/** Creates an HTTP tool. */
export async function createToolPlugin(payload: ToolPluginInput): Promise<ToolPlugin> {
  const response = await client.post<{ result: ToolPlugin }>(aiEndpoints.toolPlugins, payload);
  return response.result;
}

/** Updates a tool (built-ins only honour enabled/title/description). */
export async function updateToolPlugin(
  code: string,
  payload: ToolPluginInput,
): Promise<ToolPlugin> {
  const response = await client.put<{ result: ToolPlugin }>(aiEndpoints.toolPlugin(code), payload);
  return response.result;
}

/** Deletes a custom tool (built-ins are refused by the server). */
export async function deleteToolPlugin(code: string): Promise<void> {
  await client.delete(aiEndpoints.toolPlugin(code));
}
