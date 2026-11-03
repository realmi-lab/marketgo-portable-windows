import { useMemo, useState, type ReactNode } from "react";
import { collectorConnectionTone } from "@/lib/collector-connection-warning";

import { ObservedRangeHelp } from "@/components/common/ObservedRangeHelp";
import {
  PRIORITY_ORDER_BOOK_ASSETS,
  PriorityBestOrderBookGrid,
  PriorityBestOrderBookStatus,
} from "@/components/PriorityBestOrderBooks";
import { useMediaQuery } from "@/hooks/useMediaQuery";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatKRW,
  formatKrwMagnitude,
  formatPercentagePointMagnitude,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { noticeCategoryLabel } from "@/lib/market-share-labels";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  visibleWhaleAlerts,
  whaleAlertCountLabel,
  whaleAlertFailureNote,
  whaleAlertKindLabel,
  whaleAlertSideLabel,
} from "@/lib/market-share-whale-alert";
import { noticeAnalysisStatusLabel } from "@/lib/notice-analysis-status";
import {
  bucketLabel,
  comparisonNote,
  comparisonScope,
  comparisonUsable,
  kstDay,
  kstHour,
  kstHourLabel,
  mixCaptureSentence,
  shareBridgeSentence,
  sourceStatusLabel,
  windowComparisonNote,
  windowLabel,
  windowShare,
  type ComparisonStatus,
  official24hReceipt,
} from "@/lib/screen-copy";
import type {
  MarketShareAnalysis,
  MarketShareOfficialAligned1hState,
  MarketShareOfficialAlignedContribution,
  MarketShareOfficialMovementEvent,
  MarketShareSeriesBucketSec,
  MarketShareWindowChange,
} from "@/lib/market-share-types";
import {
  HourlyShareChart,
  SecondFlowChart,
  ShareBar,
  type ChartMark,
} from "./charts";
import {
  useOfficialHistory,
  useRecentNotices,
  useShareSeries,
  useWindowChanges,
} from "./data";
import {
  secondIsComplete,
  secondShare,
  useLiveTick,
  useMarketStream,
  useShownFlow,
} from "./market-stream";
import {
  Band,
  Card,
  Chip,
  Empty,
  Ex,
  exchangeColor,
  Foot,
  Kpi,
  Legend,
  LiveValue,
  Pct,
  Pp,
  Seg,
  StaleNote,
  Table,
  WindowSeg,
  type SegOption,
  Won,
  type CardSize,
} from "./ui";

/* Cards that more than one screen shows. */

export const DOMESTIC = [
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
] as const;

export function useAnalysis(): Partial<MarketShareAnalysis> | undefined {
  return useMarketStream().data?.analysis as
    Partial<MarketShareAnalysis> | undefined;
}

/** The selected window's share, named by its source and basis. */
export function useWindowShare() {
  const { windowSec } = useMarketStream();
  return windowShare(useAnalysis(), windowSec);
}

/**
 * The selected window's share as a figure. The detail says what is missing
 * from a partly observed window; otherwise it shows `comparison`.
 */
export function WindowShareKpi({ comparison }: { comparison?: ReactNode }) {
  const share = useWindowShare();
  return (
    <Kpi
      label={`${share.window} 점유율`}
      tag={share.source}
      value={
        <LiveValue value={share.share}>
          <Pct value={share.share} />
        </LiveValue>
      }
      detail={
        share.basis || share.note ? (
          <>
            {share.basis ? <ObservedRangeHelp /> : null}
            <span className="mg-muted">{share.note}</span>
          </>
        ) : (
          comparison
        )
      }
    />
  );
}

/**
 * "직전 10분보다 +6.55%p", naming the exchanges a provisional comparison
 * leaves out, or why there is no comparison.
 */
export function VersusPrevious({
  windowSec,
  change,
}: {
  windowSec: number;
  change:
    (ComparisonStatus & { share_change_pp: number | null }) | null | undefined;
}) {
  if (!comparisonUsable(change) || change?.share_change_pp == null) {
    return (
      <span className="mg-muted">
        {windowComparisonNote(windowSec, change)}
      </span>
    );
  }
  const scope = comparisonScope(change);
  return (
    <>
      직전 {windowLabel(windowSec)}보다 <Pp value={change.share_change_pp} />
      {scope ? <span className="mg-attn"> · {scope}</span> : null}
    </>
  );
}

