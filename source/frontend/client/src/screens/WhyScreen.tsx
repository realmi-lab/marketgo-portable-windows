import { useMemo } from "react";

import { exchangeLabel } from "@/lib/exchanges";
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
  largestFall,
  mixCaptureSentence,
  primaryDriver,
  recurringCoins,
  shareBridgeSentence,
  windowComparisonNote,
  windowLabel,
} from "@/lib/screen-copy";
import type { ScreenProps } from "./AppShell";
import { CoinMap } from "./charts";
import { useWindowChanges } from "./data";
import { useMarketStream } from "./market-stream";
import { HourContributionCard, useAnalysis, useTodayNotices } from "./shared";
import { Card, Empty, Foot, Pp, StaleNote, Table, WindowSeg, Won } from "./ui";

/** 원인: why the share rose or fell, split two ways and across windows. */
export default function WhyScreen(_props: ScreenProps) {
  return (
    <div className="mg-page">
      <WhyLead />
      <div className="mg-grid">
        <CoinMapCard />
        <TwoSplitsCard />
        <WindowCausesCard />
        <RecurringCard />
        <LargestFallCard />
        <EventsCard />
        <HourContributionCard span={6} size="m" />
        <LimitsCard />
      </div>
    </div>
  );
}

function WhyLead() {
  const analysis = useAnalysis();
  const { windowSec, setWindowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const label = windowLabel(windowSec);
  const note = windowComparisonNote(windowSec, change);
  return (
    <section className="mg-card mg-summary-why" aria-label="원인 요약">
      <div className="mg-row" style={{ justifyContent: "space-between" }}>
        <h1 className="mg-copy mg-name">{label} 변화는 무엇 때문인가</h1>
        <WindowSeg value={windowSec} onChange={setWindowSec} />
      </div>
      {comparisonUsable(change) && change?.share_change_pp != null ? (
        <>
          <p className="mg-copy" style={{ marginTop: 12 }}>
            {shareBridgeSentence(label, change)}
          </p>
          <p className="mg-copy">
            {mixCaptureSentence(change.mix_effect_pp, change.capture_effect_pp)}
          </p>
          <p className="mg-copy mg-muted">
            {primaryDriver(change.primary_driver).sentence}
            {note ? ` ${note}.` : ""} 계산으로 나눈 몫만 원인이라 부르고, 같은
            시간의 공지는 함께 있었다고만 적습니다.
          </p>
        </>
      ) : (
        <p className="mg-copy" style={{ marginTop: 12 }}>
          {note || "변화 자료를 기다리는 중입니다."}
        </p>
      )}
    </section>
  );
}

/** Where each coin sits: its part of the market and Bithumb's part of it. */
function CoinMapCard() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const assets: MarketShareChangeAsset[] = comparisonUsable(change)
    ? (change?.assets ?? [])
    : [];
  const scope = comparisonScope(change);
  // Coins without both shares are left off the map rather than drawn at 0.
  const points = assets
    .flatMap(asset => {
      const x = asset.current_domestic_share_pct;
      const y = asset.current_bithumb_capture_pct;
      if (typeof x !== "number" || typeof y !== "number") return [];
      return [
        {
          key: asset.asset,
          details: [
            { label: "국내 거래 중 이 코인", value: formatSharePercent(x) },
            { label: "이 코인 안 빗썸 몫", value: formatSharePercent(y) },
            {
              label: "기여",
              value: formatSignedPercentagePoints(asset.contribution_pp),
            },
          ],
          x,
          y,
          value: asset.contribution_pp,
        },
      ];
    })
    .slice(0, 60);
  return (
    <Card
      span={7}
      size="l"
      title="코인 지도"
      note={`${windowLabel(windowSec)} 기여가 큰 코인 60개${scope ? ` (${scope})` : ""}. 원이 클수록 기여가 크고, 초록은 올린 코인, 빨강은 내린 코인입니다.`}
    >
      {points.length === 0 ? (
        <Empty>
          {windowComparisonNote(windowSec, change) ||
            "지도에 놓을 코인이 없습니다."}
        </Empty>
      ) : (
        <CoinMap
          height={340}
          points={points}
          xLabel="국내 거래 중 이 코인 비중"
          yLabel="이 코인 안 빗썸 비중"
          label="코인별 국내 거래 비중과 빗썸 비중"
        />
      )}
      <Foot>
        오른쪽 위에 있는 코인일수록 시장에서 크고 빗썸이 강합니다. 이런 코인으로
        거래가 몰리면 코인 이동 효과로 빗썸 점유율이 오릅니다.
      </Foot>
    </Card>
  );
}

