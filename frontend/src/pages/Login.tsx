import { useEffect } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { Alert, Button, Form, Input, Typography, message } from 'antd';
import {
  BulbFilled,
  BulbOutlined,
  ConsoleSqlOutlined,
  DashboardOutlined,
  LockOutlined,
  MessageOutlined,
  UserOutlined,
} from '@ant-design/icons';
import type { ReactNode } from 'react';
import { useAppDispatch, useAppSelector } from '../store';
import { clearError, login } from '../store/userSlice';
import type { LoginArgs } from '../store/userSlice';
import BrandLogo from '../components/common/BrandLogo';
import { useI18n } from '../i18n';
import { useTheme } from '../main';

interface LoginFormValues {
  username: string;
  password: string;
}

/**
 * Split-screen sign-in: the left pane carries the 「智观 BI」 brand story on a
 * tech-blue gradient, the right pane holds the credential form. Below 960px the
 * panes stack (see `global.css`).
 */
export default function Login() {
  const dispatch = useAppDispatch();
  const navigate = useNavigate();
  const location = useLocation();
  const { status, error } = useAppSelector(state => state.user);
  const [form] = Form.useForm<LoginFormValues>();
  const { t } = useI18n();
  const { mode, toggle } = useTheme();

  useEffect(() => {
    dispatch(clearError());
  }, [dispatch]);

  const handleSubmit = async (values: LoginFormValues) => {
    try {
      const me = await dispatch(
        login({ username: values.username, password: values.password } as LoginArgs),
      ).unwrap();
      message.success(t('login.welcome_back', { name: me.username }));
      const from = (location.state as { from?: string } | null)?.from;
      navigate(from && from !== '/login' ? from : '/welcome', { replace: true });
    } catch {
      // The error state is rendered by the redux slice.
    }
  };

  if (status === 'authenticated') {
    return <Navigate to="/welcome" replace />;
  }

  const features: Array<{ icon: ReactNode; title: string; desc: string }> = [
    {
      icon: <MessageOutlined />,
      title: t('login.hero.f1_title'),
      desc: t('login.hero.f1_desc'),
    },
    {
      icon: <ConsoleSqlOutlined />,
      title: t('login.hero.f2_title'),
      desc: t('login.hero.f2_desc'),
    },
    {
      icon: <DashboardOutlined />,
      title: t('login.hero.f3_title'),
      desc: t('login.hero.f3_desc'),
    },
  ];

  return (
    <div className="login-split">
      <div className="login-left">
        <div className="login-left__grid" />
        <div className="login-left__content">
          <div className="login-left__logo">
            <BrandLogo size={40} className="login-left__logo-mark" />
            <span className="login-left__logo-text">{t('brand.name')}</span>
            <span className="app-logo__badge">AI</span>
          </div>

          <div className="login-left__slogan">
            {t('login.hero.slogan_a')}
            <span>{t('login.hero.slogan_b')}</span>
          </div>
          <div className="login-left__desc">{t('login.hero.desc')}</div>

          <div className="login-left__features">
            {features.map(feature => (
              <div className="login-left__feature" key={feature.title}>
                <span className="login-left__feature-icon">{feature.icon}</span>
                <div className="login-left__feature-text">
                  <div className="login-left__feature-title">{feature.title}</div>
                  <div className="login-left__feature-desc">{feature.desc}</div>
                </div>
              </div>
            ))}
          </div>

          <div className="login-left__footer">{t('login.footer')}</div>
        </div>
      </div>

      <div className="login-right">
        <button
          className="theme-toggle-btn login-right__theme-toggle"
          type="button"
          onClick={toggle}
          title={mode === 'dark' ? t('layout.theme.to_light') : t('layout.theme.to_dark')}
          aria-label="toggle theme"
        >
          {mode === 'dark' ? <BulbFilled /> : <BulbOutlined />}
        </button>

        <div className="login-right__form-wrap">
          <div className="login-right__brand">
            <h1 className="login-right__title">{t('login.welcome_title')}</h1>
            <div className="login-right__subtitle">{t('login.welcome_subtitle')}</div>
          </div>

          {error ? (
            <Alert
              type="error"
              showIcon
              message={error}
              style={{ marginBottom: 16 }}
              closable
              onClose={() => dispatch(clearError())}
            />
          ) : null}

          <Form<LoginFormValues>
            form={form}
            layout="vertical"
            requiredMark={false}
            initialValues={{ username: 'admin', password: 'admin' }}
            onFinish={values => void handleSubmit(values)}
          >
            <Form.Item
              name="username"
              label={t('login.username')}
              rules={[{ required: true, message: t('login.username_required') }]}
            >
              <Input
                size="large"
                autoComplete="username"
                prefix={<UserOutlined style={{ color: 'var(--color-text-tertiary)' }} />}
                placeholder={t('login.username')}
              />
            </Form.Item>
            <Form.Item
              name="password"
              label={t('login.password')}
              rules={[{ required: true, message: t('login.password_required') }]}
            >
              <Input.Password
                size="large"
                autoComplete="current-password"
                prefix={<LockOutlined style={{ color: 'var(--color-text-tertiary)' }} />}
                placeholder={t('login.password')}
              />
            </Form.Item>
            <Form.Item style={{ marginBottom: 12 }}>
              <Button
                className="login-right__login-btn"
                type="primary"
                htmlType="submit"
                size="large"
                block
                loading={status === 'loading'}
              >
                {t('login.submit')}
              </Button>
            </Form.Item>
          </Form>

          <div className="login-right__hint">{t('login.hint')}</div>
          <div className="login-right__footer">{t('brand.footer')}</div>
        </div>
      </div>
    </div>
  );
}
