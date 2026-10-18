import { Button, Popconfirm, Select, Space, Tag, Tooltip } from 'antd';
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  DeleteOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import type { RowBackground } from '../../api/types';
import type { DashboardRow } from '../../features/dashboard/layout';
import { useI18n } from '../../i18n';

export interface RowHeaderProps {
  row: DashboardRow;
  index: number;
  total: number;
  onAddChart: () => void;
  onMove: (direction: -1 | 1) => void;
  onDelete: () => void;
  onBackgroundChange: (background: RowBackground) => void;
}

const BACKGROUND_OPTIONS: Array<{ value: RowBackground; label: string }> = [
  { value: 'BACKGROUND_TRANSPARENT', label: 'Transparent' },
  { value: 'BACKGROUND_LIGHT', label: 'Light' },
  { value: 'BACKGROUND_DARK', label: 'Dark' },
];

/** Edit-mode row toolbar rendered as a full-width grid band. */
export default function RowHeader({
  row,
  index,
  total,
  onAddChart,
  onMove,
  onDelete,
  onBackgroundChange,
}: RowHeaderProps) {
  const { t } = useI18n();
  return (
    <div className="dashboard-row-head">
      <Space size={8} wrap>
        <Tag bordered={false} color="blue">
          {t('dashboard.row.label', { index: index + 1 })}
        </Tag>
        <span className="cell-muted">{t('dashboard.row.tile_count', { count: row.tiles.length })}</span>
        <Select
          size="small"
          value={row.background}
          options={BACKGROUND_OPTIONS}
          style={{ width: 132 }}
          onChange={onBackgroundChange}
        />
      </Space>
      <Space size={2}>
        <Tooltip title={t('dashboard.row.add_chart_to_row')}>
          <Button size="small" type="text" icon={<PlusOutlined />} onClick={onAddChart}>
            {t('dashboard.row.add_chart')}
          </Button>
        </Tooltip>
        <Tooltip title={t('dashboard.row.move_up')}>
          <Button
            size="small"
            type="text"
            icon={<ArrowUpOutlined />}
            disabled={index === 0}
            onClick={() => onMove(-1)}
          />
        </Tooltip>
        <Tooltip title={t('dashboard.row.move_down')}>
          <Button
            size="small"
            type="text"
            icon={<ArrowDownOutlined />}
            disabled={index >= total - 1}
            onClick={() => onMove(1)}
          />
        </Tooltip>
        <Popconfirm
          title={t('dashboard.row.delete_title')}
          description={t('dashboard.row.delete_desc')}
          okText={t('common.actions.delete')}
          cancelText={t('common.actions.cancel')}
          okButtonProps={{ danger: true }}
          onConfirm={onDelete}
        >
          <Tooltip title={t('dashboard.row.delete')}>
            <Button size="small" type="text" danger icon={<DeleteOutlined />} />
          </Tooltip>
        </Popconfirm>
      </Space>
    </div>
  );
}
