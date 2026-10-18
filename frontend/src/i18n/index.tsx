import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import dayjs from 'dayjs';
import 'dayjs/locale/zh-cn';
import { en } from './messages.en';
import { zh, type TranslationKey } from './messages.zh';

export type { TranslationKey };
export type Locale = 'zh' | 'en';

export type TranslateVars = Record<string, string | number>;

const dictionaries: Record<Locale, Record<TranslationKey, string>> = { zh, en };

/** Public storage key; `zg_locale` is read once for backwards compatibility. */
const LANG_KEY = 'zg_locale';
const LEGACY_LANG_KEY = 'superset_lang';

const HTML_LANG: Record<Locale, string> = { zh: 'zh-CN', en: 'en' };
const DAYJS_LANG: Record<Locale, string> = { zh: 'zh-cn', en: 'en' };

/**
 * Dev-only guard: the zh pack is the source of truth for `TranslationKey`, so
 * a missing English key would surface as a raw key at runtime. Fail loudly in
 * the console instead.
 */
if (import.meta.env.DEV) {
  const zhKeys = Object.keys(zh);
  const enKeys = Object.keys(en);
  const missingInEn = zhKeys.filter(key => !(key in en));
  const missingInZh = enKeys.filter(key => !(key in zh));
  if (missingInEn.length || missingInZh.length) {
    console.error(
      '[i18n] message packs are out of sync',
      { missingInEn, missingInZh },
    );
  }
}

function interpolate(raw: string, vars?: TranslateVars): string {
  if (!vars) return raw;
  return Object.entries(vars).reduce(
    (acc, [key, value]) => acc.replace(new RegExp(`\\{${key}\\}`, 'g'), String(value)),
    raw,
  );
}

/**
 * Locale used by non-React call sites (api client, redux slices). Kept in sync
 * by `I18nProvider`.
 */
let activeLang: Locale = 'zh';

/** Translate outside of React (module scope, thunks, error constructors). */
export function tGlobal(key: TranslationKey, vars?: TranslateVars): string {
  const raw = dictionaries[activeLang][key] ?? dictionaries.zh[key] ?? key;
  return interpolate(raw, vars);
}

/** Reports whether a key exists in the Chinese source pack. */
export function hasTranslation(key: string): boolean {
  return key in dictionaries.zh;
}

export function getLang(): Locale {
  return activeLang;
}

function readLang(): Locale {
  const stored = localStorage.getItem(LANG_KEY) ?? localStorage.getItem(LEGACY_LANG_KEY);
  return stored === 'en' ? 'en' : 'zh';
}

/** Keep <html lang>, the tab title and dayjs in sync with the active locale. */
function applyLang(lang: Locale) {
  activeLang = lang;
  document.documentElement.setAttribute('lang', HTML_LANG[lang]);
  document.title = dictionaries[lang]['brand.document_title'];
  dayjs.locale(DAYJS_LANG[lang]);
}

interface I18nValue {
  /** Active locale (`zh` | `en`). */
  lang: Locale;
  setLang: (lang: Locale) => void;
  /** `locale` / `setLocale` are kept as aliases for existing call sites. */
  locale: Locale;
  setLocale: (locale: Locale) => void;
  /** Translate a key, supporting `{count}` style placeholders. */
  t: (key: TranslationKey, vars?: TranslateVars) => string;
}

const I18nContext = createContext<I18nValue>({
  lang: 'zh',
  setLang: () => undefined,
  locale: 'zh',
  setLocale: () => undefined,
  t: (key, vars) => tGlobal(key, vars),
});

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Locale>(readLang);

  const setLang = useCallback((next: Locale) => {
    localStorage.setItem(LANG_KEY, next);
    localStorage.removeItem(LEGACY_LANG_KEY);
    applyLang(next);
    setLangState(next);
  }, []);

  // Apply on mount and whenever the locale changes so that <html lang> and
  // dayjs never lag behind the React tree.
  useEffect(() => {
    applyLang(lang);
  }, [lang]);

  const t = useCallback(
    (key: TranslationKey, vars?: TranslateVars) => {
      const raw = dictionaries[lang][key] ?? dictionaries.zh[key] ?? key;
      return interpolate(raw, vars);
    },
    [lang],
  );

  const value = useMemo<I18nValue>(
    () => ({ lang, setLang, locale: lang, setLocale: setLang, t }),
    [lang, setLang, t],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  return useContext(I18nContext);
}
