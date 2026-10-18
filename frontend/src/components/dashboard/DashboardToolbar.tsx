import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
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
  Tooltip,
  Typography,
  message,
} from 'antd';
import {
  CompressOutlined,
  CopyOutlined,
  DeleteOutlined,
  EditOutlined,
  ExpandOutlined,
  EyeInvisibleOutlined,
  PlusOutlined,
  ReloadOutlined,
  SaveOutlined,
  ShareAltOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Dashboard } from '../../api/types';
import PageHeader from '../common/PageHeader';
import DashboardShareModal from './DashboardShareModal';
import { useI18n } from '../../i18n';

export interface DashboardToolbarProps {
  canEdit: boolean;
  editMode: boolean;
  dirty: boolean;
  saving: boolean;
  refreshing: boolean;
  autoRefreshMs: number;
  fullscreen: boolean;
  filterCount: number;
  /** HH:mm of the last successful layout save (null while dirty/never saved). */
  savedAt?: string | null;
  onToggleEdit: (value: boolean) => void;
  onSave: () => void;
  onRefresh: () => void;
  onAutoRefreshChange: (ms: number) => void;
  onToggleFullscreen: () => void;
  onAddChart: () => void;
  onAddRow: () => void;
  onAddFilter: () => void;
  /** Security watermark preference (persisted per browser). */
  watermark?: boolean;
  onToggleWatermark?: () => void;
  /** Chart-to-chart linkage preference. */
  linked?: boolean;
  onToggleLinked?: () => void;
}

/** Dashboard action bar: refresh, auto-refresh, edit mode, save, layout tools. */
export default function DashboardToolbar({
  canEdit,
  editMode,
  dirty,
  saving,
  refreshing,
  autoRefreshMs,
  fullscreen,
  filterCount,
  savedAt = null,
  onToggleEdit,
  onSave,
  onRefresh,
  onAutoRefreshChange,
  onToggleFullscreen,
  onAddChart,
  onAddRow,
  onAddFilter,
  watermark = false,
  onToggleWatermark,
  linked = true,
  onToggleLinked,
}: DashboardToolbarProps) {
  const { t } = useI18n();
  const refreshOptions = [
    { value: 0, label: t('dashboard.toolbar.auto_refresh_off') },
    { value: 10000, label: t('dashboard.toolbar.interval.10s') },
    { value: 30000, label: t('dashboard.toolbar.interval.30s') },
    { value: 60000, label: t('dashboard.toolbar.interval.1m') },
    { value: 300000, label: t('dashboard.toolbar.interval.5m') },
  ];
  return (
    <div className="dashboard-toolbar">
      <Space size={8} wrap>
        {canEdit ? (
          <Space size={6}>
            <Switch
              checked={editMode}
              onChange={onToggleEdit}
              checkedChildren={t('common.actions.edit')}
              unCheckedChildren={t('dashboard.toolbar.view')}
            />
            {editMode ? (
              <Button
                type="primary"
                icon={<SaveOutlined />}
                loading={saving}
                disabled={!dirty}
                onClick={onSave}
              >
                {t('common.actions.save')}
              </Button>
            ) : null}
            {editMode && dirty ? (
              <Tag color="orange">{t('dashboard.toolbar.unsaved')}</Tag>
            ) : null}
            {!dirty && savedAt ? (
              <span className="cell-muted">{t('dashboard.toolbar.saved_at', { time: savedAt })}</span>
            ) : null}
          </Space>
        ) : null}

        {editMode ? (
          <Space size={4}>
            <Button icon={<PlusOutlined />} onClick={onAddChart} size="small">
              {t('dashboard.add_chart.title')}
            </Button>
            <Button icon={<PlusOutlined />} onClick={onAddRow} size="small">
              {t('dashboard.toolbar.add_row')}
            </Button>
            <Button
              icon={<PlusOutlined />}
              onClick={onAddFilter}
              size="small"
              disabled={!canEdit}
            >
              {t('dashboard.toolbar.add_filter')}
            </Button>
          </Space>
        ) : null}
      </Space>

      <Space size={8} wrap>
        <span className="cell-muted">{t('dashboard.toolbar.filter_count', { count: filterCount })}</span>
        <Button
          icon={<ReloadOutlined />}
          loading={refreshing}
          onClick={onRefresh}
          size="small"
        >
          {t('common.actions.refresh')}
        </Button>
        <Select
          size="small"
          value={autoRefreshMs}
          options={refreshOptions}
          onChange={onAutoRefreshChange}
          style={{ width: 132 }}
        />
        <Tooltip title={fullscreen ? t('layout.fullscreen.exit') : t('layout.fullscreen.enter')}>
          <Button
            size="small"
            icon={fullscreen ? <CompressOutlined /> : <ExpandOutlined />}
            onClick={onToggleFullscreen}
          />
        </Tooltip>
        {onToggleWatermark ? (
          <Tooltip title={watermark ? t('dashboard.watermark.hide') : t('dashboard.watermark.show')}>
            <Button
              size="small"
              type={watermark ? 'primary' : 'default'}
              ghost={watermark}
              icon={<EyeInvisibleOutlined />}
              onClick={onToggleWatermark}
              aria-pressed={watermark}
            />
          </Tooltip>
        ) : null}
        {onToggleLinked ? (
          <Tooltip title={linked ? t('dashboard.linkage.disable') : t('dashboard.linkage.enable')}>
            <Button
              size="small"
              type={linked ? 'primary' : 'default'}
              ghost={linked}
              icon={<ShareAltOutlined />}
              onClick={onToggleLinked}
              aria-pressed={linked}
            />
          </Tooltip>
        ) : null}
      </Space>
    </div>
  );
}

