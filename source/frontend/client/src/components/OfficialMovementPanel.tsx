import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  Activity,
  ArrowDownRight,
  ArrowUpRight,
  BarChart3,
  CheckCircle2,
  Clock3,
  Info,
  Radio,
  TrendingUp,
} from "lucide-react";

import { AnimatedMetricValue } from "@/components/common/AnimatedMetricValue";
import { InlineHelp } from "@/components/common/InlineHelp";
import { KpiCard } from "@/components/common/KpiCard";
import { exchangeLabel, exchangeNameKo } from "@/lib/exchanges";
import { selectOfficialContributions } from "@/lib/official-contribution-selection";
import { formatOfficialHourTimestamp as timestamp } from "@/lib/official-hour-metric-format";
import {
  adaptivePercentDigits,
  formatKrwCompact,
  formatPercentagePointMagnitude,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { withParticle } from "@/lib/korean-particle";
import {
  OFFICIAL_CHANGE_CARD_GROUPS,
  type OfficialSummaryCardID,
} from "@/lib/official-summary-layout";
import type {
  MarketShareOfficialDomestic24h,
  MarketShareOfficialMovementEvent,
  MarketShareOfficialMovementState,
  MarketShareOfficialTicker1mState,
} from "@/lib/market-share-types";

function finite(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function percent(value: number | null | undefined, digits = 2): string {
  return formatSharePercent(value, digits);
}

function points(value: number | null | undefined, digits = 2): string {
  if (!finite(value)) return "—";
  return formatSignedPercentagePoints(value, digits);
}

function money(value: number | null | undefined): string {
  return finite(value) ? formatKrwCompact(value) : "—";
}

function moneyHundredMillion(value: number | null | undefined): string {
  if (!finite(value)) return "—";
  if (value === 0) return "0억";
  const rounded = Math.round(Math.abs(value) / 100_000_000);
  const sign = value < 0 ? "-" : "";
  return rounded > 0
    ? `${sign}${rounded.toLocaleString("ko-KR")}억`
    : `${sign}1억 미만`;
}

function eventLabel(event: MarketShareOfficialMovementEvent): string {
  return event.direction === "UP" ? "상승" : "하락";
}

function changeTone(value: number | null | undefined): string {
  if (!finite(value) || value === 0) return "text-muted-foreground";
  return value > 0 ? "text-profit" : "text-loss";
}

function relationLabel(value: string): string {
  if (value === "SCHEDULE_OVERLAP") return "일정 겹침";
  if (value === "PUBLISHED_NEAR") return "게시 시각 인접";
  if (value === "ASSET_CONTEXT") return "주요 코인 일치";
  return "시간상 연관";
}

function checkpointLabel(value: string): string {
  if (value === "CONFIRMED") return "변화 확인";
  if (value === "EXTREME") return "최대 변화";
  if (value === "ENDED") return "변화 종료";
  return value;
}

function uniqueEvents(
  monitor: MarketShareOfficialMovementState
): MarketShareOfficialMovementEvent[] {
  const result = new Map<string, MarketShareOfficialMovementEvent>();
  if (monitor.movement) result.set(monitor.movement.event_id, monitor.movement);
  for (const event of monitor.recent_events ?? []) {
    if (!result.has(event.event_id)) result.set(event.event_id, event);
  }
  return [...result.values()];
}

export function OfficialMovementPanel({
  monitor,
  official,
  trend,
}: {
  monitor: MarketShareOfficialMovementState;
  official: MarketShareOfficialDomestic24h;
  trend: MarketShareOfficialTicker1mState | null;
}) {
  const events = useMemo(() => uniqueEvents(monitor), [monitor]);
  const [selectedID, setSelectedID] = useState("");

  useEffect(() => {
    setSelectedID(current =>
      current && events.some(event => event.event_id === current) ? current : ""
    );
  }, [events]);

  const { selected, drivers, exchangeRows } = selectOfficialContributions(
    events,
    selectedID,
    trend
  );
  const active = monitor.movement?.state === "ACTIVE";
  const officialReady =
    official.status === "LIVE" &&
    official.ready_exchange_count === official.expected_exchange_count;
  const alignedReady = Boolean(
    trend?.status === "LIVE" && trend.previous && trend.current
  );
  const threshold = monitor.data_quality.event_threshold_pp;
  const thresholdLabel = Number.isFinite(threshold)
    ? `±${threshold!.toFixed(2)}%p`
    : "기준 확인 중";
  const strongestAsset = trend?.drivers[0] ?? selected?.drivers[0];
  const bithumb = official.exchanges.bithumb;
  const includedExchanges =
    official.included_exchanges ?? Object.keys(official.exchanges);
  const includedLabel = includedExchanges.map(exchangeNameKo).join("·");
  const alignedExchangeIDs = Object.keys(monitor.current?.exchanges ?? {});
  const alignedExchangeCount = alignedExchangeIDs.length || 3;
  const alignedExchangeLabel = alignedExchangeIDs.length
    ? alignedExchangeIDs.map(exchangeNameKo).join("·")
    : "업비트·빗썸·코인원";
  const officialStatusLabel =
    official.status === "LIVE"
      ? "공식 자료 정상"
      : official.status === "STALE"
        ? "공식 자료 갱신 지연"
        : "공식 자료 일부 대기 중";
  const officialBithumbDetail =
    officialReady && bithumb
      ? `빗썸 공식 최근 24시간 자료 · ${bithumb.market_count.toLocaleString("ko-KR")}개 거래쌍 · ${timestamp(bithumb.exchange_timestamp_ms)}`
      : "빗썸 공식 자료 수신 대기";
  const alignedPreviousShare = trend?.previous?.bithumb_share_pct;
  const alignedCurrentShare =
    trend?.current?.bithumb_share_pct ?? monitor.current?.bithumb_share_pct;
  const alignedDelta = trend?.share_delta_pp;
  const flowEvidence = selected?.flow_evidence ?? [];
  const summaryCards: Record<OfficialSummaryCardID, ReactNode> = {
    share: (
      <KpiCard
        label="빗썸 점유율 · 공식 24시간"
        icon={<BarChart3 className="h-4 w-4" />}
        value={
          <AnimatedMetricValue
            value={officialReady ? official.bithumb_share_pct : null}
            preserveRawValue
            format={value => percent(value)}
            width="compact"
          />
        }
        tone={officialReady ? "neutral" : "muted"}
        sub={`국내 ${official.expected_exchange_count}곳 거래소별 공식 자료`}
        className="official-summary-card official-summary-card-share"
      />
    ),
    "bithumb-volume": (
      <KpiCard
        label="빗썸 거래금액 · 공식 24시간"
        icon={<TrendingUp className="h-4 w-4" />}
        value={
          <AnimatedMetricValue
            value={officialReady ? bithumb?.volume_krw : null}
            digits={0}
            format={moneyHundredMillion}
            width="wide"
          />
        }
        tone={officialReady ? "neutral" : "muted"}
        sub={officialBithumbDetail}
        className="official-summary-card official-summary-card-bithumb-volume"
      />
    ),
    "domestic-volume": (
      <KpiCard
        label={`국내 ${official.expected_exchange_count}곳 거래금액 · 공식 24시간`}
        icon={<Activity className="h-4 w-4" />}
        value={
          <AnimatedMetricValue
            value={officialReady ? official.total_volume_krw : null}
            digits={0}
            format={moneyHundredMillion}
            width="wide"
          />
        }
        tone={officialReady ? "neutral" : "muted"}
        sub={
          officialReady
            ? `${includedLabel} 원화 마켓 합계`
            : `공식 24시간 자료 ${official.ready_exchange_count}/${official.expected_exchange_count}곳 확보`
        }
        className="official-summary-card official-summary-card-domestic-volume"
      />
    ),
    collection: (
      <KpiCard
        label="공식 자료 수신"
        icon={<Radio className="h-4 w-4" />}
        value={
          <span className="text-lg">
            {official.ready_exchange_count}/{official.expected_exchange_count}곳
          </span>
        }
        tone={officialReady ? "profit" : "degraded"}
        sub={officialStatusLabel}
        className="official-summary-card official-summary-card-collection"
      />
    ),
    event: (
      <KpiCard
        label={`1시간 변화 · 같은 시각 ${alignedExchangeCount}곳`}
        icon={<Clock3 className="h-4 w-4" />}
        value={
          <AnimatedMetricValue
            value={alignedReady ? alignedDelta : null}
            format={value => points(value)}
            width="compact"
          />
        }
        tone={
          !finite(alignedDelta)
            ? "muted"
            : alignedDelta >= 0
              ? "profit"
              : "loss"
        }
        sub={
          alignedReady
            ? `${percent(alignedPreviousShare)} → ${percent(alignedCurrentShare)} · 공식 1시간봉 24개 합계`
            : "같은 종료 시각의 공식 자료 대기"
        }
        className="official-summary-card official-summary-card-event"
      />
    ),
    asset: (
      <KpiCard
        label="1시간 변화에 가장 크게 기여한 코인"
        icon={
          strongestAsset?.contribution_pp &&
          strongestAsset.contribution_pp < 0 ? (
            <ArrowDownRight className="h-4 w-4" />
          ) : (
            <ArrowUpRight className="h-4 w-4" />
          )
        }
        value={<span className="text-lg">{strongestAsset?.asset ?? "—"}</span>}
        tone={
          !strongestAsset
            ? "muted"
            : strongestAsset.contribution_pp >= 0
              ? "profit"
              : "loss"
        }
        sub={
          strongestAsset
            ? `${points(strongestAsset.contribution_pp)} · 빗썸 ${money(strongestAsset.bithumb_volume_change_krw)} · 경쟁 ${money(strongestAsset.competitor_volume_change_krw)}`
            : "코인별 1시간봉 비교 자료 대기"
        }
        className="official-summary-card official-summary-card-asset"
      />
    ),
    evidence: (
      <KpiCard
        label="1시간 비교 자료"
        icon={<CheckCircle2 className="h-4 w-4" />}
        value={
          <span className="text-lg">
            {alignedReady ? `${alignedExchangeCount}곳 확인` : "비교 대기"}
          </span>
        }
        tone={alignedReady ? "profit" : "degraded"}
        sub={`${alignedExchangeLabel} · 같은 UTC 종료 시각`}
        className="official-summary-card official-summary-card-evidence"
      />
    ),
  };
  // The verb carries the direction, so the amount is written without a sign.
  const alignedChangeSentence = finite(alignedDelta)
    ? alignedDelta === 0
      ? "변화가 없습니다"
      : `${formatPercentagePointMagnitude(alignedDelta, adaptivePercentDigits(alignedDelta))} ${alignedDelta > 0 ? "올랐습니다" : "내렸습니다"}`
    : "비교 중입니다";
  // The header badge and event chip report the running movement; say it in
  // the conclusion too, or "1시간 -0.10%p" reads as contradicting "-6.27%p".
  const movement = active ? monitor.movement : null;
  const movementSentence = movement ? (
    <>
      {" "}
      {timestamp(movement.started_at_ms)} 이후로는{" "}
      {percent(movement.baseline_share_pct)} →{" "}
      {percent(movement.current_share_pct)}로{" "}
      <span className={changeTone(movement.change_pp)}>
        {formatPercentagePointMagnitude(
          movement.change_pp,
          adaptivePercentDigits(movement.change_pp)
        )}{" "}
        {movement.change_pp > 0 ? "올라" : "내려"}
      </span>{" "}
      큰 변화 기준을 넘은 {eventLabel(movement)}이 이어지고 있습니다.
    </>
  ) : null;
  const statement =
    officialReady && alignedReady ? (
      <>
        국내 {official.expected_exchange_count}곳의 현재 공식 24시간 자료 기준
        빗썸 점유율은 <strong>{percent(official.bithumb_share_pct)}</strong>
        입니다. 같은 종료 시각의 {alignedExchangeCount}곳 비교에서는{" "}
        <strong>{percent(alignedCurrentShare)}</strong>이고, 1시간 전보다{" "}
        <span className={changeTone(alignedDelta)}>
          {alignedChangeSentence}
        </span>
        {strongestAsset ? (
          <>
            . {withParticle(strongestAsset.asset, "이/가")}{" "}
            <span className={changeTone(strongestAsset.contribution_pp)}>
              {points(strongestAsset.contribution_pp)}
            </span>
            로 가장 크게 기여했습니다
          </>
        ) : null}
        .{movementSentence}
      </>
    ) : officialReady ? (
      `국내 ${official.expected_exchange_count}곳의 현재 공식 24시간 자료 기준 빗썸 점유율은 ${percent(official.bithumb_share_pct)}입니다. 같은 시각 비교 자료를 준비하고 있습니다.`
    ) : (
      `국내 ${official.expected_exchange_count}곳의 공식 24시간 자료가 모두 준비되면 현재값을 표시합니다.`
    );
  const renderCards = (cards: readonly OfficialSummaryCardID[]) =>
    cards.map(card => (
      <div className="official-summary-card-slot" key={card}>
        {summaryCards[card]}
      </div>
    ));

  return (
    <section className="glass-card instrument-panel official-stocks-panel overflow-hidden">
      <div className="instrument-panel-header flex flex-wrap items-start justify-between gap-4 border-b border-border px-5 py-4">
        <div>
          <h2 className="inline-help-heading text-base font-semibold tracking-tight">
            <span className="panel-title-icon" aria-hidden="true">
              <BarChart3 className="h-4 w-4" />
            </span>
            공식 24시간 변화 요약
            <InlineHelp text="현재 수준은 국내 거래소의 공식 24시간 자료로, 시간 변화는 같은 종료 시각의 완료된 1시간 자료로 계산합니다." />
          </h2>
        </div>
        <span
          className={`inline-flex items-center gap-1.5 border px-2.5 py-1 text-xs ${
            active
              ? "border-gold/40 bg-gold/10 text-gold"
              : officialReady
                ? "border-profit/30 bg-profit/10 text-profit"
                : "border-loss/30 bg-loss/10 text-loss"
          }`}
        >
          <Activity className="h-3.5 w-3.5" />
          {active
            ? `기준 초과 ${eventLabel(monitor.movement!)} 진행 중`
            : officialReady && alignedReady
              ? "공식 자료 최신"
              : "공식 자료 일부 대기 중"}
        </span>
      </div>

      <div className="official-change-overview border-b border-border">
        <div className="official-change-answer">
          <p className="official-summary-copy">{statement}</p>
          <p className="official-summary-caption">
            현재값은 국내 {official.expected_exchange_count}곳 24시간, 1시간
            변화는 {alignedExchangeLabel} 기준이라 두 점유율이 다릅니다.{" "}
            {Number.isFinite(threshold)
              ? `${withParticle(thresholdLabel, "을/를")} 넘는 변화를 큰 변화로 봅니다.`
              : "큰 변화 기준을 확인하고 있습니다."}
          </p>
        </div>
        <div className="official-change-primary-metrics">
          {renderCards(OFFICIAL_CHANGE_CARD_GROUPS.focus)}
        </div>
      </div>

      <div className="official-change-details">
        <div className="official-change-detail-metrics">
          {renderCards(OFFICIAL_CHANGE_CARD_GROUPS.details)}
        </div>

        {events.length > 0 && (
          <div className="official-event-selector-shell border-b border-border px-4 py-3">
            <div
              className="flex flex-wrap items-center gap-2"
              aria-label="공식 점유율 이벤트 선택"
            >
              <button
                type="button"
                aria-pressed={!selected}
                onClick={() => setSelectedID("")}
                className={`official-event-chip border px-2.5 py-1.5 text-xs transition-colors ${
                  !selected
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border text-muted-foreground hover:text-foreground"
                }`}
              >
                최신 1시간 비교
              </button>
              {events.slice(0, 10).map(event => (
                <button
                  key={event.event_id}
                  type="button"
                  aria-pressed={event.event_id === selected?.event_id}
                  onClick={() => setSelectedID(event.event_id)}
                  data-direction={event.direction.toLowerCase()}
                  className={`official-event-chip border px-2.5 py-1.5 text-xs transition-colors ${
                    event.event_id === selected?.event_id
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-border text-muted-foreground hover:text-foreground"
                  }`}
                >
                  {event.state === "ACTIVE"
                    ? "진행 중"
                    : timestamp(event.ended_at_ms)}{" "}
                  · {eventLabel(event)} {points(event.extreme_change_pp)}
                </button>
              ))}
            </div>
          </div>
        )}

        <div
          className="grid gap-px bg-border xl:grid-cols-2"
          data-testid="official-contributions"
        >
          <div className="bg-card p-5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <h3 className="text-sm font-semibold">코인별 기여</h3>
              <span className="ui-caption text-muted-foreground">
                {selected
                  ? `선택 이벤트 · 기준 ${timestamp(selected.started_at_ms)} · 확인 ${timestamp(selected.confirmed_at_ms)}`
                  : `최신 1시간 비교 · ${timestamp(trend?.previous?.bucket_ms)} → ${timestamp(trend?.current?.bucket_ms)}`}
              </span>
            </div>
            <div className="mt-3 divide-y divide-border/70 border-y border-border/70">
              {drivers.slice(0, 6).map(driver => (
                <div
                  key={driver.asset}
                  className="grid grid-cols-[64px_82px_1fr] items-center gap-3 py-2 text-xs"
                >
                  <strong>{driver.asset}</strong>
                  <span
                    className={
                      driver.contribution_pp >= 0 ? "text-profit" : "text-loss"
                    }
                  >
                    {points(driver.contribution_pp)}
                  </span>
                  {/* Each amount stays on one line; a narrow row wraps
                      between them, not inside "경쟁 ₩0". */}
                  <span className="text-right text-muted-foreground">
                    <span className="whitespace-nowrap">
                      빗썸 {money(driver.bithumb_volume_change_krw)}
                    </span>{" "}
                    {/* The separator wraps with the second amount, never alone. */}
                    <span className="whitespace-nowrap">
                      · 경쟁 {money(driver.competitor_volume_change_krw)}
                    </span>
                  </span>
                </div>
              ))}
              {!drivers.length && (
                <p className="py-3 text-xs text-muted-foreground">
                  비교에 필요한 코인별 자료가 아직 확보되지 않았습니다.
                </p>
              )}
            </div>
          </div>

          <div className="bg-card p-5">
            <h3 className="text-sm font-semibold">
              거래소별 기여 · 같은 시각 {alignedExchangeCount}곳
            </h3>
            <p className="ui-caption mt-1 text-muted-foreground">
              기여 · 거래금액 변화 · 가장 크게 기여한 코인
            </p>
            <div className="mt-3 divide-y divide-border/70 border-y border-border/70">
              {exchangeRows.map(row => (
                <div
                  key={row.exchange}
                  className="grid grid-cols-[72px_82px_1fr] items-center gap-3 py-2 text-xs"
                >
                  <strong>{exchangeLabel(row.exchange)}</strong>
                  <span
                    className={
                      row.contribution_pp >= 0 ? "text-profit" : "text-loss"
                    }
                  >
                    {points(row.contribution_pp)}
                  </span>
                  <span className="text-right text-muted-foreground">
                    <span className="whitespace-nowrap">
                      {money(row.volume_change_krw)}
                    </span>{" "}
                    <span className="whitespace-nowrap">
                      · {row.leading_asset || "주요 코인 없음"}
                    </span>
                  </span>
                </div>
              ))}
              {!exchangeRows.length && (
                <p className="py-3 text-xs text-muted-foreground">
                  비교에 필요한 거래소별 자료가 아직 확보되지 않았습니다.
                </p>
              )}
            </div>
          </div>
        </div>
        {selected && (
          <div className="border-t border-border bg-card px-5 py-4">
            <div className="flex flex-wrap items-end justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold">
                  이벤트 당시 초단위 실시간 근거
                </h3>
                <p className="ui-caption mt-1 text-muted-foreground">
                  공식 변화 시각 직전 15초 안의 10초 저장본입니다. 공식 24시간
                  기여도를 보조하며 원인으로 단정하지 않습니다.
                </p>
              </div>
              <span className="ui-caption text-muted-foreground">
                {flowEvidence.length}개 체크포인트
              </span>
            </div>

            {flowEvidence.length ? (
              <div className="mt-3 grid gap-2 lg:grid-cols-3">
                {flowEvidence.map(evidence => {
                  const flow = evidence.payload;
                  const asset = flow.drivers?.[0];
                  const exchange = flow.exchange_drivers?.[0];
                  return (
                    <div
                      key={evidence.snapshot_kind}
                      className="border border-border/70 bg-inset p-3 text-xs"
                    >
                      <div className="flex items-center justify-between gap-2">
                        <strong>
                          {checkpointLabel(evidence.snapshot_kind)}
                        </strong>
                        <span className="text-muted-foreground">
                          {timestamp(evidence.flow_as_of_ms)}
                        </span>
                      </div>
                      <p className="mt-2 font-medium">
                        실시간 {flow.primary_window_sec || 10}초 점유율 변화{" "}
                        <span
                          className={
                            finite(flow.share_delta_pp) &&
                            flow.share_delta_pp < 0
                              ? "text-loss"
                              : "text-profit"
                          }
                        >
                          {points(flow.share_delta_pp)}
                        </span>
                      </p>
                      <p className="mt-1 text-muted-foreground">
                        {asset
                          ? `${asset.asset} ${points(asset.contribution_pp)}`
                          : "코인 기여 없음"}
                        {exchange
                          ? ` · ${exchangeLabel(exchange.exchange)} ${points(exchange.contribution_pp)}`
                          : ""}
                      </p>
                      <p className="ui-caption mt-1 text-muted-foreground">
                        공식 시각과 {Math.round(evidence.lag_ms / 100) / 10}초
                        차이 · {flow.status}
                      </p>
                    </div>
                  );
                })}
              </div>
            ) : (
              <p className="mt-3 border border-border p-3 text-xs text-muted-foreground">
                이벤트 당시의 초단위 실시간 자료가 없습니다. 기능 적용 이전
                이벤트이거나 기준 시각 직전 15초 안에 정상 자료가 없었던
                경우입니다.
              </p>
            )}
          </div>
        )}

        {selected ? (
          selected.notices?.length ? (
            <section className="border-t border-border bg-inset">
              <header className="px-5 py-3 text-xs font-semibold">
                참고 자료 · 같은 시각의 공지 {selected.notices.length}건
              </header>
              <div className="grid gap-2 border-t border-border p-4 sm:grid-cols-2">
                {selected.notices.map(notice => (
                  <a
                    key={`${notice.exchange}-${notice.notice_id}-${notice.anchor_ts_ms}`}
                    href={notice.url}
                    target="_blank"
                    rel="noreferrer"
                    className="border border-border p-3 text-xs hover:border-info"
                  >
                    <span className="font-semibold">{notice.title}</span>
                    <span className="mt-1 block text-muted-foreground">
                      {exchangeLabel(notice.exchange)} ·{" "}
                      {relationLabel(notice.relation)} ·{" "}
                      {notice.matched_assets.join(", ") || "코인 일치 없음"}
                    </span>
                  </a>
                ))}
              </div>
            </section>
          ) : (
            <div className="official-notice-context-empty flex items-start gap-2 border-t border-border px-5 py-3 text-xs text-muted-foreground">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              공지·상장 이벤트는 핵심 원인이 아니라 참고 자료입니다. 연결된
              공지가 없어도 수치 기여 분석은 유효합니다.
            </div>
          )
        ) : null}
      </div>
    </section>
  );
}
