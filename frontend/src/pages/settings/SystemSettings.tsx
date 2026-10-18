import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Empty,
  Row,
  Space,
  Table,
  Tabs,
  Tag,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  CheckCircleFilled,
  ClockCircleOutlined,
  CloseCircleFilled,
  MinusCircleFilled,
  ReloadOutlined,
  WarningFilled,
} from '@ant-design/icons';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { client, endpoints, errorMessage } from '../../api/client';
import type {
  ModuleStatus,
  SystemDatabaseItem,
  SystemModule,
  SystemOverview,
} from '../../api/types';
import PageHeader from '../../components/common/PageHeader';
import { useI18n } from '../../i18n';
import ThemeList from './ThemeList';

type TabKey = 'overview' | 'theme';

const TAB_KEYS: TabKey[] = ['overview', 'theme'];

/**
 * 系统管理: the operator console. Two tabs mirror the way domestic admin
 * products organise this screen — a status dashboard first (模块运行 / 数据源),
 * then the theme library.
 */
export default function SystemSettings() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const requested = params.get('tab') as TabKey | null;
  const active: TabKey = requested && TAB_KEYS.includes(requested) ? requested : 'overview';

  return (
    <>
      <PageHeader title={t('nav.system')} subtitle={t('settings.system.subtitle')} />
      <Tabs
        activeKey={active}
        onChange={key => setParams({ tab: key }, { replace: true })}
        items={[
          {
            key: 'overview',
            label: t('settings.system.tab.overview'),
            children: <OverviewPanel />,
          },
          {
            key: 'theme',
            label: t('settings.system.tab.theme'),
            children: <ThemeList embedded />,
          },
        ]}
      />
    </>
  );
}

/* ------------------------------------------------------------- status atoms */

const STATUS_ICON: Record<ModuleStatus | 'unknown', React.ReactNode> = {
  ok: <CheckCircleFilled style={{ color: 'var(--status-success, #52c41a)' }} />,
  warn: <WarningFilled style={{ color: 'var(--status-warning, #faad14)' }} />,
  down: <CloseCircleFilled style={{ color: 'var(--status-error, #ff4d4f)' }} />,
  off: <MinusCircleFilled style={{ color: 'var(--color-text-tertiary, #bfbfbf)' }} />,
  unknown: <ClockCircleOutlined style={{ color: 'var(--color-text-tertiary, #bfbfbf)' }} />,
};

const STATUS_BADGE: Record<ModuleStatus | 'unknown', 'success' | 'warning' | 'error' | 'default'> = {
  ok: 'success',
  warn: 'warning',
  down: 'error',
  off: 'default',
  unknown: 'default',
};

function statusLabel(
  t: (key: 'settings.system.status.ok') => string,
  status: ModuleStatus | 'unknown',
): string {
  return t(`settings.system.status.${status}` as 'settings.system.status.ok');
}

