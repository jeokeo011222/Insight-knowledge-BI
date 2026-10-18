import { useMemo, useRef, useState } from 'react';
import { Button, Tabs, Tag, message } from 'antd';
import { DownloadOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { useSearchParams } from 'react-router-dom';
import { client, downloadBlob, endpoints, errorMessage } from '../../api/client';
import { aiEndpoints } from '../../api/ai';
import type { AIAuditEntry } from '../../api/ai';
import type { AuditLogEntry, Paginated } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import PageHeader from '../../components/common/PageHeader';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

type TabKey = 'system' | 'ai';

/**
 * 审计日志: the immutable trail behind every privileged action. System
 * operations come from the `logs` table; AI audit rows come from the agent's
 * own `ai_audit_log`. Both are read-only.
 */
export default function AuditLog() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const active: TabKey = params.get('tab') === 'ai' ? 'ai' : 'system';

  return (
    <>
      <PageHeader title={t('nav.audit')} subtitle={t('settings.audit.subtitle')} />
      <Tabs
        activeKey={active}
        onChange={key => setParams({ tab: key }, { replace: true })}
        items={[
          {
            key: 'system',
            label: t('settings.audit.tab.system'),
            children: <SystemLogTable />,
          },
          {
            key: 'ai',
            label: t('settings.audit.tab.ai'),
            children: <AiAuditTable />,
          },
        ]}
      />
    </>
  );
}

function SystemLogTable() {
  const { t } = useI18n();
  // EntityList owns the search box, so the page keeps the query it last
  // fetched: the export must cover exactly what is on screen.
  const lastQuery = useRef<ListQuery>({ page: 1, pageSize: 25, search: '' });
  const [exporting, setExporting] = useState(false);

  const fetchLogs = (query: ListQuery): Promise<Paginated<AuditLogEntry>> => {
    lastQuery.current = query;
    return client.get<Paginated<AuditLogEntry>>(endpoints.logs, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });
  };

  const handleExport = async () => {
    setExporting(true);
    try {
      const blob = await client.getBlob(endpoints.logsExport, {
        q: lastQuery.current.search || undefined,
      });
      const stamp = new Date().toISOString().replace(/[-:]/g, '').slice(0, 15);
      await downloadBlob(blob, `audit_logs_${stamp}.csv`);
      message.success(t('settings.audit.export_done'));
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setExporting(false);
    }
  };

  const columns = useMemo<ColumnsType<AuditLogEntry>>(
    () => [
      {
        title: t('settings.audit.col.action'),
        dataIndex: 'action',
        key: 'action',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('settings.audit.col.user'),
        dataIndex: 'user_id',
        key: 'user_id',
        width: 90,
        render: (value?: number | null) => <span className="cell-muted">{value ?? '—'}</span>,
      },
      {
        title: t('settings.audit.col.target'),
        key: 'target',
        render: (_, record) => (
          <span className="cell-muted">
            {record.dashboard_id ? `dashboard#${record.dashboard_id} ` : ''}
            {record.slice_id ? `chart#${record.slice_id}` : ''}
            {!record.dashboard_id && !record.slice_id ? '—' : ''}
          </span>
        ),
      },
      {
        title: t('settings.audit.col.duration'),
        dataIndex: 'duration_ms',
        key: 'duration_ms',
        width: 110,
        render: (value?: number | null) => (
          <span className="cell-muted">{value != null ? `${value} ms` : '—'}</span>
        ),
      },
      {
        title: t('settings.audit.col.referrer'),
        dataIndex: 'referrer',
        key: 'referrer',
        render: (value?: string | null) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('settings.audit.col.time'),
        dataIndex: 'dttm',
        key: 'dttm',
        width: 170,
        render: (value?: string | null) => (
          <span className="cell-muted">{formatDateTimeShort(value ?? undefined)}</span>
        ),
      },
    ],
    [t],
  );

  return (
    <EntityList<AuditLogEntry>
      columns={columns}
      fetchData={fetchLogs}
      rowKey={record => record.id}
      persistKey="audit-system"
      searchPlaceholder={t('settings.audit.search_placeholder')}
      emptyDescription={t('settings.audit.empty')}
      toolbarExtra={
        <Button icon={<DownloadOutlined />} loading={exporting} onClick={handleExport}>
          {t('settings.audit.export')}
        </Button>
      }
    />
  );
}

function AiAuditTable() {
  const { t } = useI18n();

  const fetchAudit = (query: ListQuery): Promise<Paginated<AIAuditEntry>> =>
    client.get<Paginated<AIAuditEntry>>(aiEndpoints.audit, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const columns = useMemo<ColumnsType<AIAuditEntry>>(
    () => [
      {
        title: t('settings.audit.col.operation'),
        dataIndex: 'operation',
        key: 'operation',
        render: (value?: string | null) => <span style={{ fontWeight: 500 }}>{value || '—'}</span>,
      },
      {
        title: t('settings.audit.col.model'),
        dataIndex: 'model_name',
        key: 'model_name',
        render: (value?: string | null) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('settings.audit.col.tokens'),
        dataIndex: 'token_usage',
        key: 'token_usage',
        width: 110,
        render: (value?: number | null) => <span className="cell-muted">{value ?? '—'}</span>,
      },
      {
        title: t('settings.audit.col.latency'),
        dataIndex: 'latency_ms',
        key: 'latency_ms',
        width: 110,
        render: (value?: number | null) => (
          <span className="cell-muted">{value != null ? `${value} ms` : '—'}</span>
        ),
      },
      {
        title: t('settings.audit.col.status'),
        key: 'status',
        width: 100,
        render: (_, record) =>
          record.error_msg ? (
            <Tag color="red">{t('settings.audit.error')}</Tag>
          ) : (
            <Tag color="green">{t('settings.audit.ok')}</Tag>
          ),
      },
      {
        title: t('settings.audit.col.time'),
        dataIndex: 'created_on',
        key: 'created_on',
        width: 170,
        render: (value?: string | null) => (
          <span className="cell-muted">{formatDateTimeShort(value ?? undefined)}</span>
        ),
      },
    ],
    [t],
  );

  return (
    <EntityList<AIAuditEntry>
      columns={columns}
      fetchData={fetchAudit}
      rowKey={record => record.id}
      persistKey="audit-ai"
      searchPlaceholder={t('settings.audit.search_placeholder')}
      emptyDescription={t('settings.audit.empty')}
    />
  );
}
