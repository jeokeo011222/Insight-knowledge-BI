/**
 * Favourites (收藏).
 *
 * The backend exposes a generic `/favstar/` collection (create / delete / list)
 * keyed by `class_name` + `obj_id`, where class_name is "slice" for charts and
 * "dashboard" for boards. Loading it once and sharing the result through a tiny
 * store avoids one request per list row — a 50-row chart list would otherwise
 * fire 50 requests just to paint the stars.
 */
import { useCallback, useEffect, useState } from 'react';
import { client, endpoints } from '../../api/client';

export type FavoriteClass = 'slice' | 'dashboard';

export interface FavoriteRow {
  id: number;
  class_name: FavoriteClass | string;
  obj_id: number;
  dttm?: string | null;
}

interface State {
  loaded: boolean;
  loading: boolean;
  rows: FavoriteRow[];
  error: string | null;
}

const state: State = { loaded: false, loading: false, rows: [], error: null };
const listeners = new Set<() => void>();
let inFlight: Promise<void> | null = null;

function emit(): void {
  listeners.forEach(listener => listener());
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function favoriteKey(className: string, objId: number): string {
  return `${className}:${objId}`;
}

/** Loads the favourites once; later calls reuse the cached rows. */
export function loadFavorites(force = false): Promise<void> {
  if (inFlight) return inFlight;
  if (state.loaded && !force) return Promise.resolve();
  state.loading = true;
  state.error = null;
  emit();
  inFlight = client
    .get<{ result: FavoriteRow[] }>(endpoints.favorites)
    .then(response => {
      state.rows = response.result ?? [];
      state.loaded = true;
    })
    .catch(error => {
      state.error = error instanceof Error ? error.message : String(error);
    })
    .finally(() => {
      state.loading = false;
      inFlight = null;
      emit();
    });
  return inFlight;
}

/** Adds or removes a favourite and keeps the cache in sync. */
export async function toggleFavorite(
  className: FavoriteClass,
  objId: number,
): Promise<boolean> {
  const existing = state.rows.find(
    row => row.class_name === className && row.obj_id === objId,
  );
  if (existing) {
    await client.delete(`${endpoints.favorites}${existing.id}`);
    state.rows = state.rows.filter(row => row.id !== existing.id);
    emit();
    return false;
  }
  const response = await client.post<{ result: FavoriteRow }>(endpoints.favorites, {
    class_name: className,
    obj_id: objId,
  });
  if (response.result) {
    state.rows = [response.result, ...state.rows];
    emit();
  }
  return true;
}

/** Synchronous snapshot (call after loadFavorites resolves). */
export function getFavoriteRows(): FavoriteRow[] {
  return state.rows;
}

/** Snapshot used by components. */
function useSnapshot(): State {
  const [, setVersion] = useState(0);
  useEffect(() => {
    const unsubscribe = subscribe(() => setVersion(value => value + 1));
    void loadFavorites();
    return unsubscribe;
  }, []);
  return { ...state };
}

/** Favourite state of one object. */
export function useFavorite(className: FavoriteClass, objId: number | undefined) {
  const snapshot = useSnapshot();
  const [busy, setBusy] = useState(false);
  const starred = Boolean(
    objId && snapshot.rows.some(row => row.class_name === className && row.obj_id === objId),
  );

  const toggle = useCallback(async () => {
    if (!objId) return;
    setBusy(true);
    try {
      await toggleFavorite(className, objId);
    } finally {
      setBusy(false);
    }
  }, [className, objId]);

  return { starred, busy, toggle };
}

/** All favourites, newest first (used by the welcome page). */
export function useFavoriteRows() {
  const snapshot = useSnapshot();
  return snapshot;
}
