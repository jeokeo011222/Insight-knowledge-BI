import type { ConnectionForm } from '../api/types';

export interface ConnectionValues {
  engine?: string;
  host?: string;
  port?: number | null;
  database?: string;
  schema?: string;
  username?: string;
  password?: string;
  path?: string;
  /**
   * Query string of the stored URI (`sslmode=disable`, `charset=utf8`, …).
   * No engine form exposes it, but it must survive an edit: the backend
   * rebuilds the URI from these fields, and dropping it silently changed
   * connection semantics on the first save.
   */
  query?: string;
  sqlalchemy_uri?: string;
}

/** Trims the leading and trailing slashes of a URI path segment. */
function segment(value?: string): string {
  return (value ?? '').trim().replace(/^\/+|\/+$/g, '');
}

/** Escapes a credential the same way Go's url.UserPassword does. */
function credential(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, ch =>
    `%${ch.charCodeAt(0).toString(16).toUpperCase()}`,
  );
}

/**
 * Turns whatever was typed or pasted into the "database file" input into a bare
 * filesystem path. Every other Superset screen talks in SQLAlchemy URIs, so a
 * user naturally pastes `duckdb:///D:/data/stock.duckdb`; appending the engine
 * prefix to that already-prefixed value produced
 * `duckdb:///duckdb:///D:/data/stock.duckdb`, which the backend reported as
 * "the database file does not exist" while the visible path looked right.
 */
export function normaliseFilePath(raw: string): string {
  let path = (raw ?? '').trim();
  if (path.length >= 2) {
    const quote = path[0];
    if ((quote === '"' || quote === "'") && path[path.length - 1] === quote) {
      path = path.slice(1, -1).trim();
    }
  }
  // Repeat: the pasted value can carry the scheme more than once.
  for (;;) {
    const match = /^[a-z][a-z0-9+.-]*:\/\//i.exec(path);
    if (!match) break;
    path = path.slice(match[0].length);
    if (path.startsWith('//')) {
      // "duckdb:////data/app.db" is the absolute POSIX form: keep one slash.
      path = path.slice(1);
    } else if (/^\/[a-zA-Z]:[\\/]/.test(path)) {
      // "duckdb:///D:/data/app.db": the leading slash is not part of the drive.
      path = path.slice(1);
    }
  }
  return path.replace(/\\/g, '/');
}

/**
 * Assembles the SQLAlchemy URI shown in the create-database preview.
 *
 * The backend re-assembles the URI from the same structured fields before
 * saving, so this function only drives the preview. It mirrors
 * connectors.BuildURI deliberately: both places must agree, otherwise the
 * preview would lie about what is stored.
 */
export function assembleURI(form: ConnectionForm | null, values: ConnectionValues): string {
  const engine = (values.engine ?? '').trim();
  if (!form || !engine) return '';

  if (form.kind === 'uri') {
    return (values.sqlalchemy_uri ?? values.database ?? '').trim();
  }

  if (form.kind === 'file') {
    const raw = normaliseFilePath(values.path ?? values.database ?? '');
    if (!raw) return '';
    if (raw === ':memory:') return `${engine}://`;
    return `${engine}:///${raw}`;
  }

  const host = (values.host ?? '').trim();
  if (!host) return '';

  const portDefault = form.fields.find(field => field.name === 'port')?.default;
  const port = values.port ?? (typeof portDefault === 'number' ? portDefault : undefined);
  const hostPart = port ? `${host}:${port}` : host;

  const username = (values.username ?? '').trim();
  const password = values.password ?? '';
  let auth = '';
  if (username) {
    auth = password ? `${credential(username)}:${credential(password)}@` : `${credential(username)}@`;
  }

  const database = segment(values.database);
  // The query string has no input of its own, but the preview has to show it:
  // the backend keeps it when rebuilding the URI.
  const query = (values.query ?? '').trim().replace(/^\?/, '');
  const suffix = query ? `?${query}` : '';
  if (form.kind === 'catalog') {
    const schema = segment(values.schema);
    return `${engine}://${auth}${hostPart}/${database}${schema ? `/${schema}` : ''}${suffix}`;
  }
  return `${engine}://${auth}${hostPart}${database ? `/${database}` : ''}${suffix}`;
}

/**
 * True when a URI carries a placeholder instead of a real password.
 *
 * The API masks secrets with `XXXXXXXXXX` (see the backend's redact helper)
 * while the UI masks them with `••••`, so both spellings — plus the `****`
 * used by older records — have to be recognised. Sending a masked URI back
 * would overwrite the stored password with the placeholder.
 */
export function isMaskedURI(uri: string | undefined): boolean {
  const text = (uri ?? '').trim();
  if (!text) return false;
  return text.includes('••••') || text.includes('****') || text.includes('XXXXXXXXXX');
}

/** Masks the password inside a URI so it can be shown safely. */
export function maskURI(uri: string): string {
  const at = uri.lastIndexOf('@');
  if (at < 0) return uri;
  const schemeEnd = uri.indexOf('://');
  if (schemeEnd < 0 || schemeEnd + 3 > at) return uri;
  const head = uri.slice(0, schemeEnd + 3);
  const authority = uri.slice(schemeEnd + 3, at);
  const colon = authority.indexOf(':');
  if (colon < 0) return uri;
  return `${head}${authority.slice(0, colon)}:••••@${uri.slice(at + 1)}`;
}
