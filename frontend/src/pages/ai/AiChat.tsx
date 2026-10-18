import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Collapse, Empty, Input, Popover, Select, Space, Spin, Tag, Typography, message } from 'antd';
import {
  CloseOutlined,
  CopyOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  PlusOutlined,
  ReloadOutlined,
  RobotOutlined,
  SendOutlined,
  StopOutlined,
} from '@ant-design/icons';
import AgentStepTimeline from '../../components/ai/AgentStepTimeline';
import AiMarkdown from '../../components/ai/AiMarkdown';
import AiSkillResult from '../../components/ai/AiSkillResult';
import AiStreamingText from '../../components/ai/AiStreamingText';
import { client, downloadBlob, endpoints, errorMessage } from '../../api/client';
import { listAgents } from '../../api/ai';
import type { AIAgentConfig, AIContext, SkillResultPayload } from '../../api/ai';
import type { Dataset, Paginated } from '../../api/types';
import { useAppDispatch, useAppSelector, usePermission } from '../../store';
import { cancelAgentRun, runAgent, runReset } from '../../store/aiSlice';
import { useI18n } from '../../i18n';
import styles from './aiChat.module.css';

const { Text } = Typography;
const { TextArea } = Input;

/** Value standing for "let the backend pick the default agent". */
const DEFAULT_AGENT = 0;

