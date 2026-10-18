/**
 * Editor canvas: absolute drag, resize handles, marquee selection, snapping
 * guides and keyboard nudging for the wall document.
 *
 * All math happens in design coordinates. The stage is scaled with a CSS
 * transform, so pointer deltas are divided by the zoom factor first.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ScreenComponent, ScreenConfig, ScreenFilter } from '../../api/screens';
import { clampComponent, sortedComponents } from '../../features/screen/model';
import { alignRects, distributeRects, intersects, snapRect } from '../../features/screen/geometry';
import type { AlignMode, Guide, Rect } from '../../features/screen/geometry';
import ScreenWidget from './ScreenWidget';
import EmptyState from '../common/EmptyState';
import { useI18n } from '../../i18n';
import { Dropdown, Tooltip } from 'antd';
import {
  AlignCenterOutlined,
  ColumnWidthOutlined,
  DeleteOutlined,
  DragOutlined,
  LockOutlined,
  AlignLeftOutlined,
  AlignRightOutlined,
  VerticalAlignBottomOutlined,
  VerticalAlignMiddleOutlined,
  VerticalAlignTopOutlined,
  UnlockOutlined,
} from '@ant-design/icons';

export interface ScreenCanvasProps {
  config: ScreenConfig;
  selectedIds: string[];
  zoom: number;
  filterValues: Record<string, string>;
  onSelect: (ids: string[]) => void;
  /** Live update while dragging (no history entry). */
  onPreview: (components: ScreenComponent[]) => void;
  /** Called once the gesture ends so the editor can push an undo snapshot. */
  onCommit: () => void;
  onFilterChange: (filterId: string, value: string) => void;
  refreshKey: number;
}

type ResizeDir = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';

const HANDLES: ResizeDir[] = ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w'];

