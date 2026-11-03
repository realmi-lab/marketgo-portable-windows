import { useState, type ReactNode } from "react";

import { ObservedRangeHelp } from "@/components/common/ObservedRangeHelp";
import {
  LiveExchangeChartContent,
  liveExchangeChartCopy,
} from "@/components/LiveExchangeChart";
import { useLiveOverviewHistory } from "@/hooks/useLiveOverviewHistory";
import { useLiveChartExplorer } from "@/hooks/useLiveChartExplorer";
import {
  LiveChartControls,
  LiveChartNavigator,
} from "@/components/LiveChartNavigation";
import { formatKRW, formatSharePercent } from "@/lib/formatters";
import {
  formatMarketTimeMS,
  formatRecentMarketTimeMS,
} from "@/lib/market-time-format";
import { flowStatus, windowLabel } from "@/lib/screen-copy";
import type { ScreenProps } from "./AppShell";
import { Sparkline } from "./charts";
import { useRealtimeDay, useRealtimeHistory } from "./data";
import {
  useLiveTick,
  useMarketStream,
  useShareTrail,
  useShownFlow,
} from "./market-stream";
import {
  BestQuotesCard,
  CompositionCard,
  SecondFlowCard,
  WhaleCard,
} from "./shared";
import {
  Card,
  Chip,
  Empty,
  Ex,
  Foot,
  Kpi,
  LiveValue,
  Pct,
  Pp,
  StaleNote,
  Seg,
  Table,
  WindowSeg,
  Won,
} from "./ui";

/** 실시간: what is moving the share this second. */
export default function LiveScreen(_props: ScreenProps) {
  const day = useRealtimeDay(20);
  return (
    <div className="mg-page">
      <LiveHero />
      <div className="mg-grid">
        <ExchangeLines aside={<MoversCard />} />
        <SecondFlowCard span={7} size="l" height={240} link={false} />
        <CompositionCard span={5} size="l" picker={false} />
        <HistoryCard />
        <EpisodesCard day={day} />
        <BestQuotesCard />
        <WhaleCard span={5} size="l" />
        <DayCard day={day} />
      </div>
    </div>
  );
}

type RealtimeDay = ReturnType<typeof useRealtimeDay>;

/**
 * Bithumb's 10-second, 30-second and 1-minute shares as live gauges, each
 * with the line of its recent values, and the domestic trading of the last
 * second.
 */
function LiveHero() {
  const { flow, visible, window: shown, partial } = useShownFlow();
  const { connection, livePaused } = useMarketStream();
  const trail = useShareTrail();
  const state = flowStatus(flow?.status);
  // Before the first flow the three gauges are drawn empty, so the hero
  // keeps its size when the figures arrive.
  const gauges = flow?.windows?.length
    ? flow.windows.map(window => ({
        seconds: window.duration_sec,
        threshold: window.threshold_pp,
        status: window.status,
      }))
    : [10, 30, 60].map(seconds => ({
        seconds,
        threshold: null as number | null,
        status: "",
      }));
  // A second missing an exchange shows the trading observed in it; a second
  // never received is a gap, never a zero.
  const volumes = (flow?.recent_share_1s ?? [])
    .slice(-60)
    .map(point => point.domestic_volume_1s_krw ?? null);
  return (
    <section className="mg-card" aria-label="지금 흐름">
      <div className="mg-hero-status">
        <span
          className="mg-pulse"
          data-off={
            livePaused || !flow || connection !== "live" ? "" : undefined
          }
        />
        <Chip tone={state.tone}>{state.label}</Chip>
        <span className="mg-copy">
          {flow?.reason || "1초 흐름을 기다리는 중입니다."}
        </span>
        <span className="mg-strip-fill" />
        <span className="mg-muted mg-num">
          {formatMarketTimeMS(flow?.as_of_ms, "time-seconds")} 기준
        </span>
      </div>
      <div className="mg-hero">
        {gauges.map(gauge => {
          const values = shown(gauge.seconds);
          const label = windowLabel(gauge.seconds);
          return (
            <div className="mg-gauge" key={gauge.seconds}>
              <span className="mg-gauge-label">
                빗썸 {label}
                {gauge.threshold === null ? null : (
                  <span title="이만큼 움직이면 관찰·경고로 봅니다">
                    변동 기준 {gauge.threshold}%p
                  </span>
                )}
              </span>
              <strong className="mg-gauge-value mg-num mg-bt">
                <LiveValue value={values?.current_share_pct}>
                  {formatSharePercent(values?.current_share_pct)}
                </LiveValue>
              </strong>
              <span className="mg-gauge-detail">
                {values && !partial(gauge.seconds) ? (
                  <>
                    <Pp value={values.share_delta_pp} />{" "}
                    <span className="mg-muted">
                      직전 {label} <Pct value={values.previous_share_pct} />
                    </span>
                  </>
                ) : values ? (
                  // Some seconds or exchanges were not observed: the share is
                  // of what was, and no change is drawn from it.
                  <ObservedRangeHelp />
                ) : !flow ? (
                  <span className="mg-muted">1초 흐름을 기다리는 중</span>
                ) : (
                  <span className="mg-muted">
                    이 구간 점유율이 아직 없습니다
                  </span>
                )}
              </span>
              <Sparkline
                values={trail[gauge.seconds] ?? []}
                color="var(--exchange-bithumb)"
              />
            </div>
          );
        })}
        <div className="mg-gauge">
          <span className="mg-gauge-label">국내 1초 거래금액</span>
          <strong className="mg-gauge-value mg-num">
            <LiveValue value={flow?.domestic_volume_1s_krw}>
              {typeof flow?.domestic_volume_1s_krw === "number"
                ? formatKRW(flow.domestic_volume_1s_krw)
                : "—"}
            </LiveValue>
          </strong>
          <span className="mg-gauge-detail">
            {flow ? (
              <>
                빗썸 <Won value={flow.bithumb_volume_1s_krw} />, 1초 점유율{" "}
                <Pct value={flow.bithumb_share_1s_pct} />
                {visible ? null : <ObservedRangeHelp />}
              </>
            ) : (
              "—"
            )}
          </span>
          <Sparkline values={volumes} color="var(--foreground)" />
        </div>
      </div>
    </section>
  );
}

