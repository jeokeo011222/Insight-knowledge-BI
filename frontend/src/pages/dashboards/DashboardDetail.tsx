import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Button, Space, message } from 'antd';
import { ReloadOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type {
  Chart,
  Dashboard,
  DashboardChart,
  DashboardChartsResponse,
  DashboardDataset,
  Dataset,
  JsonObject,
  NativeFilterConfig,
  Paginated,
} from '../../api/types';
import Loading from '../../components/common/Loading';
import EmptyState from '../../components/common/EmptyState';
import AgentPanel from '../../components/ai/AgentPanel';
import { DashboardHeader } from '../../components/dashboard/DashboardToolbar';
import DashboardBody from './DashboardBody';
import { useAppSelector, usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { parseJsonObject, toDashboardCharts } from '../../features/dashboard/dataMask';
import { appendCharts, buildPositionJson, parseLayout } from '../../features/dashboard/layout';
import type {
  DashboardLayoutState,
  DashboardRow,
  DashboardTab,
} from '../../features/dashboard/layout';
import {
  buildFilterDataMask,
  parseFilterState,
  parseNativeFilters,
  readExternalFilters,
  readUrlState,
} from '../../features/dashboard/filters';
import type { FilterValue, FilterValueMap } from '../../features/dashboard/filters';

export default function DashboardDetail() {
  const { id } = useParams<{ id: string }>();
  const { t, lang } = useI18n();
  const navigate = useNavigate();
  const { can } = usePermission();
  const canWrite = can('can_write', 'Dashboard');
  const username = useAppSelector(state => state.user.me?.username ?? '');

  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [charts, setCharts] = useState<DashboardChart[]>([]);
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [layout, setLayout] = useState<DashboardLayoutState>({
    rows: [],
    header: { id: 'HEADER_ID', text: '', headerSize: 'LARGE_HEADER' },
    tabs: [],
    preserved: {},
  });
  const [savedRows, setSavedRows] = useState<DashboardRow[]>([]);
  const [savedTabs, setSavedTabs] = useState<DashboardTab[]>([]);
  const [filters, setFilters] = useState<NativeFilterConfig[]>([]);
  const [filterValues, setFilterValues] = useState<FilterValueMap>({});
  const [initialEdit, setInitialEdit] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);
    try {
      let entity: Dashboard | null = null;
      if (/^\d+$/.test(id)) {
        entity = (await client.get<{ result: Dashboard }>(endpoints.dashboard(id))).result;
      } else {
        const response = await client.get<Paginated<Dashboard>>(endpoints.dashboards, {
          page: 1,
          page_size: 100,
          q: id,
        });
        entity =
          (response.result ?? []).find(
            item => item.slug === id || String(item.id) === id,
          ) ?? null;
      }
      if (!entity) {
        setError(t('dashboard.detail.not_found', { id }));
        setDashboard(null);
        setCharts([]);
        return;
      }
      setDashboard(entity);

      const [chartsResponse, datasetList] = await Promise.all([
        client.get<DashboardChartsResponse>(endpoints.dashboardCharts(entity.id)),
        client
          .get<{ result: DashboardDataset[] }>(endpoints.dashboardDatasets(entity.id))
          .then(response => response.result ?? [])
          .catch((): DashboardDataset[] => []),
      ]);
      const dashboardCharts = chartsResponse.result ?? [];
      const parsed = parseLayout(entity.position_json, dashboardCharts);
      const placedIds = Array.from(
        new Set(
          parsed.rows.flatMap(row =>
            row.tiles.filter(tile => tile.kind === 'chart').map(tile => tile.chartId),
          ),
        ),
      );
      const missingIds = placedIds.filter(
        chartId => !dashboardCharts.some(chart => chart.id === chartId),
      );
      const missing = await Promise.all(
        missingIds.map(chartId =>
          client
            .get<{ result: DashboardChart }>(endpoints.chart(chartId))
            .then(response => response.result)
            .catch(() => null),
        ),
      );
      setCharts([
        ...dashboardCharts,
        ...missing.filter((item): item is DashboardChart => item !== null),
      ]);
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
      const loadedDatasets = await Promise.all(
        datasetIds.map(datasetId =>
          client
            .get<{ result: Dataset }>(endpoints.dataset(datasetId))
            .then(response => response.result)
            .catch(() => null),
        ),
      );
      setDatasets(loadedDatasets.filter((item): item is Dataset => item !== null));

      const defaults: FilterValueMap = {};
      parsedFilters.forEach(config => {
        const value = config.defaultDataMask?.filterState?.value;
        if (value !== undefined && value !== null) defaults[config.id] = value as FilterValue;
      });
      // Deep links: `?f_<filter>=v1,v2` from a portal / wiki / email.
      const external = readExternalFilters(window.location.search, parsedFilters);
      if (external.unmatched.length) {
        message.warning(
          t('dashboard.detail.unknown_filters', { keys: external.unmatched.join(', ') }),
        );
      }
      const urlState = readUrlState(window.location.search);
      if (urlState) {
        setFilterValues({ ...defaults, ...urlState.filterValues, ...external.values });
        setInitialEdit(urlState.edit && canWrite);
      } else if (Object.keys(external.values).length) {
        setFilterValues({ ...defaults, ...external.values });
      } else {
        const state = await client
          .get<{ result: unknown }>(endpoints.dashboardFilterState(entity.id))
          .catch(() => null);
        setFilterValues({ ...defaults, ...(state ? parseFilterState(state) : {}) });
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [id, canWrite]);

  useEffect(() => {
    void load();
  }, [load]);

  /** P5.2: reload the canvas once the agent has written charts into a dashboard. */
  const applyArtifacts = useCallback(
    (artifacts: JsonObject) => {
      const wroteDashboard =
        'page_spec' in artifacts || 'dashboard_id' in artifacts || 'chart_ids' in artifacts;
      if (!wroteDashboard) return;
      void load();
      message.success(t('ai.artifacts.dashboard_refreshed'));
    },
    [load, t],
  );

  const handleSaveLayout = async () => {
    if (!dashboard) return;
    try {
      const positionJson = buildPositionJson(layout);
      await client.put(endpoints.dashboard(dashboard.id), { position_json: positionJson });
      setSavedRows(layout.rows);
      setSavedTabs(layout.tabs);
      setDashboard(previous =>
        previous ? { ...previous, position_json: positionJson } : previous,
      );
      message.success(t('dashboard.detail.layout_saved'));
    } catch (err) {
      message.error(errorMessage(err));
      throw err;
    }
  };

  const persistFilters = async (next: NativeFilterConfig[]) => {
    if (!dashboard) throw new Error(t('dashboard.detail.not_loaded'));
    const metadata = {
      ...parseJsonObject(dashboard.json_metadata),
      native_filter_configuration: next,
    };
    await client.put(endpoints.dashboard(dashboard.id), { json_metadata: metadata });
    setDashboard(previous => (previous ? { ...previous, json_metadata: metadata } : previous));
    setFilters(next);
  };

  const handleFilterSubmit = async (config: NativeFilterConfig) => {
    try {
      const exists = filters.some(item => item.id === config.id);
      await persistFilters(
        exists
          ? filters.map(item => (item.id === config.id ? config : item))
          : [...filters, config],
      );
      message.success(
        exists ? t('dashboard.detail.filter_updated') : t('dashboard.detail.filter_created'),
      );
    } catch (err) {
      message.error(errorMessage(err));
      throw err;
    }
  };

  const handleRemoveFilter = async (filter: NativeFilterConfig) => {
    if (!dashboard) return;
    try {
      await persistFilters(filters.filter(item => item.id !== filter.id));
      setFilterValues(previous => {
        const next = { ...previous };
        delete next[filter.id];
        return next;
      });
      await client
        .delete(endpoints.dashboardFilterStateKey(dashboard.id, filter.id))
        .catch(() => undefined);
      message.success(t('dashboard.detail.filter_deleted'));
    } catch (err) {
      message.error(errorMessage(err));
      throw err;
    }
  };

  const handleFilterChange = (filterId: string, value: FilterValue) => {
    setFilterValues(previous => ({ ...previous, [filterId]: value ?? undefined }));
    if (!dashboard) return;
    const config = filters.find(item => item.id === filterId);
    const mask = config ? buildFilterDataMask(config, value) : null;
    if (mask) {
      void client
        .post(endpoints.dashboardFilterState(dashboard.id), { key: filterId, value: mask })
        .catch(() => undefined);
    } else {
      void client
        .delete(endpoints.dashboardFilterStateKey(dashboard.id, filterId))
        .catch(() => undefined);
    }
  };

  const handleAddCharts = (picked: Chart[], rowId: string | null) => {
    const mapped = toDashboardCharts(picked);
    setCharts(previous => {
      const ids = new Set(previous.map(chart => chart.id));
      return [...previous, ...mapped.filter(chart => !ids.has(chart.id))];
    });
    setLayout(previous => ({
      ...previous,
      rows: appendCharts(previous.rows, rowId, mapped),
    }));
  };

  if (loading) return <Loading height={320} />;

  if (error || !dashboard) {
    return (
      <EmptyState
        description={error ?? t('dashboard.detail.missing')}
        action={
          <Space>
            <Button onClick={() => void load()} icon={<ReloadOutlined />}>
              {t('common.actions.retry')}
            </Button>
            <Link to="/dashboard/list/">
              <Button type="primary">{t('common.actions.back_to_list')}</Button>
            </Link>
          </Space>
        }
      />
    );
  }

  const chartsById = new Map(charts.map(chart => [chart.id, chart]));

  return (
    <div>
      <DashboardHeader
        dashboard={dashboard}
        canWrite={canWrite}
        canCopy={can('can_copy_dash', 'Dashboard') || canWrite}
        canDelete={can('can_delete', 'Dashboard')}
        canShare={canWrite}
        onUpdated={setDashboard}
        onDeleted={() => navigate('/dashboard/list/')}
      />
      <DashboardBody
        canWrite={canWrite}
        layout={layout}
        savedRows={savedRows}
        savedTabs={savedTabs}
        chartsById={chartsById}
        filters={filters}
        filterValues={filterValues}
        datasets={datasets}
        initialEditMode={initialEdit}
        onRowsChange={rows => setLayout(previous => ({ ...previous, rows }))}
        onLayoutChange={next => setLayout(next)}
        onSaveLayout={handleSaveLayout}
        onAddCharts={handleAddCharts}
        onAddFilter={handleFilterSubmit}
        onRemoveFilter={handleRemoveFilter}
        onFilterChange={handleFilterChange}
        viewerName={username}
      />
      <AgentPanel
        context={{
          scope: 'dashboard',
          dashboard_id: dashboard.id,
          dataset_id: datasets[0]?.id,
          locale: lang,
        }}
        onArtifacts={applyArtifacts}
      />
    </div>
  );
}
