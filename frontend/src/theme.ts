import type { ThemeConfig } from 'antd';
import { theme as antdTheme } from 'antd';

/**
 * Design tokens — single source of truth.
 * Colors, spacing and radii live here; antd ConfigProvider and global.css
 * both consume them.
 */

/* -------- Base palette (brand 「智观 BI」: tech blue → cyan accent) -------- */
const BRAND = {
  primary: '#1b5fd9',
  primaryHover: '#1750c0',
  primaryActive: '#12409b',
  primaryLight: '#e9f0fe',
  /** Cyan accent used only in gradients / highlight strokes. */
  accent: '#22c3e6',
  /** `r, g, b` of `primary`, so CSS can build translucent brand tints. */
  primaryRgb: '27, 95, 217',
} as const;

/* -------- Status colors (shared; darkAlgorithm adjusts them) -------- */
const STATUS = {
  success: '#28a06b',
  warning: '#e8a417',
  error: '#dc3f4e',
  info: '#22c3e6',
} as const;

/* -------- Font stack (CJK first, Latin fallback) -------- */
const FONT = {
  primary:
    '-apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  mono:
    '"JetBrains Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
} as const;

/* -------- Light theme tokens -------- */
const LIGHT_TOKENS = {
  colorBgBase: '#ffffff',
  colorBgLayout: '#f4f5f7',
  colorBgSubtle: '#fafbfc',
  colorBgCard: '#ffffff',
  /** Panels that sit on top of the layout (dashboard tiles, rails, toolbars). */
  colorBgElevated: '#ffffff',
  colorText: '#232629',
  colorTextSecondary: '#5b6369',
  colorTextTertiary: '#8a9299',
  colorBorder: '#e6e8eb',
  colorBorderStrong: '#c7ccd0',
  colorBorderSubtle: '#f0f1f3',
  shadowCard: '0 1px 3px rgba(0,0,0,0.04), 0 4px 12px -4px rgba(0,0,0,0.08)',
  shadowCardHover: `0 6px 20px -6px rgba(${BRAND.primaryRgb},0.22)`,
} as const;

/* -------- Dark theme tokens (deep-blue tinted) -------- */
const DARK_TOKENS = {
  colorBgBase: '#0f1d2e',
  colorBgLayout: '#0a1420',
  colorBgSubtle: '#15283b',
  colorBgCard: '#15283b',
  /** Panels that sit on top of the layout (dashboard tiles, rails, toolbars). */
  colorBgElevated: '#16293e',
  colorText: '#e8f0f6',
  colorTextSecondary: '#a9bcc8',
  colorTextTertiary: '#5f7a8d',
  colorBorder: '#1f3550',
  colorBorderStrong: '#2c4666',
  colorBorderSubtle: '#122135',
  shadowCard: '0 1px 3px rgba(0,0,0,0.35), 0 4px 12px -4px rgba(5,30,60,0.55)',
  shadowCardHover: `0 6px 20px -6px rgba(${BRAND.primaryRgb},0.3)`,
} as const;

export type ThemeMode = 'light' | 'dark';

/** Window event fired after an admin changes a system theme slot, so the
 * ThemeProvider re-fetches GET /api/v1/theme/system and applies it live. */
export const SYSTEM_THEME_EVENT = 'system-theme-changed';

/* -------- Admin-defined system themes (GET /api/v1/theme/system) --------
 * Mirrors superset-frontend: the resolved system default / dark themes from
 * the theme administration feature override the built-in token baseline, so
 * an admin-configured theme drives the whole UI without a redeploy.
 */
export interface SystemThemeData {
  token?: Record<string, unknown>;
  components?: Record<string, unknown>;
  algorithm?: string | string[];
}

let SYSTEM_THEMES: { light: SystemThemeData | null; dark: SystemThemeData | null } = {
  light: null,
  dark: null,
};

export function applySystemThemes(light: SystemThemeData | null, dark: SystemThemeData | null) {
  SYSTEM_THEMES = { light: normalizeSystemTheme(light), dark: normalizeSystemTheme(dark) };
}

