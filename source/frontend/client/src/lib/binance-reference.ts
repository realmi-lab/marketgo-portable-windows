/** A unit price, not a compact trading amount. Preserve sub-won significant digits. */
export function formatReferenceKrwPrice(
  value: number | null | undefined
): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return `₩${value.toLocaleString("ko-KR", { maximumSignificantDigits: 8 })}`;
}

export type BinancePremiumIdentityStatus =
  | "VERIFIED_EXACT_MARKETS"
  | "SYMBOL_MATCH_UNVERIFIED"
  | "EXACT_MARKET_PROOF_MISSING"
  | "CONTRACT_IDENTITY_CONFLICT"
  | "BITHUMB_KRW_MARKET_UNAVAILABLE"
  | "AMBIGUOUS_BITHUMB_KRW_MARKET";

export type BinancePremiumStatus =
  | "LIVE"
  | "STALE_BINANCE_SUMMARY"
  | "STALE_BINANCE_CLOSE_TIME"
  | "IDENTITY_NOT_ELIGIBLE"
  | "BITHUMB_PRICE_UNAVAILABLE"
  | "BITHUMB_PRICE_QUARANTINED";

export function premiumIdentityStatusLabel(
  status: BinancePremiumIdentityStatus
): string {
  switch (status) {
    case "VERIFIED_EXACT_MARKETS":
      return "동일 자산 검증";
    case "SYMBOL_MATCH_UNVERIFIED":
      return "심볼 매칭·미검증";
    case "EXACT_MARKET_PROOF_MISSING":
      return "정확 시장 증빙 없음";
    case "CONTRACT_IDENTITY_CONFLICT":
      return "자산 식별 충돌";
    case "AMBIGUOUS_BITHUMB_KRW_MARKET":
      return "빗썸 시장 식별 모호";
    default:
      return "빗썸 KRW 시장 없음";
  }
}

export function premiumIdentityAllowsReference(
  status: BinancePremiumIdentityStatus
): boolean {
  return (
    status === "VERIFIED_EXACT_MARKETS" ||
    status === "SYMBOL_MATCH_UNVERIFIED" ||
    status === "EXACT_MARKET_PROOF_MISSING"
  );
}

export function premiumStatusLabel(
  status: BinancePremiumStatus,
  displayedPremium: number | null
): string {
  switch (status) {
    case "LIVE":
      return displayedPremium !== null && Number.isFinite(displayedPremium)
        ? "가격 차이 확인"
        : "원화 가격 차이 확인 대기";
    case "STALE_BINANCE_SUMMARY":
      return "Binance 갱신 지연";
    case "STALE_BINANCE_CLOSE_TIME":
      return "Binance 측정시각 만료";
    case "IDENTITY_NOT_ELIGIBLE":
      return "자산 식별 제외";
    case "BITHUMB_PRICE_QUARANTINED":
      return "빗썸 이상 가격 제외";
    default:
      return "빗썸 실시간가 없음";
  }
}

export function referencePremiumBps(
  premiumBps: number | null,
  identityStatus: BinancePremiumIdentityStatus,
  premiumStatus: BinancePremiumStatus
): number | null {
  if (
    premiumStatus !== "LIVE" ||
    !premiumIdentityAllowsReference(identityStatus) ||
    premiumBps === null ||
    !Number.isFinite(premiumBps)
  ) {
    return null;
  }
  return premiumBps;
}
