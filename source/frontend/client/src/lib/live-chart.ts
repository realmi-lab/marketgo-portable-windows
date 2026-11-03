export type LiveChartHistoryPoint = {
  at: number;
  gap?: 1;
  partial?: 1;
  [metric: string]: number | null | undefined;
};

type LiveTickExchangeCoverage = {
  ws_realtime_complete: boolean;
  readiness?: {
    ready_exchanges?: string[];
  };
};

/**
 * A degraded five-venue frame can still contain directly observed values for
 * the healthy venues. The aggregate transport flag must not hide those rows;
 * the readiness snapshot identifies which exchanges are actually connected.
 */
export function liveTickExchangeObserved(
  tick: LiveTickExchangeCoverage,
  exchange: string
): boolean {
  return (
    tick.ws_realtime_complete ||
    tick.readiness?.ready_exchanges?.includes(exchange) === true
  );
}

const LIVE_HISTORY_MS = 300_000;
const LIVE_FULL_RESOLUTION_MS = 30_000;

export function compactLiveHistory(
  history: LiveChartHistoryPoint[],
  latestAt: number
): LiveChartHistoryPoint[] {
  const cutoff = latestAt - LIVE_HISTORY_MS;
  const fullResolutionCutoff = latestAt - LIVE_FULL_RESOLUTION_MS;
  const visible = history.filter(row => row.at >= cutoff);
  const compacted: LiveChartHistoryPoint[] = [];
  let previousBucket = -1;

  for (const row of visible) {
    if (row.gap) {
      compacted.push(row);
      previousBucket = -1;
      continue;
    }
    if (row.at >= fullResolutionCutoff) {
      compacted.push(row);
      continue;
    }
    const bucket = Math.floor(row.at / 1_000);
    if (bucket === previousBucket) {
      compacted[compacted.length - 1] = row;
      continue;
    }
    compacted.push(row);
    previousBucket = bucket;
  }
  return compacted;
}

export function appendLiveHistoryPoint(
  history: LiveChartHistoryPoint[],
  point: LiveChartHistoryPoint
): LiveChartHistoryPoint[] {
  if (!Number.isFinite(point.at)) return history;
  const latest = history.at(-1);
  if (!latest) return [point];
  if (point.at < latest.at) return history;
  if (point.at === latest.at) {
    return compactLiveHistory([...history.slice(0, -1), point], point.at);
  }

  // A connected WebSocket can pause briefly while the all-asset analysis is
  // calculated. That is a scheduling delay, not evidence that the market feed
  // stopped. The connection hook clears the live history when transport goes
  // offline, so timestamp distance alone must not manufacture a visual gap.
  return compactLiveHistory([...history, point], point.at);
}

export function appendLiveHistoryGap(
  history: LiveChartHistoryPoint[]
): LiveChartHistoryPoint[] {
  const latest = history.at(-1);
  if (!latest || latest.gap) return history;
  const gap = { at: latest.at + 0.001, gap: 1 as const };
  return compactLiveHistory([...history, gap], gap.at);
}

/** The smallest 1, 2, 2.5 or 5 times a power of ten at or above `value`. */
function niceCeil(value: number): number {
  const power = 10 ** Math.floor(Math.log10(value));
  const step = [1, 2, 2.5, 5, 10].find(step => step * power >= value) ?? 10;
  return step * power;
}

/**
 * The value range a live chart's height spans. It moves only when the values
 * leave it or shrink well inside it, so the scale does not jump with every
 * second. `zoom` fits a share range tightly; otherwise it starts at 0.
 */
export function stableDomain(
  previous: readonly [number, number] | null,
  values: readonly number[],
  zoom: boolean
): [number, number] {
  if (values.length === 0)
    return previous ? [...previous] : zoom ? [0, 100] : [0, 1];
  const low = Math.min(...values);
  const high = Math.max(...values);
  if (!zoom) {
    if (high <= 0) return previous ? [...previous] : [0, 1];
    if (previous && high <= previous[1] && high >= previous[1] * 0.4) {
      return [...previous];
    }
    return [0, niceCeil(high * 1.1)];
  }
  const spread = high - low;
  if (
    previous &&
    low >= previous[0] &&
    high <= previous[1] &&
    spread >= (previous[1] - previous[0]) * 0.3
  ) {
    return [...previous];
  }
  const pad = Math.max(0.5, spread * 0.1);
  const step = spread <= 5 ? 1 : spread <= 20 ? 2 : 5;
  const bottom = Math.max(0, Math.floor((low - pad) / step) * step);
  const top = Math.min(100, Math.ceil((high + pad) / step) * step);
  return bottom < top ? [bottom, top] : [Math.max(0, top - 1), top];
}

/**
 * The history with the recent seconds read when the chart opened put before
 * it: only those older than its first point, so the live points stay as
 * they came.
 */
export function seedLiveHistory(
  history: LiveChartHistoryPoint[],
  seeds: readonly LiveChartHistoryPoint[]
): LiveChartHistoryPoint[] {
  const first = history[0]?.at ?? Infinity;
  const older = seeds.filter(point => point.at < first);
  if (older.length === 0) return history;
  const merged = [...older, ...history];
  return compactLiveHistory(merged, merged[merged.length - 1].at);
}
