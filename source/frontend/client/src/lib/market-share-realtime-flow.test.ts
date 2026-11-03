import assert from "node:assert/strict";
import test from "node:test";

import {
  buildRealtimeFlowHistoryPath,
  buildRealtimeFlowShareChartSeries,
  buildRealtimeFlowOneSecondShareChartSeries,
  compareOfficialHourToRealtimeDirection,
  compactRealtimeFlowHistory,
  mergeRealtimeFlowHistory,
  realtimeFlowHistoryRange,
  realtimeFlowHistoryRequest,
  realtimeFlowEpisodeDirectionTone,
  realtimeFlowEpisodeStatusLabel,
  previewRealtimeFlowEpisodes,
  REALTIME_FLOW_PANEL_SECTION_ORDER,
  realtimeFlowOrderbookValuesAreVisible,
  selectRealtimeFlowEpisodes,
  selectRealtimeFlowMissingIntervals,
  realtimeFlowTenSecondAttribution,
  realtimeFlowTenSecondWindow,
  realtimeFlowTenSecondSample,
  realtimeFlowSignalHeadline,
  realtimeFlowTransportIsLive,
  realtimeFlowValuesAreVisible,
  realtimeFlowWindowObserved,
  realtimeFlowWindowValuesAreVisible,
} from "./market-share-realtime-flow.ts";
import type {
  MarketShareRealtimeFlowState,
  MarketShareRealtimeFlowWindow,
} from "./market-share-types.ts";

test("keeps only realtime-flow events and detail in the panel hierarchy", () => {
  assert.deepEqual(REALTIME_FLOW_PANEL_SECTION_ORDER, ["events", "detail"]);
});

test("compares the latest official hour with the current ten-second direction", () => {
  assert.equal(compareOfficialHourToRealtimeDirection(0.4, "UP", true), "SAME");
  assert.equal(
    compareOfficialHourToRealtimeDirection(-0.4, "UP", true),
    "OPPOSITE"
  );
  assert.equal(
    compareOfficialHourToRealtimeDirection(0.4, "STABLE", true),
    "NEUTRAL"
  );
  assert.equal(
    compareOfficialHourToRealtimeDirection(null, "UP", true),
    "UNAVAILABLE"
  );
  assert.equal(
    compareOfficialHourToRealtimeDirection(0.4, "UP", false),
    "UNAVAILABLE"
  );
});

function state(
  asOfMS: number,
  status: MarketShareRealtimeFlowState["status"] = "ALERT"
): MarketShareRealtimeFlowState {
  return {
    version: "REALTIME_FLOW_V2",
    as_of_ms: asOfMS,
    status,
    direction: "UP",
    reason: "test",
    primary_window_sec: 10,
    share_delta_pp: 2,
    stored_driver_count: 0,
    total_driver_count: 0,
    residual_contribution_pp: 0,
    domestic_volume_1s_krw: 100,
    bithumb_volume_1s_krw: 30,
    bithumb_share_1s_pct: 30,
    windows: [],
    drivers: [],
    exchange_drivers: [],
    current_assets: [],
    data_quality: {
      complete_frame: true,
      frame_status: "LIVE",
      retained_frame_count: 120,
      unbounded_debug_payload: "discard",
    },
  };
}

test("localizes ten-second episode status badges", () => {
  assert.equal(realtimeFlowEpisodeStatusLabel("WATCH"), "관찰");
  assert.equal(realtimeFlowEpisodeStatusLabel("ALERT"), "경고");
});

test("uses distinct tones for rising and falling episode cards", () => {
  assert.equal(realtimeFlowEpisodeDirectionTone("UP"), "profit");
  assert.equal(realtimeFlowEpisodeDirectionTone("DOWN"), "loss");
});

test("previews ten episodes before expanding the compact event list", () => {
  const episodes = Array.from({ length: 12 }, (_, index) => index + 1);
  assert.deepEqual(
    previewRealtimeFlowEpisodes(episodes, false),
    episodes.slice(0, 10)
  );
  assert.deepEqual(previewRealtimeFlowEpisodes(episodes, true), episodes);
});

const completeWindow: MarketShareRealtimeFlowWindow = {
  duration_sec: 10,
  status: "COMPLETE",
  current_share_pct: 32,
  previous_share_pct: 30,
  share_delta_pp: 2,
  current_volume_krw: 1_000,
  previous_volume_krw: 900,
  threshold_pp: 1.5,
};

