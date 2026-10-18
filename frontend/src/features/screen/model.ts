/**
 * Screen document model: canvas defaults, palette metadata, component
 * factories and normalisation.
 *
 * Everything here is pure data so the editor, the read-only view and the
 * list-page thumbnails all render the same document the same way.
 */
import type {
  ScreenCanvas,
  ScreenComponent,
  ScreenComponentType,
  ScreenConfig,
  ScreenTheme,
} from '../../api/screens';

export const SCREEN_VERSION = 1;

export interface ScreenThemePreset {
  key: string;
  /** i18n key suffix, resolved by the caller. */
  label: string;
  background: string;
  theme: Required<ScreenTheme>;
}

export const SCREEN_THEMES: ScreenThemePreset[] = [
  {
    key: 'tech-blue',
    label: 'blue',
    background:
      'radial-gradient(120% 90% at 50% 0%, #0d3563 0%, #071a33 52%, #030b17 100%)',
    theme: {
      primary: '#37a2ff',
      accent: '#00e5ff',
      text: '#e9f4ff',
      muted: '#7ea9cc',
      panel: 'rgba(13,53,99,0.42)',
      border: 'rgba(55,162,255,0.38)',
    },
  },
  {
    key: 'tech-cyan',
    label: 'cyan',
    background:
      'radial-gradient(120% 90% at 50% 0%, #063a4a 0%, #041f2b 55%, #020c12 100%)',
    theme: {
      primary: '#00e0d0',
      accent: '#59f2ff',
      text: '#e6fbff',
      muted: '#79b8c6',
      panel: 'rgba(6,58,74,0.42)',
      border: 'rgba(0,224,208,0.36)',
    },
  },
  {
    key: 'tech-green',
    label: 'green',
    background:
      'radial-gradient(120% 90% at 50% 0%, #073a2c 0%, #04221a 55%, #020d0a 100%)',
    theme: {
      primary: '#00e39a',
      accent: '#9dff6b',
      text: '#e7fff5',
      muted: '#77b39c',
      panel: 'rgba(7,58,44,0.42)',
      border: 'rgba(0,227,154,0.36)',
    },
  },
  {
    key: 'tech-gold',
    label: 'gold',
    background:
      'radial-gradient(120% 90% at 50% 0%, #3a2606 0%, #221604 55%, #0d0902 100%)',
    theme: {
      primary: '#ffb020',
      accent: '#ff7043',
      text: '#fff6e6',
      muted: '#c0a173',
      panel: 'rgba(58,38,6,0.42)',
      border: 'rgba(255,176,32,0.36)',
    },
  },
  {
    key: 'tech-violet',
    label: 'violet',
    background:
      'radial-gradient(120% 90% at 50% 0%, #221a4d 0%, #130f2e 55%, #07050f 100%)',
    theme: {
      primary: '#8b7bff',
      accent: '#38e8ff',
      text: '#f0ecff',
      muted: '#9a8fc4',
      panel: 'rgba(34,26,77,0.45)',
      border: 'rgba(139,123,255,0.38)',
    },
  },
];

export function themePreset(key: string | undefined): ScreenThemePreset {
  return SCREEN_THEMES.find(item => item.key === key) ?? SCREEN_THEMES[0];
}

export function defaultTheme(): Required<ScreenTheme> {
  return { ...SCREEN_THEMES[0].theme };
}

export function defaultCanvas(width = 1920, height = 1080): ScreenCanvas {
  return {
    width,
    height,
    background: SCREEN_THEMES[0].background,
    gridSize: 8,
    showGrid: true,
    scaleMode: 'fit',
    theme: SCREEN_THEMES[0].key,
    padding: 40,
  };
}

export function emptyConfig(width = 1920, height = 1080): ScreenConfig {
  return {
    version: SCREEN_VERSION,
    canvas: defaultCanvas(width, height),
    theme: defaultTheme(),
    components: [],
    filters: [],
    interaction: { refreshSec: 0, watermark: false, showToolbar: false },
  };
}

/* --------------------------------------------------------------- palette */

export interface PaletteItem {
  type: ScreenComponentType;
  /** i18n key, e.g. `screen.widget.kpi`. */
  labelKey: string;
  icon: string;
}

export interface PaletteGroup {
  key: string;
  labelKey: string;
  items: PaletteItem[];
}

