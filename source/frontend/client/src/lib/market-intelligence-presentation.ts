import { marketShareWindowLabel } from "./market-share-labels.ts";

/** Describe the supplied evidence; never turn a short window into an hourly one. */
export function intelligenceContributionCopy(
  windowSec: number,
  source: "REALTIME" | "OFFICIAL" | "UNAVAILABLE"
) {
  if (source === "REALTIME") {
    const window = marketShareWindowLabel(windowSec);
    return {
      title: `최근 ${window} 코인별 기여`,
      source: `최근 ${window} 실시간 체결로 계산했습니다.`,
    };
  }
  return source === "OFFICIAL"
    ? {
        title: "공식 시간봉 코인별 기여",
        source: "같은 종료 시각의 1시간봉으로 코인별로 계산했습니다.",
      }
    : { title: "코인별 기여", source: "코인별 계산 자료 확인 중" };
}

export function intelligenceCaptureDescription(windowSec: number): string {
  const window = marketShareWindowLabel(windowSec);
  return `오른쪽일수록 포함된 거래소들의 최근 ${window} 거래금액이 크고, 위쪽일수록 같은 ${window} 동안 해당 코인의 거래금액에서 빗썸 비중이 높습니다.`;
}