/** The same change split by exchange and by coin, with each split's leaders. */
function TwoSplitsCard() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const assets: MarketShareChangeAsset[] = change?.assets ?? [];
  // The three coins with the largest part, as "BTC +0.12%p · XRP -0.05%p".
  const leaders = (
    pick: (asset: MarketShareChangeAsset) => number | null | undefined
  ) =>
    assets
      .flatMap(asset => {
        const value = pick(asset);
        return typeof value === "number" && value !== 0
          ? [{ asset: asset.asset, value }]
          : [];
      })
      .sort((left, right) => Math.abs(right.value) - Math.abs(left.value))
      .slice(0, 3)
      .map(
        leader =>
          `${leader.asset} ${formatSignedPercentagePoints(leader.value)}`
      )
      .join(" · ");
  const mixLeaders = leaders(asset => asset.mix_effect_pp);
  const captureLeaders = leaders(asset => asset.capture_effect_pp);
  return (
    <Card
      span={5}
      size="l"
      title={`같은 ${windowLabel(windowSec)}, 다른 나누기`}
      note={`어느 방법으로 나눠도 합하면 같은 변화가 됩니다${comparisonScope(change) ? ` (${comparisonScope(change)})` : ""}`}
    >
      {!comparisonUsable(change) || change?.share_change_pp == null ? (
        <Empty>
          {windowComparisonNote(windowSec, change) || "변화 자료가 없습니다."}
        </Empty>
      ) : (
        <>
          <Table
            label="거래소로 나누기"
            head={
              <tr>
                <th>거래소로 나누기</th>
                <th className="mg-num">몫</th>
              </tr>
            }
          >
            <tr>
              <td>
                빗썸 거래금액{" "}
                <Won value={change.bithumb_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                <Pp value={change.bithumb_direct_effect_pp} />
              </td>
            </tr>
            <tr>
              <td>
                경쟁 거래소{" "}
                <Won value={change.competitor_volume_change_krw} signed />
              </td>
              <td className="mg-num">
                <Pp value={change.competitor_effect_pp} />
              </td>
            </tr>
          </Table>
          {typeof change.mix_effect_pp === "number" ? (
            <Table
              label="코인으로 나누기"
              head={
                <tr>
                  <th>코인으로 나누기</th>
                  <th className="mg-num">몫</th>
                </tr>
              }
            >
              <tr>
                <td>
                  거래가 몰린 코인
                  <div className="mg-muted">{mixLeaders}</div>
                </td>
                <td className="mg-num">
                  <Pp value={change.mix_effect_pp} />
                </td>
              </tr>
              <tr>
                <td>
                  같은 코인 안 빗썸 비중
                  <div className="mg-muted">{captureLeaders}</div>
                </td>
                <td className="mg-num">
                  <Pp value={change.capture_effect_pp} />
                </td>
              </tr>
            </Table>
          ) : (
            <Empty>이 구간은 코인으로 나누지 않았습니다.</Empty>
          )}
          <Foot>
            {mixCaptureSentence(change.mix_effect_pp, change.capture_effect_pp)}
          </Foot>
        </>
      )}
    </Card>
  );
}