/**
 * The five exchanges' shares and their trading value and trade count per
 * second, as the dashboard drew them before the redesign. Both read one
 * history of the live screen's window.
 */
function ExchangeLines({ aside }: { aside: ReactNode }) {
  const tick = useLiveTick();
  const { connection, livePaused, windowSec, setWindowSec } = useMarketStream();
  const history = useLiveOverviewHistory({
    tick,
    connection,
    marketScope: "KRW",
    requestedWindowSec: windowSec,
  });
  const share = liveExchangeChartCopy("share");
  return (
    <ExchangeExplorer
      key={windowSec}
      history={history}
      aside={aside}
      windowSec={windowSec}
      setWindowSec={setWindowSec}
      receiving={connection === "live" && !livePaused}
      shareTitle={share.title}
    />
  );
}

function ExchangeExplorer({
  history,
  aside,
  windowSec,
  setWindowSec,
  receiving,
  shareTitle,
}: {
  history: Parameters<typeof useLiveChartExplorer>[0];
  aside: ReactNode;
  windowSec: number;
  setWindowSec: (seconds: number) => void;
  receiving: boolean;
  shareTitle: string;
}) {
  const explorer = useLiveChartExplorer(history, 120_000);
  const [rateView, setRateView] = useState<"volume" | "trades">("volume");
  const rate = liveExchangeChartCopy("rate");
  return (
    <>
      <Card
        span={8}
        size="fit"
        title={`${shareTitle} · ${windowLabel(windowSec)}`}
        note="5개 거래소의 선택 구간 점유율. 세로 눈금은 보이는 값에 맞춰 확대합니다."
        actions={<WindowSeg value={windowSec} onChange={setWindowSec} />}
      >
        <LiveChartControls explorer={explorer} receiving={receiving} />
        <LiveExchangeChartContent
          history={explorer.visible}
          metric="share"
          explorer={explorer}
          flowing={{ running: receiving }}
        />
      </Card>
      {aside}
      <Card
        span={12}
        size="fit"
        title={`${rateView === "volume" ? rate.title : "국내 거래소별 체결 횟수"} · ${windowLabel(windowSec)}`}
        note="위 점유율 차트와 같은 시각·구간을 보여 줍니다. 거래금액과 체결 횟수는 각각의 단위로 확인합니다."
        actions={
          <Seg
            label="거래 흐름 지표"
            value={rateView}
            onChange={setRateView}
            options={[
              { value: "volume", label: "거래금액" },
              { value: "trades", label: "체결 횟수" },
            ]}
          />
        }
      >
        <LiveExchangeChartContent
          history={explorer.visible}
          metric="rate"
          explorer={explorer}
          rateView={rateView}
          flowing={{ running: receiving }}
        />
        <LiveChartNavigator explorer={explorer} />
      </Card>
    </>
  );
}

