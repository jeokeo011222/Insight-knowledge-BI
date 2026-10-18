import { useCallback, useEffect, useMemo, useState } from 'react';
import { Alert, Button, Collapse, Empty, Space, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { DatasetSamples } from '../../api/types';
import { useI18n } from '../../i18n';

export interface DatasetSamplePanelProps {
  datasetId: number;
}

/** Renders a cell value without inventing formatting the backend did not send. */
function formatCell(value: unknown): string {
  if (value === null || value === undefined) return 'NULL';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

/**
 * DatasetSamplePanel reads real rows from the database behind the dataset. It
 * is the "取数" half of the dataset screen: the other tabs only describe
 * metadata, this one proves the connection works and shows what the data looks
 * like.
 */
export default function DatasetSamplePanel({ datasetId }: DatasetSamplePanelProps) {
  const { t } = useI18n();
  const [data, setData] = useState<DatasetSamples | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await client.get<{ result: DatasetSamples }>(
        endpoints.datasetSamples(datasetId),
        { limit: 100 },
      );
      setData(response.result);
    } catch (err) {
      setData(null);
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [datasetId]);

  useEffect(() => {
    setData(null);
    void load();
  }, [load]);

  const columns = useMemo<ColumnsType<Record<string, unknown>>>(() => {
    const names = data?.columns ?? [];
    const types = data?.coltypes ?? [];
    return names.map((name, index) => ({
      title: (
        <Space size={4} direction="vertical" style={{ lineHeight: 1.2 }}>
          <span className="mono">{name}</span>
          {types[index] ? <span className="cell-muted">{types[index]}</span> : null}
        </Space>
      ),
      dataIndex: name,
      key: name,
      width: 180,
      ellipsis: true,
      render: (value: unknown) => (
        <span className={value === null || value === undefined ? 'cell-muted' : 'cell-mono'}>
          {formatCell(value)}
        </span>
      ),
    }));
  }, [data]);

  const rows = useMemo(
    () => (data?.data ?? []).map((row, index) => ({ ...row, __row_key: index })),
    [data],
  );

  if (error) {
    return (
      <Alert
        type="error"
        showIcon
        message={t('dataset.detail.samples_failed')}
        description={error}
        action={
          <Button size="small" onClick={() => void load()} loading={loading}>
            {t('dataset.detail.samples_retry')}
          </Button>
        }
      />
    );
  }

  return (
    <>
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 10 }}>
        <Space size={8} wrap>
          <span className="cell-muted">
            {data
              ? t('dataset.detail.samples_meta', {
                  rows: data.rowcount,
                  ms: data.duration_ms,
                })
              : t('common.state.loading')}
          </span>
          {data?.truncated ? <Tag color="orange">{t('dataset.detail.samples_truncated')}</Tag> : null}
          {data ? <Tag bordered={false}>{t('dataset.detail.samples_limit', { limit: data.limit })}</Tag> : null}
        </Space>
        <Button size="small" icon={<ReloadOutlined />} onClick={() => void load()} loading={loading}>
          {t('dataset.detail.samples_refresh')}
        </Button>
      </Space>

      {data?.sql ? (
        <Collapse
          ghost
          size="small"
          style={{ marginBottom: 8 }}
          items={[
            {
              key: 'sql',
              label: <span className="tiny-label">{t('dataset.detail.samples_sql')}</span>,
              children: <pre className="mono-block">{data.sql}</pre>,
            },
          ]}
        />
      ) : null}

      <Table<Record<string, unknown>>
        size="small"
        rowKey="__row_key"
        loading={loading}
        columns={columns}
        dataSource={rows}
        scroll={{ x: 'max-content' }}
        pagination={{ pageSize: 20, size: 'small', showSizeChanger: false }}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={t('dataset.detail.samples_empty')}
            />
          ),
        }}
      />
    </>
  );
}
