import { useEffect, useMemo, useRef, useState } from "react";
import { Activity } from "lucide-react";
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceDot,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  usePlotArea,
  useXAxisInverseDataSnapScale,
} from "recharts";

import {
  chartAxisTickStyle,
  chartGridStroke,
  chartTooltipLabelStyle,
  chartTooltipStyle,
} from "@/components/ChartKit";
import { InlineHelp } from "@/components/common/InlineHelp";
import { Panel } from "@/components/common/Panel";
import { ExchangeBrandLabel } from "@/components/ExchangeBrandLabel";
import { exchangeLabel } from "@/lib/exchanges";
import { formatKrwCompact, formatSharePercent } from "@/lib/formatters";
import { formatLiveTradeRate } from "@/lib/live-chart-rate-format";
import { DOMESTIC_EXCHANGES } from "@/lib/market-share-config";
import type { LiveChartHistoryPoint } from "@/lib/live-chart";
import type { LiveChartExplorer } from "@/hooks/useLiveChartExplorer";
import { liveChartTicks, liveChartTime } from "@/lib/live-chart-explorer";
import { FlowChart } from "@/screens/charts";
import {
  buildLiveChartMobileAxes,
  formatLiveChartAxisTick,
} from "@/lib/live-chart-mobile";
import {
  isRelevantDomesticNotice,
  marketShareNoticeItems,
  noticeAnchorTimeMs,
} from "@/lib/market-share-reaction";
import type { MarketShareAnalysis } from "@/lib/market-share-types";

function money(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwCompact(value).replace(/^\+/, "");
}

function percent(value: number | null | undefined, digits = 1): string {
  return formatSharePercent(value, digits);
}

function chartTime(value: number): string {
  return liveChartTime(value);
}

function shareAxisDomain(
  history: LiveChartHistoryPoint[],
  hidden?: ReadonlySet<string>
): [number, number] {
  const values = history
    .flatMap(point =>
      DOMESTIC_EXCHANGES.filter(exchange => !hidden?.has(exchange)).map(
        exchange => point[`share_${exchange}`]
      )
    )
    .filter(
      (value): value is number =>
        typeof value === "number" && Number.isFinite(value)
    );
  if (!values.length) return [0, 100];
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const spread = maximum - minimum;
  const padding = Math.max(0.5, spread * 0.08);
  const step = spread <= 5 ? 1 : spread <= 20 ? 2 : 5;
  const lower = Math.max(0, Math.floor((minimum - padding) / step) * step);
  const upper = Math.min(100, Math.ceil((maximum + padding) / step) * step);
  if (lower < upper) return [lower, upper];
  if (maximum >= 100) return [99, 100];
  return [lower, Math.min(100, lower + 1)];
}

/** Each chart's heading and the sentence that explains it. */
export function liveExchangeChartCopy(metric: "share" | "rate") {
  return metric === "share"
    ? {
        title: "국내 거래소 점유율 실시간 흐름",
        description:
          "국내 5개 거래소의 선택 구간 점유율입니다. Y축은 확인된 최솟값과 최댓값에 맞춰 확대합니다. 전체 구간이 확보되지 않으면 실제로 확인한 실시간 체결만 선에 표시합니다.",
      }
    : {
        title: "국내 거래소별 거래금액 속도",
        description:
          "굵은 선은 선택 구간의 초당 거래금액(왼쪽 축), 얇은 선은 초당 체결 건수(오른쪽 축)입니다. 전체 구간이 확보되지 않으면 실제로 확인한 실시간 체결만 표시합니다.",
      };
}

