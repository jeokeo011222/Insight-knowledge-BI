import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Divider,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApiOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  PlusOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { errorMessage } from '../../api/client';
import {
  createAgent,
  createProvider,
  createSkillPlugin,
  createToolPlugin,
  deleteAgent,
  deleteProvider,
  deleteSkillPlugin,
  deleteToolPlugin,
  getAgentSettings,
  getCapabilityCatalog,
  listAgents,
  listProviders,
  listSkillPlugins,
  listToolPlugins,
  testProvider,
  updateAgent,
  updateAgentSettings,
  updateProvider,
  updateSkillPlugin,
  updateToolPlugin,
} from '../../api/ai';
import type {
  AIAgentConfig,
  AIAgentInput,
  AIProvider,
  AIProviderInput,
  AIProviderTestResult,
  CapabilityCatalog,
  SkillPlugin,
  SkillPluginInput,
  ToolPlugin,
  ToolPluginInput,
} from '../../api/ai';
import PageHeader from '../../components/common/PageHeader';
import Loading from '../../components/common/Loading';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';

const PROVIDER_TYPES = ['openai_compatible', 'ollama'];

/** Which of the four settings tabs is showing. */
type SettingsTab = 'basic' | 'agents' | 'skills' | 'tools';

/** Cost tiers understood by the planner (mirrors registry.CostHint.Tier). */
const COST_TIERS = ['cheap', 'standard', 'heavy'];
/** Domains the skills list groups by. */
const SKILL_DOMAINS = ['general', 'finance', 'sales', 'ops'];
/** HTTP methods an HTTP tool may use. */
const TOOL_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
/** Blast radius of a tool (mirrors registry.SideEffect). */
const SIDE_EFFECTS = ['read', 'write', 'destructive'];

/** Skill editor values. */
interface SkillFormValues {
  title: string;
  code: string;
  domain: string;
  description?: string;
  instructions?: string;
  input_schema?: string;
  tool_codes?: string[];
  cost_tier: string;
  enabled: boolean;
}

/** Tool editor values. */
interface ToolFormValues {
  title: string;
  code: string;
  description?: string;
  kind: string;
  method: string;
  url?: string;
  headers?: string;
  body_template?: string;
  input_schema?: string;
  side_effect: string;
  timeout_sec?: number;
  enabled: boolean;
}

/** Provider form values (numbers stay numbers; the key stays optional). */
interface ProviderFormValues {
  name: string;
  provider_type: string;
  base_url: string;
  model?: string;
  timeout_sec?: number;
  enabled: boolean;
  description?: string;
  api_key?: string;
}

/** Agent form values; `skill_codes`/`tool_codes` are multi-select lists. */
interface AgentFormValues {
  name: string;
  description?: string;
  provider_id: number;
  is_default: boolean;
  enabled: boolean;
  skill_codes?: string[];
  tool_codes?: string[];
  max_steps?: number;
  max_tool_calls?: number;
  token_budget?: number;
  run_timeout_sec?: number;
  write_tools_enabled: boolean;
  write_confirm_required: boolean;
}

/**
 * System → AI Agent configuration. The page is built around two editable
 * tables — 大模型供应商 (LLM endpoints) and 智能体 (plugin-style agents that bind a
 * provider plus the skills/tools they may use) — over a single global master
 * switch. Every change is applied to the running service immediately.
 */
