import { Tag } from 'antd';
import { HistoryOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import type { QueryHistoryItem } from '../../api/types';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

export interface QueryHistoryPanelProps {
  items: QueryHistoryItem[];
  open: boolean;
  onToggle: () => void;
  onReload: () => void;
  onPick: (sql: string) => void;
}

/** Bottom pane of SQL Lab: recent queries, click to restore the SQL. */
export default function QueryHistoryPanel({
  items,
  open,
  onToggle,
  onReload,
  onPick,
}: QueryHistoryPanelProps) {
  const { t } = useI18n();
  return (
    <div className="sqllab__history-card">
      <div className="table-toolbar" style={{ padding: '6px 12px' }}>
        <div className="table-toolbar__left">
          <a onClick={onToggle}>
            <HistoryOutlined /> {t('sqllab.history_title')} {open ? '▾' : '▸'}
          </a>
          <span className="table-toolbar__count">
            {t('sqllab.history_count', { count: items.length })}
          </span>
        </div>
        <div className="table-toolbar__right">
          <a onClick={onReload}>{t('common.actions.refresh')}</a>
        </div>
      </div>
      {open ? (
        <div className="sqllab__history-list">
          {items.length ? (
            items.map(item => (
              <div
                key={item.id}
                className="sqllab__history-item"
                onClick={() => onPick(item.sql)}
              >
                <Tag
                  color={
                    item.status === 'success'
                      ? 'green'
                      : item.status === 'running'
                        ? 'blue'
                        : 'red'
                  }
                  style={{ marginInlineEnd: 0 }}
                >
                  {item.status}
                </Tag>
                <span className="sqllab__history-sql" title={item.sql}>
                  {item.sql}
                </span>
                <span className="cell-muted">{item.database_name || '—'}</span>
                <span className="cell-muted">{item.rows ?? 0} rows</span>
                <span className="cell-muted">
                  {item.start_time
                    ? dayjs(Number(item.start_time) * 1000).format('MM-DD HH:mm')
                    : '—'}
                </span>
              </div>
            ))
          ) : (
            <div className="cell-muted" style={{ padding: '12px 14px' }}>
              {t('sqllab.history_empty')}
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}
