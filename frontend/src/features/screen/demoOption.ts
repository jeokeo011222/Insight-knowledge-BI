/**
 * Demo-data ECharts options for wall components that are not yet bound to a
 * saved chart.
 *
 * A template has to look like a finished wall the moment it is created, so
 * every chart kind can render from `props.categories` / `props.series` without
 * touching the database. Binding a real chart simply bypasses this module.
 */
import type { EChartsOption } from 'echarts';
import { prop } from './model';

const WEEKDAYS = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
const MONTHS = ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'];

/** Province data used by template map components (no dataset required). */
const DEMO_PROVINCES: Array<[string, number]> = [
  ['广东', 982], ['江苏', 876], ['山东', 812], ['浙江', 768], ['河南', 642],
  ['四川', 586], ['湖北', 524], ['湖南', 496], ['河北', 462], ['福建', 438],
  ['安徽', 402], ['上海', 386], ['北京', 372], ['陕西', 318], ['江西', 296],
  ['辽宁', 268], ['重庆', 254], ['云南', 226], ['广西', 214], ['山西', 186],
  ['内蒙古', 164], ['贵州', 152], ['新疆', 128], ['吉林', 112], ['黑龙江', 108],
  ['甘肃', 86], ['海南', 64], ['宁夏', 42], ['青海', 32], ['西藏', 18],
  ['天津', 96], ['香港', 58], ['澳门', 16], ['台湾', 46],
];

export interface DemoStyle {
  text: string;
  muted: string;
  border: string;
  panel: string;
  color: string;
  palette: string[];
}

export function demoStyle(props: Record<string, unknown>): DemoStyle {
  const color = prop.str(props, 'color', '#37a2ff');
  const palette = prop.arr<string>(props, 'palette');
  return {
    text: prop.str(props, 'text', '#e9f4ff'),
    muted: prop.str(props, 'muted', '#7ea9cc'),
    border: prop.str(props, 'border', 'rgba(55,162,255,0.38)'),
    panel: prop.str(props, 'panel', 'rgba(13,53,99,0.42)'),
    color,
    palette: palette.length ? palette : [color, '#00e5ff', '#ffd166', '#ff7a7a', '#8b7bff'],
  };
}

function grid(style: DemoStyle): EChartsOption['grid'] {
  return { left: 12, right: 18, top: 34, bottom: 8, containLabel: true };
}

function axisCommon(style: DemoStyle) {
  return {
    axisLine: { show: true, lineStyle: { color: style.border } },
    axisTick: { show: false },
    axisLabel: { color: style.muted, fontSize: 11 },
    splitLine: { show: false, lineStyle: { color: style.border, opacity: 0.25 } },
  };
}

export function isDemoKind(kind: string): boolean {
  return [
    'bar', 'hbar', 'line', 'area', 'pie', 'ring', 'radar',
    'gauge', 'map', 'funnel', 'scatter', 'heatmap',
  ].includes(kind);
}

export function needsChinaMap(props: Record<string, unknown>): boolean {
  return prop.str(props, 'kind', 'bar') === 'map';
}