function normalizeSystemTheme(data: unknown): SystemThemeData | null {
  if (!data || typeof data !== 'object') return null;
  const obj = data as Record<string, unknown>;
  return {
    token:
      obj.token && typeof obj.token === 'object' ? (obj.token as Record<string, unknown>) : undefined,
    components:
      obj.components && typeof obj.components === 'object'
        ? (obj.components as Record<string, unknown>)
        : undefined,
    algorithm:
      typeof obj.algorithm === 'string' || Array.isArray(obj.algorithm)
        ? (obj.algorithm as string | string[])
        : undefined,
  };
}

function systemThemeFor(mode: ThemeMode): SystemThemeData | null {
  return mode === 'dark' ? SYSTEM_THEMES.dark : SYSTEM_THEMES.light;
}

/** Builds the antd ThemeConfig for a mode. */
export function getAntdTheme(mode: ThemeMode): ThemeConfig {
  const system = systemThemeFor(mode);
  const tokens = mode === 'dark' ? DARK_TOKENS : LIGHT_TOKENS;
  const customToken = (system?.token ?? {}) as Record<string, unknown>;
  const base: ThemeConfig = {
    algorithm: mode === 'dark' ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: {
      colorPrimary: BRAND.primary,
      colorInfo: STATUS.info,
      colorSuccess: STATUS.success,
      colorWarning: STATUS.warning,
      colorError: STATUS.error,
      colorBgBase: tokens.colorBgBase,
      colorBgLayout: tokens.colorBgLayout,
      colorText: tokens.colorText,
      colorTextSecondary: tokens.colorTextSecondary,
      colorTextTertiary: tokens.colorTextTertiary,
      colorBorder: tokens.colorBorder,
      colorBorderSecondary: tokens.colorBorderSubtle,
      borderRadius: 6,
      borderRadiusLG: 8,
      fontFamily: FONT.primary,
      fontSize: 13,
      controlHeight: 34,
      boxShadow: tokens.shadowCard,
      wireframe: false,
      ...customToken,
    } as ThemeConfig['token'],
    components: {
      ...((system?.components ?? {}) as ThemeConfig['components']),
      Layout: {
        headerBg: tokens.colorBgBase,
        headerHeight: 60,
        headerPadding: '0 16px',
        siderBg: tokens.colorBgBase,
        bodyBg: tokens.colorBgLayout,
        triggerBg: tokens.colorBgBase,
        triggerColor: tokens.colorTextSecondary,
      },
      Menu: {
        itemBg: 'transparent',
        itemHeight: 40,
        itemBorderRadius: 6,
        itemMarginInline: 8,
        itemMarginBlock: 3,
        itemColor: tokens.colorTextSecondary,
        itemHoverBg: mode === 'dark' ? 'rgba(255,255,255,0.05)' : `rgba(${BRAND.primaryRgb}, 0.06)`,
        itemHoverColor: tokens.colorText,
        itemSelectedBg: `rgba(${BRAND.primaryRgb}, ${mode === 'dark' ? 0.24 : 0.1})`,
        itemSelectedColor: BRAND.primary,
        subMenuItemBg: 'transparent',
        iconSize: 15,
        collapsedIconSize: 17,
        groupTitleColor: tokens.colorTextTertiary,
        groupTitleFontSize: 11,
      },
      Table: {
        headerBg: mode === 'dark' ? '#2a2d33' : '#f6f7f9',
        headerColor: tokens.colorTextSecondary,
        headerSplitColor: tokens.colorBorder,
        rowHoverBg: mode === 'dark' ? `rgba(${BRAND.primaryRgb},0.1)` : `rgba(${BRAND.primaryRgb},0.05)`,
        borderColor: tokens.colorBorderSubtle,
        cellPaddingBlock: 9,
        cellPaddingInline: 12,
        fontSize: 13,
      },
      Card: {
        headerBg: 'transparent',
        headerFontSize: 14,
        paddingLG: 16,
        borderRadiusLG: 8,
      },
      Button: {
        primaryShadow: 'none',
        defaultShadow: 'none',
        fontWeight: 500,
        borderRadius: 6,
      },
      Input: {
        paddingBlock: 5,
        borderRadius: 6,
      },
      Tabs: {
        horizontalItemPadding: '10px 0',
        horizontalItemGutter: 24,
        titleFontSize: 13,
      },
      Tag: {
        defaultBg: mode === 'dark' ? '#2a2d33' : '#f4f5f7',
        borderRadiusSM: 4,
      },
      Modal: {
        titleFontSize: 15,
      },
      Drawer: {
        paddingLG: 20,
      },
      Segmented: {
        itemSelectedBg: tokens.colorBgBase,
        trackBg: mode === 'dark' ? '#2a2d33' : '#eef0f3',
      },
      Breadcrumb: {
        itemColor: tokens.colorTextTertiary,
        lastItemColor: tokens.colorTextSecondary,
        separatorColor: tokens.colorBorderStrong,
        fontSize: 13,
      },
    },
  };
  return base;
}

