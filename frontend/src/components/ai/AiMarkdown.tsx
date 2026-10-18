import type { ReactNode } from 'react';
import styles from './aiMarkdown.module.css';

export interface AiMarkdownProps {
  text: string;
}

/**
 * Dependency-free Markdown renderer for Agent answers. Unlike the minimal
 * dashboard MarkdownBox (headings + bullets only), Agent answers routinely carry
 * pipe tables, fenced code blocks and ordered lists, so this renders those too.
 *
 * It builds React nodes directly — never `dangerouslySetInnerHTML` — so model
 * output (which may echo untrusted data) can never inject markup. Links are
 * restricted to http(s)/mailto so a `javascript:` URL stays inert text.
 */
export default function AiMarkdown({ text }: AiMarkdownProps) {
  if (!text.trim()) return null;

  const lines = text.replace(/\r\n?/g, '\n').split('\n');
  const blocks: ReactNode[] = [];

  let index = 0;
  let key = 0;
  while (index < lines.length) {
    const line = lines[index];

    // Fenced code block: ```lang … ```
    const fence = line.match(/^\s*```\s*([\w+-]*)\s*$/);
    if (fence) {
      const lang = fence[1];
      const body: string[] = [];
      index += 1;
      while (index < lines.length && !/^\s*```\s*$/.test(lines[index])) {
        body.push(lines[index]);
        index += 1;
      }
      index += 1; // consume the closing fence
      blocks.push(
        <pre className={styles.pre} key={`code-${key++}`}>
          {lang ? <span className={styles.lang}>{lang}</span> : null}
          <code>{body.join('\n')}</code>
        </pre>,
      );
      continue;
    }

    // Pipe table: header row + separator row + body rows.
    if (isTableRow(line) && index + 1 < lines.length && isSeparatorRow(lines[index + 1])) {
      const header = splitRow(line);
      const rows: string[][] = [];
      index += 2;
      while (index < lines.length && isTableRow(lines[index])) {
        rows.push(splitRow(lines[index]));
        index += 1;
      }
      blocks.push(
        <div className={styles.tableWrap} key={`table-${key++}`}>
          <table className={styles.table}>
            <thead>
              <tr>
                {header.map((cell, i) => (
                  <th key={i}>{renderInline(cell)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, r) => (
                <tr key={r}>
                  {header.map((_, c) => (
                    <td key={c}>{renderInline(row[c] ?? '')}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    // ATX heading.
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length;
      blocks.push(
        <div className={`${styles.heading} ${styles[`h${level}`] ?? ''}`} key={`h-${key++}`}>
          {renderInline(heading[2])}
        </div>,
      );
      index += 1;
      continue;
    }

    // Horizontal rule.
    if (/^\s*([-*_])\1{2,}\s*$/.test(line)) {
      blocks.push(<hr className={styles.hr} key={`hr-${key++}`} />);
      index += 1;
      continue;
    }

    // Blockquote (one level, flattened).
    if (/^\s*>\s?/.test(line)) {
      const quoted: string[] = [];
      while (index < lines.length && /^\s*>\s?/.test(lines[index])) {
        quoted.push(lines[index].replace(/^\s*>\s?/, ''));
        index += 1;
      }
      blocks.push(
        <blockquote className={styles.quote} key={`quote-${key++}`}>
          {renderInline(quoted.join(' '))}
        </blockquote>,
      );
      continue;
    }

    // Unordered list.
    if (/^\s*[-*+]\s+/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\s*[-*+]\s+/.test(lines[index])) {
        items.push(lines[index].replace(/^\s*[-*+]\s+/, ''));
        index += 1;
      }
      blocks.push(
        <ul className={styles.ul} key={`ul-${key++}`}>
          {items.map((item, i) => (
            <li key={i}>{renderInline(item)}</li>
          ))}
        </ul>,
      );
      continue;
    }

    // Ordered list.
    if (/^\s*\d+[.)]\s+/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\s*\d+[.)]\s+/.test(lines[index])) {
        items.push(lines[index].replace(/^\s*\d+[.)]\s+/, ''));
        index += 1;
      }
      blocks.push(
        <ol className={styles.ol} key={`ol-${key++}`}>
          {items.map((item, i) => (
            <li key={i}>{renderInline(item)}</li>
          ))}
        </ol>,
      );
      continue;
    }

    // Blank line.
    if (!line.trim()) {
      index += 1;
      continue;
    }

    // Paragraph: consume until a blank line or the start of another block.
    const para: string[] = [];
    while (index < lines.length && lines[index].trim() && !startsBlock(lines, index)) {
      para.push(lines[index]);
      index += 1;
    }
    blocks.push(
      <p className={styles.p} key={`p-${key++}`}>
        {renderInline(para.join('\n'))}
      </p>,
    );
  }

  return <div className={styles.md}>{blocks}</div>;
}

/** startsBlock reports whether the line at `i` opens a non-paragraph block. */
function startsBlock(lines: string[], i: number): boolean {
  const line = lines[i];
  if (/^\s*```/.test(line)) return true;
  if (/^(#{1,6})\s+/.test(line)) return true;
  if (/^\s*>\s?/.test(line)) return true;
  if (/^\s*[-*+]\s+/.test(line)) return true;
  if (/^\s*\d+[.)]\s+/.test(line)) return true;
  if (/^\s*([-*_])\1{2,}\s*$/.test(line)) return true;
  if (isTableRow(line) && i + 1 < lines.length && isSeparatorRow(lines[i + 1])) return true;
  return false;
}

function isTableRow(line: string): boolean {
  return /\|/.test(line) && !/^\s*>/.test(line);
}

function isSeparatorRow(line: string): boolean {
  return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line);
}

/** splitRow splits a pipe row into trimmed cells, dropping the outer pipes. */
function splitRow(line: string): string[] {
  let body = line.trim();
  if (body.startsWith('|')) body = body.slice(1);
  if (body.endsWith('|')) body = body.slice(0, -1);
  return body.split('|').map(cell => cell.trim());
}

/** Combined inline-token regex: bold, code, link, italic. */
const INLINE_RE = /(\*\*[^*\n]+\*\*|`[^`\n]+`|\[[^\]\n]+\]\([^)\n]+\)|\*[^*\n]+\*|_[^_\n]+_)/g;

/** renderInline turns the supported inline syntax into React nodes. */
function renderInline(text: string): ReactNode[] {
  const parts = text.split(INLINE_RE).filter(part => part !== '');
  return parts.map((part, index) => {
    if (/^\*\*[^*\n]+\*\*$/.test(part)) {
      return <strong key={index}>{part.slice(2, -2)}</strong>;
    }
    if (/^`[^`\n]+`$/.test(part)) {
      return (
        <code className={styles.inlineCode} key={index}>
          {part.slice(1, -1)}
        </code>
      );
    }
    const link = part.match(/^\[([^\]\n]+)\]\(([^)\n]+)\)$/);
    if (link) {
      const href = safeHref(link[2]);
      if (!href) return <span key={index}>{link[1]}</span>;
      return (
        <a className={styles.link} href={href} target="_blank" rel="noopener noreferrer" key={index}>
          {link[1]}
        </a>
      );
    }
    if (/^\*[^*\n]+\*$/.test(part) || /^_[^_\n]+_$/.test(part)) {
      return <em key={index}>{part.slice(1, -1)}</em>;
    }
    return <span key={index}>{part}</span>;
  });
}

/** safeHref only lets http(s) and mailto through, so javascript: stays inert. */
function safeHref(raw: string): string {
  const url = raw.trim();
  if (/^https?:\/\//i.test(url) || /^mailto:/i.test(url)) return url;
  return '';
}
