/**
 * Advanced / less common visualisations ported from the upstream Superset
 * plugin set: candlestick, gantt, horizon, parallel coordinates, butterfly and
 * tree. Each renderer degrades to an empty state when the query result lacks
 * the columns it needs.
 */
import type { EChartsOption, SeriesOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import {
  AXIS_COLOR,
  AXIS_LINE_COLOR,
  CHART_COLORS,
  FONT_SIZE,
  NEGATIVE_COLOR,
  POSITIVE_COLOR,
  PRIMARY,
  SPLIT_COLOR,
  TOOLTIP_STYLE,
  asText,
  categoryAxis,
  colorAt,
  gridOption,
  horizontalValueAxis,
  makeNumberFormatter,
  readString,
  toNumber,
  valueAxis,
  verticalCategoryAxis,
  type Row,
  type VizContext,
  type VizRenderer,
} from '../utils';
import {
  countDistinct,
  dimensionColumn,
  isTimeColumn,
  metricColumn,
  numericColumns,
  resolveSeries,
} from '../dataUtils';

/** Finds a column whose name matches one of the given needles. */
function pickColumn(colnames: string[], needles: string[]): string | null {
  const lower = colnames.map(name => name.toLowerCase());
  for (const needle of needles) {
    const exact = lower.indexOf(needle);
    if (exact >= 0) return colnames[exact];
  }
  for (const needle of needles) {
    const index = lower.findIndex(name => name.includes(needle));
    if (index >= 0) return colnames[index];
  }
  return null;
}

/* ------------------------------------------------------------- candlestick */

export const CandlestickRenderer: VizRenderer = ({ ctx }) => {
  if (!ctx.rows.length || !ctx.colnames.length) return <ChartEmpty height={ctx.height} />;
  const resolved = resolveSeries(ctx);
  if (!resolved) return <ChartEmpty height={ctx.height} />;

  const xKey = resolved.xKey;
  const openKey = pickColumn(ctx.colnames, ['open']);
  const highKey = pickColumn(ctx.colnames, ['high']);
  const lowKey = pickColumn(ctx.colnames, ['low']);
  const closeKey = pickColumn(ctx.colnames, ['close']);
  const hasOhlc = Boolean(openKey && highKey && lowKey && closeKey);
  const metric = resolved.metricKeys[0];
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));

  const labels: string[] = [];
  const candles: Array<[number, number, number, number]> = [];
  ctx.rows.forEach((row, index) => {
    if (hasOhlc) {
      const open = toNumber(row[openKey as string]);
      const high = toNumber(row[highKey as string]);
      const low = toNumber(row[lowKey as string]);
      const close = toNumber(row[closeKey as string]);
      if (open === null || high === null || low === null || close === null) return;
      labels.push(asText(row[xKey]) || String(index + 1));
      candles.push([open, close, low, high]);
      return;
    }
    // No OHLC columns: synthesise a candle from the leading metric, using the
    // previous row's close as the open so the series still reads as a trend.
    const value = metric ? toNumber(row[metric]) : null;
    if (value === null) return;
    const previous = candles.length ? candles[candles.length - 1][1] : value;
    labels.push(asText(row[xKey]) || String(index + 1));
    candles.push([previous, value, Math.min(previous, value), Math.max(previous, value)]);
  });
  if (!candles.length) return <ChartEmpty height={ctx.height} />;

  const option: EChartsOption = {
    animationDuration: 260,
    grid: gridOption(ctx.compact, { bottom: 34 }),
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      valueFormatter: value => (typeof value === 'number' ? format(value) : String(value ?? '')),
      ...TOOLTIP_STYLE,
    },
    xAxis: categoryAxis({ data: labels, boundaryGap: true }),
    yAxis: valueAxis({ formatter: format }),
    series: [
      {
        type: 'candlestick',
        name: hasOhlc ? 'OHLC' : (metric ?? 'value'),
        data: candles,
        itemStyle: {
          color: POSITIVE_COLOR,
          color0: NEGATIVE_COLOR,
          borderColor: POSITIVE_COLOR,
          borderColor0: NEGATIVE_COLOR,
        },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* ------------------------------------------------------------------ gantt */

export const GanttRenderer: VizRenderer = ({ ctx }) => {
  if (!ctx.rows.length || !ctx.colnames.length) return <ChartEmpty height={ctx.height} />;
  const taskKey = dimensionColumn(ctx);
  if (!taskKey) return <ChartEmpty height={ctx.height} />;

  const startKey = pickColumn(ctx.colnames, ['start_date', 'start_time', 'start', 'begin']);
  const endKey = pickColumn(ctx.colnames, ['end_date', 'end_time', 'end', 'finish']);
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));

  const tasks: string[] = [];
  const starts: number[] = [];
  const durations: number[] = [];
  ctx.rows.forEach((row, index) => {
    const name = asText(row[taskKey]) || `#${index + 1}`;
    let start = 0;
    let duration = 0;
    if (startKey && endKey) {
      const from = toNumber(row[startKey]);
      const to = toNumber(row[endKey]);
      if (from === null || to === null) return;
      start = from;
      duration = Math.max(0, to - from);
    } else {
      const metric = metricColumn(ctx);
      const value = metric ? toNumber(row[metric]) : null;
      if (value === null) return;
      duration = Math.abs(value);
    }
    tasks.push(name);
    starts.push(start);
    durations.push(duration);
  });
  if (!tasks.length) return <ChartEmpty height={ctx.height} />;

  // A transparent offset series carries the start; a coloured series carries
  // the duration. Both stack on the same category band, so the visible bar
  // spans exactly [start, start + duration].
  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { left: 8, right: 24, top: 22, bottom: 24 }),
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, ...TOOLTIP_STYLE },
    xAxis: horizontalValueAxis({ formatter: format }),
    yAxis: verticalCategoryAxis(tasks),
    series: [
      {
        type: 'bar',
        name: 'offset',
        stack: 'gantt',
        silent: true,
        data: starts,
        itemStyle: { color: 'transparent' },
        emphasis: { disabled: true },
      },
      {
        type: 'bar',
        name: 'duration',
        stack: 'gantt',
        data: durations,
        barMaxWidth: ctx.compact ? 12 : 18,
        itemStyle: { color: PRIMARY },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* ---------------------------------------------------------------- horizon */

export const HorizonRenderer: VizRenderer = ({ ctx }) => {
  const resolved = resolveSeries(ctx);
  if (!resolved || !resolved.series.length) return <ChartEmpty height={ctx.height} />;

  const magnitudes = resolved.series
    .flatMap(spec => spec.values)
    .filter((value): value is number => value !== null)
    .map(Math.abs);
  if (!magnitudes.length) return <ChartEmpty height={ctx.height} />;

  const max = Math.max(...magnitudes, 1);
  const bands = 2;
  const threshold = max / bands;
  const series: SeriesOption[] = [];

  resolved.series.forEach(spec => {
    for (let band = 0; band < bands; band++) {
      const positive = spec.values.map(value => {
        const current = value ?? 0;
        return Math.max(0, Math.min(current - band * threshold, threshold));
      });
      const negative = spec.values.map(value => {
        const current = value ?? 0;
        return -Math.max(0, Math.min(-current - band * threshold, threshold));
      });
      if (positive.some(value => value > 0)) {
        series.push({
          type: 'line',
          name: `${spec.name} +${band + 1}`,
          stack: 'positive',
          data: positive,
          showSymbol: false,
          lineStyle: { width: 0 },
          areaStyle: { color: colorAt(2), opacity: 0.22 + band * 0.42 },
        });
      }
      if (negative.some(value => value < 0)) {
        series.push({
          type: 'line',
          name: `${spec.name} -${band + 1}`,
          stack: 'negative',
          data: negative,
          showSymbol: false,
          lineStyle: { width: 0 },
          areaStyle: { color: colorAt(1), opacity: 0.22 + band * 0.42 },
        });
      }
    }
  });
  if (!series.length) return <ChartEmpty height={ctx.height} />;

  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { bottom: 26 }),
    tooltip: { trigger: 'axis', ...TOOLTIP_STYLE },
    xAxis: categoryAxis({ data: resolved.xValues, boundaryGap: false }),
    yAxis: {
      ...valueAxis({ max, min: -max }),
      splitLine: { show: false },
    },
    series,
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* ---------------------------------------------------- parallel coordinates */

export const ParallelRenderer: VizRenderer = ({ ctx }) => {
  if (!ctx.rows.length || ctx.colnames.length < 2) return <ChartEmpty height={ctx.height} />;

  const numeric = numericColumns(ctx);
  const categorical = ctx.colnames.filter(
    key => !numeric.includes(key) && countDistinct(ctx.rows, key) <= 12,
  );
  const dims = [...numeric, ...categorical].slice(0, 8);
  if (dims.length < 2) return <ChartEmpty height={ctx.height} />;

  const axes = dims.map((key, index) => {
    const isCategory = !numeric.includes(key);
    if (isCategory) {
      const categories: string[] = [];
      ctx.rows.forEach(row => {
        const value = asText(row[key]);
        if (value && !categories.includes(value)) categories.push(value);
      });
      return {
        dim: index,
        name: key,
        type: 'category',
        data: categories,
        nameLocation: 'start',
        nameTextStyle: { color: AXIS_COLOR, fontSize: FONT_SIZE },
        axisLabel: { color: AXIS_COLOR, fontSize: FONT_SIZE },
        axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
      };
    }
    return {
      dim: index,
      name: key,
      type: 'value',
      nameLocation: 'start',
      nameTextStyle: { color: AXIS_COLOR, fontSize: FONT_SIZE },
      axisLabel: { color: AXIS_COLOR, fontSize: FONT_SIZE },
      axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
      splitLine: { lineStyle: { color: SPLIT_COLOR } },
    };
  });

  const data = ctx.rows.map(row =>
    dims.map((key, index) => {
      if (numeric.includes(key)) return toNumber(row[key]);
      const categories = (axes[index] as { data?: string[] }).data;
      const value = asText(row[key]);
      return categories ? categories.indexOf(value) : 0;
    }),
  );

  const option = {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
    parallel: {
      left: '6%',
      right: '12%',
      top: 48,
      bottom: 24,
      parallelAxisDefault: {
        axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
        axisTick: { lineStyle: { color: AXIS_LINE_COLOR } },
        axisLabel: { color: AXIS_COLOR, fontSize: FONT_SIZE },
      },
    },
    parallelAxis: axes,
    series: [
      {
        type: 'parallel',
        smooth: true,
        lineStyle: { width: 1.4, opacity: 0.55 },
        data,
      },
    ],
  } as unknown as EChartsOption;
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* -------------------------------------------------------------- butterfly */

export const ButterflyRenderer: VizRenderer = ({ ctx }) => {
  if (!ctx.rows.length || !ctx.colnames.length) return <ChartEmpty height={ctx.height} />;
  const dimKey = dimensionColumn(ctx);
  if (!dimKey) return <ChartEmpty height={ctx.height} />;

  const numeric = numericColumns(ctx).filter(key => key !== dimKey);
  if (!numeric.length) return <ChartEmpty height={ctx.height} />;
  const rightKey = numeric[0];
  const leftKey = numeric[1] ?? null;
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));

  const names = ctx.rows.map((row, index) => asText(row[dimKey]) || `#${index + 1}`);
  const right = ctx.rows.map(row => toNumber(row[rightKey]) ?? 0);
  const left = leftKey ? ctx.rows.map(row => -Math.abs(toNumber(row[leftKey]) ?? 0)) : [];
  const maxAbs = Math.max(...[...right, ...left].map(Math.abs), 1);

  const series: SeriesOption[] = [];
  if (leftKey) {
    series.push({
      type: 'bar',
      name: leftKey,
      stack: 'butterfly',
      data: left,
      barMaxWidth: ctx.compact ? 12 : 18,
      itemStyle: { color: colorAt(1) },
    });
  }
  series.push({
    type: 'bar',
    name: rightKey,
    stack: 'butterfly',
    data: right,
    barMaxWidth: ctx.compact ? 12 : 18,
    itemStyle: { color: PRIMARY },
  });

  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { left: 8, right: 16, top: 24, bottom: 24 }),
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, ...TOOLTIP_STYLE },
    xAxis: horizontalValueAxis({ formatter: format, min: -maxAbs, max: maxAbs }),
    yAxis: verticalCategoryAxis(names),
    series,
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* ------------------------------------------------------------------- tree */

