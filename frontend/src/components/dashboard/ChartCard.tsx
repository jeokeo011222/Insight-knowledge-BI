import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Dropdown, Popconfirm, Space, Spin, Tag, Tooltip, message } from 'antd';
import type { MenuProps } from 'antd';
import {
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  ExpandOutlined,
  RobotOutlined,
} from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { ChartDataResponse, DashboardChart, QueryResultItem } from '../../api/types';
import ChartPreview from '../charts/ChartPreview';
import Loading from '../common/Loading';
import AgentPanel from '../ai/AgentPanel';
import { useI18n } from '../../i18n';
import {
  buildChartQueryContext,
  chartFormData,
  hasQueryableFormData,
  masksSignature,
  mergeDataMasks,
} from '../../features/dashboard/dataMask';
import type { ActiveDataMask } from '../../features/dashboard/dataMask';
import { appliesToChart, crossFilterFromClick } from '../../features/dashboard/crossFilter';
import type { CrossFilter } from '../../features/dashboard/crossFilter';
import {
  applyDrill,
  buildJumpUrl,
  drillLevels,
  jumpConfig,
  nextDrillLevel,
} from '../../features/dashboard/interaction';
import type { DrillStep } from '../../features/dashboard/interaction';
import { exportCsv, exportElementPng, hasExportableChart } from '../../features/dashboard/export';

export interface ChartCardProps {
  chart: DashboardChart;
  /** Card height in px, derived from the layout node. */
  height: number;
  masks: ActiveDataMask[];
  refreshKey: number;
  editMode: boolean;
  onRemove?: () => void;
  /** Clicking a category value links every other tile on the same dataset. */
  onCrossFilter?: (filter: CrossFilter) => void;
  /** Public share / embed view: no explore link, no agent, no copy-link. */
  publicView?: boolean;
  /** Share links can forbid exports; defaults to allowed. */
  canExport?: boolean;
}

