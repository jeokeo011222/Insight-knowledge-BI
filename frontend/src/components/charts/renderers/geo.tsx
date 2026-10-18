import { useEffect, useState } from 'react';
import type { EChartsOption } from 'echarts';
import { EChart, ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import {
  AXIS_COLOR,
  MAP_AREA_COLOR,
  MAP_BORDER_COLOR,
  MAP_SCALE_COLORS,
  PRIMARY,
  SPLIT_COLOR,
  TOOLTIP_STYLE,
  asText,
  gridOption,
  makeNumberFormatter,
  readList,
  readString,
  toNumber,
  type VizContext,
  type VizRenderer,
} from '../utils';
import {
  extent,
  findGeoColumns,
  findRegionColumn,
  metricColumn,
} from '../dataUtils';
import {
  CHINA_MAP_NAME,
  ensureChinaMap,
  isChinaMapRegistered,
  resolveChinaRegion,
} from '../chinaMap';

/** Viz types that should draw a real map rather than a coordinate chart. */
const MAP_VIZ_TYPES = /map|geojson/i;

/** Region fill/border; a function so it tracks the live (theme-aware) palette. */
function mapItemStyle() {
  return { areaColor: MAP_AREA_COLOR, borderColor: MAP_BORDER_COLOR, borderWidth: 0.6 };
}

/** Shared `geo` frame: the China basemap with roam + brand-coloured hover. */
function chinaGeo(compact: boolean) {
  return {
    map: CHINA_MAP_NAME,
    roam: true,
    zoom: compact ? 1.1 : 1.2,
    label: { show: false },
    itemStyle: mapItemStyle(),
    emphasis: { itemStyle: { areaColor: PRIMARY } },
  };
}

/** Choropleth: one value per province, coloured by a `visualMap` gradient. */
function choroplethOption(ctx: VizContext, regionCol: string): EChartsOption | null {
  const metric = metricColumn(ctx);
  const valueKey = metric && metric !== regionCol ? metric : null;
  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));

  const totals = new Map<string, number>();
  ctx.rows.forEach(row => {
    const region = resolveChinaRegion(asText(row[regionCol]));
    if (!region) return;
    const value = valueKey ? toNumber(row[valueKey]) : 1;
    if (value === null) return;
    totals.set(region, (totals.get(region) ?? 0) + value);
  });
  if (!totals.size) return null;

  const data = Array.from(totals, ([name, value]) => ({ name, value }));
  const { min, max } = extent(data.map(item => item.value));

  return {
    tooltip: {
      trigger: 'item',
      formatter: (params: unknown) => {
        const item = params as { name?: string; value?: number };
        const shown =
          typeof item.value === 'number' && Number.isFinite(item.value) ? format(item.value) : '—';
        return `<b>${item.name ?? ''}</b><br/>${metric ?? 'count'}: ${shown}`;
      },
      ...TOOLTIP_STYLE,
    },
    visualMap: {
      min,
      max,
      left: 10,
      bottom: 10,
      calculable: true,
      itemWidth: 12,
      itemHeight: 90,
      text: ['高', '低'],
      textStyle: { color: AXIS_COLOR, fontSize: 10 },
      inRange: { color: MAP_SCALE_COLORS },
    },
    series: [
      {
        type: 'map',
        map: CHINA_MAP_NAME,
        roam: true,
        zoom: ctx.compact ? 1.1 : 1.2,
        label: { show: false },
        itemStyle: mapItemStyle(),
        emphasis: { label: { show: true, fontSize: 9, color: '#ffffff' }, itemStyle: { areaColor: PRIMARY } },
        data,
      },
    ],
  };
}

interface GeoPoint {
  name: string;
  lng: number;
  lat: number;
  value: number | null;
}

/** Collects valid lat/lng pairs from the result set. */
function collectPoints(ctx: VizContext, lat: string, lng: string): GeoPoint[] {
  const dim =
    readList(ctx.formData, 'groupby').find(key => ctx.colnames.includes(key)) ?? ctx.colnames[0];
  const metric = metricColumn(ctx);
  const points: GeoPoint[] = [];
  ctx.rows.forEach(row => {
    const latValue = toNumber(row[lat]);
    const lngValue = toNumber(row[lng]);
    if (latValue === null || lngValue === null) return;
    if (Math.abs(latValue) > 90 || Math.abs(lngValue) > 180) return;
    points.push({
      name: dim ? asText(row[dim]) : '',
      lat: latValue,
      lng: lngValue,
      value: metric ? toNumber(row[metric]) : null,
    });
  });
  return points;
}

function pointTooltip(format: (value: number) => string, bubble: boolean) {
  return (params: unknown) => {
    const item = params as { data?: { name?: string; value?: [number, number, number] } };
    const data = item.data;
    if (!data?.value) return '';
    const [lngValue, latValue, metricValue] = data.value;
    const lines = [`<b>${data.name || `${lngValue}, ${latValue}`}</b>`];
    lines.push(`lon ${lngValue.toFixed(4)} · lat ${latValue.toFixed(4)}`);
    if (bubble) lines.push(format(metricValue));
    return lines.join('<br/>');
  };
}

