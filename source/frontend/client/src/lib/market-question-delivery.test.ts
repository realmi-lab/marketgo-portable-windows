import assert from "node:assert/strict";
import test from "node:test";

import {
  marketQuestionEvidenceBinding,
  marketQuestionExplanationPresentation,
  marketQuestionFulfillmentPresentation,
} from "./market-question-delivery.ts";
import type { GroundedMarketQuestionResponse } from "./market-share-types.ts";

function stagedResponse(
  explanationStatus: string,
  validationStatus: "SUPPORTED" | "CONFLICT" | "UNDETERMINED",
  fulfillmentStatus: "FULFILLED" | "PARTIAL" = "FULFILLED"
): GroundedMarketQuestionResponse {
  return {
    contract: "GROUNDED_MARKET_QUESTION_V2",
    facts: { evidence_layer: "UI_DELIVERY_FIXTURE" },
    quality: { status: "LIVE" },
    prose: {
      answer: "검증 대상 화면 설명",
      key_points: [],
      caveat: "화면 상태 회귀용",
    },
    provider: { source: "HERMES", as_of_ms: 100 },
    evidence_bundle: {
      bundle_id: "immutable-ui-bundle-123456",
      created_at_ms: 90,
      time: { actual: { start_ms: 1, end_ms: 60, as_of_ms: 100 } },
      scope: { status: "EXACT" },
      question_contract: {
        requirements:
          fulfillmentStatus === "FULFILLED"
            ? [{ required: true, status: "SATISFIED" }]
            : [{ required: true, status: "MISSING" }],
      },
      fulfillment: {
        status: fulfillmentStatus,
        requirements:
          fulfillmentStatus === "FULFILLED"
            ? [{ required: true, status: "SATISFIED" }]
            : [{ required: true, status: "UNAVAILABLE" }],
      },
    },
    orchestration: {
      mode: "STAGED",
      overall_status: fulfillmentStatus,
      quantitative_status: "READY",
      explanation_status: explanationStatus,
      validation_status: validationStatus,
      blocks: [],
      calls: { plan: 0, explanation: 1, repair: 0, total: 1, max_total: 2 },
      timing_ms: {
        request_started: 0,
        planning_completed: 0,
        quantitative_ready: 1,
        explanation_started: 2,
        explanation_completed: 3,
        validation_completed: 4,
        total: 4,
      },
    },
  } as unknown as GroundedMarketQuestionResponse;
}

test("only supported accepted or supported partial explanation is visible", () => {
  assert.equal(
    marketQuestionExplanationPresentation(
      stagedResponse("ACCEPTED", "SUPPORTED")
    ).showProse,
    true
  );
  assert.equal(
    marketQuestionExplanationPresentation(
      stagedResponse("PARTIAL", "SUPPORTED")
    ).showProse,
    true
  );
  for (const [status, validation] of [
    ["PENDING", "SUPPORTED"],
    ["FAILED", "SUPPORTED"],
    ["TIMEOUT", "SUPPORTED"],
    ["CANCELED", "SUPPORTED"],
    ["ACCEPTED", "CONFLICT"],
    ["ACCEPTED", "UNDETERMINED"],
  ] as const) {
    assert.equal(
      marketQuestionExplanationPresentation(
        stagedResponse(status, validation)
      ).showProse,
      false,
      `${status}/${validation}`
    );
  }
});

test("missing required obligations cannot be labeled fulfilled", () => {
  assert.deepEqual(
    marketQuestionFulfillmentPresentation(
      stagedResponse("ACCEPTED", "SUPPORTED", "PARTIAL")
    ),
    {
      state: "partial",
      label: "부분 답변",
      detail: "필수 항목 1개가 아직 충족되지 않았습니다.",
      missingRequiredCount: 1,
    }
  );
});

test("cards and explanation use the immutable bundle identity and actual as-of", () => {
  const binding = marketQuestionEvidenceBinding(
    stagedResponse("ACCEPTED", "SUPPORTED")
  );
  assert.equal(binding.id, "immutable-ui-bundle-123456");
  assert.equal(binding.asOfMS, 100);
  assert.equal(binding.scopeLabel, "요청 범위와 일치");
});
