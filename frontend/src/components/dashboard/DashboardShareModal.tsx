import { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Divider,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Tag,
  Typography,
  message,
} from 'antd';
import { CopyOutlined, LinkOutlined, ReloadOutlined } from '@ant-design/icons';
import { errorMessage } from '../../api/client';
import {
  absoluteUrl,
  embedSnippet,
  getDashboardShare,
  revokeDashboardShare,
  saveDashboardShare,
} from '../../api/share';
import type { DashboardShare } from '../../api/share';
import { useI18n } from '../../i18n';

export interface DashboardShareModalProps {
  open: boolean;
  dashboardId: number;
  dashboardTitle: string;
  onClose: () => void;
}

const EXPIRY_OPTIONS = [
  { value: 0, labelKey: 'dashboard.share.expiry.never' },
  { value: 24, labelKey: 'dashboard.share.expiry.1d' },
  { value: 168, labelKey: 'dashboard.share.expiry.7d' },
  { value: 720, labelKey: 'dashboard.share.expiry.30d' },
] as const;

/**
 * Public link manager for one dashboard.
 *
 * Mirrors the screen-share dialog: enable, optional password, optional expiry,
 * optional visitor exports. The link and the iframe snippet only appear while
 * the share is actually live, because the backend stops echoing the token once
 * it is revoked or expired.
 */
