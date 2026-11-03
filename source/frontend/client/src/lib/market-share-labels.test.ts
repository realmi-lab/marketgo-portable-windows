import assert from "node:assert/strict";
import test from "node:test";

import {
  MARKET_SHARE_NETWORK_WINDOWS,
  MARKET_SHARE_WINDOWS,
  MARKET_SHARE_WINDOW_SEC,
  marketShareNetworkWindowLabel,
} from "./market-share-config.ts";

import {
  marketShareConnectionLabel,
  marketShareDurationLabel,
  marketShareGapLabel,
  marketShareWindowLabel,
  marketShareWindowStatusLabel,
  microstructureComparisonLabel,
  microstructureStatusLabel,
  marketQuestionAnswerUsesModel,
  noticeCategoryLabel,
  noticeHistoryNoteLabel,
  noticeHistoryStatusLabel,
  evidenceStrengthLabel,
  rollingBriefingTextLabel,
  noticeQuestionAnswerModeLabel,
  noticeScheduleLabel,
  priceValidationStatusLabel,
} from "./market-share-labels.ts";

test("distinguishes model answers from deterministic server summaries", () => {
  for (const source of ["HERMES", "HERMES_SOL_FALLBACK", "OPENROUTER"]) {
    assert.equal(marketQuestionAnswerUsesModel(source), true);
  }
  for (const source of ["SERVER_BRIEFING_FALLBACK", "", "unknown"]) {
    assert.equal(marketQuestionAnswerUsesModel(source), false);
  }
});

test("offers ordered network windows from one minute through 24 hours", () => {
  assert.deepEqual(
    MARKET_SHARE_NETWORK_WINDOWS.map(option => option.seconds),
    [60, 300, 600, 900, 1_800, 3_600, 7_200, 10_800, 21_600, 43_200, 86_400]
  );
  assert.equal(marketShareNetworkWindowLabel(60), "1분");
  assert.equal(marketShareNetworkWindowLabel(86_400), "24시간");
});

test("maps every domestic notice category currently emitted by official sources and Hermes", () => {
  assert.equal(
    noticeCategoryLabel("DEPOSIT_WITHDRAWAL_SUSPENSION"),
    "입출금 중단"
  );
  assert.equal(
    noticeCategoryLabel("DEPOSIT_WITHDRAWAL_RESUMPTION"),
    "입출금 재개"
  );
  assert.equal(
    noticeCategoryLabel("TRADING_SUPPORT_TERMINATION"),
    "거래지원 종료"
  );
  assert.equal(noticeCategoryLabel("SECURITY_INCIDENT"), "보안 사고");
  assert.equal(noticeCategoryLabel("디지털_자산"), "디지털 자산");
  assert.equal(noticeCategoryLabel("공시"), "공시");
  assert.equal(
    noticeCategoryLabel("UNRECOGNIZED_CATEGORY"),
    "공지 분류 확인 필요"
  );
});

test("maps every detailed domestic notice schedule currently emitted by Hermes", () => {
  const expected: Record<string, string> = {
    APPLICATION: "신청",
    BANK_MAINTENANCE: "은행 점검",
    BUY_OPEN: "매수 개시",
    CAUTION_DESIGNATION: "유의종목 지정",
    CAUTION_NOTICE: "유의 안내",
    DEPOSIT_RESUMPTION: "입금 재개",
    DEPOSIT_SUSPENSION: "입금 중단",
    DEPOSIT_WITHDRAWAL_RESUMPTION: "입출금 재개",
    DEPOSIT_WITHDRAWAL_SUSPENSION: "입출금 중단",
    DEPOSIT_WITHDRAWAL_SUSPENSION_CONTINUATION: "입출금 중단 지속",
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
  };
  for (const [kind, label] of Object.entries(expected)) {
    assert.equal(noticeScheduleLabel(kind), label, kind);
  }
  assert.equal(
    noticeScheduleLabel("UNRECOGNIZED_SCHEDULE"),
    "일정 유형 확인 필요"
  );
});

test("maps microstructure statuses to user-facing Korean labels", () => {
  assert.equal(microstructureStatusLabel("THIN_DEPTH"), "빗썸 잔량 비중 낮음");
  assert.equal(
    microstructureStatusLabel("HIGH_SLIPPAGE"),
    "매수 슬리피지 높음"
  );
  assert.equal(
    microstructureStatusLabel("UNKNOWN_STATUS"),
    "호가 상태 확인 필요"
  );
});

test("keeps microstructure comparison scope explicit", () => {
  assert.equal(
    microstructureComparisonLabel("CURRENT_SNAPSHOT_ONLY"),
    "현재 값만 확인"
  );
  assert.equal(
    microstructureComparisonLabel("BEFORE_AFTER_OBSERVED"),
    "공지 전후 비교"
  );
  assert.equal(
    microstructureComparisonLabel("BEFORE_AFTER_PARTIAL"),
    "공지 전후 일부 관측"
  );
  assert.equal(
    microstructureComparisonLabel("UNKNOWN_STATUS"),
    "비교 범위 확인 필요"
  );
});

test("maps price validation statuses without exposing API enum names", () => {
  assert.equal(priceValidationStatusLabel("NOT_EVALUATED"), "비교 불가");
  assert.equal(priceValidationStatusLabel("QUARANTINED"), "이상 가격 제외");
  assert.equal(
    priceValidationStatusLabel("UNKNOWN_STATUS"),
    "가격 검증 상태 확인 필요"
  );
});

