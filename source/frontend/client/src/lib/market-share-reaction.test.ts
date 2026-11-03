import assert from "node:assert/strict";
import test from "node:test";

import {
  CURRENT_WINDOW_CONTRIBUTION_SCOPE,
  eventCurrentWindowContribution,
  exchangeNoticeAssetLabels,
  isRelevantDomesticNotice,
  isNoticeRelevantToSelectedWindow,
  latestSlippageNoticesByAsset,
  marketShareNoticeItems,
  noticeAnchorTimeMs,
  noticeAnchorTimesMs,
  noticeAssetLabels,
  noticeTimestampLabel,
  noticeTimestampLines,
  observedExchangeReactionCount,
  reactionOriginTimingAnchor,
  selectedReactionDetailEvent,
  selectedReactionEvent,
  sortNoticesNewestFirst,
} from "./market-share-reaction.ts";
import type {
  ExchangeNotice,
  MarketShareReactionEvent,
  MarketShareReactionSummary,
} from "./market-share-types.ts";

const event = (eventId: string): MarketShareReactionEvent =>
  ({
    event_id: eventId,
  }) as MarketShareReactionEvent;

const reaction = (
  topNotice: MarketShareReactionEvent | null,
  events: MarketShareReactionEvent[]
): Pick<MarketShareReactionSummary, "events" | "top_notice"> => ({
  top_notice: topNotice,
  events,
});

test("treats an omitted dashboard notice bundle as an empty list", () => {
  const notice = { notice_id: "notice-1" } as ExchangeNotice;

  assert.deepEqual(marketShareNoticeItems(undefined), []);
  assert.deepEqual(marketShareNoticeItems(null), []);
  assert.deepEqual(marketShareNoticeItems({}), []);
  assert.deepEqual(marketShareNoticeItems({ items: null }), []);
  assert.deepEqual(marketShareNoticeItems({ items: [null, notice] }), [notice]);
});

test("defaults to the top notice when no event is selected", () => {
  const topNotice = event("top");
  assert.equal(
    selectedReactionEvent(reaction(topNotice, [topNotice]), null),
    topNotice
  );
});

test("uses the clicked event across the reaction summary", () => {
  const topNotice = event("top");
  const clickedEvent = event("clicked");
  assert.equal(
    selectedReactionEvent(
      reaction(topNotice, [topNotice, clickedEvent]),
      "clicked"
    ),
    clickedEvent
  );
});

test("accepts current-window contribution only under the non-causal scope", () => {
  const scoped = {
    contribution_scope: CURRENT_WINDOW_CONTRIBUTION_SCOPE,
    linked_asset_current_window_contribution_pp: -0.31,
  } as MarketShareReactionEvent;
  assert.equal(eventCurrentWindowContribution(scoped), -0.31);
  assert.equal(
    eventCurrentWindowContribution({
      ...scoped,
      contribution_scope: "EVENT_CAUSAL" as typeof scoped.contribution_scope,
    }),
    null
  );
  assert.equal(
    eventCurrentWindowContribution({
      ...scoped,
      linked_asset_current_window_contribution_pp: Number.NaN,
    }),
    null
  );
});

test("clears an event id that is no longer present", () => {
  const topNotice = event("top");
  assert.equal(
    selectedReactionEvent(reaction(topNotice, [topNotice]), "missing"),
    null
  );
});

test("does not open a notice detail drawer without an explicit selection", () => {
  const topNotice = event("top");
  assert.equal(
    selectedReactionDetailEvent(reaction(topNotice, [topNotice]), null),
    null
  );
  assert.equal(
    selectedReactionDetailEvent(reaction(topNotice, [topNotice]), "top"),
    topNotice
  );
});

test("does not promote an old notice just because its reaction study is mature", () => {
  assert.equal(
    isNoticeRelevantToSelectedWindow({
      within_selected_window: false,
      reaction_studies: [
        { status: "MATURE" } as ExchangeNotice["reaction_studies"][number],
      ],
    }),
    false
  );
  assert.equal(
    isNoticeRelevantToSelectedWindow({
      within_selected_window: true,
      reaction_studies: [],
    }),
    true
  );
});

test("sorts notices by publication time descending without mutating input", () => {
  const notices = [
    {
      exchange: "upbit",
      notice_id: "old",
      published_ts_ms: 1_000,
      detected_ts_ms: 9_000,
      updated_ts_ms: 20_000,
    },
    {
      exchange: "bithumb",
      notice_id: "new",
      published_ts_ms: 3_000,
      detected_ts_ms: 3_100,
      updated_ts_ms: 3_200,
    },
    {
      exchange: "coinone",
      notice_id: "middle",
      published_ts_ms: 2_000,
      detected_ts_ms: 2_100,
      updated_ts_ms: 2_200,
    },
  ];

  assert.deepEqual(
    sortNoticesNewestFirst(notices).map(notice => notice.notice_id),
    ["new", "middle", "old"]
  );
  assert.deepEqual(
    notices.map(notice => notice.notice_id),
    ["old", "new", "middle"]
  );
});

