import assert from "node:assert/strict";
import test from "node:test";

import {
  stableDomain,
  appendLiveHistoryGap,
  appendLiveHistoryPoint,
  compactLiveHistory,
  liveTickExchangeObserved,
  type LiveChartHistoryPoint,
} from "./live-chart.ts";

test("keeps observed venue rows visible when another venue is disconnected", () => {
  const degraded = {
    ws_realtime_complete: false,
    readiness: {
      ready_exchanges: ["upbit", "bithumb", "coinone", "gopax"],
    },
  };

  assert.equal(liveTickExchangeObserved(degraded, "bithumb"), true);
  assert.equal(liveTickExchangeObserved(degraded, "korbit"), false);
  assert.equal(
    liveTickExchangeObserved(
      { ws_realtime_complete: true, readiness: { ready_exchanges: [] } },
      "korbit"
    ),
    true
  );
});

test("a scheduling pause stays on the same live line", () => {
  const result = appendLiveHistoryPoint([{ at: 1_000, share_bithumb: 30 }], {
    at: 7_000,
    share_bithumb: 31,
  });

  assert.deepEqual(result, [
    { at: 1_000, share_bithumb: 30 },
    { at: 7_000, share_bithumb: 31 },
  ]);
});

test("a short analysis pause stays on the same live line", () => {
  assert.deepEqual(
    appendLiveHistoryPoint([{ at: 1_000, share_bithumb: 30 }], {
      at: 4_000,
      share_bithumb: 31,
    }),
    [
      { at: 1_000, share_bithumb: 30 },
      { at: 4_000, share_bithumb: 31 },
    ]
  );
});

test("compaction keeps an existing websocket gap", () => {
  const history: LiveChartHistoryPoint[] = [
    { at: 1_000, share_bithumb: 30 },
    { at: 1_100, share_bithumb: 31 },
    { at: 2_000, gap: 1 },
    { at: 3_000, share_bithumb: 32 },
    { at: 32_000, share_bithumb: 33 },
  ];

  assert.equal(
    compactLiveHistory(history, 32_000).some(row => row.gap === 1),
    true
  );
});

test("a live transport loss inserts one explicit chart gap", () => {
  const history = [{ at: 1_000, share_bithumb: 30 }];
  const withGap = appendLiveHistoryGap(history);

  assert.equal(withGap.at(-1)?.gap, 1);
  assert.equal(appendLiveHistoryGap(withGap), withGap);
  assert.deepEqual(
    appendLiveHistoryPoint(withGap, { at: 2_000, share_bithumb: 31 }),
    [...withGap, { at: 2_000, share_bithumb: 31 }]
  );
});

test("an out-of-order tick cannot draw the chart backwards", () => {
  const history = [{ at: 2_000, share_bithumb: 30 }];
  assert.equal(
    appendLiveHistoryPoint(history, { at: 1_900, share_bithumb: 31 }),
    history
  );
});

test("a live chart's scale holds until the values leave it or shrink well inside", () => {
  // Trading per second from 0: a nice top above the largest value.
  assert.deepEqual(stableDomain(null, [3.2e8, 4.1e8], false), [0, 5e8]);
  assert.deepEqual(stableDomain([0, 5e8], [2.5e8, 4.6e8], false), [0, 5e8]);
  assert.deepEqual(stableDomain([0, 5e8], [5.2e8], false), [0, 1e9]);
  assert.deepEqual(stableDomain([0, 5e8], [1.5e8], false), [0, 2e8]);
  assert.deepEqual(stableDomain([0, 5e8], [], false), [0, 5e8]);
  assert.deepEqual(stableDomain(null, [0, 0], false), [0, 1]);
  // Shares zoom in, and hold while they stay inside and spread enough.
  assert.deepEqual(stableDomain(null, [21.4, 23.9], true), [20, 25]);
  assert.deepEqual(stableDomain([20, 25], [22, 23.5], true), [20, 25]);
  assert.deepEqual(stableDomain([20, 25], [22.9, 23.1], true), [22, 24]);
  assert.deepEqual(stableDomain([20, 25], [19.5, 24], true), [19, 25]);
  assert.deepEqual(stableDomain(null, [99.8, 100], true), [99, 100]);
});
