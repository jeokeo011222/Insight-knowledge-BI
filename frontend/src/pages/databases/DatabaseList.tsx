import { useEffect, useMemo, useState } from 'react';
import { Button, Popconfirm, Space, Tag, Tooltip, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  QuestionCircleFilled,
  SyncOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Database, EngineInfo, Paginated } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { maskURI } from '../../utils/connectionUri';
import DatabaseModal from './DatabaseModal';

export default function DatabaseList() {
  const { can } = usePermission();
  const { t } = useI18n();
  const [modalOpen, setModalOpen] = useState(false);
  const [editing, setEditing] = useState<Database | null>(null);
  const [engines, setEngines] = useState<EngineInfo[]>([]);
  const [reloadToken, setReloadToken] = useState(0);
  const [probing, setProbing] = useState<number | 'all' | null>(null);

  /** Runs a connection probe and surfaces the outcome in place. */
  const handleProbe = async (target: number | 'all') => {
    setProbing(target);
    try {
      if (target === 'all') {
        const response = await client.post<{ probed: number; failing: number }>(
          endpoints.databaseProbeAll,
          {},
        );
        if (response.failing) {
          message.warning(
            t('database.health.probe_all_partial', {
              total: response.probed,
              failing: response.failing,
            }),
          );
        } else {
          message.success(t('database.health.probe_all_ok', { total: response.probed }));
        }
      } else {
        const response = await client.post<{ result: { ok: boolean; latency_ms: number } }>(
          endpoints.databaseProbe(target),
          {},
        );
        if (response.result?.ok) {
          message.success(t('database.health.probe_ok', { ms: response.result.latency_ms }));
        } else {
          message.error(t('database.health.probe_fail'));
        }
      }
      setReloadToken(token => token + 1);
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setProbing(null);
    }
  };

  const fetchDatabases = (query: ListQuery): Promise<Paginated<Database>> =>
    client.get<Paginated<Database>>(endpoints.databases, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  /** Loads the engine catalogue once; the modal renders its picker from it. */
  const loadEngines = async () => {
    if (engines.length) return;
    try {
      const response = await client.get<{ result: EngineInfo[] }>(endpoints.databaseAvailable);
      setEngines(response.result ?? []);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const openCreate = () => {
    setEditing(null);
    setModalOpen(true);
    void loadEngines();
  };

  // Warm the engine catalogue with the list: the edit dialog unpacks a stored
  // URI against it, and fetching it on click made the first edit wait.
  useEffect(() => {
    void loadEngines();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openEdit = (database: Database) => {
    setEditing(database);
    setModalOpen(true);
    void loadEngines();
  };

  const handleDelete = async (database: Database) => {
    try {
      await client.delete(endpoints.database(database.id));
      message.success(t('database.list.delete_success', { name: database.database_name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<Database>>(
    () => [
      {
        title: t('database.form.name'),
        dataIndex: 'database_name',
        key: 'database_name',
        render: (value: string, record) => (
          <div>
            <div style={{ fontWeight: 500 }}>{value}</div>
            {record.verbose_name ? <div className="cell-muted">{record.verbose_name}</div> : null}
          </div>
        ),
      },
      {
        title: t('common.label.type'),
        dataIndex: 'backend',
        key: 'backend',
        width: 120,
        render: (value: string) => <Tag bordered={false}>{value || '—'}</Tag>,
      },
      {
        title: 'SQLAlchemy URI',
        dataIndex: 'sqlalchemy_uri',
        key: 'sqlalchemy_uri',
        ellipsis: true,
        render: (value: string) => <span className="cell-mono">{value ? maskURI(value) : '—'}</span>,
      },
      {
        title: 'SQL Lab',
        dataIndex: 'expose_in_sqllab',
        key: 'expose_in_sqllab',
        width: 90,
        render: (value: boolean) =>
          value ? (
            <Tag color="green">{t('common.state.enabled')}</Tag>
          ) : (
            <Tag>{t('common.state.disabled')}</Tag>
          ),
      },
      {
        title: t('database.list.writable'),
        key: 'write',
        width: 210,
        render: (_, record) => (
          <Space size={4} wrap>
            {record.allow_ctas ? <Tag color="blue">CTAS</Tag> : null}
            {record.allow_cvas ? <Tag color="geekblue">CVAS</Tag> : null}
            {record.allow_dml ? <Tag color="orange">DML</Tag> : null}
            {record.allow_run_async ? <Tag color="cyan">Async</Tag> : null}
            {!record.allow_ctas && !record.allow_cvas && !record.allow_dml ? (
              <span className="cell-muted">{t('common.state.read_only')}</span>
            ) : null}
          </Space>
        ),
      },
      {
        title: t('database.health.title'),
        key: 'health',
        width: 130,
        render: (_, record) => {
          const ok = record.last_probe_ok;
          const color = ok === null || ok === undefined ? '#bfbfbf' : ok ? '#52c41a' : '#ff4d4f';
          const icon =
            ok === null || ok === undefined ? (
              <QuestionCircleFilled style={{ color }} />
            ) : ok ? (
              <CheckCircleFilled style={{ color }} />
            ) : (
              <CloseCircleFilled style={{ color }} />
            );
          const at = record.last_probe_at
            ? new Date(record.last_probe_at).toLocaleString()
            : t('database.health.never');
          const tip = (
            <div style={{ maxWidth: 320 }}>
              <div>{t('database.health.probed_at', { at })}</div>
              {record.last_probe_ms ? (
                <div>{t('database.health.latency', { ms: record.last_probe_ms })}</div>
              ) : null}
              {record.last_probe_error ? <div>{record.last_probe_error}</div> : null}
            </div>
          );
          return (
            <Space size={6}>
              <Tooltip title={tip}>{icon}</Tooltip>
              <Button
                size="small"
                type="text"
                icon={<SyncOutlined spin={probing === record.id} />}
                loading={probing === record.id}
                onClick={() => void handleProbe(record.id)}
                aria-label={t('database.health.probe')}
              />
            </Space>
          );
        },
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 110,
        render: (_, record) => (
          <Space size={2}>
            {can('can_write', 'Database') ? (
              <Button
                size="small"
                type="text"
                icon={<EditOutlined />}
                onClick={() => openEdit(record)}
              />
            ) : null}
            {can('can_delete', 'Database') ? (
              <Popconfirm
                title={t('database.list.delete_title')}
                description={t('database.list.delete_confirm', { name: record.database_name })}
                okText={t('common.actions.delete')}
                cancelText={t('common.actions.cancel')}
                okButtonProps={{ danger: true }}
                onConfirm={() => void handleDelete(record)}
              >
                <Button size="small" type="text" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            ) : null}
          </Space>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [can, t, probing],
  );

  return (
    <>
      <EntityList<Database>
        title="Databases"
        subtitle={t('database.list.subtitle')}
        columns={columns}
        fetchData={fetchDatabases}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('database.list.search_placeholder')}
        extra={
          <Space>
            {can('can_test_connection', 'Database') ? (
              <Button
                icon={<ThunderboltOutlined />}
                loading={probing === 'all'}
                onClick={() => void handleProbe('all')}
              >
                {t('database.health.probe_all')}
              </Button>
            ) : null}
            {can('can_write', 'Database') ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                {t('database.list.create')}
              </Button>
            ) : null}
          </Space>
        }
      />

      <DatabaseModal
        open={modalOpen}
        editing={editing}
        engines={engines}
        onClose={() => setModalOpen(false)}
        onSaved={() => {
          setModalOpen(false);
          setReloadToken(value => value + 1);
        }}
      />
    </>
  );
}
