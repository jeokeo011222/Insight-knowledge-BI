import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Modal, Tag, Tooltip } from 'antd';
import { CloseCircleOutlined, LinkOutlined } from '@ant-design/icons';
import AddChartDrawer from '../../components/dashboard/AddChartDrawer';
import FilterPanel from '../../components/dashboard/FilterPanel';
import FilterConfigModal from '../../components/dashboard/FilterConfigModal';
import GridLayout from '../../components/dashboard/GridLayout';
import DashboardToolbar from '../../components/dashboard/DashboardToolbar';
import EmptyState from '../../components/common/EmptyState';
import Watermark, { watermarkText } from '../../components/common/Watermark';
import type { Chart, DashboardChart, Dataset, NativeFilterConfig } from '../../api/types';
import { collectActiveMasks, syncUrlState } from '../../features/dashboard/filters';
import {
  crossFilterMasks,
  removeCrossFilter,
  toggleCrossFilter,
} from '../../features/dashboard/crossFilter';
import type { CrossFilter } from '../../features/dashboard/crossFilter';
import type { FilterValue, FilterValueMap } from '../../features/dashboard/filters';
import { addTab, makeRow, moveRow, removeTab, renameTab } from '../../features/dashboard/layout';
import type { DashboardLayoutState, DashboardRow, DashboardTab } from '../../features/dashboard/layout';
import { useDashPrefs } from '../../features/dashboard/prefs';
import { useI18n } from '../../i18n';

export interface DashboardBodyProps {
  canWrite: boolean;
  layout: DashboardLayoutState;
  savedRows: DashboardRow[];
  savedTabs: DashboardTab[];
  chartsById: Map<number, DashboardChart>;
  filters: NativeFilterConfig[];
  filterValues: FilterValueMap;
  datasets: Dataset[];
  initialEditMode: boolean;
  onRowsChange: (rows: DashboardRow[]) => void;
  onLayoutChange: (layout: DashboardLayoutState) => void;
  onSaveLayout: () => Promise<void>;
  onAddCharts: (charts: Chart[], rowId: string | null) => void;
  onAddFilter: (config: NativeFilterConfig) => Promise<void>;
  onRemoveFilter: (filter: NativeFilterConfig) => Promise<void>;
  onFilterChange: (filterId: string, value: FilterValue) => void;
  /** Rendered for anonymous viewers: no edits, no agent, watermark on. */
  publicView?: boolean;
  /** Share links may forbid downloads. */
  canExport?: boolean;
  /** Identity printed in the watermark (public view: "guest"). */
  viewerName?: string;
  /** External refresh trigger (public viewer's toolbar). */
  refreshKey?: number;
}

/**
 * Interactive canvas: edit mode, row/grid operations, refresh loop and the
 * filter rail. Data loading and persistence stay in the page component.
 */
