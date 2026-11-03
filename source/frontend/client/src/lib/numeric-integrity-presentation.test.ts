import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  numericIntegrityPresentation,
  type NumericIntegrityReport,
} from "./numeric-integrity-presentation.ts";

const report: NumericIntegrityReport = {
  status: "PASS",
  checked_scope: "DOMESTIC_ANALYSIS_ARITHMETIC",
  market_scope: "KRW",
  window_sec: 60,
  check_count: 33,
  comparison_checked: true,
  issues: [],
};

test("only a real scoped nonempty server check displays arithmetic agreement", () => {
  const state = numericIntegrityPresentation(report);
  assert.equal(state.label, "수치 계산 일치");
  assert.equal(state.scope, "국내 5개 거래소 원화 마켓");
  assert.equal(state.period, "최근 1분");
  assert.equal(state.count, 33);
  assert.equal(state.comparison, true);
});

test("missing or malformed checks never display agreement", () => {
  for (const value of [
    undefined,
    null,
    { ...report, check_count: 0 },
    { ...report, window_sec: Number.NaN },
    { ...report, checked_scope: "UNREVIEWED" },
    { ...report, market_scope: "UNKNOWN" },
    { ...report, status: "UNAVAILABLE" as const },
    {
      ...report,
      issues: [{ code: "MISSING", field: "share", message: "자료 대기" }],
    },
  ]) {
    assert.equal(numericIntegrityPresentation(value).label, "계산 자료 대기");
  }
});

test("converted domestic scope keeps its own denominator label", () => {
  assert.equal(
    numericIntegrityPresentation({
      ...report,
      market_scope: "ALL_DOMESTIC_CONVERTED",
    }).scope,
    "국내 5개 거래소 원화 환산"
  );
});

test("failure verdict is preserved and repeated issue text is compacted", () => {
  const issue = {
    code: "MISMATCH",
    field: "share",
    message: "계산이 다릅니다.",
  };
  const state = numericIntegrityPresentation({
    ...report,
    status: "FAIL",
    issues: [issue, { ...issue, field: "total" }],
  });
  assert.equal(state.label, "수치 확인 필요");
  assert.deepEqual(state.issues, [issue.message]);
});

test("unavailable prior comparison is not described as checked", () => {
  assert.equal(
    numericIntegrityPresentation({ ...report, comparison_checked: false })
      .comparison,
    false
  );
});

test("status panel keeps observation completeness separate and does not recalculate", () => {
  const source = readFileSync(
    new URL("../components/NumericIntegrityStatus.tsx", import.meta.url),
    "utf8"
  );
  assert.match(source, /observationComplete === false/);
  assert.match(source, /계산 일치와 자료의 완전성은 별개입니다/);
  assert.match(
    source,
    /원본 거래가 빠졌거나 중복 저장됐는지까지 확인하지는\s+않습니다/
  );
  assert.match(source, /직전 구간 대비 변화는 검사하지 않았습니다/);
  assert.doesNotMatch(
    source,
    /volume_krw|share_pct|\*\s*100|수치 이상 없음|데이터 정상/
  );
});
