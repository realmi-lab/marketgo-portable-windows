import type { MarketShareAssetExchangeSlippage } from "./market-share-types.ts";

type SlippageSide = "buy" | "sell";
type SlippageExecutionInput = Pick<
  MarketShareAssetExchangeSlippage,
  | "status"
  | "buy_complete"
  | "sell_complete"
  | "buy_fill_pct"
  | "sell_fill_pct"
  | "buy_slippage_bps"
  | "sell_slippage_bps"
>;

/** A blocked simulation is unknown, not proof that the order cannot fill. */
export function slippageExecutionPresentation(
  row: SlippageExecutionInput,
  side: SlippageSide
) {
  const unavailable = {
    kind: "unavailable" as const,
    detail: "호가 확인 대기",
    slippageBps: null,
    fillPct: null,
  };
  if (row.status !== "LIVE") return unavailable;
  const complete = side === "buy" ? row.buy_complete : row.sell_complete;
  const slippage =
    side === "buy" ? row.buy_slippage_bps : row.sell_slippage_bps;
  const fill = side === "buy" ? row.buy_fill_pct : row.sell_fill_pct;
  if (complete) {
    return typeof slippage === "number" && Number.isFinite(slippage)
      ? {
          kind: "complete" as const,
          detail: "",
          slippageBps: slippage,
          fillPct: fill,
        }
      : unavailable;
  }
  if (!Number.isFinite(fill) || fill < 0 || fill > 100) return unavailable;
  return {
    kind: "incomplete" as const,
    detail: "전량 불가",
    slippageBps: null,
    fillPct: fill,
  };
}

export function aggregationEngineLabel(value: string): string {
  if (value === "rust-ffi" || value === "rust-pyo3") return "고속 계산";
  if (value === "go-oracle" || value === "GO" || value === "python-fallback")
    return "기본 계산";
  return "집계 경로 확인 필요";
}

export function observationGapStatusLabel(status: string): string {
  if (status === "RECOVERED") return "연결 재개";
  if (status === "OPEN") return "진행 중";
  if (status === "CLOSED_ON_RESTART") return "재시작으로 종료";
  return "상태 확인 필요";
}

// Reason codes the calculator and collector attach to a gap record.
const OBSERVATION_GAP_REASON_LABELS: Record<string, string> = {
  WEBSOCKET_DISCONNECTED: "실시간 연결 끊김",
  WEBSOCKET_STALE: "실시간 자료 지연",
  SUBSCRIPTION_ERROR: "실시간 자료 연결 오류",
  TRADE_STREAM_NOT_READY: "체결 수신 준비 전",
  MESSAGE_PARSE_ERROR: "받은 자료를 읽지 못함",
  TRADE_EVENT_PARSE_ERROR: "체결 자료를 읽지 못함",
  TRADE_PARSE_ERROR: "체결 자료를 읽지 못함",
  AGGREGATOR_REJECTED_TRADE: "계산에서 제외한 체결",
  COLLECTOR_OBSERVATION_INCOMPLETE: "수집 서버 자료 불완전",
  COLLECTOR_HEALTH_JOURNAL_GAP: "수집 상태 기록 끊김",
  REGISTRY_UNAVAILABLE: "거래쌍 목록 없음",
  REGISTRY_REFRESH_STALE: "거래쌍 목록 갱신 지연",
  REGISTRY_SUBSCRIPTION_SET_MISMATCH: "수신 중인 거래쌍 불일치",
  GLOBAL_WEBSOCKET_POLICY_DISABLED: "해외 실시간 수집 꺼짐",
};

const MISSING_TRADE_COUNT_LABELS: Record<string, string> = {
  MISSING_TRADE_EVENTS: "빠진 체결",
  MISSING_TRADE_ROWS: "빠진 체결 기록",
};

/** Plain names for a gap's reason codes, each shown once. */
export function observationGapReasonLabels(codes: readonly string[]): string[] {
  const labels = codes.map(code => {
    const [name, count] = code.split(":");
    const countLabel = MISSING_TRADE_COUNT_LABELS[name];
    if (countLabel !== undefined) {
      const value = Number(count);
      return Number.isSafeInteger(value) && value > 0
        ? `${countLabel} ${value.toLocaleString("ko-KR")}건`
        : countLabel;
    }
    return OBSERVATION_GAP_REASON_LABELS[code] ?? "기타 사유";
  });
  return [...new Set(labels)];
}

function countLabel(value: number | null | undefined): string {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value.toLocaleString("ko-KR")
    : "—";
}

/** Raw aged books include quiet markets; do not relabel that count as delay. */
export function orderbookAgeBreakdown(
  staleCount: number,
  diagnoses: Record<string, number> | null | undefined
): string {
  const quiet = diagnoses ? (diagnoses.LOW_ACTIVITY_CANDIDATE ?? 0) : undefined;
  return `오래된 호가 ${countLabel(staleCount)} · 그중 거래 적음 ${countLabel(quiet)}`;
}

export function currentQueueLabel(value: number | null | undefined): string {
  return `현재 처리 대기 ${countLabel(value)}건`;
}

/** Present supplied per-connection maximum age without calling it the latest trade. */
export function formatCollectorElapsed(
  value: number | null | undefined
): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0)
    return "—";
  const tenths = Math.round(value * 10);
  if (!Number.isSafeInteger(tenths)) return "—";
  if (value > 0 && tenths === 0) return "0.1초 미만";
  const hours = Math.floor(tenths / 36_000);
  const minutes = Math.floor((tenths % 36_000) / 600);
  const seconds = (tenths % 600) / 10;
  const parts: string[] = [];
  if (hours > 0) parts.push(`${hours.toLocaleString("ko-KR")}시간`);
  if (minutes > 0) parts.push(`${minutes}분`);
  if (seconds > 0 || parts.length === 0) parts.push(`${seconds}초`);
  return parts.join(" ");
}

/** This is the live collection policy, not selected-window minute evidence. */
export function liveCollectionFallbackLabel(enabled: boolean): string {
  return `${enabled ? "사용" : "사용 안 함"} · 분봉 보완과 별도`;
}
