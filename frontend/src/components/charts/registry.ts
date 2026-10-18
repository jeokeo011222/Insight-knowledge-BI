import type { VizRenderer } from './utils';
import {
  AnnotatedTimeseries,
  BubbleRenderer,
  CompareRenderer,
  MixedTimeseries,
  TimeseriesArea,
  TimeseriesBar,
  TimeseriesLine,
  TimeseriesScatter,
  TimeseriesSmooth,
  TimeseriesStep,
} from './renderers/timeseries';
import {
  DonutRenderer,
  FunnelRenderer,
  GraphRenderer,
  PartitionRenderer,
  PieRenderer,
  RoseRenderer,
  SankeyRenderer,
  SunburstRenderer,
  TreemapRenderer,
} from './renderers/categorical';
import { RadarRenderer } from './renderers/radar';
import { WordCloudRenderer } from './renderers/wordcloud';
import {
  AgGridRenderer,
  BoxPlotRenderer,
  HeatmapRenderer,
  HistogramRenderer,
  PivotRenderer,
  TableRenderer,
  ViolinRenderer,
} from './renderers/matrix';
import { CalendarRenderer } from './renderers/calendar';
import {
  BigNumberRenderer,
  BigNumberTotalRenderer,
  BulletRenderer,
  GaugeRenderer,
  LiquidRenderer,
} from './renderers/numeric';
import { ParetoRenderer, WaterfallRenderer } from './renderers/composite';
import { GeoRenderer } from './renderers/geo';
import {
  FilterBoxRenderer,
  HandlebarsRenderer,
  HeaderRenderer,
  IframeRenderer,
  MarkdownRenderer,
  SeparatorRenderer,
} from './renderers/text';
import {
  ButterflyRenderer,
  CandlestickRenderer,
  GanttRenderer,
  HorizonRenderer,
  ParallelRenderer,
  TreeRenderer,
} from './renderers/advanced';

export type VizCategory =
  | 'Time series'
  | 'Categorical'
  | 'Numeric'
  | 'Matrix'
  | 'Flow & relation'
  | 'Geo'
  | 'Text';

export interface VizDefinition {
  label: string;
  category: VizCategory;
  color: string;
  renderer: VizRenderer;
  /** Text/iframe charts can render from params alone, without query rows. */
  allowEmpty?: boolean;
  /** One-line English description, used by the viz gallery details pane. */
  descriptionEn?: string;
  /** One-line Chinese description, used by the viz gallery details pane. */
  descriptionZh?: string;
  /** Free-form tags; power the gallery "Tags" filter and the featured set. */
  tags?: string[];
  /** Corner badge on the gallery thumbnail ('精选' / '高级分析'). */
  badge?: string;
  /** Thumbnail served from /public, e.g. '/viz_thumbnails/pie.png'. */
  thumbnail?: string;
}

const TS_COLOR = '#1fa8c9';
const CAT_COLOR = '#5ac189';
const NUM_COLOR = '#ff7f44';
const MAT_COLOR = '#454e7c';
const FLOW_COLOR = '#a868b7';
const GEO_COLOR = '#3cc7c5';
const TEXT_COLOR = '#666666';

/** Tag that marks the curated "featured" set in the gallery sidebar. */
export const FEATURED_TAG = 'Featured';
/** Tag that groups the less common, analysis-heavy chart types. */
export const ADVANCED_TAG = 'Advanced-Analytics';

const BADGE_FEATURED = '精选';
const BADGE_ADVANCED = '高级分析';

const thumb = (name: string) => `/viz_thumbnails/${name}.png`;

/* ------------------------------------------------------------ descriptions */

const D_LINE_EN = 'A continuous line plotted over a time axis — the default way to read trends and seasonality.';
const D_LINE_ZH = '在时间轴上连续绘制，用于观察趋势与季节性变化。';

const D_SMOOTH_EN = 'A line chart with a smoothed curve, useful when the series is noisy.';
const D_SMOOTH_ZH = '带平滑曲线的折线图，适合波动较大、需要弱化噪声的数据。';

const D_STEP_EN = 'A line chart drawn as discrete steps, best when values hold steady between points.';
const D_STEP_ZH = '以阶梯方式绘制的折线图，适合数值在区间内保持不变的场景。';

const D_AREA_EN = 'A line chart with the area under the curve filled, emphasising cumulative magnitude.';
const D_AREA_ZH = '折线下方填充面积的图，强调累计量与规模。';

const D_BAR_EN = 'Vertical bars comparing a metric across categories or time buckets.';
const D_BAR_ZH = '用竖直柱子比较各分类或时间分桶的指标值。';

const D_DIST_BAR_EN = 'Bars split by a dimension so several groups can be compared side by side.';
const D_DIST_BAR_ZH = '按维度分组并排显示的柱状图，便于多组对比。';

const D_SCATTER_EN = 'Points plotted to reveal correlation, clusters and outliers between two measures.';
const D_SCATTER_ZH = '用散点展示两个度量之间的相关性、聚集与离群点。';