export default function ScreenCanvas({
  config,
  selectedIds,
  zoom,
  filterValues,
  onSelect,
  onPreview,
  onCommit,
  onFilterChange,
  refreshKey,
}: ScreenCanvasProps) {
  const { t } = useI18n();
  const stageRef = useRef<HTMLDivElement | null>(null);
  const [guides, setGuides] = useState<Guide[]>([]);
  const [marquee, setMarquee] = useState<Rect | null>(null);
  const [dragging, setDragging] = useState(false);

  const components = useMemo(() => sortedComponents(config.components), [config.components]);
  const selected = useMemo(
    () => components.filter(component => selectedIds.includes(component.id)),
    [components, selectedIds],
  );
  const selectedMap = useMemo(
    () => new Map(selected.map(component => [component.id, component])),
    [selected],
  );

  const toDesign = useCallback(
    (clientX: number, clientY: number) => {
      const rect = stageRef.current?.getBoundingClientRect();
      if (!rect) return { x: 0, y: 0 };
      return { x: (clientX - rect.left) / zoom, y: (clientY - rect.top) / zoom };
    },
    [zoom],
  );

  /** Applies a rect patch to one component and previews it. */
  const applyRects = useCallback(
    (patches: Map<string, Rect>) => {
      if (!patches.size) return;
      onPreview(
        config.components.map(component => {
          const patch = patches.get(component.id);
          if (!patch) return component;
          return { ...component, ...patch };
        }),
      );
    },
    [config.components, onPreview],
  );

  /* ------------------------------------------------------------- dragging */

  const startDrag = (event: React.PointerEvent, component: ScreenComponent) => {
    if (component.locked) return;
    event.stopPropagation();
    event.preventDefault();
    const additive = event.shiftKey || event.metaKey || event.ctrlKey;
    let nextSelection = selectedIds;
    if (additive) {
      nextSelection = selectedIds.includes(component.id)
        ? selectedIds.filter(id => id !== component.id)
        : [...selectedIds, component.id];
      onSelect(nextSelection);
      if (!nextSelection.includes(component.id)) return;
    } else if (!selectedIds.includes(component.id)) {
      nextSelection = [component.id];
      onSelect(nextSelection);
    }

    const start = toDesign(event.clientX, event.clientY);
    const origins = new Map(
      components
        .filter(item => nextSelection.includes(item.id) && !item.locked)
        .map(item => [item.id, { x: item.x, y: item.y, w: item.w, h: item.h }]),
    );
    if (!origins.size) return;

    let moved = false;
    const onMove = (pointer: PointerEvent) => {
      const current = toDesign(pointer.clientX, pointer.clientY);
      let dx = current.x - start.x;
      let dy = current.y - start.y;
      if (!moved && Math.abs(dx) + Math.abs(dy) < 3) return;
      moved = true;
      setDragging(true);

      const primary = origins.get(component.id);
      let nextGuides: Guide[] = [];
      if (primary) {
        const others = components
          .filter(item => !origins.has(item.id))
          .map(item => ({ x: item.x, y: item.y, w: item.w, h: item.h }));
        const raw = { ...primary, x: primary.x + dx, y: primary.y + dy };
        const snapped = snapRect(
          raw,
          others,
          { width: config.canvas.width, height: config.canvas.height },
          config.canvas.gridSize,
          !pointer.altKey,
        );
        nextGuides = snapped.guides;
        dx = snapped.rect.x - primary.x;
        dy = snapped.rect.y - primary.y;
      }

      const patches = new Map<string, Rect>();
      origins.forEach((origin, id) => {
        patches.set(id, {
          x: Math.round(origin.x + dx),
          y: Math.round(origin.y + dy),
          w: origin.w,
          h: origin.h,
        });
      });
      setGuides(nextGuides);
      applyRects(patches);
    };

    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      setGuides([]);
      setDragging(false);
      if (moved) onCommit();
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp, { once: true });
  };

  /* ------------------------------------------------------------- resizing */

  const startResize = (event: React.PointerEvent, component: ScreenComponent, dir: ResizeDir) => {
    event.stopPropagation();
    event.preventDefault();
    const start = toDesign(event.clientX, event.clientY);
    const origin = { x: component.x, y: component.y, w: component.w, h: component.h };
    const min = 8;

    const onMove = (pointer: PointerEvent) => {
      const current = toDesign(pointer.clientX, pointer.clientY);
      const dx = current.x - start.x;
      const dy = current.y - start.y;
      let { x, y, w, h } = origin;

      if (dir.includes('e')) w = Math.max(min, origin.w + dx);
      if (dir.includes('s')) h = Math.max(min, origin.h + dy);
      if (dir.includes('w')) {
        w = Math.max(min, origin.w - dx);
        x = origin.x + (origin.w - w);
      }
      if (dir.includes('n')) {
        h = Math.max(min, origin.h - dy);
        y = origin.y + (origin.h - h);
      }
      if (!pointer.altKey && config.canvas.gridSize > 1) {
        const grid = config.canvas.gridSize;
        w = Math.round(w / grid) * grid;
        h = Math.round(h / grid) * grid;
        x = Math.round(x / grid) * grid;
        y = Math.round(y / grid) * grid;
      }
      if (pointer.shiftKey) {
        const ratio = origin.w / origin.h;
        if (dir === 'se' || dir === 'nw') h = Math.round(w / ratio);
        if (dir === 'ne' || dir === 'sw') h = Math.round(w / ratio);
      }
      applyRects(new Map([[component.id, { x, y, w: Math.max(min, w), h: Math.max(min, h) }]]));
    };

    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      onCommit();
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp, { once: true });
  };

  /* -------------------------------------------------------------- marquee */

  const startMarquee = (event: React.PointerEvent) => {
    if (event.button !== 0) {
      onSelect([]);
      return;
    }
    const start = toDesign(event.clientX, event.clientY);
    let active = false;

    const onMove = (pointer: PointerEvent) => {
      const current = toDesign(pointer.clientX, pointer.clientY);
      const rect = {
        x: Math.min(start.x, current.x),
        y: Math.min(start.y, current.y),
        w: Math.abs(current.x - start.x),
        h: Math.abs(current.y - start.y),
      };
      if (!active && rect.w + rect.h < 4) return;
      active = true;
      setMarquee(rect);
      const hits = components
        .filter(component => component.visible !== false)
        .filter(component => intersects(rect, component))
        .map(component => component.id);
      onSelect(hits);
    };

    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      setMarquee(null);
      if (!active) onSelect([]);
    };

    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp, { once: true });
  };

  /* ------------------------------------------------------------- keyboard */

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target && ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName)) return;
      if (target?.isContentEditable) return;
      if (!selected.length) return;

      if (event.key === 'Delete' || event.key === 'Backspace') {
        onPreview(config.components.filter(component => !selectedIds.includes(component.id)));
        onCommit();
        onSelect([]);
        event.preventDefault();
        return;
      }
      const step = event.shiftKey ? 10 : 1;
      const deltas: Record<string, [number, number]> = {
        ArrowUp: [0, -step],
        ArrowDown: [0, step],
        ArrowLeft: [-step, 0],
        ArrowRight: [step, 0],
      };
      const delta = deltas[event.key];
      if (!delta) return;
      event.preventDefault();
      onPreview(
        config.components.map(component =>
          selectedIds.includes(component.id) && !component.locked
            ? clampComponent(
                { ...component, x: component.x + delta[0], y: component.y + delta[1] },
                config.canvas,
              )
            : component,
        ),
      );
      onCommit();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [config.canvas, config.components, onCommit, onPreview, onSelect, selected.length, selectedIds]);

  /* ------------------------------------------------------------- align ops */

  const applyAlign = (mode: AlignMode) => {
    if (selected.length < 2) return;
    const rects = selected.map(component => ({
      x: component.x,
      y: component.y,
      w: component.w,
      h: component.h,
    }));
    const aligned = alignRects(rects, mode);
    const patches = new Map<string, Rect>();
    selected.forEach((component, index) => patches.set(component.id, aligned[index]));
    applyRects(patches);
    onCommit();
  };

  const applyDistribute = (axis: 'h' | 'v') => {
    if (selected.length < 3) return;
    const rects = selected.map(component => ({
      x: component.x,
      y: component.y,
      w: component.w,
      h: component.h,
    }));
    const spread = distributeRects(rects, axis);
    const patches = new Map<string, Rect>();
    selected.forEach((component, index) => patches.set(component.id, spread[index]));
    applyRects(patches);
    onCommit();
  };

  const toggleLock = () => {
    if (!selectedIds.length) return;
    onPreview(
      config.components.map(component =>
        selectedIds.includes(component.id)
          ? { ...component, locked: !component.locked }
          : component,
      ),
    );
    onCommit();
  };

  const removeSelected = () => {
    if (!selectedIds.length) return;
    onPreview(config.components.filter(component => !selectedIds.includes(component.id)));
    onCommit();
    onSelect([]);
  };

  const stageStyle = {
    width: config.canvas.width,
    height: config.canvas.height,
    transform: `scale(${zoom})`,
    transformOrigin: 'top left',
    background: config.canvas.background,
    backgroundColor: '#050b14',
    backgroundImage: config.canvas.showGrid
      ? `linear-gradient(rgba(255,255,255,0.045) 1px, transparent 1px),
         linear-gradient(90deg, rgba(255,255,255,0.045) 1px, transparent 1px)`
      : undefined,
    backgroundSize: `${config.canvas.gridSize * 4}px ${config.canvas.gridSize * 4}px`,
  } as const;

  return (
    <div className="scr-canvas">
      <div className="scr-canvas__toolbar">
        <Tooltip title={t('screen.canvas.align_left')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('left')}>
            <AlignLeftOutlined />
          </button>
        </Tooltip>
        <Tooltip title={t('screen.canvas.align_hcenter')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('hcenter')}>
            <AlignCenterOutlined />
          </button>
        </Tooltip>
        <Tooltip title={t('screen.canvas.align_right')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('right')}>
            <AlignRightOutlined />
          </button>
        </Tooltip>
        <span className="scr-canvas__sep" />
        <Tooltip title={t('screen.canvas.align_top')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('top')}>
            <VerticalAlignTopOutlined />
          </button>
        </Tooltip>
        <Tooltip title={t('screen.canvas.align_vcenter')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('vcenter')}>
            <VerticalAlignMiddleOutlined />
          </button>
        </Tooltip>
        <Tooltip title={t('screen.canvas.align_bottom')}>
          <button type="button" disabled={selected.length < 2} onClick={() => applyAlign('bottom')}>
            <VerticalAlignBottomOutlined />
          </button>
        </Tooltip>
        <span className="scr-canvas__sep" />
        <Tooltip title={t('screen.canvas.distribute_h')}>
          <button type="button" disabled={selected.length < 3} onClick={() => applyDistribute('h')}>
            <ColumnWidthOutlined />
          </button>
        </Tooltip>
        <Dropdown
          trigger={['click']}
          menu={{
            items: [
              {
                key: 'distribute-h',
                label: t('screen.canvas.distribute_h'),
                disabled: selected.length < 3,
                onClick: () => applyDistribute('h'),
              },
              {
                key: 'distribute-v',
                label: t('screen.canvas.distribute_v'),
                disabled: selected.length < 3,
                onClick: () => applyDistribute('v'),
              },
            ],
          }}
        >
          <button type="button" disabled={selected.length < 3}>
            <DragOutlined />
          </button>
        </Dropdown>
        <span className="scr-canvas__sep" />
        <Tooltip title={selected.some(item => item.locked) ? t('screen.canvas.unlock') : t('screen.canvas.lock')}>
          <button type="button" disabled={!selected.length} onClick={toggleLock}>
            {selected.some(item => item.locked) ? <UnlockOutlined /> : <LockOutlined />}
          </button>
        </Tooltip>
        <Tooltip title={t('screen.canvas.remove')}>
          <button type="button" disabled={!selected.length} onClick={removeSelected}>
            <DeleteOutlined />
          </button>
        </Tooltip>
        <span className="scr-canvas__spacer" />
        <span className="scr-canvas__hint">
          {config.canvas.width} × {config.canvas.height} · {Math.round(zoom * 100)}%
          {selected.length > 0 && ` · ${t('screen.canvas.selected')} ${selected.length}`}
        </span>
      </div>

      <div className="scr-canvas__viewport">
        <div
          className="scr-canvas__scroll"
          style={{
            width: config.canvas.width * zoom,
            height: config.canvas.height * zoom,
          }}
        >
        <div
          ref={stageRef}
          className={`scr-canvas__stage${dragging ? ' is-dragging' : ''}`}
          style={stageStyle}
          onPointerDown={startMarquee}
        >
          {components
            .filter(component => component.visible !== false)
            .map(component => {
              const isSelected = selectedIds.includes(component.id);
              return (
                <div
                  key={component.id}
                  className={`scr-node${isSelected ? ' is-selected' : ''}${
                    component.locked ? ' is-locked' : ''
                  }`}
                  style={{
                    left: component.x,
                    top: component.y,
                    width: component.w,
                    height: component.h,
                    zIndex: component.z,
                    transform: component.rotate ? `rotate(${component.rotate}deg)` : undefined,
                  }}
                  onPointerDown={event => startDrag(event, component)}
                >
                  <ScreenWidget
                    component={component}
                    mode="edit"
                    filterValues={filterValues}
                    onFilterChange={(_filter: ScreenFilter, value) =>
                      onFilterChange(component.id, value)
                    }
                    refreshKey={refreshKey}
                  />
                  {isSelected && (
                    <>
                      <span className="scr-node__tag">
                        {component.name} · {Math.round(component.w)}×{Math.round(component.h)}
                      </span>
                      {!component.locked &&
                        HANDLES.map(dir => (
                          <span
                            key={dir}
                            className={`scr-handle scr-handle--${dir}`}
                            onPointerDown={event => startResize(event, component, dir)}
                          />
                        ))}
                    </>
                  )}
                </div>
              );
            })}

          {guides.map((guide, index) => (
            <span
              key={`${guide.orientation}-${guide.position}-${index}`}
              className={`scr-guide scr-guide--${guide.orientation === 'v' ? 'v' : 'h'}`}
              style={
                guide.orientation === 'v'
                  ? { left: guide.position, top: 0, height: config.canvas.height }
                  : { top: guide.position, left: 0, width: config.canvas.width }
              }
            />
          ))}

          {marquee && (
            <div
              className="scr-marquee-box"
              style={{ left: marquee.x, top: marquee.y, width: marquee.w, height: marquee.h }}
            />
          )}

          {!components.length && (
            <div className="scr-canvas__empty">
              <EmptyState description={t('screen.empty.canvas_desc')} />
            </div>
          )}
        </div>
        </div>
      </div>
    </div>
  );
}
