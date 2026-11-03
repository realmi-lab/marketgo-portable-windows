import { useState } from "react";

import {
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type { MarketShareChangeAsset } from "@/lib/market-share-types";
import {
  comparisonNote,
  comparisonScope,
  comparisonUsable,
  kstHourLabel,
  windowComparisonNote,
  windowLabel,
} from "@/lib/screen-copy";
import type { ScreenProps } from "./AppShell";
import { heatStyle, LinesChart } from "./charts";
import {
  useOfficialContribution,
  useOfficialHistory,
  useRealtimeHistory,
  useWindowChanges,
} from "./data";
import { useMarketStream } from "./market-stream";
import {
  BridgeCard,
  DOMESTIC,
  ExchangeChangeCard,
  HourContributionCard,
  hourComparison,
  ShareTrendCard,
  useAnalysis,
  VersusPrevious,
  WindowShareKpi,
} from "./shared";
import {
  Card,
  Empty,
  Ex,
  Foot,
  Kpi,
  Pct,
  Pp,
  StaleNote,
  Table,
  WindowSeg,
  Won,
} from "./ui";

/** 점유율 변화: when the share moved and what moved it, window by window. */
export default function ChangeScreen(_props: ScreenProps) {
  const [hourMS, setHourMS] = useState<number | null>(null);
  const contribution = useOfficialContribution(hourMS);
  return (
    <div className="mg-page">
      <ChangeLead />
      <div className="mg-grid">
        <WindowTable />
        <BridgeCard span={6} size="l" />
        <BreadthCard />
        <AssetCard direction="up" />
        <AssetCard direction="down" />
        <ExchangeHeatCard />
        <ExchangeChangeCard span={7} size="m" />
        <OfficialHoursAgoCard />
        <ShareTrendCard
          span={12}
          size="l"
          chartHeight={380}
          selectedMS={hourMS}
          onSelect={setHourMS}
        />
        <HourContributionCard
          span={12}
          size="m"
          hour={
            hourMS && contribution.data
              ? hourComparison(contribution.data)
              : null
          }
          pending={
            !hourMS || contribution.data
              ? ""
              : contribution.loading
                ? `${kstHourLabel(hourMS)} 기여를 불러오는 중입니다.`
                : `${kstHourLabel(hourMS)} 기여를 받지 못했습니다. ${contribution.error}`
          }
          read={hourMS ? contribution : undefined}
        />
        <RecentWindowsCard />
      </div>
    </div>
  );
}

function ChangeLead() {
  const analysis = useAnalysis();
  const { windowSec, setWindowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const usable = comparisonUsable(change);
  const scope = comparisonScope(change);
  const hour = analysis?.official_ticker_1m;
  const note = windowComparisonNote(windowSec, change);
  return (
    <section className="mg-card" aria-label="점유율 변화 요약">
      <div className="mg-row" style={{ justifyContent: "space-between" }}>
        <h1 className="mg-copy mg-name">
          점유율 변화 ·{" "}
          {formatMarketTimeMS(
            analysis?.generated_at ? analysis.generated_at * 1000 : null,
            "time-seconds"
          )}{" "}
          기준
        </h1>
        <WindowSeg value={windowSec} onChange={setWindowSec} />
      </div>
      <div className="mg-kpis" style={{ marginTop: 12 }}>
        <WindowShareKpi
          comparison={<VersusPrevious windowSec={windowSec} change={change} />}
        />
        <Kpi
          label="빗썸 거래금액 효과"
          tag={scope || undefined}
          value={
            <Pp value={usable ? change?.bithumb_direct_effect_pp : null} />
          }
          detail={
            usable ? (
              <>
                빗썸 <Won value={change?.bithumb_volume_change_krw} signed />
              </>
            ) : (
              "—"
            )
          }
        />
        <Kpi
          label="경쟁 거래소 효과"
          tag={scope || undefined}
          value={<Pp value={usable ? change?.competitor_effect_pp : null} />}
          detail={
            usable ? (
              <>
                경쟁 거래소{" "}
                <Won value={change?.competitor_volume_change_krw} signed />
              </>
            ) : (
              "—"
            )
          }
        />
        <Kpi
          label="1시간 변화"
          tag="정시 3곳"
          value={<Pp value={hour?.share_delta_pp} />}
          detail={
            hour?.current
              ? `${formatMarketTimeMS(hour.current.bucket_ms, "time")} ${formatSharePercent(hour.current.bithumb_share_pct)}`
              : "—"
          }
        />
      </div>
      {/* On a wide screen the line is always there, so the summary keeps its
          height; a phone drops it while it is empty. */}
      <p className="mg-lead-note mg-muted">
        {note && change?.status !== "CONFIRMED" ? `${note}.` : ""}
      </p>
    </section>
  );
}

/** Every window side by side; choosing a row opens it below. */
function WindowTable() {
  const changes = useWindowChanges();
  const { windowSec, setWindowSec } = useMarketStream();
  const rows = changes.data?.windows ?? [];
  return (
    <Card
      span={12}
      size="xl"
      title="구간 비교 · 10초부터 12시간까지"
      note={`각 구간을 바로 앞의 같은 길이와 비교합니다. 줄을 누르면 아래 카드들이 그 구간으로 바뀝니다.${changes.data ? ` (${formatMarketTimeMS(changes.data.as_of_second * 1000, "time-seconds")} 기준)` : ""}`}
    >
      <StaleNote read={changes} />
      {changes.loading ? (
        <Empty>구간 비교를 불러오는 중입니다.</Empty>
      ) : rows.length === 0 ? (
        <Empty>구간 비교를 받지 못했습니다. {changes.error}</Empty>
      ) : (
        <Table
          label="구간 비교"
          head={
            <tr>
              <th>구간</th>
              <th className="mg-num">직전</th>
              <th className="mg-num">지금</th>
              <th className="mg-num">변화</th>
              <th className="mg-num">빗썸 거래금액</th>
              <th className="mg-num">경쟁 거래소</th>
              <th className="mg-num">거래가 몰린 코인</th>
              <th className="mg-num">같은 코인 안</th>
              <th>가장 크게 올린 · 내린 코인</th>
            </tr>
          }
        >
          {rows.map(row => (
            // The whole row answers a pointer; the button in it is the control
            // for keyboards and screen readers.
            <tr
              key={row.window_sec}
              data-selectable=""
              data-selected={row.window_sec === windowSec ? "" : undefined}
              onClick={() => setWindowSec(row.window_sec)}
            >
              <td className="mg-name">
                <button
                  type="button"
                  className="mg-link"
                  aria-pressed={row.window_sec === windowSec}
                  onClick={event => {
                    event.stopPropagation();
                    setWindowSec(row.window_sec);
                  }}
                >
                  {windowLabel(row.window_sec)}
                </button>
                {comparisonScope(row) ? (
                  <div className="mg-attn">{comparisonScope(row)}</div>
                ) : null}
              </td>
              {row.status === "UNAVAILABLE" ? (
                <td colSpan={8} className="mg-muted">
                  {comparisonNote(row)}
                </td>
              ) : (
                <>
                  <td className="mg-num">
                    <Pct value={row.previous_share_pct} />
                  </td>
                  <td className="mg-num">
                    <Pct value={row.current_share_pct} />
                  </td>
                  <td className="mg-num mg-name">
                    <Pp value={row.share_change_pp} />
                  </td>
                  <td className="mg-num">
                    <Pp value={row.bithumb_direct_effect_pp} />
                  </td>
                  <td className="mg-num">
                    <Pp value={row.competitor_effect_pp} />
                  </td>
                  <td className="mg-num">
                    <Pp value={row.mix_effect_pp} />
                  </td>
                  <td className="mg-num">
                    <Pp value={row.capture_effect_pp} />
                  </td>
                  <td className="mg-num">
                    {row.rising_assets[0] ? (
                      <>
                        {row.rising_assets[0].asset}{" "}
                        <Pp value={row.rising_assets[0].contribution_pp} />
                      </>
                    ) : null}
                    {row.rising_assets[0] && row.falling_assets[0] ? (
                      <span className="mg-muted"> · </span>
                    ) : null}
                    {row.falling_assets[0] ? (
                      <>
                        {row.falling_assets[0].asset}{" "}
                        <Pp value={row.falling_assets[0].contribution_pp} />
                      </>
                    ) : null}
                  </td>
                </>
              )}
            </tr>
          ))}
        </Table>
      )}
      <Foot>
        빗썸 거래금액과 경쟁 거래소 효과를 더하면 변화가 됩니다. 같은 변화를
        거래가 몰린 코인(코인 사이 이동)과 같은 코인 안 빗썸 비중으로도
        나눕니다. 24시간은 직전 24시간과 비교하지 않습니다.
      </Foot>
    </Card>
  );
}

function BreadthCard() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const breadth = comparisonUsable(change)
    ? analysis?.expert_brief?.breadth
    : undefined;
  const scope = comparisonScope(change);
  return (
    <Card
      span={6}
      size="l"
      title="얼마나 넓게 움직였나"
      note={`${windowLabel(windowSec)} 동안 거래된 코인 전체${scope ? ` (${scope})` : ""}`}
    >
      {!breadth || breadth.asset_count === 0 ? (
        <Empty>
          코인별 비교가 아직 없습니다. {windowComparisonNote(windowSec, change)}
        </Empty>
      ) : (
        <>
          <div className="mg-kpis">
            <Kpi
              label="거래된 코인"
              value={`${breadth.asset_count.toLocaleString("ko-KR")}개`}
            />
            <Kpi
              label="올린 코인"
              value={`${breadth.positive_asset_count.toLocaleString("ko-KR")}개`}
              detail={
                <>
                  합 <Pp value={breadth.positive_contribution_pp} />
                </>
              }
            />
            <Kpi
              label="내린 코인"
              value={`${breadth.negative_asset_count.toLocaleString("ko-KR")}개`}
              detail={
                <>
                  합 <Pp value={breadth.negative_contribution_pp} />
                </>
              }
            />
            <Kpi
              label="1위 코인이 차지한 몫"
              value={formatSharePercent(
                breadth.top_1_absolute_contribution_share_pct
              )}
              detail={`상위 5개 ${formatSharePercent(breadth.top_5_absolute_contribution_share_pct)}`}
            />
          </div>
          <Foot>
            몫은 코인별 기여의 크기(부호 무시)를 모두 더한 값 가운데 차지하는
            비율입니다.
          </Foot>
        </>
      )}
    </Card>
  );
}

function AssetCard({ direction }: { direction: "up" | "down" }) {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const all: MarketShareChangeAsset[] = comparisonUsable(change)
    ? (change?.assets ?? [])
    : [];
  const rows = all
    .filter(row =>
      direction === "up" ? row.contribution_pp > 0 : row.contribution_pp < 0
    )
    .slice(0, 12);
  const label = windowLabel(windowSec);
  const scope = comparisonScope(change);
  return (
    <Card
      span={6}
      size="l"
      title={
        direction === "up"
          ? `점유율을 올린 코인 · ${label}`
          : `점유율을 내린 코인 · ${label}`
      }
      note={`${
        direction === "up"
          ? "빗썸 거래가 늘거나 경쟁 거래소 거래가 줄어든 코인"
          : "경쟁 거래소 거래가 늘거나 빗썸 거래가 줄어든 코인"
      }${scope ? ` (${scope})` : ""}`}
    >
      {rows.length === 0 ? (
        <Empty>
          {windowComparisonNote(windowSec, change) ||
            "해당하는 코인이 없습니다."}
        </Empty>
      ) : (
        <Table
          label={direction === "up" ? "올린 코인" : "내린 코인"}
          head={
            <tr>
              <th>코인</th>
              <th className="mg-num">기여</th>
              <th className="mg-num">빗썸</th>
              <th className="mg-num">경쟁</th>
              <th className="mg-num">코인 안 빗썸 비중</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr key={row.asset}>
              <td className="mg-name">{row.asset}</td>
              <td className="mg-num">
                <Pp value={row.contribution_pp} />
              </td>
              <td className="mg-num">
                <Won value={row.bithumb_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                <Won value={row.competitor_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                {formatSharePercent(row.previous_bithumb_capture_pct)} →{" "}
                {formatSharePercent(row.current_bithumb_capture_pct)}
              </td>
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}

/** Each exchange's part of the change, for every window. */
function ExchangeHeatCard() {
  const changes = useWindowChanges();
  const rows = (changes.data?.windows ?? []).filter(
    row => row.status !== "UNAVAILABLE"
  );
  const values = rows.flatMap(row =>
    row.exchanges.map(exchange => Math.abs(exchange.contribution_pp))
  );
  // The largest cell sets the colour scale only.
  const max = Math.max(0, ...values);
  return (
    <Card
      span={12}
      size="m"
      title="거래소 × 구간 기여"
      note="빗썸 점유율 변화에 각 거래소가 보탠 몫. 색이 진할수록 큽니다."
    >
      <StaleNote read={changes} />
      {rows.length === 0 ? (
        <Empty>
          {changes.loading
            ? "구간 비교를 불러오는 중입니다."
            : !changes.data
              ? `구간 비교를 받지 못했습니다. ${changes.error}`
              : "비교할 수 있는 구간이 없습니다."}
        </Empty>
      ) : (
        <Table
          label="거래소별 구간 기여"
          head={
            <tr>
              <th>거래소</th>
              {rows.map(row => (
                <th key={row.window_sec} className="mg-num">
                  {windowLabel(row.window_sec)}
                </th>
              ))}
            </tr>
          }
        >
          {DOMESTIC.map(exchange => (
            <tr key={exchange}>
              <td>
                <Ex exchange={exchange} />
              </td>
              {rows.map(row => {
                const value =
                  row.exchanges.find(item => item.exchange === exchange)
                    ?.contribution_pp ?? null;
                return (
                  // The cell colour already says the sign, so the text keeps
                  // the plain foreground colour for contrast on any shade.
                  <td
                    key={row.window_sec}
                    className="mg-num"
                    style={heatStyle(value, max)}
                  >
                    {value === null ? (
                      <span className="mg-muted">—</span>
                    ) : (
                      formatSignedPercentagePoints(value)
                    )}
                  </td>
                );
              })}
            </tr>
          ))}
        </Table>
      )}
      <Foot>
        경쟁 거래소의 거래가 늘면 빗썸 점유율이 내려가므로 경쟁 거래소 칸은
        거래가 줄 때 양수입니다.
      </Foot>
    </Card>
  );
}

/** Official share at the latest hour and N hours before it, as published. */
function OfficialHoursAgoCard() {
  const history = useOfficialHistory(48);
  const items = history.data?.items ?? [];
  const latest = items.at(-1);
  const rows = [1, 2, 4, 6, 12, 24].map(hours => ({
    hours,
    point: items.find(
      point =>
        latest && point.bucket_ms === latest.bucket_ms - hours * 3_600_000
    ),
  }));
  return (
    <Card
      span={5}
      size="m"
      title="정시 N시간 전"
      note={
        latest
          ? `지금 ${formatMarketTimeMS(latest.bucket_ms, "time")} ${formatSharePercent(latest.bithumb_share_pct)}, 업비트·빗썸·코인원 공식 시간봉`
          : "업비트·빗썸·코인원 공식 시간봉"
      }
    >
      <StaleNote read={history} />
      {!history.data && history.error ? (
        <Empty>정시 자료를 받지 못했습니다. {history.error}</Empty>
      ) : null}
      <Table
        label="정시 N시간 전 점유율"
        head={
          <tr>
            <th>시간 전</th>
            <th>시각</th>
            <th className="mg-num">점유율</th>
          </tr>
        }
      >
        {rows.map(({ hours, point }) => (
          <tr key={hours}>
            <td className="mg-name">{hours}시간 전</td>
            <td className="mg-num">
              {point ? kstHourLabel(point.bucket_ms) : "—"}
            </td>
            <td className="mg-num">
              {point ? (
                <Pct value={point.bithumb_share_pct} />
              ) : (
                <span className="mg-muted">자료 없음</span>
              )}
            </td>
          </tr>
        ))}
      </Table>
      <Foot>
        정시 그래프에서 점을 누르면 아래에 그 시각의 1시간 기여가 나옵니다.
      </Foot>
    </Card>
  );
}

/** 10, 30 and 60-second shares over the last 30 minutes. */
function RecentWindowsCard() {
  const history = useRealtimeHistory(30);
  const items = [...(history.data?.items ?? [])].sort(
    (left, right) => left.as_of_ms - right.as_of_ms
  );
  // A window that was not fully observed is a gap in its line.
  const line = (seconds: number) =>
    items.map(item => {
      const window = item.windows.find(entry => entry.duration_sec === seconds);
      return {
        t: item.as_of_ms,
        v: window?.status === "COMPLETE" ? window.current_share_pct : null,
      };
    });
  return (
    <Card
      span={12}
      size="m"
      title="실시간 10초·30초·1분 기록"
      note="최근 30분, 10초마다 남긴 기록. 범례를 누르면 선을 끄고 켭니다."
    >
      <LinesChart
        label="최근 30분 빗썸 10초·30초·1분 점유율"
        height={300}
        series={[
          {
            key: "10",
            label: "10초",
            color: "var(--exchange-bithumb)",
            points: line(10),
            dashed: true,
          },
          { key: "30", label: "30초", color: "var(--info)", points: line(30) },
          {
            key: "60",
            label: "1분",
            color: "var(--foreground)",
            points: line(60),
          },
        ]}
      />
      <StaleNote read={history} />
      {!history.data && history.error ? (
        <Empty>기록을 받지 못했습니다. {history.error}</Empty>
      ) : null}
    </Card>
  );
}
