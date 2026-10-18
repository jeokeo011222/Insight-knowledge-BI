import type { ReactNode } from 'react';

export interface PageHeaderProps {
  title: ReactNode;
  subtitle?: ReactNode;
  extra?: ReactNode;
}

/** Consistent page heading: title, optional description and action area. */
export default function PageHeader({ title, subtitle, extra }: PageHeaderProps) {
  return (
    <div className="page-header">
      <div>
        <h1 className="page-header__title">{title}</h1>
        {subtitle ? <div className="page-header__subtitle">{subtitle}</div> : null}
      </div>
      {extra ? <div className="page-header__extra">{extra}</div> : null}
    </div>
  );
}
