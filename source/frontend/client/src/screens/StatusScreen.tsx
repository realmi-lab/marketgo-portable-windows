import { useMemo } from "react";
import { collectorConnectionTone } from "@/lib/collector-connection-warning";

import { exchangeLabel } from "@/lib/exchanges";
import { formatSharePercent } from "@/lib/formatters";
import { marketShareDurationLabel } from "@/lib/market-share-labels";
import { ObservedRangeHelp } from "@/components/common/ObservedRangeHelp";
import {
  observationGapReasonLabels,
  observationGapStatusLabel,
} from "@/lib/market-share-status-presentation";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  comparisonNote,
  flowStatus,
  ledgerStatusLabel,
  sourceStatusLabel,
  windowLabel,
  official24hReceipt,
} from "@/lib/screen-copy";
import {
  gapKind,
  gapRiskLabel,
  gapSummaryByPlace,
  type GapKind,
} from "@/lib/screen-status";
import type { ScreenProps } from "./AppShell";
import { GapMap } from "./charts";
import { useRealtimeDay, useRecentNotices, useWindowChanges } from "./data";
import { useLiveFlow, useMarketStream } from "./market-stream";
import { DOMESTIC, useAnalysis } from "./shared";
import {
  Card,
  Chip,
  Empty,
  Ex,
  Foot,
  Kpi,
  Legend,
  StaleNote,
  Table,
} from "./ui";

type Gap = NonNullable<
  NonNullable<ReturnType<typeof useAnalysis>>["data_quality"]
>["observation_gaps"];
type GapRow = NonNullable<Gap>[number];

const GAP_COLORS: Record<GapKind, string> = {
  connection: "var(--loss)",
  collector: "var(--gold)",
  journal: "var(--info)",
  other: "var(--muted-foreground)",
};

const GAP_KIND_LABELS: Record<GapKind, string> = {
  connection: "실시간 연결 끊김",
  collector: "수집 서버 자료 불완전",
  journal: "수집 상태 기록 끊김·서버 중단",
  other: "기타",
};

const placeLabel = (place: string) =>
  place === "all" ? "전체" : exchangeLabel(place);

/** 자료 상태: how far the numbers on every screen can be trusted. */
export default function StatusScreen(_props: ScreenProps) {
  const analysis = useAnalysis();
  // null until the gap records arrive: "none" is said only of an empty list.
  const gaps: GapRow[] | null =
    analysis?.data_quality?.observation_gaps ?? null;
  const nowSecond = analysis?.generated_at
    ? Math.floor(analysis.generated_at)
    : Math.floor(Date.now() / 1000);
  return (
    <div className="mg-page">
      <div className="mg-grid">
        <SourcesCard />
        <GapPlacesCard gaps={gaps} nowSecond={nowSecond} />
        <GapMapCard gaps={gaps} nowSecond={nowSecond} />
        <GapListCard gaps={gaps} />
        <CollectorsCard />
        <AccountingCard />
        <FlowFrequencyCard />
        <WindowsReadyCard />
        <RetentionCard />
        <DisplayRulesCard />
      </div>
    </div>
  );
}

