import { Spin } from 'antd';

export interface LoadingProps {
  tip?: string;
  fullscreen?: boolean;
  height?: number;
}

/** Inline or full page loading indicator. */
export default function Loading({ tip = 'Loading…', fullscreen = false, height }: LoadingProps) {
  if (fullscreen) {
    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          background: 'var(--color-bg-layout)',
        }}
      >
        <Spin tip={tip} size="large">
          <div style={{ width: 120, height: 60 }} />
        </Spin>
      </div>
    );
  }
  return (
    <div className="loading-block" style={height ? { minHeight: height } : undefined}>
      <Spin tip={tip}>
        <div style={{ width: 100, height: 40 }} />
      </Spin>
    </div>
  );
}
