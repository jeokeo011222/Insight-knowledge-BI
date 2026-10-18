import type { CSSProperties, ReactNode } from 'react';
import { ChartEmpty } from '../EChart';
import { tGlobal } from '../../../i18n';
import { PRIMARY, asText, readList, readString, type VizContext, type VizRenderer } from '../utils';
import { AXIS_LINE_COLOR, BORDER_COLOR, CHART_COLORS, SUBTLE_BG, TEXT_MUTED, TEXT_STRONG } from '../utils';

const INLINE = /(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\([^)]+\))/g;

function inlineNodes(text: string, prefix: string): ReactNode[] {
  return text
    .split(INLINE)
    .filter(part => part !== '')
    .map((part, index) => {
      const key = `${prefix}-${index}`;
      if (part.startsWith('**') && part.endsWith('**')) {
        return <strong key={key}>{part.slice(2, -2)}</strong>;
      }
      if (part.startsWith('`') && part.endsWith('`')) {
        return (
          <code
            key={key}
            style={{
              background: SUBTLE_BG,
              borderRadius: 3,
              padding: '1px 4px',
              fontSize: '0.92em',
              color: CHART_COLORS[1],
            }}
          >
            {part.slice(1, -1)}
          </code>
        );
      }
      const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(part);
      if (link) {
        return (
          <a key={key} href={link[2]} target="_blank" rel="noreferrer" style={{ color: PRIMARY }}>
            {link[1]}
          </a>
        );
      }
      return <span key={key}>{part}</span>;
    });
}

/** Minimal markdown renderer (headings, lists, quotes, rules, bold, code, links). */
export function MarkdownBlock({ text, compact }: { text: string; compact: boolean }) {
  const blocks: ReactNode[] = [];
  let list: string[] = [];
  const flush = () => {
    if (!list.length) return;
    const items = list;
    list = [];
    blocks.push(
      <ul key={`ul-${blocks.length}`} style={{ margin: '2px 0 6px', paddingLeft: 18 }}>
        {items.map((item, index) => (
          <li key={index} style={{ marginBottom: 2 }}>
            {inlineNodes(item, `li-${blocks.length}-${index}`)}
          </li>
        ))}
      </ul>,
    );
  };

  text.split(/\r?\n/).forEach((raw, index) => {
    const line = raw.trimEnd();
    if (!line.trim()) {
      flush();
      return;
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      flush();
      const level = heading[1].length;
      blocks.push(
        <div
          key={`h-${index}`}
          style={{
            fontSize: level <= 2 ? 16 : 14,
            fontWeight: 600,
            color: TEXT_STRONG,
            margin: '6px 0 3px',
          }}
        >
          {inlineNodes(heading[2], `h-${index}`)}
        </div>,
      );
      return;
    }
    if (/^-{3,}$/.test(line.trim())) {
      flush();
      blocks.push(
        <hr
          key={`hr-${index}`}
          style={{ border: 'none', borderTop: `1px solid ${AXIS_LINE_COLOR}`, margin: '6px 0' }}
        />,
      );
      return;
    }
    const bullet = /^[-*+]\s+(.*)$/.exec(line);
    if (bullet) {
      list.push(bullet[1]);
      return;
    }
    if (/^>\s?/.test(line)) {
      flush();
      blocks.push(
        <blockquote
          key={`q-${index}`}
          style={{
            margin: '4px 0',
            padding: '2px 10px',
            borderLeft: `3px solid ${PRIMARY}`,
            color: TEXT_MUTED,
          }}
        >
          {inlineNodes(line.replace(/^>\s?/, ''), `q-${index}`)}
        </blockquote>,
      );
      return;
    }
    flush();
    blocks.push(
      <p key={`p-${index}`} style={{ margin: '2px 0 5px', lineHeight: 1.6 }}>
        {inlineNodes(line, `p-${index}`)}
      </p>,
    );
  });
  flush();

  return <div style={{ fontSize: compact ? 11 : 12, color: 'var(--color-text-secondary)' }}>{blocks}</div>;
}