/** Builds a themed option for a demo chart. */
export function buildDemoOption(props: Record<string, unknown>): EChartsOption {
  const kind = prop.str(props, 'kind', 'bar');
  const style = demoStyle(props);
  const categories = prop.arr<string>(props, 'categories');
  const series = prop.arr<{ name?: string; data?: number[] }>(props, 'series');
  const values = series[0]?.data ?? [];
  const legend = prop.bool(props, 'legend', series.length > 1);
  const unit = prop.str(props, 'unit', '');

  const base: EChartsOption = {
    animationDuration: 600,
    backgroundColor: 'transparent',
    textStyle: { color: style.text, fontSize: 11 },
    color: style.palette,
  };

  const legendOption: EChartsOption['legend'] = legend
    ? {
        show: true,
        top: 2,
        right: 8,
        itemWidth: 8,
        itemHeight: 8,
        textStyle: { color: style.muted, fontSize: 11 },
      }
    : { show: false };

  const tooltip: EChartsOption['tooltip'] = {
    trigger: kind === 'pie' || kind === 'ring' || kind === 'funnel' ? 'item' : 'axis',
    backgroundColor: 'rgba(6,22,40,0.92)',
    borderColor: style.border,
    borderWidth: 1,
    textStyle: { color: style.text, fontSize: 12 },
    valueFormatter: unit
      ? (value => `${value as number}${unit}`)
      : undefined,
  };

  switch (kind) {
    case 'hbar':
      return {
        ...base,
        tooltip,
        legend: legendOption,
        grid: { left: 12, right: 24, top: legend ? 34 : 12, bottom: 8, containLabel: true },
        xAxis: { type: 'value', ...axisCommon(style), splitLine: { show: true, lineStyle: { color: style.border, opacity: 0.22 } } },
        yAxis: { type: 'category', inverse: true, data: categories.length ? categories : ['A', 'B', 'C', 'D', 'E'], ...axisCommon(style) },
        series: (series.length ? series : [{ name: '数值', data: values }]).map((s, i) => ({
          name: s.name ?? '数值',
          type: 'bar',
          barWidth: 12,
          data: s.data ?? [],
          itemStyle: {
            borderRadius: [0, 6, 6, 0],
            color: style.palette[i % style.palette.length],
          },
          label: {
            show: true,
            position: 'right',
            color: style.muted,
            fontSize: 11,
          },
        })),
      };

    case 'line':
    case 'area':
      return {
        ...base,
        tooltip,
        legend: legendOption,
        grid: grid(style),
        xAxis: { type: 'category', boundaryGap: false, data: categories.length ? categories : MONTHS, ...axisCommon(style) },
        yAxis: { type: 'value', ...axisCommon(style), splitLine: { show: true, lineStyle: { color: style.border, opacity: 0.22 } } },
        series: (series.length ? series : [{ name: '数值', data: values }]).map((s, i) => ({
          name: s.name ?? '数值',
          type: 'line',
          smooth: true,
          symbol: 'circle',
          symbolSize: 4,
          showSymbol: false,
          lineStyle: { width: 2 },
          areaStyle:
            kind === 'area'
              ? { opacity: 0.18, color: style.palette[i % style.palette.length] }
              : undefined,
          data: s.data ?? [],
        })),
      };

    case 'pie':
    case 'ring':
      return {
        ...base,
        tooltip,
        legend: {
          show: true,
          orient: 'vertical',
          right: 8,
          top: 'center',
          itemWidth: 8,
          itemHeight: 8,
          textStyle: { color: style.muted, fontSize: 11 },
        },
        series: [
          {
            type: 'pie',
            radius: kind === 'ring' ? ['46%', '68%'] : ['0%', '68%'],
            center: ['38%', '52%'],
            avoidLabelOverlap: true,
            itemStyle: { borderColor: 'rgba(3,11,23,0.85)', borderWidth: 2 },
            label: { color: style.text, fontSize: 11, formatter: '{b}\n{d}%' },
            labelLine: { lineStyle: { color: style.muted } },
            data: (categories.length ? categories : ['A', 'B', 'C', 'D']).map((name, i) => ({
              name,
              value: values[i] ?? 0,
            })),
          },
        ],
      };

    case 'radar': {
      const indicators = (categories.length ? categories : ['维度一', '维度二', '维度三', '维度四', '维度五']).map(
        name => ({ name, max: 120 }),
      );
      return {
        ...base,
        tooltip: { ...tooltip, trigger: 'item' },
        legend: legendOption,
        radar: {
          indicator: indicators,
          center: ['50%', '56%'],
          radius: '62%',
          axisName: { color: style.muted, fontSize: 11 },
          splitLine: { lineStyle: { color: style.border, opacity: 0.35 } },
          splitArea: { show: false },
          axisLine: { lineStyle: { color: style.border, opacity: 0.35 } },
        },
        series: [
          {
            type: 'radar',
            data: (series.length ? series : [{ name: '本月', data: [] }]).map((s, i) => ({
              name: s.name ?? `系列${i + 1}`,
              value: s.data ?? [],
              areaStyle: { opacity: 0.18 },
              lineStyle: { width: 2 },
              symbolSize: 4,
            })),
          },
        ],
      };
    }

    case 'gauge': {
      const value = Math.max(0, Math.min(100, values[0] ?? 68));
      return {
        ...base,
        series: [
          {
            type: 'gauge',
            startAngle: 210,
            endAngle: -30,
            min: 0,
            max: 100,
            radius: '92%',
            center: ['50%', '58%'],
            progress: { show: true, width: 10, itemStyle: { color: style.color } },
            axisLine: { lineStyle: { width: 10, color: [[1, 'rgba(255,255,255,0.10)']] } },
            axisTick: { show: false },
            splitLine: { show: false },
            axisLabel: { color: style.muted, fontSize: 10, distance: 14 },
            pointer: { width: 4, itemStyle: { color: style.color } },
            anchor: { show: true, size: 8, itemStyle: { color: style.color } },
            title: { show: false },
            detail: {
              valueAnimation: true,
              offsetCenter: [0, '42%'],
              formatter: `{value}${unit}`,
              color: style.text,
              fontSize: 26,
              fontWeight: 600,
            },
            data: [{ value }],
          },
        ],
      };
    }

    case 'funnel':
      return {
        ...base,
        tooltip,
        legend: {
          show: true,
          orient: 'vertical',
          right: 8,
          top: 'center',
          itemWidth: 8,
          itemHeight: 8,
          textStyle: { color: style.muted, fontSize: 11 },
        },
        series: [
          {
            type: 'funnel',
            left: '6%',
            right: '34%',
            top: 12,
            bottom: 12,
            minSize: '24%',
            gap: 2,
            label: { color: style.text, fontSize: 11, formatter: '{b} {c}' },
            labelLine: { lineStyle: { color: style.muted } },
            itemStyle: { borderColor: 'rgba(3,11,23,0.8)', borderWidth: 1 },
            data: (categories.length ? categories : ['A', 'B', 'C', 'D']).map((name, i) => ({
              name,
              value: values[i] ?? 0,
            })),
          },
        ],
      };

    case 'scatter':
      return {
        ...base,
        tooltip: { ...tooltip, trigger: 'item' },
        grid: grid(style),
        xAxis: { type: 'category', data: categories.length ? categories : ['A', 'B', 'C', 'D'], ...axisCommon(style) },
        yAxis: { type: 'value', ...axisCommon(style), splitLine: { show: true, lineStyle: { color: style.border, opacity: 0.22 } } },
        series: [
          {
            type: 'scatter',
            symbolSize: (value: unknown) => Math.max(10, Math.sqrt(Number(value) || 10) * 2.4),
            itemStyle: { color: style.color, opacity: 0.85 },
            data: values.map((value, i) => [categories[i] ?? `#${i + 1}`, value]),
          },
        ],
      };

    case 'heatmap': {
      const data: Array<[number, number, number]> = [];
      for (let d = 0; d < 7; d += 1) {
        for (let h = 0; h < 12; h += 1) {
          const v = Math.round(40 + Math.abs(Math.sin((d + 1) * (h + 2))) * 60);
          data.push([h, d, v]);
        }
      }
      return {
        ...base,
        tooltip: { ...tooltip, trigger: 'item' },
        grid: { left: 44, right: 16, top: 12, bottom: 24 },
        xAxis: { type: 'category', data: MONTHS, splitArea: { show: false }, ...axisCommon(style) },
        yAxis: { type: 'category', data: WEEKDAYS, splitArea: { show: false }, ...axisCommon(style) },
        visualMap: {
          min: 40,
          max: 100,
          calculable: false,
          orient: 'horizontal',
          left: 'center',
          bottom: 0,
          itemWidth: 10,
          itemHeight: 60,
          textStyle: { color: style.muted, fontSize: 10 },
          inRange: { color: ['rgba(55,162,255,0.12)', style.color] },
        },
        series: [
          {
            type: 'heatmap',
            data,
            itemStyle: { borderColor: 'rgba(3,11,23,0.6)', borderWidth: 1 },
          },
        ],
      };
    }

    case 'map':
      return {
        ...base,
        tooltip: {
          trigger: 'item',
          backgroundColor: 'rgba(6,22,40,0.92)',
          borderColor: style.border,
          textStyle: { color: style.text, fontSize: 12 },
          formatter: (params: unknown) => {
            const p = params as { name?: string; value?: number };
            return `${p.name ?? ''}<br/>${(p.value ?? 0).toLocaleString()}${unit}`;
          },
        },
        visualMap: {
          min: 0,
          max: 1000,
          left: 12,
          bottom: 12,
          itemWidth: 10,
          itemHeight: 80,
          text: ['高', '低'],
          textStyle: { color: style.muted, fontSize: 10 },
          inRange: { color: ['rgba(55,162,255,0.12)', style.color] },
          calculable: false,
        },
        series: [
          {
            type: 'map',
            map: 'china',
            roam: false,
            zoom: 1.16,
            top: 12,
            bottom: 12,
            label: { show: false },
            itemStyle: {
              areaColor: 'rgba(13,53,99,0.55)',
              borderColor: style.border,
              borderWidth: 0.8,
            },
            emphasis: {
              label: { show: true, color: style.text, fontSize: 11 },
              itemStyle: { areaColor: style.color, opacity: 0.85 },
            },
            data: DEMO_PROVINCES.map(([name, value]) => ({ name, value })),
          },
        ],
      };

    default:
      return {
        ...base,
        tooltip,
        grid: grid(style),
        xAxis: { type: 'category', data: categories.length ? categories : MONTHS, ...axisCommon(style) },
        yAxis: { type: 'value', ...axisCommon(style) },
        series: [
          {
            type: 'bar',
            barWidth: '46%',
            itemStyle: { color: style.color, borderRadius: [4, 4, 0, 0] },
            data: values,
          },
        ],
      };
  }
}
