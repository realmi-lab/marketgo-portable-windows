import { DOMESTIC_EXCHANGES } from "./market-share-config.ts";
import {
  liveTickExchangeObserved,
  type LiveChartHistoryPoint,
} from "./live-chart.ts";
import type {
  MarketScope,
  MarketShareClosedSecond,
  MarketShareRealtimeTick,
  MarketShareTickExchange,
} from "./market-share-types.ts";

const usable = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value) && value >= 0;

function closedSecondAt(closed: MarketShareClosedSecond): number | null {
  const second = closed?.as_of_second;
  return Number.isSafeInteger(second) &&
    second >= 0 &&
    Number.isSafeInteger((second + 1) * 1000)
    ? (second + 1) * 1000
    : null;
}

/**
 * A chart point at `at` from a window's rows: each observed exchange's share
 * as sent, and its trading and trades per observed second. Zero turnover has
 * zero speed but no share; an exchange not observed has neither.
 */
function windowPoint(
  at: number,
  rows: readonly MarketShareTickExchange[],
  observedSeconds: number,
  observed: (exchange: string) => boolean,
  shareAvailable: boolean
): LiveChartHistoryPoint {
  const byExchange = new Map(
    rows
      .filter(row => row && typeof row.exchange === "string")
      .map(row => [row.exchange, row])
  );
  const point: LiveChartHistoryPoint = { at };
  for (const exchange of DOMESTIC_EXCHANGES) {
    const row = byExchange.get(exchange);
    const seen = observed(exchange);
    const share = row?.share_pct;
    point[`share_${exchange}`] =
      seen && shareAvailable && usable(share) && share <= 100 ? share : null;
    point[`rate_${exchange}`] =
      seen && usable(row?.volume_krw) ? row.volume_krw / observedSeconds : null;
    point[`trades_${exchange}`] =
      seen && usable(row?.trade_count)
        ? row.trade_count / observedSeconds
        : null;
  }
  return point;
}

/**
 * The live chart's point for a tick: its window ending at the last closed
 * second, timed at that second's end, or (from a calculator without it) the
 * tick's own window, which includes the open second. Only observed values.
 */
export function buildLiveOverviewPoint(
  tick: MarketShareRealtimeTick | null,
  marketScope: MarketScope,
  requestedWindowSec: number
): LiveChartHistoryPoint | null {
  if (
    !tick ||
    tick.market_scope !== marketScope ||
    tick.window_sec !== requestedWindowSec ||
    !Number.isFinite(tick.generated_at) ||
    tick.generated_at <= 0 ||
    !Number.isFinite(requestedWindowSec) ||
    requestedWindowSec <= 0
  )
    return null;
  const observed = (exchange: string) =>
    liveTickExchangeObserved(tick, exchange);
  if (tick.closed_second) {
    const at = closedSecondAt(tick.closed_second);
    if (at === null) return null;
    const point = closedSecondPoint(
      tick.closed_second,
      requestedWindowSec,
      observed
    );
    if (point && !tick.ws_realtime_complete) point.partial = 1;
    return point ?? { at, gap: 1 };
  }
  if (
    !usable(tick.coverage_sec) ||
    tick.coverage_sec === 0 ||
    !Array.isArray(tick.exchanges)
  )
    return null;
  const at = tick.generated_at * 1000;
  const total = tick.summary?.domestic_volume_krw;
  if (!Number.isFinite(at) || !usable(total)) return null;
  const point = windowPoint(
    at,
    tick.exchanges,
    Math.min(requestedWindowSec, tick.coverage_sec),
    observed,
    total > 0
  );
  if (tick.window_complete === false || !tick.ws_realtime_complete)
    point.partial = 1;
  return point;
}

/**
 * The point of a window ending at a closed second, timed at the second's
 * end, or null when none of it was observed. A recent second read on its own
 * (closed-seconds) counts every exchange its coverage holds as observed.
 */
export function closedSecondPoint(
  closed: MarketShareClosedSecond,
  windowSec: number,
  observed: (exchange: string) => boolean = () => true
): LiveChartHistoryPoint | null {
  const at = closedSecondAt(closed);
  if (
    at === null ||
    !Number.isFinite(windowSec) ||
    windowSec <= 0 ||
    !Number.isFinite(closed.coverage_sec) ||
    closed.coverage_sec <= 0 ||
    !Array.isArray(closed.exchanges)
  )
    return null;
  const point = windowPoint(
    at,
    closed.exchanges,
    Math.min(windowSec, closed.coverage_sec),
    observed,
    true
  );
  if (!closed.window_complete) point.partial = 1;
  return point;
}

/** The recent closed windows, including breaks where no part was observed. */
export function buildClosedSecondsHistory(
  seconds: readonly MarketShareClosedSecond[],
  windowSec: number
): LiveChartHistoryPoint[] {
  if (!Array.isArray(seconds) || !Number.isFinite(windowSec) || windowSec <= 0)
    return [];
  return seconds.flatMap(closed => {
    const at = closedSecondAt(closed);
    return at === null
      ? []
      : [closedSecondPoint(closed, windowSec) ?? { at, gap: 1 as const }];
  });
}
