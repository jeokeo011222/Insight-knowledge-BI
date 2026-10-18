import { useCallback, useEffect, useState } from 'react';
import {
  Button,
  Drawer,
  Form,
  Input,
  Popconfirm,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Dataset, DatasetColumn, DatasetMetric } from '../../api/types';
import DatasetColumnModal from './DatasetColumnModal';
import type { ColumnFormValues } from './DatasetColumnModal';
import DatasetMetricModal from './DatasetMetricModal';
import type { MetricFormValues } from './DatasetMetricModal';
import DatasetSamplePanel from './DatasetSamplePanel';
import DatasetRefreshPanel from './DatasetRefreshPanel';
import { DatasetSemanticPanel } from './DatasetSemanticPanel';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';

export interface DatasetDetailDrawerProps {
  datasetId: number | null;
  open: boolean;
  onClose: () => void;
  onChanged?: () => void;
}

export default function DatasetDetailDrawer({
  datasetId,
  open,
  onClose,
  onChanged,
}: DatasetDetailDrawerProps) {
  const { t } = useI18n();
  const { can } = usePermission();
  const canWrite = can('can_write', 'Dataset');
  const [dataset, setDataset] = useState<Dataset | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [columnEditing, setColumnEditing] = useState<DatasetColumn | null>(null);
  const [columnOpen, setColumnOpen] = useState(false);
  const [metricEditing, setMetricEditing] = useState<DatasetMetric | null>(null);
  const [metricOpen, setMetricOpen] = useState(false);
  const [overviewForm] = Form.useForm<{ description?: string; main_dttm_col?: string }>();

  const load = useCallback(async () => {
    if (!datasetId) return;
    setLoading(true);
    try {
      const response = await client.get<{ result: Dataset }>(endpoints.dataset(datasetId));
      setDataset(response.result);
      overviewForm.setFieldsValue({
        description: response.result.description ?? '',
        main_dttm_col: response.result.main_dttm_col ?? undefined,
      });
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }, [datasetId, overviewForm]);

  useEffect(() => {
    if (open && datasetId) void load();
    if (!open) setDataset(null);
  }, [open, datasetId, load]);

  const notifyChanged = () => {
    onChanged?.();
  };

  const saveOverview = async () => {
    if (!datasetId) return;
    try {
      const values = await overviewForm.validateFields();
      setSaving(true);
      await client.put(endpoints.dataset(datasetId), {
        description: values.description,
        main_dttm_col: values.main_dttm_col,
      });
      message.success(t('dataset.detail.updated'));
      await load();
      notifyChanged();
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const submitColumn = async (values: ColumnFormValues) => {
    if (!datasetId) return;
    try {
      setSaving(true);
      const payload = {
        column_name: values.column_name,
        verbose_name: values.verbose_name,
        type: values.type,
        description: values.description,
        is_dttm: values.is_dttm,
        groupby: values.groupby,
        filterable: values.filterable,
        mask_type: values.mask_type ?? '',
        mask_rule: values.mask_rule ?? '',
        // Agent exposure (W-01): hidden columns disappear from the Agent's
        // schema and are rejected by its SQL guard.
        agent_visible: values.agent_visible,
        pii_level: values.pii_level ?? 'none',
      };
      if (columnEditing) {
        await client.put(endpoints.datasetColumn(datasetId, columnEditing.id), payload);
      } else {
        await client.post(endpoints.datasetColumns(datasetId), payload);
      }
      message.success(t('dataset.detail.column_saved'));
      setColumnOpen(false);
      await load();
      notifyChanged();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const submitMetric = async (values: MetricFormValues) => {
    if (!datasetId) return;
    try {
      setSaving(true);
      if (metricEditing) {
        await client.put(endpoints.datasetMetric(datasetId, metricEditing.id), values);
      } else {
        await client.post(endpoints.datasetMetrics(datasetId), values);
      }
      message.success(t('dataset.detail.metric_saved'));
      setMetricOpen(false);
      await load();
      notifyChanged();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const deleteColumn = async (column: DatasetColumn) => {
    if (!datasetId) return;
    try {
      await client.delete(endpoints.datasetColumn(datasetId, column.id));
      message.success(t('dataset.detail.column_deleted'));
      await load();
      notifyChanged();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const deleteMetric = async (metric: DatasetMetric) => {
    if (!datasetId) return;
    try {
      await client.delete(endpoints.datasetMetric(datasetId, metric.id));
      message.success(t('dataset.detail.metric_deleted'));
      await load();
      notifyChanged();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  // One refresh entry point for every dataset kind: external sources are
  // re-pulled, physical/virtual datasets have their columns re-read. The
  // outcome and its trigger are recorded server-side and shown in the
  // "数据刷新" tab.
  const refreshData = async () => {
    if (!datasetId) return;
    try {
      setSaving(true);
      await client.post(endpoints.datasetRefresh(datasetId), {});
      message.success(t('dataset.list.refresh_success'));
      await load();
      notifyChanged();
    } catch (error) {
      message.error(errorMessage(error));
      await load();
    } finally {
      setSaving(false);
    }
  };

  const columnColumns: ColumnsType<DatasetColumn> = [
    {
      title: t('dataset.detail.col_name'),
      dataIndex: 'column_name',
      key: 'column_name',
      render: (value: string, record) => (
        <Space size={4} wrap>
          <span className="mono">{value}</span>
          {record.is_dttm ? <Tag color="blue">{t('dataset.detail.col_time_tag')}</Tag> : null}
          {record.agent_visible === false ? (
            <Tag color="red">{t('dataset.agent.hidden_tag')}</Tag>
          ) : null}
          {record.masked ? <Tag color="orange">{t('dataset.mask.title')}</Tag> : null}
        </Space>
      ),
    },
    {
      title: t('dataset.detail.col_type'),
      dataIndex: 'type',
      key: 'type',
      width: 140,
      render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
    },
    {
      title: t('dataset.detail.col_group'),
      dataIndex: 'groupby',
      width: 70,
      render: (value?: boolean) =>
        value === false ? t('common.state.no') : t('common.state.yes'),
    },
    {
      title: t('dataset.detail.col_filter'),
      dataIndex: 'filterable',
      width: 70,
      render: (value?: boolean) =>
        value === false ? t('common.state.no') : t('common.state.yes'),
    },
    {
      title: t('common.label.description'),
      dataIndex: 'description',
      render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
    },
    {
      title: t('common.label.actions'),
      key: 'actions',
      width: 100,
      render: (_, record) => (
        <Space size={2}>
          <Button
            size="small"
            type="text"
            icon={<EditOutlined />}
            onClick={() => {
              setColumnEditing(record);
              setColumnOpen(true);
            }}
          />
          <Popconfirm
            title={t('dataset.detail.delete_column')}
            okText={t('common.actions.delete')}
            cancelText={t('common.actions.cancel')}
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteColumn(record)}
          >
            <Button size="small" type="text" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const metricColumns: ColumnsType<DatasetMetric> = [
    {
      title: t('dataset.detail.col_metric'),
      dataIndex: 'metric_name',
      key: 'metric_name',
      render: (value: string, record) => (
        <div>
          <div className="mono">{value}</div>
          {record.verbose_name ? <div className="cell-muted">{record.verbose_name}</div> : null}
        </div>
      ),
    },
    {
      title: t('dataset.detail.col_expression'),
      dataIndex: 'expression',
      key: 'expression',
      render: (value: string) => <code className="cell-mono">{value}</code>,
    },
    {
      title: t('common.label.type'),
      dataIndex: 'metric_type',
      width: 110,
      render: (value?: string) => <Tag bordered={false}>{value || '—'}</Tag>,
    },
    {
      title: t('common.label.actions'),
      key: 'actions',
      width: 100,
      render: (_, record) => (
        <Space size={2}>
          <Button
            size="small"
            type="text"
            icon={<EditOutlined />}
            onClick={() => {
              setMetricEditing(record);
              setMetricOpen(true);
            }}
          />
          <Popconfirm
            title={t('dataset.detail.delete_metric')}
            okText={t('common.actions.delete')}
            cancelText={t('common.actions.cancel')}
            okButtonProps={{ danger: true }}
            onConfirm={() => void deleteMetric(record)}
          >
            <Button size="small" type="text" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Drawer
      title={
        dataset
          ? t('dataset.detail.title_with_name', { name: dataset.table_name })
          : t('dataset.detail.title')
      }
      width={860}
      open={open}
      onClose={onClose}
      destroyOnClose
      extra={
        <Button
          icon={<ReloadOutlined />}
          onClick={() => void refreshData()}
          loading={saving}
          disabled={!canWrite}
        >
          {t('dataset.refresh.run_now')}
        </Button>
      }
    >
      <Tabs
        items={[
          {
            key: 'overview',
            label: t('dataset.detail.overview'),
            children: (
              <Form form={overviewForm} layout="vertical" disabled={!dataset}>
                <div style={{ marginBottom: 14 }}>
                  <span className="tiny-label">{t('dataset.list.source')}</span>
                  <div style={{ marginTop: 4 }}>
                    {dataset ? (
                      dataset.kind === 'external' ? (
                        dataset.source?.type === 'api' ? (
                          <Space size={6}>
                            <Tag color="geekblue" bordered={false}>
                              {t('dataset.source.api')}
                            </Tag>
                            <span className="mono cell-muted">{dataset.source?.url}</span>
                          </Space>
                        ) : (
                          <Space size={6}>
                            <Tag color="green" bordered={false}>
                              {t('dataset.source.file')}
                            </Tag>
                            <span className="mono cell-muted">{dataset.source?.filename}</span>
                          </Space>
                        )
                      ) : dataset.kind === 'virtual' || dataset.sql ? (
                        <Tag color="purple" bordered={false}>
                          {t('dataset.source.virtual')}
                        </Tag>
                      ) : (
                        <Tag bordered={false}>{t('dataset.source.physical')}</Tag>
                      )
                    ) : (
                      '—'
                    )}
                  </div>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                  <div>
                    <span className="tiny-label">Database</span>
                    <div style={{ marginTop: 4 }}>
                      {dataset ? <Tag color="cyan">{dataset.database?.database_name}</Tag> : '—'}
                    </div>
                  </div>
                  <div>
                    <span className="tiny-label">Schema</span>
                    <div style={{ marginTop: 4 }}>{dataset?.schema || '—'}</div>
                  </div>
                </div>
                <Form.Item
                  name="main_dttm_col"
                  label={t('dataset.detail.main_dttm_col')}
                  style={{ marginTop: 14 }}
                >
                  <Select
                    allowClear
                    placeholder={t('common.state.not_set')}
                    options={(dataset?.columns ?? [])
                      .filter(column => column.is_dttm)
                      .map(column => ({ value: column.column_name, label: column.column_name }))}
                  />
                </Form.Item>
                <Form.Item name="description" label={t('common.label.description')}>
                  <Input.TextArea rows={3} maxLength={400} />
                </Form.Item>
                <Button type="primary" loading={saving} onClick={() => void saveOverview()}>
                  {t('common.actions.save')}
                </Button>
                {dataset?.sql ? (
                  <>
                    <div className="tiny-label" style={{ margin: '18px 0 6px' }}>
                      Virtual dataset SQL
                    </div>
                    <pre className="mono-block">{dataset.sql}</pre>
                  </>
                ) : null}
              </Form>
            ),
          },
          {
            key: 'refresh',
            label: t('dataset.refresh.tab'),
            children: dataset ? (
              <DatasetRefreshPanel
                dataset={dataset}
                canWrite={canWrite}
                onChanged={async () => {
                  await load();
                  notifyChanged();
                }}
              />
            ) : null,
          },
          {
            key: 'samples',
            label: t('dataset.detail.tab_samples'),
            children: datasetId ? <DatasetSamplePanel datasetId={datasetId} /> : null,
          },
          {
            key: 'columns',
            label: t('dataset.detail.tab_columns', { count: dataset?.columns?.length ?? 0 }),
            children: (
              <>
                <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
                  <Button
                    size="small"
                    icon={<PlusOutlined />}
                    onClick={() => {
                      setColumnEditing(null);
                      setColumnOpen(true);
                    }}
                  >
                    {t('dataset.detail.new_column')}
                  </Button>
                </div>
                <Table<DatasetColumn>
                  size="small"
                  rowKey="id"
                  loading={loading}
                  columns={columnColumns}
                  dataSource={dataset?.columns ?? []}
                  pagination={{ pageSize: 10, size: 'small', showSizeChanger: false }}
                />
              </>
            ),
          },
          {
            key: 'semantic',
            label: t('dataset.semantic.tab'),
            children: datasetId ? <DatasetSemanticPanel datasetId={datasetId} canWrite={canWrite} /> : null,
          },
          {
            key: 'metrics',
            label: t('dataset.detail.tab_metrics', { count: dataset?.metrics?.length ?? 0 }),
            children: (
              <>
                <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
                  <Button
                    size="small"
                    icon={<PlusOutlined />}
                    onClick={() => {
                      setMetricEditing(null);
                      setMetricOpen(true);
                    }}
                  >
                    {t('dataset.detail.new_metric')}
                  </Button>
                </div>
                <Table<DatasetMetric>
                  size="small"
                  rowKey="id"
                  loading={loading}
                  columns={metricColumns}
                  dataSource={dataset?.metrics ?? []}
                  pagination={{ pageSize: 10, size: 'small', showSizeChanger: false }}
                />
              </>
            ),
          },
        ]}
      />

      <DatasetColumnModal
        open={columnOpen}
        column={columnEditing}
        saving={saving}
        onCancel={() => setColumnOpen(false)}
        onSubmit={values => void submitColumn(values)}
      />

      <DatasetMetricModal
        open={metricOpen}
        metric={metricEditing}
        saving={saving}
        onCancel={() => setMetricOpen(false)}
        onSubmit={values => void submitMetric(values)}
      />
    </Drawer>
  );
}
