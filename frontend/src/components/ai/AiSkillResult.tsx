import { Collapse, Descriptions, Space, Tag, Typography } from 'antd';
import MarkdownBox from '../dashboard/MarkdownBox';
import AiSqlPreview from './AiSqlPreview';
import type { SkillResultPayload } from '../../api/ai';
import { useI18n } from '../../i18n';
import styles from './ai.module.css';

const { Text } = Typography;

/** Skills whose payload is a candidate SQL statement rather than a narrative. */
const SQL_SKILLS = ['nl2sql', 'sql_explain'];
/** Skills whose payload is a markdown narrative (plus optional data table). */
const NARRATIVE_SKILLS = ['insight', 'dupont', 'trend', 'structure', 'yoy_mom'];

/* ------------------------------------------------------------- accessors */

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** Normalises an arbitrary payload into table rows. */
function asRows(value: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(value)) {
    return value.map(item => (item && typeof item === 'object' ? asRecord(item) : { value: item }));
  }
  const record = asRecord(value);
  return Object.keys(record).length ? [record] : [];
}

function textOf(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function scalar(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

/** Renders a string list (or a delimited string) as tags. */
function stringList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(item => scalar(item));
  if (typeof value === 'string' && value.trim()) {
    return value.split(/[,;、]/).map(part => part.trim()).filter(Boolean);
  }
  return [];
}

/* --------------------------------------------------------------- blocks */

/** Native table: avoids the antd generics churn and renders arbitrary keys. */
function ObjectTable({ rows }: { rows: Array<Record<string, unknown>> }) {
  const { t } = useI18n();
  if (!rows.length) return null;
  const columns = Array.from(new Set(rows.flatMap(row => Object.keys(row))));
  return (
    <div className={styles.stack}>
      <Text className={styles.hint}>{t('ai.result.data')}</Text>
      <div className={styles.tableWrap}>
        <table className={styles.table}>
          <thead>
            <tr>
              {columns.map(column => (
                <th key={column}>{column}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, index) => (
              <tr key={index}>
                {columns.map(column => (
                  <td key={column}>{scalar(row[column])}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SqlBlock({ output, artifacts }: { output: Record<string, unknown>; artifacts: Record<string, unknown> }) {
  const { t } = useI18n();
  const sql = textOf(output.sql) || textOf(artifacts.sql);
  const confidence = output.confidence ?? artifacts.confidence;
  const missing = stringList(output.missing ?? artifacts.missing);
  const explanation = textOf(output.explanation) || textOf(artifacts.explanation);

  return (
    <div className={styles.stack}>
      <Space size={6} wrap className={styles.metaRow}>
        <Text strong>{t('ai.result.sql')}</Text>
        {confidence !== undefined && (
          <Tag color="blue">
            {t('ai.result.confidence')}: {scalar(confidence)}
          </Tag>
        )}
        {missing.length > 0 && (
          <Tag color="orange">
            {t('ai.result.missing')}: {missing.join(', ')}
          </Tag>
        )}
        <AiSqlPreview sql={sql} />
      </Space>
      {explanation && <MarkdownBox text={explanation} />}
    </div>
  );
}

function ChartBlock({ output, artifacts }: { output: Record<string, unknown>; artifacts: Record<string, unknown> }) {
  const { t } = useI18n();
  const vizType = scalar(output.vizType ?? output.viz_type ?? artifacts.vizType ?? artifacts.viz_type);
  const metrics = stringList(output.metrics ?? artifacts.metrics);
  const groupby = stringList(output.groupby ?? artifacts.groupby);
  const reason = textOf(output.reason ?? artifacts.reason);
  const alternatives = stringList(output.alternatives ?? artifacts.alternatives);

  return (
    <div className={styles.stack}>
      <Text strong>{t('ai.result.chart')}</Text>
      <Descriptions size="small" column={1} bordered>
        <Descriptions.Item label={t('ai.result.chart.viz')}>{vizType}</Descriptions.Item>
        <Descriptions.Item label={t('ai.result.chart.metrics')}>
          {metrics.length ? metrics.join(', ') : '—'}
        </Descriptions.Item>
        <Descriptions.Item label={t('ai.result.chart.groupby')}>
          {groupby.length ? groupby.join(', ') : '—'}
        </Descriptions.Item>
        <Descriptions.Item label={t('ai.result.chart.alternatives')}>
          {alternatives.length ? alternatives.join(', ') : '—'}
        </Descriptions.Item>
      </Descriptions>
      {reason && (
        <>
          <Text className={styles.hint}>{t('ai.result.chart.reason')}</Text>
          <MarkdownBox text={reason} />
        </>
      )}
    </div>
  );
}

function MetricBlock({ output }: { output: Record<string, unknown> }) {
  const { t } = useI18n();
  const alignment = asRows(output.alignment ?? output.metrics);
  return (
    <div className={styles.stack}>
      {alignment.length > 0 && (
        <>
          <Text strong>{t('ai.result.metrics')}</Text>
          <ObjectTable rows={alignment} />
        </>
      )}
      <ObjectTable rows={asRows(output.data)} />
    </div>
  );
}

function NarrativeBlock({ markdown, output }: { markdown: string; output: Record<string, unknown> }) {
  return (
    <div className={styles.stack}>
      {markdown ? <MarkdownBox text={markdown} /> : null}
      <ObjectTable rows={asRows(output.data)} />
    </div>
  );
}

function RawBlock({ payload }: { payload: SkillResultPayload }) {
  const { t } = useI18n();
  let body: string;
  try {
    body = JSON.stringify(payload.output ?? payload.artifacts ?? {}, null, 2);
  } catch {
    body = String(payload.output);
  }
  return (
    <Collapse
      size="small"
      ghost
      items={[{ key: 'raw', label: t('ai.result.raw'), children: <pre className={styles.json}>{body}</pre> }]}
    />
  );
}

/* ----------------------------------------------------------- dispatcher */

interface Props {
  name: string;
  payload: SkillResultPayload;
}

/** Renders one observed skill result with a shape tailored to that skill. */
export default function AiSkillResult({ name, payload }: Props) {
  const output = asRecord(payload.output);
  const artifacts = asRecord(payload.artifacts);
  const markdown = payload.markdown ?? textOf(output.markdown);

  if (SQL_SKILLS.includes(name)) {
    return <SqlBlock output={output} artifacts={artifacts} />;
  }
  if (name === 'chart_design') {
    return <ChartBlock output={output} artifacts={artifacts} />;
  }
  if (name === 'metric_compute') {
    return (
      <div className={styles.stack}>
        {markdown ? <MarkdownBox text={markdown} /> : null}
        <MetricBlock output={output} />
      </div>
    );
  }
  if (NARRATIVE_SKILLS.includes(name)) {
    return <NarrativeBlock markdown={markdown} output={output} />;
  }
  if (name === 'dashboard_compose') {
    return <NarrativeBlock markdown={markdown} output={output} />;
  }
  return <RawBlock payload={payload} />;
}
