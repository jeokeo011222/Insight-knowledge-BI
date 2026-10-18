import { useEffect, useMemo, useState } from 'react';
import { Button, Form, Input, Modal, Popconfirm, Select, Space, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Paginated, Role, RlsFilter } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';

interface RlsFormValues {
  name: string;
  description?: string;
  filter_type: string;
  clause: string;
  roles: number[];
  tables: number[];
}

interface DatasetOption {
  id: number;
  table_name: string;
}

interface RlsListProps {
  /** Rendered as a tab of 用户与角色: the page supplies the heading. */
  embedded?: boolean;
}

export default function RlsList({ embedded = false }: RlsListProps) {
  const { can } = usePermission();
  const { t } = useI18n();
  const [reloadToken, setReloadToken] = useState(0);
  const [roles, setRoles] = useState<Role[]>([]);
  const [datasets, setDatasets] = useState<DatasetOption[]>([]);
  const [editing, setEditing] = useState<RlsFilter | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<RlsFormValues>();

  const canWrite = can('can_write', 'RowLevelSecurity');

  // Roles and datasets populate the association selects. Only loaded for users
  // who can actually create/edit a filter.
  useEffect(() => {
    if (!canWrite) return;
    client
      .get<Paginated<Role>>(endpoints.roles, { page: 1, page_size: 100 })
      .then(response => setRoles(response.result ?? []))
      .catch(error => message.error(errorMessage(error)));
    client
      .get<Paginated<DatasetOption>>(endpoints.datasets, { page: 1, page_size: 100 })
      .then(response => setDatasets(response.result ?? []))
      .catch(error => message.error(errorMessage(error)));
  }, [canWrite]);

  const fetchFilters = (query: ListQuery): Promise<Paginated<RlsFilter>> =>
    client.get<Paginated<RlsFilter>>(endpoints.rowLevelSecurity, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ filter_type: 'Regular', roles: [], tables: [] });
    setModalOpen(true);
  };

  const openEdit = (filter: RlsFilter) => {
    setEditing(filter);
    form.resetFields();
    form.setFieldsValue({
      name: filter.name,
      description: filter.description ?? '',
      filter_type: filter.filter_type || 'Regular',
      clause: filter.clause,
      roles: (filter.roles ?? []).map(role => role.id),
      tables: (filter.tables ?? []).map(table => table.id),
    });
    setModalOpen(true);
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);
      if (editing) {
        await client.put(endpoints.rlsFilter(editing.id), values);
        message.success(t('rls.list.updated', { name: values.name }));
      } else {
        await client.post(endpoints.rowLevelSecurity, values);
        message.success(t('rls.list.created', { name: values.name }));
      }
      setModalOpen(false);
      setReloadToken(value => value + 1);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (filter: RlsFilter) => {
    try {
      await client.delete(endpoints.rlsFilter(filter.id));
      message.success(t('rls.list.deleted', { name: filter.name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<RlsFilter>>(
    () => [
      {
        title: t('common.label.name'),
        dataIndex: 'name',
        key: 'name',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('rls.list.filter_type'),
        dataIndex: 'filter_type',
        key: 'filter_type',
        width: 120,
        render: (value: string) => <Tag bordered={false}>{value || 'Regular'}</Tag>,
      },
      {
        title: t('rls.list.sql_condition'),
        dataIndex: 'clause',
        key: 'clause',
        ellipsis: true,
        render: (value: string) => <code className="cell-mono">{value}</code>,
      },
      {
        title: t('common.label.role'),
        key: 'roles',
        width: 180,
        render: (_, record) => (
          <Space size={4} wrap>
            {(record.roles ?? []).map(role => (
              <Tag key={role.id}>{role.name}</Tag>
            ))}
            {!record.roles?.length ? <span className="cell-muted">—</span> : null}
          </Space>
        ),
      },
      {
        title: t('common.label.dataset'),
        key: 'tables',
        width: 200,
        render: (_, record) => (
          <span className="cell-muted">
            {(record.tables ?? []).map(table => table.table_name).join(', ') || '—'}
          </span>
        ),
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 100,
        render: (_, record) => (
          <Space size={2}>
            {canWrite ? (
              <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEdit(record)} />
            ) : null}
            {can('can_delete', 'RowLevelSecurity') ? (
              <Popconfirm
                title={t('rls.list.delete_title')}
                description={t('rls.list.delete_confirm', { name: record.name })}
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
    [can, canWrite, t],
  );

  return (
    <>
      <EntityList<RlsFilter>
        title={embedded ? undefined : t('nav.rls')}
        subtitle={embedded ? undefined : t('rls.list.subtitle')}
        columns={columns}
        fetchData={fetchFilters}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('rls.list.search_placeholder')}
        emptyDescription={t('rls.list.empty')}
        extra={
          canWrite ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {t('rls.list.create')}
            </Button>
          ) : null
        }
      />

      <Modal
        title={editing ? t('rls.form.title_edit') : t('rls.form.title_create')}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => void handleSubmit()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
        forceRender
        width={640}
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="name"
            label={t('common.label.name')}
            rules={[{ required: true, message: t('rls.form.name_required') }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="description" label={t('rls.form.description')}>
            <Input />
          </Form.Item>
          <Form.Item name="filter_type" label={t('rls.form.filter_type')}>
            <Select
              options={[
                { value: 'Regular', label: 'Regular' },
                { value: 'Base', label: 'Base' },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="clause"
            label={t('rls.form.clause')}
            rules={[{ required: true, message: t('rls.form.clause_required') }]}
          >
            <Input.TextArea rows={3} placeholder={t('rls.form.clause_placeholder')} />
          </Form.Item>
          <Form.Item name="roles" label={t('rls.form.roles')}>
            <Select
              mode="multiple"
              allowClear
              optionFilterProp="label"
              options={roles.map(role => ({ value: role.id, label: role.name }))}
            />
          </Form.Item>
          <Form.Item name="tables" label={t('rls.form.tables')}>
            <Select
              mode="multiple"
              allowClear
              optionFilterProp="label"
              options={datasets.map(dataset => ({ value: dataset.id, label: dataset.table_name }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
