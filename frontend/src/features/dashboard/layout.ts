/**
 * Dashboard `position_json` (Superset v2) parsing and rebuilding.
 *
 * The v2 document is a flat map of nodes keyed by id:
 *   DASHBOARD_VERSION_KEY / ROOT_ID / GRID_ID / HEADER_ID
 *   ROW-* (children: CHART-* / MARKDOWN-* / DIVIDER-* / HEADER)
 *   CHART-* (meta.chartId, meta.width, meta.height, meta.sliceName)
 *
 * The editor works on `DashboardRow[]` (easier to mutate and to feed into
 * react-grid-layout) and rebuilds the document on save so parents/children are
 * always consistent.
 */
import type { DashboardChart, JsonObject, RowBackground } from '../../api/types';
import { parseJsonObject } from './dataMask';

export const GRID_COLUMNS = 12;
/** Superset GRID_BASE_UNIT: one layout height unit is 8px. */
export const LAYOUT_UNIT_PX = 8;
/** Default tile height in layout units (40 * 8 = 320px). */
export const DEFAULT_TILE_HEIGHT = 40;
/** react-grid-layout row height (the editor uses a coarser 40px grid). */
export const RGL_ROW_HEIGHT = 40;
export const DEFAULT_BACKGROUND: RowBackground = 'BACKGROUND_TRANSPARENT';

const ROW_HEADER_PREFIX = 'ROWHEAD:';
const TAB_HEADER_PREFIX = 'TABHEAD:';
const TABS_ID = 'TABS_ID';
const REBUILT_TYPES = new Set([
  'ROOT',
  'GRID',
  'ROW',
  'CHART',
  'MARKDOWN',
  'DIVIDER',
  'HEADER',
  'TABS',
  'TAB',
]);

interface BaseTile {
  id: string;
  width: number;
  height: number;
}

export interface ChartTile extends BaseTile {
  kind: 'chart';
  chartId: number;
  sliceName: string;
}

export interface MarkdownTile extends BaseTile {
  kind: 'markdown';
  code: string;
}

export interface DividerTile extends BaseTile {
  kind: 'divider';
}

export interface HeaderTile extends BaseTile {
  kind: 'header';
  text: string;
  headerSize: string;
}

export type RowTile = ChartTile | MarkdownTile | DividerTile | HeaderTile;

/** A tab of the optional TABS container sitting between GRID and the rows. */
export interface DashboardTab {
  id: string;
  title: string;
  /** Tab activated on load (`meta.default` in the document). */
  isDefault?: boolean;
}

export interface DashboardRow {
  id: string;
  background: RowBackground;
  tiles: RowTile[];
  /** Owning tab, or `null` when the row hangs directly off the grid. */
  tabId: string | null;
}

export interface DashboardLayoutState {
  rows: DashboardRow[];
  header: { id: string; text: string; headerSize: string };
  /** Empty when the dashboard has no TABS container (the common case). */
  tabs: DashboardTab[];
  /** Nodes the editor does not own (legacy HEADER, extra containers...). */
  preserved: Record<string, JsonObject>;
}

export interface GridItem {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
  minW: number;
  minH: number;
  static?: boolean;
  isDraggable?: boolean;
  isResizable?: boolean;
}

/* --------------------------------------------------------------- helpers */

function asObject(value: unknown): JsonObject | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as JsonObject)
    : null;
}

function asString(value: unknown): string {
  if (typeof value === 'string') return value;
  return typeof value === 'number' ? String(value) : '';
}

function asNumber(value: unknown, fallback: number): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return fallback;
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(value)));
}

export function isBackground(value: unknown): value is RowBackground {
  return value === 'BACKGROUND_TRANSPARENT' || value === 'BACKGROUND_LIGHT' || value === 'BACKGROUND_DARK';
}

export function tileHeightPx(height: number): number {
  return clamp(height * LAYOUT_UNIT_PX, 160, 1600);
}

export function backgroundClass(background: RowBackground): string {
  if (background === 'BACKGROUND_LIGHT') return 'dashboard-row--light';
  if (background === 'BACKGROUND_DARK') return 'dashboard-row--dark';
  return 'dashboard-row--transparent';
}