/** How each rolling window stands now and against the window before it. */
export function WindowsCard({
  span = 12,
  size = "xl",
}: {
  span?: 7 | 12;
  size?: CardSize;
}) {
  const analysis = useAnalysis();
  const changes = useWindowChanges();
  const official = analysis?.official_domestic_24h?.bithumb_share_pct ?? null;
  const byWindow = new Map<number, MarketShareWindowChange>(
    (changes.data?.windows ?? []).map(row => [row.window_sec, row])
  );
  const hourly = useOfficialHistory(48);
  const hourPoints = hourly.data?.items ?? [];
  const latestHour = hourPoints.at(-1);
  const hourAgo = (count: number) =>
    hourPoints.find(
      point =>
        latestHour &&
        point.bucket_ms === latestHour.bucket_ms - count * 3_600_000
    );
  return (
    <Card
      span={span}
      size={size}
      title="구간별 점유율"
      note="국내 5개 거래소의 실시간 체결 기준입니다."
      actions={
        <a className="mg-button" href="#change">
          자세히
        </a>
      }
    >
      <StaleNote read={changes} />
      <StaleNote read={hourly} />
      <Legend
        items={[
          {
            key: "official",
            label: `공식 24시간 점유율 ${formatSharePercent(official)} (막대 속 세로선)`,
            color: "var(--foreground)",
            tick: true,
          },
        ]}
      />
      <Table
        label="구간별 점유율"
        head={
          <tr>
            <th>구간</th>
            <th className="mg-num">점유율</th>
            <th className="mg-num">직전 구간보다</th>
            <th className="mg-num">정시 N시간 전</th>
            <th className="mg-bar-col">공식 대비</th>
          </tr>
        }
      >
        {(analysis?.momentum ?? []).map(row => {
          const change = byWindow.get(row.duration_sec);
          const partial =
            row.coverage_status === "PARTIAL" || row.window_complete === false;
          const hourCount = row.duration_sec / 3600;
          const officialThen =
            Number.isInteger(hourCount) && hourCount >= 1
              ? hourAgo(hourCount)
              : undefined;
          return (
            <tr key={row.duration_sec}>
              <td className="mg-name">{windowLabel(row.duration_sec)}</td>
              <td className="mg-num">
                <Pct value={row.share_pct} />
                {partial && row.share_pct !== null ? (
                  <ObservedRangeHelp />
                ) : null}
              </td>
              <td className="mg-num">
                {row.duration_sec === 86_400 ? (
                  <span className="mg-muted">비교 안 함</span>
                ) : change?.share_change_pp != null ? (
                  <span title={comparisonNote(change) || undefined}>
                    <Pp value={change.share_change_pp} />
                    {comparisonScope(change) ? (
                      <span className="mg-attn">
                        {" "}
                        ({comparisonScope(change)})
                      </span>
                    ) : null}
                  </span>
                ) : (
                  <span className="mg-muted" title={comparisonNote(change)}>
                    {change ? "비교 불가" : "—"}
                  </span>
                )}
              </td>
              <td className="mg-num">
                {officialThen ? (
                  <>
                    <Pct value={officialThen.bithumb_share_pct} />{" "}
                    <span className="mg-muted">
                      {kstHour(officialThen.bucket_ms)}
                    </span>
                  </>
                ) : (
                  <span className="mg-muted">—</span>
                )}
              </td>
              <td>
                <ShareBar share={row.share_pct} reference={official} />
              </td>
            </tr>
          );
        })}
      </Table>
      <Foot>
        정시는 업비트·빗썸·코인원의 공식 시간봉입니다. 24시간 구간은 직전
        24시간과 비교하지 않습니다.
        {changes.error && !changes.data
          ? ` 직전 비교를 받지 못했습니다. ${changes.error}`
          : ""}
      </Foot>
    </Card>
  );
}

/** The server's official rising or falling phase, as one line. */
export function phaseLine(
  event: MarketShareOfficialMovementEvent | null | undefined
): string {
  if (!event) return "";
  const rising = event.direction === "UP";
  const since = formatMarketTimeMS(event.started_at_ms, "time");
  const state = event.state === "ACTIVE" ? "진행 중" : "끝남";
  return `${rising ? "상승" : "하락"} 국면 ${state} · ${since}부터 ${formatSignedPercentagePoints(event.change_pp)}`;
}

/** The share chart's bucket sizes: realtime minutes to half hours, official hours. */
type ShareUnit = MarketShareSeriesBucketSec | 3600;
const SHARE_UNITS: readonly SegOption<ShareUnit>[] = [
  { value: 60, label: "1분" },
  { value: 600, label: "10분" },
  { value: 1800, label: "30분" },
  { value: 3600, label: "1시간" },
];

/** How far back each realtime bucket size reaches (share-series). */
const REALTIME_REACH: Record<MarketShareSeriesBucketSec, string> = {
  60: "2시간",
  600: "24시간",
  1800: "24시간",
};

/**
 * Bithumb's share over time in the bucket size picked: each realtime minute
 * of two hours, ten minutes or half hour of a day (five exchanges' trades),
 * or each official hour (Upbit, Bithumb and Coinone candles). `marks` draws
 * moments such as notices on every size, and a point's tooltip lists those
 * in its bucket.
 */
export function ShareTrendCard({
  span = 7,
  size = "l",
  chartHeight = 360,
  title,
  hours = 48,
  marks,
  selectedMS,
  onSelect,
  children,
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
  chartHeight?: number;
  /** The card's title for every size, instead of one per size. */
  title?: string;
  /** How many official hours 1시간 shows. */
  hours?: number;
  marks?: readonly ChartMark[];
  /** The official hour chosen, and how to choose one (1시간 only). */
  selectedMS?: number | null;
  onSelect?: (bucketMS: number) => void;
  /** Shown under the chart, such as what the marks are. */
  children?: ReactNode;
}) {
  const [unit, setUnit] = useState<ShareUnit>(3600);
  // A phone's narrower chart needs less height for the same buckets.
  const phone = useMediaQuery("(max-width: 900px)");
  const body: ShareTrendBody = {
    span,
    size,
    height: phone ? Math.min(chartHeight, 300) : chartHeight,
    title,
    marks,
    picker: (
      <Seg
        label="점유율 단위"
        options={SHARE_UNITS}
        value={unit}
        onChange={setUnit}
      />
    ),
    children,
  };
  return unit === 3600 ? (
    <OfficialHours
      {...body}
      hours={hours}
      selectedMS={selectedMS}
      onSelect={onSelect}
    />
  ) : (
    <RealtimeBuckets {...body} bucketSec={unit} />
  );
}

