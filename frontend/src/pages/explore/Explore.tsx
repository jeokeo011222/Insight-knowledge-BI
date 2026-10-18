import { useCallback, useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  Alert,
  Button,
  Card,
  Col,
  Divider,
  Empty,
  Form,
  Input,
  Modal,
  Row,
  Space,
  Typography,
  message,
} from 'antd';
import { PlayCircleOutlined, SaveOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { client, endpoints, errorMessage } from '../../api/client';
import type {
  AnnotationLayer,
  Chart,
  ChartDataResponse,
  Dashboard,
  Dataset,
  JsonObject,
  Paginated,
  QueryContext,
  QueryResultItem,
} from '../../api/types';
import PageHeader from '../../components/common/PageHeader';
import ChartPreview from '../../components/charts/ChartPreview';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import ExploreControls from './ExploreControls';

function parseParams(raw: JsonObject | string | null | undefined): JsonObject {
  if (!raw) return {};
  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw) as unknown;
      return parsed && typeof parsed === 'object' ? (parsed as JsonObject) : {};
    } catch {
      return {};
    }
  }
  return raw;
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

export default function Explore() {
  const [searchParams] = useSearchParams();
  const sliceId = searchParams.get('slice_id');
  const { can } = usePermission();
  const { t } = useI18n();

  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [datasetId, setDatasetId] = useState<number | null>(null);
  const [dataset, setDataset] = useState<Dataset | null>(null);
  // `viz_type` lets the viz gallery (chart list / dashboard drawer) hand a
  // chosen chart type over to a brand new Explore session.
  const [vizType, setVizType] = useState(() => searchParams.get('viz_type') || 'table');
  const [metrics, setMetrics] = useState<string[]>(['count']);
  const [groupby, setGroupby] = useState<string[]>([]);
  const [timeColumn, setTimeColumn] = useState<string | null>(null);
  const [timeRange, setTimeRange] = useState('30');
  const [timeGrain, setTimeGrain] = useState('P1D');
  const [rowLimit, setRowLimit] = useState(10000);
  // Interaction config (stored in params, consumed by dashboard tiles).
  const [drillLevels, setDrillLevels] = useState<string[]>([]);
  // Annotation layers overlaid on the time axis; stored with the chart so the
  // dashboard tile requests the same events.
  const [annotationLayers, setAnnotationLayers] = useState<number[]>([]);
  const [layerOptions, setLayerOptions] = useState<AnnotationLayer[]>([]);
  const [jumpType, setJumpType] = useState<'none' | 'url' | 'dashboard'>('none');
  const [jumpUrl, setJumpUrl] = useState('');
  const [jumpDashboardId, setJumpDashboardId] = useState<number | null>(null);
  const [jumpParam, setJumpParam] = useState('');
  const [dashboards, setDashboards] = useState<Array<{ id: number; name: string }>>([]);
  const [result, setResult] = useState<QueryResultItem | null>(null);
  const [queryError, setQueryError] = useState<string | null>(null);
  const [running, setRunning] = useState(false);
  const [chartId, setChartId] = useState<number | null>(null);
  const [chartName, setChartName] = useState('');
  const [saveOpen, setSaveOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form] = Form.useForm<{ slice_name: string; description?: string }>();

  useEffect(() => {
    client
      .get<Paginated<Dataset>>(endpoints.datasets, { page: 1, page_size: 100 })
      .then(response => setDatasets(response.result ?? []))
      .catch(error => message.error(errorMessage(error)));
  }, []);

  // Jump targets: a chart can jump to any dashboard the user can open.
  useEffect(() => {
    client
      .get<Paginated<Dashboard>>(endpoints.dashboards, { page: 1, page_size: 100 })
      .then(response =>
        setDashboards(
          (response.result ?? []).map(item => ({
            id: item.id,
            name: item.dashboard_title || item.slug || `#${item.id}`,
          })),
        ),
      )
      .catch(() => setDashboards([]));
  }, []);

  // Annotation layers are optional: a user without annotation rights still gets
  // a working editor, just an empty picker.
  useEffect(() => {
    client
      .get<Paginated<AnnotationLayer>>(endpoints.annotationLayers, { page: 1, page_size: 100 })
      .then(response => setLayerOptions(response.result ?? []))
      .catch(() => setLayerOptions([]));
  }, []);

  useEffect(() => {
    if (!sliceId) return;
    let cancelled = false;
    client
      .get<{ result: Chart }>(endpoints.chart(sliceId))
      .then(response => {
        if (cancelled) return;
        const chart = response.result;
        const params = parseParams(chart.params);
        setChartId(chart.id);
        setChartName(chart.slice_name);
        setDatasetId(chart.datasource_id);
        setVizType(chart.viz_type || 'table');
        const chartMetrics = asStringArray(params.metrics);
        if (chartMetrics.length) setMetrics(chartMetrics);
        const chartGroupby = asStringArray(params.groupby);
        if (chartGroupby.length) setGroupby(chartGroupby);
        if (typeof params.row_limit === 'number') setRowLimit(params.row_limit);
        if (typeof params.granularity_sqla === 'string') setTimeColumn(params.granularity_sqla);
        if (typeof params.time_grain_sqla === 'string') setTimeGrain(params.time_grain_sqla);
        if (typeof params.time_range === 'string') {
          // Absolute ranges ("2018-01-01 : 2019-01-01") must survive the round
          // trip; only preset ranges ("Last 30 days") collapse to a day count.
          if (params.time_range.includes(':')) {
            setTimeRange(params.time_range);
          } else {
            const days = params.time_range.replace(/\D/g, '');
            setTimeRange(days ? days : 'all');
          }
        }
        setDrillLevels(asStringArray(params.drill_levels));
        setAnnotationLayers(
          Array.isArray(params.annotation_layers)
            ? params.annotation_layers
                .map(item => Number(item))
                .filter(id => Number.isFinite(id) && id > 0)
            : [],
        );
        const jump = params.jump_config;
        if (jump && typeof jump === 'object') {
          const record = jump as Record<string, unknown>;
          if (record.type === 'url' && typeof record.url === 'string') {
            setJumpType('url');
            setJumpUrl(record.url);
          } else if (record.type === 'dashboard') {
            setJumpType('dashboard');
            const target = Number(record.dashboard_id ?? record.dashboardId);
            setJumpDashboardId(Number.isFinite(target) && target > 0 ? target : null);
            setJumpParam(typeof record.param === 'string' ? record.param : '');
          }
        }
      })
      .catch(error => message.error(errorMessage(error)));
    return () => {
      cancelled = true;
    };
  }, [sliceId]);

  useEffect(() => {
    if (!datasetId) {
      setDataset(null);
      return;
    }
    let cancelled = false;
    client
      .get<{ result: Dataset }>(endpoints.dataset(datasetId))
      .then(response => {
        if (cancelled) return;
        const loaded = response.result;
        setDataset(loaded);
        const preferred =
          loaded.main_dttm_col ||
          loaded.columns?.find(column => column.is_dttm)?.column_name ||
          null;
        setTimeColumn(current => current ?? preferred);
      })
      .catch(error => {
        if (!cancelled) message.error(errorMessage(error));
      });
    return () => {
      cancelled = true;
    };
  }, [datasetId]);


  const handleDatasetChange = (value: number) => {
    setDatasetId(value);
    setMetrics(['count']);
    setGroupby([]);
    setTimeColumn(null);
    setResult(null);
    setQueryError(null);
    if (!sliceId) {
      setChartId(null);
      setChartName('');
    }
  };

  const buildFormData = useCallback((): JsonObject => {
    if (!dataset) return {};
    const data: JsonObject = {
      datasource: `${dataset.id}__table`,
      viz_type: vizType,
      metrics,
      groupby,
      granularity_sqla: timeColumn,
      time_range:
        timeRange === 'all'
          ? 'No filter'
          : timeRange.includes(':')
            ? timeRange
            : `Last ${timeRange} days`,
      row_limit: rowLimit,
      time_grain_sqla: timeGrain,
    };
    // Interaction config only ships when it is actually configured, so charts
    // saved before this feature keep byte-identical params.
    if (drillLevels.length) data.drill_levels = drillLevels;
    if (annotationLayers.length) data.annotation_layers = annotationLayers;
    if (jumpType === 'url' && jumpUrl.trim()) {
      data.jump_config = { type: 'url', url: jumpUrl.trim() };
    } else if (jumpType === 'dashboard' && jumpDashboardId) {
      data.jump_config = {
        type: 'dashboard',
        dashboard_id: jumpDashboardId,
        param: jumpParam.trim() || undefined,
      };
    }
    return data;
  }, [
    dataset,
    vizType,
    metrics,
    groupby,
    timeColumn,
    timeRange,
    rowLimit,
    timeGrain,
    drillLevels,
    annotationLayers,
    jumpType,
    jumpUrl,
    jumpDashboardId,
    jumpParam,
  ]);

  const runQuery = useCallback(async () => {
    if (!dataset) {
      message.warning(t('explore.need_dataset'));
      return;
    }
    if (!metrics.length) {
      message.warning(t('explore.need_metric'));
      return;
    }
    const days = Number(timeRange);
    const absolute = timeRange.includes(':')
      ? timeRange.split(':').map(part => part.trim())
      : null;
    const fromDttm = absolute
      ? absolute[0] || null
      : timeRange !== 'all' && Number.isFinite(days)
        ? dayjs().subtract(days, 'day').format('YYYY-MM-DD')
        : null;
    const toDttm = absolute ? absolute[1] || null : null;
    const context: QueryContext = {
      datasource: { id: dataset.id, type: 'table' },
      force: false,
      queries: [
        {
          database_id: dataset.database.id,
          schema: dataset.schema ?? undefined,
          sql: dataset.sql ?? '',
          metrics,
          groupby,
          columns: [],
          from_dttm: fromDttm,
          to_dttm: toDttm,
          granularity: timeColumn ?? undefined,
          filters: [],
          extras: { time_grain_sqla: timeGrain, where: '', having: '' },
          row_limit: rowLimit,
          row_offset: 0,
          orderby: metrics.map(metric => [metric, false] as [string, boolean]),
          post_processing: [],
          annotation_layers: annotationLayers,
          is_timeseries:
            vizType.includes('timeseries') ||
            vizType === 'line' ||
            vizType === 'area' ||
            vizType === 'bar',
        },
      ],
      form_data: buildFormData(),
      result_type: 'full',
      result_format: 'json',
    };

    setRunning(true);
    setQueryError(null);
    try {
      const response = await client.post<ChartDataResponse>(endpoints.chartData, context);
      if (response.errors?.length) {
        setQueryError(response.errors.map(item => item.message).filter(Boolean).join('; '));
        setResult(null);
      } else {
        setResult(response.result?.[0] ?? null);
        ranOnce.current = true;
      }
    } catch (error) {
      setQueryError(errorMessage(error));
      setResult(null);
    } finally {
      setRunning(false);
    }
  }, [
    dataset,
    metrics,
    groupby,
    timeColumn,
    timeRange,
    timeGrain,
    rowLimit,
    vizType,
    annotationLayers,
    buildFormData,
  ]);

  // Selecting annotation layers changes what the chart data must carry, so the
  // preview re-runs once a query has already been executed. Nothing fires
  // before the first manual run: picking layers must not query on its own.
  const ranOnce = useRef(false);
  const layerKey = useRef<string | null>(null);
  useEffect(() => {
    const key = annotationLayers.join(',');
    if (layerKey.current === null) {
      layerKey.current = key;
      return;
    }
    if (layerKey.current === key) return;
    layerKey.current = key;
    if (!ranOnce.current || running || !dataset) return;
    void runQuery();
  }, [annotationLayers, dataset, running, runQuery]);

  // Opening Explore from a saved chart (slice_id) runs the query once so the
  // preview matches what the dashboard shows.
  const autoRan = useRef<string | null>(null);
  useEffect(() => {
    if (!sliceId || !dataset) return;
    if (autoRan.current === sliceId) return;
    autoRan.current = sliceId;
    void runQuery();
  }, [sliceId, dataset, runQuery]);

  const openSave = () => {
    form.setFieldsValue({ slice_name: chartName || '' });
    setSaveOpen(true);
  };

  const handleSave = async () => {
    if (!dataset) {
      message.warning(t('explore.need_dataset'));
      return;
    }
    try {
      const values = await form.validateFields();
      setSaving(true);
      const payload = {
        slice_name: values.slice_name,
        viz_type: vizType,
        datasource_id: dataset.id,
        datasource_type: 'table',
        description: values.description,
        params: buildFormData(),
      };
      if (chartId) {
        await client.put(endpoints.chart(chartId), payload);
        message.success(t('chart.updated'));
      } else {
        const created = await client.post<{ result: Chart }>(endpoints.charts, payload);
        message.success(t('chart.saved'));
        if (created.result?.id) {
          setChartId(created.result.id);
          setChartName(created.result.slice_name);
        }
      }
      setSaveOpen(false);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const sqlText = result?.sql ?? '';

  return (
    <div>
      <PageHeader
        title={chartId ? `${t('chart.edit')}：${chartName}` : t('chart.create')}
        subtitle={t('explore.subtitle')}
        extra={
          <Space>
            <Link to="/chart/list/">
              <Button>{t('common.actions.back_to_list')}</Button>
            </Link>
            <Button
              icon={<PlayCircleOutlined />}
              onClick={() => void runQuery()}
              loading={running}
              type="primary"
            >
              {t('explore.run_query')}
            </Button>
            {can('can_write', 'Chart') ? (
              <Button icon={<SaveOutlined />} onClick={openSave} disabled={!dataset}>
                {t('common.actions.save')}
              </Button>
            ) : null}
          </Space>
        }
      />

      <Row gutter={16}>
        <Col xs={24} lg={7} xl={6}>
          <ExploreControls
            datasets={datasets}
            datasetId={datasetId}
            dataset={dataset}
            vizType={vizType}
            metrics={metrics}
            groupby={groupby}
            timeColumn={timeColumn}
            timeRange={timeRange}
            timeGrain={timeGrain}
            rowLimit={rowLimit}
            drillLevels={drillLevels}
            annotationLayers={annotationLayers}
            annotationLayerOptions={layerOptions}
            dashboards={dashboards}
            jumpType={jumpType}
            jumpUrl={jumpUrl}
            jumpDashboardId={jumpDashboardId}
            jumpParam={jumpParam}
            onDatasetChange={handleDatasetChange}
            onVizTypeChange={setVizType}
            onMetricsChange={setMetrics}
            onGroupbyChange={setGroupby}
            onTimeColumnChange={setTimeColumn}
            onTimeRangeChange={setTimeRange}
            onTimeGrainChange={setTimeGrain}
            onRowLimitChange={setRowLimit}
            onDrillLevelsChange={setDrillLevels}
            onAnnotationLayersChange={setAnnotationLayers}
            onJumpTypeChange={setJumpType}
            onJumpUrlChange={setJumpUrl}
            onJumpDashboardChange={setJumpDashboardId}
            onJumpParamChange={setJumpParam}
          />
        </Col>

        <Col xs={24} lg={17} xl={18}>
          <Card
            size="small"
            title={t('explore.result_preview')}
            className="zg-card chart-preview"
            extra={
              result ? (
                <span className="cell-muted">
                  {result.rowcount} rows · {result.colnames?.length ?? 0} cols
                </span>
              ) : null
            }
          >
            {queryError ? (
              <Alert type="error" showIcon message={t('explore.query_failed')} description={queryError} />
            ) : null}

            {!queryError && !result ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t('explore.run_first')}
                style={{ padding: '48px 0' }}
              />
            ) : null}

            {result ? (
              <>
                <div className="chart-canvas">
                  <ChartPreview result={result} vizType={vizType} height={360} />
                </div>
                {sqlText ? (
                  <>
                    <Divider style={{ margin: '12px 0 8px' }} />
                    <Typography.Text className="tiny-label">Generated SQL</Typography.Text>
                    <pre className="mono-block" style={{ marginTop: 6 }}>
                      {sqlText}
                    </pre>
                  </>
                ) : null}
              </>
            ) : null}
          </Card>
        </Col>
      </Row>

      <Modal
        title={chartId ? t('chart.update') : t('chart.save')}
        open={saveOpen}
        onCancel={() => setSaveOpen(false)}
        onOk={() => void handleSave()}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={saving}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="slice_name"
            label={t('chart.form.name')}
            rules={[{ required: true, message: t('chart.form.name') }]}
          >
            <Input maxLength={120} placeholder={t('chart.form.name_placeholder')} />
          </Form.Item>
          <Form.Item name="description" label={t('common.label.description')}>
            <Input.TextArea rows={3} maxLength={400} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
