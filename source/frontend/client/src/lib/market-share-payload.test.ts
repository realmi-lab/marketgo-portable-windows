import assert from "node:assert/strict";
import test from "node:test";

import {
  __flushMarketSharePayloadCacheForTests,
  __resetMarketSharePayloadCacheForTests,
  cachedMarketSharePayload,
  marketShareTickReferences,
  mergeMarketSharePayload,
  mergeMarketShareTickReferences,
  normalizeMarketShareCollections,
  rememberMarketSharePayload,
} from "./market-share-payload.ts";
import type {
  MarketShareAnalysis,
  MarketShareRealtimeTick,
  MarketShareSocketPayload,
} from "./market-share-types.ts";

const payload = (
  version: number,
  partialAnalysis = false
): MarketShareSocketPayload =>
  ({
    version,
    generated_at: version,
    running: true,
    partial_analysis: partialAnalysis,
    analysis: { context: { analysis_version: version } },
  }) as MarketShareSocketPayload;

class MemorySessionStorage {
  readonly values = new Map<string, string>();
  writeCount = 0;

  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }

  setItem(key: string, value: string): void {
    this.writeCount += 1;
    this.values.set(key, value);
  }
}

test("does not merge a partial frame into a different request", () => {
  const current = { requestKey: "10:KRW", payload: payload(1) };
  assert.equal(
    mergeMarketSharePayload(current, "60:KRW", payload(2, true)),
    null
  );
});

test("merges a partial frame only into the matching request", () => {
  const current = { requestKey: "10:KRW", payload: payload(1) };
  const merged = mergeMarketSharePayload(current, "10:KRW", payload(2, true));
  assert.equal(merged?.requestKey, "10:KRW");
  assert.equal(merged?.payload.analysis.context.analysis_version, 2);
});

test("a complete frame replaces the previous request", () => {
  const current = { requestKey: "10:KRW", payload: payload(1) };
  const incoming = payload(2);
  const replaced = mergeMarketSharePayload(current, "60:KRW", incoming);
  assert.equal(replaced?.requestKey, "60:KRW");
  assert.equal(replaced?.payload, incoming);
});

test("restores only the last successful payload for the same request", () => {
  __resetMarketSharePayloadCacheForTests(null);
  const first = payload(11);
  const second = payload(12);
  rememberMarketSharePayload("test:cache:first", first);
  rememberMarketSharePayload("test:cache:second", second);

  assert.equal(cachedMarketSharePayload("test:cache:first"), first);
  assert.equal(cachedMarketSharePayload("test:cache:second"), second);
  assert.equal(cachedMarketSharePayload("test:cache:missing"), null);
});

test("restores the last successful payload from session storage after a hard refresh", () => {
  const storage = new MemorySessionStorage();
  __resetMarketSharePayloadCacheForTests(storage);
  rememberMarketSharePayload("86400:default:KRW:summary2", payload(21));
  // The page is left (pagehide) before it reloads.
  __flushMarketSharePayloadCacheForTests();
  assert.equal(storage.writeCount, 1);

  // Resetting module memory while retaining the same session storage models a
  // hard browser refresh and a fresh evaluation of this module.
  __resetMarketSharePayloadCacheForTests(storage);
  const restored = cachedMarketSharePayload("86400:default:KRW:summary2");
  assert.equal(restored?.version, 21);
  assert.equal(restored?.analysis.context.analysis_version, 21);
  assert.equal(storage.writeCount, 1);
});

test("writes the session copy only when the page is left, with the newest payload", () => {
  const storage = new MemorySessionStorage();
  const requestKey = "86400:default:KRW:summary2:throttle";
  __resetMarketSharePayloadCacheForTests(storage);

  rememberMarketSharePayload(requestKey, payload(31));
  rememberMarketSharePayload(requestKey, payload(32));
  rememberMarketSharePayload(requestKey, payload(33));
  assert.equal(storage.writeCount, 0);

  __flushMarketSharePayloadCacheForTests();
  __flushMarketSharePayloadCacheForTests();
  assert.equal(storage.writeCount, 1);
  __resetMarketSharePayloadCacheForTests(storage);
  assert.equal(cachedMarketSharePayload(requestKey)?.version, 33);
});

