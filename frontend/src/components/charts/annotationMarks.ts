/**
 * Mapping of annotation events onto a categorical time axis.
 *
 * The chart x axis is made of bucket labels, so a time range has to be
 * translated back into the two buckets that bracket it. Positions are returned
 * as *category indexes*, not labels: labels are formatted (and can repeat, e.g.
 * two "00:00" buckets on a short range), and ECharts would collapse a band
 * addressed by duplicate labels to zero width.
 *
 * The helper is kept free of React and ECharts so the mapping can be reasoned
 * about on its own.
 */
import type { ChartAnnotation } from '../../api/types';
import { parseTime } from './utils';

/** Fill/stroke tones for annotation overlays, cycled per layer. */
export const ANNOTATION_TONES = [
  { fill: 'rgba(31, 168, 201, 0.16)', stroke: 'rgba(31, 168, 201, 0.55)' },
  { fill: 'rgba(255, 127, 68, 0.16)', stroke: 'rgba(255, 127, 68, 0.55)' },
  { fill: 'rgba(90, 193, 137, 0.18)', stroke: 'rgba(90, 193, 137, 0.6)' },
  { fill: 'rgba(168, 104, 183, 0.16)', stroke: 'rgba(168, 104, 183, 0.55)' },
  { fill: 'rgba(224, 67, 85, 0.16)', stroke: 'rgba(224, 67, 85, 0.55)' },
];

export type AnnotationTone = { fill: string; stroke: string };

export interface AnnotationSpan {
  /** Category index where the event starts. */
  fromIndex: number;
  /** Category index where the event ends (inclusive). */
  toIndex: number;
  name: string;
  tone: AnnotationTone;
}

export interface AnnotationMark {
  areas: AnnotationSpan[];
  /** Events without a duration: a vertical line at one bucket. */
  points: Array<{ index: number; name: string; tone: AnnotationTone }>;
}

const EMPTY: AnnotationMark = { areas: [], points: [] };

/**
 * Maps annotation events onto the chart's category axis.
 *
 * Events entirely outside the plotted window are dropped rather than clamped to
 * the edge: a marker pinned to the first bucket would claim an event happened
 * there when it did not.
 */
export function buildAnnotationMarks(
  xValues: string[],
  annotations: ChartAnnotation[],
): AnnotationMark {
  if (!annotations.length || !xValues.length) return EMPTY;

  const stamps = xValues.map(value => parseTime(value)?.valueOf() ?? Number.NaN);
  // A categorical axis has no time anchor: nothing to align the events with.
  if (stamps.some(stamp => Number.isNaN(stamp))) return EMPTY;

  const minStamp = Math.min(...stamps);
  const maxStamp = Math.max(...stamps);
  const nearest = (target: number): number => {
    let best = 0;
    let bestDistance = Number.POSITIVE_INFINITY;
    stamps.forEach((stamp, index) => {
      const distance = Math.abs(stamp - target);
      if (distance < bestDistance) {
        bestDistance = distance;
        best = index;
      }
    });
    return best;
  };
  // A bucket belongs to the range when its timestamp falls inside it, so both
  // edges round down: a range starting at 08:00 still covers that whole day's
  // bucket, and one ending at 23:59 does not spill into the next day.
  const floorIndex = (target: number): number => {
    let best = -1;
    let bestValue = Number.NEGATIVE_INFINITY;
    stamps.forEach((stamp, index) => {
      if (stamp <= target && stamp > bestValue) {
        best = index;
        bestValue = stamp;
      }
    });
    return best;
  };

  const layerTone = new Map<number, AnnotationTone>();
  const toneFor = (layerID: number): AnnotationTone => {
    const known = layerTone.get(layerID);
    if (known) return known;
    const tone = ANNOTATION_TONES[layerTone.size % ANNOTATION_TONES.length];
    layerTone.set(layerID, tone);
    return tone;
  };

  const out: AnnotationMark = { areas: [], points: [] };
  annotations.forEach(annotation => {
    const start = parseTime(annotation.start_dttm);
    const end = parseTime(annotation.end_dttm) ?? start;
    if (!start || !end) return;
    let startMs = start.valueOf();
    let endMs = Math.max(end.valueOf(), startMs);
    // Outside the plotted window, with a half-bucket tolerance so an event that
    // only touches the edge still shows up.
    const tolerance = stamps.length > 1 ? Math.abs(stamps[1] - stamps[0]) / 2 : 0;
    if (endMs < minStamp - tolerance || startMs > maxStamp + tolerance) return;
    startMs = Math.max(startMs, minStamp);
    // Clamping may not invert the interval: an event that starts after the last
    // bucket but still touches the window would otherwise come out negative and
    // be mistaken for a malformed event.
    endMs = Math.max(Math.min(endMs, maxStamp), startMs);

    const tone = toneFor(annotation.layer_id);
    const name = annotation.short_descr || annotation.layer;
    if (annotation.end_dttm && endMs > startMs) {
      let fromIndex = floorIndex(startMs);
      let toIndex = floorIndex(endMs);
      // An event shorter than one bucket still deserves a band on the bucket it
      // falls into, rather than disappearing.
      if (fromIndex < 0) fromIndex = nearest(startMs);
      if (toIndex < 0) toIndex = nearest(endMs);
      // The pair is ordered by *time*, not by position: the axis may run right
      // to left when the query returns its buckets descending.
      if (toIndex < fromIndex) {
        const swap = fromIndex;
        fromIndex = toIndex;
        toIndex = swap;
      }
      // Both edges landing in the same bucket would paint a zero-width band,
      // which is invisible. Cover one bucket instead, preferring the forward
      // neighbour and falling back to the previous one at the right edge.
      if (toIndex === fromIndex) {
        if (toIndex + 1 < stamps.length) toIndex += 1;
        else if (fromIndex > 0) fromIndex -= 1;
      }
      out.areas.push({ fromIndex, toIndex, name, tone });
    } else {
      out.points.push({ index: nearest(startMs), name, tone });
    }
  });
  return out;
}