/** Points plotted on the China basemap (deck.gl Scatterplot等). */
function chinaPointOption(
  ctx: VizContext,
  points: GeoPoint[],
  format: (value: number) => string,
): EChartsOption {
  const maxMagnitude = Math.max(...points.map(point => Math.abs(point.value ?? 1)), 1);
  const bubble = Boolean(metricColumn(ctx));
  return {
    animation: false,
    tooltip: { trigger: 'item', formatter: pointTooltip(format, bubble), ...TOOLTIP_STYLE },
    geo: chinaGeo(ctx.compact),
    series: [
      {
        type: 'scatter',
        coordinateSystem: 'geo',
        data: points.map(point => ({
          name: point.name,
          value: [point.lng, point.lat, point.value ?? 1],
          symbolSize: bubble
            ? 6 + 26 * Math.sqrt(Math.abs(point.value ?? 1) / maxMagnitude)
            : ctx.compact
              ? 6
              : 8,
        })),
        itemStyle: { color: PRIMARY, opacity: 0.75 },
        emphasis: { itemStyle: { opacity: 1 } },
      },
    ],
  };
}

/** Coordinate scatter for non-map viz types: raw lat/lng on cartesian axes. */
function scatterOption(
  ctx: VizContext,
  points: GeoPoint[],
  format: (value: number) => string,
): EChartsOption {
  const maxMagnitude = Math.max(...points.map(point => Math.abs(point.value ?? 1)), 1);
  const bubble = Boolean(metricColumn(ctx));
  const axis = {
    type: 'value' as const,
    scale: true,
    axisLine: { show: false },
    axisTick: { show: false },
    axisLabel: { color: AXIS_COLOR, fontSize: 10 },
    splitLine: { lineStyle: { color: SPLIT_COLOR } },
  };
  return {
    animation: false,
    grid: gridOption(ctx.compact, { bottom: 30, right: 18 }),
    tooltip: { trigger: 'item', formatter: pointTooltip(format, bubble), ...TOOLTIP_STYLE },
    xAxis: { ...axis, name: 'longitude', nameTextStyle: { color: AXIS_COLOR, fontSize: 10 } },
    yAxis: { ...axis, name: 'latitude', nameTextStyle: { color: AXIS_COLOR, fontSize: 10 } },
    series: [
      {
        type: 'scatter',
        data: points.map(point => ({
          name: point.name,
          value: [point.lng, point.lat, point.value ?? 1],
          symbolSize: bubble
            ? 6 + 26 * Math.sqrt(Math.abs(point.value ?? 1) / maxMagnitude)
            : ctx.compact
              ? 6
              : 8,
        })),
        itemStyle: { color: PRIMARY, opacity: 0.75 },
        emphasis: { itemStyle: { opacity: 1 } },
      },
    ],
  };
}

/**
 * Geo visualisations. Map types (world_map / country_map / mapbox / deck_geojson)
 * draw the bundled China basemap — a province choropleth when the result carries
 * region names, otherwise a lat/lng bubble layer. Deck.gl scatter types without
 * region names stay on cartesian axes.
 */
export const GeoRenderer: VizRenderer = ({ ctx }) => {
  const [mapReady, setMapReady] = useState(isChinaMapRegistered());

  useEffect(() => {
    if (mapReady) return undefined;
    let alive = true;
    void ensureChinaMap().then(ok => {
      if (alive) setMapReady(ok);
    });
    return () => {
      alive = false;
    };
  }, [mapReady]);

  if (!ctx.colnames.length) return <ChartEmpty height={ctx.height} />;

  const format = makeNumberFormatter(readString(ctx.formData, ['y_axis_format', 'number_format']));
  const mapViz = MAP_VIZ_TYPES.test(ctx.vizType);

  if (mapViz) {
    if (!mapReady) {
      return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.geo_loading')} />;
    }
    const regionCol = findRegionColumn(ctx);
    if (regionCol) {
      const option = choroplethOption(ctx, regionCol);
      if (option) return <EChart option={option} height={ctx.height} onClickData={ctx.onClickData} />;
    }
    const { lat, lng } = findGeoColumns(ctx.colnames);
    if (lat && lng) {
      const points = collectPoints(ctx, lat, lng);
      if (points.length) {
        return (
          <EChart
            option={chinaPointOption(ctx, points, format)}
            height={ctx.height}
            onClickData={ctx.onClickData}
          />
        );
      }
    }
    return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.geo_need_columns')} />;
  }

  const { lat, lng } = findGeoColumns(ctx.colnames);
  if (lat && lng) {
    const points = collectPoints(ctx, lat, lng);
    if (points.length) {
      return (
        <EChart
          option={scatterOption(ctx, points, format)}
          height={ctx.height}
          onClickData={ctx.onClickData}
        />
      );
    }
  }
  return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.geo_no_values')} />;
};
