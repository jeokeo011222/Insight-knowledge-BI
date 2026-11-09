import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Card,
  Col,
  Divider,
  Empty,
  Modal,
  Popconfirm,
  Row,
  Select,
  Space,
  Statistic,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  ApiOutlined,
  CheckCircleOutlined,
  CloudUploadOutlined,
  CodeOutlined,
  DeleteOutlined,
  DownloadOutlined,
  ExclamationCircleOutlined,
  FileProtectOutlined,
  ReloadOutlined,
  RobotOutlined,
  ThunderboltOutlined,
  WarningOutlined,
} from '@ant-design/icons';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import type { Dataset } from '../../api/types';
import { client, endpoints, errorMessage } from '../../api/client';
import {
  certifySemanticField,
  certifySemanticModel,
  certifySemanticRelationship,
  coldStartSemanticModel,
  deleteSemanticModel,
  generateSemanticModel,
  getSemanticModel,
  listSemanticModels,
  publishSemanticModel,
  scanSemanticGaps,
  semanticExportUrl,
  type SemanticFieldRow,
  type SemanticGapReport,
  type SemanticModelRow,
  type SemanticModelView,
  type SemanticRelationshipRow,
} from '../../api/semantic';
import { SkeletonChart, SkeletonTable } from '../../components/common/Skeleton';
import { formatDateTimeShort } from '../../utils/format';

