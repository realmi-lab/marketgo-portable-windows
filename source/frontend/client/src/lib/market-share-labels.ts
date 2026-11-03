import { formatDuration } from "./formatters.ts";
import { MARKET_SHARE_WINDOW_SEC } from "./market-share-config.ts";

export function marketQuestionAnswerUsesModel(source: string): boolean {
  return ["HERMES", "HERMES_SOL_FALLBACK", "OPENROUTER"].includes(
    source.trim().toUpperCase()
  );
}

export const NOTICE_CATEGORY_LABELS: Record<string, string> = {
  NOTICE: "공지",
  ANNOUNCEMENT: "공지",
  DEPOSIT_WITHDRAWAL: "입출금",
  DEPOSIT_WITHDRAWAL_SUSPENSION: "입출금 중단",
  DEPOSIT_WITHDRAWAL_RESUMPTION: "입출금 재개",
  LISTING: "신규상장",
  DELISTING: "거래지원 종료",
  TRADING_SUPPORT_TERMINATION: "거래지원 종료",
  CAUTION: "유의종목",
  SECURITY: "보안 사고",
  SECURITY_INCIDENT: "보안 사고",
  WALLET: "지갑 장애",
  NETWORK: "네트워크 장애",
  NETWORK_OUTAGE: "네트워크 장애",
  NETWORK_UPGRADE: "네트워크 업그레이드",
  TOKEN_SWAP: "토큰 스왑",
  REBRAND: "리브랜딩",
  SNAPSHOT: "기준 시점",
  AIRDROP: "에어드랍",
  MARKET_ADDITION: "원화 마켓 추가",
  QUOTE_MARKET_CHANGE: "기준통화 변경",
  MAINTENANCE: "점검",
  EVENT: "이벤트",
  UPDATE: "업데이트",
  GUIDE: "안내",
  LATEST_BINANCE_NEWS: "Binance 최신 공지",
  "LATEST BINANCE NEWS": "Binance 최신 공지",
  "ANNOUNCEMENTS-EARN-AND-LOAN": "Earn·대출 공지",
  "ANNOUNCEMENTS-TRADING-UPDATES": "거래 업데이트",
  "ANNOUNCEMENTS-WEB3": "Web3 공지",
  LATEST_ACTIVITIES: "Binance 최신 활동",
  NEW_CRYPTOCURRENCY_LISTING: "신규 암호자산 상장",
  신규서비스: "신규 서비스",
  안내: "안내",
  공시: "공시",
  후기: "소식",
  NFT: "NFT",
  디지털_자산: "디지털 자산",
  "서비스+": "서비스",
  OTHER: "기타",
};

