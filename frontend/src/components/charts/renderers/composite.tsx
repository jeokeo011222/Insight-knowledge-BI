import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import {
  AXIS_COLOR,
  PRIMARY,
  TOOLTIP_STYLE,
  categoryAxis,
  formatCompact,
  gridOption,
  makeNumberFormatter,
  readList,
  readString,
  valueAxis,
  type VizRenderer,
} from '../utils';
import { dimensionTotals, metricColumn } from '../dataUtils';
import { useI18n } from '../../../i18n';
import { ACCENT_COLOR, NEGATIVE_COLOR, POSITIVE_COLOR, TEXT_MUTED } from '../utils';

/** Waterfall and Pareto: derived from a dimension plus one metric. */
export const WaterfallRenderer: VizRenderer = ({ ctx }) => {
  const { t } = useI18n();
  const metric = metricColumn(ctx);
  const dim =
    readList(ctx.formData, 'groupby').find(key => ctx.colnames.includes(key)) ??
    ctx.colnames.find(key => key !== metric);
  if (!metric || !dim) return <ChartEmpty height={ctx.height} message={t('chart.empty.waterfall_need_dim')} />;
  const data = dimensionTotals(ctx.rows, dim, metric);
  if (!data.length) return <ChartEmpty height={ctx.height} />;

  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const bases: number[] = [];
  let running = 0;
  data.forEach(item => {
    const start = running;
    running += item.value;
    bases.push(Math.min(start, running));
  });
  const bars = data.map(item => ({
    value: Math.abs(item.value),
    itemStyle: { color: item.value >= 0 ? POSITIVE_COLOR : NEGATIVE_COLOR },
  }));

  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { bottom: ctx.compact ? 24 : 34 }),
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (params: unknown) => {
        const list = params as Array<{ dataIndex: number }>;
        const index = list[0]?.dataIndex ?? 0;
        const item = data[index];
        return `${item.name}<br/>${format(item.value)}`;
      },
      ...TOOLTIP_STYLE,
    },
    xAxis: categoryAxis({ data: data.map(item => item.name) }),
    yAxis: valueAxis({ formatter: value => formatCompact(value) }),
    series: [
      {
        type: 'bar',
        name: 'base',
        stack: 'waterfall',
        silent: true,
        data: bases,
        itemStyle: { color: 'transparent' },
        emphasis: { itemStyle: { color: 'transparent' } },
        tooltip: { show: false },
      },
      {
        type: 'bar',
        name: metric,
        stack: 'waterfall',
        data: bars,
        barMaxWidth: 34,
        label: {
          show: true,
          position: 'top',
          fontSize: 10,
          color: TEXT_MUTED,
          formatter: (params: unknown) => {
            const index = (params as { dataIndex: number }).dataIndex;
            const item = data[index];
            return `${item.value >= 0 ? '+' : ''}${formatCompact(item.value)}`;
          },
        },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const ParetoRenderer: VizRenderer = ({ ctx }) => {
  const { t } = useI18n();
  const metric = metricColumn(ctx);
  const dim =
    readList(ctx.formData, 'groupby').find(key => ctx.colnames.includes(key)) ??
    ctx.colnames.find(key => key !== metric);
  if (!metric || !dim) return <ChartEmpty height={ctx.height} message={t('chart.empty.pareto_need_dim')} />;
  const data = dimensionTotals(ctx.rows, dim, metric)
    .sort((a, b) => b.value - a.value)
    .slice(0, 40);
  if (!data.length) return <ChartEmpty height={ctx.height} />;

  const total = data.reduce((sum, item) => sum + item.value, 0) || 1;
  let accumulated = 0;
  const cumulative = data.map(item => {
    accumulated += item.value;
    return Number(((accumulated / total) * 100).toFixed(2));
  });
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));

  const option: EChartsOption = {
    grid: gridOption(ctx.compact, { bottom: ctx.compact ? 30 : 44, right: 44 }),
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, ...TOOLTIP_STYLE },
    legend: {
      top: 0,
      right: 0,
      itemWidth: 10,
      itemHeight: 8,
      textStyle: { fontSize: 10, color: TEXT_MUTED },
    },
    xAxis: {
      ...categoryAxis({ data: data.map(item => item.name) }),
      axisLabel: {
        color: AXIS_COLOR,
        fontSize: 10,
        interval: 0,
        rotate: data.length > 8 ? 32 : 0,
        hideOverlap: true,
      },
    },
    yAxis: [
      valueAxis({ formatter: value => formatCompact(value) }),
      valueAxis({ formatter: value => `${value}%`, max: 100 }),
    ],
    series: [
      {
        type: 'bar',
        name: metric,
        data: data.map(item => item.value),
        barMaxWidth: 32,
        itemStyle: { color: PRIMARY },
        tooltip: { valueFormatter: (value: unknown) => format(Number(value)) },
      },
      {
        type: 'line',
        name: 'Cumulative %',
        yAxisIndex: 1,
        data: cumulative,
        smooth: 0.2,
        showSymbol: false,
        lineStyle: { color: ACCENT_COLOR, width: 2 },
        itemStyle: { color: ACCENT_COLOR },
        tooltip: { valueFormatter: (value: unknown) => `${Number(value).toFixed(2)}%` },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};
