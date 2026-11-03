import type {
  MarketShareWhaleAlertEvent,
  MarketShareWhaleAlertState,
} from "@/lib/market-share-types";

export function isUsableWhaleAlertState(
  value: unknown
): value is MarketShareWhaleAlertState {
  if (!value || typeof value !== "object") return false;
  const state = value as MarketShareWhaleAlertState & { status?: string };
  return (
    state.status !== "UNAVAILABLE" &&
    state.version === "WHALE_ALERT_V1" &&
    Number.isFinite(state.threshold_krw) &&
    state.threshold_krw > 0 &&
    Number.isFinite(state.generated_at_ms) &&
    state.generated_at_ms >= 0 &&
    Array.isArray(state.events) &&
    state.events.every(
      event =>
        event &&
        typeof event.id === "string" &&
        typeof event.exchange === "string" &&
        typeof event.asset === "string" &&
        typeof event.market === "string" &&
        Number.isFinite(event.trade_count) &&
        Number.isFinite(event.notional_krw) &&
        Number.isFinite(event.base_quantity)
    )
  );
}

export function whaleAlertSideLabel(
  side: MarketShareWhaleAlertEvent["side"]
): string {
  return side === "BUY" ? "대형 매수" : "대형 매도";
}

export function whaleAlertKindLabel(
  kind: MarketShareWhaleAlertEvent["kind"]
): string {
  return kind === "SINGLE" ? "단일 체결" : "1초 분할 체결";
}

/** How many of the server's last 100 alerts arrived, once a list arrived. */
export function whaleAlertCountLabel(
  state: MarketShareWhaleAlertState | null
): string {
  return Array.isArray(state?.events)
    ? `최근 ${state.events.length}/100건`
    : "건수 확인 중";
}

/**
 * A failed refresh is always said; the last confirmed alerts stay on screen
 * until a fresh valid list replaces them.
 */
export function whaleAlertFailureNote(
  state: MarketShareWhaleAlertState | null,
  unavailable: boolean
): string {
  if (!unavailable) return "";
  return state
    ? "고래 체결을 갱신하지 못해 마지막 확인 자료를 표시합니다."
    : "고래 체결 자료를 확인하지 못했습니다. 연결이 복구되면 다시 받습니다.";
}

export function visibleWhaleAlerts(
  state: MarketShareWhaleAlertState | null,
  limit = 10
): MarketShareWhaleAlertEvent[] {
  if (!state || !Array.isArray(state.events) || limit <= 0) return [];
  return state.events
    .filter(
      event =>
        Boolean(event?.id) &&
        Number.isFinite(event.notional_krw) &&
        event.notional_krw >= state.threshold_krw &&
        (event.side === "BUY" || event.side === "SELL")
    )
    .slice(0, Math.trunc(limit));
}