function newId(prefix: string): string {
  return `${prefix}${Math.random().toString(36).slice(2, 10)}`;
}

/* ----------------------------------------------------------------- parse */

/** Two-charts-per-row layout used when position_json is empty. */
export function createDefaultLayout(charts: DashboardChart[]): DashboardLayoutState {
  const rows: DashboardRow[] = [];
  for (let index = 0; index < charts.length; index += 2) {
    const chunk = charts.slice(index, index + 2);
    const width = chunk.length === 1 ? GRID_COLUMNS : GRID_COLUMNS / 2;
    rows.push({
      id: newId('ROW-'),
      background: DEFAULT_BACKGROUND,
      tabId: null,
      tiles: chunk.map(chart => ({
        kind: 'chart' as const,
        id: newId('CHART-'),
        chartId: chart.id,
        sliceName: chart.slice_name,
        width,
        height: DEFAULT_TILE_HEIGHT,
      })),
    });
  }
  return {
    rows,
    header: { id: 'HEADER_ID', text: '', headerSize: 'LARGE_HEADER' },
    tabs: [],
    preserved: {},
  };
}

function parseTile(nodeId: string, node: JsonObject): RowTile | null {
  const type = asString(node.type);
  const meta = asObject(node.meta) ?? {};
  const width = clamp(asNumber(meta.width, 6), 1, GRID_COLUMNS);
  const height = clamp(asNumber(meta.height, DEFAULT_TILE_HEIGHT), 4, 200);

  if (type === 'CHART') {
    const chartId = asNumber(meta.chartId, Number(nodeId.replace(/^CHART-/, '')));
    if (!Number.isFinite(chartId) || chartId <= 0) return null;
    return { kind: 'chart', id: nodeId, chartId, sliceName: asString(meta.sliceName), width, height };
  }
  if (type === 'MARKDOWN') {
    const code = asString(meta.code) || asString(meta.text);
    return { kind: 'markdown', id: nodeId, code, width, height };
  }
  if (type === 'DIVIDER') return { kind: 'divider', id: nodeId, width, height: Math.min(height, 12) };
  if (type === 'HEADER') {
    const text = asString(meta.text);
    const headerSize = asString(meta.headerSize) || 'MEDIUM_HEADER';
    return { kind: 'header', id: nodeId, text, headerSize, width, height };
  }
  return null;
}

function normalizeRowWidths(tiles: RowTile[]): RowTile[] {
  let used = 0;
  return tiles.map(tile => {
    const width = clamp(tile.width, 1, Math.max(1, GRID_COLUMNS - used));
    used += width;
    return { ...tile, width };
  });
}

/**
 * Parses `position_json` into rows. Charts present in the dashboard but absent
 * from the layout are appended so nothing silently disappears.
 *
 * Both document shapes are supported: rows hanging directly off `GRID_ID`, and
 * the `GRID_ID → TABS_ID → TAB-* → ROW-*` nesting Superset emits once the
 * dashboard uses tabs. Tab membership is flattened onto `row.tabId`.
 */
