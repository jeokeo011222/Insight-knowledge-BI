import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Empty, Input, Tabs } from 'antd';
import {
  AppstoreOutlined,
  DeleteOutlined,
  HolderOutlined,
  PlusOutlined,
} from '@ant-design/icons';
import { Responsive, WidthProvider } from 'react-grid-layout';
import type { Layout } from 'react-grid-layout';
import 'react-grid-layout/css/styles.css';
import 'react-resizable/css/styles.css';
import type { Chart, DashboardChart } from '../../api/types';
import type { ActiveDataMask } from '../../features/dashboard/dataMask';
import {
  GRID_COLUMNS,
  backgroundClass,
  fromGridLayout,
  tileHeightPx,
  toGridLayout,
} from '../../features/dashboard/layout';
import type {
  DashboardRow,
  DashboardTab,
  GridItem,
  RowTile,
} from '../../features/dashboard/layout';
import ChartCard from './ChartCard';
import type { CrossFilter } from '../../features/dashboard/crossFilter';
import MarkdownBox from './MarkdownBox';
import RowHeader from './RowHeader';
import { useI18n } from '../../i18n';

const ResponsiveGrid = WidthProvider(Responsive);
const TAB_HEADER_PREFIX = 'TABHEAD:';

export interface GridLayoutProps {
  rows: DashboardRow[];
  tabs: DashboardTab[];
  editMode: boolean;
  chartsById: Map<number, DashboardChart>;
  masks: ActiveDataMask[];
  refreshKey: number;
  onChange: (rows: DashboardRow[]) => void;
  onAddChart: (rowId: string) => void;
  onRemoveChart: (chartId: number) => void;
  onMoveRow: (rowId: string, direction: -1 | 1) => void;
  onDeleteRow: (rowId: string) => void;
  onBackgroundChange: (rowId: string, background: DashboardRow['background']) => void;
  onAddRow: () => void;
  onAddTab: () => void;
  onRenameTab: (tabId: string, title: string) => void;
  onRemoveTab: (tabId: string) => void;
  /** Chart → chart linkage (clicking a category value). */
  onCrossFilter?: (filter: CrossFilter) => void;
  /** Public share / embed rendering: no edit affordances, no explore links. */
  publicView?: boolean;
  /** A share link can forbid downloads. */
  canExport?: boolean;
}

function chartPlaceholder(tile: RowTile & { kind: 'chart' }): DashboardChart {
  const chart: Chart = {
    id: tile.chartId,
    slice_name: tile.sliceName || `Chart #${tile.chartId}`,
    viz_type: 'table',
    datasource_id: 0,
    datasource_type: 'table',
    datasource_name: '',
    owners: [],
  };
  return { ...chart, form_data: {} };
}

function asGridItems(layout: Layout[]): GridItem[] {
  return layout.map(item => ({
    i: item.i,
    x: item.x,
    y: item.y,
    w: item.w,
    h: item.h,
    minW: item.minW ?? 1,
    minH: item.minH ?? 1,
    static: item.static,
    isDraggable: item.isDraggable,
    isResizable: item.isResizable,
  }));
}

/**
 * True on phone-width viewports, where tiles stack in a single column instead
 * of squeezing a 12-column canvas into 375px.
 */
function useNarrowViewport(): boolean {
  const query = '(max-width: 768px)';
  const [narrow, setNarrow] = useState(
    () => typeof window !== 'undefined' && window.matchMedia(query).matches,
  );
  useEffect(() => {
    const media = window.matchMedia(query);
    const onChange = () => setNarrow(media.matches);
    onChange();
    media.addEventListener('change', onChange);
    return () => media.removeEventListener('change', onChange);
  }, []);
  return narrow;
}

/**
 * Renders the dashboard canvas. View mode uses a plain 12-column CSS grid;
 * edit mode flattens every row into one react-grid-layout surface whose full
 * width headers mark the row bands (used again when rebuilding ROW nodes).
 */
