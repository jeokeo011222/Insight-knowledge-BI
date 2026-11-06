import { Space, Tag, Typography } from 'antd';
import { useI18n } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;

interface Props {
  streaming: boolean;
  aborted: boolean;
  stepCount: number;
}

/** Three bouncing dots — a lightweight, dark-mode friendly typing indicator. */
function TypingDots() {
  return (
    <span className="zg-typing" aria-hidden="true" data-testid="ai-typing">
      <span className="zg-typing__dot" />
      <span className="zg-typing__dot" />
      <span className="zg-typing__dot" />
    </span>
  );
}

/** Status line shown above the answer bubble while the Agent is working. */
export default function AiStreamingText({ streaming, aborted, stepCount }: Props) {
  const { t } = useI18n();

  if (streaming) {
    return (
      <Space size={8} className={styles.metaRow}>
        <TypingDots />
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
