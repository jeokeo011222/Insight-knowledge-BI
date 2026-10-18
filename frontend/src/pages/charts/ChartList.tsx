import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Modal, Popconfirm, Space, Tag, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { AppstoreOutlined, DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Chart, Paginated } from '../../api/types';
import EntityList from '../../components/common/EntityList';
import FavoriteStar from '../../components/common/FavoriteStar';
import type { ListQuery } from '../../components/common/EntityList';
import VizGallery from '../../components/charts/VizGallery';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

export default function ChartList() {
  const navigate = useNavigate();
  const { can } = usePermission();
  const { t } = useI18n();
  const [reloadToken, setReloadToken] = useState(0);
  const [galleryOpen, setGalleryOpen] = useState(false);
  const [galleryViz, setGalleryViz] = useState<string | null>(null);

  const openGallery = () => {
    setGalleryViz(null);
    setGalleryOpen(true);
  };

  const confirmGallery = (vizType: string | null) => {
    setGalleryOpen(false);
    if (vizType) navigate(`/explore/?viz_type=${encodeURIComponent(vizType)}`);
  };

  const fetchCharts = (query: ListQuery): Promise<Paginated<Chart>> =>
    client.get<Paginated<Chart>>(endpoints.charts, {
      page: query.page,
      page_size: query.pageSize,
      q: query.search,
      order_column: 'changed_on',
      order_direction: 'desc',
    });

  const handleDelete = async (chart: Chart) => {
    try {
      await client.delete(endpoints.chart(chart.id));
      message.success(t('chart.list.delete_success', { name: chart.slice_name }));
      setReloadToken(value => value + 1);
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const columns: ColumnsType<Chart> = [
    {
      title: t('common.label.name'),
      dataIndex: 'slice_name',
      key: 'slice_name',
      render: (_, record) => (
        <a onClick={() => navigate(`/explore/?slice_id=${record.id}`)}>{record.slice_name}</a>
      ),
    },
    {
      title: t('chart.list.visual_type'),
      dataIndex: 'viz_type',
      key: 'viz_type',
      width: 150,
      render: (value: string) => <Tag bordered={false}>{value || 'table'}</Tag>,
    },
    {
      title: t('nav.datasets'),
      dataIndex: 'datasource_name',
      key: 'datasource_name',
      render: (value?: string) => <span className="cell-muted">{value || '—'}</span>,
    },
    {
      title: t('common.label.owner'),
      key: 'owners',
      width: 180,
      render: (_, record) => (
        <span className="cell-muted">
          {(record.owners ?? [])
            .map(
              owner =>
                owner.username ||
                [owner.first_name, owner.last_name].filter(Boolean).join(' ') ||
                owner.name,
            )
            .filter(Boolean)
            .join(', ') || '—'}
        </span>
      ),
    },
    {
      title: t('common.label.changed_on'),
      dataIndex: 'changed_on',
      key: 'changed_on',
      width: 180,
      render: (value?: string) => (
        <span className="cell-muted">
          {formatDateTimeShort(value)}
        </span>
      ),
    },
    {
      title: t('common.label.actions'),
      key: 'actions',
      width: 150,
      render: (_, record) => (
        <Space size={4}>
          <FavoriteStar className="slice" objId={record.id} />
          <Button
            size="small"
            type="text"
            icon={<EditOutlined />}
            onClick={() => navigate(`/explore/?slice_id=${record.id}`)}
          />
          {can('can_delete', 'Chart') ? (
            <Popconfirm
              title={t('chart.list.delete_title')}
              description={t('chart.list.delete_confirm', { name: record.slice_name })}
              okText={t('common.actions.delete')}
              cancelText={t('common.actions.cancel')}
              okButtonProps={{ danger: true }}
              onConfirm={() => void handleDelete(record)}
            >
              <Button size="small" type="text" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          ) : null}
        </Space>
      ),
    },
  ];

  return (
    <>
      <EntityList<Chart>
        title="Charts"
        subtitle={t('chart.list.subtitle')}
        columns={columns}
        fetchData={fetchCharts}
        rowKey={record => record.id}
        reloadToken={reloadToken}
        searchPlaceholder={t('chart.list.search_placeholder')}
        onRowClick={record => navigate(`/explore/?slice_id=${record.id}`)}
        extra={
          <Space>
            <Button icon={<AppstoreOutlined />} onClick={openGallery}>
              {t('viz.gallery.browse')}
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => navigate('/explore/')}
            >
              {t('chart.list.create')}
            </Button>
          </Space>
        }
      />

      <Modal
        open={galleryOpen}
        title={t('viz.gallery.title')}
        width={1090}
        onCancel={() => setGalleryOpen(false)}
        styles={{ body: { padding: 0 } }}
        footer={[
          <Button key="cancel" onClick={() => setGalleryOpen(false)}>
            {t('common.actions.cancel')}
          </Button>,
          <Button
            key="ok"
            type="primary"
            disabled={!galleryViz}
            onClick={() => confirmGallery(galleryViz)}
          >
            {t('viz.gallery.select')}
          </Button>,
        ]}
      >
        {galleryOpen ? (
          <VizGallery
            selectedViz={galleryViz}
            onChange={setGalleryViz}
            onDoubleClick={() => confirmGallery(galleryViz)}
          />
        ) : null}
      </Modal>
    </>
  );
}
