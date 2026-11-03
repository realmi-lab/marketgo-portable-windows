import assert from "node:assert/strict";
import test from "node:test";

import {
  MARKET_INTELLIGENCE_CHAPTERS,
  MARKET_INTELLIGENCE_DASHBOARD_HASH,
  bestBookDepthBalance,
  bestBookVisualChange,
  bestBookVisualSignature,
  marketIntelligenceRefreshDisplay,
  marketIntelligenceSummaryRequestMS,
  selectActiveMarketIntelligenceChapter,
  type MarketIntelligenceChapterId,
} from "./market-intelligence-experience.ts";
import type { MarketShareBestOrderBook } from "./market-share-types.ts";

function bestBook(
  overrides: Partial<MarketShareBestOrderBook> = {}
): MarketShareBestOrderBook {
  return {
    exchange: "bithumb",
    asset: "BTC",
    market: "KRW-BTC",
    status: "LIVE",
    age_sec: 0.1,
    bid_price_krw: 100,
    bid_quantity: 2,
    bid_notional_krw: 200,
    ask_price_krw: 101,
    ask_quantity: 3,
    ask_notional_krw: 303,
    ...overrides,
  };
}

test("keeps the previous summary visible until the refresh completes", () => {
  const previous = { episodes: ["previous"] };

  assert.deepEqual(
    marketIntelligenceRefreshDisplay(
      { requestedToMS: 1_000, value: previous },
      2_000
    ),
    { value: previous, refreshing: true }
  );
  assert.deepEqual(
    marketIntelligenceRefreshDisplay(
      { requestedToMS: 2_000, value: { episodes: ["latest"] } },
      2_000
    ),
    { value: { episodes: ["latest"] }, refreshing: false }
  );
});

test("requests the 24-hour summary at most once per minute bucket", () => {
  assert.equal(marketIntelligenceSummaryRequestMS(0), 0);
  assert.equal(marketIntelligenceSummaryRequestMS(Number.NaN), 0);
  assert.equal(marketIntelligenceSummaryRequestMS(12_000), 0);
  assert.equal(marketIntelligenceSummaryRequestMS(60_000), 60_000);
  assert.equal(marketIntelligenceSummaryRequestMS(119_999), 60_000);
  assert.equal(marketIntelligenceSummaryRequestMS(120_000), 120_000);
});

test("marks only genuine top-of-book value changes for live motion", () => {
  const previous = bestBook();
  const current = bestBook({
    bid_price_krw: 101,
    bid_quantity: 1.5,
    bid_notional_krw: 151.5,
    ask_quantity: 4,
    ask_notional_krw: 404,
  });

  assert.deepEqual(bestBookVisualChange(previous, current), {
    bidPrice: "up",
    bidNotional: "down",
    bidQuantity: "down",
    askPrice: null,
    askNotional: "up",
    askQuantity: "up",
    changed: true,
  });
  assert.equal(bestBookVisualChange(null, current).changed, false);
  assert.equal(
    bestBookVisualChange(previous, { ...previous, age_sec: 0.8 }).changed,
    false
  );
});

test("builds a stable signature and real best-level depth balance", () => {
  const row = bestBook({ bid_notional_krw: 300, ask_notional_krw: 100 });

  assert.deepEqual(bestBookDepthBalance(300, 100), {
    bidPct: 75,
    askPct: 25,
  });
  assert.equal(bestBookDepthBalance(null, 100), null);
  assert.equal(bestBookDepthBalance(0, 0), null);
  assert.equal(bestBookVisualSignature(row), "100:2:300:101:3:100");
});

test("preserves the exact five-chapter market-intelligence narrative", () => {
  assert.deepEqual(
    MARKET_INTELLIGENCE_CHAPTERS.map(chapter => ({
      id: chapter.id,
      label: chapter.label,
      domId: chapter.domId,
      kicker: chapter.kicker,
    })),
    [
      {
        id: "overview",
        label: "현재",
        domId: "intelligence-overview",
        kicker: "OVERVIEW",
      },
      {
        id: "pulse",
        label: "추이",
        domId: "intelligence-pulse",
        kicker: "TREND",
      },
      {
        id: "drivers",
        label: "기여",
        domId: "intelligence-drivers",
        kicker: "CONTRIBUTION",
      },
      {
        id: "network",
        label: "관계",
        domId: "intelligence-network",
        kicker: "NETWORK",
      },
      {
        id: "episodes",
        label: "연속 변화",
        domId: "intelligence-episodes",
        kicker: "EVENTS",
      },
    ]
  );
});

test("keeps chapter identifiers and labels unique", () => {
  const uniqueCount = (values: readonly string[]) => new Set(values).size;

  assert.equal(
    uniqueCount(MARKET_INTELLIGENCE_CHAPTERS.map(chapter => chapter.id)),
    MARKET_INTELLIGENCE_CHAPTERS.length
  );
  assert.equal(
    uniqueCount(MARKET_INTELLIGENCE_CHAPTERS.map(chapter => chapter.domId)),
    MARKET_INTELLIGENCE_CHAPTERS.length
  );
  assert.equal(
    uniqueCount(MARKET_INTELLIGENCE_CHAPTERS.map(chapter => chapter.label)),
    MARKET_INTELLIGENCE_CHAPTERS.length
  );
});

test("uses DOM scroll targets without replacing the dashboard route hash", () => {
  assert.equal(MARKET_INTELLIGENCE_DASHBOARD_HASH, "#intelligence");
  for (const chapter of MARKET_INTELLIGENCE_CHAPTERS) {
    assert.match(chapter.domId, /^intelligence-[a-z]+$/);
    assert.equal(chapter.domId.startsWith("#"), false);
    assert.deepEqual(Object.keys(chapter), ["id", "domId", "label", "kicker"]);
  }
});

test("selects the largest visible ratio before top proximity", () => {
  assert.equal(
    selectActiveMarketIntelligenceChapter(
      [
        { id: "overview", ratio: 0.3, top: 8 },
        { id: "pulse", ratio: 0.7, top: 240 },
      ],
      "overview"
    ),
    "pulse"
  );
});

test("breaks visibility ties by top proximity and then manifest order", () => {
  assert.equal(
    selectActiveMarketIntelligenceChapter([
      { id: "drivers", ratio: 0.5, top: 120 },
      { id: "pulse", ratio: 0.5, top: -20 },
    ]),
    "pulse"
  );
  assert.equal(
    selectActiveMarketIntelligenceChapter([
      { id: "network", ratio: 0.5, top: 20 },
      { id: "drivers", ratio: 0.5, top: -20 },
    ]),
    "drivers"
  );
});

test("keeps the previous chapter when no valid chapter is visible", () => {
  assert.equal(selectActiveMarketIntelligenceChapter([], "network"), "network");
  assert.equal(
    selectActiveMarketIntelligenceChapter(
      [
        { id: "pulse", ratio: 0, top: 0 },
        { id: "drivers", ratio: Number.NaN, top: 10 },
      ],
      "episodes"
    ),
    "episodes"
  );
  assert.equal(
    selectActiveMarketIntelligenceChapter(
      [],
      "unknown" as MarketIntelligenceChapterId
    ),
    "overview"
  );
});
