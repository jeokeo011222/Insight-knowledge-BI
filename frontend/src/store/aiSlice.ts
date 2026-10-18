import { createAsyncThunk, createSlice } from '@reduxjs/toolkit';
import type { PayloadAction } from '@reduxjs/toolkit';
import {
  getAgentResume,
  getAgentRun,
  listAgentRuns,
  listAgentSkills,
  rollbackAgentRun,
  saveAgentChart,
  streamAgentInvoke,
  streamAgentResume,
} from '../api/ai';
import type {
  ActEvent,
  AIContext,
  AgentCapabilityKind,
  AgentFeatures,
  AgentObservationStatus,
  AgentRun,
  AgentRunDetail,
  AgentStep,
  AnswerEvent,
  Capabilities,
  ChartSpec,
  ObserveEvent,
  PlanEvent,
  PlanStep,
  PublicSkill,
  ResumeState,
  ResumeStep,
  RollbackResult,
  SaveChartResult,
  SkillResultPayload,
} from '../api/ai';
import { ApiError, errorMessage } from '../api/client';
import type { JsonObject } from '../api/types';

/** One rendered entry of the plan→act→observe timeline. */
export interface AgentStreamStep {
  /** Absent for the leading `plan` entry. */
  stepNo?: number;
  kind: 'plan' | AgentCapabilityKind;
  name: string;
  args?: unknown;
  status: 'running' | AgentObservationStatus;
  result?: unknown;
  error?: string;
}

export interface RollbackState {
  runId: number | null;
  loading: boolean;
  result: RollbackResult | null;
  error: string | null;
}

/**
 * Merges the `artifacts` every step produced into one payload (P5.2). Host pages
 * consume it to apply the agent's output — e.g. drop generated SQL into SqlLab
 * or refresh a dashboard whose tiles the agent just composed.
 */
function collectArtifacts(steps: AgentStreamStep[]): JsonObject {
  let merged: JsonObject = {};
  for (const step of steps) {
    const produced = (step.result as SkillResultPayload | undefined)?.artifacts;
    if (produced && Object.keys(produced).length > 0) {
      merged = { ...merged, ...produced };
    }
  }
  return merged;
}

export interface AiState {
  drawerOpen: boolean;
  /**
   * Context/question handed over by an inline trigger (`ChartCard`, `SqlLab`,
   * `ExploreControls`). The shared drawer host reads `pendingContext` instead of
   * its own props so every entry opens on the entity the user clicked.
   */
  pendingContext: AIContext | null;
  pendingQuestion: string;
  skills: PublicSkill[];
  features: AgentFeatures | null;
  skillsLoading: boolean;
  /** True when the backend answers 503 (AI_ASSISTANT_ENABLED=false). */
  disabled: boolean;
  loadError: string | null;
  /** Artifacts produced by the current/last run, applied back to the host page. */
  artifacts: JsonObject;

  streaming: boolean;
  aborted: boolean;
  runId: number | null;
  /** Analysis session id: follow-up questions reuse it to keep context. */
  sessionId: string | null;
  /** Prior Q/A turns of the current session, oldest first. */
  transcript: { question: string; answer: string }[];
  /** Question of the run currently streaming (for the transcript fold). */
  lastQuestion: string;
  plan: string;
  planSteps: PlanStep[];
  steps: AgentStreamStep[];
  answer: AnswerEvent | null;
  runError: string | null;

  history: AgentRun[];
  historyLoading: boolean;
  /** Page already loaded; history grows by "load more" instead of capping at 20. */
  historyPage: number;
  historyTotal: number;
  loadedRun: AgentRunDetail | null;

  /** Run a continuation stream was started from (audit §4-5). */
  resumeFrom: number | null;
  /**
   * Why a resume was refused, or that the run is still live. Held as a code
   * rather than a sentence so the panel translates it.
   */
  resumeNote: ResumeNote | null;

  rollback: RollbackState;
}

/** Outcome of asking to continue a run that turned out not to need it. */
export type ResumeNote = 'running' | 'done' | 'no_steps' | 'unavailable';

