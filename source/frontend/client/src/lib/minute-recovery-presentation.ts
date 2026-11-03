import type { MarketShareDataSource } from "./market-share-types.ts";

type RecoveryKind =
  | "RECOVERED"
  | "RECOVERED_WITH_LIMITS"
  | "PRECISION_DIFFERENCE"
  | "CONFLICT"
  | "TICKER"
  | "NOT_APPLICABLE"
  | "NOT_USED"
  | "UNAVAILABLE"
  | "INVALID";

type RecoveryPresentation = {
  kind: RecoveryKind;
  label: string;
  detail: string;
  volumeKRW: number | null;
  rowCount: number | null;
  exchanges: string[];
};

const LIMITATION =
  "공식 1분봉 합계와 같은 분의 실시간 체결을 대조해 차액만 반영합니다. 늦게 도착한 체결이 있으면 보완 금액을 다시 계산합니다. 개별 체결 시각과 건수는 별도 확인이 필요합니다.";

/** Present supplied amount provenance; never infer recovered seconds or trades. */
export function minuteRecoveryPresentation(
  source: Partial<MarketShareDataSource> | null | undefined,
  windowSeconds: number
): RecoveryPresentation {
  const state = (
    kind: RecoveryKind,
    label: string,
    detail: string
  ): RecoveryPresentation => ({
    kind,
    label,
    detail,
    volumeKRW: null,
    rowCount: null,
    exchanges: [],
  });
  const invalid = () =>
    state(
      "INVALID",
      "분봉 원본 대조 필요",
      "금액·분 단위·반영 상태가 맞지 않아 복구 금액을 표시하지 않습니다."
    );
  if (!Number.isSafeInteger(windowSeconds) || windowSeconds <= 0)
    return invalid();
  if (windowSeconds < 60) {
    return state(
      "NOT_APPLICABLE",
      "분봉으로 채우지 않는 구간",
      "1분보다 짧은 구간에는 공식 분봉을 나눠 넣지 않습니다. 실시간 체결 관측만 확인합니다."
    );
  }
  if (source?.official_24h_ticker_used === true) {
    if (
      source.official_minute_supplement_used === true ||
      windowSeconds !== 86400
    )
      return invalid();
    return state(
      "TICKER",
      "공식 최근 24시간 자료 사용",
      "이 선택값은 공식 24시간 거래금액이며 분봉 복구 금액이 아닙니다. 실시간 체결 관측은 별도입니다."
    );
  }
  const status = source?.official_minute_supplement_status;
  if (
    !source ||
    (status !== "READY" && status !== "SOURCE_PRECISION_DIFFERENCE")
  ) {
    return state(
      "UNAVAILABLE",
      status === "DISABLED"
        ? "분봉 보완 사용 안 함"
        : "분봉 보완 자료 확인 대기",
      "반영이 확인된 분봉 금액이 없습니다. 비어 있는 자료를 0원이나 복구 완료로 표시하지 않습니다."
    );
  }
  const conflicts = source.official_minute_supplement_conflict_count ?? 0;
  const precision =
    source.official_minute_supplement_precision_difference_count ?? 0;
  if (
    !Number.isSafeInteger(conflicts) ||
    conflicts < 0 ||
    !Number.isSafeInteger(precision) ||
    precision < 0
  )
    return invalid();
  if (source.official_minute_supplement_used === true) {
    if (source.official_minute_replacement_used === true) {
      const amount = source.official_minute_replacement_volume_krw;
      const rows = source.official_minute_replacement_count;
      if (
        typeof amount !== "number" ||
        !Number.isFinite(amount) ||
        amount < 0 ||
        typeof rows !== "number" ||
        !Number.isSafeInteger(rows) ||
        rows <= 0 ||
        source.official_candle_supplement_interval_sec !== 60
      )
        return invalid();
      return {
        kind: conflicts > 0 ? "RECOVERED_WITH_LIMITS" : "RECOVERED",
        label:
          conflicts > 0
            ? "공식 1분봉으로 대체 · 일부 원본 대조 필요"
            : "공식 1분봉으로 대체",
        detail:
          "누락이 있는 코인의 해당 1분은 공식 분봉 전체 금액을 사용합니다. 기존 체결 원본은 보존하며 중복해서 더하지 않습니다. 개별 체결 상세는 미복구 상태입니다.",
        volumeKRW: amount,
        rowCount: rows,
        exchanges: [
          ...new Set(source.official_minute_supplement_exchanges ?? []),
        ],
      };
    }
    const amount = source.official_minute_supplement_volume_krw;
    const rows = source.official_minute_supplement_row_count;
    if (
      typeof amount !== "number" ||
      !Number.isFinite(amount) ||
      amount <= 0 ||
      typeof rows !== "number" ||
      !Number.isSafeInteger(rows) ||
      rows <= 0 ||
      source.official_candle_supplement_interval_sec !== 60
    )
      return invalid();
    const limited = conflicts > 0 || precision > 0;
    return {
      kind: limited ? "RECOVERED_WITH_LIMITS" : "RECOVERED",
      label: limited
        ? "1분봉으로 보완 · 일부 원본 대조 필요"
        : "1분봉으로 보완",
      detail: LIMITATION,
      volumeKRW: amount,
      rowCount: rows,
      exchanges: [
        ...new Set(source.official_minute_supplement_exchanges ?? []),
      ],
    };
  }
  if (source.official_minute_supplement_used !== false) {
    return state(
      "UNAVAILABLE",
      "분봉 반영 상태 확인 대기",
      "분봉 금액의 실제 반영 여부가 없어 복구 상태를 판정하지 않습니다."
    );
  }
  if (conflicts > 0) {
    return state(
      "CONFLICT",
      "원본 대조 필요",
      "충돌한 분봉 자료는 보완 합계에서 제외합니다. 이 선택 구간에 반영된 분봉 복구 금액은 없습니다."
    );
  }
  if (precision > 0 || status === "SOURCE_PRECISION_DIFFERENCE") {
    return state(
      "PRECISION_DIFFERENCE",
      "원본 정밀도 차이 · 추가 금액 없음",
      "원본 관측 금액을 유지한 별도 출처 차이입니다. 실시간 체결 관측이 복구된 것은 아닙니다."
    );
  }
  return state(
    "NOT_USED",
    (source.gap_seconds ?? 0) > 0
      ? "분봉 보완 확인 대기"
      : "이번 구간에 더한 분봉 금액 없음",
    "분봉 조회 준비와 실제 금액 반영은 다릅니다. 전체 누락이 복구됐거나 공식 거래금액이 0원이라는 뜻은 아닙니다."
  );
}
