import { useState } from 'react';
import { Alert, Button, Input, Select, Space, Spin, Upload, message } from 'antd';
import { InboxOutlined } from '@ant-design/icons';
import type { UploadFile } from 'antd/es/upload/interface';
import { client, endpoints, errorMessage } from '../../../api/client';
import type { Dataset } from '../../../api/types';
import { useI18n } from '../../../i18n';

export interface FileSourceTabProps {
  onClose: () => void;
  onCreated?: (dataset: Dataset) => void;
}

const ACCEPT = '.csv,.tsv,.txt,.xlsx,.xls';

const isWorkbook = (name: string) => /\.(xlsx|xls)$/i.test(name);

/**
 * Imports a local CSV/Excel file. The file is uploaded to the backend, which
 * parses it, materialises the rows into the managed SQLite store and creates a
 * dataset pointing at the resulting table.
 *
 * A workbook with several worksheets is first inspected so the user can pick
 * which sheets to import; every selected sheet becomes its own dataset.
 */
export default function FileSourceTab({ onClose, onCreated }: FileSourceTabProps) {
  const { t } = useI18n();
  const [fileList, setFileList] = useState<UploadFile[]>([]);
  const [sheets, setSheets] = useState<string[]>([]);
  const [selectedSheets, setSelectedSheets] = useState<string[]>([]);
  const [inspecting, setInspecting] = useState(false);
  const [tableName, setTableName] = useState('');
  const [description, setDescription] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /** Reads the worksheet names of an Excel upload; CSV files skip this step. */
  const inspectSheets = async (file: File) => {
    setInspecting(true);
    try {
      const form = new FormData();
      form.append('file', file);
      const response = await client.postForm<{ result?: { sheets?: string[] } }>(
        endpoints.datasetUploadSheets,
        form,
      );
      const names = response.result?.sheets ?? [];
      setSheets(names);
      // One sheet needs no choice; several default to the first, so the user
      // opts in to the extra datasets instead of being surprised by them.
      setSelectedSheets(names.length > 1 ? names.slice(0, 1) : []);
    } catch (err) {
      setSheets([]);
      setSelectedSheets([]);
      setError(errorMessage(err));
    } finally {
      setInspecting(false);
    }
  };

  const handleFileChange = (next: UploadFile[]) => {
    const files = next.slice(-1);
    setFileList(files);
    setError(null);
    setSheets([]);
    setSelectedSheets([]);
    const origin = files[0]?.originFileObj;
    const name = (origin as File | undefined)?.name ?? '';
    if (origin && isWorkbook(name)) {
      void inspectSheets(origin as File);
    }
  };

  const handleSubmit = async () => {
    const origin = fileList[0]?.originFileObj;
    if (!origin) {
      message.warning(t('dataset.form.file.required'));
      return;
    }
    if (sheets.length > 1 && selectedSheets.length === 0) {
      message.warning(t('dataset.form.file.sheets_required'));
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const form = new FormData();
      form.append('file', origin as File);
      if (tableName.trim()) form.append('table_name', tableName.trim());
      if (description.trim()) form.append('description', description.trim());
      selectedSheets.forEach(sheet => form.append('sheets', sheet));
      const response = await client.postForm<{ result: Dataset; results?: Dataset[] }>(
        endpoints.datasetUpload,
        form,
      );
      const created = response.results?.length ? response.results : [response.result];
      if (created.length > 1) {
        message.success(
          t('dataset.form.file.created_many').replace('{n}', String(created.length)),
        );
      } else {
        message.success(
          t('dataset.form.file.created', { name: response.result?.table_name ?? tableName }),
        );
      }
      setFileList([]);
      setSheets([]);
      setSelectedSheets([]);
      setTableName('');
      setDescription('');
      onClose();
      onCreated?.(response.result);
    } catch (err) {
      setError(errorMessage(err));
      message.error(errorMessage(err));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div>
      <Upload.Dragger
        accept={ACCEPT}
        maxCount={1}
        multiple={false}
        fileList={fileList}
        beforeUpload={() => false}
        onChange={({ fileList: next }) => handleFileChange(next)}
        onRemove={() => handleFileChange([])}
      >
        <p className="ant-upload-drag-icon">
          <InboxOutlined />
        </p>
        <p className="ant-upload-text">{t('dataset.form.file.drop_hint')}</p>
        <p className="ant-upload-hint">{t('dataset.form.file.accept')}</p>
      </Upload.Dragger>

      {inspecting ? (
        <div style={{ marginTop: 12 }}>
          <Spin size="small" /> <span className="cell-muted">{t('dataset.form.file.sheets_reading')}</span>
        </div>
      ) : null}

      {sheets.length > 1 ? (
        <div style={{ marginTop: 12 }}>
          <div className="tiny-label" style={{ marginBottom: 4 }}>
            {t('dataset.form.file.sheets_title').replace('{n}', String(sheets.length))}
          </div>
          <Select
            mode="multiple"
            allowClear
            value={selectedSheets}
            onChange={setSelectedSheets}
            options={sheets.map(name => ({ value: name, label: name }))}
            placeholder={t('dataset.form.file.sheets_placeholder')}
            style={{ width: '100%' }}
          />
          <div className="cell-muted" style={{ fontSize: 12, marginTop: 4 }}>
            {t('dataset.form.file.sheets_hint')}
          </div>
        </div>
      ) : null}

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('dataset.form.file.table_name')}
        </div>
        <Input
          value={tableName}
          onChange={event => setTableName(event.target.value)}
          placeholder={t('dataset.form.file.table_name_placeholder')}
          maxLength={120}
        />
        <div className="cell-muted" style={{ fontSize: 12, marginTop: 4 }}>
          {t('dataset.form.file.table_name_hint')}
        </div>
      </div>

      <div style={{ marginTop: 12 }}>
        <div className="tiny-label" style={{ marginBottom: 4 }}>
          {t('common.label.description')}
        </div>
        <Input.TextArea
          rows={2}
          value={description}
          onChange={event => setDescription(event.target.value)}
          placeholder={t('dataset.form.description_placeholder')}
          maxLength={400}
        />
      </div>

      {error ? (
        <Alert type="error" showIcon style={{ marginTop: 12 }} message={error} />
      ) : null}

      <Alert
        style={{ marginTop: 16 }}
        type="info"
        showIcon
        message={t('dataset.form.file.hint')}
      />

      <Space style={{ marginTop: 16, width: '100%', justifyContent: 'flex-end' }}>
        <Button onClick={onClose}>{t('common.actions.cancel')}</Button>
        <Button type="primary" loading={submitting} onClick={() => void handleSubmit()}>
          {t('dataset.form.file.submit')}
        </Button>
      </Space>
    </div>
  );
}
