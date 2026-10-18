import { useEffect, useMemo } from 'react';
import {
  Alert,
  Checkbox,
  Form,
  Input,
  Modal,
  Select,
  Space,
} from 'antd';
import type { Dataset, NativeFilterConfig, NativeFilterType } from '../../api/types';
import {
  FILTER_TYPE_LABELS,
  createFilterId,
  filterColumnName,
} from '../../features/dashboard/filters';
import { useI18n } from '../../i18n';

export interface FilterConfigModalProps {
  open: boolean;
  /** Null creates a new filter. */
  filter: NativeFilterConfig | null;
  datasets: Dataset[];
  saving: boolean;
  onCancel: () => void;
  onSubmit: (config: NativeFilterConfig) => void;
}

interface FilterFormValues {
  name: string;
  filterType: NativeFilterType;
  datasetId?: number;
  column?: string;
  defaultValue?: string;
  description?: string;
  requiredFirst?: boolean;
  multiSelection?: boolean;
  defaultToFirstItem?: boolean;
  sortAscending?: boolean;
}

const TYPE_OPTIONS = (Object.keys(FILTER_TYPE_LABELS) as NativeFilterType[]).map(
  type => ({ value: type, label: FILTER_TYPE_LABELS[type] }),
);

/** Creates / edits one native filter definition (stored in json_metadata). */
export default function FilterConfigModal({
  open,
  filter,
  datasets,
  saving,
  onCancel,
  onSubmit,
}: FilterConfigModalProps) {
  const { t } = useI18n();
  const [form] = Form.useForm<FilterFormValues>();
  const filterType = Form.useWatch('filterType', form) ?? 'filter_select';
  const datasetId = Form.useWatch('datasetId', form);
  const dataset = useMemo(
    () => datasets.find(item => item.id === datasetId) ?? null,
    [datasets, datasetId],
  );

  useEffect(() => {
    if (!open) return;
    form.setFieldsValue({
      name: filter?.name ?? FILTER_TYPE_LABELS.filter_select,
      filterType: filter?.filterType ?? 'filter_select',
      datasetId: filter?.targets[0]?.datasetId ?? datasets[0]?.id,
      column: filter ? filterColumnName(filter) : undefined,
      defaultValue: undefined,
      description: filter?.description ?? '',
      requiredFirst: filter?.requiredFirst ?? false,
      multiSelection: filter?.controlValues?.multiSelection ?? false,
      defaultToFirstItem: filter?.controlValues?.defaultToFirstItem ?? false,
      sortAscending: filter?.controlValues?.sortAscending ?? true,
    });
  }, [open, filter, datasets, form]);

  useEffect(() => {
    if (!dataset) return;
    const current = form.getFieldValue('column') as string | undefined;
    const exists = dataset.columns.some(column => column.column_name === current);
    if (exists) return;
    const fallback =
      dataset.columns.find(column => column.is_dttm)?.column_name ??
      dataset.columns[0]?.column_name;
    form.setFieldValue('column', fallback);
  }, [dataset, form]);

  const handleOk = async () => {
    try {
      const values = await form.validateFields();
      const targets =
        values.filterType === 'filter_divider' || !values.datasetId
          ? []
          : [
              {
                datasetId: values.datasetId,
                column: { name: values.column ?? '' },
              },
            ];
      const rawDefault = values.defaultValue?.trim();
      let defaultDataMask: NativeFilterConfig['defaultDataMask'];
      if (rawDefault) {
        const parsed =
          values.filterType === 'filter_multi_select' ||
          (values.filterType === 'filter_select' && values.multiSelection)
            ? rawDefault.split(',').map(item => item.trim()).filter(Boolean)
            : rawDefault;
        defaultDataMask = {
          filterState: { value: parsed, label: rawDefault },
        };
      }
      onSubmit({
        id: filter?.id ?? createFilterId(),
        name: values.name.trim(),
        filterType: values.filterType,
        targets,
        defaultDataMask: defaultDataMask ?? filter?.defaultDataMask,
        controlValues: {
          multiSelection: values.multiSelection ?? false,
          defaultToFirstItem: values.defaultToFirstItem ?? false,
          sortAscending: values.sortAscending ?? true,
        },
        requiredFirst: values.requiredFirst ?? false,
        description: values.description?.trim() || undefined,
      });
    } catch {
      // Antd renders validation errors inline; nothing else to report here.
      return;
    }
  };

  const needsDataset = filterType !== 'filter_divider';
  const needsColumn =
    filterType !== 'filter_divider' && filterType !== 'filter_time';

  return (
    <Modal
      title={filter ? t('dashboard.filter.edit') : t('dashboard.filter.new')}
      open={open}
      onCancel={onCancel}
      onOk={() => void handleOk()}
      okText={t('common.actions.save')}
      cancelText={t('common.actions.cancel')}
      confirmLoading={saving}
      destroyOnClose
      width={560}
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item
          name="name"
          label={t('dashboard.filter.name')}
          rules={[{ required: true, message: t('dashboard.filter.name_required') }]}
        >
          <Input maxLength={80} placeholder={t('dashboard.filter.name_placeholder')} />
        </Form.Item>

        <Form.Item name="filterType" label={t('dashboard.filter.type')} rules={[{ required: true }]}>
          <Select options={TYPE_OPTIONS} />
        </Form.Item>

        {needsDataset ? (
          <Space style={{ width: '100%' }} align="start" size={12}>
            <Form.Item
              name="datasetId"
              label={t('dashboard.filter.dataset')}
              rules={[{ required: true, message: t('dashboard.filter.dataset_required') }]}
              style={{ flex: '1 1 50%' }}
            >
              <Select
                showSearch
                optionFilterProp="label"
                placeholder={t('dashboard.filter.select_dataset')}
                options={datasets.map(item => ({
                  value: item.id,
                  label: item.schema ? `${item.schema}.${item.table_name}` : item.table_name,
                }))}
              />
            </Form.Item>
            {needsColumn ? (
              <Form.Item
                name="column"
                label={t('dashboard.filter.column')}
                rules={[{ required: true, message: t('dashboard.filter.column_required') }]}
                style={{ flex: '1 1 50%' }}
              >
                <Select
                  showSearch
                  optionFilterProp="label"
                  placeholder={t('dashboard.filter.select_column')}
                  options={(dataset?.columns ?? []).map(column => ({
                    value: column.column_name,
                    label: column.is_dttm
                      ? `${column.column_name} (time)`
                      : column.column_name,
                  }))}
                />
              </Form.Item>
            ) : null}
          </Space>
        ) : null}

        {filterType !== 'filter_divider' ? (
          <Form.Item
            name="defaultValue"
            label={t('dashboard.filter.default_value')}
            extra={t('dashboard.filter.default_value_extra')}
          >
            <Input maxLength={200} placeholder={t('dashboard.filter.default_value_placeholder')} />
          </Form.Item>
        ) : null}

        <Form.Item name="description" label={t('dashboard.filter.description')}>
          <Input.TextArea rows={2} maxLength={200} />
        </Form.Item>

        <Form.Item name="requiredFirst" valuePropName="checked">
          <Checkbox>{t('dashboard.filter.required')}</Checkbox>
        </Form.Item>
        {filterType === 'filter_select' || filterType === 'filter_multi_select' ? (
          <Form.Item name="multiSelection" valuePropName="checked">
            <Checkbox>{t('dashboard.filter.multi')}</Checkbox>
          </Form.Item>
        ) : null}
        {filterType !== 'filter_divider' && filterType !== 'filter_time' ? (
          <Form.Item name="defaultToFirstItem" valuePropName="checked">
            <Checkbox>{t('dashboard.filter.first')}</Checkbox>
          </Form.Item>
        ) : null}
        {filterType === 'filter_select' || filterType === 'filter_multi_select' ? (
          <Form.Item name="sortAscending" valuePropName="checked">
            <Checkbox>{t('dashboard.filter.asc')}</Checkbox>
          </Form.Item>
        ) : null}

        <Alert
          type="info"
          showIcon
          message={t('dashboard.filter.saved_desc')}
        />
      </Form>
    </Modal>
  );
}