interface TreeNode {
  name: string;
  value?: number;
  children?: TreeNode[];
}

function groupTree(rows: Row[], dims: string[], metric: string | null, depth: number): TreeNode[] {
  if (depth >= dims.length) return [];
  const key = dims[depth];
  const buckets = new Map<string, Row[]>();
  rows.forEach(row => {
    const name = asText(row[key]) || '(null)';
    const bucket = buckets.get(name);
    if (bucket) bucket.push(row);
    else buckets.set(name, [row]);
  });
  return Array.from(buckets, ([name, group]) => {
    const children = depth + 1 < dims.length ? groupTree(group, dims, metric, depth + 1) : [];
    if (children.length) return { name, children };
    const value = metric
      ? group.reduce((sum, row) => sum + (toNumber(row[metric]) ?? 0), 0)
      : group.length;
    return { name, value };
  });
}

export const TreeRenderer: VizRenderer = ({ ctx }) => {
  if (!ctx.rows.length || !ctx.colnames.length) return <ChartEmpty height={ctx.height} />;
  const numeric = numericColumns(ctx);
  const dims = ctx.colnames.filter(key => !numeric.includes(key)).slice(0, 2);
  if (!dims.length) return <ChartEmpty height={ctx.height} />;
  const metric = metricColumn(ctx);
  const children = groupTree(ctx.rows, dims, metric && !dims.includes(metric) ? metric : null, 0);
  if (!children.length) return <ChartEmpty height={ctx.height} />;

  const option = {
    tooltip: { trigger: 'item', triggerOn: 'mousemove', ...TOOLTIP_STYLE },
    series: [
      {
        type: 'tree',
        data: [{ name: metric ?? 'All', children }],
        top: '4%',
        left: '8%',
        bottom: '4%',
        right: '20%',
        symbolSize: 8,
        orient: 'LR',
        expandAndCollapse: true,
        initialTreeDepth: 2,
        roam: true,
        label: {
          position: 'left',
          verticalAlign: 'middle',
          align: 'right',
          fontSize: FONT_SIZE,
          color: AXIS_COLOR,
        },
        leaves: {
          label: { position: 'right', verticalAlign: 'middle', align: 'left' },
        },
        lineStyle: { color: AXIS_LINE_COLOR, width: 1 },
        itemStyle: { color: PRIMARY },
      },
    ],
  } as unknown as EChartsOption;
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};
