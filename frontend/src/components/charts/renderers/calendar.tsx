import dayjs from 'dayjs';
import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import {
  AXIS_COLOR,
  MARK_BORDER_COLOR,
  TOOLTIP_STYLE,
  asText,
  makeNumberFormatter,
  readString,
  toNumber,
} from '../utils';
import { coltypeOf, extent, isTimeColumn, metricColumn } from '../dataUtils';
import type { VizRenderer } from '../utils';
import { useI18n } from '../../../i18n';
import { CHART_COLORS, HEAT_LOW_COLOR, HEAT_MID_COLOR, SPLIT_COLOR, TEXT_MUTED } from '../utils';

/** Calendar heatmap: needs a date column plus a numeric metric. */
export const CalendarRenderer: VizRenderer = ({ ctx }) => {
  const { t } = useI18n();
  const dateKey =
    readString(ctx.formData, ['granularity_sqla', 'time_column', 'x_axis']) ??
    ctx.colnames.find(key => isTimeColumn(ctx.rows, key, coltypeOf(ctx, key))) ??
    null;
  const metric = metricColumn(ctx);
  if (!dateKey || !metric) {
    return <ChartEmpty height={ctx.height} message={t('chart.empty.calendar_need_date')} />;
  }

  const dated = ctx.rows
    .map(row => ({ date: dayjs(asText(row[dateKey])), value: toNumber(row[metric]) }))
    .filter(item => item.date.isValid() && item.value !== null) as Array<{
    date: dayjs.Dayjs;
    value: number;
  }>;
  if (!dated.length) return <ChartEmpty height={ctx.height} message={t('chart.empty.calendar_no_date')} />;

  const years = Array.from(new Set(dated.map(item => item.date.year()))).sort((a, b) => a - b);
  const maxYears = Math.max(1, Math.min(3, Math.floor((ctx.height - 20) / 150)));
  const shown = years.slice(-maxYears);
  const cellHeight = Math.max(8, Math.min(14, Math.floor((ctx.height - 46) / (shown.length * 7))));
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const range = extent(dated.map(item => item.value));

  const option: EChartsOption = {
    tooltip: {
      formatter: (params: unknown) => {
        const item = params as { value?: [string, number] };
        const [date, value] = item.value ?? ['', 0];
        return `${date}<br/>${format(value)}`;
      },
      ...TOOLTIP_STYLE,
    },
    visualMap: {
      min: range.min,
      max: range.max === range.min ? range.min + 1 : range.max,
      calculable: false,
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      itemWidth: 10,
      itemHeight: 50,
      textStyle: { fontSize: 10, color: AXIS_COLOR },
      inRange: { color: [HEAT_LOW_COLOR, HEAT_MID_COLOR, CHART_COLORS[0], CHART_COLORS[1]] },
    },
    calendar: shown.map((year, index) => ({
      top: 34 + index * (cellHeight * 7 + 24),
      left: 44,
      right: 12,
      range: String(year),
      cellSize: ['auto', cellHeight],
      orient: 'horizontal',
      dayLabel: { fontSize: 9, color: AXIS_COLOR, firstDay: 1 },
      monthLabel: { fontSize: 9, color: AXIS_COLOR },
      yearLabel: { fontSize: 10, color: TEXT_MUTED, margin: 26 },
      itemStyle: { color: 'transparent', borderColor: SPLIT_COLOR },
      splitLine: { lineStyle: { color: SPLIT_COLOR } },
    })),
    series: shown.map((year, index) => ({
      type: 'heatmap',
      coordinateSystem: 'calendar',
      calendarIndex: index,
      data: dated
        .filter(item => item.date.year() === year)
        .map(item => [item.date.format('YYYY-MM-DD'), item.value]),
      itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1 },
    })),
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};
