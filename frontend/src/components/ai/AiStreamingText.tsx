import { Space, Spin, Tag, Typography } from 'antd';
import { useI18n } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;

interface Props {
  streaming: boolean;
  aborted: boolean;
  stepCount: number;
}

/** Compact status line shown above the timeline while the Agent is running. */
export default function AiStreamingText({ streaming, aborted, stepCount }: Props) {
  const { t } = useI18n();

  if (streaming) {
    return (
      <Space size={8} className={styles.metaRow}>
        <Spin size="small" />
        <Text>{t('ai.streaming.thinking')}</Text>
        {stepCount > 0 && <Tag>{t('ai.streaming.step', { count: stepCount })}</Tag>}
      </Space>
    );
  }

  if (aborted) {
    return <Tag color="default">{t('ai.status.aborted')}</Tag>;
  }

  if (stepCount > 0) {
    return (
      <Text className={styles.hint}>{t('ai.streaming.done', { count: stepCount })}</Text>
    );
  }

  return null;
}
