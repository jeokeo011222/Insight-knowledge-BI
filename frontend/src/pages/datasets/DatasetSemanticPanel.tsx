/**
 * Dataset → 语义模型 (MDL-lite).
 *
 * Why this lives inside the dataset drawer rather than a top-level menu: the
 * retired metric-dictionary page proved that a standalone "governance" screen
 * becomes an orphan. Editing a model is always a task you start from a dataset,
 * so that is where the tab belongs.
 *
 * The panel has one job per tab and refuses to mix them:
 *   字段    what exists (and what the Agent may see)
 *   关系    how two models join
 *   规则    业务口径（提示词 + 执行期默认过滤）
 *   缺口    what is still missing, one question at a time
 */
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert, Button, Empty, Form, Input, Modal, Progress, Select, Space, Switch, Table, Tabs, Tag, Tooltip, message,
} from 'antd';
import {
  CheckOutlined, CloudDownloadOutlined, DeleteOutlined, ExportOutlined, PlusOutlined,
  ReloadOutlined, RocketOutlined, ThunderboltOutlined,
} from '@ant-design/icons';
import { errorMessage } from '../../api/client';
import { useI18n } from '../../i18n';
import {
  applySemanticGap, coldStartSemanticModel, createSemanticRelationship, createSemanticRule,
  deleteSemanticModel, deleteSemanticRelationship, deleteSemanticRule, getSemanticModel,
  listSemanticModels, publishSemanticModel, scanSemanticGaps, semanticExportUrl, updateSemanticModel,
} from '../../api/semantic';
import type {
  SemanticFieldRow, SemanticGap, SemanticGapReport, SemanticModelRow, SemanticModelView,
  SemanticRelationshipRow, SemanticRuleRow,
} from '../../api/semantic';

const RULE_CATEGORIES = [
  'default_filter', 'naming', 'currency', 'time', 'external_id', 'canonical_table',
] as const;

const FIELD_KINDS = ['column', 'metric', 'dimension', 'time'] as const;

const PII_LEVELS = ['none', 'low', 'high'] as const;

interface Props {
  datasetId: number;
  canWrite: boolean;
}