export function parseLayout(
  raw: JsonObject | string | null | undefined,
  charts: DashboardChart[],
): DashboardLayoutState {
  const document = parseJsonObject(raw);
  const grid = asObject(document.GRID_ID);
  if (!grid) return createDefaultLayout(charts);

  const preserved: Record<string, JsonObject> = {};
  Object.entries(document).forEach(([key, value]) => {
    const node = asObject(value);
    if (node && !REBUILT_TYPES.has(asString(node.type))) preserved[key] = node;
  });

  const headerMeta = asObject(asObject(document.HEADER_ID)?.meta) ?? {};
  const header = {
    id: 'HEADER_ID',
    text: asString(headerMeta.text),
    headerSize: asString(headerMeta.headerSize) || 'LARGE_HEADER',
  };

  const rows: DashboardRow[] = [];
  const tabs: DashboardTab[] = [];
  const seenCharts = new Set<number>();

  /** Turns one ROW node into a row; tiles that cannot be parsed are skipped. */
  const readRow = (rowId: string, tabId: string | null) => {
    const rowNode = asObject(document[rowId]);
    if (!rowNode) return;
    const rowMeta = asObject(rowNode.meta) ?? {};
    const tiles: RowTile[] = [];

    /**
     * Recursively collects tiles. Nested containers (COLUMN, or a GRID/ROW
     * that a hand-edited document produced) are flattened into the row so no
     * chart is silently dropped - their widths already add up to the row.
     */
    const collect = (nodeIds: unknown[]) => {
      nodeIds.forEach(rawId => {
        const tileId = asString(rawId);
        const tileNode = asObject(document[tileId]);
        if (!tileNode) return;
        const tileType = asString(tileNode.type);
        if (tileType === 'COLUMN' || tileType === 'ROW' || tileType === 'GRID' || tileType === 'TABS' || tileType === 'TAB') {
          collect(Array.isArray(tileNode.children) ? tileNode.children : []);
          return;
        }
        const tile = parseTile(tileId, tileNode);
        if (!tile) return;
        if (tile.kind === 'chart') {
          if (seenCharts.has(tile.chartId)) return;
          seenCharts.add(tile.chartId);
        }
        tiles.push(tile);
      });
    };

    collect(Array.isArray(rowNode.children) ? rowNode.children : []);
    rows.push({
      id: rowId,
      background: isBackground(rowMeta.background) ? rowMeta.background : DEFAULT_BACKGROUND,
      tabId,
      tiles: normalizeRowWidths(tiles),
    });
  };

  const gridChildren = Array.isArray(grid.children) ? grid.children : [];

  gridChildren.forEach(childId => {
    const childKey = asString(childId);
    const childNode = asObject(document[childKey]);
    if (!childNode) return;
    const childType = asString(childNode.type);

    if (childType === 'TABS') {
      const tabIds = Array.isArray(childNode.children) ? childNode.children : [];
      tabIds.forEach((rawTabId, index) => {
        const tabId = asString(rawTabId);
        const tabNode = asObject(document[tabId]);
        if (!tabNode) return;
        const tabMeta = asObject(tabNode.meta) ?? {};
        const entry: DashboardTab = {
          id: tabId,
          title:
            asString(tabMeta.label) || asString(tabMeta.name) || `Tab ${index + 1}`,
        };
        if (tabMeta.default === true) entry.isDefault = true;
        tabs.push(entry);
        const rowIds = Array.isArray(tabNode.children) ? tabNode.children : [];
        rowIds.forEach(rowId => readRow(asString(rowId), tabId));
      });
      return;
    }

    if (childType === 'ROW') {
      readRow(childKey, null);
      return;
    }

    // Tolerate legacy documents that place tiles directly under the grid.
    const tile = parseTile(childKey, childNode);
    if (!tile) return;
    if (tile.kind === 'chart') seenCharts.add(tile.chartId);
    rows.push({ id: newId('ROW-'), background: DEFAULT_BACKGROUND, tabId: null, tiles: [tile] });
  });

  const missing = charts.filter(chart => !seenCharts.has(chart.id));
  if (missing.length) {
    // Charts only present in the dashboard payload join the first tab when the
    // document is tabbed, so they stay reachable.
    const fallbackTab = tabs[0]?.id ?? null;
    rows.push(
      ...createDefaultLayout(missing).rows.map(row => ({ ...row, tabId: fallbackTab })),
    );
  }
  if (!rows.length) return createDefaultLayout(charts);
  return { rows, header, tabs, preserved };
}

/* ----------------------------------------------------------------- build */

function tileNode(tile: RowTile, parents: string[]): JsonObject {
  const base = { id: tile.id, children: [], parents };
  const size = { width: tile.width, height: tile.height };
  if (tile.kind === 'chart') {
    return {
      ...base,
      type: 'CHART',
      meta: { ...size, chartId: tile.chartId, sliceName: tile.sliceName },
    };
  }
  if (tile.kind === 'markdown') {
    return { ...base, type: 'MARKDOWN', meta: { ...size, code: tile.code } };
  }
  if (tile.kind === 'divider') {
    return { ...base, type: 'DIVIDER', meta: size };
  }
  return {
    ...base,
    type: 'HEADER',
    meta: { ...size, text: tile.text, headerSize: tile.headerSize },
  };
}

