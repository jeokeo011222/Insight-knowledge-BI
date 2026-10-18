import { createServer } from 'vite';
const server = await createServer({ configFile: false, server: { middlewareMode: true }, root: process.cwd() });
const { fromGridLayout, toGridLayout } = await server.ssrLoadModule('/src/features/dashboard/layout.ts');

const rows = [{
  id: 'ROW-1', background: 'BACKGROUND_TRANSPARENT',
  tiles: [
    { kind: 'chart', id: 'CHART-1', chartId: 1, sliceName: 'A', width: 6, height: 50 },
    { kind: 'chart', id: 'CHART-2', chartId: 2, sliceName: 'B', width: 6, height: 50 },
  ],
}];

const grid = toGridLayout(rows);
const show = list => list.map(r => r.tiles.map(t => t.id).join('+')).join(' / ');

// RGL pushes the collided tile down: CHART-1 @6,1 and CHART-2 @6,11
const pushed = grid.map(g => (g.i === 'CHART-1' ? { ...g, x: 6 } : g)).map(g => (g.i === 'CHART-2' ? { ...g, y: 11 } : g));
console.log('pushed+active   :', show(fromGridLayout(pushed, rows, 'CHART-1')));
console.log('pushed no-active:', show(fromGridLayout(pushed, rows)));

// A genuine vertical drag below the band keeps both tiles but changes nothing
const same = grid.map(g => (g.i === 'CHART-1' ? { ...g, y: 12 } : g));
console.log('vertical only   :', show(fromGridLayout(same, rows, 'CHART-1')));

// No movement at all -> identical rows
console.log('unchanged?      :', JSON.stringify(fromGridLayout(grid, rows)) === JSON.stringify(rows));
await server.close();