export default function AiChat() {
  const { t, lang } = useI18n();
  const dispatch = useAppDispatch();
  const { can } = usePermission();
  const ai = useAppSelector(state => state.ai);
  const canUse = can('can_query', 'AIAnalysis');

  const [draft, setDraft] = useState('');
  const [agents, setAgents] = useState<AIAgentConfig[]>([]);
  const [agentId, setAgentId] = useState<number>(DEFAULT_AGENT);
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [datasetsLoaded, setDatasetsLoaded] = useState(false);
  const [datasetsLoading, setDatasetsLoading] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [attached, setAttached] = useState<Dataset | null>(null);

  const bottomRef = useRef<HTMLDivElement | null>(null);

  // Agents are admin-only to list, so a refusal simply leaves the default.
  useEffect(() => {
    if (!canUse) return;
    let cancelled = false;
    listAgents()
      .then(list => {
        if (cancelled) return;
        setAgents(list);
        const preferred = list.find(agent => agent.is_default) ?? list[0];
        if (preferred) setAgentId(preferred.id);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [canUse]);

  const loadDatasets = useCallback(async () => {
    setDatasetsLoading(true);
    try {
      const response = await client.get<Paginated<Dataset>>(endpoints.datasets, {
        page: 1,
        page_size: 100,
      });
      setDatasets(response.result ?? []);
      setDatasetsLoaded(true);
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setDatasetsLoading(false);
    }
  }, []);

  const openPicker = (next: boolean) => {
    setPickerOpen(next);
    if (next && !datasetsLoaded && !datasetsLoading) void loadDatasets();
  };

  const context = useMemo<AIContext>(
    () => ({ scope: 'chat', locale: lang, dataset_id: attached?.id }),
    [attached, lang],
  );

  const turns = ai.transcript;
  const hasCurrentTurn = ai.streaming || !!ai.plan || ai.steps.length > 0 || !!ai.answer || !!ai.runError;

  // Keep the newest step / answer visible while the run streams.
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end', behavior: 'smooth' });
  }, [ai.steps.length, ai.plan, ai.answer, ai.transcript.length]);

  const submit = (text?: string) => {
    const question = (text ?? draft).trim();
    if (!question) {
      message.warning(t('ai.input.required'));
      return;
    }
    setDraft('');
    dispatch(
      runAgent({
        question,
        context,
        sessionId: ai.sessionId,
        agentId: agentId === DEFAULT_AGENT ? null : agentId,
      }),
    );
  };

  const newChat = () => {
    dispatch(runReset());
    setDraft('');
    message.success(t('ai.chat.new_chat_done'));
  };

  const copyAnswer = async () => {
    const text = ai.answer?.markdown ?? '';
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      message.success(t('ai.actions.copied'));
    } catch {
      message.error(t('ai.actions.copy_failed'));
    }
  };

  // Export the finished answer as a standalone Markdown file so it can be
  // pasted into a report or a wiki without losing the tables/code blocks.
  const exportMarkdown = async (markdown: string) => {
    if (!markdown.trim()) return;
    const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-');
    const filename = `ai-answer-${stamp}.md`;
    const header = `# ${ai.lastQuestion || t('ai.chat.assistant')}\n\n`;
    await downloadBlob(new Blob([header + markdown], { type: 'text/markdown;charset=utf-8' }), filename);
    message.success(t('ai.actions.exported'));
  };

  const filtered = useMemo(() => {
    const needle = keyword.trim().toLowerCase();
    if (!needle) return datasets;
    return datasets.filter(item =>
      `${item.table_name ?? ''} ${item.description ?? ''}`.toLowerCase().includes(needle),
    );
  }, [datasets, keyword]);

  const agentOptions = useMemo(
    () => [
      { value: DEFAULT_AGENT, label: t('ai.chat.agent.default') },
      ...agents.map(agent => ({ value: agent.id, label: agent.name })),
    ],
    [agents, t],
  );

  const skillSteps = ai.steps.filter(step => step.kind === 'skill');

  const pickerContent = (
    <div className={styles.picker}>
      <Input
        autoFocus
        allowClear
        size="small"
        placeholder={t('ai.chat.dataset.search')}
        value={keyword}
        onChange={event => setKeyword(event.target.value)}
      />
      <div className={styles.pickerList}>
        {datasetsLoading ? (
          <div style={{ textAlign: 'center', padding: '18px 0' }}>
            <Spin size="small" />
          </div>
        ) : filtered.length === 0 ? (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('ai.chat.dataset.empty')} />
        ) : (
          filtered.map(item => (
            <button
              key={item.id}
              type="button"
              className={`${styles.pickerItem} ${attached?.id === item.id ? styles.pickerItemActive : ''}`}
              onClick={() => {
                setAttached(item);
                setPickerOpen(false);
              }}
            >
              <DatabaseOutlined />
              <span className={styles.pickerName}>{item.table_name}</span>
            </button>
          ))
        )}
      </div>
    </div>
  );

  if (!canUse) {
    return (
      <div className={styles.page}>
        <div className={styles.header}>
          <span className={styles.title}>
            <CommentIcon />
            {t('nav.ai_chat')}
          </span>
        </div>
        <div className={styles.notice}>
          <Alert type="warning" showIcon message={t('ai.disabled.title')} description={t('ai.disabled.desc')} />
        </div>
      </div>
    );
  }

  return (
    <div className={styles.page}>
      <div className={styles.header}>
        <span className={styles.title}>
          <CommentIcon />
          {t('nav.ai_chat')}
        </span>
        <Space size={8}>
          <Button size="small" icon={<PlusOutlined />} onClick={newChat}>
            {t('ai.chat.new_chat')}
          </Button>
        </Space>
      </div>

      {ai.disabled && (
        <div className={styles.notice}>
          <Alert type="warning" showIcon message={t('ai.disabled.title')} description={t('ai.disabled.desc')} />
        </div>
      )}

      <div className={styles.scroll}>
        <div className={styles.thread}>
          {turns.length === 0 && !hasCurrentTurn && (
            <div className={styles.hero}>
              <div className={styles.heroMark}>
                <RobotOutlined />
              </div>
              <div className={styles.heroTitle}>{t('ai.chat.hero_title')}</div>
              <div className={styles.heroDesc}>{t('ai.chat.hero_desc')}</div>
            </div>
          )}

          {turns.map((turn, index) => (
            <div key={`turn-${index}`}>
              <div className={`${styles.turn} ${styles.userTurn}`}>
                <div className={styles.bubble}>{turn.question}</div>
              </div>
              <div className={`${styles.turn} ${styles.assistantTurn}`} style={{ marginTop: 18 }}>
                <div className={styles.assistantRole}>
                  <RobotOutlined />
                  {t('ai.chat.assistant')}
                </div>
                <div className={styles.assistantBody}>
                  {turn.answer ? (
                    <AiMarkdown text={turn.answer} />
                  ) : (
                    <Text type="secondary">{t('ai.empty.answer')}</Text>
                  )}
                </div>
              </div>
            </div>
          ))}

          {hasCurrentTurn && (
            <>
              <div className={`${styles.turn} ${styles.userTurn}`}>
                <div className={styles.bubble}>{ai.lastQuestion || turns[turns.length - 1]?.question}</div>
              </div>

              <div className={`${styles.turn} ${styles.assistantTurn}`}>
                <div className={styles.assistantRole}>
                  <RobotOutlined />
                  {t('ai.chat.assistant')}
                </div>
                <div className={styles.assistantBody}>
                  <div className={styles.metaRow}>
                    <AiStreamingText
                      streaming={ai.streaming}
                      aborted={ai.aborted}
                      stepCount={ai.steps.length}
                    />
                  </div>

                  {ai.plan && <Text>{ai.plan}</Text>}

                  {skillSteps.map((step, index) => (
                    <AiSkillResult
                      key={`${step.name}-${step.stepNo ?? index}`}
                      name={step.name}
                      payload={(step.result ?? {}) as SkillResultPayload}
                    />
                  ))}

                  {ai.answer ? (
                    <>
                      <Space size={6} wrap className={styles.metaRow}>
                        <Tag>{t('ai.answer.steps', { count: ai.answer.stepCount })}</Tag>
                        <Tag>{t('ai.answer.tokens', { count: ai.answer.tokenUsage })}</Tag>
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
                          icon={<DownloadOutlined />}
                          onClick={() => void exportMarkdown(ai.answer?.markdown ?? '')}
                          title={t('ai.actions.export')}
                          aria-label={t('ai.actions.export')}
                        />
                        <Button
                          size="small"
                          type="text"
                          icon={<ReloadOutlined />}
                          onClick={() => submit(ai.lastQuestion || undefined)}
                          title={t('ai.actions.regenerate')}
                          aria-label={t('ai.actions.regenerate')}
                        />
                      </Space>
                      {ai.answer.markdown ? (
                        <AiMarkdown text={ai.answer.markdown} />
                      ) : (
                        <Text type="secondary">{t('ai.empty.answer')}</Text>
                      )}
                    </>
                  ) : ai.runError ? null : (
                    <Text type="secondary">{t('ai.empty.answer')}</Text>
                  )}

                  {ai.runError && (
                    <Alert
                      style={{ marginTop: 10 }}
                      type="error"
                      showIcon
                      message={t('ai.error.title')}
                      description={ai.runError}
                      action={
                        <Button
                          size="small"
                          icon={<ReloadOutlined />}
                          onClick={() => submit(ai.lastQuestion || undefined)}
                        >
                          {t('ai.actions.retry')}
                        </Button>
                      }
                    />
                  )}

                  {ai.steps.length > 0 && (
                    <Collapse
                      ghost
                      className={styles.steps}
                      items={[
                        {
                          key: 'steps',
                          label: t('ai.timeline.title'),
                          children: <AgentStepTimeline steps={ai.steps} />,
                        },
                      ]}
                    />
                  )}
                </div>
              </div>
            </>
          )}

          <div ref={bottomRef} />
        </div>
      </div>

      <div className={styles.composerWrap}>
        <div className={styles.composerInner}>
          <div className={styles.composer}>
            {attached && (
              <div className={styles.attachments}>
                <span className={styles.attachChip}>
                  <DatabaseOutlined />
                  {attached.table_name}
                  <button
                    type="button"
                    className={styles.attachRemove}
                    onClick={() => setAttached(null)}
                    title={t('ai.chat.dataset.remove')}
                    aria-label={t('ai.chat.dataset.remove')}
                  >
                    <CloseOutlined />
                  </button>
                </span>
              </div>
            )}

            <TextArea
              className={styles.input}
              autoSize={{ minRows: 1, maxRows: 6 }}
              placeholder={t('ai.chat.input_placeholder')}
              value={draft}
              onChange={event => setDraft(event.target.value)}
              onPressEnter={event => {
                if (!event.shiftKey) {
                  event.preventDefault();
                  submit();
                }
              }}
            />

            <div className={styles.toolbar}>
              <Popover
                open={pickerOpen}
                onOpenChange={openPicker}
                trigger="click"
                placement="topLeft"
                content={pickerContent}
                title={t('ai.chat.dataset.title')}
              >
                <button
                  type="button"
                  className={styles.iconButton}
                  title={t('ai.chat.dataset.attach')}
                  aria-label={t('ai.chat.dataset.attach')}
                >
                  <PlusOutlined />
                </button>
              </Popover>

              <Select
                className={styles.agentSelect}
                size="small"
                value={agentId}
                onChange={setAgentId}
                options={agentOptions}
                suffixIcon={<RobotOutlined />}
                title={t('ai.chat.agent')}
              />

              <span className={styles.spacer} />

              {ai.streaming ? (
                <Button
                  type="primary"
                  danger
                  shape="circle"
                  icon={<StopOutlined />}
                  onClick={() => cancelAgentRun()}
                  title={t('ai.actions.cancel')}
                  aria-label={t('ai.actions.cancel')}
                />
              ) : (
                <Button
                  type="primary"
                  shape="circle"
                  icon={<SendOutlined />}
                  onClick={() => submit()}
                  disabled={!draft.trim()}
                  title={t('ai.actions.send')}
                  aria-label={t('ai.actions.send')}
                />
              )}
            </div>
          </div>
          <div className={styles.disclaimer}>
            {t('ai.chat.disclaimer')}
            <span className={styles.mdHint}>{t('ai.chat.markdown_hint')}</span>
          </div>
        </div>
      </div>
    </div>
  );
}

/** The chat glyph used in the page header. */
function CommentIcon() {
  return (
    <span className={styles.titleIcon}>
      <RobotOutlined />
    </span>
  );
}
