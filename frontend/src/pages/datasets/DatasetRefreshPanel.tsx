/**
 * Dataset → 数据刷新.
 *
 * Two independent concerns share this panel, which is why they are laid out in
 * separate blocks: the *schedule* (how often the background worker refreshes)
 * and the *last run* (what actually happened). Operators need both — a green
 * schedule with a red last-run is the state that silently breaks dashboards.
 */
import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, InputNumber, Select, Space, Switch, Tag, message } from 'antd';
import { ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Dataset } from '../../api/types';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';
import {
  formatRefreshInterval,
  splitInterval,
  UNIT_MAX,
  UNIT_SECONDS,
  type IntervalUnit,
} from './refreshUtils';

export interface DatasetRefreshPanelProps {
  dataset: Dataset;
  canWrite: boolean;
  /** Reloads the dataset in the parent drawer after a change. */
  onChanged: () => void | Promise<void>;
}

export default function DatasetRefreshPanel({
  dataset,
  canWrite,
  onChanged,
}: DatasetRefreshPanelProps) {
  const { t } = useI18n();
  const [auto, setAuto] = useState<boolean>((dataset.refresh_interval_sec ?? 0) > 0);
  const [value, setValue] = useState<number>(1);
  const [unit, setUnit] = useState<IntervalUnit>('hour');
  const [saving, setSaving] = useState(false);
  const [refreshing, setRefreshing] = useState(false);

  // Re-sync the form whenever the drawer loads a different dataset.
  useEffect(() => {
    const seconds = dataset.refresh_interval_sec ?? 0;
    setAuto(seconds > 0);
    const split = splitInterval(seconds);
    setValue(split.value);
    setUnit(split.unit);
  }, [dataset.id, dataset.refresh_interval_sec]);

  const unitOptions = useMemo(
    () => [
      { value: 'minute' as const, label: t('dataset.refresh.unit_minute') },
      { value: 'hour' as const, label: t('dataset.refresh.unit_hour') },
      { value: 'day' as const, label: t('dataset.refresh.unit_day') },
    ],
    [t],
  );

  const intervalSec = auto ? value * UNIT_SECONDS[unit] : 0;

  const saveSchedule = async () => {
    if (auto && (!value || value < 1)) {
      message.warning(t('dataset.refresh.interval_required'));
      return;
    }
    if (auto && value > UNIT_MAX[unit]) {
      message.warning(t('dataset.refresh.interval_range'));
      return;
    }
    try {
      setSaving(true);
      await client.put(endpoints.datasetRefreshConfig(dataset.id), { interval_sec: intervalSec });
      message.success(t('dataset.refresh.saved'));
      await onChanged();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const refreshNow = async () => {
    try {
      setRefreshing(true);
      const response = await client.post<{
        refresh?: { columns_added?: number; columns_updated?: number; rows?: number };
      }>(endpoints.datasetRefresh(dataset.id), {});
      const outcome = response.refresh ?? {};
      message.success(
        outcome.rows
          ? t('dataset.refresh.done_rows', {
              rows: outcome.rows,
              added: outcome.columns_added ?? 0,
            })
          : t('dataset.refresh.done', {
              added: outcome.columns_added ?? 0,
              updated: outcome.columns_updated ?? 0,
            }),
      );
      await onChanged();
    } catch (error) {
      message.error(`${t('dataset.refresh.failed')}: ${errorMessage(error)}`);
    } finally {
      setRefreshing(false);
    }
  };

  const status = dataset.last_refresh_status ?? '';
  const statusTag =
    status === 'success' ? (
      <Tag color="green" bordered={false}>
        {t('dataset.refresh.status_success')}
      </Tag>
    ) : status === 'failed' ? (
      <Tag color="red" bordered={false}>
        {t('dataset.refresh.status_failed')}
      </Tag>
    ) : status === 'running' ? (
      <Tag color="blue" bordered={false}>
        {t('dataset.refresh.status_running')}
      </Tag>
    ) : (
      <Tag bordered={false}>{t('dataset.refresh.never')}</Tag>
    );

  const nextRun = (() => {
    const seconds = dataset.refresh_interval_sec ?? 0;
    if (seconds <= 0 || !dataset.last_refreshed_at) return null;
    const next = new Date(new Date(dataset.last_refreshed_at).getTime() + seconds * 1000);
    return next.toLocaleString();
  })();

  const kind = dataset.kind ?? 'physical';
  const scopeText =
    kind === 'external'
      ? t('dataset.refresh.scope_external')
      : kind === 'virtual'
        ? t('dataset.refresh.scope_virtual')
        : t('dataset.refresh.scope_physical');

  return (
    <div className="ds-refresh">
      <div className="ds-refresh__grid">
        <div className="ds-refresh__field">
          <span className="tiny-label">{t('dataset.refresh.auto')}</span>
          <Space size={10} style={{ marginTop: 8 }}>
            <Switch
              checked={auto}
              disabled={!canWrite}
              onChange={checked => setAuto(checked)}
              checkedChildren={t('common.state.yes')}
              unCheckedChildren={t('common.state.no')}
            />
            <span className="cell-muted" style={{ fontSize: 12 }}>
              {t('dataset.refresh.auto_hint')}
            </span>
          </Space>
        </div>

        <div className="ds-refresh__field">
          <span className="tiny-label">{t('dataset.refresh.interval')}</span>
          <Space size={8} style={{ marginTop: 6 }}>
            <InputNumber
              min={1}
              max={UNIT_MAX[unit]}
              value={value}
              disabled={!canWrite || !auto}
              onChange={next => setValue(Number(next ?? 1))}
              style={{ width: 110 }}
            />
            <Select<IntervalUnit>
              value={unit}
              disabled={!canWrite || !auto}
              onChange={next => setUnit(next)}
              options={unitOptions}
              style={{ width: 150 }}
            />
            <span className="cell-muted" style={{ fontSize: 12 }}>
              {formatRefreshInterval(intervalSec, t)}
            </span>
          </Space>
        </div>

        <div className="ds-refresh__actions">
          {canWrite ? (
            <Space size={8}>
              <Button loading={saving} icon={<SaveOutlined />} onClick={() => void saveSchedule()}>
                {t('dataset.refresh.save')}
              </Button>
              <Button
                type="primary"
                loading={refreshing}
                icon={<ReloadOutlined />}
                onClick={() => void refreshNow()}
              >
                {refreshing ? t('dataset.refresh.running') : t('dataset.refresh.run_now')}
              </Button>
            </Space>
          ) : null}
        </div>
      </div>

      <Alert
        type="info"
        showIcon
        className="ds-refresh__scope"
        message={t('dataset.refresh.scope_title')}
        description={scopeText}
      />

      <div className="ds-refresh__facts">
        <div>
          <span className="tiny-label">{t('dataset.refresh.last_run')}</span>
          <div>{formatDateTimeShort(dataset.last_refreshed_at ?? null)}</div>
        </div>
        <div>
          <span className="tiny-label">{t('dataset.refresh.interval')}</span>
          <div>{formatRefreshInterval(dataset.refresh_interval_sec ?? 0, t)}</div>
        </div>
        <div>
          <span className="tiny-label">{t('common.label.status')}</span>
          <div>{statusTag}</div>
        </div>
        <div>
          <span className="tiny-label">{t('dataset.refresh.trigger_manual')}</span>
          <div>
            {dataset.last_refresh_trigger === 'schedule'
              ? t('dataset.refresh.trigger_schedule')
              : dataset.last_refresh_trigger === 'manual'
                ? t('dataset.refresh.trigger_manual')
                : '—'}
          </div>
        </div>
        <div>
          <span className="tiny-label">{t('dataset.refresh.duration')}</span>
          <div>
            {dataset.last_refresh_duration_ms
              ? `${dataset.last_refresh_duration_ms} ms`
              : '—'}
          </div>
        </div>
        <div>
          <span className="tiny-label">{t('dataset.refresh.next_run_label')}</span>
          <div>{nextRun ?? '—'}</div>
        </div>
      </div>

      {dataset.last_refresh_status === 'failed' && dataset.last_refresh_error ? (
        <Alert
          type="error"
          showIcon
          className="ds-refresh__error"
          message={t('dataset.refresh.error')}
          description={<span className="mono">{dataset.last_refresh_error}</span>}
        />
      ) : null}
    </div>
  );
}
