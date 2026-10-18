/**
 * Wall gallery: my screens plus the built-in industry template market.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Button,
  Card,
  Dropdown,
  Empty,
  Input,
  Modal,
  Segmented,
  Skeleton,
  Tabs,
  Tag,
  Tooltip,
  Upload,
  message,
} from 'antd';
import {
  AppstoreAddOutlined,
  CopyOutlined,
  DeleteOutlined,
  DownloadOutlined,
  EditOutlined,
  EyeOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
  ShareAltOutlined,
  UploadOutlined,
} from '@ant-design/icons';
import type { ScreenSummary, ScreenTemplate } from '../../api/screens';
import {
  createScreen,
  createScreenFromTemplate,
  deleteScreen,
  duplicateScreen,
  importScreen,
  listScreenTemplates,
  listScreens,
  screenExportUrl,
} from '../../api/screens';
import { errorMessage } from '../../api/client';
import PageHeader from '../../components/common/PageHeader';
import ScreenSurface from '../../components/screen/ScreenSurface';
import ScreenShareModal from '../../components/screen/ScreenShareModal';
import { useI18n } from '../../i18n';

const CATEGORY_KEYS = ['all', 'gov', 'park', 'energy', 'manufacture'] as const;

export default function ScreenList() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [screens, setScreens] = useState<ScreenSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [templates, setTemplates] = useState<ScreenTemplate[]>([]);
  const [templatesLoading, setTemplatesLoading] = useState(true);
  const [category, setCategory] = useState<string>('all');
  const [search, setSearch] = useState('');
  const [shareTarget, setShareTarget] = useState<ScreenSummary | null>(null);
  const [preview, setPreview] = useState<ScreenTemplate | null>(null);
  const [creating, setCreating] = useState(false);
  const [nameModal, setNameModal] = useState(false);
  const [draftName, setDraftName] = useState('');
  const fileRef = useRef<HTMLInputElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await listScreens({ pageSize: 60, q: search || undefined });
      setScreens(response.result);
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [search]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setTemplatesLoading(true);
    listScreenTemplates()
      .then(response => setTemplates(response.result))
      .catch(err => message.error(errorMessage(err)))
      .finally(() => setTemplatesLoading(false));
  }, []);

  const filteredTemplates = useMemo(
    () => (category === 'all' ? templates : templates.filter(tpl => tpl.category === category)),
    [templates, category],
  );

  const createBlank = async () => {
    if (!draftName.trim()) return;
    setCreating(true);
    try {
      const screen = await createScreen({ name: draftName.trim(), category: 'general' });
      setNameModal(false);
      setDraftName('');
      navigate(`/screen/${screen.id}/edit`);
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setCreating(false);
    }
  };

  const useTemplate = async (tpl: ScreenTemplate) => {
    setCreating(true);
    try {
      const screen = await createScreenFromTemplate(tpl.key, { name: tpl.name });
      setPreview(null);
      navigate(`/screen/${screen.id}/edit`);
    } catch (err) {
      message.error(errorMessage(err));
    } finally {
      setCreating(false);
    }
  };

  const remove = (screen: ScreenSummary) => {
    Modal.confirm({
      title: t('screen.list.delete_title'),
      content: screen.name,
      okType: 'danger',
      onOk: async () => {
        try {
          await deleteScreen(screen.id);
          message.success(t('screen.list.deleted'));
          void load();
        } catch (err) {
          message.error(errorMessage(err));
        }
      },
    });
  };

  const duplicate = async (screen: ScreenSummary) => {
    try {
      const copy = await duplicateScreen(screen.id, `${screen.name} 副本`);
      message.success(t('screen.list.duplicated'));
      navigate(`/screen/${copy.id}/edit`);
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  const onImport = async (file: File) => {
    try {
      const text = await file.text();
      const bundle: unknown = JSON.parse(text);
      const screen = await importScreen(bundle);
      message.success(t('screen.list.imported'));
      navigate(`/screen/${screen.id}/edit`);
    } catch (err) {
      message.error(errorMessage(err));
    }
    return false;
  };

  const categoryLabel = (key: string) =>
    ({
      all: t('screen.category.all'),
      gov: t('screen.category.gov'),
      park: t('screen.category.park'),
      energy: t('screen.category.energy'),
      manufacture: t('screen.category.manufacture'),
      general: t('screen.category.general'),
    })[key] ?? key;

  return (
    <div className="scr-page">
      <PageHeader
        title={t('screen.list.title')}
        subtitle={t('screen.list.subtitle')}
        extra={
          <div className="scr-page__actions">
            <Input.Search
              allowClear
              placeholder={t('screen.list.search')}
              onSearch={value => setSearch(value)}
              style={{ width: 220 }}
            />
            <Button icon={<ReloadOutlined />} onClick={() => void load()}>
              {t('common.actions.refresh')}
            </Button>
            <Button icon={<UploadOutlined />} onClick={() => fileRef.current?.click()}>
              {t('screen.list.import')}
            </Button>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => {
                setDraftName(t('screen.list.new_name'));
                setNameModal(true);
              }}
            >
              {t('screen.list.new')}
            </Button>
          </div>
        }
      />

      <input
        ref={fileRef}
        type="file"
        accept="application/json,.json"
        hidden
        onChange={event => {
          const file = event.target.files?.[0];
          if (file) void onImport(file);
          event.target.value = '';
        }}
      />

      <Tabs
        items={[
          {
            key: 'mine',
            label: `${t('screen.list.mine')} (${screens.length})`,
            children: loading ? (
              <div className="scr-gallery">
                {Array.from({ length: 6 }, (_, index) => (
                  <Card key={index} className="scr-card">
                    <Skeleton active paragraph={{ rows: 3 }} />
                  </Card>
                ))}
              </div>
            ) : screens.length ? (
              <div className="scr-gallery">
                {screens.map(screen => (
                  <Card
                    key={screen.id}
                    className="scr-card"
                    hoverable
                    onClick={() => navigate(`/screen/${screen.id}/view`)}
                    cover={
                      <div
                        className="scr-card__cover"
                        style={{ background: screen.background || undefined }}
                      >
                        <div className="scr-card__grid" />
                        <div className="scr-card__title">{screen.name}</div>
                        <div className="scr-card__meta">
                          <span>{screen.width}×{screen.height}</span>
                          <span>{screen.config_rev} rev</span>
                        </div>
                        {screen.public_enabled && (
                          <Tag color="processing" className="scr-card__badge">
                            {t('screen.list.public')}
                          </Tag>
                        )}
                      </div>
                    }
                    actions={[
                      <Tooltip title={t('screen.list.edit')} key="edit">
                        <EditOutlined
                          onClick={event => {
                            event.stopPropagation();
                            navigate(`/screen/${screen.id}/edit`);
                          }}
                        />
                      </Tooltip>,
                      <Tooltip title={t('screen.list.view')} key="view">
                        <EyeOutlined
                          onClick={event => {
                            event.stopPropagation();
                            navigate(`/screen/${screen.id}/view`);
                          }}
                        />
                      </Tooltip>,
                      <Tooltip title={t('screen.share.title')} key="share">
                        <ShareAltOutlined
                          onClick={event => {
                            event.stopPropagation();
                            setShareTarget(screen);
                          }}
                        />
                      </Tooltip>,
                      <Dropdown
                        key="more"
                        trigger={['click']}
                        menu={{
                          items: [
                            {
                              key: 'duplicate',
                              icon: <CopyOutlined />,
                              label: t('screen.list.duplicate'),
                              onClick: () => void duplicate(screen),
                            },
                            {
                              key: 'export',
                              icon: <DownloadOutlined />,
                              label: t('screen.list.export'),
                              onClick: () => {
                                window.location.href = screenExportUrl(screen.id);
                              },
                            },
                            {
                              key: 'delete',
                              danger: true,
                              icon: <DeleteOutlined />,
                              label: t('common.actions.delete'),
                              onClick: () => remove(screen),
                            },
                          ],
                        }}
                      >
                        <MoreOutlined onClick={event => event.stopPropagation()} />
                      </Dropdown>,
                    ]}
                  >
                    <Card.Meta
                      title={screen.name}
                      description={
                        <span className="scr-card__desc">
                          {categoryLabel(screen.category)} · {screen.view_count} {t('screen.list.views')}
                        </span>
                      }
                    />
                  </Card>
                ))}
              </div>
            ) : (
              <Empty description={t('screen.list.empty')}>
                <Button type="primary" icon={<PlusOutlined />} onClick={() => setNameModal(true)}>
                  {t('screen.list.new')}
                </Button>
              </Empty>
            ),
          },
          {
            key: 'templates',
            label: `${t('screen.list.templates')} (${templates.length})`,
            children: (
              <div className="scr-templates">
                <div className="scr-templates__bar">
                  <Segmented
                    value={category}
                    onChange={value => setCategory(String(value))}
                    options={CATEGORY_KEYS.map(key => ({ value: key, label: categoryLabel(key) }))}
                  />
                  <span className="scr-templates__hint">{t('screen.list.template_hint')}</span>
                </div>
                {templatesLoading ? (
                  <div className="scr-gallery">
                    {Array.from({ length: 8 }, (_, index) => (
                      <Card key={index} className="scr-card">
                        <Skeleton active />
                      </Card>
                    ))}
                  </div>
                ) : (
                  <div className="scr-gallery scr-gallery--wide">
                    {filteredTemplates.map(tpl => (
                      <Card
                        key={tpl.key}
                        className="scr-card scr-card--template"
                        hoverable
                        onClick={() => setPreview(tpl)}
                        cover={
                          <div className="scr-card__preview">
                            <ScreenSurface config={tpl.config} className="scr-surface--thumb" />
                            <div className="scr-card__overlay">
                              <Button type="primary" icon={<AppstoreAddOutlined />}>
                                {t('screen.list.use_template')}
                              </Button>
                            </div>
                          </div>
                        }
                      >
                        <Card.Meta
                          title={tpl.name}
                          description={<span className="scr-card__desc">{tpl.description}</span>}
                        />
                        <div className="scr-card__tags">
                          <Tag color="blue">{tpl.category_cn}</Tag>
                          {tpl.tags.slice(0, 3).map(tag => (
                            <Tag key={tag}>{tag}</Tag>
                          ))}
                        </div>
                      </Card>
                    ))}
                  </div>
                )}
              </div>
            ),
          },
        ]}
      />

      <Modal
        open={nameModal}
        title={t('screen.list.new')}
        okText={t('common.actions.create')}
        confirmLoading={creating}
        onOk={() => void createBlank()}
        onCancel={() => setNameModal(false)}
      >
        <Input
          autoFocus
          value={draftName}
          placeholder={t('screen.list.name_placeholder')}
          onChange={event => setDraftName(event.target.value)}
          onPressEnter={() => void createBlank()}
        />
      </Modal>

      <Modal
        open={Boolean(preview)}
        title={preview?.name}
        width={1080}
        onCancel={() => setPreview(null)}
        okText={t('screen.list.use_template')}
        okButtonProps={{ loading: creating }}
        onOk={() => preview && void useTemplate(preview)}
      >
        {preview && (
          <>
            <div className="scr-preview-stage">
              <ScreenSurface config={preview.config} />
            </div>
            <p className="scr-preview-desc">{preview.description}</p>
          </>
        )}
      </Modal>

      <ScreenShareModal
        open={Boolean(shareTarget)}
        screen={shareTarget}
        onClose={() => setShareTarget(null)}
        onChanged={() => void load()}
      />
    </div>
  );
}
