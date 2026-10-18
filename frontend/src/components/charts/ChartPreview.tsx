import { Component, type ErrorInfo, type ReactNode } from 'react';
import type { QueryResultItem } from '../../api/types';
import { getVizDefinition } from './registry';
import { TableRenderer } from './renderers/matrix';
import type { VizContext } from './utils';
import { useI18n } from '../../i18n';

export interface ChartPreviewProps {
  result: QueryResultItem;
  vizType?: string;
  height?: number;
  compact?: boolean;
  formData?: Record<string, unknown>;
  onClickData?: (params: unknown) => void;
}

interface BoundaryProps {
  children: ReactNode;
  fallback: ReactNode;
}

/** Keeps a broken renderer from blanking the whole dashboard tile. */
class VizBoundary extends Component<BoundaryProps, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.warn('[charts] renderer failed, falling back to table', error, info.componentStack);
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

/**
 * Renders one query result with the visualisation registered for `vizType`.
 * Unknown types fall back to a plain table, matching legacy behaviour.
 */
export default function ChartPreview({
  result,
  vizType = 'table',
  height = 260,
  compact = false,
  formData,
  onClickData,
}: ChartPreviewProps) {
  const { t } = useI18n();
  const rows = Array.isArray(result.data) ? result.data : [];
  const colnames =
    result.colnames && result.colnames.length
      ? result.colnames
      : rows.length
        ? Object.keys(rows[0])
        : [];

  const definition = getVizDefinition(vizType);
  const ctx: VizContext = {
    rows,
    colnames,
    coltypes: Array.isArray(result.coltypes) ? result.coltypes : [],
    vizType,
    formData: formData ?? {},
    height,
    compact,
    annotations: Array.isArray(result.annotation_data) ? result.annotation_data : [],
    onClickData,
  };

  if (!rows.length || !colnames.length) {
    if (definition?.allowEmpty) {
      const Renderer = definition.renderer;
      return <Renderer ctx={ctx} />;
    }
    return (
      <div className="cell-muted" style={{ padding: 16, fontSize: 12 }}>
        {t('chart.preview.no_rows')}
      </div>
    );
  }

  if (!definition) {
    return (
      <div>
        <div className="cell-muted" style={{ fontSize: 11, padding: '4px 8px' }}>
          {t('chart.preview.unknown_type', { type: vizType })}
        </div>
        <TableRenderer ctx={ctx} />
      </div>
    );
  }

  const Renderer = definition.renderer;
  return (
    <VizBoundary key={vizType} fallback={<TableRenderer ctx={ctx} />}>
      <Renderer ctx={ctx} />
    </VizBoundary>
  );
}
