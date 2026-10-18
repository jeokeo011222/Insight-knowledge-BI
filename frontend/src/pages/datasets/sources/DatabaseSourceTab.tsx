import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Empty,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../../api/client';
import type {
  Database,
  Dataset,
  Paginated,
  TableMetadata,
  TableMetadataColumn,
} from '../../../api/types';
import { useAppSelector } from '../../../store';
import { useI18n } from '../../../i18n';

interface ColumnDraft extends TableMetadataColumn {
  checked: boolean;
}

/** One entry of /database/{id}/tables. */
interface TableOption {
  value: string;
  label: string;
  type?: string | null;
}

export interface DatabaseSourceTabProps {
  onClose: () => void;
  onCreated?: (dataset: Dataset) => void;
}

/**
 * The original "connect a database table" flow. It syncs the selected table's
 * columns and creates a physical dataset, exactly as the create drawer did
 * before the external sources were added.
 */
export default function DatabaseSourceTab({ onClose, onCreated }: DatabaseSourceTabProps) {
  const { t } = useI18n();
  const me = useAppSelector(state => state.user.me);
  const [databases, setDatabases] = useState<Database[]>([]);
  const [databaseId, setDatabaseId] = useState<number | null>(null);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [schema, setSchema] = useState<string | undefined>(undefined);
  const [tableOptions, setTableOptions] = useState<TableOption[]>([]);
  const [tableName, setTableName] = useState<string | undefined>(undefined);
  const [manualTable, setManualTable] = useState(false);
  const [description, setDescription] = useState('');
  const [columns, setColumns] = useState<ColumnDraft[]>([]);
  const [loadingSchemas, setLoadingSchemas] = useState(false);
  const [loadingTables, setLoadingTables] = useState(false);
  const [loadingColumns, setLoadingColumns] = useState(false);
  const [schemasError, setSchemasError] = useState<string | null>(null);
  const [tablesError, setTablesError] = useState<string | null>(null);
  const [columnsError, setColumnsError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [schemaReloadToken, setSchemaReloadToken] = useState(0);
  const [tableReloadToken, setTableReloadToken] = useState(0);

  useEffect(() => {
    client
      .get<Paginated<Database>>(endpoints.databases, { page: 1, page_size: 100 })
      .then(response => setDatabases(response.result ?? []))
      .catch(error => message.error(errorMessage(error)));
  }, []);

  // Schemas. The backend also reports the connection's own database as the
  // default schema; without that hint a MySQL server would preselect whichever
  // schema happens to sort first.
  useEffect(() => {
    if (!databaseId) {
      setSchemas([]);
      setSchemasError(null);
      return;
    }
    let cancelled = false;
    setLoadingSchemas(true);
    setSchemasError(null);
    client
      .get<{ result: string[]; default_schema?: string }>(endpoints.databaseSchemas(databaseId))
      .then(response => {
        if (cancelled) return;
        const list = response.result ?? [];
        setSchemas(list);
        setSchema(current => {
          if (current && list.includes(current)) return current;
          const preferred = response.default_schema;
          if (preferred && list.includes(preferred)) return preferred;
          return list[0];
        });
      })
      .catch(error => {
        if (cancelled) return;
        setSchemas([]);
        setSchemasError(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setLoadingSchemas(false);
      });
    return () => {
      cancelled = true;
    };
  }, [databaseId, schemaReloadToken]);

  // Tables.
  useEffect(() => {
    if (!databaseId || !schema) {
      setTableOptions([]);
      return;
    }
    let cancelled = false;
    setLoadingTables(true);
    setTablesError(null);
    client
      .get<{ result: Array<{ value: string; label: string; type?: string | null }> }>(
        endpoints.databaseTables(databaseId),
        { schema_name: schema },
      )
      .then(response => {
        if (cancelled) return;
        setTableOptions(
          (response.result ?? []).map(item => ({
            value: item.value,
            label: item.label || item.value,
            type: item.type ?? null,
          })),
        );
      })
      .catch(error => {
        if (cancelled) return;
        setTableOptions([]);
        setTablesError(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setLoadingTables(false);
      });
    return () => {
      cancelled = true;
    };
  }, [databaseId, schema, tableReloadToken]);

  const loadColumns = useCallback(
    async (value: string) => {
      if (!databaseId || !schema || !value) return;
      setTableName(value);
      setLoadingColumns(true);
      setColumnsError(null);
      setColumns([]);
      try {
        const metadata = await client.get<TableMetadata>(endpoints.databaseTableMetadata(databaseId), {
          schema_name: schema,
          table_name: value,
        });
        const rows = (metadata.columns ?? []).map(column => ({ ...column, checked: true }));
        setColumns(rows);
        if (!rows.length) setColumnsError(t('dataset.form.no_columns_detected'));
      } catch (error) {
        setColumns([]);
        setColumnsError(errorMessage(error));
      } finally {
        setLoadingColumns(false);
      }
    },
    [databaseId, schema, t],
  );

  const toggleColumn = (name: string, checked: boolean) => {
    setColumns(rows => rows.map(row => (row.name === name ? { ...row, checked } : row)));
  };

  const toggleDttm = (name: string, isDttm: boolean) => {
    setColumns(rows => rows.map(row => (row.name === name ? { ...row, is_dttm: isDttm } : row)));
  };

  const selectedCount = useMemo(() => columns.filter(column => column.checked).length, [columns]);

  const handleSubmit = async () => {
    if (!databaseId || !tableName) {
      message.warning(t('dataset.form.select_db_table_required'));
      return;
    }
    if (!selectedCount) {
      message.warning(t('dataset.form.select_column_required'));
      return;
    }
    setSubmitting(true);
    try {
      const payload = {
        table_name: tableName,
        database_id: databaseId,
        schema,
        description: description || undefined,
        columns: columns
          .filter(column => column.checked)
          .map(column => ({
            column_name: column.name,
            type: column.type,
            is_dttm: column.is_dttm,
          })),
        owners: me?.id ? [me.id] : undefined,
      };
      const response = await client.post<{ result: Dataset }>(endpoints.datasets, payload);
      message.success(t('dataset.form.created', { name: tableName }));
      onClose();
      onCreated?.(response.result);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSubmitting(false);
    }
  };

  const columnColumns: ColumnsType<ColumnDraft> = [
    {
      title: t('common.actions.sync'),
      dataIndex: 'checked',
      width: 60,
      render: (checked: boolean, record) => (
        <Checkbox
          checked={checked}
          onChange={event => toggleColumn(record.name, event.target.checked)}
        />
      ),
    },
    {
      title: t('dataset.form.column_name'),
      dataIndex: 'name',
      key: 'name',
      render: value => <span className="mono">{value}</span>,
    },
    {
      title: t('common.label.type'),
      dataIndex: 'type',
      key: 'type',
      render: value => <Tag bordered={false}>{value}</Tag>,
    },
    {
      title: t('dataset.form.column_dttm'),
      dataIndex: 'is_dttm',
      width: 90,
      render: (isDttm: boolean, record) => (
        <Checkbox checked={isDttm} onChange={event => toggleDttm(record.name, event.target.checked)} />
      ),
    },
  ];

  const tableSelectOptions = tableOptions.map(item => ({
    value: item.value,
    label: (
      <Space size={6}>
        <span className="mono">{item.label}</span>
        {item.type ? <span className="cell-muted">{item.type}</span> : null}
      </Space>
    ),
  }));

  return (
    <div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('common.label.database')}
          </div>
          <Select
            showSearch
            optionFilterProp="label"
            style={{ width: '100%' }}
            placeholder={t('dataset.form.select_db')}
            value={databaseId ?? undefined}
            onChange={value => {
              setDatabaseId(value);
              setSchema(undefined);
              setTableName(undefined);
              setTableOptions([]);
              setColumns([]);
              setColumnsError(null);
            }}
            options={databases.map(database => ({
              value: database.id,
              label: `${database.database_name}${database.backend ? ` · ${database.backend}` : ''}`,
            }))}
          />
        </div>
        <div>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            <Space size={4}>
              <span>Schema</span>
              <Tooltip title={t('dataset.form.reload_schemas')}>
                <Button
                  size="small"
                  type="text"
                  icon={<ReloadOutlined spin={loadingSchemas} />}
                  disabled={!databaseId || loadingSchemas}
                  onClick={() => setSchemaReloadToken(token => token + 1)}
                />
              </Tooltip>
            </Space>
          </div>
          <Select
            style={{ width: '100%' }}
            placeholder={
              loadingSchemas ? t('common.state.loading') : t('dataset.form.select_schema')
            }
            value={schema}
            onChange={value => {
              setSchema(value);
              setTableName(undefined);
              setColumns([]);
              setColumnsError(null);
            }}
            options={schemas.map(item => ({ value: item, label: item }))}
            disabled={!databaseId || loadingSchemas || !schemas.length}
            notFoundContent={
              <span className="cell-muted" style={{ fontSize: 12 }}>
                {databaseId ? t('dataset.form.schema_empty') : t('dataset.form.select_db')}
              </span>
            }
          />
        </div>
      </div>

      {schemasError ? (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 12 }}
          message={t('dataset.form.schema_load_failed')}
          description={schemasError}
          action={
            <Button size="small" onClick={() => setSchemaReloadToken(token => token + 1)}>
              {t('common.actions.retry')}
            </Button>
          }
        />
      ) : null}

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          <Space size={8}>
            <span>{t('common.label.table')}</span>
            <Tooltip title={t('dataset.form.reload_tables')}>
              <Button
                size="small"
                type="text"
                icon={<ReloadOutlined spin={loadingTables} />}
                disabled={!schema || loadingTables}
                onClick={() => setTableReloadToken(token => token + 1)}
              />
            </Tooltip>
            <a onClick={() => setManualTable(next => !next)}>
              {manualTable ? t('dataset.form.pick_from_list') : t('dataset.form.manual_table')}
            </a>
          </Space>
        </div>
        {manualTable ? (
          <Input
            value={tableName}
            placeholder={t('dataset.form.manual_table_placeholder')}
            addonAfter={
              <Button
                type="link"
                size="small"
                icon={<SearchOutlined />}
                onClick={() => void loadColumns(tableName ?? '')}
              >
                {t('dataset.form.load_columns')}
              </Button>
            }
            onChange={event => setTableName(event.target.value)}
            onPressEnter={() => void loadColumns(tableName ?? '')}
          />
        ) : (
          <Select
            showSearch
            optionFilterProp="value"
            style={{ width: '100%' }}
            placeholder={
              loadingTables ? t('common.state.loading') : t('dataset.form.select_table')
            }
            value={tableName}
            onChange={value => void loadColumns(value)}
            options={tableSelectOptions}
            disabled={!schema || loadingTables}
            loading={loadingTables}
            notFoundContent={
              <span className="cell-muted" style={{ fontSize: 12 }}>
                {!schema ? t('dataset.form.select_schema_first') : t('dataset.form.tables_empty')}
              </span>
            }
          />
        )}
        {!manualTable && schema && !loadingTables && !tablesError && tableOptions.length === 0 ? (
          <div className="cell-muted" style={{ fontSize: 12, marginTop: 4 }}>
            {t('dataset.form.tables_empty_hint')}
          </div>
        ) : null}
      </div>

      {tablesError ? (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 12 }}
          message={t('dataset.form.tables_load_failed')}
          description={tablesError}
          action={
            <Button size="small" onClick={() => setTableReloadToken(token => token + 1)}>
              {t('common.actions.retry')}
            </Button>
          }
        />
      ) : null}

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('common.label.description')}
        </div>
        <Input.TextArea
          rows={2}
          value={description}
          onChange={event => setDescription(event.target.value)}
          placeholder={t('dataset.form.description_placeholder')}
          maxLength={400}
        />
      </div>

      <div style={{ marginTop: 18, marginBottom: 8, display: 'flex', justifyContent: 'space-between' }}>
        <span className="section-title" style={{ margin: 0 }}>
          {t('dataset.form.column_sync')}{' '}
          <span className="section-title__hint">
            {t('dataset.form.column_sync_selected', { count: selectedCount })}
          </span>
        </span>
        {columns.length ? (
          <Space>
            <a onClick={() => setColumns(rows => rows.map(row => ({ ...row, checked: true })))}>
              {t('common.actions.select_all')}
            </a>
            <a onClick={() => setColumns(rows => rows.map(row => ({ ...row, checked: false })))}>
              {t('common.actions.select_none')}
            </a>
          </Space>
        ) : null}
      </div>

      {columnsError ? (
        <Alert type="warning" showIcon message={columnsError} style={{ marginBottom: 12 }} />
      ) : null}

      {loadingColumns ? (
        <div className="cell-muted">{t('dataset.form.loading_columns')}</div>
      ) : columns.length ? (
        <Table<ColumnDraft>
          size="small"
          rowKey="name"
          columns={columnColumns}
          dataSource={columns}
          pagination={{ pageSize: 8, size: 'small', showSizeChanger: false }}
        />
      ) : (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            tableName ? t('dataset.form.no_columns_detected') : t('dataset.form.select_table_first')
          }
          style={{ padding: 24 }}
        />
      )}

      <Alert
        style={{ marginTop: 16 }}
        type="info"
        showIcon
        message={t('dataset.form.sync_hint')}
      />

      <div style={{ marginTop: 16, display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
        <Button onClick={onClose}>{t('common.actions.cancel')}</Button>
        <Button type="primary" loading={submitting} onClick={() => void handleSubmit()}>
          {t('dataset.form.create_submit')}
        </Button>
      </div>
    </div>
  );
}
