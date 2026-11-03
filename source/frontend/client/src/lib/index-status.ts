import type { IndexDayResponse } from "./index-day.ts";

export function indexHasConfirmedAmount(data: IndexDayResponse | null): boolean {
  const total = data?.domestic?.totals.domestic;
  return Boolean(data && ((total?.total != null && (!total.unavailable || total.total !== 0)) ||
    data.rows.some(row => row.volume_krw != null || row.observed_volume_krw != null)));
}

export function indexStatus(data: IndexDayResponse | null, error = "") {
  if (error) return { tone: "warning", label: "조회 지연", detail: data ? "마지막으로 받은 자료를 표시하고 있습니다." : "자료를 불러오지 못했습니다. 다시 조회해 주세요." };
  if (!data) return { tone: "neutral", label: "자료 확인 중", detail: "공식 시간봉 자료를 불러오고 있습니다." };
  if (data.rows.some(row => row.status === "ROLLING_STALE"))
    return { tone: "warning", label: "갱신 지연", detail: "아래 집계 기간의 이전 자료입니다. 최신 시간봉이 아직 반영되지 않았습니다." };
  if (data.rows.some(row => row.status === "ROLLING_COLLECTING"))
    return { tone: "active", label: "시간봉 수집 중", detail: "확보된 금액부터 반영하고 있습니다. 화면은 15초마다 갱신됩니다." };
  if (!data.rows.length || !indexHasConfirmedAmount(data))
    return { tone: "neutral", label: "자료 없음", detail: "선택한 기간에 확인된 거래금액이 없습니다." };
  if (data.domestic?.totals.domestic?.partial || /PARTIAL|INCOMPLETE|FAILED/.test(data.status))
    return { tone: "warning", label: "일부 자료", detail: "집계 기간 중 확보된 시간봉만 합산합니다. 미제공 금액은 —로 표시합니다." };
  return { tone: "neutral", label: "공식 시간봉", detail: "선택한 기간의 공식 시간봉 금액입니다. 화면은 1분마다 갱신됩니다." };
}
