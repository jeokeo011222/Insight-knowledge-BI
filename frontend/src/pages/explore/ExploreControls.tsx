import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Card, Divider, Input, InputNumber, Select, Tag, Tooltip } from 'antd';
import { RobotOutlined } from '@ant-design/icons';
import type { AnnotationLayer, Dataset } from '../../api/types';
import { hasTranslation, useI18n } from '../../i18n';
import type { TranslationKey } from '../../i18n';
import { VIZ_CATEGORIES, VIZ_TYPES } from '../../components/charts/vizTypes';
import VizTypePicker from '../../components/charts/VizTypePicker';
import AgentPanel from '../../components/ai/AgentPanel';

/** Time grains offered in Explore, matching ISO-8601 durations. */
export const TIME_GRAINS = [
  { value: 'PT1H', key: 'explore.grain.hour' },
  { value: 'P1D', key: 'explore.grain.day' },
  { value: 'P1W', key: 'explore.grain.week' },
  { value: 'P1M', key: 'explore.grain.month' },
  { value: 'P3M', key: 'explore.grain.quarter' },
  { value: 'P1Y', key: 'explore.grain.year' },
] as const;

/** Time range presets, expressed as `<value> -> i18n key`. */
export const TIME_RANGES = [
  { value: '7', key: 'explore.range.7d' },
  { value: '30', key: 'explore.range.30d' },
  { value: '90', key: 'explore.range.90d' },
  { value: '365', key: 'explore.range.12m' },
  { value: 'all', key: 'explore.range.all' },
] as const;

export interface ExploreControlsProps {
  datasets: Dataset[];
  datasetId: number | null;
  dataset: Dataset | null;
  vizType: string;
  metrics: string[];
  groupby: string[];
  timeColumn: string | null;
  timeRange: string;
  timeGrain: string;
  rowLimit: number;
  /** Drill hierarchy: clicking a value moves the chart one level deeper. */
  drillLevels: string[];
  /** Annotation layers overlaid on the time axis. */
  annotationLayers: number[];
  /** Every layer the user may pick (empty when they cannot read any). */
  annotationLayerOptions: AnnotationLayer[];
  /** Dashboards offered as jump targets. */
  dashboards: Array<{ id: number; name: string }>;
  jumpType: 'none' | 'url' | 'dashboard';
  jumpUrl: string;
  jumpDashboardId: number | null;
  jumpParam: string;
  onDatasetChange: (id: number) => void;
  onVizTypeChange: (value: string) => void;
  onMetricsChange: (value: string[]) => void;
  onGroupbyChange: (value: string[]) => void;
  onTimeColumnChange: (value: string | null) => void;
  onTimeRangeChange: (value: string) => void;
  onTimeGrainChange: (value: string) => void;
  onRowLimitChange: (value: number) => void;
  onDrillLevelsChange: (value: string[]) => void;
  onAnnotationLayersChange: (value: number[]) => void;
  onJumpTypeChange: (value: 'none' | 'url' | 'dashboard') => void;
  onJumpUrlChange: (value: string) => void;
  onJumpDashboardChange: (value: number | null) => void;
  onJumpParamChange: (value: string) => void;
}