/** Each data source, its state and the values that depend on it. */
function SourcesCard() {
  const analysis = useAnalysis();
  const { readiness, deferredCollectorWarnings } = useMarketStream();
  const day = useRealtimeDay(1);
  const notices = useRecentNotices(1);
  const quality = analysis?.data_quality;
  const official = analysis?.official_domestic_24h;
  const hour = analysis?.official_ticker_1m;
  const sources = notices.data?.snapshot;
  const rows: Array<{
    name: string;
    state: string;
    tone: "ok" | "warn" | "alert" | "muted";
    affects: string;
  }> = [
    {
      name: "공식 24시간",
      state: official24hReceipt(official).label,
      tone: official24hReceipt(official).ok ? "ok" : "warn",
      affects: "브리핑 맨 위 점유율, 거래소 24시간 거래금액",
    },
    {
      name: "정시 1시간봉",
      state: hour ? `${sourceStatusLabel(hour.status)} · 3곳` : "자료 없음",
      tone: hour?.status === "LIVE" ? "ok" : "warn",
      affects: "정시 점유율 48시간, 1시간 변화와 기여 (코빗·고팍스 미공개)",
    },
    {
      name: "실시간 체결",
      state: readiness
        ? `${readiness.connected_market_count.toLocaleString("ko-KR")}/${readiness.registered_market_count.toLocaleString("ko-KR")}개 마켓`
        : "연결 확인 중",
      tone: collectorConnectionTone(readiness, deferredCollectorWarnings),
      affects: "10초~24시간 실시간 점유율, 원인 나누기, 코인·거래소 화면",
    },
    {
      name: "10초 표본",
      state: day.data
        ? `최근 24시간 ${formatSharePercent(day.data.coverage_pct)} 관측${day.stale ? " · 갱신 실패" : ""}`
        : day.error
          ? "받지 못함"
          : "확인 중",
      tone:
        day.data && !day.stale && day.data.coverage_pct >= 99 ? "ok" : "warn",
      affects: "실시간 흐름 상태, 10초 표본 기록, 자주 등장한 코인",
    },
    {
      name: "호가",
      state: quality?.components?.liquidity
        ? `${quality.components.liquidity.fresh_market_count.toLocaleString("ko-KR")}개 마켓 최근 갱신 · 조용한 ${quality.components.liquidity.quiet_market_count.toLocaleString("ko-KR")}개 제외`
        : "자료 없음",
      tone: quality?.components?.liquidity?.status === "OK" ? "ok" : "warn",
      affects: "호가 금액 점유율, 스프레드, 체결 비용",
    },
    {
      name: "공지",
      state: sources
        ? `${sources.healthy_source_count}/${sources.source_count}곳 수집${notices.stale ? " · 갱신 실패" : ""}`
        : notices.error
          ? "받지 못함"
          : "확인 중",
      tone:
        sources &&
        !notices.stale &&
        sources.healthy_source_count === sources.source_count
          ? "ok"
          : "warn",
      affects: "공지 화면, 사건과 함께 보기",
    },
    {
      name: "환율",
      state:
        quality?.components?.fx &&
        quality.components.fx.domestic_unresolved_trade_count > 0
          ? `원화로 바꾸지 못한 체결 ${quality.components.fx.domestic_unresolved_trade_count.toLocaleString("ko-KR")}건`
          : quality?.components?.fx
            ? "모두 원화로 환산"
            : "자료 없음",
      tone: !quality?.components?.fx
        ? "muted"
        : quality.components.fx.domestic_unresolved_trade_count > 0
          ? "warn"
          : "ok",
      affects: "원화가 아닌 마켓을 포함한 점유율",
    },
  ];
  return (
    <Card
      span={12}
      size="m"
      title="빠진 자료와 영향받는 값"
      note="자료마다 지금 상태와 그 자료로 계산하는 값"
    >
      <ul className="mg-list">
        {rows.map(row => (
          <li key={row.name}>
            <span className="mg-row">
              <span className="mg-name">{row.name}</span>
              <Chip tone={row.tone}>{row.state}</Chip>
            </span>
            <span className="mg-muted">{row.affects}</span>
          </li>
        ))}
      </ul>
      <Foot>
        빠진 값은 0으로 채우지 않습니다. 비어 있으면 —로 표시합니다. 일부만
        관측한 값의 계산 기준은 도움말에서 확인할 수 있습니다.
        <ObservedRangeHelp />
      </Foot>
    </Card>
  );
}

const GAPS_WAITING = "끊김 기록을 받는 중입니다.";

