import type { LiveChartHistoryPoint } from "./live-chart.ts";

export type ChartRange = readonly [number, number];

/** Viewport coordinates only. Never aggregate or interpolate market values. */
export function liveChartRange(
  rows: readonly LiveChartHistoryPoint[],
  durationMS: number | null,
  heldRange: ChartRange | null = null
): ChartRange {
  if (!rows.length) return [0, 0];
  const end = rows.length - 1;
  if (heldRange) {
    const first = rows.findIndex(row => row.at >= heldRange[0]);
    const last = rows.findLastIndex(row => row.at <= heldRange[1]);
    return normalizeChartRange(rows.length, first < 0 ? end : first, last);
  }
  const cutoff = durationMS === null ? -Infinity : rows[end].at - durationMS;
  const first = rows.findIndex(row => row.at >= cutoff);
  return normalizeChartRange(rows.length, first < 0 ? end : first, end);
}

export function normalizeChartRange(
  count: number,
  first: number,
  last: number
): ChartRange {
  if (count < 2) return [0, 0];
  const start = Math.min(
    count - 2,
    Math.max(0, Number.isFinite(first) ? Math.trunc(first) : 0)
  );
  const end = Math.min(
    count - 1,
    Math.max(start + 1, Number.isFinite(last) ? Math.trunc(last) : count - 1)
  );
  return [start, end];
}

/** A rolling/compacted stream must never silently select a different point. */
export function liveChartPointIndex(
  rows: readonly LiveChartHistoryPoint[],
  at: number | null
): number {
  return at === null ? -1 : rows.findIndex(row => row.at === at);
}

/** Avoid repeated second labels while the initial subsecond history grows. */
export function liveChartTicks(
  rows: readonly LiveChartHistoryPoint[],
  count: number
): number[] {
  const seconds = new Map<number, number>();
  for (const row of rows)
    if (Number.isFinite(row.at)) seconds.set(Math.floor(row.at / 1000), row.at);
  const candidates = [...seconds.values()];
  const limit = Math.max(2, Math.trunc(count));
  if (candidates.length <= limit) return candidates;
  return Array.from(
    { length: limit },
    (_, index) =>
      candidates[Math.round((index * (candidates.length - 1)) / (limit - 1))]
  );
}

export function liveChartTime(at: number | undefined): string {
  if (at === undefined || !Number.isFinite(at)) return "—";
  return new Date(at).toLocaleTimeString("ko-KR", {
    timeZone: "Asia/Seoul",
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}