const initialState: AiState = {
  drawerOpen: false,
  pendingContext: null,
  pendingQuestion: '',
  skills: [],
  features: null,
  skillsLoading: false,
  disabled: false,
  loadError: null,
  artifacts: {},

  streaming: false,
  aborted: false,
  runId: null,
  sessionId: null,
  transcript: [],
  lastQuestion: '',
  plan: '',
  planSteps: [],
  steps: [],
  answer: null,
  runError: null,

  history: [],
  historyLoading: false,
  historyPage: 1,
  historyTotal: 0,
  loadedRun: null,

  resumeFrom: null,
  resumeNote: null,

  rollback: { runId: null, loading: false, result: null, error: null },
};

/** In-flight SSE request, kept outside the store (it is not serialisable). */
let activeController: AbortController | null = null;

/** Maps a persisted run trajectory onto the live timeline shape. */
export function stepsFromRunDetail(steps: AgentStep[]): AgentStreamStep[] {
  return steps.map(step => ({
    stepNo: step.step_no,
    kind: step.kind === 'skill' ? 'skill' : 'tool',
    name: step.name,
    args: step.input_args,
    status: (step.status || 'ok') as AgentStreamStep['status'],
    result: step.result,
    error: step.error_msg ?? undefined,
  }));
}

/**
 * Same mapping for the resume snapshot, so a replayed `observe` frame lands on
 * the step that already exists (named, with its args) instead of appending an
 * anonymous placeholder.
 */
export function stepsFromResume(steps: ResumeStep[]): AgentStreamStep[] {
  return steps.map(step => ({
    stepNo: step.stepNo,
    kind: step.kind === 'skill' ? 'skill' : 'tool',
    name: step.name,
    args: step.args,
    status: (step.status || 'ok') as AgentStreamStep['status'],
    result: step.result,
    error: step.error || undefined,
  }));
}

export interface RunAgentResult {
  runId: number | null;
  aborted: boolean;
  disabled: boolean;
}

/** Runs the Agent once and folds every SSE frame into the timeline. */
export const runAgent = createAsyncThunk<
  RunAgentResult,
  { question: string; context: AIContext; sessionId?: string | null; agentId?: number | null }
>('ai/runAgent', async ({ question, context, sessionId, agentId }, { dispatch }) => {
    activeController?.abort();
    const controller = new AbortController();
    activeController = controller;
    dispatch(runStarted(question));

    try {
      await streamAgentInvoke(
        question,
        context,
        {
          onPlan: (event: PlanEvent) => dispatch(planReceived(event)),
          onAct: (event: ActEvent) => dispatch(actReceived(event)),
          onObserve: (event: ObserveEvent) => dispatch(observeReceived(event)),
          onAnswer: (event: AnswerEvent) => dispatch(answerReceived(event)),
          onError: event => dispatch(runFailed(event.message)),
        },
        controller.signal,
        sessionId,
        agentId,
      );
      return { runId: null, aborted: false, disabled: false };
    } catch (err) {
      if (controller.signal.aborted) {
        return { runId: null, aborted: true, disabled: false };
      }
      const status = err instanceof ApiError ? err.status : 0;
      dispatch(runFailed(errorMessage(err)));
      return { runId: null, aborted: false, disabled: status === 503 };
    } finally {
      if (activeController === controller) {
        activeController = null;
      }
    }
  },
);

/** Maps the server's refusal reason onto a translatable note code. */
function resumeNoteOf(reason?: string): ResumeNote {
  switch (reason) {
    case 'running':
      return 'running';
    case 'done':
      return 'done';
    case 'no_steps':
      return 'no_steps';
    default:
      return 'unavailable';
  }
}

/**
 * Continues a run whose stream was interrupted (audit §4-5).
 *
 * It first asks the server whether the run is attachable at all: a run that is
 * still executing must NOT be started twice (that would pay for the same
 * answer twice), and a finished run should just be followed up instead.
 */
export const resumeAgentRun = createAsyncThunk<
  RunAgentResult,
  { runId: number; question?: string; context?: AIContext },
  { state: { ai: AiState } }