function tenSecondState(
  asOfMS: number,
  deltaPP: number,
  primaryWindowSec = 10
): MarketShareRealtimeFlowState {
  const result = state(asOfMS, Math.abs(deltaPP) >= 8 ? "WATCH" : "NORMAL");
  result.direction = deltaPP > 0 ? "UP" : deltaPP < 0 ? "DOWN" : "STABLE";
  result.primary_window_sec = primaryWindowSec;
  result.share_delta_pp = deltaPP;
  result.windows = [
    {
      ...completeWindow,
      previous_share_pct: 30,
      current_share_pct: 30 + deltaPP,
      share_delta_pp: deltaPP,
      current_volume_krw: 150_000_000,
      previous_volume_krw: 140_000_000,
      threshold_pp: 8,
    },
  ];
  const drivers = [
    {
      rank: 1,
      asset: "XRP",
      contribution_pp: deltaPP / 2,
      bithumb_volume_change_krw: 1,
      competitor_volume_change_krw: 1,
      previous_bithumb_volume_krw: 1,
      current_bithumb_volume_krw: 2,
      previous_competitor_volume_krw: 1,
      current_competitor_volume_krw: 2,
      bithumb_buy_pct: 50,
      top_competitor: "upbit",
      top_competitor_volume_krw: 2,
      exchange_impacts: [],
    },
  ];
  result.ten_second_attribution = {
    window_sec: 10,
    stored_driver_count: 1,
    total_driver_count: 1,
    residual_contribution_pp: deltaPP / 2,
    drivers,
    exchange_drivers: [],
  };
  if (primaryWindowSec === 10) result.drivers = drivers;
  return result;
}

test("describes the server's multi-window direction independently from the 10-second change", () => {
  const input = tenSecondState(1788525090000, 1.2, 60);
  input.status = "ALERT";
  input.direction = "DOWN";
  input.windows.push(
    { ...completeWindow, duration_sec: 30, share_delta_pp: -7 },
    { ...completeWindow, duration_sec: 60, share_delta_pp: -6 }
  );
  const sample = realtimeFlowTenSecondSample(input);
  assert.equal(sample?.direction, "UP");
  assert.match(
    realtimeFlowSignalHeadline(input, input.status, sample!.direction),
    /2개 이상 구간에서 하락 폭이 기준을 넘었습니다/
  );
  assert.equal(
    compareOfficialHourToRealtimeDirection(0.5, sample!.direction, true),
    "SAME"
  );
  input.status = "WATCH";
  assert.match(
    realtimeFlowSignalHeadline(input, input.status, sample!.direction),
    /1개 구간에서 하락 폭이 기준을 넘었습니다/
  );
  assert.match(
    realtimeFlowSignalHeadline(input, "DEGRADED", sample!.direction),
    /새 판정은 보류/
  );
});

test("shows only complete live realtime-flow values", () => {
  const live = state(2_000);
  assert.equal(realtimeFlowValuesAreVisible(live), true);
  assert.equal(realtimeFlowWindowValuesAreVisible(live, completeWindow), true);

  const partial = { ...completeWindow, status: "PARTIAL" };
  assert.equal(realtimeFlowWindowValuesAreVisible(live, partial), false);
  assert.equal(realtimeFlowValuesAreVisible(state(2_000, "DEGRADED")), false);

  const staleFrame = state(2_000);
  staleFrame.data_quality = { complete_frame: true, frame_status: "STALE" };
  assert.equal(realtimeFlowValuesAreVisible(staleFrame), false);
});

test("keeps the share of a partly observed window, marked by its status", () => {
  const live = state(2_000);
  const partial = {
    ...completeWindow,
    status: "PARTIAL",
    share_delta_pp: null,
    previous_share_pct: null,
  };
  assert.equal(realtimeFlowWindowObserved(live, partial), true);
  assert.equal(realtimeFlowWindowValuesAreVisible(live, partial), false);
  // A window without a measured share is not shown, and no share is made up.
  assert.equal(
    realtimeFlowWindowObserved(live, { ...partial, current_share_pct: null }),
    false
  );
  assert.equal(realtimeFlowWindowObserved(null, completeWindow), false);
});

