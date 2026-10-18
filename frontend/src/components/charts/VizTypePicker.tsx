/**
 * Explore-side entry point to the visualisation gallery: shows the current
 * chart type next to a "view all charts" link that opens the gallery in a
 * modal, mirroring the upstream `VizTypeControl`.
 */
import { useState } from 'react';
import { Button, Modal } from 'antd';
import { useI18n } from '../../i18n';
import VizGallery from './VizGallery';
import { findVizType, vizLabel } from './vizTypes';

export interface VizTypePickerProps {
  value: string;
  onChange: (vizType: string) => void;
  /** Viz types the host does not support. */
  denyList?: string[];
}

export default function VizTypePicker({ value, onChange, denyList }: VizTypePickerProps) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState<string | null>(value);

  const current = findVizType(value);
  const currentLabel = current ? vizLabel(current, t) : value;

  const openGallery = () => {
    setPending(value);
    setOpen(true);
  };

  const confirm = (vizType: string | null) => {
    if (vizType) onChange(vizType);
    setOpen(false);
  };

  return (
    <>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 8,
          marginTop: 6,
        }}
      >
        <span className="cell-muted" style={{ fontSize: 12, minWidth: 0 }}>
          {currentLabel}
        </span>
        <Button type="link" size="small" style={{ padding: 0 }} onClick={openGallery}>
          {t('viz.gallery.view_all')}
        </Button>
      </div>

      <Modal
        open={open}
        title={t('viz.gallery.title')}
        width={1090}
        onCancel={() => setOpen(false)}
        styles={{ body: { padding: 0 } }}
        footer={[
          <Button key="cancel" onClick={() => setOpen(false)}>
            {t('common.actions.cancel')}
          </Button>,
          <Button key="ok" type="primary" disabled={!pending} onClick={() => confirm(pending)}>
            {t('viz.gallery.select')}
          </Button>,
        ]}
      >
        {open ? (
          <VizGallery
            selectedViz={pending}
            onChange={setPending}
            onDoubleClick={() => confirm(pending)}
            denyList={denyList}
          />
        ) : null}
      </Modal>
    </>
  );
}
