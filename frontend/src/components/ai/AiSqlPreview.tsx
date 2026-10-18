import { useState } from 'react';
import { Button, Modal, Space, message } from 'antd';
import { CopyOutlined, EyeOutlined } from '@ant-design/icons';
import CodeEditor from '../common/CodeEditor';
import { useI18n } from '../../i18n';

interface Props {
  sql: string;
}

/**
 * Read-only SQL preview: the Agent's candidate statement is shown in the same
 * Monaco setup SQL Lab uses, so the user can eyeball the口径 before trusting it.
 */
export default function AiSqlPreview({ sql }: Props) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);

  if (!sql) return null;

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(sql);
      message.success(t('ai.actions.copied'));
    } catch {
      message.error(t('ai.actions.copy_failed'));
    }
  };

  return (
    <>
      <Space size={6}>
        <Button size="small" icon={<EyeOutlined />} onClick={() => setOpen(true)}>
          {t('ai.actions.preview_sql')}
        </Button>
        <Button size="small" icon={<CopyOutlined />} onClick={copy}>
          {t('ai.actions.copy')}
        </Button>
      </Space>
      <Modal
        open={open}
        title={t('ai.result.sql')}
        width={720}
        footer={null}
        onCancel={() => setOpen(false)}
      >
        <CodeEditor value={sql} onChange={() => undefined} height={360} readOnly />
      </Modal>
    </>
  );
}