test("does not hide live 10-second flow behind partial rolling coverage", () => {
  const tick = {
    ws_realtime_complete: true,
    ws_status: "LIVE" as const,
    coverage_status: "PARTIAL" as const,
  };
  assert.equal(realtimeFlowTransportIsLive(tick, "live"), true);
  assert.equal(realtimeFlowTransportIsLive(tick, "stale"), false);
  assert.equal(
    realtimeFlowTransportIsLive({ ...tick, ws_status: "DEGRADED" }, "live"),
    false
  );
});

test("hides stale ten-level orderbook derivatives", () => {
  assert.equal(
    realtimeFlowOrderbookValuesAreVisible({
      status: "STALE",
      age_sec: 15,
      imbalance: 0.4,
      spread_bps: 3,
      bid_depth_krw: 100,
      ask_depth_krw: 50,
      buy_slippage_bps: 1,
      sell_slippage_bps: 2,
    }),
    false
  );
  assert.equal(
    realtimeFlowOrderbookValuesAreVisible({
      status: "LIVE",
      age_sec: 1,
      imbalance: 0.4,
      spread_bps: 3,
      bid_depth_krw: 100,
      ask_depth_krw: 50,
      buy_slippage_bps: 1,
      sell_slippage_bps: 2,
    }),
    true
  );
});

test("extracts only a complete live 10-second comparison", () => {
  const live = tenSecondState(10_000, 8.5);
  assert.equal(realtimeFlowTenSecondWindow(live)?.duration_sec, 10);

  live.windows[0] = { ...live.windows[0]!, status: "PARTIAL" };
  assert.equal(realtimeFlowTenSecondWindow(live), null);
  assert.equal(realtimeFlowTenSecondWindow(state(20_000, "DEGRADED")), null);
});

test("builds a chronological deduplicated share series within inclusive bounds", () => {
  const first = tenSecondState(10_000, 1);
  first.windows[0]!.current_share_pct = 31.234567;
  const duplicate = tenSecondState(10_000, 9);
  const result = buildRealtimeFlowShareChartSeries(
    {
      items: [
        tenSecondState(30_001, 3),
        tenSecondState(20_000, 2),
        duplicate,
        first,
        tenSecondState(9_999, 1),
        tenSecondState(30_000, 3),
      ],
    },
    10_000,
    30_000
  );

  assert.deepEqual(
    result.points.map(point => point.as_of_ms),
    [10_000, 20_000, 30_000]
  );
  assert.equal(result.points[0]?.current_share_pct, 39);
  assert.deepEqual(
    {
      total: result.total_sample_count,
      used: result.used_sample_count,
      excluded: result.excluded_sample_count,
    },
    { total: 6, used: 3, excluded: 3 }
  );
});

test("builds the recent minute from server-derived one-second shares", () => {
  const flow = state(5_000, "NORMAL");
  flow.recent_share_1s = [
    {
      as_of_ms: 3_000,
      status: "LIVE",
      complete: true,
      domestic_volume_1s_krw: 100,
      bithumb_volume_1s_krw: 32,
      bithumb_share_1s_pct: 32,
      exchange_shares_1s_pct: { bithumb: 32, upbit: 68 },
    },
    {
      as_of_ms: 1_000,
      status: "LIVE",
      complete: true,
      domestic_volume_1s_krw: 100,
      bithumb_volume_1s_krw: 30,
      bithumb_share_1s_pct: 30,
      exchange_shares_1s_pct: { bithumb: 30, upbit: 70 },
    },
    {
      as_of_ms: 2_000,
      status: "LIVE",
      complete: false,
      domestic_volume_1s_krw: 100,
      bithumb_volume_1s_krw: 31,
      bithumb_share_1s_pct: null,
      exchange_shares_1s_pct: {},
    },
    {
      as_of_ms: 5_000,
      status: "LIVE",
      complete: true,
      domestic_volume_1s_krw: 100,
      bithumb_volume_1s_krw: 33,
      bithumb_share_1s_pct: 33,
      exchange_shares_1s_pct: { bithumb: 33, upbit: 67 },
    },
  ];
  const result = buildRealtimeFlowOneSecondShareChartSeries(flow, 1_000, 5_000);
  assert.deepEqual(
    result.points.map(point => point.as_of_ms),
    [1_000, 3_000, 5_000]
  );
  assert.deepEqual(
    result.points.map(point => point.segment_id),
    ["segment-1000", "segment-3000", "segment-5000"]
  );
  assert.deepEqual(result.points.at(-1)?.exchange_shares_pct, {
    bithumb: 33,
    upbit: 67,
  });
  assert.deepEqual(
    {
      total: result.total_sample_count,
      used: result.used_sample_count,
      excluded: result.excluded_sample_count,
    },
    { total: 4, used: 3, excluded: 1 }
  );
});