export default function DashboardShareModal({
  open,
  dashboardId,
  dashboardTitle,
  onClose,
}: DashboardShareModalProps) {
  const { t } = useI18n();
  const [share, setShare] = useState<DashboardShare | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [password, setPassword] = useState('');
  const [hasPassword, setHasPassword] = useState(false);
  const [allowExport, setAllowExport] = useState(true);
  const [expiryHours, setExpiryHours] = useState<number>(0);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setPassword('');
    getDashboardShare(dashboardId)
      .then(result => {
        if (cancelled) return;
        setShare(result);
        setEnabled(result.enabled);
        setHasPassword(result.has_password);
        setAllowExport(result.allow_export);
        setExpiryHours(
          result.expires_at
            ? Math.max(
                1,
                Math.round((new Date(result.expires_at).getTime() - Date.now()) / 3_600_000),
              )
            : 0,
        );
      })
      .catch(err => {
        if (!cancelled) message.error(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, dashboardId]);

  const applyResult = (result: DashboardShare) => {
    setShare(result);
    setEnabled(result.enabled);
    setHasPassword(result.has_password);
    setAllowExport(result.allow_export);
  };

  const handleSave = async (regenerate = false) => {
    setSaving(true);
    try {
      const result = await saveDashboardShare(dashboardId, {
        enabled,
        password: password || undefined,
        allow_export: allowExport,
        expires_in_hours: expiryHours,
        regenerate,
      });
      applyResult(result);
      setPassword('');
      message.success(t('dashboard.share.saved'));
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleClearPassword = async () => {
    setSaving(true);
    try {
      const result = await saveDashboardShare(dashboardId, { clear_password: true });
      applyResult(result);
      message.success(t('dashboard.share.password_cleared'));
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const handleRevoke = async () => {
    setSaving(true);
    try {
      const result = await revokeDashboardShare(dashboardId);
      applyResult(result);
      message.success(t('dashboard.share.revoked_msg'));
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      message.success(t('screen.share.copied'));
    } catch {
      message.warning(value);
    }
  };

  const liveUrl = share?.url ? absoluteUrl(share.url) : '';
  const snippet = share?.embed_url ? embedSnippet(share.embed_url) : '';

  return (
    <Modal
      title={t('dashboard.share.title', { name: dashboardTitle })}
      open={open}
      onCancel={onClose}
      width={640}
      footer={
        <Space>
          {share?.enabled ? (
            <Button danger onClick={() => void handleRevoke()} loading={saving}>
              {t('dashboard.share.revoke')}
            </Button>
          ) : null}
          <Button onClick={onClose}>{t('common.actions.close')}</Button>
          <Button type="primary" loading={saving} onClick={() => void handleSave(false)}>
            {t('common.actions.save')}
          </Button>
        </Space>
      }
    >
      {loading ? (
        <div style={{ padding: 40, textAlign: 'center' }}>
          <Spin />
        </div>
      ) : (
        <>
          <div className="share-dialog__row">
            <div>
              <div className="share-dialog__label">{t('dashboard.share.enable')}</div>
              <div className="share-dialog__desc">{t('dashboard.share.enable_desc')}</div>
            </div>
            <Switch checked={enabled} onChange={setEnabled} />
          </div>

          <div className="share-dialog__row">
            <div>
              <div className="share-dialog__label">{t('dashboard.share.password')}</div>
              <div className="share-dialog__desc">
                {hasPassword ? t('dashboard.share.password_set') : t('dashboard.share.password_desc')}
              </div>
            </div>
            <Space direction="vertical" size={4} style={{ alignItems: 'flex-end' }}>
              <Input.Password
                value={password}
                onChange={event => setPassword(event.target.value)}
                placeholder={hasPassword ? '••••••' : t('dashboard.share.password_placeholder')}
                style={{ width: 200 }}
                disabled={!enabled}
              />
              {hasPassword ? (
                <Button size="small" type="link" onClick={() => void handleClearPassword()}>
                  {t('dashboard.share.password_clear')}
                </Button>
              ) : null}
            </Space>
          </div>

          <div className="share-dialog__row">
            <div>
              <div className="share-dialog__label">{t('dashboard.share.expiry')}</div>
              <div className="share-dialog__desc">{t('dashboard.share.expiry_desc')}</div>
            </div>
            <Space>
              <Select
                value={expiryHours}
                onChange={setExpiryHours}
                disabled={!enabled}
                style={{ width: 160 }}
                options={EXPIRY_OPTIONS.map(option => ({
                  value: option.value,
                  label: t(option.labelKey as 'dashboard.share.expiry.never'),
                }))}
              />
              {expiryHours > 0 ? (
                <InputNumber
                  min={1}
                  max={8760}
                  value={expiryHours}
                  onChange={value => setExpiryHours(value ?? 0)}
                  addonAfter={t('dashboard.share.hours')}
                  style={{ width: 150 }}
                  disabled={!enabled}
                />
              ) : null}
            </Space>
          </div>

          <div className="share-dialog__row">
            <div>
              <div className="share-dialog__label">{t('dashboard.share.allow_export')}</div>
              <div className="share-dialog__desc">{t('dashboard.share.allow_export_desc')}</div>
            </div>
            <Switch checked={allowExport} onChange={setAllowExport} disabled={!enabled} />
          </div>

          {share?.enabled && liveUrl ? (
            <>
              <Divider style={{ margin: '12px 0' }} />
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('dashboard.share.link')}
              </Typography.Text>
              <div className="share-dialog__link" style={{ margin: '6px 0 12px' }}>
                <Input value={liveUrl} readOnly prefix={<LinkOutlined />} />
                <Button icon={<CopyOutlined />} onClick={() => void copy(liveUrl)}>
                  {t('common.actions.copy')}
                </Button>
                <Button
                  icon={<ReloadOutlined />}
                  onClick={() => void handleSave(true)}
                  title={t('dashboard.share.regenerate')}
                />
              </div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {t('dashboard.share.snippet')}
              </Typography.Text>
              <div className="share-dialog__snippet" style={{ marginTop: 6 }}>
                {snippet}
              </div>
              <Space size={8} style={{ marginTop: 10, flexWrap: 'wrap' }}>
                <Tag bordered={false}>
                  {t('dashboard.share.views', { count: share.view_count })}
                </Tag>
                {share.has_password ? (
                  <Tag color="orange">{t('dashboard.share.has_password')}</Tag>
                ) : (
                  <Tag color="red">{t('dashboard.share.no_password')}</Tag>
                )}
                {share.expires_at ? (
                  <Tag>{t('dashboard.share.expires_at', { at: share.expires_at.slice(0, 16).replace('T', ' ') })}</Tag>
                ) : null}
              </Space>
              {!share.has_password ? (
                <Alert
                  style={{ marginTop: 12 }}
                  type="warning"
                  showIcon
                  message={t('dashboard.share.public_warning')}
                />
              ) : null}
            </>
          ) : null}
        </>
      )}
    </Modal>
  );
}