export const SCREEN_PALETTE: PaletteGroup[] = [
  {
    key: 'data',
    labelKey: 'screen.palette.data',
    items: [
      { type: 'kpi', labelKey: 'screen.widget.kpi', icon: 'number' },
      { type: 'chart', labelKey: 'screen.widget.chart', icon: 'chart' },
      { type: 'table', labelKey: 'screen.widget.table', icon: 'table' },
      { type: 'progress', labelKey: 'screen.widget.progress', icon: 'progress' },
      { type: 'list', labelKey: 'screen.widget.list', icon: 'list' },
    ],
  },
  {
    key: 'text',
    labelKey: 'screen.palette.text',
    items: [
      { type: 'text', labelKey: 'screen.widget.text', icon: 'text' },
      { type: 'marquee', labelKey: 'screen.widget.marquee', icon: 'marquee' },
      { type: 'clock', labelKey: 'screen.widget.clock', icon: 'clock' },
      { type: 'richtext', labelKey: 'screen.widget.richtext', icon: 'rich' },
    ],
  },
  {
    key: 'media',
    labelKey: 'screen.palette.media',
    items: [
      { type: 'image', labelKey: 'screen.widget.image', icon: 'image' },
      { type: 'video', labelKey: 'screen.widget.video', icon: 'video' },
      { type: 'iframe', labelKey: 'screen.widget.iframe', icon: 'iframe' },
    ],
  },
  {
    key: 'decor',
    labelKey: 'screen.palette.decor',
    items: [
      { type: 'border', labelKey: 'screen.widget.border', icon: 'border' },
      { type: 'decoration', labelKey: 'screen.widget.decoration', icon: 'decor' },
      { type: 'shape', labelKey: 'screen.widget.shape', icon: 'shape' },
    ],
  },
  {
    key: 'interactive',
    labelKey: 'screen.palette.interactive',
    items: [
      { type: 'filter', labelKey: 'screen.widget.filter', icon: 'filter' },
      { type: 'group', labelKey: 'screen.widget.group', icon: 'group' },
    ],
  },
];

export const COMPONENT_LABEL_KEY: Record<ScreenComponentType, string> = {
  kpi: 'screen.widget.kpi',
  chart: 'screen.widget.chart',
  table: 'screen.widget.table',
  text: 'screen.widget.text',
  marquee: 'screen.widget.marquee',
  clock: 'screen.widget.clock',
  image: 'screen.widget.image',
  video: 'screen.widget.video',
  iframe: 'screen.widget.iframe',
  border: 'screen.widget.border',
  decoration: 'screen.widget.decoration',
  progress: 'screen.widget.progress',
  shape: 'screen.widget.shape',
  richtext: 'screen.widget.richtext',
  filter: 'screen.widget.filter',
  group: 'screen.widget.group',
  list: 'screen.widget.list',
};

/* ------------------------------------------------------------- factories */

let seq = 0;

/** Short, collision-safe id (no crypto dependency, deterministic enough). */
export function uid(prefix = 'c'): string {
  seq += 1;
  return `${prefix}_${Date.now().toString(36)}${seq.toString(36)}${Math.random()
    .toString(36)
    .slice(2, 6)}`;
}

/** Deterministic demo series so a freshly dropped chart is never empty. */
export function demoSeries(
  n: number,
  base: number,
  amp: number,
  seed = 7,
): number[] {
  let x = (seed * 2654435761 + 12345) >>> 0;
  return Array.from({ length: n }, () => {
    x = (x * 1664525 + 1013904223) >>> 0;
    const r = (x % 1000) / 1000;
    return Math.round((base + (r - 0.4) * amp) * 10) / 10;
  });
}

const MONTHS = ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'];
const WEEKDAYS = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];

interface ComponentSeed {
  w: number;
  h: number;
  props: Record<string, unknown>;
}

