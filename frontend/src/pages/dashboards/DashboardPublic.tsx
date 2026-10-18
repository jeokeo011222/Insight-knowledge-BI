import { useCallback, useEffect, useRef, useState } from 'react';
import { useParams, useSearchParams } from 'react-router-dom';
import { Alert, Button, Input, Space, Spin, Tag, message } from 'antd';
import { DownloadOutlined, LockOutlined, ReloadOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage, guestTokenStore } from '../../api/client';
import type {
  Dashboard,
  DashboardChart,
  DashboardChartsResponse,
  DashboardDataset,
  Dataset,
  NativeFilterConfig,
} from '../../api/types';
import { getPublicDashboard, unlockPublicDashboard } from '../../api/share';
import type { PublicDashboard } from '../../api/share';
import EmptyState from '../../components/common/EmptyState';
import Watermark, { watermarkText } from '../../components/common/Watermark';
import Loading from '../../components/common/Loading';
import DashboardBody from './DashboardBody';
import { useI18n } from '../../i18n';
import { parseLayout } from '../../features/dashboard/layout';
import type {
  DashboardLayoutState,
  DashboardRow,
  DashboardTab,
} from '../../features/dashboard/layout';
import { parseNativeFilters } from '../../features/dashboard/filters';
import type { FilterValue, FilterValueMap } from '../../features/dashboard/filters';

const EMPTY_LAYOUT: DashboardLayoutState = {
  rows: [],
  header: { id: 'HEADER_ID', text: '', headerSize: 'LARGE_HEADER' },
  tabs: [],
  preserved: {},
};

/**
 * Anonymous dashboard viewer behind a public share link.
 *
 * The link itself is the credential: after unlock the backend hands back a
 * read-only guest token which every subsequent request carries as
 * `X-GuestToken`. That way the board renders through exactly the same layout
 * and filter code as the console, with no second render path to keep in sync.
 */
