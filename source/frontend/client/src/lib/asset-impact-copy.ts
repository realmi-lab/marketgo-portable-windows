import { formatPercentagePointMagnitude } from "./formatters.ts";
import type { MarketShareChangeAsset } from "./market-share-types.ts";

function spokenKrw(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "확인되지 않은 금액";
  }
  const absolute = Math.abs(value);
  const sign = value < 0 ? "-" : "";
  if (absolute >= 100_000_000) {
    const eok = Number((absolute / 100_000_000).toFixed(2));
    return `약 ${sign}${eok.toLocaleString("ko-KR")}억 원`;
  }
  if (absolute >= 10_000) {
    return `약 ${sign}${Math.round(absolute / 10_000).toLocaleString("ko-KR")}만 원`;
  }
  return `${sign}${Math.round(absolute).toLocaleString("ko-KR")}원`;
}

function domesticVolumeSentence(row: MarketShareChangeAsset): string {
  const transition = `${spokenKrw(row.previous_domestic_volume_krw)}에서 ${spokenKrw(row.current_domestic_volume_krw)}으로`;
  if (row.domestic_volume_change_krw > 0) {
    return `이 코인의 국내 거래금액은 ${transition} 늘었습니다.`;
  }
  if (row.domestic_volume_change_krw < 0) {
    return `이 코인의 국내 거래금액은 ${transition} 줄었습니다.`;
  }
  return `이 코인의 국내 거래금액은 ${spokenKrw(row.current_domestic_volume_krw)}으로 그대로입니다.`;
}

function shareHelpSentence(row: MarketShareChangeAsset): string {
  if (row.contribution_pp === 0) {
    return "빗썸 점유율에는 거의 영향을 주지 않았습니다.";
  }
  const direction = row.contribution_pp > 0 ? "높이는" : "낮추는";
  const movingDirection = row.contribution_pp > 0 ? "높아지는" : "낮아지는";
  const contribution = formatPercentagePointMagnitude(row.contribution_pp, 2);
  const bithumbUnchanged = row.bithumb_volume_change_krw === 0;
  const competitorsDisappeared =
    row.competitor_volume_change_krw < 0 &&
    row.previous_competitor_volume_krw > 0 &&
    row.current_competitor_volume_krw === 0;

  if (bithumbUnchanged && competitorsDisappeared) {
    return `빗썸 거래금액은 그대로였지만, 다른 거래소의 거래금액이 모두 사라지면서 빗썸 점유율을 ${contribution} ${direction} 데 도움이 됐습니다.`;
  }
  if (bithumbUnchanged && row.competitor_volume_change_krw < 0) {
    return `빗썸 거래금액은 그대로였지만, 다른 거래소의 거래금액이 줄면서 빗썸 점유율을 ${contribution} ${direction} 데 도움이 됐습니다.`;
  }
  if (bithumbUnchanged && row.competitor_volume_change_krw > 0) {
    return `빗썸 거래금액은 그대로였지만, 다른 거래소의 거래금액이 늘면서 빗썸 점유율이 ${contribution} ${movingDirection} 쪽으로 움직였습니다.`;
  }
  return `빗썸과 다른 거래소의 거래금액 변화를 함께 보면, 빗썸 점유율이 ${contribution} ${movingDirection} 쪽으로 움직였습니다.`;
}

export function assetImpactInterpretation(row: MarketShareChangeAsset): string {
  return `${domesticVolumeSentence(row)} ${shareHelpSentence(row)}`;
}
