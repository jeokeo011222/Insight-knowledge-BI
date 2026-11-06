import React from 'react';

export interface SkeletonShape {
  w?: number | string;
  h?: number | string;
  r?: number; // border-radius in px
  circle?: boolean;
  inline?: boolean;
  flex?: number | string;
  className?: string;
  style?: React.CSSProperties;
}

export interface SkeletonProps {
  /** Shimmer + grey-out animation. Enabled by default. */
  animated?: boolean;
  /** Gap between stacked rows in px (only when rows > 1). */
  gap?: number;
  /** Spacing between cells inside a row, in px. */
  cellGap?: number;
  children?: React.ReactNode;
  className?: string;
  style?: React.CSSProperties;
}

/**
 * Primitive block: one grey placeholder rectangle (or circle).
 * Builds on .zg-skeleton CSS + .zg-shimmer animation declared in global.css.
 */
export function SkeletonBlock({
  w = '100%',
  h = 14,
  r = 4,
  circle = false,
  animated = true,
  inline = false,
  className = '',
  style,
}: SkeletonShape & { animated?: boolean }) {
  const s: React.CSSProperties = {
    width: w,
    height: circle ? (typeof w === 'number' ? w : h) : h,
    borderRadius: circle ? '50%' : r,
    ...(circle ? { aspectRatio: '1 / 1' } : {}),
    ...(inline ? { display: 'inline-block', verticalAlign: 'middle' } : {}),
    ...style,
  };
  return (
    <div
      className={`zg-skeleton${animated ? ' zg-shimmer' : ''} ${className}`}
      style={s}
      aria-hidden="true"
    />
  );
}

/**
 * Stacks N identical rows (for table bodies, KPI lists, etc.).
 * Each row is an array of SkeletonShape specs rendered left-to-right.
 */
export function SkeletonRows({
  rows = 5,
  gap = 10,
  cellGap = 12,
  animated = true,
  shapes,
  rowHeight = 14,
}: {
  rows?: number;
  gap?: number;
  cellGap?: number;
  animated?: boolean;
  shapes?: SkeletonShape[]; // per-cell spec; defaults to one full-width rect
  rowHeight?: number;
}) {
  const cells: SkeletonShape[] = shapes ?? [{ h: rowHeight }];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap }}>
      {Array.from({ length: rows }).map((_, ri) => (
        <div key={ri} style={{ display: 'flex', alignItems: 'center', gap: cellGap }}>
          {cells.map((c, ci) => (
            <SkeletonBlock key={ci} animated={animated} {...c} />
          ))}
        </div>
      ))}
    </div>
  );
}

/* ------------------------------- presets ------------------------------- */

/** KPI card skeleton: label + big number + optional trend pill. */
export function SkeletonKpi({ animated = true }: { animated?: boolean }) {
  return (
    <div style={{ padding: 14, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <SkeletonBlock animated={animated} w="40%" h={12} r={3} />
      <SkeletonBlock animated={animated} w="75%" h={28} r={4} />
      <SkeletonBlock animated={animated} w={80} h={16} r={8} />
    </div>
  );
}

/** Table body skeleton: header row + N data rows. */
export function SkeletonTable({
  rows = 6,
  cols = 4,
  animated = true,
}: {
  rows?: number;
  cols?: number;
  animated?: boolean;
}) {
  const headerShapes: SkeletonShape[] = Array.from({ length: cols }).map(() => ({ h: 12 }));
  const bodyShapes: SkeletonShape[] = Array.from({ length: cols }).map((_, i) => ({
    h: 14,
    w: i === 0 ? '45%' : 'auto',
    flex: 1,
  }));
  return (
    <div style={{ padding: 8 }}>
      <SkeletonRows rows={1} gap={8} cellGap={10} animated={animated} shapes={headerShapes} />
      <div style={{ height: 1, background: 'var(--color-border)', margin: '10px 0' }} />
      <SkeletonRows rows={rows} gap={10} cellGap={10} animated={animated} shapes={bodyShapes} />
    </div>
  );
}

/** Chart skeleton: title bar + big plot area + legend row. */
export function SkeletonChart({ animated = true, height = 260 }: { animated?: boolean; height?: number }) {
  return (
    <div style={{ padding: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <SkeletonBlock animated={animated} w={140} h={14} r={3} />
        <SkeletonBlock animated={animated} w={60} h={12} r={3} />
      </div>
      <SkeletonBlock animated={animated} w="100%" h={height} r={6} />
      <div style={{ display: 'flex', gap: 16 }}>
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <SkeletonBlock animated={animated} w={10} h={10} r={3} />
            <SkeletonBlock animated={animated} w={50 + i * 20} h={11} r={2} />
          </div>
        ))}
      </div>
    </div>
  );
}

/** Dashboard card-grid skeleton: N KPI tiles + one chart tile. */
export function SkeletonDashboardGrid({ animated = true }: { animated?: boolean }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12 }}>
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="zg-card">
            <SkeletonKpi animated={animated} />
          </div>
        ))}
      </div>
      <div className="zg-card">
        <SkeletonChart animated={animated} height={300} />
      </div>
    </div>
  );
}
