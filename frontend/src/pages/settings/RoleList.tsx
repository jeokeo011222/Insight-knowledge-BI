import { useMemo, useState } from 'react';
import { Button, Drawer, Form, Input, Modal, Popconfirm, Select, Space, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Paginated, RoleDetail } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';

interface RoleFormValues {
  name: string;
  permission_view_ids: number[];
}

interface PermissionRow {
  id: number;
  permission: string;
  view_menu: string;
  permission_id: number;
  view_menu_id: number;
}

const PERMISSION_PAGE_SIZE = 100;

interface RoleListProps {
  /** When rendered inside the merged 用户与角色 tabs, drop the page header. */
  embedded?: boolean;
}

export default function RoleList({ embedded = false }: RoleListProps) {
  const { can } = usePermission();
  const { t } = useI18n();
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editing, setEditing] = useState<RoleDetail | null>(null);
  const [detail, setDetail] = useState<RoleDetail | null>(null);
  const [permissions, setPermissions] = useState<PermissionRow[]>([]);
  const [permissionsLoading, setPermissionsLoading] = useState(false);
  const [permissionsLoaded, setPermissionsLoaded] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [form] = Form.useForm<RoleFormValues>();

  const fetchRoles = (query: ListQuery): Promise<Paginated<RoleDetail>> =>
    client.get<Paginated<RoleDetail>>(endpoints.roles, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  // Permissions are paginated by the API (capped at 100 rows), so load every
  // page once and cache them for the role editor's multi-select.
  const loadPermissions = async () => {
    if (permissionsLoaded || permissionsLoading) return;
    setPermissionsLoading(true);
    try {
      const first = await client.get<Paginated<PermissionRow>>(endpoints.permissions, {
        page: 1,
        page_size: PERMISSION_PAGE_SIZE,
      });
      const total = first.count ?? first.result.length;
      const pages = Math.max(1, Math.ceil(total / PERMISSION_PAGE_SIZE));
      const rest = await Promise.all(
        Array.from({ length: pages - 1 }, (_, index) =>
          client.get<Paginated<PermissionRow>>(endpoints.permissions, {
            page: index + 2,
            page_size: PERMISSION_PAGE_SIZE,
          }),
        ),
      );
      setPermissions([first, ...rest].flatMap(page => page.result));
      setPermissionsLoaded(true);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setPermissionsLoading(false);
    }
  };

  const permissionOptions = useMemo(
    () =>
      permissions.map(permission => ({
        value: permission.id,
        label: `${permission.view_menu} / ${permission.permission}`,
      })),
    [permissions],
  );

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ permission_view_ids: [] });
    setModalOpen(true);
    void loadPermissions();
  };

  const openEdit = (role: RoleDetail) => {
    setEditing(role);
    form.resetFields();
    form.setFieldsValue({
      name: role.name,
      permission_view_ids: (role.permissions ?? [])
        .map(permission => permission.permission_view_id)
        .filter((id): id is number => typeof id === 'number'),
    });
    setModalOpen(true);
    void loadPermissions();
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);
      const payload = {
        name: values.name,
        permission_view_ids: values.permission_view_ids ?? [],
      };
      if (editing) {
        await client.put(endpoints.role(editing.id), payload);
        message.success(t('settings.role.updated_msg'));
      } else {
        await client.post(endpoints.roles, payload);
        message.success(t('settings.role.created_msg'));
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

  const handleDelete = async (role: RoleDetail) => {
    try {
      await client.delete(endpoints.role(role.id));
      message.success(t('settings.role.deleted', { name: role.name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<RoleDetail>>(
    () => [
      {
        title: t('settings.role.name'),
        dataIndex: 'name',
        key: 'name',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('settings.role.user_count'),
        dataIndex: 'user_count',
        key: 'user_count',
        width: 100,
        render: (value?: number) => value ?? 0,
      },
      {
        title: t('common.label.permission'),
        key: 'permissions',
        render: (_, record) => {
          const permissions = record.permissions ?? [];
          const preview = permissions.slice(0, 3);
          return (
            <Space size={4} wrap>
              {preview.map(permission => (
                <Tag key={`${permission.id}-${permission.name}`} bordered={false}>
                  {permission.name} / {permission.view_menu_name ?? '—'}
                </Tag>
              ))}
              {permissions.length > preview.length ? (
                <span className="cell-muted">+{permissions.length - preview.length}</span>
              ) : null}
              {!permissions.length ? <span className="cell-muted">{t('common.state.none')}</span> : null}
            </Space>
          );
        },
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
              icon={<EyeOutlined />}
              onClick={() => setDetail(record)}
            />
            {can('can_write', 'Role') ? (
              <Button
                size="small"
                type="text"
                icon={<EditOutlined />}
                onClick={() => openEdit(record)}
              />
            ) : null}
            {can('can_delete', 'Role') && !record.is_builtin ? (
              <Popconfirm
                title={t('settings.role.delete_title')}
                description={t('settings.role.delete_confirm', { name: record.name })}
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
      <EntityList<RoleDetail>
        title={embedded ? undefined : t('nav.roles')}
        subtitle={embedded ? undefined : t('settings.role.subtitle')}
        persistKey="settings-roles"
        columns={columns}
        fetchData={fetchRoles}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('settings.role.search_placeholder')}
        extra={
          can('can_write', 'Role') ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {t('settings.role.create')}
            </Button>
          ) : null
        }
      />

      <Modal
        title={editing ? t('settings.role.edit') : t('settings.role.create')}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => void handleSubmit()}
        okText={editing ? t('common.actions.save') : t('common.actions.create')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
        forceRender
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="name"
            label={t('settings.role.name')}
            rules={[{ required: true, message: t('settings.role.name_required') }]}
          >
            <Input placeholder={t('settings.role.name_placeholder')} disabled={editing?.is_builtin} />
          </Form.Item>
          <Form.Item name="permission_view_ids" label={t('settings.role.permissions')}>
            <Select
              mode="multiple"
              allowClear
              showSearch
              optionFilterProp="label"
              loading={permissionsLoading}
              placeholder={t('settings.role.permissions_placeholder')}
              options={permissionOptions}
              maxTagCount="responsive"
              style={{ width: '100%' }}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Drawer
        title={
          detail
            ? t('settings.role.detail_title_with_name', { name: detail.name })
            : t('settings.role.detail_title')
        }
        width={620}
        open={detail !== null}
        onClose={() => setDetail(null)}
      >
        <div className="cell-muted" style={{ marginBottom: 12 }}>
          {t('settings.role.detail_summary', {
            permissions: detail?.permissions?.length ?? 0,
            users: detail?.user_count ?? 0,
          })}
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
          {(detail?.permissions ?? []).map(permission => (
            <Tag key={`${permission.id}-${permission.view_menu_id}`} bordered={false}>
              {permission.name} on {permission.view_menu_name ?? '—'}
            </Tag>
          ))}
          {detail && !detail.permissions?.length ? (
            <span className="cell-muted">{t('settings.role.no_permissions')}</span>
          ) : null}
        </div>
      </Drawer>
    </>
  );
}