export default function DashboardPublic() {
  const { token = '' } = useParams<{ token: string }>();
  const [search] = useSearchParams();
  const embed = search.get('embed') === '1';
  const { t } = useI18n();

  const [meta, setMeta] = useState<PublicDashboard | null>(null);
  const [password, setPassword] = useState('');
  const [unlocking, setUnlocking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loadingMeta, setLoadingMeta] = useState(true);

  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [charts, setCharts] = useState<DashboardChart[]>([]);
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [layout, setLayout] = useState<DashboardLayoutState>(EMPTY_LAYOUT);
  const [savedRows, setSavedRows] = useState<DashboardRow[]>([]);
  const [savedTabs, setSavedTabs] = useState<DashboardTab[]>([]);
  const [filters, setFilters] = useState<NativeFilterConfig[]>([]);
  const [filterValues, setFilterValues] = useState<FilterValueMap>({});
  const [loadingBoard, setLoadingBoard] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const loadedFor = useRef<number | null>(null);

  // The token is the only credential this page has; a stale one from a
  // previous visit must never leak into the anonymous requests.
  useEffect(() => {
    guestTokenStore.clear();
    return () => guestTokenStore.clear();
  }, [token]);

  useEffect(() => {
    let cancelled = false;
    setLoadingMeta(true);
    setError(null);
    getPublicDashboard(token)
      .then(result => {
        if (cancelled) return;
        setMeta(result);
        if (result.guest_token) guestTokenStore.set(result.guest_token);
      })
      .catch(err => {
        if (!cancelled) setError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoadingMeta(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  const loadBoard = useCallback(
    async (dashboardId: number) => {
      setLoadingBoard(true);
      try {
        const entity = (
          await client.get<{ result: Dashboard }>(endpoints.dashboard(dashboardId), undefined, {
            anonymous: true,
          })
        ).result;
        setDashboard(entity);

        const [chartsResponse, datasetList] = await Promise.all([
          client.get<DashboardChartsResponse>(
            endpoints.dashboardCharts(dashboardId),
            undefined,
            { anonymous: true },
          ),
          client
            .get<{ result: DashboardDataset[] }>(endpoints.dashboardDatasets(dashboardId), undefined, {
              anonymous: true,
            })
            .then(response => response.result ?? [])
            .catch((): DashboardDataset[] => []),
        ]);
        const dashboardCharts = chartsResponse.result ?? [];
        const parsed = parseLayout(entity.position_json, dashboardCharts);
        setCharts(dashboardCharts);
        setLayout(parsed);
        setSavedRows(parsed.rows);
        setSavedTabs(parsed.tabs);

        const parsedFilters = parseNativeFilters(entity.json_metadata);
        setFilters(parsedFilters);

        const datasetIds = Array.from(
          new Set([
            ...datasetList.map(item => item.id),
            ...parsedFilters.flatMap(config => config.targets.map(target => target.datasetId)),
          ]),
        );
        const loaded = await Promise.all(
          datasetIds.map(id =>
            client
              .get<{ result: Dataset }>(endpoints.dataset(id), undefined, { anonymous: true })
              .then(response => response.result)
              .catch(() => null),
          ),
        );
        setDatasets(loaded.filter((item): item is Dataset => item !== null));

        const defaults: FilterValueMap = {};
        parsedFilters.forEach(config => {
          const value = config.defaultDataMask?.filterState?.value;
          if (value !== undefined && value !== null) defaults[config.id] = value as FilterValue;
        });
        setFilterValues(defaults);
      } catch (err) {
        setError(errorMessage(err));
      } finally {
        setLoadingBoard(false);
      }
    },
    [],
  );

  useEffect(() => {
    if (!meta?.guest_token || !meta.dashboard_id) return;
    if (loadedFor.current === meta.dashboard_id) return;
    loadedFor.current = meta.dashboard_id;
    void loadBoard(meta.dashboard_id);
  }, [meta, loadBoard]);

  useEffect(() => {
    if (!refreshing) return;
    const timer = window.setTimeout(() => setRefreshing(false), 800);
    return () => window.clearTimeout(timer);
  }, [refreshing]);

  const handleUnlock = async () => {
    if (!password) return;
    setUnlocking(true);
    try {
      const result = await unlockPublicDashboard(token, password);
      if (result.guest_token) {
        guestTokenStore.set(result.guest_token);
        setMeta(result);
        setPassword('');
      }
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setUnlocking(false);
    }
  };

  const handleDownload = (format: 'png' | 'pdf') => {
    if (!meta?.dashboard_id) return;
    // The export endpoint needs a bearer token, so a public viewer gets the
    // browser's own print pipeline instead of a server render.
    if (format === 'pdf') {
      window.print();
      return;
    }
    message.info(t('dashboard.public.export_hint'));
  };

  if (loadingMeta) {
    return (
      <div className="public-gate">
        <Spin />
      </div>
    );
  }

  if (error) {
    return (
      <div className="public-gate">
        <EmptyState description={error} />
      </div>
    );
  }

  if (meta?.requires_password) {
    return (
      <div className="public-gate">
        <div className="public-gate__card">
          <h2>
            <LockOutlined /> {meta.title || t('dashboard.public.locked_title')}
          </h2>
          <div className="public-gate__hint">{t('dashboard.public.locked_hint')}</div>
          <Input.Password
            value={password}
            onChange={event => setPassword(event.target.value)}
            onPressEnter={() => void handleUnlock()}
            placeholder={t('dashboard.public.password_placeholder')}
            autoFocus
          />
          <Button type="primary" block loading={unlocking} onClick={() => void handleUnlock()}>
            {t('dashboard.public.unlock')}
          </Button>
          {meta.expires_at ? (
            <div className="public-gate__hint">
              {t('dashboard.public.expires_at', { at: meta.expires_at.slice(0, 16).replace('T', ' ') })}
            </div>
          ) : null}
        </div>
      </div>
    );
  }

  const canExport = meta?.can_export ?? meta?.allow_export ?? false;

  return (
    <div className="public-view">
      {embed ? null : (
        <div className="public-view__topbar">
          <div>
            <div className="public-view__title">{meta?.title || dashboard?.dashboard_title}</div>
            <div className="public-view__subtitle">
              {meta?.description || t('dashboard.public.subtitle')}
            </div>
          </div>
          <Space size={8} wrap>
            {canExport ? (
              <>
                <Button size="small" icon={<DownloadOutlined />} onClick={() => handleDownload('pdf')}>
                  {t('dashboard.export.pdf')}
                </Button>
                <Button size="small" onClick={() => handleDownload('png')}>
                  {t('dashboard.export.png')}
                </Button>
              </>
            ) : (
              <Tag>{t('dashboard.public.export_disabled')}</Tag>
            )}
            <Button
              size="small"
              icon={<ReloadOutlined />}
              loading={refreshing}
              onClick={() => {
                setRefreshing(true);
                setRefreshKey(key => key + 1);
              }}
            >
              {t('common.actions.refresh')}
            </Button>
          </Space>
        </div>
      )}

      <div className="public-view__body">
        {meta?.expires_at ? (
          <Alert
            type="info"
            showIcon
            style={{ marginBottom: 12 }}
            message={t('dashboard.public.expiry_notice', {
              at: meta.expires_at.slice(0, 16).replace('T', ' '),
            })}
          />
        ) : null}

        {loadingBoard && !dashboard ? (
          <Loading height={360} tip={t('common.state.loading')} />
        ) : (
          <DashboardBody
            canWrite={false}
            publicView
            canExport={canExport}
            layout={layout}
            savedRows={savedRows}
            savedTabs={savedTabs}
            chartsById={new Map(charts.map(chart => [chart.id, chart]))}
            filters={filters}
            filterValues={filterValues}
            datasets={datasets}
            initialEditMode={false}
            refreshKey={refreshKey}
            onRowsChange={() => undefined}
            onLayoutChange={() => undefined}
            onSaveLayout={async () => undefined}
            onAddCharts={() => undefined}
            onAddFilter={async () => undefined}
            onRemoveFilter={async () => undefined}
            onFilterChange={(filterId, value) =>
              setFilterValues(previous => ({ ...previous, [filterId]: value ?? undefined }))
            }
          />
        )}
      </div>

      <Watermark text={watermarkText(t('dashboard.watermark.guest'))} opacity={0.05} />
    </div>
  );
}