/* ------------------------------------------------------------------------ */

export interface DashboardHeaderProps {
  dashboard: Dashboard;
  canWrite: boolean;
  canCopy: boolean;
  canDelete: boolean;
  /** Sharing needs can_write (the backend enforces the same rule). */
  canShare?: boolean;
  onUpdated: (dashboard: Dashboard) => void;
  onDeleted: () => void;
}

interface MetaFormValues {
  dashboard_title: string;
  slug: string;
  description: string;
  published: boolean;
}

/**
 * Dashboard title block: metadata, owners and the object-level actions
 * (edit metadata, copy, delete). Kept next to the toolbar so the page file
 * only orchestrates data.
 */
export function DashboardHeader({
  dashboard,
  canWrite,
  canCopy,
  canDelete,
  canShare = false,
  onUpdated,
  onDeleted,
}: DashboardHeaderProps) {
  const navigate = useNavigate();
  const { t } = useI18n();
  const [shareOpen, setShareOpen] = useState(false);
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<MetaFormValues>();

  const ownerText =
    (dashboard.owners ?? [])
      .map(
        owner =>
          owner.username ||
          [owner.first_name, owner.last_name].filter(Boolean).join(' ') ||
          owner.name,
      )
      .filter(Boolean)
      .join(', ') || '—';

  const openEdit = () => {
    form.setFieldsValue({
      dashboard_title: dashboard.dashboard_title,
      slug: dashboard.slug ?? '',
      description: dashboard.description ?? '',
      published: dashboard.published,
    });
    setOpen(true);
  };

  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      setSaving(true);
      const response = await client.put<{ result: Dashboard }>(
        endpoints.dashboard(dashboard.id),
        values,
      );
      onUpdated(response.result ?? { ...dashboard, ...values });
      message.success(t('dashboard.toolbar.updated'));
      setOpen(false);
    } catch (err) {
      if (err && typeof err === 'object' && 'errorFields' in err) return;
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleCopy = async () => {
    try {
      const response = await client.post<{ result: Dashboard }>(
        endpoints.dashboardCopy(dashboard.id),
        {},
      );
      message.success(t('dashboard.toolbar.copied'));
      if (response.result?.id) navigate(`/dashboard/${response.result.id}/`);
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  const handleDelete = async () => {
    try {
      await client.delete(endpoints.dashboard(dashboard.id));
      message.success(t('dashboard.list.deleted_msg'));
      onDeleted();
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  return (
    <>
      <PageHeader
        title={dashboard.dashboard_title}
        subtitle={
          <Space size={8} wrap>
            <Tag color={dashboard.published ? 'green' : 'default'}>
              {dashboard.published ? t('common.state.published') : t('common.state.draft')}
            </Tag>
            <span>{t('common.label.owner')}：{ownerText}</span>
            {dashboard.changed_on ? (
              <span>{t('common.label.changed_on')}：{dayjs(dashboard.changed_on).format('YYYY-MM-DD HH:mm')}</span>
            ) : null}
            {dashboard.slug ? <span className="mono">/{dashboard.slug}</span> : null}
          </Space>
        }
        extra={
          <Space>
            {canShare ? (
              <Button icon={<ShareAltOutlined />} onClick={() => setShareOpen(true)}>
                {t('dashboard.share.action')}
              </Button>
            ) : null}
            {canWrite ? (
              <Button icon={<EditOutlined />} onClick={openEdit}>
                {t('common.actions.edit')}
              </Button>
            ) : null}
            {canCopy ? (
              <Button icon={<CopyOutlined />} onClick={() => void handleCopy()}>
                {t('dashboard.toolbar.duplicate')}
              </Button>
            ) : null}
            {canDelete ? (
              <Popconfirm
                title={t('common.actions.delete')}
                okText={t('common.actions.delete')}
                cancelText={t('common.actions.cancel')}
                okButtonProps={{ danger: true }}
                onConfirm={() => void handleDelete()}
              >
                <Button danger icon={<DeleteOutlined />}>
                  {t('common.actions.delete')}
                </Button>
              </Popconfirm>
            ) : null}
          </Space>
        }
      />

      {dashboard.description ? (
        <Typography.Paragraph type="secondary" style={{ marginBottom: 16 }}>
          {dashboard.description}
        </Typography.Paragraph>
      ) : null}

      <DashboardShareModal
        open={shareOpen}
        dashboardId={dashboard.id}
        dashboardTitle={dashboard.dashboard_title}
        onClose={() => setShareOpen(false)}
      />

      <Modal
        title={t('dashboard.toolbar.edit')}
        open={open}
        onCancel={() => setOpen(false)}
        onOk={() => void handleSave()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="dashboard_title"
            label={t('dashboard.form.title')}
            rules={[{ required: true, message: t('dashboard.toolbar.title_required') }]}
          >
            <Input maxLength={120} />
          </Form.Item>
          <Form.Item name="slug" label="Slug">
            <Input placeholder="sales-overview" />
          </Form.Item>
          <Form.Item name="description" label={t('dashboard.form.description')}>
            <Input.TextArea rows={3} maxLength={400} />
          </Form.Item>
          <Form.Item name="published" label={t('dashboard.form.published')} valuePropName="checked">
            <Switch checkedChildren={t('common.state.published')} unCheckedChildren={t('common.state.draft')} />
          </Form.Item>
        </Form>
      </Modal>
    </>
  );
}
