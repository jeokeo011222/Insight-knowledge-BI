import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import {
  CHART_COLORS,
  FONT_SIZE,
  MARK_BORDER_COLOR,
  TOOLTIP_STYLE,
  asText,
  makeNumberFormatter,
  readList,
  readNumber,
  readString,
  stableColor,
  toNumber,
  type VizContext,
  type VizRenderer,
} from '../utils';
import {
  coltypeOf,
  dimensionTotals,
  isTimeColumn,
  metricColumn,
  textColumns,
} from '../dataUtils';
import { BORDER_COLOR, TEXT_MUTED, TEXT_STRONG } from '../utils';

function categoricalDims(ctx: VizContext): string[] {
  const declared = readList(ctx.formData, 'groupby').filter(key => ctx.colnames.includes(key));
  const fallback = textColumns(ctx).filter(
    key => !isTimeColumn(ctx.rows, key, coltypeOf(ctx, key)),
  );
  const dims = (declared.length ? declared : fallback).filter(key => key !== metricColumn(ctx));
  return dims.length ? dims : ctx.colnames.slice(0, 1);
}

function pieData(ctx: VizContext): Array<{ name: string; value: number }> | null {
  const dim = categoricalDims(ctx)[0];
  if (!dim) return null;
  const metric = metricColumn(ctx);
  const valueKey = metric && metric !== dim ? metric : null;
  const data = dimensionTotals(ctx.rows, dim, valueKey).sort((a, b) => b.value - a.value);
  return data.length ? data : null;
}

function pieOption(ctx: VizContext, inner: string, rose: boolean): EChartsOption | null {
  const data = pieData(ctx);
  if (!data) return null;
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  return {
    color: CHART_COLORS,
    tooltip: {
      trigger: 'item',
      formatter: (params: unknown) => {
        const item = params as { name?: string; value?: number; percent?: number };
        return `${item.name ?? ''}: ${format(item.value ?? 0)} (${item.percent ?? 0}%)`;
      },
      ...TOOLTIP_STYLE,
    },
    legend:
      !ctx.compact && data.length <= 12
        ? {
            type: 'scroll',
            bottom: 0,
            left: 'center',
            itemWidth: 10,
            itemHeight: 8,
            textStyle: { fontSize: FONT_SIZE, color: TEXT_MUTED },
          }
        : undefined,
    series: [
      {
        type: 'pie',
        radius: rose ? ['18%', '68%'] : [inner, '70%'],
        center: ['50%', ctx.compact ? '50%' : '44%'],
        roseType: rose ? 'radius' : undefined,
        avoidLabelOverlap: true,
        itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1 },
        // Slice colours key off the category name so the same value keeps the
        // same colour across every pie/bar on the dashboard.
        data: data.map(datum => ({ ...datum, itemStyle: { color: stableColor(datum.name) } })),
        label: ctx.compact
          ? { show: false }
          : { fontSize: FONT_SIZE, color: TEXT_MUTED, formatter: '{b}\n{d}%' },
        labelLine: { length: 8, length2: 6 },
        emphasis: { scale: true, scaleSize: 4 },
      },
    ],
  };
}

export const PieRenderer: VizRenderer = ({ ctx }) => {
  const inner = readNumber(ctx.formData, ['innerRadius', 'inner_radius']);
  const option = pieOption(ctx, inner ? `${Math.min(80, Math.max(0, inner))}%` : '0%', false);
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};

export const DonutRenderer: VizRenderer = ({ ctx }) => {
  const inner = readNumber(ctx.formData, ['innerRadius', 'inner_radius']) ?? 45;
  const option = pieOption(ctx, `${Math.min(80, Math.max(10, inner))}%`, false);
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};

export const RoseRenderer: VizRenderer = ({ ctx }) => {
  const option = pieOption(ctx, '18%', true);
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};

interface HierarchyNode {
  name: string;
  value?: number;
  children?: Array<{ name: string; value: number }>;
}

function hierarchyData(ctx: VizContext): HierarchyNode[] | null {
  const dims = categoricalDims(ctx);
  const first = dims[0];
  const second = dims[1] ?? null;
  const metric = metricColumn(ctx);
  if (!first) return null;

  const roots = new Map<string, Map<string, number>>();
  ctx.rows.forEach(row => {
    const parent = asText(row[first]) || '(null)';
    const child = second ? asText(row[second]) || '(null)' : '';
    const value =
      metric && metric !== first && metric !== second ? toNumber(row[metric]) : 1;
    if (value === null) return;
    let bucket = roots.get(parent);
    if (!bucket) {
      bucket = new Map<string, number>();
      roots.set(parent, bucket);
    }
    bucket.set(child, (bucket.get(child) ?? 0) + value);
  });
  if (!roots.size) return null;

  return Array.from(roots, ([name, children]) => {
    if (!second) {
      const total = Array.from(children.values()).reduce((sum, value) => sum + value, 0);
      return { name, value: total };
    }
    return {
      name,
      children: Array.from(children, ([childName, value]) => ({ name: childName, value })),
    };
  });
}

