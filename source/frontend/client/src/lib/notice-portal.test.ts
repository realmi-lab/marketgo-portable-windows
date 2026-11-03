import assert from "node:assert/strict";
import test from "node:test";
import {
  countPortalNotices,
  filterPortalNotices,
  isNoticePortal,
  noticeBodyText,
  noticeAssetMarkets,
  noticeQuantity,
  noticeHourlyAmount,
  noticeHourlySummary,
  noticeLiveVolume,
} from "./notice-portal.ts";
import type { IndexDayResponse, IndexDayRow } from "./index-day.ts";
import type {
  ExchangeNotice,
  MarketShareAsset,
} from "./market-share-types.ts";

test("notice site has an explicit host and preview path without changing other entrypoints", () => {
  assert.equal(isNoticePortal("notice.realmi.net", "/"), true);
  assert.equal(isNoticePortal("localhost", "/notices/"), true);
  assert.equal(isNoticePortal("share.realmi.net", "/"), false);
  assert.equal(
    isNoticePortal("notice.realmi.net.example.com", "/notices-other"),
    false
  );
});

test("collecting placeholder zero is missing while verified zero remains zero", () => {
  const row = {
    volume_krw: 0,
    observed_volume_krw: 0,
    amount_hours: 0,
    status: "ROLLING_COLLECTING",
  } as IndexDayRow;
  assert.equal(noticeHourlyAmount(row), null);
  assert.equal(noticeHourlyAmount({ ...row, amount_hours: 6 }), 0);
  assert.equal(noticeHourlyAmount({ ...row, status: "COMPLETE" }), 0);
  assert.equal(
    noticeHourlyAmount({ ...row, volume_krw: null, observed_volume_krw: null }),
    null
  );
});

test("coin total uses its own server cell and candle evidence, never the whole market", () => {
  const data = {
    rows: [
      {
        base: "EGLD",
        volume_krw: 125,
        amount_hours: 6,
        status: "ROLLING_COLLECTING",
      },
      {
        base: "CT",
        volume_krw: 0,
        amount_hours: 0,
        status: "ROLLING_COLLECTING",
      },
    ],
    domestic: {
      totals: { domestic: { total: 999999 } },
      coins: [
        {
          base: "EGLD",
          cells: {
            domestic: { total: 125, partial: true, unavailable: false },
          },
        },
        {
          base: "CT",
          cells: { domestic: { total: 0, partial: true, unavailable: false } },
        },
      ],
    },
  } as IndexDayResponse;
  assert.equal(noticeHourlySummary(data, "EGLD").amount, 125);
  assert.equal(noticeHourlySummary(data, "EGLD").partial, true);
  assert.equal(noticeHourlySummary(data, "EGLD").refreshMS, 15000);
  assert.equal(noticeHourlySummary(data, "CT").amount, null);
  assert.equal(noticeHourlySummary(data, "UNKNOWN").amount, null);
  assert.deepEqual(noticeHourlySummary(data, "CT").rows, [data.rows[1]]);
});

test("complete candle amounts retain zero and delayed rows retain the warning", () => {
  const data = {
    rows: [
      { base: "CT", volume_krw: 0, amount_hours: 24, status: "ROLLING_OK" },
    ],
    domestic: {
      coins: [
        {
          base: "CT",
          cells: { domestic: { total: 0, partial: false, unavailable: false } },
        },
      ],
    },
  } as IndexDayResponse;
  const result = noticeHourlySummary(data, "CT");
  assert.equal(result.amount, 0);
  assert.equal(result.partial, false);
  assert.equal(result.refreshMS, 60000);
  data.rows[0].status = "ROLLING_STALE";
  assert.equal(noticeHourlySummary(data, "CT").label, "갱신 지연");
});

