import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  buildClosedSecondsHistory,
  buildLiveOverviewPoint,
  closedSecondPoint,
} from "./market-share-overview-point.ts";
import { appendLiveHistoryPoint, seedLiveHistory } from "./live-chart.ts";
import { smoothLinePath } from "./chart-curves.ts";
import type { MarketShareRealtimeTick } from "./market-share-types.ts";

function tick(volume: number, at = 100): MarketShareRealtimeTick {
  return {
    generated_at: at,
    window_sec: 10,
    market_scope: "KRW",
    coverage_sec: 10,
    ws_realtime_complete: true,
    summary: { domestic_volume_krw: volume },
    exchanges: [
      {
        exchange: "bithumb",
        volume_krw: volume,
        share_pct: volume > 0 ? 100 : null,
        trade_count: volume > 0 ? 10 : 0,
      },
    ],
  } as MarketShareRealtimeTick;
}

test("confirmed zero turnover advances the graph with zero speeds and absent share", () => {
  const first = buildLiveOverviewPoint(tick(100), "KRW", 10)!;
  const next = buildLiveOverviewPoint(tick(0, 101), "KRW", 10)!;
  const history = appendLiveHistoryPoint([first], next);
  assert.equal(first.rate_bithumb, 10);
  assert.equal(history.length, 2);
  assert.equal(history.at(-1)?.rate_bithumb, 0);
  assert.equal(history.at(-1)?.trades_bithumb, 0);
  assert.equal(history.at(-1)?.share_bithumb, null);
  assert.equal(history.at(-1)?.at, 101000);
  assert.equal(history.at(-1)?.rate_upbit, null);
});

test("unobserved zero values stay unknown and healthy venues remain available", () => {
  const input = tick(0);
  input.ws_realtime_complete = false;
  input.readiness = {
    ready_exchanges: ["bithumb"],
  } as MarketShareRealtimeTick["readiness"];
  input.exchanges.push({
    exchange: "upbit",
    volume_krw: 0,
    share_pct: null,
    trade_count: 0,
    trade_count_share_pct: null,
  });
  const before = JSON.stringify(input);
  const point = buildLiveOverviewPoint(input, "KRW", 10)!;
  assert.equal(point.rate_bithumb, 0);
  assert.equal(point.rate_upbit, null);
  assert.equal(point.trades_upbit, null);
  assert.equal(point.partial, 1);
  assert.equal(JSON.stringify(input), before);
});

test("an incomplete window keeps the server share and its observed-range label together", () => {
  const input = { ...tick(100), window_complete: false };
  const point = buildLiveOverviewPoint(input, "KRW", 10)!;
  assert.equal(point.share_bithumb, input.exchanges[0].share_pct);
  assert.equal(point.partial, 1);
  assert.equal(
    buildLiveOverviewPoint({ ...input, window_complete: true }, "KRW", 10)
      ?.partial,
    undefined
  );
});

test("a degraded older frame without a venue list leaves chart values unknown", () => {
  const input = tick(100);
  input.ws_realtime_complete = false;
  input.readiness = {} as MarketShareRealtimeTick["readiness"];
  const before = JSON.stringify(input);
  const point = buildLiveOverviewPoint(input, "KRW", 10)!;
  assert.equal(point.partial, 1);
  assert.equal(point.share_bithumb, null);
  assert.equal(point.rate_bithumb, null);
  assert.equal(point.trades_bithumb, null);
  assert.equal(JSON.stringify(input), before);
});

test("period, scope, missing coverage and invalid row values remain guarded", () => {
  assert.equal(buildLiveOverviewPoint(null, "KRW", 10), null);
  assert.equal(buildLiveOverviewPoint(tick(100), "KRW", 60), null);
  assert.equal(
    buildLiveOverviewPoint(tick(100), "ALL_DOMESTIC_CONVERTED", 10),
    null
  );
  for (const coverage of [0, -1, NaN, Infinity]) {
    assert.equal(
      buildLiveOverviewPoint(
        { ...tick(100), coverage_sec: coverage },
        "KRW",
        10
      ),
      null
    );
  }
  for (const total of [-1, NaN, Infinity]) {
    assert.equal(buildLiveOverviewPoint(tick(total), "KRW", 10), null);
  }
  const invalid = tick(100);
  invalid.exchanges[0].volume_krw = NaN;
  invalid.exchanges[0].trade_count = -1;
  const point = buildLiveOverviewPoint(invalid, "KRW", 10)!;
  assert.equal(point.rate_bithumb, null);
  assert.equal(point.trades_bithumb, null);
});