interface ShareTrendBody {
  span: 5 | 6 | 7 | 12;
  size: CardSize;
  height: number;
  title?: string;
  marks?: readonly ChartMark[];
  picker: ReactNode;
  children?: ReactNode;
}

/** What a card with marks adds to its note. */
const MARKS_NOTE =
  " 점선은 공지가 올라온 시각(거래소 색)이고, 점을 가리키거나 누르면 그 구간의 공지가 나옵니다.";

/** Each realtime minute (two hours), ten minutes or half hour (a day). */
function RealtimeBuckets({
  span,
  size,
  height,
  title,
  marks,
  picker,
  children,
  bucketSec,
}: ShareTrendBody & { bucketSec: MarketShareSeriesBucketSec }) {
  const series = useShareSeries(bucketSec);
  const unit = SHARE_UNITS.find(option => option.value === bucketSec)!.label;
  const items = series.data?.points ?? [];
  const latest = items.at(-1);
  const partial = items.filter(
    point => !point.complete && point.bithumb_share_pct !== null
  ).length;
  const missing = items.filter(point => point.status === "NOT_OBSERVED").length;
  return (
    <Card
      span={span}
      size={size}
      title={title ?? "점유율 흐름"}
      note={`국내 5곳 실시간 체결의 ${unit} 점유율, 최근 ${REALTIME_REACH[bucketSec]}.${marks ? MARKS_NOTE : ""}`}
      actions={picker}
    >
      <StaleNote read={series} />
      {series.loading ? (
        <Empty>{unit} 점유율을 불러오는 중입니다.</Empty>
      ) : !series.data ? (
        <Empty>
          {unit} 점유율을 받지 못했습니다. {series.error}
        </Empty>
      ) : (
        <HourlyShareChart
          label={`${unit} 점유율 흐름`}
          height={height}
          bucketMS={bucketSec * 1_000}
          points={items.map(point => ({
            bucket_ms: point.bucket_ms,
            share: point.bithumb_share_pct,
            partial: !point.complete,
          }))}
          marks={marks}
        />
      )}
      <Foot>
        {latest
          ? `마지막 ${unit} ${bucketLabel(latest.bucket_ms, bucketSec * 1_000)} ${formatSharePercent(latest.bithumb_share_pct)}.`
          : `${unit} 자료 없음.`}
        {partial ? ` 빈 점 ${partial}개는 일부만 관측했습니다.` : ""}
        {missing ? ` 관측하지 못한 ${missing}개 구간은 비워 두었습니다.` : ""}
        {partial ? <ObservedRangeHelp /> : null}
      </Foot>
      {children}
    </Card>
  );
}

/** Each official hour of the last `hours`, each hour's value on the line. */
function OfficialHours({
  span,
  size,
  height,
  title,
  marks,
  picker,
  children,
  hours,
  selectedMS,
  onSelect,
}: ShareTrendBody & {
  hours: number;
  selectedMS?: number | null;
  onSelect?: (bucketMS: number) => void;
}) {
  const analysis = useAnalysis();
  const history = useOfficialHistory(hours);
  const movement = analysis?.official_share_monitor?.movement ?? null;
  const items = history.data?.items ?? [];
  const latest = items.at(-1);
  const at = (hoursAgo: number) =>
    items.find(
      point =>
        latest && point.bucket_ms === latest.bucket_ms - hoursAgo * 3_600_000
    );
  const dayAgo = at(24);
  const halfDayAgo = at(12);
  return (
    <Card
      span={span}
      size={size}
      title={title ?? `정시 점유율 ${hours}시간`}
      note={
        onSelect
          ? "업비트·빗썸·코인원 공식 시간봉. 점을 누르면 그 시각의 1시간 기여를 아래에서 봅니다."
          : `업비트·빗썸·코인원 공식 시간봉의 24시간 합계, 매시 정각.${marks ? MARKS_NOTE : ""}`
      }
      actions={
        <>
          {picker}
          {movement ? (
            <Chip tone={movement.direction === "UP" ? "ok" : "alert"}>
              {phaseLine(movement)}
            </Chip>
          ) : null}
          {onSelect && items.length ? (
            // The list the chart's hours can also be chosen from by keyboard.
            <select
              className="mg-button"
              aria-label="1시간 기여를 볼 시각"
              value={selectedMS ? String(selectedMS) : ""}
              onChange={event => onSelect(Number(event.target.value))}
            >
              <option value="" disabled>
                시각 고르기
              </option>
              {[...items].reverse().map(point => (
                <option key={point.bucket_ms} value={String(point.bucket_ms)}>
                  {kstHourLabel(point.bucket_ms)}{" "}
                  {formatSharePercent(point.bithumb_share_pct)}
                </option>
              ))}
            </select>
          ) : null}
        </>
      }
    >
      <StaleNote read={history} />
      {history.loading ? (
        <Empty>정시 자료를 불러오는 중입니다.</Empty>
      ) : !history.data ? (
        <Empty>정시 자료를 받지 못했습니다. {history.error}</Empty>
      ) : (
        <HourlyShareChart
          label={`정시 점유율 ${hours}시간`}
          height={height}
          points={items.map(point => ({
            bucket_ms: point.bucket_ms,
            share: point.bithumb_share_pct,
          }))}
          phase={
            movement
              ? {
                  fromMS: movement.started_at_ms,
                  toMS: movement.ended_at_ms ?? Date.now(),
                  direction: movement.direction,
                }
              : null
          }
          marks={marks}
          selectedMS={selectedMS}
          onSelect={onSelect}
        />
      )}
      <Foot>
        {latest
          ? `지금 ${kstHour(latest.bucket_ms)} ${formatSharePercent(latest.bithumb_share_pct)}`
          : "정시 자료 없음"}
        {dayAgo
          ? `, 24시간 전 ${formatSharePercent(dayAgo.bithumb_share_pct)}`
          : ""}
        {halfDayAgo
          ? `, 12시간 전 ${formatSharePercent(halfDayAgo.bithumb_share_pct)}`
          : ""}
        .
        {movement
          ? ` 색칠한 부분은 ${formatMarketTimeMS(movement.started_at_ms, "time")}에 시작해 ${formatPercentagePointMagnitude(movement.threshold_pp)} 넘게 ${movement.direction === "UP" ? "오른" : "내린"} 국면입니다.`
          : ""}
        {history.data && history.data.missing_point_count > 0
          ? ` 빠진 정시 ${history.data.missing_point_count}개는 비워 두었습니다.`
          : ""}
      </Foot>
      {children}
    </Card>
  );
}

