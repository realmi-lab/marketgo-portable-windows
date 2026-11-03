import { Activity, Info } from "lucide-react";
import {
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import { Panel } from "@/components/common/Panel";
import { exchangeLabel } from "@/lib/exchanges";
import type { MarketShareRolling24h } from "@/lib/market-share-types";

const NOTICE_EXCHANGES = [
  { id: "bithumb", label: "빗썸" },
  { id: "upbit", label: "업비트" },
  { id: "coinone", label: "코인원" },
  { id: "korbit", label: "코빗" },
  { id: "gopax", label: "고팍스" },
] as const;

function finite(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function percent(value: number | null | undefined, digits = 2): string {
  return finite(value) ? `${value.toFixed(digits)}%` : "—";
}

function timestamp(value: number | null | undefined): string {
  if (!finite(value) || value <= 0) return "시각 정보 없음";
  return new Date(value).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function noticeExchangeLabel(exchange: string): string {
  return (
    NOTICE_EXCHANGES.find(item => item.id === exchange)?.label ??
    exchangeLabel(exchange)
  );
}

export default function RollingTrendChart({
  monitor,
}: {
  monitor: MarketShareRolling24h;
}) {
  const series = monitor.trend.map(point => ({
    ...point,
    label: timestamp(point.as_of_ms),
  }));
  const hasRollingSeries =
    series.filter(point => finite(point.rolling_24h_share_pct)).length >= 2;
  const hasFlowSeries = (
    ["recent_flow_share_pct", "expiring_flow_share_pct"] as const
  ).some(key => series.filter(point => finite(point[key])).length >= 2);
  const hasTrendLine = hasRollingSeries || hasFlowSeries;
  const trendStart = series[0]?.as_of_ms;
  const trendEnd = series[series.length - 1]?.as_of_ms;
  const movementStart = monitor.movement.started_at_ms;
  const noticeMarkers = monitor.evidence.domestic_notices
    .filter(
      notice =>
        notice.related &&
        notice.anchor_ts_ms &&
        (!trendStart || notice.anchor_ts_ms >= trendStart) &&
        (!trendEnd || notice.anchor_ts_ms <= trendEnd)
    )
    .slice(0, 4);
  const tooltipLabels: Record<string, string> = {
    rolling_24h_share_pct: "24시간 점유율",
    recent_flow_share_pct: "최근 10분 신규 거래 몫",
    expiring_flow_share_pct: "24시간 범위에서 빠진 거래 몫",
  };
  const trendDescription = hasRollingSeries
    ? "최근 24시간 점유율과 최근 10분에 들어오고 빠진 거래 몫을 함께 표시합니다. 자료가 없는 시간은 선으로 연결하지 않습니다."
    : hasFlowSeries
      ? "현재 24시간 비교 자료가 부족해 들어오고 빠진 거래 흐름만 표시합니다. 자료가 없는 시간은 선으로 연결하지 않습니다."
      : "현재 점유율은 관측됐지만, 비교 가능한 추이 값이 두 시점 이상 확보되기 전에는 차트를 그리지 않습니다.";

  return (
    <Panel
      title="24시간 점유율 추이"
      description={trendDescription}
      descriptionRole={hasRollingSeries ? "help" : "status"}
      icon={<Activity className="h-4 w-4" />}
      actions={
        <span className="text-xs text-muted-foreground">
          현재 {percent(monitor.rolling_24h.current_share_pct)}
        </span>
      }
    >
      {series.length >= 2 && hasTrendLine ? (
        <div className="h-72 px-2 py-4">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart
              accessibilityLayer
              data={series}
              margin={{ top: 8, right: 20, left: 4, bottom: 4 }}
            >
              <XAxis
                dataKey="as_of_ms"
                tickFormatter={(value: number) => timestamp(value).slice(-8)}
                minTickGap={42}
              />
              <YAxis
                domain={["auto", "auto"]}
                tickFormatter={(value: number) => `${value.toFixed(1)}%`}
                width={64}
              />
              <Tooltip
                labelFormatter={value => timestamp(Number(value))}
                formatter={(value, name) => [
                  percent(Number(value)),
                  tooltipLabels[String(name)] ?? String(name),
                ]}
              />
              {movementStart &&
                (!trendStart || movementStart >= trendStart) &&
                (!trendEnd || movementStart <= trendEnd) && (
                  <ReferenceLine
                    x={movementStart}
                    stroke="var(--loss)"
                    strokeDasharray="4 4"
                    label={{
                      value: "변화 구간 시작",
                      position: "insideTop",
                      fill: "var(--loss)",
                      fontSize: 16,
                    }}
                  />
                )}
              {noticeMarkers.map(notice => (
                <ReferenceLine
                  key={`${notice.exchange}-${notice.notice_id}`}
                  x={notice.anchor_ts_ms ?? undefined}
                  stroke="var(--info)"
                  strokeDasharray="2 5"
                  label={{
                    value: noticeExchangeLabel(notice.exchange),
                    position: "insideBottom",
                    fill: "var(--info)",
                    fontSize: 16,
                  }}
                />
              ))}
              <Line
                type="monotone"
                dataKey="rolling_24h_share_pct"
                stroke="var(--exchange-bithumb)"
                strokeWidth={2}
                dot={false}
                connectNulls={false}
              />
              <Line
                type="monotone"
                dataKey="recent_flow_share_pct"
                stroke="var(--info)"
                strokeWidth={1.5}
                strokeDasharray="5 4"
                dot={false}
                connectNulls={false}
              />
              <Line
                type="monotone"
                dataKey="expiring_flow_share_pct"
                stroke="var(--loss)"
                strokeWidth={1.5}
                strokeDasharray="2 4"
                dot={false}
                connectNulls={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : (
        <div className="flex min-h-56 items-center justify-center px-5 text-center text-xs text-muted-foreground">
          {series.length === 1
            ? "추이 기준을 축적하는 중입니다."
            : series.length >= 2
              ? "현재 점유율은 관측됐지만, 비교 가능한 추이 값이 두 시점 이상 확보되지 않았습니다."
              : "24시간 점유율 추이 이력이 아직 없습니다."}
        </div>
      )}
      <div className="flex flex-wrap gap-4 border-t border-border px-4 py-3 text-base text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <span className="h-2 w-6 bg-gold" />
          24시간 누적 점유율
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-0.5 w-6 border-t border-dashed border-info" />
          최근 10분 신규 몫
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span className="h-0.5 w-6 border-t border-dashed border-loss" />
          24시간 전 만료 몫
        </span>
        <span className="inline-flex items-center gap-1.5">
          <Info className="h-3.5 w-3.5" />
          점선은 누적 점유율이 아닌 교체 흐름입니다.
        </span>
      </div>
    </Panel>
  );
}