>('ai/resumeAgentRun', async ({ runId, question, context }, { dispatch, getState }) => {
    const prior = getState().ai;
    let state: ResumeState;
    try {
      state = await getAgentResume(runId);
    } catch (err) {
      dispatch(runFailed(errorMessage(err)));
      return { runId, aborted: false, disabled: false };
    }
    if (!state.resumable) {
      dispatch(resumeRefused(resumeNoteOf(state.reason)));
      return { runId, aborted: false, disabled: false };
    }

    activeController?.abort();
    const controller = new AbortController();
    activeController = controller;
    dispatch(resumeStarted({ resume: state, question: question ?? prior.lastQuestion }));

    try {
      await streamAgentResume(
        runId,
        {
          onPlan: (event: PlanEvent) => dispatch(planReceived(event)),
          onAct: (event: ActEvent) => dispatch(actReceived(event)),
          onObserve: (event: ObserveEvent) => dispatch(observeReceived(event)),
          onAnswer: (event: AnswerEvent) => dispatch(answerReceived(event)),
          onError: event => dispatch(runFailed(event.message)),
          onAttached: () => dispatch(resumeRefused('running')),
        },
        controller.signal,
        { question, context: context ?? prior.pendingContext ?? undefined },
      );
      return { runId, aborted: false, disabled: false };
    } catch (err) {
      if (controller.signal.aborted) {
        return { runId, aborted: true, disabled: false };
      }
      const status = err instanceof ApiError ? err.status : 0;
      dispatch(runFailed(errorMessage(err)));
      return { runId, aborted: false, disabled: status === 503 };
    } finally {
      if (activeController === controller) {
        activeController = null;
      }
    }
  },
);

/** Fetches the capability menu; 503 is reported as "AI disabled". */
export const loadSkills = createAsyncThunk<
  Capabilities,
  void,
  { rejectValue: { message: string; disabled: boolean } }
>('ai/loadSkills', async (_arg, { rejectWithValue }) => {
  try {
    return await listAgentSkills();
  } catch (err) {
    const status = err instanceof ApiError ? err.status : 0;
    return rejectWithValue({ message: errorMessage(err), disabled: status === 503 });
  }
});

/** Fetches one page of run history (page 1 replaces, later pages append). */
export const loadHistory = createAsyncThunk<
  { page: number; rows: AgentRun[]; total: number },
  { page?: number } | void,
  { rejectValue: string }
>('ai/loadHistory', async (arg, { rejectWithValue }) => {
  try {
    const page = (arg && arg.page) || 1;
    const result = await listAgentRuns(page, 20);
    return { page, rows: result.result ?? [], total: result.count ?? 0 };
  } catch (err) {
    return rejectWithValue(errorMessage(err));
  }
});

/** Loads one historical run with its steps. */
export const loadRunDetail = createAsyncThunk<
  AgentRunDetail,
  number,
  { rejectValue: string }
>('ai/loadRunDetail', async (runId, { rejectWithValue }) => {
  try {
    return await getAgentRun(runId);
  } catch (err) {
    return rejectWithValue(errorMessage(err));
  }
});

/** Reverts the write operations of a run. */
export const rollbackRun = createAsyncThunk<
  RollbackResult,
  number,
  { rejectValue: string }
>('ai/rollbackRun', async (runId, { rejectWithValue }) => {
  try {
    return await rollbackAgentRun(runId);
  } catch (err) {
    return rejectWithValue(errorMessage(err));
  }
});

/** Persists an Agent answer's chart_spec as a chart (optionally on a dashboard). */
export const saveChartFromSpec = createAsyncThunk<
  SaveChartResult,
  { spec: ChartSpec },
  { rejectValue: string }
>('ai/saveChartFromSpec', async ({ spec }, { rejectWithValue }) => {
  try {
    return await saveAgentChart(spec);
  } catch (err) {
    return rejectWithValue(errorMessage(err));
  }
});

