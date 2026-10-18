/**
 * Async SQL Lab transport.
 *
 * Queries are submitted with `run_async: true`; the server queues them and
 * reports progress on the `/api/v1/ws` channel (see backend/internal/api/ws.go
 * for the full protocol). The hook keeps one socket for the whole page:
 *
 *   {"type":"connected","user_id":3}
 *   {"type":"query_update","query_id":12,"status":"running","row_count":0}
 *   {"type":"query_result","query_id":12,"status":"success","rowcount":42}
 *   {"type":"error","query_id":12,"status":"failed","message":"..."}
 *
 * When the socket cannot be established (proxy without `ws: true`, offline,
 * expired token …) callers fall back to polling `/sqllab/{id}/results/`.
 */
import { useCallback, useEffect, useRef, useState } from 'react';
import { endpoints, tokenStore } from '../../api/client';

export type QueryEventType =
  | 'connected'
  | 'query_update'
  | 'query_result'
  | 'error'
  | 'subscribed'
  | 'pong';

export interface QueryEvent {
  type: QueryEventType;
  query_id?: number;
  status?: string;
  row_count?: number;
  rowcount?: number;
  duration_ms?: number;
  message?: string;
  results_path?: string;
  user_id?: number;
}

/** Lifecycle states reported by the query table. */
export const TERMINAL_STATES = new Set(['success', 'failed', 'cancelled']);

function socketUrl(): string | null {
  const token = tokenStore.access;
  if (!token) return null;
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${window.location.host}${endpoints.apiBase}${endpoints.sqllabSocket}?token=${encodeURIComponent(token)}`;
}

export interface QuerySocket {
  /** True once the server acknowledged the upgrade. */
  connected: boolean;
  /** Registers interest in a query id (the server echoes `subscribed`). */
  subscribe: (queryId: number) => void;
}

/**
 * Opens the async query channel and forwards every server frame to `onEvent`.
 * `onEvent` is read through a ref so callers may pass an inline closure.
 */
export function useQuerySocket(onEvent: (event: QueryEvent) => void): QuerySocket {
  const [connected, setConnected] = useState(false);
  const socketRef = useRef<WebSocket | null>(null);
  const handlerRef = useRef(onEvent);
  const retryRef = useRef(0);
  const closedRef = useRef(false);

  handlerRef.current = onEvent;

  useEffect(() => {
    closedRef.current = false;
    let pingTimer: number | undefined;
    let retryTimer: number | undefined;

    const connect = () => {
      const url = socketUrl();
      if (!url || closedRef.current) return;
      let socket: WebSocket;
      try {
        socket = new WebSocket(url);
      } catch {
        return;
      }
      socketRef.current = socket;

      socket.onopen = () => {
        retryRef.current = 0;
        setConnected(true);
        pingTimer = window.setInterval(() => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: 'ping' }));
          }
        }, 25000);
      };

      socket.onmessage = event => {
        try {
          const parsed = JSON.parse(String(event.data)) as QueryEvent;
          if (parsed?.type) handlerRef.current(parsed);
        } catch {
          // Ignore frames that are not JSON (there should be none).
        }
      };

      const teardown = () => {
        setConnected(false);
        if (pingTimer) window.clearInterval(pingTimer);
        pingTimer = undefined;
        socketRef.current = null;
        // Exponential backoff capped at 15s; the polling fallback keeps the
        // UI usable while the socket is down.
        if (closedRef.current) return;
        retryRef.current = Math.min(retryRef.current + 1, 5);
        const delay = Math.min(1000 * 2 ** retryRef.current, 15000);
        retryTimer = window.setTimeout(connect, delay);
      };

      socket.onclose = teardown;
      socket.onerror = () => {
        try {
          socket.close();
        } catch {
          // Already closed.
        }
      };
    };

    connect();

    return () => {
      closedRef.current = true;
      if (pingTimer) window.clearInterval(pingTimer);
      if (retryTimer) window.clearTimeout(retryTimer);
      const socket = socketRef.current;
      socketRef.current = null;
      setConnected(false);
      if (socket) {
        socket.onclose = null;
        socket.onerror = null;
        socket.onmessage = null;
        try {
          socket.close();
        } catch {
          // Nothing to do.
        }
      }
    };
  }, []);

  const subscribe = useCallback((queryId: number) => {
    const socket = socketRef.current;
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'subscribe', query_id: queryId }));
    }
  }, []);

  return { connected, subscribe };
}