/** CSS variable map, injected by main.tsx into :root / [data-theme="dark"]. */
export function getCssVars(mode: ThemeMode): Record<string, string> {
  const tokens = mode === 'dark' ? DARK_TOKENS : LIGHT_TOKENS;
  const vars: Record<string, string> = {
    '--brand-primary': BRAND.primary,
    '--brand-primary-hover': BRAND.primaryHover,
    '--brand-primary-active': BRAND.primaryActive,
    '--brand-primary-light': BRAND.primaryLight,
    '--brand-accent': BRAND.accent,
    '--brand-primary-rgb': BRAND.primaryRgb,
    '--brand-gradient': `linear-gradient(135deg, ${BRAND.primary} 0%, ${BRAND.accent} 100%)`,

    '--color-bg-base': tokens.colorBgBase,
    '--color-bg-layout': tokens.colorBgLayout,
    '--color-bg-subtle': tokens.colorBgSubtle,
    '--color-bg-card': tokens.colorBgCard,
    '--color-bg-elevated': tokens.colorBgElevated,

    '--color-text': tokens.colorText,
    '--color-text-secondary': tokens.colorTextSecondary,
    '--color-text-tertiary': tokens.colorTextTertiary,

    '--color-border': tokens.colorBorder,
    '--color-border-strong': tokens.colorBorderStrong,
    '--color-border-subtle': tokens.colorBorderSubtle,

    '--color-success': STATUS.success,
    '--color-warning': STATUS.warning,
    '--color-error': STATUS.error,
    '--color-info': STATUS.info,

    '--font-primary': FONT.primary,
    '--font-mono': FONT.mono,

    '--shadow-card': tokens.shadowCard,
    '--shadow-card-hover': tokens.shadowCardHover,

    '--radius-sm': '4px',
    '--radius-md': '6px',
    '--radius-lg': '8px',

    '--sidebar-width': '240px',
    '--sidebar-collapsed-width': '64px',
    '--header-height': '60px',
  };

  // Admin-defined system tokens override the mapped CSS variables, keeping
  // global.css-driven surfaces in sync with the antd ConfigProvider.
  const custom = systemThemeFor(mode)?.token ?? {};
  const varOverrides: Record<string, string> = {
    colorPrimary: '--brand-primary',
    colorInfo: '--color-info',
    colorSuccess: '--color-success',
    colorWarning: '--color-warning',
    colorError: '--color-error',
    colorBgBase: '--color-bg-base',
    colorBgLayout: '--color-bg-layout',
    colorText: '--color-text',
    colorTextSecondary: '--color-text-secondary',
    colorTextTertiary: '--color-text-tertiary',
    colorBorder: '--color-border',
    fontFamily: '--font-primary',
    fontFamilyCode: '--font-mono',
  };
  Object.entries(varOverrides).forEach(([tokenKey, cssVar]) => {
    const value = custom[tokenKey];
    if (typeof value === 'string' && value) vars[cssVar] = value;
  });
  return vars;
}

/** Backwards-compatible default export for `import theme from './theme'`. */
export const theme = getAntdTheme('light');
export default theme;