export default function AISettings() {
  const { can } = usePermission();
  const { t } = useI18n();
  const editable = can('can_write', 'AIAgent');

  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [tab, setTab] = useState<SettingsTab>('basic');
  const [enabled, setEnabled] = useState(false);
  const [savingSwitch, setSavingSwitch] = useState(false);

  const [providers, setProviders] = useState<AIProvider[]>([]);
  const [agents, setAgents] = useState<AIAgentConfig[]>([]);
  const [skills, setSkills] = useState<SkillPlugin[]>([]);
  const [tools, setTools] = useState<ToolPlugin[]>([]);
  const [catalog, setCatalog] = useState<CapabilityCatalog>({ skills: [], tools: [] });

  const [providerModalOpen, setProviderModalOpen] = useState(false);
  const [editingProvider, setEditingProvider] = useState<AIProvider | null>(null);
  const [savingProvider, setSavingProvider] = useState(false);
  const [testingProvider, setTestingProvider] = useState(false);
  const [providerProbe, setProviderProbe] = useState<AIProviderTestResult | null>(null);
  const [agentModalOpen, setAgentModalOpen] = useState(false);
  const [editingAgent, setEditingAgent] = useState<AIAgentConfig | null>(null);

  const [skillModalOpen, setSkillModalOpen] = useState(false);
  const [editingSkill, setEditingSkill] = useState<SkillPlugin | null>(null);
  const [savingSkill, setSavingSkill] = useState(false);
  const [toolModalOpen, setToolModalOpen] = useState(false);
  const [editingTool, setEditingTool] = useState<ToolPlugin | null>(null);
  const [savingTool, setSavingTool] = useState(false);

  const [providerForm] = Form.useForm<ProviderFormValues>();
  // Watched so the probe result's "reused the stored key" note follows the field
  // live instead of freezing at render time.
  const providerApiKey = Form.useWatch('api_key', providerForm);
  const [agentForm] = Form.useForm<AgentFormValues>();
  const [skillForm] = Form.useForm<SkillFormValues>();
  const [toolForm] = Form.useForm<ToolFormValues>();

  // Built-in capability rows are read-only: their editors open in a view-only
  // mode instead of the regular edit form.
  const skillViewOnly = Boolean(editingSkill?.builtin);
  const toolViewOnly = Boolean(editingTool?.builtin);
  const agentViewOnly = Boolean(editingAgent?.builtin);

  const loadAll = useCallback(async () => {
    const [settings, providerRows, agentRows, capabilityCatalog, skillRows, toolRows] =
      await Promise.all([
        getAgentSettings(),
        listProviders(),
        listAgents(),
        getCapabilityCatalog(),
        listSkillPlugins(),
        listToolPlugins(),
      ]);
    setEnabled(settings.enabled);
    setProviders(providerRows);
    setAgents(agentRows);
    setCatalog(capabilityCatalog);
    setSkills(skillRows);
    setTools(toolRows);
  }, []);

  useEffect(() => {
    let alive = true;
    void (async () => {
      try {
        await loadAll();
      } catch (error) {
        if (alive) setLoadError(errorMessage(error));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, [loadAll]);

  const reload = useCallback(async () => {
    try {
      await loadAll();
    } catch (error) {
      message.error(errorMessage(error));
    }
  }, [loadAll]);

  const providerOptions = useMemo(
    () =>
      providers.map(item => ({
        value: item.id,
        label: item.enabled ? item.name : `${item.name} (${t('settings.ai.providers.disabled')})`,
      })),
    [providers, t],
  );

  // Capabilities are grouped by kind and tagged with their wiring state: an
  // operator must be able to tell "has a real executor" from "registered but
  // not implemented", otherwise a saved agent silently loses capabilities.
  const skillOptions = useMemo(
    () =>
      catalog.skills.map(skill => ({
        value: skill.name,
        label: `${skill.title || skill.name}（${skill.name}）${
          skill.available === false ? ` · ${t('settings.ai.agents.unwired')}` : ''
        }`,
        disabled: skill.available === false,
      })),
    [catalog.skills, t],
  );

  const toolOptions = useMemo(() => {
    const decorate = (tool: (typeof catalog.tools)[number]) => ({
      value: tool.name,
      label: `${tool.title || tool.name}（${tool.name}）${
        tool.available === false ? ` · ${t('settings.ai.agents.unwired')}` : ''
      }`,
      disabled: tool.available === false,
    });
    const read = catalog.tools.filter(tool => tool.side_effect === 'read');
    const write = catalog.tools.filter(tool => tool.side_effect !== 'read');
    return [
      {
        label: t('settings.ai.agents.group.read'),
        options: read.map(decorate),
      },
      {
        label: t('settings.ai.agents.group.write'),
        options: write.map(decorate),
      },
    ];
  }, [catalog.tools, t]);

  // Data-read tools only: the "select all read tools" shortcut a one-tool agent
  // needs most, since an agent bound to a single reader cannot cross-check.
  const readToolNames = useMemo(
    () =>
      catalog.tools
        .filter(tool => tool.side_effect === 'read' && tool.available !== false)
        .map(tool => tool.name),
    [catalog.tools],
  );

  const handleSaveSwitch = async (next: boolean) => {
    try {
      setSavingSwitch(true);
      const saved = await updateAgentSettings({ enabled: next });
      setEnabled(saved.enabled);
      message.success(t('settings.ai.saved'));
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSavingSwitch(false);
    }
  };

  /* ------------------------------------------------------------ providers */

  const openProviderModal = (row: AIProvider | null) => {
    setEditingProvider(row);
    setProviderProbe(null);
    providerForm.resetFields();
    setProviderModalOpen(true);
    if (row) {
      providerForm.setFieldsValue({
        name: row.name,
        provider_type: row.provider_type || 'openai_compatible',
        base_url: row.base_url,
        model: row.model,
        timeout_sec: row.timeout_sec || 60,
        enabled: row.enabled,
        description: row.description,
        api_key: undefined,
      });
    } else {
      providerForm.setFieldsValue({
        provider_type: 'openai_compatible',
        timeout_sec: 60,
        enabled: true,
        api_key: undefined,
      });
    }
  };

  // Probe the endpoint with the values currently in the form, before saving.
  // An edit dialog never receives the stored secret, so a blank key is sent as
  // "reuse whatever is saved" by passing the row id along.
  const runProviderTest = async () => {
    let values: ProviderFormValues;
    try {
      values = await providerForm.validateFields(['base_url', 'provider_type']);
    } catch {
      return;
    }
    setTestingProvider(true);
    setProviderProbe(null);
    try {
      const result = await testProvider({
        id: editingProvider?.id,
        provider_type: values.provider_type,
        base_url: values.base_url?.trim(),
        model: providerForm.getFieldValue('model')?.trim() || '',
        timeout_sec: providerForm.getFieldValue('timeout_sec') ?? 60,
        api_key: providerForm.getFieldValue('api_key')?.trim() || undefined,
      });
      setProviderProbe(result);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setTestingProvider(false);
    }
  };

  const submitProvider = async () => {
    try {
      const values = await providerForm.validateFields();
      const payload: AIProviderInput = {
        name: values.name.trim(),
        provider_type: values.provider_type,
        base_url: values.base_url.trim(),
        model: values.model?.trim() ?? '',
        timeout_sec: values.timeout_sec ?? 60,
        enabled: values.enabled,
        description: values.description?.trim() ?? '',
      };
      // Leaving the key blank on an edit keeps the stored secret untouched.
      if (values.api_key?.trim()) {
        payload.api_key = values.api_key.trim();
      }
      setSavingProvider(true);
      if (editingProvider) {
        await updateProvider(editingProvider.id, payload);
        message.success(t('settings.ai.providers.updated'));
      } else {
        await createProvider(payload);
        message.success(t('settings.ai.providers.created'));
      }
      setProviderModalOpen(false);
      await reload();
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setSavingProvider(false);
    }
  };

  const removeProvider = async (row: AIProvider) => {
    try {
      await deleteProvider(row.id);
      message.success(t('settings.ai.providers.deleted'));
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const providerColumns: ColumnsType<AIProvider> = [
    {
      title: t('settings.ai.providers.name'),
      dataIndex: 'name',
      key: 'name',
      render: (name: string) => <strong>{name}</strong>,
    },
    {
      title: t('settings.ai.providers.type'),
      dataIndex: 'provider_type',
      key: 'provider_type',
      width: 170,
      render: (value: string) => <Tag>{value}</Tag>,
    },
    {
      title: t('settings.ai.providers.base_url'),
      dataIndex: 'base_url',
      key: 'base_url',
      ellipsis: true,
      render: (value: string) => (
        <Tooltip title={value}>
          <span className="mono">{value}</span>
        </Tooltip>
      ),
    },
    {
      title: t('settings.ai.providers.model'),
      dataIndex: 'model',
      key: 'model',
      width: 160,
      render: (value: string) => value || <span className="cell-muted">—</span>,
    },
    {
      title: t('settings.ai.api_key'),
      dataIndex: 'api_key_configured',
      key: 'api_key_configured',
      width: 110,
      render: (configured: boolean) => (
        <Tag color={configured ? 'green' : 'default'}>
          {configured
            ? t('settings.ai.api_key_configured')
            : t('settings.ai.api_key_not_configured')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.providers.status'),
      dataIndex: 'enabled',
      key: 'enabled',
      width: 100,
      render: (value: boolean) => (
        <Tag color={value ? 'blue' : 'default'}>
          {value ? t('settings.ai.providers.enabled') : t('settings.ai.providers.disabled')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.actions.title'),
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_: unknown, row: AIProvider) => (
        <Space size={4}>
          <Button
            type="link"
            size="small"
            icon={<EditOutlined />}
            disabled={!editable}
            onClick={() => openProviderModal(row)}
          >
            {t('settings.ai.actions.edit')}
          </Button>
          <Popconfirm
            title={t('settings.ai.providers.delete_confirm', { name: row.name })}
            okText={t('settings.ai.actions.confirm')}
            cancelText={t('settings.ai.actions.cancel')}
            disabled={!editable}
            onConfirm={() => void removeProvider(row)}
          >
            <Button type="link" size="small" danger icon={<DeleteOutlined />} disabled={!editable}>
              {t('settings.ai.actions.delete')}
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  /* --------------------------------------------------------------- agents */

  const openAgentModal = (row: AIAgentConfig | null) => {
    setEditingAgent(row);
    agentForm.resetFields();
    setAgentModalOpen(true);
    if (row) {
      agentForm.setFieldsValue({
        name: row.name,
        description: row.description,
        provider_id: row.provider_id,
        is_default: row.is_default,
        enabled: row.enabled,
        skill_codes: row.skill_codes,
        tool_codes: row.tool_codes,
        max_steps: row.max_steps,
        max_tool_calls: row.max_tool_calls,
        token_budget: row.token_budget,
        run_timeout_sec: row.run_timeout_sec,
        write_tools_enabled: row.write_tools_enabled,
        write_confirm_required: row.write_confirm_required,
      });
    } else {
      agentForm.setFieldsValue({
        provider_id: providers[0]?.id ?? 0,
        is_default: agents.length === 0,
        enabled: true,
        skill_codes: [],
        tool_codes: [],
        max_steps: 6,
        max_tool_calls: 8,
        token_budget: 120000,
        run_timeout_sec: 120,
        write_tools_enabled: false,
        write_confirm_required: false,
      });
    }
  };

  const submitAgent = async () => {
    try {
      const values = await agentForm.validateFields();
      const payload: AIAgentInput = {
        name: values.name.trim(),
        description: values.description?.trim() ?? '',
        provider_id: values.provider_id ?? 0,
        is_default: values.is_default,
        enabled: values.enabled,
        skill_codes: values.skill_codes ?? [],
        tool_codes: values.tool_codes ?? [],
        max_steps: values.max_steps ?? 6,
        max_tool_calls: values.max_tool_calls ?? 8,
        token_budget: values.token_budget ?? 40000,
        run_timeout_sec: values.run_timeout_sec ?? 120,
        write_tools_enabled: values.write_tools_enabled,
        write_confirm_required: values.write_confirm_required,
      };
      if (editingAgent) {
        await updateAgent(editingAgent.id, payload);
        message.success(t('settings.ai.agents.updated'));
      } else {
        await createAgent(payload);
        message.success(t('settings.ai.agents.created'));
      }
      setAgentModalOpen(false);
      await reload();
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    }
  };

  const removeAgent = async (row: AIAgentConfig) => {
    try {
      await deleteAgent(row.id);
      message.success(t('settings.ai.agents.deleted'));
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const capabilitySummary = (codes: string[]) =>
    codes.length === 0 ? (
      <Tag color="green">{t('settings.ai.agents.all')}</Tag>
    ) : (
      <span>{t('settings.ai.agents.count', { count: codes.length })}</span>
    );

  const agentColumns: ColumnsType<AIAgentConfig> = [
    {
      title: t('settings.ai.agents.name'),
      dataIndex: 'name',
      key: 'name',
      render: (name: string, row) => (
        <Space size={6}>
          <strong>{name}</strong>
          {row.is_default ? <Tag color="gold">{t('settings.ai.agents.default_tag')}</Tag> : null}
        </Space>
      ),
    },
    {
      title: t('settings.ai.agents.provider'),
      dataIndex: 'provider_name',
      key: 'provider_name',
      width: 180,
      render: (value: string | undefined) =>
        value || <span className="cell-muted">{t('settings.ai.agents.provider_fallback')}</span>,
    },
    {
      title: t('settings.ai.plugins.source'),
      dataIndex: 'builtin',
      key: 'builtin',
      width: 100,
      render: (builtin: boolean) => (
        <Tag color={builtin ? 'default' : 'geekblue'}>
          {builtin ? t('settings.ai.plugins.builtin') : t('settings.ai.plugins.custom')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.agents.skills'),
      dataIndex: 'skill_codes',
      key: 'skill_codes',
      width: 110,
      render: (codes: string[]) => capabilitySummary(codes),
    },
    {
      title: t('settings.ai.agents.tools'),
      dataIndex: 'tool_codes',
      key: 'tool_codes',
      width: 110,
      render: (codes: string[]) => capabilitySummary(codes),
    },
    {
      title: t('settings.ai.agents.status'),
      dataIndex: 'enabled',
      key: 'enabled',
      width: 100,
      render: (value: boolean) => (
        <Tag color={value ? 'blue' : 'default'}>
          {value ? t('settings.ai.providers.enabled') : t('settings.ai.providers.disabled')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.actions.title'),
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_: unknown, row: AIAgentConfig) =>
        row.builtin ? (
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => openAgentModal(row)}
          >
            {t('settings.ai.actions.view')}
          </Button>
        ) : (
          <Space size={4}>
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              disabled={!editable}
              onClick={() => openAgentModal(row)}
            >
              {t('settings.ai.actions.edit')}
            </Button>
            <Popconfirm
              title={t('settings.ai.agents.delete_confirm', { name: row.name })}
              okText={t('settings.ai.actions.confirm')}
              cancelText={t('settings.ai.actions.cancel')}
              disabled={!editable}
              onConfirm={() => void removeAgent(row)}
            >
              <Button
                type="link"
                size="small"
                danger
                icon={<DeleteOutlined />}
                disabled={!editable}
              >
                {t('settings.ai.actions.delete')}
              </Button>
            </Popconfirm>
          </Space>
        ),
    },
  ];

  /* --------------------------------------------------------------- skills */

  // A skill is a plugin: a name + prompt that the planner can call. Built-in
  // skills keep their compiled behaviour, so their prompt fields are read-only
  // and only the enable switch (plus the description) is editable.
  const openSkillModal = (row: SkillPlugin | null) => {
    setEditingSkill(row);
    skillForm.resetFields();
    setSkillModalOpen(true);
    if (row) {
      skillForm.setFieldsValue({
        title: row.title,
        code: row.code,
        domain: row.domain || 'general',
        description: row.description,
        instructions: row.instructions,
        input_schema: row.input_schema === '{}' ? '' : row.input_schema,
        tool_codes: row.tool_codes,
        cost_tier: row.cost_tier || 'standard',
        enabled: row.enabled,
      });
    } else {
      skillForm.setFieldsValue({
        domain: 'general',
        cost_tier: 'standard',
        enabled: true,
        tool_codes: [],
      });
    }
  };

  const submitSkill = async () => {
    let values: SkillFormValues;
    try {
      values = await skillForm.validateFields();
    } catch {
      return;
    }
    const payload: SkillPluginInput = {
      title: values.title.trim(),
      domain: values.domain,
      description: values.description?.trim() ?? '',
      instructions: values.instructions?.trim() ?? '',
      input_schema: values.input_schema?.trim() ?? '',
      tool_codes: values.tool_codes ?? [],
      cost_tier: values.cost_tier,
      enabled: values.enabled,
    };
    setSavingSkill(true);
    try {
      if (editingSkill) {
        await updateSkillPlugin(editingSkill.code, payload);
        message.success(t('settings.ai.skills.updated'));
      } else {
        await createSkillPlugin({ ...payload, code: values.code.trim() });
        message.success(t('settings.ai.skills.created'));
      }
      setSkillModalOpen(false);
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSavingSkill(false);
    }
  };

  const removeSkill = async (row: SkillPlugin) => {
    try {
      await deleteSkillPlugin(row.code);
      message.success(t('settings.ai.skills.deleted'));
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const skillToolOptions = useMemo(
    () =>
      tools.map(tool => ({
        value: tool.code,
        label: `${tool.title || tool.code}（${tool.code}）`,
      })),
    [tools],
  );

  const skillColumns: ColumnsType<SkillPlugin> = [
    {
      title: t('settings.ai.skills.name'),
      dataIndex: 'title',
      key: 'title',
      render: (title: string, row) => (
        <Space direction="vertical" size={0}>
          <strong>{title || row.code}</strong>
          <span className="cell-mono cell-muted">{row.code}</span>
        </Space>
      ),
    },
    {
      title: t('settings.ai.skills.domain'),
      dataIndex: 'domain',
      key: 'domain',
      width: 110,
      render: (value: string) => <Tag>{value || 'general'}</Tag>,
    },
    {
      title: t('settings.ai.plugins.source'),
      dataIndex: 'builtin',
      key: 'builtin',
      width: 110,
      render: (builtin: boolean) => (
        <Tag color={builtin ? 'default' : 'geekblue'}>
          {builtin ? t('settings.ai.plugins.builtin') : t('settings.ai.plugins.custom')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.plugins.state'),
      dataIndex: 'enabled',
      key: 'enabled',
      width: 150,
      render: (value: boolean, row) => (
        <Space size={4}>
          <Tag color={value ? 'blue' : 'default'}>
            {value ? t('settings.ai.plugins.enabled') : t('settings.ai.plugins.disabled')}
          </Tag>
          {row.available ? null : (
            <Tag color="orange">{t('settings.ai.plugins.unwired')}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('settings.ai.skills.tools'),
      dataIndex: 'tool_codes',
      key: 'tool_codes',
      width: 110,
      render: (codes: string[]) =>
        codes.length ? (
          <span>{t('settings.ai.agents.count', { count: codes.length })}</span>
        ) : (
          <span className="cell-muted">—</span>
        ),
    },
    {
      title: t('settings.ai.actions.title'),
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_: unknown, row: SkillPlugin) =>
        row.builtin ? (
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => openSkillModal(row)}
          >
            {t('settings.ai.actions.view')}
          </Button>
        ) : (
          <Space size={4}>
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              disabled={!editable}
              onClick={() => openSkillModal(row)}
            >
              {t('settings.ai.actions.edit')}
            </Button>
            <Popconfirm
              title={t('settings.ai.skills.delete_confirm', { name: row.title || row.code })}
              okText={t('settings.ai.actions.confirm')}
              cancelText={t('settings.ai.actions.cancel')}
              disabled={!editable || row.builtin}
              onConfirm={() => void removeSkill(row)}
            >
              <Button
                type="link"
                size="small"
                danger
                icon={<DeleteOutlined />}
                disabled={!editable || row.builtin}
              >
                {t('settings.ai.actions.delete')}
              </Button>
            </Popconfirm>
          </Space>
        ),
    },
  ];

  /* ---------------------------------------------------------------- tools */

  const openToolModal = (row: ToolPlugin | null) => {
    setEditingTool(row);
    toolForm.resetFields();
    setToolModalOpen(true);
    if (row) {
      toolForm.setFieldsValue({
        title: row.title,
        code: row.code,
        description: row.description,
        kind: row.kind || 'http',
        method: row.method || 'GET',
        url: row.url,
        headers: row.headers === '{}' ? '' : row.headers,
        body_template: row.body_template,
        input_schema: row.input_schema === '{}' ? '' : row.input_schema,
        side_effect: row.side_effect || 'read',
        timeout_sec: row.timeout_sec || 30,
        enabled: row.enabled,
      });
    } else {
      toolForm.setFieldsValue({
        kind: 'http',
        method: 'GET',
        side_effect: 'read',
        timeout_sec: 30,
        enabled: true,
      });
    }
  };

  const submitTool = async () => {
    let values: ToolFormValues;
    try {
      values = await toolForm.validateFields();
    } catch {
      return;
    }
    const payload: ToolPluginInput = {
      title: values.title.trim(),
      description: values.description?.trim() ?? '',
      kind: values.kind,
      method: values.method,
      url: values.url?.trim() ?? '',
      headers: values.headers?.trim() ?? '',
      body_template: values.body_template?.trim() ?? '',
      input_schema: values.input_schema?.trim() ?? '',
      side_effect: values.side_effect,
      timeout_sec: values.timeout_sec ?? 30,
      enabled: values.enabled,
    };
    setSavingTool(true);
    try {
      if (editingTool) {
        await updateToolPlugin(editingTool.code, payload);
        message.success(t('settings.ai.tools.updated'));
      } else {
        await createToolPlugin({ ...payload, code: values.code.trim() });
        message.success(t('settings.ai.tools.created'));
      }
      setToolModalOpen(false);
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSavingTool(false);
    }
  };

  const removeTool = async (row: ToolPlugin) => {
    try {
      await deleteToolPlugin(row.code);
      message.success(t('settings.ai.tools.deleted'));
      await reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const sideEffectColor = (value: string) =>
    value === 'read' ? 'green' : value === 'write' ? 'gold' : 'red';

  const toolColumns: ColumnsType<ToolPlugin> = [
    {
      title: t('settings.ai.tools.name'),
      dataIndex: 'title',
      key: 'title',
      render: (title: string, row) => (
        <Space direction="vertical" size={0}>
          <strong>{title || row.code}</strong>
          <span className="cell-mono cell-muted">{row.code}</span>
        </Space>
      ),
    },
    {
      title: t('settings.ai.plugins.source'),
      dataIndex: 'builtin',
      key: 'builtin',
      width: 100,
      render: (builtin: boolean) => (
        <Tag color={builtin ? 'default' : 'geekblue'}>
          {builtin ? t('settings.ai.plugins.builtin') : t('settings.ai.plugins.custom')}
        </Tag>
      ),
    },
    {
      title: t('settings.ai.tools.kind'),
      dataIndex: 'kind',
      key: 'kind',
      width: 110,
      render: (value: string, row) =>
        value === 'http' ? (
          <Tag color="cyan">{t('settings.ai.tools.kind_http')}</Tag>
        ) : (
          <Tag>{row.builtin ? t('settings.ai.plugins.builtin') : value}</Tag>
        ),
    },
    {
      title: t('settings.ai.tools.side_effect'),
      dataIndex: 'side_effect',
      key: 'side_effect',
      width: 120,
      render: (value: string) => (
        <Tag color={sideEffectColor(value)}>{value}</Tag>
      ),
    },
    {
      title: t('settings.ai.plugins.state'),
      dataIndex: 'enabled',
      key: 'enabled',
      width: 150,
      render: (value: boolean, row) => (
        <Space size={4}>
          <Tag color={value ? 'blue' : 'default'}>
            {value ? t('settings.ai.plugins.enabled') : t('settings.ai.plugins.disabled')}
          </Tag>
          {row.available ? null : (
            <Tag color="orange">{t('settings.ai.plugins.unwired')}</Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('settings.ai.actions.title'),
      key: 'actions',
      width: 150,
      align: 'right',
      render: (_: unknown, row: ToolPlugin) =>
        row.builtin ? (
          <Button
            type="link"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => openToolModal(row)}
          >
            {t('settings.ai.actions.view')}
          </Button>
        ) : (
          <Space size={4}>
            <Button
              type="link"
              size="small"
              icon={<EditOutlined />}
              disabled={!editable}
              onClick={() => openToolModal(row)}
            >
              {t('settings.ai.actions.edit')}
            </Button>
            <Popconfirm
              title={t('settings.ai.tools.delete_confirm', { name: row.title || row.code })}
              okText={t('settings.ai.actions.confirm')}
              cancelText={t('settings.ai.actions.cancel')}
              disabled={!editable || row.builtin}
              onConfirm={() => void removeTool(row)}
            >
              <Button
                type="link"
                size="small"
                danger
                icon={<DeleteOutlined />}
                disabled={!editable || row.builtin}
              >
                {t('settings.ai.actions.delete')}
              </Button>
            </Popconfirm>
          </Space>
        ),
    },
  ];

  if (loading) return <Loading height={360} tip={t('common.state.loading_workspace')} />;

  return (
    <>
      <PageHeader title={t('nav.ai_settings')} subtitle={t('settings.ai.subtitle')} />

      {loadError ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('settings.ai.load_error')}
          description={loadError}
        />
      ) : null}

      {!editable ? (
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 16 }}
          message={t('settings.ai.read_only')}
        />
      ) : null}

      <Tabs
        activeKey={tab}
        onChange={key => setTab(key as SettingsTab)}
        items={[
          {
            key: 'basic',
            label: t('settings.ai.tab.basic'),
            children: (
              <>
      <Card
        title={t('settings.ai.section.general')}
        className="zg-card"
        style={{ marginBottom: 16 }}
      >
        <Row gutter={24} align="middle">
          <Col span={8}>
            <Space size={12}>
              <Switch
                checked={enabled}
                disabled={!editable || savingSwitch}
                onChange={value => void handleSaveSwitch(value)}
              />
              <span>{t('settings.ai.enabled')}</span>
            </Space>
          </Col>
          <Col span={16}>
            <Alert type="info" showIcon message={t('settings.ai.enabled_hint')} />
          </Col>
        </Row>
      </Card>

      <Card
        title={t('settings.ai.providers.title')}
        className="zg-card"
        style={{ marginBottom: 16 }}
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={!editable}
            onClick={() => openProviderModal(null)}
          >
            {t('settings.ai.providers.new')}
          </Button>
        }
      >
        <p className="cell-muted" style={{ marginTop: 0 }}>
          {t('settings.ai.providers.hint')}
        </p>
        <Table<AIProvider>
          rowKey="id"
          size="middle"
          columns={providerColumns}
          dataSource={providers}
          pagination={false}
          locale={{ emptyText: t('settings.ai.providers.empty') }}
        />
      </Card>
              </>
            ),
          },
          {
            key: 'agents',
            label: t('settings.ai.tab.agents'),
            children: (
      <Card
        title={t('settings.ai.agents.title')}
        className="zg-card"
        extra={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={!editable}
            onClick={() => openAgentModal(null)}
          >
            {t('settings.ai.agents.new')}
          </Button>
        }
      >
        <p className="cell-muted" style={{ marginTop: 0 }}>
          {t('settings.ai.agents.hint')}
        </p>
        <Table<AIAgentConfig>
          rowKey="id"
          size="middle"
          columns={agentColumns}
          dataSource={agents}
          pagination={false}
          locale={{ emptyText: t('settings.ai.agents.empty') }}
        />
      </Card>
            ),
          },
          {
            key: 'skills',
            label: t('settings.ai.tab.skills'),
            children: (
              <Card
                title={t('settings.ai.skills.title')}
                className="zg-card"
                extra={
                  <Button
                    type="primary"
                    icon={<PlusOutlined />}
                    disabled={!editable}
                    onClick={() => openSkillModal(null)}
                  >
                    {t('settings.ai.skills.new')}
                  </Button>
                }
              >
                <p className="cell-muted" style={{ marginTop: 0 }}>
                  {t('settings.ai.skills.hint')}
                </p>
                <Table<SkillPlugin>
                  rowKey="code"
                  size="middle"
                  columns={skillColumns}
                  dataSource={skills}
                  pagination={false}
                  locale={{ emptyText: t('settings.ai.skills.empty') }}
                />
              </Card>
            ),
          },
          {
            key: 'tools',
            label: t('settings.ai.tab.tools'),
            children: (
              <Card
                title={t('settings.ai.tools.title')}
                className="zg-card"
                extra={
                  <Button
                    type="primary"
                    icon={<PlusOutlined />}
                    disabled={!editable}
                    onClick={() => openToolModal(null)}
                  >
                    {t('settings.ai.tools.new')}
                  </Button>
                }
              >
                <p className="cell-muted" style={{ marginTop: 0 }}>
                  {t('settings.ai.tools.hint')}
                </p>
                <Table<ToolPlugin>
                  rowKey="code"
                  size="middle"
                  columns={toolColumns}
                  dataSource={tools}
                  pagination={false}
                  locale={{ emptyText: t('settings.ai.tools.empty') }}
                />
              </Card>
            ),
          },
        ]}
      />

      {/* Provider editor */}
      <Modal
        open={providerModalOpen}
        title={
          editingProvider ? t('settings.ai.providers.edit') : t('settings.ai.providers.new')
        }
        onCancel={() => setProviderModalOpen(false)}
        forceRender
        width={720}
        footer={[
          <Button
            key="test"
            icon={<ApiOutlined />}
            onClick={() => void runProviderTest()}
            loading={testingProvider}
            disabled={!editable}
            style={{ marginRight: 'auto' }}
          >
            {testingProvider
              ? t('settings.ai.providers.testing')
              : t('settings.ai.providers.test')}
          </Button>,
          <Button key="cancel" onClick={() => setProviderModalOpen(false)}>
            {t('settings.ai.actions.cancel')}
          </Button>,
          <Button
            key="ok"
            type="primary"
            loading={savingProvider}
            onClick={() => void submitProvider()}
          >
            {t('settings.ai.save')}
          </Button>,
        ]}
      >
        <Form form={providerForm} layout="vertical" className="ai-form">
          {providerProbe ? (
            <Alert
              type={providerProbe.ok ? 'success' : 'error'}
              showIcon
              closable
              className="ai-probe"
              onClose={() => setProviderProbe(null)}
              message={
                providerProbe.ok
                  ? t('settings.ai.providers.test_ok')
                  : t('settings.ai.providers.test_failed')
              }
              description={
                <div className="ai-probe__body">
                  <div>{providerProbe.message}</div>
                  <div className="ai-probe__meta">
                    <span className="mono">{providerProbe.endpoint}</span>
                    <span>·</span>
                    <span>
                      {t('settings.ai.providers.test_latency', {
                        ms: providerProbe.latency_ms,
                      })}
                    </span>
                    {providerProbe.model ? (
                      <>
                        <span>·</span>
                        <span>
                          {t('settings.ai.providers.test_model_used', {
                            model: providerProbe.model,
                          })}
                        </span>
                      </>
                    ) : null}
                  </div>
                  {providerProbe.models && providerProbe.models.length > 0 ? (
                    <div className="ai-probe__models">
                      <span className="cell-muted">
                        {t('settings.ai.providers.test_models_list', {
                          count: providerProbe.models.length,
                        })}
                      </span>
                      {providerProbe.models.slice(0, 8).map(name => (
                        <Tag key={name} bordered={false} className="mono">
                          {name}
                        </Tag>
                      ))}
                      {providerProbe.models.length > 8 ? (
                        <span className="cell-muted">
                          +{providerProbe.models.length - 8}
                        </span>
                      ) : null}
                    </div>
                  ) : null}
                  {editingProvider?.api_key_configured && !providerApiKey ? (
                    <div className="cell-muted">
                      {t('settings.ai.providers.test_saved_key')}
                    </div>
                  ) : null}
                </div>
              }
            />
          ) : null}

          <section className="ai-form-section">
            <div className="ai-form-section__title">
              {t('settings.ai.providers.section.basic')}
            </div>
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item
                  name="name"
                  label={t('settings.ai.providers.name')}
                  rules={[{ required: true, message: t('settings.ai.providers.name_required') }]}
                >
                  <Input placeholder={t('settings.ai.providers.name_placeholder')} />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item
                  name="provider_type"
                  label={t('settings.ai.providers.type')}
                  tooltip={t('settings.ai.provider_hint')}
                  rules={[{ required: true }]}
                >
                  <Select options={PROVIDER_TYPES.map(value => ({ value, label: value }))} />
                </Form.Item>
              </Col>
              <Col span={24}>
                <Form.Item name="description" label={t('settings.ai.providers.description')}>
                  <Input.TextArea
                    rows={2}
                    maxLength={200}
                    placeholder={t('settings.ai.providers.description_placeholder')}
                  />
                </Form.Item>
              </Col>
            </Row>
          </section>

          <section className="ai-form-section">
            <div className="ai-form-section__title">
              {t('settings.ai.providers.section.connection')}
            </div>
            <Row gutter={16}>
              <Col span={24}>
                <Form.Item
                  name="base_url"
                  label={t('settings.ai.providers.base_url')}
                  tooltip={t('settings.ai.base_url_hint')}
                  rules={[
                    { required: true, message: t('settings.ai.providers.base_url_required') },
                  ]}
                >
                  <Input placeholder="http://127.0.0.1:11434/v1" className="mono" />
                </Form.Item>
              </Col>
              <Col span={16}>
                <Form.Item
                  name="api_key"
                  label={
                    <Space size={6}>
                      <span>{t('settings.ai.api_key')}</span>
                      {editingProvider ? (
                        <Tag
                          bordered={false}
                          color={editingProvider.api_key_configured ? 'green' : 'default'}
                        >
                          {editingProvider.api_key_configured
                            ? t('settings.ai.api_key_configured')
                            : t('settings.ai.api_key_not_configured')}
                        </Tag>
                      ) : null}
                    </Space>
                  }
                  tooltip={t('settings.ai.api_key_hint')}
                >
                  <Input.Password
                    autoComplete="new-password"
                    placeholder={
                      editingProvider?.api_key_configured
                        ? t('settings.ai.api_key_placeholder')
                        : t('settings.ai.api_key_empty')
                    }
                  />
                </Form.Item>
              </Col>
              <Col span={8}>
                <Form.Item
                  name="timeout_sec"
                  label={t('settings.ai.timeout')}
                  tooltip={t('settings.ai.timeout_hint')}
                >
                  <InputNumber min={1} max={3600} style={{ width: '100%' }} />
                </Form.Item>
              </Col>
            </Row>
          </section>

          <section className="ai-form-section">
            <div className="ai-form-section__title">
              {t('settings.ai.providers.section.behavior')}
            </div>
            <Row gutter={16}>
              <Col span={12}>
                <Form.Item
                  name="model"
                  label={t('settings.ai.providers.model')}
                  tooltip={t('settings.ai.base_url_hint')}
                >
                  <Input
                    placeholder={t('settings.ai.providers.model_placeholder')}
                    className="mono"
                  />
                </Form.Item>
              </Col>
              <Col span={12}>
                <Form.Item
                  name="enabled"
                  label={t('settings.ai.providers.status')}
                  valuePropName="checked"
                >
                  <div className="ai-form-switch">
                    <Switch
                      checkedChildren={t('settings.ai.providers.enabled')}
                      unCheckedChildren={t('settings.ai.providers.disabled')}
                    />
                  </div>
                </Form.Item>
              </Col>
            </Row>
            <p className="ai-form-hint">{t('settings.ai.providers.test_hint')}</p>
          </section>
        </Form>
      </Modal>

      {/* Agent (plugin) editor */}
      <Modal
        open={agentModalOpen}
        title={
          agentViewOnly
            ? `${t('settings.ai.actions.view')} · ${editingAgent?.name}`
            : editingAgent
              ? t('settings.ai.agents.edit')
              : t('settings.ai.agents.new')
        }
        onCancel={() => setAgentModalOpen(false)}
        onOk={() => void submitAgent()}
        okText={t('settings.ai.save')}
        cancelText={t('settings.ai.actions.cancel')}
        footer={
          agentViewOnly ? (
            <Button onClick={() => setAgentModalOpen(false)}>
              {t('settings.ai.actions.cancel')}
            </Button>
          ) : undefined
        }
        forceRender
        width={760}
      >
        <Form form={agentForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="name"
                label={t('settings.ai.agents.name')}
                rules={
                  agentViewOnly
                    ? undefined
                    : [{ required: true, message: t('settings.ai.agents.name_required') }]
                }
              >
                <Input placeholder="数据分析助手" disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="provider_id"
                label={t('settings.ai.agents.provider')}
                tooltip={t('settings.ai.agents.provider_hint')}
              >
                <Select
                  disabled={agentViewOnly}
                  options={[
                    { value: 0, label: t('settings.ai.agents.provider_fallback') },
                    ...providerOptions,
                  ]}
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="description" label={t('settings.ai.agents.description')}>
                <Input.TextArea rows={2} disabled={agentViewOnly} />
              </Form.Item>
            </Col>

            <Col span={12}>
              <Form.Item
                name="skill_codes"
                label={t('settings.ai.agents.skills')}
                tooltip={t('settings.ai.agents.skills_hint')}
              >
                <Select
                  mode="multiple"
                  allowClear
                  optionFilterProp="label"
                  disabled={agentViewOnly}
                  placeholder={t('settings.ai.agents.all')}
                  options={skillOptions}
                />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="tool_codes"
                label={t('settings.ai.agents.tools')}
                tooltip={t('settings.ai.agents.tools_hint')}
                extra={
                  <Form.Item noStyle shouldUpdate>
                    {({ getFieldValue, setFieldValue }) => {
                      const selected: string[] = getFieldValue('tool_codes') ?? [];
                      return (
                        <div className="ai-tool-hints">
                          <span className="cell-muted">
                            {t('settings.ai.agents.tools_count', { n: selected.length })}
                          </span>
                          <Button
                            type="link"
                            size="small"
                            disabled={agentViewOnly || readToolNames.length === 0}
                            onClick={() => setFieldValue('tool_codes', readToolNames)}
                          >
                            {t('settings.ai.agents.select_read_tools')}
                          </Button>
                          <Button
                            type="link"
                            size="small"
                            disabled={agentViewOnly || selected.length === 0}
                            onClick={() => setFieldValue('tool_codes', [])}
                          >
                            {t('settings.ai.agents.use_all')}
                          </Button>
                        </div>
                      );
                    }}
                  </Form.Item>
                }
              >
                <Select
                  mode="multiple"
                  allowClear
                  optionFilterProp="label"
                  disabled={agentViewOnly}
                  placeholder={t('settings.ai.agents.all')}
                  options={toolOptions}
                />
              </Form.Item>
            </Col>

            <Col span={12}>
              <Form.Item name="is_default" label={t('settings.ai.agents.is_default')} valuePropName="checked">
                <Switch disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="enabled" label={t('settings.ai.agents.status')} valuePropName="checked">
                <Switch disabled={agentViewOnly} />
              </Form.Item>
            </Col>

            <Col span={24}>
              <Divider style={{ marginTop: 0 }}>{t('settings.ai.section.guardrails')}</Divider>
            </Col>
            <Col span={6}>
              <Form.Item name="max_steps" label={t('settings.ai.max_steps')}>
                <InputNumber min={1} max={50} style={{ width: '100%' }} disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="max_tool_calls" label={t('settings.ai.max_tool_calls')}>
                <InputNumber min={1} max={100} style={{ width: '100%' }} disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="token_budget" label={t('settings.ai.token_budget')}>
                <InputNumber min={1000} step={1000} style={{ width: '100%' }} disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="run_timeout_sec" label={t('settings.ai.run_timeout')}>
                <InputNumber min={10} max={3600} style={{ width: '100%' }} disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="write_tools_enabled"
                label={t('settings.ai.write_tools')}
                valuePropName="checked"
                tooltip={t('settings.ai.write_tools_hint')}
              >
                <Switch disabled={agentViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="write_confirm_required"
                label={t('settings.ai.write_confirm')}
                valuePropName="checked"
                tooltip={t('settings.ai.write_confirm_hint')}
              >
                <Switch disabled={agentViewOnly} />
              </Form.Item>
            </Col>
          </Row>
          {agentViewOnly ? (
            <p className="ai-form-hint">{t('settings.ai.agents.builtin_hint')}</p>
          ) : null}
        </Form>
      </Modal>

      {/* Skill plugin editor: a prompt plus the tools it may reference. */}
      <Modal
        open={skillModalOpen}
        title={
          skillViewOnly
            ? `${t('settings.ai.actions.view')} · ${editingSkill?.title || editingSkill?.code}`
            : editingSkill
              ? t('settings.ai.skills.edit')
              : t('settings.ai.skills.new')
        }
        onCancel={() => setSkillModalOpen(false)}
        onOk={() => void submitSkill()}
        okText={t('settings.ai.save')}
        cancelText={t('settings.ai.actions.cancel')}
        confirmLoading={savingSkill}
        footer={
          skillViewOnly ? (
            <Button onClick={() => setSkillModalOpen(false)}>
              {t('settings.ai.actions.cancel')}
            </Button>
          ) : undefined
        }
        forceRender
        width={780}
      >
        <Form form={skillForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="title"
                label={t('settings.ai.skills.name')}
                rules={
                  skillViewOnly
                    ? undefined
                    : [{ required: true, message: t('settings.ai.skills.name_required') }]
                }
              >
                <Input placeholder="口径核查" disabled={skillViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="code"
                label={t('settings.ai.plugins.code')}
                tooltip={t('settings.ai.plugins.code_hint')}
                rules={
                  skillViewOnly
                    ? undefined
                    : editingSkill
                      ? undefined
                      : [
                          {
                            required: true,
                            pattern: /^[a-z][a-z0-9_]{1,63}$/,
                            message: t('settings.ai.plugins.code_invalid'),
                          },
                        ]
                }
              >
                <Input
                  className="mono"
                  disabled={Boolean(editingSkill) || skillViewOnly}
                  placeholder="metric_audit"
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="domain" label={t('settings.ai.skills.domain')}>
                <Select
                  disabled={skillViewOnly}
                  options={SKILL_DOMAINS.map(value => ({ value, label: value }))}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cost_tier" label={t('settings.ai.skills.cost_tier')}>
                <Select
                  disabled={Boolean(editingSkill?.builtin) || skillViewOnly}
                  options={COST_TIERS.map(value => ({ value, label: value }))}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="enabled"
                label={t('settings.ai.plugins.state')}
                valuePropName="checked"
              >
                <div className="ai-form-switch">
                  <Switch
                    disabled={skillViewOnly}
                    checkedChildren={t('settings.ai.plugins.enabled')}
                    unCheckedChildren={t('settings.ai.plugins.disabled')}
                  />
                </div>
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="description" label={t('settings.ai.skills.description')}>
                <Input.TextArea rows={2} maxLength={400} disabled={skillViewOnly} />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="instructions"
                label={t('settings.ai.skills.instructions')}
                tooltip={t('settings.ai.skills.instructions_hint')}
                rules={
                  skillViewOnly
                    ? undefined
                    : editingSkill?.builtin
                      ? undefined
                      : [{ required: true, message: t('settings.ai.skills.instructions_required') }]
                }
              >
                <Input.TextArea
                  rows={7}
                  className="mono-block"
                  disabled={Boolean(editingSkill?.builtin) || skillViewOnly}
                  placeholder={t('settings.ai.skills.instructions_placeholder')}
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="tool_codes"
                label={t('settings.ai.skills.tools')}
                tooltip={t('settings.ai.skills.tools_hint')}
              >
                <Select
                  mode="multiple"
                  allowClear
                  optionFilterProp="label"
                  disabled={Boolean(editingSkill?.builtin) || skillViewOnly}
                  placeholder={t('settings.ai.skills.tools_placeholder')}
                  options={skillToolOptions}
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="input_schema"
                label={t('settings.ai.plugins.input_schema')}
                tooltip={t('settings.ai.plugins.input_schema_hint')}
              >
                <Input.TextArea
                  rows={4}
                  className="mono-block"
                  disabled={Boolean(editingSkill?.builtin) || skillViewOnly}
                  placeholder='{"type":"object","properties":{"question":{"type":"string"}}}'
                />
              </Form.Item>
            </Col>
          </Row>
          {editingSkill?.builtin ? (
            <p className="ai-form-hint">{t('settings.ai.plugins.builtin_hint')}</p>
          ) : null}
        </Form>
      </Modal>

      {/* Tool plugin editor: HTTP tools only carry a transport. */}
      <Modal
        open={toolModalOpen}
        title={
          toolViewOnly
            ? `${t('settings.ai.actions.view')} · ${editingTool?.title || editingTool?.code}`
            : editingTool
              ? t('settings.ai.tools.edit')
              : t('settings.ai.tools.new')
        }
        onCancel={() => setToolModalOpen(false)}
        onOk={() => void submitTool()}
        okText={t('settings.ai.save')}
        cancelText={t('settings.ai.actions.cancel')}
        confirmLoading={savingTool}
        footer={
          toolViewOnly ? (
            <Button onClick={() => setToolModalOpen(false)}>
              {t('settings.ai.actions.cancel')}
            </Button>
          ) : undefined
        }
        forceRender
        width={780}
      >
        <Form form={toolForm} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="title"
                label={t('settings.ai.tools.name')}
                rules={
                  toolViewOnly
                    ? undefined
                    : [{ required: true, message: t('settings.ai.tools.name_required') }]
                }
              >
                <Input placeholder="汇率查询" disabled={toolViewOnly} />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="code"
                label={t('settings.ai.plugins.code')}
                tooltip={t('settings.ai.plugins.code_hint')}
                rules={
                  toolViewOnly
                    ? undefined
                    : editingTool
                      ? undefined
                      : [
                          {
                            required: true,
                            pattern: /^[a-z][a-z0-9_]{1,63}$/,
                            message: t('settings.ai.plugins.code_invalid'),
                          },
                        ]
                }
              >
                <Input
                  className="mono"
                  disabled={Boolean(editingTool) || toolViewOnly}
                  placeholder="fx_rate"
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="kind" label={t('settings.ai.tools.kind')}>
                <Select
                  disabled={Boolean(editingTool) || toolViewOnly}
                  options={
                    editingTool
                      ? [
                          { value: 'http', label: t('settings.ai.tools.kind_http') },
                          { value: 'builtin', label: t('settings.ai.plugins.builtin') },
                        ]
                      : [{ value: 'http', label: t('settings.ai.tools.kind_http') }]
                  }
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="side_effect" label={t('settings.ai.tools.side_effect')}>
                <Select
                  disabled={Boolean(editingTool?.builtin) || toolViewOnly}
                  options={SIDE_EFFECTS.map(value => ({ value, label: value }))}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="enabled"
                label={t('settings.ai.plugins.state')}
                valuePropName="checked"
              >
                <div className="ai-form-switch">
                  <Switch
                    disabled={toolViewOnly}
                    checkedChildren={t('settings.ai.plugins.enabled')}
                    unCheckedChildren={t('settings.ai.plugins.disabled')}
                  />
                </div>
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="description" label={t('settings.ai.tools.description')}>
                <Input.TextArea rows={2} maxLength={400} disabled={toolViewOnly} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="method" label={t('settings.ai.tools.method')}>
                <Select
                  disabled={Boolean(editingTool?.builtin) || toolViewOnly}
                  options={TOOL_METHODS.map(value => ({ value, label: value }))}
                />
              </Form.Item>
            </Col>
            <Col span={16}>
              <Form.Item
                name="url"
                label={t('settings.ai.tools.url')}
                tooltip={t('settings.ai.tools.url_hint')}
                rules={
                  toolViewOnly
                    ? undefined
                    : editingTool?.builtin
                      ? undefined
                      : [{ required: true, message: t('settings.ai.tools.url_required') }]
                }
              >
                <Input
                  className="mono"
                  disabled={
                    Boolean(editingTool?.builtin) ||
                    editingTool?.kind === 'builtin' ||
                    toolViewOnly
                  }
                  placeholder="https://api.example.com/fx?pair={{pair}}"
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="headers"
                label={t('settings.ai.tools.headers')}
                tooltip={t('settings.ai.tools.headers_hint')}
              >
                <Input.TextArea
                  rows={2}
                  className="mono-block"
                  disabled={Boolean(editingTool?.builtin) || toolViewOnly}
                  placeholder='{"Authorization":"Bearer {{token}}"}'
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="body_template"
                label={t('settings.ai.tools.body')}
                tooltip={t('settings.ai.tools.body_hint')}
              >
                <Input.TextArea
                  rows={3}
                  className="mono-block"
                  disabled={Boolean(editingTool?.builtin) || toolViewOnly}
                  placeholder='{"pair":"{{pair}}"}'
                />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item
                name="input_schema"
                label={t('settings.ai.plugins.input_schema')}
                tooltip={t('settings.ai.plugins.input_schema_hint')}
              >
                <Input.TextArea
                  rows={4}
                  className="mono-block"
                  disabled={Boolean(editingTool?.builtin) || toolViewOnly}
                  placeholder='{"type":"object","properties":{"pair":{"type":"string"}}}'
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="timeout_sec" label={t('settings.ai.tools.timeout')}>
                <InputNumber min={1} max={300} style={{ width: '100%' }} disabled={toolViewOnly} />
              </Form.Item>
            </Col>
          </Row>
          {editingTool?.builtin ? (
            <p className="ai-form-hint">{t('settings.ai.plugins.builtin_hint')}</p>
          ) : null}
        </Form>
      </Modal>
    </>
  );
}