export const NOTICE_SCHEDULE_LABELS: Record<string, string> = {
  NOTICE: "공지",
  APPLICATION: "신청",
  BANK_MAINTENANCE: "은행 점검",
  BUY_OPEN: "매수 개시",
  CAUTION_DESIGNATION: "유의종목 지정",
  CAUTION_NOTICE: "유의 안내",
  DEPOSIT: "입금",
  DEPOSIT_RESUMPTION: "입금 재개",
  DEPOSIT_SUSPENSION: "입금 중단",
  WITHDRAWAL: "출금",
  DEPOSIT_WITHDRAWAL: "입출금",
  DEPOSIT_WITHDRAWAL_RESUMPTION: "입출금 재개",
  DEPOSIT_WITHDRAWAL_SUSPENSION: "입출금 중단",
  DEPOSIT_WITHDRAWAL_SUSPENSION_CONTINUATION: "입출금 중단 지속",
  TRADING: "거래",
  FEE_RATE_CHANGE_APPLICATION: "수수료율 변경 적용",
  FEE_REDUCTION_APPLICATION: "수수료 인하 적용",
  INVITATION_CODE_REGISTRATION_PERIOD: "초대코드 등록 기간",
  MAINTENANCE_COMPLETION_AND_DEPOSIT_WITHDRAWAL_RESUMPTION:
    "점검 완료·입출금 재개",
  MARKET_AND_RESERVED_ORDER_OPEN: "거래·예약주문 개시",
  MARKET_OPEN: "거래 개시",
  OPEN_API_FEE_FREE_EVENT: "Open API 수수료 무료",
  OPEN_API_ZERO_FEE_EVENT: "Open API 수수료 0원",
  POLICY_CHANGE: "정책 변경",
  REBRAND_COMPLETION: "리브랜딩 완료",
  REWARD_PAYMENT_PERIOD: "혜택 지급 기간",
  REWARD_DISTRIBUTION: "혜택 지급 예정일",
  ELIGIBILITY_DEADLINE: "참여 조건 유지 기준일",
  SECURITY_INCIDENT: "보안 사고",
  SECURITY_INCIDENT_PERIOD: "보안 사고 기간",
  SELL_OPEN: "매도 개시",
  SUSPENSION: "중단",
  TOKEN_SWAP_UPDATE: "토큰 스왑 변경",
  TRADING_CAUTION_DESIGNATION: "거래유의 지정",
  TRADING_FEE_FREE_PERIOD: "거래 수수료 무료 기간",
  TRADING_FEE_REDUCTION: "거래 수수료 인하",
  TRADING_SUPPORT: "거래지원",
  TRADING_SUPPORT_TERMINATION: "거래지원 종료",
  TRADING_SUSPENSION: "거래 중단",
  USDC_ZERO_FEE_APPLICATION: "USDC 수수료 0원 적용",
  USDT_MAKER_FEE_REDUCTION: "USDT 메이커 수수료 인하",
  WITHDRAWAL_RESUMPTION: "출금 재개",
  WITHDRAWAL_SERVICE_TERMINATION: "출금 서비스 종료",
  WITHDRAWAL_SUPPORT_TERMINATION: "출금지원 종료",
  WITHDRAWAL_SUSPENSION: "출금 중단",
  LISTING: "상장",
  DELISTING: "거래지원 종료",
  CAUTION: "유의",
  START: "시작",
  END: "종료",
  RESUME: "재개",
  SUSPEND: "중단",
  SECURITY: "보안 사고",
  WALLET: "지갑 장애",
  NETWORK: "네트워크 장애",
  NETWORK_UPGRADE: "네트워크 업그레이드",
  TOKEN_SWAP: "토큰 스왑",
  REBRAND: "리브랜딩",
  SNAPSHOT: "기준 시점",
  AIRDROP: "에어드랍",
  MARKET_ADDITION: "원화 마켓 추가",
  QUOTE_MARKET_CHANGE: "기준통화 변경",
  MAINTENANCE: "점검",
  UPDATE: "업데이트",
  EVENT: "이벤트",
};

const MICROSTRUCTURE_STATUS_LABELS: Record<string, string> = {
  LIVE: "관측",
  QUIET: "거래 적음·최근 값 유지",
  WAITING: "준비 중",
  INCOMPLETE: "호가 일부 누락",
  CROSSED: "교차 호가",
  SEQUENCE_GAP: "호가 순서 단절",
  STALE: "호가 지연",
  FX_UNRESOLVED: "환산 불가",
  NO_DOMESTIC_ORDERBOOK: "국내 호가 없음",
  UNINITIALIZED: "호가 준비 전",
  UNLISTED_OR_BOOK_MISSING: "빗썸 미상장·호가 없음",
  THIN_DEPTH: "빗썸 잔량 비중 낮음",
  WIDE_SPREAD: "스프레드 넓음",
  INSUFFICIENT_TEN_LEVEL_DEPTH: "10단계 호가로 체결 불가",
  HIGH_SLIPPAGE: "매수 슬리피지 높음",
  COMPETITIVE: "경쟁 범위",
  QUARANTINED: "이상 가격 제외",
  UNAVAILABLE: "자료 부족",
};

const PRICE_VALIDATION_STATUS_LABELS: Record<string, string> = {
  NOT_EVALUATED: "비교 불가",
  PROVISIONAL: "검증 중",
  QUARANTINED: "이상 가격 제외",
};

const NOTICE_HISTORY_STATUS_LABELS: Record<string, string> = {
  PENDING: "확인 대기",
  COMPLETE: "전체 확인",
  HERMES_30D_ACTIVE: "최근 30일 공지 확인 완료",
  SOURCE_LIMITED: "출처 제한",
  INCOMPLETE: "일부 확인",
};

