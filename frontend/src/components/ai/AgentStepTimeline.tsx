import { Collapse, Empty, Space, Tag, Timeline, Typography } from 'antd';
import type { ReactNode } from 'react';
import type { AgentStreamStep } from '../../store/aiSlice';
import { useI18n } from '../../i18n';
import type { TranslationKey } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;

const STATUS_KEYS: Record<AgentStreamStep['status'], TranslationKey> = {
  ok: 'ai.status.ok',
  running: 'ai.status.running',
  error: 'ai.status.error',
  denied: 'ai.status.denied',
};

const STATUS_COLORS: Record<AgentStreamStep['status'], string> = {
  ok: 'green',
  running: 'blue',
  error: 'red',
  denied: 'orange',
};

const DOT_COLORS: Record<AgentStreamStep['status'], string> = {
  ok: 'green',
  running: 'blue',
  error: 'red',
  denied: 'orange',
};

/** Renders a JSON payload in a scrollable <pre>, keeping strings readable. */
function jsonText(value: unknown): string {
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function hasPayload(value: unknown): boolean {
  if (value === undefined || value === null) return false;
  if (typeof value === 'string') return value.trim().length > 0;
  return true;
}

interface Props {
  steps: AgentStreamStep[];
}

/** plan→act→observe timeline with collapsible arguments and results. */
export default function AgentStepTimeline({ steps }: Props) {
  const { t } = useI18n();

  if (!steps.length) {
    return (
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={<Text className={styles.hint}>{t('ai.timeline.empty')}</Text>}
      />
    );
  }

  const items = steps.map((step, index) => {
    const panelItems: { key: string; label: string; children: ReactNode }[] = [];
    if (hasPayload(step.args)) {
      panelItems.push({
        key: 'args',
        label: t('ai.timeline.args'),
        children: <pre className={styles.json}>{jsonText(step.args)}</pre>,
      });
    }
    if (hasPayload(step.result) && step.kind !== 'plan') {
      panelItems.push({
        key: 'result',
        label: t('ai.timeline.result'),
        children: <pre className={styles.json}>{jsonText(step.result)}</pre>,
      });
    }

    return {
      key: `${step.kind}-${step.stepNo ?? 'plan'}-${index}`,
      color: DOT_COLORS[step.status],
      children: (
        <div className={styles.stepBody}>
          <Space size={6} wrap className={styles.metaRow}>
            <Tag>{step.kind === 'plan' ? t('ai.timeline.plan') : step.kind}</Tag>
            {step.kind !== 'plan' && <Text strong>{step.name}</Text>}
            <Tag color={STATUS_COLORS[step.status]}>{t(STATUS_KEYS[step.status])}</Tag>
          </Space>
          {step.kind === 'plan' && <Text>{jsonText(step.result ?? step.args)}</Text>}
          {step.error && <Text type="danger">{step.error}</Text>}
          {panelItems.length > 0 && (
            <Collapse size="small" ghost items={panelItems} />
          )}
        </div>
      ),
    };
  });

  return <Timeline items={items} />;
}
