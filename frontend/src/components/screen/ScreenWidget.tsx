/**
 * One wall component renderer, shared by the editor, the viewer and the
 * template gallery. Everything is driven by the stored document — no per-type
 * React state lives outside this file.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import { Spin } from 'antd';
import { client, endpoints, errorMessage } from '../../api/client';
import type { ChartDataResponse, DashboardChart, QueryResultItem } from '../../api/types';
import ChartPreview from '../charts/ChartPreview';
import { EChart } from '../charts/EChart';
import {
  buildChartQueryContext,
  chartFormData,
  hasQueryableFormData,
} from '../../features/dashboard/dataMask';
import { buildDemoOption, needsChinaMap } from '../../features/screen/demoOption';
import { ensureChinaMap } from '../charts/chinaMap';
import { prop } from '../../features/screen/model';
import type { ScreenChartRef, ScreenComponent, ScreenFilter } from '../../api/screens';
import { useI18n } from '../../i18n';

export interface ScreenWidgetProps {
  component: ScreenComponent;
  mode: 'edit' | 'view';
  filterValues?: Record<string, string>;
  onFilterChange?: (filter: ScreenFilter, value: string) => void;
  refreshKey?: number;
}

/* ------------------------------------------------------------- utilities */

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六'];

function pad(value: number): string {
  return value < 10 ? `0${value}` : String(value);
}