/** 语义层管理页：Auto Ontology Discover → Certify → Learn 三阶段的前端落点。 */
export default function SemanticManager() {
  const { can } = usePermission();
  const { t } = useI18n();
  const [datasets, setDatasets] = useState<Dataset[]>([]);
  const [selectedDatasetID, setSelectedDatasetID] = useState<number | null>(null);
  const [models, setModels] = useState<SemanticModelRow[]>([]);
  const [selectedModelID, setSelectedModelID] = useState<number | null>(null);
  const [detail, setDetail] = useState<SemanticModelView | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [gaps, setGaps] = useState<SemanticGapReport | null>(null);
  const [gapsLoading, setGapsLoading] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [autoCertifyRunning, setAutoCertifyRunning] = useState(false);
  const [activeTab, setActiveTab] = useState<string>('models');

  // --- 1. 加载 dataset 列表 -------------------------------------------------
  useEffect(() => {
    (async () => {
      try {
        const resp = await client.get<Dataset[]>(endpoints.datasets, { page_size: 200 });
        const list = 'items' in resp ? (resp as unknown as { items: Dataset[] }).items : (resp as unknown as Dataset[]);
        setDatasets(list ?? []);
      } catch {
        // 静默失败 — dataset 列表空会让整个页面显示 Empty，这是正确行为
      }
    })();
  }, []);

  // --- 2. 选中 dataset 后加载 semantic models ------------------------------
  const reloadModels = useCallback(async (datasetID: number) => {
    try {
      const list = await listSemanticModels(datasetID);
      setModels(list);
      return list;
    } catch (err) {
      message.error(`加载语义模型失败：${errorMessage(err)}`);
      setModels([]);
      return [];
    }
  }, []);

  const handleSelectDataset = async (id: number | null) => {
    setSelectedDatasetID(id);
    setSelectedModelID(null);
    setDetail(null);
    setGaps(null);
    if (id != null) {
      await reloadModels(id);
    } else {
      setModels([]);
    }
  };

  // --- 3. 选中 model 后加载详情 ----------------------------------------------
  const reloadDetail = useCallback(async (modelID: number) => {
    setDetailLoading(true);
    try {
      const view = await getSemanticModel(modelID);
      setDetail(view);
    } catch (err) {
      message.error(`加载模型详情失败：${errorMessage(err)}`);
    } finally {
      setDetailLoading(false);
    }
  }, []);

  const handleSelectModel = async (id: number | null) => {
    setSelectedModelID(id);
    if (id != null) {
      await reloadDetail(id);
    } else {
      setDetail(null);
    }
  };

  // --- 4. 自动生成（ontogen.go Discover 阶段前端入口） ----------------------
  const handleGenerate = async () => {
    if (selectedDatasetID == null) {
      message.warning('请先选择数据集');
      return;
    }
    setGenerating(true);
    try {
      const view = await generateSemanticModel(selectedDatasetID);
      message.success(`已生成 ${view.fields.length} 个字段 + ${view.relationships.length} 个关系（全部为草稿）`);
      // 重新加载 models 列表并选中刚生成的
      const list = await reloadModels(selectedDatasetID);
      if (list.length > 0) {
        await handleSelectModel(list[list.length - 1].id);
      }
      setActiveTab('detail');
    } catch (err) {
      message.error(`自动生成失败：${errorMessage(err)}`);
    } finally {
      setGenerating(false);
    }
  };

  // --- 5. 一键冷启动（cold-start 是老接口，留着兜底） ------------------------
  const handleColdStart = async () => {
    if (selectedDatasetID == null) {
      message.warning('请先选择数据集');
      return;
    }
    setGenerating(true);
    try {
      await coldStartSemanticModel(selectedDatasetID, true);
      message.success('冷启动完成');
      const list = await reloadModels(selectedDatasetID);
      if (list.length > 0) {
        await handleSelectModel(list[list.length - 1].id);
      }
    } catch (err) {
      message.error(`冷启动失败：${errorMessage(err)}`);
    } finally {
      setGenerating(false);
    }
  };

  // --- 6. 认证 / 撤销（Certification 状态机） --------------------------------
  const withConfirm = (action: () => Promise<void>, confirmText?: string) => {
    const run = async () => {
      try {
        await action();
        message.success('操作成功');
        if (selectedDatasetID != null) await reloadModels(selectedDatasetID);
        if (selectedModelID != null) await reloadDetail(selectedModelID);
      } catch (err) {
        message.error(errorMessage(err));
      }
    };
    if (confirmText) {
      Modal.confirm({
        title: confirmText,
        okText: '确认',
        cancelText: '取消',
        onOk: run,
      });
    } else {
      run();
    }
  };

  const handleCertifyModel = (certified: boolean) => {
    if (selectedModelID == null) return;
    withConfirm(
      async () => { await certifySemanticModel(selectedModelID, certified); },
      certified
        ? '确认认证？认证后模型及其所有字段/关系将立即被 Agent 信任。'
        : '确认撤销？撤销后 Agent 将标注"口径未认证"。',
    );
  };

  const handleCertifyField = (fieldID: number, certified: boolean) => {
    withConfirm(async () => { await certifySemanticField(fieldID, certified); });
  };

  const handleCertifyRel = (relID: number, certified: boolean) => {
    withConfirm(async () => { await certifySemanticRelationship(relID, certified); });
  };

  const handlePublish = () => {
    if (selectedModelID == null) return;
    withConfirm(
      async () => { await publishSemanticModel(selectedModelID); },
      '确认发布？发布后数据集将使用此模型作为结构化查询的主路径。',
    );
  };

  const handleDelete = () => {
    if (selectedModelID == null) return;
    withConfirm(
      async () => {
        await deleteSemanticModel(selectedModelID);
        setSelectedModelID(null);
        setDetail(null);
        if (selectedDatasetID != null) await reloadModels(selectedDatasetID);
      },
      '确认删除？删除后 Agent 将失去此模型。',
    );
  };

  // --- 7. 缺口扫描 ----------------------------------------------------------
  const handleScanGaps = async (sample = false) => {
    if (selectedDatasetID == null) return;
    setGapsLoading(true);
    try {
      const report = await scanSemanticGaps(selectedDatasetID, sample);
      setGaps(report);
      setActiveTab('gaps');
      message.success(`扫描完成：覆盖度 ${(report.score * 100).toFixed(0)}%，${report.gaps.length} 个缺口`);
    } catch (err) {
      message.error(`扫描失败：${errorMessage(err)}`);
    } finally {
      setGapsLoading(false);
    }
  };

  // --- 8. 自动认证（管理员一键操作，会级联所有字段） --------------------------
  const handleAutoCertifyAllDrafts = () => {
    if (selectedModelID == null || !detail) return;
    setAutoCertifyRunning(true);
    // 先发布（如果还没发布），再认证
    withConfirm(
      async () => {
        if (detail.model.status !== 'published') {
          await publishSemanticModel(selectedModelID!);
        }
        await certifySemanticModel(selectedModelID!, true);
      },
      '一键认证会将此模型及其所有字段/关系全部签名。确认？',
    );
    setTimeout(() => setAutoCertifyRunning(false), 500);
  };

  // --- 派生统计 --------------------------------------------------------------
  const stats = useMemo(() => {
    if (!detail) return null;
    const fields = detail.fields ?? [];
    const certFields = fields.filter(f => f.certified_at).length;
    const metrics = fields.filter(f => f.kind === 'metric');
    const certMetrics = metrics.filter(f => f.certified_at).length;
    const rels = detail.relationships ?? [];
    const certRels = rels.filter(r => r.certified_at).length;
    const drafts = fields.filter(f => !f.certified_at).length;
    const total = fields.length + rels.length;
    return { certFields, certMetrics, certRels, drafts, total, metricsCount: metrics.length };
  }, [detail]);

  // --- 模型列表 Tab 列 ------------------------------------------------------
  const modelColumns: ColumnsType<SemanticModelRow> = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    { title: '名称', dataIndex: 'name', ellipsis: true, render: (v, r) => (
      <a onClick={() => handleSelectModel(r.id)} style={{ fontWeight: r.id === selectedModelID ? 600 : 400 }}>
        {v}
      </a>
    ) },
    { title: '来源', dataIndex: 'source', width: 110, render: v => sourceTag(v) },
    { title: '状态', dataIndex: 'status', width: 110, render: v => statusTag(v) },
    { title: '已认证', dataIndex: 'certified_at', width: 160, render: v => v ? (
      <Tag color="green"><CheckCircleOutlined /> {formatDateTimeShort(v)}</Tag>
    ) : <Tag color="default">草稿</Tag> },
    { title: '版本', dataIndex: 'version', width: 80 },
    {
      title: '操作', key: 'actions', width: 240, render: (_, r) => (
        <Space size="small">
          <Button size="small" type="link" onClick={() => handleSelectModel(r.id)}>详情</Button>
          {r.status !== 'published' && (
            <Button size="small" type="link" onClick={() => {
              setSelectedModelID(r.id); handlePublish();
            }}>发布</Button>
          )}
          <Button size="small" type="link" danger onClick={() => {
            setSelectedModelID(r.id); handleDelete();
          }}>删除</Button>
        </Space>
      ),
    },
  ];

  // --- 字段 Tab 列 ----------------------------------------------------------
  const fieldColumns: ColumnsType<SemanticFieldRow> = [
    { title: '名称', dataIndex: 'name', width: 200, ellipsis: true },
    { title: '类型', dataIndex: 'kind', width: 100, render: v => kindTag(v) },
    { title: '表达式', dataIndex: 'expression', ellipsis: true, render: v => v ? <code>{v}</code> : <span style={{ color: '#999' }}>—</span> },
    { title: '描述', dataIndex: 'description', ellipsis: true },
    { title: '单位', dataIndex: 'unit', width: 80, render: v => v || '—' },
    { title: '认证', dataIndex: 'certified_at', width: 140, render: (v, r) => v ? (
      <Tag color="green"><CheckCircleOutlined /></Tag>
    ) : <Tag color="orange"><WarningOutlined /> 草稿</Tag> },
    {
      title: '操作', key: 'actions', width: 160, render: (_, r) => (
        r.certified_at ? (
          <Button size="small" type="link" onClick={() => handleCertifyField(r.id, false)}>撤销</Button>
        ) : (
          <Button size="small" type="link" onClick={() => handleCertifyField(r.id, true)}>认证</Button>
        )
      ),
    },
  ];

  // --- 关系 Tab 列 ----------------------------------------------------------
  const relColumns: ColumnsType<SemanticRelationshipRow> = [
    { title: '名称', dataIndex: 'name', width: 200, ellipsis: true },
    { title: 'Join 类型', dataIndex: 'join_type', width: 120 },
    { title: '条件', dataIndex: 'condition', ellipsis: true, render: v => <code style={{ fontSize: 12 }}>{v}</code> },
    { title: '认证', dataIndex: 'certified_at', width: 140, render: v => v ? (
      <Tag color="green"><CheckCircleOutlined /></Tag>
    ) : <Tag color="orange"><WarningOutlined /> 草稿</Tag> },
    {
      title: '操作', key: 'actions', width: 160, render: (_, r) => (
        r.certified_at ? (
          <Button size="small" type="link" onClick={() => handleCertifyRel(r.id, false)}>撤销</Button>
        ) : (
          <Button size="small" type="link" onClick={() => handleCertifyRel(r.id, true)}>认证</Button>
        )
      ),
    },
  ];

  // --- 主视图渲染 ----------------------------------------------------------
  return (
    <div style={{ padding: 24 }}>
      <Card
        title={
          <Space>
            <ApiOutlined />
            <span>语义层管理</span>
            <Tag color="geekblue">Auto Ontology</Tag>
          </Space>
        }
        extra={
          <Space>
            <Select
              style={{ width: 260 }}
              placeholder="选择数据集..."
              allowClear
              value={selectedDatasetID ?? undefined}
              onChange={handleSelectDataset}
              options={datasets.map(d => ({
                label: d.table_name,
                value: d.id,
              }))}
            />
            <Button
              icon={<RobotOutlined />}
              type="primary"
              loading={generating}
              disabled={!selectedDatasetID || !can('semantic-layer', 'write')}
              onClick={handleGenerate}
            >
              自动生成 Draft 模型
            </Button>
            <Button icon={<ReloadOutlined />} loading={generating} disabled={!selectedDatasetID} onClick={handleColdStart}>
              冷启动
            </Button>
          </Space>
        }
      >
        {!selectedDatasetID ? (
          <Empty
            description={
              <span>
                请先选择一个数据集。语义模型是数据集的「结构化视图」，由 Agent 或操作员维护。
                <br />
                一键「自动生成 Draft 模型」会根据数据集的列 profile 推断 key/dimension/time/measure 角色，
                <br />
                并自动生成常用 metric 初稿（SUM/AVG/COUNT）。全部为草稿，操作员审核后认证即可被 Agent 信任。
              </span>
            }
          />
        ) : (
          <>
            {stats && detail && (
              <Row gutter={16} style={{ marginBottom: 16 }}>
                <Col span={6}>
                  <Card size="small" loading={detailLoading}>
                    <Statistic
                      title="字段总数"
                      value={detail.fields?.length ?? 0}
                      suffix={detail.model.certified_at ? <Tag color="green">✅ 已认证</Tag> : <Tag color="default">草稿</Tag>}
                    />
                  </Card>
                </Col>
                <Col span={6}>
                  <Card size="small" loading={detailLoading}>
                    <Statistic title="已认证指标" value={stats.certMetrics} suffix={`/ ${stats.metricsCount}`} />
                  </Card>
                </Col>
                <Col span={6}>
                  <Card size="small" loading={detailLoading}>
                    <Statistic title="未认证字段" value={stats.drafts} suffix={stats.drafts > 0 ? <WarningOutlined style={{ color: '#fa8c16' }} /> : null} />
                  </Card>
                </Col>
                <Col span={6}>
                  <Card size="small" loading={detailLoading}>
                    <Statistic title="Join 关系" value={detail.relationships?.length ?? 0} suffix={`已认证 ${stats.certRels}`} />
                  </Card>
                </Col>
              </Row>
            )}

            <Tabs
              activeKey={activeTab}
              onChange={setActiveTab}
              items={[
                {
                  key: 'models',
                  label: <Space><CodeOutlined /> 模型列表 ({models.length})</Space>,
                  children: (
                    models.length === 0 ? (
                      <Empty description="此数据集暂无语义模型。点击右上「自动生成 Draft 模型」一键创建。" />
                    ) : (
                      <Table
                        rowKey="id"
                        size="middle"
                        loading={detailLoading}
                        columns={modelColumns}
                        dataSource={models}
                        pagination={false}
                        rowClassName={r => r.id === selectedModelID ? 'zg-row-selected' : ''}
                        onRow={r => ({ onClick: () => handleSelectModel(r.id), style: { cursor: 'pointer' } })}
                      />
                    )
                  ),
                },
                {
                  key: 'detail',
                  label: <Space><FileProtectOutlined /> 模型详情</Space>,
                  disabled: selectedModelID == null,
                  children: (
                    !detail ? (
                      <SkeletonTable rows={6} />
                    ) : (
                      <div>
                        <Card size="small" title={
                          <Space>
                            <span>{detail.model.name}</span>
                            {detail.model.certified_at ? (
                              <Tag color="green"><CheckCircleOutlined /> 已认证 {formatDateTimeShort(detail.model.certified_at)}</Tag>
                            ) : (
                              <Tag color="orange"><WarningOutlined /> 草稿</Tag>
                            )}
                            {detail.model.status === 'published' ? (
                              <Tag color="geekblue">已发布</Tag>
                            ) : (
                              <Tag color="default">未发布</Tag>
                            )}
                          </Space>
                        } extra={
                          <Space>
                            {can('semantic-layer', 'write') && (
                              <>
                                {detail.model.status !== 'published' && (
                                  <Button size="small" type="primary" onClick={handlePublish}>发布模型</Button>
                                )}
                                <Button
                                  size="small"
                                  icon={detail.model.certified_at ? <ExclamationCircleOutlined /> : <CheckCircleOutlined />}
                                  onClick={() => handleCertifyModel(!detail.model.certified_at)}
                                >
                                  {detail.model.certified_at ? '撤销认证' : '一键认证全模型'}
                                </Button>
                              </>
                            )}
                            <Button size="small" icon={<DownloadOutlined />} onClick={() => {
                              const url = semanticExportUrl(selectedDatasetID!, 'json');
                              window.open(url, '_blank');
                            }}>
                              导出 JSON
                            </Button>
                            <Button size="small" icon={<DownloadOutlined />} onClick={() => {
                              const url = semanticExportUrl(selectedDatasetID!, 'osi');
                              window.open(url, '_blank');
                            }}>
                              导出 OSI
                            </Button>
                          </Space>
                        }>
                          {detail.model.description && (
                            <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
                              {detail.model.description}
                            </Typography.Paragraph>
                          )}
                        </Card>

                        <Divider orientation="left">字段 ({detail.fields?.length ?? 0})</Divider>
                        <Table rowKey="id" size="small" columns={fieldColumns} dataSource={detail.fields ?? []} pagination={false} />

                        {detail.relationships && detail.relationships.length > 0 && (
                          <>
                            <Divider orientation="left">Join 关系 ({detail.relationships.length})</Divider>
                            <Table rowKey="id" size="small" columns={relColumns} dataSource={detail.relationships} pagination={false} />
                          </>
                        )}

                        {detail.issues && detail.issues.length > 0 && (
                          <>
                            <Divider orientation="left"><WarningOutlined /> 问题 ({detail.issues.length})</Divider>
                            {detail.issues.map((issue, idx) => (
                              <Alert
                                key={idx}
                                showIcon
                                type={issue.severity === 'error' ? 'error' : 'warning'}
                                message={issue.message}
                                description={issue.hint && <span style={{ color: '#666' }}>{issue.hint}</span>}
                                style={{ marginBottom: 8 }}
                              />
                            ))}
                          </>
                        )}
                      </div>
                    )
                  ),
                },
                {
                  key: 'gaps',
                  label: <Space><ThunderboltOutlined /> 缺口扫描</Space>,
                  disabled: selectedDatasetID == null,
                  children: (
                    <div>
                      <Space style={{ marginBottom: 12 }}>
                        <Button icon={<ThunderboltOutlined />} loading={gapsLoading} onClick={() => handleScanGaps(false)}>快速扫描</Button>
                        <Button icon={<CloudUploadOutlined />} loading={gapsLoading} onClick={() => handleScanGaps(true)}>深度扫描（采样数据）</Button>
                      </Space>
                      {gaps && (
                        <Row gutter={16} style={{ marginBottom: 16 }}>
                          <Col span={8}>
                            <Card size="small">
                              <Statistic title="覆盖度" value={Math.round(gaps.score * 100)} suffix="%" />
                            </Card>
                          </Col>
                          <Col span={8}>
                            <Card size="small">
                              <Statistic title="高严重度缺口" value={gaps.gaps.filter(g => g.severity === 'high').length} valueStyle={{ color: '#cf1322' }} />
                            </Card>
                          </Col>
                          <Col span={8}>
                            <Card size="small">
                              <Statistic title="总缺口" value={gaps.gaps.length} />
                            </Card>
                          </Col>
                        </Row>
                      )}
                      {gaps && gaps.gaps.length > 0 && (
                        <Table
                          rowKey="id"
                          size="small"
                          pagination={false}
                          columns={[
                            { title: '严重度', dataIndex: 'severity', width: 100, render: v => (
                              <Tag color={v === 'high' ? 'red' : v === 'medium' ? 'orange' : 'blue'}>{v}</Tag>
                            ) },
                            { title: '目标', dataIndex: 'target', width: 200 },
                            { title: '问题', dataIndex: 'question', ellipsis: true },
                            { title: '自动修复建议', dataIndex: 'draft', ellipsis: true, render: v => <code style={{ fontSize: 12 }}>{v}</code> },
                            { title: '置信度', dataIndex: 'confidence', width: 100 },
                          ]}
                          dataSource={gaps.gaps}
                        />
                      )}
                    </div>
                  ),
                },
              ]}
            />
          </>
        )}
      </Card>

      <Divider />
      <Card size="small" title="🔄 Auto Ontology 闭环">
        <Row gutter={[16, 16]}>
          <Col span={8}>
            <Alert
              showIcon
              icon={<RobotOutlined />}
              type="info"
              message="① Discover（自动生成）"
              description={
                <span>
                  ontogen.go 从 dataset 的 columns profile 推断 key/dimension/time/measure 角色，
                  自动生成 draft 模型。5 分钟出初稿。
                </span>
              }
            />
          </Col>
          <Col span={8}>
            <Alert
              showIcon
              icon={<FileProtectOutlined />}
              type="success"
              message="② Certify（人工认证）"
              description={
                <span>
                  操作员审核后点「认证」。CertifiedAt 时间戳写入，Agent prompt 自动区分
                  「✅ 已认证」vs「草稿」，回答更可信。
                </span>
              }
            />
          </Col>
          <Col span={8}>
            <Alert
              showIcon
              icon={<CheckCircleOutlined />}
              type="warning"
              message="③ Learn（自动反哺）"
              description={
                <span>
                  beat cron 每 12h 扫 Agent SQL，相同表达式出现 ≥ 3 次自动生成 draft metric。
                  口径越用越准。
                </span>
              }
            />
          </Col>
        </Row>
      </Card>
    </div>
  );
}

// --- helpers ---------------------------------------------------------------

function sourceTag(source?: string) {
  switch (source) {
    case 'generated':
      return <Tag color="blue"><RobotOutlined /> AI 生成</Tag>;
    case 'agent_adopted':
      return <Tag color="purple"><ApiOutlined /> Agent 采纳</Tag>;
    default:
      return <Tag>手工</Tag>;
  }
}

function statusTag(status: string) {
  switch (status) {
    case 'published':
      return <Tag color="geekblue">已发布</Tag>;
    case 'draft':
      return <Tag color="default">草稿</Tag>;
    case 'reviewed':
      return <Tag color="orange">审核中</Tag>;
    default:
      return <Tag>{status}</Tag>;
  }
}

function kindTag(kind: string) {
  switch (kind) {
    case 'metric':
      return <Tag color="red">指标</Tag>;
    case 'dimension':
      return <Tag color="green">维度</Tag>;
    case 'time':
      return <Tag color="cyan">时间</Tag>;
    case 'column':
      return <Tag color="default">物理列</Tag>;
    default:
      return <Tag>{kind}</Tag>;
  }
}
