import { useMemo, useRef, useState } from 'react';
import { Button, Dropdown, Form, Input, Modal, Popconfirm, Space, Switch, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { MenuProps } from 'antd';
import {
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EyeFilled,
  MoonFilled,
  PlusOutlined,
  StopOutlined,
  SunFilled,
  UploadOutlined,
} from '@ant-design/icons';
import { client, downloadBlob, endpoints, errorMessage } from '../../api/client';
import type { JsonObject, Paginated, Theme } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { SYSTEM_THEME_EVENT } from '../../theme';
import { formatDateTimeShort } from '../../utils/format';

interface ThemeFormValues {
  theme_name: string;
  json_data?: string;
  is_default: boolean;
  is_dark: boolean;
}

const DEFAULT_JSON = `{
  "token": {
    "colorPrimary": "#1b5fd9",
    "borderRadius": 4
  }
}`;

interface ThemeListProps {
  /** When rendered inside the 系统管理 tabs, drop the page header. */
  embedded?: boolean;
}

export default function ThemeList({ embedded = false }: ThemeListProps) {
  const { can, isAdmin } = usePermission();
  const { t } = useI18n();
  const [editing, setEditing] = useState<Theme | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const importInputRef = useRef<HTMLInputElement>(null);
  const [form] = Form.useForm<ThemeFormValues>();

  const fetchThemes = (query: ListQuery): Promise<Paginated<Theme>> =>
    client.get<Paginated<Theme>>(endpoints.themes, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
    });

  const openCreate = () => {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue({ is_default: false, is_dark: false, json_data: DEFAULT_JSON });
    setModalOpen(true);
  };

  const openEdit = (theme: Theme) => {
    setEditing(theme);
    form.resetFields();
    form.setFieldsValue({
      theme_name: theme.theme_name,
      json_data: theme.json_data ? JSON.stringify(theme.json_data, null, 2) : DEFAULT_JSON,
      is_default: theme.is_default,
      is_dark: Boolean(theme.is_system_dark),
    });
    setModalOpen(true);
  };

  const handleSubmit = async () => {
    try {
      const values = await form.validateFields();
      let jsonData: JsonObject | undefined;
      if (values.json_data?.trim()) {
        try {
          jsonData = JSON.parse(values.json_data) as JsonObject;
        } catch {
          message.error(t('settings.theme.json_invalid'));
          return;
        }
      }
      setSaving(true);
      // System slots are admin-only and move atomically through the
      // set/unset_system_* endpoints, so they are kept out of the CRUD payload.
      const payload = {
        theme_name: values.theme_name,
        json_data: jsonData,
      };
      let savedId: number;
      if (editing) {
        await client.put(endpoints.theme(editing.id), payload);
        savedId = editing.id;
        message.success(t('settings.theme.updated'));
      } else {
        const created = await client.post<{ result: Theme }>(endpoints.themes, payload);
        savedId = created.result.id;
        message.success(t('settings.theme.created_msg'));
      }

      let slotChanged = false;
      if (isAdmin) {
        const wasDefault = Boolean(editing?.is_system_default);
        if (values.is_default !== wasDefault) {
          if (values.is_default) await client.put(`/theme/${savedId}/set_system_default`, {});
          else await client.delete('/theme/unset_system_default');
          slotChanged = true;
        }
        const wasDark = Boolean(editing?.is_system_dark);
        if (values.is_dark !== wasDark) {
          if (values.is_dark) await client.put(`/theme/${savedId}/set_system_dark`, {});
          else await client.delete('/theme/unset_system_dark');
          slotChanged = true;
        }
      }

      setModalOpen(false);
      setReloadToken(value => value + 1);
      if (slotChanged) notifySystemChange();
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (theme: Theme) => {
    try {
      await client.delete(endpoints.theme(theme.id));
      message.success(t('settings.theme.deleted', { name: theme.theme_name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  // System theme slots (superset: set_system_default / set_system_dark and
  // their unset counterparts). After a change the page reloads and the app
  // re-fetches GET /theme/system so the new theme applies live.
  const notifySystemChange = () => window.dispatchEvent(new Event(SYSTEM_THEME_EVENT));

  const setSystemSlot = async (theme: Theme, slot: 'default' | 'dark') => {
    try {
      await client.put(`/theme/${theme.id}/set_system_${slot}`, {});
      message.success(t(`settings.theme.set_system_${slot}_ok`, { name: theme.theme_name }));
      setReloadToken(value => value + 1);
      notifySystemChange();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const unsetSystemSlot = async (slot: 'default' | 'dark') => {
    try {
      await client.delete(`/theme/unset_system_${slot}`);
      message.success(t(`settings.theme.unset_system_${slot}_ok`));
      setReloadToken(value => value + 1);
      notifySystemChange();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const handleExport = async () => {
    try {
      const data = await client.get<{ themes: unknown[] }>('/theme/export/');
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      await downloadBlob(blob, 'themes_export.json');
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const handleImportFile = async (file: File) => {
    try {
      const parsed = JSON.parse(await file.text()) as { themes?: unknown[] } | unknown[];
      const themes = Array.isArray(parsed) ? parsed : (parsed.themes ?? []);
      if (!Array.isArray(themes) || themes.length === 0) {
        message.error(t('settings.theme.import_empty'));
        return;
      }
      const res = await client.post<{ created?: number; updated?: number }>('/theme/import/', {
        themes,
        overwrite: true,
      });
      message.success(
        t('settings.theme.import_ok', {
          created: res.created ?? 0,
          updated: res.updated ?? 0,
        }),
      );
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  // Row-level system slot menu (admins only, mirroring the superset UI).
  const slotMenu = (record: Theme): MenuProps => ({
    items: [
      record.is_system_default
        ? {
            key: 'unset_default',
            icon: <StopOutlined />,
            label: t('settings.theme.unset_system_default'),
            onClick: () => void unsetSystemSlot('default'),
          }
        : {
            key: 'set_default',
            icon: <SunFilled />,
            label: t('settings.theme.set_system_default'),
            onClick: () => void setSystemSlot(record, 'default'),
          },
      record.is_system_dark
        ? {
            key: 'unset_dark',
            icon: <StopOutlined />,
            label: t('settings.theme.unset_system_dark'),
            onClick: () => void unsetSystemSlot('dark'),
          }
        : {
            key: 'set_dark',
            icon: <MoonFilled />,
            label: t('settings.theme.set_system_dark'),
            onClick: () => void setSystemSlot(record, 'dark'),
          },
    ],
  });

  const columns = useMemo<ColumnsType<Theme>>(
    () => [
      {
        title: t('settings.theme.name'),
        dataIndex: 'theme_name',
        key: 'theme_name',
        render: (value: string, record) => (
          <Space size={6}>
            <span style={{ fontWeight: 500 }}>{value}</span>
            {record.is_system ? <Tag color="blue">system</Tag> : null}
            {record.is_system_default ? (
              <Tag color="green" icon={<SunFilled />}>
                {t('settings.theme.system_default')}
              </Tag>
            ) : null}
            {record.is_system_dark ? (
              <Tag color="purple" icon={<MoonFilled />}>
                {t('settings.theme.system_dark')}
              </Tag>
            ) : null}
          </Space>
        ),
      },
      {
        title: t('settings.theme.is_default'),
        dataIndex: 'is_default',
        key: 'is_default',
        width: 90,
        render: (value: boolean) =>
          value ? <Tag color="green">{t('settings.theme.is_default')}</Tag> : <Tag>—</Tag>,
      },
      {
        title: t('settings.theme.is_dark'),
        dataIndex: 'is_system_dark',
        key: 'is_system_dark',
        width: 90,
        render: (value?: boolean) => (value ? <Tag color="purple">dark</Tag> : <Tag>light</Tag>),
      },
      {
        title: t('common.label.changed_on'),
        dataIndex: 'changed_on',
        key: 'changed_on',
        width: 170,
        render: (value?: string) => (
          <span className="cell-muted">{formatDateTimeShort(value)}</span>
        ),
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 140,
        render: (_, record) => (
          <Space size={2}>
            {can('can_write', 'Theme') ? (
              <Button size="small" type="text" icon={<EditOutlined />} onClick={() => openEdit(record)} />
            ) : null}
            {isAdmin && can('can_write', 'Theme') ? (
              <Dropdown menu={slotMenu(record)} trigger={['click']}>
                <Button size="small" type="text" icon={<EyeFilled />} />
              </Dropdown>
            ) : null}
            {can('can_delete', 'Theme') && !record.is_system ? (
              <Popconfirm
                title={t('settings.theme.delete_title')}
                description={t('settings.theme.delete_confirm', { name: record.theme_name })}
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
    [can, isAdmin, t],
  );

  return (
    <>
      <EntityList<Theme>
        title={embedded ? undefined : t('nav.themes')}
        subtitle={embedded ? undefined : t('settings.theme.subtitle')}
        persistKey="settings-themes"
        columns={columns}
        fetchData={fetchThemes}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('settings.theme.search_placeholder')}
        extra={
          <Space size={8}>
            {can('can_read', 'Theme') ? (
              <Button icon={<DownloadOutlined />} onClick={() => void handleExport()}>
                {t('settings.theme.export')}
              </Button>
            ) : null}
            {can('can_write', 'Theme') ? (
              <Button icon={<UploadOutlined />} onClick={() => importInputRef.current?.click()}>
                {t('settings.theme.import')}
              </Button>
            ) : null}
            {can('can_write', 'Theme') ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                {t('settings.theme.create')}
              </Button>
            ) : null}
            <input
              ref={importInputRef}
              type="file"
              accept=".json,application/json"
              style={{ display: 'none' }}
              onChange={event => {
                const file = event.target.files?.[0];
                event.target.value = '';
                if (file) void handleImportFile(file);
              }}
            />
          </Space>
        }
      />

      <Modal
        title={editing ? t('settings.theme.edit') : t('settings.theme.create')}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={() => void handleSubmit()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        width={640}
        confirmLoading={saving}
        destroyOnClose
      >
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="theme_name"
            label={t('settings.theme.name')}
            rules={[{ required: true, message: t('settings.theme.name_required') }]}
          >
            <Input placeholder={t('settings.theme.name_placeholder')} />
          </Form.Item>
          <Form.Item
            name="json_data"
            label={t('settings.theme.json_config')}
            tooltip={t('settings.theme.json_config_hint')}
          >
            <Input.TextArea rows={10} className="mono" />
          </Form.Item>
          {isAdmin ? (
            <Space size={24}>
              <Form.Item name="is_default" label={t('settings.theme.set_default')} valuePropName="checked">
                <Switch size="small" />
              </Form.Item>
              <Form.Item name="is_dark" label={t('settings.theme.dark_theme')} valuePropName="checked">
                <Switch size="small" />
              </Form.Item>
            </Space>
          ) : null}
        </Form>
      </Modal>
    </>
  );
}
