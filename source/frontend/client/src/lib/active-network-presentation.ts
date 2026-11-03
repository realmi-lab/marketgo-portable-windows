import { formatSignedPercentagePoints } from "./formatters.ts";

export function activeNetworkContribution(
  value: number | null | undefined
): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  return formatSignedPercentagePoints(value);
}

export function activeNetworkPresentation(
  status: string | undefined,
  reason: string | undefined
) {
  if (status === "CONFIRMED") {
    return { label: "비교 자료 확인", detail: "" };
  }
  if (status === "PROVISIONAL") {
    return {
      label: "일부 거래소 비교",
      detail:
        "확인된 거래소 자료로 비교한 값입니다. 국내 전체 거래소의 확정값은 아닙니다.",
    };
  }
  const reasons: Record<string, string> = {
    COMPARISON_WINDOW_INCOMPLETE:
      "현재 구간과 직전 구간의 관측 자료가 모두 확보되지 않았습니다.",
    NO_CURRENT_DOMESTIC_VOLUME:
      "현재 구간의 국내 거래금액이 0원이라 점유율 변화를 계산할 수 없습니다.",
    NO_PREVIOUS_DOMESTIC_VOLUME:
      "직전 구간의 국내 거래금액이 0원이라 점유율 변화를 계산할 수 없습니다.",
    CURRENT_UNRESOLVED_FX: "현재 구간의 원화 환산값을 확인하고 있습니다.",
    PREVIOUS_UNRESOLVED_FX: "직전 구간의 원화 환산값을 확인하고 있습니다.",
  };
  return {
    label: "비교 자료 확인 중",
    detail:
      reasons[reason ?? ""] ??
      "점유율 변화에 필요한 비교 자료를 확인하고 있습니다.",
  };
}
