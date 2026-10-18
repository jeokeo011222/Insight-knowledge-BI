import { Tabs } from 'antd';
import { useSearchParams } from 'react-router-dom';
import PageHeader from '../../components/common/PageHeader';
import { useI18n } from '../../i18n';
import { usePermission } from '../../store';
import RlsList from '../rls/RlsList';
import UserList from './UserList';
import RoleList from './RoleList';

type TabKey = 'users' | 'roles' | 'rls';

const TAB_KEYS: TabKey[] = ['users', 'roles', 'rls'];

/**
 * Merged 用户与角色 page: accounts, roles and row-level security all live
 * behind one tab strip. RLS is a per-role data-scope rule, so keeping it here
 * (instead of a separate menu entry) puts "who" and "what they may see" side
 * by side — the way Chinese admin consoles group account governance.
 */
export default function Access() {
  const { t } = useI18n();
  const { can } = usePermission();
  const [params, setParams] = useSearchParams();
  const requested = params.get('tab') as TabKey | null;
  const active: TabKey = requested && TAB_KEYS.includes(requested) ? requested : 'users';

  const canReadRls = can('can_read', 'RowLevelSecurity');

  return (
    <>
      <PageHeader title={t('nav.access')} subtitle={t('settings.access.subtitle')} />
      <Tabs
        activeKey={active}
        onChange={key => setParams({ tab: key }, { replace: true })}
        items={[
          {
            key: 'users',
            label: t('nav.users'),
            children: <UserList embedded />,
          },
          {
            key: 'roles',
            label: t('nav.roles'),
            children: <RoleList embedded />,
          },
          ...(canReadRls
            ? [
                {
                  key: 'rls',
                  label: t('nav.rls'),
                  children: <RlsList embedded />,
                },
              ]
            : []),
        ]}
      />
    </>
  );
}