test("keeps only complete live V2 states with displayable statuses and a 10-second window", () => {
  const normal = tenSecondState(10_000, 1);
  const watch = tenSecondState(20_000, 8);
  const alert = tenSecondState(30_000, 9);
  alert.status = "ALERT";
  const warming = tenSecondState(40_000, 1);
  warming.status = "WARMING";
  const degraded = tenSecondState(50_000, 1);
  degraded.status = "DEGRADED";
  const stale = tenSecondState(60_000, 1);
  stale.data_quality = { complete_frame: true, frame_status: "STALE" };
  const incomplete = tenSecondState(70_000, 1);
  incomplete.data_quality = { complete_frame: false, frame_status: "LIVE" };
  const partialWindow = tenSecondState(80_000, 1);
  partialWindow.windows[0]!.status = "PARTIAL";
  const wrongDuration = tenSecondState(90_000, 1);
  wrongDuration.windows[0]!.duration_sec = 30;
  const wrongVersion = tenSecondState(100_000, 1);
  wrongVersion.version = "REALTIME_FLOW_V1";

  const result = buildRealtimeFlowShareChartSeries(
    {
      items: [
        wrongVersion,
        wrongDuration,
        partialWindow,
        incomplete,
        stale,
        degraded,
        warming,
        alert,
        watch,
        normal,
      ],
    },
    0,
    100_000
  );

  assert.deepEqual(
    result.points.map(point => point.as_of_ms),
    [10_000, 20_000, 30_000]
  );
  assert.equal(result.total_sample_count, 10);
  assert.equal(result.used_sample_count, 3);
  assert.equal(result.excluded_sample_count, 7);
});

test("starts a new deterministic segment only after a gap greater than 15 seconds", () => {
  const result = buildRealtimeFlowShareChartSeries(
    {
      items: [
        tenSecondState(55_001, 1),
        tenSecondState(40_000, 1),
        tenSecondState(25_000, 1),
        tenSecondState(10_000, 1),
      ],
    },
    10_000,
    55_001
  );

  assert.deepEqual(
    result.points.map(point => point.segment_id),
    ["segment-10000", "segment-10000", "segment-10000", "segment-55001"]
  );
});

test("invalid chart bounds exclude every stored sample deterministically", () => {
  const result = buildRealtimeFlowShareChartSeries(
    { items: [tenSecondState(10_000, 1)] },
    20_000,
    10_000
  );

  assert.deepEqual(result, {
    points: [],
    total_sample_count: 1,
    used_sample_count: 0,
    excluded_sample_count: 1,
  });
});

test("keeps 10-second attribution when another window is primary", () => {
  const flow = tenSecondState(10_000, 8.5, 60);
  assert.equal(realtimeFlowTenSecondAttribution(flow)?.window_sec, 10);
  assert.deepEqual(
    realtimeFlowTenSecondAttribution(flow)?.drivers.map(driver => driver.asset),
    ["XRP"]
  );
});

test("filters server episodes without reclassifying their status", () => {
  const baseEpisode = {
    id: "episode-1",
    start_ms: 10_000,
    end_ms: 20_000,
    direction: "UP",
    status: "WATCH",
    observation_count: 2,
    duration_sec: 10,
    net_share_change_pp: 1,
    gross_share_change_pp: 1,
    peak_share_change_pp: 1,
    attributed_sample_count: 0,
    top_asset: "",
    top_asset_contribution_pp: 0,
    top_exchange: "",
    top_exchange_contribution_pp: 0,
    drivers: [],
  };
  const episodes = selectRealtimeFlowEpisodes(
    {
      episodes: [
        baseEpisode,
        {
          ...baseEpisode,
          id: "episode-2",
          start_ms: 30_000,
          end_ms: 40_000,
          status: "ALERT",
        },
        { ...baseEpisode, id: "ignored", direction: "UNKNOWN" },
      ],
    },
    0,
    45_000
  );
  assert.deepEqual(
    episodes.map(episode => [episode.id, episode.status]),
    [
      ["episode-2", "ALERT"],
      ["episode-1", "WATCH"],
    ]
  );
});

