/**
 * Sharing dialog for a wall: public link, password, expiry, guest export and
 * the iframe snippet used to embed it in a third-party portal.
 */
import { useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Divider,
  Input,
  InputNumber,
  Modal,
  Space,
  Switch,
  Typography,
  message,
} from 'antd';
import { CopyOutlined, LinkOutlined } from '@ant-design/icons';
import type { EmbedInfo, ScreenSummary } from '../../api/screens';
import { getScreenEmbed, publishScreen, unpublishScreen } from '../../api/screens';
import { errorMessage } from '../../api/client';
import { useI18n } from '../../i18n';

export interface ScreenShareModalProps {
  open: boolean;
  screen: ScreenSummary | null;
  onClose: () => void;
  onChanged?: () => void;
}

export default function ScreenShareModal({
  open,
  screen,
  onClose,
  onChanged,
}: ScreenShareModalProps) {
  const { t } = useI18n();
  const [enabled, setEnabled] = useState(false);
  const [password, setPassword] = useState('');
  const [expires, setExpires] = useState(0);
  const [allowExport, setAllowExport] = useState(true);
  const [saving, setSaving] = useState(false);
  const [embed, setEmbed] = useState<EmbedInfo | null>(null);

  useEffect(() => {
    if (!open || !screen) return;
    setEnabled(screen.public_enabled);
    setPassword('');
    setAllowExport(screen.allow_export);
    setExpires(0);
    setEmbed(null);
    if (screen.public_enabled) {
      void getScreenEmbed(screen.id)
        .then(setEmbed)
        .catch(() => setEmbed(null));
    }
  }, [open, screen]);

  const absoluteUrl = (path: string) =>
    path.startsWith('http') ? path : `${window.location.origin}${path}`;

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      message.success(t('screen.share.copied'));
    } catch {
      message.error(t('screen.share.copy_failed'));
    }
  };

  const save = async () => {
    if (!screen) return;
    setSaving(true);
    try {
      if (!enabled) {
        await unpublishScreen(screen.id);
        message.success(t('screen.share.disabled'));
      } else {
        const result = await publishScreen(screen.id, {
          enabled: true,
          password: password || undefined,
          expires_in_hours: expires,
          allow_export: allowExport,
        });
        setEmbed({
          url: absoluteUrl(result.url),
          iframe: `<iframe src="${absoluteUrl(result.url)}?embed=1" width="1920" height="1080" frameborder="0" allowfullscreen style="border:0;width:100%;height:100%"></iframe>`,
          expires_at: result.result.public_expires_at,
          has_password: result.result.has_password,
        });
        message.success(t('screen.share.saved'));
      }
      onChanged?.();
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={t('screen.share.title')}
      onCancel={onClose}
      footer={[
        <Button key="close" onClick={onClose}>
          {t('common.actions.close')}
        </Button>,
        <Button key="save" type="primary" loading={saving} onClick={() => void save()}>
          {t('common.actions.save')}
        </Button>,
      ]}
      width={640}
      destroyOnClose
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <div className="scr-share__row">
          <span>{t('screen.share.enable')}</span>
          <Switch checked={enabled} onChange={setEnabled} />
        </div>
        {enabled && (
          <>
            <div className="scr-share__row">
              <span>{t('screen.share.password')}</span>
              <Input.Password
                placeholder={t('screen.share.password_hint')}
                value={password}
                onChange={e => setPassword(e.target.value)}
                style={{ maxWidth: 260 }}
              />
            </div>
            <div className="scr-share__row">
              <span>{t('screen.share.expires')}</span>
              <InputNumber
                min={0}
                max={24 * 365}
                value={expires}
                onChange={value => setExpires(value ?? 0)}
                addonAfter={t('screen.share.hours')}
                style={{ width: 180 }}
              />
            </div>
            <div className="scr-share__row">
              <span>{t('screen.share.allow_export')}</span>
              <Checkbox checked={allowExport} onChange={e => setAllowExport(e.target.checked)}>
                {t('screen.share.allow_export_hint')}
              </Checkbox>
            </div>
          </>
        )}

        {embed && (
          <>
            <Divider plain>{t('screen.share.link')}</Divider>
            <Input
              readOnly
              value={embed.url}
              prefix={<LinkOutlined />}
              addonAfter={
                <Button type="text" size="small" icon={<CopyOutlined />} onClick={() => void copy(embed.url)}>
                  {t('screen.share.copy')}
                </Button>
              }
            />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {t('screen.share.iframe_hint')}
            </Typography.Text>
            <Input.TextArea readOnly autoSize={{ minRows: 3, maxRows: 5 }} value={embed.iframe} />
            <Button icon={<CopyOutlined />} onClick={() => void copy(embed.iframe)}>
              {t('screen.share.copy_iframe')}
            </Button>
            {embed.has_password && (
              <Alert type="info" showIcon message={t('screen.share.password_tip')} />
            )}
          </>
        )}

        <Alert type="warning" showIcon message={t('screen.share.security_note')} />
      </Space>
    </Modal>
  );
}