function GapPlacesCard({
  gaps,
  nowSecond,
}: {
  gaps: GapRow[] | null;
  nowSecond: number;
}) {
  const summary = useMemo(
    () => gapSummaryByPlace(gaps ?? [], nowSecond - 86_400),
    [gaps, nowSecond]
  );
  return (
    <Card
      span={5}
      size="m"
      title="끊김을 만든 곳"
      note="서버가 남긴 최근 끊김 기록(최대 100건) 가운데 24시간 안의 건수와 가장 긴 끊김"
    >
      {!gaps ? (
        <Empty>{GAPS_WAITING}</Empty>
      ) : summary.length === 0 ? (
        <Empty>최근 24시간 끊김 기록이 없습니다.</Empty>
      ) : (
        <ul className="mg-list">
          {summary.map(row => (
            <li key={row.place}>
              <span>
                {row.place === "all" ? (
                  <span className="mg-name">전체 거래소</span>
                ) : (
                  <Ex exchange={row.place} />
                )}
              </span>
              <span className="mg-num">
                {row.count}건 · 가장 긴{" "}
                {marketShareDurationLabel(row.longestSec)}
              </span>
            </li>
          ))}
        </ul>
      )}
      <Foot>
        한 거래소만 끊겨도 그 시간은 모든 구간 비교에서 빠집니다. 거래 비중 0.1%
        이하인 거래소 전체가 끊겼다면 그곳을 빼고 잠정으로 비교합니다. 끊김이
        잦아 비중을 재지 못한 끊김은 하루 안에 그 거래소를 마지막으로 잰 비중을
        씁니다.
      </Foot>
    </Card>
  );
}

function GapMapCard({
  gaps,
  nowSecond,
}: {
  gaps: GapRow[] | null;
  nowSecond: number;
}) {
  const from = nowSecond - 86_400;
  // Gaps that ended before the map's 24 hours are not on it.
  const spans = (gaps ?? [])
    .filter(gap => (gap.end_second ?? nowSecond) >= from)
    .flatMap(gap =>
      (gap.affected_exchanges.length ? gap.affected_exchanges : ["all"]).map(
        place => ({
          place,
          start: gap.start_second,
          end: gap.end_second ?? nowSecond,
          color: GAP_COLORS[gapKind(gap.reason_codes)],
          title: `${formatMarketTimeMS(gap.start_second * 1000, "time-seconds")} ${placeLabel(place)} ${gap.duration_sec}초 · ${observationGapReasonLabels(gap.reason_codes).join(", ")}`,
        })
      )
    );
  return (
    <Card
      span={7}
      size="m"
      title="24시간 끊김 지도"
      note="거래소별 끊김 기록. 색은 끊긴 이유이고, 막대를 가리키거나 누르면 자세히 나옵니다."
    >
      <GapMap
        label="최근 24시간 거래소별 끊김"
        rows={[
          ...DOMESTIC.map(place => ({ place, label: exchangeLabel(place) })),
          { place: "all", label: "전체" },
        ]}
        spans={spans}
        fromSecond={from}
        toSecond={nowSecond}
      />
      <Legend
        items={(Object.keys(GAP_COLORS) as GapKind[]).map(kind => ({
          key: kind,
          label: GAP_KIND_LABELS[kind],
          color: GAP_COLORS[kind],
        }))}
      />
    </Card>
  );
}

