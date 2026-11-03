import type {
  MarketShareAnalysis,
  MarketShareRealtimeTick,
  MarketShareSocketPayload,
} from "./market-share-types";

// Go encodes a nil slice as JSON null. The UI treats these contract fields as
// collections, so normalize them once at the WebSocket boundary instead of
// scattering optional chaining across every table and chart.
const MARKET_SHARE_ARRAY_FIELDS = new Set([
  "affected_assets",
  "affected_exchanges",
  "affected_markets",
  "asset_changes",
  "asset_drivers",
  "asset_snapshots",
  "assets",
  "asset_exchange_shares",
  "average_buy_asset_slippage_scenarios",
  "average_sell_asset_slippage_scenarios",
  "basis",
  "blocking_exchanges",
  "bridges",
  "checks",
  "comparable_exchanges",
  "comparisons",
  "conflict_signals",
  "contextual_summary",
  "current_assets",
  "diagnostic_summary",
  "disabled_orderbook_exchanges",
  "disabled_ws_exchanges",
  "domestic_exchanges",
  "domestic_notices",
  "drivers",
  "edges",
  "enabled_ws_exchanges",
  "error_history",
  "errors",
  "events",
  "evidence_reasons",
  "exchange_comparison",
  "exchange_drivers",
  "exchange_impacts",
  "exchange_reactions",
  "exchanges",
  "flow_evidence",
  "findings",
  "global_exchanges",
  "global_reference_exchanges",
  "horizons",
  "image_analysis",
  "image_urls",
  "included_exchanges",
  "issues",
  "items",
  "leading_assets",
  "lines",
  "linked_assets",
  "listing_segments",
  "major_assets",
  "market_comparison",
  "market_context",
  "matched_assets",
  "matched_signals",
  "measured_summary",
  "microstructure",
  "momentum",
  "negative_assets",
  "notices",
  "nodes",
  "notice_source_errors",
  "observation_gaps",
  "observed_asset_drivers",
  "observed_exchange_drivers",
  "orderbook_health_by_exchange",
  "path",
  "positive_assets",
  "quarantined_assets",
  "quote_comparison",
  "quote_markets",
  "quotes",
  "reaction_studies",
  "ready_exchanges",
  "reason_codes",
  "recent_events",
  "recovered_gap_ids",
  "registry_errors",
  "related_event_ids",
  "rest_usage",
  "risk_tags",
  "schedules",
  "semantic_types",
  "share_bridge",
  "signals",
  "timeline",
  "timeline_schedules",
  "trend",
  "unhealthy_collectors",
  "unresolved_quote_assets",
  "windows",
  "ws_enabled_exchanges",
]);

function normalizeNilArrayFields(value: unknown, field = ""): unknown {
  if (value === null) return MARKET_SHARE_ARRAY_FIELDS.has(field) ? [] : value;
  if (Array.isArray(value)) {
    let normalized: unknown[] | null = null;
    for (let index = 0; index < value.length; index += 1) {
      const item = normalizeNilArrayFields(value[index]);
      if (item === value[index]) continue;
      normalized ??= value.slice();
      normalized[index] = item;
    }
    return normalized ?? value;
  }
  if (!value || typeof value !== "object") return value;

  const source = value as Record<string, unknown>;
  let normalized: Record<string, unknown> | null = null;
  for (const [key, item] of Object.entries(source)) {
    const next = normalizeNilArrayFields(item, key);
    if (next === item) continue;
    normalized ??= { ...source };
    normalized[key] = next;
  }
  return normalized ?? value;
}

export function normalizeMarketShareCollections<T>(payload: T): T {
  return normalizeNilArrayFields(payload) as T;
}

export type KeyedMarketSharePayload = {
  requestKey: string;
  payload: MarketShareSocketPayload;
};

const MARKET_SHARE_PAYLOAD_CACHE_LIMIT = 8;
const MARKET_SHARE_PAYLOAD_STORAGE_KEY = "market-share.payload-cache.v1";
const MARKET_SHARE_PAYLOAD_STORAGE_VERSION = 1;

type MarketSharePayloadStorage = Pick<Storage, "getItem" | "setItem">;

type StoredMarketSharePayloadCache = {
  version: typeof MARKET_SHARE_PAYLOAD_STORAGE_VERSION;
  entries: Array<{
    request_key: string;
    payload: MarketShareSocketPayload;
  }>;
};

const latestPayloadByRequest = new Map<string, MarketShareSocketPayload>();
let storageHydrated = false;
let storageDisabled = false;
let storageDirty = false;
let pagehideFlushInstalled = false;
let testStorageOverride: MarketSharePayloadStorage | null | undefined;

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isStoredMarketSharePayload(
  value: unknown
): value is MarketShareSocketPayload {
  return (
    isRecord(value) &&
    typeof value.version === "number" &&
    Number.isFinite(value.version) &&
    typeof value.generated_at === "number" &&
    Number.isFinite(value.generated_at) &&
    typeof value.running === "boolean" &&
    isRecord(value.analysis) &&
    isRecord(value.analysis.context)
  );
}

function marketSharePayloadStorage(): MarketSharePayloadStorage | null {
  if (storageDisabled) return null;
  if (testStorageOverride !== undefined) return testStorageOverride;
  try {
    if (!("sessionStorage" in globalThis)) return null;
    const storage = globalThis.sessionStorage;
    return storage &&
      typeof storage.getItem === "function" &&
      typeof storage.setItem === "function"
      ? storage
      : null;
  } catch {
    storageDisabled = true;
    return null;
  }
}

function trimMarketSharePayloadCache(): void {
  while (latestPayloadByRequest.size > MARKET_SHARE_PAYLOAD_CACHE_LIMIT) {
    const oldestKey = latestPayloadByRequest.keys().next().value;
    if (typeof oldestKey !== "string") break;
    latestPayloadByRequest.delete(oldestKey);
  }
}

