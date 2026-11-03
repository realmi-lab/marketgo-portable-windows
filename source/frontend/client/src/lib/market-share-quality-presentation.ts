/** Missing analysis fields are not evidence that a quality condition is absent. */
export function tradeAccountingInputPresentation(accounting: {
  input_source?: string;
  collector_lag_status?: string;
}) {
  const independentLifetimes =
    accounting.input_source === "DURABLE_JOURNAL" ||
    accounting.collector_lag_status === "NOT_COMPARABLE_PROCESS_LIFETIMES";
  return independentLifetimes
    ? {
        title: "체결 처리 건수 검증",
        description:
          "처리 단계별 건수가 맞는지 확인합니다. 건수가 일치해도 전체 기간의 자료가 모두 수집됐다는 뜻은 아닙니다.",
        // Two counters with different start points can never "match"; the
        // status covers the calculator's own processing stages.
        balancedLabel: "처리 단계 일치",
        countersLabel: "수집 단계 누적 / 계산 단계 입력 누적",
        separator: " / ",
        differenceLabel: "두 단계는 시작 시점이 달라 차이를 비교하지 않음",
        help: "수집 서버와 계산 서버는 따로 시작하거나 재시작하므로 누적 건수의 시작 시점이 다릅니다. 두 수의 차이는 저장된 자료의 처리 대기량이 아닙니다. 저장된 자료 중 가장 최근 위치와 계산을 마친 위치는 별도 지표로 확인합니다. 현재 대기 수는 계산 서버 내부 대기열만 뜻합니다.",
      }
    : {
        title: "체결 건수 누락 검증",
        description:
          "거래소에서 받은 체결 건수와 처리·집계된 건수가 같은지 확인합니다. 순간적인 수집 지연은 누락과 구분해 별도 상태로 표시합니다.",
        balancedLabel: "일치",
        countersLabel: "받은 체결 → 대기열 등록 누적",
        separator: " → ",
        differenceLabel: "받은 수−등록 수",
        help: "받은 체결 수와 대기열 등록 수를 읽은 시각이 다르면 차이가 음수일 수 있습니다. 이 값만으로 체결 손실이나 지연을 단정하지 않으며, 아래 처리 보존 차이를 따로 확인합니다.",
      };
}

export function observationGapRecordsEmptyLabel(
  gaps: readonly unknown[] | null | undefined
): string {
  return Array.isArray(gaps)
    ? "받은 기록 목록에 자료 끊김 기록이 없습니다."
    : "자료 끊김 기록 목록을 아직 받지 못했습니다.";
}

export function qualityFilterLabel(
  coinFilter: boolean | null | undefined,
  marketFilter: boolean | null | undefined
): string {
  if (coinFilter === true || marketFilter === true) return "있음";
  if (coinFilter === false && marketFilter === false) return "없음";
  return "확인 자료 없음";
}

export function tradeAccountingStatusPresentation(
  status: string | null | undefined,
  balancedLabel = "일치"
): { label: string; tone: "profit" | "gold" | "loss" } {
  switch (status) {
    case "BALANCED":
      return { label: balancedLabel, tone: "profit" };
    case "COLLECTOR_LAG":
      // The server compares received and enqueued counters. The difference can
      // be negative because the two counters are sampled at different times.
      return { label: "수집 집계 차이", tone: "gold" };
    case "MISMATCH":
      return { label: "불일치", tone: "loss" };
    default:
      return { label: "검증 상태 확인 필요", tone: "gold" };
  }
}

export function formatQualitySignedCount(
  value: number | null | undefined
): string {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) return "—";
  if (value === 0) return "0";
  return `${value > 0 ? "+" : ""}${value.toLocaleString("ko-KR")}`;
}
