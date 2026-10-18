/**
 * Wall editor: palette + absolute canvas + inspector, with undo/redo, autosave
 * guard, version history and the sharing dialog.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  Button,
  Divider,
  Input,
  Modal,
  Select,
  Space,
  Tag,
  Tooltip,
  message,
} from 'antd';
import {
  ArrowLeftOutlined,
  CloudUploadOutlined,
  CompressOutlined,
  EyeOutlined,
  HistoryOutlined,
  RedoOutlined,
  SaveOutlined,
  ShareAltOutlined,
  UndoOutlined,
} from '@ant-design/icons';
import type {
  ScreenComponent,
  ScreenComponentType,
  ScreenConfig,
  ScreenDetail,
  ScreenRevision,
} from '../../api/screens';
import {
  getScreen,
  listScreenRevisions,
  restoreScreenRevision,
  saveScreenConfig,
} from '../../api/screens';
import { client, endpoints, errorMessage } from '../../api/client';
import type { Chart } from '../../api/types';
import ScreenCanvas from '../../components/screen/ScreenCanvas';
import ScreenLeftPanel from '../../components/screen/ScreenLeftPanel';
import ScreenPropertyPanel from '../../components/screen/ScreenPropertyPanel';
import ScreenShareModal from '../../components/screen/ScreenShareModal';
import Loading from '../../components/common/Loading';
import {
  clampComponent,
  createComponent,
  defaultTheme,
  duplicateComponent,
  emptyConfig,
  normalizeConfig,
  sortedComponents,
  themePreset,
} from '../../features/screen/model';
import { useI18n } from '../../i18n';

const MAX_HISTORY = 60;

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

export default function ScreenEditor() {
  const { t } = useI18n();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const viewportRef = useRef<HTMLDivElement | null>(null);

  const [screen, setScreen] = useState<ScreenDetail | null>(null);
  const [config, setConfig] = useState<ScreenConfig>(emptyConfig());
  const configRef = useRef<ScreenConfig>(config);
  const committedRef = useRef<ScreenConfig>(config);
  const pastRef = useRef<ScreenConfig[]>([]);
  const futureRef = useRef<ScreenConfig[]>([]);
  const [historyTick, setHistoryTick] = useState(0);
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [zoom, setZoom] = useState(0.4);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loading, setLoading] = useState(true);
  const [shareOpen, setShareOpen] = useState(false);
  const [revisionsOpen, setRevisionsOpen] = useState(false);
  const [revisions, setRevisions] = useState<ScreenRevision[]>([]);
  const [charts, setCharts] = useState<Chart[]>([]);
  const [chartsLoading, setChartsLoading] = useState(false);
  const [filterValues, setFilterValues] = useState<Record<string, string>>({});
  const [refreshKey, setRefreshKey] = useState(0);

  /* --------------------------------------------------------------- loading */

  useEffect(() => {
    if (!id) return;
    setLoading(true);
    getScreen(id)
      .then(detail => {
        const normalized = normalizeConfig(detail.config);
        setScreen(detail);
        setConfig(normalized);
        configRef.current = normalized;
        committedRef.current = clone(normalized);
        pastRef.current = [];
        futureRef.current = [];
        setDirty(false);
      })
      .catch(err => message.error(errorMessage(err)))
      .finally(() => setLoading(false));
  }, [id]);

  const loadCharts = useCallback((keyword: string) => {
    setChartsLoading(true);
    client
      .get<{ result: Chart[] }>(endpoints.charts, { page_size: 100, q: keyword || undefined })
      .then(response => setCharts(response.result))
      .catch(err => message.error(errorMessage(err)))
      .finally(() => setChartsLoading(false));
  }, []);

  useEffect(() => {
    loadCharts('');
  }, [loadCharts]);

  /* --------------------------------------------------------------- history */

  const fitToViewport = useCallback((canvas: ScreenConfig['canvas']) => {
    const element = viewportRef.current;
    if (!element) return;
    const rect = element.getBoundingClientRect();
    const scale = Math.min(
      (rect.width - 48) / canvas.width,
      (rect.height - 48) / canvas.height,
    );
    setZoom(Math.max(0.1, Math.min(1, Number(scale.toFixed(3)))));
  }, []);

  useEffect(() => {
    if (!loading && screen) {
      const timer = window.setTimeout(() => fitToViewport(configRef.current.canvas), 60);
      return () => window.clearTimeout(timer);
    }
    return undefined;
  }, [loading, screen, fitToViewport]);

  const pushHistory = useCallback((next: ScreenConfig) => {
    pastRef.current.push(clone(committedRef.current));
    if (pastRef.current.length > MAX_HISTORY) pastRef.current.shift();
    futureRef.current = [];
    committedRef.current = clone(next);
    setDirty(true);
    setHistoryTick(tick => tick + 1);
  }, []);

  /** Single funnel for every document mutation. */
  const update = useCallback(
    (updater: (current: ScreenConfig) => ScreenConfig, commit = true) => {
      const next = updater(configRef.current);
      configRef.current = next;
      setConfig(next);
      if (commit) pushHistory(next);
    },
    [pushHistory],
  );

  const undo = useCallback(() => {
    const previous = pastRef.current.pop();
    if (!previous) return;
    futureRef.current.push(clone(committedRef.current));
    committedRef.current = clone(previous);
    configRef.current = previous;
    setConfig(previous);
    setDirty(true);
    setHistoryTick(tick => tick + 1);
  }, []);

  const redo = useCallback(() => {
    const next = futureRef.current.pop();
    if (!next) return;
    pastRef.current.push(clone(committedRef.current));
    committedRef.current = clone(next);
    configRef.current = next;
    setConfig(next);
    setDirty(true);
    setHistoryTick(tick => tick + 1);
  }, []);

  /* ---------------------------------------------------------------- saving */

  const save = useCallback(async () => {
    if (!id || saving) return;
    setSaving(true);
    try {
      const response = await saveScreenConfig(id, configRef.current);
      committedRef.current = clone(configRef.current);
      setDirty(false);
      setScreen(current =>
        current
          ? { ...current, config_rev: response.config_rev, width: configRef.current.canvas.width, height: configRef.current.canvas.height }
          : current,
      );
      message.success(t('screen.editor.saved'));
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }, [id, saving, t]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const meta = event.metaKey || event.ctrlKey;
      if (!meta) return;
      if (event.key.toLowerCase() === 's') {
        event.preventDefault();
        void save();
      } else if (event.key.toLowerCase() === 'z') {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
      } else if (event.key.toLowerCase() === 'd') {
        event.preventDefault();
        duplicateSelection();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [save, undo, redo, selectedIds, config.components]);

  useEffect(() => {
    const handler = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

  useEffect(() => {
    if (!config.interaction?.refreshSec) return undefined;
    const timer = window.setInterval(
      () => setRefreshKey(key => key + 1),
      Math.max(5, config.interaction.refreshSec) * 1000,
    );
    return () => window.clearInterval(timer);
  }, [config.interaction?.refreshSec]);

  /* ------------------------------------------------------------- mutations */

  const addComponent = useCallback(
    (type: ScreenComponentType) => {
      update(current => {
        const theme = current.theme ? { ...defaultTheme(), ...current.theme } : defaultTheme();
        const offset = (current.components.length % 6) * 24;
        const component = createComponent(type, current.canvas, theme, {
          x: Math.round(current.canvas.width / 2 - 200 + offset),
          y: Math.round(current.canvas.height / 2 - 120 + offset),
        });
        component.z = Math.max(100, ...current.components.map(item => item.z)) + 1;
        setSelectedIds([component.id]);
        return { ...current, components: [...current.components, component] };
      });
    },
    [update],
  );

  const patchComponent = useCallback(
    (componentId: string, patch: Partial<ScreenComponent>, commit = true) => {
      update(
        current => ({
          ...current,
          components: current.components.map(component =>
            component.id === componentId
              ? clampComponent({ ...component, ...patch }, current.canvas)
              : component,
          ),
        }),
        commit,
      );
    },
    [update],
  );

  const patchProps = useCallback(
    (componentId: string, props: Record<string, unknown>, commit = true) => {
      update(
        current => ({
          ...current,
          components: current.components.map(component =>
            component.id === componentId ? { ...component, props } : component,
          ),
        }),
        commit,
      );
    },
    [update],
  );

  const removeComponent = useCallback(
    (componentId: string) => {
      update(current => ({
        ...current,
        components: current.components.filter(component => component.id !== componentId),
      }));
      setSelectedIds(ids => ids.filter(item => item !== componentId));
    },
    [update],
  );

  const reorder = useCallback(
    (componentId: string, direction: 'up' | 'down' | 'top' | 'bottom') => {
      update(current => {
        const ordered = sortedComponents(current.components);
        const index = ordered.findIndex(component => component.id === componentId);
        if (index < 0) return current;
        const [item] = ordered.splice(index, 1);
        if (direction === 'top') ordered.push(item);
        else if (direction === 'bottom') ordered.unshift(item);
        else if (direction === 'up') ordered.splice(Math.min(ordered.length, index + 1), 0, item);
        else ordered.splice(Math.max(0, index - 1), 0, item);
        return {
          ...current,
          components: ordered.map((component, position) => ({ ...component, z: 100 + position })),
        };
      });
    },
    [update],
  );

  const duplicateSelection = useCallback(() => {
    if (!selectedIds.length) return;
    update(current => {
      const copies = current.components
        .filter(component => selectedIds.includes(component.id))
        .map(component => ({ ...duplicateComponent(component), z: component.z + 1 }));
      setSelectedIds(copies.map(copy => copy.id));
      return { ...current, components: [...current.components, ...copies] };
    });
  }, [selectedIds, update]);

  const selected = useMemo(
    () => config.components.find(component => component.id === selectedIds[0]) ?? null,
    [config.components, selectedIds],
  );

  /* ------------------------------------------------------------- revisions */

  const openRevisions = async () => {
    if (!id) return;
    try {
      const response = await listScreenRevisions(id);
      setRevisions(response.result);
      setRevisionsOpen(true);
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  const restore = async (rev: number) => {
    if (!id) return;
    try {
      const detail = await restoreScreenRevision(id, rev);
      const normalized = normalizeConfig(detail.config);
      committedRef.current = clone(normalized);
      configRef.current = normalized;
      setConfig(normalized);
      setDirty(false);
      setRevisionsOpen(false);
      message.success(t('screen.editor.restored'));
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  if (loading) return <Loading fullscreen tip={t('screen.editor.loading')} />;
  if (!screen) return null;

  return (
    <div className="scr-editor">
      <header className="scr-editor__header">
        <Button
          type="text"
          icon={<ArrowLeftOutlined />}
          onClick={() => {
            if (dirty && !window.confirm(t('screen.editor.leave_confirm'))) return;
            navigate('/screen/list');
          }}
        />
        <Input
          className="scr-editor__name"
          variant="borderless"
          value={screen.name}
          onChange={event => setScreen({ ...screen, name: event.target.value })}
          onBlur={async () => {
            if (!screen.name.trim()) return;
            try {
              await client.put(`/screen/${screen.id}`, { name: screen.name });
            } catch (err) {
              message.error(errorMessage(err));
            }
          }}
        />
        {dirty && <Tag color="warning">{t('screen.editor.unsaved')}</Tag>}
        {screen.public_enabled && <Tag color="processing">{t('screen.list.public')}</Tag>}

        <div className="scr-editor__spacer" />

        <Space size={4}>
          <Tooltip title={`${t('screen.editor.undo')} (⌘Z)`}>
            <Button
              type="text"
              icon={<UndoOutlined />}
              disabled={!pastRef.current.length}
              onClick={undo}
              data-tick={historyTick}
            />
          </Tooltip>
          <Tooltip title={`${t('screen.editor.redo')} (⇧⌘Z)`}>
            <Button
              type="text"
              icon={<RedoOutlined />}
              disabled={!futureRef.current.length}
              onClick={redo}
              data-tick={historyTick}
            />
          </Tooltip>
          <Select
            size="small"
            value={zoom}
            style={{ width: 108 }}
            onChange={value => setZoom(Number(value))}
            options={[
              { value: 0.25, label: '25%' },
              { value: 0.4, label: '40%' },
              { value: 0.5, label: '50%' },
              { value: 0.75, label: '75%' },
              { value: 1, label: '100%' },
            ]}
          />
          <Tooltip title={t('screen.editor.fit')}>
            <Button
              type="text"
              icon={<CompressOutlined />}
              onClick={() => fitToViewport(config.canvas)}
            />
          </Tooltip>
          <Divider type="vertical" />
          <Button icon={<HistoryOutlined />} onClick={() => void openRevisions()}>
            {t('screen.editor.revisions')}
          </Button>
          <Button icon={<EyeOutlined />} onClick={() => window.open(`/screen/${screen.id}/view`, '_blank')}>
            {t('screen.editor.preview')}
          </Button>
          <Button icon={<ShareAltOutlined />} onClick={() => setShareOpen(true)}>
            {t('screen.share.title')}
          </Button>
          <Button
            type="primary"
            icon={<SaveOutlined />}
            loading={saving}
            onClick={() => void save()}
          >
            {t('common.actions.save')}
          </Button>
        </Space>
      </header>

      <div className="scr-editor__body">
        <ScreenLeftPanel
          config={config}
          selectedIds={selectedIds}
          onAdd={addComponent}
          onSelect={setSelectedIds}
          onChange={(componentId, patch) => patchComponent(componentId, patch)}
          onRemove={removeComponent}
          onReorder={reorder}
        />

        <div className="scr-editor__canvas" ref={viewportRef}>
          <ScreenCanvas
            config={config}
            selectedIds={selectedIds}
            zoom={zoom}
            filterValues={filterValues}
            onSelect={setSelectedIds}
            onPreview={components =>
              update(current => ({ ...current, components }), false)
            }
            onCommit={() => pushHistory(configRef.current)}
            onFilterChange={(filterId, value) =>
              setFilterValues(current => ({ ...current, [filterId]: value }))
            }
            refreshKey={refreshKey}
          />
        </div>

        <ScreenPropertyPanel
          config={config}
          component={selected}
          selectedCount={selectedIds.length}
          onChange={patch => selected && patchComponent(selected.id, patch, false)}
          onProps={props => selected && patchProps(selected.id, props, false)}
          onCommit={() => pushHistory(configRef.current)}
          onCanvas={patch =>
            update(current => ({
              ...current,
              canvas: { ...current.canvas, ...patch },
              ...(patch.theme
                ? { theme: { ...defaultTheme(), ...themePreset(patch.theme).theme } }
                : {}),
            }), false)
          }
          onInteraction={patch =>
            update(current => ({
              ...current,
              interaction: { ...current.interaction, ...patch },
            }), false)
          }
          charts={charts}
          chartsLoading={chartsLoading}
          onSearchCharts={loadCharts}
        />
      </div>

      <ScreenShareModal
        open={shareOpen}
        screen={screen}
        onClose={() => setShareOpen(false)}
        onChanged={() => {
          if (!id) return;
          void getScreen(id).then(setScreen);
        }}
      />

      <Modal
        open={revisionsOpen}
        title={t('screen.editor.revisions')}
        footer={null}
        onCancel={() => setRevisionsOpen(false)}
      >
        <div className="scr-revisions">
          {revisions.map(revision => (
            <div className="scr-revisions__row" key={revision.id}>
              <span className="scr-revisions__rev">r{revision.rev}</span>
              <span className="scr-revisions__note">
                {revision.note}
                <em>{revision.created_on ? new Date(revision.created_on).toLocaleString() : ''}</em>
              </span>
              <span className="scr-revisions__size">{Math.round(revision.size / 1024)} KB</span>
              <Button size="small" icon={<CloudUploadOutlined />} onClick={() => void restore(revision.rev)}>
                {t('screen.editor.restore')}
              </Button>
            </div>
          ))}
          {!revisions.length && <div className="scr-layers__empty">{t('screen.editor.no_revisions')}</div>}
        </div>
      </Modal>
    </div>
  );
}
