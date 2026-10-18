import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Tag,
  Tooltip,
  Tree,
  message,
} from 'antd';
import type { DataNode } from 'antd/es/tree';
import {
  CaretRightOutlined,
  CloudDownloadOutlined,
  FormatPainterOutlined,
  PauseCircleOutlined,
  ReloadOutlined,
  RobotOutlined,
  SaveOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import {
  client,
  downloadBlob,
  endpoints,
  errorMessage,
} from '../../api/client';
import type {
  CatalogNode,
  Database,
  JsonObject,
  Paginated,
  QueryHistoryItem,
  SqlLabResult,
} from '../../api/types';
import CodeEditor from '../../components/common/CodeEditor';
import { useI18n } from '../../i18n';
import PageHeader from '../../components/common/PageHeader';
import { TERMINAL_STATES, useQuerySocket } from '../../features/sqllab/useQuerySocket';
import type { QueryEvent } from '../../features/sqllab/useQuerySocket';
import QueryHistoryPanel from './QueryHistoryPanel';
import SqlLabResults from './SqlLabResults';
import AgentPanel from '../../components/ai/AgentPanel';

const LIMITS = [100, 1000, 10000, 100000].map(value => ({ value, label: String(value) }));

/** Polling interval used when the WebSocket channel is unavailable. */
const POLL_INTERVAL_MS = 1200;

interface HistoryResponse {
  count: number;
  result: QueryHistoryItem[];
}

/** Response of `POST /sqllab/` when the query was queued. */
interface SubmitResponse extends Partial<SqlLabResult> {
  id: number;
  status: string;
  async?: boolean;
  async_fallback?: boolean;
}

export default function SqlLab() {
  const { t } = useI18n();
  const [databases, setDatabases] = useState<Database[]>([]);
  const [databaseId, setDatabaseId] = useState<number | null>(null);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [schema, setSchema] = useState<string | undefined>(undefined);
  const [catalog, setCatalog] = useState<CatalogNode[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [treeSearch, setTreeSearch] = useState('');
  const [sql, setSql] = useState('SELECT 1 AS ok');
  const [limit, setLimit] = useState(1000);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<SqlLabResult | null>(null);
  const [runError, setRunError] = useState<string | null>(null);
  const [runStatus, setRunStatus] = useState<string | null>(null);
  const [activeQueryId, setActiveQueryId] = useState<number | null>(null);
  const [history, setHistory] = useState<QueryHistoryItem[]>([]);
  const [historyOpen, setHistoryOpen] = useState(true);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveForm] = Form.useForm<{ label: string; description?: string }>();
  const abortRef = useRef<AbortController | null>(null);

  /** P5.2: land SQL produced by the agent straight into the editor. */
  const applyArtifacts = useCallback(
    (artifacts: JsonObject) => {
      const raw = artifacts.sql;
      const generated =
        typeof raw === 'string'
          ? raw
          : Array.isArray(raw)
            ? raw.filter((part): part is string => typeof part === 'string').join(';\n')
            : '';
      if (!generated) return;
      setSql(generated);
      message.success(t('ai.artifacts.sql_applied'));
    },
    [t],
  );

  // The socket handler is registered once; the id it filters on lives in a ref
  // so a new submission never needs to reopen the channel.
  const activeQueryRef = useRef<number | null>(null);
  const pollTimerRef = useRef<number | null>(null);

  useEffect(() => {
    client
      .get<Paginated<Database>>(endpoints.databases, { page: 1, page_size: 100 })
      .then(response => {
        const all = response.result ?? [];
        const exposed = all.filter(database => database.expose_in_sqllab);
        const list = exposed.length ? exposed : all;
        setDatabases(list);
        if (list.length) setDatabaseId(current => current ?? list[0].id);
      })
      .catch(error => message.error(errorMessage(error)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadHistory = useCallback(async () => {
    try {
      const response = await client.get<HistoryResponse>(endpoints.sqllabHistory, {
        page: 1,
        page_size: 20,
      });
      setHistory(response.result ?? []);
    } catch (error) {
      message.error(errorMessage(error));
    }
  }, []);

  useEffect(() => {
    void loadHistory();
  }, [loadHistory]);

  const loadCatalog = useCallback(async (id: number, schemaName?: string) => {
    setCatalogLoading(true);
    try {
      const response = await client.get<{ result: CatalogNode[] }>(endpoints.databaseCatalog(id));
      setCatalog(response.result ?? []);
      if (schemaName) setSchema(schemaName);
    } catch (error) {
      setCatalog([]);
      message.error(errorMessage(error));
    } finally {
      setCatalogLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!databaseId) return;
    let cancelled = false;
    client
      .get<{ result: string[] }>(endpoints.databaseSchemas(databaseId))
      .then(response => {
        if (cancelled) return;
        const list = response.result ?? [];
        setSchemas(list);
        setSchema(current => current ?? list[0]);
      })
      .catch(error => {
        if (!cancelled) message.error(errorMessage(error));
      });
    void loadCatalog(databaseId);
    return () => {
      cancelled = true;
    };
  }, [databaseId, loadCatalog]);

  const tableSql = useMemo(() => {
    const map = new Map<string, string>();
    catalog.forEach(schemaNode => {
      (schemaNode.children ?? []).forEach(table => {
        map.set(`table:${schemaNode.name}.${table.name}`, `${schemaNode.name}.${table.name}`);
      });
    });
    return map;
  }, [catalog]);

  const treeData = useMemo<DataNode[]>(() => {
    const keyword = treeSearch.trim().toLowerCase();
    const nodes: DataNode[] = [];
    catalog.forEach(schemaNode => {
      const tables = (schemaNode.children ?? []).filter(
        table => !keyword || table.name.toLowerCase().includes(keyword),
      );
      if (keyword && !schemaNode.name.toLowerCase().includes(keyword) && !tables.length) {
        return;
      }
      nodes.push({
        title: `${schemaNode.name} (${tables.length})`,
        key: `schema:${schemaNode.name}`,
        selectable: false,
        children: tables.map(table => ({
          title: table.name,
          key: `table:${schemaNode.name}.${table.name}`,
          isLeaf: true,
        })),
      });
    });
    return nodes;
  }, [catalog, treeSearch]);

  const insertTable = (key: string) => {
    const qualified = tableSql.get(key);
    if (!qualified) return;
    setSql(current => `${current.trimEnd()}\nSELECT * FROM ${qualified} LIMIT 100;`);
  };

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current !== null) {
      window.clearTimeout(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  /** Loads the stored result document and closes the run. */
  const collectResult = useCallback(
    async (queryId: number) => {
      try {
        const response = await client.get<SqlLabResult>(endpoints.sqllabResults(queryId));
        const terminal = TERMINAL_STATES.has(response.status);
        const notReady = (response.errors ?? []).some(item =>
          /not available yet/i.test(item.message ?? ''),
        );
        if (!terminal || notReady) {
          // The worker has not written the payload yet: keep waiting.
          return false;
        }
        setResult(response);
        if (response.status === 'failed') {
          const detail =
            response.errors?.map(item => item.message).filter(Boolean).join('; ') ||
            t('sqllab.query_failed');
          setRunError(detail);
          message.error(t('sqllab.exec_failed'));
        } else if (response.status === 'cancelled') {
          message.info(t('sqllab.stopped'));
        } else {
          message.success(t('sqllab.completed', { count: response.data?.length ?? 0 }));
        }
        setRunning(false);
        setActiveQueryId(null);
        activeQueryRef.current = null;
        void loadHistory();
        return true;
      } catch (error) {
        setRunError(errorMessage(error));
        setRunning(false);
        setActiveQueryId(null);
        activeQueryRef.current = null;
        return true;
      }
    },
    [loadHistory, t],
  );

  /** Fallback used while the socket is down: ask the query row for its state. */
  const poll = useCallback(
    async (queryId: number) => {
      try {
        const response = await client.get<{ result: { status?: string } }>(
          endpoints.sqllabQuery(queryId),
        );
        const status = response.result?.status ?? '';
        if (status) setRunStatus(status);
        if (TERMINAL_STATES.has(status)) {
          await collectResult(queryId);
          return;
        }
      } catch {
        // Transient failures are retried on the next tick.
      }
      if (activeQueryRef.current === queryId) {
        pollTimerRef.current = window.setTimeout(() => void poll(queryId), POLL_INTERVAL_MS);
      }
    },
    [collectResult],
  );

  const handleSocketEvent = useCallback(
    (event: QueryEvent) => {
      const id = activeQueryRef.current;
      if (!id || event.query_id !== id) return;
      if (event.type === 'query_update') {
        if (event.status) setRunStatus(event.status);
        return;
      }
      if (event.type === 'error') {
        setRunStatus(event.status ?? 'failed');
        setRunError(event.message ?? t('sqllab.query_failed'));
        setRunning(false);
        setActiveQueryId(null);
        activeQueryRef.current = null;
        message.error(t('sqllab.exec_failed'));
        void loadHistory();
        return;
      }
      if (event.type === 'query_result') {
        setRunStatus(event.status ?? 'success');
        void collectResult(id);
      }
    },
    [collectResult, loadHistory, t],
  );

  const { connected: socketReady, subscribe } = useQuerySocket(handleSocketEvent);

  // Without a socket the page polls; with a socket the poller stays idle.
  useEffect(() => {
    if (!running || activeQueryId === null || socketReady) {
      stopPolling();
      return;
    }
    stopPolling();
    pollTimerRef.current = window.setTimeout(
      () => void poll(activeQueryId),
      POLL_INTERVAL_MS,
    );
    return stopPolling;
  }, [running, activeQueryId, socketReady, poll, stopPolling]);

  useEffect(
    () => () => {
      stopPolling();
    },
    [stopPolling],
  );

  const runQuery = useCallback(async () => {
    if (!databaseId) {
      message.warning(t('sqllab.db_required'));
      return;
    }
    if (!sql.trim()) {
      message.warning(t('sqllab.sql_required'));
      return;
    }
    const controller = new AbortController();
    abortRef.current = controller;
    stopPolling();
    setRunning(true);
    setRunError(null);
    setResult(null);
    setRunStatus('pending');
    try {
      const response = await client.post<SubmitResponse>(
        endpoints.sqllab,
        {
          database_id: databaseId,
          schema,
          sql,
          queryLimit: limit,
          run_async: true,
        },
        { signal: controller.signal },
      );
      activeQueryRef.current = response.id;
      setActiveQueryId(response.id);
      setRunStatus(response.status ?? 'pending');
      subscribe(response.id);
      if (!response.async) {
        // The server answered inline (async disabled): use the payload as is.
        setResult(response as SqlLabResult);
        if (response.status === 'failed') {
          const detail =
            response.errors?.map(item => item.message).filter(Boolean).join('; ') ||
            t('sqllab.query_failed');
          setRunError(detail);
          message.error(t('sqllab.exec_failed'));
        } else {
          message.success(t('sqllab.completed', { count: response.data?.length ?? 0 }));
        }
        setRunning(false);
        setActiveQueryId(null);
        activeQueryRef.current = null;
        void loadHistory();
        return;
      }
      // The row may already be finished by the time the response arrives.
      await collectResult(response.id);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        message.info(t('sqllab.stopped'));
      } else {
        setRunError(errorMessage(error));
        message.error(errorMessage(error));
      }
      setRunning(false);
      setRunStatus(null);
      setActiveQueryId(null);
      activeQueryRef.current = null;
    } finally {
      abortRef.current = null;
    }
  }, [
    databaseId,
    schema,
    sql,
    limit,
    loadHistory,
    collectResult,
    subscribe,
    stopPolling,
    t,
  ]);

  const handleStop = () => {
    const id = activeQueryRef.current;
    abortRef.current?.abort();
    stopPolling();
    if (id === null) {
      setRunning(false);
      return;
    }
    void client
      .post(endpoints.sqllabCancel(id))
      .catch(() => undefined)
      .finally(() => {
        setRunStatus('cancelled');
        setRunning(false);
        setActiveQueryId(null);
        activeQueryRef.current = null;
      });
  };

  const handleFormat = async () => {
    if (!sql.trim()) return;
    try {
      const response = await client.post<{ result: string }>(endpoints.sqllabFormat, { sql });
      if (response.result) setSql(response.result);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const handleExport = async () => {
    if (!databaseId || !sql.trim()) {
      message.warning(t('sqllab.sql_required_first'));
      return;
    }
    try {
      const blob = await client.postBlob(endpoints.sqllabExport, {
        database_id: databaseId,
        schema,
        sql,
        query_limit: limit,
      });
      await downloadBlob(blob, `sqllab_export_${dayjs().format('YYYYMMDD_HHmmss')}.csv`);
      message.success(t('sqllab.csv_exported'));
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const openSave = () => {
    saveForm.setFieldsValue({ label: '' });
    setSaveOpen(true);
  };

  const handleSave = async () => {
    if (!databaseId) {
      message.warning(t('sqllab.db_required'));
      return;
    }
    try {
      const values = await saveForm.validateFields();
      setSaving(true);
      await client.post(endpoints.savedQueries, {
        label: values.label,
        description: values.description,
        db_id: databaseId,
        schema,
        sql,
      });
      message.success(t('sqllab.saved'));
      setSaveOpen(false);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };


  return (
    <div>
      <PageHeader
        title="SQL Lab"
        subtitle={t('sqllab.subtitle')}
        extra={
          <Space>
            <Tag bordered={false}>{t('sqllab.schema_tag', { schema: schema ?? '—' })}</Tag>
            <Tag bordered={false}>{t('sqllab.limit_tag', { limit })}</Tag>
          </Space>
        }
      />

      <div className="sqllab">
        <div className="sqllab__sidebar">
          <div className="sqllab__sidebar-head">
            <Select
              showSearch
              optionFilterProp="label"
              size="small"
              style={{ width: '100%' }}
              placeholder={t('sqllab.db_placeholder')}
              value={databaseId ?? undefined}
              onChange={value => {
                setDatabaseId(value);
                setSchema(undefined);
                setCatalog([]);
              }}
              options={databases.map(database => ({
                value: database.id,
                label: database.database_name,
              }))}
            />
            <Select
              size="small"
              style={{ width: '100%' }}
              placeholder="schema"
              value={schema}
              onChange={setSchema}
              options={schemas.map(item => ({ value: item, label: item }))}
              allowClear
            />
            <Input
              size="small"
              allowClear
              placeholder={t('sqllab.table_filter_placeholder')}
              prefix={<SearchOutlined style={{ color: 'var(--color-text-tertiary)' }} />}
              value={treeSearch}
              onChange={event => setTreeSearch(event.target.value)}
            />
            <Button
              size="small"
              icon={<ReloadOutlined />}
              loading={catalogLoading}
              onClick={() => databaseId && void loadCatalog(databaseId)}
            >
              {t('sqllab.refresh_schema')}
            </Button>
          </div>
          <div className="sqllab__tree">
            {treeData.length ? (
              <Tree
                treeData={treeData}
                showLine
                blockNode
                defaultExpandAll
                onDoubleClick={(_, node) => insertTable(String(node.key))}
              />
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={catalogLoading ? t('common.state.loading') : t('sqllab.no_tables')}
                style={{ marginTop: 32 }}
              />
            )}
          </div>
        </div>

        <div className="sqllab__main">
          <div className="sqllab__editor-card">
            <div className="sqllab__toolbar">
              <Tooltip title={t('sqllab.run_hint')}>
                <Button
                  type="primary"
                  size="small"
                  icon={<CaretRightOutlined />}
                  loading={running}
                  onClick={() => void runQuery()}
                >
                  {t('sqllab.run')}
                </Button>
              </Tooltip>
              <Button
                size="small"
                danger
                icon={<PauseCircleOutlined />}
                disabled={!running}
                onClick={handleStop}
              >
                {t('sqllab.stop')}
              </Button>
              <Button size="small" icon={<FormatPainterOutlined />} onClick={() => void handleFormat()}>
                {t('sqllab.format')}
              </Button>
              <AgentPanel
                variant="inline"
                icon={<RobotOutlined />}
                label={t('ai.actions.gen_sql')}
                question={t('ai.preset.nl2sql')}
                context={{
                  scope: 'sqllab',
                  extra: databaseId ? { database_id: databaseId, sql } : { sql },
                }}
              />
              <Button size="small" icon={<SaveOutlined />} onClick={openSave}>
                {t('sqllab.save')}
              </Button>
              <Button size="small" icon={<CloudDownloadOutlined />} onClick={() => void handleExport()}>
                {t('sqllab.csv')}
              </Button>
              <div className="sqllab__toolbar-spacer" />
              <span className="cell-muted">{t('sqllab.rows')}</span>
              <Select
                size="small"
                style={{ width: 92 }}
                value={limit}
                onChange={setLimit}
                options={LIMITS}
              />
            </div>
            <CodeEditor value={sql} onChange={setSql} height={206} onRun={() => void runQuery()} />
          </div>

          <SqlLabResults result={result} runError={runError} status={runStatus} />

          <QueryHistoryPanel
            items={history}
            open={historyOpen}
            onToggle={() => setHistoryOpen(value => !value)}
            onReload={() => void loadHistory()}
            onPick={setSql}
          />
        </div>
      </div>

      <Modal
        title={t('sqllab.save_query')}
        open={saveOpen}
        onCancel={() => setSaveOpen(false)}
        onOk={() => void handleSave()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
      >
        <Form form={saveForm} layout="vertical">
          <Form.Item
            name="label"
            label={t('common.label.name')}
            rules={[{ required: true, message: t('settings.annotation.name_required') }]}
          >
            <Input placeholder={t('sqllab.query_name_placeholder')} maxLength={120} />
          </Form.Item>
          <Form.Item name="description" label={t('common.label.description')}>
            <Input.TextArea rows={3} maxLength={400} />
          </Form.Item>
          <Alert
            type="info"
            showIcon
            message={
              schema
                ? t('sqllab.save_hint_with_schema', { id: databaseId ?? '—', schema })
                : t('sqllab.save_hint', { id: databaseId ?? '—' })
            }
          />
        </Form>
      </Modal>

      {/* Drawer host shared by the toolbar trigger. */}
      <AgentPanel variant="host" context={{ scope: 'sqllab' }} onArtifacts={applyArtifacts} />
    </div>
  );
}