function seedFor(type: ScreenComponentType, theme: Required<ScreenTheme>): ComponentSeed {
  switch (type) {
    case 'kpi':
      return {
        w: 300,
        h: 120,
        props: {
          label: '指标名称',
          value: '12,345',
          unit: '',
          trend: 8.2,
          color: theme.primary,
          spark: demoSeries(12, 40, 30, 3),
          panel: theme.panel,
          border: theme.border,
          muted: theme.muted,
        },
      };
    case 'chart':
      return {
        w: 600,
        h: 320,
        props: {
          title: '图表标题',
          kind: 'bar',
          categories: MONTHS.slice(0, 6),
          series: [{ name: '指标', data: demoSeries(6, 400, 260, 9) }],
          color: theme.primary,
          palette: [theme.primary, theme.accent, '#ffd166', '#ff7a7a', '#8b7bff'],
          legend: false,
          panel: theme.panel,
          border: theme.border,
          muted: theme.muted,
          text: theme.text,
        },
      };
    case 'table':
      return {
        w: 620,
        h: 320,
        props: {
          title: '明细排行',
          columns: ['排名', '名称', '数值'],
          rows: [
            ['1', '示例 A', '8,642'],
            ['2', '示例 B', '7,128'],
            ['3', '示例 C', '6,486'],
            ['4', '示例 D', '5,204'],
          ],
          color: theme.primary,
          text: theme.text,
          muted: theme.muted,
          panel: theme.panel,
          border: theme.border,
        },
      };
    case 'progress':
      return {
        w: 380,
        h: 120,
        props: {
          label: '目标完成率',
          value: 68,
          max: 100,
          color: theme.primary,
          showPercent: true,
          text: theme.text,
          muted: theme.muted,
          panel: theme.panel,
          border: theme.border,
        },
      };
    case 'list':
      return {
        w: 420,
        h: 300,
        props: {
          title: '实时排行',
          items: [
            { label: '示例项目 A', value: 8620 },
            { label: '示例项目 B', value: 7240 },
            { label: '示例项目 C', value: 5860 },
            { label: '示例项目 D', value: 4280 },
            { label: '示例项目 E', value: 2640 },
          ],
          color: theme.primary,
          text: theme.text,
          muted: theme.muted,
          panel: theme.panel,
          border: theme.border,
        },
      };
    case 'text':
      return {
        w: 420,
        h: 60,
        props: {
          text: '文本内容',
          fontSize: 24,
          color: theme.text,
          align: 'left',
          bold: false,
          letterSpacing: 0,
          lineHeight: 1.4,
        },
      };
    case 'marquee':
      return {
        w: 900,
        h: 40,
        props: {
          text: '滚动播报：这里是需要循环播放的实时信息。',
          speed: 60,
          fontSize: 14,
          color: theme.muted,
        },
      };
    case 'clock':
      return {
        w: 280,
        h: 60,
        props: { format: 'datetime', fontSize: 20, color: theme.text, mutedColor: theme.muted },
      };
    case 'image':
      return {
        w: 400,
        h: 260,
        props: { src: '', fit: 'cover', opacity: 1, radius: 4, color: theme.muted },
      };
    case 'video':
      return {
        w: 480,
        h: 270,
        props: { src: '', autoplay: true, loop: true, muted: true, controls: false },
      };
    case 'iframe':
      return {
        w: 640,
        h: 360,
        props: { src: '', title: '嵌入页面', color: theme.muted },
      };
    case 'border':
      return {
        w: 600,
        h: 320,
        props: { variant: 'tech-1', color: theme.primary, title: '面板标题', text: theme.text },
      };
    case 'decoration':
      return {
        w: 1920,
        h: 1080,
        props: { variant: 'grid', color: theme.primary, opacity: 0.1 },
      };
    case 'shape':
      return {
        w: 240,
        h: 4,
        props: { shape: 'rect', fill: theme.border, radius: 0, opacity: 1 },
      };
    case 'richtext':
      return {
        w: 520,
        h: 180,
        props: {
          html: '<p style="font-size:16px">富文本支持<b>加粗</b>、<span style="color:#37a2ff">着色</span>与列表</p>',
          color: theme.text,
        },
      };
    case 'filter':
      return {
        w: 260,
        h: 56,
        props: {
          label: '区域',
          kind: 'select',
          options: ['全部', '华东', '华北', '华南', '西部'],
          defaultValue: '全部',
          color: theme.primary,
          text: theme.text,
          panel: theme.panel,
          border: theme.border,
        },
      };
    case 'group':
      return { w: 800, h: 400, props: { title: '分组' } };
    default:
      return { w: 300, h: 160, props: {} };
  }
}

export function createComponent(
  type: ScreenComponentType,
  canvas: ScreenCanvas,
  theme: Required<ScreenTheme>,
  at?: { x: number; y: number },
): ScreenComponent {
  const seed = seedFor(type, theme);
  const id = uid(type);
  const x = at?.x ?? Math.round((canvas.width - seed.w) / 2);
  const y = at?.y ?? Math.round((canvas.height - seed.h) / 2);
  return {
    id,
    type,
    name: COMPONENT_LABEL_KEY[type].replace('screen.widget.', ''),
    x: Math.max(0, Math.round(x)),
    y: Math.max(0, Math.round(y)),
    w: seed.w,
    h: seed.h,
    rotate: 0,
    z: 100,
    locked: false,
    visible: true,
    props: seed.props,
    style: {},
  };
}