test("falls back quietly when session storage is unavailable or throws", () => {
  __resetMarketSharePayloadCacheForTests(null);
  const memoryOnly = payload(41);
  assert.doesNotThrow(() =>
    rememberMarketSharePayload("test:cache:ssr", memoryOnly)
  );
  assert.equal(cachedMarketSharePayload("test:cache:ssr"), memoryOnly);

  const readFailure = {
    getItem(): string | null {
      throw new Error("storage denied");
    },
    setItem(): void {
      throw new Error("storage denied");
    },
  };
  __resetMarketSharePayloadCacheForTests(readFailure);
  assert.doesNotThrow(() => cachedMarketSharePayload("test:cache:denied"));
  const afterReadFailure = payload(42);
  assert.doesNotThrow(() =>
    rememberMarketSharePayload("test:cache:denied", afterReadFailure)
  );
  assert.equal(cachedMarketSharePayload("test:cache:denied"), afterReadFailure);

  const writeFailure = {
    getItem(): string | null {
      return null;
    },
    setItem(): void {
      throw new Error("quota exceeded");
    },
  };
  __resetMarketSharePayloadCacheForTests(writeFailure);
  const afterWriteFailure = payload(43);
  assert.doesNotThrow(() =>
    rememberMarketSharePayload("test:cache:quota", afterWriteFailure)
  );
  assert.equal(cachedMarketSharePayload("test:cache:quota"), afterWriteFailure);
});

test("ignores a malformed session payload without blocking a later valid write", () => {
  const storage = new MemorySessionStorage();
  storage.values.set("market-share.payload-cache.v1", "{broken-json");
  __resetMarketSharePayloadCacheForTests(storage);

  assert.equal(cachedMarketSharePayload("test:cache:malformed"), null);
  rememberMarketSharePayload("test:cache:malformed", payload(51));
  __flushMarketSharePayloadCacheForTests();
  assert.equal(storage.writeCount, 1);
  __resetMarketSharePayloadCacheForTests(storage);
  assert.equal(cachedMarketSharePayload("test:cache:malformed")?.version, 51);
});

test("ignores stored entries that do not satisfy the payload contract", () => {
  const storage = new MemorySessionStorage();
  storage.values.set(
    "market-share.payload-cache.v1",
    JSON.stringify({
      version: 1,
      entries: [
        {
          request_key: "test:cache:invalid-shape",
          payload: { version: 52, analysis: {} },
        },
      ],
    })
  );
  __resetMarketSharePayloadCacheForTests(storage);

  assert.equal(cachedMarketSharePayload("test:cache:invalid-shape"), null);
});