/** The coins and exchanges behind the latest flow, largest first. */
function MoversCard() {
  const { flow, visible } = useShownFlow();
  const drivers = visible ? (flow?.drivers ?? []) : [];
  return (
    <Card
      span={4}
      size="l"
      title="지금 점유율을 움직이는 것"
      note={`${flow?.primary_window_sec ? `${flow.primary_window_sec}초 비교, ` : ""}기여가 큰 순서`}
    >
      {drivers.length === 0 ? (
        <Empty>
          {!flow
            ? "1초 흐름 자료를 받는 중입니다."
            : visible
              ? "지금은 눈에 띄는 기여가 없습니다."
              : "빠진 초가 있어 기여는 나누지 않았습니다."}
        </Empty>
      ) : (
        <ul className="mg-list">
          {drivers.slice(0, 5).map(driver => (
            <li key={driver.asset}>
              <span>
                <b>{driver.asset}</b>{" "}
                <span className="mg-muted">
                  빗썸 <Won value={driver.bithumb_volume_change_krw} signed />,
                  경쟁{" "}
                  <Won value={driver.competitor_volume_change_krw} signed />
                </span>
              </span>
              <Pp value={driver.contribution_pp} />
            </li>
          ))}
        </ul>
      )}
      <Table
        label="거래소별 기여"
        head={
          <tr>
            <th>거래소</th>
            <th className="mg-num">거래금액 변화</th>
            <th className="mg-num">기여</th>
          </tr>
        }
      >
        {(visible ? (flow?.exchange_drivers ?? []) : []).map(row => (
          <tr key={row.exchange}>
            <td>
              <Ex exchange={row.exchange} />
            </td>
            <td className="mg-num">
              <Won value={row.volume_change_krw} signed />
            </td>
            <td className="mg-num">
              <Pp value={row.contribution_pp} />
            </td>
          </tr>
        ))}
      </Table>
    </Card>
  );
}

/** Ten-second samples of the last 30 minutes, newest first. */
function HistoryCard() {
  const history = useRealtimeHistory(30);
  const items = [...(history.data?.items ?? [])]
    .sort((left, right) => right.as_of_ms - left.as_of_ms)
    .slice(0, 60);
  const share = (item: (typeof items)[number], seconds: number) =>
    item.windows.find(window => window.duration_sec === seconds);
  return (
    <Card
      span={7}
      size="l"
      title="상태와 구간 점유율 기록"
      note="최근 10분, 10초마다 남긴 기록. 새 기록이 위에 옵니다."
    >
      <StaleNote read={history} />
      {history.loading ? (
        <Empty>기록을 불러오는 중입니다.</Empty>
      ) : items.length === 0 ? (
        <Empty>
          {history.error
            ? `기록을 받지 못했습니다. ${history.error}`
            : "최근 기록이 없습니다."}
        </Empty>
      ) : (
        <Table
          label="10초 표본 기록"
          head={
            <tr>
              <th>시각</th>
              <th>상태</th>
              <th className="mg-num">10초</th>
              <th className="mg-num">30초</th>
              <th className="mg-num">1분</th>
              <th>가장 큰 기여</th>
            </tr>
          }
        >
          {items.map(item => {
            const state = flowStatus(item.status);
            return (
              <tr key={item.as_of_ms}>
                <td className="mg-num">
                  {formatMarketTimeMS(item.as_of_ms, "time-seconds")}
                </td>
                <td>
                  <Chip tone={state.tone}>{state.label}</Chip>
                </td>
                {[10, 30, 60].map(seconds => {
                  // A window that was not fully observed has no share to list.
                  const window = share(item, seconds);
                  const complete = window?.status === "COMPLETE";
                  return (
                    <td key={seconds} className="mg-num">
                      <Pct value={complete ? window.current_share_pct : null} />{" "}
                      <Pp value={complete ? window.share_delta_pp : null} />
                    </td>
                  );
                })}
                <td>
                  {item.drivers[0] ? (
                    <>
                      {item.drivers[0].asset}{" "}
                      <Pp value={item.drivers[0].contribution_pp} />
                    </>
                  ) : (
                    "—"
                  )}
                </td>
              </tr>
            );
          })}
        </Table>
      )}
    </Card>
  );
}

