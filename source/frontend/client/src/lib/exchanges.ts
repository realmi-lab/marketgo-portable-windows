/**
 * exchanges.ts — 거래소 식별자·라벨·분류의 단일 소스.
 *
 * 이전엔 display-labels.ts, abi2-adapters.ts(normalizeExchangeName),
 * exchange-fees.ts(DOMESTIC_FEE_LABELS), trade-pricing.ts(KRW_VENUES),
 * 그리고 pages/Domestic.tsx(EXCHANGE_LABELS) 에 각각 분산·중복 정의돼 있었다.
 * 모두 여기로 통합한다.
 */

/** 정규화된(canonical) 소문자 거래소 ID */
export const EXCHANGE_IDS = {
  UPBIT: "upbit",
  BITHUMB: "bithumb",
  COINONE: "coinone",
  KORBIT: "korbit",
  GOPAX: "gopax",
  BINANCE_SPOT: "binance_spot",
  BINANCE_FUTURES: "binance_futures",
  BINANCE_COINM: "binance_coinm",
  OKX_SPOT: "okx_spot",
  OKX_FUTURES: "okx_futures",
  BYBIT_SPOT: "bybit_spot",
  BYBIT_FUTURES: "bybit_futures",
  HTX_SPOT: "htx_spot",
  HTX_FUTURES: "htx_futures",
} as const;

export type ExchangeId = (typeof EXCHANGE_IDS)[keyof typeof EXCHANGE_IDS];

/** 별칭 → canonical 매핑 (binance_usdm/usdtm 등 변종 흡수) */
const ALIAS_TO_CANONICAL: Record<string, string> = {
  binance: "binance_spot",
  binance_usdm: "binance_futures",
  binance_usdtm: "binance_futures",
  okx: "okx_spot",
  okx_swap: "okx_futures",
  bybit: "bybit_spot",
  bybit_linear: "bybit_futures",
  htx: "htx_spot",
  htx_swap: "htx_futures",
  huobi: "htx_spot",
  huobi_swap: "htx_futures",
};

export const KORBIT_DISPLAY_NAME = import.meta.env?.VITE_PORTABLE_DESKTOP === "true" ? "디지털엑스" : "코빗";

/** 거래소 ID → 표시명 */
const LABEL_MAP: Record<string, string> = {
  // Domestic venues use their Korean names everywhere, so one view never
  // mixes "Upbit" and "업비트".
  upbit: "업비트",
  bithumb: "빗썸",
  coinone: "코인원",
  korbit: KORBIT_DISPLAY_NAME,
  gopax: "고팍스",
  binance_spot: "Binance Spot",
  binance_futures: "Binance USD-M Perp",
  binance_coinm: "Binance COIN-M",
  okx_spot: "OKX Spot",
  okx_futures: "OKX USDT Swap",
  bybit_spot: "Bybit Spot",
  bybit_futures: "Bybit USDT Perp",
  htx_spot: "HTX Spot",
  htx_futures: "HTX USDT Swap",
};

/** 입력 문자열을 canonical 소문자 ID로 정규화 */
export function canonicalExchangeId(exchange: string): string {
  const key = String(exchange || "")
    .toLowerCase()
    .replace(/[-\s]/g, "_");
  return ALIAS_TO_CANONICAL[key] ?? key;
}

/** 입력 문자열(대소문자/별칭 무관) → 표시명 */
export function exchangeLabel(exchange: string): string {
  const id = canonicalExchangeId(exchange);
  return LABEL_MAP[id] ?? exchange;
}

/** Korean names read in a sentence; venues without one keep exchangeLabel. */
const KOREAN_NAME_MAP: Record<string, string> = {
  binance_spot: "바이낸스",
};

/** 문장 안 거래소 이름: "Bithumb 비중"이 아니라 "빗썸 비중". */
export function exchangeNameKo(exchange: string): string {
  return (
    KOREAN_NAME_MAP[canonicalExchangeId(exchange)] ?? exchangeLabel(exchange)
  );
}

/** Settings 페이지 토글용 대문자 키 → 표시명 */
export function exchangeLabelByKey(upperKey: string): string {
  return exchangeLabel(upperKey);
}

/** 국내(KRW) 거래소 판별 — 단일 정규 패턴 */
const KRW_VENUES = /^(upbit|bithumb|coinone|korbit|gopax)$/i;

export function isKrwVenue(exchange: string): boolean {
  return KRW_VENUES.test(canonicalExchangeId(exchange));
}

/** 해외(USD/USDT) 거래소 판별 */
export function isUsdVenue(exchange: string): boolean {
  return /^(binance|okx|bybit|htx)/i.test(canonicalExchangeId(exchange));
}

/** 국내 거래소 여부 (KRW venue 와 동치이지만 의도 명시용) */
export function isDomesticExchangeName(exchange: string): boolean {
  return isKrwVenue(exchange);
}

/** 크로스보더(해외↔국내) 관점에서 해외 레그인지 */
export function isCrossBorderExchangeName(exchange: string): boolean {
  return isUsdVenue(exchange);
}

/** 국내 거래소 표시명 목록 */
export const DOMESTIC_EXCHANGE_LABELS = [
  "Upbit",
  "Bithumb",
  "Coinone",
  "Korbit",
  "GOPAX",
] as const;

/** Settings 토글 행 — UI 전용 상수 (대문자 키 유지, 기존 호환) */
export const EXCHANGE_TOGGLE_ROWS = [
  { key: "UPBIT", label: "업비트" },
  { key: "BITHUMB", label: "빗썸" },
  { key: "COINONE", label: "코인원" },
  { key: "KORBIT", label: "코빗" },
  { key: "BINANCE_FUTURES", label: "Binance USD-M Perp" },
  { key: "BINANCE_SPOT", label: "Binance Spot" },
  { key: "BINANCE_COINM", label: "Binance COIN-M" },
  { key: "OKX_SPOT", label: "OKX Spot" },
  { key: "OKX_FUTURES", label: "OKX USDT Swap" },
  { key: "BYBIT_SPOT", label: "Bybit Spot" },
  { key: "BYBIT_FUTURES", label: "Bybit USDT Perp" },
  { key: "HTX_SPOT", label: "HTX Spot" },
  { key: "HTX_FUTURES", label: "HTX USDT Swap" },
] as const;
