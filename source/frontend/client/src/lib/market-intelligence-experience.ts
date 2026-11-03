import type { MarketShareBestOrderBook } from "./market-share-types";

export const MARKET_INTELLIGENCE_DASHBOARD_HASH = "#intelligence";

export const MARKET_INTELLIGENCE_CHAPTERS = [
  {
    id: "overview",
    domId: "intelligence-overview",
    label: "현재",
    kicker: "OVERVIEW",
  },
  {
    id: "pulse",
    domId: "intelligence-pulse",
    label: "추이",
    kicker: "TREND",
  },
  {
    id: "drivers",
    domId: "intelligence-drivers",
    label: "기여",
    kicker: "CONTRIBUTION",
  },
  {
    id: "network",
    domId: "intelligence-network",
    label: "관계",
    kicker: "NETWORK",
  },
  {
    id: "episodes",
    domId: "intelligence-episodes",
    label: "연속 변화",
    kicker: "EVENTS",
  },
] as const;

export type MarketIntelligenceChapterId =
  (typeof MARKET_INTELLIGENCE_CHAPTERS)[number]["id"];

export interface MarketIntelligenceChapterObservation {
  id: MarketIntelligenceChapterId;
  ratio: number;
  top: number;
}

export interface MarketIntelligenceRefreshResult<T> {
  requestedToMS: number;
  value: T;
}

export interface MarketIntelligenceRefreshDisplay<T> {
  value: T | null;
  refreshing: boolean;
}

export function marketIntelligenceSummaryRequestMS(asOfMS: number): number {
  if (!Number.isFinite(asOfMS) || asOfMS <= 0) return 0;
  return Math.floor(asOfMS / 60_000) * 60_000;
}

export type BestBookChangeDirection = "up" | "down";

export interface BestBookVisualChange {
  bidPrice: BestBookChangeDirection | null;
  bidNotional: BestBookChangeDirection | null;
  bidQuantity: BestBookChangeDirection | null;
  askPrice: BestBookChangeDirection | null;
  askNotional: BestBookChangeDirection | null;
  askQuantity: BestBookChangeDirection | null;
  changed: boolean;
}

export interface BestBookDepthBalance {
  bidPct: number;
  askPct: number;
}

function numericDirection(
  previous: number | null | undefined,
  current: number | null | undefined
): BestBookChangeDirection | null {
  if (
    typeof previous !== "number" ||
    !Number.isFinite(previous) ||
    typeof current !== "number" ||
    !Number.isFinite(current) ||
    previous === current
  ) {
    return null;
  }
  return current > previous ? "up" : "down";
}

/**
 * Compares two genuine WebSocket top-of-book projections. The first frame is
 * deliberately quiet so retained or freshly loaded values do not look like a
 * market move.
 */
export function bestBookVisualChange(
  previous: MarketShareBestOrderBook | null | undefined,
  current: MarketShareBestOrderBook | null | undefined
): BestBookVisualChange {
  const change = {
    bidPrice: numericDirection(previous?.bid_price_krw, current?.bid_price_krw),
    bidNotional: numericDirection(
      previous?.bid_notional_krw,
      current?.bid_notional_krw
    ),
    bidQuantity: numericDirection(
      previous?.bid_quantity,
      current?.bid_quantity
    ),
    askPrice: numericDirection(previous?.ask_price_krw, current?.ask_price_krw),
    askNotional: numericDirection(
      previous?.ask_notional_krw,
      current?.ask_notional_krw
    ),
    askQuantity: numericDirection(
      previous?.ask_quantity,
      current?.ask_quantity
    ),
  };

  return {
    ...change,
    changed: Object.values(change).some(Boolean),
  };
}

export function bestBookDepthBalance(
  bidNotional: number | null | undefined,
  askNotional: number | null | undefined
): BestBookDepthBalance | null {
  if (
    typeof bidNotional !== "number" ||
    !Number.isFinite(bidNotional) ||
    bidNotional < 0 ||
    typeof askNotional !== "number" ||
    !Number.isFinite(askNotional) ||
    askNotional < 0
  ) {
    return null;
  }
  const total = bidNotional + askNotional;
  if (total <= 0) return null;
  const bidPct = Math.min(100, Math.max(0, (bidNotional / total) * 100));
  return { bidPct, askPct: 100 - bidPct };
}

export function bestBookVisualSignature(
  row: MarketShareBestOrderBook | null | undefined
): string {
  if (!row) return "waiting";
  return [
    row.bid_price_krw,
    row.bid_quantity,
    row.bid_notional_krw,
    row.ask_price_krw,
    row.ask_quantity,
    row.ask_notional_krw,
  ]
    .map(value =>
      typeof value === "number" && Number.isFinite(value) ? String(value) : "—"
    )
    .join(":");
}

/**
 * Keeps the last completed payload visible while a newer request is pending.
 * The completed payload is replaced only after the new response arrives.
 */
export function marketIntelligenceRefreshDisplay<T>(
  completed: MarketIntelligenceRefreshResult<T> | null,
  requestedToMS: number
): MarketIntelligenceRefreshDisplay<T> {
  return {
    value: completed?.value ?? null,
    refreshing: Boolean(
      completed &&
      requestedToMS > 0 &&
      completed.requestedToMS !== requestedToMS
    ),
  };
}

const DEFAULT_CHAPTER: MarketIntelligenceChapterId = "overview";
const CHAPTER_ORDER = new Map<MarketIntelligenceChapterId, number>(
  MARKET_INTELLIGENCE_CHAPTERS.map((chapter, index) => [chapter.id, index])
);

function isChapterId(value: unknown): value is MarketIntelligenceChapterId {
  return CHAPTER_ORDER.has(value as MarketIntelligenceChapterId);
}

/**
 * Chooses one visible chapter without changing the dashboard route hash.
 * Visibility ratio wins, followed by proximity to the scroll-root top and the
 * manifest order. If nothing is visible, the previous valid chapter remains.
 */
export function selectActiveMarketIntelligenceChapter(
  observations: readonly MarketIntelligenceChapterObservation[],
  previous: MarketIntelligenceChapterId = DEFAULT_CHAPTER
): MarketIntelligenceChapterId {
  const fallback = isChapterId(previous) ? previous : DEFAULT_CHAPTER;
  const active = observations
    .filter(
      observation =>
        isChapterId(observation.id) &&
        Number.isFinite(observation.ratio) &&
        observation.ratio > 0 &&
        Number.isFinite(observation.top)
    )
    .map(observation => ({
      ...observation,
      ratio: Math.min(1, observation.ratio),
    }))
    .sort(
      (left, right) =>
        right.ratio - left.ratio ||
        Math.abs(left.top) - Math.abs(right.top) ||
        (CHAPTER_ORDER.get(left.id) ?? Number.MAX_SAFE_INTEGER) -
          (CHAPTER_ORDER.get(right.id) ?? Number.MAX_SAFE_INTEGER)
    )[0];

  return active?.id ?? fallback;
}
