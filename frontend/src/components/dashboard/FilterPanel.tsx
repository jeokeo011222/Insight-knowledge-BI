import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Button, DatePicker, Empty, Input, InputNumber, Popconfirm, Select, Slider, Space, Tooltip, Typography,
} from 'antd';
import { DeleteOutlined, EditOutlined, LeftOutlined, PlusOutlined, RightOutlined } from '@ant-design/icons';
import dayjs from 'dayjs';
import { client, endpoints, errorMessage } from '../../api/client';
import type { ChartDataResponse, Dataset, NativeFilterConfig } from '../../api/types';
import {
  FILTER_TYPE_LABELS, TIME_PRESETS, buildDistinctValuesContext, defaultFilterValue,
  filterColumnName, isFilterValueEmpty, numericBounds,
} from '../../features/dashboard/filters';
import type { FilterValue, FilterValueMap } from '../../features/dashboard/filters';
import { useI18n } from '../../i18n';

export interface FilterPanelProps {
  filters: NativeFilterConfig[];
  values: FilterValueMap;
  datasets: Dataset[];
  collapsed: boolean;
  canEdit: boolean;
  editMode: boolean;
  onChange: (filterId: string, value: FilterValue) => void;
  onToggleCollapse: () => void;
  onAdd: () => void;
  onEdit: (filter: NativeFilterConfig) => void;
  onRemove: (filter: NativeFilterConfig) => void;
}

interface Candidates {
  options: string[];
  loading: boolean;
  error: string | null;
}