const D_BUBBLE_EN = 'A scatter plot that adds a size channel, encoding a third measure as bubble radius.';
const D_BUBBLE_ZH = '在散点基础上用气泡半径编码第三维度。';

const D_ANNOTATED_EN = 'A time-series line that overlays an average line and annotations for context.';
const D_ANNOTATED_ZH = '在时间序列折线上叠加均值线与标注，帮助解读。';

const D_MIXED_EN = 'Combines bars and lines in one chart so two measures of different scale share an axis.';
const D_MIXED_ZH = '在同一张图中混合柱与折线，便于不同量级的指标共用坐标。';

const D_COMPARE_EN = 'Two groups compared bar by bar, usually a period over period comparison.';
const D_COMPARE_ZH = '逐条对比两个分组，常用于同比/环比对照。';

const D_PIE_EN = 'Slice a total into shares — best with a handful of categories.';
const D_PIE_ZH = '把一个总量切成若干占比，适合分类较少的场景。';

const D_DONUT_EN = 'A pie with a hollow centre that frees room for a headline value.';
const D_DONUT_ZH = '中间留空的饼图，可在圆心补充一个总计值。';

const D_ROSE_EN = 'Slices with equal angles but varying radius, letting many categories fit.';
const D_ROSE_ZH = '角度相同、半径不同的扇形图，可容纳较多分类。';

const D_SUNBURST_EN = 'A radial hierarchy: rings are levels, arc size is the measure.';
const D_SUNBURST_ZH = '径向层级图：每一环代表一个层级，弧长代表度量大小。';

const D_FUNNEL_EN = 'Stages of a process narrowing stage by stage, e.g. a conversion funnel.';
const D_FUNNEL_ZH = '逐级收窄的流程阶段图，常用于转化漏斗。';

const D_TREEMAP_EN = 'Nested rectangles sized by value, showing a hierarchy as a whole.';
const D_TREEMAP_ZH = '按数值大小划分的嵌套矩形，整体呈现层级结构。';

const D_PARTITION_EN = 'A hierarchical partition that shows levels as stacked bands.';
const D_PARTITION_ZH = '以分层带状方式呈现层级结构的分块图。';

const D_SANKEY_EN = 'Flows between nodes, with band width proportional to volume.';
const D_SANKEY_ZH = '节点之间的流向图，带宽与流量成正比。';

const D_CHORD_EN = 'A circular flow diagram relating several groups at once.';
const D_CHORD_ZH = '环形流向图，可同时展示多组之间的关系。';

const D_GRAPH_EN = 'A node-link network chart for relationships and connections.';
const D_GRAPH_ZH = '节点连线的关系网络图，用于展示关联。';

const D_RADAR_EN = 'Several measures on radial axes, comparing profiles across entities.';
const D_RADAR_ZH = '多指标沿放射轴排布，用于对比多个对象的画像。';

const D_WORD_CLOUD_EN = 'Terms sized by frequency — a quick read on what dominates a text field.';
const D_WORD_CLOUD_ZH = '按词频决定字号，快速看出文本字段中的主导内容。';

const D_BIG_NUMBER_EN = 'A single KPI figure, optionally with a comparison delta and sparkline.';
const D_BIG_NUMBER_ZH = '突出展示单个 KPI 数字，可选环比涨跌与迷你趋势线。';

const D_BIG_NUMBER_TOTAL_EN = 'A KPI figure aggregated over all returned rows, with a trendline.';
const D_BIG_NUMBER_TOTAL_ZH = '对全部返回行求和的 KPI 数字，附趋势线。';

const D_GAUGE_EN = 'A dial showing one value against a target range.';
const D_GAUGE_ZH = '表盘样式的刻度图，展示单值相对目标区间的位置。';

const D_LIQUID_EN = 'A fill-level gauge where the fill height encodes a percentage.';
const D_LIQUID_ZH = '水位式仪表，填充高度表示完成百分比。';

const D_BULLET_EN = 'A horizontal bar with a target marker, tracking progress against a goal.';
const D_BULLET_ZH = '带目标标记的横向条，展示相对目标的完成度。';

const D_WATERFALL_EN = 'Sequential bars that add up and subtract, explaining how a total changed.';
const D_WATERFALL_ZH = '逐项增减的瀑布图，解释总量是如何形成的。';

const D_PARETO_EN = 'Bars sorted descending with a cumulative line, separating the vital few.';
const D_PARETO_ZH = '降序柱状图叠加累计线，区分关键少数与次要多数。';

const D_TABLE_EN = 'A paginated table of raw result rows — the safe default for any query.';
const D_TABLE_ZH = '分页展示原始结果行，任何查询都能用的安全默认视图。';

const D_AG_GRID_EN = 'A feature-rich grid with sorting, filtering and column resizing.';
const D_AG_GRID_ZH = '支持排序、筛选与列宽拖拽的高级表格。';

const D_TIME_TABLE_EN = 'A table with time-oriented formatting and per-column sparklines.';
const D_TIME_TABLE_ZH = '面向时间格式化的表格，可按列附带迷你趋势。';

const D_PIVOT_EN = 'Rows and columns crossed against a metric, with totals.';
const D_PIVOT_ZH = '行列交叉的透视表，可带合计。';

