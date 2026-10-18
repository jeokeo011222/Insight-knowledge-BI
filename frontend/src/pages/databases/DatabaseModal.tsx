import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import {
  Alert,
  Button,
  Col,
  Collapse,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Space,
  Switch,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd';
import {
  ApiOutlined,
  ArrowLeftOutlined,
  CheckCircleFilled,
  CloseCircleFilled,
  DatabaseOutlined,
  EditOutlined,
  SearchOutlined,
  SwapOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { client, endpoints, errorMessage } from '../../api/client';
import type {
  ConnectionField,
  ConnectionForm,
  ConnectionTestResult,
  Database,
  EngineInfo,
} from '../../api/types';
import { hasTranslation, useI18n } from '../../i18n';
import type { TranslationKey } from '../../i18n';
import { assembleURI, isMaskedURI, maskURI, normaliseFilePath } from '../../utils/connectionUri';
import type { ConnectionValues } from '../../utils/connectionUri';

/** Which step of the wizard the modal is showing. */
type Step = 'engine' | 'form';
/** Superset splits the database form into these three panes. */
type FormTab = 'basic' | 'advanced' | 'sqllab';

interface DatabaseFormValues extends ConnectionValues {
  database_name: string;
  verbose_name?: string;
  expose_in_sqllab: boolean;
  allow_ctas: boolean;
  allow_cvas: boolean;
  allow_dml: boolean;
  allow_run_async: boolean;
  allow_file_upload: boolean;
  allow_multi_schema_metadata_fetch: boolean;
  explore_database: boolean;
  impersonate_user: boolean;
  is_managed_externally: boolean;
  cache_timeout?: number;
  force_ctas_schema?: string;
  server_cert?: string;
  extra?: string;
  encrypted_extra?: string;
}

interface TestState {
  ok: boolean;
  message: string;
  latency?: number;
}

export interface DatabaseModalProps {
  open: boolean;
  editing: Database | null;
  engines: EngineInfo[];
  onClose: () => void;
  onSaved: (created: Database | null) => void;
}

/** Picker group order, mirroring connectors.Categories(). */
const CATEGORY_ORDER = ['relational', 'analytics', 'cloud', 'file', 'nosql', 'other'];

/** Which engines need a protocol specific DSN rather than host/port fields. */
const KIND_HINT_KEY: Record<string, string> = {
  file: 'database.form.kind_hint_file',
  uri: 'database.form.kind_hint_uri',
  catalog: 'database.form.kind_hint_catalog',
};

/** Booleans that only govern SQL Lab behaviour. */
const SQLLAB_SWITCHES = [
  'expose_in_sqllab',
  'allow_ctas',
  'allow_cvas',
  'allow_dml',
  'allow_run_async',
  'allow_file_upload',
  'explore_database',
] as const;

/** Booleans about identity, metadata and trust. */
const ADVANCED_SWITCHES = [
  'impersonate_user',
  'allow_multi_schema_metadata_fetch',
  'is_managed_externally',
] as const;

const DEFAULT_VALUES: Partial<DatabaseFormValues> = {
  expose_in_sqllab: true,
  allow_ctas: false,
  allow_cvas: false,
  allow_dml: false,
  allow_run_async: false,
  allow_file_upload: false,
  allow_multi_schema_metadata_fetch: false,
  explore_database: true,
  impersonate_user: false,
  is_managed_externally: false,
  cache_timeout: 0,
};

/**
 * Seeds the form. These values travel as `initialValues` rather than through a
 * `setFieldsValue` call in an effect: the modal mounts its Form lazily, so the
 * effect that opened the modal ran before the form existed and every write was
 * dropped ("Instance created by `useForm` is not connected to any Form
 * element") — which is why the edit dialog opened with an empty name, an empty
 * display name and no connection fields. `initialValues` is applied by the Form
 * itself on mount, so the order no longer matters.
 */
function initialValuesFor(database: Database | null): Partial<DatabaseFormValues> {
  if (!database) return { ...DEFAULT_VALUES };
  return {
    database_name: database.database_name,
    verbose_name: database.verbose_name ?? '',
    expose_in_sqllab: database.expose_in_sqllab,
    allow_ctas: database.allow_ctas,
    allow_cvas: database.allow_cvas,
    allow_dml: database.allow_dml,
    allow_run_async: database.allow_run_async,
    allow_file_upload: Boolean(database.allow_file_upload),
    allow_multi_schema_metadata_fetch: Boolean(database.allow_multi_schema_metadata_fetch),
    explore_database: database.explore_database !== false,
    impersonate_user: Boolean(database.impersonate_user),
    is_managed_externally: Boolean(database.is_managed_externally),
    cache_timeout: database.cache_timeout ?? 0,
    force_ctas_schema: database.force_ctas_schema ?? '',
    server_cert: database.server_cert ?? '',
    extra: database.extra && database.extra !== '{}' ? database.extra : '',
  };
}

/** What /database/{id}/connection reports about a stored URI. */
interface ParsedConnection {
  engine: string;
  kind?: string;
  host?: string;
  port?: number;
  database?: string;
  schema?: string;
  path?: string;
  username?: string;
  query?: string;
}

/** Parses a JSON textarea, returning undefined for blank input. */
function parseJsonField(raw: string | undefined, label: string): unknown {
  const text = (raw ?? '').trim();
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`${label}: invalid JSON`);
  }
}

