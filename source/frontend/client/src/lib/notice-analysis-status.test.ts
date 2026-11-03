import assert from "node:assert/strict";
import test from "node:test";

import {
  noticeAnalysisStatusDetail,
  noticeAnalysisStatusLabel,
  noticeSummaryDisplayText,
} from "./notice-analysis-status.ts";

test("labels domestic on-demand indexing without claiming background LLM analysis", () => {
  assert.equal(
    noticeAnalysisStatusLabel(
      "ANALYSIS_NOT_REQUIRED",
      "ON_DEMAND_RAG",
      "HERMES_RAW_DETAIL"
    ),
    "공식 원문 확인 가능"
  );
  assert.match(
    noticeAnalysisStatusDetail(
      "ANALYSIS_NOT_REQUIRED",
      "ON_DEMAND_RAG",
      "HERMES_RAW_DETAIL"
    ) ?? "",
    /저장된 공식 원문/
  );
});

test("distinguishes durable official evidence from a missing detail failure", () => {
  assert.equal(
    noticeAnalysisStatusLabel("ANALYSIS_FAILED", "", "HERMES_RAW_DETAIL"),
    "공식 자료 확보 · AI 재분석 대기"
  );
  assert.match(
    noticeAnalysisStatusDetail("ANALYSIS_FAILED", "", "HERMES_RAW_DETAIL") ??
      "",
    /질문 근거/
  );
  assert.equal(
    noticeAnalysisStatusLabel("ANALYSIS_FAILED", "", "HERMES_RAW_DETAIL_EMPTY"),
    "공식 상세 내용 없음"
  );
});

test("shows classified transient retries as waiting instead of failed", () => {
  assert.equal(
    noticeAnalysisStatusLabel(
      "ANALYSIS_DEFERRED_QUOTA",
      "RETRY_TIMEOUT_OR_PREEMPTION",
      "HERMES_RAW_DETAIL"
    ),
    "AI 분석 재시도 대기"
  );
  assert.match(
    noticeAnalysisStatusDetail(
      "ANALYSIS_DEFERRED_QUOTA",
      "RETRY_TIMEOUT_OR_PREEMPTION",
      "HERMES_RAW_DETAIL"
    ) ?? "",
    /공식 본문·이미지는 확보/
  );
  assert.equal(
    noticeAnalysisStatusLabel(
      "ANALYSIS_DEFERRED_QUOTA",
      "RETRY_RUNTIME_UNAVAILABLE",
      "HERMES_RAW_DETAIL"
    ),
    "AI 분석 일시 지연"
  );
});

test("labels rejected model output without treating official evidence as bad", () => {
  assert.equal(
    noticeAnalysisStatusLabel(
      "ANALYSIS_FAILED",
      "ANALYSIS_VALIDATION_REJECTED",
      "HERMES_RAW_DETAIL"
    ),
    "원문 불일치 · 재분석 예정"
  );
  assert.match(
    noticeAnalysisStatusDetail(
      "ANALYSIS_FAILED",
      "ANALYSIS_VALIDATION_REJECTED",
      "HERMES_RAW_DETAIL"
    ) ?? "",
    /모델 결과를 저장하지 않았습니다/
  );
});

test("stored notice summaries read 정보 없음 for a time the notice did not state", () => {
  assert.equal(
    noticeSummaryDisplayText(
      "중단·재개 일정: 2026-09-29 19:00 ~ 종료 시각 미확보"
    ),
    "중단·재개 일정: 2026-09-29 19:00 ~ 종료 시각 정보 없음"
  );
  assert.equal(
    noticeSummaryDisplayText("시작 시각 미확보 ~ 2026-10-01 18:00"),
    "시작 시각 정보 없음 ~ 2026-10-01 18:00"
  );
  assert.equal(
    noticeSummaryDisplayText("기간·조치 시각 미확보"),
    "기간·조치 시각 정보 없음"
  );
  assert.equal(noticeSummaryDisplayText("변경 없음"), "변경 없음");
});