export function DatasetSemanticPanel({ datasetId, canWrite }: Props) {
  const { t } = useI18n();
  const [loading, setLoading] = useState(false);
  const [models, setModels] = useState<SemanticModelRow[]>([]);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [view, setView] = useState<SemanticModelView | null>(null);
  const [fields, setFields] = useState<SemanticFieldRow[]>([]);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [activeKey, setActiveKey] = useState('fields');

  const [relOpen, setRelOpen] = useState(false);
  const [ruleOpen, setRuleOpen] = useState(false);
  const [relForm] = Form.useForm();
  const [ruleForm] = Form.useForm();

  const [gapReport, setGapReport] = useState<SemanticGapReport | null>(null);
  const [gapLoading, setGapLoading] = useState(false);
  const [gapIndex, setGapIndex] = useState(0);
  const [gapDraft, setGapDraft] = useState('');

  const loadModels = useCallback(async () => {
    setLoading(true);
    try {
      const list = await listSemanticModels(datasetId);
      setModels(list);
      setActiveId(current => {
        if (current && list.some(m => m.id === current)) return current;
        return list[0]?.id ?? null;
      });
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }, [datasetId]);

  const loadView = useCallback(async (id: number) => {
    setLoading(true);
    try {
      const next = await getSemanticModel(id);
      setView(next);
      setFields(next.fields);
      setDirty(false);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadModels();
  }, [loadModels]);

  useEffect(() => {
    if (activeId) void loadView(activeId);
    else setView(null);
  }, [activeId, loadView]);

  const coldStart = async (force = false) => {
    setSaving(true);
    try {
      const created = await coldStartSemanticModel(datasetId, force);
      message.success(t('dataset.semantic.generated'));
      await loadModels();
      setActiveId(created.model.id);
      await loadView(created.model.id);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const saveFields = async () => {
    if (!view) return;
    setSaving(true);
    try {
      const next = await updateSemanticModel(view.model.id, {
        fields: fields.map(f => ({
          name: f.name,
          kind: f.kind,
          expression: f.expression ?? '',
          type: f.type ?? '',
          description: f.description ?? '',
          unit: f.unit ?? '',
          enum_values: f.enum_values ?? undefined,
          hidden: f.hidden,
          pii_level: f.pii_level,
          default_agg: f.default_agg ?? '',
          is_dttm: f.is_dttm,
          sort_order: f.sort_order,
        })),
      });
      setView(next);
      setFields(next.fields);
      setDirty(false);
      message.success(t('dataset.semantic.saved'));
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const publish = async () => {
    if (!view) return;
    setSaving(true);
    try {
      const next = await publishSemanticModel(view.model.id);
      setView(next);
      message.success(t('dataset.semantic.published'));
      await loadModels();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const removeModel = async () => {
    if (!view) return;
    Modal.confirm({
      title: t('dataset.semantic.delete_confirm'),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await deleteSemanticModel(view.model.id);
          setActiveId(null);
          setView(null);
          await loadModels();
          message.success(t('dataset.semantic.deleted'));
        } catch (error) {
          message.error(errorMessage(error));
        }
      },
    });
  };

  const scanGaps = async (sample: boolean) => {
    setGapLoading(true);
    try {
      const report = await scanSemanticGaps(datasetId, sample);
      setGapReport(report);
      setGapIndex(0);
      setGapDraft(report.gaps[0]?.draft ?? '');
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setGapLoading(false);
    }
  };

  const currentGap: SemanticGap | undefined = gapReport?.gaps[gapIndex];

  const advanceGap = () => {
    if (!gapReport) return;
    const next = gapIndex + 1;
    setGapIndex(next);
    setGapDraft(gapReport.gaps[next]?.draft ?? '');
  };

  const applyGap = async () => {
    if (!currentGap) return;
    setSaving(true);
    try {
      await applySemanticGap(datasetId, currentGap.id, gapDraft);
      message.success(t('dataset.semantic.gap_applied'));
      if (activeId) await loadView(activeId);
      advanceGap();
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const fieldColumns = useMemo(
    () => [
      {
        title: t('dataset.semantic.field_name'),
        dataIndex: 'name',
        width: 170,
        render: (value: string, row: SemanticFieldRow) => (
          <Space direction="vertical" size={2}>
            <span className="mono">{value}</span>
            {row.is_dttm ? <Tag color="blue">{t('dataset.detail.col_time_tag')}</Tag> : null}
          </Space>
        ),
      },
      {
        title: t('dataset.semantic.field_kind'),
        dataIndex: 'kind',
        width: 130,
        render: (value: string, row: SemanticFieldRow) => (
          <Select
            size="small"
            value={value}
            disabled={!canWrite}
            style={{ width: 110 }}
            options={FIELD_KINDS.map(k => ({ value: k, label: k }))}
            onChange={next => {
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, kind: next as SemanticFieldRow['kind'] } : f)));
              setDirty(true);
            }}
          />
        ),
      },
      {
        title: t('dataset.semantic.field_expression'),
        dataIndex: 'expression',
        width: 220,
        render: (value: string, row: SemanticFieldRow) => (
          <Input
            size="small"
            value={value}
            disabled={!canWrite}
            placeholder={row.name}
            onChange={event => {
              const next = event.target.value;
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, expression: next } : f)));
              setDirty(true);
            }}
          />
        ),
      },
      {
        title: t('common.label.description'),
        dataIndex: 'description',
        render: (value: string, row: SemanticFieldRow) => (
          <Input
            size="small"
            value={value}
            disabled={!canWrite}
            placeholder={t('dataset.semantic.description_hint')}
            onChange={event => {
              const next = event.target.value;
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, description: next } : f)));
              setDirty(true);
            }}
          />
        ),
      },
      {
        title: t('dataset.semantic.field_unit'),
        dataIndex: 'unit',
        width: 100,
        render: (value: string, row: SemanticFieldRow) => (
          <Input
            size="small"
            value={value}
            disabled={!canWrite}
            onChange={event => {
              const next = event.target.value;
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, unit: next } : f)));
              setDirty(true);
            }}
          />
        ),
      },
      {
        title: t('dataset.agent.visible'),
        dataIndex: 'hidden',
        width: 110,
        render: (hidden: boolean, row: SemanticFieldRow) => (
          <Switch
            size="small"
            checked={!hidden}
            disabled={!canWrite}
            onChange={checked => {
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, hidden: !checked } : f)));
              setDirty(true);
            }}
          />
        ),
      },
      {
        title: t('dataset.agent.pii_level'),
        dataIndex: 'pii_level',
        width: 110,
        render: (value: string, row: SemanticFieldRow) => (
          <Select
            size="small"
            value={value || 'none'}
            disabled={!canWrite}
            style={{ width: 90 }}
            options={PII_LEVELS.map(level => ({ value: level, label: level }))}
            onChange={next => {
              setFields(prev => prev.map(f => (f.id === row.id ? { ...f, pii_level: next } : f)));
              setDirty(true);
            }}
          />
        ),
      },
    ],
    [canWrite, t],
  );

  const relColumns = [
    {
      title: t('dataset.semantic.rel_from'),
      dataIndex: 'from_model_id',
      width: 140,
      render: (id: number) => models.find(m => m.id === id)?.name ?? `#${id}`,
    },
    {
      title: t('dataset.semantic.rel_to'),
      dataIndex: 'to_model_id',
      width: 140,
      render: (id: number) => models.find(m => m.id === id)?.name ?? `#${id}`,
    },
    { title: t('dataset.semantic.rel_type'), dataIndex: 'join_type', width: 130 },
    { title: t('dataset.semantic.rel_condition'), dataIndex: 'condition', render: (v: string) => <span className="mono">{v}</span> },
    {
      title: '',
      width: 50,
      render: (_: unknown, row: SemanticRelationshipRow) =>
        canWrite ? (
          <Button
            type="text"
            danger
            size="small"
            icon={<DeleteOutlined />}
            onClick={async () => {
              try {
                await deleteSemanticRelationship(row.id);
                if (activeId) await loadView(activeId);
              } catch (error) {
                message.error(errorMessage(error));
              }
            }}
          />
        ) : null,
    },
  ];

  const ruleColumns = [
    {
      title: t('dataset.semantic.rule_category'),
      dataIndex: 'category',
      width: 150,
      render: (value: string) => <Tag>{value}</Tag>,
    },
    { title: t('dataset.semantic.rule_title'), dataIndex: 'title', width: 180 },
    { title: t('dataset.semantic.rule_body'), dataIndex: 'body' },
    {
      title: t('dataset.semantic.rule_scope'),
      dataIndex: 'scope',
      width: 100,
      render: (value: string) => <Tag color={value === 'global' ? 'gold' : 'default'}>{value}</Tag>,
    },
    {
      title: '',
      width: 50,
      render: (_: unknown, row: SemanticRuleRow) =>
        canWrite ? (
          <Button
            type="text"
            danger
            size="small"
            icon={<DeleteOutlined />}
            onClick={async () => {
              try {
                await deleteSemanticRule(row.id);
                if (activeId) await loadView(activeId);
              } catch (error) {
                message.error(errorMessage(error));
              }
            }}
          />
        ) : null,
    },
  ];

  /* --------------------------------------------------------------- empty */

  if (!loading && models.length === 0) {
    return (
      <div style={{ padding: '32px 0' }}>
        <Empty description={t('dataset.semantic.empty')}>
          <Space direction="vertical" size={12} style={{ maxWidth: 520 }}>
            <div className="tiny-label">{t('dataset.semantic.empty_hint')}</div>
            {canWrite ? (
              <Button type="primary" icon={<ThunderboltOutlined />} loading={saving} onClick={() => void coldStart(false)}>
                {t('dataset.semantic.generate')}
              </Button>
            ) : null}
          </Space>
        </Empty>
      </div>
    );
  }

  const errorIssues = view?.issues.filter(i => i.severity === 'error') ?? [];
  const warningIssues = view?.issues.filter(i => i.severity === 'warning') ?? [];

  return (
    <div>
      <Space wrap style={{ marginBottom: 12 }}>
        <Select
          style={{ minWidth: 200 }}
          value={activeId ?? undefined}
          loading={loading}
          options={models.map(m => ({
            value: m.id,
            label: `${m.name}${m.status === 'published' ? ' ✓' : ''}`,
          }))}
          onChange={value => setActiveId(value)}
        />
        {view ? (
          <>
            <Tag color={view.model.status === 'published' ? 'green' : 'default'}>
              {view.model.status === 'published' ? t('dataset.semantic.status_published') : t('dataset.semantic.status_draft')}
            </Tag>
            <Tooltip title={t('dataset.semantic.hash_hint')}>
              <Tag className="mono">{(view.model.manifest_hash || '—').slice(0, 10)}</Tag>
            </Tooltip>
            <span className="tiny-label">v{view.model.version}</span>
          </>
        ) : null}
        <span style={{ flex: 1 }} />
        {canWrite ? (
          <Button size="small" icon={<RocketOutlined />} loading={saving} disabled={!view || !view.publishable} onClick={() => void publish()}>
            {t('dataset.semantic.publish')}
          </Button>
        ) : null}
        <Button
          size="small"
          icon={<ExportOutlined />}
          onClick={() => window.open(semanticExportUrl(datasetId, 'json'), '_blank')}
        >
          JSON
        </Button>
        <Button
          size="small"
          icon={<CloudDownloadOutlined />}
          onClick={() => window.open(semanticExportUrl(datasetId, 'yaml'), '_blank')}
        >
          YAML
        </Button>
        {canWrite ? (
          <>
            <Button size="small" icon={<ReloadOutlined />} loading={saving} onClick={() => void coldStart(true)}>
              {t('dataset.semantic.regenerate')}
            </Button>
            <Button size="small" danger icon={<DeleteOutlined />} onClick={() => void removeModel()}>
              {t('common.actions.delete')}
            </Button>
          </>
        ) : null}
      </Space>

      {errorIssues.length > 0 ? (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('dataset.semantic.issues_error', { count: errorIssues.length })}
          description={
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {errorIssues.map(issue => (
                <li key={issue.code + issue.field + issue.message}>
                  {issue.field ? <b className="mono">{issue.field}</b> : null} {issue.message}
                  {issue.hint ? <span className="tiny-label"> — {issue.hint}</span> : null}
                </li>
              ))}
            </ul>
          }
        />
      ) : warningIssues.length > 0 ? (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={t('dataset.semantic.issues_warning', { count: warningIssues.length })}
          description={
            <ul style={{ margin: 0, paddingLeft: 18 }}>
              {warningIssues.slice(0, 6).map(issue => (
                <li key={issue.code + issue.field + issue.message}>{issue.message}</li>
              ))}
            </ul>
          }
        />
      ) : null}

      <Tabs
        activeKey={activeKey}
        onChange={setActiveKey}
        items={[
          {
            key: 'fields',
            label: t('dataset.semantic.tab_fields', { count: fields.length }),
            children: (
              <>
                <Space style={{ marginBottom: 8 }}>
                  {canWrite ? (
                    <Button type="primary" size="small" loading={saving} disabled={!dirty} onClick={() => void saveFields()}>
                      {t('common.actions.save')}
                    </Button>
                  ) : null}
                  {dirty ? <span className="tiny-label">{t('dataset.semantic.unsaved')}</span> : null}
                </Space>
                <Table<SemanticFieldRow>
                  size="small"
                  rowKey="id"
                  loading={loading}
                  columns={fieldColumns}
                  dataSource={fields}
                  pagination={{ pageSize: 12, size: 'small', showSizeChanger: false }}
                  scroll={{ x: 1180 }}
                />
              </>
            ),
          },
          {
            key: 'relations',
            label: t('dataset.semantic.tab_relations', { count: view?.relationships.length ?? 0 }),
            children: (
              <>
                {canWrite ? (
                  <Space style={{ marginBottom: 8 }}>
                    <Button size="small" icon={<PlusOutlined />} onClick={() => setRelOpen(true)}>
                      {t('dataset.semantic.new_relation')}
                    </Button>
                  </Space>
                ) : null}
                <Table<SemanticRelationshipRow>
                  size="small"
                  rowKey="id"
                  columns={relColumns}
                  dataSource={view?.relationships ?? []}
                  pagination={false}
                />
              </>
            ),
          },
          {
            key: 'rules',
            label: t('dataset.semantic.tab_rules', { count: view?.rules.length ?? 0 }),
            children: (
              <>
                <Space style={{ marginBottom: 8 }}>
                  {canWrite ? (
                    <Button size="small" icon={<PlusOutlined />} onClick={() => setRuleOpen(true)}>
                      {t('dataset.semantic.new_rule')}
                    </Button>
                  ) : null}
                  <span className="tiny-label">{t('dataset.semantic.rule_hint')}</span>
                </Space>
                <Table<SemanticRuleRow>
                  size="small"
                  rowKey="id"
                  columns={ruleColumns}
                  dataSource={view?.rules ?? []}
                  pagination={{ pageSize: 10, size: 'small', showSizeChanger: false }}
                />
              </>
            ),
          },
          {
            key: 'gaps',
            label: t('dataset.semantic.tab_gaps'),
            children: (
              <div>
                <Space wrap style={{ marginBottom: 12 }}>
                  <Button size="small" loading={gapLoading} onClick={() => void scanGaps(false)}>
                    {t('dataset.semantic.scan')}
                  </Button>
                  <Button size="small" loading={gapLoading} onClick={() => void scanGaps(true)}>
                    {t('dataset.semantic.scan_sample')}
                  </Button>
                  <span className="tiny-label">{t('dataset.semantic.scan_hint')}</span>
                </Space>

                {gapReport ? (
                  <>
                    <Progress
                      percent={gapReport.score}
                      size="small"
                      status={gapReport.score >= 80 ? 'success' : 'active'}
                      format={value => `${value}% ${t('dataset.semantic.health')}`}
                    />
                    <div className="tiny-label" style={{ marginBottom: 12 }}>
                      {t('dataset.semantic.coverage', {
                        described: String(gapReport.coverage.described_columns ?? 0),
                        total: String(gapReport.coverage.columns ?? 0),
                        gaps: gapReport.gaps.length,
                      })}
                      {gapReport.note ? ` · ${gapReport.note}` : ''}
                    </div>

                    {currentGap ? (
                      <div style={{ border: '1px solid var(--color-border)', borderRadius: 8, padding: 16, background: 'var(--color-bg-container)' }}>
                        <Space style={{ marginBottom: 8 }}>
                          <Tag color={currentGap.severity === 'high' ? 'red' : currentGap.severity === 'medium' ? 'orange' : 'default'}>
                            {currentGap.severity}
                          </Tag>
                          <Tag>{currentGap.kind}</Tag>
                          <span className="tiny-label">
                            {gapIndex + 1} / {gapReport.gaps.length}
                          </span>
                        </Space>
                        <div style={{ fontSize: 15, fontWeight: 600, marginBottom: 6 }}>{currentGap.question}</div>
                        <div className="tiny-label mono" style={{ marginBottom: 10 }}>
                          {currentGap.target} · {currentGap.evidence}
                        </div>
                        <Input.TextArea
                          rows={3}
                          value={gapDraft}
                          disabled={!canWrite}
                          onChange={event => setGapDraft(event.target.value)}
                        />
                        <Space style={{ marginTop: 12 }}>
                          {canWrite && currentGap.apply_mode ? (
                            <Button type="primary" size="small" icon={<CheckOutlined />} loading={saving} onClick={() => void applyGap()}>
                              {t('dataset.semantic.adopt')}
                            </Button>
                          ) : null}
                          <Button size="small" onClick={advanceGap}>
                            {t('dataset.semantic.skip')}
                          </Button>
                          <span className="tiny-label">
                            {currentGap.confidence === 'high'
                              ? t('dataset.semantic.confidence_high')
                              : currentGap.confidence === 'medium'
                                ? t('dataset.semantic.confidence_medium')
                                : t('dataset.semantic.confidence_low')}
                          </span>
                        </Space>
                      </div>
                    ) : (
                      <Alert type="success" showIcon message={t('dataset.semantic.gaps_done')} />
                    )}
                  </>
                ) : (
                  <Empty description={t('dataset.semantic.gap_empty')} />
                )}
              </div>
            ),
          },
        ]}
      />

      <Modal
        title={t('dataset.semantic.new_relation')}
        open={relOpen}
        onCancel={() => setRelOpen(false)}
        onOk={async () => {
          try {
            const values = await relForm.validateFields();
            if (!view) return;
            await createSemanticRelationship(view.model.id, values);
            setRelOpen(false);
            relForm.resetFields();
            await loadView(view.model.id);
            message.success(t('dataset.semantic.saved'));
          } catch (error) {
            if (error && typeof error === 'object' && 'errorFields' in error) return;
            message.error(errorMessage(error));
          }
        }}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
      >
        <Form form={relForm} layout="vertical" initialValues={{ join_type: 'many_to_one' }}>
          <Form.Item name="to_model_id" label={t('dataset.semantic.rel_to')} rules={[{ required: true }]}>
            <Select options={(models ?? []).filter(m => m.id !== view?.model.id).map(m => ({ value: m.id, label: m.name }))} />
          </Form.Item>
          <Form.Item name="join_type" label={t('dataset.semantic.rel_type')} rules={[{ required: true }]}>
            <Select
              options={['many_to_one', 'one_to_many', 'one_to_one', 'many_to_many'].map(v => ({ value: v, label: v }))}
            />
          </Form.Item>
          <Form.Item
            name="condition"
            label={t('dataset.semantic.rel_condition')}
            rules={[{ required: true }]}
            extra="orders.customer_id = customers.id"
          >
            <Input.TextArea rows={2} className="mono" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={t('dataset.semantic.new_rule')}
        open={ruleOpen}
        onCancel={() => setRuleOpen(false)}
        onOk={async () => {
          try {
            const values = await ruleForm.validateFields();
            // `spec` is edited as JSON text; only the machine-readable half of a
            // default_filter is executable, so a typo must fail here rather than
            // silently becoming a prompt-only rule.
            let spec: unknown;
            if (typeof values.spec === 'string' && values.spec.trim()) {
              try {
                spec = JSON.parse(values.spec);
              } catch {
                message.error(t('dataset.semantic.rule_spec_invalid'));
                return;
              }
            }
            await createSemanticRule(datasetId, { ...values, spec });
            setRuleOpen(false);
            ruleForm.resetFields();
            if (activeId) await loadView(activeId);
            message.success(t('dataset.semantic.saved'));
          } catch (error) {
            if (error && typeof error === 'object' && 'errorFields' in error) return;
            message.error(errorMessage(error));
          }
        }}
        okText={t('common.actions.save')}
        cancelText={t('common.actions.cancel')}
      >
        <Form form={ruleForm} layout="vertical" initialValues={{ scope: 'dataset', category: 'naming' }}>
          <Form.Item name="category" label={t('dataset.semantic.rule_category')} rules={[{ required: true }]}>
            <Select options={RULE_CATEGORIES.map(c => ({ value: c, label: c }))} />
          </Form.Item>
          <Form.Item name="title" label={t('dataset.semantic.rule_title')}>
            <Input maxLength={120} />
          </Form.Item>
          <Form.Item name="body" label={t('dataset.semantic.rule_body')} rules={[{ required: true }]}>
            <Input.TextArea rows={4} />
          </Form.Item>
          <Form.Item
            name="spec"
            label={t('dataset.semantic.rule_spec')}
            extra='{"field":"deleted_at","op":"is_null"}'
          >
            <Input.TextArea rows={2} className="mono" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