/** One dashboard tile: re-queries its chart with the active native filters. */
export default function ChartCard({
  chart,
  height,
  masks,
  refreshKey,
  editMode,
  onRemove,
  onCrossFilter,
  publicView = false,
  canExport = true,
}: ChartCardProps) {
  const { t } = useI18n();
  const [result, setResult] = useState<QueryResultItem | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [exportable, setExportable] = useState(false);
  const bodyRef = useRef<HTMLDivElement | null>(null);

  const applicable = useMemo(
    () =>
      masks.filter(
        mask => mask.datasetIds.includes(chart.datasource_id) && appliesToChart(mask, chart.id),
      ),
    [masks, chart.datasource_id, chart.id],
  );
  const signature = useMemo(() => masksSignature(applicable), [applicable]);
  const formData = useMemo(() => chartFormData(chart), [chart]);
  const levels = useMemo(() => drillLevels(chart), [chart]);
  const jump = useMemo(() => jumpConfig(chart), [chart]);
  const [drill, setDrill] = useState<DrillStep[]>([]);
  const drillLevel = nextDrillLevel(levels, drill.length);
  // A drill path belongs to one tile: swapping the chart resets it.
  useEffect(() => {
    setDrill([]);
  }, [chart.id]);
  const drillSignature = drill.length ? JSON.stringify(drill) : '';

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const context = drill.length
      ? applyDrill(buildChartQueryContext(chart), drill, drillLevel)
      : buildChartQueryContext(chart);
    const request = hasQueryableFormData(chart)
      ? client.post<ChartDataResponse>(
          endpoints.chartData,
          mergeDataMasks(context, chart.datasource_id, applicable),
        )
      : client.get<ChartDataResponse>(endpoints.chartDataById(chart.id));

    request
      .then(response => {
        if (cancelled) return;
        if (response.errors?.length) {
          setError(
            response.errors.map(item => item.message).filter(Boolean).join('; ') ||
              t('explore.query_failed'),
          );
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
    // `signature` captures every active filter result; `masks` identity changes
    // on each render and must not retrigger the request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chart.id, refreshKey, signature, drillSignature]);

  // ECharts renders asynchronously; the PNG entry only appears once an SVG is
  // actually in the DOM.
  useEffect(() => {
    if (loading) return;
    const timer = window.setTimeout(
      () => setExportable(hasExportableChart(bodyRef.current)),
      60,
    );
    return () => window.clearTimeout(timer);
  }, [loading, result]);

  const handleCopyLink = async () => {
    const url = `${window.location.origin}/explore/?slice_id=${chart.id}`;
    try {
      await navigator.clipboard.writeText(url);
      message.success(t('dashboard.card.link_copied'));
    } catch {
      message.warning(t('dashboard.card.copy_failed', { url }));
    }
  };

  const handleClickData = (params: unknown) => {
    const filter = crossFilterFromClick(params, chart);
    // Drilling owns the click while the hierarchy has levels left: the tile
    // itself re-groups instead of filtering its neighbours.
    if (filter && drillLevel) {
      setDrill(current => [...current, { column: filter.column, value: filter.value }]);
      return;
    }
    if (filter && jump) {
      const url = buildJumpUrl(jump, filter.column, filter.value);
      if (url) {
        window.open(url, '_blank', 'noopener,noreferrer');
        return;
      }
    }
    if (!onCrossFilter) return;
    if (filter) onCrossFilter(filter);
  };

  const handleExport = async (kind: 'png' | 'csv') => {
    try {
      if (kind === 'csv') {
        if (!result) return;
        exportCsv(result, chart.slice_name);
        message.success(t('dashboard.export.csv_done'));
        return;
      }
      const done = await exportElementPng(bodyRef.current, chart.slice_name);
      if (done) message.success(t('dashboard.export.png_done'));
      else message.warning(t('dashboard.export.png_unavailable'));
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  const exportItems: MenuProps['items'] = [
    {
      key: 'png',
      label: t('dashboard.export.png'),
      disabled: !exportable,
    },
    {
      key: 'csv',
      label: t('dashboard.export.csv'),
      disabled: !result,
    },
  ];

  const barHeight = drill.length ? 28 : 0;
  const previewHeight = Math.max(120, height - 64 - barHeight);
  const clickable = Boolean(onCrossFilter) || Boolean(jump) || Boolean(drillLevel);

  return (
    <div className="chart-card dashboard-tile" style={{ height }}>
      <div className="chart-card__head dashboard-tile__drag">
        <span className="chart-card__title" title={chart.slice_name}>
          {chart.slice_name}
        </span>
        <Space size={4} className="chart-card__actions">
          {loading ? <Spin size="small" /> : null}
          <Tag bordered={false}>{chart.viz_type}</Tag>
          {canExport ? (
            <Dropdown
              trigger={['click']}
              menu={{
                items: exportItems,
                onClick: ({ key }) => void handleExport(key as 'png' | 'csv'),
              }}
            >
              <Tooltip title={t('dashboard.export.label')}>
                <Button
                  type="text"
                  size="small"
                  icon={<DownloadOutlined />}
                  aria-label={t('dashboard.export.label')}
                />
              </Tooltip>
            </Dropdown>
          ) : null}
          {publicView ? null : (
            <>
              <Tooltip title={t('ai.actions.explain_chart')}>
                <AgentPanel
                  variant="inline"
                  icon={<RobotOutlined />}
                  context={{ scope: 'chart', chart_id: chart.id, dataset_id: chart.datasource_id }}
                  question={t('ai.preset.chart_insight', { name: chart.slice_name })}
                />
              </Tooltip>
              <Tooltip title={t('dashboard.card.in_explore')}>
                <Link to={`/explore/?slice_id=${chart.id}`}>
                  <ExpandOutlined />
                </Link>
              </Tooltip>
            </>
          )}
          {editMode ? (
            <>
              <Tooltip title={t('dashboard.card.copy_link')}>
                <Button
                  type="text"
                  size="small"
                  icon={<CopyOutlined />}
                  onClick={() => void handleCopyLink()}
                />
              </Tooltip>
              {onRemove ? (
                <Popconfirm
                  title={t('dashboard.card.remove')}
                  description={t('dashboard.card.remove_desc')}
                  okText={t('dashboard.card.remove')}
                  cancelText={t('common.actions.cancel')}
                  onConfirm={onRemove}
                >
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                </Popconfirm>
              ) : null}
            </>
          ) : null}
        </Space>
      </div>
      {drill.length ? (
        <div className="drill-bar">
          <Tag bordered={false} className="drill-bar__root">
            {levels[0]}
          </Tag>
          {drill.map((step, index) => (
            <Tag
              key={`${step.column}-${index}`}
              closable
              className="drill-bar__step"
              onClose={event => {
                event.preventDefault();
                setDrill(current => current.slice(0, index));
              }}
            >
              {step.column}: {String(step.value)}
            </Tag>
          ))}
          <Button type="link" size="small" onClick={() => setDrill([])}>
            {t('dashboard.drill.reset')}
          </Button>
          {drillLevel ? (
            <span className="cell-muted">→ {drillLevel}</span>
          ) : null}
        </div>
      ) : null}
      <div
        ref={bodyRef}
        className={`chart-card__body${clickable ? ' chart-card__body--linked' : ''}`}
        style={{ height: height - 44 - barHeight }}
      >
        {loading && !result ? <Loading height={previewHeight} tip={t('common.state.loading_chart')} /> : null}
        {!loading && error ? <div className="chart-card__error">{error}</div> : null}
        {error && loading ? <div className="chart-card__error">{error}</div> : null}
        {!error && result ? (
          <ChartPreview
            result={result}
            vizType={chart.viz_type}
            height={previewHeight}
            compact
            formData={formData}
            onClickData={clickable ? handleClickData : undefined}
          />
        ) : null}
        {!loading && !error && !result ? (
          <div className="cell-muted" style={{ padding: 16 }}>
            {t('dashboard.card.no_data')}
          </div>
        ) : null}
      </div>
    </div>
  );
}