/**
 * Rebuilds a valid v2 document; ROOT/GRID/TABS/TAB/ROW children are
 * regenerated from the model so parents and children stay consistent.
 */
export function buildPositionJson(state: DashboardLayoutState): JsonObject {
  const document: JsonObject = { ...state.preserved };
  const tabs = state.tabs ?? [];
  const tabbed = tabs.length > 0;

  document.DASHBOARD_VERSION_KEY = 'v2';
  document.ROOT_ID = {
    id: 'ROOT_ID',
    type: 'ROOT',
    children: ['GRID_ID'],
    parents: [],
  };
  document.HEADER_ID = {
    id: 'HEADER_ID',
    type: 'HEADER',
    meta: { text: state.header.text, headerSize: state.header.headerSize },
  };
  document.GRID_ID = {
    id: 'GRID_ID',
    type: 'GRID',
    children: tabbed ? [TABS_ID] : state.rows.map(row => row.id),
    parents: ['ROOT_ID'],
  };

  if (tabbed) {
    document[TABS_ID] = {
      id: TABS_ID,
      type: 'TABS',
      children: tabs.map(tab => tab.id),
      parents: ['ROOT_ID', 'GRID_ID'],
      meta: {},
    };
    // Exactly one tab carries `default`; fall back to the first one so the
    // document always opens on a well-defined tab.
    const defaultTabId = tabs.find(tab => tab.isDefault)?.id ?? tabs[0]?.id;
    tabs.forEach(tab => {
      const tabRows = state.rows.filter(row => row.tabId === tab.id);
      document[tab.id] = {
        id: tab.id,
        type: 'TAB',
        children: tabRows.map(row => row.id),
        parents: ['ROOT_ID', 'GRID_ID', TABS_ID],
        meta: { label: tab.title, ...(tab.id === defaultTabId ? { default: true } : {}) },
      };
    });
  }

  state.rows.forEach(row => {
    const owners = tabbed && row.tabId ? ['ROOT_ID', 'GRID_ID', TABS_ID, row.tabId] : ['ROOT_ID', 'GRID_ID'];
    const parents = [...owners, row.id];
    document[row.id] = {
      id: row.id,
      type: 'ROW',
      children: row.tiles.map(tile => tile.id),
      parents: owners,
      meta: { background: row.background },
    };
    row.tiles.forEach(tile => {
      document[tile.id] = tileNode(tile, parents);
    });
  });
  return document;
}

/* ------------------------------------------------------- grid conversion */

/**
 * Flattens rows into a single 12-column grid (one row header per band).
 *
 * Tabbed dashboards additionally get a static tab header in front of the rows
 * of every tab, so tab membership stays visible - and draggable - inside one
 * continuous surface rather than in a nested grid.
 */
