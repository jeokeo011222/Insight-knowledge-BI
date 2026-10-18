import { Button, Tooltip, message } from 'antd';
import { StarFilled, StarOutlined } from '@ant-design/icons';
import { errorMessage } from '../../api/client';
import { useFavorite } from '../../features/favorites/store';
import type { FavoriteClass } from '../../features/favorites/store';
import { useI18n } from '../../i18n';

export interface FavoriteStarProps {
  className: FavoriteClass;
  objId: number | undefined;
  size?: 'small' | 'middle';
}

/** Star toggle for a chart or dashboard row. */
export default function FavoriteStar({ className, objId, size = 'small' }: FavoriteStarProps) {
  const { t } = useI18n();
  const { starred, busy, toggle } = useFavorite(className, objId);

  const handleClick = async () => {
    try {
      await toggle();
    } catch (err) {
      message.error(errorMessage(err));
    }
  };

  return (
    <Tooltip title={starred ? t('welcome.unfavorite') : t('welcome.favorites')}>
      <Button
        size={size}
        type="text"
        loading={busy}
        aria-pressed={starred}
        aria-label={starred ? t('welcome.unfavorite') : t('welcome.favorites')}
        icon={starred ? <StarFilled style={{ color: '#faad14' }} /> : <StarOutlined />}
        onClick={() => void handleClick()}
      />
    </Tooltip>
  );
}