const D_TIME_PIVOT_EN = 'A pivot whose column hierarchy follows time granularity.';
const D_TIME_PIVOT_ZH = '列维度按时间粒度展开的透视表。';

const D_HEATMAP_EN = 'A colour matrix revealing density and patterns across two dimensions.';
const D_HEATMAP_ZH = '用颜色矩阵展示两个维度上的密度与规律。';

const D_CAL_HEATMAP_EN = 'A calendar grid where each day is shaded by its value.';
const D_CAL_HEATMAP_ZH = '日历网格，按每天的值着色。';

const D_HISTOGRAM_EN = 'Bins a numeric column to show its distribution.';
const D_HISTOGRAM_ZH = '对数值列分桶，展示其分布形态。';

const D_BOX_PLOT_EN = 'Box-and-whisker summary of a numeric column: median, quartiles and outliers.';
const D_BOX_PLOT_ZH = '箱线图：展示中位数、四分位与离群点。';

const D_VIOLIN_EN = 'A box plot with a density curve, showing the full shape of a distribution.';
const D_VIOLIN_ZH = '在箱线图基础上叠加密度曲线，呈现分布全貌。';

const D_DECK_SCATTER_EN = 'Latitude/longitude points drawn on a map, sized or coloured by a measure.';
const D_DECK_SCATTER_ZH = '按经纬度在地图上打点，可用度量决定大小或颜色。';

const D_DECK_GEOJSON_EN = 'GeoJSON features rendered as data-driven map shapes.';
const D_DECK_GEOJSON_ZH = '把 GeoJSON 要素渲染为数据驱动的面状地图。';

const D_DECK_GRID_EN = 'A map grid where each cell aggregates the points inside it.';
const D_DECK_GRID_ZH = '把落入同一网格的点聚合展示的地图。';

const D_DECK_HEX_EN = 'A hexagonal binning map that smooths point density.';
const D_DECK_HEX_ZH = '用六边形分箱平滑点密度地图。';

const D_DECK_PATH_EN = 'Arcs or lines connecting coordinates, e.g. routes and flows.';
const D_DECK_PATH_ZH = '连接坐标的弧线或路径，用于航线与流向。';

const D_DECK_ARC_EN = 'Weighted arcs between two coordinate sets, typically origin to destination.';
const D_DECK_ARC_ZH = '在两组坐标之间绘制带权弧线，常用于起讫流向。';

const D_DECK_POLYGON_EN = 'Filled polygons drawn from coordinate sequences.';
const D_DECK_POLYGON_ZH = '按坐标序列绘制填充多边形。';

const D_DECK_SCREENGRID_EN = 'A pixel-density screen grid visualising point clusters.';
const D_DECK_SCREENGRID_ZH = '以像素密度着色来呈现点聚集的地图。';

const D_DECK_HEATMAP_EN = 'A smoothed heat surface built from point density.';
const D_DECK_HEATMAP_ZH = '基于点密度生成平滑热力表面的地图。';

const D_WORLD_MAP_EN = 'A choropleth shading world countries by a measure.';
const D_WORLD_MAP_ZH = '按度量给世界各国着色的分级统计地图。';

const D_COUNTRY_MAP_EN = 'A choropleth of administrative regions within one country.';
const D_COUNTRY_MAP_ZH = '单一国家内部行政区的分级统计地图。';

const D_MAPBOX_EN = 'A base map with overlays for spatial context.';
const D_MAPBOX_ZH = '带底图叠加层的地图，提供空间背景。';

const D_MARKDOWN_EN = 'Free-form Markdown text rendered on the dashboard.';
const D_MARKDOWN_ZH = '在仪表板上渲染的自由 Markdown 文本。';

const D_HEADER_EN = 'A heading block that labels and divides dashboard sections.';
const D_HEADER_ZH = '标题块，用于标注与切分仪表板区块。';

const D_SEPARATOR_EN = 'A thin divider used to space out dashboard sections.';
const D_SEPARATOR_ZH = '用于分隔仪表板区块的细分隔线。';

const D_IFRAME_EN = 'Embeds an external web page inside a dashboard tile.';
const D_IFRAME_ZH = '在仪表板卡片内嵌外部网页。';

const D_HANDLEBARS_EN = 'A templated text block rendered from the query result.';
const D_HANDLEBARS_ZH = '依据查询结果渲染的模板化文本块。';

const D_FILTER_BOX_EN = 'A filter control that narrows the charts on the dashboard.';
const D_FILTER_BOX_ZH = '用于收窄仪表板图表范围的过滤器控件。';

const D_CANDLE_EN = 'Open/high/low/close candles showing price-style movement over time.';
const D_CANDLE_ZH = '开高低收蜡烛图，展示随时间变化的行情走势。';

const D_GANTT_EN = 'Horizontal bars spanning each task interval, revealing schedule overlap.';
const D_GANTT_ZH = '按任务区间横向展开的条形图，可看出排期重叠。';

const D_HORIZON_EN = 'Stacked area bands that compress large value ranges into a compact height.';
const D_HORIZON_ZH = '分层堆叠的面积带，把大范围数值压缩到紧凑高度。';

