import { lazy, Suspense, useEffect } from 'react';
import type { ReactElement } from 'react';
import { Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { tokenStore } from './api/client';
import { useAppDispatch, useAppSelector } from './store';
import { fetchMe } from './store/userSlice';
import AppLayout from './components/layout/AppLayout';
import Loading from './components/common/Loading';
import Login from './pages/Login';
import Welcome from './pages/Welcome';
import DashboardList from './pages/dashboards/DashboardList';
import DashboardDetail from './pages/dashboards/DashboardDetail';
import ChartList from './pages/charts/ChartList';
import Explore from './pages/explore/Explore';
import DatasetList from './pages/datasets/DatasetList';
import DatabaseList from './pages/databases/DatabaseList';
import Access from './pages/settings/Access';
import SystemSettings from './pages/settings/SystemSettings';
import AISettings from './pages/settings/AISettings';
import AnnotationList from './pages/settings/AnnotationList';
import AuditLog from './pages/settings/AuditLog';
import Schedules from './pages/schedules/Schedules';
import NotFound from './pages/NotFound';
import ScreenList from './pages/screens/ScreenList';
import ScreenEditor from './pages/screens/ScreenEditor';
import ScreenView from './pages/screens/ScreenView';
import DashboardPublic from './pages/dashboards/DashboardPublic';
import AiChat from './pages/ai/AiChat';
import { useI18n } from './i18n';

// Monaco is heavy: keep SQL Lab out of the initial bundle.
const SqlLab = lazy(() => import('./pages/sqllab/SqlLab'));

function RequireAuth({ children }: { children: ReactElement }) {
  const dispatch = useAppDispatch();
  const status = useAppSelector(state => state.user.status);
  const location = useLocation();
  const hasToken = Boolean(tokenStore.access);
  const { t } = useI18n();

  useEffect(() => {
    if (hasToken && status === 'idle') {
      void dispatch(fetchMe());
    }
  }, [dispatch, hasToken, status]);

  if (!hasToken) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  if (status === 'idle' || status === 'loading') {
    return <Loading fullscreen tip={t('common.state.loading_workspace')} />;
  }
  if (status === 'anonymous') {
    return <Navigate to="/login" replace />;
  }
  return children;
}

export default function App() {
  const { t } = useI18n();
  return (
    <Routes>
      <Route path="/login" element={<Login />} />

      <Route
        element={
          <RequireAuth>
            <AppLayout />
          </RequireAuth>
        }
      >
        <Route path="/" element={<Navigate to="/welcome" replace />} />
        <Route path="/welcome" element={<Welcome />} />
        <Route path="/superset/welcome" element={<Navigate to="/welcome" replace />} />

        <Route path="/dashboard/list/" element={<DashboardList />} />
        <Route path="/dashboard/list" element={<DashboardList />} />
        <Route path="/dashboard/:id/" element={<DashboardDetail />} />
        <Route path="/superset/dashboard/:id/" element={<DashboardDetail />} />

        <Route path="/screen/list" element={<ScreenList />} />
        <Route path="/screen/list/" element={<ScreenList />} />

        <Route path="/chart/list/" element={<ChartList />} />
        <Route path="/chart/list" element={<ChartList />} />
        <Route path="/explore/" element={<Explore />} />
        <Route path="/explore" element={<Explore />} />

        <Route path="/ai/chat/" element={<AiChat />} />
        <Route path="/ai/chat" element={<AiChat />} />

        <Route path="/tablemodelview/list/" element={<DatasetList />} />
        <Route path="/tablemodelview/list" element={<DatasetList />} />
        <Route path="/databaseview/list/" element={<DatabaseList />} />
        <Route path="/databaseview/list" element={<DatabaseList />} />

        <Route
          path="/sqllab/"
          element={
            <Suspense fallback={<Loading height={420} tip={t('common.state.loading_sql_editor')} />}>
              <SqlLab />
            </Suspense>
          }
        />
        <Route
          path="/sqllab"
          element={
            <Suspense fallback={<Loading height={420} tip={t('common.state.loading_sql_editor')} />}>
              <SqlLab />
            </Suspense>
          }
        />

        <Route path="/settings" element={<Navigate to="/settings/access" replace />} />
        <Route path="/settings/" element={<Navigate to="/settings/access" replace />} />
        <Route path="/settings/access" element={<Access />} />
        <Route path="/settings/system" element={<SystemSettings />} />
        <Route path="/settings/audit" element={<AuditLog />} />
        <Route path="/settings/ai" element={<AISettings />} />
        <Route path="/settings/schedules" element={<Schedules />} />

        {/* Legacy entries kept as redirects so old bookmarks still land. */}
        {/* 指标字典 page removed: its rows only fed the Agent prompt. */}
        <Route path="/settings/metrics" element={<Navigate to="/settings/ai" replace />} />
        {/* 标注图层 now lives in 数据开发; /annotation is the canonical path. */}
        <Route path="/settings/annotation" element={<Navigate to="/annotation" replace />} />
        <Route path="/settings/users" element={<Navigate to="/settings/access" replace />} />
        <Route path="/settings/roles" element={<Navigate to="/settings/access?tab=roles" replace />} />
        <Route path="/settings/themes" element={<Navigate to="/settings/system?tab=theme" replace />} />
        <Route path="/users/list/" element={<Navigate to="/settings/access" replace />} />
        <Route path="/roles/list/" element={<Navigate to="/settings/access?tab=roles" replace />} />

        <Route path="/annotation" element={<AnnotationList />} />
        <Route path="/annotation/" element={<AnnotationList />} />
        <Route path="/alert/list" element={<Navigate to="/settings/schedules?tab=alert" replace />} />
        <Route path="/alert/list/" element={<Navigate to="/settings/schedules?tab=alert" replace />} />
        <Route path="/report/list" element={<Navigate to="/settings/schedules?tab=report" replace />} />
        <Route path="/report/list/" element={<Navigate to="/settings/schedules?tab=report" replace />} />
        {/* 行级安全 was merged into 用户与角色 as a tab; keep the old
            bookmarks working instead of showing a dead page. */}
        <Route path="/rls/list" element={<Navigate to="/settings/access?tab=rls" replace />} />
        <Route path="/rls/list/" element={<Navigate to="/settings/access?tab=rls" replace />} />

        <Route path="*" element={<NotFound />} />
      </Route>

      {/* Walls and walls' public links live outside the console chrome: the
          editor needs the whole viewport, and a shared link has no account. */}
      <Route
        path="/screen/:id/edit"
        element={
          <RequireAuth>
            <ScreenEditor />
          </RequireAuth>
        }
      />
      <Route
        path="/screen/:id/view"
        element={
          <RequireAuth>
            <ScreenView />
          </RequireAuth>
        }
      />
      <Route path="/screen/public/:token" element={<ScreenView />} />
      {/* A published dashboard opens without an account: the token is the
          credential and the backend mints a read-only guest session for it. */}
      <Route path="/dashboard/public/:token" element={<DashboardPublic />} />

      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}