export default function DashboardBody({
  canWrite,
  layout,
  savedRows,
  savedTabs,
  chartsById,
  filters,
  filterValues,
  datasets,
  initialEditMode,
  onRowsChange,
  onLayoutChange,
  onSaveLayout,
  onAddCharts,
  onAddFilter,
  onRemoveFilter,
  onFilterChange,
  publicView = false,
  canExport = true,
  viewerName,
  refreshKey: externalRefreshKey = 0,
}: DashboardBodyProps) {
  const { t } = useI18n();
  const [prefs, setPrefs] = useDashPrefs();
  const [crossFilters, setCrossFilters] = useState<CrossFilter[]>([]);
  const [editMode, setEditMode] = useState(Boolean(initialEditMode && canWrite));
  const [collapsed, setCollapsed] = useState(filters.length === 0);
  const [autoRefreshMs, setAutoRefreshMs] = useState(0);
  const [fullscreen, setFullscreen] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [refreshing, setRefreshing] = useState(false);
  const [savingLayout, setSavingLayout] = useState(false);
  const [lastSavedAt, setLastSavedAt] = useState<string | null>(null);
  const [savingFilter, setSavingFilter] = useState(false);
  const [addChartOpen, setAddChartOpen] = useState(false);
  const [addChartRowId, setAddChartRowId] = useState<string | null>(null);
  const [filterModalOpen, setFilterModalOpen] = useState(false);
  const [editingFilter, setEditingFilter] = useState<NativeFilterConfig | null>(null);

  const dirty = useMemo(
    () =>
      JSON.stringify(layout.rows) !== JSON.stringify(savedRows) ||
      JSON.stringify(layout.tabs) !== JSON.stringify(savedTabs),
    [layout.rows, layout.tabs, savedRows, savedTabs],
  );
  const masks = useMemo(() => {
    const base = collectActiveMasks(filters, filterValues);
    if (!prefs.linked) return base;
    return [...base, ...crossFilterMasks(crossFilters)];
  }, [filters, filterValues, crossFilters, prefs.linked]);
  const placedChartIds = useMemo(
    () =>
      Array.from(
        new Set(
          layout.rows.flatMap(row =>
            row.tiles.filter(tile => tile.kind === 'chart').map(tile => tile.chartId),
          ),
        ),
      ),
    [layout.rows],
  );

  useEffect(() => {
    // The public viewer must not rewrite its own URL: the token lives there.
    if (publicView) return;
    syncUrlState(filterValues, editMode);
  }, [filterValues, editMode, publicView]);

  const handleCrossFilter = useCallback(
    (next: CrossFilter) => {
      setCrossFilters(previous => toggleCrossFilter(previous, next));
    },
    [],
  );

  const clearCrossFilters = () => setCrossFilters([]);

  useEffect(() => {
    const handler = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

  useEffect(() => {
    if (!autoRefreshMs) return;
    const timer = window.setInterval(() => setRefreshKey(key => key + 1), autoRefreshMs);
    return () => window.clearInterval(timer);
  }, [autoRefreshMs]);

  useEffect(() => {
    if (!refreshing) return;
    const timer = window.setTimeout(() => setRefreshing(false), 800);
    return () => window.clearTimeout(timer);
  }, [refreshing]);

  useEffect(() => {
    const onChange = () => setFullscreen(Boolean(document.fullscreenElement));
    document.addEventListener('fullscreenchange', onChange);
    return () => document.removeEventListener('fullscreenchange', onChange);
  }, []);

  const handleRefresh = () => {
    setRefreshing(true);
    setRefreshKey(key => key + 1);
  };

  const handleToggleEdit = (next: boolean) => {
    if (next === editMode) return;
    if (!next && dirty) {
      Modal.confirm({
        title: t('dashboard.body.discard_title'),
        content: t('dashboard.body.discard_content'),
        okText: t('dashboard.body.discard_ok'),
        okType: 'danger',
        cancelText: t('dashboard.body.discard_cancel'),
        onOk: () => {
          onLayoutChange({ ...layout, rows: savedRows, tabs: savedTabs });
          setEditMode(false);
        },
      });
      return;
    }
    setEditMode(next);
  };

  const handleSave = async () => {
    setSavingLayout(true);
    try {
      await onSaveLayout();
      setLastSavedAt(
        new Date().toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }),
      );
    } catch {
      // The page reports the failure and keeps the dirty layout intact.
    } finally {
      setSavingLayout(false);
    }
  };

  const handleFilterSubmit = async (config: NativeFilterConfig) => {
    setSavingFilter(true);
    try {
      await onAddFilter(config);
      setFilterModalOpen(false);
      setEditingFilter(null);
      setCollapsed(false);
    } catch {
      // Keep the modal open so the user can fix the payload.
    } finally {
      setSavingFilter(false);
    }
  };

  const handleRemoveFilter = async (filter: NativeFilterConfig) => {
    setSavingFilter(true);
    try {
      await onRemoveFilter(filter);
    } catch {
      // Error toast already shown by the page.
    } finally {
      setSavingFilter(false);
    }
  };

  const handleRemoveChart = (chartId: number) => {
    const next = layout.rows
      .map(row => ({
        ...row,
        tiles: row.tiles.filter(
          tile => !(tile.kind === 'chart' && tile.chartId === chartId),
        ),
      }))
      .filter(row => row.tiles.length > 0);
    onRowsChange(next.length ? next : [makeRow()]);
  };

  /**
   * Tab operations. A tab always keeps at least one row so it stays reachable
   * on the canvas; deleting a tab takes its rows with it.
   */
  const handleAddTab = () => {
    onLayoutChange(
      addTab(layout, t('dashboard.tabs.default_title', { index: layout.tabs.length + 1 })),
    );
  };

  const handleRenameTab = (tabId: string, title: string) => {
    onLayoutChange(renameTab(layout, tabId, title));
  };

  const handleRemoveTab = (tabId: string) => {
    const tab = layout.tabs.find(entry => entry.id === tabId);
    const rowCount = layout.rows.filter(row => row.tabId === tabId).length;
    Modal.confirm({
      title: t('dashboard.tabs.delete_title'),
      content: t('dashboard.tabs.delete_content', {
        title: tab?.title ?? '',
        count: rowCount,
      }),
      okText: t('common.actions.delete'),
      okType: 'danger',
      cancelText: t('common.actions.cancel'),
      onOk: () => onLayoutChange(removeTab(layout, tabId)),
    });
  };

  /**
   * New rows join the last tab when the dashboard is tabbed, otherwise they
   * land outside the container.
   */
  const handleAddRow = () => {
    const target = layout.tabs[layout.tabs.length - 1];
    onRowsChange([...layout.rows, makeRow(target?.id ?? null)]);
  };

  const toggleFullscreen = async () => {
    const next = !fullscreen;
    setFullscreen(next);
    try {
      if (next) {
        await document.documentElement.requestFullscreen?.();
      } else if (document.fullscreenElement) {
        await document.exitFullscreen();
      }
    } catch {
      // CSS fullscreen stays active when the browser API is blocked.
    }
  };

  const markText = watermarkText(viewerName || t('dashboard.watermark.guest'));
  const showWatermark = prefs.watermark || publicView;

  return (
    <div className={`dashboard-page${fullscreen ? ' dashboard-page--fullscreen' : ''}`}>
      {showWatermark ? <Watermark text={markText} /> : null}

      {publicView ? null : (
      <DashboardToolbar
        canEdit={canWrite}
        editMode={editMode}
        dirty={dirty}
        saving={savingLayout}
        refreshing={refreshing}
        autoRefreshMs={autoRefreshMs}
        fullscreen={fullscreen}
        filterCount={filters.length}
        savedAt={lastSavedAt}
        onToggleEdit={handleToggleEdit}
        onSave={() => void handleSave()}
        onRefresh={handleRefresh}
        onAutoRefreshChange={setAutoRefreshMs}
        onToggleFullscreen={() => void toggleFullscreen()}
        onAddChart={() => {
          setAddChartRowId(layout.rows[layout.rows.length - 1]?.id ?? null);
          setAddChartOpen(true);
        }}
        onAddRow={handleAddRow}
        onAddFilter={() => {
          setEditingFilter(null);
          setFilterModalOpen(true);
        }}
        watermark={prefs.watermark}
        onToggleWatermark={() => setPrefs({ watermark: !prefs.watermark })}
        linked={prefs.linked}
        onToggleLinked={() => setPrefs({ linked: !prefs.linked })}
      />
      )}

      {crossFilters.length ? (
        <div className="crossfilter-bar">
          <span className="crossfilter-bar__label">
            <LinkOutlined /> {t('dashboard.linkage.active')}
          </span>
          {crossFilters.map(filter => (
            <Tag
              key={filter.id}
              closable
              className="crossfilter-chip"
              onClose={event => {
                event.preventDefault();
                setCrossFilters(previous => removeCrossFilter(previous, filter.id));
              }}
            >
              {filter.label}
            </Tag>
          ))}
          <Button size="small" type="link" onClick={clearCrossFilters}>
            {t('dashboard.linkage.clear')}
          </Button>
        </div>
      ) : null}

      <div className="dashboard-body">
        <FilterPanel
          filters={filters}
          values={filterValues}
          datasets={datasets}
          collapsed={collapsed}
          canEdit={canWrite}
          editMode={editMode}
          onChange={onFilterChange}
          onToggleCollapse={() => setCollapsed(value => !value)}
          onAdd={() => {
            setEditingFilter(null);
            setFilterModalOpen(true);
          }}
          onEdit={filter => {
            setEditingFilter(filter);
            setFilterModalOpen(true);
          }}
          onRemove={filter => void handleRemoveFilter(filter)}
        />

        {layout.rows.length || editMode ? (
          <GridLayout
            rows={layout.rows}
            tabs={layout.tabs}
            editMode={editMode}
            chartsById={chartsById}
            masks={masks}
            refreshKey={refreshKey + externalRefreshKey}
            onChange={onRowsChange}
            onAddChart={rowId => {
              setAddChartRowId(rowId);
              setAddChartOpen(true);
            }}
            onRemoveChart={handleRemoveChart}
            onMoveRow={(rowId, direction) =>
              onRowsChange(moveRow(layout.rows, rowId, direction))
            }
            onDeleteRow={rowId => {
              const next = layout.rows.filter(row => row.id !== rowId);
              onRowsChange(next.length ? next : [makeRow()]);
            }}
            onBackgroundChange={(rowId, background) =>
              onRowsChange(
                layout.rows.map(row => (row.id === rowId ? { ...row, background } : row)),
              )
            }
            onAddRow={handleAddRow}
            onAddTab={handleAddTab}
            onRenameTab={handleRenameTab}
            onRemoveTab={handleRemoveTab}
            onCrossFilter={prefs.linked ? handleCrossFilter : undefined}
            publicView={publicView}
            canExport={canExport}
          />
        ) : (
          <div className="dashboard-canvas">
            <EmptyState
              description={t('dashboard.body.empty')}
              action={
                canWrite ? (
                  <Button
                    type="primary"
                    onClick={() => {
                      setEditMode(true);
                      setAddChartOpen(true);
                    }}
                  >
                    {t('dashboard.row.add_chart')}
                  </Button>
                ) : undefined
              }
            />
          </div>
        )}
      </div>

      {publicView ? null : (
      <AddChartDrawer
        open={addChartOpen}
        existingIds={placedChartIds}
        onClose={() => {
          setAddChartOpen(false);
          setAddChartRowId(null);
        }}
        onAdd={charts => onAddCharts(charts, addChartRowId)}
      />
      )}

      {publicView ? null : (
      <FilterConfigModal
        open={filterModalOpen}
        filter={editingFilter}
        datasets={datasets}
        saving={savingFilter}
        onCancel={() => {
          setFilterModalOpen(false);
          setEditingFilter(null);
        }}
        onSubmit={config => void handleFilterSubmit(config)}
      />
      )}
    </div>
  );
}
