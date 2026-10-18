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
 * A gradient tile ("观" as a lens) holding three ascending bars (data rising):
 * the mark reads as "intelligent observation of data". Pure inline SVG so it
 * stays crisp at any size and needs no bitmap asset.
 */
export default function BrandLogo({ size = 32, className, style }: BrandLogoProps) {
  const { t } = useI18n();
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 48 48"
      width={size}
      height={size}
      className={className}
      style={style}
      role="img"
      aria-label={t('brand.name')}
    >
      <defs>
        <linearGradient id="zg-brand-gradient" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#1b5fd9" />
          <stop offset="100%" stopColor="#22c3e6" />
        </linearGradient>
      </defs>
      <rect x="0" y="0" width="48" height="48" rx="14" fill="url(#zg-brand-gradient)" />
      {/* Lens halo — the "观" (observe) reading. */}
      <circle cx="24" cy="24" r="13.5" fill="#ffffff" opacity="0.16" />
      {/* Ascending bars — the "智" (insight) reading. */}
      <rect x="14" y="26" width="5" height="9.5" rx="2.5" fill="#ffffff" />
      <rect x="21.5" y="21" width="5" height="14.5" rx="2.5" fill="#ffffff" />
      <rect x="29" y="15.5" width="5" height="20" rx="2.5" fill="#ffffff" />
    </svg>
  );
}
