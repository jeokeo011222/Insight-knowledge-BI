import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import {
  AXIS_COLOR,
  CHART_COLORS,
  FONT_SIZE,
  TOOLTIP_STYLE,
  asText,
  makeNumberFormatter,
  readString,
  toNumber,
} from '../utils';
import {
  dimensionColumn,
  extent,
  numericColumns,
  takeRows,
} from '../dataUtils';
import type { VizRenderer } from '../utils';
import { SPLIT_COLOR, TEXT_MUTED } from '../utils';

/** Radar across numeric columns: one series per row, axis per metric. */
export const RadarRenderer: VizRenderer = ({ ctx }) => {
  const metrics = numericColumns(ctx).slice(0, 8);
  if (metrics.length < 2) {
    return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.radar_need_numeric')} />;
  }
  const dim = dimensionColumn(ctx);
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const rows = takeRows(ctx.rows, 8);
  const maxima = metrics.map(metric => {
    const range = extent(ctx.rows.map(row => toNumber(row[metric]) ?? 0));
    return Math.max(range.max, 1);
  });

  const option: EChartsOption = {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
    legend: {
      type: 'scroll',
      bottom: 0,
      left: 'center',
      itemWidth: 10,
      itemHeight: 8,
      textStyle: { fontSize: FONT_SIZE, color: TEXT_MUTED },
    },
    radar: {
      center: ['50%', ctx.compact ? '48%' : '44%'],
      radius: '62%',
      indicator: metrics.map((metric, index) => ({
        name: metric,
        max: Math.ceil(maxima[index] * 1.1),
      })),
      axisName: { color: AXIS_COLOR, fontSize: 10 },
      splitLine: { lineStyle: { color: SPLIT_COLOR } },
      axisLine: { lineStyle: { color: SPLIT_COLOR } },
      splitArea: { show: false },
    },
    series: [
      {
        type: 'radar',
        symbolSize: 4,
        lineStyle: { width: 1.5 },
        areaStyle: { opacity: 0.12 },
        data: rows.map((row, index) => ({
          name: (dim ? asText(row[dim]) : '') || `#${index + 1}`,
          value: metrics.map(metric => toNumber(row[metric]) ?? 0),
          tooltip: { valueFormatter: (value: unknown) => format(Number(value)) },
        })),
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};