test("registry membership without observed trades is not a measured zero", () => {
  const row = {
    domestic_trade_count: 0,
    domestic_base_quantity: 0,
    domestic_volume_krw: 0,
  } as MarketShareAsset;
  assert.deepEqual(noticeLiveVolume(row, false), {
    quantity: null,
    amount: null,
  });
  assert.deepEqual(noticeLiveVolume(row, true), { quantity: 0, amount: 0 });
  assert.deepEqual(
    noticeLiveVolume(
      {
        ...row,
        domestic_trade_count: 2,
        domestic_base_quantity: 3,
        domestic_volume_krw: 125,
      },
      false
    ),
    { quantity: 3, amount: 125 }
  );
  assert.deepEqual(noticeLiveVolume(undefined, true), {
    quantity: null,
    amount: null,
  });
});

test("notice coin selection never mixes another asset or replaces missing quantities", () => {
  const rows = [
    { base: "CT", market: "KRW-CT", volume_krw: null },
    { base: "BTC", market: "KRW-BTC", volume_krw: 100 },
  ];
  const result = noticeAssetMarkets({ rows } as IndexDayResponse, "CT");
  assert.deepEqual(result, [rows[0]]);
  assert.equal(result[0], rows[0]);
  assert.equal(noticeQuantity(null), "—");
  assert.equal(noticeQuantity(0), "0");
  assert.equal(noticeQuantity(0.00001), "0.0001 미만");
});

const portalNotice = (fields: Partial<ExchangeNotice>) =>
  ({
    exchange: "bithumb",
    notice_id: "1",
    title: "",
    category: "LISTING",
    assets: [],
    body_excerpt: "",
    ...fields,
  }) as ExchangeNotice;

test("portal filters the whole archive by exchange, category, coin and body text", () => {
  const notices = [
    portalNotice({ notice_id: "1", title: "BTC 입출금 중단", assets: ["BTC"], category: "DEPOSIT_WITHDRAWAL" }),
    portalNotice({ notice_id: "2", exchange: "upbit", title: "신규 거래지원", assets: ["CT"], original_body_text: "콘크리트 KRW 마켓" }),
    portalNotice({ notice_id: "3", title: "점검 안내", category: "MAINTENANCE" }),
  ];
  const all = { exchange: "all", category: "all", asset: "all", query: "" };
  const ids = (filter: typeof all) =>
    filterPortalNotices(notices, filter).map(notice => notice.notice_id);
  assert.deepEqual(ids(all), ["1", "2", "3"]);
  assert.deepEqual(ids({ ...all, exchange: "upbit" }), ["2"]);
  assert.deepEqual(ids({ ...all, category: "MAINTENANCE" }), ["3"]);
  assert.deepEqual(ids({ ...all, asset: "BTC" }), ["1"]);
  assert.deepEqual(ids({ ...all, query: "콘크리트" }), ["2"]);
  assert.deepEqual(countPortalNotices(notices, "exchange"), [["bithumb", 2], ["upbit", 1]]);
});

test("repeated searches over the same archive keep matching the whole body", () => {
  const notices = [
    portalNotice({ notice_id: "1", title: "Event", original_body_text: "본문 끝의 KRW 마켓" }),
    portalNotice({ notice_id: "2", title: "점검", assets: ["ETH"] }),
  ];
  const all = { exchange: "all", category: "all", asset: "all" };
  for (const [query, expected] of [
    ["krw", ["1"]],
    ["EVENT", ["1"]],
    ["eth", ["2"]],
    ["krw", ["1"]],
    ["없는 단어", []],
  ] as const)
    assert.deepEqual(
      filterPortalNotices(notices, { ...all, query }).map(notice => notice.notice_id),
      expected
    );
});

test("the portal shows the whole body, not the cut excerpt", () => {
  assert.equal(noticeBodyText({ body_excerpt: "앞부분", original_body_text: "앞부분과 끝" }), "앞부분과 끝");
  assert.equal(noticeBodyText({ body_excerpt: "본문" }), "본문");
});
