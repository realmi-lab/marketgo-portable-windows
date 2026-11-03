import { useEffect, useState } from "react";

import {
  coinPoints,
  coinSecondRows,
  coinShareSentence,
  coinSharesNotes,
  coinShareText,
  coinVolumeText,
  LIVE_LINE_SECONDS,
  type LiveCoinStream,
  type LiveCoinView,
} from "@/lib/coin-shares-presentation";
import type {
  MarketShareLiveCoinShare,
  MarketShareLiveCoinShares,
} from "@/lib/market-share-types";
import type { ScreenProps } from "./AppShell";
import { LiveLine } from "./charts";
import { useLiveCoinView, useMarketStream } from "./market-stream";
import { Card, Empty, exchangeColor } from "./ui";

/** Every line spans 0 to 100%, so the tiles share one scale. */
const SHARE_SCALE = [0, 100] as const;
/**
 * How long an open stream may bring no new second before the page says so:
 * none at all (a calculator without live coin shares) or none since the last.
 */
const STILL_MS = 10_000;

/**
 * 코인 인덱스 2: Bithumb's share of each of the most traded coins in the last
 * minute, sent over the WebSocket every second, with a live line per coin.
 */
export default function CoinSharesScreen(_props: ScreenProps) {
  const view = useLiveCoinView();
  const { connection, livePaused } = useMarketStream();
  // A tick error marks the stream stale while its coin frames still arrive,
  // so only a closed or reopening socket counts as cut off.
  const connected = connection !== "offline" && connection !== "connecting";
  const shares = view.shares;
  const still = useStill(
    shares?.as_of_second ?? null,
    connected && !livePaused && !view.unavailable
  );
  const seconds = view.seconds.map(second => second.second);
  return (
    <div className="mg-page">
      <Card
        size="fit"
        title="코인별 실시간 빗썸 점유율"
        note={`24시간 거래금액 상위${shares ? ` ${shares.coins.length}개` : ""} 코인의 최근 1분 거래금액과 빗썸 점유율입니다. 선의 높이는 0~100%입니다.`}
      >
        <SharesNotes
          view={view}
          stream={{ connected, paused: livePaused, still }}
        />
      </Card>
      {shares?.coins.length ? (
        <ol
          className="mg-coin-grid"
          aria-label={`코인 ${shares.coins.length}개`}
        >
          {shares.coins.map(coin => (
            <CoinTile
              key={coin.asset}
              coin={coin}
              shares={shares}
              view={view}
              seconds={seconds}
            />
          ))}
        </ol>
      ) : shares ? (
        <Empty>아직 거래가 관측된 코인이 없습니다.</Empty>
      ) : null}
    </div>
  );
}

/** True once `second` has stayed the same for STILL_MS while `watching`. */
function useStill(second: number | null, watching: boolean): boolean {
  const [stillAt, setStillAt] = useState<number | null | undefined>();
  useEffect(() => {
    if (!watching) return;
    const timer = setTimeout(() => setStillAt(second), STILL_MS);
    return () => clearTimeout(timer);
  }, [second, watching]);
  return watching && stillAt === second;
}

/**
 * When the shares are from, then one line kept for notes (a stopped stream,
 * coins not in official order, a partly observed minute), so the tiles below
 * do not move when a note comes or goes. Only the notes are read aloud; the
 * observed part of the minute changes every second and each tile says it.
 */
function SharesNotes({
  view,
  stream,
}: {
  view: LiveCoinView;
  stream: LiveCoinStream;
}) {
  const { time, notes, coverage } = coinSharesNotes(view, stream);
  const line = [...notes, coverage].filter(Boolean).join(" ");
  return (
    <div className="mg-coin-meta">
      <p className="mg-muted">{time}</p>
      <p
        className="mg-attn mg-coin-note"
        title={line || undefined}
        aria-hidden="true"
      >
        {line}
      </p>
      <p className="mg-sr-only" role="status">
        {notes.join(" ")}
      </p>
    </div>
  );
}

/**
 * One coin: its name, the minute's trading and Bithumb's share in one line,
 * then its live line, which a pointer, a tap or the arrow keys read second
 * by second.
 */
function CoinTile({
  coin,
  shares,
  view,
  seconds,
}: {
  coin: MarketShareLiveCoinShare;
  shares: MarketShareLiveCoinShares;
  view: LiveCoinView;
  seconds: readonly number[];
}) {
  return (
    <li className="mg-coin">
      <span className="mg-sr-only">{coinShareSentence(coin, shares)}</span>
      <p className="mg-coin-head" aria-hidden="true">
        <strong className="mg-coin-name">{coin.asset}</strong>
        {/* A missing amount's reason is the share's, said once. */}
        {typeof coin.domestic_volume_krw === "number" ? (
          <span className="mg-coin-volume">{coinVolumeText(coin)}</span>
        ) : null}
        <span className="mg-coin-share">{coinShareText(coin)}</span>
      </p>
      <LiveLine
        label={`${coin.asset} 최근 1분 빗썸 점유율 흐름`}
        seconds={seconds}
        values={coinPoints(view, coin.asset).map(
          point => point?.bithumb_share_pct ?? null
        )}
        rows={index =>
          coinSecondRows(view.seconds[index], coin.asset, shares.window_sec)
        }
        span={LIVE_LINE_SECONDS}
        color={exchangeColor("bithumb")}
        domain={SHARE_SCALE}
      />
    </li>
  );
}