test("uses detection time when a notice has no publication time", () => {
  const notices = [
    {
      exchange: "upbit",
      notice_id: "published",
      published_ts_ms: 1_000,
      detected_ts_ms: 1_100,
      updated_ts_ms: 5_000,
    },
    {
      exchange: "bithumb",
      notice_id: "detected",
      published_ts_ms: 0,
      detected_ts_ms: 2_000,
      updated_ts_ms: 2_100,
    },
  ];

  assert.deepEqual(
    sortNoticesNewestFirst(notices).map(notice => notice.notice_id),
    ["detected", "published"]
  );
});

test("labels publication and later revision times separately", () => {
  const notice = {
    published_ts_ms: Date.UTC(2026, 6, 27, 8, 1, 44),
    updated_ts_ms: Date.UTC(2026, 7, 25, 9, 56, 12),
    detected_ts_ms: Date.UTC(2026, 7, 25, 10, 0, 0),
    timestamp_precision: "SECOND" as const,
  };
  const label = noticeTimestampLabel(notice);

  assert.match(label, /^게시 /);
  assert.match(label, / · 수정 /);
  assert.ok(label.indexOf("게시 ") < label.indexOf(" · 수정 "));
  assert.deepEqual(
    noticeTimestampLines(notice).map(line => line.split(" ")[0]),
    ["게시", "수정"]
  );
});

test("Overview context keeps only linked domestic notices", () => {
  const linkedNotice = {
    exchange: "upbit",
    within_selected_window: true,
    market_context: [{}],
    reaction_studies: [],
  } as ExchangeNotice;
  assert.equal(isRelevantDomesticNotice(linkedNotice), true);
  assert.equal(
    isRelevantDomesticNotice({
      ...linkedNotice,
      exchange: "binance",
    }),
    false
  );
  assert.equal(
    isRelevantDomesticNotice({
      ...linkedNotice,
      market_context: [],
    }),
    false
  );
  assert.equal(
    isRelevantDomesticNotice({
      ...linkedNotice,
      reaction_studies: [
        { status: "MATURE" } as ExchangeNotice["reaction_studies"][number],
      ],
    }),
    true
  );
  assert.equal(
    isRelevantDomesticNotice({
      ...linkedNotice,
      within_selected_window: false,
      reaction_studies: [
        { status: "MATURE" } as ExchangeNotice["reaction_studies"][number],
      ],
    }),
    false
  );
});

test("keeps an FX-incomplete reaction visible without promoting an old notice", () => {
  const notice = {
    exchange: "upbit",
    within_selected_window: true,
    market_context: [],
    reaction_studies: [{ status: "INCOMPLETE_FX_CONVERSION" }],
  } as unknown as ExchangeNotice;
  assert.equal(isRelevantDomesticNotice(notice), true);
  assert.equal(
    isRelevantDomesticNotice({ ...notice, within_selected_window: false }),
    false
  );
});

test("anchors a notice to its latest selected-window time", () => {
  const notice = {
    published_ts_ms: 900_000,
    detected_ts_ms: 900_100,
    timestamp_precision: "DAY",
    schedules: [{ start_ts_ms: 1_200_000 }, { start_ts_ms: 1_000_000 }],
  } as ExchangeNotice;
  assert.equal(noticeAnchorTimeMs(notice), 1_200_000);
  assert.equal(
    noticeAnchorTimeMs({
      ...notice,
      timestamp_precision: "SECOND",
    }),
    1_200_000
  );
  assert.deepEqual(
    noticeAnchorTimesMs({
      ...notice,
      timestamp_precision: "SECOND",
    }),
    [900_000, 1_200_000, 1_000_000]
  );
});

test("anchors a detected-only notice to its collection time", () => {
  assert.equal(
    noticeAnchorTimeMs({
      published_ts_ms: 0,
      detected_ts_ms: 1_500_000,
      timestamp_precision: "DETECTED",
      schedules: [],
    } as ExchangeNotice),
    1_500_000
  );
});

test("anchors a notice to a schedule end when it is the latest event time", () => {
  assert.equal(
    noticeAnchorTimeMs({
      published_ts_ms: 900_000,
      detected_ts_ms: 0,
      timestamp_precision: "SECOND",
      schedules: [{ start_ts_ms: 1_000_000, end_ts_ms: 1_300_000 }],
    } as ExchangeNotice),
    1_300_000
  );
});

