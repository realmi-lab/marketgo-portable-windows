/*
 * What 코인 인덱스 2 says about the live coin shares, and the seconds each
 * coin's line is drawn from. Every number is the server's; these helpers only
 * name, format and keep them.
 */
import { formatKRW, formatSharePercent } from "./formatters.ts";
import type {
  MarketShareCoinShareValue,
  MarketShareLiveCoinShare,
  MarketShareLiveCoinShares,
} from "./market-share-types.ts";
import { formatMarketTimeMS } from "./market-time-format.ts";
import { windowLabel } from "./screen-copy.ts";
import {
  OBSERVED_RANGE_BASIS_LABEL,
  observedSpanNote,
} from "./selected-window-presentation.ts";

/** Why a coin's share is missing, in a few words. */
export function coinShareGapLabel(value: MarketShareCoinShareValue): string {
  switch (value.status) {
    case "NO_TRADES":
      return "거래 없음";
    case "NOT_OBSERVED":
      return "관측 없음";
    case "UNRESOLVED_FX":
      return value.unresolved_fx_count
        ? `원화 환산 전 ${value.unresolved_fx_count.toLocaleString("ko-KR")}건`
        : "원화 환산 전";
    case "UNAVAILABLE":
      return "자료 없음";
    default:
      return "확인 필요";
  }
}

/** A coin's share as the server sent it, or why it is missing. */
export function coinShareText(value: MarketShareCoinShareValue): string {
  return typeof value.bithumb_share_pct === "number"
    ? formatSharePercent(value.bithumb_share_pct)
    : coinShareGapLabel(value);
}

/** A coin's trading as the server sent it, or why it is missing. */
export function coinVolumeText(value: MarketShareCoinShareValue): string {
  return typeof value.domestic_volume_krw === "number"
    ? formatKRW(value.domestic_volume_krw)
    : coinShareGapLabel(value);
}

/**
 * One coin tile in words: "BTC: 최근 1분 국내 거래금액 ₩8.33억, 빗썸 점유율
 * 12.08%."
 */
export function coinShareSentence(
  coin: MarketShareLiveCoinShare,
  shares: MarketShareLiveCoinShares
): string {
  const share =
    typeof coin.bithumb_share_pct === "number"
      ? `빗썸 점유율 ${formatSharePercent(coin.bithumb_share_pct)}`
      : `빗썸 점유율 없음(${coinShareGapLabel(coin)})`;
  const basis = shares.window_complete ? "" : `, ${OBSERVED_RANGE_BASIS_LABEL}`;
  return `${coin.asset}: 최근 ${windowLabel(shares.window_sec)} 국내 거래금액 ${coinVolumeText(coin)}, ${share}${basis}.`;
}

/** A minute of points, one a second. */
export const LIVE_LINE_SECONDS = 60;

/** One second of the lines: each coin's share then, by asset. */
export interface LiveCoinSecond {
  /** Unix seconds. */
  second: number;
  /** Null when no frame came for this second. */
  coins: Readonly<Record<string, MarketShareLiveCoinShare>> | null;
}

/** The coins on screen and the seconds their lines are drawn from. */
export interface LiveCoinView {
  shares: MarketShareLiveCoinShares | null;
  /** The latest frame had no shares; the last ones stay on screen. */
  unavailable: boolean;
  /** The last LIVE_LINE_SECONDS seconds, oldest first, one per second. */
  seconds: LiveCoinSecond[];
}

export const EMPTY_LIVE_COIN_VIEW: LiveCoinView = {
  shares: null,
  unavailable: false,
  seconds: [],
};

function isLiveCoinShares(frame: unknown): frame is MarketShareLiveCoinShares {
  const shares = frame as Partial<MarketShareLiveCoinShares> | null;
  if (
    !shares ||
    !Number.isSafeInteger(shares.as_of_second) ||
    shares.as_of_second! <= 0 ||
    !Number.isSafeInteger(shares.window_sec) ||
    shares.window_sec! <= 0 ||
    !Number.isSafeInteger(shares.coverage_sec) ||
    shares.coverage_sec! < 0 ||
    shares.coverage_sec! > shares.window_sec! ||
    typeof shares.window_complete !== "boolean" ||
    typeof shares.ranking !== "string" ||
    !Array.isArray(shares.coins) ||
    shares.coins.length > 100
  )
    return false;
  const assets = new Set<string>();
  return shares.coins.every(coin => {
    if (
      !coin ||
      typeof coin.asset !== "string" ||
      !coin.asset.trim() ||
      assets.has(coin.asset) ||
      typeof coin.status !== "string"
    )
      return false;
    assets.add(coin.asset);
    for (const value of [
      coin.domestic_volume_krw,
      coin.bithumb_volume_krw,
      coin.bithumb_share_pct,
    ]) {
      if (
        value !== null &&
        (typeof value !== "number" || !Number.isFinite(value) || value < 0)
      )
        return false;
    }
    return coin.bithumb_share_pct === null || coin.bithumb_share_pct <= 100;
  });
}