/** Loads DISTINCT values for a filter column through `POST /chart/data`. */
function useCandidates(
  datasetId: number | null,
  column: string,
  enabled: boolean,
): Candidates {
  const { t } = useI18n();
  const [options, setOptions] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!enabled || !datasetId || !column) {
      setOptions([]);
      setLoading(false);
      setError(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    client
      .post<ChartDataResponse>(
        endpoints.chartData,
        buildDistinctValuesContext(datasetId, column),
      )
      .then(response => {
        if (cancelled) return;
        if (response.errors?.length) {
          setError(
            response.errors.map(item => item.message).filter(Boolean).join('; ') ||
              t('dashboard.filter.load_candidates_failed'),
          );
          setOptions([]);
          return;
        }
        const rows = response.result?.[0]?.data ?? [];
        const values = rows
          .map(row => row[column])
          .filter(value => value !== null && value !== undefined)
          .map(value => String(value));
        setOptions(Array.from(new Set(values)));
      })
      .catch(err => {
        if (cancelled) return;
        setError(errorMessage(err));
        setOptions([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [datasetId, column, enabled, t]);

  return { options, loading, error };
}

interface ControlProps {
  config: NativeFilterConfig;
  value: FilterValue | undefined;
  dataset: Dataset | null;
  onChange: (value: FilterValue) => void;
}
function SelectControl({ config, value, dataset, onChange }: ControlProps) {
  const { t } = useI18n();
  const column = filterColumnName(config);
  const multi =
    config.filterType === 'filter_multi_select' ||
    Boolean(config.controlValues?.multiSelection);
  const { options, loading, error } = useCandidates(
    dataset?.id ?? null,
    column,
    Boolean(column),
  );
  const sorted = useMemo(() => {
    if (config.controlValues?.sortAscending === false) {
      return [...options].sort((a, b) => b.localeCompare(a));
    }
    return options;
  }, [options, config.controlValues?.sortAscending]);

  const initialised = useRef(false);
  useEffect(() => {
    if (initialised.current || loading || !sorted.length) return;
    initialised.current = true;
    if (!isFilterValueEmpty(value)) return;
    const fallback = defaultFilterValue(config, sorted);
    if (fallback !== undefined) onChange(fallback);
  }, [loading, sorted, value, config, onChange]);

  const current = multi
    ? Array.isArray(value)
      ? value.map(String)
      : typeof value === 'string' && value
        ? [value]
        : []
    : typeof value === 'string' || typeof value === 'number'
      ? String(value)
      : undefined;

  return (
    <>
      <Select
        showSearch
        allowClear
        loading={loading}
        status={error ? 'error' : undefined}
        mode={multi ? 'multiple' : undefined}
        placeholder={
          column
            ? t('dashboard.filter.select_value', { name: config.name })
            : t('dashboard.filter.select_column_first')
        }
        value={current}
        options={sorted.map(option => ({ value: option, label: option }))}
        onChange={next => onChange((next as FilterValue) ?? null)}
        style={{ width: '100%' }}
        maxTagCount="responsive"
      />
      {error ? <div className="dashboard-filter__error">{error}</div> : null}
    </>
  );
}
function RangeControl({ config, value, dataset, onChange }: ControlProps) {
  const column = filterColumnName(config);
  const { options, loading } = useCandidates(
    dataset?.id ?? null,
    column,
    Boolean(column),
  );
  const [low, high] = useMemo((): [number, number] => {
    const numeric = options.map(item => Number(item)).filter(item => Number.isFinite(item));
    return numeric.length ? numericBounds(options) : [0, 100];
  }, [options]);
  const numericAvailable = useMemo(
    () => options.some(item => Number.isFinite(Number(item))),
    [options],
  );
  const tuple: [number | null, number | null] = Array.isArray(value)
    ? [typeof value[0] === 'number' ? value[0] : null, typeof value[1] === 'number' ? value[1] : null]
    : [null, null];

  const setBound = (index: 0 | 1, next: number | null) => {
    const updated: [number | null, number | null] = [...tuple];
    updated[index] = next;
    onChange(updated[0] === null && updated[1] === null ? null : updated);
  };

  return (
    <div className="dashboard-filter__range">
      <Space.Compact style={{ width: '100%' }}>
        <InputNumber
          style={{ width: '50%' }}
          placeholder="Min"
          value={tuple[0]}
          onChange={next => setBound(0, typeof next === 'number' ? next : null)}
        />
        <InputNumber
          style={{ width: '50%' }}
          placeholder="Max"
          value={tuple[1]}
          onChange={next => setBound(1, typeof next === 'number' ? next : null)}
        />
      </Space.Compact>
      <Slider
        range
        min={low}
        max={high}
        disabled={loading || !numericAvailable}
        value={[tuple[0] ?? low, tuple[1] ?? high]}
        tooltip={{ formatter: item => (typeof item === 'number' ? item : '') }}
        onChange={next => {
          if (!Array.isArray(next)) return;
          onChange([next[0], next[1]]);
        }}
      />
    </div>
  );
}
function TimeControl({ value, onChange }: ControlProps) {
  const preset =
    typeof value === 'string'
      ? value
      : value && typeof value === 'object' && !Array.isArray(value)
        ? 'Custom'
        : 'No filter';
  const custom =
    value && typeof value === 'object' && !Array.isArray(value) ? value : null;
  const range: [dayjs.Dayjs, dayjs.Dayjs] | null = custom
    ? [dayjs(custom.from), dayjs(custom.to)]
    : null;

  return (
    <div className="dashboard-filter__range">
      <Select
        value={preset}
        options={TIME_PRESETS}
        onChange={next => {
          if (next === 'Custom') {
            onChange({
              from: dayjs().subtract(7, 'day').format('YYYY-MM-DD'),
              to: dayjs().format('YYYY-MM-DD'),
            });
            return;
          }
          onChange(next === 'No filter' ? null : next);
        }}
        style={{ width: '100%' }}
      />
      {preset === 'Custom' ? (
        <DatePicker.RangePicker
          style={{ width: '100%', marginTop: 8 }}
          value={range}
          onChange={dates => {
            if (!dates || !dates[0] || !dates[1]) {
              onChange(null);
              return;
            }
            onChange({
              from: dates[0].format('YYYY-MM-DD'),
              to: dates[1].format('YYYY-MM-DD'),
            });
          }}
        />
      ) : null}
    </div>
  );
}
function TimeColumnControl({ config, value, dataset, onChange }: ControlProps) {
  const { t } = useI18n();
  const columns = (dataset?.columns ?? []).filter(column => column.is_dttm);
  const preferred =
    value && typeof value === 'string'
      ? value
      : dataset?.main_dttm_col || columns[0]?.column_name || '';

  useEffect(() => {
    if (preferred && preferred !== value) onChange(preferred);
  }, [preferred, value, onChange]);


  return (
    <Select
      showSearch
      value={preferred || undefined}
      placeholder={columns.length ? t('dashboard.filter.select_time_column') : t('dashboard.filter.no_time_column')}
      options={columns.map(column => ({
        value: column.column_name,
        label: column.verbose_name
          ? `${column.column_name} (${column.verbose_name})`
          : column.column_name,
      }))}
      onChange={next => onChange(next ?? null)}
      style={{ width: '100%' }}
    />
  );
}
function ValueControl({ value, onChange }: ControlProps) {
  const { t } = useI18n();
  const [text, setText] = useState(
    typeof value === 'string' || typeof value === 'number' ? String(value) : '',
  );

  useEffect(() => {
    setText(typeof value === 'string' || typeof value === 'number' ? String(value) : '');
  }, [value]);

  const commit = () => {
    const next = text.trim();
    onChange(next === '' ? null : next);
  };

  return (
    <Input
      allowClear
      value={text}
      placeholder={t('dashboard.filter.value_placeholder')}
      onChange={event => setText(event.target.value)}
      onPressEnter={commit}
      onBlur={commit}
    />
  );
}

function FilterControl(props: ControlProps) {
  const type = props.config.filterType;
  if (type === 'filter_select' || type === 'filter_multi_select') return <SelectControl {...props} />;
  if (type === 'filter_range') return <RangeControl {...props} />;
  if (type === 'filter_time') return <TimeControl {...props} />;
  if (type === 'filter_timecolumn') return <TimeColumnControl {...props} />;
  if (type === 'filter_value') return <ValueControl {...props} />;
  return null;
}

function FilterActions({
  filter,
  onEdit,
  onRemove,
}: {
  filter: NativeFilterConfig;
  onEdit: (filter: NativeFilterConfig) => void;
  onRemove: (filter: NativeFilterConfig) => void;
}) {
  const { t } = useI18n();
  return (
    <Space size={0}>
      <Button type="text" size="small" icon={<EditOutlined />} onClick={() => onEdit(filter)} />
      <Popconfirm
        title={t('common.actions.delete')}
        okText={t('common.actions.delete')}
        cancelText={t('common.actions.cancel')}
        okButtonProps={{ danger: true }}
        onConfirm={() => onRemove(filter)}
      >
        <Button type="text" size="small" danger icon={<DeleteOutlined />} />
      </Popconfirm>
    </Space>
  );
}

/** Left-hand native filter rail (collapsible). */
export default function FilterPanel({
  filters,
  values,
  datasets,
  collapsed,
  canEdit,
  editMode,
  onChange,
  onToggleCollapse,
  onAdd,
  onEdit,
  onRemove,
}: FilterPanelProps) {
  const { t } = useI18n();
  if (collapsed) {
    return (
      <div className="dashboard-filters dashboard-filters--collapsed">
        <Tooltip title={t('dashboard.filter.expand')} placement="right">
          <Button type="text" size="small" icon={<RightOutlined />} onClick={onToggleCollapse} />
        </Tooltip>
        <span className="dashboard-filters__collapsed-label">Filters</span>
      </div>
    );
  }

  return (
    <aside className="dashboard-filters">
      <div className="dashboard-filters__head">
        <Space size={6}>
          <Typography.Text strong>Filters</Typography.Text>
          <span className="cell-muted">{filters.length}</span>
        </Space>
        <Space size={2}>
          {canEdit && editMode ? (
            <Tooltip title={t('dashboard.filter.new')}>
              <Button type="text" size="small" icon={<PlusOutlined />} onClick={onAdd} />
            </Tooltip>
          ) : null}
          <Tooltip title={t('dashboard.filter.collapse')}>
            <Button type="text" size="small" icon={<LeftOutlined />} onClick={onToggleCollapse} />
          </Tooltip>
        </Space>
      </div>

      <div className="dashboard-filters__body">
        {!filters.length ? (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={
              <span className="cell-muted">
                {canEdit && editMode
                  ? t('dashboard.filter.create_first')
                  : t('dashboard.filter.none_configured')}
              </span>
            }
          />
        ) : null}

        {filters.map(filter => {
          const dataset =
            datasets.find(item => item.id === filter.targets[0]?.datasetId) ?? null;
          if (filter.filterType === 'filter_divider') {
            return (
              <div className="dashboard-filter dashboard-filter--divider" key={filter.id}>
                <div className="dashboard-filter__divider">
                  <span>{filter.name}</span>
                  {canEdit && editMode ? (
                    <FilterActions filter={filter} onEdit={onEdit} onRemove={onRemove} />
                  ) : null}
                </div>
              </div>
            );
          }
          return (
            <div className="dashboard-filter" key={filter.id}>
              <div className="dashboard-filter__head">
                <Tooltip title={filter.description || FILTER_TYPE_LABELS[filter.filterType]}>
                  <span className="dashboard-filter__name">
                    {filter.name}
                    {filter.requiredFirst ? <span className="dashboard-filter__required">*</span> : null}
                  </span>
                </Tooltip>
                {canEdit && editMode ? (
                  <FilterActions filter={filter} onEdit={onEdit} onRemove={onRemove} />
                ) : null}
              </div>
              <FilterControl
                config={filter}
                value={values[filter.id]}
                dataset={dataset}
                onChange={next => onChange(filter.id, next)}
              />
            </div>
          );
        })}
      </div>
    </aside>
  );
}
