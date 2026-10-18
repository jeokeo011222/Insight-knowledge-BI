import { useMemo, useState } from 'react';
import { Button, Popconfirm, Space, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { DeleteOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Paginated, ReportSchedule } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import type { ListQuery } from '../../components/common/EntityList';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

export interface ScheduleListProps {
  type: 'Alert' | 'Report';
  title: string;
  subtitle: string;
  /** When rendered inside the 告警与报告 tabs, drop the page header. */
  embedded?: boolean;
}

/** Shared read-only list for alerts and reports (same ReportSchedule model). */
export default function ScheduleList({ type, title, subtitle, embedded = false }: ScheduleListProps) {
  const { can } = usePermission();
  const { t } = useI18n();
  const [reloadToken, setReloadToken] = useState(0);

  const fetchSchedules = (query: ListQuery): Promise<Paginated<ReportSchedule>> =>
    client.get<Paginated<ReportSchedule>>(endpoints.reports, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
      type,
    });

  const handleDelete = async (schedule: ReportSchedule) => {
    try {
      await client.delete(endpoints.report(schedule.id));
      message.success(t('schedule.deleted', { name: schedule.name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns = useMemo<ColumnsType<ReportSchedule>>(
    () => [
      {
        title: t('common.label.name'),
        dataIndex: 'name',
        key: 'name',
        render: (value: string) => <span style={{ fontWeight: 500 }}>{value}</span>,
      },
      {
        title: t('schedule.col_type'),
        dataIndex: 'type',
        key: 'type',
        width: 90,
        render: (value: string) => <Tag bordered={false}>{value}</Tag>,
      },
      {
        title: t('common.label.status'),
        dataIndex: 'active',
        key: 'active',
        width: 90,
        render: (value: boolean) =>
          value ? (
            <Tag color="green">{t('schedule.enabled')}</Tag>
          ) : (
            <Tag>{t('schedule.disabled')}</Tag>
          ),
      },
      {
        title: t('schedule.col_schedule'),
        key: 'crontab',
        render: (_, record) => (
          <span className="cell-mono">
            {record.crontab}
            {record.timezone ? ` (${record.timezone})` : ''}
          </span>
        ),
      },
      {
        title: t('common.label.owner'),
        key: 'owners',
        width: 150,
        render: (_, record) => (
          <span className="cell-muted">
            {(record.owners ?? [])
              .map(owner => owner.username || owner.name)
              .filter(Boolean)
              .join(', ') || '—'}
          </span>
        ),
      },
      {
        title: t('schedule.col_last_status'),
        dataIndex: 'last_state',
        key: 'last_state',
        width: 120,
        render: (value?: string) =>
          value ? (
            <Tag color={value === 'Success' ? 'green' : value === 'Working' ? 'blue' : 'red'}>
              {value}
            </Tag>
          ) : (
            <span className="cell-muted">{t('schedule.never_run')}</span>
          ),
      },
      {
        title: t('common.label.changed_on'),
        dataIndex: 'changed_on',
        key: 'changed_on',
        width: 160,
        render: (value?: string) => (
          <span className="cell-muted">{formatDateTimeShort(value)}</span>
        ),
      },
      {
        title: t('common.label.actions'),
        key: 'actions',
        width: 70,
        render: (_, record) =>
          can('can_delete', 'ReportSchedule') ? (
            <Popconfirm
              title={t('common.actions.delete')}
              description={t('schedule.delete_confirm', { name: record.name })}
              okText={t('common.actions.delete')}
              cancelText={t('common.actions.cancel')}
              okButtonProps={{ danger: true }}
              onConfirm={() => void handleDelete(record)}
            >
              <Button size="small" type="text" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          ) : null,
      },
    ],
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [can, t],
  );

  return (
    <EntityList<ReportSchedule>
      title={embedded ? undefined : title}
      subtitle={embedded ? undefined : subtitle}
      persistKey={`schedule-${type.toLowerCase()}`}
      columns={columns}
      fetchData={fetchSchedules}
      rowKey={record => record.id}
      reloadToken={reloadToken}
      searchPlaceholder={
        type === 'Alert' ? t('schedule.search_alert') : t('schedule.search_report')
      }
      emptyDescription={type === 'Alert' ? t('schedule.empty_alert') : t('schedule.empty_report')}
      extra={
        <Space>
          <Tag bordered={false}>
            {type === 'Alert' ? t('schedule.kind_threshold') : t('schedule.kind_scheduled')}
          </Tag>
        </Space>
      }
    />
  );
}
