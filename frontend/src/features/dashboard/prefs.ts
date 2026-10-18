/**
 * Per-browser display preferences for dashboards.
 *
 * These are view preferences, not dashboard content: they must not be written
 * into position_json (the layout is shared by every viewer), so they live in
 * localStorage and are read synchronously on first render.
 */
import { useCallback, useEffect, useState } from 'react';

const STORAGE_KEY = 'superset.dashboard.prefs';

export interface DashPrefs {
  /** Security watermark (user + timestamp) over the canvas. */
  watermark: boolean;
  /** Chart → chart linkage on click. */
  linked: boolean;
}

const DEFAULTS: DashPrefs = { watermark: false, linked: true };

function read(): DashPrefs {
  if (typeof window === 'undefined') return DEFAULTS;
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<DashPrefs>;
    return {
      watermark: typeof parsed.watermark === 'boolean' ? parsed.watermark : DEFAULTS.watermark,
      linked: typeof parsed.linked === 'boolean' ? parsed.linked : DEFAULTS.linked,
    };
  } catch {
    return DEFAULTS;
  }
}

function write(next: DashPrefs): void {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    // Private-mode quotas: the preference just does not persist.
  }
}

/** Reads and persists dashboard display preferences. */
export function useDashPrefs(): [DashPrefs, (patch: Partial<DashPrefs>) => void] {
  const [prefs, setPrefs] = useState<DashPrefs>(read);

  // Keep tabs in sync: toggling the watermark in one tab applies to the others.
  useEffect(() => {
    const onStorage = (event: StorageEvent) => {
      if (event.key === STORAGE_KEY) setPrefs(read());
    };
    window.addEventListener('storage', onStorage);
    return () => window.removeEventListener('storage', onStorage);
  }, []);

  const update = useCallback((patch: Partial<DashPrefs>) => {
    setPrefs(previous => {
      const next = { ...previous, ...patch };
      write(next);
      return next;
    });
  }, []);

  return [prefs, update];
}

/** Reads the preferences outside React (export/print paths). */
export function readDashPrefs(): DashPrefs {
  return read();
}
