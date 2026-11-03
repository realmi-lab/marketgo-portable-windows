import assert from "node:assert/strict";
import test from "node:test";

import {
  bucketLabel,
  bucketTick,
  official24hReceipt,
  comparisonNote,
  depthSentence,
  exchangeNames,
  flowStatus,
  hourDriverSentence,
  largestFall,
  mixCaptureSentence,
  phaseSentence,
  comparisonScope,
  comparisonUsable,
  kstDay,
  kstHour,
  kstHourLabel,
  kstMinute,
  ledgerStatusLabel,
  sourceStatusLabel,
  windowComparisonNote,
  primaryDriver,
  readFailureReason,
  recurringCoins,
  shareBridgeSentence,
  windowLabel,
  windowLeadSentence,
  windowShare,
} from "./screen-copy.ts";
import type { MarketShareAnalysis } from "./market-share-types.ts";

test("names every analysis window", () => {
  assert.equal(windowLabel(10), "10초");
  assert.equal(windowLabel(300), "5분");
  assert.equal(windowLabel(7_200), "2시간");
  assert.equal(windowLabel(43_200), "12시간");
  assert.equal(windowLabel(86_400), "24시간");
});

test("names exchanges in Korean, joined", () => {
  assert.equal(exchangeNames(["gopax", "korbit"]), "고팍스·코빗");
  assert.equal(exchangeNames(undefined), "");
});

test("names the one-second flow states", () => {
  assert.deepEqual(flowStatus("NORMAL"), { label: "평소", tone: "ok" });
  assert.deepEqual(flowStatus("ALERT"), { label: "경고", tone: "alert" });
  assert.deepEqual(flowStatus(undefined), { label: "준비 중", tone: "muted" });
});

test("explains provisional and missing comparisons and stays quiet when complete", () => {
  assert.equal(comparisonNote({ status: "CONFIRMED" }), "");
  assert.equal(
    comparisonNote({
      status: "PROVISIONAL",
      provisional_reason: "MINOR_OBSERVATION_GAP",
      excluded_exchanges: ["gopax"],
    }),
    "고팍스를 빼고 비교했습니다. 끊김이 있었고 거래 비중이 0.1% 이하인 곳입니다"
  );
  assert.equal(
    comparisonNote({
      status: "PROVISIONAL",
      provisional_reason: "INCOMPLETE_EXCHANGE_SET",
      excluded_exchanges: ["korbit"],
    }),
    "지금 연결이 끊긴 코빗을 빼고 비교했습니다"
  );
  assert.equal(
    comparisonNote({
      status: "PROVISIONAL",
      provisional_reason: "OBSERVED_RANGE",
      excluded_exchanges: [],
    }),
    "빠진 시간은 빼고 관측한 시간끼리 비교했습니다(관측범위기준)"
  );
  assert.match(
    comparisonNote({
      status: "UNAVAILABLE",
      unavailable_reason: "COMPARISON_WINDOW_INCOMPLETE",
    }),
    /빠진 시간이 있어/
  );
  assert.equal(comparisonNote(null), "변화 자료를 아직 받지 못했습니다");
});

test("splits a change into Bithumb and competitor effects in words", () => {
  const bridge = {
    status: "CONFIRMED",
    share_change_pp: 6.55,
    bithumb_direct_effect_pp: 4.51,
    competitor_effect_pp: 2.04,
    competitor_volume_change_krw: -1_591_000_000,
  };
  assert.equal(
    shareBridgeSentence("10분", bridge),
    "10분 변화 +6.55%p는 빗썸 거래금액 효과 +4.51%p와 경쟁 거래소 거래 감소 효과 +2.04%p로 나뉩니다."
  );
  // Without the competitors' own change the sentence does not guess its direction.
  assert.equal(
    shareBridgeSentence("10분", {
      ...bridge,
      competitor_volume_change_krw: undefined,
    }),
    "10분 변화 +6.55%p는 빗썸 거래금액 효과 +4.51%p와 경쟁 거래소 효과 +2.04%p로 나뉩니다."
  );
  assert.equal(
    shareBridgeSentence("10분", {
      ...bridge,
      status: "PROVISIONAL",
      excluded_exchanges: ["gopax"],
    }),
    "10분 변화(고팍스 제외) +6.55%p는 빗썸 거래금액 효과 +4.51%p와 경쟁 거래소 거래 감소 효과 +2.04%p로 나뉩니다."
  );
  assert.equal(
    shareBridgeSentence("10분", { ...bridge, status: "UNAVAILABLE" }),
    ""
  );
  assert.equal(
    shareBridgeSentence("10분", { ...bridge, share_change_pp: null }),
    ""
  );
});