const MARKET_SHARE_WINDOW_LABELS: Record<number, string> = {
  [MARKET_SHARE_WINDOW_SEC.REALTIME]: "실시간",
  [MARKET_SHARE_WINDOW_SEC.TEN_SECONDS]: "10초",
  [MARKET_SHARE_WINDOW_SEC.THIRTY_SECONDS]: "30초",
  [MARKET_SHARE_WINDOW_SEC.ONE_MINUTE]: "1분",
  [MARKET_SHARE_WINDOW_SEC.TEN_MINUTES]: "10분",
  [MARKET_SHARE_WINDOW_SEC.ONE_HOUR]: "1시간",
  [MARKET_SHARE_WINDOW_SEC.ONE_DAY]: "24시간",
};

export function microstructureStatusLabel(value: string): string {
  return MICROSTRUCTURE_STATUS_LABELS[value] ?? "호가 상태 확인 필요";
}

export function microstructureComparisonLabel(value: string): string {
  if (value === "CURRENT_SNAPSHOT_ONLY") return "현재 값만 확인";
  if (value === "BEFORE_AFTER_OBSERVED") return "공지 전후 비교";
  if (value === "BEFORE_AFTER_PARTIAL") return "공지 전후 일부 관측";
  return "비교 범위 확인 필요";
}

export function priceValidationStatusLabel(value: string): string {
  return PRICE_VALIDATION_STATUS_LABELS[value] ?? "가격 검증 상태 확인 필요";
}

export function noticeHistoryStatusLabel(value: string): string {
  return NOTICE_HISTORY_STATUS_LABELS[value] ?? "이력 상태 확인 필요";
}

export function noticeHistoryNoteLabel(value: string): string {
  const note = value.trim();
  if (!note) return "최근 공지를 처음 불러오는 중";
  if (note === "Hermes recent 30-day ingest") {
    return "최근 30일 공식 공지 수집";
  }
  return note;
}

export function evidenceStrengthLabel(value: string): string {
  const labels: Record<string, string> = {
    STRONG: "근거 강함",
    MODERATE: "근거 보통",
    WEAK: "근거 약함",
    UNAVAILABLE: "판단 보류",
  };
  return labels[value] ?? "근거 확인 필요";
}

/**
 * The server's 24-hour briefing in plain wording. Current calculators send
 * finished sentences; this rewrites only the phrasing of earlier releases,
 * and it rewrites whole phrases: replacing every 기여도 once turned
 * "…의 정확한 기여도도" into the broken "…의 정확히 미친 영향도".
 */
export function rollingBriefingTextLabel(value: string): string {
  return value
    .replace(/(\d+시간) 0분/g, "$1")
    .replace(
      /(\d+)초 누락이 있습니다/g,
      (_, seconds: string) =>
        `${formatDuration(Number(seconds))} 누락이 있습니다`
    )
    .replaceAll(
      "새 거래·만료 거래의 정확한 기여도",
      "새로 들어오거나 24시간 범위에서 빠진 거래의 정확한 영향"
    )
    .replaceAll(
      "새 거래·만료 거래",
      "새로 들어오거나 24시간 범위에서 빠진 거래"
    )
    .replaceAll(
      "신규·만료 구간의 산술적 기여도가 아직 확보되지 않아",
      "새로 들어오거나 빠진 거래의 기여를 비교할 자료가 아직 없어"
    )
    .replaceAll(
      "산술적으로 분해한 변화 기여도는 새 거래",
      "점유율 변화를 나누면 새로 들어온 거래"
    )
    .replaceAll("24시간 전 만료 거래", "24시간 범위에서 빠진 거래")
    .replaceAll("점유율 기여도는", "점유율 기여는");
}

export function noticeQuestionAnswerModeLabel(value: string): string {
  switch (value) {
    case "HERMES_NOTICE":
      return "LLM 생성 · 공식 근거 검증";
    case "HERMES_NOTICE_SOL_FALLBACK":
      return "LLM 재검증 · 공식 근거 검증";
    case "SERVER_NOTICE_DETERMINISTIC":
      return "공식 근거 자동 정리";
    case "SERVER_NOTICE_FALLBACK":
      return "AI 해설 대신 기본 안내";
    case "SERVER_NOTICE_CLARIFICATION":
      return "추가 질문 필요";
    default:
      return "공식 근거 답변";
  }
}