const aiSlice = createSlice({
  name: 'ai',
  initialState,
  reducers: {
    setDrawerOpen(state, action: PayloadAction<boolean>) {
      state.drawerOpen = action.payload;
      if (!action.payload) {
        // Closing the drawer drops the handover so the next open falls back to
        // the host's own context instead of a stale tile.
        state.pendingContext = null;
        state.pendingQuestion = '';
      }
    },
    /** Opens the shared drawer from an inline trigger, seeding its context. */
    agentPanelOpened(state, action: PayloadAction<{ context?: AIContext; question?: string }>) {
      state.drawerOpen = true;
      state.pendingContext = action.payload.context ?? null;
      state.pendingQuestion = action.payload.question ?? '';
    },
    runStarted(state, action: PayloadAction<string | undefined>) {
      // Fold the finished Q/A pair into the session transcript.
      if (state.answer?.markdown && state.lastQuestion) {
        state.transcript = [
          ...state.transcript,
          { question: state.lastQuestion, answer: state.answer.markdown },
        ];
      }
      state.lastQuestion = action.payload ?? state.lastQuestion;
      state.streaming = true;
      state.aborted = false;
      state.runId = null;
      state.plan = '';
      state.planSteps = [];
      state.steps = [];
      state.answer = null;
      state.runError = null;
      state.loadedRun = null;
      state.pendingQuestion = '';
      state.artifacts = {};
      state.resumeFrom = null;
      state.resumeNote = null;
      state.rollback = { runId: null, loading: false, result: null, error: null };
    },
    planReceived(state, action: PayloadAction<PlanEvent>) {
      state.runId = action.payload.runId || null;
      // The backend hands the session id back on the first frame so follow-ups
      // keep the conversation.
      if (action.payload.sessionId) {
        state.sessionId = action.payload.sessionId;
      }
      state.plan = action.payload.thought ?? '';
      state.planSteps = action.payload.steps ?? [];
      // A replayed plan replaces the previous entry instead of stacking a second
      // one: the run keeps the route it committed to before the disconnect.
      const planStep: AgentStreamStep = {
        kind: 'plan',
        name: 'plan',
        status: 'ok',
        result: action.payload.thought ?? '',
      };
      state.steps = state.steps.length > 0 && state.steps[0].kind === 'plan'
        ? [planStep, ...state.steps.slice(1)]
        : [planStep, ...state.steps];
    },
    /** Enters continuation mode and seeds the timeline from the persisted run. */
    resumeStarted(state, action: PayloadAction<{ resume: ResumeState; question: string }>) {
      const { resume, question } = action.payload;
      state.streaming = true;
      state.aborted = false;
      state.runError = null;
      state.resumeNote = null;
      state.resumeFrom = resume.runId;
      state.answer = null;
      state.pendingQuestion = '';
      state.loadedRun = null;
      state.sessionId = resume.sessionId ?? state.sessionId;
      if (question) {
        state.lastQuestion = question;
      }
      // The timeline is rebuilt from the audit log: name + args survive, so the
      // replayed observe frames update entries in place.
      state.steps = stepsFromResume(resume.steps ?? []);
      state.planSteps = resume.plan ?? [];
      state.artifacts = collectArtifacts(state.steps);
    },
    /** Reports why a run did not need (or could not take) a continuation. */
    resumeRefused(state, action: PayloadAction<ResumeNote>) {
      state.resumeNote = action.payload;
    },
    resumeNoteCleared(state) {
      state.resumeNote = null;
    },
    actReceived(state, action: PayloadAction<ActEvent>) {
      state.steps = [
        ...state.steps,
        {
          stepNo: action.payload.stepNo,
          kind: action.payload.kind,
          name: action.payload.name,
          args: action.payload.args,
          status: 'running',
        },
      ];
    },
    observeReceived(state, action: PayloadAction<ObserveEvent>) {
      // A replayed frame carries the stepNo of a step that already exists, so
      // match on the number first and only fall back to "a running step".
      let index = state.steps.findIndex(
        step => step.stepNo === action.payload.stepNo && step.status === 'running',
      );
      if (index < 0) {
        index = state.steps.findIndex(step => step.stepNo === action.payload.stepNo);
      }
      const patch: Partial<AgentStreamStep> = {
        status: action.payload.status,
        result: action.payload.result,
        error: action.payload.error,
      };
      if (index >= 0) {
        state.steps[index] = { ...state.steps[index], ...patch };
      } else {
        state.steps = [...state.steps, { kind: 'tool', name: '', ...patch } as AgentStreamStep];
      }
      state.artifacts = collectArtifacts(state.steps);
    },
    answerReceived(state, action: PayloadAction<AnswerEvent>) {
      state.runId = action.payload.runId || state.runId;
      if (action.payload.sessionId) {
        state.sessionId = action.payload.sessionId;
      }
      state.answer = action.payload;
    },
    runFailed(state, action: PayloadAction<string>) {
      state.runError = action.payload;
      state.steps = state.steps.map(step =>
        step.status === 'running' ? { ...step, status: 'error', error: action.payload } : step,
      );
    },
    runReset(state) {
      state.streaming = false;
      state.aborted = false;
      state.runId = null;
      // Resetting also opens a fresh analysis session.
      state.sessionId = null;
      state.transcript = [];
      state.plan = '';
      state.planSteps = [];
      state.steps = [];
      state.answer = null;
      state.runError = null;
      state.loadedRun = null;
      state.pendingQuestion = '';
      state.artifacts = {};
      state.resumeFrom = null;
      state.resumeNote = null;
    },
    /** Shows a historical run inside the same timeline. */
    runDetailShown(state, action: PayloadAction<AgentRunDetail>) {
      state.loadedRun = action.payload;
      state.streaming = false;
      state.aborted = false;
      state.runId = action.payload.run.id;
      state.plan = action.payload.run.question;
      state.planSteps = [];
      state.steps = stepsFromRunDetail(action.payload.steps ?? []);
      state.artifacts = collectArtifacts(state.steps);
      state.runError = action.payload.run.error_msg ?? null;
      state.answer = action.payload.run.final_answer
        ? {
            runId: action.payload.run.id,
            markdown: action.payload.run.final_answer,
            tokenUsage: action.payload.run.token_usage,
            stepCount: action.payload.run.step_count,
          }
        : null;
    },
  },
  extraReducers: builder => {
    builder
      .addCase(runAgent.fulfilled, (state, action) => {
        state.streaming = false;
        state.aborted = action.payload.aborted;
        if (action.payload.disabled) {
          state.disabled = true;
        }
      })
      .addCase(resumeAgentRun.fulfilled, (state, action) => {
        state.streaming = false;
        state.aborted = action.payload.aborted;
        if (action.payload.disabled) {
          state.disabled = true;
        }
      })
      .addCase(loadSkills.pending, state => {
        state.skillsLoading = true;
        state.loadError = null;
      })
      .addCase(loadSkills.fulfilled, (state, action) => {
        state.skillsLoading = false;
        state.disabled = false;
        state.skills = action.payload.skills ?? [];
        state.features = action.payload.features;
      })
      .addCase(loadSkills.rejected, (state, action) => {
        state.skillsLoading = false;
        state.loadError = action.payload?.message ?? null;
        state.disabled = action.payload?.disabled ?? false;
      })
      .addCase(loadHistory.pending, state => {
        state.historyLoading = true;
      })
      .addCase(loadHistory.fulfilled, (state, action) => {
        state.historyLoading = false;
        state.historyPage = action.payload.page;
        state.historyTotal = action.payload.total;
        state.history =
          action.payload.page <= 1
            ? action.payload.rows
            : [...state.history, ...action.payload.rows];
      })
      .addCase(loadHistory.rejected, state => {
        state.historyLoading = false;
      })
      .addCase(loadRunDetail.fulfilled, (state, action) => {
        state.loadedRun = action.payload;
      })
      .addCase(rollbackRun.pending, (state, action) => {
        state.rollback = { runId: action.meta.arg, loading: true, result: null, error: null };
      })
      .addCase(rollbackRun.fulfilled, (state, action) => {
        state.rollback = {
          runId: action.payload.run_id,
          loading: false,
          result: action.payload,
          error: null,
        };
      })
      .addCase(rollbackRun.rejected, (state, action) => {
        state.rollback = {
          runId: state.rollback.runId,
          loading: false,
          result: null,
          error: action.payload ?? 'rollback failed',
        };
      });
  },
});

export const {
  setDrawerOpen,
  agentPanelOpened,
  runStarted,
  planReceived,
  actReceived,
  observeReceived,
  answerReceived,
  runFailed,
  runReset,
  runDetailShown,
  resumeStarted,
  resumeRefused,
  resumeNoteCleared,
} = aiSlice.actions;

/** Cancels the in-flight stream (used by the panel's stop button). */
export function cancelAgentRun(): void {
  activeController?.abort();
  activeController = null;
}

export default aiSlice.reducer;
