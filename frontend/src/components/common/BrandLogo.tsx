import type { CSSProperties } from 'react';
import { useI18n } from '../../i18n';

interface BrandLogoProps {
  size?: number;
  className?: string;
  style?: CSSProperties;
}

/**
 * 「智观 BI」 brand mark.
 *
 * The emblem reads as "AI reading your data": a circuit-traced brain (AI) rising
 * above an open book (knowledge), with ascending bars (data rising) and a spark
 * (insight). Served as a background-free PNG so the mark keeps its own gradient
 * on both the light and the dark theme without any per-theme recolouring.
 */
export default function BrandLogo({ size = 32, className, style }: BrandLogoProps) {
  const { t } = useI18n();
  return (
    <img
      src="/logo.png"
      width={size}
      height={size}
      className={className}
      style={style}
      alt={t('brand.name')}
      draggable={false}
    />
  );
}
