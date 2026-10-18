/**
 * Visualisation gallery — a port of the upstream Superset `VizTypeGallery`.
 *
 * Layout mirrors the original: a left sidebar of "All charts / Featured" plus
 * collapsible Category and Tags lists, a search box, a scrollable thumbnail
 * grid and a details pane that follows the current selection. Single click
 * selects, double click confirms.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Collapse, Input, Tag, Tooltip } from 'antd';
import type { InputRef } from 'antd';
import {
  AppstoreOutlined,
  CloseOutlined,
  FireOutlined,
  FolderOutlined,
  NumberOutlined,
  SearchOutlined,
} from '@ant-design/icons';
import { useI18n } from '../../i18n';
import {
  FEATURED_TAG,
  VIZ_CATEGORIES,
  VIZ_TYPES,
  vizCategoryLabel,
  vizLabel,
  vizTagLabel,
  type VizTypeOption,
} from './vizTypes';

export interface VizGalleryProps {
  selectedViz: string | null;
  onChange: (vizType: string | null) => void;
  onDoubleClick: () => void;
  /** Viz types the host does not support (rendered out of the list). */
  denyList?: string[];
  className?: string;
}

type SectionId = 'ALL_CHARTS' | 'FEATURED' | 'CATEGORY' | 'TAGS';

const ALL_CHARTS = 'ALL_CHARTS';
const FEATURED = 'FEATURED';
const OTHER = 'OTHER';

const THUMBNAIL_UNIT = 108;

/** Inline SVG placeholder for types without a bundled PNG thumbnail. */
function fallbackThumbnail(option: VizTypeOption): string {
  const initial = (option.label || option.value).trim().charAt(0).toUpperCase() || '?';
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="120" height="120">` +
    `<rect width="120" height="120" rx="6" fill="${option.color}" opacity="0.18"/>` +
    `<rect x="1" y="1" width="118" height="118" rx="6" fill="none" stroke="${option.color}" stroke-opacity="0.45"/>` +
    `<text x="60" y="60" font-family="sans-serif" font-size="46" font-weight="600" ` +
    `fill="${option.color}" text-anchor="middle" dominant-baseline="central">${initial}</text>` +
    `</svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

function SelectorButton({
  label,
  icon,
  selected,
  onClick,
}: {
  label: string;
  icon: ReactNode;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={selected}
      onClick={onClick}
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: 8,
        width: '100%',
        margin: '6px 0',
        padding: '5px 10px',
        border: 'none',
        borderRadius: 6,
        cursor: 'pointer',
        textAlign: 'left',
        lineHeight: 2,
        color: selected ? '#fff' : 'inherit',
        background: selected ? 'var(--brand-primary, #1b5fd9)' : 'transparent',
      }}
    >
      {icon}
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {label}
      </span>
    </button>
  );
}

function Thumbnail({
  option,
  label,
  selected,
  onSelect,
  onConfirm,
}: {
  option: VizTypeOption;
  label: string;
  selected: boolean;
  onSelect: () => void;
  onConfirm: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      title={label}
      onClick={onSelect}
      onDoubleClick={onConfirm}
      style={{
        appearance: 'none',
        border: 'none',
        background: 'none',
        padding: 0,
        cursor: 'pointer',
        width: THUMBNAIL_UNIT,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
      }}
    >
      <div style={{ position: 'relative', width: THUMBNAIL_UNIT, lineHeight: 0 }}>
        <img
          src={option.thumbnail ?? fallbackThumbnail(option)}
          alt={label}
          width={THUMBNAIL_UNIT}
          height={THUMBNAIL_UNIT}
          style={{
            border: selected ? '2px solid var(--brand-primary, #1b5fd9)' : '1px solid var(--color-border, #d9d9d9)',
            borderRadius: 6,
            background: 'var(--surface-card, #fff)',
            objectFit: 'cover',
          }}
        />
        {option.badge ? (
          <span
            style={{
              position: 'absolute',
              right: 4,
              bottom: 4,
              padding: '0 6px',
              fontSize: 11,
              fontWeight: 600,
              lineHeight: '17px',
              borderRadius: 4,
              color: 'var(--brand-primary, #1b5fd9)',
              border: '1px solid var(--brand-primary, #1b5fd9)',
              background: 'var(--surface-card, #fff)',
            }}
          >
            {option.badge}
          </span>
        ) : null}
      </div>
      <div
        style={{
          marginTop: 6,
          fontSize: 12,
          lineHeight: '17px',
          height: 34,
          overflow: 'hidden',
          textAlign: 'center',
          display: '-webkit-box',
          WebkitLineClamp: 2,
          WebkitBoxOrient: 'vertical',
          wordBreak: 'break-word',
        }}
      >
        {label}
      </div>
    </button>
  );
}

