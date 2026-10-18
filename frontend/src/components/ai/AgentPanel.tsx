import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Alert, Button, Drawer, Empty, Input, List, Space, Tabs, Tag, Typography, message } from 'antd';
import {
  BookOutlined,
  CommentOutlined,
  CopyOutlined,
  PlayCircleOutlined,
  ReloadOutlined,
  SaveOutlined,
  SendOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import AgentStepTimeline from './AgentStepTimeline';
import AiSkillResult from './AiSkillResult';
import AiStreamingText from './AiStreamingText';
import AiWriteConfirm from './AiWriteConfirm';
import MarkdownBox from '../dashboard/MarkdownBox';
import { useAppDispatch, useAppSelector, usePermission } from '../../store';
import {
  agentPanelOpened,
  cancelAgentRun,
  loadHistory,
  loadRunDetail,
  loadSkills,
  resumeAgentRun,
  resumeNoteCleared,
  rollbackRun,
  runAgent,
  runDetailShown,
  runReset,
  saveChartFromSpec,
  setDrawerOpen,
} from '../../store/aiSlice';
import { saveSqlMemory } from '../../api/ai';
import type { AIContext, ChartSpec, SkillResultPayload } from '../../api/ai';
import type { JsonObject } from '../../api/types';
import { useI18n } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;
const { TextArea } = Input;

/** Width below which the drawer takes the whole viewport. */
const NARROW_QUERY = '(max-width: 768px)';

/** Why a resume was refused, mapped to its message key. */
const RESUME_NOTE_KEY = {
  running: 'ai.resume.running',
  done: 'ai.resume.done',
  no_steps: 'ai.resume.no_steps',
  unavailable: 'ai.resume.unavailable',
} as const;

export interface AgentPanelProps {
  /** Page/entity context forwarded to the Agent with every question. */
  context: AIContext;
  /** Called with the artifacts of a finished answer (e.g. a chart page spec). */
  onArtifacts?: (artifacts: JsonObject) => void;
  /**
   * `floating` (default) renders the dashboard entry plus the drawer,
   * `inline` renders only a trigger button, `host` renders only the drawer.
   * Page-level triggers keep a `host` alongside them so every entry opens one
   * shared drawer; grid tiles (chart cards) reuse the dashboard's host.
   */
  variant?: 'floating' | 'inline' | 'host';
  /** Question pre-filled into the composer when this trigger is clicked. */
  question?: string;
  /** Trigger label; omit it for an icon-only trigger. */
  label?: string;
  icon?: ReactNode;
}

/** True while the viewport is narrower than the mobile breakpoint. */
function useIsNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== 'undefined' && window.matchMedia(NARROW_QUERY).matches,
  );
  useEffect(() => {
    const query = window.matchMedia(NARROW_QUERY);
    const onChange = () => setNarrow(query.matches);
    onChange();
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);
  return narrow;
}

/**
 * The unified 「智观 BI」 Agent panel: a floating button opening a drawer that
 * streams the plan→act→observe trajectory, renders per-skill result cards and
 * exposes the write/rollback entry point.
 */
