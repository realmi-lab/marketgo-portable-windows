import assert from "node:assert/strict";
import test from "node:test";

import {
  coinTradedInView,
  queryCoinTableRows,
  type CoinTableFilters,
  type CoinTableSort,
} from "./coin-table-query.ts";
import type {
  MarketShareAsset,
  MarketShareChangeAsset,
} from "./market-share-types.ts";

function asset(
  name: string,
  contribution: number | null,
  capture: number | null,
  listing: "only" | "upbit" | "other" | "unlisted"
): MarketShareAsset {
  return {
    asset: name,
    listed_on_bithumb: listing !== "unlisted",
    gap_type: "AT_OR_ABOVE_BASELINE",
    domestic_volume_krw: capture === null ? null : capture * 100,
    global_volume_krw: null,
    bithumb_volume_krw: 0,
    upbit_volume_krw: 0,
    upbit_capture_pct: null,
    bithumb_minus_upbit_capture_pp: null,
    competitor_volume_krw: 0,
    bithumb_capture_pct: capture,
    capture_gap_vs_venue_pp: capture,
    domestic_volume_share_pct: null,
    global_volume_share_pct: null,
    global_minus_domestic_interest_pp: null,
    competitor_leader: listing === "unlisted" ? "upbit" : "coinone",
    competitor_leader_volume_krw: 0,
    domestic_exchanges: [],
    domestic_exchange_count: 0,
    exclusive_on_bithumb: listing === "only",
    shared_bithumb_upbit: listing === "upbit",
    bithumb_market_count: 0,
    bithumb_trade_count: 0,
    domestic_trade_count: 0,
    bithumb_trade_count_capture_pct: null,
    bithumb_base_quantity: 0,
    domestic_base_quantity: 0,
    bithumb_base_quantity_capture_pct: null,
    quotes: [],
    share_change_contribution_pp: contribution,
    domestic_volume_change_krw: null,
    bithumb_volume_change_krw: null,
    microstructure: {} as MarketShareAsset["microstructure"],
  };
}

const assets = [
  asset("BTC", 2, 30, "upbit"),
  asset("ETH", -5, 80, "only"),
  asset("XRP", 0, null, "unlisted"),
  asset("SOL", 1, 50, "other"),
];
const changes = new Map<string, MarketShareChangeAsset>();
const noticeAssets = new Set(["ETH"]);
const baseFilters: CoinTableFilters = {
  search: "",
  direction: "all",
  listing: "all",
  notice: "all",
  trade: "traded",
};

test("combines search, direction, listing and notice filters", () => {
  const rows = queryCoinTableRows({
    assets,
    changes,
    noticeAssets,
    filters: {
      ...baseFilters,
      search: "et",
      direction: "negative",
      listing: "bithumb-only",
      notice: "present",
    },
    sort: { key: null, direction: "desc" },
    view: "impact",
  }).rows;
  assert.deepEqual(
    rows.map(row => row.asset),
    ["ETH"]
  );
});

test("toggles numeric column order and keeps missing values last", () => {
  const ascending: CoinTableSort = { key: "capture", direction: "asc" };
  const descending: CoinTableSort = { key: "capture", direction: "desc" };
  const query = (sort: CoinTableSort) =>
    queryCoinTableRows({
      assets,
      changes,
      noticeAssets,
      filters: { ...baseFilters, trade: "all" },
      sort,
      view: "capture",
    }).rows.map(row => row.asset);

  assert.deepEqual(query(ascending), ["BTC", "SOL", "ETH", "XRP"]);
  assert.deepEqual(query(descending), ["ETH", "SOL", "BTC", "XRP"]);
});

test("preserves the existing default ranking until a column is selected", () => {
  const rows = queryCoinTableRows({
    assets,
    changes,
    noticeAssets,
    filters: baseFilters,
    sort: { key: null, direction: "desc" },
    view: "impact",
  }).rows;
  assert.deepEqual(
    rows.map(row => row.asset),
    ["ETH", "BTC", "SOL", "XRP"]
  );
});

// A 10-second window often has trades in about a fifth of the coins; the rest
// had empty rows that filled every page after the first.
const withIdle = [
  ...assets,
  asset("BOB", null, null, "other"),
  asset("GALA", null, null, "upbit"),
  // No comparison row (e.g. the previous window was unavailable) but traded now.
  asset("ADA", null, 40, "upbit"),
];

test("tells coins with values in each view from coins with nothing to show", () => {
  const traded = (name: string, view: "impact" | "capture") =>
    coinTradedInView(
      withIdle.find(row => row.asset === name)!,
      view
    );
  // XRP traded only in the compared window: a change value, no capture now.
  assert.equal(traded("XRP", "impact"), true, "a zero change is still a value");
  assert.equal(traded("XRP", "capture"), false);
  assert.equal(traded("ADA", "impact"), true);
  assert.equal(traded("ADA", "capture"), true);
  assert.equal(traded("BOB", "impact"), false);
  assert.equal(traded("BOB", "capture"), false);
  const tradesWithoutAmount = {
    ...asset("ZRX", null, null, "other"),
    domestic_trade_count: 3,
  };
  assert.equal(coinTradedInView(tradesWithoutAmount, "capture"), true);
});

test("hides coins without trades unless asked and counts what the other filters kept", () => {
  const query = (filters: Partial<CoinTableFilters>) =>
    queryCoinTableRows({
      assets: withIdle,
      changes,
      noticeAssets,
      filters: { ...baseFilters, ...filters },
      sort: { key: null, direction: "desc" },
      view: "impact",
    });

  const byDefault = query({});
  assert.deepEqual(
    byDefault.rows.map(row => row.asset),
    ["ETH", "BTC", "SOL", "XRP", "ADA"]
  );
  assert.equal(byDefault.hiddenUntraded, 2);

  const everything = query({ trade: "all" });
  assert.deepEqual(
    everything.rows.map(row => row.asset),
    ["ETH", "BTC", "SOL", "XRP", "ADA", "BOB", "GALA"]
  );
  assert.equal(everything.hiddenUntraded, 0);

  assert.equal(query({ listing: "bithumb-other" }).hiddenUntraded, 1);
  assert.equal(query({ direction: "positive" }).hiddenUntraded, 0);

  const searched = query({ search: "bo" });
  assert.deepEqual(searched.rows, []);
  assert.equal(searched.hiddenUntraded, 1);
});

test("shows only coins traded in the selected window on the capture view", () => {
  const result = queryCoinTableRows({
    assets: withIdle,
    changes,
    noticeAssets,
    filters: baseFilters,
    sort: { key: null, direction: "desc" },
    view: "capture",
  });
  assert.deepEqual(
    result.rows.map(row => row.asset),
    ["BTC", "ADA", "SOL", "ETH"]
  );
  assert.equal(result.hiddenUntraded, 3);
});

test("keeps coins without a capture gap after the ranked ones by default", () => {
  const rows = queryCoinTableRows({
    assets: withIdle,
    changes,
    noticeAssets,
    filters: { ...baseFilters, trade: "all" },
    sort: { key: null, direction: "desc" },
    view: "capture",
  }).rows;
  assert.deepEqual(
    rows.map(row => row.asset),
    ["BTC", "ADA", "SOL", "ETH", "BOB", "GALA", "XRP"]
  );
});
