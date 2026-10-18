import ReactEChartsCore from 'echarts-for-react/lib/core';
import type { EChartsOption } from 'echarts';
import { useEffect, useRef } from 'react';
import { echarts } from './echartsSetup';
import { useI18n } from '../../i18n';

export interface EChartProps {
  option: EChartsOption;
  height: number;
  onClickData?: (params: unknown) => void;
  /** Text alternative for screen readers ("Pie: girl 52%, boy 48%"). */
  ariaLabel?: string;
}

/** The slice of the echarts instance we use; keeps the ref typed. */
interface EChartsHandle {
  getEchartsInstance: () => { resize: () => void } | undefined;
}

const RESIZE_DEBOUNCE_MS = 80;

/**
 * Thin echarts wrapper: SVG renderer, no merge, optional click callback.
 *
 * echarts-for-react only listens to window resizes, so a dashboard tile that is
 * dragged smaller (or a collapsed sidebar) used to leave the chart at its old
 * size — overflowing or leaving a gap. A ResizeObserver on the *container*
 * fixes that; the 80 ms debounce keeps a drag from re-laying out on every frame.
 */
export function EChart({ option, height, onClickData, ariaLabel }: EChartProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<ReactEChartsCore | null>(null);

  useEffect(() => {
    const element = containerRef.current;
    if (!element || typeof ResizeObserver === 'undefined') return undefined;
    let timer: number | undefined;
    const observer = new ResizeObserver(() => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        (chartRef.current as unknown as EChartsHandle | null)?.getEchartsInstance()?.resize();
      }, RESIZE_DEBOUNCE_MS);
    });
    observer.observe(element);
    return () => {
      window.clearTimeout(timer);
      observer.disconnect();
    };
  }, []);

  return (
    <div
      ref={containerRef}
      style={{ width: '100%' }}
      role="img"
      aria-label={ariaLabel ?? summarizeOption(option)}
    >
      <ReactEChartsCore
        ref={chartRef}
        echarts={echarts}
        option={option}
        notMerge
        lazyUpdate
        style={{ height, width: '100%' }}
        opts={{ renderer: 'svg' }}
        onEvents={onClickData ? { click: (params: unknown) => onClickData(params) } : undefined}
      />
    </div>
  );
}

/** Builds a short text alternative when the caller does not supply one. */
function summarizeOption(option: EChartsOption): string {
  const raw = option.series as unknown;
  const list = Array.isArray(raw) ? raw : raw ? [raw] : [];
  const parts: string[] = [];
  list.slice(0, 3).forEach(item => {
    if (!item || typeof item !== 'object') return;
    const series = item as { type?: unknown; name?: unknown; data?: unknown };
    const type = typeof series.type === 'string' ? series.type : 'chart';
    const name = typeof series.name === 'string' ? series.name : '';
    const count = Array.isArray(series.data) ? series.data.length : 0;
    parts.push(`${name || type}${count ? ` (${count})` : ''}`);
  });
  return parts.join(', ') || 'chart';
}

export interface ChartEmptyProps {
  height?: number;
  message?: string;
  /** Shown under the message: what the user can do to get data back. */
  hint?: string;
}

/** Empty state with an actionable hint instead of a grey one-liner. */
export function ChartEmpty({ height, message, hint }: ChartEmptyProps) {
  const { t } = useI18n();
  const displayMessage = message ?? t('common.state.no_chart_data');
  return (
    <div
      className="cell-muted chart-empty"
      style={{
        height,
        minHeight: 48,
        display: 'flex',
        flexDirection: 'column',
        gap: 4,
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 12,
        padding: 12,
        textAlign: 'center',
      }}
    >
      <span>{displayMessage}</span>
      <span className="chart-empty-hint" style={{ opacity: 0.72, fontSize: 11 }}>
        {hint ?? t('chart.empty.hint')}
      </span>
    </div>
  );
}