test("maps notice history statuses for the source health panel", () => {
  assert.equal(noticeHistoryStatusLabel("COMPLETE"), "전체 확인");
  assert.equal(
    noticeHistoryStatusLabel("HERMES_30D_ACTIVE"),
    "최근 30일 공지 확인 완료"
  );
  assert.equal(noticeHistoryStatusLabel("SOURCE_LIMITED"), "출처 제한");
  assert.equal(
    noticeHistoryStatusLabel("UNKNOWN_STATUS"),
    "이력 상태 확인 필요"
  );
});

test("converts server evidence and briefing jargon before rendering", () => {
  assert.equal(evidenceStrengthLabel("STRONG"), "근거 강함");
  assert.equal(evidenceStrengthLabel("UNKNOWN"), "근거 확인 필요");
  // The earlier server wording is rewritten into whole, grammatical phrases.
  assert.equal(
    rollingBriefingTextLabel(
      "최근 24시간 0분 중 9시간 46분을 관측했고 51190초 누락이 있습니다. 변화 시작 시각과 새 거래·만료 거래의 정확한 기여도도 계산하지 않습니다."
    ),
    "최근 24시간 중 9시간 46분을 관측했고 14시간 13분 10초 누락이 있습니다. 변화 시작 시각과 새로 들어오거나 24시간 범위에서 빠진 거래의 정확한 영향도 계산하지 않습니다."
  );
  assert.equal(
    rollingBriefingTextLabel("점유율 기여도는 -0.20%p로 별도 표시합니다."),
    "점유율 기여는 -0.20%p로 별도 표시합니다."
  );
});

test("replaces the internal notice ingestion note with plain Korean", () => {
  assert.equal(
    noticeHistoryNoteLabel("Hermes recent 30-day ingest"),
    "최근 30일 공식 공지 수집"
  );
  assert.equal(noticeHistoryNoteLabel(""), "최근 공지를 처음 불러오는 중");
});

test("discloses whether a notice answer came from the LLM or a server fallback", () => {
  assert.equal(
    noticeQuestionAnswerModeLabel("HERMES_NOTICE"),
    "LLM 생성 · 공식 근거 검증"
  );
  assert.equal(
    noticeQuestionAnswerModeLabel("HERMES_NOTICE_SOL_FALLBACK"),
    "LLM 재검증 · 공식 근거 검증"
  );
  assert.equal(
    noticeQuestionAnswerModeLabel("SERVER_NOTICE_DETERMINISTIC"),
    "공식 근거 자동 정리"
  );
  assert.equal(
    noticeQuestionAnswerModeLabel("SERVER_NOTICE_FALLBACK"),
    "AI 해설 대신 기본 안내"
  );
  assert.equal(
    noticeQuestionAnswerModeLabel("SERVER_NOTICE_CLARIFICATION"),
    "추가 질문 필요"
  );
  assert.equal(noticeQuestionAnswerModeLabel("UNKNOWN"), "공식 근거 답변");
});

test("keeps connection labels user-facing across market-share screens", () => {
  assert.equal(marketShareConnectionLabel("live"), "정상");
  assert.equal(marketShareConnectionLabel("stale"), "지연");
  assert.equal(marketShareConnectionLabel("connecting"), "연결 중");
  assert.equal(marketShareConnectionLabel("offline"), "끊김");
});

test("keeps window labels user-facing instead of exposing API window keys", () => {
  assert.equal(marketShareWindowLabel(10), "10초");
  assert.equal(marketShareWindowLabel(30), "30초");
  assert.equal(marketShareWindowLabel(600), "10분");
  assert.equal(marketShareWindowLabel(3600), "1시간");
  assert.equal(marketShareWindowLabel(1800), "30분");
  assert.equal(marketShareWindowLabel(7200), "2시간");
});

test("exposes the 30-second window in the selectable analysis options", () => {
  assert.deepEqual(
    MARKET_SHARE_WINDOWS.map(window => window.seconds),
    [
      MARKET_SHARE_WINDOW_SEC.TEN_SECONDS,
      MARKET_SHARE_WINDOW_SEC.THIRTY_SECONDS,
      MARKET_SHARE_WINDOW_SEC.ONE_MINUTE,
      MARKET_SHARE_WINDOW_SEC.TEN_MINUTES,
      MARKET_SHARE_WINDOW_SEC.ONE_HOUR,
      MARKET_SHARE_WINDOW_SEC.ONE_DAY,
    ]
  );
});

test("does not expose unknown analysis status enums", () => {
  assert.equal(marketShareWindowStatusLabel("CONFIRMED"), "전체 관측");
  assert.equal(marketShareWindowStatusLabel("RESTORED"), "공식 기록 복구 포함");
  assert.equal(
    marketShareWindowStatusLabel("ESTIMATED"),
    "공식 캔들 추정 포함"
  );
  assert.equal(marketShareWindowStatusLabel("PARTIAL"), "일부 누락");
  assert.equal(
    marketShareWindowStatusLabel("UNKNOWN_STATUS"),
    "상태 확인 필요"
  );
});

test("does not render undefined seconds for old payloads without gap metadata", () => {
  assert.equal(marketShareDurationLabel(undefined), "—");
  assert.equal(marketShareGapLabel(undefined), "공백 확인 불가");
  assert.equal(marketShareGapLabel(null), "공백 확인 불가");
  assert.equal(marketShareGapLabel(Number.NaN), "공백 확인 불가");
  assert.equal(marketShareGapLabel(-1), "공백 확인 불가");
  assert.equal(marketShareGapLabel(0), "공백 0초");
  assert.equal(marketShareGapLabel(90), "공백 1분");
});

test("exchange-published Korean notice boards keep their own name", () => {
  assert.equal(noticeCategoryLabel("공시"), "공시");
  assert.equal(noticeCategoryLabel("서비스+"), "서비스");
  assert.equal(noticeCategoryLabel("UNKNOWN_CODE"), "공지 분류 확인 필요");
});
