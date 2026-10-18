import { Drawer, Tabs } from 'antd';
import type { Dataset } from '../../api/types';
import { useI18n } from '../../i18n';
import DatabaseSourceTab from './sources/DatabaseSourceTab';
import FileSourceTab from './sources/FileSourceTab';
import ApiSourceTab from './sources/ApiSourceTab';

export interface CreateDatasetDrawerProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (dataset: Dataset) => void;
}

/**
 * Entry point for creating a dataset. Three tabs cover the three supported data
 * acquisition paths: an existing database table, an uploaded CSV/Excel file and
 * a remote JSON-over-HTTP API.
 */
export default function CreateDatasetDrawer({ open, onClose, onCreated }: CreateDatasetDrawerProps) {
  const { t } = useI18n();

  return (
    <Drawer
      title={t('dataset.form.create')}
      width={860}
      open={open}
      onClose={onClose}
      destroyOnClose
    >
      <Tabs
        destroyInactiveTabPane
        items={[
          {
            key: 'database',
            label: t('dataset.form.tab.database'),
            children: <DatabaseSourceTab onClose={onClose} onCreated={onCreated} />,
          },
          {
            key: 'file',
            label: t('dataset.form.tab.file'),
            children: <FileSourceTab onClose={onClose} onCreated={onCreated} />,
          },
          {
            key: 'api',
            label: t('dataset.form.tab.api'),
            children: <ApiSourceTab onClose={onClose} onCreated={onCreated} />,
          },
        ]}
      />
    </Drawer>
  );
}
