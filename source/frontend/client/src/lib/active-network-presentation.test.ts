import assert from "node:assert/strict";
import test from "node:test";
import {
  activeNetworkContribution,
  activeNetworkPresentation,
} from "./active-network-presentation.ts";

test("network contributions distinguish measured zero, small nonzero and missing", () => {
  assert.equal(activeNetworkContribution(0), "변화 없음");
  assert.equal(activeNetworkContribution(-0), "변화 없음");
  // Two decimals, three when two would show 0.00 (the 2026-09-29 rule).
  assert.equal(activeNetworkContribution(0.000001), "0.001%p 미만 상승");
  assert.equal(activeNetworkContribution(-0.000001), "0.001%p 미만 하락");
  assert.equal(activeNetworkContribution(-0.001), "-0.001%p");
  assert.equal(activeNetworkContribution(0.001), "+0.001%p");
  assert.equal(activeNetworkContribution(-1.25), "-1.25%p");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(activeNetworkContribution(value), "—");
  }
});

test("network badges report provided comparison state rather than unconditional completion", () => {
  assert.equal(
    activeNetworkPresentation("CONFIRMED", "").label,
    "비교 자료 확인"
  );
  const partial = activeNetworkPresentation("PROVISIONAL", "");
  assert.equal(partial.label, "일부 거래소 비교");
  assert.match(partial.detail, /확인된 거래소 자료/);
  assert.match(
    activeNetworkPresentation("UNAVAILABLE", "COMPARISON_WINDOW_INCOMPLETE")
      .detail,
    /모두 확보되지/
  );
  assert.match(
    activeNetworkPresentation("UNAVAILABLE", "NO_PREVIOUS_DOMESTIC_VOLUME")
      .detail,
    /직전 구간.*0원/
  );
  for (const status of [undefined, "WARMING", "PARTIAL", "UNAVAILABLE"]) {
    assert.equal(
      activeNetworkPresentation(status, "unknown").label,
      "비교 자료 확인 중"
    );
    assert.doesNotMatch(
      activeNetworkPresentation(status, "unknown").detail,
      /unknown|완료/
    );
  }
});
