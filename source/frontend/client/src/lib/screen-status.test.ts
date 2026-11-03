import assert from "node:assert/strict";
import test from "node:test";

import { gapKind, gapRiskLabel, gapSummaryByPlace } from "./screen-status.ts";

const gap = (
  exchanges: string[],
  duration: number,
  end: number | null,
  reason = "COLLECTOR_OBSERVATION_INCOMPLETE"
) => ({
  start_second: (end ?? 1_000) - duration,
  end_second: end,
  duration_sec: duration,
  affected_exchanges: exchanges,
  reason_codes: [reason],
  observed_volume_at_risk_pct: null,
});

test("counts gaps per exchange and keeps every-exchange gaps apart", () => {
  const summary = gapSummaryByPlace(
    [
      gap(["gopax"], 81, 900),
      gap(["gopax"], 37, 950),
      gap([], 600, 980),
      gap(["bithumb", "gopax"], 19, 990),
      gap(["upbit"], 5, 100),
    ],
    500
  );
  assert.deepEqual(summary, [
    { place: "gopax", count: 3, longestSec: 81 },
    { place: "all", count: 1, longestSec: 600 },
    { place: "bithumb", count: 1, longestSec: 19 },
  ]);
});

test("keeps an open gap and names gap families", () => {
  assert.equal(gapSummaryByPlace([gap(["korbit"], 40, null)], 500)[0].count, 1);
  assert.equal(
    gapKind(["COLLECTOR_OBSERVATION_INCOMPLETE", "WEBSOCKET_DISCONNECTED"]),
    "connection"
  );
  assert.equal(gapKind(["COLLECTOR_HEALTH_JOURNAL_GAP"]), "journal");
  assert.equal(gapKind(["MISSING_TRADE_EVENTS:3"]), "other");
});

test("labels the trading at risk without hiding unknown values", () => {
  assert.equal(
    gapRiskLabel({
      affected_exchanges: ["gopax"],
      observed_volume_at_risk_pct: 0.004,
      end_second: 100,
    }),
    "0.01% 미만"
  );
  assert.equal(
    gapRiskLabel({
      affected_exchanges: ["gopax"],
      observed_volume_at_risk_pct: 0.02,
      end_second: 100,
    }),
    "0.02%"
  );
  // An open gap may still be weighed; a closed one never will be.
  assert.equal(
    gapRiskLabel({
      affected_exchanges: ["gopax"],
      observed_volume_at_risk_pct: null,
      end_second: null,
    }),
    "계산 전"
  );
  assert.equal(
    gapRiskLabel({
      affected_exchanges: ["gopax"],
      observed_volume_at_risk_pct: null,
      end_second: 100,
    }),
    "알 수 없음"
  );
  assert.equal(
    gapRiskLabel({
      affected_exchanges: [],
      observed_volume_at_risk_pct: null,
      end_second: 100,
    }),
    "전체"
  );
  assert.equal(
    gapRiskLabel({
      affected_exchanges: ["upbit"],
      affected_markets: ["KRW-XYZ"],
      observed_volume_at_risk_pct: 0.005,
      end_second: 100,
    }),
    "0.01% 미만 · 마켓 1개"
  );
});
