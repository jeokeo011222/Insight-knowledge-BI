import { useEffect, useState } from 'react';
import { Drawer, Empty, Skeleton, Space, Tag, Typography, message } from 'antd';
import {
  DashboardOutlined,
  DatabaseOutlined,
  LineChartOutlined,
} from '@ant-design/icons';
import { errorMessage } from '../../api/client';
import { getLineage } from '../../api/share';
import type { LineageGraph, LineageNode } from '../../api/share';
import { useI18n } from '../../i18n';

export interface LineageDrawerProps {
  open: boolean;
  type: 'dataset' | 'chart' | 'dashboard';
  id: number | null;
  name?: string;
  onClose: () => void;
}

function nodeIcon(type: string) {
  if (type === 'dataset') return <DatabaseOutlined />;
  if (type === 'chart') return <LineChartOutlined />;
  return <DashboardOutlined />;
}

/**
 * Lineage drawer: dataset → chart → dashboard.
 *
 * A list instead of a node graph on purpose: the question a reviewer actually
 * asks is "what breaks if I change this?", and a sorted list answers it faster
 * than a canvas they have to pan around. The graph edges are already returned
 * by the API, so a canvas view can be added later without a backend change.
 */
export default function LineageDrawer({ open, type, id, name, onClose }: LineageDrawerProps) {
  const { t } = useI18n();
  const [graph, setGraph] = useState<LineageGraph | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open || !id) return;
    let cancelled = false;
    setLoading(true);
    setGraph(null);
    getLineage(type, id)
      .then(result => {
        if (!cancelled) setGraph(result);
      })
      .catch(err => {
        if (!cancelled) message.error(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, id, type]);

  const renderGroup = (title: string, nodes: LineageNode[], emptyHint: string) => (
    <div className="lineage-group">
      <div className="lineage-group__title">
        {title} · {nodes.length}
      </div>
      {nodes.length ? (
        nodes.map(node => (
          <div className="lineage-node" key={node.key}>
            <Tag bordered={false}>{nodeIcon(node.type)}</Tag>
            <div style={{ minWidth: 0 }}>
              <div className="lineage-node__name">{node.name || `#${node.id}`}</div>
              <div className="lineage-node__meta">
                {t(`lineage.type.${node.type}` as 'lineage.type.dataset')}
                {node.subtitle ? ` · ${node.subtitle}` : ''}
                {node.detail ? ` · ${node.detail}` : ''}
              </div>
            </div>
          </div>
        ))
      ) : (
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {emptyHint}
        </Typography.Text>
      )}
    </div>
  );

  return (
    <Drawer
      title={t('lineage.title')}
      open={open}
      onClose={onClose}
      width={460}
      destroyOnClose
    >
      <Space direction="vertical" size={2} style={{ marginBottom: 16 }}>
        <Typography.Text strong>{name || `#${id ?? ''}`}</Typography.Text>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('lineage.subtitle')}
        </Typography.Text>
      </Space>

      {loading ? (
        <Skeleton active paragraph={{ rows: 6 }} />
      ) : !graph || (!graph.upstream.length && !graph.downstream.length) ? (
        <Empty description={t('lineage.empty')} />
      ) : (
        <>
          {renderGroup(t('lineage.upstream'), graph.upstream, t('lineage.empty'))}
          {renderGroup(t('lineage.downstream'), graph.downstream, t('lineage.empty'))}
        </>
      )}
    </Drawer>
  );
}