export function toGridLayout(
  rows: DashboardRow[],
  tabs: DashboardTab[] = [],
): GridItem[] {
  const layout: GridItem[] = [];
  let y = 0;

  const emitRow = (row: DashboardRow) => {
    layout.push({
      i: `${ROW_HEADER_PREFIX}${row.id}`,
      x: 0,
      y,
      w: GRID_COLUMNS,
      h: 1,
      minW: GRID_COLUMNS,
      minH: 1,
      static: true,
      isDraggable: false,
      isResizable: false,
    });
    const bandHeight = Math.max(
      4,
      ...row.tiles.map(tile =>
        Math.max(2, Math.round((tile.height * LAYOUT_UNIT_PX) / RGL_ROW_HEIGHT)),
      ),
    );
    let x = 0;
    row.tiles.forEach(tile => {
      const w = clamp(tile.width, 1, GRID_COLUMNS);
      const h = Math.max(2, Math.round((tile.height * LAYOUT_UNIT_PX) / RGL_ROW_HEIGHT));
      layout.push({
        i: tile.id,
        x: Math.min(x, GRID_COLUMNS - 1),
        y: y + 1,
        w: Math.min(w, GRID_COLUMNS - x),
        h,
        minW: 1,
        minH: 3,
      });
      x += w;
    });
    y += bandHeight + 2;
  };

  const emitTabHeader = (tab: DashboardTab) => {
    layout.push({
      i: `${TAB_HEADER_PREFIX}${tab.id}`,
      x: 0,
      y,
      w: GRID_COLUMNS,
      h: 2,
      minW: GRID_COLUMNS,
      minH: 2,
      static: true,
      isDraggable: false,
      isResizable: false,
    });
    y += 3;
  };

  const knownTabs = new Set(tabs.map(tab => tab.id));
  rows.filter(row => !row.tabId).forEach(emitRow);
  tabs.forEach(tab => {
    emitTabHeader(tab);
    rows.filter(row => row.tabId === tab.id).forEach(emitRow);
  });
  // Rows pointing at a tab that no longer exists stay editable instead of
  // silently dropping off the canvas.
  rows.filter(row => row.tabId && !knownTabs.has(row.tabId)).forEach(emitRow);
  return layout;
}

/**
 * Regroups a flat grid into rows using the row headers as band markers; tiles
 * dragged vertically attach to the closest preceding header.
 *
 * Superset stores no x coordinate: a tile's column is the running sum of the
 * preceding widths in its row, so a horizontal drag means "reorder inside the
 * row". Tiles are therefore grouped by band first (react-grid-layout pushes a
 * collided tile downwards, which must not be mistaken for a row change) and
 * only then ordered by their centre; `activeId` - the tile that was just
 * dropped - wins every tie, which reproduces the insert behaviour of the
 * original drag-and-drop editor.
 *
 * The tab header that precedes a band decides the row's `tabId`, which is what
 * makes dragging a row into another tab work.
 */
export function fromGridLayout(
  layout: GridItem[],
  previousRows: DashboardRow[],
  activeId?: string,
): DashboardRow[] {
  const previousById = new Map(previousRows.map(row => [row.id, row]));
  const tileById = new Map<string, RowTile>();
  previousRows.forEach(row => row.tiles.forEach(tile => tileById.set(tile.id, tile)));

  const headers = layout
    .filter(item => item.i.startsWith(ROW_HEADER_PREFIX))
    .sort((a, b) => a.y - b.y);
  const tabHeaders = layout
    .filter(item => item.i.startsWith(TAB_HEADER_PREFIX))
    .sort((a, b) => a.y - b.y);
  const tabAt = (y: number): string | null => {
    let id: string | null = null;
    tabHeaders.forEach(header => {
      if (header.y <= y) id = header.i.slice(TAB_HEADER_PREFIX.length);
    });
    return id;
  };

  const bandOf = (item: GridItem): number => {
    let index = -1;
    headers.forEach((header, position) => {
      if (header.y <= item.y) index = position;
    });
    return index;
  };

  const bands = new Map<number, GridItem[]>();
  layout
    .filter(item => !item.i.startsWith(ROW_HEADER_PREFIX) && !item.i.startsWith(TAB_HEADER_PREFIX))
    .forEach(item => {
      const key = bandOf(item);
      const bucket = bands.get(key);
      if (bucket) bucket.push(item);
      else bands.set(key, [item]);
    });

  const centre = (item: GridItem) => item.x + item.w / 2;
  const rows: DashboardRow[] = [];

  [...bands.keys()]
    .sort((a, b) => a - b)
    .forEach(key => {
      const band = bands.get(key)!;
      const rowId = key >= 0 ? headers[key].i.slice(ROW_HEADER_PREFIX.length) : null;
      const bandY = key >= 0 ? headers[key].y : Math.min(...band.map(item => item.y));
      const row: DashboardRow = {
        id: rowId ?? newId('ROW-'),
        background: (rowId ? previousById.get(rowId)?.background : undefined) ?? DEFAULT_BACKGROUND,
        tabId: tabAt(bandY),
        tiles: [],
      };
      band
        .sort((a, b) => {
          const delta = centre(a) - centre(b);
          if (delta !== 0) return delta;
          if (activeId && (a.i === activeId || b.i === activeId)) {
            return a.i === activeId ? 1 : -1;
          }
          return 0;
        })
        .forEach(item => {
          const tile = tileById.get(item.i);
          if (!tile) return;
          const height = clamp(Math.round((item.h * RGL_ROW_HEIGHT) / LAYOUT_UNIT_PX), 4, 200);
          row.tiles.push({
            ...tile,
            width: clamp(item.w, 1, GRID_COLUMNS),
            height,
          } as RowTile);
        });
      if (row.tiles.length) rows.push(row);
    });

  return rows;
}

