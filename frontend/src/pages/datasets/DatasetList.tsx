import { useMemo, useState } from 'react';
import { Button, Popconfirm, Space, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  DeleteOutlined,
  EditOutlined,
  PartitionOutlined,
  PlusOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Dataset, Paginated } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import LineageDrawer from '../../components/common/LineageDrawer';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';
import CreateDatasetDrawer from './CreateDatasetDrawer';
import DatasetDetailDrawer from './DatasetDetailDrawer';
import { formatRefreshInterval } from './refreshUtils';

export default function DatasetList() {
  const { can } = usePermission();
  const { t } = useI18n();
  const [createOpen, setCreateOpen] = useState(false);
  const [detailId, setDetailId] = useState<number | null>(null);
  const [reloadToken, setReloadToken] = useState(0);

  const fetchDatasets = (query: ListQuery): Promise<Paginated<Dataset>> =>
    client.get<Paginated<Dataset>>(endpoints.datasets, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const reload = () => setReloadToken(value => value + 1);

  const handleDelete = async (dataset: Dataset) => {
    try {
      await client.delete(endpoints.dataset(dataset.id));
      message.success(t('dataset.list.delete_success', { name: dataset.table_name }));
      reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  // One refresh action covers every kind: external datasets are re-pulled,
  // physical/virtual ones have their columns re-read. Failures are recorded on
  // the dataset, so reloading shows the reason in the 刷新 column.
  const handleRefresh = async (dataset: Dataset) => {
    try {
      await client.post(endpoints.datasetRefresh(dataset.id), {});
      message.success(t('dataset.list.refresh_success'));
      reload();
    } catch (error) {
      message.error(`${t('dataset.refresh.failed')}: ${errorMessage(error)}`);
      reload();
    }
  };

  const renderSource = (dataset: Dataset) => {
    if (dataset.kind === 'external') {
      return dataset.source?.type === 'api' ? (
        <Tag color="geekblue" bordered={false}>
          {t('dataset.source.api')}
        </Tag>
      ) : (
        <Tag color="green" bordered={false}>
          {t('dataset.source.file')}
        </Tag>
      );
    }
    if (dataset.kind === 'virtual' || dataset.is_sqllab_view || dataset.sql) {
      return (
        <Tag color="purple" bordered={false}>
          {t('dataset.source.virtual')}
        </Tag>
      );
    }
    return <Tag bordered={false}>{t('dataset.source.physical')}</Tag>;
  };

  const [lineage, setLineage] = useState<{ id: number; name: string } | null>(null);

  const columns = useMemo<ColumnsType<Dataset>>(
    () => [
      {
        title: t('common.label.name'),
        dataIndex: 'table_name',
        key: 'table_name',
        render: (_, record) => (
          <Space size={6}>
            <a onClick={() => setDetailId(record.id)}>{record.table_name}</a>
            {record.is_sqllab_view || record.sql ? <Tag color="purple">virtual</Tag> : null}
          </Space>
        ),
      },
      {
        title: t('common.label.database'),
        key: 'database',
        render: (_, record) => (
          <span className="cell-muted">{record.database?.database_name || '—'}</span>
        ),
      },
      {
        title: t('dataset.list.source'),
        key: 'source',
        width: 110,
        render: (_, record) => renderSource(record),
      },
      {
        title: t('common.label.schema'),
        dataIndex: 'schema',
        key: 'schema',
        width: 120,
        render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('common.label.columns'),
        key: 'columns',
        width: 70,
        render: (_, record) => record.columns?.length ?? 0,
      },
      {
        title: t('common.label.metrics'),
        key: 'metrics',
        width: 70,
        render: (_, record) => record.metrics?.length ?? 0,
      },
      {
        title: t('common.label.owner'),
        key: 'owners',
        width: 160,
        render: (_, record) => (
          <span className="cell-muted">
            {(record.owners ?? [])
              .map(owner => owner.username || owner.name)
              .filter(Boolean)
              .join(', ') || '—'}
          </span>
        ),
      },
      {
        title: t('dataset.list.refresh_col'),
        key: 'refresh',
        width: 190,
        render: (_, record) => {
          const status = record.last_refresh_status ?? '';
          return (
            <Space direction="vertical" size={0}>
              <Space size={6}>
                <span style={{ fontSize: 12 }}>
                  {formatRefreshInterval(record.refresh_interval_sec ?? 0, t)}
                </span>
                {status === 'failed' ? (
                  <Tag color="red" bordered={false}>
                    {t('dataset.refresh.status_failed')}
                  </Tag>
                ) : status === 'running' ? (
                  <Tag color="blue" bordered={false}>
                    {t('dataset.refresh.status_running')}
                  </Tag>
                ) : null}
              </Space>
              <span className="cell-muted" style={{ fontSize: 12 }}>
                {record.last_refreshed_at
                  ? formatDateTimeShort(record.last_refreshed_at)
                  : t('dataset.refresh.never')}
              </span>
            </Space>
          );
        },
      },
      {
        title: t('common.label.changed_on'),
        dataIndex: 'changed_on',
        key: 'changed_on',
        width: 160,
        render: (value?: string) => (
          <span className="cell-muted">{formatDateTimeShort(value)}</span>
        ),
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 150,
        render: (_, record) => (
          <Space size={2}>
            <Button
              size="small"
              type="text"
              icon={<EditOutlined />}
              onClick={() => setDetailId(record.id)}
            />
            {can('can_write', 'Dataset') ? (
              <Button
                size="small"
                type="text"
                icon={<ReloadOutlined />}
                title={t('dataset.list.refresh_now')}
                onClick={() => void handleRefresh(record)}
              />
            ) : null}
            <Button
              size="small"
              type="text"
              icon={<PartitionOutlined />}
              title={t('lineage.action')}
              onClick={() => setLineage({ id: record.id, name: record.table_name })}
            />
            {can('can_delete', 'Dataset') ? (
              <Popconfirm
                title={t('dataset.list.delete_title')}
                description={t('dataset.list.delete_confirm', { name: record.table_name })}
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
    [can, t],
  );

  return (
    <>
      <EntityList<Dataset>
        title={t('nav.datasets')}
        subtitle={t('dataset.list.subtitle')}
        columns={columns}
        fetchData={fetchDatasets}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('dataset.list.search_placeholder')}
        extra={
          can('can_write', 'Dataset') ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
              {t('dataset.list.create')}
            </Button>
          ) : null
        }
      />

      <CreateDatasetDrawer
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={dataset => {
          reload();
          setDetailId(dataset.id);
        }}
      />

      <DatasetDetailDrawer
        datasetId={detailId}
        open={detailId !== null}
        onClose={() => setDetailId(null)}
        onChanged={reload}
      />
      <LineageDrawer
        open={lineage !== null}
        type="dataset"
        id={lineage?.id ?? null}
        name={lineage?.name}
        onClose={() => setLineage(null)}
      />
    </>
  );
}
