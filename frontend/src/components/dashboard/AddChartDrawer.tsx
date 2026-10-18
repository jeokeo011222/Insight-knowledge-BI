import { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Checkbox,
  Drawer,
  Empty,
  Input,
  List,
  Modal,
  Space,
  Tag,
  message,
} from 'antd';
import { AppstoreOutlined, SearchOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Chart, Paginated } from '../../api/types';
import Loading from '../common/Loading';
import VizGallery from '../charts/VizGallery';
import { useI18n } from '../../i18n';

export interface AddChartDrawerProps {
  open: boolean;
  existingIds: number[];
  onClose: () => void;
  onAdd: (charts: Chart[]) => void;
}

/** Picks saved charts and appends them to the layout row in focus. */
export default function AddChartDrawer({
  open,
  existingIds,
  onClose,
  onAdd,
}: AddChartDrawerProps) {
  const { t } = useI18n();
  const [charts, setCharts] = useState<Chart[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<number[]>([]);
  // Viz type picked from the gallery, narrowing the saved-chart list.
  const [vizFilter, setVizFilter] = useState<string | null>(null);
  const [galleryOpen, setGalleryOpen] = useState(false);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    client
      .get<Paginated<Chart>>(endpoints.charts, { page: 1, page_size: 200 })
      .then(response => {
        if (!cancelled) setCharts(response.result ?? []);
      })
      .catch(err => {
        if (!cancelled) setError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      setSelected([]);
      setSearch('');
      setVizFilter(null);
      setGalleryOpen(false);
    }
  }, [open]);

  const visible = useMemo(() => {
    const term = search.trim().toLowerCase();
    return charts.filter(chart => {
      if (vizFilter && chart.viz_type !== vizFilter) return false;
      if (!term) return true;
      return (
        chart.slice_name.toLowerCase().includes(term) ||
        chart.viz_type.toLowerCase().includes(term) ||
        (chart.datasource_name ?? '').toLowerCase().includes(term)
      );
    });
  }, [charts, search, vizFilter]);

  const handleAdd = () => {
    const picked = charts.filter(chart => selected.includes(chart.id));
    if (!picked.length) {
      message.warning(t('dashboard.add_chart.select_at_least_one'));
      return;
    }
    onAdd(picked);
    setSelected([]);
    onClose();
  };

  return (
    <Drawer
      title={t('dashboard.add_chart.title')}
      width={420}
      open={open}
      onClose={onClose}
      extra={
        <Space>
          <Button onClick={onClose}>{t('common.actions.cancel')}</Button>
          <Button type="primary" onClick={handleAdd} disabled={!selected.length}>
            {selected.length
              ? t('dashboard.add_chart.submit_count', { count: selected.length })
              : t('dashboard.add_chart.submit')}
          </Button>
        </Space>
      }
    >
      <Space style={{ width: '100%', marginBottom: 12 }} align="start">
        <Input
          allowClear
          prefix={<SearchOutlined />}
          placeholder={t('dashboard.add_chart.search_placeholder')}
          value={search}
          onChange={event => setSearch(event.target.value)}
          style={{ width: 236 }}
        />
        <Button icon={<AppstoreOutlined />} onClick={() => setGalleryOpen(true)}>
          {t('viz.gallery.browse')}
        </Button>
      </Space>

      {vizFilter ? (
        <div style={{ marginBottom: 8 }}>
          <Tag bordered={false} closable onClose={() => setVizFilter(null)}>
            {vizFilter}
          </Tag>
        </div>
      ) : null}

      {loading ? <Loading height={200} tip={t('common.state.loading_chart')} /> : null}
      {!loading && error ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={error} />
      ) : null}
      {!loading && !error && !visible.length ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('common.state.no_matching_charts')} />
      ) : null}

      {!loading && !error && visible.length ? (
        <Checkbox.Group
          style={{ display: 'block' }}
          value={selected}
          onChange={values => setSelected(values.map(Number))}
        >
          <List
            size="small"
            dataSource={visible}
            renderItem={chart => {
              const placed = existingIds.includes(chart.id);
              return (
                <List.Item>
                  <Space align="start" style={{ width: '100%' }}>
                    <Checkbox value={chart.id} disabled={placed} />
                    <div style={{ flex: '1 1 auto', minWidth: 0 }}>
                      <div className="chart-card__title" style={{ fontWeight: 500 }}>
                        {chart.slice_name}
                      </div>
                      <Space size={6} wrap style={{ marginTop: 4 }}>
                        <Tag bordered={false}>{chart.viz_type}</Tag>
                        <span className="cell-muted">
                          {chart.datasource_name || `dataset #${chart.datasource_id}`}
                        </span>
                        {placed ? <Tag color="green">{t('dashboard.add_chart.already_placed')}</Tag> : null}
                      </Space>
                    </div>
                  </Space>
                </List.Item>
              );
            }}
          />
        </Checkbox.Group>
      ) : null}

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
            disabled={!vizFilter}
            onClick={() => setGalleryOpen(false)}
          >
            {t('viz.gallery.select')}
          </Button>,
        ]}
      >
        {galleryOpen ? (
          <VizGallery
            selectedViz={vizFilter}
            onChange={setVizFilter}
            onDoubleClick={() => setGalleryOpen(false)}
          />
        ) : null}
      </Modal>
    </Drawer>
  );
}
