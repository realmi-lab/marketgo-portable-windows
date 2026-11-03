import assert from "node:assert/strict";
import test from "node:test";

import {
  domesticExchangeFrameIsComplete,
  selectStableExchangeFrame,
  selectStableOfficialDomestic,
  selectStableOfficialTrend,
  selectStableRealtimeFlowState,
  type StableExchangeObservation,
} from "./market-share-display-stability.ts";
import type {
  MarketShareOfficialDomestic24h,
  MarketShareOfficialTicker1mState,
  MarketShareRealtimeFlowState,
} from "./market-share-types.ts";

function officialSnapshot(
  asOf: number,
  status: MarketShareOfficialDomestic24h["status"] = "LIVE"
): MarketShareOfficialDomestic24h {
  return {
    status,
    source: "OFFICIAL_TEST",
    as_of: asOf,
    started_at: asOf - 86_400,
    collection_span_ms: 86_400_000,
    expected_exchange_count: 5,
    ready_exchange_count: status === "LIVE" ? 5 : 2,
    total_volume_krw: status === "LIVE" ? 1_000 : null,
    bithumb_volume_krw: status === "LIVE" ? 300 : null,
    bithumb_share_pct: status === "LIVE" ? 30 : null,
    exchanges: {
      bithumb: {
        exchange: "bithumb",
        status: status === "LIVE" ? "LIVE" : "STALE",
        source: "OFFICIAL_TEST",
        volume_krw: status === "LIVE" ? 300 : null,
        market_count: 10,
        as_of: asOf,
        exchange_timestamp_ms: asOf * 1_000,
      },
    },
  };
}

function officialTrend(
  generatedAtMS: number,
  status = "LIVE"
): MarketShareOfficialTicker1mState {
  return {
    status,
    generated_at_ms: generatedAtMS,
    previous: status === "LIVE" ? { bithumb_share_pct: 30 } : null,
    current: status === "LIVE" ? { bithumb_share_pct: 31 } : null,
    share_delta_pp: status === "LIVE" ? 1 : null,
    trend: status === "LIVE" ? [{ bithumb_share_pct: 31 }] : [],
  } as unknown as MarketShareOfficialTicker1mState;
}

function realtimeState(
  asOfMS: number,
  complete: boolean
): MarketShareRealtimeFlowState {
  return {
    version: "REALTIME_FLOW_V2",
    as_of_ms: asOfMS,
    status: complete ? "NORMAL" : "DEGRADED",
    data_quality: {
      complete_frame: complete,
      frame_status: complete ? "LIVE" : "DEGRADED",
    },
    windows: complete
      ? [
          {
            duration_sec: 10,
            status: "COMPLETE",
            current_share_pct: 31,
            previous_share_pct: 30,
            share_delta_pp: 1,
            current_volume_krw: 200,
            previous_volume_krw: 180,
          },
        ]
      : [],
  } as unknown as MarketShareRealtimeFlowState;
}

const completeExchanges: StableExchangeObservation[] = [
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
].map((exchange, index) => ({
  exchange,
  volume_krw: 100 - index,
  share_pct: 20,
  trade_count: 10,
  trade_count_share_pct: 20,
}));

test("retains the last complete official values across a partial refresh", () => {
  const retainedOfficial = officialSnapshot(1_000);
  const partialOfficial = officialSnapshot(2_000, "PARTIAL");
  assert.equal(
    selectStableOfficialDomestic(partialOfficial, retainedOfficial),
    retainedOfficial
  );
  const incompleteBithumbRow = officialSnapshot(2_500);
  incompleteBithumbRow.exchanges.bithumb.volume_krw = null;
  assert.equal(
    selectStableOfficialDomestic(incompleteBithumbRow, retainedOfficial),
    retainedOfficial
  );

  const retainedTrend = officialTrend(1_000);
  const gapTrend = officialTrend(2_000, "GAP");
  assert.equal(
    selectStableOfficialTrend(gapTrend, retainedTrend),
    retainedTrend
  );
  assert.equal(
    selectStableOfficialTrend(officialTrend(3_000), retainedTrend)
      ?.generated_at_ms,
    3_000
  );
});

test("does not replace five exchange rows with a partial reconnect frame", () => {
  const partial = completeExchanges.slice(0, 2);
  assert.equal(domesticExchangeFrameIsComplete(partial), false);
  assert.equal(
    selectStableExchangeFrame(partial, completeExchanges),
    completeExchanges
  );
  assert.equal(
    domesticExchangeFrameIsComplete(completeExchanges, ["bithumb", "upbit"]),
    false
  );
  assert.equal(
    selectStableExchangeFrame(
      completeExchanges.map(row => ({ ...row, volume_krw: 0 })),
      completeExchanges,
      completeExchanges.map(row => row.exchange),
      false
    ),
    completeExchanges
  );
});

test("retains the newest complete ten-second sample across degradation", () => {
  const retained = realtimeState(10_000, true);
  const degraded = realtimeState(20_000, false);
  assert.equal(selectStableRealtimeFlowState(degraded, retained), retained);

  const newer = realtimeState(30_000, true);
  assert.equal(selectStableRealtimeFlowState(newer, retained), newer);
  assert.equal(selectStableRealtimeFlowState(retained, newer), newer);
});
