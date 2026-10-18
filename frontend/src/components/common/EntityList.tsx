import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Key, MouseEvent as ReactMouseEvent, ReactNode, ThHTMLAttributes } from 'react';
import { Button, Input, Segmented, Skeleton, Space, Table, Tooltip, message } from 'antd';
import { DownloadOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';

type TableSize = 'small' | 'middle' | 'large';
import type { Paginated } from '../../api/types';
import { errorMessage } from '../../api/client';
import PageHeader from './PageHeader';
import { useI18n } from '../../i18n';
import { downloadCsv } from '../../utils/csv';
import type { CsvColumn } from '../../utils/csv';
import { exportStamp } from '../../utils/format';

/** Persisted column widths for one table, keyed by column key. */
type ColumnWidths = Record<string, number>;

const WIDTH_STORAGE_PREFIX = 'table-widths:';
const MIN_COLUMN_WIDTH = 72;

/** Reads the saved widths of a table, tolerating a corrupt entry. */
function readWidths(storageKey: string): ColumnWidths {
  try {
    const raw = localStorage.getItem(WIDTH_STORAGE_PREFIX + storageKey);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== 'object') return {};
    const out: ColumnWidths = {};
    Object.entries(parsed as Record<string, unknown>).forEach(([key, value]) => {
      if (typeof value === 'number' && Number.isFinite(value) && value >= MIN_COLUMN_WIDTH) {
        out[key] = value;
      }
    });
    return out;
  } catch {
    return {};
  }
}

/** Stable identity for a column: its key, else its dataIndex, else its index. */
function columnKeyOf<T>(column: ColumnsType<T>[number], index: number): string {
  if ('key' in column && column.key !== undefined && column.key !== null) {
    return `k:${String(column.key)}`;
  }
  if ('dataIndex' in column && column.dataIndex) {
    const key = Array.isArray(column.dataIndex) ? column.dataIndex.join('.') : column.dataIndex;
    return `d:${String(key)}`;
  }
  return `i:${index}`;
}

/** Header cell with a drag handle on its right edge. */
interface ResizableCellProps extends ThHTMLAttributes<HTMLTableCellElement> {
  children?: ReactNode;
  resizable?: boolean;
  onResize?: (event: ReactMouseEvent<HTMLElement>) => void;
}

function ResizableHeaderCell({ resizable, onResize, children, ...rest }: ResizableCellProps) {
  if (!resizable || !onResize) {
    return <th {...rest}>{children}</th>;
  }
  return (
    <th {...rest} style={{ ...rest.style, position: 'relative' }}>
      {children}
      <span
        role="separator"
        aria-orientation="vertical"
        onMouseDown={onResize}
        onClick={event => event.stopPropagation()}
        style={{
          position: 'absolute',
          insetInlineEnd: 0,
          top: 0,
          bottom: 0,
          width: 8,
          cursor: 'col-resize',
          userSelect: 'none',
          touchAction: 'none',
        }}
      />
    </th>
  );
}


export interface ListQuery {
  page: number;
  pageSize: number;
  search: string;
}

export interface EntityListProps<T extends object> {
  title?: ReactNode;
  subtitle?: ReactNode;
  extra?: ReactNode;
  columns: ColumnsType<T>;
  fetchData: (query: ListQuery) => Promise<Paginated<T>>;
  rowKey: (record: T) => Key;
  searchPlaceholder?: string;
  emptyDescription?: ReactNode;
  emptyAction?: ReactNode;
  reloadToken?: number;
  defaultPageSize?: number;
  showSearch?: boolean;
  onRowClick?: (record: T) => void;
  toolbarExtra?: ReactNode;
  /**
   * Identity used to persist the user's column widths. Defaults to `title`;
   * pass a stable slug (e.g. `charts`) so a translated title does not reset the
   * layout.
   */
  persistKey?: string;
}

/**
 * Generic paginated list: search box, table, pagination and a reload hook.
 * Data always comes from the real API through `fetchData`.
 */