const D_PARA_EN = 'Parallel axes letting you spot patterns and trade-offs across several measures.';
const D_PARA_ZH = '平行坐标轴，用于在多指标间发现模式与取舍。';

const D_BUTTERFLY_EN = 'Mirrored horizontal bars comparing two groups around a shared axis.';
const D_BUTTERFLY_ZH = '围绕中轴左右镜像的横向条形图，对比两个分组。';

const D_TREE_EN = 'A hierarchical tree laid out left to right, from root to leaves.';
const D_TREE_ZH = '自左向右展开的层级树图，从根节点到叶子。';

export const REGISTRY: Record<string, VizDefinition> = {
  /* --------------------------------------------------------- time series */
  echarts_timeseries: {
    label: 'Time-series Line Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesLine,
    descriptionEn: D_LINE_EN, descriptionZh: D_LINE_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('line'),
  },
  echarts_timeseries_line: {
    label: 'Time-series Line Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesLine,
    descriptionEn: D_LINE_EN, descriptionZh: D_LINE_ZH, tags: ['ECharts', 'Time Series', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('line'),
  },
  echarts_timeseries_smooth: {
    label: 'Time-series Smooth Line', category: 'Time series', color: TS_COLOR, renderer: TimeseriesSmooth,
    descriptionEn: D_SMOOTH_EN, descriptionZh: D_SMOOTH_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('line'),
  },
  echarts_timeseries_step: {
    label: 'Time-series Step Line', category: 'Time series', color: TS_COLOR, renderer: TimeseriesStep,
    descriptionEn: D_STEP_EN, descriptionZh: D_STEP_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('line'),
  },
  echarts_timeseries_area: {
    label: 'Time-series Area Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesArea,
    descriptionEn: D_AREA_EN, descriptionZh: D_AREA_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('area'),
  },
  echarts_timeseries_bar: {
    label: 'Time-series Bar Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesBar,
    descriptionEn: D_BAR_EN, descriptionZh: D_BAR_ZH, tags: ['ECharts', 'Time Series', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('bar'),
  },
  echarts_timeseries_scatter: {
    label: 'Time-series Scatter', category: 'Time series', color: TS_COLOR, renderer: TimeseriesScatter,
    descriptionEn: D_SCATTER_EN, descriptionZh: D_SCATTER_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('bubble'),
  },
  line: {
    label: 'Line Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesLine,
    descriptionEn: D_LINE_EN, descriptionZh: D_LINE_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('line'),
  },
  area: {
    label: 'Area Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesArea,
    descriptionEn: D_AREA_EN, descriptionZh: D_AREA_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('area'),
  },
  bar: {
    label: 'Bar Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesBar,
    descriptionEn: D_BAR_EN, descriptionZh: D_BAR_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('bar'),
  },
  dist_bar: {
    label: 'Distribution - Bar Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesBar,
    descriptionEn: D_DIST_BAR_EN, descriptionZh: D_DIST_BAR_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('dist_bar'),
  },
  scatter: {
    label: 'Scatter Plot', category: 'Time series', color: TS_COLOR, renderer: TimeseriesScatter,
    descriptionEn: D_SCATTER_EN, descriptionZh: D_SCATTER_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('bubble'),
  },
  bubble: {
    label: 'Bubble Chart', category: 'Time series', color: TS_COLOR, renderer: BubbleRenderer,
    descriptionEn: D_BUBBLE_EN, descriptionZh: D_BUBBLE_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('bubble'),
  },
  annotated_timeseries: {
    label: 'Annotated Time-series', category: 'Time series', color: TS_COLOR, renderer: AnnotatedTimeseries,
    descriptionEn: D_ANNOTATED_EN, descriptionZh: D_ANNOTATED_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('line_multi'),
  },
  mixed_timeseries: {
    label: 'Mixed Time-series', category: 'Time series', color: TS_COLOR, renderer: MixedTimeseries,
    descriptionEn: D_MIXED_EN, descriptionZh: D_MIXED_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('dual_line'),
  },
  compare: {
    label: 'Compare', category: 'Time series', color: TS_COLOR, renderer: CompareRenderer,
    descriptionEn: D_COMPARE_EN, descriptionZh: D_COMPARE_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('compare'),
  },

  /* --------------------------------------------------------- categorical */
  pie: {
    label: 'Pie Chart', category: 'Categorical', color: CAT_COLOR, renderer: PieRenderer,
    descriptionEn: D_PIE_EN, descriptionZh: D_PIE_ZH, tags: ['ECharts', 'Part to Whole', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('pie'),
  },
  donut: {
    label: 'Donut Chart', category: 'Categorical', color: CAT_COLOR, renderer: DonutRenderer,
    descriptionEn: D_DONUT_EN, descriptionZh: D_DONUT_ZH, tags: ['ECharts', 'Part to Whole'], thumbnail: thumb('pie'),
  },
  rose: {
    label: 'Rose Chart', category: 'Categorical', color: CAT_COLOR, renderer: RoseRenderer,
    descriptionEn: D_ROSE_EN, descriptionZh: D_ROSE_ZH, tags: ['ECharts', 'Part to Whole'], thumbnail: thumb('rose'),
  },
  sunburst: {
    label: 'Sunburst Chart', category: 'Categorical', color: CAT_COLOR, renderer: SunburstRenderer,
    descriptionEn: D_SUNBURST_EN, descriptionZh: D_SUNBURST_ZH, tags: ['ECharts', 'Hierarchy'], thumbnail: thumb('sunburst'),
  },
  funnel: {
    label: 'Funnel Chart', category: 'Categorical', color: CAT_COLOR, renderer: FunnelRenderer,
    descriptionEn: D_FUNNEL_EN, descriptionZh: D_FUNNEL_ZH, tags: ['ECharts', 'Comparison'],
  },
  treemap: {
    label: 'Treemap', category: 'Categorical', color: CAT_COLOR, renderer: TreemapRenderer,
    descriptionEn: D_TREEMAP_EN, descriptionZh: D_TREEMAP_ZH, tags: ['ECharts', 'Hierarchy'], thumbnail: thumb('treemap'),
  },
  partition: {
    label: 'Partition', category: 'Categorical', color: CAT_COLOR, renderer: PartitionRenderer,
    descriptionEn: D_PARTITION_EN, descriptionZh: D_PARTITION_ZH, tags: ['ECharts', 'Hierarchy'], thumbnail: thumb('partition'),
  },
  sankey: {
    label: 'Sankey Diagram', category: 'Flow & relation', color: FLOW_COLOR, renderer: SankeyRenderer,
    descriptionEn: D_SANKEY_EN, descriptionZh: D_SANKEY_ZH, tags: ['ECharts', 'Relationship', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('sankey'),
  },
  graph: {
    label: 'Graph Chart', category: 'Flow & relation', color: FLOW_COLOR, renderer: GraphRenderer,
    descriptionEn: D_GRAPH_EN, descriptionZh: D_GRAPH_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('directed_force'),
  },
  chord: {
    label: 'Chord Diagram', category: 'Flow & relation', color: FLOW_COLOR, renderer: SankeyRenderer,
    descriptionEn: D_CHORD_EN, descriptionZh: D_CHORD_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('chord'),
  },
  radar: {
    label: 'Radar Chart', category: 'Flow & relation', color: FLOW_COLOR, renderer: RadarRenderer,
    descriptionEn: D_RADAR_EN, descriptionZh: D_RADAR_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('multi'),
  },
  word_cloud: {
    label: 'Word Cloud', category: 'Categorical', color: CAT_COLOR, renderer: WordCloudRenderer,
    descriptionEn: D_WORD_CLOUD_EN, descriptionZh: D_WORD_CLOUD_ZH, tags: ['Text'], thumbnail: thumb('word_cloud'),
  },

  /* -------------------------------------------------------------- numeric */
  big_number: {
    label: 'Big Number', category: 'Numeric', color: NUM_COLOR, renderer: BigNumberRenderer,
    descriptionEn: D_BIG_NUMBER_EN, descriptionZh: D_BIG_NUMBER_ZH, tags: ['KPI', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('big_number'),
  },
  big_number_total: {
    label: 'Big Number with Trendline', category: 'Numeric', color: NUM_COLOR, renderer: BigNumberTotalRenderer,
    descriptionEn: D_BIG_NUMBER_TOTAL_EN, descriptionZh: D_BIG_NUMBER_TOTAL_ZH, tags: ['KPI'], thumbnail: thumb('big_number_total'),
  },
  gauge: {
    label: 'Gauge Chart', category: 'Numeric', color: NUM_COLOR, renderer: GaugeRenderer,
    descriptionEn: D_GAUGE_EN, descriptionZh: D_GAUGE_ZH, tags: ['ECharts', 'KPI'], thumbnail: thumb('bullet'),
  },
  liquid: {
    label: 'Liquid Gauge', category: 'Numeric', color: NUM_COLOR, renderer: LiquidRenderer,
    descriptionEn: D_LIQUID_EN, descriptionZh: D_LIQUID_ZH, tags: ['ECharts', 'KPI'], thumbnail: thumb('bullet'),
  },
  bullet: {
    label: 'Bullet Chart', category: 'Numeric', color: NUM_COLOR, renderer: BulletRenderer,
    descriptionEn: D_BULLET_EN, descriptionZh: D_BULLET_ZH, tags: ['ECharts', 'KPI'], thumbnail: thumb('bullet'),
  },
  waterfall: {
    label: 'Waterfall Chart', category: 'Numeric', color: NUM_COLOR, renderer: WaterfallRenderer,
    descriptionEn: D_WATERFALL_EN, descriptionZh: D_WATERFALL_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('bar'),
  },
  pareto: {
    label: 'Pareto Chart', category: 'Numeric', color: NUM_COLOR, renderer: ParetoRenderer,
    descriptionEn: D_PARETO_EN, descriptionZh: D_PARETO_ZH, tags: ['ECharts', 'Comparison'], thumbnail: thumb('bar'),
  },

  /* --------------------------------------------------------------- matrix */
  table: {
    label: 'Table', category: 'Matrix', color: MAT_COLOR, renderer: TableRenderer,
    descriptionEn: D_TABLE_EN, descriptionZh: D_TABLE_ZH, tags: ['Tabular', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('table'),
  },
  ag_grid_table: {
    label: 'AG Grid Table', category: 'Matrix', color: MAT_COLOR, renderer: AgGridRenderer,
    descriptionEn: D_AG_GRID_EN, descriptionZh: D_AG_GRID_ZH, tags: ['Tabular'], thumbnail: thumb('table'),
  },
  time_table: {
    label: 'Time-series Table', category: 'Matrix', color: MAT_COLOR, renderer: AgGridRenderer,
    descriptionEn: D_TIME_TABLE_EN, descriptionZh: D_TIME_TABLE_ZH, tags: ['Tabular'], thumbnail: thumb('time_table'),
  },
  pivot_table: {
    label: 'Pivot Table', category: 'Matrix', color: MAT_COLOR, renderer: PivotRenderer,
    descriptionEn: D_PIVOT_EN, descriptionZh: D_PIVOT_ZH, tags: ['Tabular', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('pivot_table'),
  },
  time_pivot: {
    label: 'Time Pivot', category: 'Matrix', color: MAT_COLOR, renderer: PivotRenderer,
    descriptionEn: D_TIME_PIVOT_EN, descriptionZh: D_TIME_PIVOT_ZH, tags: ['Tabular'], thumbnail: thumb('time_pivot'),
  },
  heatmap: {
    label: 'Heatmap', category: 'Matrix', color: MAT_COLOR, renderer: HeatmapRenderer,
    descriptionEn: D_HEATMAP_EN, descriptionZh: D_HEATMAP_ZH, tags: ['ECharts', 'Distribution', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('heatmap'),
  },
  calendar_heatmap: {
    label: 'Calendar Heatmap', category: 'Matrix', color: MAT_COLOR, renderer: CalendarRenderer,
    descriptionEn: D_CAL_HEATMAP_EN, descriptionZh: D_CAL_HEATMAP_ZH, tags: ['Time Series'], thumbnail: thumb('cal_heatmap'),
  },
  histogram: {
    label: 'Histogram', category: 'Matrix', color: MAT_COLOR, renderer: HistogramRenderer,
    descriptionEn: D_HISTOGRAM_EN, descriptionZh: D_HISTOGRAM_ZH, tags: ['ECharts', 'Distribution'], thumbnail: thumb('histogram'),
  },
  box_plot: {
    label: 'Box Plot', category: 'Matrix', color: MAT_COLOR, renderer: BoxPlotRenderer,
    descriptionEn: D_BOX_PLOT_EN, descriptionZh: D_BOX_PLOT_ZH, tags: ['ECharts', 'Distribution'], thumbnail: thumb('box_plot'),
  },
  violin: {
    label: 'Violin Plot', category: 'Matrix', color: MAT_COLOR, renderer: ViolinRenderer,
    descriptionEn: D_VIOLIN_EN, descriptionZh: D_VIOLIN_ZH, tags: ['ECharts', 'Distribution'], thumbnail: thumb('box_plot'),
  },

  /* ------------------------------------------------------------------ geo */
  deck_scatter: {
    label: 'Deck.gl Scatterplot', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_SCATTER_EN, descriptionZh: D_DECK_SCATTER_ZH, tags: ['Geo', FEATURED_TAG],
    badge: BADGE_FEATURED, thumbnail: thumb('deck_scatter'),
  },
  deck_geojson: {
    label: 'Deck.gl GeoJSON', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_GEOJSON_EN, descriptionZh: D_DECK_GEOJSON_ZH, tags: ['Geo'], thumbnail: thumb('deck_geojson'),
  },
  deck_grid: {
    label: 'Deck.gl Grid', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_GRID_EN, descriptionZh: D_DECK_GRID_ZH, tags: ['Geo'], thumbnail: thumb('deck_grid'),
  },
  deck_hex: {
    label: 'Deck.gl Hexagon', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_HEX_EN, descriptionZh: D_DECK_HEX_ZH, tags: ['Geo'], thumbnail: thumb('deck_hex'),
  },
  deck_path: {
    label: 'Deck.gl Path', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_PATH_EN, descriptionZh: D_DECK_PATH_ZH, tags: ['Geo'], thumbnail: thumb('deck_path'),
  },
  deck_arc: {
    label: 'Deck.gl Arc', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_ARC_EN, descriptionZh: D_DECK_ARC_ZH, tags: ['Geo'], thumbnail: thumb('deck_arc'),
  },
  deck_polygon: {
    label: 'Deck.gl Polygon', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_POLYGON_EN, descriptionZh: D_DECK_POLYGON_ZH, tags: ['Geo'], thumbnail: thumb('deck_polygon'),
  },
  deck_screengrid: {
    label: 'Deck.gl Screen Grid', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_SCREENGRID_EN, descriptionZh: D_DECK_SCREENGRID_ZH, tags: ['Geo'], thumbnail: thumb('deck_screengrid'),
  },
  deck_heatmap: {
    label: 'Deck.gl Heatmap', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_HEATMAP_EN, descriptionZh: D_DECK_HEATMAP_ZH, tags: ['Geo'], thumbnail: thumb('deck_screengrid'),
  },
  world_map: {
    label: 'World Map', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_WORLD_MAP_EN, descriptionZh: D_WORLD_MAP_ZH, tags: ['Geo'], thumbnail: thumb('world_map'),
  },
  country_map: {
    label: 'Country Map', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_COUNTRY_MAP_EN, descriptionZh: D_COUNTRY_MAP_ZH, tags: ['Geo'], thumbnail: thumb('country_map'),
  },
  mapbox: {
    label: 'MapBox', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_MAPBOX_EN, descriptionZh: D_MAPBOX_ZH, tags: ['Geo'], thumbnail: thumb('mapbox'),
  },

  /* ----------------------------------------------------------------- text */
  markdown: {
    label: 'Markdown', category: 'Text', color: TEXT_COLOR, renderer: MarkdownRenderer, allowEmpty: true,
    descriptionEn: D_MARKDOWN_EN, descriptionZh: D_MARKDOWN_ZH, tags: ['Text'], thumbnail: thumb('markup'),
  },
  header: {
    label: 'Header', category: 'Text', color: TEXT_COLOR, renderer: HeaderRenderer, allowEmpty: true,
    descriptionEn: D_HEADER_EN, descriptionZh: D_HEADER_ZH, tags: ['Text'], thumbnail: thumb('markup'),
  },
  separator: {
    label: 'Separator', category: 'Text', color: TEXT_COLOR, renderer: SeparatorRenderer, allowEmpty: true,
    descriptionEn: D_SEPARATOR_EN, descriptionZh: D_SEPARATOR_ZH, tags: ['Text'], thumbnail: thumb('separator'),
  },
  iframe: {
    label: 'iFrame', category: 'Text', color: TEXT_COLOR, renderer: IframeRenderer, allowEmpty: true,
    descriptionEn: D_IFRAME_EN, descriptionZh: D_IFRAME_ZH, tags: ['Text'], thumbnail: thumb('iframe'),
  },
  handlebars: {
    label: 'Handlebars', category: 'Text', color: TEXT_COLOR, renderer: HandlebarsRenderer, allowEmpty: true,
    descriptionEn: D_HANDLEBARS_EN, descriptionZh: D_HANDLEBARS_ZH, tags: ['Text'], thumbnail: thumb('markup'),
  },
  filter_box: {
    label: 'Filter Box', category: 'Text', color: TEXT_COLOR, renderer: FilterBoxRenderer, allowEmpty: true,
    descriptionEn: D_FILTER_BOX_EN, descriptionZh: D_FILTER_BOX_ZH, tags: ['Text'], thumbnail: thumb('filter_box'),
  },

  /* ------------------------------------------------- advanced / aliases */
  candlestick: {
    label: 'Candlestick Chart', category: 'Time series', color: TS_COLOR, renderer: CandlestickRenderer,
    descriptionEn: D_CANDLE_EN, descriptionZh: D_CANDLE_ZH, tags: ['ECharts', 'Time Series', ADVANCED_TAG],
    badge: BADGE_ADVANCED,
  },
  gantt_chart: {
    label: 'Gantt Chart', category: 'Time series', color: TS_COLOR, renderer: GanttRenderer,
    descriptionEn: D_GANTT_EN, descriptionZh: D_GANTT_ZH, tags: [ADVANCED_TAG], badge: BADGE_ADVANCED,
  },
  horizon: {
    label: 'Horizon Chart', category: 'Time series', color: TS_COLOR, renderer: HorizonRenderer,
    descriptionEn: D_HORIZON_EN, descriptionZh: D_HORIZON_ZH, tags: ['ECharts', 'Time Series', ADVANCED_TAG],
    badge: BADGE_ADVANCED, thumbnail: thumb('horizon'),
  },
  para: {
    label: 'Parallel Coordinates', category: 'Flow & relation', color: FLOW_COLOR, renderer: ParallelRenderer,
    descriptionEn: D_PARA_EN, descriptionZh: D_PARA_ZH, tags: ['ECharts', ADVANCED_TAG], badge: BADGE_ADVANCED,
    thumbnail: thumb('para'),
  },
  butterfly: {
    label: 'Butterfly Chart', category: 'Numeric', color: NUM_COLOR, renderer: ButterflyRenderer,
    descriptionEn: D_BUTTERFLY_EN, descriptionZh: D_BUTTERFLY_ZH, tags: ['ECharts', 'Comparison', ADVANCED_TAG],
    badge: BADGE_ADVANCED, thumbnail: thumb('paired_ttest'),
  },
  tree_chart: {
    label: 'Tree Chart', category: 'Flow & relation', color: FLOW_COLOR, renderer: TreeRenderer,
    descriptionEn: D_TREE_EN, descriptionZh: D_TREE_ZH, tags: ['ECharts', 'Hierarchy', ADVANCED_TAG],
    badge: BADGE_ADVANCED,
  },
  echarts_area: {
    label: 'Area Chart', category: 'Time series', color: TS_COLOR, renderer: TimeseriesArea,
    descriptionEn: D_AREA_EN, descriptionZh: D_AREA_ZH, tags: ['ECharts', 'Time Series'], thumbnail: thumb('area'),
  },
  cal_heatmap: {
    label: 'Calendar Heatmap', category: 'Matrix', color: MAT_COLOR, renderer: CalendarRenderer,
    descriptionEn: D_CAL_HEATMAP_EN, descriptionZh: D_CAL_HEATMAP_ZH, tags: ['Time Series'], thumbnail: thumb('cal_heatmap'),
  },
  heatmap_v2: {
    label: 'Heatmap', category: 'Matrix', color: MAT_COLOR, renderer: HeatmapRenderer,
    descriptionEn: D_HEATMAP_EN, descriptionZh: D_HEATMAP_ZH, tags: ['ECharts', 'Distribution'], thumbnail: thumb('heatmap'),
  },
  histogram_v2: {
    label: 'Histogram', category: 'Matrix', color: MAT_COLOR, renderer: HistogramRenderer,
    descriptionEn: D_HISTOGRAM_EN, descriptionZh: D_HISTOGRAM_ZH, tags: ['ECharts', 'Distribution'], thumbnail: thumb('histogram'),
  },
  sankey_v2: {
    label: 'Sankey Diagram', category: 'Flow & relation', color: FLOW_COLOR, renderer: SankeyRenderer,
    descriptionEn: D_SANKEY_EN, descriptionZh: D_SANKEY_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('sankey'),
  },
  sunburst_v2: {
    label: 'Sunburst Chart', category: 'Categorical', color: CAT_COLOR, renderer: SunburstRenderer,
    descriptionEn: D_SUNBURST_EN, descriptionZh: D_SUNBURST_ZH, tags: ['ECharts', 'Hierarchy'], thumbnail: thumb('sunburst'),
  },
  treemap_v2: {
    label: 'Treemap', category: 'Categorical', color: CAT_COLOR, renderer: TreemapRenderer,
    descriptionEn: D_TREEMAP_EN, descriptionZh: D_TREEMAP_ZH, tags: ['ECharts', 'Hierarchy'], thumbnail: thumb('treemap'),
  },
  pivot_table_v2: {
    label: 'Pivot Table', category: 'Matrix', color: MAT_COLOR, renderer: PivotRenderer,
    descriptionEn: D_PIVOT_EN, descriptionZh: D_PIVOT_ZH, tags: ['Tabular'], thumbnail: thumb('pivot_table'),
  },
  bubble_v2: {
    label: 'Bubble Chart', category: 'Time series', color: TS_COLOR, renderer: BubbleRenderer,
    descriptionEn: D_BUBBLE_EN, descriptionZh: D_BUBBLE_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('bubble'),
  },
  gauge_chart: {
    label: 'Gauge Chart', category: 'Numeric', color: NUM_COLOR, renderer: GaugeRenderer,
    descriptionEn: D_GAUGE_EN, descriptionZh: D_GAUGE_ZH, tags: ['ECharts', 'KPI'], thumbnail: thumb('bullet'),
  },
  graph_chart: {
    label: 'Graph Chart', category: 'Flow & relation', color: FLOW_COLOR, renderer: GraphRenderer,
    descriptionEn: D_GRAPH_EN, descriptionZh: D_GRAPH_ZH, tags: ['ECharts', 'Relationship'], thumbnail: thumb('directed_force'),
  },
  paired_ttest: {
    label: 'Paired t-test Table', category: 'Matrix', color: MAT_COLOR, renderer: TableRenderer,
    descriptionEn: 'A table summarising a paired t-test between two groups.',
    descriptionZh: '汇总两组配对 t 检验结果的表格。',
    tags: ['Tabular', 'Distribution'], thumbnail: thumb('paired_ttest'),
  },
  pop_kpi: {
    label: 'Period over Period KPI', category: 'Numeric', color: NUM_COLOR, renderer: BigNumberTotalRenderer,
    descriptionEn: 'A KPI compared against the previous period, with the delta called out.',
    descriptionZh: '与上一周期对比的 KPI，突出显示变化幅度。',
    tags: ['KPI'], thumbnail: thumb('big_number_total'),
  },
  point_cluster_map: {
    label: 'Point Cluster Map', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_DECK_SCATTER_EN, descriptionZh: D_DECK_SCATTER_ZH, tags: ['Geo'], thumbnail: thumb('deck_scatter'),
  },
  cartodiagram: {
    label: 'Cartodiagram', category: 'Geo', color: GEO_COLOR, renderer: GeoRenderer,
    descriptionEn: D_COUNTRY_MAP_EN, descriptionZh: D_COUNTRY_MAP_ZH, tags: ['Geo'], thumbnail: thumb('country_map'),
  },
};

export function getVizDefinition(vizType: string): VizDefinition | undefined {
  return REGISTRY[vizType];
}

export const VIZ_TYPE_COUNT = Object.keys(REGISTRY).length;
