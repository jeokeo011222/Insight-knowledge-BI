/**
 * Left sidebar of the wall editor: the component palette and the layer tree.
 */
import { Tabs, Tooltip } from 'antd';
import {
  AppstoreOutlined,
  BarChartOutlined,
  BorderOutlined,
  ClockCircleOutlined,
  CodeOutlined,
  DeleteOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  FieldNumberOutlined,
  FileImageOutlined,
  FilterOutlined,
  FontSizeOutlined,
  FormOutlined,
  FundOutlined,
  GroupOutlined,
  LayoutOutlined,
  LineOutlined,
  LockOutlined,
  OrderedListOutlined,
  PlayCircleOutlined,
  UnlockOutlined,
  VerticalAlignBottomOutlined,
  VerticalAlignTopOutlined,
} from '@ant-design/icons';
import type { ScreenComponent, ScreenComponentType, ScreenConfig } from '../../api/screens';
import { SCREEN_PALETTE, COMPONENT_LABEL_KEY, sortedComponents } from '../../features/screen/model';
import { useI18n } from '../../i18n';

const ICONS: Record<string, React.ReactNode> = {
  number: <FieldNumberOutlined />,
  chart: <BarChartOutlined />,
  table: <AppstoreOutlined />,
  progress: <FundOutlined />,
  list: <OrderedListOutlined />,
  text: <FontSizeOutlined />,
  marquee: <PlayCircleOutlined />,
  clock: <ClockCircleOutlined />,
  rich: <CodeOutlined />,
  image: <FileImageOutlined />,
  video: <PlayCircleOutlined />,
  iframe: <LayoutOutlined />,
  border: <BorderOutlined />,
  decor: <AppstoreOutlined />,
  shape: <LineOutlined />,
  filter: <FilterOutlined />,
  group: <GroupOutlined />,
};

export interface ScreenLeftPanelProps {
  config: ScreenConfig;
  selectedIds: string[];
  onAdd: (type: ScreenComponentType) => void;
  onSelect: (ids: string[]) => void;
  onChange: (id: string, patch: Partial<ScreenComponent>) => void;
  onRemove: (id: string) => void;
  onReorder: (id: string, direction: 'up' | 'down' | 'top' | 'bottom') => void;
}

export default function ScreenLeftPanel({
  config,
  selectedIds,
  onAdd,
  onSelect,
  onChange,
  onRemove,
  onReorder,
}: ScreenLeftPanelProps) {
  const { t } = useI18n();
  const layers = sortedComponents(config.components).reverse();

  return (
    <div className="scr-left">
      <Tabs
        size="small"
        defaultActiveKey="palette"
        items={[
          {
            key: 'palette',
            label: t('screen.panel.components'),
            children: (
              <div className="scr-palette">
                {SCREEN_PALETTE.map(group => (
                  <div key={group.key} className="scr-palette__group">
                    <div className="scr-palette__label">
                      {t(group.labelKey as 'screen.palette.data')}
                    </div>
                    <div className="scr-palette__grid">
                      {group.items.map(item => (
                        <button
                          key={item.type}
                          type="button"
                          className="scr-palette__item"
                          onClick={() => onAdd(item.type)}
                          title={t(item.labelKey as 'screen.widget.kpi')}
                        >
                          <span className="scr-palette__icon">{ICONS[item.icon]}</span>
                          <span>{t(item.labelKey as 'screen.widget.kpi')}</span>
                        </button>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            ),
          },
          {
            key: 'layers',
            label: `${t('screen.panel.layers')} (${config.components.length})`,
            children: (
              <div className="scr-layers">
                {layers.map(component => {
                  const active = selectedIds.includes(component.id);
                  return (
                    <div
                      key={component.id}
                      className={`scr-layers__row${active ? ' is-active' : ''}${
                        component.visible === false ? ' is-hidden' : ''
                      }`}
                      onClick={() => onSelect([component.id])}
                    >
                      <span className="scr-layers__icon">
                        {ICONS[
                          SCREEN_PALETTE.flatMap(group => group.items).find(
                            item => item.type === component.type,
                          )?.icon ?? 'shape'
                        ] ?? <FormOutlined />}
                      </span>
                      <span className="scr-layers__name">
                        {component.name}
                        <em>{t(COMPONENT_LABEL_KEY[component.type] as 'screen.widget.kpi')}</em>
                      </span>
                      <span className="scr-layers__actions" onClick={e => e.stopPropagation()}>
                        <Tooltip title={t('screen.layers.up')}>
                          <button type="button" onClick={() => onReorder(component.id, 'top')}>
                            <VerticalAlignTopOutlined />
                          </button>
                        </Tooltip>
                        <Tooltip title={t('screen.layers.down')}>
                          <button type="button" onClick={() => onReorder(component.id, 'bottom')}>
                            <VerticalAlignBottomOutlined />
                          </button>
                        </Tooltip>
                        <Tooltip
                          title={
                            component.locked ? t('screen.canvas.unlock') : t('screen.canvas.lock')
                          }
                        >
                          <button
                            type="button"
                            onClick={() => onChange(component.id, { locked: !component.locked })}
                          >
                            {component.locked ? <LockOutlined /> : <UnlockOutlined />}
                          </button>
                        </Tooltip>
                        <Tooltip title={t('screen.layers.visible')}>
                          <button
                            type="button"
                            onClick={() =>
                              onChange(component.id, { visible: component.visible === false })
                            }
                          >
                            {component.visible === false ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                          </button>
                        </Tooltip>
                        <Tooltip title={t('screen.canvas.remove')}>
                          <button type="button" onClick={() => onRemove(component.id)}>
                            <DeleteOutlined />
                          </button>
                        </Tooltip>
                      </span>
                    </div>
                  );
                })}
                {!layers.length && <div className="scr-layers__empty">{t('screen.layers.empty')}</div>}
              </div>
            ),
          },
        ]}
      />
    </div>
  );
}