function hydrateMarketSharePayloadCache(): void {
  if (storageHydrated) return;
  const storage = marketSharePayloadStorage();
  if (!storage) return;
  storageHydrated = true;

  let raw: string | null;
  try {
    raw = storage.getItem(MARKET_SHARE_PAYLOAD_STORAGE_KEY);
  } catch {
    storageDisabled = true;
    return;
  }
  if (!raw) return;

  try {
    const stored = JSON.parse(raw) as Partial<StoredMarketSharePayloadCache>;
    if (
      stored.version !== MARKET_SHARE_PAYLOAD_STORAGE_VERSION ||
      !Array.isArray(stored.entries)
    ) {
      return;
    }
    for (const entry of stored.entries) {
      if (
        !isRecord(entry) ||
        typeof entry.request_key !== "string" ||
        entry.request_key.length === 0 ||
        !isStoredMarketSharePayload(entry.payload)
      ) {
        continue;
      }
      // A live in-memory value is always newer than its session fallback.
      if (latestPayloadByRequest.has(entry.request_key)) continue;
      latestPayloadByRequest.set(
        entry.request_key,
        normalizeMarketShareCollections(entry.payload)
      );
    }
    trimMarketSharePayloadCache();
  } catch {
    // A stale or manually edited session entry must never block live startup.
  }
}

function storedMarketSharePayloadCache(): StoredMarketSharePayloadCache {
  return {
    version: MARKET_SHARE_PAYLOAD_STORAGE_VERSION,
    entries: [...latestPayloadByRequest].map(([requestKey, payload]) => ({
      request_key: requestKey,
      payload,
    })),
  };
}

function persistMarketSharePayloadCache(): void {
  if (!storageDirty) return;
  const storage = marketSharePayloadStorage();
  if (!storage) return;
  try {
    storage.setItem(
      MARKET_SHARE_PAYLOAD_STORAGE_KEY,
      JSON.stringify(storedMarketSharePayloadCache())
    );
    storageDirty = false;
  } catch {
    // Quota and storage-policy failures degrade to the existing memory cache.
    storageDisabled = true;
    storageDirty = false;
  }
}

/**
 * The session copy is written only when the page is hidden or left, which is
 * before any reload: writing every few seconds serialized megabytes of
 * payloads on the main thread while the page was in use.
 */
function installPagehidePayloadFlush(): void {
  if (pagehideFlushInstalled || typeof window === "undefined") return;
  try {
    window.addEventListener("pagehide", persistMarketSharePayloadCache);
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden")
        persistMarketSharePayloadCache();
    });
    pagehideFlushInstalled = true;
  } catch {
    // Non-browser runtimes and restricted DOM shims keep the memory cache only.
  }
}

function scheduleMarketSharePayloadPersistence(): void {
  storageDirty = true;
  if (marketSharePayloadStorage()) installPagehidePayloadFlush();
}

export function cachedMarketSharePayload(
  requestKey: string
): MarketShareSocketPayload | null {
  hydrateMarketSharePayloadCache();
  return latestPayloadByRequest.get(requestKey) ?? null;
}

export function rememberMarketSharePayload(
  requestKey: string,
  payload: MarketShareSocketPayload
): MarketShareSocketPayload {
  hydrateMarketSharePayloadCache();
  latestPayloadByRequest.delete(requestKey);
  latestPayloadByRequest.set(requestKey, payload);
  trimMarketSharePayloadCache();
  scheduleMarketSharePayloadPersistence();
  return payload;
}

/** Test-only state reset; session contents are deliberately left intact. */
export function __resetMarketSharePayloadCacheForTests(
  storage?: MarketSharePayloadStorage | null
): void {
  latestPayloadByRequest.clear();
  storageHydrated = false;
  storageDisabled = false;
  storageDirty = false;
  testStorageOverride = storage;
}

/** Test-only equivalent of the browser pagehide flush. */
export function __flushMarketSharePayloadCacheForTests(): void {
  persistMarketSharePayloadCache();
}

export function mergeMarketSharePayload(
  current: KeyedMarketSharePayload | null,
  requestKey: string,
  incoming: MarketShareSocketPayload
): KeyedMarketSharePayload | null {
  const currentPayload =
    current?.requestKey === requestKey ? current.payload : null;
  if (incoming.partial_analysis && !currentPayload) return null;

  const payload =
    incoming.partial_analysis && currentPayload
      ? {
          ...incoming,
          analysis: {
            ...currentPayload.analysis,
            ...incoming.analysis,
          },
        }
      : incoming;
  return { requestKey, payload };
}

export type MarketShareTickReferences = Partial<
  Pick<
    MarketShareAnalysis,
    | "official_domestic_24h"
    | "official_ticker_1m"
    | "official_binance_24h"
    | "realtime_flow"
  >
>;

export function marketShareTickReferences(
  analysis: MarketShareAnalysis
): MarketShareTickReferences {
  const references: MarketShareTickReferences = {};
  for (const field of [
    "official_domestic_24h",
    "official_ticker_1m",
    "official_binance_24h",
    "realtime_flow",
  ] as const) {
    // Partial/projection frames omit fields they do not update. Explicit null
    // still clears an unavailable reference; omission must preserve it.
    if (analysis[field] !== undefined)
      Object.assign(references, { [field]: analysis[field] });
  }
  return references;
}

export function mergeMarketShareTickReferences(
  tick: MarketShareRealtimeTick,
  references: MarketShareTickReferences | null
): MarketShareRealtimeTick {
  // A legacy full tick remains compatible: explicit realtime fields win over
  // the slower reference snapshot when both are present.
  return references ? { ...references, ...tick } : tick;
}