export default function EntityList<T extends object>({
  title,
  subtitle,
  extra,
  columns,
  fetchData,
  rowKey,
  searchPlaceholder,
  emptyDescription,
  emptyAction,
  reloadToken = 0,
  defaultPageSize = 25,
  showSearch = true,
  onRowClick,
  toolbarExtra,
  persistKey,
}: EntityListProps<T>) {
  const { t } = useI18n();
  const [rows, setRows] = useState<T[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(defaultPageSize);
  const [searchInput, setSearchInput] = useState('');
  const [search, setSearch] = useState('');
  const [refreshToken, setRefreshToken] = useState(0);
  const [density, setDensity] = useState<TableSize>(
    () => (localStorage.getItem('table-density') as TableSize | null) ?? 'middle',
  );
  // Column widths are per table and per browser: a list the user tuned once
  // stays tuned (UI-4). Keyed by persistKey/title so two lists cannot fight.
  const storageKey = persistKey ?? (typeof title === 'string' ? title : 'default');
  const [widths, setWidths] = useState<ColumnWidths>(() => readWidths(storageKey));
  const fetchRef = useRef(fetchData);

  fetchRef.current = fetchData;

  // A different table on the same mount point (rare, but route reuse happens)
  // must load its own widths instead of inheriting the previous list's.
  useEffect(() => {
    setWidths(readWidths(storageKey));
  }, [storageKey]);

  useEffect(() => {
    try {
      if (Object.keys(widths).length === 0) {
        localStorage.removeItem(WIDTH_STORAGE_PREFIX + storageKey);
      } else {
        localStorage.setItem(WIDTH_STORAGE_PREFIX + storageKey, JSON.stringify(widths));
      }
    } catch {
      // Private mode / quota: resizing still works for this session.
    }
  }, [widths, storageKey]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 320);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    fetchRef
      .current({ page, pageSize, search })
      .then(response => {
        if (cancelled) return;
        setRows(Array.isArray(response.result) ? response.result : []);
        setTotal(typeof response.count === 'number' ? response.count : 0);
      })
      .catch(error => {
        if (cancelled) return;
        setRows([]);
        setTotal(0);
        message.error(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [page, pageSize, search, reloadToken, refreshToken]);

  useEffect(() => {
    localStorage.setItem('table-density', density);
  }, [density]);

  const reload = useCallback(() => {
    setRefreshToken(current => current + 1);
  }, []);

  /**
   * Pointer-driven column resize. The measured header width is the starting
   * point so the first drag does not jump, and the new width is clamped to a
   * readable minimum.
   */
  const startResize = useCallback(
    (key: string) => (event: ReactMouseEvent<HTMLElement>) => {
      event.preventDefault();
      event.stopPropagation();
      const startWidth =
        event.currentTarget.parentElement?.getBoundingClientRect().width ?? 140;
      const startX = event.clientX;
      const onMove = (moveEvent: MouseEvent) => {
        const next = Math.max(
          MIN_COLUMN_WIDTH,
          Math.round(startWidth + (moveEvent.clientX - startX)),
        );
        setWidths(previous => ({ ...previous, [key]: next }));
      };
      const onUp = () => {
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
        document.body.style.cursor = '';
      };
      document.body.style.cursor = 'col-resize';
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
    },
    [],
  );

  // Widths only take effect in a fixed layout, which in turn needs an explicit
  // total width: mixing `max-content` with `tableLayout: fixed` makes columns
  // jump while dragging.
  const { tableColumns, totalWidth } = useMemo(() => {
    let sum = 0;
    const cols = columns.map((column, index) => {
      const key = columnKeyOf(column, index);
      const declared = 'width' in column && typeof column.width === 'number' ? column.width : undefined;
      const width = widths[key] ?? declared;
      sum += width ?? 140;
      return {
        ...column,
        ...(width ? { width } : {}),
        onHeaderCell: () => ({ resizable: true, onResize: startResize(key) }),
      };
    }) as ColumnsType<T>;
    return { tableColumns: cols, totalWidth: sum };
  }, [columns, widths, startResize]);

  const hasCustomWidth = Object.keys(widths).length > 0;

  /** Exports the loaded page with the visible column titles. */
  const exportCsv = () => {
    const exportable: CsvColumn<T>[] = [];
    columns.forEach(column => {
      if (!('dataIndex' in column)) return;
      const dataIndex = column.dataIndex;
      if (typeof column.title !== 'string' || !dataIndex) return;
      const key = Array.isArray(dataIndex) ? dataIndex.join('.') : String(dataIndex);
      exportable.push({
        key,
        title: column.title,
        value: row => (row as Record<string, unknown>)[key],
      });
    });
    if (exportable.length === 0 || rows.length === 0) {
      message.warning(t('common.actions.export_empty'));
      return;
    }
    const name = typeof title === 'string' ? title : 'export';
    downloadCsv(`${name}-${exportStamp()}.csv`, rows, exportable);
  };

  return (
    <div>
      {title ? <PageHeader title={title} subtitle={subtitle} extra={extra} /> : null}
      <div className="list-card">
        <div className="table-toolbar">
          <div className="table-toolbar__left">
            {showSearch ? (
              <Input
                allowClear
                value={searchInput}
                onChange={event => setSearchInput(event.target.value)}
                placeholder={searchPlaceholder ?? `${t('common.actions.search')}…`}
                prefix={<SearchOutlined style={{ color: 'var(--color-text-tertiary)' }} />}
                style={{ width: 260 }}
              />
            ) : null}
            {toolbarExtra}
            <span className="table-toolbar__count">
              {t('common.label.total')} {total} {t('common.label.items')}
            </span>
          </div>
          <div className="table-toolbar__right">
            {/* Embedded lists (inside a Tabs host) drop the PageHeader, so the
                caller's action buttons move into the toolbar instead. */}
            {!title && extra ? <Space size={8}>{extra}</Space> : null}
            {hasCustomWidth ? (
              <Tooltip title={t('common.table.reset_width')}>
                <Button
                  size="small"
                  type="text"
                  onClick={() => setWidths({})}
                  aria-label={t('common.table.reset_width')}
                >
                  {t('common.table.reset_width')}
                </Button>
              </Tooltip>
            ) : null}
            <Tooltip title={t('common.actions.export_csv')}>
              <Button size="small" type="text" icon={<DownloadOutlined />} onClick={exportCsv} aria-label={t('common.actions.export_csv')} />
            </Tooltip>
            <Tooltip title={t('common.table.density')}>
              <Segmented
                size="small"
                value={density}
                onChange={value => setDensity(value as TableSize)}
                options={[
                  { value: 'small', label: t('common.table.density.compact') },
                  { value: 'middle', label: t('common.table.density.cozy') },
                ]}
              />
            </Tooltip>
            <a onClick={reload} role="button" tabIndex={0}>
              <ReloadOutlined /> {t('common.actions.refresh')}
            </a>
          </div>
        </div>
        {/* First load shows a skeleton (page keeps its shape); refreshes keep
            the table and only overlay a spinner. */}
        {loading && rows.length === 0 ? (
          <Skeleton active title={false} paragraph={{ rows: 8 }} style={{ padding: 16 }} />
        ) : (
        <Table<T>
          size={density}
          loading={loading}
          columns={tableColumns}
          components={{ header: { cell: ResizableHeaderCell } }}
          tableLayout={hasCustomWidth ? 'fixed' : undefined}
          dataSource={rows}
          rowKey={rowKey}
          scroll={{ x: hasCustomWidth ? totalWidth : 'max-content' }}
          locale={{
            emptyText:
              emptyDescription || emptyAction ? (
                <div style={{ padding: '24px 0' }}>
                  <div style={{ color: 'var(--color-text-tertiary)', marginBottom: 12 }}>
                    {emptyDescription ?? t('common.state.no_data')}
                  </div>
                  {emptyAction}
                </div>
              ) : (
                t('common.state.no_data')
              ),
          }}
          onRow={record =>
            onRowClick
              ? {
                  onClick: event => {
                    const target = event.target as HTMLElement;
                    if (target.closest('button, a, .ant-dropdown-trigger, input')) return;
                    onRowClick(record);
                  },
                  style: { cursor: 'pointer' },
                }
              : {}
          }
          pagination={{
            current: page,
            pageSize,
            total,
            showSizeChanger: true,
            pageSizeOptions: [10, 25, 50, 100],
            showTotal: count => `${t('common.label.total')} ${count} ${t('common.label.items')}`,
            onChange: (nextPage, nextSize) => {
              setPage(nextPage);
              setPageSize(nextSize);
            },
          }}
        />
        )}
      </div>
    </div>
  );
}
