import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import {
  AXIS_COLOR,
  PRIMARY,
  TOOLTIP_STYLE,
  formatCompact,
  gridOption,
  horizontalValueAxis,
  makeNumberFormatter,
  readBool,
  readNumber,
  readString,
  toNumber,
  verticalCategoryAxis,
  type VizContext,
  type VizRenderer,
} from '../utils';
import { metricColumn, resolveSeries } from '../dataUtils';
import { buildCartesian } from './timeseries';
import { CHART_COLORS, SUBTLE_BG, TEXT_STRONG, TRACK_COLOR } from '../utils';

function currentValue(ctx: VizContext, aggregate: boolean): number | null {
  const metric = metricColumn(ctx);
  if (!metric) return null;
  if (aggregate && ctx.rows.length > 1) {
    const total = ctx.rows.reduce((sum, row) => sum + (toNumber(row[metric]) ?? 0), 0);
    return total;
  }
  return toNumber(ctx.rows[0]?.[metric]);
}

function sparkline(ctx: VizContext, metric: string): EChartsOption | null {
  const resolved = resolveSeries(ctx);
  const series = resolved?.series[0];
  if (!series) return null;
  return {
    animationDuration: 200,
    grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: 'category', show: false, data: resolved.xValues },
    yAxis: { type: 'value', show: false, scale: true },
    tooltip: {
      trigger: 'axis',
      formatter: (params: unknown) => {
        const list = params as Array<{ axisValue?: string; value?: number }>;
        const first = list[0];
        return `${first?.axisValue ?? ''}: ${formatCompact(Number(first?.value ?? 0))}`;
      },
      ...TOOLTIP_STYLE,
    },
    series: [
      {
        type: 'line',
        name: metric,
        data: series.values,
        smooth: 0.35,
        showSymbol: false,
        lineStyle: { color: PRIMARY, width: 1.6 },
        areaStyle: { color: PRIMARY, opacity: 0.14 },
      },
    ],
  };
}

function renderBigNumber(ctx: VizContext, aggregate: boolean) {
  const metric = metricColumn(ctx);
  if (!metric) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.big_number_need_numeric')} />;
  const value = currentValue(ctx, aggregate);
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const label = readString(ctx.formData, ['metric_name', 'header_text']) ?? metric;
  const subheader = readString(ctx.formData, ['subheader']) ?? '';
  const showTrend = readBool(
    ctx.formData,
    'show_trend_line',
    readBool(ctx.formData, 'trendline', !aggregate),
  );

  const spark = showTrend && ctx.rows.length > 1 ? sparkline(ctx, metric) : null;
  const sparkHeight = Math.min(56, Math.max(30, Math.round(ctx.height * 0.2)));

  let delta: number | null = null;
  if (ctx.rows.length > 1) {
    const first = toNumber(ctx.rows[0][metric]);
    const last = toNumber(ctx.rows[ctx.rows.length - 1][metric]);
    if (first !== null && last !== null && first !== 0) delta = ((last - first) / Math.abs(first)) * 100;
  }

  return (
    <div
      style={{
        height: ctx.height,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        textAlign: 'center',
        padding: ctx.compact ? '8px 10px' : '16px 18px',
      }}
    >
      <div className="tiny-label" style={{ fontSize: ctx.compact ? 10 : 12 }} title={label}>
        {label}
      </div>
      <div
        style={{
          fontSize: ctx.compact ? 28 : Math.min(52, Math.max(30, ctx.height * 0.26)),
          fontWeight: 600,
          lineHeight: 1.15,
          color: TEXT_STRONG,
          marginTop: 2,
        }}
      >
        {value === null ? '—' : format(value)}
      </div>
      {delta !== null && (
        <div style={{ fontSize: 11, color: delta >= 0 ? 'var(--color-success)' : 'var(--color-error)', marginTop: 2 }}>
          {delta >= 0 ? '▲' : '▼'} {Math.abs(delta).toFixed(1)}%
        </div>
      )}
      {subheader && <div style={{ fontSize: 11, color: AXIS_COLOR, marginTop: 2 }}>{subheader}</div>}
      {spark && (
        <div style={{ width: '100%', marginTop: 6 }}>
          <EChart option={spark} height={sparkHeight} />
        </div>
      )}
    </div>
  );
}

export const BigNumberRenderer: VizRenderer = ({ ctx }) => renderBigNumber(ctx, false);

export const BigNumberTotalRenderer: VizRenderer = ({ ctx }) => renderBigNumber(ctx, true);

