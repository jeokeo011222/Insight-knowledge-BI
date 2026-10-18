/**
 * Exercises parseLayout / buildPositionJson / toGridLayout / fromGridLayout
 * against a real Superset v2 document that uses the TABS container.
 *
 * Run:  pnpm test:layout        (from the frontend directory)
 *
 * Covers: tab parsing, tab membership of rows, parents/children rebuilding,
 * round-trip stability, empty tabs, tab separators in the grid, dragging a
 * tile across tabs, flattening of COLUMN containers and documents without tabs.
 */
import { createServer } from 'vite';

const server = await createServer({
  configFile: 'vite.config.ts',
  server: { middlewareMode: true },
  logLevel: 'error',
});

const mod = await server.ssrLoadModule('/src/features/dashboard/layout.ts');
const { parseLayout, buildPositionJson, toGridLayout, fromGridLayout } = mod;

const positionJson = {
  DASHBOARD_VERSION_KEY: 'v2',
  ROOT_ID: { id: 'ROOT_ID', type: 'ROOT', children: ['GRID_ID'], parents: [] },
  HEADER_ID: { id: 'HEADER_ID', type: 'HEADER', meta: { text: 'Sales', headerSize: 'LARGE_HEADER' } },
  GRID_ID: { id: 'GRID_ID', type: 'GRID', children: ['TABS_ID'], parents: ['ROOT_ID'] },
  TABS_ID: { id: 'TABS_ID', type: 'TABS', children: ['TAB-alpha', 'TAB-beta'], parents: ['ROOT_ID', 'GRID_ID'], meta: {} },
  'TAB-alpha': {
    id: 'TAB-alpha', type: 'TAB', children: ['ROW-a1'], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID'],
    meta: { label: 'Overview', default: true },
  },
  'TAB-beta': {
    id: 'TAB-beta', type: 'TAB', children: ['ROW-b1'], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID'],
    meta: { label: 'Detail' },
  },
  'ROW-a1': { id: 'ROW-a1', type: 'ROW', children: ['CHART-1', 'CHART-2'], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-alpha'], meta: { background: 'BACKGROUND_LIGHT' } },
  'ROW-b1': { id: 'ROW-b1', type: 'ROW', children: ['CHART-3'], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-beta'], meta: { background: 'BACKGROUND_TRANSPARENT' } },
  'CHART-1': { id: 'CHART-1', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-alpha', 'ROW-a1'], meta: { width: 6, height: 50, chartId: 1, sliceName: 'By month' } },
  'CHART-2': { id: 'CHART-2', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-alpha', 'ROW-a1'], meta: { width: 6, height: 50, chartId: 2, sliceName: 'By state' } },
  'CHART-3': { id: 'CHART-3', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-beta', 'ROW-b1'], meta: { width: 12, height: 40, chartId: 3, sliceName: 'Pie' } },
};

const charts = [
  { id: 1, slice_name: 'By month' },
  { id: 2, slice_name: 'By state' },
  { id: 3, slice_name: 'Pie' },
];

const state = parseLayout(positionJson, charts);
const check = (label, actual, expected) => {
  const ok = JSON.stringify(actual) === JSON.stringify(expected);
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}${ok ? '' : `\n      got=${JSON.stringify(actual)}\n      exp=${JSON.stringify(expected)}`}`);
  if (!ok) process.exitCode = 1;
};

check('tabs parsed', state.tabs.map(t => t.title), ['Overview', 'Detail']);
check('row tab membership', state.rows.map(r => [r.id, r.tabId]), [
  ['ROW-a1', 'TAB-alpha'],
  ['ROW-b1', 'TAB-beta'],
]);
check('tile counts', state.rows.map(r => r.tiles.length), [2, 1]);
check('background kept', state.rows.map(r => r.background), ['BACKGROUND_LIGHT', 'BACKGROUND_TRANSPARENT']);

const rebuilt = buildPositionJson(state);
check('grid -> tabs', rebuilt.GRID_ID.children, ['TABS_ID']);
check('tabs children', rebuilt.TABS_ID.children, ['TAB-alpha', 'TAB-beta']);
check('tab defaults', [rebuilt['TAB-alpha'].meta.default, rebuilt['TAB-beta'].meta.default], [true, undefined]);
check('tab alpha rows', rebuilt['TAB-alpha'].children, ['ROW-a1']);
check('row parents', rebuilt['ROW-a1'].parents, ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-alpha']);
check('tile parents', rebuilt['CHART-1'].parents, ['ROOT_ID', 'GRID_ID', 'TABS_ID', 'TAB-alpha', 'ROW-a1']);

// Round trip must be stable.
const again = parseLayout(rebuilt, charts);
check('round trip rows', again.rows.map(r => [r.id, r.tabId, r.tiles.length]), [
  ['ROW-a1', 'TAB-alpha', 2],
  ['ROW-b1', 'TAB-beta', 1],
]);
check('round trip tabs', again.tabs, state.tabs);

// Grid projection carries the tab separators.
const grid = toGridLayout(state.rows, state.tabs);
const tabHeads = grid.filter(i => i.i.startsWith('TABHEAD:')).map(i => i.i);
check('tab separators', tabHeads, ['TABHEAD:TAB-alpha', 'TABHEAD:TAB-beta']);
check('separators are static', grid.filter(i => i.i.startsWith('TABHEAD:')).every(i => i.static === true), true);

// Moving CHART-3 above the alpha tab header makes it join tab alpha.
const moved = grid.map(item =>
  item.i === 'CHART-3' ? { ...item, y: 1, x: 0 } : item,
);
const regrouped = fromGridLayout(moved, state.rows, 'CHART-3');
console.log('       after drag, rows =', JSON.stringify(regrouped.map(r => [r.id, r.tabId, r.tiles.map(t => t.id)])));

// A tab with no rows survives save/load.
const withEmpty = { ...state, tabs: [...state.tabs, { id: 'TAB-empty', title: 'Empty' }] };
const emptyDoc = buildPositionJson(withEmpty);
check('empty tab kept', emptyDoc['TAB-empty'].children, []);
check('empty tab label', emptyDoc['TAB-empty'].meta.label, 'Empty');

// No-tab documents keep the flat shape.
const flat = parseLayout(
  {
    GRID_ID: { id: 'GRID_ID', type: 'GRID', children: ['ROW-1'], parents: ['ROOT_ID'] },
    'ROW-1': { id: 'ROW-1', type: 'ROW', children: ['CHART-1'], parents: ['ROOT_ID', 'GRID_ID'], meta: { background: 'BACKGROUND_TRANSPARENT' } },
    'CHART-1': { id: 'CHART-1', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'ROW-1'], meta: { width: 12, chartId: 1 } },
  },
  charts.slice(0, 1),
);
check('flat rows', flat.rows.map(r => [r.id, r.tabId]), [['ROW-1', null]]);
check('flat has no tabs', flat.tabs, []);
const flatDoc = buildPositionJson(flat);
check('flat grid children', flatDoc.GRID_ID.children, ['ROW-1']);
check('flat has no TABS node', flatDoc.TABS_ID, undefined);


// A COLUMN container must be flattened, not dropped.
const withColumn = parseLayout(
  {
    GRID_ID: { id: 'GRID_ID', type: 'GRID', children: ['ROW-1'], parents: ['ROOT_ID'] },
    'ROW-1': { id: 'ROW-1', type: 'ROW', children: ['COLUMN-x'], parents: ['ROOT_ID', 'GRID_ID'], meta: { background: 'BACKGROUND_TRANSPARENT' } },
    'COLUMN-x': { id: 'COLUMN-x', type: 'COLUMN', children: ['CHART-1', 'CHART-2'], parents: ['ROOT_ID', 'GRID_ID', 'ROW-1'], meta: { width: 12 } },
    'CHART-1': { id: 'CHART-1', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'ROW-1', 'COLUMN-x'], meta: { width: 6, chartId: 1 } },
    'CHART-2': { id: 'CHART-2', type: 'CHART', children: [], parents: ['ROOT_ID', 'GRID_ID', 'ROW-1', 'COLUMN-x'], meta: { width: 6, chartId: 2 } },
  },
  charts,
);
check('column flattened into first row', withColumn.rows[0].tiles.map(t => t.chartId), [1, 2]);
check('unplaced chart still appended', withColumn.rows.length, 2);
check('column widths sum', withColumn.rows[0].tiles.reduce((a, t) => a + t.width, 0) <= 12, true);

await server.close();
console.log(process.exitCode ? '\nFAILURES PRESENT' : '\nall checks passed');
