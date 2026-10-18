import { Button, Result } from 'antd';
import { useNavigate } from 'react-router-dom';
import { useI18n } from '../i18n';

export default function NotFound() {
  const navigate = useNavigate();
  const { t } = useI18n();
  return (
    <div style={{ padding: 48 }}>
      <Result
        status="404"
        title="404"
        subTitle={t('error.page_not_found')}
        extra={
          <Button type="primary" onClick={() => navigate('/welcome')}>
            {t('error.go_home')}
          </Button>
        }
      />
    </div>
  );
}
