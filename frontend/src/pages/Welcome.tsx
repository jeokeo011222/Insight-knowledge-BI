import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Card, Col, Empty, Row, Space, Tag, Typography } from 'antd';
import {
  BarChartOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  RobotOutlined,
  StarFilled,
  TableOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { client, endpoints, errorMessage } from '../api/client';
import { listAgents } from '../api/ai';
import type { Chart, Dashboard, Dataset, Paginated } from '../api/types';
import PageHeader from '../components/common/PageHeader';
import Loading from '../components/common/Loading';
import { useAppSelector } from '../store';
import { getFavoriteRows, loadFavorites } from '../features/favorites/store';
import type { FavoriteRow } from '../features/favorites/store';
import { useI18n } from '../i18n';

interface FavoriteItem {
  key: string;
  name: string;
  type: 'chart' | 'dashboard';
  to: string;
}

interface Overview {
  dashboards: Dashboard[];
  charts: Chart[];
  datasets: Dataset[];
  favorites: FavoriteItem[];
  counts: { dashboards: number; charts: number; datasets: number; agents: number | null };
  errors: string[];
}

const emptyOverview: Overview = {
  dashboards: [],
  charts: [],
  datasets: [],
  favorites: [],
  counts: { dashboards: 0, charts: 0, datasets: 0, agents: null },
  errors: [],
};

/** Max favourites shown on the landing page. */
const FAVORITE_LIMIT = 6;

/**
 * Resolves favourite ids into clickable names.
 *
 * The favstar rows only carry class_name + obj_id, so each entry costs one
 * lookup. The list is capped at FAVORITE_LIMIT, which keeps the landing page at
 * a handful of requests even for a heavily starred user.
 */
async function resolveFavorites(rows: FavoriteRow[]): Promise<FavoriteItem[]> {
  const picked = rows.slice(0, FAVORITE_LIMIT);
  const resolved = await Promise.all(
    picked.map(async row => {
      try {
        if (row.class_name === 'slice') {
          const response = await client.get<{ result: Chart }>(endpoints.chart(row.obj_id));
          return {
            key: `chart-${row.obj_id}`,
            name: response.result?.slice_name ?? `#${row.obj_id}`,
            type: 'chart' as const,
            to: `/explore/?slice_id=${row.obj_id}`,
          };
        }
        if (row.class_name === 'dashboard') {
          const response = await client.get<{ result: Dashboard }>(endpoints.dashboard(row.obj_id));
          return {
            key: `dashboard-${row.obj_id}`,
            name: response.result?.dashboard_title ?? `#${row.obj_id}`,
            type: 'dashboard' as const,
            to: `/dashboard/${row.obj_id}/`,
          };
        }
        return null;
      } catch {
        // A deleted object leaves a dangling favourite: hide it rather than
        // showing a dead link.
        return null;
      }
    }),
  );
  return resolved.filter((item): item is FavoriteItem => item !== null);
}

export default function Welcome() {
  const me = useAppSelector(state => state.user.me);
  const { t } = useI18n();
  const [data, setData] = useState<Overview>(emptyOverview);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const errors: string[] = [];

    Promise.allSettled([
      client.get<Paginated<Dashboard>>(endpoints.dashboards, {
        page: 1,
        page_size: 5,
        order_column: 'changed_on',
        order_direction: 'desc',
      }),
      client.get<Paginated<Chart>>(endpoints.charts, {
        page: 1,
        page_size: 5,
        order_column: 'changed_on',
        order_direction: 'desc',
      }),
      client.get<Paginated<Dataset>>(endpoints.datasets, { page: 1, page_size: 5 }),
      // Agent listing is permission-gated; a refusal silently leaves the count
      // blank instead of surfacing an error on the landing page.
      listAgents(),
    ]).then(([dashboards, charts, datasets, agents]) => {
      if (cancelled) return;
      const next: Overview = { ...emptyOverview, counts: { ...emptyOverview.counts } };
      if (dashboards.status === 'fulfilled') {
        next.dashboards = dashboards.value.result ?? [];
        next.counts.dashboards = dashboards.value.count ?? 0;
      } else {
        errors.push(`Dashboards: ${errorMessage(dashboards.reason)}`);
      }
      if (charts.status === 'fulfilled') {
        next.charts = charts.value.result ?? [];
        next.counts.charts = charts.value.count ?? 0;
      } else {
        errors.push(`Charts: ${errorMessage(charts.reason)}`);
      }
      if (datasets.status === 'fulfilled') {
        next.datasets = datasets.value.result ?? [];
        next.counts.datasets = datasets.value.count ?? 0;
      } else {
        errors.push(`Datasets: ${errorMessage(datasets.reason)}`);
      }
      if (agents.status === 'fulfilled') {
        next.counts.agents = agents.value.length;
      }
      next.errors = errors;
      setData(next);
      setLoading(false);
      // Favourites resolve in the background so a slow lookup cannot delay the
      // rest of the page.
      void loadFavorites()
        .then(async () => {
          const favorites = await resolveFavorites(getFavoriteRows());
          if (!cancelled) setData(previous => ({ ...previous, favorites }));
        })
        .catch(() => undefined);
    });

    return () => {
      cancelled = true;
    };
  }, []);

  const displayName =
    [me?.first_name, me?.last_name].filter(Boolean).join(' ') || me?.username || '';

  const kpis = [
    {
      label: t('nav.dashboards'),
      value: data.counts.dashboards,
      icon: <DashboardOutlined />,
      to: '/dashboard/list/',
      color: 'var(--brand-primary)',
    },
    {
      label: t('nav.charts'),
      value: data.counts.charts,
      icon: <BarChartOutlined />,
      to: '/chart/list/',
      color: 'var(--color-info)',
    },
    {
      label: t('nav.datasets'),
      value: data.counts.datasets,
      icon: <TableOutlined />,
      to: '/tablemodelview/list/',
      color: 'var(--color-success)',
    },
    {
      label: t('welcome.kpi.agents'),
      value: data.counts.agents ?? '—',
      icon: <RobotOutlined />,
      to: '/settings/ai',
      color: 'var(--color-warning, #faad14)',
    },
  ];

  return (
    <div>
      <PageHeader
        title={displayName ? t('login.welcome_back', { name: displayName }) : t('welcome.greet')}
        subtitle={t('welcome.subtitle')}
        extra={
          <Space>
            <Link to="/sqllab/">
              <Button icon={<DatabaseOutlined />}>SQL Lab</Button>
            </Link>
            <Link to="/explore/">
              <Button type="primary" icon={<BarChartOutlined />}>
                {t('welcome.create_chart')}
              </Button>
            </Link>
          </Space>
        }
      />

      {loading ? <Loading height={240} /> : null}

      {!loading && data.errors.length ? (
        <div className="cell-muted" style={{ marginBottom: 12 }}>
          {data.errors.join(' · ')}
        </div>
      ) : null}

      {!loading ? (
        <>
          <Row gutter={16} style={{ marginBottom: 20 }}>
            {kpis.map(kpi => (
              <Col xs={24} sm={12} lg={6} key={kpi.label}>
                <Link to={kpi.to}>
                  <div className="kpi-card" style={{ display: 'flex', alignItems: 'center', gap: 14 }}>
                    <span
                      style={{
                        width: 38,
                        height: 38,
                        borderRadius: 6,
                        background: `${kpi.color}14`,
                        color: kpi.color,
                        display: 'inline-flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        fontSize: 18,
                      }}
                    >
                      {kpi.icon}
                    </span>
                    <div>
                      <div className="kpi-card__label">{kpi.label}</div>
                      <div className="kpi-card__value">{kpi.value}</div>
                    </div>
                  </div>
                </Link>
              </Col>
            ))}
          </Row>

          <Card
            title={
              <Space size={6}>
                <StarFilled style={{ color: '#faad14' }} />
                {t('welcome.favorites')}
              </Space>
            }
            size="small"
            className="zg-card"
            style={{ marginBottom: 16 }}
            styles={{ body: { padding: data.favorites.length ? 8 : 24 } }}
          >
            {data.favorites.length ? (
              <Space size={8} wrap>
                {data.favorites.map(item => (
                  <Link key={item.key} to={item.to}>
                    <Tag
                      bordered={false}
                      icon={item.type === 'chart' ? <BarChartOutlined /> : <DashboardOutlined />}
                      style={{ padding: '4px 10px', fontSize: 12.5, cursor: 'pointer' }}
                    >
                      {item.name}
                    </Tag>
                  </Link>
                ))}
              </Space>
            ) : (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t('welcome.favorites.empty')}
              />
            )}
          </Card>

          <Row gutter={16}>
            <Col xs={24} lg={8}>
              <Card
                title={t('welcome.recent.dashboards')}
                size="small"
                extra={<Link to="/dashboard/list/">{t('common.label.all')}</Link>}
                className="zg-card"
                styles={{ body: { padding: 0 } }}
              >
                {data.dashboards.length ? (
                  data.dashboards.map(dashboard => (
                    <Link
                      key={dashboard.id}
                      to={`/dashboard/${dashboard.id}/`}
                      className="welcome-list__row"
                    >
                      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                        <Typography.Text strong ellipsis style={{ fontSize: 13 }}>
                          {dashboard.dashboard_title}
                        </Typography.Text>
                        <Tag color={dashboard.published ? 'green' : 'default'}>
                          {dashboard.published ? t('common.state.published') : t('common.state.draft')}
                        </Tag>
                      </div>
                      <div className="cell-muted" style={{ marginTop: 3 }}>
                        {dashboard.changed_on
                          ? dayjs(dashboard.changed_on).format('YYYY-MM-DD HH:mm')
                          : '—'}
                        {dashboard.chart_count !== undefined
                          ? ` · ${t('welcome.count.charts', { count: dashboard.chart_count })}`
                          : ''}
                      </div>
                    </Link>
                  ))
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t('welcome.empty.dashboards')}
                    style={{ padding: 24 }}
                  />
                )}
              </Card>
            </Col>

            <Col xs={24} lg={8}>
              <Card
                title={t('welcome.recent.charts')}
                size="small"
                extra={<Link to="/chart/list/">{t('common.label.all')}</Link>}
                className="zg-card"
                styles={{ body: { padding: 0 } }}
              >
                {data.charts.length ? (
                  data.charts.map(chart => (
                    <Link
                      key={chart.id}
                      to={`/explore/?slice_id=${chart.id}`}
                      className="welcome-list__row"
                    >
                      <Typography.Text strong ellipsis style={{ fontSize: 13 }}>
                        {chart.slice_name}
                      </Typography.Text>
                      <div className="cell-muted" style={{ marginTop: 3 }}>
                        <Tag bordered={false}>{chart.viz_type}</Tag>
                        {chart.datasource_name || '—'}
                      </div>
                    </Link>
                  ))
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t('welcome.empty.charts')}
                    style={{ padding: 24 }}
                  />
                )}
              </Card>
            </Col>

            <Col xs={24} lg={8}>
              <Card
                title={t('welcome.recent.datasets')}
                size="small"
                extra={<Link to="/tablemodelview/list/">{t('common.label.all')}</Link>}
                className="zg-card"
                styles={{ body: { padding: 0 } }}
              >
                {data.datasets.length ? (
                  data.datasets.map(dataset => (
                    <div key={dataset.id} className="welcome-list__row">
                      <Typography.Text strong ellipsis style={{ fontSize: 13 }}>
                        {dataset.table_name}
                      </Typography.Text>
                      <div className="cell-muted" style={{ marginTop: 3 }}>
                        {dataset.database?.database_name || '—'}
                        {dataset.schema ? ` · ${dataset.schema}` : ''}
                        {` · ${t('welcome.count.metrics', { count: dataset.metrics?.length ?? 0 })}`}
                      </div>
                    </div>
                  ))
                ) : (
                  <Empty
                    image={Empty.PRESENTED_IMAGE_SIMPLE}
                    description={t('welcome.empty.datasets')}
                    style={{ padding: 24 }}
                  />
                )}
              </Card>
            </Col>
          </Row>
        </>
      ) : null}
    </div>
  );
}