test("uses linked assets when official notice asset extraction is empty", () => {
  assert.deepEqual(
    noticeAssetLabels({
      affected_assets: [],
      linked_assets: [{ asset: "XRP" }, { asset: "XRP" }, { asset: "BTC" }],
    } as MarketShareReactionEvent),
    ["XRP", "BTC"]
  );
  assert.deepEqual(
    noticeAssetLabels({
      affected_assets: ["ETH"],
      linked_assets: [{ asset: "BTC" }],
    } as MarketShareReactionEvent),
    ["ETH"]
  );
});

test("uses market context when an exchange notice has no official assets", () => {
  assert.deepEqual(
    exchangeNoticeAssetLabels({
      assets: [],
      market_context: [{ asset: "XRP" }, { asset: "XRP" }, { asset: "BTC" }],
    } as ExchangeNotice),
    ["XRP", "BTC"]
  );
  assert.deepEqual(
    exchangeNoticeAssetLabels({
      assets: ["ETH"],
      market_context: [{ asset: "BTC" }],
    } as ExchangeNotice),
    ["ETH"]
  );
});

test("indexes only the latest Bithumb and Upbit notices for each explicit asset", () => {
  const notice = (
    exchange: string,
    noticeId: string,
    published: number,
    assets: string[]
  ) =>
    ({
      exchange,
      notice_id: noticeId,
      title: `${noticeId} 공지`,
      published_ts_ms: published,
      detected_ts_ms: published,
      updated_ts_ms: published,
      assets,
    }) as ExchangeNotice;
  const indexed = latestSlippageNoticesByAsset([
    notice("upbit", "upbit-old", 100, ["usdt"]),
    notice("coinone", "coinone-new", 500, ["USDT"]),
    notice("bithumb", "bithumb-new", 400, ["USDT"]),
    notice("upbit", "upbit-new", 300, ["USDT"]),
    notice("bithumb", "bithumb-btc", 350, ["BTC"]),
    {
      ...notice("upbit", "inj-markets", 600, ["INJ", "BTC", "USDT"]),
      title: "인젝티브(INJ) 거래 유의 종목 지정 안내",
    } as ExchangeNotice,
    notice("bithumb", "bithumb-eth", 600, ["ETH"]),
    {
      ...notice("bithumb", "ignored-market-context", 700, []),
      market_context: [{ asset: "XRP" }],
    } as ExchangeNotice,
  ]);

  assert.deepEqual(
    indexed.get("USDT")?.map(item => item.notice_id),
    ["bithumb-new", "upbit-new"]
  );
  assert.deepEqual(
    indexed.get("BTC")?.map(item => item.notice_id),
    ["bithumb-btc"]
  );
  assert.deepEqual(
    indexed.get("INJ")?.map(item => item.notice_id),
    ["inj-markets"]
  );
  assert.equal(indexed.has("XRP"), false);
  assert.equal(indexed.has("ETH"), true);
});

test("counts only observed exchange reactions", () => {
  assert.equal(
    observedExchangeReactionCount({
      exchange_reactions: [
        { exchange: "bithumb", data_status: "OBSERVED" },
        { exchange: "upbit", data_status: "UNAVAILABLE" },
        { exchange: "coinone", data_status: "UNAVAILABLE" },
      ],
    } as MarketShareReactionEvent),
    1
  );
  assert.equal(
    observedExchangeReactionCount({
      exchange_reactions: [],
    } as MarketShareReactionEvent),
    0
  );
});

test("uses the selected effective time when it is the reaction anchor", () => {
  assert.deepEqual(
    reactionOriginTimingAnchor({
      origin: {
        event_time_ms: 1_700_000,
        event_time_basis: "EFFECTIVE",
        notice_time_ms: 900_000,
        effective_time_ms: 1_700_000,
      },
    } as MarketShareReactionEvent),
    {
      time: 1_700_000,
      shortLabel: "적용 기준",
      longLabel: "국내 적용 시각 기준",
    }
  );
});

test("labels a scheduled reaction card with its schedule anchor", () => {
  assert.deepEqual(
    reactionOriginTimingAnchor({
      origin: {
        event_time_ms: 1_700_000,
        event_time_basis: "SCHEDULE",
        notice_time_ms: 900_000,
        effective_time_ms: 1_700_000,
      },
    } as MarketShareReactionEvent),
    {
      time: 1_700_000,
      shortLabel: "일정 기준",
      longLabel: "국내 공지 일정 기준",
    }
  );
});