test("the actual live chart effect admits zero points only while connected", () => {
  // The old page and the dashboard's live screen share one history hook.
  const hook = readFileSync(
    new URL("../hooks/useLiveOverviewHistory.ts", import.meta.url),
    "utf8"
  );
  assert.match(hook, /if \(connection !== "live"\) return/);
  assert.match(
    hook,
    /buildLiveOverviewPoint\(tick, marketScope, requestedWindowSec\)/
  );
  assert.doesNotMatch(hook, /domestic_volume_krw <= 0/);
  assert.match(hook, /appendLiveHistoryGap\(current\.history\)/);
  for (const user of [
    "../pages/MarketShare.tsx",
    "../screens/LiveScreen.tsx",
  ]) {
    assert.match(
      readFileSync(new URL(user, import.meta.url), "utf8"),
      /useLiveOverviewHistory\(\{/,
      user
    );
  }
});

test("draws the window ending at the last closed second, timed at its end", () => {
  // The tick's own window holds the open second; the closed one does not dip.
  const input = {
    ...tick(100, 100.4),
    closed_second: {
      as_of_second: 99,
      window_complete: false,
      coverage_sec: 5,
      exchanges: [
        {
          exchange: "bithumb",
          volume_krw: 50,
          share_pct: 40,
          trade_count: 5,
          trade_count_share_pct: 40,
        },
      ],
    },
  };
  const point = buildLiveOverviewPoint(input, "KRW", 10)!;
  assert.equal(point.at, 100_000);
  assert.equal(point.rate_bithumb, 10); // 50 KRW over the 5 seconds observed
  assert.equal(point.trades_bithumb, 1);
  assert.equal(point.share_bithumb, 40);
  assert.equal(point.partial, 1);
  assert.equal(closedSecondPoint(input.closed_second, 10)?.partial, 1);
  assert.equal(point.rate_upbit, null); // no row: not observed
  // Two ticks in one second give one point, the later one.
  const later = buildLiveOverviewPoint(
    { ...input, generated_at: 100.9 },
    "KRW",
    10
  )!;
  assert.equal(appendLiveHistoryPoint([point], later).length, 1);
  assert.equal(
    closedSecondPoint({ ...input.closed_second, coverage_sec: 0 }, 10),
    null
  );
});

test("a chart opens with the recent seconds older than its first live point", () => {
  const seed = (at: number) => ({ at, rate_bithumb: at });
  const live = [{ at: 5_000, rate_bithumb: 1 }];
  const seeded = seedLiveHistory(live, [seed(3_000), seed(4_000), seed(5_000)]);
  assert.deepEqual(
    seeded.map(point => point.at),
    [3_000, 4_000, 5_000]
  );
  assert.equal(seeded.at(-1)?.rate_bithumb, 1); // the live point stays
  assert.equal(seedLiveHistory(live, [seed(6_000)]), live);
});

test("recent closed windows preserve an unobserved stretch as a chart break", () => {
  const seconds = [90, 91, 92].map((at, index) => ({
    as_of_second: at,
    coverage_sec: index === 1 ? 0 : 1,
    window_complete: false,
    exchanges: [
      {
        exchange: "bithumb",
        volume_krw: 10 + index,
        share_pct: 40 + index,
        trade_count: 1,
        trade_count_share_pct: 100,
      },
    ],
  }));
  const before = JSON.stringify(seconds);
  const history = seedLiveHistory([], buildClosedSecondsHistory(seconds, 10));
  assert.deepEqual(
    history.map(point => point.at),
    [91_000, 92_000, 93_000]
  );
  assert.equal(history[1].gap, 1);
  assert.equal(history[1].share_bithumb, undefined);
  assert.equal(
    smoothLinePath(
      history.map(point => ({ x: point.at, y: point.share_bithumb ?? null }))
    ),
    ""
  );
  assert.equal(JSON.stringify(seconds), before);
});

test("a received unobserved closed second breaks the live line", () => {
  const closed = (at: number, coverage = 10) => ({
    as_of_second: at,
    window_complete: coverage === 10,
    coverage_sec: coverage,
    exchanges: tick(100).exchanges,
  });
  const inputs = [closed(100), closed(101, 0), closed(102)];
  let history: ReturnType<typeof buildClosedSecondsHistory> = [];
  for (const second of inputs) {
    const point = buildLiveOverviewPoint(
      { ...tick(100), closed_second: second },
      "KRW",
      10
    );
    if (point) history = appendLiveHistoryPoint(history, point);
  }
  assert.equal(history.length, 3);
  assert.equal(history[1].gap, 1);
  assert.equal(history[1].share_bithumb, undefined);
  assert.equal(
    smoothLinePath(
      history.map(point => ({ x: point.at, y: point.share_bithumb ?? null }))
    ),
    ""
  );
});

test("closed-second timestamps and window length cannot poison chart coordinates or rates", () => {
  const closed = {
    as_of_second: 100,
    coverage_sec: 10,
    window_complete: true,
    exchanges: tick(100).exchanges,
  };
  for (const asOf of [NaN, Infinity, -1, 0.5]) {
    assert.equal(
      closedSecondPoint({ ...closed, as_of_second: asOf }, 10),
      null
    );
    assert.deepEqual(
      buildClosedSecondsHistory([{ ...closed, as_of_second: asOf }], 10),
      []
    );
  }
  for (const window of [0, -1, NaN, Infinity]) {
    assert.equal(closedSecondPoint(closed, window), null);
    assert.deepEqual(buildClosedSecondsHistory([closed], window), []);
  }
});

test("legacy ticks without a summary or finite millisecond timestamp stay absent", () => {
  for (const summary of [undefined, null]) {
    const input = {
      ...tick(100),
      summary,
    } as unknown as MarketShareRealtimeTick;
    assert.equal(buildLiveOverviewPoint(input, "KRW", 10), null);
  }
  assert.equal(
    buildLiveOverviewPoint(tick(100, Number.MAX_VALUE), "KRW", 10),
    null
  );
});