export default function AgentPanel({
  context,
  onArtifacts,
  variant = 'floating',
  question,
  label,
  icon = <CommentOutlined />,
}: AgentPanelProps) {
  const { t, lang } = useI18n();
  const dispatch = useAppDispatch();
  const { can } = usePermission();
  const ai = useAppSelector(state => state.ai);

  const [draft, setDraft] = useState('');
  const [tab, setTab] = useState('chat');
  const [bootstrapped, setBootstrapped] = useState(false);
  const [savingChart, setSavingChart] = useState(false);
  const [savingMemory, setSavingMemory] = useState(false);
  const narrow = useIsNarrow();
  const bottomRef = useRef<HTMLDivElement | null>(null);
  // Artifacts are handed to the host page exactly once per run: the old effect
  // fired on every observe frame, so the dashboard reloaded repeatedly.
  const appliedArtifactsRef = useRef<string>('');

  const canUse = can('can_query', 'AIAnalysis');
  const canRollback = can('can_write', 'AIAgent');

  // The drawer host serves every entry point, so it follows the handed-over
  // context when a tile trigger opened it, falling back to its own props.
  const effectiveContext = ai.pendingContext ?? context;

  // Load the capability menu and the run history the first time the drawer opens.
  useEffect(() => {
    if (!ai.drawerOpen || bootstrapped) return;
    setBootstrapped(true);
    dispatch(loadSkills());
    dispatch(loadHistory());
  }, [ai.drawerOpen, bootstrapped, dispatch]);

  // Seed the composer with the question an inline trigger pre-selected.
  useEffect(() => {
    if (ai.pendingQuestion) setDraft(ai.pendingQuestion);
  }, [ai.pendingQuestion]);

  // Hand the run's artifacts to the host page (SQL into the editor, dashboard
  // refresh) — once per unique payload, not once per streamed frame.
  useEffect(() => {
    if (!onArtifacts || Object.keys(ai.artifacts).length === 0) return;
    const key = JSON.stringify({
      run: ai.runId,
      chartIds: ai.artifacts.chart_ids ?? null,
      dashboardId: ai.artifacts.dashboard_id ?? null,
      sql: ai.artifacts.sql ?? null,
    });
    if (appliedArtifactsRef.current === key) return;
    appliedArtifactsRef.current = key;
    onArtifacts(ai.artifacts);
  }, [ai.artifacts, ai.runId, onArtifacts]);

  // Keep the newest step / answer visible while the run streams.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }, [ai.steps.length, ai.answer, ai.transcript.length]);

  if (!canUse) return null;

  const sendText = (text: string) => {
    const value = text.trim();
    if (!value) {
      message.warning(t('ai.input.required'));
      return;
    }
    dispatch(
      runAgent({
        question: value,
        context: { ...effectiveContext, locale: effectiveContext.locale ?? lang },
        // Follow-up questions continue the analysis session.
        sessionId: ai.sessionId,
      }),
    );
  };

  const send = () => sendText(draft);

  /** The question of the last run, used by "regenerate"/"retry". */
  const lastQuestion =
    ai.lastQuestion ||
    (ai.transcript.length > 0 ? ai.transcript[ai.transcript.length - 1].question : '');

  const regenerate = () => {
    if (!lastQuestion) {
      message.warning(t('ai.input.required'));
      return;
    }
    sendText(lastQuestion);
  };

  /**
   * Continues a run whose stream was cut (network drop, tab refresh, SSE
   * timeout). The server replays the persisted plan and steps, so the user pays
   * only for the remaining work instead of re-asking from scratch.
   */
  const resumeRun = (runId: number | null) => {
    if (!runId || ai.streaming) return;
    dispatch(resumeNoteCleared());
    dispatch(
      resumeAgentRun({
        runId,
        question: lastQuestion || undefined,
        context: { ...effectiveContext, locale: effectiveContext.locale ?? lang },
      }),
    );
  };

  const copyAnswer = async () => {    const text = ai.answer?.markdown ?? '';
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      message.success(t('ai.actions.copied'));
    } catch {
      message.error(t('ai.actions.copy_failed'));
    }
  };

  /** One-click "save as chart": persists the answer's chart_spec artifact. */
  const chartSpec = (ai.artifacts.chart_spec ?? null) as ChartSpec | null;
  const saveAsChart = async () => {
    if (!chartSpec?.dataset_id) {
      message.warning(t('ai.save_chart.unavailable'));
      return;
    }
    setSavingChart(true);
    try {
      const result = await dispatch(
        saveChartFromSpec({
          spec: { ...chartSpec, dashboard_id: effectiveContext.dashboard_id },
        }),
      ).unwrap();
      message.success(
        result.added_to_dashboard
          ? t('ai.save_chart.success_dashboard', { name: result.slice_name })
          : t('ai.save_chart.success', { name: result.slice_name }),
      );
      // Let the host page pick the new chart up (e.g. refresh the dashboard).
      if (onArtifacts && result.added_to_dashboard) {
        onArtifacts({ chart_ids: [result.chart_id], dashboard_id: result.dashboard_id });
      }
    } catch (err) {
      message.error(String(err || t('ai.save_chart.unavailable')));
    } finally {
      setSavingChart(false);
    }
  };

  /**
   * "保存为范例": stores the question and the SQL that actually ran as a
   * confirmed example. The next similar question reuses it (NL→SQL memory).
   */
  const generatedSql = typeof ai.artifacts.sql === 'string' ? ai.artifacts.sql : '';
  const memoryDatasetId = effectiveContext.dataset_id;
  const saveAsExample = async () => {
    if (!generatedSql || !memoryDatasetId || !lastQuestion) {
      message.warning(t('ai.memory.unavailable'));
      return;
    }
    setSavingMemory(true);
    try {
      await saveSqlMemory({
        dataset_id: memoryDatasetId,
        question: lastQuestion,
        sql: generatedSql,
        spec: (ai.artifacts.chart_spec ?? undefined) as JsonObject | undefined,
      });
      message.success(t('ai.memory.saved'));
    } catch (err) {
      message.error(String(err || t('ai.memory.unavailable')));
    } finally {
      setSavingMemory(false);
    }
  };

  /** Opens the shared drawer seeded with this trigger's context and question. */
  const openFromTrigger = () => dispatch(agentPanelOpened({ context, question }));

  const openRun = async (runId: number) => {
    try {
      const detail = await dispatch(loadRunDetail(runId)).unwrap();
      dispatch(runDetailShown(detail));
      setTab('chat');
    } catch {
      // The slice already carries the failure message.
    }
  };

  const skillSteps = ai.steps.filter(
    step => step.kind === 'skill' && step.status === 'ok' && step.result !== undefined,
  );
  const observedSteps = ai.steps.filter(step => step.kind !== 'plan').length;
  const hasMoreHistory = ai.history.length < ai.historyTotal;

  const chatPane = (
    <div className={styles.stack}>
      <Text className={styles.subtitle}>{t('ai.panel.subtitle')}</Text>

      {ai.disabled && (
        <Alert
          type="warning"
          showIcon
          message={t('ai.disabled.title')}
          description={t('ai.disabled.desc')}
        />
      )}
      {!ai.disabled && ai.loadError && <Alert type="error" showIcon message={ai.loadError} />}

      {ai.skills.length > 0 && (
        <div>
          <div className={styles.sectionTitle}>
            <Text strong>{t('ai.skills.title')}</Text>
          </div>
          <Text className={styles.hint}>{t('ai.skills.hint')}</Text>
          <div className={styles.capabilities}>
            {ai.skills.map(skill => (
              <Tag
                key={skill.name}
                className={styles.capabilityTag}
                color={skill.domain && skill.domain !== 'general' ? 'cyan' : undefined}
                onClick={() => setDraft(t('ai.skills.prefill', { title: skill.title }))}
              >
                {skill.title}
              </Tag>
            ))}
          </div>
        </div>
      )}
      {!ai.skillsLoading && !ai.disabled && !ai.loadError && ai.skills.length === 0 && (
        <Text className={styles.hint}>{t('ai.skills.empty')}</Text>
      )}

      <TextArea
        rows={3}
        value={draft}
        disabled={ai.streaming || ai.disabled}
        placeholder={t('ai.input.placeholder')}
        aria-label={t('ai.input.placeholder')}
        onChange={event => setDraft(event.target.value)}
        onKeyDown={event => {
          // Cmd/Ctrl+Enter sends without leaving the keyboard.
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            if (!ai.streaming && !ai.disabled) send();
          }
        }}
      />
      <Space size={8} wrap align="center">
        <Button
          type="primary"
          icon={<SendOutlined />}
          loading={ai.streaming}
          disabled={ai.disabled}
          onClick={send}
        >
          {t('ai.actions.send')}
        </Button>
        {ai.streaming && (
          <Button onClick={() => cancelAgentRun()}>{t('ai.actions.stop')}</Button>
        )}
        <Button
          onClick={() => {
            cancelAgentRun();
            dispatch(runReset());
            setDraft('');
            appliedArtifactsRef.current = '';
          }}
        >
          {t('ai.actions.reset')}
        </Button>
        <Text className={styles.hint}>{t('ai.input.hint')}</Text>
      </Space>

      {ai.transcript.length > 0 && (
        <div className={styles.stack}>
          <Text strong>{t('ai.session.title')}</Text>
          {ai.transcript.map((turn, index) => (
            <div key={index} className={styles.sessionTurn}>
              <Text className={styles.hint}>Q：{turn.question}</Text>
              <MarkdownBox text={turn.answer} />
            </div>
          ))}
          {ai.sessionId && <Tag>{t('ai.session.tag')}</Tag>}
        </div>
      )}

      <AiStreamingText
        streaming={ai.streaming}
        aborted={ai.aborted}
        stepCount={observedSteps}
      />
      {ai.runError && (
        <Alert
          type="error"
          showIcon
          message={t('ai.error.title')}
          description={ai.runError}
          action={
            <Space size={6} wrap>
              {ai.runId ? (
                <Button size="small" icon={<PlayCircleOutlined />} onClick={() => resumeRun(ai.runId)}>
                  {t('ai.actions.resume')}
                </Button>
              ) : null}
              {lastQuestion ? (
                <Button size="small" icon={<ReloadOutlined />} onClick={regenerate}>
                  {t('ai.actions.retry')}
                </Button>
              ) : null}
            </Space>
          }
        />
      )}

      {ai.resumeNote && (
        <Alert
          type="info"
          showIcon
          closable
          onClose={() => dispatch(resumeNoteCleared())}
          message={t(RESUME_NOTE_KEY[ai.resumeNote])}
        />
      )}

      {(ai.plan || ai.planSteps.length > 0) && (
        <div className={styles.stack}>
          <Space size={6} wrap>
            <Text strong>{t('ai.plan.title')}</Text>
            {ai.planSteps.length > 0 && (
              <Tag>{t('ai.plan.steps', { count: ai.planSteps.length })}</Tag>
            )}
          </Space>
          <Text className={styles.hint}>{ai.plan}</Text>
          {ai.planSteps.length > 0 && (
            <ol className={styles.planList}>
              {ai.planSteps.map((step, index) => (
                <li key={index}>
                  <Text>{step.intent}</Text>
                  {step.dataset ? <Text className={styles.hint}> · {step.dataset}</Text> : null}
                  {step.metrics && step.metrics.length > 0 ? (
                    <Text className={styles.hint}> · {step.metrics.join(', ')}</Text>
                  ) : null}
                </li>
              ))}
            </ol>
          )}
        </div>
      )}

      <Text strong>{t('ai.timeline.title')}</Text>
      <AgentStepTimeline steps={ai.steps} />

      {skillSteps.map((step, index) => (
        <AiSkillResult
          key={`${step.name}-${step.stepNo ?? index}`}
          name={step.name}
          payload={(step.result ?? {}) as SkillResultPayload}
        />
      ))}

      <AiWriteConfirm
        runId={ai.runId}
        steps={ai.steps}
        canRollback={canRollback}
        confirmRequired={ai.features?.write_confirm_required ?? false}
        rollback={ai.rollback}
        onRollback={runId => dispatch(rollbackRun(runId))}
      />

      {ai.answer && (
        <div className={styles.stack}>
          <Space size={6} wrap>
            <Text strong>{t('ai.answer.title')}</Text>
            <Button
              size="small"
              type="text"
              icon={<CopyOutlined />}
              onClick={copyAnswer}
              title={t('ai.actions.copy')}
              aria-label={t('ai.actions.copy')}
            />
            <Button
              size="small"
              type="text"
              icon={<ReloadOutlined />}
              onClick={regenerate}
              title={t('ai.actions.regenerate')}
              aria-label={t('ai.actions.regenerate')}
            />
          </Space>
          <div className={styles.answerBox}>
            {ai.answer.markdown ? (
              <MarkdownBox text={ai.answer.markdown} />
            ) : (
              <Text className={styles.hint}>{t('ai.empty.answer')}</Text>
            )}
          </div>
          <Space size={6} wrap>
            <Tag>{t('ai.answer.steps', { count: ai.answer.stepCount })}</Tag>
            <Tag>{t('ai.answer.tokens', { count: ai.answer.tokenUsage })}</Tag>
            {ai.answer.firstTokenMs ? (
              <Tag>{t('ai.metrics.first_token', { ms: ai.answer.firstTokenMs })}</Tag>
            ) : null}
            {ai.answer.emptyResultCount ? (
              <Tag color="orange">
                {t('ai.metrics.empty_results', { count: ai.answer.emptyResultCount })}
              </Tag>
            ) : null}
            {ai.answer.selfCheckCount ? (
              <Tag color="blue">
                {t('ai.metrics.self_check', { count: ai.answer.selfCheckCount })}
              </Tag>
            ) : null}
            {chartSpec?.dataset_id && (
              <Button
                size="small"
                icon={<SaveOutlined />}
                loading={savingChart}
                onClick={saveAsChart}
              >
                {t('ai.save_chart.button')}
              </Button>
            )}
            {generatedSql && memoryDatasetId && lastQuestion ? (
              <Button
                size="small"
                icon={<BookOutlined />}
                loading={savingMemory}
                onClick={() => void saveAsExample()}
                title={t('ai.memory.hint')}
              >
                {t('ai.memory.button')}
              </Button>
            ) : null}
          </Space>

          {/* Follow-up suggestions: one click continues the analysis session. */}
          <div className={styles.capabilities}>
            <Text className={styles.hint}>{t('ai.suggest.title')}</Text>
            {[t('ai.suggest.by_gender'), t('ai.suggest.yoy'), t('ai.suggest.topn')].map(text => (
              <Tag
                key={text}
                className={styles.capabilityTag}
                onClick={() => sendText(text)}
                style={{ cursor: ai.streaming ? 'not-allowed' : 'pointer' }}
              >
                {text}
              </Tag>
            ))}
          </div>
        </div>
      )}

      {/* Scroll anchor: keeps the newest content in view while streaming. */}
      <div ref={bottomRef} />
    </div>
  );

  const historyPane = (
    <>
      <List
        size="small"
        loading={ai.historyLoading}
        dataSource={ai.history}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={<Text className={styles.hint}>{t('ai.history.empty')}</Text>}
            />
          ),
        }}
        renderItem={run => (
          <List.Item
            actions={[
              // Only an interrupted run has something left to do; a finished
              // one is continued by asking a follow-up question instead.
              run.status === 'failed' || run.status === 'stopped' ? (
                <Button
                  key="resume"
                  type="link"
                  size="small"
                  icon={<PlayCircleOutlined />}
                  disabled={ai.streaming}
                  onClick={() => {
                    setTab('chat');
                    resumeRun(run.id);
                  }}
                >
                  {t('ai.actions.resume')}
                </Button>
              ) : null,
              <Button key="open" type="link" size="small" onClick={() => openRun(run.id)}>
                {t('ai.actions.view_run')}
              </Button>,
            ]}
          >
            <List.Item.Meta
              title={
                <Space size={6} wrap>
                  <Tag>{run.status}</Tag>
                  <Text>{run.question}</Text>
                </Space>
              }
              description={
                <Text className={styles.hint}>
                  {run.created_on ? dayjs(run.created_on).format('YYYY-MM-DD HH:mm') : ''}
                </Text>
              }
            />
          </List.Item>
        )}
      />
      {hasMoreHistory && (
        <Button
          block
          size="small"
          loading={ai.historyLoading}
          onClick={() => dispatch(loadHistory({ page: ai.historyPage + 1 }))}
        >
          {t('ai.actions.load_more')}
        </Button>
      )}
    </>
  );

  const drawer = (
    <Drawer
      open={ai.drawerOpen}
      width={narrow ? '100%' : 460}
      onClose={() => dispatch(setDrawerOpen(false))}
      title={
        <Space size={6} align="center" wrap>
          <span className={styles.brandName}>{t('ai.brand')}</span>
          <span className={styles.brandBadge}>Agent</span>
          <Text className={styles.hint}>{t('ai.panel.title')}</Text>
        </Space>
      }
    >
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          { key: 'chat', label: t('ai.tabs.chat'), children: chatPane },
          { key: 'history', label: t('ai.tabs.history'), children: historyPane },
        ]}
      />
    </Drawer>
  );

  if (variant === 'inline') {
    return (
      <Button size="small" icon={icon} title={label} onClick={openFromTrigger}>
        {label}
      </Button>
    );
  }

  if (variant === 'host') return drawer;

  return (
    <>
      <Button
        className={styles.floating}
        type="primary"
        shape="round"
        size="large"
        icon={icon}
        onClick={() => dispatch(setDrawerOpen(true))}
      >
        {t('ai.trigger.open')}
      </Button>
      {drawer}
    </>
  );
}
