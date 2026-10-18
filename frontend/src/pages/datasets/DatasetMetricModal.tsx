import { useEffect } from 'react';
import { Form, Input, Modal, Select } from 'antd';
import type { DatasetMetric } from '../../api/types';
import { useI18n } from '../../i18n';

export interface MetricFormValues {
  metric_name: string;
  verbose_name?: string;
  expression: string;
  metric_type?: string;
  d3format?: string;
  description?: string;
}

export interface DatasetMetricModalProps {
  open: boolean;
  metric: DatasetMetric | null;
  saving: boolean;
  onCancel: () => void;
  onSubmit: (values: MetricFormValues) => void;
}

const METRIC_TYPES = ['count', 'sum', 'avg', 'min', 'max', 'count_distinct', 'expression'].map(
  value => ({ value, label: value }),
);

/** Create / edit a SQL metric on a dataset. */
export default function DatasetMetricModal({
  open,
  metric,
  saving,
  onCancel,
  onSubmit,
}: DatasetMetricModalProps) {
  const { t } = useI18n();
  const [form] = Form.useForm<MetricFormValues>();

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      metric_name: metric?.metric_name ?? '',
      verbose_name: metric?.verbose_name ?? '',
      expression: metric?.expression ?? '',
      metric_type: metric?.metric_type ?? 'sum',
      d3format: metric?.d3format ?? '',
      description: metric?.description ?? '',
    });
  }, [open, metric, form]);

  const handleOk = async () => {
    try {
      const values = await form.validateFields();
      onSubmit(values);
    } catch {
      // Validation errors are rendered inline by antd.
    }
  };

  return (
    <Modal
      title={metric ? t('dataset.metric.edit') : t('dataset.metric.new')}
      open={open}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      confirmLoading={saving}
      okText={t('common.actions.save')}
      cancelText={t('common.actions.cancel')}
      destroyOnClose
    >
      <Form form={form} layout="vertical" preserve={false}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <Form.Item
            name="metric_name"
            label={t('dataset.metric.name')}
            rules={[{ required: true, message: t('dataset.metric.name_required') }]}
          >
            <Input placeholder="revenue" />
          </Form.Item>
          <Form.Item name="verbose_name" label={t('dataset.column.verbose_name')}>
            <Input placeholder="Revenue" />
          </Form.Item>
        </div>
        <Form.Item
          name="expression"
          label={t('dataset.metric.expression')}
          rules={[{ required: true, message: t('dataset.metric.expression_required') }]}
        >
          <Input.TextArea rows={2} placeholder="SUM(amount)" className="mono" />
        </Form.Item>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <Form.Item name="metric_type" label={t('common.label.type')}>
            <Select options={METRIC_TYPES} />
          </Form.Item>
          <Form.Item name="d3format" label={t('dataset.metric.d3format')}>
            <Input placeholder=",0.00" />
          </Form.Item>
        </div>
        <Form.Item name="description" label={t('common.label.description')}>
          <Input.TextArea rows={2} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
