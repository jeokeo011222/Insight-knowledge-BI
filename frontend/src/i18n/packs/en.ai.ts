/**
 * English message pack — AI Agent panel. Mirrors `packs/zh.ai.ts` key for key.
 */
export const enAi = {
  /* Brand & shell */
  'ai.brand': 'ZG BI',
  'ai.panel.title': 'Data Analysis Agent',
  'ai.panel.subtitle': 'Ask in plain language; the Agent queries, analyses and answers.',
  'ai.trigger.open': 'AI Assistant',
  'ai.tabs.chat': 'Chat',
  'ai.tabs.history': 'Run history',

  /* Input */
  'ai.input.placeholder': 'e.g. Why did East China sales drop last month?',
  'ai.input.required': 'Please enter a question',
  'ai.actions.send': 'Send',
  'ai.actions.stop': 'Stop',
  'ai.actions.reset': 'New chat',
  'ai.actions.cancel': 'Cancel',
  'ai.actions.copy': 'Copy',
  'ai.actions.regenerate': 'Regenerate',
  'ai.actions.retry': 'Retry',
  'ai.actions.load_more': 'Load more',
  'ai.actions.resume': 'Continue analysis',
  'ai.suggest.title': 'Suggested follow-ups',
  'ai.suggest.by_gender': 'Break down by gender',
  'ai.suggest.yoy': 'Show year-over-year change',
  'ai.suggest.topn': 'Top 10 only',
  'ai.metrics.first_token': 'First token {ms}ms',
  'ai.metrics.empty_results': '{count} empty results',
  'ai.metrics.self_check': '{count} self-checks',
  'ai.input.hint': '⌘/Ctrl + Enter to send',
  'ai.plan.title': 'Plan',
  'ai.plan.steps': '{count} steps',
  'ai.actions.copied': 'Copied',
  'ai.actions.copy_failed': 'Copy failed — select the text manually',
  'ai.actions.export': 'Export Markdown',
  'ai.actions.exported': 'Markdown file exported',
  'ai.actions.preview_sql': 'Preview SQL',
  'ai.actions.rollback': 'Roll back this run',
  'ai.actions.view_run': 'View this run',
  'ai.actions.gen_sql': 'AI SQL helper',
  'ai.actions.explain_chart': 'AI insight',
  'ai.actions.design_chart': 'AI chart advisor',

  /* Entry presets (SqlLab / chart card / explore triggers) */
  'ai.preset.nl2sql': 'Rewrite the requirement below as a single read-only SQL using the nl2sql capability:',
  'ai.preset.chart_insight': 'Explain the data behaviour and anomalies of the chart "{name}":',
  'ai.preset.chart_design': 'Recommend a suitable chart type for the current dataset and explain why:',

  /* Capability menu */
  'ai.skills.title': 'Capabilities',
  'ai.skills.empty': 'No capability available',
  'ai.skills.hint': 'Click to ask about a capability; the Agent decides which ones to use.',
  'ai.skills.prefill': 'Use the "{title}" capability: ',

  /* Timeline */
  'ai.timeline.title': 'Execution',
  'ai.timeline.plan': 'Plan',
  'ai.timeline.empty': 'Nothing yet — ask a question below.',
  'ai.timeline.args': 'Arguments',
  'ai.timeline.result': 'Result',
  'ai.status.running': 'Running',
  'ai.status.ok': 'Done',
  'ai.status.error': 'Failed',
  'ai.status.denied': 'Denied',
  'ai.status.aborted': 'Stopped',
  'ai.streaming.thinking': 'Thinking…',
  'ai.streaming.step': 'Step {count}',
  'ai.streaming.done': 'Completed {count} steps',

  /* Answer & skill results */
  'ai.answer.title': 'Conclusion',
  'ai.answer.tokens': '{count} tokens',
  'ai.answer.steps': '{count} steps',
  'ai.result.sql': 'Candidate SQL',
  'ai.result.confidence': 'Confidence',
  'ai.result.missing': 'Missing fields',
  'ai.result.chart': 'Chart recommendation',
  'ai.result.chart.viz': 'Chart type',
  'ai.result.chart.metrics': 'Metrics',
  'ai.result.chart.groupby': 'Dimensions',
  'ai.result.chart.reason': 'Rationale',
  'ai.result.chart.alternatives': 'Alternatives',
  'ai.result.metrics': 'Definition alignment',
  'ai.result.data': 'Data',
  'ai.result.raw': 'Raw output',

  /* Write operations & rollback */
  'ai.write.title': 'Write operations',
  'ai.write.desc': 'This run modified the objects below; they can be rolled back as a whole.',
  'ai.write.target_ids': 'Target IDs',
  'ai.write.rollback_confirm': 'Roll back the write operations of this run?',
  'ai.write.confirm_required': 'This deployment requires manual confirmation for write operations; verify the result before rolling back.',
  'ai.write.rollback_done': 'Rolled back {rolled}, skipped {skipped}',
  'ai.write.rollback_detail': 'Details',

  /* Artifacts applied back to the page (P5.2) */
  'ai.artifacts.sql_applied': 'AI-generated SQL written into the editor',
  'ai.artifacts.dashboard_refreshed': 'Dashboard refreshed with the AI-generated charts',

  /* History */
  'ai.history.empty': 'No runs yet',

  /* States */
  'ai.disabled.title': 'AI assistant is disabled',
  'ai.disabled.desc': 'The backend has AI turned off (AI_ASSISTANT_ENABLED=false). Ask an administrator to enable it.',
  'ai.error.title': 'Run failed',
  'ai.empty.answer': 'No conclusion yet',
  /* Resume (reconnect and carry on) */
  'ai.resume.running': 'This analysis is still running in the background; check History for its result.',
  'ai.resume.done': 'This analysis already finished — just ask a follow-up.',
  'ai.resume.no_steps': 'This analysis has no completed steps; please ask again.',
  'ai.resume.unavailable': 'This analysis cannot be continued; please ask again.',

  /* Agent configuration page */
  'settings.ai.subtitle':
    'Manage LLM providers and plug-in agents as tables. Changes apply immediately, with no restart.',
  'settings.ai.section.general': 'Global master switch',
  'settings.ai.enabled': 'Enable the AI Agent',
  'settings.ai.enabled_hint':
    'When off, every Agent endpoint under /api/v1/ai returns 503, but this page stays reachable.',
  'settings.ai.saved': 'Saved and applied immediately',
  'settings.ai.save': 'Save',
  'settings.ai.load_error': 'Could not load the configuration',
  'settings.ai.read_only':
    'This account only has read access (AIAgent.can_read); contact an administrator to change it.',

  /* Shared row actions */
  'settings.ai.actions.title': 'Actions',
  'settings.ai.actions.edit': 'Edit',
  'settings.ai.actions.view': 'View',
  'settings.ai.actions.delete': 'Delete',
  'settings.ai.actions.confirm': 'OK',
  'settings.ai.actions.cancel': 'Cancel',

  /* LLM providers */
  'settings.ai.providers.title': 'LLM providers',
  'settings.ai.providers.hint':
    'Each provider is an LLM endpoint an agent can bind to; the key stays server-side and is never echoed.',
  'settings.ai.providers.new': 'New provider',
  'settings.ai.providers.edit': 'Edit provider',
  'settings.ai.providers.empty': 'No providers yet — use “New provider” above to add one.',
  'settings.ai.providers.name': 'Name',
  'settings.ai.providers.name_required': 'Enter a provider name',
  'settings.ai.providers.type': 'API protocol',
  'settings.ai.providers.base_url': 'Base URL',
  'settings.ai.providers.base_url_required': 'Enter the base URL',
  'settings.ai.providers.model': 'Model',
  'settings.ai.providers.status': 'Status',
  'settings.ai.providers.enabled': 'Enabled',
  'settings.ai.providers.disabled': 'Disabled',
  'settings.ai.providers.description': 'Notes',
  'settings.ai.providers.created': 'Provider created',
  'settings.ai.providers.updated': 'Provider updated',
  'settings.ai.providers.deleted': 'Provider deleted',
  'settings.ai.providers.delete_confirm': 'Delete provider “{name}”?',

  /* Provider editor (sections + connection test) */
  'settings.ai.providers.section.basic': 'Basics',
  'settings.ai.providers.section.connection': 'Connection',
  'settings.ai.providers.section.behavior': 'Runtime',
  'settings.ai.providers.name_placeholder': 'e.g. DeepSeek production / local Ollama',
  'settings.ai.providers.model_placeholder': 'e.g. deepseek-chat',
  'settings.ai.providers.description_placeholder': 'Optional note about this endpoint or its owner.',
  'settings.ai.providers.test': 'Test connection',
  'settings.ai.providers.testing': 'Testing connection',
  'settings.ai.providers.test_ok': 'Connection succeeded',
  'settings.ai.providers.test_failed': 'Connection failed',
  'settings.ai.providers.test_hint':
    'The test reads the model list first (no tokens spent) and falls back to one minimal chat call when the endpoint lacks it.',
  'settings.ai.providers.test_endpoint': 'Endpoint: {endpoint}',
  'settings.ai.providers.test_latency': '{ms} ms',
  'settings.ai.providers.test_models': '{count} models available',
  'settings.ai.providers.test_models_list': 'Available models (first {count})',
  'settings.ai.providers.test_model_used': 'Verified model: {model}',
  'settings.ai.providers.test_required': 'Fill in the base URL and protocol first',
  'settings.ai.providers.test_saved_key': 'This test reused the stored API key',

  /* Shared LLM form fields */
  'settings.ai.provider_hint':
    'openai_compatible covers OpenAI, vLLM, DeepSeek and any service exposing /chat/completions.',
  'settings.ai.base_url_hint':
    'Without the /chat/completions suffix, e.g. http://127.0.0.1:11434/v1',
  'settings.ai.api_key': 'API Key',
  'settings.ai.api_key_hint':
    'For security the stored key is never echoed; leave blank to keep it unchanged.',
  'settings.ai.api_key_configured': 'Configured',
  'settings.ai.api_key_not_configured': 'Not configured',
  'settings.ai.api_key_placeholder': 'Leave blank to keep the current key',
  'settings.ai.api_key_empty': 'Not needed for local services such as Ollama',
  'settings.ai.timeout': 'Request timeout (seconds)',
  'settings.ai.timeout_hint':
    'Maximum wait per LLM request; timeouts trigger retry and circuit breaking.',

  /* Agents (plug-in) */
  'settings.ai.agents.title': 'Agents',
  'settings.ai.agents.hint':
    'An agent is plug-in configuration: bind a provider and pick the skills and tools it may use (none selected = all).',
  'settings.ai.agents.new': 'New agent',
  'settings.ai.agents.edit': 'Edit agent',
  'settings.ai.agents.empty':
    'No agents yet — an unconfigured system falls back to the deployment default.',
  'settings.ai.agents.name': 'Name',
  'settings.ai.agents.name_required': 'Enter an agent name',
  'settings.ai.agents.description': 'Description',
  'settings.ai.agents.provider': 'Bound provider',
  'settings.ai.agents.provider_hint': 'Leave empty to use the deployment default endpoint.',
  'settings.ai.agents.provider_fallback': 'System default (config)',
  'settings.ai.agents.skills': 'Allowed skills',
  'settings.ai.agents.skills_hint': 'None selected means every skill is allowed.',
  'settings.ai.agents.tools': 'Allowed tools',
  'settings.ai.agents.tools_hint': 'None selected means every tool is allowed.',
  'settings.ai.agents.group.read': 'Read tools (data access)',
  'settings.ai.agents.group.write': 'Write tools (mutations)',
  'settings.ai.agents.unwired': 'not wired',
  'settings.ai.agents.tools_count': '{n} selected · empty means all tools',
  'settings.ai.agents.select_read_tools': 'Select all read tools',
  'settings.ai.agents.use_all': 'Reset to all',
  'settings.ai.agents.all': 'All',
  'settings.ai.agents.count': '{count} selected',
  'settings.ai.agents.status': 'Status',
  'settings.ai.agents.is_default': 'Default agent',
  'settings.ai.agents.default_tag': 'Default',
  'settings.ai.agents.created': 'Agent created',
  'settings.ai.agents.updated': 'Agent updated',
  'settings.ai.agents.deleted': 'Agent deleted',
  'settings.ai.agents.delete_confirm': 'Delete agent “{name}”?',
  'settings.ai.agents.builtin_hint':
    'Built-in agents are shipped with the product. They are read-only: they cannot be edited or deleted.',

  /* Agent guardrails */
  'settings.ai.section.guardrails': 'Agent guardrails',
  'settings.ai.max_steps': 'Max steps',
  'settings.ai.max_tool_calls': 'Max tool calls',
  'settings.ai.token_budget': 'Token budget',
  'settings.ai.run_timeout': 'Run timeout (seconds)',
  'settings.ai.write_tools': 'Allow write tools',
  'settings.ai.write_tools_hint':
    'Lets the Agent create charts and change configuration; snapshots are taken before each write.',
  'settings.ai.write_confirm': 'Require confirmation for writes',
  'settings.ai.write_confirm_hint':
    'Blocks write operations until a human confirms them.',

  /* Settings tabs */
  'settings.ai.tab.basic': 'Basic',
  'settings.ai.tab.agents': 'Agents',
  'settings.ai.tab.skills': 'Skills',
  'settings.ai.tab.tools': 'Tools',

  /* Shared plugin vocabulary */
  'settings.ai.plugins.source': 'Source',
  'settings.ai.plugins.builtin': 'Built-in',
  'settings.ai.plugins.custom': 'Custom',
  'settings.ai.plugins.state': 'Status',
  'settings.ai.plugins.enabled': 'Enabled',
  'settings.ai.plugins.disabled': 'Disabled',
  'settings.ai.plugins.unwired': 'Not wired',
  'settings.ai.plugins.code': 'Code',
  'settings.ai.plugins.code_hint':
    'The code is how an agent references this capability: lowercase letters, digits and underscores, immutable once created.',
  'settings.ai.plugins.code_invalid':
    'Code must start with a lowercase letter and contain only lowercase letters, digits and underscores (2-64 chars)',
  'settings.ai.plugins.input_schema': 'Parameter schema',
  'settings.ai.plugins.input_schema_hint': 'Optional JSON Schema; leave blank for no parameters.',
  'settings.ai.plugins.builtin_hint':
    'Built-in capabilities are implemented in code. They are read-only: they cannot be edited or deleted.',

  /* Skills */
  'settings.ai.skills.title': 'Skill plugins',
  'settings.ai.skills.hint':
    'A skill is a capability the agent can call. Built-in skills are implemented in code; a custom skill is a prompt the model follows with the call arguments.',
  'settings.ai.skills.new': 'New skill',
  'settings.ai.skills.edit': 'Edit skill',
  'settings.ai.skills.empty': 'No skills configured yet.',
  'settings.ai.skills.name': 'Skill name',
  'settings.ai.skills.name_required': 'Please enter a skill name',
  'settings.ai.skills.domain': 'Domain',
  'settings.ai.skills.cost_tier': 'Cost tier',
  'settings.ai.skills.description': 'Description',
  'settings.ai.skills.tools': 'Referenced tools',
  'settings.ai.skills.tools_hint':
    'The skill may read data through these tools; selected codes are written into the prompt.',
  'settings.ai.skills.tools_placeholder': 'Optional',
  'settings.ai.skills.instructions': 'Skill instructions (prompt)',
  'settings.ai.skills.instructions_hint':
    'Describe what the skill does and what format it answers in; this text becomes the system prompt.',
  'settings.ai.skills.instructions_required': 'A custom skill needs instructions',
  'settings.ai.skills.instructions_placeholder':
    'e.g. You are a finance metric reviewer. Check the metric definition in the arguments for contradictions and answer in Markdown.',
  'settings.ai.skills.created': 'Skill created',
  'settings.ai.skills.updated': 'Skill updated',
  'settings.ai.skills.deleted': 'Skill deleted',
  'settings.ai.skills.delete_confirm':
    'Delete skill "{name}"? Agents bound to it will lose the capability.',

  /* Tools */
  'settings.ai.tools.title': 'Tool plugins',
  'settings.ai.tools.hint':
    'A tool is how the agent reads or writes external data. Built-in tools are implemented in code; a custom tool calls an HTTP endpoint, with {{arg}} placeholders in the URL and body.',
  'settings.ai.tools.new': 'New tool',
  'settings.ai.tools.edit': 'Edit tool',
  'settings.ai.tools.empty': 'No tools configured yet.',
  'settings.ai.tools.name': 'Tool name',
  'settings.ai.tools.name_required': 'Please enter a tool name',
  'settings.ai.tools.description': 'Description',
  'settings.ai.tools.kind': 'Type',
  'settings.ai.tools.kind_http': 'HTTP endpoint',
  'settings.ai.tools.side_effect': 'Side effect',
  'settings.ai.tools.method': 'Method',
  'settings.ai.tools.url': 'Endpoint',
  'settings.ai.tools.url_hint':
    'Must be http(s); supports {{arg}} placeholders, e.g. https://api.example.com/fx?pair={{pair}}',
  'settings.ai.tools.url_required': 'Please enter the endpoint',
  'settings.ai.tools.headers': 'Headers',
  'settings.ai.tools.headers_hint':
    'JSON object; values support {{arg}} placeholders. Secrets belong here and are never echoed to end users.',
  'settings.ai.tools.body': 'Body template',
  'settings.ai.tools.body_hint':
    'JSON template; values support {{arg}} placeholders. Leave blank for GET.',
  'settings.ai.tools.timeout': 'Timeout (s)',
  'settings.ai.tools.created': 'Tool created',
  'settings.ai.tools.updated': 'Tool updated',
  'settings.ai.tools.deleted': 'Tool deleted',
  'settings.ai.tools.delete_confirm':
    'Delete tool "{name}"? Agents bound to it will lose the capability.',

  /* Analysis session + save-as-chart closed loop */
  'ai.session.title': 'Session context',
  'ai.session.tag': 'Follow-ups carry prior context',
  'ai.memory.button': 'Save as example',
  'ai.memory.hint': 'Store this question and SQL as an example that similar questions will reuse',
  'ai.memory.saved': 'Saved; similar questions will reuse this SQL',
  'ai.memory.unavailable': 'This answer has no SQL to save, or no dataset is bound',
  'ai.save_chart.button': 'Save as chart',
  'ai.save_chart.success': 'Chart "{name}" created',
  'ai.save_chart.success_dashboard': 'Chart "{name}" created and added to this dashboard',
  'ai.save_chart.unavailable': 'This answer has no chart-ready data to save',

  /* Chat page (AI Analysis) */
  'ai.chat.hero_title': 'What would you like to know?',
  'ai.chat.hero_desc':
    'Attach a dataset with the "+" below, then ask for a year-over-year, composition, trend or ranking analysis.',
  'ai.chat.assistant': 'Agent',
  'ai.chat.new_chat': 'New chat',
  'ai.chat.new_chat_done': 'Started a new conversation',
  'ai.chat.dataset.attach': 'Attach a dataset',
  'ai.chat.dataset.title': 'Pick the dataset to analyse',
  'ai.chat.dataset.search': 'Search dataset name',
  'ai.chat.dataset.empty': 'No dataset available yet',
  'ai.chat.dataset.remove': 'Remove',
  'ai.chat.agent': 'Choose an agent',
  'ai.chat.agent.default': 'Default agent',
  'ai.chat.input_placeholder': 'Ask a data question — Enter to send, Shift + Enter for a new line',
  'ai.chat.disclaimer': 'Answers are AI-generated; verify the numbers before deciding.',
  'ai.chat.markdown_hint': 'Markdown supported · answers export as .md',
} as const;
