/**
 * Full-viewport wall viewer. Serves three routes:
 *   /screen/:id/view          — authenticated preview
 *   /screen/public/:token     — shared link (optionally password gated)
 *   any of the above?embed=1  — chrome-less, for an iframe
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { Button, Input, Modal, Tooltip, message } from 'antd';
import {
  ArrowLeftOutlined,
  DownloadOutlined,
  EditOutlined,
  FullscreenOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import type { PublicScreen, ScreenConfig, ScreenDetail } from '../../api/screens';
import { getPublicScreen, getScreen, screenExportUrl, unlockPublicScreen } from '../../api/screens';
import { errorMessage } from '../../api/client';
import ScreenSurface, { Watermark } from '../../components/screen/ScreenSurface';
import Loading from '../../components/common/Loading';
import { normalizeConfig } from '../../features/screen/model';
import { syncChartTheme } from '../../components/charts/utils';
import { useAppSelector } from '../../store';
import { useI18n } from '../../i18n';

interface ViewModel {
  id: number;
  name: string;
  config: ScreenConfig;
  refreshSec: number;
  allowExport: boolean;
  publicToken?: string;
}

export default function ScreenView() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const { id, token } = useParams<{ id?: string; token?: string }>();
  const [params] = useSearchParams();
  const embed = params.get('embed') === '1';
  const me = useAppSelector(state => state.user.me);

  const [model, setModel] = useState<ViewModel | null>(null);
  const [loading, setLoading] = useState(true);
  const [gate, setGate] = useState<{ name: string; cover?: string } | null>(null);
  const [password, setPassword] = useState('');
  const [unlocking, setUnlocking] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [filterValues, setFilterValues] = useState<Record<string, string>>({});
  const [clock, setClock] = useState(() => new Date());

  /* ------------------------------------------------------------ data load */

  const fromPublic = useCallback((result: PublicScreen): ViewModel | null => {
    if (!result.config) return null;
    return {
      id: result.id ?? 0,
      name: result.name,
      config: normalizeConfig(result.config),
      refreshSec: result.refresh_sec ?? 0,
      allowExport: result.allow_export !== false,
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const load = async () => {
      try {
        if (token) {
          const result = await getPublicScreen(token);
          if (cancelled) return;
          if (result.requires_password) {
            setGate({ name: result.name, cover: result.cover });
            setModel(null);
          } else {
            const next = fromPublic(result);
            setModel(next);
          }
        } else if (id) {
          const detail: ScreenDetail = await getScreen(id);
          if (cancelled) return;
          setModel({
            id: detail.id,
            name: detail.name,
            config: normalizeConfig(detail.config),
            refreshSec: detail.refresh_sec,
            allowExport: detail.allow_export,
            publicToken: detail.public_token,
          });
        }
      } catch (err) {
        if (!cancelled) message.error(errorMessage(err));
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, [id, token, fromPublic]);

  /* ------------------------------------------------------------- theming */

  useEffect(() => {
    const previous =
      (document.documentElement.getAttribute('data-theme') as 'light' | 'dark' | null) ?? 'light';
    syncChartTheme('dark');
    return () => syncChartTheme(previous);
  }, []);

  /* ------------------------------------------------------------- refresh */

  useEffect(() => {
    if (!model?.refreshSec) return undefined;
    const timer = window.setInterval(
      () => setRefreshKey(key => key + 1),
      Math.max(5, model.refreshSec) * 1000,
    );
    return () => window.clearInterval(timer);
  }, [model?.refreshSec]);

  useEffect(() => {
    const timer = window.setInterval(() => setClock(new Date()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  /* --------------------------------------------------------------- unlock */

  const unlock = async () => {
    if (!token) return;
    setUnlocking(true);
    try {
      const result = await unlockPublicScreen(token, password);
      const next = fromPublic(result);
      if (next) {
        setModel(next);
        setGate(null);
      }
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setUnlocking(false);
    }
  };

  const watermarkText = useMemo(() => {
    if (!model?.config.interaction?.watermark) return '';
    const who = me?.username ?? model.name;
    return `${who} · ${clock.toLocaleDateString()} ${clock.toLocaleTimeString().slice(0, 5)}`;
  }, [model, me, clock]);

  if (loading) return <Loading fullscreen tip={t('screen.view.loading')} />;

  if (gate) {
    return (
      <div
        className="scr-gate"
        style={{ background: gate.cover || 'radial-gradient(circle at 50% 0%, #0d3563, #030b17)' }}
      >
        <div className="scr-gate__card">
          <h1>{gate.name}</h1>
          <p>{t('screen.view.password_prompt')}</p>
          <Input.Password
            autoFocus
            value={password}
            placeholder={t('screen.share.password')}
            onChange={event => setPassword(event.target.value)}
            onPressEnter={() => void unlock()}
          />
          <Button type="primary" block loading={unlocking} onClick={() => void unlock()}>
            {t('screen.view.open')}
          </Button>
        </div>
      </div>
    );
  }

  if (!model) return null;

  return (
    <div className={`scr-viewer${embed ? ' is-embed' : ''}`}>
      {!embed && (
        <div className="scr-viewer__bar">
          <Button
            type="text"
            icon={<ArrowLeftOutlined />}
            onClick={() => (token ? window.close() : navigate('/screen/list'))}
          />
          <span className="scr-viewer__name">{model.name}</span>
          <span className="scr-viewer__spacer" />
          <Tooltip title={t('common.actions.refresh')}>
            <Button type="text" icon={<ReloadOutlined />} onClick={() => setRefreshKey(key => key + 1)} />
          </Tooltip>
          <Tooltip title={t('screen.view.fullscreen')}>
            <Button
              type="text"
              icon={<FullscreenOutlined />}
              onClick={() => void document.documentElement.requestFullscreen?.()}
            />
          </Tooltip>
          {model.allowExport && !token && (
            <Tooltip title={t('screen.list.export')}>
              <Button
                type="text"
                icon={<DownloadOutlined />}
                onClick={() => {
                  window.location.href = screenExportUrl(model.id);
                }}
              />
            </Tooltip>
          )}
          {!token && (
            <Button
              type="primary"
              icon={<EditOutlined />}
              onClick={() => navigate(`/screen/${model.id}/edit`)}
            >
              {t('screen.list.edit')}
            </Button>
          )}
        </div>
      )}

      <div className="scr-viewer__stage">
        <ScreenSurface
          config={model.config}
          mode="view"
          filterValues={filterValues}
          onFilterChange={(filterId, value) =>
            setFilterValues(current => ({ ...current, [filterId]: value }))
          }
          refreshKey={refreshKey}
          overlay={watermarkText ? <Watermark text={watermarkText} /> : undefined}
        />
      </div>
    </div>
  );
}