function textSource(ctx: VizContext): string {
  const direct = readString(ctx.formData, [
    'markdown',
    'text',
    'header',
    'code',
    'paragraph',
    'handlebars',
  ]);
  if (direct) return direct;
  const first = ctx.rows[0];
  if (!first) return '';
  const value = Object.values(first)[0];
  return asText(value);
}

const frameStyle = (height: number): CSSProperties => ({
  height,
  overflow: 'auto',
  padding: '8px 10px',
});

export const MarkdownRenderer: VizRenderer = ({ ctx }) => {
  const text = textSource(ctx);
  if (!text.trim()) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.no_text')} />;
  return (
    <div style={frameStyle(ctx.height)}>
      <MarkdownBlock text={text} compact={ctx.compact} />
    </div>
  );
};

export const HeaderRenderer: VizRenderer = ({ ctx }) => {
  const text = textSource(ctx) || 'Header';
  return (
    <div
      style={{
        height: ctx.height,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'flex-start',
        padding: '8px 12px',
        fontSize: ctx.compact ? 18 : 22,
        fontWeight: 600,
        color: TEXT_STRONG,
        overflow: 'hidden',
      }}
    >
      {text}
    </div>
  );
};

export const SeparatorRenderer: VizRenderer = ({ ctx }) => (
  <div style={{ height: ctx.height, display: 'flex', alignItems: 'center', padding: '0 8px' }}>
    <hr style={{ width: '100%', border: 'none', borderTop: `1px solid ${AXIS_LINE_COLOR}`, margin: 0 }} />
  </div>
);

export const IframeRenderer: VizRenderer = ({ ctx }) => {
  const url =
    readString(ctx.formData, ['url', 'iframe_url', 'src']) ??
    (textSource(ctx).startsWith('http') ? textSource(ctx) : null);
  if (!url || !/^https?:\/\//i.test(url)) {
    return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.iframe_need_url')} />;
  }
  return (
    <iframe
      src={url}
      title="superset-iframe"
      sandbox="allow-scripts allow-same-origin allow-popups"
      style={{ width: '100%', height: ctx.height, border: 'none', display: 'block' }}
    />
  );
};

export const HandlebarsRenderer: VizRenderer = ({ ctx }) => {
  const template = textSource(ctx);
  if (!template.trim()) return <ChartEmpty height={ctx.height} message={tGlobal('chart.empty.no_template')} />;
  const row = ctx.rows[0] ?? {};
  const rendered = template.replace(/\{\{\s*([^}]+?)\s*\}\}/g, (_match, key: string) =>
    asText(row[key.trim()]),
  );
  return (
    <div style={frameStyle(ctx.height)}>
      <MarkdownBlock text={rendered} compact={ctx.compact} />
    </div>
  );
};

export const FilterBoxRenderer: VizRenderer = ({ ctx }) => {
  const filters = readList(ctx.formData, 'groupby');
  const rawAdhoc = ctx.formData.adhoc_filters;
  const adhocCount = Array.isArray(rawAdhoc) ? rawAdhoc.length : 0;
  return (
    <div
      style={{
        height: ctx.height,
        margin: 8,
        border: `1px dashed ${BORDER_COLOR}`,
        borderRadius: 4,
        padding: '10px 12px',
        fontSize: 11,
        color: 'var(--color-text-tertiary)',
        overflow: 'auto',
      }}
    >
      <div style={{ fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 4 }}>Filter box</div>
      <div>{tGlobal('chart.empty.filter_placeholder')}</div>
      {filters.length > 0 && <div style={{ marginTop: 6 }}>Filters: {filters.join(', ')}</div>}
      {adhocCount > 0 && <div style={{ marginTop: 2 }}>Ad-hoc: {adhocCount} condition(s)</div>}
    </div>
  );
};