/* ------------------------------------------------------------ mutations */

export function makeChartTile(chart: DashboardChart, width = 6): ChartTile {
  return {
    kind: 'chart',
    id: newId('CHART-'),
    chartId: chart.id,
    sliceName: chart.slice_name,
    width,
    height: DEFAULT_TILE_HEIGHT,
  };
}

/** Appends charts to a row, wrapping to a new row when the band is full. */
export function appendCharts(
  rows: DashboardRow[],
  rowId: string | null,
  charts: DashboardChart[],
): DashboardRow[] {
  if (!charts.length) return rows;
  const index = rows.findIndex(row => row.id === rowId);
  if (index < 0) return [...rows, ...createDefaultLayout(charts).rows];
  const target = rows[index];
  const used = target.tiles.reduce((total, tile) => total + tile.width, 0);
  const available = GRID_COLUMNS - used;
  const width = available >= 4 ? Math.floor(available / charts.length) || 4 : 4;
  const next = [...rows];
  next[index] = {
    ...target,
    tiles: normalizeRowWidths([
      ...target.tiles,
      ...charts.map(chart => makeChartTile(chart, width)),
    ]),
  };
  return next;
}

export function makeRow(tabId: string | null = null): DashboardRow {
  return { id: newId('ROW-'), background: DEFAULT_BACKGROUND, tabId, tiles: [] };
}

export function makeTab(title: string): DashboardTab {
  return { id: newId('TAB-'), title };
}

/**
 * Appends a tab together with its first (empty) row: a tab without rows would
 * otherwise be unreachable from the canvas.
 */
export function addTab(
  state: DashboardLayoutState,
  title: string,
): DashboardLayoutState {
  const tab = makeTab(title);
  return {
    ...state,
    tabs: [...state.tabs, tab],
    rows: [...state.rows, makeRow(tab.id)],
  };
}

/** Removes a tab and every row that belonged to it. */
export function removeTab(
  state: DashboardLayoutState,
  tabId: string,
): DashboardLayoutState {
  return {
    ...state,
    tabs: state.tabs.filter(tab => tab.id !== tabId),
    rows: state.rows.filter(row => row.tabId !== tabId),
  };
}

export function renameTab(
  state: DashboardLayoutState,
  tabId: string,
  title: string,
): DashboardLayoutState {
  return {
    ...state,
    tabs: state.tabs.map(tab => (tab.id === tabId ? { ...tab, title } : tab)),
  };
}

export function moveRow(
  rows: DashboardRow[],
  rowId: string,
  direction: -1 | 1,
): DashboardRow[] {
  const index = rows.findIndex(row => row.id === rowId);
  if (index < 0) return rows;
  // Movement is confined to the row's own tab so its membership never changes
  // as a side effect of reordering.
  const target = rows[index];
  const siblings = rows.filter(row => row.tabId === target.tabId);
  const position = siblings.findIndex(row => row.id === rowId);
  const neighbour = siblings[position + direction];
  if (!neighbour) return rows;
  const nextIndex = rows.findIndex(row => row.id === neighbour.id);
  const copy = [...rows];
  copy[index] = rows[nextIndex];
  copy[nextIndex] = target;
  return copy;
}
