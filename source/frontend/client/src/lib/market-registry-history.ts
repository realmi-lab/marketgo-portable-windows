import type {
  MarketRegistryHistoryResponse,
  MarketRegistryLifecycleEvent,
  MarketRegistryRefreshObservation,
} from "./market-share-types";

export const MARKET_REGISTRY_HISTORY_LIMIT = 200;

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object"
    ? (value as Record<string, unknown>)
    : null;
}

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function number(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function nullableNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function quoteCounts(value: unknown): Record<string, number> {
  const source = record(value);
  if (!source) return {};
  return Object.fromEntries(
    Object.entries(source)
      .filter(
        ([, count]) =>
          typeof count === "number" && Number.isFinite(count) && count >= 0
      )
      .map(([quote, count]) => [quote.toUpperCase(), number(count)])
  );
}

function refresh(value: unknown): MarketRegistryRefreshObservation | null {
  const source = record(value);
  if (!source) return null;
  const observedAtMS = number(source.observed_at_ms);
  const exchange = text(source.exchange).toLowerCase();
  if (observedAtMS <= 0 || !exchange) return null;
  return {
    observed_at_ms: observedAtMS,
    exchange,
    status: text(source.status).toUpperCase(),
    authoritative: source.authoritative === true,
    registry_version: number(source.registry_version),
    previous_market_count: number(source.previous_market_count),
    observed_market_count: number(source.observed_market_count),
    active_market_count: number(source.active_market_count),
    active_asset_count: number(source.active_asset_count),
    quote_counts: quoteCounts(source.quote_counts),
    public_fee_market_count: number(source.public_fee_market_count),
    discovered_count: number(source.discovered_count),
    added_count: number(source.added_count),
    removed_count: number(source.removed_count),
    error: text(source.error),
  };
}

function event(value: unknown): MarketRegistryLifecycleEvent | null {
  const source = record(value);
  if (!source) return null;
  const observedAtMS = number(source.observed_at_ms);
  const exchange = text(source.exchange).toLowerCase();
  const market = text(source.market).toUpperCase();
  if (observedAtMS <= 0 || !exchange || !market) return null;
  return {
    observed_at_ms: observedAtMS,
    exchange,
    market,
    base: text(source.base).toUpperCase(),
    quote: text(source.quote).toUpperCase(),
    scope: text(source.scope),
    event_type: text(source.event_type).toUpperCase(),
    initial_discovery: source.initial_discovery === true,
    maker_fee_rate: nullableNumber(source.maker_fee_rate),
    taker_fee_rate: nullableNumber(source.taker_fee_rate),
    fee_source: text(source.fee_source),
  };
}

export function normalizeMarketRegistryHistory(
  value: unknown
): MarketRegistryHistoryResponse {
  const source = record(value);
  const refreshes = Array.isArray(source?.refreshes)
    ? source.refreshes.map(refresh).filter(item => item !== null)
    : [];
  const events = Array.isArray(source?.events)
    ? source.events.map(event).filter(item => item !== null)
    : [];
  return {
    refreshes,
    events,
    generated_at: number(source?.generated_at),
  };
}

function eventIdentity(event: MarketRegistryLifecycleEvent): string {
  return `${event.observed_at_ms}:${event.exchange.toLowerCase()}:${event.market.toUpperCase()}`;
}

/** Excludes the initial registry baseline, including its paired ADDED rows. */
export function actualMarketRegistryEvents(
  events: MarketRegistryLifecycleEvent[]
): MarketRegistryLifecycleEvent[] {
  const baseline = new Set(
    events
      .filter(
        item => item.initial_discovery || item.event_type === "DISCOVERED"
      )
      .map(eventIdentity)
  );
  const seen = new Set<string>();
  return events
    .filter(item => {
      if (item.event_type !== "ADDED" && item.event_type !== "REMOVED")
        return false;
      if (item.initial_discovery) return false;
      if (item.event_type === "ADDED" && baseline.has(eventIdentity(item)))
        return false;
      const identity = `${eventIdentity(item)}:${item.event_type}`;
      if (seen.has(identity)) return false;
      seen.add(identity);
      return true;
    })
    .sort((left, right) => right.observed_at_ms - left.observed_at_ms);
}

export function latestMarketRegistryRefreshes(
  refreshes: MarketRegistryRefreshObservation[],
  exchanges: readonly string[]
): MarketRegistryRefreshObservation[] {
  const latest = new Map<string, MarketRegistryRefreshObservation>();
  for (const item of refreshes) {
    const exchange = item.exchange.toLowerCase();
    const current = latest.get(exchange);
    if (!current || item.observed_at_ms > current.observed_at_ms) {
      latest.set(exchange, item);
    }
  }
  return exchanges.flatMap(exchange => {
    const item = latest.get(exchange.toLowerCase());
    return item ? [item] : [];
  });
}

export function buildMarketRegistryHistoryPath(
  limit = MARKET_REGISTRY_HISTORY_LIMIT
) {
  const boundedLimit = Math.max(1, Math.min(200, Math.trunc(limit) || 1));
  return `/api/market-share/registry-history?limit=${boundedLimit}`;
}
