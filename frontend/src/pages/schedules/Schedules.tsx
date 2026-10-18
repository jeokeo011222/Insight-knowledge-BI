import { Tabs } from 'antd';
import { useSearchParams } from 'react-router-dom';
import PageHeader from '../../components/common/PageHeader';
import { useI18n } from '../../i18n';
import ScheduleList from '../reports/ScheduleList';

type TabKey = 'alert' | 'report';

/**
 * Merged 告警与报告 page. Alerts and scheduled reports share the same
 * ReportSchedule model, so a single page with two tabs replaces the two
 * near-identical list routes.
 */
export default function Schedules() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const active: TabKey = params.get('tab') === 'report' ? 'report' : 'alert';

  return (
    <>
      <PageHeader title={t('nav.schedules')} subtitle={t('settings.schedules.subtitle')} />
      <Tabs
        activeKey={active}
        onChange={key => setParams({ tab: key }, { replace: true })}
        items={[
          {
            key: 'alert',
            label: t('nav.alerts'),
            children: (
              <ScheduleList type="Alert" embedded title={t('nav.alerts')} subtitle={t('alert.list.subtitle')} />
            ),
          },
          {
            key: 'report',
            label: t('nav.reports'),
            children: (
              <ScheduleList type="Report" embedded title={t('nav.reports')} subtitle={t('report.list.subtitle')} />
            ),
          },
        ]}
      />
    </>
  );
}