test("names KST days and hours whatever the browser's time zone", () => {
  const ms = Date.UTC(2026, 9, 26, 15, 30); // 2026-10-27 00:30 KST
  assert.equal(kstDay(ms), "2026-10-27");
  assert.equal(kstHour(ms), "00시");
  assert.equal(kstHourLabel(ms), "10/27 00시");
  assert.equal(kstMinute(ms), "00:30");
  assert.equal(bucketTick(ms, 3_600_000), "00시");
  assert.equal(bucketTick(ms, 60_000), "00:30");
  assert.equal(bucketLabel(ms, 600_000), "10/27 00:30–00:40");
  assert.equal(bucketLabel(ms, 3_600_000), "10/27 00시");
});

test("names source and ledger states in words, never as raw codes", () => {
  assert.equal(sourceStatusLabel("STALE"), "갱신 지연");
  assert.equal(sourceStatusLabel("SOMETHING_NEW"), "상태 확인 필요");
  assert.equal(ledgerStatusLabel("BALANCED"), "맞음");
  assert.equal(ledgerStatusLabel(undefined), "상태 확인 필요");
});

test("shows a comparison only when the server confirmed it or named what it left out", () => {
  assert.equal(comparisonUsable({ status: "CONFIRMED" }), true);
  assert.equal(comparisonUsable({ status: "PROVISIONAL" }), true);
  assert.equal(comparisonUsable({ status: "UNAVAILABLE" }), false);
  assert.equal(comparisonUsable(null), false);
  assert.equal(
    comparisonScope({
      status: "PROVISIONAL",
      excluded_exchanges: ["gopax", "korbit"],
    }),
    "고팍스·코빗 제외"
  );
  assert.equal(
    comparisonScope({
      status: "PROVISIONAL",
      provisional_reason: "OBSERVED_RANGE",
      excluded_exchanges: [],
    }),
    "관측범위기준"
  );
  assert.equal(
    comparisonScope({ status: "CONFIRMED", excluded_exchanges: ["gopax"] }),
    ""
  );
  // 24 hours has no earlier window; its empty comparison is not a gap.
  assert.equal(
    windowComparisonNote(86_400, {
      status: "UNAVAILABLE",
      unavailable_reason: "COMPARISON_WINDOW_INCOMPLETE",
    }),
    "24시간은 직전 24시간과 비교하지 않습니다"
  );
  assert.match(
    windowComparisonNote(600, {
      status: "UNAVAILABLE",
      unavailable_reason: "COMPARISON_WINDOW_INCOMPLETE",
    }),
    /빠진 시간/
  );
});

test("names the selected window's share by its source and basis", () => {
  const frame = (status: string, extra: Record<string, unknown> = {}) =>
    ({
      window_sec: 600,
      context: {
        selected_window_status: status,
        selected_window_coverage_sec: status === "PARTIAL" ? 420 : 600,
        selected_window_required_sec: 600,
        data_source: {},
        ...extra,
      },
      summary: {
        domestic_volume_krw: 100,
        bithumb_volume_krw: 27,
        bithumb_share_pct: 27,
        domestic_unresolved_fx_count: 0,
      },
    }) as unknown as Partial<MarketShareAnalysis>;
  assert.deepEqual(windowShare(frame("CONFIRMED"), 60), {
    window: "10분",
    source: "실시간 5곳",
    official: false,
    share: 27,
    basis: "",
    note: "",
  });
  const partial = windowShare(frame("PARTIAL"), 600);
  assert.equal(partial.basis, "관측범위기준(웹소켓)");
  assert.match(partial.note, /일부 관측 · 관측 7분 \/ 필요 10분 · 누락 3분/);
  const official = windowShare(
    {
      ...frame("CONFIRMED"),
      window_sec: 86_400,
      context: {
        ...frame("CONFIRMED").context!,
        data_source: { official_24h_ticker_used: true },
      },
    } as Partial<MarketShareAnalysis>,
    600
  );
  assert.equal(official.source, "공식 24시간");
  assert.equal(windowShare(undefined, 600).share, null);
});