/** Left hand query configuration panel of Explore. */
export default function ExploreControls({
  datasets,
  datasetId,
  dataset,
  vizType,
  metrics,
  groupby,
  timeColumn,
  timeRange,
  timeGrain,
  rowLimit,
  drillLevels,
  annotationLayers,
  annotationLayerOptions,
  dashboards,
  jumpType,
  jumpUrl,
  jumpDashboardId,
  jumpParam,
  onDatasetChange,
  onVizTypeChange,
  onMetricsChange,
  onGroupbyChange,
  onTimeColumnChange,
  onTimeRangeChange,
  onTimeGrainChange,
  onRowLimitChange,
  onDrillLevelsChange,
  onAnnotationLayersChange,
  onJumpTypeChange,
  onJumpUrlChange,
  onJumpDashboardChange,
  onJumpParamChange,
}: ExploreControlsProps) {
  const { t } = useI18n();
  const groupbyOptions = useMemo(
    () =>
      (dataset?.columns ?? [])
        .filter(column => column.groupby !== false && column.is_active !== false)
        .map(column => ({ value: column.column_name, label: column.column_name })),
    [dataset],
  );

  const metricOptions = useMemo(
    () =>
      (dataset?.metrics ?? []).map(metric => ({
        value: metric.metric_name,
        label: metric.verbose_name || metric.metric_name,
      })),
    [dataset],
  );

  // Grouped by category, exactly like the upstream viz picker. Translation
  // falls back to the renderer's own English label when no zh key exists.
  const vizOptions = useMemo(
    () =>
      VIZ_CATEGORIES.map(category => ({
        label: category,
        options: VIZ_TYPES.filter(option => option.category === category).map(option => {
          const key = `chart.type.${option.value}`;
          return {
            value: option.value,
            label: hasTranslation(key) ? t(key as TranslationKey) : option.label,
          };
        }),
      })),
    [t],
  );

  const timeColumnOptions = useMemo(
    () =>
      (dataset?.columns ?? [])
        .filter(column => column.is_dttm)
        .map(column => ({ value: column.column_name, label: column.column_name })),
    [dataset],
  );

  return (
    <Card
      size="small"
      title={t('explore.controls.card_title')}
      className="zg-card"
      extra={
        <Tooltip title={t('ai.actions.design_chart')}>
          <AgentPanel
            variant="inline"
            icon={<RobotOutlined />}
            context={{ scope: 'explore', dataset_id: datasetId ?? undefined }}
            question={t('ai.preset.chart_design')}
          />
        </Tooltip>
      }
    >
      <div className="tiny-label" style={{ marginBottom: 4 }}>
        Dataset
      </div>
      <Select
        showSearch
        optionFilterProp="label"
        style={{ width: '100%' }}
        placeholder={t('explore.controls.dataset_placeholder')}
        value={datasetId ?? undefined}
        onChange={onDatasetChange}
        options={datasets.map(item => ({
          value: item.id,
          label: `${item.table_name} · ${item.database?.database_name ?? ''}`,
        }))}
      />

      <Divider style={{ margin: '14px 0' }} />

      <div className="tiny-label" style={{ marginBottom: 4 }}>
        {t('chart.list.visual_type')}
      </div>
      <Select
        style={{ width: '100%' }}
        value={vizType}
        onChange={onVizTypeChange}
        options={vizOptions}
      />
      <VizTypePicker value={vizType} onChange={onVizTypeChange} />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.metrics')}
      </div>
      <Select
        mode="tags"
        style={{ width: '100%' }}
        placeholder={t('explore.controls.metrics_placeholder')}
        value={metrics}
        onChange={onMetricsChange}
        options={metricOptions}
        tokenSeparators={[',']}
      />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.dimensions')}
      </div>
      <Select
        mode="multiple"
        style={{ width: '100%' }}
        placeholder={t('explore.controls.dimensions_placeholder')}
        value={groupby}
        onChange={onGroupbyChange}
        options={groupbyOptions}
        allowClear
      />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.time_column')}
      </div>
      <Select
        style={{ width: '100%' }}
        placeholder={t('explore.controls.time_column_placeholder')}
        value={timeColumn ?? undefined}
        onChange={value => onTimeColumnChange(value ?? null)}
        options={timeColumnOptions}
        allowClear
      />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.time_range')}
      </div>
      <Select
        style={{ width: '100%' }}
        value={timeRange}
        onChange={onTimeRangeChange}
        options={TIME_RANGES.map(item => ({ value: item.value, label: t(item.key) }))}
      />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.time_grain')}
      </div>
      <Select
        style={{ width: '100%' }}
        value={timeGrain}
        onChange={onTimeGrainChange}
        options={TIME_GRAINS.map(item => ({ value: item.value, label: t(item.key) }))}
      />

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.annotation.title')}
      </div>
      <Select
        mode="multiple"
        allowClear
        style={{ width: '100%' }}
        placeholder={t('explore.annotation.placeholder')}
        value={annotationLayers}
        onChange={onAnnotationLayersChange}
        options={annotationLayerOptions.map(layer => ({ value: layer.id, label: layer.name }))}
      />
      {annotationLayerOptions.length ? (
        <div className="cell-muted" style={{ marginTop: 4, lineHeight: 1.6 }}>
          {t('explore.annotation.hint')}
        </div>
      ) : (
        <div className="cell-muted" style={{ marginTop: 4, lineHeight: 1.6 }}>
          {t('explore.annotation.empty')}{' '}
          <Link to="/annotation">{t('explore.annotation.empty_link')}</Link>
        </div>
      )}

      <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
        {t('explore.controls.row_limit')}
      </div>
      <InputNumber
        style={{ width: '100%' }}
        min={1}
        max={100000}
        value={rowLimit}
        onChange={value => onRowLimitChange(value ?? 10000)}
      />

      <Divider style={{ margin: '14px 0' }}>
        <span className="tiny-label">{t('explore.interaction.title')}</span>
      </Divider>
      <div className="explore-interaction">
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('explore.interaction.drill')}
        </div>
        <Select
          mode="multiple"
          allowClear
          placeholder={t('explore.interaction.drill_placeholder')}
          value={drillLevels}
          onChange={onDrillLevelsChange}
          options={groupbyOptions}
        />
        <div className="cell-muted" style={{ marginTop: 4, lineHeight: 1.6 }}>
          {t('explore.interaction.drill_hint')}
        </div>

        <div className="tiny-label" style={{ margin: '12px 0 4px' }}>
          {t('explore.interaction.jump')}
        </div>
        <Select
          value={jumpType}
          onChange={value => onJumpTypeChange(value)}
          options={[
            { value: 'none', label: t('explore.interaction.jump_none') },
            { value: 'dashboard', label: t('explore.interaction.jump_dashboard') },
            { value: 'url', label: t('explore.interaction.jump_url') },
          ]}
        />
        {jumpType === 'dashboard' ? (
          <>
            <Select
              style={{ marginTop: 8 }}
              showSearch
              optionFilterProp="label"
              allowClear
              placeholder={t('explore.interaction.jump_target')}
              value={jumpDashboardId ?? undefined}
              onChange={value => onJumpDashboardChange(value ?? null)}
              options={dashboards.map(item => ({ value: item.id, label: item.name }))}
            />
            <Input
              style={{ marginTop: 8 }}
              placeholder={t('explore.interaction.jump_param')}
              value={jumpParam}
              onChange={event => onJumpParamChange(event.target.value)}
            />
          </>
        ) : null}
        {jumpType === 'url' ? (
          <Input
            style={{ marginTop: 8 }}
            placeholder="https://example.com/detail?id={value}"
            value={jumpUrl}
            onChange={event => onJumpUrlChange(event.target.value)}
          />
        ) : null}
        {jumpType !== 'none' ? (
          <div className="cell-muted" style={{ marginTop: 4, lineHeight: 1.6 }}>
            {t('explore.interaction.jump_hint')}
          </div>
        ) : null}
      </div>

      {dataset ? (
        <>
          <Divider style={{ margin: '14px 0' }} />
          <div className="cell-muted" style={{ lineHeight: 1.9 }}>
            <div>
              <Tag bordered={false}>{dataset.columns?.length ?? 0}</Tag> columns
            </div>
            <div>
              <Tag bordered={false}>{dataset.metrics?.length ?? 0}</Tag> metrics
            </div>
            <div>
              {t('explore.controls.database', { name: dataset.database?.database_name ?? '—' })}
            </div>
          </div>
        </>
      ) : null}

      {/* Drawer host shared by the header trigger. */}
      <AgentPanel variant="host" context={{ scope: 'explore' }} />
    </Card>
  );
}
