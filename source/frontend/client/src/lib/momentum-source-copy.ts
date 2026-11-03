type MomentumSource = {
  data_source?: {
    official_minute_supplement_used?: boolean;
    official_24h_ticker_used?: boolean;
  };
};

export const MOMENTUM_SOURCE_HELP =
  "10초·30초는 실제 체결만 사용합니다. 1분 이상은 선택 구간 안에 온전히 들어오는 종료된 1분만 공식 자료로 보완합니다. 같은 분에 이미 관측한 거래금액을 빼고 부족한 금액만 더하며, 초나 10분 단위로 나눠 넣지 않습니다. 공식 분봉은 업비트·빗썸·코인원의 원화 마켓만 지원합니다. 원화 마켓 24시간은 국내 5개 거래소의 공식 자료가 모두 준비되면 그 값으로 표시합니다. 실제 보완 여부와 실시간 누락은 각 행에서 따로 확인합니다.";

/** Describe only sources the backend says contributed to this momentum list. */
export function momentumSourcePresentation(rows: readonly MomentumSource[]) {
  const minuteUsed = rows.some(
    row => row.data_source?.official_minute_supplement_used === true
  );
  const tickerUsed = rows.some(
    row => row.data_source?.official_24h_ticker_used === true
  );
  const sources = ["실시간 체결"];
  if (minuteUsed) sources.push("종료된 공식 1분 자료의 부족 금액");
  if (tickerUsed) sources.push("공식 최근 24시간 자료");
  return {
    title:
      minuteUsed || tickerUsed ? "실시간 + 공식 거래금액" : "실시간 직접관측",
    detail: `${sources.join(" + ")} · 현재 시각 기준`,
  };
}