test("reads the coin mix and capture split without inventing values", () => {
  assert.equal(
    mixCaptureSentence(7.2, -0.66),
    "빗썸이 강한 코인으로 거래가 몰려 7.20%p 올렸고 같은 코인 안에서 빗썸이 가져간 비중은 0.66%p만큼 줄었습니다."
  );
  assert.equal(mixCaptureSentence(null, 1), "");
});

test("builds the briefing's lead sentences from server values only", () => {
  const hour = (ms: number) => (ms === 1 ? "새벽 2시" : "?");
  assert.equal(
    phaseSentence(
      {
        direction: "UP",
        started_at_ms: 1,
        baseline_share_pct: 18.24,
        current_share_pct: 21.98,
        change_pp: 3.74,
      },
      hour
    ),
    "정시 3곳 점유율은 새벽 2시 18.24%에서 지금 21.98%로 3.74%p 올랐습니다."
  );
  const share = {
    window: "10분",
    source: "실시간 5곳",
    official: false,
    share: 27.02,
    basis: "",
    note: "",
  };
  assert.equal(
    windowLeadSentence(
      share,
      { status: "CONFIRMED", share_change_pp: 6.55 },
      { asset: "PYBOBO", contribution_pp: 5.12 }
    ),
    "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다. 직전 10분보다 6.55%p 높았고, PYBOBO가 +5.12%p를 만들었습니다."
  );
  assert.equal(
    windowLeadSentence(
      { ...share, window: "1시간", share: 20 },
      { status: "CONFIRMED", share_change_pp: -1 },
      null
    ),
    "최근 1시간(실시간 5곳) 빗썸 점유율은 20.00%입니다. 직전 1시간보다 1.00%p 낮았습니다."
  );
  assert.equal(
    windowLeadSentence(
      share,
      {
        status: "PROVISIONAL",
        excluded_exchanges: ["upbit"],
        share_change_pp: 53,
      },
      null
    ),
    "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다. 업비트를 뺀 거래소끼리 비교하면 직전 10분보다 53.00%p 높았습니다."
  );
  assert.equal(
    windowLeadSentence(
      share,
      { status: "UNAVAILABLE", share_change_pp: 6.55 },
      { asset: "PYBOBO", contribution_pp: 5.12 }
    ),
    "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다."
  );
  assert.equal(
    windowLeadSentence({ ...share, basis: "관측범위기준(웹소켓)" }, null, null),
    "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다. 이 점유율은 관측범위기준(웹소켓)이며 구간 전체의 점유율과 다를 수 있습니다."
  );
  assert.equal(
    hourDriverSentence(
      0.39,
      {
        exchange: "upbit",
        volume_change_krw: -44_168_000_000,
        contribution_pp: 0.33,
      },
      value => `${(value / 100_000_000).toFixed(2)}억`
    ),
    "1시간 변화 +0.39%p는 업비트 거래금액이 441.68억 줄어든 효과(+0.33%p)가 가장 컸습니다."
  );
  assert.equal(
    depthSentence(27.02, 35.7, 25),
    "거래금액 점유율 27.02%보다 호가 금액 점유율 35.70%가 높습니다. 체결 건수 점유율은 25.00%입니다."
  );
});

test("moves only the selected-window basis prose to caller-owned help", () => {
  const share = {
    window: "10분",
    source: "실시간 5곳",
    official: false,
    share: 27.02,
    basis: "관측범위기준(웹소켓)",
    note: "일부 관측 · 관측 7분 / 필요 10분 · 누락 3분",
  };
  const change = {
    status: "PROVISIONAL",
    excluded_exchanges: ["gopax"],
    share_change_pp: 1.25,
  };
  const options = { basisInHelp: true };
  const sentence = windowLeadSentence(share, change, null, options);
  assert.equal(
    sentence,
    "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다. 고팍스를 뺀 거래소끼리 비교하면 직전 10분보다 1.25%p 높았습니다."
  );
  assert.match(windowLeadSentence(share, change, null), /관측범위기준\(웹소켓\)/);
  assert.equal(share.basis, "관측범위기준(웹소켓)");
  assert.equal(share.note, "일부 관측 · 관측 7분 / 필요 10분 · 누락 3분");
  assert.equal(
    windowLeadSentence({ ...share, share: null }, null, null, options),
    "최근 10분(실시간 5곳) 빗썸 점유율은 표시하지 않습니다."
  );
  assert.match(
    windowLeadSentence({ ...share, share: 0 }, null, null, options),
    /0\.00%/
  );
});

