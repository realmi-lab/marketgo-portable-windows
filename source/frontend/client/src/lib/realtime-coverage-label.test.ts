import assert from "node:assert/strict";
import test from "node:test";
import { realtimeFlowCoverageLabel } from "./market-share-realtime-flow.ts";

test("coverage labels distinguish stored evidence without claiming websocket loss", () => {
  assert.equal(realtimeFlowCoverageLabel("MISSING", "NO_SAMPLE"), "비교 기록 없음");
  assert.equal(realtimeFlowCoverageLabel("MISSING", "OBSERVATION_INCOMPLETE"), "관측 확인 불완전");
  assert.equal(realtimeFlowCoverageLabel("MISSING", "COMPARISON_INCOMPLETE"), "비교 구간 자료 부족");
  assert.equal(realtimeFlowCoverageLabel("MISSING", "SAMPLE_UNUSABLE"), "비교값 확인 불가");
  assert.equal(realtimeFlowCoverageLabel("MISSING"), "실시간 비교 불완전");
  assert.equal(realtimeFlowCoverageLabel("MISSING", "future_reason"), "실시간 비교 불완전");
  assert.equal(realtimeFlowCoverageLabel("OBSERVED"), "직접 관측");
});