test("uses only server-declared missing coverage intervals", () => {
  const intervals = selectRealtimeFlowMissingIntervals(
    {
      coverage_segments: [
        { start_ms: 0, end_ms: 20_000, status: "OBSERVED" },
        { start_ms: 20_000, end_ms: 40_000, status: "MISSING" },
        { start_ms: 40_000, end_ms: 60_000, status: "OBSERVED" },
      ],
    },
    25_000,
    50_000
  );
  assert.deepEqual(intervals, [
    { start_ms: 25_000, end_ms: 40_000, status: "MISSING" },
  ]);
});

test("bounds, sorts and de-duplicates history before React retains it", () => {
  const newest = state(3_000);
  newest.windows = Array.from({ length: 8 }, () => completeWindow);
  newest.drivers = Array.from({ length: 12 }, (_, index) => ({
    rank: index + 1,
    asset: `ASSET${index}`,
    contribution_pp: 1,
    bithumb_volume_change_krw: 1,
    competitor_volume_change_krw: 1,
    previous_bithumb_volume_krw: 1,
    current_bithumb_volume_krw: 2,
    previous_competitor_volume_krw: 1,
    current_competitor_volume_krw: 2,
    bithumb_buy_pct: 50,
    top_competitor: "upbit",
    top_competitor_volume_krw: 2,
    exchange_impacts: Array.from({ length: 9 }, () => ({
      exchange: "upbit",
      contribution_pp: 1,
      volume_change_krw: 1,
      previous_volume_krw: 1,
      current_volume_krw: 2,
      buy_pct: 50,
    })),
  }));

  const result = compactRealtimeFlowHistory(
    { items: [state(1_000), newest, state(3_000), state(2_000)] },
    2
  );
  assert.deepEqual(
    result.map(item => item.as_of_ms),
    [3_000, 2_000]
  );
  assert.equal(result[0]?.windows.length, 3);
  assert.equal(result[0]?.drivers.length, 8);
  assert.equal(result[0]?.drivers[0]?.exchange_impacts.length, 5);
  assert.equal(
    "unbounded_debug_payload" in (result[0]?.data_quality ?? {}),
    false
  );
});

test("builds a normalized and server-bounded history query", () => {
  assert.equal(
    buildRealtimeFlowHistoryPath({
      fromMS: 1_000.9,
      toMS: 2_000.2,
      status: "ALERT",
      asset: " xrp ",
      exchange: " UPBIT ",
      limit: 5_000,
    }),
    "/api/market-share/realtime-history?from_ms=1000&to_ms=2000&status=ALERT&asset=XRP&exchange=upbit&limit=1000"
  );
});

test("anchors history ranges to the shared backend tick time", () => {
  assert.deepEqual(realtimeFlowHistoryRange(2_000.1), {
    fromMS: 200_000,
    toMS: 2_000_000,
  });
  assert.deepEqual(
    realtimeFlowHistoryRange(2_009.9),
    realtimeFlowHistoryRange(2_000.1)
  );
  assert.equal(realtimeFlowHistoryRange(0), null);
});

test("requests only unseen realtime-flow rows during a live ten-second advance", () => {
  const range = { fromMS: 1_000_000, toMS: 2_800_000 };
  const retained = [state(2_790_000), state(2_780_000)];
  assert.deepEqual(realtimeFlowHistoryRequest(retained, range, 2_790_000), {
    fromMS: 2_790_001,
    toMS: 2_800_000,
    limit: 3,
    incremental: true,
  });
  assert.deepEqual(realtimeFlowHistoryRequest(retained, range, 2_750_000), {
    fromMS: 1_000_000,
    toMS: 2_800_000,
    limit: 200,
    incremental: false,
  });
});

test("merges incremental history without losing the retained thirty-minute window", () => {
  const range = { fromMS: 1_000_000, toMS: 2_800_000 };
  const merged = mergeRealtimeFlowHistory(
    { items: [state(2_800_000), state(2_790_000)] },
    [state(2_790_000), state(2_780_000), state(900_000)],
    range,
    true
  );
  assert.deepEqual(
    merged.map(item => item.as_of_ms),
    [2_800_000, 2_790_000, 2_780_000]
  );
});

 test("same ten-second boundary never requests an inverted history range", () => {
 const range = {fromMS: 1_000_000, toMS: 2_800_000};
 assert.deepEqual(realtimeFlowHistoryRequest([state(range.toMS)], range, range.toMS), {fromMS: range.toMS, toMS: range.toMS, limit: 2, incremental: true});
 });
