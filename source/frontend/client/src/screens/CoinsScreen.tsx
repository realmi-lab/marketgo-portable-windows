import { useState } from "react";

import {
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type {
  MarketShareAsset,
  MarketShareChangeAsset,
} from "@/lib/market-share-types";
import {
  comparisonNote,
  comparisonScope,
  comparisonUsable,
  windowComparisonNote,
  windowLabel,
} from "@/lib/screen-copy";
import type { ScreenProps } from "./AppShell";
import { ShareBar } from "./charts";
import { useRealtimeDay } from "./data";
import { useMarketStream } from "./market-stream";
import { OrbitNetwork } from "./OrbitNetwork";
import { NoticeCoinsCard, useAnalysis } from "./shared";
import {
  Card,
  Empty,
  Foot,
  Pct,
  Pp,
  StaleNote,
  Table,
  WindowSeg,
  Won,
} from "./ui";

/** 코인: which coins made the share, how they connect to exchanges. */
export default function CoinsScreen(_props: ScreenProps) {
  const [selected, setSelected] = useState<string | null>(null);
  return (
    <div className="mg-page">
      <div className="mg-grid">
        <NetworkCard selected={selected} onSelect={setSelected} />
        <NetworkTableCard selected={selected} onSelect={setSelected} />
        <NoticeCoinsCard />
        <ExclusiveCard />
        <HourCoinsCard />
        <FrequentCard />
      </div>
    </div>
  );
}

/** Coins orbiting the exchanges they trade on. */
function NetworkCard({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (asset: string | null) => void;
}) {
  const analysis = useAnalysis();
  const { windowSec, setWindowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const network = change?.asset_exchange_network;
  const usable = comparisonUsable(change);
  const scope = comparisonScope(change);
  const bithumbShare = new Map(
    (change?.assets ?? []).map(asset => [
      asset.asset,
      asset.current_bithumb_capture_pct,
    ])
  );
  return (
    <Card
      span={8}
      size="xl"
      title={`코인과 거래소의 연결 · ${windowLabel(windowSec)}`}
      note={`가운데 원이 거래소, 바깥 궤도가 코인이고 선 하나가 한 거래소에서의 그 코인 거래입니다.${scope ? ` (${scope})` : ""}`}
      actions={<WindowSeg value={windowSec} onChange={setWindowSec} compact />}
    >
      {network && usable && network.nodes.length ? (
        <OrbitNetwork
          network={network}
          bithumbShare={bithumbShare}
          centre={{
            label: `빗썸 ${windowLabel(windowSec)} 점유율`,
            value: formatSharePercent(change?.current_share_pct),
            detail:
              typeof change?.share_change_pp === "number"
                ? `직전보다 ${formatSignedPercentagePoints(change.share_change_pp)}`
                : "",
          }}
          selected={selected}
          onSelect={onSelect}
          height={580}
        />
      ) : (
        <Empty>
          {comparisonNote(change) ||
            (network?.unavailable_reason
              ? `연결을 그릴 수 없습니다. ${network.unavailable_reason}`
              : "연결 자료를 기다리는 중입니다.")}
        </Empty>
      )}
    </Card>
  );
}

/** The coins of the graph as a table: contribution and Bithumb's part. */
function NetworkTableCard({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (asset: string | null) => void;
}) {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  const rows: MarketShareChangeAsset[] = comparisonUsable(change)
    ? (change?.assets ?? []).slice(0, 20)
    : [];
  const scope = comparisonScope(change);
  const choose = (asset: string) => onSelect(asset === selected ? null : asset);
  return (
    <Card
      span={4}
      size="xl"
      title={`기여가 큰 코인 20개 · ${windowLabel(windowSec)}`}
      note={`막대는 그 코인 거래 중 빗썸의 몫입니다. 코인을 누르면 연결도에서 그 코인을 봅니다.${scope ? ` (${scope})` : ""}`}
    >
      {rows.length === 0 ? (
        <Empty>
          {windowComparisonNote(windowSec, change) || "코인 자료가 없습니다."}
        </Empty>
      ) : (
        <Table
          label="기여가 큰 코인"
          head={
            <tr>
              <th>코인</th>
              <th>빗썸 몫</th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr
              key={row.asset}
              data-selectable=""
              data-selected={row.asset === selected ? "" : undefined}
              onClick={() => choose(row.asset)}
            >
              <td className="mg-name">
                <button
                  type="button"
                  className="mg-link"
                  aria-pressed={row.asset === selected}
                  onClick={event => {
                    event.stopPropagation();
                    choose(row.asset);
                  }}
                >
                  {row.asset}
                </button>
              </td>
              <td style={{ minWidth: 96 }}>
                <ShareBar share={row.current_bithumb_capture_pct} max={100} />
                <span className="mg-muted mg-num">
                  {formatSharePercent(row.current_bithumb_capture_pct)}
                </span>
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

/** Coins only Bithumb lists among domestic exchanges, by Bithumb trading. */
function ExclusiveCard() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const assets = (analysis?.assets ?? []) as Array<
    Partial<MarketShareAsset> & { asset: string }
  >;
  const rows = assets
    .filter(asset => asset.exclusive_on_bithumb)
    .sort(
      (left, right) =>
        (right.bithumb_volume_krw ?? 0) - (left.bithumb_volume_krw ?? 0)
    )
    .slice(0, 10);
  const change = analysis?.change_attribution;
  const contribution = new Map(
    (comparisonUsable(change) ? (change?.assets ?? []) : []).map(asset => [
      asset.asset,
      asset.contribution_pp,
    ])
  );
  return (
    <Card
      span={6}
      size="m"
      title="빗썸 단독 상장 상위"
      note={`국내에서 빗썸에만 있는 코인, ${windowLabel(windowSec)} 빗썸 거래금액이 큰 순서`}
    >
      {rows.length === 0 ? (
        <Empty>
          {analysis
            ? "빗썸 단독 상장 코인의 거래가 없습니다."
            : "코인 자료를 받는 중입니다."}
        </Empty>
      ) : (
        <Table
          label="빗썸 단독 상장 상위"
          head={
            <tr>
              <th>코인</th>
              <th className="mg-num">빗썸 거래금액</th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr key={row.asset}>
              <td className="mg-name">{row.asset}</td>
              <td className="mg-num">
                <Won value={row.bithumb_volume_krw ?? null} />
              </td>
              <td className="mg-num">
                <Pp value={contribution.get(row.asset)} />
              </td>
            </tr>
          ))}
        </Table>
      )}
      <Foot>
        단독 상장 코인은 경쟁 거래소가 없어 빗썸 거래금액이 그대로 점유율이
        됩니다.
      </Foot>
    </Card>
  );
}

/** The official hour's coin drivers. */
function HourCoinsCard() {
  const analysis = useAnalysis();
  const hour = analysis?.official_ticker_1m;
  const rows = (hour?.drivers ?? []).slice(0, 20);
  return (
    <Card
      span={6}
      size="l"
      title="1시간 기여 상위 코인"
      note={
        hour?.previous && hour.current
          ? `${formatMarketTimeMS(hour.previous.bucket_ms, "time")} → ${formatMarketTimeMS(hour.current.bucket_ms, "time")}, 업비트·빗썸·코인원 공식 시간봉`
          : "업비트·빗썸·코인원 공식 시간봉"
      }
    >
      {rows.length === 0 ? (
        <Empty>
          {analysis
            ? "1시간 코인 기여 자료가 없습니다."
            : "1시간 코인 기여 자료를 받는 중입니다."}
        </Empty>
      ) : (
        <Table
          label="1시간 기여 상위 코인"
          head={
            <tr>
              <th>코인</th>
              <th className="mg-num">빗썸</th>
              <th className="mg-num">경쟁 거래소</th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {rows.map(row => (
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
      )}
    </Card>
  );
}

/** Coins that kept appearing in the day's ten-second samples. */
function FrequentCard() {
  const day = useRealtimeDay(5);
  const rows = (day.data?.drivers ?? []).slice(0, 12);
  return (
    <Card
      span={6}
      size="l"
      title="24시간 자주 등장한 코인"
      note="10초마다 남긴 기록에 기여 코인으로 나온 횟수와 누적 기여"
    >
      <StaleNote read={day} />
      {rows.length === 0 ? (
        <Empty>
          {day.loading
            ? "불러오는 중입니다."
            : !day.data
              ? `받지 못했습니다. ${day.error}`
              : "최근 24시간 표본 기록이 없습니다."}
        </Empty>
      ) : (
        <Table
          label="24시간 자주 등장한 코인"
          head={
            <tr>
              <th>코인</th>
              <th className="mg-num">등장</th>
              <th className="mg-num">같은 방향</th>
              <th className="mg-num">누적 기여</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr key={row.asset}>
              <td className="mg-name">{row.asset}</td>
              <td className="mg-num">
                {row.observation_count.toLocaleString("ko-KR")}번
              </td>
              <td className="mg-num">
                <Pct value={row.direction_consistency_pct} />
              </td>
              <td className="mg-num">
                <Pp value={row.cumulative_contribution_pp} />
              </td>
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}
