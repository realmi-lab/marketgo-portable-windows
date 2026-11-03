import type { DomesticExchange } from "./market-share-config.ts";
import type { GroundedMarketQuestionTargetExchange } from "./market-share-types.ts";
import { formatKRW } from "./formatters.ts";

export const DEFAULT_MARKET_QUESTION =
  "최근 1시간 빗썸 공식 점유율 변화와 시간 경로, 상승·하락 기여 코인을 보고서로 분석해 주세요.";

export function marketQuestionEvidenceTimestamp(value: number): string {
  const date = new Date(value);
  if (
    !Number.isFinite(value) ||
    value <= 0 ||
    !Number.isFinite(date.getTime())
  ) {
    return "기준 시각 확인 필요";
  }
  return `${new Intl.DateTimeFormat("ko-KR", {
    timeZone: "Asia/Seoul",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(date)} 한국시간 기준`;
}

const EXCHANGE_NAMES: Record<DomesticExchange, string> = {
  bithumb: "빗썸",
  upbit: "업비트",
  coinone: "코인원",
  korbit: "코빗",
  gopax: "고팍스",
};

export interface MarketQuestionSuggestion {
  key: string;
  label: string;
  question: string;
}

/** Examples only. Free-form input is submitted unchanged to server planning. */
export function exchangeMarketQuestionSuggestions(
  exchange: DomesticExchange
): MarketQuestionSuggestion[] {
  const name = EXCHANGE_NAMES[exchange];
  return [
    {
      key: "diagnosis",
      label: `${name} 핵심 진단`,
      question:
        exchange === "bithumb"
          ? DEFAULT_MARKET_QUESTION
          : `최근 1시간 동안 ${name} 점유율과 거래금액을 직전 1시간과 비교해 분석해 주세요.`,
    },
    {
      key: "current",
      label: `${name} 현재 비중`,
      question: `현재 ${name} 점유율과 거래금액을 근거와 함께 자세히 설명해 주세요.`,
    },
    {
      key: "compare-30",
      label: `${name} 30분 비교`,
      question: `최근 30분 동안 ${name} 점유율과 거래금액을 직전 30분과 비교해 주세요.`,
    },
    ...(exchange === "bithumb"
      ? [
          {
            key: "asset-capture",
            label: "빗썸 BTC 비중",
            question:
              "현재 BTC의 빗썸 거래금액을 BTC의 국내 전체 거래금액, 빗썸 전체 거래금액과 각각 비교해 두 비중을 구분해 설명해 주세요.",
          },
        ]
      : [
          {
            key: "monthly-high",
            label: `${name} 한 달 고점`,
            question: `최근 한 달 동안 ${name} 점유율이 가장 높았던 확인 가능한 구간과 그때 거래금액을 설명해 주세요.`,
          },
        ]),
    {
      key: "notice-context",
      label: `${name} 공지 맥락`,
      question: `최근 1시간 ${name} 점유율 변화와 관련 공지를 함께 살펴보고, 시간·자산의 연관성과 인과관계를 구분해 단정하지 말아 주세요.`,
    },
    ...(exchange === "bithumb"
      ? [
          {
            key: "orderbook",
            label: "빗썸 BTC 호가",
            question:
              "현재 BTC의 빗썸 거래 비중과 호가·슬리피지를 함께 설명하되, 현재 호가를 과거 점유율 변화의 원인으로 단정하지 말아 주세요.",
          },
        ]
      : []),
  ];
}

export interface MarketQuestionDraft {
  question: string;
  suggestionKey: string | null;
}

type DraftAction =
  | { type: "edit"; question: string }
  | { type: "suggestion"; suggestion: MarketQuestionSuggestion }
  | { type: "exchange"; exchange: DomesticExchange }
  | { type: "completed" };

export function marketQuestionDraftReducer(
  draft: MarketQuestionDraft,
  action: DraftAction
): MarketQuestionDraft {
  switch (action.type) {
    case "edit":
      return { question: action.question, suggestionKey: null };
    case "suggestion":
      return {
        question: action.suggestion.question,
        suggestionKey: action.suggestion.key,
      };
    case "exchange": {
      if (draft.suggestionKey === null) return draft;
      const suggestions = exchangeMarketQuestionSuggestions(action.exchange);
      // A Bithumb-only coin example cannot become an unsupported coin query
      // for another exchange. Edited free-form text was already preserved above.
      const suggestion =
        suggestions.find(item => item.key === draft.suggestionKey) ??
        suggestions[0];
      return { question: suggestion.question, suggestionKey: suggestion.key };
    }
    // Answers and editable drafts have separate owners. Completion must never
    // replace text typed or selected while the preceding request was running.
    case "completed":
      return draft;
  }
}

export function marketQuestionEvidenceLabel(layer: string): string {
  const labels: Record<string, string> = {
    DOMESTIC_OFFICIAL_NOTICE_7D_SNAPSHOT: "최근 7일 국내 거래소 공식 공지 자료",
    WEBSOCKET_DIRECT_OBSERVATION: "실시간 체결 직접 관측",
    WEBSOCKET_HISTORICAL_RANGE: "지정한 과거 구간의 실시간 체결 관측 자료",
    WEBSOCKET_ROLLING_24H_ASSET_SNAPSHOT:
      "실시간 체결 기준 최근 24시간 코인 자료",
    OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER:
      "국내 5개 거래소 공식 최근 24시간 자료",
    OFFICIAL_ALIGNED_24H_CURRENT:
      "업비트·빗썸·코인원의 같은 종료 시각 공식 24시간 자료",
    OFFICIAL_ALIGNED_24H_ENDPOINTS:
      "업비트·빗썸·코인원의 공식 24시간 시작·끝 비교 자료",
    OFFICIAL_ALIGNED_KST_CALENDAR_DAY:
      "업비트·빗썸·코인원의 한국 날짜 기준 하루 마감 자료",
    OFFICIAL_ALIGNED_DAILY_ROLLING_24H_ENDPOINTS:
      "업비트·빗썸·코인원의 같은 한국 시각 날짜별 공식 24시간 자료",
  };
  return labels[layer] ?? "자료 종류 확인 필요";
}

export const CURRENT_ASSET_TOTAL_SHARE_LABEL =
  "국내 전체 거래금액 중 빗썸의 이 코인 거래 비중";

export function marketQuestionAbsoluteKRW(value: number): string {
  return Number.isFinite(value) ? formatKRW(value) : "—";
}

/** These driver fields describe Bithumb's share, not the named venue's share. */
export function showMarketQuestionBithumbDrivers(
  target:
    Pick<GroundedMarketQuestionTargetExchange, "exchange"> | null | undefined,
  queryExchange = ""
): boolean {
  return [target?.exchange, queryExchange].every(value => {
    const exchange = (value ?? "").trim().toLowerCase();
    return exchange === "" || exchange === "bithumb";
  });
}

/** A partial current snapshot may still have a valid separate ticker amount. */
export function showMarketQuestionOfficialReference(
  layer: string,
  target: Pick<
    GroundedMarketQuestionTargetExchange,
    "current_volume_krw" | "current_share_pct"
  >
): boolean {
  return !(
    layer === "OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER" &&
    Number.isFinite(target.current_volume_krw) &&
    Number.isFinite(target.current_share_pct)
  );
}
