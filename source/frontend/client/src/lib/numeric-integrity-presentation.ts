import { marketShareWindowLabel } from "./market-share-labels.ts";

export type NumericIntegrityReport = {
  status: "PASS" | "FAIL" | "UNAVAILABLE";
  checked_scope: string;
  market_scope: string;
  window_sec: number;
  check_count: number;
  comparison_checked: boolean;
  issues: Array<{ code: string; field: string; message: string }>;
};

/** Render the server's scoped verdict; do not recalculate market data here. */
export function numericIntegrityPresentation(
  report?: NumericIntegrityReport | null
) {
  const knownReport =
    report?.checked_scope === "DOMESTIC_ANALYSIS_ARITHMETIC" &&
    (report.market_scope === "KRW" ||
      report.market_scope === "ALL_DOMESTIC_CONVERTED") &&
    Number.isInteger(report.window_sec) &&
    report.window_sec > 0 &&
    Number.isInteger(report.check_count) &&
    report.check_count > 0 &&
    Array.isArray(report.issues);
  const status =
    report?.status === "FAIL"
      ? "FAIL"
      : knownReport && report.status === "PASS" && report.issues.length === 0
        ? "PASS"
        : "UNAVAILABLE";

  return {
    status,
    label:
      status === "PASS"
        ? "수치 계산 일치"
        : status === "FAIL"
          ? "수치 확인 필요"
          : "계산 자료 대기",
    className:
      status === "PASS"
        ? "text-profit"
        : status === "FAIL"
          ? "text-loss"
          : "text-muted-foreground",
    scope:
      report?.market_scope === "KRW"
        ? "국내 5개 거래소 원화 마켓"
        : report?.market_scope === "ALL_DOMESTIC_CONVERTED"
          ? "국내 5개 거래소 원화 환산"
          : "국내 거래소 · 시장 범위 확인 중",
    period:
      knownReport && report
        ? `최근 ${marketShareWindowLabel(report.window_sec)}`
        : "기간 확인 중",
    count: knownReport && report ? report.check_count : null,
    comparison: !!(knownReport && report?.comparison_checked),
    issues: Array.isArray(report?.issues)
      ? [...new Set(report.issues.map(issue => issue.message))]
      : [],
  };
}