/** Snap to an actual datum using the chart's public scale, including touch taps. */
function LiveInspectLayer({ explorer }: { explorer: LiveChartExplorer }) {
  const plot = usePlotArea();
  const inverse = useXAxisInverseDataSnapScale();
  const down = useRef<{ x: number; y: number } | null>(null);
  if (!plot || !inverse) return null;
  const timeAt = (clientX: number, element: SVGRectElement) => {
    const svg = element.ownerSVGElement!;
    const box = svg.getBoundingClientRect();
    const scale = svg.viewBox.baseVal.width
      ? svg.viewBox.baseVal.width / box.width
      : 1;
    const at = inverse((clientX - box.left) * scale);
    return typeof at === "number" && Number.isFinite(at) ? at : null;
  };
  return (
    <rect
      className="live-chart-inspect-layer"
      x={plot.x}
      y={plot.y}
      width={plot.width}
      height={plot.height}
      fill="transparent"
      aria-hidden="true"
      onPointerMove={event => {
        if (event.pointerType !== "touch")
          explorer.inspect(timeAt(event.clientX, event.currentTarget));
      }}
      onPointerLeave={() => explorer.inspect(null)}
      onPointerDown={event => {
        down.current = { x: event.clientX, y: event.clientY };
      }}
      onPointerCancel={() => {
        down.current = null;
      }}
      onPointerUp={event => {
        const previous = down.current;
        down.current = null;
        if (
          !previous ||
          Math.hypot(event.clientX - previous.x, event.clientY - previous.y) > 8
        )
          return;
        const at = timeAt(event.clientX, event.currentTarget);
        if (at !== null) explorer.pin(at);
      }}
    />
  );
}

export default function LiveExchangeChart({
  history,
  analysis,
  metric,
}: {
  history: LiveChartHistoryPoint[];
  analysis: MarketShareAnalysis;
  metric: "share" | "rate";
}) {
  const shareChart = metric === "share";
  const { title, description } = liveExchangeChartCopy(metric);
  return (
    <Panel
      title={
        shareChart ? (
          <span className="inline-help-heading">
            {title}
            <InlineHelp text={description} />
          </span>
        ) : (
          title
        )
      }
      description={shareChart ? undefined : description}
      className="live-exchange-chart min-h-[390px]"
      icon={<Activity className="h-4 w-4" />}
    >
      <LiveExchangeChartContent
        history={history}
        notices={analysis.notices}
        metric={metric}
      />
    </Panel>
  );
}

/**
 * The five exchanges' latest values and their lines: shares, or trading
 * value and trade count per second. Also used by the dashboard's live screen.
 */