function hierarchyOption(ctx: VizContext, type: 'sunburst' | 'treemap'): EChartsOption | null {
  const data = hierarchyData(ctx);
  if (!data) return null;
  if (type === 'sunburst') {
    return {
      color: CHART_COLORS,
      tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
      series: [
        {
          type: 'sunburst',
          data,
          radius: ['8%', '92%'],
          center: ['50%', '50%'],
          sort: (a: unknown, b: unknown) =>
            ((b as { value?: number }).value ?? 0) - ((a as { value?: number }).value ?? 0),
          itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1 },
          label: { fontSize: FONT_SIZE, color: TEXT_STRONG, minAngle: 6 },
          emphasis: { focus: 'ancestor' },
        },
      ],
    };
  }
  return {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
    series: [
      {
        type: 'treemap',
        data,
        top: 4,
        left: 4,
        right: 4,
        bottom: 4,
        roam: false,
        nodeClick: false,
        breadcrumb: { show: false },
        itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1, gapWidth: 1 },
        upperLabel: { show: Boolean(categoricalDims(ctx)[1]), height: 18, fontSize: FONT_SIZE, color: '#ffffff' },
        label: { fontSize: FONT_SIZE, color: '#ffffff' },
        levels: [
          { itemStyle: { borderWidth: 2, gapWidth: 2 } },
          { itemStyle: { borderWidth: 1, gapWidth: 1 }, colorSaturation: [0.35, 0.55] },
        ],
      },
    ],
  };
}

export const SunburstRenderer: VizRenderer = ({ ctx }) => {
  const option = hierarchyOption(ctx, 'sunburst');
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};

export const TreemapRenderer: VizRenderer = ({ ctx }) => {
  const option = hierarchyOption(ctx, 'treemap');
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};

export const FunnelRenderer: VizRenderer = ({ ctx }) => {
  const data = pieData(ctx);
  if (!data) return <ChartEmpty height={ctx.height} />;
  const option: EChartsOption = {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', formatter: '{b}: {c}', ...TOOLTIP_STYLE },
    legend:
      !ctx.compact && data.length <= 10
        ? { bottom: 0, left: 'center', itemWidth: 10, itemHeight: 8, textStyle: { fontSize: FONT_SIZE } }
        : undefined,
    series: [
      {
        type: 'funnel',
        left: '12%',
        right: '12%',
        top: 12,
        bottom: ctx.compact ? 12 : 34,
        sort: 'descending',
        minSize: '12%',
        gap: 1,
        label: { show: !ctx.compact, fontSize: FONT_SIZE, color: TEXT_STRONG, position: 'inside' },
        itemStyle: { borderColor: MARK_BORDER_COLOR, borderWidth: 1 },
        data,
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

function linkPairs(ctx: VizContext): {
  links: Array<{ source: string; target: string; value: number }>;
  nodes: string[];
} | null {
  const dims = categoricalDims(ctx);
  const source = dims[0];
  const target = dims[1];
  const metric = metricColumn(ctx);
  if (!source || !target) return null;
  const totals = new Map<string, { source: string; target: string; value: number }>();
  const nodes: string[] = [];
  ctx.rows.forEach(row => {
    const from = asText(row[source]) || '(null)';
    const to = asText(row[target]) || '(null)';
    const value = metric && metric !== source && metric !== target ? toNumber(row[metric]) : 1;
    if (value === null) return;
    const key = `${from}\u0001${to}`;
    const existing = totals.get(key);
    if (existing) existing.value += value;
    else totals.set(key, { source: from, target: to, value });
    if (!nodes.includes(from)) nodes.push(from);
    if (!nodes.includes(to)) nodes.push(to);
  });
  if (!totals.size) return null;
  return { links: Array.from(totals.values()), nodes };
}

export const SankeyRenderer: VizRenderer = ({ ctx }) => {
  const pairs = linkPairs(ctx);
  if (!pairs) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.sankey_need_dims')} />;
  const option: EChartsOption = {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
    series: [
      {
        type: 'sankey',
        left: 8,
        right: 8,
        top: 8,
        bottom: 8,
        data: pairs.nodes.map(name => ({ name })),
        links: pairs.links,
        nodeGap: 10,
        nodeWidth: 12,
        emphasis: { focus: 'adjacency' },
        lineStyle: { color: 'gradient', curveness: 0.5, opacity: 0.35 },
        label: { fontSize: FONT_SIZE, color: TEXT_STRONG },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const GraphRenderer: VizRenderer = ({ ctx }) => {
  const pairs = linkPairs(ctx);
  if (!pairs) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.graph_need_dims')} />;
  const weight = new Map<string, number>();
  pairs.links.forEach(link => {
    weight.set(link.source, (weight.get(link.source) ?? 0) + link.value);
    weight.set(link.target, (weight.get(link.target) ?? 0) + link.value);
  });
  const maxWeight = Math.max(...Array.from(weight.values()), 1);
  const option: EChartsOption = {
    color: CHART_COLORS,
    tooltip: { trigger: 'item', ...TOOLTIP_STYLE },
    series: [
      {
        type: 'graph',
        layout: 'force',
        roam: true,
        draggable: true,
        top: 8,
        bottom: 8,
        force: { repulsion: 140, edgeLength: [40, 90], gravity: 0.08 },
        data: pairs.nodes.map(name => ({
          name,
          symbolSize: 10 + 22 * Math.sqrt((weight.get(name) ?? 0) / maxWeight),
          // Stable per-node colour so a shared category keeps its identity.
          itemStyle: { color: stableColor(name) },
        })),
        links: pairs.links.map(link => ({ source: link.source, target: link.target })),
        label: { show: true, fontSize: 10, color: TEXT_STRONG },
        lineStyle: { color: BORDER_COLOR, width: 1, curveness: 0.12 },
        emphasis: { focus: 'adjacency', lineStyle: { width: 2 } },
      },
    ],
  };
  return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
};

export const PartitionRenderer: VizRenderer = ({ ctx }) => {
  const option = hierarchyOption(ctx, 'treemap');
  return option ? (
    <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />
  ) : (
    <ChartEmpty height={ctx.height} />
  );
};