test("groups coins that lead several windows without adding their effects", () => {
  const coins = recurringCoins([
    {
      window_sec: 10,
      rising_assets: [{ asset: "PYBOBO", contribution_pp: 1 }],
      falling_assets: [{ asset: "DOOD", contribution_pp: -0.2 }],
    },
    {
      window_sec: 600,
      rising_assets: [{ asset: "PYBOBO", contribution_pp: 5 }],
      falling_assets: [{ asset: "BTC", contribution_pp: -0.3 }],
    },
    {
      window_sec: 3600,
      rising_assets: [{ asset: "PYBOBO", contribution_pp: 2 }],
      falling_assets: [{ asset: "DOOD", contribution_pp: -1 }],
    },
  ]);
  assert.deepEqual(
    coins.map(coin => [
      coin.asset,
      coin.windows.map(window => window.window_sec),
    ]),
    [
      ["PYBOBO", [10, 600, 3600]],
      ["DOOD", [10, 3600]],
    ]
  );
});

test("finds the window that fell the most and names the leading effect", () => {
  assert.deepEqual(
    largestFall([
      { share_change_pp: 1 },
      { share_change_pp: -3.7 },
      { share_change_pp: null },
      { share_change_pp: -0.4 },
    ]),
    { share_change_pp: -3.7 }
  );
  assert.equal(largestFall([{ share_change_pp: 1 }]), null);
  assert.deepEqual(primaryDriver("COMPETITOR_VOLUME_LED"), {
    short: "경쟁 거래소 거래",
    sentence: "경쟁 거래소 거래가 변화를 이끌었습니다.",
  });
  assert.equal(primaryDriver(undefined).short, "비교 불가");
  assert.equal(primaryDriver("NEW_CODE").short, "비교 불가");
});

test("says why a read failed without the request's English detail", () => {
  assert.equal(
    readFailureReason(new Error("Window changes request failed (404)")),
    "서버에 아직 이 자료가 없습니다(404)."
  );
  assert.equal(
    readFailureReason(new Error("Window changes request failed (503)")),
    "서버가 이 자료를 내지 못했습니다(503)."
  );
  assert.equal(
    readFailureReason(new Error("Window changes response is invalid")),
    "서버 응답 형식이 맞지 않습니다."
  );
  assert.equal(
    readFailureReason(new TypeError("Failed to fetch")),
    "서버에 연결하지 못했습니다."
  );
  assert.equal(
    readFailureReason(new DOMException("signal timed out", "TimeoutError")),
    "응답이 늦어 기다리지 않았습니다."
  );
  assert.equal(
    readFailureReason(new Error("선택한 날짜의 자료를 확인할 수 없습니다.")),
    "선택한 날짜의 자료를 확인할 수 없습니다."
  );
});

test("a stale official 24 hours keeps its counts but is not shown as healthy", () => {
  const counts = { ready_exchange_count: 5, expected_exchange_count: 5 };
  assert.deepEqual(official24hReceipt({ status: "LIVE", ...counts }), {
    label: "5/5곳 수신",
    ok: true,
  });
  assert.deepEqual(official24hReceipt({ status: "STALE", ...counts }), {
    label: "갱신 지연 · 5/5곳 수신",
    ok: false,
  });
  assert.deepEqual(
    official24hReceipt({
      status: "LIVE",
      ready_exchange_count: 4,
      expected_exchange_count: 5,
    }),
    { label: "4/5곳 수신", ok: false }
  );
  assert.deepEqual(official24hReceipt(undefined), {
    label: "자료 없음",
    ok: false,
  });
});