function GapListCard({ gaps }: { gaps: GapRow[] | null }) {
  return (
    <Card
      span={12}
      size="l"
      title="끊김 기록"
      note="새 기록이 위에 옵니다. 영향은 끊기기 전 10분 동안 그 거래소가 국내 원화 거래에서 차지한 비중입니다."
    >
      {!gaps ? (
        <Empty>{GAPS_WAITING}</Empty>
      ) : gaps.length === 0 ? (
        <Empty>끊김 기록이 없습니다.</Empty>
      ) : (
        <Table
          label="끊김 기록"
          head={
            <tr>
              <th>시작</th>
              <th>끝</th>
              <th className="mg-num">길이</th>
              <th>거래소</th>
              <th>이유</th>
              <th className="mg-num">영향</th>
              <th>상태</th>
            </tr>
          }
        >
          {gaps.map(gap => (
            <tr key={`${gap.gap_id}:${gap.start_second}`}>
              <td className="mg-num">
                {formatMarketTimeMS(gap.start_second * 1000, "date-time")}
              </td>
              <td className="mg-num">
                {gap.end_second
                  ? formatMarketTimeMS(gap.end_second * 1000, "time-seconds")
                  : "진행 중"}
              </td>
              <td className="mg-num">
                {marketShareDurationLabel(gap.duration_sec)}
              </td>
              <td>
                {gap.affected_exchanges.length
                  ? gap.affected_exchanges.map(exchangeLabel).join("·")
                  : "전체"}
              </td>
              <td>{observationGapReasonLabels(gap.reason_codes).join(", ")}</td>
              <td className="mg-num">{gapRiskLabel(gap)}</td>
              <td>{observationGapStatusLabel(gap.status)}</td>
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}

/** Each exchange's collector as the calculator sees it. */
function CollectorsCard() {
  const analysis = useAnalysis();
  const { deferredCollectorWarnings } = useMarketStream();
  const quality = analysis?.data_quality;
  // Without the server's list there is nothing to call 정상.
  const known = Array.isArray(quality?.unhealthy_collectors);
  const unhealthy = new Set(quality?.unhealthy_collectors ?? []);
  const books = analysis?.liquidity?.exchanges ?? [];
  return (
    <Card
      span={6}
      size="m"
      title="거래소 연결과 호가"
      note="수집 서버 상태와 호가 수신"
    >
      <Table
        label="거래소 연결"
        head={
          <tr>
            <th>거래소</th>
            <th>체결 연결</th>
            <th className="mg-num">호가 받는 마켓</th>
          </tr>
        }
      >
        {DOMESTIC.map(exchange => {
          const book = books.find(row => row.exchange === exchange);
          return (
            <tr key={exchange}>
              <td>
                <Ex exchange={exchange} />
              </td>
              <td>
                {!known ? (
                  <Chip>확인 전</Chip>
                ) : unhealthy.has(exchange) ? (
                  deferredCollectorWarnings.includes(exchange) ? (
                    <Chip>연결 확인 중</Chip>
                  ) : (
                    <Chip tone="alert">끊김</Chip>
                  )
                ) : (
                  <Chip tone="ok">정상</Chip>
                )}
              </td>
              <td className="mg-num">
                {book ? formatSharePercent(book.live_market_pct) : "—"}
              </td>
            </tr>
          );
        })}
      </Table>
    </Card>
  );
}

/** Trades in, trades counted: the calculator's own ledger. */
function AccountingCard() {
  const analysis = useAnalysis();
  const ledger = analysis?.data_quality?.trade_accounting;
  return (
    <Card
      span={6}
      size="m"
      title="체결 처리 장부"
      note="받은 체결과 계산에 들어간 체결"
    >
      {!ledger ? (
        <Empty>
          {analysis ? "장부 자료가 없습니다." : "장부 자료를 받는 중입니다."}
        </Empty>
      ) : (
        <>
          <div className="mg-kpis">
            <Kpi
              label="받은 체결"
              value={ledger.trades_enqueued.toLocaleString("ko-KR")}
            />
            <Kpi
              label="계산에 들어감"
              value={ledger.aggregator_accepted_trades.toLocaleString("ko-KR")}
            />
            <Kpi
              label="중복 제외"
              value={ledger.duplicate_trade_events.toLocaleString("ko-KR")}
            />
            <Kpi
              label="잘못된 체결"
              value={ledger.invalid_trade_events.toLocaleString("ko-KR")}
            />
          </div>
          <Foot>
            상태 {ledgerStatusLabel(ledger.status)} · 처리 대기{" "}
            {ledger.queue_in_flight.toLocaleString("ko-KR")}건 · 처리 오류{" "}
            {ledger.trade_processing_errors.toLocaleString("ko-KR")}건
          </Foot>
        </>
      )}
    </Card>
  );
}

/** How often the one-second flow raised watch or alert today. */
function FlowFrequencyCard() {
  const day = useRealtimeDay(1);
  const summary = day.data;
  const watch = flowStatus("WATCH");
  const alert = flowStatus("ALERT");
  // The thresholds are the server's, as the live flow reports them.
  const thresholds = (useLiveFlow()?.windows ?? [])
    .map(
      window => `${windowLabel(window.duration_sec)} ${window.threshold_pp}%p`
    )
    .join(", ");
  return (
    <Card
      span={6}
      size="m"
      title="흐름 판정 빈도"
      note="최근 24시간 10초 표본 가운데 관찰·경고가 된 비율"
    >
      <StaleNote read={day} />
      {!summary ? (
        <Empty>
          {day.error ? `받지 못했습니다. ${day.error}` : "불러오는 중입니다."}
        </Empty>
      ) : (
        <>
          <ul className="mg-list">
            <li>
              <span>관측한 표본</span>
              <span className="mg-num">
                {summary.observed_sample_count.toLocaleString("ko-KR")}개
              </span>
            </li>
            <li>
              <span>
                <Chip tone={watch.tone}>{watch.label}</Chip>
              </span>
              <span className="mg-num">
                {summary.watch_sample_count.toLocaleString("ko-KR")}개
              </span>
            </li>
            <li>
              <span>
                <Chip tone={alert.tone}>{alert.label}</Chip>
              </span>
              <span className="mg-num">
                {summary.alert_sample_count.toLocaleString("ko-KR")}개
              </span>
            </li>
          </ul>
          <Foot>
            관찰·경고가 하루 표본의 큰 몫을 차지하면 기준선
            {thresholds ? `(${thresholds})` : ""}을 다시 볼 때입니다.
          </Foot>
        </>
      )}
    </Card>
  );
}

/** Which windows can be split into causes right now. */
function WindowsReadyCard() {
  const changes = useWindowChanges();
  const rows = changes.data?.windows ?? [];
  return (
    <Card
      span={6}
      size="m"
      title="구간마다 원인을 나눌 수 있나"
      note="지금 각 구간의 비교 상태"
    >
      <StaleNote read={changes} />
      {rows.length === 0 ? (
        <Empty>
          {changes.loading
            ? "불러오는 중입니다."
            : `구간 비교를 받지 못했습니다. ${changes.error}`}
        </Empty>
      ) : (
        <ul className="mg-list">
          {rows.map(row => (
            <li key={row.window_sec}>
              <span className="mg-name">{windowLabel(row.window_sec)}</span>
              <span>
                {row.status === "CONFIRMED" ? (
                  <Chip tone="ok">나눌 수 있음</Chip>
                ) : (
                  <span
                    className={
                      row.status === "PROVISIONAL" ? "mg-attn" : "mg-muted"
                    }
                  >
                    {comparisonNote(row)}
                  </span>
                )}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function RetentionCard() {
  return (
    <Card span={6} size="fit" title="보관과 복구">
      <ul className="mg-list">
        <li>
          실시간 체결 합계는 48시간 보관합니다. 가장 긴 비교(12시간 + 직전
          12시간)와 24시간을 덮습니다.
        </li>
        <li>
          공지는 기간 제한 없이 보관합니다. 목록은 최근 30일을 먼저 보여 줍니다.
        </li>
        <li>
          빠진 시세를 나중에 채우는 자동 복구는 꺼져 있습니다. 빠진 시간은 빠진
          채로 둡니다.
        </li>
      </ul>
    </Card>
  );
}

function DisplayRulesCard() {
  return (
    <Card span={6} size="fit" title="화면 표시 기준">
      <ul className="mg-list">
        <li>
          숫자 옆에 공식 24시간, 정시 3곳, 실시간 5곳 등의 기준을 적습니다. 일부
          관측 상태의 자세한 계산 기준은 옆의 ? 도움말에 있습니다.
        </li>
        <li>
          색은 오름·내림, 주의, 거래소 구분에만 씁니다. 빗썸 주황은 빗썸 값에만
          씁니다.
        </li>
        <li>
          계산으로 나눈 몫만 원인이라고 부르고, 같은 시간의 공지는 함께
          있었다고만 적습니다.
        </li>
        <li>매초 바뀌는 줄은 위의 일시정지로 멈출 수 있습니다.</li>
      </ul>
    </Card>
  );
}