/**
 * The view after a frame. A newer second adds a point to every line, and
 * seconds the stream skipped (a reconnect) stay empty, so each point is one
 * second. A second sent again (a replacement calculator) replaces its point,
 * so the line and the tile agree; an older one is ignored. A frame without
 * shares keeps the last ones and says so.
 */
export function nextLiveCoinView(
  view: LiveCoinView,
  frame: unknown
): LiveCoinView {
  if (!isLiveCoinShares(frame)) {
    return view.unavailable ? view : { ...view, unavailable: true };
  }
  const last = view.seconds.at(-1)?.second;
  if (last !== undefined && frame.as_of_second < last) return view;
  const again = frame.as_of_second === last;
  const from =
    last === undefined || again
      ? frame.as_of_second
      : Math.max(last + 1, frame.as_of_second - LIVE_LINE_SECONDS + 1);
  const skipped: LiveCoinSecond[] = [];
  for (let second = from; second < frame.as_of_second; second += 1) {
    skipped.push({ second, coins: null });
  }
  const coins = Object.fromEntries(frame.coins.map(coin => [coin.asset, coin]));
  return {
    shares: frame,
    unavailable: false,
    seconds: [
      ...(again ? view.seconds.slice(0, -1) : view.seconds),
      ...skipped,
      { second: frame.as_of_second, coins },
    ].slice(-LIVE_LINE_SECONDS),
  };
}

/** A coin's share each second of the view, or null where it had none. */
export function coinPoints(
  view: LiveCoinView,
  asset: string
): (MarketShareLiveCoinShare | null)[] {
  return view.seconds.map(second => second.coins?.[asset] ?? null);
}

/**
 * A coin's tooltip rows for one second: its share and the minute's trading,
 * or why there is no point (the second never came, or the coin was not
 * listed then).
 */
export function coinSecondRows(
  second: LiveCoinSecond,
  asset: string,
  windowSec: number
): { label: string; value: string }[] {
  const point = second.coins?.[asset];
  if (!point) {
    return [
      {
        label: "빗썸 점유율",
        value: second.coins ? "이때는 목록에 없음" : "이 1초는 받지 못함",
      },
    ];
  }
  return [
    { label: "빗썸 점유율", value: coinShareText(point) },
    {
      label: `${windowLabel(windowSec)} 거래금액`,
      value: coinVolumeText(point),
    },
  ];
}

/** How the page's stream stands, beside the frames it holds. */
export interface LiveCoinStream {
  /** The socket is open, though it may be between frames. */
  connected: boolean;
  /** The user paused the live figures. */
  paused: boolean;
  /** Open for a while without a new second (none at all, or none since). */
  still: boolean;
}

/**
 * The screen's header: when the shares are from, notes on the stream and on
 * coins not in official order, and the observed part of a partly observed
 * minute, kept apart because it changes every second and is not read aloud.
 */
export function coinSharesNotes(
  view: LiveCoinView,
  stream: LiveCoinStream
): { time: string; notes: string[]; coverage: string } {
  const shares = view.shares;
  if (!shares) {
    if (!view.unavailable && !stream.still) {
      return {
        time: "코인별 점유율을 기다리는 중입니다.",
        notes: [],
        coverage: "",
      };
    }
    return {
      time: "코인별 점유율을 받지 못했습니다.",
      notes: [
        view.unavailable
          ? "서버가 코인별 점유율을 계산하지 못했습니다. 계속 기다립니다."
          : "서버가 아직 실시간 코인별 점유율을 보내지 않습니다.",
      ],
      coverage: "",
    };
  }
  const at = formatMarketTimeMS(shares.as_of_second * 1000, "time-seconds");
  const notes: string[] = [];
  if (stream.paused) {
    notes.push(`일시정지해 ${at} 값에서 멈춰 있습니다.`);
  } else if (!stream.connected) {
    notes.push(
      `실시간 연결이 끊겨 ${at} 값에서 멈췄습니다. 다시 연결하는 중입니다.`
    );
  } else if (view.unavailable) {
    notes.push(`서버가 코인별 점유율을 계산하지 못해 ${at} 값을 보여 줍니다.`);
  } else if (stream.still) {
    notes.push(`${at} 뒤로 새 값이 오지 않습니다. 계속 기다립니다.`);
  }
  if (shares.ranking !== "OFFICIAL_24H_TICKER") {
    notes.push(
      "공식 24시간 자료가 없어 실시간으로 관측한 24시간 거래금액 순서로 보입니다."
    );
  }
  return {
    time: `${at} 기준이며 1초마다 새로 받습니다.`,
    notes,
    coverage: shares.window_complete
      ? ""
      : `${OBSERVED_RANGE_BASIS_LABEL} · ${observedSpanNote(shares.coverage_sec, shares.window_sec)}`,
  };
}
