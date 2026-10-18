import { Alert, Button, List, Popconfirm, Space, Tag, Typography } from 'antd';
import { RollbackOutlined } from '@ant-design/icons';
import type { AgentStreamStep, RollbackState } from '../../store/aiSlice';
import { useI18n } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;

/** Tool names that mutate data (mirrors the backend write tool registry). */
const WRITE_TOOLS = ['chart_writer', 'dashboard_writer', 'dashboard_compose_writer'];
/** `op` values the backend writers report back on a successful write. */
const WRITE_OPS = ['create', 'update', 'compose'];

interface WriteEntry {
  name: string;
  op: string;
  ids: string;
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/**
 * Locates the payload describing a write: tool results carry it at the top
 * level, while a skill (e.g. `dashboard_compose`) nests the tool output under
 * `output`.
 */
function writePayload(step: AgentStreamStep): Record<string, unknown> {
  const result = asRecord(step.result);
  const inner = asRecord(result.output);
  return Object.keys(inner).length ? inner : result;
}

/** Marks steps that mutated data — write tools, or skills wrapping one. */
function isWriteStep(step: AgentStreamStep): boolean {
  if (step.status !== 'ok') return false;
  if (step.kind === 'tool' && WRITE_TOOLS.includes(step.name)) return true;
  const payload = writePayload(step);
  return (
    WRITE_OPS.includes(String(payload.op)) ||
    payload.dashboard_id !== undefined ||
    payload.chart_ids !== undefined
  );
}

function describe(step: AgentStreamStep): WriteEntry {
  const payload = writePayload(step);
  const ids: string[] = [];
  ['chart_id', 'dashboard_id'].forEach(key => {
    if (payload[key] !== undefined) ids.push(String(payload[key]));
  });
  if (Array.isArray(payload.chart_ids)) {
    ids.push(`charts=[${payload.chart_ids.join(', ')}]`);
  }
  return {
    name: step.name,
    op: payload.op === undefined ? '' : String(payload.op),
    ids: ids.join(' · '),
  };
}

interface Props {
  runId: number | null;
  steps: AgentStreamStep[];
  canRollback: boolean;
  confirmRequired: boolean;
  rollback: RollbackState;
  onRollback: (runId: number) => void;
}

/**
 * Summarises the write operations a run performed and offers the reverse-order
 * rollback entry point backed by `POST /agent/runs/{id}/rollback`.
 */
export default function AiWriteConfirm({
  runId,
  steps,
  canRollback,
  confirmRequired,
  rollback,
  onRollback,
}: Props) {
  const { t } = useI18n();

  const entries = steps.filter(isWriteStep).map(describe);

  if (!entries.length) return null;

  return (
    <div className={styles.stack}>
      <Text strong>{t('ai.write.title')}</Text>
      <Text className={styles.hint}>{t('ai.write.desc')}</Text>
      <List
        size="small"
        dataSource={entries}
        renderItem={entry => (
          <List.Item>
            <Space size={6} wrap>
              <Tag color="volcano">{entry.name}</Tag>
              {entry.op && <Tag>{entry.op}</Tag>}
              {entry.ids && (
                <Text>
                  {t('ai.write.target_ids')}: {entry.ids}
                </Text>
              )}
            </Space>
          </List.Item>
        )}
      />
      {confirmRequired && <Alert type="warning" showIcon message={t('ai.write.confirm_required')} />}
      <Space size={8} wrap>
        <Popconfirm
          title={t('ai.write.rollback_confirm')}
          okText={t('ai.actions.rollback')}
          cancelText={t('ai.actions.cancel')}
          disabled={!runId || !canRollback || rollback.loading}
          onConfirm={() => runId && onRollback(runId)}
        >
          <Button
            danger
            size="small"
            icon={<RollbackOutlined />}
            loading={rollback.loading}
            disabled={!runId || !canRollback}
          >
            {t('ai.actions.rollback')}
          </Button>
        </Popconfirm>
        {!canRollback && <Text className={styles.hint}>{t('ai.status.denied')}</Text>}
      </Space>
      {rollback.error && <Alert type="error" showIcon message={rollback.error} />}
      {rollback.result && (
        <Alert
          type="success"
          showIcon
          message={t('ai.write.rollback_done', {
            rolled: rollback.result.rolled_back,
            skipped: rollback.result.skipped,
          })}
          description={
            rollback.result.details?.length ? (
              <div>
                <Text className={styles.hint}>{t('ai.write.rollback_detail')}</Text>
                <ul style={{ margin: '4px 0 0', paddingLeft: 18 }}>
                  {rollback.result.details.map((detail, index) => (
                    <li key={index}>{detail}</li>
                  ))}
                </ul>
              </div>
            ) : null
          }
        />
      )}
    </div>
  );
}
