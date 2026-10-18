import { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Tag,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Paginated, Role, UserEntity } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

interface UserFormValues {
  username: string;
  first_name?: string;
  last_name?: string;
  email: string;
  password?: string;
  active: boolean;
  roles: number[];
}

interface UserListProps {
  /** When rendered inside the merged 用户与角色 tabs, drop the page header. */
  embedded?: boolean;
}

export default function UserList({ embedded = false }: UserListProps) {
  const { can } = usePermission();
  const { t } = useI18n();
  const [roles, setRoles] = useState<Role[]>([]);
  const [editing, setEditing] = useState<UserEntity | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [form] = Form.useForm<UserFormValues>();

  useEffect(() => {
    client
      .get<Paginated<Role>>(endpoints.roles, { page: 1, page_size: 100 })
      .then(response => setRoles(response.result ?? []))
      .catch(error => message.error(errorMessage(error)));
  }, []);

  const fetchUsers = (query: ListQuery): Promise<Paginated<UserEntity>> =>
    client.get<Paginated<UserEntity>>(endpoints.users, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ active: true, roles: [] });
    setModalOpen(true);
  };

  const openEdit = (user: UserEntity) => {
    setEditing(user);
    form.resetFields();
    form.setFieldsValue({
      username: user.username,
      first_name: user.first_name ?? '',
      last_name: user.last_name ?? '',
      email: user.email ?? '',
      active: user.active,
      roles: (user.roles ?? []).map(role => role.id),
    });
    setModalOpen(true);
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);
      if (editing) {
        await client.put(endpoints.user(editing.id), {
          ...values,
          password: values.password || undefined,
        });
        message.success(t('settings.user.updated'));
      } else {
        await client.post(endpoints.users, values);
        message.success(t('settings.user.created'));
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

  const handleDelete = async (user: UserEntity) => {
    try {
      await client.delete(endpoints.user(user.id));
      message.success(t('settings.user.deleted', { name: user.username }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<UserEntity>>(
    () => [
      {
        title: t('common.label.username'),
        dataIndex: 'username',
        key: 'username',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('settings.user.full_name'),
        key: 'name',
        render: (_, record) =>
          [record.first_name, record.last_name].filter(Boolean).join(' ') || '—',
      },
      {
        title: t('common.label.email'),
        dataIndex: 'email',
        key: 'email',
        render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
      },
      {
        title: t('common.label.role'),
        key: 'roles',
        render: (_, record) => (
          <Space size={4} wrap>
            {(record.roles ?? []).map(role => (
              <Tag key={role.id}>{role.name}</Tag>
            ))}
          </Space>
        ),
      },
      {
        title: t('common.label.status'),
        dataIndex: 'active',
        key: 'active',
        width: 90,
        render: (active: boolean) =>
          active ? (
            <Tag color="green">{t('settings.user.active')}</Tag>
          ) : (
            <Tag color="red">{t('settings.user.inactive')}</Tag>
          ),
      },
      {
        title: t('settings.user.last_login'),
        dataIndex: 'last_login',
        key: 'last_login',
        width: 160,
        render: (value?: string) => (
          <span className="cell-muted">{formatDateTimeShort(value)}</span>
        ),
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 100,
        render: (_, record) => (
          <Space size={2}>
            {can('can_write', 'User') ? (
              <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEdit(record)} />
            ) : null}
            {can('can_delete', 'User') ? (
              <Popconfirm
                title={t('settings.user.delete_title')}
                description={t('settings.user.delete_confirm', { name: record.username })}
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
      <EntityList<UserEntity>
        title={embedded ? undefined : t('nav.users')}
        subtitle={embedded ? undefined : t('settings.user.subtitle')}
        persistKey="settings-users"
        columns={columns}
        fetchData={fetchUsers}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('settings.user.search_placeholder')}
        extra={
          can('can_write', 'User') ? (
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {t('settings.user.create')}
            </Button>
          ) : null
        }
      />

      <Modal
        title={editing ? t('settings.user.edit') : t('settings.user.create')}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => void handleSubmit()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
        forceRender
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="username"
            label={t('common.label.username')}
            rules={[{ required: true, message: t('settings.user.username_required') }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <Form.Item name="first_name" label={t('settings.user.first_name')}>
              <Input />
            </Form.Item>
            <Form.Item name="last_name" label={t('settings.user.last_name')}>
              <Input />
            </Form.Item>
          </div>
          <Form.Item
            name="email"
            label={t('common.label.email')}
            rules={[
              { required: true, message: t('settings.user.email_required') },
              { type: 'email', message: t('settings.user.email_invalid') },
            ]}
          >
            <Input />
          </Form.Item>
          <Form.Item
            name="password"
            label={editing ? t('settings.user.password_placeholder') : t('common.label.password')}
            rules={
              editing ? [] : [{ required: true, message: t('settings.user.password_required') }]
            }
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item name="roles" label={t('common.label.role')}>
            <Select
              mode="multiple"
              placeholder={t('settings.user.roles_placeholder')}
              options={roles.map(role => ({ value: role.id, label: role.name }))}
            />
          </Form.Item>
          <Form.Item name="active" label={t('settings.user.active_account')} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
