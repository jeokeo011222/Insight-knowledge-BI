import { Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import {
  AXIS_COLOR,
  FONT_SIZE,
  MARK_BORDER_COLOR,
  PRIMARY,
  TOOLTIP_STYLE,
  asText,
  categoryAxis,
  formatCompact,
  formatNumber,
  gridOption,
  makeNumberFormatter,
  readList,
  readNumber,
  readString,
  toNumber,
  valueAxis,
  type Row,
  type VizContext,
  type VizRenderer,
} from '../utils';
import {
  boxStats,
  extent,
  histogram,
  metricColumn,
  numericColumns,
  pivotResult,
  textColumns,
} from '../dataUtils';
import { AXIS_LINE_COLOR, CHART_COLORS, HEAT_LOW_COLOR, HEAT_MID_COLOR, TEXT_STRONG } from '../utils';

export function TableRenderer({ ctx }: { ctx: VizContext }) {
  const columns: ColumnsType<Row> = ctx.colnames.map(column => ({
    title: column,
    dataIndex: column,
    key: column,
    ellipsis: true,
    render: (value: unknown) =>
      value === null || value === undefined ? (
        <span className="cell-muted">NULL</span>
      ) : (
        String(value)
      ),
  }));
  return (
    <Table<Row>
      size="small"
      rowKey={(_, index) => String(index)}
      columns={columns}
      dataSource={ctx.rows}
      pagination={ctx.compact ? false : { pageSize: 10, size: 'small', showSizeChanger: false }}
      scroll={{ x: 'max-content' }}
    />
  );
}

export const AgGridRenderer: VizRenderer = ({ ctx }) => <TableRenderer ctx={ctx} />;

function numericFormat(ctx: VizContext): string | null {
  return readString(ctx.formData, ['y_axis_format', 'number_format']);
}

/* ----------------------------------------------------------------- pivot */

function rowSpans(rowOrder: string[], cellDims: Map<string, string[]>, width: number) {
  const spans = new Map<string, number>();
  for (let dim = 0; dim < width; dim += 1) {
    let start = 0;
    while (start < rowOrder.length) {
      const prefix = (cellDims.get(rowOrder[start]) ?? []).slice(0, dim + 1).join('\u0001');
      let end = start + 1;
      while (
        end < rowOrder.length &&
        (cellDims.get(rowOrder[end]) ?? []).slice(0, dim + 1).join('\u0001') === prefix
      ) {
        end += 1;
      }
      spans.set(`${rowOrder[start]}\u0003${dim}`, end - start);
      for (let index = start + 1; index < end; index += 1) {
        spans.set(`${rowOrder[index]}\u0003${dim}`, 0);
      }
      start = end;
    }
  }
  return spans;
}

export const PivotRenderer: VizRenderer = ({ ctx }) => {
  const existing = (keys: string[]) => keys.filter(key => ctx.colnames.includes(key));
  const rowDims = existing(readList(ctx.formData, 'groupby'));
  if (!rowDims.length) {
    const fallback = textColumns(ctx)[0];
    if (fallback) rowDims.push(fallback);
  }
  const colDims = existing(readList(ctx.formData, 'columns')).filter(key => !rowDims.includes(key));
  const metrics = existing(readList(ctx.formData, 'metrics'));
  const metric = metrics[0] ?? metricColumn(ctx);
  if (!rowDims.length || !metric) return <TableRenderer ctx={ctx} />;

  const colKey = colDims[0] ?? null;
  const pivot = pivotResult(ctx.rows, rowDims, colKey, metric);
  const format = numericFormat(ctx);
  const spans = rowSpans(pivot.rowOrder, pivot.rowValues, rowDims.length);

  const records = pivot.rowOrder.map(rowKey => {
    const record: Record<string, unknown> = { __key: rowKey };
    rowDims.forEach((dim, index) => {
      record[dim] = pivot.rowValues.get(rowKey)?.[index] ?? '';
    });
    if (colKey) {
      pivot.colOrder.forEach(col => {
        record[`${colKey}\u0002${col}`] = pivot.cell.get(`${rowKey}\u0002${col}`) ?? null;
      });
    } else {
      record[metric] = pivot.cell.get(rowKey) ?? null;
    }
    return record;
  });

  const dimensionColumns: ColumnsType<Record<string, unknown>> = rowDims.map((dim, index) => ({
    title: dim,
    dataIndex: dim,
    key: dim,
    width: 150,
    onCell: record => ({
      rowSpan: spans.get(`${String(record.__key)}\u0003${index}`) ?? 1,
    }),
  }));

  const valueColumns: ColumnsType<Record<string, unknown>> = colKey
    ? pivot.colOrder.map(col => ({
        title: col,
        key: `${colKey}-${col}`,
        children: [
          {
            title: metric,
            dataIndex: `${colKey}\u0002${col}`,
            key: `${colKey}-${col}-${metric}`,
            align: 'right' as const,
            width: 110,
            render: (value: unknown) => formatNumber(toNumber(value), format),
          },
        ],
      }))
    : [
        {
          title: metric,
          dataIndex: metric,
          key: metric,
          align: 'right' as const,
          width: 140,
          render: (value: unknown) => formatNumber(toNumber(value), format),
        },
      ];

  return (
    <Table<Record<string, unknown>>
      size="small"
      bordered
      rowKey="__key"
      columns={[...dimensionColumns, ...valueColumns]}
      dataSource={records}
      pagination={false}
      scroll={{ x: 'max-content', y: ctx.compact ? undefined : Math.max(160, ctx.height - 60) }}
    />
  );
};

/* ---------------------------------------------------------------- heatmap */

export const HeatmapRenderer: VizRenderer = ({ ctx }) => {
  const dims = (readList(ctx.formData, 'groupby').filter(key => ctx.colnames.includes(key))
    .length
    ? readList(ctx.formData, 'groupby').filter(key => ctx.colnames.includes(key))
    : textColumns(ctx)
  ).slice(0, 2);
  const metric = metricColumn(ctx);
  if (dims.length < 2 || !metric) {
    return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.heatmap_need_dims')} />;
  }
  const [xKey, yKey] = dims;
  const xValues: string[] = [];
  const yValues: string[] = [];
  const xIndex = new Map<string, number>();
  const yIndex = new Map<string, number>();
  const points: Array<[number, number, number]> = [];
  ctx.rows.forEach(row => {
    const x = asText(row[xKey]) || '(null)';
    const y = asText(row[yKey]) || '(null)';
    const value = toNumber(row[metric]);
    if (value === null) return;
    if (!xIndex.has(x)) {
      xIndex.set(x, xValues.length);
      xValues.push(x);
    }
    if (!yIndex.has(y)) {
      yIndex.set(y, yValues.length);
      yValues.push(y);
    }
    points.push([xIndex.get(x) as number, yIndex.get(y) as number, Number(value.toFixed(3))]);
  });
  if (!points.length) return <ChartEmpty height={ctx.height} />;

  const values = points.map(point => point[2]);
  const range = extent(values);
  const format = makeNumberFormatter(numericFormat(ctx));
  const option: EChartsOption = {
    animation: false,
    tooltip: {
      position: 'top',
      formatter: (params: unknown) => {
        const item = params as { value?: [number, number, number] };
        const [x, y, value] = item.value ?? [0, 0, 0];
        return `${xValues[x]} · ${yValues[y]}<br/>${format(value)}`;
      },
      ...TOOLTIP_STYLE,
    },
    grid: gridOption(ctx.compact, {
      bottom: 44,
      left: 8,
      right: 8,
      top: 12,
      containLabel: true,
    }),
    xAxis: categoryAxis({ data: xValues, boundaryGap: true }),
    yAxis: {
      ...valueAxis(),
      type: 'category',
      data: yValues,
      splitLine: { show: false },
      axisLine: { lineStyle: { color: AXIS_LINE_COLOR } },
      axisLabel: { color: AXIS_COLOR, fontSize: FONT_SIZE },
    },
    visualMap: {
      min: range.min,
      max: range.max === range.min ? range.min + 1 : range.max,
      calculable: !ctx.compact,
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      itemWidth: 10,
      itemHeight: 60,
      textStyle: { fontSize: 10, color: AXIS_COLOR },
      inRange: { color: [HEAT_LOW_COLOR, HEAT_MID_COLOR, CHART_COLORS[0], CHART_COLORS[1]] },
    },
    series: [
      {
        type: 'heatmap',
        data: points,
        label: { show: points.length <= 40 && !ctx.compact, fontSize: 10, color: TEXT_STRONG },
        itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1 },
        emphasis: { itemStyle: { shadowBlur: 6, shadowColor: 'rgba(0,0,0,0.2)' } },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* --------------------------------------------------------------- calendar */

/* -------------------------------------------------------------- histogram */

export const HistogramRenderer: VizRenderer = ({ ctx }) => {
  const key =
    readString(ctx.formData, ['all_columns_x', 'column', 'x']) ??
    numericColumns(ctx)[0] ??
    null;
  if (!key || !ctx.colnames.includes(key)) {
    return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.histogram_need_numeric')} />;
  }
  const values = ctx.rows
    .map(row => toNumber(row[key]))
    .filter((value): value is number => value !== null);
  if (!values.length) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.no_numeric_values')} />;

  const bins = readNumber(ctx.formData, ['bins', 'bin_count']) ?? 12;
  const { labels, counts } = histogram(values, Math.max(2, Math.min(40, bins)));
  const option: EChartsOption = {
    color: [PRIMARY],
    grid: gridOption(ctx.compact),
    tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, ...TOOLTIP_STYLE },
    xAxis: categoryAxis({ data: labels }),
    yAxis: valueAxis({ formatter: value => formatCompact(value) }),
    series: [
      {
        type: 'bar',
        name: key,
        data: counts,
        barMaxWidth: 40,
        itemStyle: { color: PRIMARY, opacity: 0.85 },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

/* ---------------------------------------------------------------- boxplot */

function renderBoxplot(ctx: VizContext, note: boolean) {
  const valueKey = numericColumns(ctx)[0] ?? null;
  if (!valueKey) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.box_need_numeric')} />;
  const dim = readList(ctx.formData, 'groupby')[0] ?? textColumns(ctx)[0] ?? null;
  const groups = new Map<string, number[]>();
  ctx.rows.forEach(row => {
    const value = toNumber(row[valueKey]);
    if (value === null) return;
    const name = dim && ctx.colnames.includes(dim) ? asText(row[dim]) || '(null)' : valueKey;
    const bucket = groups.get(name);
    if (bucket) bucket.push(value);
    else groups.set(name, [value]);
  });
  if (!groups.size) return <ChartEmpty height={ctx.height} />;

  const names = Array.from(groups.keys()).slice(0, 24);
  const stats = names.map(name => boxStats(groups.get(name) ?? []));
  const format = makeNumberFormatter(numericFormat(ctx));
  const option: EChartsOption = {
    color: [PRIMARY],
    grid: gridOption(ctx.compact, { bottom: note ? 34 : 30, top: note ? 24 : 26 }),
    tooltip: {
      trigger: 'item',
      formatter: (params: unknown) => {
        const item = params as { name?: string; value?: number[] };
        const [min, q1, median, q3, max] = item.value ?? [0, 0, 0, 0, 0];
        return [
          `<b>${item.name ?? ''}</b>`,
          `max ${format(max)}`,
          `q3 ${format(q3)}`,
          `median ${format(median)}`,
          `q1 ${format(q1)}`,
          `min ${format(min)}`,
        ].join('<br/>');
      },
      ...TOOLTIP_STYLE,
    },
    graphic: note
      ? [
          {
            type: 'text',
            left: 8,
            top: 2,
            style: {
              text: 'Violin approximation (box plot + quartiles)',
              fontSize: 10,
              fill: AXIS_COLOR,
            },
          },
        ]
      : undefined,
    xAxis: categoryAxis({
      data: names,
      boundaryGap: true,
    }),
    yAxis: valueAxis({ formatter: value => formatCompact(value) }),
    series: [
      {
        type: 'boxplot',
        data: stats.map(item => [item.min, item.q1, item.median, item.q3, item.max]),
        boxWidth: ['25%', '55%'],
        itemStyle: { color: 'rgba(31,168,201,0.18)', borderColor: PRIMARY, borderWidth: 1.4 },
        emphasis: { itemStyle: { borderWidth: 2 } },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
}

export const BoxPlotRenderer: VizRenderer = ({ ctx }) => renderBoxplot(ctx, false);

export const ViolinRenderer: VizRenderer = ({ ctx }) => renderBoxplot(ctx, true);