/** Watch and alert episodes of the day, newest first. */
function EpisodesCard({ day }: { day: RealtimeDay }) {
  const episodes = [...(day.data?.episodes ?? [])].sort(
    (left, right) => right.end_ms - left.end_ms
  );
  return (
    <Card
      span={5}
      size="l"
      title="관찰·경고 구간"
      note="최근 24시간 동안 관찰이나 경고가 이어진 구간과 그동안의 점유율 변화"
    >
      <StaleNote read={day} />
      {day.loading ? (
        <Empty>기록을 불러오는 중입니다.</Empty>
      ) : episodes.length === 0 ? (
        <Empty>
          {day.error
            ? `기록을 받지 못했습니다. ${day.error}`
            : "최근 24시간 관찰·경고 구간이 없습니다."}
        </Empty>
      ) : (
        <ul className="mg-list">
          {episodes.map(episode => {
            const state = flowStatus(episode.status);
            return (
              <li key={episode.id}>
                <span>
                  <span className="mg-num mg-muted">
                    {formatRecentMarketTimeMS(episode.start_ms)}
                  </span>{" "}
                  <Chip tone={state.tone}>{state.label}</Chip>{" "}
                  {episode.duration_sec}초 · {episode.top_asset}
                </span>
                <Pp value={episode.net_share_change_pp} />
              </li>
            );
          })}
        </ul>
      )}
    </Card>
  );
}

/** The day of ten-second samples in one card. */
function DayCard({ day }: { day: RealtimeDay }) {
  const summary = day.data;
  return (
    <Card
      span={7}
      size="l"
      title="최근 24시간 실시간 요약"
      note="10초마다 남긴 기록 가운데 실제로 관측한 범위만 셉니다."
    >
      <StaleNote read={day} />
      {!summary ? (
        <Empty>
          {day.error
            ? `요약을 받지 못했습니다. ${day.error}`
            : "요약을 불러오는 중입니다."}
        </Empty>
      ) : (
        <>
          <div className="mg-kpis">
            <Kpi
              label="관측한 기록"
              value={formatSharePercent(summary.coverage_pct)}
              detail={`${summary.observed_sample_count.toLocaleString("ko-KR")}/${summary.expected_sample_count.toLocaleString("ko-KR")}개, 빠진 ${summary.missing_sec.toLocaleString("ko-KR")}초`}
            />
            <Kpi
              label="관찰·경고 기록"
              value={`${summary.signal_sample_count.toLocaleString("ko-KR")}개`}
              detail={`관찰 ${summary.watch_sample_count.toLocaleString("ko-KR")}, 경고 ${summary.alert_sample_count.toLocaleString("ko-KR")}`}
            />
            <Kpi
              label="관측한 점유율 변화"
              value={<Pp value={summary.observed_share_change_pp} />}
              detail={`나누어 설명한 기록 ${formatSharePercent(summary.attribution_coverage_pct)}`}
            />
            <Kpi
              label="1위 코인이 차지한 몫"
              value={formatSharePercent(
                summary.driver_breadth.top1_absolute_contribution_share_pct
              )}
              detail={`상위 5개 ${formatSharePercent(summary.driver_breadth.top5_absolute_contribution_share_pct)}, 올린 ${summary.driver_breadth.positive_driver_count}, 내린 ${summary.driver_breadth.negative_driver_count}`}
            />
          </div>
          <Table
            label="최근 24시간 자주 기여한 코인"
            head={
              <tr>
                <th>코인</th>
                <th className="mg-num">누적 기여</th>
                <th className="mg-num">등장</th>
                <th className="mg-num">같은 방향</th>
              </tr>
            }
          >
            {summary.drivers.slice(0, 8).map(driver => (
              <tr key={driver.asset}>
                <td className="mg-name">{driver.asset}</td>
                <td className="mg-num">
                  <Pp value={driver.cumulative_contribution_pp} />
                </td>
                <td className="mg-num">
                  {driver.observation_count.toLocaleString("ko-KR")}번
                </td>
                <td className="mg-num">
                  {formatSharePercent(driver.direction_consistency_pct)}
                </td>
              </tr>
            ))}
          </Table>
          <Foot>
            {formatMarketTimeMS(summary.from_ms, "date-time")} ~{" "}
            {formatMarketTimeMS(summary.to_ms, "date-time")}. 웹소켓 체결로
            계산한 값이며 공식 24시간 점유율과 맞추지 않습니다.
          </Foot>
        </>
      )}
    </Card>
  );
}