test("normalizes Go nil slices before a page can call array methods", () => {
  const incoming = {
    asset_exchange_shares: [
      {
        asset: "BTC",
        exchanges: null,
      },
    ],
    analysis: {
      assets: null,
      expert_brief: {
        major_assets: null,
        positive_assets: null,
        attribution_headline: {
          dominant_listing_structure: {
            leading_assets: null,
          },
        },
      },
      rolling_24h: {
        trend: null,
        bridges: null,
      },
      realtime_flow: {
        drivers: null,
        current_assets: null,
      },
      change_attribution: {
        asset_exchange_network: {
          nodes: null,
          edges: null,
        },
      },
      official_share_monitor: {
        recent_events: [
          {
            notices: null,
            flow_evidence: null,
          },
        ],
      },
      fx: {
        quote_assets: {
          USDT: { path: null },
        },
      },
      reaction_summary: {
        events: null,
      },
    },
  };

  const normalized = normalizeMarketShareCollections(incoming);
  assert.deepEqual(normalized.asset_exchange_shares[0].exchanges, []);
  assert.deepEqual(normalized.analysis.assets, []);
  assert.deepEqual(normalized.analysis.expert_brief.major_assets, []);
  assert.deepEqual(normalized.analysis.expert_brief.positive_assets, []);
  assert.deepEqual(
    normalized.analysis.expert_brief.attribution_headline
      .dominant_listing_structure.leading_assets,
    []
  );
  assert.deepEqual(normalized.analysis.rolling_24h.trend, []);
  assert.deepEqual(normalized.analysis.rolling_24h.bridges, []);
  assert.deepEqual(normalized.analysis.realtime_flow.drivers, []);
  assert.deepEqual(normalized.analysis.realtime_flow.current_assets, []);
  assert.deepEqual(
    normalized.analysis.change_attribution.asset_exchange_network.nodes,
    []
  );
  assert.deepEqual(
    normalized.analysis.change_attribution.asset_exchange_network.edges,
    []
  );
  assert.deepEqual(
    normalized.analysis.official_share_monitor.recent_events[0].notices,
    []
  );
  assert.deepEqual(
    normalized.analysis.official_share_monitor.recent_events[0].flow_evidence,
    []
  );
  assert.deepEqual(normalized.analysis.fx.quote_assets.USDT.path, []);
  assert.deepEqual(normalized.analysis.reaction_summary.events, []);
});

test("keeps nullable measurements null and preserves clean payload references", () => {
  const clean = {
    analysis: {
      assets: [],
      summary: { bithumb_share_pct: null },
      change_attribution: { share_change_pp: null },
    },
  };
  const normalized = normalizeMarketShareCollections(clean);
  assert.equal(normalized, clean);
  assert.equal(normalized.analysis.summary.bithumb_share_pct, null);
  assert.equal(normalized.analysis.change_attribution.share_change_pp, null);
});

test("adds slow analysis references to a compact realtime tick", () => {
  const analysis = {
    official_domestic_24h: { status: "LIVE" },
    official_ticker_1m: { status: "LIVE" },
    official_binance_24h: { status: "LIVE", assets: [{ asset: "BTC" }] },
    realtime_flow: { version: "v1", status: "LIVE" },
  } as MarketShareAnalysis;
  const compact = {
    version: 7,
    generated_at: 10,
    exchanges: [],
    summary: {},
  } as MarketShareRealtimeTick;

  const merged = mergeMarketShareTickReferences(
    compact,
    marketShareTickReferences(analysis)
  );

  assert.equal(merged.version, 7);
  assert.equal(merged.official_domestic_24h, analysis.official_domestic_24h);
  assert.equal(merged.official_binance_24h, analysis.official_binance_24h);
  assert.equal(merged.realtime_flow, analysis.realtime_flow);
});

test("keeps reference fields carried by a legacy full tick", () => {
  const reference = {
    official_domestic_24h: { status: "REFERENCE" },
    official_ticker_1m: { status: "REFERENCE" },
    official_binance_24h: { status: "REFERENCE" },
    realtime_flow: { status: "REFERENCE" },
  } as unknown as ReturnType<typeof marketShareTickReferences>;
  const legacy = {
    official_domestic_24h: { status: "TICK" },
  } as MarketShareRealtimeTick;

  const merged = mergeMarketShareTickReferences(legacy, reference);

  assert.equal(merged.official_domestic_24h?.status, "TICK");
});

test("partial reference extraction preserves omitted fields and explicit unavailable null", () => {
  const retained = {
    official_domestic_24h: { status: "LIVE" },
    official_ticker_1m: { status: "LIVE" },
  };
  const references = {
    ...retained,
    ...marketShareTickReferences({
      official_ticker_1m: null,
    } as unknown as MarketShareAnalysis),
  };
  assert.equal(
    references.official_domestic_24h,
    retained.official_domestic_24h
  );
  assert.equal(references.official_ticker_1m, null);
  assert.equal("official_binance_24h" in references, false);
});
