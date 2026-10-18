import { useMemo, useState } from 'react';
import { Alert, Button, Input, InputNumber, Select, Space, Table, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { client, endpoints, errorMessage } from '../../../api/client';
import type { ApiAuthType, ApiSourceConfig, Dataset, IngestPreview } from '../../../api/types';
import { useI18n } from '../../../i18n';

export interface ApiSourceTabProps {
  onClose: () => void;
  onCreated?: (dataset: Dataset) => void;
}

interface PreviewRow {
  key: number;
  row: unknown[];
}

/** Parses an optional JSON object field, returning undefined when blank. */
function parseObjectField(text: string): { value?: Record<string, string>; error?: string } {
  const trimmed = text.trim();
  if (!trimmed) return {};
  try {
    const parsed = JSON.parse(trimmed);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return { error: 'not-object' };
    }
    const value: Record<string, string> = {};
    Object.entries(parsed as Record<string, unknown>).forEach(([key, item]) => {
      value[key] = typeof item === 'string' ? item : String(item);
    });
    return { value };
  } catch {
    return { error: 'invalid-json' };
  }
}

function formatCell(value: unknown): string {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

/**
 * Creates a dataset backed by a remote JSON-over-HTTP API. The configuration is
 * validated with a dry-run fetch before the dataset is created; the same config
 * is stored on the dataset so it can be re-pulled later.
 */
export default function ApiSourceTab({ onClose, onCreated }: ApiSourceTabProps) {
  const { t } = useI18n();
  const [url, setUrl] = useState('');
  const [method, setMethod] = useState('GET');
  const [headersText, setHeadersText] = useState('');
  const [paramsText, setParamsText] = useState('');
  const [bodyText, setBodyText] = useState('');
  const [authType, setAuthType] = useState<ApiAuthType>('none');
  const [token, setToken] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [keyName, setKeyName] = useState('');
  const [keyValue, setKeyValue] = useState('');
  const [keyIn, setKeyIn] = useState<'header' | 'query'>('header');
  const [recordPath, setRecordPath] = useState('');
  const [maxRows, setMaxRows] = useState<number | null>(1000);
  const [timeoutSec, setTimeoutSec] = useState<number | null>(30);
  const [tableName, setTableName] = useState('');
  const [description, setDescription] = useState('');
  const [preview, setPreview] = useState<IngestPreview | null>(null);
  const [testing, setTesting] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const buildSource = (): ApiSourceConfig | null => {
    if (!url.trim()) {
      message.warning(t('dataset.form.api.url_required'));
      return null;
    }
    const headers = parseObjectField(headersText);
    if (headers.error) {
      setError(t('dataset.form.api.json_invalid', { field: t('dataset.form.api.headers') }));
      return null;
    }
    const params = parseObjectField(paramsText);
    if (params.error) {
      setError(t('dataset.form.api.json_invalid', { field: t('dataset.form.api.params') }));
      return null;
    }
    return {
      url: url.trim(),
      method,
      headers: headers.value,
      params: params.value,
      body: bodyText.trim() || undefined,
      auth:
        authType === 'none'
          ? { type: 'none' }
          : {
              type: authType,
              token: authType === 'bearer' ? token : undefined,
              username: authType === 'basic' ? username : undefined,
              password: authType === 'basic' ? password : undefined,
              key_name: authType === 'api_key' ? keyName : undefined,
              key_value: authType === 'api_key' ? keyValue : undefined,
              in: authType === 'api_key' ? keyIn : undefined,
            },
      record_path: recordPath.trim() || undefined,
      max_rows: maxRows ?? undefined,
      timeout_sec: timeoutSec ?? undefined,
    };
  };

  const handleTest = async () => {
    const source = buildSource();
    if (!source) return;
    setTesting(true);
    setError(null);
    setPreview(null);
    try {
      const response = await client.post<{ result: IngestPreview }>(
        endpoints.datasetApiSourceTest,
        { source },
      );
      setPreview(response.result);
      message.success(t('dataset.form.api.test_ok', { count: response.result?.rowcount ?? 0 }));
    } catch (err) {
      setError(errorMessage(err));
      message.error(errorMessage(err));
    } finally {
      setTesting(false);
    }
  };

  const handleSubmit = async () => {
    const source = buildSource();
    if (!source) return;
    setSubmitting(true);
    setError(null);
    try {
      const response = await client.post<{ result: Dataset }>(endpoints.datasetApiSource, {
        table_name: tableName.trim() || undefined,
        description: description.trim() || undefined,
        source,
      });
      message.success(
        t('dataset.form.api.created', { name: response.result?.table_name ?? tableName }),
      );
      setUrl('');
      setTableName('');
      setDescription('');
      setPreview(null);
      onClose();
      onCreated?.(response.result);
    } catch (err) {
      setError(errorMessage(err));
      message.error(errorMessage(err));
    } finally {
      setSubmitting(false);
    }
  };

  const previewColumns: ColumnsType<PreviewRow> = useMemo(() => {
    if (!preview) return [];
    return preview.columns.map((column, index) => ({
      title: (
        <Space size={4}>
          <span className="mono">{column.column_name}</span>
          <Tag bordered={false}>{column.type}</Tag>
          {column.is_dttm ? <Tag color="geekblue" bordered={false}>time</Tag> : null}
        </Space>
      ),
      key: `c${index}`,
      render: (_: unknown, record: PreviewRow) => formatCell(record.row[index]),
    }));
  }, [preview]);

  const previewData: PreviewRow[] = useMemo(
    () => (preview?.data ?? []).map((row, index) => ({ key: index, row })),
    [preview],
  );

  return (
    <div>
      <div style={{ display: 'grid', gridTemplateColumns: '120px 1fr', gap: 12 }}>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.method')}
          </div>
          <Select
            style={{ width: '100%' }}
            value={method}
            onChange={setMethod}
            options={['GET', 'POST', 'PUT', 'PATCH'].map(item => ({ value: item, label: item }))}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.url')}
          </div>
          <Input
            value={url}
            onChange={event => setUrl(event.target.value)}
            placeholder="https://api.example.com/v1/records"
          />
        </div>
      </div>

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('dataset.form.api.headers')}
        </div>
        <Input.TextArea
          rows={2}
          value={headersText}
          onChange={event => setHeadersText(event.target.value)}
          placeholder='{"X-Tenant": "acme"}'
        />
      </div>

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('dataset.form.api.params')}
        </div>
        <Input.TextArea
          rows={2}
          value={paramsText}
          onChange={event => setParamsText(event.target.value)}
          placeholder='{"page": "1", "size": "500"}'
        />
      </div>

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('dataset.form.api.body')}
        </div>
        <Input.TextArea
          rows={2}
          value={bodyText}
          onChange={event => setBodyText(event.target.value)}
          placeholder='{"query": "*"}'
        />
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '150px 1fr', gap: 12, marginTop: 12 }}>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.auth_type')}
          </div>
          <Select
            style={{ width: '100%' }}
            value={authType}
            onChange={value => setAuthType(value as ApiAuthType)}
            options={[
              { value: 'none', label: t('dataset.form.api.auth_none') },
              { value: 'bearer', label: t('dataset.form.api.auth_bearer') },
              { value: 'basic', label: t('dataset.form.api.auth_basic') },
              { value: 'api_key', label: t('dataset.form.api.auth_api_key') },
            ]}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {authType === 'bearer'
              ? t('dataset.form.api.token')
              : authType === 'basic'
                ? t('dataset.form.api.username')
                : authType === 'api_key'
                  ? t('dataset.form.api.key_name')
                  : t('dataset.form.api.auth_hint')}
          </div>
          {authType === 'bearer' ? (
            <Input.Password value={token} onChange={event => setToken(event.target.value)} />
          ) : authType === 'basic' ? (
            <Space.Compact style={{ width: '100%' }}>
              <Input
                value={username}
                onChange={event => setUsername(event.target.value)}
                placeholder={t('dataset.form.api.username')}
              />
              <Input.Password
                value={password}
                onChange={event => setPassword(event.target.value)}
                placeholder={t('dataset.form.api.password')}
              />
            </Space.Compact>
          ) : authType === 'api_key' ? (
            <Space.Compact style={{ width: '100%' }}>
              <Input
                value={keyName}
                onChange={event => setKeyName(event.target.value)}
                placeholder="X-API-Key"
              />
              <Input.Password
                value={keyValue}
                onChange={event => setKeyValue(event.target.value)}
                placeholder={t('dataset.form.api.key_value')}
              />
              <Select
                style={{ width: 110 }}
                value={keyIn}
                onChange={value => setKeyIn(value)}
                options={[
                  { value: 'header', label: t('dataset.form.api.key_in_header') },
                  { value: 'query', label: t('dataset.form.api.key_in_query') },
                ]}
              />
            </Space.Compact>
          ) : (
            <Input disabled value={t('dataset.form.api.auth_none_hint')} />
          )}
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 130px 130px', gap: 12, marginTop: 12 }}>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.record_path')}
          </div>
          <Input
            value={recordPath}
            onChange={event => setRecordPath(event.target.value)}
            placeholder={t('dataset.form.api.record_path_placeholder')}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.max_rows')}
          </div>
          <InputNumber
            style={{ width: '100%' }}
            min={1}
            max={100000}
            value={maxRows ?? undefined}
            onChange={value => setMaxRows(value ?? null)}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.api.timeout')}
          </div>
          <InputNumber
            style={{ width: '100%' }}
            min={1}
            max={120}
            value={timeoutSec ?? undefined}
            onChange={value => setTimeoutSec(value ?? null)}
          />
        </div>
      </div>

      <Space style={{ marginTop: 12 }}>
        <Button loading={testing} onClick={() => void handleTest()}>
          {t('dataset.form.api.test')}
        </Button>
        <span className="cell-muted" style={{ fontSize: 12 }}>
          {t('dataset.form.api.test_hint')}
        </span>
      </Space>

      {error ? (
        <Alert type="error" showIcon style={{ marginTop: 12 }} message={error} />
      ) : null}

      {preview ? (
        <div style={{ marginTop: 16 }}>
          <div className="section-title" style={{ marginBottom: 8 }}>
            {t('dataset.form.api.preview')}{' '}
            <span className="section-title__hint">
              {t('dataset.form.api.preview_meta', {
                rows: preview.rowcount,
                ms: preview.duration_ms,
              })}
            </span>
          </div>
          <Table<PreviewRow>
            size="small"
            rowKey="key"
            columns={previewColumns}
            dataSource={previewData}
            scroll={{ x: true }}
            pagination={{ pageSize: 5, size: 'small', showSizeChanger: false }}
          />
        </div>
      ) : null}

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 16 }}>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.file.table_name')}
          </div>
          <Input
            value={tableName}
            onChange={event => setTableName(event.target.value)}
            placeholder={t('dataset.form.api.table_name_placeholder')}
            maxLength={120}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('common.label.description')}
          </div>
          <Input
            value={description}
            onChange={event => setDescription(event.target.value)}
            placeholder={t('dataset.form.description_placeholder')}
            maxLength={400}
          />
        </div>
      </div>

      <Space style={{ marginTop: 16, width: '100%', justifyContent: 'flex-end' }}>
        <Button onClick={onClose}>{t('common.actions.cancel')}</Button>
        <Button type="primary" loading={submitting} onClick={() => void handleSubmit()}>
          {t('dataset.form.api.submit')}
        </Button>
      </Space>
    </div>
  );
}