function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分`;
  return `${m} 分`;
}

/* ------------------------------------------------------------------ overview */

function OverviewPanel() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [data, setData] = useState<SystemOverview | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await client.get<{ result: SystemOverview }>(endpoints.systemOverview);
      setData(response.result);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const modules = data?.modules ?? [];
  const healthy = modules.filter(m => m.status === 'ok').length;
  const activeModules = modules.filter(m => m.status !== 'off').length;
  const dbSummary = data?.databases;
  const counts = data?.counts;

  const moduleColumns = useMemo<ColumnsType<SystemModule>>(
    () => [
      {
        title: t('settings.system.module.name'),
        dataIndex: 'name',
        key: 'name',
        width: 150,
        render: (name: string, row) => (
          <Space size={8}>
            <Badge status={STATUS_BADGE[row.status]} />
            <span style={{ fontWeight: 500 }}>{name}</span>
          </Space>
        ),
      },
      {
        title: t('settings.system.module.status'),
        dataIndex: 'status',
        key: 'status',
        width: 110,
        render: (status: ModuleStatus) => (
          <Space size={6}>
            {STATUS_ICON[status]}
            <span>{statusLabel(t, status)}</span>
          </Space>
        ),
      },
      {
        title: t('settings.system.module.detail'),
        dataIndex: 'detail',
        key: 'detail',
        ellipsis: true,
        render: (detail: string) => <span className="cell-muted">{detail}</span>,
      },
      {
        title: t('settings.system.module.metric'),
        key: 'metric',
        width: 130,
        align: 'right',
        render: (_, row) =>
          row.unit ? (
            <span>
              <strong>{row.metric}</strong> <span className="cell-muted">{row.unit}</span>
            </span>
          ) : (
            <span className="cell-muted">—</span>
          ),
      },
      {
        title: t('settings.system.module.latency'),
        dataIndex: 'latency_ms',
        key: 'latency_ms',
        width: 100,
        align: 'right',
        render: (value: number) =>
          value > 0 ? <span className="cell-muted">{value} ms</span> : <span className="cell-muted">—</span>,
      },
    ],
    [t],
  );

  const engineColumns = useMemo<ColumnsType<SystemDatabaseItem>>(
    () => [
      {
        title: t('settings.system.engine.name'),
        dataIndex: 'name',
        key: 'name',
        render: (name: string) => <span style={{ fontWeight: 500 }}>{name}</span>,
      },
      {
        title: t('settings.system.engine.engine'),
        dataIndex: 'engine',
        key: 'engine',
        width: 140,
        render: (engine: string) => (
          <Tag bordered={false} className="cell-mono">
            {engine || '—'}
          </Tag>
        ),
      },
      {
        title: t('settings.system.engine.status'),
        dataIndex: 'status',
        key: 'status',
        width: 120,
        render: (status: 'ok' | 'down' | 'unknown', row) => (
          <Tooltip title={row.error || undefined}>
            <Space size={6}>
              {STATUS_ICON[status]}
              <span>{statusLabel(t, status)}</span>
            </Space>
          </Tooltip>
        ),
      },
      {
        title: t('settings.system.engine.latency'),
        dataIndex: 'latency_ms',
        key: 'latency_ms',
        width: 110,
        align: 'right',
        render: (value: number, row) =>
          row.status === 'unknown' ? (
            <span className="cell-muted">—</span>
          ) : (
            <span className="cell-muted">{value} ms</span>
          ),
      },
      {
        title: t('settings.system.engine.checked_at'),
        dataIndex: 'checked_at',
        key: 'checked_at',
        width: 170,
        render: (value: string) =>
          value ? (
            <span className="cell-muted">{new Date(value).toLocaleString()}</span>
          ) : (
            <span className="cell-muted">{t('settings.system.engine.never')}</span>
          ),
      },
    ],
    [t],
  );

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {data?.warnings?.length ? (
        <Alert
          type="warning"
          showIcon
          message={t('settings.system.warning_title')}
          description={
            <ul className="sys-warning-list">
              {data.warnings.map(warning => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      {/* KPI strip — the first thing a console user reads. */}
      <Row gutter={[16, 16]}>
        <Col xs={24} sm={12} xl={6}>
          <Card className="sys-kpi" loading={loading && !data}>
            <div className="sys-kpi__label">{t('settings.system.kpi.runtime')}</div>
            <div className="sys-kpi__value">
              {data ? formatUptime(data.app.uptime_sec) : '—'}
            </div>
            <div className="sys-kpi__hint">
              {t('settings.system.kpi.version')} {data?.app.version ?? '—'}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card className="sys-kpi" loading={loading && !data}>
            <div className="sys-kpi__label">{t('settings.system.kpi.modules')}</div>
            <div className="sys-kpi__value">
              <span className="sys-kpi__num">{healthy}</span>
              <span className="sys-kpi__den">/ {activeModules}</span>
            </div>
            <div className="sys-kpi__hint">
              {modules.some(m => m.status === 'down')
                ? t('settings.system.kpi.modules_down')
                : modules.some(m => m.status === 'warn')
                  ? t('settings.system.kpi.modules_warn')
                  : t('settings.system.kpi.modules_ok')}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card className="sys-kpi" loading={loading && !data}>
            <div className="sys-kpi__label">{t('settings.system.kpi.engines')}</div>
            <div className="sys-kpi__value">
              <span className="sys-kpi__num">{dbSummary?.ok ?? 0}</span>
              <span className="sys-kpi__den">/ {dbSummary?.total ?? 0}</span>
            </div>
            <div className="sys-kpi__hint">
              {dbSummary && dbSummary.failing > 0
                ? t('settings.system.kpi.engines_failing', { n: dbSummary.failing })
                : t('settings.system.kpi.engines_ok')}
            </div>
          </Card>
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <Card className="sys-kpi" loading={loading && !data}>
            <div className="sys-kpi__label">{t('settings.system.kpi.assets')}</div>
            <div className="sys-kpi__value">
              <span className="sys-kpi__num">{counts?.dashboards ?? 0}</span>
              <span className="sys-kpi__den">/ {counts?.charts ?? 0}</span>
            </div>
            <div className="sys-kpi__hint">{t('settings.system.kpi.assets_hint')}</div>
          </Card>
        </Col>
      </Row>

      <Card
        title={t('settings.system.card.modules')}
        extra={
          <Button icon={<ReloadOutlined />} size="small" loading={loading} onClick={() => void load()}>
            {t('common.actions.refresh')}
          </Button>
        }
        styles={{ body: { padding: 0 } }}
      >
        <Table<SystemModule>
          rowKey="key"
          size="small"
          columns={moduleColumns}
          dataSource={modules}
          pagination={false}
          loading={loading}
        />
      </Card>

      <Row gutter={[16, 16]}>
        <Col xs={24} xl={14}>
          <Card
            title={t('settings.system.card.engines')}
            extra={
              <Space size={8}>
                <Button size="small" onClick={() => navigate('/databaseview/list/')}>
                  {t('settings.system.engine.manage')}
                </Button>
              </Space>
            }
            styles={{ body: { padding: 0 } }}
          >
            <Table<SystemDatabaseItem>
              rowKey="id"
              size="small"
              columns={engineColumns}
              dataSource={dbSummary?.items ?? []}
              pagination={false}
              loading={loading}
              locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
            />
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card title={t('settings.system.card.app')} loading={loading && !data}>
            <Descriptions column={1} size="small" colon={false}>
              <Descriptions.Item label={t('settings.system.app.version')}>
                {data?.app.version ?? '—'}
              </Descriptions.Item>
              <Descriptions.Item label={t('settings.system.app.go')}>
                {data?.app.go_version ?? '—'}
              </Descriptions.Item>
              <Descriptions.Item label={t('settings.system.app.started')}>
                {data ? new Date(data.app.started_at).toLocaleString() : '—'}
              </Descriptions.Item>
              <Descriptions.Item label={t('settings.system.app.host')}>
                {data?.app.hostname ?? '—'} <span className="cell-muted"># {data?.app.pid ?? '—'}</span>
              </Descriptions.Item>
              <Descriptions.Item label={t('settings.system.app.config')}>
                <span className="cell-mono cell-muted">{data?.app.config_path || '—'}</span>
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>

      <Card title={t('settings.system.card.assets')} loading={loading && !data}>
        <Row gutter={[16, 16]}>
          {[
            { key: 'users', label: t('settings.system.asset.users'), value: counts?.users },
            { key: 'roles', label: t('settings.system.asset.roles'), value: counts?.roles },
            { key: 'dashboards', label: t('settings.system.asset.dashboards'), value: counts?.dashboards },
            { key: 'charts', label: t('settings.system.asset.charts'), value: counts?.charts },
            { key: 'datasets', label: t('settings.system.asset.datasets'), value: counts?.datasets },
            { key: 'screens', label: t('settings.system.asset.screens'), value: counts?.screens },
            { key: 'queries', label: t('settings.system.asset.queries'), value: counts?.queries_24h },
          ].map(item => (
            <Col xs={12} sm={8} md={6} xl={3} key={item.key}>
              <div className="sys-asset">
                <div className="sys-asset__value">{item.value ?? 0}</div>
                <div className="sys-asset__label">{item.label}</div>
              </div>
            </Col>
          ))}
        </Row>
      </Card>
    </Space>
  );
}
