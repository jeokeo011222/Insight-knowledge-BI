import { useMemo } from 'react';
import type { CSSProperties } from 'react';

export interface WatermarkProps {
  /** Repeated text — usually "<user> <timestamp>". */
  text: string;
  /** Fill opacity of the glyphs; keep it low enough to read charts through. */
  opacity?: number;
  /** Cell size in px. */
  gap?: number;
  /** Rotation in degrees. */
  rotate?: number;
  className?: string;
  style?: CSSProperties;
}

function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

/**
 * Tiled security watermark.
 *
 * Implemented as a repeating SVG background instead of hundreds of DOM nodes:
 * it stays crisp at any zoom, costs one paint, and — unlike a positioned
 * overlay — it cannot be scrolled away from the content it protects.
 */
export default function Watermark({
  text,
  opacity = 0.06,
  gap = 220,
  rotate = -22,
  className,
  style,
}: WatermarkProps) {
  const backgroundImage = useMemo(() => {
    const cell = Math.max(120, gap);
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${cell}" height="${cell}">
  <g transform="translate(${cell / 2} ${cell / 2}) rotate(${rotate})">
    <text x="0" y="0" text-anchor="middle" dominant-baseline="middle"
      font-family="-apple-system, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif"
      font-size="13" fill="currentColor" fill-opacity="${opacity}">${escapeXml(text)}</text>
  </g>
</svg>`;
    return `url("data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}")`;
  }, [text, opacity, gap, rotate]);

  if (!text.trim()) return null;

  return (
    <div
      className={`wb-watermark${className ? ` ${className}` : ''}`}
      aria-hidden
      style={{ backgroundImage, color: 'var(--wb-watermark-color, #000)', ...style }}
    />
  );
}

/** Builds the standard "<user> <yyyy-mm-dd hh:mm>" watermark text. */
export function watermarkText(user?: string | null): string {
  const now = new Date();
  const pad = (value: number) => String(value).padStart(2, '0');
  const stamp = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())} ${pad(now.getHours())}:${pad(now.getMinutes())}`;
  return user ? `${user} ${stamp}` : stamp;
}
