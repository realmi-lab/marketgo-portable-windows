import { Activity, BarChart3, Clock3, Waves } from "lucide-react";

import { ExchangeBrandLabel } from "@/components/ExchangeBrandLabel";
import { InlineHelp } from "@/components/common/InlineHelp";
import {
  formatKRW,
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { marketShareWindowLabel } from "@/lib/market-share-labels";
import { formatSlippageRate } from "@/lib/market-numeric-presentation";
import { selectedWindowFromAnalysis } from "@/lib/selected-window-presentation";
import { selectTargetExchangeMetrics } from "@/lib/target-exchange";
import { exchangeLabel, exchangeNameKo } from "@/lib/exchanges";
import type { MarketShareQualityAnalysis } from "@/lib/market-share-types";
import type { DomesticExchange } from "@/lib/market-share-config";

function percent(value: number | null | undefined): string {
  return formatSharePercent(value, 2);
}

function points(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "—";
  }
  return formatSignedPercentagePoints(value);
}

function money(value: number | null | undefined, signed = false): string {
  return value !== null && value !== undefined && Number.isFinite(value)
    ? signed
      ? formatKrwCompact(value)
      : formatKRW(value)
    : "—";
}

function spreadPercent(value: number | null | undefined): string {
  return formatSlippageRate(value);
}

export function TargetExchangePerspectivePanel({
  analysis,
  targetExchangeOverride,
}: {
  analysis: MarketShareQualityAnalysis;
  targetExchangeOverride?: DomesticExchange;
}) {
  const targetExchange = targetExchangeOverride ?? "bithumb";
  const metricsValue = selectTargetExchangeMetrics(analysis, targetExchange);
  // The market intelligence page passes a frame that has no summary. The window
  // decision needs one, so without it only the per-exchange values are shown.
  const windowView = analysis.summary
    ? selectedWindowFromAnalysis(analysis)
    : null;
  // Compact market-analysis frames retain the canonical context, not every
  // duplicated top-level field from the full HTTP response.
  const windowLabel = marketShareWindowLabel(analysis.context.window_sec);
  const comparisonWindowLabel = marketShareWindowLabel(
    analysis.context.comparison_window_sec
  );
  const excludedComparison =
    analysis.change_attribution.excluded_exchanges ?? [];
  const comparisonScope = excludedComparison.length
    ? `${excludedComparison.map(exchangeLabel).join("·")} 제외`
    : "국내 거래소";

  const metrics = [
    {
      label: metricsValue.officialWindow
        ? "선택 기간 공식 24시간 점유율"
        : `최근 ${windowLabel} 관측 점유율`,
      value: percent(metricsValue.windowSharePct),
      detail: money(metricsValue.windowVolumeKrw),
      // A share that covers only the observed range is named by its basis; a
      // share that is absent says why, from values the server sent.
      basis:
        metricsValue.windowShareBasis ||
        (windowView &&
        metricsValue.windowSharePct === null &&
        !metricsValue.officialWindow
          ? windowView.shareReason
          : ""),
      help: metricsValue.officialWindow
        ? "국내 5개 거래소가 공개한 최근 24시간 원화 거래금액 중 이 거래소의 비중입니다."
        : `선택한 ${windowLabel}의 ${analysis.context.market_scope_label} 관측 거래금액 중 이 거래소의 비중입니다.`,
      icon: BarChart3,
    },
    {
      label: analysis.context.comparison_enabled
        ? `직전 ${comparisonWindowLabel} 대비`
        : "직전 구간 비교 안 함",
      basis: "",
      value: points(metricsValue.changePP),
      detail: money(metricsValue.volumeChangeKrw, true),
      help: `이 거래소 자체 점유율의 변화입니다. ${comparisonScope}의 현재 구간과 직전 같은 길이 구간을 비교하며, 빗썸 점유율에 대한 기여도와 다릅니다.`,
      icon: Activity,
    },
    {
      label: "현재 10단계 호가 금액 비중",
      basis: "",
      value: percent(metricsValue.liquiditySharePct),
      detail: `대표 매수·매도 가격 차이 ${spreadPercent(metricsValue.spreadBps)}`,
      help: "현재 사용할 수 있는 국내 거래소의 10단계 호가 금액 중 이 거래소의 비중입니다. 체결 거래금액 점유율과 다르며, 확인된 호가가 없으면 표시하지 않습니다.",
      icon: Waves,
    },
    {
      label: "공식 24시간 점유율 · 국내 5곳",
      basis: "",
      value: percent(metricsValue.officialSharePct),
      detail: `${metricsValue.officialVolumeRetained ? "마지막 수신 거래금액 " : "거래금액 "}${money(metricsValue.officialVolumeKrw)}`,
      help: "국내 5개 거래소가 공개한 최근 24시간 원화 거래금액 기준입니다. 일부 거래소가 확인되지 않으면 전체 점유율은 표시하지 않고, 확인된 거래금액만 따로 표시합니다.",
      icon: Clock3,
    },
  ] as const;

  return (
    <section
      className="glass-card overflow-hidden"
      aria-label="선택 거래소 관점"
    >
      <div className="flex items-center border-b border-border px-4 py-3">
        {/* The same panel shows 10 seconds on one screen and 1 minute on
            another, so the title names the window its values belong to. */}
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm font-semibold">
          <span className="inline-flex items-center gap-1.5">
            <ExchangeBrandLabel
              exchange={targetExchange}
              label={exchangeNameKo(targetExchange)}
            />{" "}
            기준
          </span>
          <span className="font-normal text-muted-foreground">
            최근 {windowLabel} 기준
          </span>
        </div>
      </div>
      <div className="grid divide-y divide-border/60 sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-4">
        {metrics.map(({ label, value, detail, basis, help, icon: Icon }) => (
          <div key={label} className="p-4">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Icon className="h-3.5 w-3.5" />
              {label}
              <InlineHelp text={help} />
            </div>
            <strong className="mt-2 block text-lg tabular-nums">{value}</strong>
            <span className="mt-1 block text-xs text-muted-foreground tabular-nums">
              {detail}
            </span>
            {basis ? (
              <span className="mt-1 block text-xs text-muted-foreground">
                {basis}
              </span>
            ) : null}
          </div>
        ))}
      </div>
      {targetExchange !== "bithumb" ? (
        <p className="border-t border-border px-4 py-2 text-xs text-muted-foreground">
          아래의 빗썸 기여 진단은 빗썸 기준 자료이고, 위 네 지표만 선택 거래소
          기준입니다.
        </p>
      ) : null}
    </section>
  );
}
