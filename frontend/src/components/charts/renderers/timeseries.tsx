import type { EChartsOption, SeriesOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import { buildAnnotationMarks, type AnnotationMark } from '../annotationMarks';
import { AXIS_COLOR, CHART_COLORS, FONT_SIZE, categoryAxis, gridOption, seriesColor } from '../utils';
import {
  TOOLTIP_STYLE,
  makeNumberFormatter,
  parseTime,
  readBool,
  readString,
  toNumber,
  valueAxis,
  type VizContext,
  type VizRenderer,
} from '../utils';
import {
  resolveSeries,
  type ResolvedSeries,
} from '../dataUtils';
import { TEXT_MUTED } from '../utils';

interface CartesianOptions {
  type: 'line' | 'bar' | 'scatter';
  smooth?: boolean;
  step?: boolean;
  area?: boolean;
  bubble?: boolean;
  mixed?: boolean;
  annotate?: boolean;
}

function timeFormatter(ctx: VizContext, resolved: ResolvedSeries): (value: string) => string {
  if (!resolved.isTime) return value => value;
  const declared = readString(ctx.formData, ['x_axis_time_format']);
  const first = parseTime(resolved.xValues[0]);
  const last = parseTime(resolved.xValues[resolved.xValues.length - 1]);
  const spanDays = first && last ? Math.abs(last.diff(first, 'day')) : 0;
  return value => {
    const parsed = parseTime(value);
    if (!parsed) return value;
    if (declared && declared !== 'smart_date') return parsed.format(declared);
    return spanDays > 2 ? parsed.format('MMM D') : parsed.format('HH:mm');
  };
}

export function buildCartesian(
  ctx: VizContext,
  options: CartesianOptions,
): EChartsOption | null {
  const resolved = resolveSeries(ctx);
  if (!resolved || !resolved.series.length) return null;

  const { formData } = ctx;
  const stack = readBool(formData, 'stack') || formData.stack === 'stack';
  const normalize = readBool(formData, 'normalize') || formData.normalize === 'normalize';
  const showLegend = readBool(formData, 'show_legend', true);
  const richTooltip = readBool(formData, 'rich_tooltip', true);
  const logAxis = readBool(formData, 'logAxis');
  const minorTicks = readBool(formData, 'minorTicks', false);
  const numberFormat = readString(formData, ['y_axis_format', 'number_format']);
  const formatter = makeNumberFormatter(numberFormat);
  const formatX = timeFormatter(ctx, resolved);

  const sizeKey = readString(formData, ['size', 'bubble_size', 'metric_size']);
  const sizeValues =
    options.bubble && sizeKey && ctx.colnames.includes(sizeKey)
      ? ctx.rows.map(row => toNumber(row[sizeKey]) ?? 0)
      : [];
  const maxSize = sizeValues.length ? Math.max(...sizeValues) : 1;

  let data = resolved.series;
  if (options.bubble && sizeKey) {
    data = data.filter(spec => spec.name !== sizeKey);
  }
  if (normalize) {
    const totals = resolved.xValues.map((_, index) =>
      data.reduce((sum, spec) => sum + (spec.values[index] ?? 0), 0),
    );
    data = data.map(spec => ({
      name: spec.name,
      values: spec.values.map((value, index) =>
        totals[index] ? ((value ?? 0) / totals[index]) * 100 : 0,
      ),
    }));
  }

  const buildSeries = (spec: { name: string; values: Array<number | null> }, index: number): SeriesOption => {
    // Named series colour consistently across charts; single series stays canonical.
    const color = seriesColor(spec.name, index, data.length);
    const seriesType = options.mixed && index > 0 ? 'line' : options.type;
    const shared = {
      name: spec.name,
      ...(stack || normalize ? { stack: 'total' } : {}),
      emphasis: { focus: 'series' as const },
    };
    if (seriesType === 'line') {
      const line: SeriesOption = {
        ...shared,
        type: 'line',
        data: spec.values,
        smooth: options.smooth ? 0.35 : false,
        step: options.step ? 'middle' : undefined,
        showSymbol: false,
        symbol: 'circle',
        symbolSize: 4,
        lineStyle: { width: 2 },
        itemStyle: { color },
        areaStyle: options.area || normalize ? { opacity: 0.22 } : undefined,
      };
      if (options.annotate) {
        line.markLine = {
          symbol: 'none',
          silent: true,
          lineStyle: { color: AXIS_COLOR, type: 'dashed' },
          label: { color: AXIS_COLOR, fontSize: 10, formatter: 'avg' },
          data: [{ type: 'average' }],
        };
      }
      return line;
    }
    if (seriesType === 'bar') {
      return {
        ...shared,
        type: 'bar',
        data: spec.values,
        barMaxWidth: ctx.compact ? 18 : 32,
        itemStyle: { color },
      };
    }
    const scatterData = options.bubble
      ? spec.values.map((value, pointIndex) => {
          const size = sizeValues[pointIndex] ?? 0;
          return {
            value,
            symbolSize: 6 + 28 * Math.sqrt(Math.max(0, size) / (maxSize || 1)),
          };
        })
      : spec.values;
    return {
      ...shared,
      type: 'scatter',
      data: scatterData,
      symbolSize: options.bubble ? undefined : 7,
      itemStyle: { color, opacity: 0.85 },
    };
  };

  const series = data.map(buildSeries);
  const hasZoom = resolved.xValues.length > 60;

  // Annotation overlays only make sense on a real time axis.
  const marks = resolved.isTime
    ? buildAnnotationMarks(resolved.xValues, ctx.annotations ?? [])
    : { areas: [], points: [] };
  if (series.length && (marks.areas.length || marks.points.length)) {
    applyAnnotationMarks(series[0], marks);
  }

  return {
    color: CHART_COLORS,
    animationDuration: 320,
    grid: gridOption(ctx.compact, { bottom: hasZoom ? (ctx.compact ? 32 : 50) : 30 }),
    tooltip: {
      trigger: richTooltip ? 'axis' : 'item',
      axisPointer: { type: options.type === 'bar' ? 'shadow' : 'line' },
      valueFormatter: (value: unknown) =>
        typeof value === 'number' ? formatter(value) : String(value ?? ''),
      ...TOOLTIP_STYLE,
    },
    legend:
      showLegend && series.length > 1
        ? {
            type: 'scroll',
            top: 0,
            right: 0,
            itemWidth: 10,
            itemHeight: 8,
            textStyle: { fontSize: FONT_SIZE, color: TEXT_MUTED },
          }
        : undefined,
    xAxis: categoryAxis({
      data: resolved.xValues.map(formatX),
      boundaryGap: options.type === 'bar' || options.mixed,
      formatter: value => value,
    }),
    yAxis: valueAxis({
      log: logAxis,
      formatter,
      max: normalize ? 100 : undefined,
      minorTick: minorTicks,
    }),
    dataZoom: hasZoom
      ? [
          { type: 'inside', throttle: 60 },
          { type: 'slider', height: 14, bottom: 2, show: !ctx.compact, brushSelect: false },
        ]
      : undefined,
    series,
  };
}

/**
 * Draws the annotation marks on one series. markLine may already carry the
 * average line, so the point events are appended instead of replacing it.
 */
function applyAnnotationMarks(series: SeriesOption, marks: AnnotationMark) {
  const target = series as SeriesOption & {
    markArea?: Record<string, unknown>;
    markLine?: Record<string, unknown>;
  };

  if (marks.areas.length) {
    target.markArea = {
      silent: true,
      itemStyle: { borderWidth: 0 },
      label: {
        show: true,
        position: 'insideTop',
        fontSize: FONT_SIZE - 1,
        color: TEXT_MUTED,
        // Keep the axis readable: a long description would cover the curve.
        formatter: (params: { name?: string }) => truncateLabel(params.name),
      },
      data: marks.areas.map(area => [
        // Category indexes, not labels: formatted labels may collide and ECharts
        // would then collapse the band to zero width.
        { xAxis: area.fromIndex, name: area.name, itemStyle: { color: area.tone.fill } },
        { xAxis: area.toIndex },
      ]),
    };
  }

  if (marks.points.length) {
    const existing = target.markLine ?? {};
    const existingData = Array.isArray(existing.data) ? existing.data : [];
    target.markLine = {
      symbol: 'none',
      silent: true,
      ...existing,
      data: [
        ...existingData,
        ...marks.points.map(point => ({
          xAxis: point.index,
          name: point.name,
          lineStyle: { color: point.tone.stroke, type: 'dashed', width: 1.5 },
          label: {
            show: true,
            // 'end' keeps the caption horizontal; the inside* variants get
            // rotated 90° by ECharts when the line sits near an edge.
            position: 'end',
            fontSize: FONT_SIZE - 1,
            color: TEXT_MUTED,
            formatter: () => truncateLabel(point.name),
          },
        })),
      ],
    };
  }
}

/** Annotation captions are labels, not text blocks: 12 chars then ellipsis. */
function truncateLabel(name?: string): string {
  const text = (name ?? '').trim();
  return text.length > 12 ? `${text.slice(0, 11)}…` : text;
}

function renderCartesian(ctx: VizContext, options: CartesianOptions) {
  const option = buildCartesian(ctx, options);
  if (!option) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.no_series')} />;
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
}

export const TimeseriesLine: VizRenderer = ({ ctx }) => renderCartesian(ctx, { type: 'line' });

export const TimeseriesSmooth: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'line', smooth: true });

export const TimeseriesStep: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'line', step: true });

export const TimeseriesArea: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'line', area: true });

export const TimeseriesBar: VizRenderer = ({ ctx }) => renderCartesian(ctx, { type: 'bar' });

export const TimeseriesScatter: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'scatter' });

export const BubbleRenderer: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'scatter', bubble: true });

export const AnnotatedTimeseries: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'line', area: true, annotate: true });

export const MixedTimeseries: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'bar', mixed: true });

export const CompareRenderer: VizRenderer = ({ ctx }) =>
  renderCartesian(ctx, { type: 'bar', mixed: true });