export default function VizGallery({
  selectedViz,
  onChange,
  onDoubleClick,
  denyList = [],
  className,
}: VizGalleryProps) {
  const { t, lang } = useI18n();
  const searchRef = useRef<InputRef>(null);
  const [search, setSearch] = useState('');
  const [activeSelector, setActiveSelector] = useState<string>(ALL_CHARTS);
  const [activeSection, setActiveSection] = useState<SectionId>('ALL_CHARTS');

  const labelOf = useCallback((option: VizTypeOption) => vizLabel(option, t), [t]);

  const denied = useMemo(() => new Set(denyList), [denyList]);

  const entries = useMemo(
    () =>
      VIZ_TYPES.filter(option => !denied.has(option.value)).sort((a, b) =>
        labelOf(a).localeCompare(labelOf(b)),
      ),
    [denied, labelOf],
  );

  const byCategory = useMemo(() => {
    const map = new Map<string, VizTypeOption[]>();
    entries.forEach(option => {
      const bucket = map.get(option.category);
      if (bucket) bucket.push(option);
      else map.set(option.category, [option]);
    });
    return map;
  }, [entries]);

  const categories = useMemo(() => {
    const known = VIZ_CATEGORIES.filter(category => byCategory.has(category));
    // Any category coming from a third-party registry entry lands under "Other".
    const extras = Array.from(byCategory.keys()).filter(
      category => !(VIZ_CATEGORIES as readonly string[]).includes(category),
    );
    return extras.length ? [...known, OTHER] : known;
  }, [byCategory]);

  const tags = useMemo(() => {
    const set = new Set<string>();
    entries.forEach(option => (option.tags ?? []).forEach(tag => set.add(tag)));
    set.delete(FEATURED_TAG);
    // Sort by the displayed name: sorting by the English identifier would put
    // the Chinese labels in a seemingly random order.
    return Array.from(set).sort((a, b) =>
      vizTagLabel(a, t).localeCompare(vizTagLabel(b, t), lang),
    );
  }, [entries, t, lang]);

  const featured = useMemo(
    () => entries.filter(option => (option.tags ?? []).includes(FEATURED_TAG)),
    [entries],
  );

  const searchResults = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return [];
    const scored: Array<{ option: VizTypeOption; score: number }> = [];
    entries.forEach(option => {
      const name = labelOf(option).toLowerCase();
      let score = 0;
      if (name.includes(term)) score = Math.max(score, 4);
      if ((option.tags ?? []).some(tag => tag.toLowerCase().includes(term))) score = Math.max(score, 2);
      const description = (lang === 'zh' ? option.descriptionZh : option.descriptionEn) ?? '';
      if (description.toLowerCase().includes(term)) score = Math.max(score, 1);
      if (
        (option.tags ?? []).some(tag => vizTagLabel(tag, t).toLowerCase().includes(term))
      ) {
        score = Math.max(score, 2);
      }
      if (option.value.toLowerCase().includes(term)) score = Math.max(score, 3);
      if (score > 0) scored.push({ option, score });
    });
    return scored.sort((a, b) => b.score - a.score || labelOf(a.option).localeCompare(labelOf(b.option)))
      .map(item => item.option);
  }, [search, entries, labelOf, lang, t]);

  const isActivelySearching = Boolean(search.trim());

  const visible = useMemo(() => {
    if (isActivelySearching) return searchResults;
    if (activeSection === 'ALL_CHARTS') return entries;
    if (activeSection === 'FEATURED') return featured;
    if (activeSection === 'CATEGORY') {
      if (activeSelector === OTHER) {
        return entries.filter(option => !VIZ_CATEGORIES.includes(option.category as (typeof VIZ_CATEGORIES)[number]));
      }
      return byCategory.get(activeSelector) ?? [];
    }
    if (activeSection === 'TAGS') {
      return entries.filter(option => (option.tags ?? []).includes(activeSelector));
    }
    return [];
  }, [isActivelySearching, searchResults, activeSection, activeSelector, entries, featured, byCategory]);

  const selectedOption = selectedViz ? entries.find(option => option.value === selectedViz) ?? null : null;

  const clickSelector = useCallback(
    (selector: string, section: SectionId) => {
      setActiveSelector(selector);
      setActiveSection(section);
      setSearch('');
    },
    [],
  );

  // Focus the search box when the gallery mounts, matching upstream behaviour.
  useEffect(() => {
    searchRef.current?.focus();
  }, []);

  const gridRows = selectedOption
    ? 'auto minmax(140px, 1fr) minmax(180px, 33%)'
    : 'auto minmax(140px, 1fr)';

  return (
    <div
      className={className}
      style={{
        display: 'grid',
        gridTemplateRows: gridRows,
        gridTemplateColumns: 'minmax(160px, 220px) 5fr',
        gridTemplateAreas: "'sidebar search' 'sidebar main' 'details details'",
        height: '66vh',
        overflow: 'auto',
      }}
    >
      <div
        aria-label={t('viz.gallery.title')}
        role="tablist"
        style={{
          gridArea: 'sidebar',
          borderRight: '1px solid var(--color-border, #d9d9d9)',
          padding: '10px 8px',
          overflow: 'auto',
        }}
      >
        <SelectorButton
          label={t('viz.gallery.all_charts')}
          icon={<AppstoreOutlined />}
          selected={!isActivelySearching && activeSelector === ALL_CHARTS && activeSection === 'ALL_CHARTS'}
          onClick={() => clickSelector(ALL_CHARTS, 'ALL_CHARTS')}
        />
        <SelectorButton
          label={t('viz.gallery.featured')}
          icon={<FireOutlined />}
          selected={!isActivelySearching && activeSelector === FEATURED && activeSection === 'FEATURED'}
          onClick={() => clickSelector(FEATURED, 'FEATURED')}
        />
        <Collapse
          ghost
          expandIconPosition="end"
          defaultActiveKey={['CATEGORY']}
          items={[
            {
              key: 'CATEGORY',
              label: <span style={{ fontSize: 12 }}>{t('viz.gallery.category')}</span>,
              children: categories.map(category => (
                <SelectorButton
                  key={category}
                  label={
                    category === OTHER
                      ? t('viz.gallery.other')
                      : vizCategoryLabel(category, t)
                  }
                  icon={<FolderOutlined />}
                  selected={
                    !isActivelySearching && activeSelector === category && activeSection === 'CATEGORY'
                  }
                  onClick={() => clickSelector(category, 'CATEGORY')}
                />
              )),
            },
            {
              key: 'TAGS',
              label: <span style={{ fontSize: 12 }}>{t('viz.gallery.tags')}</span>,
              children: tags.map(tag => (
                <SelectorButton
                  key={tag}
                  label={vizTagLabel(tag, t)}
                  icon={<NumberOutlined />}
                  selected={!isActivelySearching && activeSelector === tag && activeSection === 'TAGS'}
                  onClick={() => clickSelector(tag, 'TAGS')}
                />
              )),
            },
          ]}
        />
      </div>

      <div style={{ gridArea: 'search', margin: '12px 16px 4px' }}>
        <Input
          ref={searchRef}
          type="text"
          allowClear
          value={search}
          placeholder={t('viz.gallery.search_placeholder')}
          onChange={event => setSearch(event.target.value)}
          prefix={<SearchOutlined />}
          suffix={
            search ? (
              <CloseOutlined
                aria-label={t('common.actions.close')}
                onClick={() => setSearch('')}
                style={{ cursor: 'pointer', color: 'var(--color-text-secondary, #999)' }}
              />
            ) : null
          }
        />
      </div>

      <div style={{ gridArea: 'main', overflowY: 'auto', padding: 12 }}>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: `repeat(auto-fill, ${THUMBNAIL_UNIT}px)`,
            gridAutoRows: 'max-content',
            gap: 14,
            justifyContent: 'space-evenly',
            alignItems: 'start',
          }}
        >
          {visible.map(option => (
            <Thumbnail
              key={option.value}
              option={option}
              label={labelOf(option)}
              selected={selectedViz === option.value}
              onSelect={() => onChange(option.value)}
              onConfirm={onDoubleClick}
            />
          ))}
        </div>
      </div>

      {selectedOption ? (
        <div
          style={{
            gridArea: 'details',
            borderTop: '1px solid var(--color-border, #d9d9d9)',
            padding: 16,
            display: 'grid',
            gridTemplateColumns: '1fr 1fr',
            gridTemplateRows: 'auto auto 1fr',
            gridTemplateAreas: "'viz-name examples-header' 'viz-tags examples' 'description examples'",
            columnGap: 12,
          }}
        >
          <h3
            style={{
              gridArea: 'viz-name',
              margin: 0,
              fontSize: 15,
              fontWeight: 600,
              display: 'flex',
              alignItems: 'center',
              gap: 8,
            }}
          >
            {labelOf(selectedOption)}
            {selectedOption.badge ? (
              <span
                style={{
                  padding: '0 6px',
                  fontSize: 11,
                  fontWeight: 600,
                  border: '1px solid var(--brand-primary, #1b5fd9)',
                  borderRadius: 4,
                  color: 'var(--brand-primary, #1b5fd9)',
                }}
              >
                {selectedOption.badge}
              </span>
            ) : null}
          </h3>
          <div style={{ gridArea: 'viz-tags', paddingRight: 24, paddingBottom: 8 }}>
            {(selectedOption.tags ?? []).map(tag => (
              <Tooltip key={tag} title={tag}>
                <Tag bordered={false} style={{ marginBottom: 6 }}>
                  {vizTagLabel(tag, t)}
                </Tag>
              </Tooltip>
            ))}
          </div>
          <p style={{ gridArea: 'description', margin: 0, overflow: 'auto', paddingRight: 24 }}>
            {(lang === 'zh' ? selectedOption.descriptionZh : selectedOption.descriptionEn) ??
              t('viz.gallery.no_description')}
          </p>
          <h3 style={{ gridArea: 'examples-header', margin: 0, fontSize: 15, fontWeight: 600 }}>
            {t('viz.gallery.examples')}
          </h3>
          <div
            style={{
              gridArea: 'examples',
              display: 'flex',
              gap: 16,
              overflow: 'auto',
              alignItems: 'center',
            }}
          >
            <img
              src={selectedOption.thumbnail ?? fallbackThumbnail(selectedOption)}
              alt={labelOf(selectedOption)}
              title={labelOf(selectedOption)}
              style={{
                maxHeight: 150,
                borderRadius: 6,
                border: '1px solid var(--color-border, #d9d9d9)',
                background: 'var(--surface-card, #fff)',
              }}
            />
          </div>
        </div>
      ) : null}
    </div>
  );
}
