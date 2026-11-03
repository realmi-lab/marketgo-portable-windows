import assert from "node:assert/strict";
import test from "node:test";

import {
  coinPoints,
  coinSecondRows,
  coinShareGapLabel,
  coinShareSentence,
  coinSharesNotes,
  EMPTY_LIVE_COIN_VIEW,
  LIVE_LINE_SECONDS,
  nextLiveCoinView,
  type LiveCoinView,
} from "./coin-shares-presentation.ts";
import type {
  MarketShareLiveCoinShare,
  MarketShareLiveCoinShares,
} from "./market-share-types.ts";
import { formatMarketTimeMS } from "./market-time-format.ts";
import { createReducerStore } from "./reducer-store.ts";

const AS_OF = 1_793_000_000;

function coin(asset: string, share: number | null): MarketShareLiveCoinShare {
  return share === null
    ? {
        asset,
        status: "NO_TRADES",
        domestic_volume_krw: 0,
        bithumb_volume_krw: 0,
        bithumb_share_pct: null,
      }
    : {
        asset,
        status: "AVAILABLE",
        domestic_volume_krw: 833_000_000,
        bithumb_volume_krw: share * 8_330_000,
        bithumb_share_pct: share,
      };
}

function frame(
  second: number,
  overrides: Partial<MarketShareLiveCoinShares> = {}
): MarketShareLiveCoinShares {
  return {
    window_sec: 60,
    as_of_second: second,
    window_complete: true,
    coverage_sec: 60,
    ranking: "OFFICIAL_24H_TICKER",
    coins: [coin("BTC", 12.08), coin("XRP", null)],
    ...overrides,
  };
}

const after = (...frames: unknown[]): LiveCoinView =>
  frames.reduce<LiveCoinView>(nextLiveCoinView, EMPTY_LIVE_COIN_VIEW);

test("says why a coin's share is missing instead of showing a zero", () => {
  const missing = (status: string, count?: number) =>
    coinShareGapLabel({
      status,
      domestic_volume_krw: null,
      bithumb_volume_krw: null,
      bithumb_share_pct: null,
      unresolved_fx_count: count,
    });
  assert.equal(missing("NO_TRADES"), "거래 없음");
  assert.equal(missing("NOT_OBSERVED"), "관측 없음");
  assert.equal(missing("UNRESOLVED_FX", 3), "원화 환산 전 3건");
  assert.equal(missing("UNAVAILABLE"), "자료 없음");
  assert.equal(missing("SOMETHING_NEW"), "확인 필요");
});

test("reads a tile aloud with the minute's trading and share", () => {
  const shares = frame(AS_OF);
  assert.equal(
    coinShareSentence(shares.coins[0], shares),
    "BTC: 최근 1분 국내 거래금액 ₩8.33억, 빗썸 점유율 12.08%."
  );
  assert.equal(
    coinShareSentence(shares.coins[1], { ...shares, window_complete: false }),
    "XRP: 최근 1분 국내 거래금액 ₩0, 빗썸 점유율 없음(거래 없음), 관측범위기준(웹소켓)."
  );
});

test("adds one point a second and leaves skipped seconds empty", () => {
  const first = after(frame(AS_OF));
  assert.deepEqual(
    first.seconds.map(second => second.second),
    [AS_OF]
  );
  assert.equal(first.shares?.as_of_second, AS_OF);
  // Two seconds the stream skipped stay empty, so each point is one second.
  const later = nextLiveCoinView(first, frame(AS_OF + 3));
  assert.deepEqual(
    later.seconds.map(second => second.second),
    [AS_OF, AS_OF + 1, AS_OF + 2, AS_OF + 3]
  );
  assert.deepEqual(
    coinPoints(later, "BTC").map(point => point?.bithumb_share_pct ?? null),
    [12.08, null, null, 12.08]
  );
  // A coin without trades keeps its point, which says why it has no share.
  assert.equal(coinPoints(later, "XRP")[3]?.status, "NO_TRADES");
  assert.deepEqual(coinPoints(later, "ETH"), [null, null, null, null]);
});

test("a second sent again replaces its point and an older one is ignored", () => {
  const view = after(frame(AS_OF), frame(AS_OF + 1));
  const again = frame(AS_OF + 1, { coins: [coin("BTC", 13)] });
  const replaced = nextLiveCoinView(view, again);
  assert.equal(replaced.shares, again);
  assert.deepEqual(
    replaced.seconds.map(second => second.second),
    [AS_OF, AS_OF + 1]
  );
  // The newest point now matches the tile, which shows the frame sent again.
  assert.equal(coinPoints(replaced, "BTC")[1]?.bithumb_share_pct, 13);
  assert.equal(coinPoints(replaced, "XRP")[1], null);
  assert.equal(nextLiveCoinView(view, frame(AS_OF - 5)), view);
});

test("a tooltip says why a second has no point", () => {
  const view = after(frame(AS_OF), frame(AS_OF + 2));
  assert.deepEqual(coinSecondRows(view.seconds[0], "BTC", 60), [
    { label: "빗썸 점유율", value: "12.08%" },
    { label: "1분 거래금액", value: "₩8.33억" },
  ]);
  assert.deepEqual(coinSecondRows(view.seconds[0], "XRP", 60), [
    { label: "빗썸 점유율", value: "거래 없음" },
    { label: "1분 거래금액", value: "₩0" },
  ]);
  assert.deepEqual(coinSecondRows(view.seconds[1], "BTC", 60), [
    { label: "빗썸 점유율", value: "이 1초는 받지 못함" },
  ]);
  assert.deepEqual(coinSecondRows(view.seconds[2], "ETH", 60), [
    { label: "빗썸 점유율", value: "이때는 목록에 없음" },
  ]);
});

