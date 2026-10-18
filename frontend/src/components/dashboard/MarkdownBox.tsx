import type { ReactNode } from 'react';

export interface MarkdownBoxProps {
  text: string;
  height?: number;
}

/** Splits `**bold**` runs into text/strong nodes without using innerHTML. */
function renderInline(text: string): ReactNode[] {
  const parts = text.split(/(\*\*[^*]+\*\*)/g);
  return parts.map((part, index) => {
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) {
      return <strong key={index}>{part.slice(2, -2)}</strong>;
    }
    return <span key={index}>{part}</span>;
  });
}

/**
 * Minimal, XSS-safe markdown block used by MARKDOWN tiles: headings, bullet
 * lists and paragraphs (rich HTML is intentionally not evaluated).
 */
export default function MarkdownBox({ text, height }: MarkdownBoxProps) {
  if (!text.trim()) {
    return (
      <div className="dashboard-markdown dashboard-markdown--empty" style={{ height }}>
        <span className="cell-muted">Empty markdown block</span>
      </div>
    );
  }

  const lines = text.split(/\r?\n/);
  return (
    <div className="dashboard-markdown" style={height ? { height } : undefined}>
      {lines.map((line, index) => {
        const key = `${index}-${line.slice(0, 12)}`;
        if (/^###\s+/.test(line)) {
          return <h5 key={key}>{renderInline(line.replace(/^###\s+/, ''))}</h5>;
        }
        if (/^##\s+/.test(line)) {
          return <h4 key={key}>{renderInline(line.replace(/^##\s+/, ''))}</h4>;
        }
        if (/^#\s+/.test(line)) {
          return <h3 key={key}>{renderInline(line.replace(/^#\s+/, ''))}</h3>;
        }
        if (/^\s*[-*]\s+/.test(line)) {
          return (
            <div className="dashboard-markdown__item" key={key}>
              <span className="dashboard-markdown__bullet">•</span>
              <span>{renderInline(line.replace(/^\s*[-*]\s+/, ''))}</span>
            </div>
          );
        }
        if (/^\s*---\s*$/.test(line)) {
          return <hr key={key} className="dashboard-markdown__rule" />;
        }
        if (!line.trim()) return <div key={key} className="dashboard-markdown__gap" />;
        return <p key={key}>{renderInline(line)}</p>;
      })}
    </div>
  );
}