/**
 * Merges value sources without letting an `undefined` in a later source erase a
 * real value from an earlier one.
 *
 * antd keeps a store entry for every registered Form.Item, including the hidden
 * `engine` input. When the engine was written with setFieldsValue before the
 * form was mounted that entry comes back as `undefined`, so a plain spread
 * silently dropped the engine; the URI was then assembled from nothing and the
 * UI reported "the connection is incomplete" while every visible field was
 * filled in.
 */
function mergeDefined(
  ...sources: Array<Record<string, unknown> | undefined>
): DatabaseFormValues {
  const out: Record<string, unknown> = {};
  for (const source of sources) {
    if (!source) continue;
    for (const [key, value] of Object.entries(source)) {
      if (value !== undefined) out[key] = value;
    }
  }
  return out as unknown as DatabaseFormValues;
}

export default function DatabaseModal({
  open,
  editing,
  engines,
  onClose,
  onSaved,
}: DatabaseModalProps) {
  const { t } = useI18n();
  const [form] = Form.useForm<DatabaseFormValues>();

  /**
   * The engine catalogue. The parent warms it, but the modal cannot render a
   * connection form without it, so it also fetches the catalogue itself: the
   * parent's `loadEngines()` is fire-and-forget, and relying on it left the
   * connection pane empty whenever Edit was the first thing clicked.
   */
  const [engineList, setEngineList] = useState<EngineInfo[]>(engines);

  useEffect(() => {
    if (engines.length) setEngineList(engines);
  }, [engines]);

  useEffect(() => {
    if (!open || engineList.length) return;
    let cancelled = false;
    client
      .get<{ result: EngineInfo[] }>(endpoints.databaseAvailable)
      .then(response => {
        if (!cancelled) setEngineList(response.result ?? []);
      })
      .catch(() => {
        /* The picker shows an empty state; no need to shout about it here. */
        if (!cancelled) setCatalogueFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [open, engineList.length]);

  const [step, setStep] = useState<Step>('engine');
  const [tab, setTab] = useState<FormTab>('basic');
  const [engineQuery, setEngineQuery] = useState('');
  const [activeForm, setActiveForm] = useState<ConnectionForm | null>(null);
  const [formValues, setFormValues] = useState<ConnectionValues>({});
  const [manualURI, setManualURI] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<TestState | null>(null);

  /**
   * The values the Form is built from. The Form is only mounted once this is
   * non-null, so `initialValues` carries the whole seed (identity fields plus
   * the unpacked connection) and no write can land before the form exists.
   * That ordering bug is what emptied the edit dialog: the engine was known,
   * the URI was parsed, and every field still rendered blank.
   */
  const [seed, setSeed] = useState<Record<string, unknown> | null>(null);

  /** Set when the catalogue cannot be fetched, so editing still has a way out. */
  const [catalogueFailed, setCatalogueFailed] = useState(false);

  /**
   * The engine picked in step one. It is kept in a ref as well as in state
   * because the hidden `engine` Form.Item can report `undefined` before the
   * form mounts, and losing it made a correctly filled form look incomplete.
   */
  const engineRef = useRef('');

  /** The engine to build a URI from: ref first, state as the fallback. */
  const activeEngine = (): string =>
    (engineRef.current || formValues.engine || '').trim();

  const tt = (key: string, params?: Record<string, string | number>): string =>
    hasTranslation(key) ? t(key as TranslationKey, params) : key;

  /** Resets everything to the "create" starting point. */
  const resetState = () => {
    setStep('engine');
    setTab('basic');
    setEngineQuery('');
    setActiveForm(null);
    setFormValues({});
    setManualURI(false);
    setTest(null);
    setPendingConnection(null);
    setSeed(null);
    engineRef.current = '';
    // No form.resetFields() here: at this point the Form is not mounted yet
    // (step is still the engine picker), so the call would be dropped with a
    // "not connected to any Form element" warning. `initialValues` seeds it.
  };

  /**
   * Resolves an engine into the values its form should start with. It returns
   * them instead of writing them into the form, so the caller can seed a Form
   * that is about to mount and no write ever races the mount.
   */
  const seedForEngine = (
    engine: string,
    prefill?: Record<string, unknown>,
  ): { connection: ConnectionForm | null; values: ConnectionValues } => {
    const info = engineList.find(item => item.engine === engine);
    const connection = info?.connection ?? null;
    const values: ConnectionValues = { engine };
    if (connection) {
      for (const field of connection.fields) {
        if (field.default !== undefined && field.default !== '') {
          (values as Record<string, unknown>)[field.name] = field.default;
        }
      }
    }
    if (prefill) {
      for (const [key, value] of Object.entries(prefill)) {
        if (value !== undefined && value !== null) {
          (values as Record<string, unknown>)[key] = value;
        }
      }
    }
    return { connection, values };
  };

  /** Opens the form for an engine picked in step one. */
  const chooseEngine = (engine: string) => {
    const { connection, values } = seedForEngine(engine);
    // Switching engines from inside the form resets the connection pane only:
    // a name the user already typed has nothing to do with the driver choice.
    // (Read the store only when the form is mounted, otherwise antd warns.)
    const typed = step === 'form' ? (form.getFieldsValue(true) as Record<string, unknown>) : {};
    const identity: Record<string, unknown> = {};
    for (const key of [
      'database_name',
      'verbose_name',
      ...SQLLAB_SWITCHES,
      ...ADVANCED_SWITCHES,
      'cache_timeout',
      'force_ctas_schema',
      'server_cert',
      'extra',
      'encrypted_extra',
    ]) {
      if (typed[key] !== undefined) identity[key] = typed[key];
    }
    engineRef.current = engine;
    setActiveForm(connection);
    setTest(null);
    setManualURI(false);
    setSeed({ ...DEFAULT_VALUES, ...identity, ...(values as Record<string, unknown>) });
    setFormValues(values);
    setStep('form');
  };

  /**
   * The parsed connection of the row being edited. It is held here until the
   * engine catalogue is available: unpacking it earlier found no matching
   * engine, produced no connection schema, and the pane fell back to "pick an
   * engine" even though the row plainly had one.
   */
  const [pendingConnection, setPendingConnection] = useState<ParsedConnection | null>(null);

  // Opening the modal decides the entry step: create starts at the engine
  // picker (like Superset's "Connect a database" grid), editing goes straight
  // to the form once the stored URI has been parsed.
  useEffect(() => {
    if (!open) return;
    if (!editing) {
      resetState();
      return;
    }
    setStep('form');
    setTab('basic');
    setTest(null);
    setManualURI(false);
    setPendingConnection(null);
    setSeed(null);
    setCatalogueFailed(false);

    let cancelled = false;
    client
      .get<{ result: ParsedConnection }>(endpoints.databaseConnection(editing.id))
      .then(response => {
        if (!cancelled) setPendingConnection(response.result);
      })
      .catch(() => {
        // Older connections may not be parseable; the raw URI editor is the
        // fallback so the user can still fix the connection.
        if (cancelled) return;
        setManualURI(true);
        setSeed({
          ...initialValuesFor(editing),
          engine: editing.backend,
          sqlalchemy_uri: editing.sqlalchemy_uri,
        });
        setFormValues({ engine: editing.backend, sqlalchemy_uri: editing.sqlalchemy_uri });
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing]);

  // Unpacking needs the engine's declared connection schema, which ships with
  // the catalogue, so the seed is only built once the catalogue is in. A failed
  // catalogue still produces a seed (raw URI editor) instead of a dead dialog.
  useEffect(() => {
    if (!open || !editing || !pendingConnection) return;
    if (!engineList.length && !catalogueFailed) return;

    const parsed = pendingConnection;
    const { connection, values } = seedForEngine(parsed.engine, {
      host: parsed.host,
      port: parsed.port || undefined,
      database: parsed.database,
      schema: parsed.schema,
      path: parsed.path,
      username: parsed.username,
      query: parsed.query || undefined,
    });
    // Engines with a protocol specific DSN (BigQuery, Snowflake, …) have no
    // host/port to unpack, so the stored URI is the only editable
    // representation. It is shown masked; leaving the mask untouched keeps the
    // stored secret (see handleSubmit).
    const uriOnly = parsed.kind === 'uri' || (!connection && !engineList.length);
    const seedValues: Record<string, unknown> = {
      ...initialValuesFor(editing),
      ...(values as Record<string, unknown>),
    };
    if (uriOnly) seedValues.sqlalchemy_uri = editing.sqlalchemy_uri;

    engineRef.current = parsed.engine;
    setActiveForm(connection);
    setManualURI(uriOnly);
    setSeed(seedValues);
    setFormValues(uriOnly
      ? { engine: parsed.engine, sqlalchemy_uri: editing.sqlalchemy_uri }
      : values);
    setPendingConnection(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, editing, pendingConnection, engineList, catalogueFailed]);

  const engineGroups = useMemo(() => {
    const needle = engineQuery.trim().toLowerCase();
    const filtered = needle
      ? engineList.filter(
          engine =>
            engine.engine.toLowerCase().includes(needle) ||
            (engine.engine_name ?? '').toLowerCase().includes(needle) ||
            (engine.driver ?? '').toLowerCase().includes(needle),
        )
      : engineList;
    const buckets = new Map<string, EngineInfo[]>();
    for (const engine of filtered) {
      const key = engine.category && CATEGORY_ORDER.includes(engine.category)
        ? engine.category
        : 'other';
      const list = buckets.get(key) ?? [];
      list.push(engine);
      buckets.set(key, list);
    }
    return CATEGORY_ORDER.filter(key => buckets.get(key)?.length).map(key => ({
      key,
      engines: (buckets.get(key) ?? []).sort((a, b) =>
        (a.engine_name || a.engine).localeCompare(b.engine_name || b.engine),
      ),
    }));
  }, [engineList, engineQuery]);

  const previewURI = useMemo(() => {
    // Reading the form before it is mounted would both warn and return {}.
    if (step !== 'form' || !seed) return '';
    const values = mergeDefined(
      formValues as Record<string, unknown>,
      form.getFieldsValue(true) as Record<string, unknown>,
    );
    if (!(values.engine ?? '').trim()) values.engine = engineRef.current || formValues.engine;
    return assembleURI(activeForm, values);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeForm, formValues, step, seed]);

  /**
   * Names the input that is actually still empty. "The connection is
   * incomplete" sent the user hunting through fields that were already filled
   * in, which is how a correct file path looked like the problem.
   */
  const connectionGapHint = (values: DatabaseFormValues): string => {
    if (!activeForm) return tt('database.modal.pick_engine_first');
    if (!(values.engine ?? '').trim()) return tt('database.modal.pick_engine_first');
    const raw = values as unknown as Record<string, unknown>;
    const missing = activeForm.fields.find(
      field => field.required && !String(raw[field.name] ?? '').trim(),
    );
    if (missing) return t('database.form.field_required', { label: fieldLabel(missing) });
    return t('database.form.connection_incomplete');
  };

  const fieldLabel = (field: ConnectionField): string =>
    hasTranslation(field.label_key) ? t(field.label_key as TranslationKey) : field.label;

  const fieldHelp = (field: ConnectionField): string | undefined => {
    if (field.help_key && hasTranslation(field.help_key)) return t(field.help_key as TranslationKey);
    return field.help || undefined;
  };

  /** Structured connection fields only — what the backend re-assembles the URI from. */
  const connectionPayload = (values: DatabaseFormValues): Record<string, unknown> => {
    const out: Record<string, unknown> = {};
    // The engine selection lives in React state as well as in a hidden input.
    // Reading it from state as a fallback keeps the payload complete even if
    // the hidden field was never registered (e.g. after switching tabs).
    const engine = (values.engine ?? '').trim() || activeEngine();
    if (engine) out.engine = engine;
    const keys = [
      'host',
      'database',
      'schema',
      'path',
      'username',
      'password',
      // Carried through untouched: the backend rebuilds the URI from these
      // fields, and without it `sslmode`/`charset` vanished on the first save.
      'query',
      'sqlalchemy_uri',
    ] as const;
    for (const key of keys) {
      const value = values[key];
      if (value !== undefined && value !== null && value !== '') out[key] = value;
    }
    if (values.port !== undefined && values.port !== null) out.port = values.port;
    return out;
  };

  const handleTest = async () => {
    try {
      const names = activeForm?.fields.map(field => field.name) ?? ['sqlalchemy_uri'];
      const values = await form.validateFields(names as (keyof DatabaseFormValues)[]);
      const merged = mergeDefined(
        formValues as Record<string, unknown>,
        form.getFieldsValue(true) as Record<string, unknown>,
        values as unknown as Record<string, unknown>,
      );
      if (!(merged.engine ?? '').trim()) merged.engine = activeEngine();
      const payload = connectionPayload(merged);
      // A masked URI is what the browser got for a stored secret. Probing with
      // it would fail on a fake password, so it never leaves the form.
      if (typeof payload.sqlalchemy_uri === 'string' && isMaskedURI(payload.sqlalchemy_uri)) {
        delete payload.sqlalchemy_uri;
      }

      // The URI is a convenience, not a requirement: the backend rebuilds it
      // from the structured fields, so a form that produced no preview URI is
      // still sent rather than blocked here. Blocking on the preview is what
      // turned a drifted React state into "the connection is incomplete" while
      // the path on screen was filled in.
      const uri = manualURI ? '' : assembleURI(activeForm, merged as ConnectionValues);
      if (!manualURI && uri) {
        const passwordTyped = Boolean((merged.password ?? '').trim());
        if (!editing || passwordTyped) {
          payload.sqlalchemy_uri = uri;
        }
      }
      // Only a payload that says nothing at all about the connection is worth
      // stopping for; the backend names the field that is missing.
      if (
        !payload.sqlalchemy_uri &&
        !payload.engine &&
        !payload.path &&
        !payload.host
      ) {
        message.warning(connectionGapHint(merged));
        return;
      }
      // Editing an existing connection: the stored password is reused
      // server-side, so testing does not require retyping it.
      if (editing) payload.database_id = editing.id;
      setTesting(true);
      const response = await client.post<ConnectionTestResult>(
        endpoints.databaseTestConnection,
        payload,
      );
      const ok = response.message === 'Seems OK!';
      setTest({
        ok,
        message: response.message || (ok ? 'Seems OK!' : t('database.form.test_fail')),
        latency: response.result?.latency_ms,
      });
      if (ok) message.success(t('database.form.test_ok'));
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      setTest({ ok: false, message: errorMessage(error) });
    } finally {
      setTesting(false);
    }
  };

  const handleSubmit = async () => {
    let values: DatabaseFormValues;
    try {
      values = await form.validateFields();
    } catch {
      return;
    }

    const merged = mergeDefined(
      formValues as Record<string, unknown>,
      // The whole store, not just the fields the active tab happens to expose:
      // unregistered entries (a hidden `engine`, values typed on another tab)
      // would otherwise be undefined and would blank out a good payload.
      form.getFieldsValue(true) as Record<string, unknown>,
      values as unknown as Record<string, unknown>,
    );
    if (!(merged.engine ?? '').trim()) merged.engine = activeEngine();
    const engine = (merged.engine ?? '').trim();

    if (!editing && !engine && !manualURI) {
      message.warning(t('database.list.engine_placeholder'));
      setStep('engine');
      return;
    }

    // The structured fields below are what the backend assembles the URI from,
    // so an empty preview URI is not a reason to refuse: it only means the
    // fields were read from a different place than the preview was.
    const structuredURI = manualURI ? '' : assembleURI(activeForm, merged as ConnectionValues);
    const structuredFields = connectionPayload(merged);
    if (
      !editing &&
      !manualURI &&
      !structuredURI &&
      !structuredFields.engine &&
      !structuredFields.path &&
      !structuredFields.host
    ) {
      message.warning(connectionGapHint(merged));
      return;
    }

    let extra: unknown;
    let encryptedExtra: unknown;
    try {
      extra = parseJsonField(merged.extra, t('database.modal.field.extra'));
      encryptedExtra = parseJsonField(
        merged.encrypted_extra,
        t('database.modal.field.encrypted_extra'),
      );
    } catch (error) {
      message.error(error instanceof Error ? error.message : String(error));
      return;
    }

    const shared = {
      database_name: merged.database_name,
      verbose_name: merged.verbose_name,
      expose_in_sqllab: merged.expose_in_sqllab,
      allow_ctas: merged.allow_ctas,
      allow_cvas: merged.allow_cvas,
      allow_dml: merged.allow_dml,
      allow_run_async: merged.allow_run_async,
      allow_file_upload: merged.allow_file_upload,
      allow_multi_schema_metadata_fetch: merged.allow_multi_schema_metadata_fetch,
      explore_database: merged.explore_database,
      impersonate_user: merged.impersonate_user,
      is_managed_externally: merged.is_managed_externally,
      cache_timeout: merged.cache_timeout ?? 0,
      force_ctas_schema: merged.force_ctas_schema || undefined,
      server_cert: merged.server_cert || undefined,
      extra,
      encrypted_extra: encryptedExtra,
    };

    // A masked URI (•••• / XXXXXXXXXX) is what the browser received for a
    // stored secret. Sending it back would overwrite the real password, so it
    // is treated as "unchanged" and the backend keeps the stored value.
    const uriValue = (values.sqlalchemy_uri ?? '').trim();
    const uriIsMasked = isMaskedURI(uriValue);
    let uriPatch: Record<string, string> = {};
    if (manualURI) {
      if (uriValue && !uriIsMasked) uriPatch = { sqlalchemy_uri: uriValue };
    } else if (!editing && structuredURI) {
      uriPatch = { sqlalchemy_uri: structuredURI };
    }

    // Structured fields travel with the update too: editing the path of a file-
    // backed connection (or the host of a server one) has to rebuild the URI,
    // exactly like creating it does. The URI itself stays in uriPatch, which
    // already handles the masked-secret case.
    const structPayload = connectionPayload(merged);
    delete structPayload.sqlalchemy_uri;

    setSaving(true);
    try {
      if (editing) {
        await client.put(endpoints.database(editing.id), {
          ...shared,
          ...uriPatch,
          ...structPayload,
        });
        message.success(t('database.form.saved'));
        onSaved(null);
      } else {
        const response = await client.post<{ result: Database }>(endpoints.databases, {
          ...shared,
          ...uriPatch,
          ...structPayload,
        });
        message.success(t('database.form.created'));
        onSaved(response.result ?? null);
      }
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const renderControl = (field: ConnectionField): ReactNode => {
    if (field.type === 'password') {
      return <Input.Password autoComplete="new-password" placeholder={field.placeholder} />;
    }
    if (field.type === 'int') {
      return (
        <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder={field.placeholder} />
      );
    }
    if (field.name === 'path') {
      // The field wants a bare path, but the rest of the product speaks in
      // SQLAlchemy URIs, so pasting one is normal. Leave the input as the user
      // typed it while editing, then rewrite it into the path on blur so what
      // they see matches what is stored.
      return (
        <Input
          placeholder={field.placeholder}
          autoComplete="off"
          onBlur={event => {
            const cleaned = normaliseFilePath(event.target.value);
            if (cleaned === event.target.value) return;
            form.setFieldValue('path', cleaned);
            setFormValues(current => ({ ...current, path: cleaned }));
          }}
        />
      );
    }
    return <Input placeholder={field.placeholder} autoComplete="off" />;
  };

  const renderField = (field: ConnectionField): ReactNode => (
    <Form.Item
      key={field.name}
      name={field.name}
      label={fieldLabel(field)}
      rules={
        field.required
          ? [{ required: true, message: t('database.form.field_required', { label: fieldLabel(field) }) }]
          : undefined
      }
      extra={fieldHelp(field)}
    >
      {renderControl(field)}
    </Form.Item>
  );

  /** Renders one field group, pairing host/port and username/password side by side. */
  const renderGroup = (group: ConnectionField['group']): ReactNode[] => {
    const fields = (activeForm?.fields ?? []).filter(field => field.group === group);
    const nodes: ReactNode[] = [];
    for (let index = 0; index < fields.length; index += 1) {
      const field = fields[index];
      const next = fields[index + 1];
      const pairsWithPort = field.name === 'host' && next?.name === 'port';
      const pairsWithPassword = field.name === 'username' && next?.name === 'password';
      if (next && (pairsWithPort || pairsWithPassword)) {
        nodes.push(
          <Row gutter={12} key={field.name}>
            <Col span={pairsWithPort ? 16 : 12}>{renderField(field)}</Col>
            <Col span={pairsWithPort ? 8 : 12}>{renderField(next)}</Col>
          </Row>,
        );
        index += 1;
        continue;
      }
      nodes.push(renderField(field));
    }
    return nodes;
  };

  const renderSwitch = (
    name: string,
    labelKey: string,
    helpKey?: string,
  ): ReactNode => (
    <Row key={name} gutter={12} align="top" style={{ marginBottom: 12 }}>
      <Col flex="auto">
        <div style={{ fontWeight: 500 }}>{tt(labelKey)}</div>
        {helpKey ? <div className="cell-muted" style={{ fontSize: 12 }}>{tt(helpKey)}</div> : null}
      </Col>
      <Col flex="72px" style={{ textAlign: 'right' }}>
        <Form.Item name={name} valuePropName="checked" noStyle>
          <Switch size="small" />
        </Form.Item>
      </Col>
    </Row>
  );

  const renderEnginePicker = () => (
    <>
      <Alert
        type="info"
        showIcon
        icon={<DatabaseOutlined />}
        message={tt('database.modal.engine_subtitle')}
        style={{ marginBottom: 16 }}
      />
      <Input
        allowClear
        size="large"
        prefix={<SearchOutlined />}
        placeholder={tt('database.modal.engine_search')}
        value={engineQuery}
        onChange={event => setEngineQuery(event.target.value)}
        style={{ marginBottom: 16 }}
      />
      <div style={{ maxHeight: 420, overflowY: 'auto', paddingRight: 4 }}>
        {engineGroups.length ? (
          engineGroups.map(group => (
            <div key={group.key} style={{ marginBottom: 18 }}>
              <div className="tiny-label" style={{ marginBottom: 8 }}>
                {tt(`database.modal.category.${group.key}`)}
                <span className="cell-muted" style={{ marginLeft: 6 }}>
                  {group.engines.length}
                </span>
              </div>
              <div className="engine-grid">
                {group.engines.map(engine => (
                  <button
                    key={engine.engine}
                    type="button"
                    className="engine-card"
                    onClick={() => chooseEngine(engine.engine)}
                  >
                    <div className="engine-card__name">{engine.engine_name || engine.engine}</div>
                    <div className="engine-card__meta">
                      <span className="cell-mono">{engine.driver || engine.engine}</span>
                      {engine.connection?.kind ? (
                        <Tag bordered={false} className="engine-card__tag">
                          {tt(`database.modal.kind.${engine.connection.kind}`)}
                        </Tag>
                      ) : null}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          ))
        ) : (
          <div className="cell-muted" style={{ padding: 24, textAlign: 'center' }}>
            {tt('database.modal.no_engine_match')}
          </div>
        )}
      </div>
    </>
  );

  const renderBasic = () => (
    <>
      <div className="section-title">{tt('database.modal.section.identity')}</div>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item
            name="database_name"
            label={t('database.form.name')}
            rules={[{ required: true, message: t('database.form.name') }]}
            extra={tt('database.modal.name_help')}
          >
            <Input placeholder={t('database.form.name_placeholder')} />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item name="verbose_name" label={t('database.form.verbose_name')}>
            <Input placeholder={t('database.form.verbose_name_placeholder')} />
          </Form.Item>
        </Col>
      </Row>

      <div className="section-title">{t('database.form.connection_section')}</div>

      {activeForm && KIND_HINT_KEY[activeForm.kind] ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message={t(KIND_HINT_KEY[activeForm.kind] as TranslationKey)}
        />
      ) : null}

      {manualURI ? (
        <Form.Item
          name="sqlalchemy_uri"
          label={t('database.field.uri')}
          extra={tt('database.modal.manual_uri_help')}
        >
          <Input.TextArea rows={2} className="mono-block" placeholder="dialect+driver://user:password@host:port/database" />
        </Form.Item>
      ) : activeForm ? (
        <>
          {renderGroup('connection')}
          {renderGroup('auth')}
        </>
      ) : (
        <Empty4Engine
          text={tt('database.modal.pick_engine_first')}
          action={
            <Button size="small" onClick={() => setStep('engine')}>
              {tt('database.modal.change_engine')}
            </Button>
          }
        />
      )}

      <Form.Item name="engine" hidden>
        <Input />
      </Form.Item>

      {activeForm && !manualURI && activeForm.fields.some(field => field.group === 'advanced') ? (
        <Collapse
          ghost
          size="small"
          items={[
            {
              key: 'advanced',
              label: t('database.form.advanced_connection'),
              children: renderGroup('advanced'),
            },
          ]}
        />
      ) : null}

      <div style={{ marginBottom: 8 }}>
        <Space size={12}>
          <Button
            icon={<EditOutlined />}
            size="small"
            type="link"
            onClick={() => {
              setManualURI(next => !next);
              setTest(null);
            }}
          >
            {manualURI ? tt('database.modal.use_structured') : tt('database.modal.manual_uri')}
          </Button>
        </Space>
      </div>

      <div className="section-title">{t('database.form.uri_preview')}</div>
      <Typography.Paragraph
        className="mono-block"
        style={{ marginBottom: 8 }}
        copyable={
          previewURI
            ? { text: previewURI, tooltips: [t('common.actions.copy'), t('common.actions.copy')] }
            : false
        }
      >
        {previewURI ? maskURI(previewURI) : <span className="cell-muted">{t('database.form.uri_preview_empty')}</span>}
      </Typography.Paragraph>

      <Space style={{ marginBottom: 8 }}>
        <Button icon={<ThunderboltOutlined />} onClick={() => void handleTest()} loading={testing}>
          {t('common.actions.test_connection')}
        </Button>
        <span className="cell-muted">{t('database.form.test_hint')}</span>
      </Space>

      {test ? (
        <Alert
          type={test.ok ? 'success' : 'error'}
          showIcon
          icon={test.ok ? <CheckCircleFilled /> : <CloseCircleFilled />}
          message={test.message}
          description={
            test.ok && test.latency !== undefined
              ? t('database.form.latency', { ms: test.latency as number })
              : undefined
          }
        />
      ) : null}
    </>
  );

  const renderAdvanced = () => (
    <>
      <div className="section-title">{tt('database.modal.section.security')}</div>
      {ADVANCED_SWITCHES.map(name =>
        renderSwitch(
          name,
          `database.modal.field.${name}`,
          `database.modal.field.${name}_help`,
        ),
      )}

      <div className="section-title">{tt('database.modal.section.metadata')}</div>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item
            name="cache_timeout"
            label={t('database.form.cache_timeout')}
            extra={tt('database.modal.field.cache_timeout_help')}
          >
            <InputNumber min={0} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item
            name="force_ctas_schema"
            label={tt('database.modal.field.force_ctas_schema')}
            extra={tt('database.modal.field.force_ctas_schema_help')}
          >
            <Input placeholder="ctas_schema" />
          </Form.Item>
        </Col>
      </Row>

      <div className="section-title">{tt('database.modal.section.json')}</div>
      <Collapse
        ghost
        size="small"
        items={[
          {
            key: 'extra',
            label: t('database.modal.field.extra'),
            children: (
              <Form.Item
                name="extra"
                extra={tt('database.modal.field.extra_help')}
                style={{ marginBottom: 0 }}
              >
                <Input.TextArea rows={4} className="mono-block" placeholder='{"engine_params": {}}' />
              </Form.Item>
            ),
          },
          {
            key: 'encrypted_extra',
            label: t('database.modal.field.encrypted_extra'),
            children: (
              <Form.Item
                name="encrypted_extra"
                extra={tt('database.modal.field.encrypted_extra_help')}
                style={{ marginBottom: 0 }}
              >
                <Input.TextArea rows={4} className="mono-block" placeholder='{"private_key": "..."}' />
              </Form.Item>
            ),
          },
          {
            key: 'server_cert',
            label: t('database.modal.field.server_cert'),
            children: (
              <Form.Item name="server_cert" style={{ marginBottom: 0 }}>
                <Input.TextArea rows={4} className="mono-block" placeholder="-----BEGIN CERTIFICATE-----" />
              </Form.Item>
            ),
          },
        ]}
      />
    </>
  );

  const renderSqlLab = () => (
    <>
      <div className="section-title">{tt('database.modal.section.capabilities')}</div>
      <Alert
        type="warning"
        showIcon
        message={tt('database.modal.write_warning')}
        style={{ marginBottom: 16 }}
      />
      {SQLLAB_SWITCHES.map(name =>
        renderSwitch(name, `database.modal.field.${name}`, `database.modal.field.${name}_help`),
      )}
    </>
  );

  return (
    <Modal
      title={editing ? t('database.list.edit') : t('database.list.create')}
      open={open}
      onCancel={onClose}
      width={step === 'engine' ? 880 : 820}
      destroyOnHidden
      maskClosable={false}
      footer={
        <Space>
          {step === 'form' && !editing ? (
            <Button icon={<ArrowLeftOutlined />} onClick={() => setStep('engine')}>
              {tt('database.modal.back')}
            </Button>
          ) : null}
          <Button onClick={onClose}>{t('common.actions.cancel')}</Button>
          {step === 'form' ? (
            <Button type="primary" loading={saving} disabled={!seed} onClick={() => void handleSubmit()}>
              {editing ? t('common.actions.save') : t('common.actions.create')}
            </Button>
          ) : null}
        </Space>
      }
    >
      {step === 'engine' ? (
        renderEnginePicker()
      ) : !seed ? (
        <div className="cell-muted" style={{ padding: '28px 0', textAlign: 'center' }}>
          {t('common.state.loading')}
        </div>
      ) : (
        <Form
          form={form}
          layout="vertical"
          preserve={false}
          initialValues={seed}
          onValuesChange={(_, all) =>
            setFormValues(current => ({ ...current, ...(all as ConnectionValues) }))
          }
        >
          <div className="engine-banner">
            <Space size={10} align="center">
              <ApiOutlined />
              <strong>
                {editing
                  ? editing.database_name
                  : engineList.find(engine => engine.engine === formValues.engine)?.engine_name ||
                    formValues.engine ||
                    '—'}
              </strong>
              <Tag bordered={false}>{formValues.engine || editing?.backend || '—'}</Tag>
              {editing ? null : (
                <Button size="small" type="link" icon={<SwapOutlined />} onClick={() => setStep('engine')}>
                  {tt('database.modal.change_engine')}
                </Button>
              )}
            </Space>
          </div>

          <Tabs
            activeKey={tab}
            onChange={key => setTab(key as FormTab)}
            items={[
              { key: 'basic', label: tt('database.modal.tab.basic'), children: renderBasic() },
              { key: 'advanced', label: tt('database.modal.tab.advanced'), children: renderAdvanced() },
              { key: 'sqllab', label: tt('database.modal.tab.sqllab'), children: renderSqlLab() },
            ]}
          />
        </Form>
      )}
    </Modal>
  );
}

/** Small inline empty state for the connection pane. */
function Empty4Engine({ text, action }: { text: string; action?: ReactNode }) {
  return (
    <div
      style={{
        border: '1px dashed var(--ant-color-border, #d9d9d9)',
        borderRadius: 8,
        padding: '18px 16px',
        marginBottom: 12,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 12,
      }}
    >
      <span className="cell-muted">{text}</span>
      {action}
    </div>
  );
}
