import assert from "node:assert/strict";
import test from "node:test";
import type { IndexDayResponse } from "./index-day.ts";
import { indexHasConfirmedAmount, indexStatus } from "./index-status.ts";

const payload = (status: string, rowStatus = status) => ({
  status, rows: [{ status: rowStatus }],
  domestic: { totals: { domestic: { total: 0, partial: false } } },
}) as IndexDayResponse;

test("index status preserves partial, stale and failed evidence during refresh", () => {
  assert.equal(indexStatus(payload("ROLLING_PARTIAL", "ROLLING_INCOMPLETE")).label, "일부 자료");
  assert.equal(indexStatus(payload("ROLLING_PARTIAL", "ROLLING_COLLECTING")).label, "시간봉 수집 중");
  assert.equal(indexStatus(payload("ROLLING_PARTIAL", "ROLLING_STALE")).label, "갱신 지연");
  assert.match(indexStatus(payload("COMPLETE"), "timeout").detail, /마지막으로 받은 자료/);
});

test("index status distinguishes missing, initial loading and an observed zero", () => {
  assert.equal(indexStatus(null).label, "자료 확인 중");
  assert.equal(indexStatus({ ...payload("COMPLETE"), rows: [] }).label, "자료 없음");
  assert.equal(indexStatus(payload("COMPLETE")).label, "공식 시간봉");
  assert.equal(indexStatus(null, "network").label, "조회 지연");
  const missing = payload("PARTIAL", "MISSING");
  missing.domestic!.totals.domestic.unavailable = true;
  assert.equal(indexHasConfirmedAmount(missing), false);
  assert.equal(indexStatus(missing).label, "자료 없음");
  missing.rows[0].volume_krw = 0;
  assert.equal(indexHasConfirmedAmount(missing), true);
  assert.equal(indexStatus(missing).label, "일부 자료");
});
