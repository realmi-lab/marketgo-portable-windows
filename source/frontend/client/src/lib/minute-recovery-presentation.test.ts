import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { minuteRecoveryPresentation } from "./minute-recovery-presentation.ts";

test("whole-minute replacement displays verified zero and lower official totals", () => {
  for (const amount of [0, 29, 100]) {
    const result = minuteRecoveryPresentation(
      {
        official_minute_supplement_status: "READY",
        official_minute_supplement_used: true,
        official_minute_replacement_used: true,
        official_minute_replacement_count: 1,
        official_minute_replacement_volume_krw: amount,
        official_minute_replacement_adjustment_krw: amount - 30,
        official_candle_supplement_interval_sec: 60,
      },
      3600
    );
    assert.equal(result.kind, "RECOVERED");
    assert.equal(result.volumeKRW, amount);
    assert.match(result.label, /공식 1분봉으로 대체/);
    assert.match(result.detail, /체결 상세는 미복구/);
  }
});

const recovered = {
  official_minute_supplement_status: "READY",
  official_minute_supplement_used: true,
  official_candle_supplement_interval_sec: 60,
  official_minute_supplement_volume_krw: 1234.5,
  official_minute_supplement_row_count: 3,
  official_minute_supplement_exchanges: ["bithumb", "coinone"],
};

test("labels only the backend's applied minute amount as recovered", () => {
  const result = minuteRecoveryPresentation(recovered, 3600);
  assert.equal(result.kind, "RECOVERED");
  assert.equal(result.label, "1분봉으로 보완");
  assert.equal(result.volumeKRW, 1234.5);
  assert.equal(result.rowCount, 3);
  assert.deepEqual(result.exchanges, ["bithumb", "coinone"]);
  assert.match(result.detail, /개별 체결 시각과 건수는 별도 확인이 필요합니다/);
  assert.doesNotMatch(result.detail, /모든 누락 복구|수집 완료|중복 없음/);
});

test("a ready reader or zero-valued row alone is not recovered money", () => {
  const unused = minuteRecoveryPresentation(
    {
      ...recovered,
      official_minute_supplement_used: false,
    },
    3600
  );
  assert.equal(unused.kind, "NOT_USED");
  assert.equal(unused.volumeKRW, null);
  for (const value of [0, -1, NaN, Infinity]) {
    const result = minuteRecoveryPresentation(
      {
        ...recovered,
        official_minute_supplement_volume_krw: value,
      },
      3600
    );
    assert.equal(result.kind, "INVALID");
    assert.equal(result.volumeKRW, null);
  }
});

test("an unresolved window asks for supplement verification without claiming a running job", () => {
  const waiting = minuteRecoveryPresentation(
    {
      official_minute_supplement_status: "READY",
      official_minute_supplement_used: false,
      gap_seconds: 20,
    },
    600
  );
  assert.equal(waiting.label, "분봉 보완 확인 대기");
  assert.equal(waiting.volumeKRW, null);
  assert.doesNotMatch(waiting.label, /미수집|보완 중|완료/);
  const conflict = minuteRecoveryPresentation(
    {
      official_minute_supplement_status: "READY",
      official_minute_supplement_used: false,
      official_minute_supplement_conflict_count: 1,
      gap_seconds: 20,
    },
    600
  );
  assert.equal(conflict.label, "원본 대조 필요");
});

test("requires a positive integer row count and whole one-minute source", () => {
  for (const rowCount of [undefined, 0, -1, 1.5, NaN, Infinity]) {
    assert.equal(
      minuteRecoveryPresentation(
        {
          ...recovered,
          official_minute_supplement_row_count: rowCount,
        },
        3600
      ).kind,
      "INVALID"
    );
  }
  for (const interval of [undefined, 10, 600]) {
    assert.equal(
      minuteRecoveryPresentation(
        {
          ...recovered,
          official_candle_supplement_interval_sec: interval,
        },
        3600
      ).kind,
      "INVALID"
    );
  }
});

test("short windows and ticker-only windows never acquire a minute claim", () => {
  for (const window of [10, 30]) {
    const result = minuteRecoveryPresentation(recovered, window);
    assert.equal(result.kind, "NOT_APPLICABLE");
    assert.equal(result.volumeKRW, null);
  }
  const ticker = minuteRecoveryPresentation(
    {
      official_24h_ticker_used: true,
    },
    86400
  );
  assert.equal(ticker.kind, "TICKER");
  assert.equal(ticker.volumeKRW, null);
  assert.doesNotMatch(ticker.label, /분봉 복구/);
});

test("conflicts and zero-only precision differences remain distinct", () => {
  const conflict = minuteRecoveryPresentation(
    {
      ...recovered,
      official_minute_supplement_used: false,
      official_minute_supplement_conflict_count: 2,
    },
    3600
  );
  assert.equal(conflict.kind, "CONFLICT");
  assert.equal(conflict.volumeKRW, null);
  const precision = minuteRecoveryPresentation(
    {
      ...recovered,
      official_minute_supplement_used: false,
      official_minute_supplement_status: "SOURCE_PRECISION_DIFFERENCE",
      official_minute_supplement_precision_difference_count: 1,
    },
    3600
  );
  assert.equal(precision.kind, "PRECISION_DIFFERENCE");
  assert.equal(precision.volumeKRW, null);
  assert.match(precision.label, /추가 금액 없음/);
  const mixed = minuteRecoveryPresentation(
    {
      ...recovered,
      official_minute_supplement_conflict_count: 2,
      official_minute_supplement_precision_difference_count: 1,
    },
    3600
  );
  assert.equal(mixed.kind, "RECOVERED_WITH_LIMITS");
  assert.equal(mixed.volumeKRW, 1234.5);
  assert.match(mixed.label, /일부 원본 대조 필요/);
});

test("missing, disabled, failed, or inconsistent sources remain unconfirmed", () => {
  for (const status of [undefined, "UNAVAILABLE", "DISABLED", "ERROR"]) {
    const result = minuteRecoveryPresentation(
      {
        ...recovered,
        official_minute_supplement_status: status,
      },
      3600
    );
    assert.equal(result.kind, "UNAVAILABLE");
    assert.equal(result.volumeKRW, null);
  }
  assert.equal(minuteRecoveryPresentation(undefined, 3600).kind, "UNAVAILABLE");
  assert.equal(
    minuteRecoveryPresentation(
      {
        official_minute_supplement_status: "READY",
      },
      3600
    ).kind,
    "UNAVAILABLE"
  );
  assert.equal(minuteRecoveryPresentation(recovered, NaN).kind, "INVALID");
  assert.equal(
    minuteRecoveryPresentation(
      {
        ...recovered,
        official_24h_ticker_used: true,
      },
      86400
    ).kind,
    "INVALID"
  );
});

test("new recovery copy is opt-in and does not relabel individual gap status", () => {
  const source = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const unix = readFileSync(
    new URL("../../../unix_share/src/App.tsx", import.meta.url),
    "utf8"
  );
  assert.match(source, /showMinuteRecovery = false/);
  assert.match(source, /minuteRecoveryPresentation\(/);
  assert.match(source, /선택 구간 분봉 보완/);
  assert.match(source, /거래소·종목·분/);
  assert.match(source, /recovery\.volumeKRW < 1 \? "1원 미만"/);
  assert.match(source, /최근 24시간 실시간 체결 관측/);
  assert.doesNotMatch(unix, /showMinuteRecovery/);
  assert.doesNotMatch(source, /gap\.status === "RECOVERED_CANDLE"/);
});
