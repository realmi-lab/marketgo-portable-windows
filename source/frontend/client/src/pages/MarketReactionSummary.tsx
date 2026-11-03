import { type ReactNode } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowDownRight,
  BarChart3,
  Bell,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  Clock3,
  ExternalLink,
  GitBranch,
  Info,
  Layers3,
  Radio,
  Timer,
  X,
} from "lucide-react";

import { KpiCard } from "@/components/common/KpiCard";
import { Panel } from "@/components/common/Panel";
import { PageHeader } from "@/components/common/PageHeader";
import RollingShareSummary2 from "@/pages/RollingShareSummary2";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatKrwCompact,
  formatKrwPrice,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import {
  DOMESTIC_EXCHANGES,
  MARKET_SHARE_WINDOWS,
} from "@/lib/market-share-config";
import {
  marketShareWindowLabel,
  marketShareWindowStatusLabel,
  microstructureComparisonLabel,
  microstructureStatusLabel,
  noticeCategoryLabel as sharedNoticeCategoryLabel,
} from "@/lib/market-share-labels";
import {
  eventCurrentWindowContribution,
  noticeAssetLabels,
  observedExchangeReactionCount,
  reactionOriginTimingAnchor,
  selectedReactionDetailEvent,
} from "@/lib/market-share-reaction";
import type {
  MarketShareEvidenceStatus,
  MarketShareMicrostructureValues,
  MarketShareReactionEvent,
  MarketShareReactionSummary,
} from "@/lib/market-share-types";

const EVIDENCE_LABELS: Record<MarketShareEvidenceStatus, string> = {
  STRONG: "근거 강함",
  MODERATE: "근거 보통",
  WEAK: "근거 약함",
  UNAVAILABLE: "판단 보류",
};

const MICROSTRUCTURE_COMPARISON_METRICS: Array<{
  key: keyof MarketShareMicrostructureValues;
  label: string;
  kind: "price" | "money" | "percent" | "fill";
}> = [
  { key: "bithumb_mid_krw", label: "빗썸 매수·매도 중간 가격", kind: "price" },
  { key: "bithumb_spread_bps", label: "매수·매도 가격 차이", kind: "percent" },
  { key: "bithumb_depth_krw", label: "10단계 호가 잔량", kind: "money" },
  {
    key: "bithumb_buy_slippage_bps",
    label: "매수 예상 가격 손해",
    kind: "percent",
  },
  {
    key: "bithumb_sell_slippage_bps",
    label: "매도 예상 가격 손해",
    kind: "percent",
  },
  { key: "bithumb_buy_fill_pct", label: "매수 체결률", kind: "fill" },
  { key: "bithumb_sell_fill_pct", label: "매도 체결률", kind: "fill" },
];

function money(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwCompact(value).replace(/^\+/, "");
}

function price(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwPrice(value).replace(/^\+/, "");
}

function signedMoney(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwCompact(value);
}

function percent(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return `${value.toFixed(digits)}%`;
}

function signedPercent(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return `${value > 0 ? "+" : ""}${value.toFixed(digits)}%`;
}

function microstructureComparisonValue(
  value: number | null | undefined,
  kind: "price" | "money" | "percent" | "fill"
): string {
  if (kind === "price") return price(value);
  if (kind === "money") return money(value);
  if (kind === "fill") return percent(value);
  return percent(
    value === null || value === undefined ? value : value / 100,
    3
  );
}

function points(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  const precision = Math.min(2, Math.max(0, Math.trunc(digits)));
  return formatSignedPercentagePoints(value, precision);
}

function timestamp(value: number | null | undefined): string {
  if (
    value === null ||
    value === undefined ||
    !Number.isFinite(value) ||
    value <= 0
  )
    return "미정";
  return new Date(value).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function clock(value: number | null | undefined): string {
  if (
    value === null ||
    value === undefined ||
    !Number.isFinite(value) ||
    value <= 0
  )
    return "미정";
  return new Date(value).toLocaleTimeString("ko-KR", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function duration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds))
    return "—";
  const absolute = Math.abs(seconds);
  if (absolute >= 3600) return `${(seconds / 3600).toFixed(1)}시간`;
  if (absolute >= 60) return `${(seconds / 60).toFixed(1)}분`;
  return `${seconds.toFixed(1)}초`;
}

function noticeTimestamp(event: MarketShareReactionEvent): string {
  if (event.timestamp_precision === "SECOND")
    return timestamp(event.published_at_ms);
  if (event.timestamp_precision === "DETECTED") {
    return event.collected_at_ms !== null
      ? `${timestamp(event.collected_at_ms)} · 감지 시각`
      : "감지 시각 정보 없음";
  }
  if (event.published_at_ms === null) return "정보 없음";
  return `${new Date(event.published_at_ms).toLocaleDateString("ko-KR")} · 일 단위`;
}

function eventElapsed(asOf: number, event: MarketShareReactionEvent): string {
  const anchor = reactionOriginTimingAnchor(event);
  if (anchor.time === null || !Number.isFinite(anchor.time))
    return "기준 시각 경과 정보 없음";
  const seconds = asOf - anchor.time / 1000;
  return seconds >= 0
    ? `${anchor.shortLabel} 후 ${duration(seconds)}`
    : `${anchor.shortLabel} 전 ${duration(-seconds)}`;
}

function timelineTimestamp(
  event: MarketShareReactionEvent,
  item: MarketShareReactionEvent["timeline"][number]
): string {
  if (item.kind === "NOTICE" && event.timestamp_precision !== "SECOND") {
    return event.timestamp_precision === "DAY"
      ? "게시일만 확인"
      : "게시 시각 정보 없음";
  }
  return timestamp(item.at_ms);
}

function timelineStatusDescription(
  item: MarketShareReactionEvent["timeline"][number]
): string {
  if (item.status !== "OBSERVED") return "현재 자료에 시각 정보 없음";
  if (item.kind === "NOTICE") return "공지 원문 게시시각 확인";
  if (item.kind === "EFFECTIVE" || item.kind === "ENDED")
    return "공지 원문 일정 확인";
  return "실제 시장 반응 시각 확인";
}

function reactionTimingLabel(event: MarketShareReactionEvent): string {
  const anchor = reactionOriginTimingAnchor(event);
  const reactionAt = event.origin.domestic_reaction_time_ms;
  if (anchor.time === null || reactionAt === null)
    return "연속 반응 시각 정보 없음";
  const seconds = (reactionAt - anchor.time) / 1000;
  return seconds >= 0
    ? `${duration(seconds)} 후 시작 · ${anchor.shortLabel}`
    : `${duration(-seconds)} 전 시작 · ${anchor.shortLabel}`;
}

function originBasisLabel(event: MarketShareReactionEvent): string {
  return reactionOriginTimingAnchor(event).longLabel;
}

function categoryLabel(value: string): string {
  return sharedNoticeCategoryLabel(value);
}

