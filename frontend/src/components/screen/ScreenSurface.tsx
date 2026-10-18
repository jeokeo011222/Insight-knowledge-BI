/**
 * Read-only wall surface: renders the document at its design resolution and
 * scales it to the available box. Used by the viewer, the public page and the
 * list/template thumbnails, so all three show the same picture.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import type { ScreenConfig } from '../../api/screens';
import { sortedComponents } from '../../features/screen/model';
import ScreenWidget from './ScreenWidget';

export type ScaleMode = 'fit' | 'width' | 'actual';

export interface ScreenSurfaceProps {
  config: ScreenConfig;
  mode?: 'edit' | 'view';
  className?: string;
  /** Rendered above the components (watermark, empty hint, …). */
  overlay?: ReactNode;
  filterValues?: Record<string, string>;
  onFilterChange?: (filterId: string, value: string) => void;
  refreshKey?: number;
}

/** Resolves the scale factor for a fixed design size inside a container. */
export function useFitScale(
  container: React.RefObject<HTMLElement | null>,
  design: { width: number; height: number },
  mode: ScaleMode,
  override?: number,
): number {
  const [scale, setScale] = useState(override ?? 1);

  useEffect(() => {
    if (override !== undefined) {
      setScale(override);
      return undefined;
    }
    const element = container.current;
    if (!element) return undefined;
    const measure = () => {
      const rect = element.getBoundingClientRect();
      if (!rect.width || !rect.height) return;
      const sx = rect.width / design.width;
      const sy = rect.height / design.height;
      setScale(mode === 'width' ? sx : mode === 'actual' ? 1 : Math.min(sx, sy));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [container, design.width, design.height, mode, override]);

  return scale;
}

/** Diagonal watermark repeated across the canvas. */
export function Watermark({ text }: { text: string }) {
  return (
    <div className="scr-watermark" aria-hidden>
      {Array.from({ length: 40 }, (_, index) => (
        <span key={index}>{text}</span>
      ))}
    </div>
  );
}

export default function ScreenSurface({
  config,
  mode = 'view',
  className,
  overlay,
  filterValues,
  onFilterChange,
  refreshKey,
}: ScreenSurfaceProps) {
  const wrapperRef = useRef<HTMLDivElement | null>(null);
  const { width, height, scaleMode, background } = config.canvas;
  const scale = useFitScale(wrapperRef, { width, height }, scaleMode);
  const components = useMemo(() => sortedComponents(config.components), [config.components]);

  return (
    <div ref={wrapperRef} className={`scr-surface ${className ?? ''}`}>
      <div
        className="scr-surface__stage"
        style={{
          width,
          height,
          transform: `scale(${scale})`,
          transformOrigin: 'top left',
          background,
        }}
      >
        {components
          .filter(component => component.visible !== false)
          .map(component => (
            <div
              key={component.id}
              className="scr-surface__item"
              style={{
                left: component.x,
                top: component.y,
                width: component.w,
                height: component.h,
                zIndex: component.z,
                transform: component.rotate ? `rotate(${component.rotate}deg)` : undefined,
              }}
            >
              <ScreenWidget
                component={component}
                mode={mode}
                filterValues={filterValues}
                onFilterChange={
                  onFilterChange ? (_filter, value) => onFilterChange(component.id, value) : undefined
                }
                refreshKey={refreshKey}
              />
            </div>
          ))}
        {overlay}
      </div>
    </div>
  );
}
