/**
 * Right-hand inspector: geometry, data binding, per-type style fields and the
 * canvas settings shown when nothing is selected.
 */
import { useEffect, useMemo, useState } from 'react';
import { Divider, Input, InputNumber, Select, Switch, Tooltip } from 'antd';
import { client, endpoints } from '../../api/client';
import type { Chart, Dataset } from '../../api/types';
import type { ScreenComponent, ScreenConfig } from '../../api/screens';
import { VIZ_CATEGORIES, VIZ_TYPES, vizLabel } from '../charts/vizTypes';
import {
  COMPONENT_LABEL_KEY,
  SCREEN_THEMES,
  defaultTheme,
  prop,
} from '../../features/screen/model';
import { useI18n } from '../../i18n';

const { TextArea } = Input;

export interface ScreenPropertyPanelProps {
  config: ScreenConfig;
  component: ScreenComponent | null;
  selectedCount: number;
  onChange: (patch: Partial<ScreenComponent>) => void;
  onProps: (props: Record<string, unknown>) => void;
  onCommit: () => void;
  onCanvas: (patch: Partial<ScreenConfig['canvas']>) => void;
  onInteraction: (patch: Partial<NonNullable<ScreenConfig['interaction']>>) => void;
  charts: Chart[];
  chartsLoading: boolean;
  onSearchCharts: (keyword: string) => void;
}

interface RowProps {
  label: string;
  children: React.ReactNode;
}

function Row({ label, children }: RowProps) {
  return (
    <label className="scr-field">
      <span className="scr-field__label">{label}</span>
      <span className="scr-field__control">{children}</span>
    </label>
  );
}

function ColorRow({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <Row label={label}>
      <span className="scr-color">
        <input type="color" value={normalizeColor(value)} onChange={e => onChange(e.target.value)} />
        <Input size="small" value={value} onChange={e => onChange(e.target.value)} />
      </span>
    </Row>
  );
}