export const GaugeRenderer: VizRenderer = ({ ctx }) => {
  const metric = metricColumn(ctx);
  const value = currentValue(ctx, false);
  if (!metric || value === null) return <ChartEmpty height={ctx.height} />;
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const max = readNumber(ctx.formData, ['max_value', 'gauge_max']) ?? (Math.abs(value) * 1.25 || 100);
  const min = readNumber(ctx.formData, ['min_value', 'gauge_min']) ?? 0;

  const option: EChartsOption = {
    tooltip: { formatter: '{b}: {c}', ...TOOLTIP_STYLE },
    series: [
      {
        type: 'gauge',
        min,
        max: max === min ? min + 1 : max,
        startAngle: 210,
        endAngle: -30,
        radius: '82%',
        center: ['50%', '58%'],
        progress: { show: true, width: ctx.compact ? 9 : 12, itemStyle: { color: PRIMARY } },
        axisLine: { lineStyle: { width: ctx.compact ? 9 : 12, color: [[1, SUBTLE_BG]] } },
        pointer: { width: 4, length: '58%', itemStyle: { color: CHART_COLORS[1] } },
        axisTick: { distance: -18, length: 4, lineStyle: { color: TRACK_COLOR } },
        splitLine: { distance: -22, length: 10, lineStyle: { color: TRACK_COLOR } },
        axisLabel: { distance: -4, fontSize: 9, color: AXIS_COLOR },
        title: { fontSize: 11, color: AXIS_COLOR, offsetCenter: [0, '82%'] },
        detail: {
          valueAnimation: true,
          fontSize: ctx.compact ? 18 : 24,
          fontWeight: 600,
          color: TEXT_STRONG,
          offsetCenter: [0, '52%'],
          formatter: (current: number) => format(current),
        },
        data: [{ value, name: metric }],
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const LiquidRenderer: VizRenderer = ({ ctx }) => {
  const metric = metricColumn(ctx);
  const value = currentValue(ctx, false);
  if (!metric || value === null) return <ChartEmpty height={ctx.height} />;
  const max =
    readNumber(ctx.formData, ['max_value', 'liquid_max']) ?? (Math.abs(value) * 1.25 || 100);
  const safeMax = max === 0 ? 1 : max;

  const option: EChartsOption = {
    series: [
      {
        type: 'gauge',
        min: 0,
        max: safeMax,
        startAngle: 220,
        endAngle: -40,
        radius: '82%',
        center: ['50%', '56%'],
        pointer: { show: false },
        progress: {
          show: true,
          roundCap: true,
          width: ctx.compact ? 10 : 14,
          itemStyle: { color: PRIMARY },
        },
        axisLine: { lineStyle: { width: ctx.compact ? 10 : 14, color: [[1, SUBTLE_BG]] } },
        axisTick: { show: false },
        splitLine: { show: false },
        axisLabel: { show: false },
        title: { fontSize: 11, color: AXIS_COLOR, offsetCenter: [0, '74%'] },
        detail: {
          valueAnimation: true,
          fontSize: ctx.compact ? 20 : 26,
          fontWeight: 600,
          color: TEXT_STRONG,
          offsetCenter: [0, '26%'],
          formatter: (current: number) => `${((current / safeMax) * 100).toFixed(0)}%`,
        },
        data: [{ value, name: metric }],
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const BulletRenderer: VizRenderer = ({ ctx }) => {
  const metric = metricColumn(ctx);
  const value = currentValue(ctx, false);
  if (!metric || value === null) return <ChartEmpty height={ctx.height} />;
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const target =
    readNumber(ctx.formData, ['target', 'bullet_target']) ??
    (metric ? toNumber(ctx.rows[0]?.[`${metric} target`]) : null) ??
    value * 1.1;
  const max = readNumber(ctx.formData, ['max_value']) ?? Math.max(value, target) * 1.2;

  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { left: 8, right: 26, top: 24, bottom: 20 }),
    tooltip: { trigger: 'axis', ...TOOLTIP_STYLE },
    xAxis: horizontalValueAxis({ formatter: format, max }),
    yAxis: verticalCategoryAxis([metric]),
    series: [
      {
        type: 'bar',
        name: metric,
        data: [value],
        barWidth: ctx.compact ? 18 : 26,
        showBackground: true,
        backgroundStyle: { color: SUBTLE_BG },
        itemStyle: { color: PRIMARY },
        label: {
          show: true,
          position: 'right',
          fontSize: 11,
          color: TEXT_STRONG,
          formatter: () => format(value),
        },
        markLine: {
          symbol: 'none',
          silent: true,
          lineStyle: { color: CHART_COLORS[1], width: 2, type: 'solid' },
          label: { show: true, position: 'end', fontSize: 10, color: CHART_COLORS[1], formatter: 'target' },
          data: [{ xAxis: target }],
        },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const CompareRenderer: VizRenderer = ({ ctx }) => {
  const option = buildCartesian(ctx, { type: 'bar', mixed: true });
  if (!option) return <ChartEmpty height={ctx.height} />;
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