export default function GridLayout({
  rows,
  tabs,
  editMode,
  chartsById,
  masks,
  refreshKey,
  onChange,
  onAddChart,
  onRemoveChart,
  onMoveRow,
  onDeleteRow,
  onBackgroundChange,
  onAddRow,
  onAddTab,
  onRenameTab,
  onRemoveTab,
  onCrossFilter,
  publicView = false,
  canExport = true,
}: GridLayoutProps) {
  const { t } = useI18n();
  const narrow = useNarrowViewport();
  const signature = useMemo(() => JSON.stringify({ rows, tabs }), [rows, tabs]);
  const [layout, setLayout] = useState<GridItem[]>(() => toGridLayout(rows, tabs));

  // The drag/resize callbacks keep a stable closure over the latest model.
  const rowsRef = useRef(rows);
  const tabsRef = useRef(tabs);
  const timerRef = useRef<number | null>(null);
  rowsRef.current = rows;
  tabsRef.current = tabs;

  useEffect(() => {
    setLayout(toGridLayout(rows, tabs));
    // Re-seed the editor whenever the row/tab model changes from the outside.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature]);

  useEffect(
    () => () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    },
    [],
  );

  /**
   * Projects the canvas back onto the row model. Superset stores no x
   * coordinate, so the tile whose grid cell drifted away from the canonical
   * slot is the one being dragged; it wins ties against its neighbour and thus
   * swaps places instead of piling up on top of it.
   */
  const commit = (items: GridItem[], snap: boolean) => {
    const canonical = new Map(
      toGridLayout(rowsRef.current, tabsRef.current).map(item => [item.i, item]),
    );
    const moved = items.find(item => {
      const base = canonical.get(item.i);
      return (
        base &&
        (base.x !== item.x ||
          base.y !== item.y ||
          base.w !== item.w ||
          base.h !== item.h)
      );
    });
    const committed = fromGridLayout(items, rowsRef.current, moved?.i);
    if (JSON.stringify(committed) === JSON.stringify(rowsRef.current)) {
      if (snap) setLayout(toGridLayout(rowsRef.current, tabsRef.current));
      return;
    }
    onChange(committed);
    if (snap) setLayout(toGridLayout(committed, tabsRef.current));
  };

  const scheduleCommit = (items: GridItem[]) => {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    // Fires once the grid settles, which covers the drop even when RGL does not
    // report onDragStop (custom handles, lost mouseup outside the viewport…).
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      commit(items, false);
    }, 220);
  };

  const finishInteraction = (items: GridItem[]) => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    setLayout(items);
    commit(items, true);
  };

  const renderTile = (tile: RowTile, height: number) => {
    if (tile.kind === 'chart') {
      const chart = chartsById.get(tile.chartId) ?? chartPlaceholder(tile);
      return (
        <ChartCard
          chart={chart}
          height={height}
          masks={masks}
          refreshKey={refreshKey}
          editMode={editMode}
          onRemove={editMode ? () => onRemoveChart(tile.chartId) : undefined}
          onCrossFilter={onCrossFilter}
          publicView={publicView}
          canExport={canExport}
        />
      );
    }
    if (tile.kind === 'markdown') {
      return (
        <div className="dashboard-tile dashboard-tile--text" style={{ height }}>
          {editMode ? (
            <div className="dashboard-tile__handle">
              <HolderOutlined />
            </div>
          ) : null}
          <MarkdownBox text={tile.code} />
        </div>
      );
    }
    if (tile.kind === 'divider') {
      return (
        <div className="dashboard-tile dashboard-tile--divider" style={{ height }}>
          {editMode ? (
            <div className="dashboard-tile__handle">
              <HolderOutlined />
            </div>
          ) : null}
          <div className="dashboard-divider" />
        </div>
      );
    }
    return (
      <div className="dashboard-tile dashboard-tile--header" style={{ height }}>
        {editMode ? (
          <div className="dashboard-tile__handle">
            <HolderOutlined />
          </div>
        ) : null}
        <h3 className="dashboard-header">{tile.text || 'Header'}</h3>
      </div>
    );
  };

  const renderRow = (row: DashboardRow) => (
    <div className={`dashboard-row ${backgroundClass(row.background)}`} key={row.id}>
      <div
        className="dashboard-row__grid"
        style={{
          gridTemplateColumns: `repeat(${narrow ? 1 : GRID_COLUMNS}, minmax(0, 1fr))`,
        }}
      >
        {row.tiles.map(tile => (
          <div
            className="dashboard-cell"
            key={tile.id}
            style={{ gridColumn: `span ${narrow ? 1 : tile.width}` }}
          >
            {renderTile(tile, tileHeightPx(tile.height))}
          </div>
        ))}
      </div>
    </div>
  );

  if (!editMode) {
    // Rows outside any tab render above the container, mirroring the document
    // order; tabbed rows are only instantiated for the active tab.
    return (
      <div className="dashboard-canvas">
        {rows.filter(row => !row.tabId).map(renderRow)}
        {tabs.length ? (
          <Tabs
            className="dashboard-tabs"
            defaultActiveKey={(tabs.find(tab => tab.isDefault) ?? tabs[0]).id}
            items={tabs.map(tab => ({
              key: tab.id,
              label: tab.title,
              children: (
                <div className="dashboard-tabs__panel">
                  {rows.filter(row => row.tabId === tab.id).map(renderRow)}
                </div>
              ),
            }))}
          />
        ) : null}
        {!rows.length ? (
          <Empty description={t('dashboard.grid.empty')} />
        ) : null}
      </div>
    );
  }

  const rowIndex = new Map(rows.map((row, index) => [row.id, index]));
  const tileById = new Map<string, RowTile>();
  rows.forEach(row => row.tiles.forEach(tile => tileById.set(tile.id, tile)));

  return (
    <div className="dashboard-canvas dashboard-canvas--edit">
      <ResponsiveGrid
        className="dashboard-grid"
        layouts={{ lg: layout }}
        breakpoints={{ lg: 0 }}
        cols={{ lg: GRID_COLUMNS }}
        rowHeight={40}
        margin={[12, 12]}
        containerPadding={[0, 0]}
        draggableHandle=".dashboard-tile__drag, .dashboard-tile__handle"
        draggableCancel=".ant-btn, .ant-tag, a, input"
        onLayoutChange={current => {
          if (!current.length) return;
          const items = asGridItems(current);
          setLayout(items);
          if (editMode) scheduleCommit(items);
        }}
        onDragStop={next => finishInteraction(asGridItems(next))}
        onResizeStop={next => finishInteraction(asGridItems(next))}
      >
        {layout.map(item => {
          if (item.i.startsWith(TAB_HEADER_PREFIX)) {
            const tabId = item.i.slice(TAB_HEADER_PREFIX.length);
            const tab = tabs.find(entry => entry.id === tabId);
            if (!tab) return <div key={item.i} />;
            return (
              <div key={item.i} className="dashboard-grid__tab-head">
                <AppstoreOutlined className="dashboard-grid__tab-icon" />
                <Input
                  className="dashboard-grid__tab-input"
                  size="small"
                  value={tab.title}
                  placeholder={t('dashboard.tabs.name_placeholder')}
                  onChange={event => onRenameTab(tab.id, event.target.value)}
                />
                <Button
                  size="small"
                  type="text"
                  danger
                  icon={<DeleteOutlined />}
                  aria-label={t('dashboard.tabs.delete_title')}
                  onClick={() => onRemoveTab(tab.id)}
                />
              </div>
            );
          }
          if (item.i.startsWith('ROWHEAD:')) {
            const rowId = item.i.slice('ROWHEAD:'.length);
            const row = rows.find(entry => entry.id === rowId);
            if (!row) return <div key={item.i} />;
            return (
              <div key={item.i} className="dashboard-grid__row-head">
                <RowHeader
                  row={row}
                  index={rowIndex.get(row.id) ?? 0}
                  total={rows.length}
                  onAddChart={() => onAddChart(row.id)}
                  onMove={direction => onMoveRow(row.id, direction)}
                  onDelete={() => onDeleteRow(row.id)}
                  onBackgroundChange={background => onBackgroundChange(row.id, background)}
                />
              </div>
            );
          }
          const tile = tileById.get(item.i);
          if (!tile) return <div key={item.i} />;
          const height = Math.max(160, item.h * 40 - 12);
          return (
            <div key={item.i} className="dashboard-grid__item">
              {renderTile(tile, height)}
            </div>
          );
        })}
      </ResponsiveGrid>

      <div className="dashboard-canvas__footer" style={{ display: 'flex', gap: 8 }}>
        <Button icon={<PlusOutlined />} onClick={onAddRow} type="dashed" block>
          {t('dashboard.toolbar.add_row')}
        </Button>
        <Button icon={<AppstoreOutlined />} onClick={onAddTab} type="dashed" block>
          {t('dashboard.tabs.add')}
        </Button>
      </div>
    </div>
  );
}
