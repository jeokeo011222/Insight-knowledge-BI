import { Empty } from 'antd';
import type { ReactNode } from 'react';
import { useI18n } from '../../i18n';

export interface EmptyStateProps {
  description?: ReactNode;
  action?: ReactNode;
  image?: ReactNode;
}

/** Neutral empty placeholder used when the API returns no rows. */
export default function EmptyState({ description, action, image }: EmptyStateProps) {
  const { t } = useI18n();
  return (
    <div className="empty-block">
      <Empty
        image={image ?? Empty.PRESENTED_IMAGE_SIMPLE}
        description={description ?? t('common.state.no_data')}
      >
        {action}
      </Empty>
    </div>
  );
}