function domesticExchangeLabel(value: string): string {
  const labels: Record<string, string> = {
    upbit: "업비트",
    bithumb: "빗썸",
    coinone: "코인원",
    korbit: "코빗",
    gopax: "고팍스",
  };
  return labels[value.toLowerCase()] ?? exchangeLabel(value);
}

function maturityLabel(value: string): string {
  if (value === "UNAVAILABLE") return "반응 단계 자료 없음";
  if (value === "AFTERCARE") return "사후 확인";
  if (value === "SUSTAINED") return "반응 지속";
  if (value === "PARTIAL") return "일부 확인";
  if (value === "EARLY") return "초기 반응";
  return "반응 단계 확인 필요";
}

function evidenceLabel(value: string): string {
  return EVIDENCE_LABELS[value as MarketShareEvidenceStatus] ?? "판단 보류";
}

function evidenceTone(value: MarketShareEvidenceStatus): string {
  if (value === "STRONG") return "border-profit/30 bg-profit/10 text-profit";
  if (value === "MODERATE") return "border-info/30 bg-info/10 text-info";
  if (value === "WEAK") return "border-gold/30 bg-gold/10 text-gold";
  return "border-border bg-secondary text-muted-foreground";
}

function originTone(value: string): string {
  if (value === "GLOBAL_FIRST" || value === "BINANCE_FIRST") return "text-info";
  if (value === "DOMESTIC_NOTICE_FIRST") return "text-gold";
  if (value === "DOMESTIC_MARKET_FIRST") return "text-profit";
  if (value === "MIXED") return "text-primary";
  return "text-muted-foreground";
}

function changeTone(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "text-muted-foreground";
  if (value > 0) return "text-profit";
  if (value < 0) return "text-loss";
  return "text-muted-foreground";
}

function statusLabel(value: string): string {
  return marketShareWindowStatusLabel(value);
}

function exchangeObservationLabel(
  value:
    | MarketShareReactionEvent["exchange_reactions"][number]["data_status"]
    | undefined
): string {
  if (value === "OBSERVED") return "확인";
  if (value === "OBSERVED_ZERO") return "체결 없음 확인";
  if (value === "PARTIAL_OBSERVATION") return "일부 확인";
  if (value === "UNRESOLVED_FX") return "원화 환산 미완료";
  if (value === "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY") return "검증 보류";
  return "계산 불가";
}