function formatThousands(raw: string): string {
  const value = Number(raw.replace(/,/g, ''));
  if (!Number.isFinite(value)) return raw;
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

/** Strips script-ish markup from authored rich text. */
export function sanitizeHtml(html: string): string {
  return html
    .replace(/<\s*script[\s\S]*?<\s*\/\s*script\s*>/gi, '')
    .replace(/<\s*iframe[\s\S]*?<\s*\/\s*iframe\s*>/gi, '')
    .replace(/\son\w+\s*=\s*"[^"]*"/gi, '')
    .replace(/\son\w+\s*=\s*'[^']*'/gi, '')
    .replace(/javascript:/gi, '');
}

function safeUrl(raw: string, allowData = false): string {
  const value = raw.trim();
  if (!value) return '';
  if (/^https?:\/\//i.test(value) || value.startsWith('/') || value.startsWith('//')) {
    return value;
  }
  if (allowData && /^data:image\//i.test(value)) return value;
  return '';
}

/* ------------------------------------------------------------------ KPI */

function Sparkline({ data, color }: { data: number[]; color: string }) {
  if (data.length < 2) return null;
  const width = 96;
  const height = 28;
  const min = Math.min(...data);
  const max = Math.max(...data);
  const span = max - min || 1;
  const points = data
    .map((value, index) => {
      const x = (index / (data.length - 1)) * width;
      const y = height - ((value - min) / span) * (height - 4) - 2;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');
  return (
    <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} aria-hidden>
      <polyline
        points={points}
        fill="none"
        stroke={color}
        strokeWidth={1.6}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

function KpiBody({
  label,
  value,
  unit,
  color,
  muted,
  trend,
  spark,
}: {
  label: string;
  value: string;
  unit: string;
  color: string;
  muted: string;
  trend: number;
  spark: number[];
}) {
  return (
    <>
      <div className="scr-kpi__head">
        <span className="scr-kpi__label" style={{ color: muted }}>
          {label}
        </span>
        {trend !== 0 && (
          <span className="scr-kpi__trend" style={{ color: trend > 0 ? '#ff6b6b' : '#22c55e' }}>
            {trend > 0 ? '▲' : '▼'} {Math.abs(trend).toFixed(1)}%
          </span>
        )}
      </div>
      <div className="scr-kpi__body">
        <span className="scr-kpi__value" style={{ color }}>
          {value}
        </span>
        <span className="scr-kpi__unit" style={{ color: muted }}>
          {unit}
        </span>
      </div>
      <div className="scr-kpi__spark">
        <Sparkline data={spark} color={color} />
      </div>
      <i className="scr-kpi__bar" style={{ background: color }} />
    </>
  );
}

function KpiWidget({ component, refreshKey }: { component: ScreenComponent; refreshKey?: number }) {
  const p = component.props;
  const color = prop.str(p, 'color', '#37a2ff');
  const panel = prop.str(p, 'panel', 'rgba(13,53,99,0.42)');
  const border = prop.str(p, 'border', 'rgba(55,162,255,0.38)');
  const muted = prop.str(p, 'muted', '#7ea9cc');
  const bound = useBoundData(component, refreshKey);
  const boundActive = hasDataRef(component);

  const rows = bound.result?.data ?? [];
  const columns = bound.result ? resultColumns(bound.result) : [];
  const valueColumn = firstNumericColumn(rows, columns);

  // A bound metric supplies the figure; the label stays authored in the panel.
  const value = boundActive
    ? kpiValueText(valueColumn ? rows[0]?.[valueColumn] : '')
    : prop.str(p, 'value', '0');
  const spark = boundActive
    ? valueColumn
      ? rows.map(row => toNumber(row[valueColumn]))
      : []
    : prop.arr<number>(p, 'spark');

  const body = (
    <KpiBody
      label={prop.str(p, 'label', '指标')}
      value={value}
      unit={prop.str(p, 'unit', '')}
      color={color}
      muted={muted}
      trend={prop.num(p, 'trend', 0)}
      spark={spark}
    />
  );

  return (
    <div className="scr-kpi" style={{ background: panel, borderColor: border }}>
      {boundActive ? (
        <DataState bound={bound} muted={muted}>
          {body}
        </DataState>
      ) : (
        body
      )}
    </div>
  );
}

/* ------------------------------------------------------------- result data */

type ResultRow = Record<string, unknown>;

/** Columns of a query result, falling back to the first row's keys. */
function resultColumns(result: QueryResultItem): string[] {
  if (result.colnames?.length) return result.colnames;
  const rows = Array.isArray(result.data) ? result.data : [];
  return rows.length ? Object.keys(rows[0]) : [];
}

function isNumericValue(value: unknown): boolean {
  if (typeof value === 'number') return Number.isFinite(value);
  if (typeof value === 'string' && value.trim() !== '') return Number.isFinite(Number(value));
  return false;
}

/** First column whose value in the first row parses as a number. */
function firstNumericColumn(rows: ResultRow[], columns: string[]): string | null {
  const first = rows[0];
  if (!first) return null;
  return columns.find(column => isNumericValue(first[column])) ?? null;
}

function toNumber(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

/** Table cell text; mirrors the string-only contract of the demo props. */
function cellText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'object') return JSON.stringify(value);
  if (typeof value === 'number') return value.toLocaleString('zh-CN', { maximumFractionDigits: 4 });
  return String(value);
}

function kpiValueText(value: unknown): string {
  if (typeof value === 'number') return formatThousands(String(value));
  return value === null || value === undefined ? '' : String(value);
}

export interface BoundData {
  result: QueryResultItem | null;
  loading: boolean;
  error: string | null;
}

/** True when a tile is driven by a saved chart or by a bound dataset. */
export function hasDataRef(component: ScreenComponent): boolean {
  return Boolean(component.chart?.id || component.dataset?.id);
}

/** Loading / error / empty states shared by every data-driven widget. */
function DataState({
  bound,
  muted,
  children,
}: {
  bound: BoundData;
  muted: string;
  children: React.ReactNode;
}) {
  const { t } = useI18n();
  if (bound.error) {
    return (
      <div className="scr-widget__error" style={{ color: '#ff8a8a' }}>
        {bound.error}
      </div>
    );
  }
  if (bound.loading && !bound.result) {
    return (
      <div className="scr-widget__center">
        <Spin size="small" />
      </div>
    );
  }
  if (!bound.result) {
    return (
      <div className="scr-widget__error" style={{ color: muted }}>
        {t('screen.widget.no_data')}
      </div>
    );
  }
  return <>{children}</>;
}

/* ---------------------------------------------------------------- charts */

function useBoundData(component: ScreenComponent, refreshKey = 0): BoundData {
  // A tile can be driven by a saved chart or by a dataset bound directly.
  const ref = useMemo<ScreenChartRef | null>(() => {
    if (component.chart) return component.chart;
    if (component.dataset) {
      return {
        name: component.dataset.name,
        vizType: component.dataset.vizType,
        datasourceId: component.dataset.id,
        formData: component.dataset.formData,
      };
    }
    return null;
  }, [component.chart, component.dataset]);
  const [result, setResult] = useState<QueryResultItem | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const formData = useMemo(
    () => (ref?.formData ?? {}) as Record<string, unknown>,
    [ref?.formData],
  );

  useEffect(() => {
    if (!ref?.id && !ref?.datasourceId) {
      setResult(null);
      setError(null);
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    const chartLike = {
      id: ref.id,
      slice_name: ref.name ?? '',
      viz_type: ref.vizType ?? 'table',
      datasource_id: ref.datasourceId ?? 0,
      datasource_type: 'table',
      datasource_name: '',
      owners: [],
      params: formData,
      form_data: formData,
    } as unknown as DashboardChart;

    // A dataset-bound tile has no saved chart to fetch, so any tile without a
    // queryable form_data must still go through the explicit query endpoint.
    const request =
      hasQueryableFormData(chartLike) || !ref.id
        ? client.post<ChartDataResponse>(endpoints.chartData, {
            ...buildChartQueryContext(chartLike),
          })
        : client.get<ChartDataResponse>(endpoints.chartDataById(ref.id));

    request
      .then(response => {
        if (cancelled) return;
        if (response.errors?.length) {
          setError(response.errors.map(item => item.message).filter(Boolean).join('; '));
          setResult(null);
        } else {
          setResult(response.result?.[0] ?? null);
          setError(null);
        }
      })
      .catch(err => {
        if (!cancelled) {
          setError(errorMessage(err));
          setResult(null);
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [ref?.id, ref?.vizType, ref?.datasourceId, ref?.name, formData, refreshKey]);

  return { result, loading, error };
}

function WidgetTitle({
  title,
  color,
  muted,
}: {
  title: string;
  color: string;
  muted: string;
}) {
  if (!title) return null;
  return (
    <div className="scr-widget__title" style={{ color }}>
      <i style={{ background: color }} />
      <span>{title}</span>
      <em style={{ background: `linear-gradient(90deg, ${color}, transparent)` }} />
      <span className="scr-widget__title-gap" style={{ color: muted }} />
    </div>
  );
}

function ChartWidget({ component, refreshKey }: { component: ScreenComponent; refreshKey?: number }) {
  const p = component.props;
  const title = prop.str(p, 'title');
  const color = prop.str(p, 'color', '#37a2ff');
  const muted = prop.str(p, 'muted', '#7ea9cc');
  const bound = useBoundData(component, refreshKey);
  const [mapReady, setMapReady] = useState(!needsChinaMap(p));

  useEffect(() => {
    if (!needsChinaMap(p) || mapReady) return;
    let alive = true;
    void ensureChinaMap().then(() => {
      if (alive) setMapReady(true);
    });
    return () => {
      alive = false;
    };
  }, [p, mapReady]);

  const option = useMemo(() => buildDemoOption(p), [p]);
  const bodyHeight = Math.max(60, component.h - (title ? 34 : 0));

  return (
    <div className="scr-widget">
      <WidgetTitle title={title} color={color} muted={muted} />
      <div className="scr-widget__body" style={{ height: bodyHeight }}>
        {hasDataRef(component) ? (
          <DataState bound={bound} muted={muted}>
            <ChartPreview
              result={bound.result!}
              vizType={component.chart?.vizType ?? component.dataset?.vizType ?? 'table'}
              height={bodyHeight}
              formData={component.chart?.formData ?? component.dataset?.formData}
            />
          </DataState>
        ) : needsChinaMap(p) && !mapReady ? (
          <div className="scr-widget__center">
            <Spin size="small" />
          </div>
        ) : (
          <EChart option={option} height={bodyHeight} ariaLabel={`${title || 'chart'}`} />
        )}
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------- table */

function TableWidget({ component, refreshKey }: { component: ScreenComponent; refreshKey?: number }) {
  const p = component.props;
  const title = prop.str(p, 'title');
  const color = prop.str(p, 'color', '#37a2ff');
  const text = prop.str(p, 'text', '#e9f4ff');
  const muted = prop.str(p, 'muted', '#7ea9cc');
  const panel = prop.str(p, 'panel', 'rgba(13,53,99,0.42)');
  const border = prop.str(p, 'border', 'rgba(55,162,255,0.38)');
  const bound = useBoundData(component, refreshKey);
  const boundActive = hasDataRef(component);

  // A bound dataset supplies both the header and the body rows.
  const boundColumns = bound.result ? resultColumns(bound.result) : [];
  const columns = boundActive ? boundColumns : prop.arr<string>(p, 'columns');
  const rows =
    boundActive && bound.result
      ? bound.result.data.map(row => boundColumns.map(column => cellText(row[column])))
      : prop.arr<string[]>(p, 'rows');

  const table = (
    <table>
      <thead>
        <tr>
          {columns.map((column, index) => (
            <th key={`${column}-${index}`} style={{ color, borderColor: border }}>
              {column}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, rowIndex) => (
          <tr key={rowIndex} style={{ background: rowIndex % 2 ? 'rgba(255,255,255,0.03)' : 'transparent' }}>
            {row.map((cell, cellIndex) => (
              <td key={cellIndex} style={{ color: cellIndex === 0 ? text : muted, borderColor: border }}>
                {cell}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );

  return (
    <div className="scr-widget" style={{ background: panel, borderColor: border }}>
      <WidgetTitle title={title} color={color} muted={muted} />
      <div className="scr-table" style={{ height: Math.max(40, component.h - (title ? 34 : 0)) }}>
        {boundActive ? (
          <DataState bound={bound} muted={muted}>
            {table}
          </DataState>
        ) : (
          table
        )}
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ list */

function ListWidget({ component, refreshKey }: { component: ScreenComponent; refreshKey?: number }) {
  const p = component.props;
  const title = prop.str(p, 'title');
  const color = prop.str(p, 'color', '#37a2ff');
  const text = prop.str(p, 'text', '#e9f4ff');
  const muted = prop.str(p, 'muted', '#7ea9cc');
  const panel = prop.str(p, 'panel', 'rgba(13,53,99,0.42)');
  const border = prop.str(p, 'border', 'rgba(55,162,255,0.38)');
  const bound = useBoundData(component, refreshKey);
  const boundActive = hasDataRef(component);

  // A bound dataset is folded into label/value pairs: the first text column
  // names the row, the first numeric column sizes it.
  const boundItems = useMemo(() => {
    const result = bound.result;
    if (!result) return [];
    const columns = resultColumns(result);
    const valueColumn = firstNumericColumn(result.data, columns);
    const labelColumn = columns.find(column => column !== valueColumn) ?? columns[0];
    return result.data.map(row => ({
      label: cellText(row[labelColumn]),
      value: valueColumn ? toNumber(row[valueColumn]) : 0,
    }));
  }, [bound.result]);

  const items = boundActive ? boundItems : prop.arr<{ label: string; value: number }>(p, 'items');
  const max = Math.max(1, ...items.map(item => Number(item.value) || 0));

  const list = (
    <div className="scr-list">
      {items.slice(0, 12).map((item, index) => (
        <div className="scr-list__row" key={`${item.label}-${index}`}>
          <span className="scr-list__rank" style={{ background: index < 3 ? color : 'rgba(255,255,255,0.10)' }}>
            {index + 1}
          </span>
          <span className="scr-list__label" style={{ color: text }}>
            {item.label}
          </span>
          <span className="scr-list__value" style={{ color }}>
            {formatThousands(String(item.value))}
          </span>
          <span className="scr-list__track">
            <i style={{ width: `${Math.max(4, (Number(item.value) / max) * 100)}%`, background: color }} />
          </span>
        </div>
      ))}
      {!items.length && (
        <div className="scr-widget__error" style={{ color: muted }}>
          No data
        </div>
      )}
    </div>
  );

  return (
    <div className="scr-widget" style={{ background: panel, borderColor: border }}>
      <WidgetTitle title={title} color={color} muted={muted} />
      {boundActive ? (
        <DataState bound={bound} muted={muted}>
          {list}
        </DataState>
      ) : (
        list
      )}
    </div>
  );
}

/* ----------------------------------------------------------------- clock */

function ClockWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const format = prop.str(p, 'format', 'datetime');
  const color = prop.str(p, 'color', '#e9f4ff');
  const muted = prop.str(p, 'mutedColor', '#7ea9cc');
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const date = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
  const time = `${pad(now.getHours())}:${pad(now.getMinutes())}:${pad(now.getSeconds())}`;
  return (
    <div
      className="scr-clock"
      style={{
        color,
        fontSize: prop.num(p, 'fontSize', 20),
        justifyContent: prop.str(p, 'align', 'left') === 'right' ? 'flex-end' : 'flex-start',
      }}
    >
      {format !== 'time' && <span>{date}</span>}
      {format !== 'date' && (
        <span style={{ color: format === 'datetime' ? color : muted }}>{time}</span>
      )}
      {format === 'datetime' && (
        <span style={{ color: muted, fontSize: '0.62em' }}>星期{WEEKDAYS[now.getDay()]}</span>
      )}
    </div>
  );
}

/* ------------------------------------------------------------- decoration */

function BorderWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const color = prop.str(p, 'color', '#37a2ff');
  const variant = prop.str(p, 'variant', 'tech-1');
  const title = prop.str(p, 'title');
  const text = prop.str(p, 'text', '#e9f4ff');
  return (
    <div className={`scr-border scr-border--${variant}`} style={{ borderColor: color }}>
      <i className="scr-border__tl" style={{ borderColor: color }} />
      <i className="scr-border__tr" style={{ borderColor: color }} />
      <i className="scr-border__bl" style={{ borderColor: color }} />
      <i className="scr-border__br" style={{ borderColor: color }} />
      {title && (
        <div className="scr-border__title" style={{ color: text }}>
          <i style={{ background: color }} />
          {title}
        </div>
      )}
    </div>
  );
}

function DecorationWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const variant = prop.str(p, 'variant', 'grid');
  const color = prop.str(p, 'color', '#37a2ff');
  const opacity = prop.num(p, 'opacity', 0.1);
  if (variant === 'glow') {
    return (
      <div
        className="scr-decor"
        style={{
          background: `radial-gradient(60% 50% at 50% 0%, ${color}33 0%, transparent 70%)`,
          opacity,
        }}
      />
    );
  }
  if (variant === 'line') {
    return <div className="scr-decor scr-decor--scan" style={{ color }} />;
  }
  if (variant === 'corner') {
    return (
      <div className="scr-decor scr-decor--corner" style={{ color, opacity }}>
        <i /><i /><i /><i />
      </div>
    );
  }
  return (
    <div
      className="scr-decor"
      style={{
        opacity,
        backgroundImage: `linear-gradient(${color} 1px, transparent 1px), linear-gradient(90deg, ${color} 1px, transparent 1px)`,
        backgroundSize: '40px 40px',
      }}
    />
  );
}

/* ---------------------------------------------------------------- shapes */

function ShapeWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const shape = prop.str(p, 'shape', 'rect');
  const fill = prop.str(p, 'fill', '#37a2ff');
  const radius = prop.num(p, 'radius', 0);
  const opacity = prop.num(p, 'opacity', 1);
  const style: React.CSSProperties = { background: fill, opacity };
  if (shape === 'circle') style.borderRadius = '50%';
  else if (shape === 'line') style.height = 2;
  else style.borderRadius = radius;
  return <div className="scr-shape" style={style} />;
}

/* -------------------------------------------------------------- progress */

function ProgressWidget({ component, refreshKey }: { component: ScreenComponent; refreshKey?: number }) {
  const p = component.props;
  const bound = useBoundData(component, refreshKey);
  const boundActive = hasDataRef(component);

  const boundRows = bound.result?.data ?? [];
  const boundColumns = bound.result ? resultColumns(bound.result) : [];
  const valueColumn = firstNumericColumn(boundRows, boundColumns);
  const value = boundActive
    ? toNumber(valueColumn ? boundRows[0]?.[valueColumn] : 0)
    : prop.num(p, 'value', 68);
  const max = Math.max(1, prop.num(p, 'max', 100));
  const ratio = Math.min(1, Math.max(0, value / max));
  const color = prop.str(p, 'color', '#37a2ff');
  const text = prop.str(p, 'text', '#e9f4ff');
  const muted = prop.str(p, 'muted', '#7ea9cc');
  const panel = prop.str(p, 'panel', 'rgba(13,53,99,0.42)');
  const border = prop.str(p, 'border', 'rgba(55,162,255,0.38)');

  const body = (
    <>
      <div className="scr-progress__head">
        <span style={{ color: muted }}>{prop.str(p, 'label', '完成率')}</span>
        <span style={{ color }}>
          {prop.bool(p, 'showPercent', true) ? `${Math.round(ratio * 100)}%` : `${value}/${max}`}
        </span>
      </div>
      <div className="scr-progress__track">
        <i style={{ width: `${ratio * 100}%`, background: `linear-gradient(90deg, ${color}, ${color}99)` }} />
      </div>
      <div className="scr-progress__foot" style={{ color: text }}>
        <span style={{ opacity: 0.7 }}>{prop.str(p, 'note', '')}</span>
      </div>
    </>
  );

  return (
    <div className="scr-progress" style={{ background: panel, borderColor: border }}>
      {boundActive ? (
        <DataState bound={bound} muted={muted}>
          {body}
        </DataState>
      ) : (
        body
      )}
    </div>
  );
}

/* ----------------------------------------------------------------- text */

function TextWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  return (
    <div
      className="scr-text"
      style={{
        fontSize: prop.num(p, 'fontSize', 24),
        color: prop.str(p, 'color', '#e9f4ff'),
        textAlign: prop.str(p, 'align', 'left') as React.CSSProperties['textAlign'],
        fontWeight: prop.bool(p, 'bold') ? 700 : 400,
        letterSpacing: prop.num(p, 'letterSpacing', 0),
        lineHeight: prop.num(p, 'lineHeight', 1.4),
        background: prop.str(p, 'background', 'transparent'),
      }}
    >
      {prop.str(p, 'text', '文本')}
    </div>
  );
}

function MarqueeWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const speed = Math.max(10, prop.num(p, 'speed', 60));
  return (
    <div className="scr-marquee" style={{ background: prop.str(p, 'background', 'transparent') }}>
      <span
        className="scr-marquee__text"
        style={{
          color: prop.str(p, 'color', '#7ea9cc'),
          fontSize: prop.num(p, 'fontSize', 14),
          animationDuration: `${speed}s`,
        }}
      >
        {prop.str(p, 'text', '')}
      </span>
    </div>
  );
}

/* ---------------------------------------------------------------- media */

function ImageWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const src = safeUrl(prop.str(p, 'src'), true);
  const radius = prop.num(p, 'radius', 4);
  if (!src) {
    return (
      <div className="scr-placeholder" style={{ color: prop.str(p, 'color', '#7ea9cc'), borderRadius: radius }}>
        图片占位 · 在右侧填写图片地址
      </div>
    );
  }
  return (
    <img
      src={src}
      alt={component.name}
      className="scr-media"
      style={{
        objectFit: prop.str(p, 'fit', 'cover') as React.CSSProperties['objectFit'],
        opacity: prop.num(p, 'opacity', 1),
        borderRadius: radius,
      }}
    />
  );
}

function VideoWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const src = safeUrl(prop.str(p, 'src'));
  if (!src) {
    return (
      <div className="scr-placeholder" style={{ color: '#7ea9cc' }}>
        视频占位 · 支持 MP4 / HLS 地址
      </div>
    );
  }
  return (
    <video
      className="scr-media"
      src={src}
      autoPlay={prop.bool(p, 'autoplay', true)}
      loop={prop.bool(p, 'loop', true)}
      muted={prop.bool(p, 'muted', true)}
      controls={prop.bool(p, 'controls', false)}
      playsInline
    />
  );
}

function IframeWidget({ component, mode }: { component: ScreenComponent; mode: 'edit' | 'view' }) {
  const p = component.props;
  const src = safeUrl(prop.str(p, 'src'));
  if (!src) {
    return (
      <div className="scr-placeholder" style={{ color: '#7ea9cc' }}>
        网页嵌入占位 · 支持 http/https 页面
      </div>
    );
  }
  return (
    <iframe
      className="scr-media scr-frame"
      src={src}
      title={prop.str(p, 'title', component.name)}
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      style={{ pointerEvents: mode === 'edit' ? 'none' : 'auto' }}
    />
  );
}

function RichTextWidget({ component }: { component: ScreenComponent }) {
  const p = component.props;
  const html = useMemo(() => sanitizeHtml(prop.str(p, 'html', '')), [p]);
  return (
    <div
      className="scr-richtext"
      style={{ color: prop.str(p, 'color', '#e9f4ff') }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

/* ---------------------------------------------------------------- filter */

function FilterWidget({
  component,
  mode,
  filterValues,
  onFilterChange,
}: {
  component: ScreenComponent;
  mode: 'edit' | 'view';
  filterValues?: Record<string, string>;
  onFilterChange?: (filter: ScreenFilter, value: string) => void;
}) {
  const p = component.props;
  const label = prop.str(p, 'label', '筛选');
  const options = prop.arr<string>(p, 'options');
  const filter: ScreenFilter = {
    id: component.id,
    name: label,
    kind: 'select',
    options,
  };
  const current = filterValues?.[component.id] ?? prop.str(p, 'defaultValue', options[0] ?? '');
  return (
    <div
      className="scr-filter"
      style={{
        background: prop.str(p, 'panel', 'rgba(13,53,99,0.42)'),
        borderColor: prop.str(p, 'border', 'rgba(55,162,255,0.38)'),
      }}
    >
      <span style={{ color: prop.str(p, 'muted', '#7ea9cc') }}>{label}</span>
      <select
        value={current}
        disabled={mode === 'edit'}
        onChange={event => onFilterChange?.(filter, event.target.value)}
        style={{ color: prop.str(p, 'text', '#e9f4ff'), borderColor: prop.str(p, 'border', '') }}
      >
        {(options.length ? options : ['全部']).map(option => (
          <option key={option} value={option} style={{ color: '#111' }}>
            {option}
          </option>
        ))}
      </select>
    </div>
  );
}

/* ------------------------------------------------------------------ main */

export default function ScreenWidget(props: ScreenWidgetProps) {
  const { component, mode, filterValues, onFilterChange, refreshKey } = props;
  const p = component.props;
  const color = prop.str(p, 'color', '#37a2ff');
  const panel = prop.str(p, 'panel', 'transparent');
  const border = prop.str(p, 'border', 'transparent');

  switch (component.type) {
    case 'kpi':
      return <KpiWidget component={component} refreshKey={refreshKey} />;
    case 'chart':
      return <ChartWidget component={component} refreshKey={refreshKey} />;
    case 'table':
      return <TableWidget component={component} refreshKey={refreshKey} />;
    case 'list':
      return <ListWidget component={component} refreshKey={refreshKey} />;
    case 'text':
      return <TextWidget component={component} />;
    case 'marquee':
      return <MarqueeWidget component={component} />;
    case 'clock':
      return <ClockWidget component={component} />;
    case 'image':
      return <ImageWidget component={component} />;
    case 'video':
      return <VideoWidget component={component} />;
    case 'iframe':
      return <IframeWidget component={component} mode={mode} />;
    case 'border':
      return <BorderWidget component={component} />;
    case 'decoration':
      return <DecorationWidget component={component} />;
    case 'shape':
      return <ShapeWidget component={component} />;
    case 'progress':
      return <ProgressWidget component={component} refreshKey={refreshKey} />;
    case 'richtext':
      return <RichTextWidget component={component} />;
    case 'filter':
      return (
        <FilterWidget
          component={component}
          mode={mode}
          filterValues={filterValues}
          onFilterChange={onFilterChange}
        />
      );
    case 'group':
      return (
        <div
          className="scr-group"
          style={{ background: panel, borderColor: border }}
        >
          <span style={{ color, background: `${color}22` }}>{component.name}</span>
        </div>
      );
    default:
      return (
        <div className="scr-placeholder" style={{ color }}>
          {component.type}
        </div>
      );
  }
}
