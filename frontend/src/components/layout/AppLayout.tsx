import { useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom';
import {
  Alert,
  Avatar,
  Breadcrumb,
  Button,
  Dropdown,
  Layout,
  Menu,
  Modal,
  Space,
  Tag,
  Typography,
  message,
} from 'antd';
import type { BreadcrumbProps, MenuProps } from 'antd';
import {
  AuditOutlined,
  BarChartOutlined,
  BellOutlined,
  BulbFilled,
  BulbOutlined,
  ConsoleSqlOutlined,
  DashboardOutlined,
  DatabaseOutlined,
  DownOutlined,
  FundProjectionScreenOutlined,
  GlobalOutlined,
  HomeOutlined,
  CommentOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  RobotOutlined,
  SettingOutlined,
  TableOutlined,
  TagsOutlined,
  TeamOutlined,
  UserOutlined,
} from '@ant-design/icons';
import { useAppDispatch, useAppSelector, usePermission } from '../../store';
import { logout } from '../../store/userSlice';
import { useI18n } from '../../i18n';
import type { Locale } from '../../i18n';
import { useTheme } from '../../main';
import BrandLogo from '../common/BrandLogo';
import ErrorBoundary from '../common/ErrorBoundary';

const { Header, Sider, Content } = Layout;

interface NavEntry {
  key: string;
  label: string;
  icon: ReactNode;
  onSelect: () => void;
  permission?: { action: string; resource: string };
}

export default function AppLayout() {
  const [collapsed, setCollapsed] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const location = useLocation();
  const navigate = useNavigate();
  const dispatch = useAppDispatch();
  const me = useAppSelector(state => state.user.me);
  const { can } = usePermission();
  const { t, lang, setLang } = useI18n();
  const { mode, toggle } = useTheme();

  const permitted = (entry: NavEntry): boolean => {
    if (!entry.permission) return true;
    return can(entry.permission.action, entry.permission.resource);
  };

  const mainEntries = useMemo<NavEntry[]>(
    () => [
      {
        key: 'welcome',
        label: t('nav.workspace'),
        icon: <HomeOutlined />,
        onSelect: () => navigate('/welcome'),
      },
      {
        key: 'ai-chat',
        label: t('nav.ai_chat'),
        icon: <CommentOutlined />,
        onSelect: () => navigate('/ai/chat'),
        // 分析 plane permission: using the Agent is an analyst capability,
        // configuring it (nav.ai_settings below) is an administrator one.
        permission: { action: 'can_query', resource: 'AIAnalysis' },
      },
      {
        key: 'dashboards',
        label: t('nav.dashboards'),
        icon: <DashboardOutlined />,
        onSelect: () => navigate('/dashboard/list/'),
        permission: { action: 'can_read', resource: 'Dashboard' },
      },
      {
        key: 'screens',
        label: t('nav.screens'),
        icon: <FundProjectionScreenOutlined />,
        onSelect: () => navigate('/screen/list'),
        permission: { action: 'can_read', resource: 'Screen' },
      },
      {
        key: 'charts',
        label: t('nav.charts'),
        icon: <BarChartOutlined />,
        onSelect: () => navigate('/chart/list/'),
        permission: { action: 'can_read', resource: 'Chart' },
      },
    ],
    [navigate, t],
  );

  // 数据集与数据库 are plumbing rather than assets the analyst consumes, so
  // they sit with SQL Lab in 数据开发.
  const devEntries = useMemo<NavEntry[]>(
    () => [
      {
        key: 'datasets',
        label: t('nav.datasets'),
        icon: <TableOutlined />,
        onSelect: () => navigate('/tablemodelview/list/'),
        permission: { action: 'can_read', resource: 'Dataset' },
      },
      {
        key: 'databases',
        label: t('nav.databases'),
        icon: <DatabaseOutlined />,
        onSelect: () => navigate('/databaseview/list/'),
        permission: { action: 'can_read', resource: 'Database' },
      },
      {
        key: 'sqllab',
        label: t('nav.sql_lab'),
        icon: <ConsoleSqlOutlined />,
        onSelect: () => navigate('/sqllab/'),
        permission: { action: 'can_write', resource: 'Query' },
      },
      // 标注图层 annotates time ranges on charts, i.e. it is authoring work on
      // data rather than console administration, so it belongs to 数据开发.
      {
        key: 'annotations',
        label: t('nav.annotations'),
        icon: <TagsOutlined />,
        onSelect: () => navigate('/annotation'),
        permission: { action: 'can_read', resource: 'AnnotationLayer' },
      },
    ],
    [navigate, t],
  );

  const settingsEntries = useMemo<NavEntry[]>(
    () => [
      // 系统管理 comes first in the system group: it is the console landing
      // page (module health, metadata DB, appearance), so operators reach it
      // before the account/authorisation screens.
      {
        key: 'system',
        label: t('nav.system'),
        icon: <SettingOutlined />,
        onSelect: () => navigate('/settings/system'),
        permission: { action: 'can_read', resource: 'System' },
      },
      {
        key: 'access',
        label: t('nav.access'),
        icon: <TeamOutlined />,
        onSelect: () => navigate('/settings/access'),
        permission: { action: 'can_read', resource: 'User' },
      },
      // AI 智能体 sits directly under 用户与角色: both are identity/agent
      // configuration surfaces, so they read as one pair.
      {
        key: 'ai-settings',
        label: t('nav.ai_settings'),
        icon: <RobotOutlined />,
        onSelect: () => navigate('/settings/ai'),
        permission: { action: 'can_read', resource: 'AIAgent' },
      },
      {
        key: 'schedules',
        label: t('nav.schedules'),
        icon: <BellOutlined />,
        onSelect: () => navigate('/settings/schedules'),
        permission: { action: 'can_read', resource: 'ReportSchedule' },
      },
      {
        key: 'audit',
        label: t('nav.audit'),
        icon: <AuditOutlined />,
        onSelect: () => navigate('/settings/audit'),
        permission: { action: 'can_read', resource: 'Log' },
      },
    ],
    [navigate, t],
  );

  const visibleMain = mainEntries.filter(permitted);
  const visibleDev = devEntries.filter(permitted);
  const visibleSettings = settingsEntries.filter(permitted);

  /**
   * One menu, three labelled sections: the workspace entry stays on top while
   * data-asset / data-development / system groups follow, matching the way
   * Chinese enterprise consoles organise navigation.
   */
  const menuItems = useMemo<MenuProps['items']>(() => {
    const items: NonNullable<MenuProps['items']> = [];
    const welcome = visibleMain.find(entry => entry.key === 'welcome');
    if (welcome) {
      items.push({ key: welcome.key, icon: welcome.icon, label: welcome.label });
    }
    const section = (label: string, entries: NavEntry[]) =>
      entries.length
        ? items.push({
            type: 'group',
            label,
            children: entries.map(entry => ({
              key: entry.key,
              icon: entry.icon,
              label: entry.label,
            })),
          })
        : undefined;

    section(
      t('nav.group.data'),
      visibleMain.filter(entry => entry.key !== 'welcome'),
    );
    section(t('nav.group.dev'), visibleDev);
    if (visibleSettings.length) {
      items.push({ type: 'divider' });
      section(t('nav.group.system'), visibleSettings);
    }
    return items;
  }, [visibleMain, visibleDev, visibleSettings, t]);

  const selectedKey = useMemo(() => {
    const path = location.pathname;
    if (path === '/' || path.startsWith('/welcome') || path.startsWith('/superset/welcome')) return 'welcome';
    if (path.startsWith('/ai/chat')) return 'ai-chat';
    if (path.startsWith('/dashboard/list')) return 'dashboards';
    if (path.startsWith('/dashboard/')) return 'dashboards';
    if (path.startsWith('/screen/')) return 'screens';
    if (path.startsWith('/chart/list')) return 'charts';
    if (path.startsWith('/explore')) return 'charts';
    if (path.startsWith('/tablemodelview')) return 'datasets';
    if (path.startsWith('/databaseview')) return 'databases';
    if (path.startsWith('/sqllab')) return 'sqllab';
    // 行级安全 now lives inside 用户与角色 as a tab; the old /rls route
    // redirects there, so both paths highlight the same menu entry.
    if (
      path.startsWith('/settings/access') ||
      path.startsWith('/settings/users') ||
      path.startsWith('/settings/roles') ||
      path.startsWith('/rls')
    )
      return 'access';
    if (path.startsWith('/settings/schedules') || path.startsWith('/alert') || path.startsWith('/report')) return 'schedules';
    if (path.startsWith('/settings/system') || path.startsWith('/settings/themes')) return 'system';
    if (path.startsWith('/settings/audit')) return 'audit';
    if (path.startsWith('/settings/ai')) return 'ai-settings';
    if (path.startsWith('/rls')) return 'access';
    if (path.startsWith('/annotation')) return 'annotations';
    return '';
  }, [location.pathname]);

  const breadcrumbItems = useMemo<BreadcrumbProps['items']>(() => {
    const items: Array<{ title: ReactNode }> = [
      { title: <Link to="/welcome">{t('layout.breadcrumb.home')}</Link> },
    ];
    const path = location.pathname;
    const entryLabel = (key: string) =>
      [...mainEntries, ...devEntries, ...settingsEntries].find(entry => entry.key === key)?.label ?? key;

    if (path.startsWith('/dashboard/list')) {
      items.push({ title: t('nav.dashboards') });
    } else if (path.startsWith('/ai/chat')) {
      items.push({ title: t('nav.ai_chat') });
    } else if (path.startsWith('/dashboard/')) {
      items.push({ title: <Link to="/dashboard/list/">{t('nav.dashboards')}</Link> });
      items.push({ title: decodeURIComponent(path.split('/')[2] ?? '') });
    } else if (path.startsWith('/chart/list')) {
      items.push({ title: t('nav.charts') });
    } else if (path.startsWith('/explore')) {
      items.push({ title: <Link to="/chart/list/">{t('nav.charts')}</Link> });
      items.push({ title: t('nav.explore') });
    } else if (path.startsWith('/tablemodelview')) {
      items.push({ title: t('nav.datasets') });
    } else if (path.startsWith('/databaseview')) {
      items.push({ title: t('nav.databases') });
    } else if (path.startsWith('/sqllab')) {
      items.push({ title: t('nav.sql_lab') });
    } else if (path.startsWith('/settings')) {
      items.push({ title: t('nav.settings') });
      items.push({ title: entryLabel(selectedKey) });
    } else if (path.startsWith('/alert')) {
      items.push({ title: t('nav.alerts') });
    } else if (path.startsWith('/report')) {
      items.push({ title: t('nav.reports') });
    } else if (path.startsWith('/rls')) {
      items.push({ title: t('nav.rls') });
    } else if (path.startsWith('/annotation')) {
      items.push({ title: t('nav.annotations') });
    } else if (path.startsWith('/welcome') || path.startsWith('/superset/welcome') || path === '/') {
      items.push({ title: t('nav.workspace') });
    }
    return items;
  }, [location.pathname, mainEntries, devEntries, settingsEntries, selectedKey, t]);

  const handleMenuClick = (key: string) => {
    const entry = [...mainEntries, ...devEntries, ...settingsEntries].find(item => item.key === key);
    entry?.onSelect();
  };

  const handleLogout = async () => {
    await dispatch(logout());
    message.success(t('common.msg.logout_success'));
    navigate('/login', { replace: true });
  };

  const languageItems: MenuProps['items'] = [
    { key: 'zh', label: t('layout.language.zh') },
    { key: 'en', label: 'English' },
  ];

  const userItems: MenuProps['items'] = [
    { key: 'profile', icon: <UserOutlined />, label: t('common.actions.profile') },
    { type: 'divider' },
    { key: 'logout', icon: <LogoutOutlined />, label: t('common.actions.logout'), danger: true },
  ];

  const displayName =
    [me?.first_name, me?.last_name].filter(Boolean).join(' ') || me?.username || 'User';
  const initial = (displayName || 'U').slice(0, 1).toUpperCase();

  return (
    <Layout className="app-shell">
      <Sider
        className="app-sider"
        theme={mode}
        width={240}
        collapsedWidth={64}
        collapsible
        collapsed={collapsed}
        trigger={null}
        // Collapse to the icon rail on tablet/phone instead of eating half the
        // viewport; the menu stays reachable via the header toggle.
        breakpoint="md"
        onBreakpoint={broken => {
          if (broken) setCollapsed(true);
        }}
      >
        <div className="app-logo" style={collapsed ? { padding: '0 19px', justifyContent: 'center' } : undefined}>
          <BrandLogo size={collapsed ? 28 : 34} className="app-logo__mark" />
          {!collapsed ? (
            <div className="app-logo__body">
              <div className="app-logo__title-row">
                <span className="app-logo__text">{t('brand.name')}</span>
                <span className="app-logo__badge">AI</span>
              </div>
              <span className="app-logo__subtitle">{t('brand.subtitle')}</span>
            </div>
          ) : null}
        </div>
        <Menu
          mode="inline"
          inlineCollapsed={collapsed}
          className="app-sider__menu"
          selectedKeys={[selectedKey]}
          items={menuItems}
          onClick={({ key }) => handleMenuClick(key)}
        />
      </Sider>

      <Layout>
        <Header className="app-header">
          <Button
            type="text"
            aria-label="toggle sidebar"
            icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
            onClick={() => setCollapsed(value => !value)}
          />
          <Breadcrumb className="app-header__breadcrumb" items={breadcrumbItems} />
          <div className="app-header__actions">
            <button
              className="theme-toggle-btn"
              type="button"
              onClick={toggle}
              title={mode === 'dark' ? t('layout.theme.to_light') : t('layout.theme.to_dark')}
              aria-label="toggle theme"
            >
              {mode === 'dark' ? <BulbFilled /> : <BulbOutlined />}
            </button>
            <Dropdown
              trigger={['click']}
              menu={{
                items: languageItems,
                selectable: true,
                selectedKeys: [lang],
                onClick: ({ key }) => setLang(key as Locale),
              }}
            >
              <Button type="text" icon={<GlobalOutlined />}>
                {lang === 'zh' ? t('layout.language.zh') : t('layout.language.en')}
              </Button>
            </Dropdown>
            <Dropdown
              trigger={['click']}
              menu={{
                items: userItems,
                onClick: ({ key }) => {
                  if (key === 'logout') void handleLogout();
                  if (key === 'profile') setProfileOpen(true);
                },
              }}
            >
              <Button type="text">
                <Space size={6}>
                  <Avatar size={24} style={{ background: 'var(--brand-primary)', fontSize: 12 }}>
                    {initial}
                  </Avatar>
                  <span style={{ maxWidth: 140, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {displayName}
                  </span>
                  <DownOutlined style={{ fontSize: 10, color: 'var(--color-text-tertiary)' }} />
                </Space>
              </Button>
            </Dropdown>
          </div>
        </Header>

        <Content className="app-content">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </Content>
      </Layout>

      <Modal
        title={t('common.actions.profile')}
        open={profileOpen}
        onCancel={() => setProfileOpen(false)}
        footer={<Button onClick={() => setProfileOpen(false)}>{t('common.actions.close')}</Button>}
      >
        <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 16 }}>
          <Avatar size={48} style={{ background: 'var(--brand-primary)', fontSize: 20 }}>
            {initial}
          </Avatar>
          <div>
            <Typography.Text strong style={{ fontSize: 15 }}>
              {displayName}
            </Typography.Text>
            <div className="cell-muted">{me?.email || '—'}</div>
          </div>
        </div>
        <div style={{ marginBottom: 8 }}>
          <span className="tiny-label">{t('common.label.username')}</span>
          <div>{me?.username ?? '—'}</div>
        </div>
        <div style={{ marginBottom: 8 }}>
          <span className="tiny-label">{t('common.label.role')}</span>
          <div style={{ marginTop: 4 }}>
            {(me?.roles ?? []).length ? (
              me?.roles.map(role => <Tag key={role.id || role.name}>{role.name}</Tag>)
            ) : (
              <span className="cell-muted">—</span>
            )}
          </div>
        </div>
        <div>
          <span className="tiny-label">{t('common.label.permission')}</span>
          <div style={{ marginTop: 4 }}>
            <Tag color="cyan">{t('common.msg.permission_count', { count: me?.permissions?.length ?? 0 })}</Tag>
            {me?.permissions?.includes('* on *') ? <Tag color="gold">{t('common.state.super_admin')}</Tag> : null}
          </div>
        </div>
        {!me?.permissions?.length ? (
          <Alert
            style={{ marginTop: 12 }}
            type="info"
            showIcon
            message={t('common.msg.no_permission')}
          />
        ) : null}
      </Modal>
    </Layout>
  );
}