/** The selected window's change split into Bithumb and competitor effects. */
export function BridgeCard({
  span = 5,
  size = "l",
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
}) {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const label = windowLabel(windowSec);
  const segments = analysis?.expert_brief?.listing_segments ?? [];
  const note = windowComparisonNote(windowSec, change);
  const scope = comparisonScope(change);
  const range =
    analysis?.context?.current_interval_start &&
    analysis.context.current_interval_end
      ? `${formatMarketTimeMS(analysis.context.current_interval_start * 1000, "time-seconds")}~${formatMarketTimeMS(analysis.context.current_interval_end * 1000, "time-seconds")}`
      : "";
  return (
    <Card
      span={span}
      size={size}
      title={`${label} 변화 나누기`}
      note={`${range ? `${range}, ` : ""}${scope ? `${scope}하고 비교` : "실시간 5개 거래소"}`}
    >
      {!comparisonUsable(change) || change?.share_change_pp == null ? (
        <Empty>{note || "변화 자료가 없습니다."}</Empty>
      ) : (
        <>
          <ul className="mg-list">
            <li>
              <span>직전 {label}</span>
              <Pct value={change.previous_share_pct} />
            </li>
            <li>
              <span>
                빗썸 거래금액{" "}
                <Won value={change.bithumb_volume_change_krw} signed />
              </span>
              <Pp value={change.bithumb_direct_effect_pp} />
            </li>
            <li>
              <span>
                경쟁 거래소{" "}
                <Won value={change.competitor_volume_change_krw} signed />
              </span>
              <Pp value={change.competitor_effect_pp} />
            </li>
            <li>
              <span className="mg-name">지금 {label}</span>
              <span className="mg-name">
                <Pct value={change.current_share_pct} /> (
                <Pp value={change.share_change_pp} />)
              </span>
            </li>
          </ul>
          {typeof change.mix_effect_pp === "number" ? (
            <ul className="mg-list" aria-label="같은 변화를 코인으로 나누기">
              <li>
                <span>거래가 몰린 코인 효과</span>
                <Pp value={change.mix_effect_pp} />
              </li>
              <li>
                <span>같은 코인 안 빗썸 비중</span>
                <Pp value={change.capture_effect_pp} />
              </li>
            </ul>
          ) : null}
          {segments.length ? (
            <Table
              label="상장 구분별 기여"
              head={
                <tr>
                  <th>상장 구분</th>
                  <th className="mg-num">코인</th>
                  <th className="mg-num">기여</th>
                </tr>
              }
            >
              {segments.map(segment => (
                <tr key={segment.segment}>
                  <td>{segment.label}</td>
                  <td className="mg-num">
                    {segment.asset_count.toLocaleString("ko-KR")}개
                  </td>
                  <td className="mg-num">
                    <Pp value={segment.contribution_pp} />
                  </td>
                </tr>
              ))}
            </Table>
          ) : null}
          <Foot>
            {shareBridgeSentence(label, change)}{" "}
            {mixCaptureSentence(change.mix_effect_pp, change.capture_effect_pp)}
            {note ? ` ${note}.` : ""}
          </Foot>
        </>
      )}
    </Card>
  );
}

/** One official hour compared with the hour before it. */
export type HourComparison = Pick<
  MarketShareOfficialAligned1hState,
  | "status"
  | "reason"
  | "previous"
  | "current"
  | "share_delta_pp"
  | "domestic_volume_delta_krw"
  | "bithumb_volume_delta_krw"
  | "drivers"
  | "exchange_drivers"
>;

/** The selected-hour endpoint names its coin list asset_drivers. */
export function hourComparison(
  value: MarketShareOfficialAlignedContribution
): HourComparison {
  return { ...value, drivers: value.asset_drivers };
}

