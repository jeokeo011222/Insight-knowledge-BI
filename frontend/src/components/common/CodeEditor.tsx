import { useEffect } from 'react';
import Editor, { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor/esm/vs/editor/editor.api';
import 'monaco-editor/esm/vs/basic-languages/sql/sql.contribution';
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker';
import { useI18n } from '../../i18n';

/**
 * Monaco is bundled locally (no CDN) so SQL Lab works offline against the Go
 * backend. Importing `editor.api` instead of the editor.main bundle keeps the
 * dozens of unrelated language definitions out of the chunk.
 */
interface MonacoEnvironmentLike {
  getWorker: () => Worker;
}

const globalScope = self as unknown as { MonacoEnvironment?: MonacoEnvironmentLike };
if (!globalScope.MonacoEnvironment) {
  globalScope.MonacoEnvironment = {
    getWorker: () => new editorWorker(),
  };
}
loader.config({ monaco: monaco as unknown as typeof import('monaco-editor') });

export interface CodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  height?: number | string;
  language?: string;
  readOnly?: boolean;
  onRun?: () => void;
}

export default function CodeEditor({
  value,
  onChange,
  height = 230,
  language = 'sql',
  readOnly = false,
  onRun,
}: CodeEditorProps) {
  const { t } = useI18n();
  useEffect(() => {
    if (!onRun) return undefined;
    const handler = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
        event.preventDefault();
        onRun();
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onRun]);

  return (
    <div style={{ height }}>
      <Editor
        height="100%"
        language={language}
        theme="vs"
        value={value}
        onChange={next => onChange(next ?? '')}
        loading={<div style={{ padding: 16, color: 'var(--color-text-tertiary)' }}>{t('common.state.loading_editor')}</div>}
        options={{
          readOnly,
          minimap: { enabled: false },
          fontSize: 13,
          fontFamily:
            '"JetBrains Mono", "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace',
          lineNumbersMinChars: 3,
          scrollBeyondLastLine: false,
          automaticLayout: true,
          wordWrap: 'on',
          padding: { top: 10, bottom: 10 },
          renderLineHighlight: 'line',
          tabSize: 2,
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
        }}
      />
    </div>
  );
}
