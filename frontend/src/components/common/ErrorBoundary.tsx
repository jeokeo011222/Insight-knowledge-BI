import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { Button, Typography } from 'antd';
import { tGlobal } from '../../i18n';

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  error: Error | null;
}

/** Catches render errors so a broken page cannot blank the whole SPA. */
export default class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // eslint-disable-next-line no-console
    console.error('[ErrorBoundary]', error, info.componentStack);
  }

  private handleReload = (): void => {
    this.setState({ error: null });
    window.location.reload();
  };

  render(): ReactNode {
    const { error } = this.state;
    if (!error) return this.props.children;
    if (this.props.fallback) return this.props.fallback;
    return (
      <div className="error-boundary">
        <Typography.Title level={4} style={{ marginBottom: 8 }}>
          {tGlobal('error.boundary.title')}
        </Typography.Title>
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          {tGlobal('error.boundary.desc')}
        </Typography.Paragraph>
        <pre className="mono-block">{error.message}</pre>
        <Button type="primary" onClick={this.handleReload} style={{ marginTop: 16 }}>
          {tGlobal('common.actions.reload')}
        </Button>
      </div>
    );
  }
}