/** What moved the official share over the last closed hour. */
export function HourContributionCard({
  span = 6,
  size = "l",
  hour,
  pending = "",
  read,
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
  /** A chosen hour; the latest hour when absent. */
  hour?: HourComparison | null;
  /** Why the chosen hour is not shown yet; the card never falls back to the latest hour meanwhile. */
  pending?: string;
  /** The chosen hour's read, to say when it is stale. */
  read?: { stale: boolean; error: string; receivedAtMS: number };
}) {
  const analysis = useAnalysis();
  const state = pending ? null : (hour ?? analysis?.official_ticker_1m ?? null);
  const [view, setView] = useState<"exchanges" | "coins">("exchanges");
  const drivers = state?.drivers ?? [];
  const exchanges = state?.exchange_drivers ?? [];
  const title =
    state?.previous && state.current
      ? `${formatMarketTimeMS(state.previous.bucket_ms, "time")} → ${formatMarketTimeMS(state.current.bucket_ms, "time")}`
      : "";
  return (
    <Card
      span={span}
      size={size}
      title="1시간 변화에 기여한 것"
      note={`${title ? `${title}, ` : ""}업비트·빗썸·코인원 공식 시간봉`}
      actions={
        <div className="mg-seg" role="group" aria-label="나누는 기준">
          <button
            type="button"
            aria-pressed={view === "exchanges"}
            onClick={() => setView("exchanges")}
          >
            거래소
          </button>
          <button
            type="button"
            aria-pressed={view === "coins"}
            onClick={() => setView("coins")}
          >
            코인
          </button>
        </div>
      }
    >
      {read ? <StaleNote read={read} /> : null}
      {pending ? <Empty>{pending}</Empty> : null}
      <p className="mg-copy">
        점유율 <Pp value={state?.share_delta_pp} />, 국내 3곳 거래금액{" "}
        <Won value={state?.domestic_volume_delta_krw} signed />, 빗썸{" "}
        <Won value={state?.bithumb_volume_delta_krw} signed />
      </p>
      {view === "exchanges" ? (
        exchanges.length ? (
          <Table
            label="거래소별 1시간 기여"
            head={
              <tr>
                <th>거래소</th>
                <th className="mg-num">거래금액 변화</th>
                <th>가장 큰 코인</th>
                <th className="mg-num">기여</th>
              </tr>
            }
          >
            {exchanges.map(row => (
              <tr key={row.exchange}>
                <td>
                  <Ex exchange={row.exchange} />
                </td>
                <td className="mg-num">
                  <Won value={row.volume_change_krw} signed />
                </td>
                <td>
                  {row.leading_asset}{" "}
                  <Pp value={row.leading_asset_contribution_pp} />
                </td>
                <td className="mg-num">
                  <Pp value={row.contribution_pp} />
                </td>
              </tr>
            ))}
          </Table>
        ) : (
          <Empty>
            1시간 비교 자료가 없습니다
            {state?.status ? ` (${sourceStatusLabel(state.status)})` : ""}.
          </Empty>
        )
      ) : drivers.length ? (
        <Table
          label="코인별 1시간 기여"
          head={
            <tr>
              <th>코인</th>
              <th className="mg-num">빗썸</th>
              <th className="mg-num">경쟁 거래소</th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {drivers.slice(0, 10).map(row => (
            <tr key={row.asset}>
              <td className="mg-name">{row.asset}</td>
              <td className="mg-num">
                <Won value={row.bithumb_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                <Won value={row.competitor_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                <Pp value={row.contribution_pp} />
              </td>
            </tr>
          ))}
        </Table>
      ) : (
        <Empty>코인별 1시간 기여 자료가 없습니다.</Empty>
      )}
      <Foot>
        경쟁 거래소 거래가 줄면 빗썸 점유율이 오르므로, 거래금액 변화와 기여의
        부호가 반대일 수 있습니다. 코빗·고팍스는 시간별 원화 거래금액을 공개하지
        않아 3곳으로 비교합니다.
      </Foot>
    </Card>
  );
}

/**
 * Today's notices (KST) from the newest page of stored notices. When that
 * page is full and its oldest notice is still today's, there may be more:
 * `countLabel` then reads "100건 이상".
 */
export function useTodayNotices() {
  const notices = useRecentNotices();
  const today = kstDay(Date.now());
  const page = notices.data;
  const items = useMemo(
    () =>
      (page?.snapshot.items ?? []).filter(
        item =>
          item.published_ts_ms > 0 && kstDay(item.published_ts_ms) === today
      ),
    [page, today]
  );
  const oldest = page?.snapshot.items.at(-1);
  const more = Boolean(
    page &&
    oldest &&
    page.total > page.offset + page.snapshot.items.length &&
    kstDay(oldest.published_ts_ms) === today
  );
  return {
    notices,
    items,
    countLabel: page ? `${items.length}건${more ? " 이상" : ""}` : "—",
    /** Until a page arrives: that it is loading or failed, never "none". */
    waiting: page
      ? null
      : notices.error
        ? `공지를 받지 못했습니다. ${notices.error}`
        : "공지를 불러오는 중입니다.",
  };
}

/** Today's stored notices, newest first. */
export function TodayNoticesCard({
  span = 7,
  size = "s",
  limit = 20,
}: {
  span?: 6 | 7 | 12;
  size?: CardSize;
  limit?: number;
}) {
  const { notices, items, countLabel, waiting } = useTodayNotices();
  const byExchange = DOMESTIC.map(exchange => ({
    exchange,
    count: items.filter(item => item.exchange === exchange).length,
  })).filter(row => row.count > 0);
  return (
    <Card
      span={span}
      size={size}
      title={`오늘 공지 ${countLabel}`}
      note={
        byExchange
          .map(row => `${exchangeLabel(row.exchange)} ${row.count}건`)
          .join(", ") || (waiting ? "" : "오늘 올라온 공지가 없습니다")
      }
      actions={
        <a className="mg-button" href="#notices">
          공지 전체
        </a>
      }
    >
      {notices.loading ? (
        <Empty>공지를 불러오는 중입니다.</Empty>
      ) : notices.error && !notices.data ? (
        <Empty>공지를 받지 못했습니다. {notices.error}</Empty>
      ) : (
        <>
          <StaleNote read={notices} />
          <ul className="mg-list">
            {items.slice(0, limit).map(item => (
              <li key={`${item.exchange}:${item.notice_id}`}>
                <span>
                  <span className="mg-num mg-muted">
                    {formatMarketTimeMS(item.published_ts_ms, "time")}
                  </span>{" "}
                  <a href={item.url} target="_blank" rel="noreferrer">
                    {item.title}
                  </a>
                </span>
                <span className="mg-muted">
                  {exchangeLabel(item.exchange)} ·{" "}
                  {noticeCategoryLabel(item.category)} ·{" "}
                  {noticeAnalysisStatusLabel(item.analysis_status)}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </Card>
  );
}

/** The last minute second by second, with the three largest contributors. */
export function SecondFlowCard({
  span = 5,
  size = "xl",
  height = 300,
  link = true,
}: {
  span?: 5 | 6 | 7 | 8 | 12;
  size?: CardSize;
  height?: number;
  link?: boolean;
}) {
  const { flow, visible } = useShownFlow();
  // A second missing an exchange shows what was observed in it; a second
  // never received leaves a gap.
  const seconds = (flow?.recent_share_1s ?? []).slice(-60);
  const points = seconds.map(point => ({
    as_of_ms: point.as_of_ms,
    share: secondShare(point),
    values: point.exchange_volumes_1s_krw,
  }));
  const partial = seconds.some(point => !secondIsComplete(point));
  return (
    <Card
      span={span}
      size={size}
      title="지금 1초 흐름"
      note="막대는 거래소별 1초 거래금액, 가는 선은 빗썸의 1초 점유율입니다."
      actions={
        link ? (
          <a className="mg-button" href="#live">
            실시간에서 크게
          </a>
        ) : null
      }
    >
      <SecondFlowChart
        points={points}
        exchanges={DOMESTIC}
        height={height}
        formatValue={formatKRW}
        label="최근 60초 거래소별 1초 거래금액과 빗썸 점유율"
      />
      <ul className="mg-list" aria-label="지금 점유율을 가장 크게 움직인 코인">
        {(visible ? (flow?.drivers ?? []) : []).slice(0, 3).map(driver => (
          <li key={driver.asset}>
            <span>
              {driver.rank}위 <b>{driver.asset}</b>
            </span>
            <Pp value={driver.contribution_pp} />
          </li>
        ))}
      </ul>
      {flow?.reason || partial ? (
        <Foot>
          {flow?.reason}
          {partial ? <ObservedRangeHelp /> : null}
        </Foot>
      ) : null}
    </Card>
  );
}

/** One line per data source: how far the numbers on screen can be trusted. */
export function DataStatusCard({
  span = 5,
  size = "s",
}: {
  span?: 5 | 6 | 12;
  size?: CardSize;
}) {
  const analysis = useAnalysis();
  const { readiness, connection, deferredCollectorWarnings } =
    useMarketStream();
  const official = analysis?.official_domestic_24h;
  const officialReceipt = official24hReceipt(official);
  const hour = analysis?.official_ticker_1m;
  const summary = analysis?.summary;
  const rows: Array<{
    name: string;
    value: string;
    tone: "ok" | "warn" | "alert" | "muted";
  }> = [
    {
      name: "공식 24시간",
      value: official
        ? `${officialReceipt.label} · ${formatMarketTimeMS(official.as_of * 1000, "time-seconds")}`
        : officialReceipt.label,
      tone: officialReceipt.ok ? "ok" : "warn",
    },
    {
      name: "정시 시간봉",
      value: hour
        ? `${sourceStatusLabel(hour.status)} · 3곳 (코빗·고팍스 미공개)`
        : "자료 없음",
      tone: hour?.status === "LIVE" ? "ok" : "warn",
    },
    {
      name: "실시간 체결",
      value: readiness
        ? `${readiness.connected_market_count.toLocaleString("ko-KR")}/${readiness.registered_market_count.toLocaleString("ko-KR")}개 마켓 연결`
        : connection === "live"
          ? "연결됨"
          : "연결 확인 중",
      tone: collectorConnectionTone(readiness, deferredCollectorWarnings),
    },
    {
      name: "선택 구간",
      value: analysis?.context
        ? analysis.context.selected_window_complete
          ? "빠진 시간 없음"
          : `${analysis.context.selected_window_coverage_sec.toLocaleString("ko-KR")}/${analysis.context.selected_window_required_sec.toLocaleString("ko-KR")}초 관측`
        : "자료 없음",
      tone: analysis?.context?.selected_window_complete ? "ok" : "warn",
    },
    {
      name: "환율",
      value:
        summary && summary.domestic_unresolved_fx_count > 0
          ? `원화로 바꾸지 못한 체결 ${summary.domestic_unresolved_fx_count.toLocaleString("ko-KR")}건`
          : summary
            ? "모두 원화로 환산"
            : "자료 없음",
      tone: !summary
        ? "muted"
        : summary.domestic_unresolved_fx_count > 0
          ? "warn"
          : "ok",
    },
  ];
  return (
    <Card
      span={span}
      size={size}
      title="자료 상태"
      note="이 화면의 숫자를 어디까지 믿어도 되는지"
      actions={
        <a className="mg-button" href="#status">
          자세히
        </a>
      }
    >
      <ul className="mg-list">
        {rows.map(row => (
          <li key={row.name}>
            <span className="mg-name">{row.name}</span>
            <Chip tone={row.tone}>{row.value}</Chip>
          </li>
        ))}
      </ul>
    </Card>
  );
}

/** Each exchange's part of the selected window, refreshed every tick. */
export function CompositionCard({
  span = 6,
  size = "m",
  picker = true,
  compactPicker = true,
  windowPicker,
  selectedExchange,
  onSelectExchange,
  showCoverageNote = true,
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
  /** Off where another card on the screen already picks the window. */
  picker?: boolean;
  compactPicker?: boolean;
  windowPicker?: ReactNode;
  selectedExchange?: string;
  showCoverageNote?: boolean;
  onSelectExchange?: (exchange: string) => void;
}) {
  const tick = useLiveTick();
  const { windowSec, setWindowSec } = useMarketStream();
  const rows = DOMESTIC.map(exchange =>
    tick?.exchanges?.find(row => row.exchange === exchange)
  ).filter((row): row is NonNullable<typeof row> => Boolean(row));
  // The heading names the window the shown tick was computed for.
  const shownWindow = windowLabel(tick?.window_sec ?? windowSec);
  return (
    <Card
      span={span}
      size={size}
      title={`거래소 구성 · ${shownWindow}`}
      note="국내 5개 거래소 실시간 체결, 0.5초마다 바뀝니다"
      actions={
        picker ? (
          windowPicker ?? <WindowSeg value={windowSec} onChange={setWindowSec} compact={compactPicker} />
        ) : null
      }
    >
      <Band
        label={`${shownWindow} 거래소별 점유율`}
        parts={rows.map(row => ({
          key: row.exchange,
          share: row.share_pct,
          color: exchangeColor(row.exchange),
        }))}
      />
      <Table
        label="거래소별 점유율"
        head={
          <tr>
            <th>거래소</th>
            <th className="mg-num">점유율</th>
            <th className="mg-num">거래금액</th>
            <th className="mg-num">체결</th>
            <th className="mg-num">체결 비중</th>
          </tr>
        }
      >
        {rows.map(row => (
          <tr
            key={row.exchange}
            className={row.exchange === "bithumb" ? "mg-me" : undefined}
            data-selectable={onSelectExchange ? "" : undefined}
            data-selected={selectedExchange === row.exchange ? "" : undefined}
            onClick={onSelectExchange ? () => onSelectExchange(row.exchange) : undefined}
          >
            <td>
              {onSelectExchange ? <button type="button" className="mg-link portable-exchange-filter" aria-label={`${exchangeLabel(row.exchange)} 코인 보기`} aria-pressed={selectedExchange === row.exchange} title={selectedExchange === row.exchange ? "다시 클릭하면 전체 코인 표시" : "왼쪽에서 이 거래소의 코인 보기"}><Ex exchange={row.exchange} /></button> : <Ex exchange={row.exchange} />}
            </td>
            <td className="mg-num">
              <LiveValue value={row.share_pct}>
                <Pct value={row.share_pct} />
              </LiveValue>
            </td>
            <td className="mg-num">
              <Won value={row.volume_krw} />
            </td>
            <td className="mg-num">
              {row.trade_count.toLocaleString("ko-KR")}건
            </td>
            <td className="mg-num">
              <Pct value={row.trade_count_share_pct} />
            </td>
          </tr>
        ))}
      </Table>
      {showCoverageNote && tick && !tick.window_complete ? (
        <Foot>
          <ObservedRangeHelp />
        </Foot>
      ) : null}
    </Card>
  );
}

/** Large single or split trades from the stream. */
export function WhaleCard({
  span = 6,
  size = "m",
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
}) {
  const { whaleAlerts, whaleAlertsUnavailable, connection } = useMarketStream();
  const events = visibleWhaleAlerts(whaleAlerts, 20);
  // A closed stream brings no new large trades, so the list is the last one.
  const failure = whaleAlertFailureNote(
    whaleAlerts,
    whaleAlertsUnavailable || connection === "offline"
  );
  return (
    <Card
      span={span}
      size={size}
      title={
        whaleAlerts
          ? `큰 체결 · ${formatKrwMagnitude(whaleAlerts.threshold_krw)} 이상`
          : "큰 체결"
      }
      note="한 번에, 또는 1초 안에 나눠 체결된 큰 거래"
      actions={<Chip>{whaleAlertCountLabel(whaleAlerts)}</Chip>}
    >
      <div role="status">
        {failure ? <p className="mg-attn">{failure}</p> : null}
      </div>
      {events.length === 0 ? (
        failure ? null : (
          <Empty>
            {whaleAlerts
              ? "최근 큰 체결이 없습니다."
              : "큰 체결 자료를 받는 중입니다."}
          </Empty>
        )
      ) : (
        <ul className="mg-list">
          {events.map(event => (
            <li key={event.id}>
              <span>
                <span className="mg-num mg-muted">
                  {formatMarketTimeMS(event.event_at_ms, "time-seconds")}
                </span>{" "}
                <b>{event.asset}</b> {exchangeLabel(event.exchange)} ·{" "}
                <span className={event.side === "BUY" ? "mg-up" : "mg-down"}>
                  {whaleAlertSideLabel(event.side)}
                </span>{" "}
                · {whaleAlertKindLabel(event.kind)}
              </span>
              <Won value={event.notional_krw} />
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/**
 * The best bid and ask of the main coins on Bithumb and Upbit, in the cards
 * the dashboard used before the redesign. Phones swipe through the coins
 * instead of stacking five tall cards.
 */
export function BestQuotesCard() {
  const tick = useLiveTick();
  const { connection } = useMarketStream();
  // The phone width of .mg-quotes in screens.css and of the stacked quote rows
  // in priority-best-order-books.css.
  const swipe = useMediaQuery("(max-width: 767px)");
  return (
    <Card
      span={12}
      size="fit"
      title="업비트·빗썸에서 가장 좋은 매수·매도 가격"
      note="금액은 그 가격에 남은 주문을 원화로 바꾼 값이고, 거래소 옆 괄호는 그 코인 거래금액 중 그 거래소의 몫입니다."
      actions={
        <PriorityBestOrderBookStatus tick={tick} connection={connection} />
      }
    >
      {/* A row swiped sideways can also be scrolled from the keyboard. */}
      <PriorityBestOrderBookGrid
        tick={tick}
        className="mg-quotes"
        tabIndex={swipe ? 0 : undefined}
        role={swipe ? "region" : undefined}
        aria-label={
          swipe
            ? `코인 ${PRIORITY_ORDER_BOOK_ASSETS.length}개의 최우선 호가`
            : undefined
        }
      />
    </Card>
  );
}

/** Each exchange's trading change and part of the selected window's change. */
export function ExchangeChangeCard({
  span = 5,
  size = "m",
}: {
  span?: 5 | 6 | 7 | 12;
  size?: CardSize;
}) {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const rows = comparisonUsable(change) ? (change?.exchanges ?? []) : [];
  const scope = comparisonScope(change);
  return (
    <Card
      span={span}
      size={size}
      title={`${windowLabel(windowSec)} 거래소 비교`}
      note={`직전 ${windowLabel(windowSec)}보다 거래금액과 점유율이 어떻게 바뀌었나${scope ? ` (${scope})` : ""}`}
    >
      {rows.length === 0 ? (
        <Empty>
          {windowComparisonNote(windowSec, change) || "비교 자료가 없습니다."}
        </Empty>
      ) : (
        <Table
          label="거래소 비교"
          head={
            <tr>
              <th>거래소</th>
              <th className="mg-num">거래금액</th>
              <th className="mg-num">변화</th>
              <th className="mg-num">점유율</th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr
              key={row.exchange}
              className={row.exchange === "bithumb" ? "mg-me" : undefined}
            >
              <td>
                <Ex exchange={row.exchange} />
              </td>
              <td className="mg-num">
                <Won value={row.current_volume_krw} />
              </td>
              <td className="mg-num">
                <Won value={row.volume_change_krw} signed />
              </td>
              <td className="mg-num">
                {formatSharePercent(row.previous_share_pct)} →{" "}
                {formatSharePercent(row.current_share_pct)}
              </td>
              <td className="mg-num">
                <Pp value={row.contribution_pp} />
              </td>
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}

/**
 * Coins named in today's notices and what they contribute now. The notices
 * screen's stream carries no coin contributions, so there the card lists the
 * coins and points to the coins screen.
 */
export function NoticeCoinsCard({
  span = 6,
  size = "m",
  contributions = true,
}: {
  span?: 6 | 12;
  size?: CardSize;
  contributions?: boolean;
}) {
  const analysis = useAnalysis();
  const { items, waiting } = useTodayNotices();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  // Without a usable comparison there is no coin list to look a coin up in.
  const compared = comparisonUsable(change);
  const assets = new Map(
    (compared ? (change?.assets ?? []) : []).map(asset => [asset.asset, asset])
  );
  const named = new Map<
    string,
    { exchanges: Set<string>; latest: number; titles: string[] }
  >();
  for (const item of items) {
    for (const asset of item.assets) {
      const entry = named.get(asset) ?? {
        exchanges: new Set(),
        latest: 0,
        titles: [],
      };
      entry.exchanges.add(item.exchange);
      entry.latest = Math.max(entry.latest, item.published_ts_ms);
      entry.titles.push(item.title);
      named.set(asset, entry);
    }
  }
  const rows = [...named.entries()].sort(
    (left, right) => right[1].latest - left[1].latest
  );
  return (
    <Card
      span={span}
      size={size}
      title="공지가 있는 코인"
      note={
        contributions
          ? `오늘 공지에 나온 코인과 그 코인의 지금 ${windowLabel(windowSec)} 기여`
          : "오늘 공지에 나온 코인입니다. 지금 기여는 코인 화면에서 봅니다."
      }
    >
      {rows.length === 0 ? (
        <Empty>{waiting ?? "오늘 공지에 나온 코인이 없습니다."}</Empty>
      ) : (
        <Table
          label="공지가 있는 코인"
          head={
            <tr>
              <th>코인</th>
              <th>공지</th>
              {contributions ? <th className="mg-num">지금 기여</th> : null}
            </tr>
          }
        >
          {rows.slice(0, 20).map(([asset, entry]) => (
            <tr key={asset}>
              <td className="mg-name">{asset}</td>
              <td>
                <span className="mg-num mg-muted">
                  {formatMarketTimeMS(entry.latest, "time")}
                </span>{" "}
                {[...entry.exchanges].map(exchangeLabel).join("·")} ·{" "}
                {entry.titles[0]}
              </td>
              {contributions ? (
                <td className="mg-num">
                  {assets.has(asset) ? (
                    <Pp value={assets.get(asset)?.contribution_pp} />
                  ) : (
                    <span className="mg-muted">
                      {compared ? "거래 없음" : "—"}
                    </span>
                  )}
                </td>
              ) : null}
            </tr>
          ))}
        </Table>
      )}
      {contributions ? (
        <Foot>
          {compared ? "" : `${windowComparisonNote(windowSec, change)}. `}
          공지와 기여가 같은 시간에 있었다는 뜻일 뿐, 공지가 원인이라는 뜻은
          아닙니다.
        </Foot>
      ) : (
        <Foot>
          <a className="mg-button" href="#coins">
            코인 화면에서 기여 보기
          </a>
        </Foot>
      )}
    </Card>
  );
}
