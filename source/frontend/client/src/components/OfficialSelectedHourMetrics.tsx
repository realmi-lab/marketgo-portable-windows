import { InlineHelp } from "@/components/common/InlineHelp";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatOfficialHourContribution,
  formatOfficialHourMoneyChange,
  formatOfficialHourShare,
} from "@/lib/official-hour-metric-format";
import type { MarketShareOfficialAlignedContribution } from "@/lib/market-share-types";

function contributionTone(value: number | null | undefined): string {
  return value === null || value === undefined || !Number.isFinite(value)
    ? "text-muted-foreground"
    : value > 0
      ? "text-profit"
      : value < 0
        ? "text-loss"
        : "text-muted-foreground";
}

/** Main-dashboard opt-in: a fixed hour's raw values have one visible/accessible copy. */
export function OfficialSelectedHourMetrics({
  contribution,
}: {
  contribution: MarketShareOfficialAlignedContribution;
}) {
  return (
    <div className="official-hour-precise">
      <p className="official-hour-precise-basis inline-help-heading">
        정시마다 직전 24시간 합계를 비교합니다
        <InlineHelp text="한 시간 간격으로 비교한 최근 24시간 거래금액과 점유율입니다. 최근 한 시간에 새로 체결된 금액이 아닙니다. 점유율과 기여는 소수 둘째 자리(0.00으로 보이는 작은 값은 셋째 자리), 금액은 억 단위 소수 둘째 자리까지 표시합니다. 각 값을 따로 반올림하므로 표시값끼리 뺀 값이나 기여 합에 끝자리 차이가 날 수 있습니다. 계산은 반올림 전 공식 자료를 사용합니다." />
      </p>
      {/* The change is the answer, so it comes first and largest; the two
          levels it was computed from follow. */}
      <div className="official-selected-hour-metrics">
        <div className="official-hour-answer">
          <span>한 시간 전 대비 점유율 변화</span>
          <strong className={contributionTone(contribution.share_delta_pp)}>
            {formatOfficialHourContribution(contribution.share_delta_pp)}
          </strong>
        </div>
        <div>
          <span>빗썸 점유율</span>
          <div className="official-hour-share-pair">
            <div>
              <span>이전</span>
              <strong>
                {formatOfficialHourShare(
                  contribution.previous?.bithumb_share_pct
                )}
              </strong>
            </div>
            <span aria-hidden="true">→</span>
            <div>
              <span>선택 시간</span>
              <strong>
                {formatOfficialHourShare(
                  contribution.current?.bithumb_share_pct
                )}
              </strong>
            </div>
          </div>
        </div>
        <div>
          <span>국내 3곳 거래금액 변화</span>
          <strong>
            {formatOfficialHourMoneyChange(
              contribution.domestic_volume_delta_krw
            )}
          </strong>
        </div>
        <div>
          <span>빗썸 거래금액 변화</span>
          <strong>
            {formatOfficialHourMoneyChange(
              contribution.bithumb_volume_delta_krw
            )}
          </strong>
        </div>
      </div>
      <p className="official-hour-precise-impact inline-help-heading">
        거래소별 기여
        <InlineHelp text="거래소별 점유율 자체의 변화가 아니라 각 거래소의 거래금액 변화가 빗썸 점유율 변화에 기여한 계산값입니다. 예를 들어 코인원의 거래금액이 늘면 빗썸 점유율에는 음수 기여가 될 수 있습니다. 가장 큰 기여 코인은 해당 거래소 안에서 기여 절댓값이 가장 큰 코인입니다. 거래소 전체 금액 변화가 그 코인 하나의 금액은 아닙니다." />
      </p>
      <div className="official-selected-hour-exchanges">
        {contribution.exchange_drivers.map(row => (
          <div key={row.exchange}>
            <span className="official-hour-exchange-name">
              {exchangeLabel(row.exchange)}
            </span>
            <strong className={contributionTone(row.contribution_pp)}>
              {formatOfficialHourContribution(row.contribution_pp)}
            </strong>
            <dl className="official-hour-exchange-details">
              <div>
                <dt>거래소 전체 금액 변화</dt>
                <dd>{formatOfficialHourMoneyChange(row.volume_change_krw)}</dd>
              </div>
              <div>
                <dt>가장 큰 기여 코인</dt>
                <dd>{row.leading_asset || "확인 자료 없음"}</dd>
              </div>
            </dl>
          </div>
        ))}
      </div>
    </div>
  );
}
