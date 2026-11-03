import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
  type ReactNode,
} from "react";
import {
  Activity,
  AlertTriangle,
  ArrowDownRight,
  ArrowUpRight,
  BarChart3,
  Clock3,
  Crosshair,
  Database,
  Layers3,
  Minus,
  Network as NetworkIcon,
  Waves,
} from "lucide-react";
import {
  CartesianGrid,
  LabelList,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import {
  intelligenceCaptureDescription,
  intelligenceContributionCopy,
} from "@/lib/market-intelligence-presentation";
import { WhaleAlertPanel } from "@/components/WhaleAlertPanel";
import { TargetExchangePerspectivePanel } from "@/components/TargetExchangePerspectivePanel";
import { DeferredRender } from "@/components/common/DeferredRender";
import { MobileHelp } from "@/components/common/MobileHelp";
import { Panel as SurfacePanel } from "@/components/common/Panel";
import { ExchangeBrandLabel } from "@/components/ExchangeBrandLabel";
import { PriorityBestOrderBooks } from "@/components/PriorityBestOrderBooks";
import {
  useMarketShare,
  type MarketShareRealtimeFlowStore,
  type MarketShareTickStore,
} from "@/hooks/useMarketShare";
import { getMarketShareRealtimeSummary } from "@/lib/api-client";
import { captureMapMarker } from "@/lib/market-numeric-presentation";
import { exchangeLabel } from "@/lib/exchanges";
import { withParticle } from "@/lib/korean-particle";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  formatDuration,
  formatKRW,
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import {
  MARKET_INTELLIGENCE_CHAPTERS,
  marketIntelligenceRefreshDisplay,
  marketIntelligenceSummaryRequestMS,
  selectActiveMarketIntelligenceChapter,
  type MarketIntelligenceChapterId,
} from "@/lib/market-intelligence-experience";
import { buildRealtimeFlowOneSecondShareChartSeries, realtimeFlowCoverageLabel } from "@/lib/market-share-realtime-flow";
import {
  marketShareNetworkWindowLabel,
  type MarketShareNetworkWindowSec,
} from "@/lib/market-share-config";
import type {
  MarketShareAnalysis,
  MarketShareAsset,
  MarketShareChangeAsset,
  MarketShareOfficialMovementDriver,
  MarketShareRealtimeFlowCoverageSegment,
  MarketShareRealtimeFlowEpisode,
  MarketShareRealtimeFlowPeriodDriver,
  MarketShareRealtimeFlowPeriodSummary,
  MarketShareRealtimeFlowState,
} from "@/lib/market-share-types";
import { buildShareYAxis } from "@/lib/share-chart-axis";
import {
  buildOfficialHourlyChartSeries,
  officialHourlySeriesCoverageLabel,
} from "@/lib/official-hourly-series";
import "@/market-intelligence-experience.css";

const AssetExchangeShareNetwork = lazy(() =>
  import("@/components/ActiveNoticeShareNetwork").then(module => ({
    default: module.AssetExchangeShareNetwork,
  }))
);

type ContributionRow = {
  asset: string;
  contributionPP: number;
  bithumbChangeKRW: number;
  competitorChangeKRW: number;
};

type RealtimeMinuteChartPoint = {
  as_of_ms: number;
  [key: string]: number | null;
};

type IntelligenceSummaryResult = {
  requestedToMS: number;
  value: MarketShareRealtimeFlowPeriodSummary;
};

let retainedIntelligenceSummary: IntelligenceSummaryResult | null = null;

const REALTIME_SIXTY_SECOND_RANGE_MS = 60_000;
const REALTIME_SIXTY_SECOND_EXPECTED_SAMPLES = 60;
const DISPLAYED_OFFICIAL_HOURLY_POINT_COUNT = 30;
const DOMESTIC_REALTIME_EXCHANGES = [
  "upbit",
  "bithumb",
  "coinone",
  "korbit",
  "gopax",
] as const;
const DOMESTIC_OFFICIAL_EXCHANGE_NAMES = {
  upbit: "업비트",
  bithumb: "빗썸",
  coinone: "코인원",
  korbit: "코빗",
  gopax: "고팍스",
} satisfies Record<(typeof DOMESTIC_REALTIME_EXCHANGES)[number], string>;
const CHAPTER_ICONS = {
  overview: BarChart3,
  pulse: Activity,
  drivers: Layers3,
  network: NetworkIcon,
  episodes: Clock3,
} satisfies Record<MarketIntelligenceChapterId, typeof Activity>;

function scrollToIntelligenceChapter(id: MarketIntelligenceChapterId) {
  const chapter = MARKET_INTELLIGENCE_CHAPTERS.find(item => item.id === id);
  const target = chapter ? document.getElementById(chapter.domId) : null;
  if (!target) return;
  const reducedMotion = window.matchMedia(
    "(prefers-reduced-motion: reduce)"
  ).matches;
  target.scrollIntoView({
    behavior: reducedMotion ? "auto" : "smooth",
    block: "start",
  });
  window.requestAnimationFrame(() => {
    target.querySelector<HTMLElement>("h1, h2")?.focus({
      preventScroll: true,
    });
  });
}

function IntelligenceChapterNav({
  activeChapter,
  selectedAsset,
  onSelectChapter,
}: {
  activeChapter: MarketIntelligenceChapterId;
  selectedAsset: string;
  onSelectChapter: (id: MarketIntelligenceChapterId) => void;
}) {
  const activeButtonRef = useRef<HTMLButtonElement>(null);
  const activeIndex = Math.max(
    0,
    MARKET_INTELLIGENCE_CHAPTERS.findIndex(
      chapter => chapter.id === activeChapter
    )
  );
  useEffect(() => {
    const button = activeButtonRef.current;
    const track = button?.parentElement;
    if (!button || !track || track.scrollWidth <= track.clientWidth) return;
    const reducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;
    track.scrollTo({
      left: button.offsetLeft - (track.clientWidth - button.offsetWidth) / 2,
      behavior: reducedMotion ? "auto" : "smooth",
    });
  }, [activeChapter]);
  return (
    <nav
      className="mi-chapter-nav"
      aria-label="시장 분석 내 이동"
      style={
        {
          "--mi-progress": `${((activeIndex + 1) / MARKET_INTELLIGENCE_CHAPTERS.length) * 100}%`,
        } as CSSProperties
      }
    >
      <div className="mi-chapter-nav-track">
        <span className="mi-chapter-progress" aria-hidden="true">
          {String(activeIndex + 1).padStart(2, "0")}
          <span>/</span>
          {String(MARKET_INTELLIGENCE_CHAPTERS.length).padStart(2, "0")}
        </span>
        {MARKET_INTELLIGENCE_CHAPTERS.map((chapter, index) => {
          const Icon = CHAPTER_ICONS[chapter.id];
          const active = activeChapter === chapter.id;
          return (
            <button
              key={chapter.id}
              ref={active ? activeButtonRef : undefined}
              type="button"
              aria-current={active ? "true" : undefined}
              onClick={() => {
                onSelectChapter(chapter.id);
                scrollToIntelligenceChapter(chapter.id);
              }}
              className="mi-chapter-nav-button"
            >
              <span aria-hidden="true">
                {String(index + 1).padStart(2, "0")}
              </span>
              <Icon className="h-4 w-4" aria-hidden="true" />
              {chapter.label}
            </button>
          );
        })}
        {selectedAsset ? (
          <span className="mi-chapter-selected-asset">
            선택 코인 <strong>{selectedAsset}</strong>
          </span>
        ) : null}
      </div>
    </nav>
  );
}

function IntelligenceChapterHeading({
  chapterId,
  title,
  description,
}: {
  chapterId: MarketIntelligenceChapterId;
  title: string;
  description: string;
}) {
  const index = MARKET_INTELLIGENCE_CHAPTERS.findIndex(
    chapter => chapter.id === chapterId
  );
  const chapter = MARKET_INTELLIGENCE_CHAPTERS[index];
  return (
    <header className="mi-section-heading">
      <div className="mi-section-heading-copy">
        <p className="mi-section-kicker">
          <span className="mi-section-kicker-index">
            {String(index + 1).padStart(2, "0")}
          </span>
          <span className="mi-section-kicker-label">{chapter?.kicker}</span>
        </p>
        <div className="mobile-help-heading">
          <h2
            id={`${chapter?.domId ?? chapterId}-title`}
            className="mi-section-title"
            tabIndex={-1}
          >
            {title}
          </h2>
          <MobileHelp label={`${title} 설명`}>{description}</MobileHelp>
        </div>
      </div>
      <p className="mi-section-description mobile-description-copy">
        {description}
      </p>
    </header>
  );
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function normalizedTimestampMS(value: number | null | undefined): number {
  if (!finite(value) || value <= 0) return 0;
  return value < 10_000_000_000 ? value * 1000 : value;
}

function analysisAsOfMS(analysis: MarketShareAnalysis): number {
  const candidates = [
    analysis.realtime_flow?.as_of_ms,
    analysis.official_ticker_1m?.generated_at_ms,
    analysis.official_domestic_24h?.as_of,
    analysis.context?.as_of,
    analysis.generated_at,
  ];
  for (const candidate of candidates) {
    const timestamp = normalizedTimestampMS(candidate);
    if (timestamp > 0) return Math.floor(timestamp / 10_000) * 10_000;
  }
  return 0;
}

function dateTime(ms: number): string {
  return formatMarketTimeMS(ms, "date-time");
}

function timeOnly(ms: number): string {
  return formatMarketTimeMS(ms, "time");
}

// Called for every chart axis label and tooltip; built once.
const OFFICIAL_HOUR_FORMAT = new Intl.DateTimeFormat("ko-KR", {
  timeZone: "Asia/Seoul",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  hour12: false,
});

function officialHourLabel(ms: number): string {
  if (!finite(ms) || ms <= 0) return "—";
  return OFFICIAL_HOUR_FORMAT.format(new Date(ms));
}

function OfficialSharePointValueLabel({
  x,
  y,
  value,
  index,
}: {
  x?: number | string;
  y?: number | string;
  value?: number | string;
  index?: number;
}) {
  const pointX = Number(x);
  const pointY = Number(y);
  const share = Number(value);
  if (![pointX, pointY, share].every(Number.isFinite)) return null;
  return (
    <text
      x={pointX}
      y={pointY - ((index ?? 0) % 2 === 0 ? 10 : 25)}
      fill="var(--foreground)"
      fontSize={13}
      fontWeight={600}
      textAnchor="middle"
    >
      {percent(share, 2)}
    </text>
  );
}

function timeWithSeconds(ms: number): string {
  return formatMarketTimeMS(ms, "time-seconds");
}

function percent(value: number | null | undefined, digits = 1): string {
  return formatSharePercent(value, digits);
}

function points(value: number | null | undefined): string {
  return finite(value) ? formatSignedPercentagePoints(value, 2) : "—";
}

function money(value: number | null | undefined): string {
  return finite(value) ? formatKRW(value) : "—";
}

function signedMoney(value: number | null | undefined): string {
  return finite(value) ? formatKrwCompact(value) : "—";
}

function effectiveCount(value: number | null | undefined): string {
  return finite(value)
    ? value.toLocaleString("ko-KR", { maximumFractionDigits: 1 })
    : "—";
}

function officialStatusLabel(
  status: string,
  readyExchangeCount: number,
  expectedExchangeCount: number
): string {
  if (status === "LIVE" && readyExchangeCount === expectedExchangeCount)
    return "전체 확인";
  if (status === "STALE") return "갱신 지연";
  if (status === "PARTIAL") return "일부 확인";
  return "확인 대기";
}

function directionTone(value: number | null | undefined): string {
  if (!finite(value) || Math.abs(value) < 1e-12) return "text-muted-foreground";
  return value > 0 ? "text-profit" : "text-loss";
}

function Panel({
  title,
  description,
  descriptionRole,
  icon,
  actions,
  children,
  className = "",
}: {
  title: string;
  description?: string;
  descriptionRole?: "help" | "status";
  icon?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <SurfacePanel
      title={title}
      description={description}
      descriptionRole={descriptionRole}
      icon={icon}
      actions={actions}
      surfaceClassName="mi-panel"
      className={className}
    >
      {children}
    </SurfacePanel>
  );
}

function MetricCard({
  label,
  value,
  detail,
  tone = "text-foreground",
  icon,
  className = "",
}: {
  label: string;
  value: string;
  detail: ReactNode;
  tone?: string;
  icon: ReactNode;
  className?: string;
}) {
  return (
    <article className={`glass-card mi-metric-card min-w-0 p-4 ${className}`}>
      <div className="flex items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>{label}</span>
        <span className="text-primary">{icon}</span>
      </div>
      <p
        className={`mi-metric-value mt-3 font-mono text-2xl font-semibold ${tone}`}
      >
        {value}
      </p>
      <div className="mt-2 text-base leading-6 text-muted-foreground">
        {detail}
      </div>
    </article>
  );
}

function coverageTone(status: string): string {
  const normalized = status.toUpperCase();
  if (
    normalized.includes("MISSING") ||
    normalized.includes("GAP") ||
    normalized.includes("UNAVAILABLE")
  )
    return "bg-muted-foreground/25";
  if (normalized.includes("PARTIAL")) return "bg-gold/70";
  if (normalized.includes("ATTRIBUT")) return "bg-profit";
  return "bg-primary";
}

function CoverageRail({
  segments,
  fromMS,
  toMS,
}: {
  segments: MarketShareRealtimeFlowCoverageSegment[];
  fromMS: number;
  toMS: number;
}) {
  const span = toMS - fromMS;
  if (!segments.length || span <= 0) {
    return (
      <div className="flex h-9 items-center justify-center rounded-md border border-dashed border-border text-base text-muted-foreground">
        시간대별 자료 상태를 집계하고 있습니다.
      </div>
    );
  }

  return (
    <div>
      <div
        className="relative h-4 overflow-hidden rounded-sm bg-secondary"
        role="img"
        aria-label={`24시간 실시간 비교 자료 상태. ${Array.from(new Set(segments.map(segment => realtimeFlowCoverageLabel(segment.status, segment.reason)))).join(", ")}`}
      >
        {segments.map((segment, index) => {
          const left = Math.max(
            0,
            Math.min(100, ((segment.start_ms - fromMS) / span) * 100)
          );
          const right = Math.max(
            0,
            Math.min(100, ((segment.end_ms - fromMS) / span) * 100)
          );
          return (
            <span
              key={`${segment.start_ms}-${segment.end_ms}-${index}`}
              aria-hidden="true"
              title={`${dateTime(segment.start_ms)}–${dateTime(segment.end_ms)} · ${realtimeFlowCoverageLabel(segment.status, segment.reason)}`}
              className={`absolute inset-y-0 ${coverageTone(segment.status)}`}
              style={{
                left: `${left}%`,
                width: `${Math.max(0, right - left)}%`,
              }}
            />
          );
        })}
      </div>
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-base text-muted-foreground">
        {Array.from(
          new Set(segments.map(segment => realtimeFlowCoverageLabel(segment.status, segment.reason)))
        ).map(label => {
          const segment = segments.find(
            row => realtimeFlowCoverageLabel(row.status, row.reason) === label
          );
          return (
            <span key={label} className="inline-flex items-center gap-1.5">
              <span
                className={`h-2 w-2 rounded-sm ${coverageTone(segment?.status ?? "")}`}
              />
              {label}
            </span>
          );
        })}
      </div>
    </div>
  );
}

function ContributionWaterfall({
  rows,
  selectedAsset,
  onSelect,
}: {
  rows: ContributionRow[];
  selectedAsset: string;
  onSelect: (asset: string) => void;
}) {
  if (!rows.length) {
    return (
      <div className="flex min-h-64 items-center justify-center p-6 text-sm text-muted-foreground">
        비교 가능한 코인 기여 자료를 기다리고 있습니다.
      </div>
    );
  }
  const visibleRows = [...rows]
    .sort((a, b) => Math.abs(b.contributionPP) - Math.abs(a.contributionPP))
    .slice(0, 10);
  const max = Math.max(
    ...visibleRows.map(row => Math.abs(row.contributionPP)),
    1e-9
  );

  return (
    <div className="space-y-3 p-4 sm:p-5" aria-label="코인별 점유율 변화 기여">
      {visibleRows.map(row => {
        const width = Math.max(1.2, (Math.abs(row.contributionPP) / max) * 100);
        const positive = row.contributionPP > 0;
        const negative = row.contributionPP < 0;
        return (
          <button
            key={row.asset}
            type="button"
            aria-pressed={row.asset === selectedAsset}
            aria-label={`${row.asset} 선택. 점유율 기여 ${points(row.contributionPP)}, 빗썸 거래금액 변화 ${signedMoney(row.bithumbChangeKRW)}, 경쟁 거래소 거래금액 변화 ${signedMoney(row.competitorChangeKRW)}`}
            onClick={() => onSelect(row.asset)}
            className="mi-waterfall-row grid w-full grid-cols-[4.75rem_1fr_4.5rem] items-center gap-3 rounded-lg px-2 py-1.5 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          >
            <span className="truncate font-mono text-xs font-semibold">
              {row.asset}
            </span>
            <div className="grid h-7 grid-cols-2 overflow-hidden rounded-sm bg-inset">
              <div className="relative border-r border-border/80">
                {negative ? (
                  <span
                    className="absolute inset-y-1 right-0 rounded-l-sm bg-loss/75"
                    style={{ width: `${width}%` }}
                    title={`빗썸 ${signedMoney(row.bithumbChangeKRW)} · 경쟁 거래소 ${signedMoney(row.competitorChangeKRW)}`}
                  />
                ) : null}
              </div>
              <div className="relative">
                {positive ? (
                  <span
                    className="absolute inset-y-1 left-0 rounded-r-sm bg-profit/75"
                    style={{ width: `${width}%` }}
                    title={`빗썸 ${signedMoney(row.bithumbChangeKRW)} · 경쟁 거래소 ${signedMoney(row.competitorChangeKRW)}`}
                  />
                ) : null}
              </div>
            </div>
            <span
              className={`text-right font-mono text-xs ${directionTone(row.contributionPP)}`}
            >
              {points(row.contributionPP)}
            </span>
          </button>
        );
      })}
      <div className="grid grid-cols-[3.5rem_1fr_4.5rem] gap-3 text-base text-muted-foreground">
        <span />
        <div className="grid grid-cols-2">
          <span className="pr-2 text-right">하락 기여</span>
          <span className="pl-2">상승 기여</span>
        </div>
        <span />
      </div>
    </div>
  );
}

// Plot area of the capture map. The left edge leaves room for the rotated
// axis title; at x=58 it ran through the 25-75% tick labels.
const CAPTURE_PLOT_LEFT = 78;
const CAPTURE_PLOT_RIGHT = 730;

function captureX(value: number, min: number, max: number): number {
  const low = Math.log10(Math.max(1, min));
  const high = Math.log10(Math.max(1, max));
  if (Math.abs(high - low) < 1e-9)
    return (CAPTURE_PLOT_LEFT + CAPTURE_PLOT_RIGHT) / 2;
  return (
    CAPTURE_PLOT_LEFT +
    ((Math.log10(Math.max(1, value)) - low) / (high - low)) *
      (CAPTURE_PLOT_RIGHT - CAPTURE_PLOT_LEFT)
  );
}

function CaptureMap({
  assets,
  selectedAsset,
  onSelect,
}: {
  assets: MarketShareAsset[];
  selectedAsset: string;
  onSelect: (asset: string) => void;
}) {
  const visible = assets
    .filter(
      asset =>
        finite(asset.domestic_volume_krw) &&
        asset.domestic_volume_krw > 0 &&
        finite(asset.bithumb_capture_pct)
    )
    .sort((a, b) => (b.domestic_volume_krw ?? 0) - (a.domestic_volume_krw ?? 0))
    .slice(0, 80);
  if (!visible.length) {
    return (
      <div className="flex min-h-80 items-center justify-center p-6 text-sm text-muted-foreground">
        코인별 빗썸 거래금액 비중 자료를 기다리고 있습니다.
      </div>
    );
  }
  const volumes = visible.map(asset => asset.domestic_volume_krw ?? 0);
  const minVolume = Math.min(...volumes);
  const maxVolume = Math.max(...volumes);
  const maxContribution = Math.max(
    ...visible.flatMap(asset =>
      finite(asset.share_change_contribution_pp)
        ? [Math.abs(asset.share_change_contribution_pp)]
        : []
    ),
    1e-9
  );
  const selectedVisibleIndex = visible.findIndex(
    asset => asset.asset === selectedAsset
  );

  return (
    <div className="p-4 sm:p-5">
      <div className="overflow-x-auto">
        <svg
          viewBox="0 0 760 340"
          className="mi-capture-map h-auto min-w-[680px] w-full"
          role="group"
          aria-label="코인별 국내 거래금액과 빗썸 거래금액 비중 지도"
        >
          {[0, 25, 50, 75, 100].map(value => {
            const y = 292 - (value / 100) * 250;
            return (
              <g key={value}>
                <line
                  x1={CAPTURE_PLOT_LEFT}
                  y1={y}
                  x2={CAPTURE_PLOT_RIGHT}
                  y2={y}
                  className="stroke-border"
                  strokeDasharray="3 5"
                />
                <text
                  x={CAPTURE_PLOT_LEFT - 9}
                  y={y + 4}
                  textAnchor="end"
                  className="fill-muted-foreground text-base"
                >
                  {value}%
                </text>
              </g>
            );
          })}
          <line
            x1={CAPTURE_PLOT_LEFT}
            y1="292"
            x2={CAPTURE_PLOT_RIGHT}
            y2="292"
            className="stroke-border"
          />
          <text
            x={(CAPTURE_PLOT_LEFT + CAPTURE_PLOT_RIGHT) / 2}
            y="328"
            textAnchor="middle"
            className="fill-muted-foreground text-base"
          >
            국내 거래금액 →
          </text>
          <text
            x="14"
            y="167"
            transform="rotate(-90 14 167)"
            textAnchor="middle"
            className="fill-muted-foreground text-base"
          >
            빗썸 거래금액 비중
          </text>
          <text
            x={CAPTURE_PLOT_LEFT}
            y="309"
            className="fill-muted-foreground text-base"
          >
            {money(minVolume)}
          </text>
          <text
            x={CAPTURE_PLOT_RIGHT}
            y="309"
            textAnchor="end"
            className="fill-muted-foreground text-base"
          >
            {money(maxVolume)}
          </text>
          {visible.map((asset, index) => {
            const marker = captureMapMarker(
              asset.share_change_contribution_pp,
              asset.capture_gap_vs_venue_pp,
              maxContribution
            );
            const x = captureX(
              asset.domestic_volume_krw ?? 0,
              minVolume,
              maxVolume
            );
            const y = 292 - ((asset.bithumb_capture_pct ?? 0) / 100) * 250;
            const radius = marker.radius;
            const selected = asset.asset === selectedAsset;
            const keyboardTarget =
              selected || (selectedVisibleIndex < 0 && index === 0);
            const accessibleLabel = `${asset.asset} 선택. 국내 거래금액 ${money(asset.domestic_volume_krw)}, 빗썸 거래금액 비중 ${percent(asset.bithumb_capture_pct, 2)}, 전체 빗썸 점유율 대비 ${points(asset.capture_gap_vs_venue_pp)}, 점유율 기여 ${marker.contributionPP === null ? "확인 대기" : points(marker.contributionPP)}`;
            return (
              <g
                key={asset.asset}
                role="button"
                tabIndex={keyboardTarget ? 0 : -1}
                data-capture-index={index}
                aria-label={accessibleLabel}
                aria-pressed={selected}
                onClick={() => onSelect(asset.asset)}
                onKeyDown={event => {
                  const svg = event.currentTarget.ownerSVGElement;
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    onSelect(asset.asset);
                    return;
                  }
                  const direction =
                    event.key === "ArrowRight" || event.key === "ArrowDown"
                      ? 1
                      : event.key === "ArrowLeft" || event.key === "ArrowUp"
                        ? -1
                        : 0;
                  const nextIndex =
                    event.key === "Home"
                      ? 0
                      : event.key === "End"
                        ? visible.length - 1
                        : direction
                          ? (index + direction + visible.length) %
                            visible.length
                          : index;
                  if (nextIndex === index) return;
                  event.preventDefault();
                  onSelect(visible[nextIndex].asset);
                  window.requestAnimationFrame(() => {
                    svg
                      ?.querySelector<SVGGElement>(
                        `[data-capture-index="${nextIndex}"]`
                      )
                      ?.focus();
                  });
                }}
                className={`mi-capture-node cursor-pointer outline-none ${selected ? "is-selected" : ""}`}
              >
                <circle
                  cx={x}
                  cy={y}
                  r="22"
                  fill="transparent"
                  stroke="none"
                  pointerEvents="all"
                />
                <circle
                  cx={x}
                  cy={y}
                  r={selected ? radius + 3 : radius}
                  className={
                    marker.tone === "above"
                      ? "fill-profit/70 stroke-profit"
                      : marker.tone === "below"
                        ? "fill-loss/70 stroke-loss"
                        : marker.tone === "equal"
                          ? "fill-primary/55 stroke-primary"
                          : "fill-muted/70 stroke-muted-foreground"
                  }
                  strokeDasharray={
                    marker.contributionPP === null ? "3 2" : undefined
                  }
                  strokeWidth={selected ? 2.5 : 1}
                />
                <title>{accessibleLabel}</title>
                {(selected || index < 8) && (
                  <text
                    x={x + radius + 3}
                    y={y - radius - 1}
                    className="fill-foreground text-base font-semibold"
                  >
                    {asset.asset}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
      </div>
      <p className="ui-caption mt-2 text-muted-foreground">
        거래금액 상위 코인을 표시합니다. 초록은 전체 빗썸 점유율보다 해당 코인의
        빗썸 거래금액 비중이 높고, 빨강은 낮으며, 파랑은 두 값이 같은 코인입니다. 회색은
        비중 비교 자료를 기다리는 코인입니다. 점 크기는 점유율 기여 크기이며,
        점선은 기여 자료를 기다리는 코인입니다.
      </p>
    </div>
  );
}

function episodeDirection(episode: MarketShareRealtimeFlowEpisode): string {
  if (episode.direction === "UP") return "상승";
  if (episode.direction === "DOWN") return "하락";
  if (episode.direction === "STABLE") return "보합";
  return "방향 미확정";
}

function episodeStatus(status: string): string {
  const normalized = status.toUpperCase();
  if (normalized.includes("PERSIST")) return "지속";
  if (normalized.includes("VOLAT")) return "요동";
  if (normalized.includes("ALERT")) return "경고";
  if (normalized.includes("WATCH")) return "관찰";
  if (normalized.includes("ACTIVE") || normalized.includes("OPEN"))
    return "진행 중";
  if (normalized.includes("ENDED") || normalized.includes("CLOSED"))
    return "종료";
  return "관측";
}

// The episode list shows the latest episodes only; the summary request asks
// the server for just these.
const RECENT_EPISODE_COUNT = 8;

function EpisodeList({
  episodes,
}: {
  episodes: MarketShareRealtimeFlowEpisode[];
}) {
  if (!episodes.length) {
    return null;
  }
  const recentEpisodes = [...episodes]
    .sort((left, right) => right.end_ms - left.end_ms)
    .slice(0, RECENT_EPISODE_COUNT);
  return (
    <div className="divide-y divide-border">
      {recentEpisodes.map(episode => (
        <article
          key={episode.id}
          className="grid gap-3 px-4 py-4 sm:grid-cols-[10rem_1fr_auto] sm:px-5"
        >
          <div>
            <p className="font-mono text-xs font-semibold">
              {timeWithSeconds(episode.start_ms)}–
              {timeWithSeconds(episode.end_ms)}
            </p>
            <p className="mt-1 whitespace-nowrap text-base text-muted-foreground">
              구간 {formatDuration(episode.duration_sec)} · 관측{" "}
              {episode.observation_count.toLocaleString("ko-KR")}회 ·{" "}
              {episodeStatus(episode.status)}
            </p>
          </div>
          <div className="min-w-0">
            <p className="text-xs font-semibold">
              {episodeDirection(episode)} ·{" "}
              {episode.top_asset || "기여 코인 확인 중"}
            </p>
            <p className="mt-1 text-base leading-6 text-muted-foreground">
              10초 최대 {points(episode.peak_share_change_pp)} · 관측된 10초
              변화 합계 {points(episode.net_share_change_pp)}
              {episode.top_exchange
                ? ` · 주요 거래소 ${episode.top_exchange} ${points(episode.top_exchange_contribution_pp)}`
                : ""}
            </p>
          </div>
          <div className="text-left sm:text-right">
            <p
              className={`font-mono text-sm ${directionTone(episode.top_asset_contribution_pp)}`}
            >
              {points(episode.top_asset_contribution_pp)}
            </p>
            <p className="mt-1 text-base text-muted-foreground">
              주요 코인 기여 · 확인{" "}
              {episode.attributed_sample_count.toLocaleString("ko-KR")}회
            </p>
          </div>
        </article>
      ))}
    </div>
  );
}

function DriverRanking({
  title,
  rows,
  mode,
}: {
  title: string;
  rows: MarketShareRealtimeFlowPeriodDriver[];
  mode: "consistency" | "episodes";
}) {
  const ranked = [...rows]
    .filter(row =>
      mode === "consistency"
        ? row.observation_count >= 2
        : finite(row.episode_count) && row.episode_count > 0
    )
    .sort((a, b) =>
      mode === "consistency"
        ? b.direction_consistency_pct - a.direction_consistency_pct ||
          b.observation_count - a.observation_count
        : (b.episode_count ?? 0) - (a.episode_count ?? 0)
    )
    .slice(0, 5);
  return (
    <div className="rounded-lg border border-border bg-inset p-4">
      <h3 className="text-xs font-semibold">{title}</h3>
      {mode === "consistency" ? (
        <p className="mt-1 text-base text-muted-foreground">
          주요 기여 코인 중 2회 이상 관측 기준
        </p>
      ) : (
        <p className="mt-1 text-base text-muted-foreground">
          주요 기여 코인의 독립 사건 수
        </p>
      )}
      {ranked.length ? (
        <ol className="mt-3 space-y-2.5">
          {ranked.map((row, index) => (
            <li
              key={row.asset}
              className="grid grid-cols-[1.25rem_1fr_auto] items-center gap-2 text-xs"
            >
              <span className="font-mono text-muted-foreground">
                {index + 1}
              </span>
              <span className="font-mono font-semibold">{row.asset}</span>
              <span className="text-right">
                {mode === "consistency"
                  ? `${percent(row.direction_consistency_pct, 1)} · 관측 ${row.observation_count.toLocaleString("ko-KR")}회`
                  : `${row.episode_count?.toLocaleString("ko-KR")}개 사건`}
              </span>
            </li>
          ))}
        </ol>
      ) : (
        <p className="mt-3 text-base leading-6 text-muted-foreground">
          순위를 만들 수 있는 사건 자료를 축적하고 있습니다.
        </p>
      )}
    </div>
  );
}

function contributionRows(
  assets: MarketShareChangeAsset[],
  fallback: MarketShareOfficialMovementDriver[],
  windowSec: number
): { rows: ContributionRow[]; source: string; title: string } {
  if (assets.length) {
    return {
      ...intelligenceContributionCopy(windowSec, "REALTIME"),
      rows: assets.map(asset => ({
        asset: asset.asset,
        contributionPP: asset.contribution_pp,
        bithumbChangeKRW: asset.bithumb_volume_change_krw,
        competitorChangeKRW: asset.competitor_volume_change_krw,
      })),
    };
  }
  return {
    ...intelligenceContributionCopy(
      windowSec,
      fallback.length ? "OFFICIAL" : "UNAVAILABLE"
    ),
    rows: fallback.map(asset => ({
      asset: asset.asset,
      contributionPP: asset.contribution_pp,
      bithumbChangeKRW: asset.bithumb_volume_change_krw,
      competitorChangeKRW: asset.competitor_volume_change_krw,
    })),
  };
}

function RealtimeExchangeShareChart({
  initialFlow,
  realtimeFlowStore,
}: {
  initialFlow: MarketShareRealtimeFlowState;
  realtimeFlowStore: MarketShareRealtimeFlowStore;
}) {
  const streamedFlow = useSyncExternalStore(
    realtimeFlowStore.subscribe,
    realtimeFlowStore.getSnapshot,
    realtimeFlowStore.getSnapshot
  );
  const realtimeFlow = streamedFlow ?? initialFlow;
  const minuteToMS = normalizedTimestampMS(realtimeFlow.as_of_ms);
  const minuteFromMS = Math.max(
    0,
    minuteToMS - REALTIME_SIXTY_SECOND_RANGE_MS + 1_000
  );
  const minuteShareSeries = buildRealtimeFlowOneSecondShareChartSeries(
    realtimeFlow,
    minuteFromMS,
    minuteToMS
  );
  const minuteChartData = minuteShareSeries.points.flatMap(
    (point, index, points): RealtimeMinuteChartPoint[] => {
      const previous = points[index - 1];
      const current = {
        as_of_ms: point.as_of_ms,
        ...Object.fromEntries(
          DOMESTIC_REALTIME_EXCHANGES.map(exchange => [
            `${exchange}_share_pct`,
            point.exchange_shares_pct?.[exchange] ?? null,
          ])
        ),
      };
      if (!previous || previous.segment_id === point.segment_id)
        return [current];
      return [
        {
          as_of_ms: Math.round((previous.as_of_ms + point.as_of_ms) / 2),
          ...Object.fromEntries(
            DOMESTIC_REALTIME_EXCHANGES.map(exchange => [
              `${exchange}_share_pct`,
              null,
            ])
          ),
        },
        current,
      ];
    }
  );
  const latestMinuteShare = minuteShareSeries.points.at(-1) ?? null;
  const minuteShareAxis = buildShareYAxis(
    minuteShareSeries.points.flatMap(point =>
      DOMESTIC_REALTIME_EXCHANGES.flatMap(exchange => {
        const value = point.exchange_shares_pct?.[exchange];
        return finite(value) ? [value] : [];
      })
    )
  );

  return (
    <Panel
      title="국내 거래소 점유율 실시간 흐름"
      description="국내 5개 거래소의 완료된 1초 체결 거래금액 점유율을 최근 1분 동안 초 단위로 보여줍니다. 공식 24시간 점유율과 계산 구간이 다릅니다."
      icon={<Activity className="h-4 w-4" />}
      actions={
        <span className="text-base text-muted-foreground">
          {latestMinuteShare ? "5개 거래소 · 1초" : "확인 중"}
        </span>
      }
      className="h-full"
    >
      <div className="p-4 sm:p-5">
        {minuteChartData.some(point =>
          DOMESTIC_REALTIME_EXCHANGES.some(exchange =>
            finite(point[`${exchange}_share_pct`])
          )
        ) ? (
          <div
            className="h-72"
            role="img"
            aria-label={`국내 5개 거래소 점유율 실시간 흐름. 최근 1분 중 ${minuteShareSeries.used_sample_count.toLocaleString("ko-KR")}초 확인`}
          >
            <ResponsiveContainer width="100%" height="100%">
              <LineChart
                data={minuteChartData}
                margin={{ top: 8, right: 12, bottom: 4, left: 4 }}
              >
                <CartesianGrid
                  stroke="var(--border)"
                  strokeDasharray="3 6"
                  vertical={false}
                />
                <XAxis
                  type="number"
                  dataKey="as_of_ms"
                  domain={[minuteFromMS, minuteToMS]}
                  tickFormatter={value => timeWithSeconds(Number(value))}
                  tick={{ fill: "var(--muted-foreground)", fontSize: 16 }}
                  axisLine={{ stroke: "var(--border)" }}
                  tickLine={false}
                  minTickGap={32}
                />
                <YAxis
                  domain={minuteShareAxis.domain}
                  ticks={minuteShareAxis.ticks}
                  interval="preserveStartEnd"
                  tickFormatter={value => `${Number(value).toFixed(1)}%`}
                  tick={{ fill: "var(--muted-foreground)", fontSize: 16 }}
                  axisLine={false}
                  tickLine={false}
                  width={64}
                />
                <Tooltip
                  labelFormatter={value => dateTime(Number(value))}
                  formatter={(value, name) => [
                    percent(Number(value), 2),
                    String(name),
                  ]}
                  contentStyle={{
                    border: "1px solid var(--border)",
                    borderRadius: "0.5rem",
                    background: "var(--popover)",
                    color: "var(--popover-foreground)",
                    fontSize: "16px",
                  }}
                />
                {DOMESTIC_REALTIME_EXCHANGES.map(exchange => (
                  <Line
                    key={exchange}
                    type="linear"
                    dataKey={`${exchange}_share_pct`}
                    name={exchangeLabel(exchange)}
                    stroke={`var(--exchange-${exchange})`}
                    strokeWidth={exchange === "bithumb" ? 2.4 : 1.7}
                    dot={false}
                    activeDot={{
                      r: exchange === "bithumb" ? 4 : 3,
                      fill: `var(--exchange-${exchange})`,
                      strokeWidth: 0,
                    }}
                    isAnimationActive={false}
                    connectNulls={false}
                  />
                ))}
              </LineChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="flex h-72 items-center justify-center px-6 text-center text-sm text-muted-foreground">
            국내 5개 거래소의 완료된 1초 점유율을 확인하고 있습니다.
          </div>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-base text-muted-foreground">
          {DOMESTIC_REALTIME_EXCHANGES.map(exchange => (
            <span key={exchange} className="inline-flex items-center gap-1.5">
              <i
                className="h-2 w-2 rounded-full"
                style={{ backgroundColor: `var(--exchange-${exchange})` }}
              />
              <ExchangeBrandLabel exchange={exchange} />
              <strong className="font-semibold text-foreground">
                {percent(latestMinuteShare?.exchange_shares_pct?.[exchange], 2)}
              </strong>
            </span>
          ))}
        </div>
        <div className="mt-4 flex items-center justify-between gap-3 border-t border-border pt-4 text-base text-muted-foreground">
          <span>
            {timeWithSeconds(minuteFromMS)}–{timeWithSeconds(minuteToMS)}
          </span>
          <span>
            확인 {minuteShareSeries.used_sample_count.toLocaleString("ko-KR")}/
            {REALTIME_SIXTY_SECOND_EXPECTED_SAMPLES.toLocaleString("ko-KR")}초
          </span>
        </div>
      </div>
    </Panel>
  );
}

export default function MarketIntelligence() {
  const {
    data,
    tickStore,
    realtimeFlowStore,
    whaleAlerts,
    whaleAlertsUnavailable,
    connection,
    error,
  } = useMarketShare(60, null, "KRW", "full");
  const analysis = data?.analysis ?? null;
  const asOfMS = analysis ? analysisAsOfMS(analysis) : 0;
  const summaryRequestMS = marketIntelligenceSummaryRequestMS(asOfMS);
  const [summaryResult, setSummaryResult] =
    useState<IntelligenceSummaryResult | null>(retainedIntelligenceSummary);
  const [summaryError, setSummaryError] = useState("");
  const [selectedAssetName, setSelectedAssetName] = useState("");
  const [networkWindowSec, setNetworkWindowSec] =
    useState<MarketShareNetworkWindowSec>(60);
  const [activeChapter, setActiveChapter] =
    useState<MarketIntelligenceChapterId>("overview");
  const chapterNavLockUntilRef = useRef(0);
  const experienceRef = useRef<HTMLDivElement>(null);
  const analysisAvailable = analysis !== null;

  useEffect(() => {
    if (!summaryRequestMS) return;
    const controller = new AbortController();
    setSummaryError("");
    void getMarketShareRealtimeSummary(summaryRequestMS, controller.signal, {
      limit: RECENT_EPISODE_COUNT,
    })
      .then(value => {
        retainedIntelligenceSummary = {
          requestedToMS: summaryRequestMS,
          value,
        };
        setSummaryResult(retainedIntelligenceSummary);
      })
      .catch(cause => {
        if (controller.signal.aborted) return;
        setSummaryError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => controller.abort();
  }, [summaryRequestMS]);

  useEffect(() => {
    if (!analysisAvailable || !experienceRef.current) return;
    const experience = experienceRef.current;
    const scrollRoot = experience.closest<HTMLElement>(".dashboard-content");
    const sections = MARKET_INTELLIGENCE_CHAPTERS.flatMap(chapter => {
      const element = document.getElementById(chapter.domId);
      return element ? [{ chapter, element }] : [];
    });
    if (!sections.length) return;

    const reducedMotion = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;
    const canObserve = "IntersectionObserver" in window;
    let revealObserver: IntersectionObserver | null = null;
    if (canObserve && !reducedMotion) {
      revealObserver = new IntersectionObserver(
        entries => {
          entries.forEach(entry => {
            if (!entry.isIntersecting) return;
            entry.target.classList.add("mi-revealed");
            revealObserver?.unobserve(entry.target);
          });
        },
        {
          root: scrollRoot,
          rootMargin: "0px 0px -8% 0px",
          threshold: 0.06,
        }
      );
      sections.forEach(({ element }) => {
        element.classList.add("mi-reveal-ready");
        revealObserver?.observe(element);
      });
    } else {
      sections.forEach(({ element }) => element.classList.add("mi-revealed"));
    }

    if (!canObserve) return () => revealObserver?.disconnect();
    const visible = new Map<
      MarketIntelligenceChapterId,
      { id: MarketIntelligenceChapterId; ratio: number; top: number }
    >();
    const chapterObserver = new IntersectionObserver(
      entries => {
        const rootTop = scrollRoot?.getBoundingClientRect().top ?? 0;
        entries.forEach(entry => {
          const chapter = MARKET_INTELLIGENCE_CHAPTERS.find(
            item => item.domId === entry.target.id
          );
          if (!chapter) return;
          if (entry.isIntersecting && entry.intersectionRatio > 0) {
            visible.set(chapter.id, {
              id: chapter.id,
              ratio: entry.intersectionRatio,
              top: entry.boundingClientRect.top - rootTop,
            });
          } else {
            visible.delete(chapter.id);
          }
        });
        if (performance.now() >= chapterNavLockUntilRef.current) {
          setActiveChapter(previous =>
            selectActiveMarketIntelligenceChapter(
              [...visible.values()],
              previous
            )
          );
        }
      },
      {
        root: scrollRoot,
        rootMargin: "-8% 0px -58% 0px",
        threshold: [0, 0.04, 0.12, 0.25, 0.5],
      }
    );
    sections.forEach(({ element }) => chapterObserver.observe(element));

    return () => {
      revealObserver?.disconnect();
      chapterObserver.disconnect();
    };
  }, [analysisAvailable]);

  const summaryDisplay = marketIntelligenceRefreshDisplay(
    summaryResult,
    summaryRequestMS
  );
  const periodSummary = summaryDisplay.value;
  const contribution = useMemo(
    () =>
      analysis
        ? contributionRows(
            analysis.change_attribution.assets ?? [],
            analysis.official_ticker_1m.status === "LIVE"
              ? (analysis.official_ticker_1m.drivers ?? [])
              : [],
            analysis.context.window_sec
          )
        : { rows: [], ...intelligenceContributionCopy(60, "UNAVAILABLE") },
    [analysis]
  );
  const assetCandidates = useMemo(() => {
    if (!analysis) return [];
    const preferred = contribution.rows.map(row => row.asset);
    const remaining = analysis.assets
      .slice()
      .sort(
        (a, b) => (b.domestic_volume_krw ?? 0) - (a.domestic_volume_krw ?? 0)
      )
      .map(asset => asset.asset);
    return Array.from(new Set([...preferred, ...remaining]));
  }, [analysis, contribution.rows]);
  const effectiveSelectedAssetName = assetCandidates.includes(selectedAssetName)
    ? selectedAssetName
    : (assetCandidates[0] ?? "");

  useEffect(() => {
    if (!assetCandidates.length) return;
    setSelectedAssetName(current =>
      assetCandidates.includes(current) ? current : assetCandidates[0]
    );
  }, [assetCandidates]);

  const selectedAsset =
    analysis?.assets.find(
      asset => asset.asset === effectiveSelectedAssetName
    ) ?? null;

  if (!analysis) {
    return (
      <div className="mi-experience">
        <div className="glass-card flex min-h-52 items-center justify-center p-6">
          <div className="text-center">
            <Database className="mx-auto h-5 w-5 text-muted-foreground" />
            <p className="mt-3 text-sm font-semibold">
              완료된 시장 분석 기록이 없습니다.
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              공식 분석을 준비하는 동안 실시간 최우선 호가는 아래에서 먼저
              표시합니다.
            </p>
            {error ? <p className="mt-3 text-xs text-loss">{error}</p> : null}
          </div>
        </div>
        <WhaleAlertPanel
          state={whaleAlerts}
          unavailable={whaleAlertsUnavailable || connection !== "live"}
        />
        <PriorityBestOrderBooks tickStore={tickStore} connection={connection} />
      </div>
    );
  }

  const ticker = analysis.official_ticker_1m;
  const official = analysis.official_domestic_24h;
  const officialHeroTrend = buildOfficialHourlyChartSeries(
    ticker.trend,
    ticker.interval_sec || 3_600,
    Math.max(ticker.trend.length, 1)
  );
  const officialHourlyTrend = buildOfficialHourlyChartSeries(
    ticker.trend,
    ticker.interval_sec || 3_600,
    DISPLAYED_OFFICIAL_HOURLY_POINT_COUNT
  );
  const officialHourlyCoverage =
    officialHourlySeriesCoverageLabel(officialHourlyTrend);
  const officialShareAxis = buildShareYAxis(
    officialHourlyTrend.map(point => point.share)
  );
  const coverageSegments = periodSummary?.coverage_segments ?? [];
  const episodes = periodSummary?.episodes ?? [];
  const periodDrivers = periodSummary?.drivers ?? [];
  const breadth = periodSummary?.driver_breadth;
  const breadthAvailable = Boolean(
    breadth &&
    periodSummary &&
    periodSummary.attributed_sample_count > 0 &&
    periodDrivers.length > 0
  );
  const officialReady =
    official.status === "LIVE" &&
    official.ready_exchange_count === official.expected_exchange_count;
  const officialStatus = officialStatusLabel(
    official.status,
    official.ready_exchange_count,
    official.expected_exchange_count
  );
  const tickerReady = Boolean(
    ticker.status === "LIVE" &&
    ticker.previous &&
    ticker.current &&
    finite(ticker.share_delta_pp)
  );
  const hasRealtimeBasis =
    normalizedTimestampMS(analysis.realtime_flow?.as_of_ms) > 0;
  const coverageIsPartial =
    periodSummary !== null && periodSummary.status !== "COMPLETE";
  const attributionIsPartial =
    periodSummary !== null && periodSummary.attribution_coverage_pct < 100;

  return (
    <div ref={experienceRef} className="mi-experience">
      <TargetExchangePerspectivePanel analysis={analysis} />
      <section
        id="intelligence-overview"
        data-mi-chapter="overview"
        className="mi-chapter mi-overview-chapter"
        aria-labelledby="intelligence-overview-title"
      >
        <div className="mi-hero">
          {ticker.trend.length ? (
            <div className="mi-hero-chart" aria-hidden="true">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart
                  data={officialHeroTrend}
                  margin={{ top: 28, right: 0, bottom: 12, left: 0 }}
                >
                  <Line
                    type="monotone"
                    dataKey="share"
                    stroke="rgba(255,255,255,0.58)"
                    strokeWidth={2.4}
                    dot={false}
                    isAnimationActive={false}
                    connectNulls={false}
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
          ) : null}
          <div className="mi-hero-grid">
            <div className="mi-hero-copy">
              <div className="mi-hero-status-row">
                <span
                  className={`mi-live-status ${connection === "live" ? "is-live" : "is-waiting"}`}
                >
                  <span aria-hidden="true" />
                  {connection === "live" ? "실시간 연결" : "마지막 자료 유지"}
                </span>
                <span>
                  {hasRealtimeBasis ? "10초 흐름 기준" : "자료 기준"}{" "}
                  {dateTime(asOfMS)}
                </span>
              </div>
              <p className="mi-hero-eyebrow">시장 분석</p>
              <div className="mobile-help-heading">
                <h1
                  id="intelligence-overview-title"
                  className="mi-hero-title"
                  tabIndex={-1}
                >
                  빗썸 점유율 분석
                </h1>
                <MobileHelp label="시장 분석 화면 설명">
                  현재 공식 점유율은 국내 5개 거래소 기준입니다. 시간별 변화는
                  업비트·빗썸·코인원의 같은 시각 자료로 계산하며, 아래 코인
                  기여는 빗썸 점유율 기준입니다.
                </MobileHelp>
              </div>
              <p className="mi-hero-description mobile-description-copy">
                현재 공식 점유율은 국내 5개 거래소 기준입니다. 시간별 변화는
                업비트·빗썸·코인원의 같은 시각 자료로 계산하며, 아래 코인 기여는
                빗썸 점유율 기준입니다.
              </p>
              <div className="mi-hero-actions">
                <button
                  type="button"
                  className="mi-hero-action is-primary"
                  onClick={() => scrollToIntelligenceChapter("pulse")}
                >
                  24시간·최근 1시간
                  <ArrowDownRight className="h-4 w-4" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  className="mi-hero-action"
                  onClick={() => scrollToIntelligenceChapter("network")}
                >
                  코인·거래소 관계
                  <NetworkIcon className="h-4 w-4" aria-hidden="true" />
                </button>
              </div>
            </div>

            <MetricCard
              className="mi-metric-featured"
              label="공식 24시간 빗썸 점유율"
              value={
                officialReady ? percent(official.bithumb_share_pct, 2) : "—"
              }
              detail={
                officialReady
                  ? `${officialStatus} · ${official.ready_exchange_count}/${official.expected_exchange_count}개 국내 거래소 · 공식 24시간 거래금액 ${money(official.total_volume_krw)} · 기준 ${dateTime(normalizedTimestampMS(official.as_of))}`
                  : `${officialStatus} · ${official.ready_exchange_count}/${official.expected_exchange_count}개 포함 거래소 · 기준 ${dateTime(normalizedTimestampMS(official.as_of))}`
              }
              icon={<BarChart3 className="h-4 w-4" />}
            />
          </div>

          <div className="mi-hero-metrics">
            <MetricCard
              label="정시 1시간 변화 (업비트·빗썸·코인원)"
              value={tickerReady ? points(ticker.share_delta_pp) : "—"}
              detail={
                tickerReady && ticker.current
                  ? `업비트·빗썸·코인원 · ${timeOnly(ticker.previous?.bucket_ms ?? 0)} → ${timeOnly(ticker.current.bucket_ms)}`
                  : "같은 종료 시각의 1시간봉 비교 준비 중"
              }
              tone={
                tickerReady ? directionTone(ticker.share_delta_pp) : undefined
              }
              icon={
                tickerReady &&
                finite(ticker.share_delta_pp) &&
                ticker.share_delta_pp < 0 ? (
                  <ArrowDownRight className="h-4 w-4" />
                ) : tickerReady &&
                  finite(ticker.share_delta_pp) &&
                  ticker.share_delta_pp > 0 ? (
                  <ArrowUpRight className="h-4 w-4" />
                ) : (
                  <Minus className="h-4 w-4" />
                )
              }
            />
            <MetricCard
              label="빗썸 24시간 누적 거래금액의 1시간 변화"
              value={
                tickerReady ? signedMoney(ticker.bithumb_volume_delta_krw) : "—"
              }
              detail={
                tickerReady ? (
                  <>
                    빗썸
                    <span
                      className={directionTone(ticker.bithumb_volume_delta_krw)}
                    >
                      {` ${signedMoney(ticker.bithumb_volume_delta_krw)}`}
                    </span>
                    {` · 업비트·빗썸·코인원 합계 ${signedMoney(ticker.domestic_volume_delta_krw)}`}
                  </>
                ) : (
                  "같은 종료 시각의 1시간봉 비교 준비 중"
                )
              }
              tone={
                tickerReady
                  ? directionTone(ticker.bithumb_volume_delta_krw)
                  : undefined
              }
              icon={<Waves className="h-4 w-4" />}
            />
            <MetricCard
              label="최근 24시간 중 10초 자료 확인 비율"
              value={percent(periodSummary?.coverage_pct, 1)}
              detail={
                periodSummary
                  ? `저장된 10초 기록 기준 · 확인 ${formatDuration(periodSummary.observed_sec)} · 빠진 시간 ${formatDuration(periodSummary.missing_sec)}`
                  : "같은 기준시각의 자료 상태 확인 중"
              }
              tone={
                periodSummary?.status === "COMPLETE"
                  ? "text-profit"
                  : "text-gold"
              }
              icon={<Database className="h-4 w-4" />}
            />
          </div>
        </div>

        <Panel
          title="국내 5개 거래소 공식 24시간 점유율"
          description="각 거래소가 공개한 최근 24시간 원화 거래금액을 같은 시각에 모았습니다. 점유율은 다섯 거래소 합계를 분모로 합니다."
          icon={<BarChart3 className="h-4 w-4" />}
          actions={
            <span className="text-base text-muted-foreground">
              {official.ready_exchange_count}/{official.expected_exchange_count}
              개 거래소 확인
            </span>
          }
        >
          <div className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-5">
            {DOMESTIC_REALTIME_EXCHANGES.map(exchange => {
              const exchangeTicker = official.exchanges[exchange];
              const shareValue =
                officialReady && exchangeTicker?.status === "LIVE"
                  ? exchangeTicker.share_pct
                  : null;
              const volumeValue =
                exchangeTicker?.status !== "UNAVAILABLE"
                  ? exchangeTicker?.volume_krw
                  : null;
              const statusLabel =
                exchangeTicker?.status === "LIVE"
                  ? `${exchangeTicker.market_count.toLocaleString("ko-KR")}개 원화 마켓`
                  : exchangeTicker?.status === "STALE"
                    ? "갱신 지연 · 마지막 수신 거래금액"
                    : "자료 확인 중";

              return (
                <article
                  key={exchange}
                  className={`min-w-0 rounded-xl border p-4 ${
                    exchange === "bithumb"
                      ? "border-primary/40 bg-primary/10"
                      : "border-border bg-card"
                  }`}
                >
                  <h3 className="ui-body font-semibold">
                    <ExchangeBrandLabel
                      exchange={exchange}
                      label={DOMESTIC_OFFICIAL_EXCHANGE_NAMES[exchange]}
                    />
                  </h3>
                  <p className="ui-title mt-3 font-mono font-semibold tabular-nums">
                    {percent(shareValue, 2)}
                  </p>
                  <p className="ui-caption mt-1 text-muted-foreground">
                    국내 거래금액 중 점유율
                  </p>
                  <p className="ui-body mt-3 font-semibold tabular-nums">
                    {money(volumeValue)}
                  </p>
                  <p className="ui-caption mt-1 text-muted-foreground">
                    공식 최근 24시간 거래금액
                  </p>
                  <p className="ui-caption mt-3 text-muted-foreground">
                    {statusLabel}
                  </p>
                </article>
              );
            })}
          </div>
        </Panel>

        {(!officialReady ||
          coverageIsPartial ||
          attributionIsPartial ||
          summaryError) && (
          <aside
            className="mi-alert flex items-start gap-3 rounded-xl border border-gold/35 bg-gold/10 p-4 text-xs leading-6 text-gold"
            role="status"
            aria-live="polite"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <div>
              <p className="font-semibold">자료 상태를 함께 확인하세요.</p>
              <p className="mt-1 text-muted-foreground">
                {!officialReady
                  ? `공식 24시간 자료는 ${officialStatus} 상태이므로 점유율 수치를 숨겼습니다. `
                  : ""}
                {summaryError
                  ? periodSummary
                    ? `24시간 10초 흐름 요약을 갱신하지 못해 ${dateTime(periodSummary.to_ms)} 기준 자료를 유지합니다.`
                    : "24시간 10초 흐름 요약을 불러오지 못했습니다. 이전 공식 수치는 그대로 유지합니다."
                  : coverageIsPartial || attributionIsPartial
                    ? periodSummary
                      ? `저장된 10초 기록 기준으로 24시간 중 ${withParticle(formatDuration(periodSummary.observed_sec), "을/를")} 확인했습니다. 코인 기여는 ${periodSummary.attributed_sec === periodSummary.observed_sec ? "같은 시간" : `그중 ${formatDuration(periodSummary.attributed_sec)}`}의 자료로 계산했습니다. 순위와 사건은 확인된 구간만 표시합니다.`
                      : "24시간 10초 흐름 요약이 아직 확보되지 않았습니다."
                    : ""}
              </p>
            </div>
          </aside>
        )}

        <PriorityBestOrderBooks tickStore={tickStore} connection={connection} />
        <WhaleAlertPanel
          state={whaleAlerts}
          unavailable={whaleAlertsUnavailable || connection !== "live"}
        />
      </section>

      <IntelligenceChapterNav
        activeChapter={activeChapter}
        selectedAsset={effectiveSelectedAssetName}
        onSelectChapter={chapter => {
          chapterNavLockUntilRef.current = performance.now() + 1_000;
          setActiveChapter(chapter);
        }}
      />

      <section
        id="intelligence-pulse"
        data-mi-chapter="pulse"
        className="mi-chapter"
        aria-labelledby="intelligence-pulse-title"
      >
        <IntelligenceChapterHeading
          chapterId="pulse"
          title="점유율 추이"
          description="업비트·빗썸·코인원의 같은 시각 자료로 계산한 빗썸 24시간 점유율과 국내 5개 거래소의 최근 1분 체결 흐름을 나란히 확인합니다."
        />

        <div className="grid gap-6 xl:grid-cols-[minmax(0,4fr)_minmax(0,3fr)]">
          <Panel
            title="업비트·빗썸·코인원 기준 점유율 추이"
            description="이 선은 원화 거래금액이 직접 제공되는 업비트·빗썸·코인원의 종료 시각을 맞춘 1시간봉 자료입니다. 코빗·고팍스의 봉은 코인 거래수량만 제공되어 이 정시 변화선에는 넣지 않습니다. 위의 공식 24시간 현재값과 아래의 5개 거래소 실시간 흐름에는 두 거래소를 포함합니다."
            icon={<Clock3 className="h-4 w-4" />}
            actions={
              <span className="text-base text-muted-foreground">
                {officialHourlyCoverage}
              </span>
            }
            className="h-full"
          >
            <div className="p-4 sm:p-5">
              {officialHourlyTrend.length ? (
                <div
                  className="h-80 overflow-hidden"
                  role="img"
                  aria-label={`공식 점유율 추이. ${officialHourlyCoverage}. 점유율 숫자와 시각을 한 화면에 모두 표시, 현재 ${percent(officialHourlyTrend.at(-1)?.share, 2)}.`}
                >
                  <ResponsiveContainer width="100%" height="100%">
                    <LineChart
                      data={officialHourlyTrend}
                      margin={{ top: 34, right: 20, bottom: 4, left: 4 }}
                    >
                      <CartesianGrid
                        stroke="var(--border)"
                        strokeDasharray="3 6"
                        vertical={false}
                      />
                      <XAxis
                        type="number"
                        dataKey="bucket_ms"
                        domain={["dataMin", "dataMax"]}
                        ticks={officialHourlyTrend.map(
                          point => point.bucket_ms
                        )}
                        tickFormatter={value =>
                          officialHourLabel(Number(value))
                        }
                        tick={{
                          fill: "var(--muted-foreground)",
                          fontSize: 13,
                        }}
                        axisLine={{ stroke: "var(--border)" }}
                        tickLine={false}
                        interval={0}
                        angle={-60}
                        textAnchor="end"
                        height={68}
                      />
                      <YAxis
                        domain={officialShareAxis.domain}
                        ticks={officialShareAxis.ticks}
                        tickFormatter={value => `${Number(value).toFixed(1)}%`}
                        tick={{
                          fill: "var(--muted-foreground)",
                          fontSize: 16,
                        }}
                        axisLine={false}
                        tickLine={false}
                        width={64}
                      />
                      <Tooltip
                        labelFormatter={value => dateTime(Number(value))}
                        formatter={value => [
                          percent(typeof value === "number" ? value : null, 2),
                          "빗썸 점유율",
                        ]}
                        contentStyle={{
                          border: "1px solid var(--border)",
                          borderRadius: "0.5rem",
                          background: "var(--popover)",
                          color: "var(--popover-foreground)",
                          fontSize: "16px",
                        }}
                      />
                      <Line
                        type="monotone"
                        dataKey="share"
                        stroke="var(--exchange-bithumb)"
                        strokeWidth={2}
                        dot={{
                          r: 2.75,
                          fill: "var(--exchange-bithumb)",
                          stroke: "var(--background)",
                          strokeWidth: 1,
                        }}
                        isAnimationActive={false}
                        connectNulls={false}
                      >
                        <LabelList
                          dataKey="share"
                          content={<OfficialSharePointValueLabel />}
                        />
                      </Line>
                    </LineChart>
                  </ResponsiveContainer>
                </div>
              ) : (
                <div className="flex h-72 items-center justify-center text-sm text-muted-foreground">
                  같은 종료 시각의 1시간봉 점유율을 축적하고 있습니다.
                </div>
              )}
              <div className="mt-4 border-t border-border pt-4">
                <div className="mb-2 flex items-center justify-between gap-3 text-base text-muted-foreground">
                  <span>{dateTime(periodSummary?.from_ms ?? 0)}</span>
                  <span>실시간 비교 자료 상태 · 10초 단위</span>
                  <span>{dateTime(periodSummary?.to_ms ?? 0)}</span>
                </div>
                <p className="mb-2 text-sm text-muted-foreground">회색은 실시간 체결의 비교 근거가 부족한 구간입니다. 위 1시간봉의 누락 여부와는 별개입니다.</p>
                <CoverageRail
                  segments={coverageSegments}
                  fromMS={periodSummary?.from_ms ?? 0}
                  toMS={periodSummary?.to_ms ?? 0}
                />
              </div>
            </div>
          </Panel>

          <RealtimeExchangeShareChart
            initialFlow={analysis.realtime_flow}
            realtimeFlowStore={realtimeFlowStore}
          />
        </div>
      </section>

      <section
        id="intelligence-drivers"
        data-mi-chapter="drivers"
        className="mi-chapter"
        aria-labelledby="intelligence-drivers-title"
      >
        <IntelligenceChapterHeading
          chapterId="drivers"
          title="코인별 빗썸 점유율 기여"
          description="코인별 빗썸 점유율 변화 기여와 해당 코인 거래금액 중 빗썸 비중을 확인합니다."
        />
        <div className="grid gap-6 xl:grid-cols-2">
          <Panel
            title={contribution.title}
            description={`계산된 코인별 점유율 기여를 0을 기준으로 비교합니다. ${contribution.source || "자료 확인 중"}.`}
            descriptionRole="status"
            icon={<Layers3 className="h-4 w-4" />}
          >
            <ContributionWaterfall
              rows={contribution.rows}
              selectedAsset={effectiveSelectedAssetName}
              onSelect={setSelectedAssetName}
            />
          </Panel>
          <Panel
            title="코인별 빗썸 거래금액 비중"
            description={intelligenceCaptureDescription(
              analysis.context.window_sec
            )}
            icon={<Crosshair className="h-4 w-4" />}
            actions={
              selectedAsset ? (
                <span className="rounded-md border border-primary/30 bg-primary/10 px-2 py-1 font-mono text-base text-primary">
                  {selectedAsset.asset}
                </span>
              ) : null
            }
          >
            <CaptureMap
              assets={analysis.assets}
              selectedAsset={effectiveSelectedAssetName}
              onSelect={setSelectedAssetName}
            />
            {selectedAsset ? (
              <div className="grid gap-2 border-t border-border px-4 py-3 text-base sm:grid-cols-3 sm:px-5">
                <span>국내 {money(selectedAsset.domestic_volume_krw)}</span>
                <span>
                  빗썸 거래금액 비중{" "}
                  {percent(selectedAsset.bithumb_capture_pct, 2)}
                </span>
                <span>
                  전체 빗썸 점유율 대비{" "}
                  {points(selectedAsset.capture_gap_vs_venue_pp)}
                </span>
              </div>
            ) : null}
          </Panel>
        </div>
      </section>

      <section
        id="intelligence-network"
        data-mi-chapter="network"
        className="mi-chapter"
        aria-labelledby="intelligence-network-title"
      >
        <IntelligenceChapterHeading
          chapterId="network"
          title="코인·거래소 관계"
          description="코인별 점유율 변화 기여와 국내 거래소별 거래금액이 어떻게 연결되는지 확인합니다."
        />
        <Panel
          title={`${marketShareNetworkWindowLabel(networkWindowSec)} 코인·거래소 점유율 기여 관계도`}
          description="슬라이더에서 1분부터 24시간까지 선택해, 최근 구간과 직전 동일 구간의 점유율 기여와 거래소별 거래금액 연결을 비교합니다."
          icon={<NetworkIcon className="h-4 w-4" />}
        >
          <DeferredRender
            rootMargin="160px 0px"
            fallback={<div className="min-h-[420px]" aria-hidden="true" />}
          >
            <Suspense
              fallback={<div className="min-h-[420px]" aria-hidden="true" />}
            >
              <AssetExchangeShareNetwork
                windowSec={networkWindowSec}
                onWindowSecChange={setNetworkWindowSec}
              />
            </Suspense>
          </DeferredRender>
        </Panel>
      </section>

      <section
        id="intelligence-episodes"
        data-mi-chapter="episodes"
        className="mi-chapter"
        aria-labelledby="intelligence-episodes-title"
      >
        <IntelligenceChapterHeading
          chapterId="episodes"
          title="10초 연속 변화"
          description="같은 방향으로 이어진 10초 변화를 하나로 묶어 지속 시간, 가장 큰 변화와 반복해서 나타난 코인을 확인합니다."
        />
        <Panel
          title="같은 방향의 10초 변화 묶음"
          description="같은 방향의 10초 변화가 20초 안에 이어지면 하나로 묶습니다. 각 묶음에서 지속 시간, 실제 확인 횟수, 가장 큰 변화와 주요 코인을 확인합니다."
          icon={<Activity className="h-4 w-4" />}
          actions={
            breadthAvailable && breadth ? (
              <span className="text-base text-muted-foreground">
                기여 확인 코인 {effectiveCount(breadth.effective_driver_count)}
                개
              </span>
            ) : null
          }
        >
          {breadthAvailable && breadth ? (
            <div className="grid gap-px border-b border-border bg-border sm:grid-cols-3 xl:grid-cols-6">
              {[
                [
                  "주요 상승 기여 코인",
                  breadth.positive_driver_count.toLocaleString("ko-KR"),
                ],
                [
                  "주요 하락 기여 코인",
                  breadth.negative_driver_count.toLocaleString("ko-KR"),
                ],
                [
                  "기여 확인 코인",
                  effectiveCount(breadth.effective_driver_count),
                ],
                [
                  "1위 코인 기여 비중",
                  percent(breadth.top1_absolute_contribution_share_pct, 1),
                ],
                [
                  "상위 3개 코인 기여 비중",
                  percent(breadth.top3_absolute_contribution_share_pct, 1),
                ],
                [
                  "상위 5개 코인 기여 비중",
                  percent(breadth.top5_absolute_contribution_share_pct, 1),
                ],
              ].map(([label, value]) => (
                <div key={label} className="bg-card px-4 py-3">
                  <p className="text-base text-muted-foreground">{label}</p>
                  <p className="mt-1 font-mono text-sm font-semibold">
                    {value}
                  </p>
                </div>
              ))}
            </div>
          ) : null}
          <div className="grid xl:grid-cols-[minmax(0,1.4fr)_minmax(19rem,0.6fr)]">
            <EpisodeList episodes={episodes} />
            <div className="space-y-3 border-t border-border p-4 xl:border-l xl:border-t-0">
              <DriverRanking
                title="같은 방향으로 기여한 비율"
                rows={periodDrivers}
                mode="consistency"
              />
              <DriverRanking
                title="변화 묶음에 등장한 횟수"
                rows={periodDrivers}
                mode="episodes"
              />
            </div>
          </div>
        </Panel>
      </section>

      <footer className="flex flex-wrap items-center justify-between gap-3 px-1 text-base leading-6 text-muted-foreground">
        <span>
          {hasRealtimeBasis ? "10초 흐름 기준" : "자료 기준"} {dateTime(asOfMS)}
        </span>
      </footer>
    </div>
  );
}
