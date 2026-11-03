import {
  Area,
  AreaChart,
  CartesianGrid,
  LabelList,
  Line,
  ReferenceDot,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { AnimatedMetricValue } from "@/components/common/AnimatedMetricValue";
import type { MarketShareOfficialTicker1mState } from "@/lib/market-share-types";
import {
  buildOfficialHourlyChartSeries,
  OFFICIAL_HOURLY_CHART_POINT_COUNT,
  officialHourlySeriesCoverageLabel,
} from "@/lib/official-hourly-series";
import { buildShareYAxis } from "@/lib/share-chart-axis";
import {
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";

function finite(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function percent(value: number | null | undefined, digits = 2): string {
  return formatSharePercent(value, digits);
}

function timestamp(value: number | null | undefined): string {
  if (!finite(value) || value <= 0) return "시각 정보 없음";
  return new Date(value).toLocaleString("ko-KR", {
    timeZone: "Asia/Seoul",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function hourLabel(value: number): string {
  if (!finite(value) || value <= 0) return "—";
  return new Intl.DateTimeFormat("ko-KR", {
    timeZone: "Asia/Seoul",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    hour12: false,
  }).format(new Date(value));
}

function changeTone(value: number | null | undefined): string {
  if (!finite(value) || value === 0) return "text-muted-foreground";
  return value > 0 ? "text-profit" : "text-loss";
}

// Values for the latest, selected, highest and lowest hours only. A label on
// each of the 30 points overlapped its neighbours, and every hour's value is
// in the selector under the chart.
function SharePointValueLabel({
  x,
  y,
  value,
  index,
  labeled,
  lastIndex,
}: {
  x?: number | string;
  y?: number | string;
  value?: number | string;
  index?: number;
  labeled: ReadonlySet<number>;
  lastIndex: number;
}) {
  const pointX = Number(x);
  const pointY = Number(y);
  const share = Number(value);
  if (index === undefined || !labeled.has(index)) return null;
  if (![pointX, pointY, share].every(Number.isFinite)) return null;
  return (
    <text
      className="official-share-point-label"
      x={pointX}
      y={pointY - 12}
      fill="var(--foreground)"
      fontSize={13}
      fontWeight={600}
      textAnchor={
        index === lastIndex ? "end" : index === 0 ? "start" : "middle"
      }
    >
      {percent(share)}
    </text>
  );
}

// Horizontal hour labels spaced by the chart; the first and last are anchored
// inside the plot so they are not cut off at the edges.
function HourTick({
  x,
  y,
  payload,
  index,
  visibleTicksCount,
}: {
  x?: number | string;
  y?: number | string;
  payload?: { value?: number };
  index?: number;
  visibleTicksCount?: number;
}) {
  const last = index !== undefined && index === (visibleTicksCount ?? 0) - 1;
  return (
    <text
      className="recharts-cartesian-axis-tick-value"
      x={Number(x)}
      y={Number(y) + 14}
      textAnchor={index === 0 ? "start" : last ? "end" : "middle"}
      fill="var(--muted-foreground)"
      fontSize={13}
    >
      {hourLabel(Number(payload?.value))}
    </text>
  );
}

function keyShareIndices(
  points: ReadonlyArray<{ share: number | null; available: boolean }>,
  selectedIndex: number
): ReadonlySet<number> {
  const available = points
    .map((point, index) => ({ share: point.share, index, ok: point.available }))
    .filter(
      (point): point is { share: number; index: number; ok: boolean } =>
        point.ok && finite(point.share)
    );
  if (!available.length) return new Set();
  const byShare = [...available].sort((a, b) => a.share - b.share);
  const labeled = new Set([
    available[available.length - 1].index,
    byShare[0].index,
    byShare[byShare.length - 1].index,
  ]);
  if (selectedIndex >= 0) labeled.add(selectedIndex);
  return labeled;
}

export default function OfficialTrendCharts({
  trend,
  selectedEndMS,
  selectedShareDeltaPP,
  onSelectEndMS,
}: {
  trend: MarketShareOfficialTicker1mState;
  selectedEndMS: number;
  selectedShareDeltaPP: number | null;
  onSelectEndMS: (value: number) => void;
}) {
  const chartData = buildOfficialHourlyChartSeries(
    trend.trend ?? [],
    trend.interval_sec || 3_600,
    OFFICIAL_HOURLY_CHART_POINT_COUNT
  );
  const availableChartData = chartData.filter(point => point.available);
  const missingPointCount = chartData.length - availableChartData.length;
  const shareAxis = buildShareYAxis(chartData.map(point => point.share));
  const selectedIndex = chartData.findIndex(
    point => point.bucket_ms === selectedEndMS
  );
  const selectedPoint = chartData[selectedIndex] ?? null;
  const coverageLabel = officialHourlySeriesCoverageLabel(chartData);
  const labeledPoints = keyShareIndices(chartData, selectedIndex);

  return (
    <div className="official-trend-primary">
      <div className="official-trend-chart-card">
        <div className="official-trend-chart-heading">
          <div>
            <p className="official-trend-eyebrow">
              시간별 공식 24시간 점유율 · 시간을 선택하세요
            </p>
            <div className="official-trend-current-row">
              <strong>
                <AnimatedMetricValue
                  resetKey={selectedEndMS}
                  value={selectedPoint?.share}
                  preserveRawValue
                  format={value => percent(value)}
                  width="compact"
                />
              </strong>
              <span className={changeTone(selectedShareDeltaPP)}>
                <AnimatedMetricValue
                  resetKey={selectedEndMS}
                  value={selectedShareDeltaPP}
                  preserveRawValue
                  format={value => formatSignedPercentagePoints(value, 2)}
                  width="compact"
                />
              </span>
            </div>
          </div>
          <span className="official-trend-sample-count">
            {coverageLabel}
            {missingPointCount > 0
              ? ` · 누락 ${missingPointCount.toLocaleString("ko-KR")}개는 선을 끊어 표시`
              : " · 누락 없음"}
          </span>
        </div>
        {chartData.length >= 2 ? (
          <div
            className="official-trend-chart h-64 overflow-hidden"
            aria-label={`${coverageLabel} · 시간별 공식 점유율 차트`}
          >
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart
                accessibilityLayer
                data={chartData}
                margin={{ top: 34, right: 20, left: 0, bottom: 0 }}
                onClick={(state: unknown) => {
                  const active = state as {
                    activePayload?: Array<{
                      payload?: { bucket_ms?: number };
                    }>;
                  };
                  const bucketMS =
                    active.activePayload?.[0]?.payload?.bucket_ms;
                  if (finite(bucketMS)) onSelectEndMS(bucketMS);
                }}
              >
                <CartesianGrid
                  vertical={false}
                  stroke="rgb(60 60 67 / 10%)"
                  strokeDasharray="2 5"
                />
                <XAxis
                  dataKey="observed_at_ms"
                  tick={<HourTick />}
                  interval="preserveStartEnd"
                  minTickGap={28}
                  height={30}
                  axisLine={false}
                  tickLine={false}
                />
                <YAxis
                  domain={shareAxis.domain}
                  ticks={shareAxis.ticks}
                  tickFormatter={(value: number) => `${value.toFixed(1)}%`}
                  width={64}
                  axisLine={false}
                  tickLine={false}
                />
                <Tooltip
                  labelFormatter={value => timestamp(Number(value))}
                  formatter={value => [percent(Number(value)), "빗썸 점유율"]}
                  contentStyle={{
                    border: "1px solid rgb(60 60 67 / 14%)",
                    borderRadius: "12px",
                    boxShadow: "0 10px 30px rgb(0 0 0 / 10%)",
                  }}
                />
                {finite(trend.current?.bithumb_share_pct) && (
                  <>
                    <ReferenceLine
                      y={trend.current.bithumb_share_pct}
                      stroke="var(--exchange-bithumb)"
                      strokeOpacity={0.32}
                      strokeDasharray="4 5"
                    />
                    <ReferenceDot
                      x={trend.current.observed_at_ms}
                      y={trend.current.bithumb_share_pct}
                      r={4}
                      fill="var(--exchange-bithumb)"
                      stroke="white"
                      strokeWidth={2}
                    />
                  </>
                )}
                {selectedPoint && finite(selectedPoint.share) && (
                  <>
                    <ReferenceLine
                      x={selectedPoint.observed_at_ms}
                      stroke="var(--primary)"
                      strokeOpacity={0.68}
                      strokeDasharray="3 4"
                    />
                    <ReferenceDot
                      x={selectedPoint.observed_at_ms}
                      y={selectedPoint.share}
                      r={6}
                      fill="var(--primary)"
                      stroke="var(--card)"
                      strokeWidth={3}
                    />
                  </>
                )}
                <Area
                  type="monotone"
                  dataKey="share"
                  isAnimationActive={false}
                  stroke="var(--exchange-bithumb)"
                  strokeWidth={2.5}
                  fill="var(--exchange-bithumb)"
                  fillOpacity={0.08}
                  dot={{
                    r: 2.75,
                    fill: "var(--exchange-bithumb)",
                    stroke: "white",
                    strokeWidth: 1,
                  }}
                  activeDot={{
                    r: 4,
                    fill: "var(--exchange-bithumb)",
                    stroke: "white",
                    strokeWidth: 2,
                  }}
                  connectNulls={false}
                  style={{ cursor: "pointer" }}
                />
                <Line
                  type="monotone"
                  dataKey="share"
                  stroke="transparent"
                  dot={false}
                  activeDot={false}
                  isAnimationActive={false}
                  legendType="none"
                  tooltipType="none"
                >
                  <LabelList
                    dataKey="share"
                    content={
                      <SharePointValueLabel
                        labeled={labeledPoints}
                        lastIndex={chartData.length - 1}
                      />
                    }
                  />
                </Line>
              </AreaChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="flex h-56 items-center justify-center text-center text-xs text-muted-foreground">
            같은 종료 시각으로 맞춘 1시간봉 이력을 축적하고 있습니다.
          </div>
        )}
        {chartData.length > 0 && (
          <div
            className="official-hour-selector"
            aria-label="기여 코인을 볼 시간 선택"
          >
            {availableChartData.map(point => (
              <button
                key={point.bucket_ms}
                type="button"
                aria-pressed={point.bucket_ms === selectedEndMS}
                onClick={() => onSelectEndMS(point.bucket_ms)}
              >
                <span>{hourLabel(point.observed_at_ms)}</span>
                <strong>{percent(point.share)}</strong>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