test("keeps a minute, even after a long gap", () => {
  const frames = Array.from({ length: LIVE_LINE_SECONDS + 10 }, (_, index) =>
    frame(AS_OF + index)
  );
  const full = after(...frames);
  assert.equal(full.seconds.length, LIVE_LINE_SECONDS);
  assert.equal(full.seconds[0].second, AS_OF + 10);
  const gap = nextLiveCoinView(full, frame(AS_OF + 1_000));
  assert.equal(gap.seconds.length, LIVE_LINE_SECONDS);
  assert.equal(gap.seconds[0].second, AS_OF + 1_000 - LIVE_LINE_SECONDS + 1);
  assert.equal(gap.seconds.filter(second => second.coins).length, 1);
});

test("a frame without shares keeps the last ones and says so", () => {
  const view = after(frame(AS_OF));
  const failed = nextLiveCoinView(view, {
    status: "UNAVAILABLE",
    detail: "analysis read failed",
  });
  assert.equal(failed.unavailable, true);
  assert.equal(failed.shares, view.shares);
  assert.equal(failed.seconds, view.seconds);
  assert.equal(nextLiveCoinView(failed, { status: "UNAVAILABLE" }), failed);
  assert.equal(nextLiveCoinView(failed, frame(AS_OF + 1)).unavailable, false);
});

test("malformed websocket coin frames preserve the last valid values and recover", () => {
  const previous = after(frame(AS_OF));
  for (const invalid of [
    null,
    frame(NaN),
    frame(Infinity),
    frame(AS_OF + 0.5),
    frame(AS_OF + 1, { coverage_sec: 61 }),
    frame(AS_OF + 1, { coins: [null] as never }),
    frame(AS_OF + 1, { coins: [coin("BTC", 10), coin("BTC", 20)] }),
    frame(AS_OF + 1, { coins: [coin("BTC", -1)] }),
    frame(AS_OF + 1, { coins: [coin("BTC", 101)] }),
    frame(AS_OF + 1, { coins: [coin("BTC", Infinity)] }),
  ]) {
    const result = nextLiveCoinView(previous, invalid);
    assert.equal(result.unavailable, true);
    assert.equal(result.shares, previous.shares);
    assert.equal(result.seconds, previous.seconds);
    assert.equal(nextLiveCoinView(result, frame(AS_OF + 2)).unavailable, false);
  }
});

test("the header says when the shares are from and what they do not cover", () => {
  const at = formatMarketTimeMS(AS_OF * 1000, "time-seconds");
  const live = { connected: true, paused: false, still: false };
  assert.deepEqual(coinSharesNotes(after(frame(AS_OF)), live), {
    time: `${at} 기준이며 1초마다 새로 받습니다.`,
    notes: [],
    coverage: "",
  });
  const partial = after(
    frame(AS_OF, {
      window_complete: false,
      coverage_sec: 20,
      ranking: "REALTIME_24H",
    })
  );
  // The observed part changes every second, so it is not among the notes
  // read aloud.
  assert.deepEqual(coinSharesNotes(partial, live), {
    time: `${at} 기준이며 1초마다 새로 받습니다.`,
    notes: [
      "공식 24시간 자료가 없어 실시간으로 관측한 24시간 거래금액 순서로 보입니다.",
    ],
    coverage:
      "관측범위기준(웹소켓) · 일부 관측 · 관측 20초 / 필요 1분 · 누락 40초",
  });
  const failed = nextLiveCoinView(after(frame(AS_OF)), {
    status: "UNAVAILABLE",
  });
  assert.deepEqual(coinSharesNotes(failed, live).notes, [
    `서버가 코인별 점유율을 계산하지 못해 ${at} 값을 보여 줍니다.`,
  ]);
  assert.deepEqual(
    coinSharesNotes(failed, { ...live, connected: false }).notes,
    [`실시간 연결이 끊겨 ${at} 값에서 멈췄습니다. 다시 연결하는 중입니다.`]
  );
  assert.deepEqual(coinSharesNotes(failed, { ...live, paused: true }).notes, [
    `일시정지해 ${at} 값에서 멈춰 있습니다.`,
  ]);
  assert.deepEqual(coinSharesNotes(EMPTY_LIVE_COIN_VIEW, live), {
    time: "코인별 점유율을 기다리는 중입니다.",
    notes: [],
    coverage: "",
  });
  // A server without live coin shares connects but never sends one; a
  // stream whose frames stop says since when.
  assert.deepEqual(
    coinSharesNotes(EMPTY_LIVE_COIN_VIEW, { ...live, still: true }).notes,
    ["서버가 아직 실시간 코인별 점유율을 보내지 않습니다."]
  );
  assert.deepEqual(
    coinSharesNotes(after(frame(AS_OF)), { ...live, still: true }).notes,
    [`${at} 뒤로 새 값이 오지 않습니다. 계속 기다립니다.`]
  );
  assert.equal(
    coinSharesNotes({ ...EMPTY_LIVE_COIN_VIEW, unavailable: true }, live).time,
    "코인별 점유율을 받지 못했습니다."
  );
});

test("a store keeps every frame, telling readers only when its view changes", () => {
  const store = createReducerStore(EMPTY_LIVE_COIN_VIEW, nextLiveCoinView);
  let told = 0;
  const stop = store.subscribe(() => (told += 1));
  // Two frames before any reader renders: neither second is lost.
  store.publish(frame(AS_OF));
  store.publish(frame(AS_OF + 1));
  store.publish(frame(AS_OF - 1));
  assert.equal(store.getSnapshot().seconds.length, 2);
  assert.equal(told, 2);
  stop();
  store.publish(frame(AS_OF + 2));
  assert.equal(told, 2);
});
