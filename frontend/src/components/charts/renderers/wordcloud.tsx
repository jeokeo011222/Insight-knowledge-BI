import { ChartEmpty } from '../EChart';
import { asText, formatCompact, readList, stableColor } from '../utils';
import { dimensionTotals, metricColumn } from '../dataUtils';
import type { VizRenderer } from '../utils';

const MAX_WORDS = 120;

/** Word cloud fallback: sized text layout, no echarts-wordcloud dependency. */
export const WordCloudRenderer: VizRenderer = ({ ctx }) => {
  const metric = metricColumn(ctx);
  const dim =
    readList(ctx.formData, 'groupby').find(key => ctx.colnames.includes(key)) ??
    ctx.colnames.find(key => key !== metric) ??
    ctx.colnames[0];
  if (!dim) return <ChartEmpty height={ctx.height} />;

  const data = dimensionTotals(ctx.rows, dim, metric && metric !== dim ? metric : null)
    .sort((a, b) => b.value - a.value)
    .slice(0, MAX_WORDS);
  if (!data.length) return <ChartEmpty height={ctx.height} />;

  const words = data.map(word => ({
    name: asText(word.name) || '(null)',
    value: word.value,
  }));
  const max = Math.max(...words.map(word => word.value), 1);
  const base = ctx.compact ? 9 : 11;
  const spread = ctx.compact ? 20 : 30;

  return (
    <div
      style={{
        height: ctx.height,
        display: 'flex',
        flexWrap: 'wrap',
        alignContent: 'center',
        justifyContent: 'center',
        gap: '4px 12px',
        padding: 10,
        overflow: 'hidden',
      }}
    >
      {words.map((word, index) => {
        const ratio = Math.sqrt(Math.max(0, word.value) / max);
        return (
          <span
            key={`${word.name}-${index}`}
            title={`${word.name}: ${formatCompact(word.value)}`}
            style={{
              fontSize: base + spread * ratio,
              color: stableColor(word.name),
              fontWeight: ratio > 0.62 ? 600 : 400,
              lineHeight: 1.1,
              whiteSpace: 'nowrap',
            }}
          >
            {word.name}
          </span>
        );
      })}
    </div>
  );
};
