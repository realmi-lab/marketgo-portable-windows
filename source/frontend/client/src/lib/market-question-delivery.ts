import type { GroundedMarketQuestionResponse } from "./market-share-types.ts";

export interface MarketQuestionEvidenceBindingPresentation {
  legacy: boolean;
  id: string;
  shortID: string;
  asOfMS: number | null;
  createdAtMS: number;
  scopeLabel: string;
}

export interface MarketQuestionExplanationPresentation {
  state:
    | "legacy"
    | "pending"
    | "accepted"
    | "partial"
    | "failed"
    | "timeout"
    | "canceled"
    | "blocked";
  label: string;
  detail: string;
  showProse: boolean;
}

export interface MarketQuestionFulfillmentPresentation {
  state: "legacy" | "fulfilled" | "partial" | "unsupported" | "clarify";
  label: string;
  detail: string;
  missingRequiredCount: number;
}

export function marketQuestionEvidenceBinding(
  response: GroundedMarketQuestionResponse
): MarketQuestionEvidenceBindingPresentation {
  const bundle = response.evidence_bundle;
  if (!bundle) {
    return {
      legacy: true,
      id: "",
      shortID: "기존 단일 응답",
      asOfMS: response.provider.as_of_ms,
      createdAtMS: response.provider.as_of_ms,
      scopeLabel: "기존 응답 범위",
    };
  }
  const id = bundle.bundle_id;
  const shortID = id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id;
  const scopeLabel =
    bundle.scope.status === "EXACT"
      ? "요청 범위와 일치"
      : bundle.scope.status === "SUBSTITUTED_PARTIAL"
        ? "일부 시장 범위로 계산"
        : "시장 범위 확인 필요";
  return {
    legacy: false,
    id,
    shortID,
    asOfMS: Number.isFinite(bundle.time.actual.as_of_ms)
      ? (bundle.time.actual.as_of_ms ?? null)
      : null,
    createdAtMS: bundle.created_at_ms,
    scopeLabel,
  };
}

export function marketQuestionExplanationPresentation(
  response: GroundedMarketQuestionResponse,
  terminalOverride?: "FAILED" | "TIMEOUT" | "CANCELED"
): MarketQuestionExplanationPresentation {
  const orchestration = response.orchestration;
  if (!orchestration) {
    return {
      state: "legacy",
      label: "기존 검증 응답",
      detail: "단일 응답 계약으로 받은 설명입니다.",
      showProse: response.prose.answer.trim().length > 0,
    };
  }
  const status = (
    terminalOverride ?? orchestration.explanation_status
  ).toUpperCase();
  const validation = orchestration.validation_status;
  if (status === "PENDING" || status === "NOT_STARTED") {
    return {
      state: "pending",
      label: "설명 검증 중",
      detail: "정량 결과는 확정됐고, 설명은 검증을 마친 뒤 표시합니다.",
      showProse: false,
    };
  }
  if (status === "TIMEOUT") {
    return {
      state: "timeout",
      label: "설명 시간 초과",
      detail: "설명은 시간 안에 완료되지 않았습니다. 위 계산 결과는 유지됩니다.",
      showProse: false,
    };
  }
  if (status === "CANCELED" || status === "CANCELLED") {
    return {
      state: "canceled",
      label: "설명 취소",
      detail: "설명 요청을 취소했습니다. 위 계산 결과는 유지됩니다.",
      showProse: false,
    };
  }
  if (status === "FAILED") {
    return {
      state: "failed",
      label: "설명 실패",
      detail: "설명을 완료하지 못했습니다. 위 계산 결과는 유지됩니다.",
      showProse: false,
    };
  }
  if (validation === "CONFLICT") {
    return {
      state: "blocked",
      label: "설명 차단",
      detail: "설명이 계산 근거와 충돌해 공개하지 않았습니다.",
      showProse: false,
    };
  }
  if (validation === "UNDETERMINED" || status === "UNVERIFIED") {
    return {
      state: "blocked",
      label: "설명 검증 불가",
      detail: "설명의 의미를 근거와 대조해 확정하지 못해 공개하지 않았습니다.",
      showProse: false,
    };
  }
  if (
    validation === "SUPPORTED" &&
    status === "PARTIAL" &&
    response.prose.answer.trim().length > 0
  ) {
    return {
      state: "partial",
      label: "검증된 부분 설명",
      detail: "검증된 설명만 표시하며, 충족하지 못한 항목은 별도로 남깁니다.",
      showProse: true,
    };
  }
  if (
    validation === "SUPPORTED" &&
    ["ACCEPTED", "READY", "COMPLETE", "COMPLETED"].includes(status) &&
    response.prose.answer.trim().length > 0
  ) {
    return {
      state: "accepted",
      label: "검증된 설명",
      detail: "위 정량 결과와 같은 근거 묶음으로 검증했습니다.",
      showProse: true,
    };
  }
  return {
    state: "failed",
    label: "설명 없음",
    detail: "공개할 수 있는 검증된 설명이 없습니다. 위 계산 결과는 유지됩니다.",
    showProse: false,
  };
}

export function marketQuestionFulfillmentPresentation(
  response: GroundedMarketQuestionResponse
): MarketQuestionFulfillmentPresentation {
  const fulfillment = response.evidence_bundle?.fulfillment;
  if (!fulfillment) {
    return {
      state: "legacy",
      label: "기존 응답",
      detail: "질문 충족 상태가 분리되기 전의 단일 응답입니다.",
      missingRequiredCount: 0,
    };
  }
  const missingRequiredCount = fulfillment.requirements.filter(
    requirement =>
      requirement.required && requirement.status !== "SATISFIED"
  ).length;
  if (
    fulfillment.status === "FULFILLED" &&
    missingRequiredCount === 0
  ) {
    return {
      state: "fulfilled",
      label: "필수 정량 항목 포함",
      detail: "계산 근거에 서버가 확정한 필수 정량 항목이 포함됐습니다.",
      missingRequiredCount: 0,
    };
  }
  if (fulfillment.status === "UNSUPPORTED") {
    return {
      state: "unsupported",
      label: "지원하지 않는 분석",
      detail: "아직 이 질문에 필요한 항목을 계산할 수 없습니다.",
      missingRequiredCount,
    };
  }
  if (fulfillment.status === "NEEDS_CLARIFICATION") {
    return {
      state: "clarify",
      label: "질문 확인 필요",
      detail: "기간·대상·지표 중 확인이 필요한 조건이 있습니다.",
      missingRequiredCount,
    };
  }
  return {
    state: "partial",
    label: "부분 답변",
    detail:
      missingRequiredCount > 0
        ? `필수 항목 ${missingRequiredCount}개가 아직 충족되지 않았습니다.`
        : "질문의 일부만 지원하거나 확인할 수 있습니다.",
    missingRequiredCount,
  };
}