/** `<input type=color>` only accepts #rrggbb. */
function normalizeColor(value: string): string {
  if (/^#[0-9a-f]{6}$/i.test(value)) return value;
  if (/^#[0-9a-f]{3}$/i.test(value)) {
    return `#${value[1]}${value[1]}${value[2]}${value[2]}${value[3]}${value[3]}`;
  }
  return '#37a2ff';
}

function parseNumberList(raw: string): number[] {
  return raw
    .split(/[,\s]+/)
    .map(item => Number(item))
    .filter(item => Number.isFinite(item));
}

function parseStringList(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map(item => item.trim())
    .filter(Boolean);
}

/** Default viz type used when a chart tile binds a dataset without a choice. */
const DEFAULT_DATASET_VIZ = 'echarts_timeseries_bar';

/** Which data shape a component needs out of its bound dataset. */
type DatasetBindKind = 'chart' | 'kpi' | 'table' | 'list' | 'progress';

function asStringList(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string')
    : [];
}

/**
 * Builds a queryable `form_data` for a dataset-bound tile: a dataset alone
 * carries no query, so each component kind gets a sensible default — a KPI
 * wants one aggregate, a table wants raw columns, a list wants one dimension
 * plus one metric, a chart wants a metric grouped by a dimension.
 */
function defaultDatasetFormData(
  dataset: Dataset,
  kind: DatasetBindKind,
): Record<string, unknown> {
  const columns = (dataset.columns ?? []).filter(column => column.is_active !== false);
  const names = columns.map(column => column.column_name);
  const metricNames = (dataset.metrics ?? []).map(metric => metric.metric_name);
  const dimensions = columns
    .filter(column => column.groupby !== false && !column.is_dttm)
    .map(column => column.column_name);
  const timeColumn = columns.find(column => column.is_dttm)?.column_name;

  const form: Record<string, unknown> = { row_limit: 10000 };
  if (timeColumn) form.granularity_sqla = timeColumn;

  if (kind === 'table') {
    // A table shows raw rows, so select the columns themselves.
    form.viz_type = 'table';
    form.columns = names.slice(0, 20);
    return form;
  }
  if (kind === 'kpi' || kind === 'progress') {
    // A single figure: one aggregate, no grouping.
    form.viz_type = 'big_number';
    form.metrics = metricNames.length ? [metricNames[0]] : [];
    if (!metricNames.length) form.columns = names.slice(0, 4);
    return form;
  }
  if (kind === 'list') {
    // label + value pairs: one dimension against one metric.
    form.viz_type = 'table';
    if (metricNames.length) {
      form.metrics = [metricNames[0]];
      if (dimensions.length) form.groupby = [dimensions[0]];
    } else {
      form.columns = names.slice(0, 5);
    }
    return form;
  }
  form.viz_type = DEFAULT_DATASET_VIZ;
  if (metricNames.length) {
    form.metrics = [metricNames[0]];
    if (dimensions.length) form.groupby = [dimensions[0]];
  } else {
    form.columns = names.slice(0, 12);
  }
  return form;
}

/** Dataset picker plus the metric / dimension picks it feeds. */
function DatasetBindingSection({
  component,
  kind,
  onChange,
  onCommit,
}: {
  component: ScreenComponent;
  kind: DatasetBindKind;
  onChange: (patch: Partial<ScreenComponent>) => void;
  onCommit: () => void;
}) {
  const { t } = useI18n();
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [datasetsLoading, setDatasetsLoading] = useState(false);
  const [datasetDetail, setDatasetDetail] = useState<Dataset | null>(null);

  const boundDatasetId = component.dataset?.id ?? null;

  useEffect(() => {
    let alive = true;
    setDatasetsLoading(true);
    client
      .get<{ result?: Dataset[] }>(endpoints.datasets, { page_size: 200 })
      .then(response => {
        if (alive) setDatasets(response.result ?? []);
      })
      .catch(() => {
        if (alive) setDatasets([]);
      })
      .finally(() => {
        if (alive) setDatasetsLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (!boundDatasetId) {
      setDatasetDetail(null);
      return undefined;
    }
    let alive = true;
    client
      .get<Dataset>(endpoints.dataset(boundDatasetId))
      .then(response => {
        if (alive) setDatasetDetail(response);
      })
      .catch(() => {
        if (alive) setDatasetDetail(null);
      });
    return () => {
      alive = false;
    };
  }, [boundDatasetId]);

  const datasetOptions = useMemo(
    () =>
      datasets.map(item => ({
        value: item.id,
        label: `${item.table_name} · ${item.database?.database_name ?? ''}`,
      })),
    [datasets],
  );

  const datasetVizOptions = useMemo(
    () =>
      VIZ_CATEGORIES.map(category => ({
        label: category,
        options: VIZ_TYPES.filter(option => option.category === category).map(option => ({
          value: option.value,
          label: vizLabel(option, t),
        })),
      })),
    [t],
  );

  const bound = component.dataset ?? null;
  const form = (bound?.formData ?? {}) as Record<string, unknown>;
  const metricOptions = (datasetDetail?.metrics ?? []).map(metric => ({
    value: metric.metric_name,
    label: metric.verbose_name || metric.metric_name,
  }));
  const dimensionOptions = (datasetDetail?.columns ?? [])
    .filter(column => column.groupby !== false && column.is_active !== false && !column.is_dttm)
    .map(column => ({ value: column.column_name, label: column.column_name }));
  const columnOptions = (datasetDetail?.columns ?? [])
    .filter(column => column.is_active !== false)
    .map(column => ({ value: column.column_name, label: column.column_name }));

  const update = (patch: {
    vizType?: string;
    metrics?: string[];
    groupby?: string[];
    columns?: string[];
  }) => {
    if (!bound) return;
    const vizType = patch.vizType ?? bound.vizType;
    const formData: Record<string, unknown> = { ...form, viz_type: vizType };
    formData.metrics = patch.metrics ?? asStringList(form.metrics);
    formData.groupby = patch.groupby ?? asStringList(form.groupby);
    formData.columns = patch.columns ?? asStringList(form.columns);
    onChange({ dataset: { ...bound, vizType, formData } });
    onCommit();
  };

  return (
    <>
      <Row label={t('screen.field.dataset_bind')}>
        <Select
          size="small"
          allowClear
          showSearch
          optionFilterProp="label"
          loading={datasetsLoading}
          placeholder={t('screen.field.dataset_bind_hint')}
          value={bound?.id}
          onChange={(value: number | undefined) => {
            if (!value) {
              onChange({ dataset: null });
              onCommit();
              return;
            }
            const target = datasets.find(item => item.id === value);
            if (!target) return;
            onChange({
              chart: null,
              dataset: {
                id: target.id,
                name: target.table_name,
                vizType: kind === 'chart' ? DEFAULT_DATASET_VIZ : String(form.viz_type ?? 'big_number'),
                formData: defaultDatasetFormData(target, kind),
              },
            });
            onCommit();
          }}
          options={datasetOptions}
        />
      </Row>
      {bound && (
        <>
          <div className="scr-inspector__note">
            {t('screen.field.dataset_bound')} #{bound.id} {bound.name}
          </div>
          {kind === 'chart' && (
            <Row label={t('screen.field.viz_type')}>
              <Select
                size="small"
                showSearch
                optionFilterProp="label"
                value={bound.vizType}
                onChange={value => update({ vizType: value })}
                options={datasetVizOptions}
              />
            </Row>
          )}
          <Row label={t('screen.field.metrics')}>
            <Select
              size="small"
              mode="multiple"
              value={asStringList(form.metrics)}
              placeholder={t('screen.field.metrics')}
              onChange={value => update({ metrics: value })}
              options={metricOptions}
            />
          </Row>
          <Row label={t('screen.field.groupby')}>
            <Select
              size="small"
              mode="multiple"
              value={asStringList(form.groupby)}
              placeholder={t('screen.field.groupby')}
              onChange={value => update({ groupby: value })}
              options={dimensionOptions}
            />
          </Row>
          <Row label={t('screen.field.dataset_columns')}>
            <Select
              size="small"
              mode="multiple"
              value={asStringList(form.columns)}
              placeholder={t('screen.field.dataset_columns')}
              onChange={value => update({ columns: value })}
              options={columnOptions}
            />
          </Row>
        </>
      )}
    </>
  );
}

export default function ScreenPropertyPanel({
  config,
  component,
  selectedCount,
  onChange,
  onProps,
  onCommit,
  onCanvas,
  onInteraction,
  charts,
  chartsLoading,
  onSearchCharts,
}: ScreenPropertyPanelProps) {
  const { t } = useI18n();

  if (!component) {
    return (
      <div className="scr-inspector">
        <div className="scr-inspector__head">{t('screen.canvas.title')}</div>
        {selectedCount > 1 && (
          <div className="scr-inspector__note">
            {t('screen.canvas.multi_hint').replace('{n}', String(selectedCount))}
          </div>
        )}
        <Divider orientation="left" plain>
          {t('screen.canvas.size')}
        </Divider>
        <Row label={t('screen.field.width')}>
          <InputNumber
            size="small"
            min={320}
            max={7680}
            value={config.canvas.width}
            onChange={value => {
              if (value) onCanvas({ width: value });
            }}
            onBlur={onCommit}
          />
        </Row>
        <Row label={t('screen.field.height')}>
          <InputNumber
            size="small"
            min={180}
            max={4320}
            value={config.canvas.height}
            onChange={value => {
              if (value) onCanvas({ height: value });
            }}
            onBlur={onCommit}
          />
        </Row>
        <Row label={t('screen.field.scale_mode')}>
          <Select
            size="small"
            value={config.canvas.scaleMode}
            onChange={value => {
              onCanvas({ scaleMode: value });
              onCommit();
            }}
            options={[
              { value: 'fit', label: t('screen.scale.fit') },
              { value: 'width', label: t('screen.scale.width') },
              { value: 'actual', label: t('screen.scale.actual') },
            ]}
          />
        </Row>
        <Row label={t('screen.field.grid')}>
          <InputNumber
            size="small"
            min={1}
            max={64}
            value={config.canvas.gridSize}
            onChange={value => {
              if (value) onCanvas({ gridSize: value });
            }}
            onBlur={onCommit}
          />
        </Row>
        <Row label={t('screen.field.show_grid')}>
          <Switch
            size="small"
            checked={config.canvas.showGrid}
            onChange={value => {
              onCanvas({ showGrid: value });
              onCommit();
            }}
          />
        </Row>

        <Divider orientation="left" plain>
          {t('screen.field.theme')}
        </Divider>
        <div className="scr-themes">
          {SCREEN_THEMES.map(preset => (
            <Tooltip key={preset.key} title={preset.key}>
              <button
                type="button"
                className={`scr-themes__item${
                  config.canvas.theme === preset.key ? ' is-active' : ''
                }`}
                style={{ background: preset.background }}
                onClick={() => {
                  onCanvas({ theme: preset.key, background: preset.background });
                  onCommit();
                }}
              >
                <i style={{ background: preset.theme.primary }} />
                <i style={{ background: preset.theme.accent }} />
              </button>
            </Tooltip>
          ))}
        </div>
        <ColorRow
          label={t('screen.field.background')}
          value={config.canvas.background}
          onChange={value => onCanvas({ background: value })}
        />

        <Divider orientation="left" plain>
          {t('screen.field.interaction')}
        </Divider>
        <Row label={t('screen.field.refresh')}>
          <InputNumber
            size="small"
            min={0}
            max={86400}
            value={config.interaction?.refreshSec ?? 0}
            onChange={value => onInteraction({ refreshSec: value ?? 0 })}
            onBlur={onCommit}
            addonAfter="s"
          />
        </Row>
        <Row label={t('screen.field.watermark')}>
          <Switch
            size="small"
            checked={Boolean(config.interaction?.watermark)}
            onChange={value => {
              onInteraction({ watermark: value });
              onCommit();
            }}
          />
        </Row>
      </div>
    );
  }

  const p = component.props;
  const setProp = (key: string, value: unknown) => onProps({ ...p, [key]: value });

  /** True when the tile shows data from a saved chart or a bound dataset. */
  const dataBound = Boolean(component.chart?.id || component.dataset?.id);

  const color = (key = 'color') => (
    <ColorRow
      label={t('screen.field.color')}
      value={prop.str(p, key, defaultTheme().primary)}
      onChange={value => setProp(key, value)}
    />
  );

  return (
    <div className="scr-inspector">
      <div className="scr-inspector__head">
        {t(COMPONENT_LABEL_KEY[component.type] as 'screen.widget.kpi')}
        <span className="scr-inspector__id">{component.type}</span>
      </div>

      <Divider orientation="left" plain>
        {t('screen.field.basic')}
      </Divider>
      <Row label={t('screen.field.name')}>
        <Input
          size="small"
          value={component.name}
          onChange={e => onChange({ name: e.target.value })}
          onBlur={onCommit}
        />
      </Row>
      <div className="scr-field-grid">
        <Row label="X">
          <InputNumber
            size="small"
            value={Math.round(component.x)}
            onChange={value => onChange({ x: value ?? 0 })}
            onBlur={onCommit}
          />
        </Row>
        <Row label="Y">
          <InputNumber
            size="small"
            value={Math.round(component.y)}
            onChange={value => onChange({ y: value ?? 0 })}
            onBlur={onCommit}
          />
        </Row>
        <Row label="W">
          <InputNumber
            size="small"
            min={8}
            value={Math.round(component.w)}
            onChange={value => onChange({ w: value ?? 8 })}
            onBlur={onCommit}
          />
        </Row>
        <Row label="H">
          <InputNumber
            size="small"
            min={8}
            value={Math.round(component.h)}
            onChange={value => onChange({ h: value ?? 8 })}
            onBlur={onCommit}
          />
        </Row>
        <Row label={t('screen.field.rotate')}>
          <InputNumber
            size="small"
            min={-180}
            max={180}
            value={component.rotate ?? 0}
            onChange={value => onChange({ rotate: value ?? 0 })}
            onBlur={onCommit}
          />
        </Row>
        <Row label="Z">
          <InputNumber
            size="small"
            value={component.z}
            onChange={value => onChange({ z: value ?? 1 })}
            onBlur={onCommit}
          />
        </Row>
      </div>
      <Row label={t('screen.field.locked')}>
        <Switch
          size="small"
          checked={Boolean(component.locked)}
          onChange={value => {
            onChange({ locked: value });
            onCommit();
          }}
        />
      </Row>
      <Row label={t('screen.field.visible')}>
        <Switch
          size="small"
          checked={component.visible !== false}
          onChange={value => {
            onChange({ visible: value });
            onCommit();
          }}
        />
      </Row>

      {component.type === 'chart' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.data')}
          </Divider>
          <Row label={t('screen.field.chart_bind')}>
            <Select
              size="small"
              allowClear
              showSearch
              loading={chartsLoading}
              placeholder={t('screen.field.chart_bind_hint')}
              value={component.chart?.id}
              filterOption={false}
              onSearch={onSearchCharts}
              onChange={(value: number | undefined) => {
                if (!value) {
                  onChange({ chart: null });
                  onCommit();
                  return;
                }
                const chart = charts.find(item => item.id === value);
                if (!chart) return;
                onChange({
                  dataset: null,
                  chart: {
                    id: chart.id,
                    name: chart.slice_name,
                    vizType: chart.viz_type,
                    datasourceId: chart.datasource_id,
                    formData:
                      (typeof chart.params === 'string'
                        ? safeParse(chart.params)
                        : chart.params) ?? {},
                  },
                });
                onCommit();
              }}
              options={charts.map(chart => ({
                value: chart.id,
                label: `${chart.slice_name} · ${chart.viz_type}`,
              }))}
            />
          </Row>
          <DatasetBindingSection
            component={component}
            kind="chart"
            onChange={onChange}
            onCommit={onCommit}
          />
          <Row label={t('screen.field.title')}>
            <Input
              size="small"
              value={prop.str(p, 'title')}
              onChange={e => setProp('title', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          {component.chart?.id && (
            <div className="scr-inspector__note">
              {t('screen.field.bound_to')} #{component.chart.id} {component.chart.name}
            </div>
          )}
          {!dataBound && (
            <>
              <Row label={t('screen.field.kind')}>
                <Select
                  size="small"
                  value={prop.str(p, 'kind', 'bar')}
                  onChange={value => {
                    setProp('kind', value);
                    onCommit();
                  }}
                  options={[
                    'bar', 'hbar', 'line', 'area', 'pie', 'ring', 'radar',
                    'gauge', 'map', 'funnel', 'scatter', 'heatmap',
                  ].map(value => ({ value, label: value }))}
                />
              </Row>
              <Row label={t('screen.field.categories')}>
                <Input
                  size="small"
                  value={prop.arr<string>(p, 'categories').join(',')}
                  onChange={e => setProp('categories', parseStringList(e.target.value))}
                  onBlur={onCommit}
                />
              </Row>
              <Row label={t('screen.field.series')}>
                <TextArea
                  autoSize={{ minRows: 3, maxRows: 8 }}
                  value={prop
                    .arr<{ name?: string; data?: number[] }>(p, 'series')
                    .map(item => `${item.name ?? ''}:${(item.data ?? []).join(',')}`)
                    .join('\n')}
                  onChange={e =>
                    setProp(
                      'series',
                      e.target.value
                        .split('\n')
                        .map(line => {
                          const [name, data] = line.split(':');
                          return { name: name?.trim() || '数值', data: parseNumberList(data ?? '') };
                        })
                        .filter(item => item.data.length),
                    )
                  }
                  onBlur={onCommit}
                />
              </Row>
              <Row label={t('screen.field.legend')}>
                <Switch
                  size="small"
                  checked={prop.bool(p, 'legend')}
                  onChange={value => {
                    setProp('legend', value);
                    onCommit();
                  }}
                />
              </Row>
              <Row label={t('screen.field.unit')}>
                <Input
                  size="small"
                  value={prop.str(p, 'unit')}
                  onChange={e => setProp('unit', e.target.value)}
                  onBlur={onCommit}
                />
              </Row>
            </>
          )}
          {color()}
        </>
      )}

      {component.type === 'kpi' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.data')}
          </Divider>
          <Row label={t('screen.field.label')}>
            <Input
              size="small"
              value={prop.str(p, 'label')}
              onChange={e => setProp('label', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <DatasetBindingSection
            component={component}
            kind="kpi"
            onChange={onChange}
            onCommit={onCommit}
          />
          {!dataBound && (
            <>
              <Row label={t('screen.field.value')}>
                <Input
                  size="small"
                  value={prop.str(p, 'value')}
                  onChange={e => setProp('value', e.target.value)}
                  onBlur={onCommit}
                />
              </Row>
              <Row label={t('screen.field.trend')}>
                <InputNumber
                  size="small"
                  step={0.1}
                  value={prop.num(p, 'trend')}
                  onChange={value => setProp('trend', value ?? 0)}
                  onBlur={onCommit}
                />
              </Row>
              <Row label={t('screen.field.spark')}>
                <Input
                  size="small"
                  value={prop.arr<number>(p, 'spark').join(',')}
                  onChange={e => setProp('spark', parseNumberList(e.target.value))}
                  onBlur={onCommit}
                />
              </Row>
            </>
          )}
          <Row label={t('screen.field.unit')}>
            <Input
              size="small"
              value={prop.str(p, 'unit')}
              onChange={e => setProp('unit', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'table' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.data')}
          </Divider>
          <Row label={t('screen.field.title')}>
            <Input
              size="small"
              value={prop.str(p, 'title')}
              onChange={e => setProp('title', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <DatasetBindingSection
            component={component}
            kind="table"
            onChange={onChange}
            onCommit={onCommit}
          />
          {!dataBound && (
            <>
              <Row label={t('screen.field.columns')}>
                <Input
                  size="small"
                  value={prop.arr<string>(p, 'columns').join(',')}
                  onChange={e => setProp('columns', parseStringList(e.target.value))}
                  onBlur={onCommit}
                />
              </Row>
              <Row label={t('screen.field.rows')}>
                <TextArea
                  autoSize={{ minRows: 4, maxRows: 10 }}
                  value={prop
                    .arr<string[]>(p, 'rows')
                    .map(row => row.join(' | '))
                    .join('\n')}
                  onChange={e =>
                    setProp(
                      'rows',
                      e.target.value
                        .split('\n')
                        .filter(Boolean)
                        .map(line => line.split('|').map(cell => cell.trim())),
                    )
                  }
                  onBlur={onCommit}
                />
              </Row>
            </>
          )}
          {color()}
        </>
      )}

      {component.type === 'list' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.data')}
          </Divider>
          <Row label={t('screen.field.title')}>
            <Input
              size="small"
              value={prop.str(p, 'title')}
              onChange={e => setProp('title', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <DatasetBindingSection
            component={component}
            kind="list"
            onChange={onChange}
            onCommit={onCommit}
          />
          {!dataBound && (
            <>
              <Row label={t('screen.field.items')}>
                <TextArea
                  autoSize={{ minRows: 4, maxRows: 12 }}
                  value={prop
                    .arr<{ label: string; value: number }>(p, 'items')
                    .map(item => `${item.label},${item.value}`)
                    .join('\n')}
                  onChange={e =>
                    setProp(
                      'items',
                      e.target.value
                        .split('\n')
                        .filter(Boolean)
                        .map(line => {
                          const [label, value] = line.split(',');
                          return { label: label?.trim() ?? '', value: Number(value) || 0 };
                        }),
                    )
                  }
                  onBlur={onCommit}
                />
              </Row>
            </>
          )}
          {color()}
        </>
      )}

      {component.type === 'progress' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.data')}
          </Divider>
          <Row label={t('screen.field.label')}>
            <Input
              size="small"
              value={prop.str(p, 'label')}
              onChange={e => setProp('label', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <DatasetBindingSection
            component={component}
            kind="progress"
            onChange={onChange}
            onCommit={onCommit}
          />
          {!dataBound && (
            <Row label={t('screen.field.value')}>
              <InputNumber
                size="small"
                value={prop.num(p, 'value', 68)}
                onChange={value => setProp('value', value ?? 0)}
                onBlur={onCommit}
              />
            </Row>
          )}
          <Row label={t('screen.field.max')}>
            <InputNumber
              size="small"
              value={prop.num(p, 'max', 100)}
              onChange={value => setProp('max', value ?? 100)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'text' && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.content')}
          </Divider>
          <Row label={t('screen.field.text')}>
            <TextArea
              autoSize={{ minRows: 2, maxRows: 6 }}
              value={prop.str(p, 'text')}
              onChange={e => setProp('text', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.font_size')}>
            <InputNumber
              size="small"
              min={8}
              max={200}
              value={prop.num(p, 'fontSize', 24)}
              onChange={value => setProp('fontSize', value ?? 24)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.align')}>
            <Select
              size="small"
              value={prop.str(p, 'align', 'left')}
              onChange={value => {
                setProp('align', value);
                onCommit();
              }}
              options={[
                { value: 'left', label: t('screen.align.left') },
                { value: 'center', label: t('screen.align.center') },
                { value: 'right', label: t('screen.align.right') },
              ]}
            />
          </Row>
          <Row label={t('screen.field.bold')}>
            <Switch
              size="small"
              checked={prop.bool(p, 'bold')}
              onChange={value => {
                setProp('bold', value);
                onCommit();
              }}
            />
          </Row>
          <Row label={t('screen.field.letter_spacing')}>
            <InputNumber
              size="small"
              value={prop.num(p, 'letterSpacing')}
              onChange={value => setProp('letterSpacing', value ?? 0)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'marquee' && (
        <>
          <Row label={t('screen.field.text')}>
            <TextArea
              autoSize={{ minRows: 2, maxRows: 4 }}
              value={prop.str(p, 'text')}
              onChange={e => setProp('text', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.speed')}>
            <InputNumber
              size="small"
              min={5}
              max={200}
              value={prop.num(p, 'speed', 60)}
              onChange={value => setProp('speed', value ?? 60)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.font_size')}>
            <InputNumber
              size="small"
              value={prop.num(p, 'fontSize', 14)}
              onChange={value => setProp('fontSize', value ?? 14)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'clock' && (
        <>
          <Row label={t('screen.field.format')}>
            <Select
              size="small"
              value={prop.str(p, 'format', 'datetime')}
              onChange={value => {
                setProp('format', value);
                onCommit();
              }}
              options={[
                { value: 'datetime', label: t('screen.clock.datetime') },
                { value: 'date', label: t('screen.clock.date') },
                { value: 'time', label: t('screen.clock.time') },
              ]}
            />
          </Row>
          <Row label={t('screen.field.font_size')}>
            <InputNumber
              size="small"
              value={prop.num(p, 'fontSize', 20)}
              onChange={value => setProp('fontSize', value ?? 20)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {(component.type === 'image' || component.type === 'video' || component.type === 'iframe') && (
        <>
          <Divider orientation="left" plain>
            {t('screen.field.source')}
          </Divider>
          <Row label="URL">
            <Input
              size="small"
              value={prop.str(p, 'src')}
              placeholder="https://"
              onChange={e => setProp('src', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          {component.type === 'image' && (
            <>
              <Row label={t('screen.field.fit')}>
                <Select
                  size="small"
                  value={prop.str(p, 'fit', 'cover')}
                  onChange={value => {
                    setProp('fit', value);
                    onCommit();
                  }}
                  options={['cover', 'contain', 'fill', 'none'].map(value => ({
                    value,
                    label: value,
                  }))}
                />
              </Row>
              <Row label={t('screen.field.radius')}>
                <InputNumber
                  size="small"
                  min={0}
                  value={prop.num(p, 'radius', 4)}
                  onChange={value => setProp('radius', value ?? 0)}
                  onBlur={onCommit}
                />
              </Row>
            </>
          )}
          {component.type === 'video' && (
            <>
              <Row label={t('screen.field.autoplay')}>
                <Switch
                  size="small"
                  checked={prop.bool(p, 'autoplay', true)}
                  onChange={value => {
                    setProp('autoplay', value);
                    onCommit();
                  }}
                />
              </Row>
              <Row label={t('screen.field.loop')}>
                <Switch
                  size="small"
                  checked={prop.bool(p, 'loop', true)}
                  onChange={value => {
                    setProp('loop', value);
                    onCommit();
                  }}
                />
              </Row>
              <Row label={t('screen.field.controls')}>
                <Switch
                  size="small"
                  checked={prop.bool(p, 'controls')}
                  onChange={value => {
                    setProp('controls', value);
                    onCommit();
                  }}
                />
              </Row>
            </>
          )}
        </>
      )}

      {component.type === 'border' && (
        <>
          <Row label={t('screen.field.variant')}>
            <Select
              size="small"
              value={prop.str(p, 'variant', 'tech-1')}
              onChange={value => {
                setProp('variant', value);
                onCommit();
              }}
              options={['tech-1', 'tech-2', 'tech-3', 'tech-4', 'tech-5'].map(value => ({
                value,
                label: value,
              }))}
            />
          </Row>
          <Row label={t('screen.field.title')}>
            <Input
              size="small"
              value={prop.str(p, 'title')}
              onChange={e => setProp('title', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'decoration' && (
        <>
          <Row label={t('screen.field.variant')}>
            <Select
              size="small"
              value={prop.str(p, 'variant', 'grid')}
              onChange={value => {
                setProp('variant', value);
                onCommit();
              }}
              options={['grid', 'glow', 'line', 'corner'].map(value => ({ value, label: value }))}
            />
          </Row>
          <Row label={t('screen.field.opacity')}>
            <InputNumber
              size="small"
              min={0}
              max={1}
              step={0.05}
              value={prop.num(p, 'opacity', 0.1)}
              onChange={value => setProp('opacity', value ?? 0.1)}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}

      {component.type === 'shape' && (
        <>
          <Row label={t('screen.field.shape')}>
            <Select
              size="small"
              value={prop.str(p, 'shape', 'rect')}
              onChange={value => {
                setProp('shape', value);
                onCommit();
              }}
              options={['rect', 'circle', 'line'].map(value => ({ value, label: value }))}
            />
          </Row>
          <Row label={t('screen.field.radius')}>
            <InputNumber
              size="small"
              min={0}
              value={prop.num(p, 'radius')}
              onChange={value => setProp('radius', value ?? 0)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.opacity')}>
            <InputNumber
              size="small"
              min={0}
              max={1}
              step={0.05}
              value={prop.num(p, 'opacity', 1)}
              onChange={value => setProp('opacity', value ?? 1)}
              onBlur={onCommit}
            />
          </Row>
          <ColorRow
            label={t('screen.field.fill')}
            value={prop.str(p, 'fill', defaultTheme().primary)}
            onChange={value => setProp('fill', value)}
          />
        </>
      )}

      {component.type === 'richtext' && (
        <>
          <Divider orientation="left" plain>
            HTML
          </Divider>
          <TextArea
            autoSize={{ minRows: 6, maxRows: 14 }}
            value={prop.str(p, 'html')}
            onChange={e => setProp('html', e.target.value)}
            onBlur={onCommit}
          />
          {color()}
        </>
      )}

      {component.type === 'filter' && (
        <>
          <Row label={t('screen.field.label')}>
            <Input
              size="small"
              value={prop.str(p, 'label')}
              onChange={e => setProp('label', e.target.value)}
              onBlur={onCommit}
            />
          </Row>
          <Row label={t('screen.field.options')}>
            <Input
              size="small"
              value={prop.arr<string>(p, 'options').join(',')}
              onChange={e => setProp('options', parseStringList(e.target.value))}
              onBlur={onCommit}
            />
          </Row>
          {color()}
        </>
      )}
    </div>
  );
}

function safeParse(raw: string): Record<string, unknown> | null {
  try {
    const parsed: unknown = JSON.parse(raw);
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}