export function noticeCategoryLabel(value: string): string {
  const known = NOTICE_CATEGORY_LABELS[value];
  if (known) return known;
  // Exchanges also publish their own Korean board names ("안내", "공시").
  const korean = value?.replace(/\+$/, "").trim();
  return korean && /[가-힣]/.test(korean) ? korean : "공지 분류 확인 필요";
}

const LISTING_HEADLINE_LABELS: Record<string, string> = {
  BITHUMB_EXCLUSIVE: "빗썸 단독상장",
  // The breakdown below splits this group into 업비트 and 기타 거래소 rows.
  BITHUMB_SHARED: "빗썸 동시상장(업비트·기타 거래소 합산)",
  BITHUMB_UNLISTED: "빗썸 미상장",
};

/** The headline group's name in the same words as the listing breakdown. */
export function listingHeadlineLabel(headline: {
  structure: string;
  label: string;
}): string {
  return LISTING_HEADLINE_LABELS[headline.structure] ?? headline.label;
}

export function noticeScheduleLabel(value: string): string {
  return NOTICE_SCHEDULE_LABELS[value] ?? "일정 유형 확인 필요";
}

export function marketShareWindowStatusLabel(value: string): string {
  if (value === "CONFIRMED") return "전체 관측";
  if (value === "PROVISIONAL") return "확인 중";
  if (value === "RESTORED") return "공식 기록 복구 포함";
  if (value === "ESTIMATED") return "공식 캔들 추정 포함";
  if (value === "PARTIAL") return "일부 누락";
  if (value === "UNAVAILABLE") return "계산 불가";
  return "상태 확인 필요";
}

export function marketShareWindowLabel(seconds: number): string {
  const normalized = Math.floor(seconds);
  const known = MARKET_SHARE_WINDOW_LABELS[normalized];
  if (known) return known;
  if (!Number.isFinite(normalized) || normalized < 0) return "구간 확인 필요";
  if (
    normalized >= MARKET_SHARE_WINDOW_SEC.ONE_DAY &&
    normalized % MARKET_SHARE_WINDOW_SEC.ONE_DAY === 0
  )
    return `${normalized / MARKET_SHARE_WINDOW_SEC.ONE_DAY}일`;
  if (
    normalized >= MARKET_SHARE_WINDOW_SEC.ONE_HOUR &&
    normalized % MARKET_SHARE_WINDOW_SEC.ONE_HOUR === 0
  )
    return `${normalized / MARKET_SHARE_WINDOW_SEC.ONE_HOUR}시간`;
  if (
    normalized >= MARKET_SHARE_WINDOW_SEC.ONE_MINUTE &&
    normalized % MARKET_SHARE_WINDOW_SEC.ONE_MINUTE === 0
  )
    return `${normalized / MARKET_SHARE_WINDOW_SEC.ONE_MINUTE}분`;
  return `${normalized}초`;
}

export function marketShareConnectionLabel(value: string): string {
  if (value === "live") return "정상";
  if (value === "stale") return "지연";
  if (value === "connecting") return "연결 중";
  return "끊김";
}

export function marketShareDurationLabel(value: unknown): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0)
    return "—";
  const seconds = Math.floor(value);
  if (seconds >= 86_400)
    return `${(seconds / 86_400).toFixed(seconds % 86_400 ? 1 : 0)}일`;
  if (seconds >= 3_600) return `${Math.floor(seconds / 3_600)}시간`;
  if (seconds >= 60) return `${Math.floor(seconds / 60)}분`;
  return `${seconds}초`;
}

export function marketShareGapLabel(value: unknown): string {
  const duration = marketShareDurationLabel(value);
  // An unknown span is not the same as no gap: saying "none recorded" for a
  // value the server did not supply told viewers a partly observed window had
  // been observed throughout.
  return duration === "—" ? "공백 확인 불가" : `공백 ${duration}`;
}