function StatusBadge({
  label,
  tone = "muted",
}: {
  label: string;
  tone?: "live" | "warn" | "muted" | "error";
}) {
  const classes =
    tone === "live"
      ? "border-profit/30 bg-profit/10 text-profit"
      : tone === "warn"
        ? "border-gold/30 bg-gold/10 text-gold"
        : tone === "error"
          ? "border-loss/30 bg-loss/10 text-loss"
          : "border-border bg-secondary text-muted-foreground";
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap border px-2 py-1 text-base ${classes}`}
    >
      <CircleDot className="h-3 w-3" />
      {label}
    </span>
  );
}

function ReactionPath({ event }: { event: MarketShareReactionEvent | null }) {
  const observedExchangeCount = event
    ? observedExchangeReactionCount(event)
    : 0;
  const exchangeComparisonPartial = event?.data_quality.status === "PARTIAL";
  const steps = event
    ? [
        {
          label: originBasisLabel(event),
          detail: `${domesticExchangeLabel(event.exchange)} · ${categoryLabel(event.event_type)}`,
          time: event.origin.event_time_ms,
          state: event.origin.event_time_ms ? "관측" : "시각 없음",
          tone: "gold",
        },
        {
          label: "국내 시장 반응",
          detail: reactionTimingLabel(event),
          time: event.origin.domestic_reaction_time_ms,
          state: event.origin.domestic_reaction_time_ms ? "관측" : "자료 부족",
          tone: "profit",
        },
        {
          label: "거래소별 변화",
          detail: observedExchangeCount
            ? `${observedExchangeCount}개 거래소 확인${exchangeComparisonPartial ? " · 일부 누락" : ""}`
            : event.exchange_reactions.length
              ? "전후 비교 자료 부족"
              : "전후 비교 자료 없음",
          time: null,
          state:
            observedExchangeCount && !exchangeComparisonPartial
              ? "비교"
              : event.exchange_reactions.length
                ? "자료 부족"
                : "정보 없음",
          tone: "info",
        },
        {
          label: "바이낸스 공식 24시간 참고",
          detail: "독립 참고값 · 공지·반응 이벤트와 매칭하지 않음",
          time: null,
          state: "정책 분리",
          tone: "info",
        },
        {
          label: "현재 구간 점유율 기여",
          detail: event.linked_assets.length
            ? `${points(event.linked_asset_current_window_contribution_pp)} · ${event.linked_assets.length}개 자산 (공지 영향과 별개 값)`
            : "연결 자산 없음",
          time: null,
          state:
            event.linked_asset_current_window_contribution_pp !== null
              ? "별도 계산"
              : "자료 부족",
          tone:
            event.linked_asset_current_window_contribution_pp === null
              ? "info"
              : event.linked_asset_current_window_contribution_pp >= 0
                ? "profit"
                : "loss",
        },
      ]
    : [];

  return (
    <div className="grid gap-px bg-border sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
      {steps.map((step, index) => (
        <div key={step.label} className="relative min-w-0 bg-card p-4">
          <div className="flex items-center justify-between gap-2">
            <span className="text-base uppercase tracking-[0.08em] text-muted-foreground">
              0{index + 1}
            </span>
            <span
              className={`text-base ${step.tone === "loss" ? "text-loss" : step.tone === "profit" ? "text-profit" : step.tone === "gold" ? "text-gold" : "text-info"}`}
            >
              {step.state}
            </span>
          </div>
          <p className="mt-3 text-sm font-semibold">{step.label}</p>
          <p className="mt-1 min-h-10 text-xs leading-6 text-muted-foreground">
            {step.detail}
          </p>
          <p className="mt-3 font-mono text-base text-foreground">
            {step.time ? clock(step.time) : "—"}
          </p>
          {index < steps.length - 1 && (
            <ChevronRight className="absolute -right-3 top-1/2 z-10 hidden h-5 w-5 -translate-y-1/2 text-muted-foreground lg:block" />
          )}
        </div>
      ))}
      {!event && (
        <div className="bg-card p-5 text-xs text-muted-foreground lg:col-span-5">
          선택 구간에 연결된 공지가 없어 반응 경로를 구성하지 않았습니다.
        </div>
      )}
    </div>
  );
}

function NoticeRow({
  event,
  onSelect,
}: {
  event: MarketShareReactionEvent;
  onSelect: () => void;
}) {
  const assetLabels = noticeAssetLabels(event);
  const timingAnchor = reactionOriginTimingAnchor(event);
  const currentWindowContribution = eventCurrentWindowContribution(event);
  return (
    <button
      type="button"
      onClick={onSelect}
      className="grid w-full gap-3 border-b border-border/70 px-4 py-4 text-left transition-colors last:border-b-0 hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none md:grid-cols-[100px_112px_minmax(220px,1fr)_150px_125px_120px] md:items-center"
    >
      <div>
        <p className="text-base text-muted-foreground">
          {timingAnchor.shortLabel}
        </p>
        <p className="mt-1 font-mono text-xs text-foreground">
          {clock(timingAnchor.time)}
        </p>
        <p className="mt-1 text-base text-muted-foreground">
          {maturityLabel(event.maturity)}
        </p>
      </div>
      <div>
        <p className="text-xs font-semibold">
          {domesticExchangeLabel(event.exchange)}
        </p>
        <p className="mt-1 text-base text-muted-foreground">
          {categoryLabel(event.event_type)}
          {event.related_notice_count > 1
            ? ` · 같은 공지 흐름 ${event.related_notice_count}건 묶음`
            : ""}
        </p>
      </div>
      <div className="min-w-0">
        <p className="truncate text-xs font-medium">
          {event.title || "제목 없음"}
        </p>
        <div className="mt-1 flex flex-wrap gap-1.5">
          {assetLabels.slice(0, 5).map(asset => (
            <span
              key={asset}
              className="border border-border bg-secondary px-1.5 py-0.5 text-base text-muted-foreground"
            >
              {asset}
            </span>
          ))}
          {assetLabels.length > 5 && (
            <span className="text-base text-muted-foreground">
              +{assetLabels.length - 5}
            </span>
          )}
        </div>
      </div>
      <div className="text-xs">
        <p
          className={changeTone(
            event.linked_assets.length ? currentWindowContribution : null
          )}
        >
          {event.linked_assets.length ? points(currentWindowContribution) : "—"}
        </p>
        <p className="mt-1 text-base text-muted-foreground">
          {event.linked_assets.length
            ? `연결 자산 ${event.linked_assets.length}개`
            : "연결 자산 없음"}
        </p>
      </div>
      <div className="text-xs">
        <p className={changeTone(event.domestic_volume_change_pct)}>
          {signedPercent(event.domestic_volume_change_pct)}
        </p>
        <p className="mt-1 text-base text-muted-foreground">국내 거래금액</p>
      </div>
      <div className="flex items-center justify-between gap-2 md:block md:text-right">
        <span
          className={`inline-flex border px-2 py-1 text-base ${evidenceTone(event.evidence_level)}`}
        >
          {evidenceLabel(event.evidence_level)}
        </span>
        <p className={`mt-1 text-base ${originTone(event.origin.type)}`}>
          {event.origin.label}
        </p>
        <p className="mt-1 text-base text-muted-foreground">
          {event.reaction_label}
        </p>
      </div>
    </button>
  );
}

function DetailRow({
  label,
  value,
  tone,
}: {
  label: string;
  value: ReactNode;
  tone?: string;
}) {
  return (
    <div className="grid grid-cols-[minmax(108px,0.65fr)_minmax(0,1.35fr)] gap-3 border-b border-border/60 py-2.5 text-xs last:border-b-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={`min-w-0 break-words ${tone ?? "text-foreground"}`}>
        {value}
      </dd>
    </div>
  );
}

function EventDetail({
  event,
  onClose,
}: {
  event: MarketShareReactionEvent;
  onClose: () => void;
}) {
  const hasCurrentWindowContribution =
    event.linked_asset_current_window_contribution_pp !== null;
  const assetLabels = noticeAssetLabels(event);
  const microstructureComparisons = event.microstructure.filter(
    row => row.comparison && row.comparison_status !== "CURRENT_SNAPSHOT_ONLY"
  );
  const assetLabel =
    (event.affected_assets ?? []).length > 0 ? "대상 자산" : "연결 자산";
  const studyWindowDescription = event.reaction_study
    ? `기준 시각 전 ${marketShareWindowLabel(event.reaction_study.pre_window_sec)}·후 ${marketShareWindowLabel(event.reaction_study.post_window_sec)} 확인 구간`
    : "기준 시각 전후 확인 구간";
  return (
    <>
      <button
        type="button"
        aria-label="공지 상세 닫기"
        onClick={onClose}
        className="fixed inset-0 z-40 bg-background/60 backdrop-blur-[1px]"
      />
      <aside
        role="dialog"
        aria-modal="true"
        aria-label="공지 상세"
        className="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col border-l border-border bg-background shadow-2xl"
      >
        <div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span
                className={`border px-2 py-1 text-base ${evidenceTone(event.evidence_level)}`}
              >
                {evidenceLabel(event.evidence_level)}
              </span>
              <span className={`text-xs ${originTone(event.origin.type)}`}>
                {event.origin.label}
              </span>
            </div>
            <h2 className="mt-3 text-base font-semibold leading-6">
              {event.title || "공지 상세"}
            </h2>
            <p className="mt-1 text-xs text-muted-foreground">
              {domesticExchangeLabel(event.exchange)} ·{" "}
              {categoryLabel(event.event_type)} · 게시 {noticeTimestamp(event)}
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="공지 상세 닫기"
            className="flex h-8 w-8 shrink-0 items-center justify-center border border-border text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto p-5">
          <div className="space-y-6">
            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Bell className="h-4 w-4 text-muted-foreground" />
                공지 정보
              </h3>
              <dl className="mt-3 border-y border-border/70">
                <DetailRow
                  label="거래소"
                  value={domesticExchangeLabel(event.exchange)}
                />
                <DetailRow
                  label="공지 유형"
                  value={categoryLabel(event.event_type)}
                />
                <DetailRow label="게시 시각" value={noticeTimestamp(event)} />
                <DetailRow
                  label="수집 시각"
                  value={timestamp(event.collected_at_ms)}
                />
                <DetailRow
                  label="분석 기준 시각"
                  value={`${timestamp(event.origin.event_time_ms)} · ${originBasisLabel(event)}`}
                />
                <DetailRow
                  label="최초 적용 시각"
                  value={timestamp(event.effective_at_ms)}
                />
                <DetailRow
                  label="최종 종료·재개 시각"
                  value={timestamp(event.ended_at_ms)}
                />
                {event.related_notice_count > 1 && (
                  <DetailRow
                    label="같은 공지 흐름"
                    value={`${event.related_notice_count}건을 하나의 사건으로 묶음`}
                  />
                )}
                <DetailRow
                  label={assetLabel}
                  value={
                    assetLabels.length ? assetLabels.join(", ") : "매핑 없음"
                  }
                />
                <DetailRow
                  label="원문"
                  value={
                    event.source_url ? (
                      <a
                        href={event.source_url}
                        target="_blank"
                        rel="noreferrer"
                        className="inline-flex items-center gap-1 text-info hover:underline"
                      >
                        공식 공지 열기 <ExternalLink className="h-3 w-3" />
                      </a>
                    ) : (
                      "—"
                    )
                  }
                />
              </dl>
            </section>

            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <BarChart3 className="h-4 w-4 text-primary" />
                공지 연결 자산의 현재 구간 점유율 기여
              </h3>
              <p className="mt-1 text-xs leading-6 text-muted-foreground">
                공지 전후 영향값이 아닙니다. 현재 선택 구간에서 계산된 자산별
                점유율 기여를 공지 대상 자산과 별도로 대조한 값입니다.
              </p>
              <div className="mt-3 grid grid-cols-3 gap-px bg-border">
                <div className="bg-card p-3">
                  <p className="text-base text-muted-foreground">
                    현재 구간 순기여
                  </p>
                  <p
                    className={`mt-1 text-lg font-semibold ${changeTone(event.linked_asset_current_window_contribution_pp)}`}
                  >
                    {hasCurrentWindowContribution
                      ? points(
                          event.linked_asset_current_window_contribution_pp
                        )
                      : "—"}
                  </p>
                </div>
                <div className="bg-card p-3">
                  <p className="text-base text-muted-foreground">상승 연결</p>
                  <p
                    className={`mt-1 text-lg font-semibold ${event.positive_current_window_contribution_pp === null ? "text-muted-foreground" : "text-profit"}`}
                  >
                    {event.positive_current_window_contribution_pp === null
                      ? "—"
                      : points(event.positive_current_window_contribution_pp)}
                  </p>
                </div>
                <div className="bg-card p-3">
                  <p className="text-base text-muted-foreground">하락 연결</p>
                  <p
                    className={`mt-1 text-lg font-semibold ${event.negative_current_window_contribution_pp === null ? "text-muted-foreground" : "text-loss"}`}
                  >
                    {event.negative_current_window_contribution_pp === null
                      ? "—"
                      : points(event.negative_current_window_contribution_pp)}
                  </p>
                </div>
              </div>
              <div className="mt-3 overflow-x-auto border border-border">
                <table
                  className="market-data-table w-full text-left text-xs"
                  data-table-schema="label-numeric-label"
                >
                  <thead>
                    <tr>
                      <th>자산</th>
                      <th className="text-right">현재 구간 점유율 기여</th>
                      <th className="text-right">국내 거래금액</th>
                      <th className="text-right">국내 거래금액 증감</th>
                      <th>상장 구조</th>
                    </tr>
                  </thead>
                  <tbody>
                    {event.linked_assets.map(asset => (
                      <tr key={asset.asset}>
                        <td className="font-semibold">{asset.asset}</td>
                        <td
                          className={`text-right ${changeTone(asset.current_window_contribution_pp)}`}
                        >
                          {points(asset.current_window_contribution_pp)}
                        </td>
                        <td className="text-right">
                          {money(asset.domestic_volume_krw)}
                        </td>
                        <td
                          className={`text-right ${changeTone(asset.domestic_volume_change_krw)}`}
                        >
                          {signedMoney(asset.domestic_volume_change_krw)}
                        </td>
                        <td>
                          {asset.listed_on_bithumb
                            ? asset.domestic_exchanges
                                .map(domesticExchangeLabel)
                                .join(" · ") || "빗썸 상장"
                            : "빗썸 미상장"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <GitBranch className="h-4 w-4 text-muted-foreground" />
                기준 시각 전후 반응 타임라인
              </h3>
              <div className="mt-3 space-y-0 border-l border-border pl-5">
                {event.timeline.map(item => (
                  <div
                    key={`${item.kind}-${item.at_ms ?? "unavailable"}-${item.status}`}
                    className="relative border-b border-border/60 py-3 last:border-b-0"
                  >
                    <span
                      className={`absolute -left-[25px] top-4 h-2.5 w-2.5 rounded-full border-2 border-background ${item.status === "OBSERVED" ? "bg-info" : "bg-secondary"}`}
                    />
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <p className="text-xs font-semibold">{item.label}</p>
                      <span className="font-mono text-base text-muted-foreground">
                        {timelineTimestamp(event, item)}
                      </span>
                    </div>
                    <p className="mt-1 text-base text-muted-foreground">
                      {timelineStatusDescription(item)}
                    </p>
                  </div>
                ))}
              </div>
              <div className="mt-3 border border-border bg-inset p-3 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className={`font-semibold ${originTone(event.origin.type)}`}
                  >
                    {event.origin.label}
                  </span>
                  <span
                    className={`border px-2 py-0.5 text-base ${evidenceTone(event.origin.evidence_level)}`}
                  >
                    {evidenceLabel(event.origin.evidence_level)}
                  </span>
                </div>
                <p className="mt-2 leading-6 text-muted-foreground">
                  {event.evidence_reasons.join(" · ")}
                </p>
                <p className="mt-2 text-base text-muted-foreground">
                  {event.data_quality.note}
                </p>
              </div>
            </section>

            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Activity className="h-4 w-4 text-profit" />
                국내 거래소별 반응
              </h3>
              <p className="mt-1 text-xs text-muted-foreground">
                {originBasisLabel(event)} {studyWindowDescription}의 거래금액을
                동일한 계약으로 비교합니다.
              </p>
              <div className="mt-3 overflow-x-auto border border-border">
                <table
                  className="market-data-table w-full text-left text-xs"
                  data-table-schema="label-numeric-status"
                >
                  <thead>
                    <tr>
                      <th>거래소</th>
                      <th className="text-right">기준 전</th>
                      <th className="text-right">기준 후</th>
                      <th className="text-right">거래금액 변화</th>
                      <th className="text-right">체결 수</th>
                      <th>상태</th>
                    </tr>
                  </thead>
                  <tbody>
                    {DOMESTIC_EXCHANGES.map(exchange => {
                      const row = event.exchange_reactions.find(
                        item => item.exchange === exchange
                      );
                      const beforeTrades =
                        row?.before_trade_count === null ||
                        row?.before_trade_count === undefined
                          ? "—"
                          : row.before_trade_count.toLocaleString();
                      const afterTrades =
                        row?.after_trade_count === null ||
                        row?.after_trade_count === undefined
                          ? "—"
                          : row.after_trade_count.toLocaleString();
                      return (
                        <tr key={exchange}>
                          <td className="font-semibold">
                            {domesticExchangeLabel(exchange)}
                          </td>
                          <td className="text-right">
                            {money(row?.before_volume_krw)}
                          </td>
                          <td className="text-right">
                            {money(row?.after_volume_krw)}
                          </td>
                          <td
                            className={`text-right ${changeTone(row?.volume_change_pct)}`}
                          >
                            {signedPercent(row?.volume_change_pct)}
                          </td>
                          <td className="text-right">
                            {row ? `${beforeTrades} → ${afterTrades}` : "—"}
                          </td>
                          <td>
                            {exchangeObservationLabel(row?.data_status)}
                            {row?.data_status === "UNRESOLVED_FX" && (
                              <span className="block text-xs text-muted-foreground">
                                기준 전{" "}
                                {row.before_unresolved_fx_count?.toLocaleString() ??
                                  "미확인"}
                                건 · 기준 후{" "}
                                {row.after_unresolved_fx_count?.toLocaleString() ??
                                  "미확인"}
                                건
                              </span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </section>

            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Radio className="h-4 w-4 text-muted-foreground" />
                바이낸스 공식 24시간 참고
              </h3>
              <p className="mt-3 border border-info/20 bg-info/10 p-3 text-xs leading-6 text-muted-foreground">
                바이낸스 실시간 체결·호가 수집과 공지 연결은 정책상 비활성화되어
                있습니다. 공식 24시간 거래금액은 코인 분석 화면의 독립 참고
                탭에서만 확인하며 이 공지 반응 분석에는 결합하지 않습니다.
              </p>
            </section>

            <section>
              <h3 className="flex items-center gap-2 text-sm font-semibold">
                <Layers3 className="h-4 w-4 text-muted-foreground" />
                가격·호가·예상 가격 손해
              </h3>
              <p className="mt-1 text-xs leading-6 text-muted-foreground">
                현재 기준 시각의 자료와, 확보된 경우 공지 전후 확인 자료를
                분리합니다. 없는 값은 변화값으로 표시하지 않습니다.
              </p>
              <div className="mt-3 overflow-x-auto border border-border">
                <table
                  className="market-data-table w-full text-left text-xs"
                  data-table-schema="four-label-numeric"
                >
                  <thead>
                    <tr>
                      <th>자산</th>
                      <th>상태</th>
                      <th>비교 범위</th>
                      <th>기준 시각</th>
                      <th className="text-right">빗썸 매수·매도 중간 가격</th>
                      <th className="text-right">매수·매도 가격 차이</th>
                      <th className="text-right">10단계 호가 잔량</th>
                      <th className="text-right">매수 예상 가격 손해</th>
                      <th className="text-right">매도 예상 가격 손해</th>
                      <th className="text-right">매수 체결률</th>
                      <th className="text-right">매도 체결률</th>
                    </tr>
                  </thead>
                  <tbody>
                    {event.microstructure.map(row => (
                      <tr key={row.asset}>
                        <td className="font-semibold">{row.asset}</td>
                        <td>{microstructureStatusLabel(row.status)}</td>
                        <td>
                          {microstructureComparisonLabel(row.comparison_status)}
                        </td>
                        <td className="whitespace-nowrap font-mono text-base">
                          {timestamp(row.as_of_ms)}
                        </td>
                        <td className="text-right">
                          {price(row.bithumb_mid_krw)}
                        </td>
                        <td className="text-right">
                          {row.bithumb_spread_bps === null
                            ? "—"
                            : `${(row.bithumb_spread_bps / 100).toFixed(3)}%`}
                        </td>
                        <td className="text-right">
                          {money(row.bithumb_depth_krw)}
                        </td>
                        <td className="text-right">
                          {row.bithumb_buy_slippage_bps === null
                            ? "—"
                            : `${(row.bithumb_buy_slippage_bps / 100).toFixed(3)}%`}
                        </td>
                        <td className="text-right">
                          {row.bithumb_sell_slippage_bps === null
                            ? "—"
                            : `${(row.bithumb_sell_slippage_bps / 100).toFixed(3)}%`}
                        </td>
                        <td className="text-right">
                          {percent(row.bithumb_buy_fill_pct)}
                        </td>
                        <td className="text-right">
                          {percent(row.bithumb_sell_fill_pct)}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {microstructureComparisons.length > 0 && (
                <div className="mt-4 border-t border-border pt-4">
                  <p className="text-xs font-semibold">공지 전후 관측 비교</p>
                  <p className="mt-1 text-base leading-6 text-muted-foreground">
                    각 구간에서 확인된 실시간 호가의 중앙값입니다.{" "}
                    {microstructureComparisons
                      .map(
                        row =>
                          `${row.asset} ${microstructureComparisonLabel(row.comparison_status)} · 기준 전 ${row.comparison?.pre_point_count ?? 0}개 · 기준 후 ${row.comparison?.post_point_count ?? 0}개`
                      )
                      .join(" / ")}
                  </p>
                  <div className="mt-3 overflow-x-auto border border-border">
                    <table
                      className="market-data-table w-full text-left text-xs"
                      data-table-schema="label-detail-numeric"
                    >
                      <thead>
                        <tr>
                          <th>자산</th>
                          <th>지표</th>
                          <th className="text-right">기준 전</th>
                          <th className="text-right">기준 후</th>
                          <th className="text-right">변화율</th>
                        </tr>
                      </thead>
                      <tbody>
                        {microstructureComparisons.flatMap(row =>
                          MICROSTRUCTURE_COMPARISON_METRICS.map(metric => {
                            const comparison = row.comparison;
                            if (!comparison) return null;
                            const before = comparison.before[metric.key];
                            const after = comparison.after[metric.key];
                            const change = comparison.change_pct[metric.key];
                            return (
                              <tr key={`${row.asset}-${metric.key}`}>
                                <td className="font-semibold">{row.asset}</td>
                                <td>{metric.label}</td>
                                <td className="text-right">
                                  {microstructureComparisonValue(
                                    before,
                                    metric.kind
                                  )}
                                </td>
                                <td className="text-right">
                                  {microstructureComparisonValue(
                                    after,
                                    metric.kind
                                  )}
                                </td>
                                <td
                                  className={`text-right ${changeTone(change)}`}
                                >
                                  {signedPercent(change)}
                                </td>
                              </tr>
                            );
                          })
                        )}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
              {!event.microstructure.length && (
                <p className="border-t border-border p-4 text-xs text-muted-foreground">
                  연결 자산의 현재 호가 자료가 없습니다.
                </p>
              )}
            </section>
          </div>
        </div>
      </aside>
    </>
  );
}

function Summary2Header({
  windowSec,
  onWindowChange,
  reaction,
}: {
  windowSec: number;
  onWindowChange: (value: number) => void;
  reaction: MarketShareReactionSummary;
}) {
  return (
    <PageHeader
      title="요약 2 · 공지와 시장 반응"
      subtitle={`업비트·빗썸을 포함한 국내 5개 거래소 공지와 반응만 연결합니다. 바이낸스 공식 24시간 거래금액은 별도 참고값이며 이 화면의 공지와 연결하지 않습니다. 공지와 반응은 시간이 가까울 뿐, 원인으로 단정하지 않습니다. · 원화 현물 · ${marketShareWindowLabel(windowSec)} · 기준 시각 ${timestamp(reaction.as_of * 1000)}`}
      icon={<GitBranch className="h-5 w-5" />}
      actions={
        <div className="flex w-full flex-wrap items-center gap-2 2xl:w-auto 2xl:justify-end">
          <div className="segmented-control flex max-w-full overflow-x-auto border border-border bg-inset p-1">
            {MARKET_SHARE_WINDOWS.map(window => (
              <button
                key={window.seconds}
                type="button"
                onClick={() => onWindowChange(window.seconds)}
                className={`segmented-control-item whitespace-nowrap px-3 py-1.5 text-xs transition-colors ${windowSec === window.seconds ? "segmented-control-item-active bg-background text-foreground" : "text-muted-foreground hover:text-foreground"}`}
              >
                {window.label}
              </button>
            ))}
          </div>
          <a
            href="#summary"
            className="inline-flex items-center gap-1.5 border border-border px-3 py-2 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <BarChart3 className="h-3.5 w-3.5" />
            요약 1 전체 분석
          </a>
        </div>
      }
    />
  );
}

function NoNoticeContext({
  context,
}: {
  context: MarketShareReactionSummary["no_notice_context"];
}) {
  const momentum = context.momentum;
  const leadingAsset = context.leading_asset;
  const marketSignal = context.market_signal;
  const marketSignalValue = marketSignal
    ? marketSignal.value_pct !== null && marketSignal.value_pct !== undefined
      ? marketSignal.kind === "GLOBAL_INTEREST_GAP"
        ? points(marketSignal.value_pct)
        : percent(marketSignal.value_pct)
      : marketSignal.value_bps !== null && marketSignal.value_bps !== undefined
        ? percent(marketSignal.value_bps / 100, 2)
        : money(marketSignal.value_krw)
    : "—";
  return (
    <Panel
      title="공지 없는 구간의 국내 시장 움직임"
      description={context.message}
      icon={<Radio className="h-4 w-4" />}
    >
      <div className="grid gap-px bg-border sm:grid-cols-2 xl:grid-cols-4">
        <div className="bg-card p-4">
          <p className="text-base text-muted-foreground">국내 거래금액</p>
          <p className="mt-1 text-lg font-semibold">
            {money(context.domestic_volume_krw)}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            업비트·빗썸 등 국내 5개 거래소
          </p>
        </div>
        <div className="bg-card p-4">
          <p className="text-base text-muted-foreground">국내 자체 모멘텀</p>
          <p
            className={`mt-1 text-lg font-semibold ${changeTone(momentum?.change_pp)}`}
          >
            {points(momentum?.change_pp)}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            10초 {percent(momentum?.short_share_pct, 2)} · 1시간{" "}
            {percent(momentum?.long_share_pct, 2)}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            10초 상태 {statusLabel(momentum?.short_status ?? "UNAVAILABLE")}
          </p>
        </div>
        <div className="bg-card p-4">
          <p className="text-base text-muted-foreground">현재 빗썸 점유율</p>
          <p className="mt-1 text-lg font-semibold">
            {percent(context.domestic_share_pct, 2)}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            국내 거래금액 안에서의 현재 비중
          </p>
        </div>
        <div className="bg-card p-4">
          <p className="text-base text-muted-foreground">
            현재 구간 자산별 점유율 기여
          </p>
          <p className="mt-1 text-lg font-semibold">
            {leadingAsset?.asset || "—"}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            {leadingAsset
              ? `기여 ${points(leadingAsset.share_change_contribution_pp)} · 거래금액 ${money(leadingAsset.domestic_volume_krw)}`
              : "현재 구간 기여값 없음"}
          </p>
        </div>
      </div>
      <p className="border-t border-border px-4 py-3 text-base text-muted-foreground">
        바이낸스 실시간 반응은 연결하지 않으며, 공식 24시간 거래금액도 이 이벤트
        분석에는 사용하지 않습니다.
      </p>
      {marketSignal && (
        <div className="border-t border-border bg-inset p-4 text-xs">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="font-semibold text-gold">시장 이상 관측</span>
            <span className="font-mono text-foreground">
              {marketSignalValue}
            </span>
          </div>
          <p className="mt-1 font-medium">
            {marketSignal.title}
            {marketSignal.asset ? ` · ${marketSignal.asset}` : ""}
          </p>
          <p className="mt-1 leading-6 text-muted-foreground">
            {marketSignal.detail}
          </p>
        </div>
      )}
    </Panel>
  );
}

function Summary2Content({
  reaction,
  selectedEvent,
  onSelect,
  windowSec,
}: {
  reaction: MarketShareReactionSummary;
  selectedEvent: MarketShareReactionEvent | null;
  onSelect: (event: MarketShareReactionEvent) => void;
  windowSec: number;
}) {
  const events = reaction.events.length
    ? reaction.events
    : selectedEvent
      ? [selectedEvent]
      : [];
  const selectedOrigin = selectedEvent?.origin ?? reaction.origin;
  const hasNotice = reaction.notice_summary.event_count > 0;
  const linkedContribution =
    reaction.notice_summary.linked_asset_current_window_contribution_pp;
  const hasLinkedContribution = linkedContribution !== null;
  const contributionAssetCountLabel = hasLinkedContribution
    ? `상승 연결 ${reaction.notice_summary.positive_current_window_contribution_asset_count ?? 0}개 · 하락 연결 ${reaction.notice_summary.negative_current_window_contribution_asset_count ?? 0}개`
    : "상승·하락 연결 자산 영향 자료 부족";
  const hasCurrentShare =
    reaction.market_share.status !== "UNAVAILABLE" &&
    reaction.market_share.current_available;
  const hasShareComparison =
    reaction.market_share.previous_pct !== null &&
    Number.isFinite(reaction.market_share.previous_pct) &&
    reaction.market_share.change_pp !== null &&
    Number.isFinite(reaction.market_share.change_pp);
  const largestExchange = reaction.largest_exchange_change;
  const dataStatus = reaction.market_share.status;
  const noticeTime = selectedOrigin.event_time_ms;
  const selectedTitle = selectedEvent?.title?.trim() || "제목 없음";
  const selectedAssetCount = selectedEvent
    ? noticeAssetLabels(selectedEvent).length
    : 0;
  const selectedTimingAnchor = selectedEvent
    ? reactionOriginTimingAnchor(selectedEvent)
    : null;
  return (
    <div className="space-y-6">
      <section className="metric-grid sm:grid-cols-2 xl:grid-cols-6">
        <KpiCard
          label="현재 빗썸 점유율"
          icon={<BarChart3 className="h-4 w-4" />}
          value={
            hasCurrentShare
              ? percent(reaction.market_share.current_pct, 2)
              : "—"
          }
          tone={hasCurrentShare ? "gold" : "muted"}
          sub={
            hasCurrentShare ? (
              hasShareComparison ? (
                <>
                  직전 {percent(reaction.market_share.previous_pct, 2)} ·{" "}
                  {points(reaction.market_share.change_pp, 2)}
                  <br />
                  선택 구간 {marketShareWindowLabel(windowSec)}
                </>
              ) : (
                <>
                  직전 동일 구간 비교 자료 부족
                  <br />
                  선택 구간 {marketShareWindowLabel(windowSec)}
                </>
              )
            ) : (
              statusLabel(reaction.market_share.status)
            )
          }
        />
        <KpiCard
          label="시장 시작점"
          icon={<GitBranch className="h-4 w-4" />}
          value={<span className="text-lg">{selectedOrigin.label}</span>}
          tone={selectedOrigin.type === "UNRESOLVED" ? "muted" : "info"}
          sub={
            selectedEvent ? (
              <>
                {originBasisLabel(selectedEvent)} {clock(noticeTime)} · 국내
                반응 {clock(selectedOrigin.domestic_reaction_time_ms)}
              </>
            ) : (
              "연결된 공지가 없어 시작점을 판단하지 않음"
            )
          }
        />
        <KpiCard
          label="주요 국내 공지"
          icon={<Bell className="h-4 w-4" />}
          value={
            <span className="text-lg">
              {selectedEvent ? categoryLabel(selectedEvent.event_type) : "없음"}
            </span>
          }
          tone={selectedEvent ? "gold" : "muted"}
          sub={
            selectedEvent ? (
              <>
                <span className="block truncate" title={selectedTitle}>
                  제목: {selectedTitle}
                </span>
                <span className="block">
                  {domesticExchangeLabel(selectedEvent.exchange)} · 연결 자산{" "}
                  {selectedAssetCount}개 ·{" "}
                  {eventElapsed(reaction.as_of, selectedEvent)}
                </span>
              </>
            ) : (
              "선택 구간 관련 공지 없음"
            )
          }
        />
        <KpiCard
          label="연결 자산 현재 구간 기여"
          icon={<ArrowDownRight className="h-4 w-4" />}
          value={hasLinkedContribution ? points(linkedContribution) : "—"}
          tone={
            hasLinkedContribution
              ? linkedContribution >= 0
                ? "profit"
                : "loss"
              : "muted"
          }
          sub={
            hasNotice ? (
              <>
                {contributionAssetCountLabel}
                <br />
                공지 영향과 별개 값 · 거래금액 포함률{" "}
                {hasLinkedContribution
                  ? percent(
                      reaction.notice_summary.domestic_volume_coverage_pct
                    )
                  : "—"}
              </>
            ) : (
              "선택 구간 관련 공지 없음"
            )
          }
        />
        <KpiCard
          label="가장 큰 거래소 변화"
          icon={<Activity className="h-4 w-4" />}
          value={
            <span className="text-lg">
              {largestExchange
                ? domesticExchangeLabel(largestExchange.exchange)
                : "—"}
            </span>
          }
          tone={largestExchange ? "info" : "muted"}
          sub={
            largestExchange ? (
              <>
                <span className="block">
                  거래금액 변화{" "}
                  {signedPercent(largestExchange.volume_change_pct)} ·{" "}
                  {signedMoney(largestExchange.volume_change_krw)}
                </span>
                <span className="block">
                  연결 자산 {largestExchange.linked_asset_count}개 · 현재 구간
                  기여 {points(largestExchange.current_window_contribution_pp)}
                </span>
                <span
                  className="block truncate"
                  title={largestExchange.event_title}
                >
                  비교 공지: {largestExchange.event_title || "제목 없음"}
                </span>
              </>
            ) : (
              "공지 전후 비교 자료 없음"
            )
          }
        />
        <KpiCard
          label="현재 반응 상태"
          icon={<Timer className="h-4 w-4" />}
          value={
            <span className="text-lg">
              {selectedEvent?.reaction_label ?? "판단 보류"}
            </span>
          }
          tone={selectedEvent ? "gold" : "muted"}
          sub={
            selectedEvent ? (
              <>
                {eventElapsed(reaction.as_of, selectedEvent)}
                <br />
                최근 국내 거래금액{" "}
                {signedPercent(selectedEvent.domestic_volume_change_pct)} · 빗썸
                점유율 {points(selectedEvent.bithumb_share_change_pp)}
              </>
            ) : (
              "연결된 반응 없음"
            )
          }
        />
      </section>

      <div className="flex flex-wrap items-center gap-2 border border-border bg-inset px-4 py-3 text-xs">
        <StatusBadge
          label={`선택 구간 ${statusLabel(dataStatus)}`}
          tone={
            dataStatus === "CONFIRMED"
              ? "live"
              : dataStatus === "UNAVAILABLE"
                ? "muted"
                : "warn"
          }
        />
        <StatusBadge
          label={`연결 공지 ${reaction.notice_summary.event_count}건`}
        />
        <StatusBadge
          label={`연결 자산 ${reaction.notice_summary.linked_asset_count}개`}
        />
        <span className="ml-auto text-muted-foreground">
          분석 컨텍스트 {reaction.analysis_context_id ?? "정보 없음"}
        </span>
      </div>

      <Panel
        title="반응 경로"
        description="선택한 국내 공지의 게시·적용 시각과 국내 거래소 반응만 확인합니다. 바이낸스 공식 24시간 값은 공지와 연결하지 않습니다."
        icon={<GitBranch className="h-4 w-4" />}
      >
        <ReactionPath event={selectedEvent} />
      </Panel>

      <Panel
        title="주요 국내 공지"
        description="기본 정렬은 공지 대상 자산의 현재 구간 점유율 기여 절댓값, 공지 전후 거래금액 변화, 시각과 데이터 완성도를 따릅니다. 현재 구간 기여는 공지 영향값이 아닙니다."
        icon={<Bell className="h-4 w-4" />}
        actions={
          <span className="text-xs text-muted-foreground">
            {events.length.toLocaleString("ko-KR")}건
          </span>
        }
      >
        {events.length ? (
          <div className="max-h-[42rem] overflow-auto">
            <div className="hidden min-w-[900px] grid-cols-[100px_112px_minmax(220px,1fr)_150px_125px_120px] gap-3 border-b border-border bg-inset px-4 py-3 text-base text-muted-foreground md:grid">
              <span>기준 시각·반응 단계</span>
              <span>거래소·유형</span>
              <span>공지·대상 자산</span>
              <span>현재 구간 자산 기여</span>
              <span>국내 거래금액 변화</span>
              <span>시장 시작점·근거·상태</span>
            </div>
            {events.map(event => (
              <NoticeRow
                key={event.event_id}
                event={event}
                onSelect={() => onSelect(event)}
              />
            ))}
          </div>
        ) : (
          <div className="flex items-start gap-3 p-5 text-xs">
            <Info className="mt-0.5 h-4 w-4 shrink-0 text-info" />
            <div>
              <p className="font-semibold">관련 국내 공지 없음</p>
              <p className="mt-1 leading-6 text-muted-foreground">
                {reaction.no_notice_context.message} 오래된 공지를 임의로
                연결하지 않습니다.
              </p>
            </div>
          </div>
        )}
      </Panel>

      {!events.length && (
        <NoNoticeContext context={reaction.no_notice_context} />
      )}

      <div className="grid gap-6 xl:grid-cols-[1.2fr_0.8fr]">
        <Panel
          title="선택 공지 반응 요약"
          description="선택한 공지 전후의 국내 거래소 반응과 대상 코인의 현재 점유율 영향을 따로 요약합니다. 바이낸스는 공지 비교에서 뺍니다."
          icon={<BarChart3 className="h-4 w-4" />}
        >
          {selectedEvent ? (
            <button
              type="button"
              onClick={() => onSelect(selectedEvent)}
              className="block w-full text-left transition-colors hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none"
            >
              <div className="border-b border-border p-4">
                <div className="flex flex-wrap items-center gap-2">
                  <span
                    className={`text-xs font-semibold ${originTone(selectedEvent.origin.type)}`}
                  >
                    {selectedEvent.origin.label}
                  </span>
                  <span
                    className={`border px-2 py-1 text-base ${evidenceTone(selectedEvent.evidence_level)}`}
                  >
                    {evidenceLabel(selectedEvent.evidence_level)}
                  </span>
                  <span className="text-base text-muted-foreground">
                    {selectedTimingAnchor?.shortLabel}{" "}
                    {timestamp(selectedTimingAnchor?.time)}
                  </span>
                </div>
                <p className="mt-2 line-clamp-2 text-sm font-semibold">
                  {selectedEvent.title}
                </p>
              </div>
              <div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-4">
                <div className="bg-card p-4">
                  <p className="text-base text-muted-foreground">
                    연결 자산 현재 구간 기여
                  </p>
                  <p
                    className={`mt-1 text-lg font-semibold ${changeTone(selectedEvent.linked_assets.length ? selectedEvent.linked_asset_current_window_contribution_pp : null)}`}
                  >
                    {selectedEvent.linked_assets.length
                      ? points(
                          selectedEvent.linked_asset_current_window_contribution_pp,
                          3
                        )
                      : "—"}
                  </p>
                </div>
                <div className="bg-card p-4">
                  <p className="text-base text-muted-foreground">
                    국내 거래금액 변화
                  </p>
                  <p
                    className={`mt-1 text-lg font-semibold ${changeTone(selectedEvent.domestic_volume_change_pct)}`}
                  >
                    {signedPercent(selectedEvent.domestic_volume_change_pct)}
                  </p>
                </div>
                <div className="bg-card p-4">
                  <p className="text-base text-muted-foreground">
                    빗썸 점유율 변화
                  </p>
                  <p
                    className={`mt-1 text-lg font-semibold ${changeTone(selectedEvent.bithumb_share_change_pp)}`}
                  >
                    {points(selectedEvent.bithumb_share_change_pp)}
                  </p>
                </div>
                <div className="bg-card p-4">
                  <p className="text-base text-muted-foreground">반응 상태</p>
                  <p className="mt-1 text-lg font-semibold">
                    {selectedEvent.reaction_label}
                  </p>
                </div>
              </div>
              <div className="flex items-center justify-between gap-3 border-t border-border px-4 py-3 text-xs text-muted-foreground">
                <span>
                  {selectedEvent.linked_assets
                    .map(asset => asset.asset)
                    .join(" · ") || "연결 자산 없음"}
                </span>
                <span className="inline-flex items-center gap-1 text-info">
                  상세 보기 <ChevronRight className="h-3.5 w-3.5" />
                </span>
              </div>
            </button>
          ) : (
            <p className="p-5 text-xs text-muted-foreground">
              선택 구간에 상세 반응을 보여줄 공지가 없습니다.
            </p>
          )}
        </Panel>

        <Panel
          title="근거 수준과 데이터 상태"
          description="시장 반응 단계와 데이터 품질은 서로 다른 축으로 표시합니다."
          icon={<CheckCircle2 className="h-4 w-4" />}
        >
          <div className="space-y-3 p-4 text-xs">
            <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-3">
              <span className="text-muted-foreground">선택 공지 근거</span>
              <span
                className={`border px-2 py-1 ${evidenceTone(selectedOrigin.evidence_level)}`}
              >
                {evidenceLabel(selectedOrigin.evidence_level)}
              </span>
            </div>
            <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-3">
              <span className="text-muted-foreground">선택 구간</span>
              <span>{statusLabel(reaction.market_share.status)}</span>
            </div>
            <div className="flex items-center justify-between gap-3 border-b border-border/60 pb-3">
              <span className="text-muted-foreground">공지 반응 단계</span>
              <span>
                {selectedEvent
                  ? maturityLabel(selectedEvent.maturity)
                  : "정보 없음"}
              </span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-muted-foreground">바이낸스 공지 연결</span>
              <span className="text-info">정책상 비활성</span>
            </div>
            <p className="border border-gold/20 bg-gold/10 p-3 leading-6 text-muted-foreground">
              가격·호가·예상 가격 손해는 실제 전후 자료가 있을 때만 비교합니다.
              현재 값만 있는 경우 기준 시각의 현재 자료로 구분합니다.
            </p>
          </div>
        </Panel>
      </div>

      <Panel
        title="요약 2의 해석 원칙"
        description="확정 계산과 시간적 맥락을 분리해 읽습니다."
        icon={<Info className="h-4 w-4" />}
      >
        <div className="grid gap-px bg-border text-xs sm:grid-cols-3">
          <div className="bg-card p-4">
            <p className="font-semibold text-profit">확정 계산</p>
            <p className="mt-2 leading-6 text-muted-foreground">
              점유율, 거래금액과 현재 구간의 자산별 점유율 기여는 기존
              시장점유율 계산 결과를 재사용하며 공지 영향값으로 취급하지
              않습니다.
            </p>
          </div>
          <div className="bg-card p-4">
            <p className="font-semibold text-info">시간적 맥락</p>
            <p className="mt-2 leading-6 text-muted-foreground">
              국내 공지·적용과 업비트·빗썸 등 국내 반응의 순서를 먼저 확인하고,
              바이낸스 실시간 반응은 연결하지 않습니다. 국내 공지와 반응의
              시각이 가깝다는 사실만으로 원인을 확정할 수 없습니다.
            </p>
          </div>
          <div className="bg-card p-4">
            <p className="font-semibold text-gold">자료가 부족할 때</p>
            <p className="mt-2 leading-6 text-muted-foreground">
              과거 이력 공백과 호가 이력 부족은 0으로 대체하지 않고 자료
              부족으로 남깁니다.
            </p>
          </div>
        </div>
      </Panel>

      <footer className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-3 text-base text-muted-foreground">
        <span>
          국내 5개 거래소 공지 · 국내 반응 전용 · 바이낸스 공지 연결 제외
        </span>
        <span className="inline-flex items-center gap-1">
          <Clock3 className="h-3 w-3" />
          {timestamp(reaction.as_of * 1000)}
        </span>
      </footer>
    </div>
  );
}

export default function MarketReactionSummary() {
  return <RollingShareSummary2 />;
}