/** Every window's change with what led it, from one observation. */
function WindowCausesCard() {
  const changes = useWindowChanges();
  const rows = changes.data?.windows ?? [];
  return (
    <Card
      span={12}
      size="xl"
      title="구간별 원인"
      note={`모든 구간을 같은 시각에 계산했습니다${changes.data ? ` (${formatMarketTimeMS(changes.data.as_of_second * 1000, "time-seconds")} 기준)` : ""}. 국내 5개 거래소 실시간 체결.`}
    >
      <StaleNote read={changes} />
      {rows.length === 0 ? (
        <Empty>
          {changes.loading
            ? "불러오는 중입니다."
            : `구간별 원인을 받지 못했습니다. ${changes.error}`}
        </Empty>
      ) : (
        <Table
          label="구간별 원인"
          head={
            <tr>
              <th>구간</th>
              <th className="mg-num">변화</th>
              <th>이끈 쪽</th>
              <th className="mg-num">코인 이동</th>
              <th className="mg-num">같은 코인 안</th>
              <th>올린 코인</th>
              <th>내린 코인</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr key={row.window_sec}>
              <td className="mg-name">{windowLabel(row.window_sec)}</td>
              {row.status === "UNAVAILABLE" ? (
                <td colSpan={6} className="mg-muted">
                  {comparisonNote(row)}
                </td>
              ) : (
                <>
                  <td className="mg-num">
                    <Pp value={row.share_change_pp} />
                  </td>
                  <td>
                    {primaryDriver(row.primary_driver).short}
                    {comparisonScope(row) ? (
                      <div className="mg-attn">{comparisonScope(row)}</div>
                    ) : null}
                  </td>
                  <td className="mg-num">
                    <Pp value={row.mix_effect_pp} />
                  </td>
                  <td className="mg-num">
                    <Pp value={row.capture_effect_pp} />
                  </td>
                  <td>
                    {row.rising_assets.map(asset => (
                      <div key={asset.asset} className="mg-line">
                        {asset.asset} <Pp value={asset.contribution_pp} />
                      </div>
                    ))}
                  </td>
                  <td>
                    {row.falling_assets.map(asset => (
                      <div key={asset.asset} className="mg-line">
                        {asset.asset} <Pp value={asset.contribution_pp} />
                      </div>
                    ))}
                  </td>
                </>
              )}
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}

/** Coins that lead several windows at once. */
function RecurringCard() {
  const changes = useWindowChanges();
  const coins = useMemo(
    () => recurringCoins(changes.data?.windows ?? []),
    [changes.data]
  );
  return (
    <Card
      span={6}
      size="m"
      title="같은 코인이 계속 원인인가"
      note="여러 구간에서 올리거나 내린 상위 3개에 든 코인"
    >
      <StaleNote read={changes} />
      {coins.length === 0 ? (
        <Empty>
          {changes.loading
            ? "구간 비교를 불러오는 중입니다."
            : !changes.data
              ? `구간 비교를 받지 못했습니다. ${changes.error}`
              : "여러 구간에 걸쳐 같은 코인이 앞에 나서지 않았습니다."}
        </Empty>
      ) : (
        <ul className="mg-list">
          {coins.map(coin => (
            <li key={coin.asset}>
              <span className="mg-name">{coin.asset}</span>
              <span>
                {coin.windows.map(window => (
                  <span key={window.window_sec} className="mg-pair">
                    {windowLabel(window.window_sec)}{" "}
                    <Pp value={window.contribution_pp} />
                  </span>
                ))}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/** The window that fell the most, and what pulled it down. */
function LargestFallCard() {
  const changes = useWindowChanges();
  const fall = largestFall(changes.data?.windows ?? []);
  return (
    <Card
      span={6}
      size="m"
      title={
        fall ? `내린 사례 · ${windowLabel(fall.window_sec)} ` : "내린 사례"
      }
      note="지금 비교한 구간 가운데 가장 많이 내린 구간"
      actions={fall ? <Pp value={fall.share_change_pp} /> : null}
    >
      <StaleNote read={changes} />
      {!fall ? (
        <Empty>
          {changes.loading
            ? "구간 비교를 불러오는 중입니다."
            : !changes.data
              ? `구간 비교를 받지 못했습니다. ${changes.error}`
              : "지금은 직전보다 내린 구간이 없습니다."}
        </Empty>
      ) : (
        <>
          <p className="mg-copy">
            {shareBridgeSentence(windowLabel(fall.window_sec), fall)}{" "}
            {primaryDriver(fall.primary_driver).sentence}
          </p>
          <Table
            label="내린 사례의 거래소별 기여"
            head={
              <tr>
                <th>거래소</th>
                <th className="mg-num">거래금액 변화</th>
                <th className="mg-num">기여</th>
              </tr>
            }
          >
            {fall.exchanges.map(row => (
              <tr key={row.exchange}>
                <td>{exchangeLabel(row.exchange)}</td>
                <td className="mg-num">
                  <Won value={row.volume_change_krw} signed />
                </td>
                <td className="mg-num">
                  <Pp value={row.contribution_pp} />
                </td>
              </tr>
            ))}
          </Table>
          <Foot>
            내리게 한 코인:{" "}
            {fall.falling_assets
              .map(
                asset =>
                  `${asset.asset} ${formatSignedPercentagePoints(asset.contribution_pp)}`
              )
              .join(" · ") || "없음"}
          </Foot>
        </>
      )}
    </Card>
  );
}

/** Today's notices next to the selected window's contribution of their coins. */
function EventsCard() {
  const analysis = useAnalysis();
  const { notices, items } = useTodayNotices();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  // Without a usable comparison there is no coin list to look a coin up in.
  const compared = comparisonUsable(change);
  const byAsset = new Map(
    (compared ? (change?.assets ?? []) : []).map(asset => [
      asset.asset,
      asset.contribution_pp,
    ])
  );
  const rows = items.filter(item => item.assets.length > 0);
  return (
    <Card
      span={6}
      size="m"
      title="사건과 함께 보기"
      note={`오늘 코인이 나온 공지와 그 코인의 ${windowLabel(windowSec)} 기여. 함께 있었다는 뜻일 뿐 원인은 아닙니다.`}
    >
      <StaleNote read={notices} />
      {rows.length === 0 ? (
        <Empty>
          {notices.loading
            ? "공지를 불러오는 중입니다."
            : !notices.data
              ? `공지를 받지 못했습니다. ${notices.error}`
              : "오늘 코인이 적힌 공지가 없습니다."}
        </Empty>
      ) : (
        <ul className="mg-list">
          {rows.slice(0, 20).map(item => (
            <li key={`${item.exchange}:${item.notice_id}`}>
              <span>
                <span className="mg-num mg-muted">
                  {formatMarketTimeMS(item.published_ts_ms, "time")}
                </span>{" "}
                {exchangeLabel(item.exchange)} · {item.title}
              </span>
              <span>
                {item.assets.map(asset => (
                  <span key={asset} className="mg-pair">
                    {asset}{" "}
                    {byAsset.has(asset) ? (
                      <Pp value={byAsset.get(asset)} />
                    ) : (
                      <span className="mg-muted">
                        {compared ? "거래 없음" : "—"}
                      </span>
                    )}
                  </span>
                ))}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function LimitsCard() {
  return (
    <Card span={12} size="fit" title="이 화면이 아직 말하지 못하는 것">
      <ul className="mg-list">
        <li>
          거래가 왜 그 코인으로 몰렸는지는 알 수 없습니다. 같은 시간의 공지는
          함께 있었다고만 적습니다.
        </li>
        <li>
          코빗·고팍스는 시간별 원화 거래금액을 공개하지 않아 정시 1시간 비교는
          3곳만 봅니다.
        </li>
        <li>24시간은 직전 24시간과 비교하지 않습니다.</li>
        <li>
          지금이나 직전 구간에 빠진 시간이 있으면 그 구간은 나누지 않습니다.
          거래 비중 0.1% 이하인 거래소 전체가 끊겼을 때만 그곳을 빼고 잠정으로
          나눕니다.
        </li>
      </ul>
    </Card>
  );
}
