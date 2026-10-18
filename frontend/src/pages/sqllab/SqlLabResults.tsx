import { useMemo } from 'react';
import { Alert, Empty, Table, Tag } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { SqlLabColumn, SqlLabResult } from '../../api/types';
import { useI18n } from '../../i18n';

export interface SqlLabResultsProps {
  result: SqlLabResult | null;
  runError: string | null;
  /** Live query status from the async channel (pending/running/…). */
  status?: string | null;
}

/** Result grid for SQL Lab with error state and client side pagination. */
export default function SqlLabResults({ result, runError, status }: SqlLabResultsProps) {
  const { t } = useI18n();
  const columns = useMemo<ColumnsType<Record<string, unknown>>>(() => {
    const list: SqlLabColumn[] = result?.columns ?? [];
    return list.map(column => {
      const name = column.column_name || column.name;
      return {
        title: (
          <span>
            {name}
            <span className="cell-muted" style={{ marginLeft: 6, fontWeight: 400 }}>
              {column.type}
            </span>
          </span>
        ),
        dataIndex: name,
        key: name,
        ellipsis: true,
        render: (value: unknown) =>
          value === null || value === undefined ? (
            <span className="cell-muted">NULL</span>
          ) : (
            String(value)
          ),
      };
    });
  }, [result]);

  const rows = result?.data ?? [];

  return (
    <div className="sqllab__result-card">
      <div className="table-toolbar">
        <div className="table-toolbar__left">
          <span className="section-title" style={{ margin: 0 }}>
            {t('sqllab.results')}
          </span>
          {result ? (
            <span className="table-toolbar__count">
              {result.data?.length ?? 0} rows · {result.columns?.length ?? 0} cols
              {result.query?.id ? ` · query #${result.query.id}` : ''}
            </span>
          ) : null}
        </div>
        <div className="table-toolbar__right">
          {status ? (
            <Tag color={status === 'success' ? 'green' : status === 'running' ? 'blue' : 'default'}>
              {status}
            </Tag>
          ) : null}
          {result?.query?.status ? (
            <Tag color={result.query.status === 'success' ? 'green' : 'red'}>
              {result.query.status}
            </Tag>
          ) : null}
        </div>
      </div>
      <div className="sqllab__result-body">
        {runError ? (
          <Alert
            type="error"
            showIcon
            message={t('sqllab.exec_failed_short')}
            description={<pre className="mono-block" style={{ margin: 0 }}>{runError}</pre>}
            style={{ margin: 12 }}
          />
        ) : null}
        {!runError && !result ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t('sqllab.results_empty')}
            style={{ padding: '36px 0' }}
          />
        ) : null}
        {result && columns.length ? (
          <Table<Record<string, unknown>>
            size="small"
            rowKey={(_, index) => String(index)}
            columns={columns}
            dataSource={rows}
            scroll={{ x: 'max-content' }}
            pagination={{
              pageSize: 25,
              size: 'small',
              showSizeChanger: false,
              showTotal: count => t('sqllab.row_total', { count }),
            }}
          />
        ) : null}
        {result && !runError && !columns.length ? (
          <div className="cell-muted" style={{ padding: 16 }}>
            {t('sqllab.no_result_set')}
          </div>
        ) : null}
      </div>
    </div>
  );
}
