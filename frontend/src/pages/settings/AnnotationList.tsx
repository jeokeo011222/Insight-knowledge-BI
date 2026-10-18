import { useMemo, useState } from 'react';
import {
  Button,
  DatePicker,
  Drawer,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined, TagsOutlined } from '@ant-design/icons';
import type { Dayjs } from 'dayjs';
import dayjs from 'dayjs';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Annotation, AnnotationLayer, Paginated } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

interface LayerFormValues {
  name: string;
  descr?: string;
}

interface AnnotationFormValues {
  short_descr: string;
  long_descr?: string;
  start_dttm?: Dayjs | null;
  end_dttm?: Dayjs | null;
}

const TIME_FORMAT = 'YYYY-MM-DD HH:mm:ss';

export default function AnnotationList() {
  const { can } = usePermission();
  const { t } = useI18n();
  const [reloadToken, setReloadToken] = useState(0);

  // Layer create / edit
  const [layerModalOpen, setLayerModalOpen] = useState(false);
  const [editingLayer, setEditingLayer] = useState<AnnotationLayer | null>(null);
  const [layerSaving, setLayerSaving] = useState(false);
  const [layerForm] = Form.useForm<LayerFormValues>();

  // Annotation events of the selected layer
  const [eventsLayer, setEventsLayer] = useState<AnnotationLayer | null>(null);
  const [eventsReloadToken, setEventsReloadToken] = useState(0);
  const [annotationModalOpen, setAnnotationModalOpen] = useState(false);
  const [editingAnnotation, setEditingAnnotation] = useState<Annotation | null>(null);
  const [annotationSaving, setAnnotationSaving] = useState(false);
  const [annotationForm] = Form.useForm<AnnotationFormValues>();

  const canWriteLayer = can('can_write', 'AnnotationLayer');
  const canWriteAnnotation = can('can_write', 'Annotation');

  const fetchLayers = (query: ListQuery): Promise<Paginated<AnnotationLayer>> =>
    client.get<Paginated<AnnotationLayer>>(endpoints.annotationLayers, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const fetchEvents = (query: ListQuery): Promise<Paginated<Annotation>> =>
    client.get<Paginated<Annotation>>(`${endpoints.annotationLayer(eventsLayer?.id ?? 0)}/annotation/`, {
      page: query.page,
      page_size: query.pageSize,
    });

  const openCreateLayer = () => {
    setEditingLayer(null);
    layerForm.resetFields();
    setLayerModalOpen(true);
  };

  const openEditLayer = (layer: AnnotationLayer) => {
    setEditingLayer(layer);
    layerForm.resetFields();
    layerForm.setFieldsValue({ name: layer.name, descr: layer.descr ?? '' });
    setLayerModalOpen(true);
  };

  const handleSubmitLayer = async () => {
    try {
      const values = await layerForm.validateFields();
      setLayerSaving(true);
      if (editingLayer) {
        await client.put(endpoints.annotationLayer(editingLayer.id), values);
        message.success(t('settings.annotation.updated', { name: values.name }));
      } else {
        await client.post(endpoints.annotationLayers, values);
        message.success(t('settings.annotation.created'));
      }
      setLayerModalOpen(false);
      setReloadToken(value => value + 1);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setLayerSaving(false);
    }
  };

  const handleDeleteLayer = async (layer: AnnotationLayer) => {
    try {
      await client.delete(endpoints.annotationLayer(layer.id));
      message.success(t('settings.annotation.deleted', { name: layer.name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const openCreateAnnotation = () => {
    setEditingAnnotation(null);
    annotationForm.resetFields();
    setAnnotationModalOpen(true);
  };

  const openEditAnnotation = (annotation: Annotation) => {
    setEditingAnnotation(annotation);
    annotationForm.resetFields();
    annotationForm.setFieldsValue({
      short_descr: annotation.short_descr,
      long_descr: annotation.long_descr ?? '',
      start_dttm: annotation.start_dttm ? dayjs(annotation.start_dttm) : null,
      end_dttm: annotation.end_dttm ? dayjs(annotation.end_dttm) : null,
    });
    setAnnotationModalOpen(true);
  };

  const handleSubmitAnnotation = async () => {
    if (!eventsLayer) return;
    try {
      const values = await annotationForm.validateFields();
      setAnnotationSaving(true);
      const payload = {
        short_descr: values.short_descr,
        long_descr: values.long_descr ?? '',
        layer_id: eventsLayer.id,
        start_dttm: values.start_dttm ? values.start_dttm.format(TIME_FORMAT) : '',
        end_dttm: values.end_dttm ? values.end_dttm.format(TIME_FORMAT) : '',
      };
      if (editingAnnotation) {
        await client.put(endpoints.annotation(editingAnnotation.id), payload);
        message.success(t('settings.annotation.events.updated'));
      } else {
        await client.post(endpoints.annotations, payload);
        message.success(t('settings.annotation.events.created'));
      }
      setAnnotationModalOpen(false);
      setEventsReloadToken(value => value + 1);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setAnnotationSaving(false);
    }
  };

  const handleDeleteAnnotation = async (annotation: Annotation) => {
    try {
      await client.delete(endpoints.annotation(annotation.id));
      message.success(t('settings.annotation.events.deleted'));
      setEventsReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<AnnotationLayer>>(
    () => [
      {
        title: t('common.label.name'),
        dataIndex: 'name',
        key: 'name',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('common.label.description'),
        dataIndex: 'descr',
        key: 'descr',
        render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('settings.annotation.created_at'),
        dataIndex: 'created_on',
        key: 'created_on',
        width: 180,
        render: (value?: string) => <span className="cell-muted">{formatDateTimeShort(value)}</span>,
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
              icon={<TagsOutlined />}
              onClick={() => {
                setEventsLayer(record);
                setEventsReloadToken(value => value + 1);
              }}
            >
              {t('settings.annotation.manage')}
            </Button>
            {canWriteLayer ? (
              <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEditLayer(record)} />
            ) : null}
            {can('can_delete', 'AnnotationLayer') ? (
              <Popconfirm
                title={t('settings.annotation.delete_title')}
                description={t('settings.annotation.delete_confirm', { name: record.name })}
                okText={t('common.actions.delete')}
                cancelText={t('common.actions.cancel')}
                okButtonProps={{ danger: true }}
                onConfirm={() => void handleDeleteLayer(record)}
              >
                <Button size="small" type="text" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            ) : null}
          </Space>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [can, canWriteLayer, t],
  );

  const eventColumns = useMemo<ColumnsType<Annotation>>(
    () => [
      {
        title: t('settings.annotation.events.short_descr'),
        dataIndex: 'short_descr',
        key: 'short_descr',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('settings.annotation.events.long_descr'),
        dataIndex: 'long_descr',
        key: 'long_descr',
        render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('settings.annotation.events.start'),
        dataIndex: 'start_dttm',
        key: 'start_dttm',
        width: 160,
        render: (value?: string) => <span className="cell-muted">{formatDateTimeShort(value)}</span>,
      },
      {
        title: t('settings.annotation.events.end'),
        dataIndex: 'end_dttm',
        key: 'end_dttm',
        width: 160,
        render: (value?: string) => <span className="cell-muted">{formatDateTimeShort(value)}</span>,
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 100,
        render: (_, record) => (
          <Space size={2}>
            {canWriteAnnotation ? (
              <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEditAnnotation(record)} />
            ) : null}
            {can('can_delete', 'Annotation') ? (
              <Popconfirm
                title={t('settings.annotation.events.delete_title')}
                okText={t('common.actions.delete')}
                cancelText={t('common.actions.cancel')}
                okButtonProps={{ danger: true }}
                onConfirm={() => void handleDeleteAnnotation(record)}
              >
                <Button size="small" type="text" danger icon={<DeleteOutlined />} />
              </Popconfirm>
            ) : null}
          </Space>
        ),
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [can, canWriteAnnotation, t],
  );

  return (
    <>
      <EntityList<AnnotationLayer>
        title={t('nav.annotations')}
        subtitle={t('settings.annotation.subtitle')}
        columns={columns}
        fetchData={fetchLayers}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('settings.annotation.search_placeholder')}
        extra={
          canWriteLayer ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreateLayer}>
              {t('settings.annotation.create')}
            </Button>
          ) : null
        }
      />

      <Modal
        title={editingLayer ? t('settings.annotation.edit_layer') : t('settings.annotation.create')}
        open={layerModalOpen}
        onCancel={() => setLayerModalOpen(false)}
        onOk={() => void handleSubmitLayer()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={layerSaving}
        destroyOnClose
      >
        <Form form={layerForm} layout="vertical" preserve={false}>
          <Form.Item
            name="name"
            label={t('common.label.name')}
            rules={[{ required: true, message: t('settings.annotation.name_required') }]}
          >
            <Input placeholder={t('settings.annotation.name_placeholder')} />
          </Form.Item>
          <Form.Item name="descr" label={t('common.label.description')}>
            <Input.TextArea rows={3} maxLength={400} />
          </Form.Item>
          <Space>
            <span className="cell-muted">{t('settings.annotation.hint')}</span>
          </Space>
        </Form>
      </Modal>

      <Drawer
        title={`${t('settings.annotation.events.title')} · ${eventsLayer?.name ?? ''}`}
        width={720}
        open={Boolean(eventsLayer)}
        onClose={() => setEventsLayer(null)}
        destroyOnClose
      >
        <EntityList<Annotation>
          columns={eventColumns}
          fetchData={fetchEvents}
          rowKey={record => record.id}
          reloadToken={eventsReloadToken}
          showSearch={false}
          emptyDescription={t('settings.annotation.events.empty')}
          extra={
            canWriteAnnotation ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreateAnnotation}>
                {t('settings.annotation.events.create')}
              </Button>
            ) : null
          }
        />
      </Drawer>

      <Modal
        title={editingAnnotation ? t('settings.annotation.events.edit') : t('settings.annotation.events.create')}
        open={annotationModalOpen}
        onCancel={() => setAnnotationModalOpen(false)}
        onOk={() => void handleSubmitAnnotation()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={annotationSaving}
        destroyOnClose
      >
        <Form form={annotationForm} layout="vertical" preserve={false}>
          <Form.Item
            name="short_descr"
            label={t('settings.annotation.events.short_descr')}
            rules={[{ required: true, message: t('settings.annotation.events.short_required') }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="long_descr" label={t('settings.annotation.events.long_descr')}>
            <Input.TextArea rows={2} maxLength={400} />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <Form.Item name="start_dttm" label={t('settings.annotation.events.start')}>
              <DatePicker showTime style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item name="end_dttm" label={t('settings.annotation.events.end')}>
              <DatePicker showTime style={{ width: '100%' }} />
            </Form.Item>
          </div>
        </Form>
      </Modal>
    </>
  );
}
