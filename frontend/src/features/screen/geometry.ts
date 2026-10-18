/**
 * Canvas geometry: grid snapping and smart alignment guides.
 *
 * The editor works in *design* coordinates (e.g. 1920×1080) and scales with a
 * CSS transform, so every pointer delta is divided by the zoom factor before
 * it reaches this module.
 */

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Guide {
  orientation: 'v' | 'h';
  position: number;
}

export interface SnapResult {
  rect: Rect;
  guides: Guide[];
}

export type AlignMode = 'left' | 'hcenter' | 'right' | 'top' | 'vcenter' | 'bottom';

const SNAP_THRESHOLD = 6;

function nearestDelta(target: number, candidates: number[]): { delta: number; hit?: number } {
  let best = Number.POSITIVE_INFINITY;
  let hit: number | undefined;
  for (const candidate of candidates) {
    const delta = candidate - target;
    if (Math.abs(delta) < Math.abs(best) && Math.abs(delta) <= SNAP_THRESHOLD) {
      best = delta;
      hit = candidate;
    }
  }
  return { delta: Number.isFinite(best) ? best : 0, hit };
}

/**
 * Snaps the moving rectangle to the grid and to other components' edges and
 * centres. Guides are returned so the canvas can draw the magenta lines.
 */
export function snapRect(
  moving: Rect,
  others: Rect[],
  canvas: { width: number; height: number },
  gridSize: number,
  useGrid: boolean,
): SnapResult {
  let x = moving.x;
  let y = moving.y;
  if (useGrid && gridSize > 1) {
    x = Math.round(x / gridSize) * gridSize;
    y = Math.round(y / gridSize) * gridSize;
  }

  const vCandidates = [0, canvas.width / 2, canvas.width];
  const hCandidates = [0, canvas.height / 2, canvas.height];
  for (const other of others) {
    vCandidates.push(other.x, other.x + other.w / 2, other.x + other.w);
    hCandidates.push(other.y, other.y + other.h / 2, other.y + other.h);
  }

  const guides: Guide[] = [];

  const left = nearestDelta(x, vCandidates);
  const right = nearestDelta(x + moving.w, vCandidates);
  const centerX = nearestDelta(x + moving.w / 2, vCandidates);
  const pickX = [left, right, centerX].sort(
    (a, b) => Math.abs(a.delta) - Math.abs(b.delta),
  )[0];
  if (pickX.hit !== undefined) {
    x += pickX.delta;
    guides.push({ orientation: 'v', position: pickX.hit });
  }

  const top = nearestDelta(y, hCandidates);
  const bottom = nearestDelta(y + moving.h, hCandidates);
  const centerY = nearestDelta(y + moving.h / 2, hCandidates);
  const pickY = [top, bottom, centerY].sort(
    (a, b) => Math.abs(a.delta) - Math.abs(b.delta),
  )[0];
  if (pickY.hit !== undefined) {
    y += pickY.delta;
    guides.push({ orientation: 'h', position: pickY.hit });
  }

  return { rect: { ...moving, x, y }, guides };
}

/** Aligns a set of rectangles inside the union of their bounds. */
export function alignRects(rects: Rect[], mode: AlignMode): Rect[] {
  if (rects.length < 2) return rects;
  const minX = Math.min(...rects.map(r => r.x));
  const maxX = Math.max(...rects.map(r => r.x + r.w));
  const minY = Math.min(...rects.map(r => r.y));
  const maxY = Math.max(...rects.map(r => r.y + r.h));
  return rects.map(r => {
    switch (mode) {
      case 'left':
        return { ...r, x: minX };
      case 'hcenter':
        return { ...r, x: Math.round((minX + maxX) / 2 - r.w / 2) };
      case 'right':
        return { ...r, x: maxX - r.w };
      case 'top':
        return { ...r, y: minY };
      case 'vcenter':
        return { ...r, y: Math.round((minY + maxY) / 2 - r.h / 2) };
      case 'bottom':
        return { ...r, y: maxY - r.h };
      default:
        return r;
    }
  });
}

/** Spreads rectangles evenly between the first and last one on an axis. */
export function distributeRects(rects: Rect[], axis: 'h' | 'v'): Rect[] {
  if (rects.length < 3) return rects;
  const order = [...rects].sort((a, b) => (axis === 'h' ? a.x - b.x : a.y - b.y));
  const first = order[0];
  const last = order[order.length - 1];
  const total = axis === 'h'
    ? last.x + last.w - first.x
    : last.y + last.h - first.y;
  const occupied = order.reduce((sum, r) => sum + (axis === 'h' ? r.w : r.h), 0);
  const gap = (total - occupied) / (order.length - 1);
  let cursor = axis === 'h' ? first.x : first.y;
  const placed = new Map<string, Rect>();
  for (const rect of order) {
    placed.set(`${rect.x}:${rect.y}:${rect.w}:${rect.h}`, {
      ...rect,
      ...(axis === 'h' ? { x: Math.round(cursor) } : { y: Math.round(cursor) }),
    });
    cursor += (axis === 'h' ? rect.w : rect.h) + gap;
  }
  return rects.map(r => placed.get(`${r.x}:${r.y}:${r.w}:${r.h}`) ?? r);
}

export function intersects(a: Rect, b: Rect): boolean {
  return !(a.x + a.w < b.x || b.x + b.w < a.x || a.y + a.h < b.y || b.y + b.h < a.y);
}
