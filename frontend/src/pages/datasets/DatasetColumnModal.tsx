import { useEffect } from 'react';
import { Alert, Checkbox, Form, Input, Modal, Select, Space, Switch } from 'antd';
import type { DatasetColumn } from '../../api/types';
import { useI18n } from '../../i18n';

export interface ColumnFormValues {
  column_name: string;
  verbose_name?: string;
  type?: string;
  description?: string;
  is_dttm: boolean;
  groupby: boolean;
  filterable: boolean;
  /** "" (off) | phone | idcard | email | name | full | custom */
  mask_type?: string;
  mask_rule?: string;
  /** Whether the Agent may see this column at all (default true). */
  agent_visible: boolean;
  /** none | low | high — coarse sensitivity class. */
  pii_level?: string;
}

export interface DatasetColumnModalProps {
  open: boolean;
  column: DatasetColumn | null;
  saving: boolean;
  onCancel: () => void;
  onSubmit: (values: ColumnFormValues) => void;
}

/** Create / edit a physical or computed dataset column. */
export default function DatasetColumnModal({
  open,
  column,
  saving,
  onCancel,
  onSubmit,
}: DatasetColumnModalProps) {
  const { t } = useI18n();
  const [form] = Form.useForm<ColumnFormValues>();

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      column_name: column?.column_name ?? '',
      verbose_name: column?.verbose_name ?? '',
      type: column?.type ?? '',
      description: column?.description ?? '',
      is_dttm: Boolean(column?.is_dttm),
      groupby: column?.groupby !== false,
      filterable: column?.filterable !== false,
      mask_type: column?.mask_type ?? '',
      mask_rule: column?.mask_rule ?? '',
      agent_visible: column?.agent_visible !== false,
      pii_level: column?.pii_level ?? 'none',
    });
  }, [open, column, form]);

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
      title={column ? t('dataset.column.edit') : t('dataset.column.new')}
      open={open}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      confirmLoading={saving}
      okText={t('common.actions.save')}
      cancelText={t('common.actions.cancel')}
      destroyOnClose
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item
          name="column_name"
          label={t('dataset.detail.col_name')}
          rules={[{ required: true, message: t('dataset.column.name_required') }]}
        >
          <Input placeholder={t('dataset.column.name_placeholder')} />
        </Form.Item>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <Form.Item name="verbose_name" label={t('dataset.column.verbose_name')}>
            <Input />
          </Form.Item>
          <Form.Item name="type" label={t('common.label.type')}>
            <Input placeholder="VARCHAR(255)" />
          </Form.Item>
        </div>
        <Form.Item name="description" label={t('common.label.description')}>
          <Input.TextArea rows={2} />
        </Form.Item>
        <Space size={20}>
          <Form.Item name="is_dttm" label={t('dataset.column.dttm')} valuePropName="checked">
            <Switch size="small" />
          </Form.Item>
          <Form.Item name="groupby" label={t('dataset.column.groupby')} valuePropName="checked">
            <Checkbox />
          </Form.Item>
          <Form.Item name="filterable" label={t('dataset.column.filterable')} valuePropName="checked">
            <Checkbox />
          </Form.Item>
        </Space>

        <Form.Item
          name="mask_type"
          label={t('dataset.mask.title')}
          extra={t('dataset.mask.hint')}
        >
          <Select
            allowClear
            placeholder={t('dataset.mask.off')}
            options={[
              { value: '', label: t('dataset.mask.off') },
              { value: 'phone', label: t('dataset.mask.phone') },
              { value: 'idcard', label: t('dataset.mask.idcard') },
              { value: 'email', label: t('dataset.mask.email') },
              { value: 'name', label: t('dataset.mask.name') },
              { value: 'full', label: t('dataset.mask.full') },
              { value: 'custom', label: t('dataset.mask.custom') },
            ]}
          />
        </Form.Item>

        <Form.Item noStyle shouldUpdate>
          {({ getFieldValue }) =>
            getFieldValue('mask_type') === 'custom' ? (
              <>
                <Form.Item
                  name="mask_rule"
                  label={t('dataset.mask.rule')}
                  rules={[{ required: true, message: t('dataset.mask.rule') }]}
                >
                  <Input placeholder={t('dataset.mask.rule_placeholder')} />
                </Form.Item>
                <Alert type="info" showIcon message={t('dataset.mask.hint')} />
              </>
            ) : null
          }
        </Form.Item>

        {/* Agent exposure (W-01): hiding a column is stronger than masking it,
            because the model never learns the column exists. */}
        <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: 12, alignItems: 'start' }}>
          <Form.Item
            name="agent_visible"
            label={t('dataset.agent.visible')}
            valuePropName="checked"
            extra={t('dataset.agent.visible_hint')}
          >
            <Switch size="small" />
          </Form.Item>
          <Form.Item
            name="pii_level"
            label={t('dataset.agent.pii_level')}
            extra={t('dataset.agent.pii_level_hint')}
          >
            <Select
              options={[
                { value: 'none', label: t('dataset.agent.pii_none') },
                { value: 'low', label: t('dataset.agent.pii_low') },
                { value: 'high', label: t('dataset.agent.pii_high') },
              ]}
            />
          </Form.Item>
        </div>
      </Form>
    </Modal>
  );
}
