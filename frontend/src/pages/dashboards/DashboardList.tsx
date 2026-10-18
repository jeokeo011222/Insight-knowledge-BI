import { useCallback, useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Dropdown,
  Form,
  Input,
  Modal,
  Pagination,
  Popconfirm,
  Segmented,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  AppstoreOutlined,
  BarsOutlined,
  DashboardOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EllipsisOutlined,
  PlusOutlined,
  SearchOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import { client, downloadBlob, endpoints, errorMessage } from '../../api/client';
import type { Dashboard, DashboardPayload, Paginated } from '../../api/types';
import PageHeader from '../../components/common/PageHeader';
import FavoriteStar from '../../components/common/FavoriteStar';
import EmptyState from '../../components/common/EmptyState';
import Loading from '../../components/common/Loading';
import { usePermission } from '../../store';
import { useI18n } from '../../i18n';
import { formatDateTimeShort } from '../../utils/format';

const PAGE_SIZE = 24;

export default function DashboardList() {
  const navigate = useNavigate();
  const { can } = usePermission();
  const { t } = useI18n();
  const [rows, setRows] = useState<Dashboard[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [searchInput, setSearchInput] = useState('');
  const [search, setSearch] = useState('');
  const [loading, setLoading] = useState(false);
  const [reloadToken, setReloadToken] = useState(0);
  const [view, setView] = useState<'card' | 'list'>('card');
  const [createOpen, setCreateOpen] = useState(false);
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const importInputRef = useRef<HTMLInputElement | null>(null);
  const [form] = Form.useForm<DashboardPayload>();

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 320);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    client
      .get<Paginated<Dashboard>>(endpoints.dashboards, {
        page,
        page_size: PAGE_SIZE,
        q: search,
        order_column: 'changed_on',
        order_direction: 'desc',
      })
      .then(response => {
        if (cancelled) return;
        setRows(response.result ?? []);
        setTotal(response.count ?? 0);
      })
      .catch(error => {
        if (cancelled) return;
        setRows([]);
        setTotal(0);
        message.error(errorMessage(error));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [page, search, reloadToken]);

  const reload = useCallback(() => setReloadToken(value => value + 1), []);

  const handleDelete = async (dashboard: Dashboard) => {
    try {
      await client.delete(endpoints.dashboard(dashboard.id));
      message.success(t('dashboard.list.delete_success', { name: dashboard.dashboard_title }));
      reload();
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      setCreating(true);
      const created = await client.post<{ result: Dashboard }>(endpoints.dashboards, {
        dashboard_title: values.dashboard_title,
        slug: values.slug,
        description: values.description,
        published: values.published ?? false,
        position_json: {},
        json_metadata: {},
      });
      message.success(t('dashboard.list.created'));
      setCreateOpen(false);
      form.resetFields();
      reload();
      if (created.result?.id) navigate(`/dashboard/${created.result.id}/`);
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) return;
      message.error(errorMessage(error));
    } finally {
      setCreating(false);
    }
  };

  // Export writes the same JSON bundle the import accepts, so a board becomes a
  // portable template: charts, datasets and datasource references travel with
  // it and are re-created (or matched by name) on the target instance.
  const handleExport = async (dashboard: Dashboard) => {
    try {
      const blob = await client.getBlob(endpoints.exportAsset('dashboard', dashboard.id));
      await downloadBlob(blob, `${dashboard.slug || `dashboard_${dashboard.id}`}.json`);
      message.success(t('dashboard.list.export_done', { name: dashboard.dashboard_title }));
    } catch (error) {
      message.error(errorMessage(error));
    }
  };

  const handleImportFile = async (file: File) => {
    setImporting(true);
    try {
      const text = await file.text();
      const contents = JSON.parse(text) as unknown;
      const response = await client.post<{ result?: { dashboard?: { id?: number } } }>(
        endpoints.importExport,
        { type: 'dashboard', contents },
      );
      const created = response.result?.dashboard;
      message.success(t('dashboard.list.import_done'));
      reload();
      if (created?.id) navigate(`/dashboard/${created.id}/`);
    } catch (error) {
      message.error(errorMessage(error));
    } finally {
      setImporting(false);
    }
  };

  const ownerText = (dashboard: Dashboard): string => {
    const owners = dashboard.owners ?? [];
    if (!owners.length) return '—';
    return owners
      .map(owner => owner.name || owner.username || [owner.first_name, owner.last_name].filter(Boolean).join(' '))
      .filter(Boolean)
      .join(', ');
  };

  const columns: ColumnsType<Dashboard> = [
    {
      title: t('common.label.name'),
      dataIndex: 'dashboard_title',
      key: 'dashboard_title',
      render: (_, record) => (
        <a onClick={() => navigate(`/dashboard/${record.id}/`)}>{record.dashboard_title}</a>
      ),
    },
    {
      title: t('common.label.status'),
      dataIndex: 'published',
      key: 'published',
      width: 100,
      render: (published: boolean) => (
        <Tag color={published ? 'green' : 'default'}>
          {published ? t('common.state.published') : t('common.state.draft')}
        </Tag>
      ),
    },
    {
      title: t('common.label.charts'),
      dataIndex: 'chart_count',
      key: 'chart_count',
      width: 90,
      render: (value?: number) => value ?? 0,
    },
    {
      title: t('common.label.owner'),
      key: 'owners',
      render: (_, record) => <span className="cell-muted">{ownerText(record)}</span>,
    },
    {
      title: t('common.label.changed_on'),
      dataIndex: 'changed_on',
      key: 'changed_on',
      width: 180,
      render: (value?: string, record?: Dashboard) => (
        <span className="cell-muted">
          {value ? formatDateTimeShort(value) : record?.changed_on_delta_humanized || '—'}
        </span>
      ),
    },
    {
      title: t('common.label.actions'),
      key: 'actions',
      width: 180,
      render: (_, record) => (
        <Space size={4}>
          <FavoriteStar className="dashboard" objId={record.id} />
          <Button
            size="small"
            type="text"
            icon={<EditOutlined />}
            onClick={() => navigate(`/dashboard/${record.id}/`)}
          />
          {can('can_export', 'ImportExport') ? (
            <Tooltip title={t('dashboard.list.export')}>
              <Button
                size="small"
                type="text"
                icon={<DownloadOutlined />}
                onClick={() => void handleExport(record)}
              />
            </Tooltip>
          ) : null}
          {can('can_delete', 'Dashboard') ? (
            <Popconfirm
              title={t('dashboard.list.delete_title')}
              description={t('dashboard.list.delete_confirm', { name: record.dashboard_title })}
              okText={t('common.actions.delete')}
              cancelText={t('common.actions.cancel')}
              okButtonProps={{ danger: true }}
              onConfirm={() => void handleDelete(record)}
            >
              <Button size="small" type="text" danger icon={<DeleteOutlined />} />
            </Popconfirm>
          ) : null}
        </Space>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={t('nav.dashboards')}
        subtitle={t('dashboard.list.subtitle')}
        extra={
          <Space>
            <Input
              allowClear
              value={searchInput}
              onChange={event => setSearchInput(event.target.value)}
              placeholder={t('dashboard.list.search_placeholder')}
              prefix={<SearchOutlined style={{ color: 'var(--color-text-tertiary)' }} />}
              style={{ width: 240 }}
            />
            <Segmented
              value={view}
              onChange={value => setView(value as 'card' | 'list')}
              options={[
                { value: 'card', icon: <AppstoreOutlined /> },
                { value: 'list', icon: <BarsOutlined /> },
              ]}
            />
            {can('can_import', 'ImportExport') ? (
              <>
                <input
                  ref={importInputRef}
                  type="file"
                  accept="application/json,.json"
                  style={{ display: 'none' }}
                  onChange={event => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (file) void handleImportFile(file);
                  }}
                />
                <Button
                  icon={<UploadOutlined />}
                  loading={importing}
                  onClick={() => importInputRef.current?.click()}
                >
                  {t('dashboard.list.import')}
                </Button>
              </>
            ) : null}
            {can('can_write', 'Dashboard') ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
                {t('dashboard.list.create')}
              </Button>
            ) : null}
          </Space>
        }
      />

      {loading ? <Loading height={200} /> : null}

      {!loading && rows.length === 0 ? (
        <EmptyState
          description={search ? t('dashboard.list.empty_no_match', { name: search }) : t('dashboard.list.empty_none')}
          action={
            can('can_write', 'Dashboard') ? (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
                {t('dashboard.list.create')}
              </Button>
            ) : undefined
          }
        />
      ) : null}

      {!loading && rows.length > 0 && view === 'card' ? (
        <>
          <div className="card-grid">
            {rows.map(dashboard => (
              <div
                key={dashboard.id}
                className="zg-card"
                style={{ cursor: 'pointer', overflow: 'hidden' }}
                onClick={() => navigate(`/dashboard/${dashboard.id}/`)}
              >
                <div className="entity-thumb">
                  <DashboardOutlined />
                </div>
                <div className="entity-card__body">
                  <div style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <span className="entity-card__title" title={dashboard.dashboard_title}>
                        {dashboard.dashboard_title}
                      </span>
                      <div className="cell-muted" style={{ marginTop: 4 }}>
                        {dashboard.changed_on
                          ? formatDateTimeShort(dashboard.changed_on)
                          : dashboard.changed_on_delta_humanized || '—'}
                      </div>
                    </div>
                    <Dropdown
                      trigger={['click']}
                      menu={{
                        items: [
                          { key: 'open', icon: <EditOutlined />, label: t('common.actions.open') },
                          ...(can('can_delete', 'Dashboard')
                            ? [{ key: 'delete', icon: <DeleteOutlined />, label: t('common.actions.delete'), danger: true }]
                            : []),
                        ],
                        onClick: ({ key, domEvent }) => {
                          domEvent.stopPropagation();
                          if (key === 'open') navigate(`/dashboard/${dashboard.id}/`);
                          if (key === 'delete') {
                            Modal.confirm({
                              title: t('dashboard.list.delete_title'),
                              content: t('dashboard.list.delete_confirm', { name: dashboard.dashboard_title }),
                              okText: t('common.actions.delete'),
                              cancelText: t('common.actions.cancel'),
                              okButtonProps: { danger: true },
                              onOk: () => handleDelete(dashboard),
                            });
                          }
                        },
                      }}
                    >
                      <Button
                        size="small"
                        type="text"
                        icon={<EllipsisOutlined />}
                        onClick={event => event.stopPropagation()}
                      />
                    </Dropdown>
                  </div>
                  <div className="entity-card__tags">
                    <Tag color={dashboard.published ? 'green' : 'default'}>
                      {dashboard.published ? t('common.state.published') : t('common.state.draft')}
                    </Tag>
                    <Tag bordered={false}>{dashboard.chart_count ?? 0} charts</Tag>
                  </div>
                  <div className="entity-card__meta">
                    <span title={ownerText(dashboard)} style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {ownerText(dashboard)}
                    </span>
                    {dashboard.slug ? <span className="mono">{dashboard.slug}</span> : null}
                  </div>
                </div>
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 18 }}>
            <Pagination
              current={page}
              pageSize={PAGE_SIZE}
              total={total}
              showSizeChanger={false}
              showTotal={count => t('dashboard.list.total', { count })}
              onChange={setPage}
            />
          </div>
        </>
      ) : null}

      {!loading && rows.length > 0 && view === 'list' ? (
        <div className="list-card">
          <Table<Dashboard>
            rowKey="id"
            size="middle"
            columns={columns}
            dataSource={rows}
            pagination={{
              current: page,
              pageSize: PAGE_SIZE,
              total,
              showSizeChanger: false,
              showTotal: count => t('dashboard.list.total', { count }),
              onChange: setPage,
            }}
          />
        </div>
      ) : null}

      <Modal
        title={t('dashboard.list.create')}
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onOk={() => void handleCreate()}
        okText={t('common.actions.create')}
        cancelText={t('common.actions.cancel')}
        confirmLoading={creating}
        destroyOnClose
      >
        <Form<DashboardPayload> form={form} layout="vertical" preserve={false}>
          <Form.Item
            name="dashboard_title"
            label={t('dashboard.form.title')}
            rules={[{ required: true, message: t('dashboard.form.title_required') }]}
          >
            <Input placeholder={t('dashboard.form.title_placeholder')} maxLength={120} />
          </Form.Item>
          <Form.Item name="slug" label={t('dashboard.form.slug')} tooltip={t('dashboard.form.slug_hint')}>
            <Input placeholder="sales-overview" />
          </Form.Item>
          <Form.Item name="description" label={t('dashboard.form.description')}>
            <Input.TextArea rows={3} placeholder={t('dashboard.form.description_placeholder')} maxLength={400} />
          </Form.Item>
          <Form.Item
            name="published"
            label={t('dashboard.form.published')}
            valuePropName="checked"
            initialValue={false}
          >
            <Switch
              checkedChildren={t('dashboard.form.published_hint')}
              unCheckedChildren={t('dashboard.form.draft_hint')}
            />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}