/* ---------------------------------------------------------- normalisation */

function num(v: unknown, fallback: number): number {
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) ? n : fallback;
}

/** Fills every missing field so a hand-edited document cannot crash a render. */
export function normalizeConfig(raw: unknown): ScreenConfig {
  const base = emptyConfig();
  if (!raw || typeof raw !== 'object') return base;
  const doc = raw as Partial<ScreenConfig>;
  const canvas: ScreenCanvas = {
    ...base.canvas,
    ...(doc.canvas ?? {}),
    width: Math.max(320, num(doc.canvas?.width, base.canvas.width)),
    height: Math.max(180, num(doc.canvas?.height, base.canvas.height)),
    gridSize: Math.max(1, num(doc.canvas?.gridSize, 8)),
  };
  const components = Array.isArray(doc.components) ? doc.components : [];
  return {
    version: num(doc.version, SCREEN_VERSION),
    canvas,
    theme: { ...defaultTheme(), ...(doc.theme ?? {}) },
    components: components
      .filter((c): c is ScreenComponent => Boolean(c) && typeof c === 'object')
      .map((c, index) => ({
        id: typeof c.id === 'string' && c.id ? c.id : uid('fix'),
        type: (c.type ?? 'text') as ScreenComponentType,
        name: typeof c.name === 'string' && c.name ? c.name : `组件 ${index + 1}`,
        x: num(c.x, 0),
        y: num(c.y, 0),
        w: Math.max(1, num(c.w, 200)),
        h: Math.max(1, num(c.h, 120)),
        rotate: num(c.rotate, 0),
        z: num(c.z, 100 + index),
        locked: Boolean(c.locked),
        visible: c.visible !== false,
        props: (c.props ?? {}) as Record<string, unknown>,
        style: (c.style ?? {}) as Record<string, unknown>,
        chart: c.chart ?? null,
        dataset: c.dataset ?? null,
        children: Array.isArray(c.children) ? c.children : undefined,
      })),
    filters: Array.isArray(doc.filters) ? doc.filters : [],
    interaction: {
      refreshSec: num(doc.interaction?.refreshSec, 0),
      watermark: Boolean(doc.interaction?.watermark),
      showToolbar: Boolean(doc.interaction?.showToolbar),
      autoRotate: Boolean(doc.interaction?.autoRotate),
      rotateSec: num(doc.interaction?.rotateSec, 15),
    },
    meta: doc.meta ?? {},
  };
}

/* -------------------------------------------------------------- utilities */

export function sortedComponents(components: ScreenComponent[]): ScreenComponent[] {
  return [...components].sort((a, b) => a.z - b.z || a.id.localeCompare(b.id));
}

export function clampComponent(
  component: ScreenComponent,
  canvas: ScreenCanvas,
): ScreenComponent {
  const w = Math.min(Math.max(8, component.w), canvas.width);
  const h = Math.min(Math.max(8, component.h), canvas.height);
  return {
    ...component,
    w,
    h,
    x: Math.min(Math.max(0, component.x), Math.max(0, canvas.width - w)),
    y: Math.min(Math.max(0, component.y), Math.max(0, canvas.height - h)),
  };
}

export function duplicateComponent(component: ScreenComponent): ScreenComponent {
  return {
    ...component,
    id: uid(component.type),
    name: `${component.name} 副本`,
    x: component.x + 16,
    y: component.y + 16,
    locked: false,
    props: JSON.parse(JSON.stringify(component.props ?? {})) as Record<string, unknown>,
    chart: component.chart ? { ...component.chart } : null,
  };
}

/** Coerces a loose props bag for renderers. */
export const prop = {
  str(props: Record<string, unknown>, key: string, fallback = ''): string {
    const v = props[key];
    return typeof v === 'string' ? v : typeof v === 'number' ? String(v) : fallback;
  },
  num(props: Record<string, unknown>, key: string, fallback = 0): number {
    const v = props[key];
    const n = typeof v === 'number' ? v : Number(v);
    return Number.isFinite(n) ? n : fallback;
  },
  bool(props: Record<string, unknown>, key: string, fallback = false): boolean {
    const v = props[key];
    return typeof v === 'boolean' ? v : fallback;
  },
  arr<T>(props: Record<string, unknown>, key: string): T[] {
    const v = props[key];
    return Array.isArray(v) ? (v as T[]) : [];
  },
};
