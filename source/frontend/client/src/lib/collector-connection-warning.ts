import type { MarketShareReadiness } from "./market-share-types.ts";

export const COLLECTOR_WARNING_GRACE_MS = 30_000;
const GRACE_EXCHANGES = ["gopax", "korbit", "coinone"] as const;
export type CollectorWarningState = Record<
  string,
  { ready: boolean; disconnectedAt: number | null }
>;

/** Presentation only: never change readiness, coverage, or calculated values. */
export function advanceCollectorWarnings(
  previous: CollectorWarningState,
  readiness: MarketShareReadiness | null,
  nowMS: number
): CollectorWarningState {
  if (!readiness) return previous;
  const next = { ...previous };
  const eligible =
    (readiness.phase === "CONNECTING" || readiness.phase === "OBSERVING") &&
    readiness.aggregator_consumer_alive &&
    readiness.configured_exchange_count === readiness.enabled_exchange_count &&
    readiness.configured_exchange_count > 0 &&
    (readiness.active_trade_parse_error_chunks ?? 0) === 0;
  for (const exchange of GRACE_EXCHANGES) {
    if (readiness.ready_exchanges?.includes(exchange)) {
      next[exchange] = { ready: true, disconnectedAt: null };
    } else if (readiness.blocking_exchanges?.includes(exchange)) {
      const old = previous[exchange];
      next[exchange] = {
        ready: false,
        // An unknown initial state or a real processing error gets no grace.
        disconnectedAt: eligible
          ? old?.ready
            ? nowMS
            : (old?.disconnectedAt ?? null)
          : null,
      };
    } else {
      delete next[exchange];
    }
  }
  return next;
}

export function deferredCollectorWarnings(
  state: CollectorWarningState,
  nowMS: number
): string[] {
  return GRACE_EXCHANGES.filter(exchange => {
    const since = state[exchange]?.disconnectedAt;
    return (
      since != null &&
      nowMS >= since &&
      nowMS - since < COLLECTOR_WARNING_GRACE_MS
    );
  });
}

export function collectorConnectionTone(
  readiness: MarketShareReadiness | null,
  deferred: readonly string[]
): "ok" | "muted" | "warn" {
  if (!readiness) return "warn";
  if (!Array.isArray(readiness.blocking_exchanges)) return "warn";
  if (!readiness.blocking_exchanges.length) return "ok";
  return readiness.blocking_exchanges.every(exchange =>
    deferred.includes(exchange)
  )
    ? "muted"
    : "warn";
}