export function LiveExchangeChartContent({
  history,
  notices,
  metric,
  explorer,
  rateView = "both",
  flowing,
  singleChart = false,
  showValues = true,
}: {
  history: LiveChartHistoryPoint[];
  notices?: MarketShareAnalysis["notices"];
  metric: "share" | "rate";
  explorer?: LiveChartExplorer;
  rateView?: "both" | "volume" | "trades";
  singleChart?: boolean;
  showValues?: boolean;
  flowing?: { running: boolean; sampleIntervalMS?: number; pauseOnHover?: boolean };
}) {
  const shareChart = metric === "share";
  const tradesOnly = !shareChart && rateView === "trades";
  const plottedMetric = tradesOnly ? "trades" : metric;
  const dualAxes = !shareChart && rateView === "both";
  const start = history[0]?.at ?? 0;
  const end = history.at(-1)?.at ?? 0;
  const latest =
    history.find(point => point.at === explorer?.selectedAt) ?? history.at(-1);
  const shareDomain = useMemo(
    () => shareAxisDomain(history, explorer?.hidden),
    [history, explorer?.hidden]
  );
  const plotRef = useRef<HTMLDivElement>(null);
  const [compactAxes, setCompactAxes] = useState(false);
  useEffect(() => {
    const mobileViewport = window.matchMedia("(max-width: 767px)");
    const updateCompactAxes = () => {
      setCompactAxes(
        !!plotRef.current?.closest(".main-dashboard, .mg") &&
          mobileViewport.matches
      );
    };
    updateCompactAxes();
    mobileViewport.addEventListener("change", updateCompactAxes);
    return () =>
      mobileViewport.removeEventListener("change", updateCompactAxes);
  }, []);
  const mobileAxes = useMemo(
    () => buildLiveChartMobileAxes(history),
    [history]
  );
  const axisTickStyle = compactAxes
    ? { ...chartAxisTickStyle, fontSize: 13 }
    : chartAxisTickStyle;
  const markers = shareChart
    ? marketShareNoticeItems(notices)
        .filter(isRelevantDomesticNotice)
        .flatMap(notice => {
          const at = noticeAnchorTimeMs(notice);
          return at !== null && at >= start && at <= end
            ? [{ at, label: `${exchangeLabel(notice.exchange)} 공지` }]
            : [];
        })
        .slice(-3)
    : [];
  return (
    <>
      {showValues && <>
      <div
        className="live-exchange-chart-values grid grid-cols-1 gap-px border-b border-border bg-border sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5"
        data-reading-at={latest?.at}
      >
        {DOMESTIC_EXCHANGES.map(exchange => {
          const value = latest?.[`${plottedMetric}_${exchange}`];
          const trades = latest?.[`trades_${exchange}`];
          const ValueCell = explorer ? "button" : "div";
          return (
            <ValueCell
              key={exchange}
              type={explorer ? "button" : undefined}
              aria-label={
                explorer ? `${exchangeLabel(exchange)} 그래프 표시` : undefined
              }
              aria-pressed={
                explorer ? !explorer.hidden.has(exchange) : undefined
              }
              onClick={
                explorer ? () => explorer.toggleExchange(exchange) : undefined
              }
              data-hidden={explorer?.hidden.has(exchange)}
              data-exchange={exchange}
              className="grid min-w-0 grid-cols-[10px_76px_minmax(0,1fr)] items-center gap-2 bg-card px-4 py-2.5"
            >
              <span
                className="h-2 w-2"
                style={{ backgroundColor: `var(--exchange-${exchange})` }}
              />
              <ExchangeBrandLabel
                exchange={exchange}
                className="truncate text-base text-muted-foreground"
              />
              <strong className="min-w-0 whitespace-nowrap text-right text-base font-semibold tabular-nums">
                {shareChart
                  ? percent(value, 2)
                  : tradesOnly
                    ? formatLiveTradeRate(value)
                    : value === null || value === undefined
                      ? "—"
                      : `${money(value)}/초`}
              </strong>
              {dualAxes ? (
                <span className="col-start-3 whitespace-nowrap text-right text-base tabular-nums text-muted-foreground">
                  {formatLiveTradeRate(trades)}
                </span>
              ) : null}
            </ValueCell>
          );
        })}
        {/* Five exchanges leave one empty cell in 2 and 3 columns; without a
            card-colored filler the grid's border color showed as a block. */}
        <div
          aria-hidden="true"
          className="hidden bg-card sm:block 2xl:hidden"
        />
      </div>
      {explorer ? (
        <div className="mg-chart-value-caption">
          {explorer.selectedAt !== null ? "선택 시각의 값" : "마지막 수신 값"} ·
          거래소를 눌러 {singleChart ? "그래프에서" : "두 그래프에서"} 표시·숨김
        </div>
      ) : null}
      </>}
      {compactAxes && !shareChart ? (
        <div className="flex justify-between gap-2 px-2 pt-3 ui-caption text-muted-foreground">
          {!tradesOnly ? <span>거래금액({mobileAxes.volume.unit})</span> : null}
          {rateView !== "volume" ? (
            <span>체결({mobileAxes.trades.unit})</span>
          ) : null}
        </div>
      ) : null}
      {flowing ? (
        <FlowChart
          label={`${shareChart ? "점유율" : tradesOnly ? "체결 횟수" : "거래금액"} 그래프. 방향키로 시점 탐색, Escape로 최신 흐름 복귀`}
          times={history.map(point => point.at)}
          series={DOMESTIC_EXCHANGES.filter(
            exchange => !explorer?.hidden.has(exchange)
          ).map(exchange => ({
            key: exchange,
            label: exchangeLabel(exchange),
            color: `var(--exchange-${exchange})`,
            values: history.map(point =>
              point.gap ? null : (point[`${plottedMetric}_${exchange}`] ?? null)
            ),
          }))}
          format={value =>
            shareChart
              ? percent(value, 2)
              : tradesOnly
                ? formatLiveTradeRate(value)
                : `${money(value)}/초`
          }
          axisFormat={value =>
            shareChart
              ? percent(value)
              : tradesOnly
                ? formatLiveTradeRate(value, "회")
                : money(value)
          }
          zoom={shareChart}
          running={
            flowing.running && !explorer?.held && (flowing.pauseOnHover === false || explorer?.selectedAt == null)
          }
          sampleIntervalMS={flowing.sampleIntervalMS}
          pauseOnHover={flowing.pauseOnHover}
          spanMs={
            explorer?.held
              ? Math.max(1_000, end - start)
              : (explorer?.durationMS ?? Math.max(120_000, end - start))
          }
          explorer={explorer}
        />
      ) : (
        <div
          ref={plotRef}
          className="live-exchange-chart-plot h-80 p-4"
          role={explorer ? "group" : undefined}
          aria-label={
            explorer
              ? `${shareChart ? "점유율" : tradesOnly ? "체결 횟수" : "거래금액"} 그래프. 방향키로 시점 탐색, Escape로 최신 흐름 복귀`
              : undefined
          }
          tabIndex={explorer ? 0 : undefined}
          onKeyDown={event => {
            if (!explorer) return;
            if (
              !["ArrowLeft", "ArrowRight", "Home", "End", "Escape"].includes(
                event.key
              )
            )
              return;
            event.preventDefault();
            event.stopPropagation();
            if (event.key === "Escape") explorer.follow();
            else if (event.key === "Home" && history[0])
              explorer.pin(history[0].at);
            else if (event.key === "End" && history.at(-1))
              explorer.pin(history.at(-1)!.at);
            else explorer.step(event.key === "ArrowLeft" ? -1 : 1);
          }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart
              accessibilityLayer={!explorer}
              data={history}
              margin={{
                top: 8,
                right: compactAxes ? 4 : shareChart ? 20 : 4,
                left: compactAxes ? 0 : 4,
                bottom: 0,
              }}
            >
              <CartesianGrid stroke={chartGridStroke} strokeDasharray="2 6" />
              <XAxis
                dataKey="at"
                type="number"
                domain={["dataMin", "dataMax"]}
                allowDataOverflow
                tick={axisTickStyle}
                tickFormatter={value => chartTime(Number(value))}
                ticks={liveChartTicks(history, compactAxes ? 3 : 5)}
                minTickGap={compactAxes ? 24 : 48}
                interval={compactAxes ? "preserveStartEnd" : undefined}
                // Room between the time labels and the value labels at both
                // bottom corners.
                tickMargin={8}
                padding={{ left: 12, right: 12 }}
              />
              <YAxis
                yAxisId={
                  shareChart ? "share" : tradesOnly ? "trades" : "volume"
                }
                domain={shareChart ? shareDomain : [0, "auto"]}
                width={compactAxes ? "auto" : 72}
                tick={axisTickStyle}
                tickMargin={compactAxes ? 3 : undefined}
                tickFormatter={value =>
                  shareChart
                    ? `${value}%`
                    : tradesOnly
                      ? compactAxes
                        ? formatLiveChartAxisTick(
                            Number(value),
                            mobileAxes.trades.divisor
                          )
                        : formatLiveTradeRate(Number(value), "회")
                      : compactAxes
                        ? formatLiveChartAxisTick(
                            Number(value),
                            mobileAxes.volume.divisor
                          )
                        : money(Number(value))
                }
              />
              {dualAxes ? (
                <YAxis
                  yAxisId="trades"
                  orientation="right"
                  domain={[0, "auto"]}
                  width={compactAxes ? "auto" : 72}
                  tick={axisTickStyle}
                  tickMargin={compactAxes ? 3 : undefined}
                  tickFormatter={value =>
                    compactAxes
                      ? formatLiveChartAxisTick(
                          Number(value),
                          mobileAxes.trades.divisor
                        )
                      : formatLiveTradeRate(Number(value), "회")
                  }
                />
              ) : null}
              <Tooltip
                content={explorer ? () => null : undefined}
                cursor={explorer ? false : undefined}
                isAnimationActive={false}
                contentStyle={chartTooltipStyle}
                labelStyle={chartTooltipLabelStyle}
                labelFormatter={value => chartTime(Number(value))}
                formatter={(value, name) => [
                  shareChart
                    ? percent(Number(value), 2)
                    : String(name).endsWith("체결/초")
                      ? formatLiveTradeRate(Number(value))
                      : `${money(Number(value))}/초`,
                  name,
                ]}
              />
              {markers.map(marker => (
                <ReferenceLine
                  key={`${marker.at}-${marker.label}`}
                  x={marker.at}
                  stroke="var(--info)"
                  strokeDasharray="3 4"
                />
              ))}
              {explorer?.selectedAt !== null &&
              explorer?.selectedAt !== undefined ? (
                <ReferenceLine
                  x={explorer.selectedAt}
                  yAxisId={
                    shareChart ? "share" : tradesOnly ? "trades" : "volume"
                  }
                  stroke="var(--foreground)"
                  strokeDasharray={explorer.pinned ? undefined : "4 4"}
                />
              ) : null}
              {DOMESTIC_EXCHANGES.filter(
                exchange => !explorer?.hidden.has(exchange)
              ).map(exchange => (
                <Line
                  key={exchange}
                  yAxisId={
                    shareChart ? "share" : tradesOnly ? "trades" : "volume"
                  }
                  type="monotoneX"
                  dataKey={`${plottedMetric}_${exchange}`}
                  name={exchangeLabel(exchange)}
                  stroke={`var(--exchange-${exchange})`}
                  strokeWidth={2}
                  dot={false}
                  activeDot={{ r: 3 }}
                  isAnimationActive={false}
                  connectNulls={false}
                />
              ))}
              {dualAxes
                ? DOMESTIC_EXCHANGES.filter(
                    exchange => !explorer?.hidden.has(exchange)
                  ).map(exchange => (
                    <Line
                      key={`trades-${exchange}`}
                      yAxisId="trades"
                      type="monotoneX"
                      dataKey={`trades_${exchange}`}
                      name={`${exchangeLabel(exchange)} 체결/초`}
                      stroke={`var(--exchange-${exchange})`}
                      strokeWidth={1}
                      strokeOpacity={0.6}
                      dot={false}
                      activeDot={{ r: 2 }}
                      isAnimationActive={false}
                      connectNulls={false}
                    />
                  ))
                : null}
              {explorer?.selectedAt != null
                ? DOMESTIC_EXCHANGES.filter(
                    exchange => !explorer.hidden.has(exchange)
                  ).flatMap(exchange => {
                    const value = latest?.[`${plottedMetric}_${exchange}`];
                    return typeof value === "number" && Number.isFinite(value)
                      ? [
                          <ReferenceDot
                            key={`selected-${exchange}`}
                            x={explorer.selectedAt!}
                            y={value}
                            yAxisId={
                              shareChart
                                ? "share"
                                : tradesOnly
                                  ? "trades"
                                  : "volume"
                            }
                            r={4}
                            fill={`var(--exchange-${exchange})`}
                            stroke="var(--card)"
                          />,
                        ]
                      : [];
                  })
                : null}
              {explorer ? <LiveInspectLayer explorer={explorer} /> : null}
            </LineChart>
          </ResponsiveContainer>
        </div>
      )}
    </>
  );
}
