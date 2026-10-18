import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { Provider } from 'react-redux';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import enUS from 'antd/locale/en_US';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { store } from './store';
import { getAntdTheme, getCssVars, applySystemThemes, SYSTEM_THEME_EVENT, type ThemeMode, type SystemThemeData } from './theme';
import { syncChartTheme } from './components/charts/utils';
import { I18nProvider, useI18n } from './i18n';
import { client } from './api/client';
import './styles/global.css';
import './styles/screen.css';

/* ---------- System theme bootstrap (GET /api/v1/theme/system) ----------
 * Mirrors superset-frontend: the resolved system default/dark themes are
 * fetched once at page load and re-fetched when an admin changes the system
 * slots, so admin-configured themes apply live without a full reload. */
interface SystemThemeResponse {
  result: {
    default: { json_data?: SystemThemeData | null };
    dark: { json_data?: SystemThemeData | null };
  };
}

function fetchSystemThemes(): Promise<void> {
  return client
    .get<SystemThemeResponse>('/theme/system')
    .then(res => {
      applySystemThemes(res.result?.default?.json_data ?? null, res.result?.dark?.json_data ?? null);
    })
    .catch(() => {
      // Theme administration endpoint unavailable: keep built-in tokens.
    });
}

/* ---------- Theme context ---------- */
const THEME_KEY = 'zg_theme';

interface ThemeContextValue {
  mode: ThemeMode;
  toggle: () => void;
  setMode: (m: ThemeMode) => void;
}

const ThemeContext = createContext<ThemeContextValue>({
  mode: 'light',
  toggle: () => undefined,
  setMode: () => undefined,
});

function readTheme(): ThemeMode {
  const stored = localStorage.getItem(THEME_KEY) as ThemeMode | null;
  if (stored === 'dark' || stored === 'light') return stored;
  // Superset ships light-only by default; dark stays an explicit opt-in.
  return 'light';
}

function injectThemeToDom(mode: ThemeMode) {
  const root = document.documentElement;
  root.setAttribute('data-theme', mode);
  const vars = getCssVars(mode);
  Object.entries(vars).forEach(([key, value]) => {
    root.style.setProperty(key, value);
  });
  root.style.colorScheme = mode;
}

function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<ThemeMode>(readTheme);
  const [systemTick, setSystemTick] = useState(0);

  // Sync the shared chart palette before children render, so a theme switch
  // (which re-renders the tree) builds options with the new colours.
  syncChartTheme(mode);

  useEffect(() => {
    injectThemeToDom(mode);
    localStorage.setItem(THEME_KEY, mode);
  }, [mode, systemTick]);

  // Load admin-defined system themes and re-apply them when an admin changes
  // the system slots (ThemeList dispatches the event after set/unset).
  useEffect(() => {
    void fetchSystemThemes().then(() => setSystemTick(t => t + 1));
    const onChange = () => {
      void fetchSystemThemes().then(() => setSystemTick(t => t + 1));
    };
    window.addEventListener(SYSTEM_THEME_EVENT, onChange);
    return () => window.removeEventListener(SYSTEM_THEME_EVENT, onChange);
  }, []);

  const setMode = useCallback((m: ThemeMode) => setModeState(m), []);
  const toggle = useCallback(
    () => setModeState(prev => (prev === 'light' ? 'dark' : 'light')),
    [],
  );

  // systemTick re-renders the tree after the fetched system themes land.
  const value = useMemo<ThemeContextValue>(
    () => ({ mode, toggle, setMode }),
    [mode, toggle, setMode, systemTick],
  );
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  return useContext(ThemeContext);
}

/* ---------- antd + router ---------- */
const ANTD_LOCALES = { zh: zhCN, en: enUS } as const;

function ThemedRoot() {
  const { lang } = useI18n();
  const { mode } = useTheme();
  // Rebuild the antd locale bundle only when the language actually changes.
  const antdLocale = useMemo(() => ANTD_LOCALES[lang], [lang]);
  return (
    <ConfigProvider
      theme={getAntdTheme(mode)}
      locale={antdLocale}
      componentSize="middle"
    >
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </ConfigProvider>
  );
}

const container = document.getElementById('root');
if (!container) {
  throw new Error('Root container #root not found');
}

createRoot(container).render(
  <StrictMode>
    <Provider store={store}>
      <I18nProvider>
        <ThemeProvider>
          <ThemedRoot />
        </ThemeProvider>
      </I18nProvider>
    </Provider>
  </StrictMode>,
);
