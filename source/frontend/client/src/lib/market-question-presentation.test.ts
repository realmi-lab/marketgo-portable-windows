import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import type { DomesticExchange } from "./market-share-config.ts";
import { formatKrwCompact } from "./formatters.ts";
import {
  CURRENT_ASSET_TOTAL_SHARE_LABEL,
  DEFAULT_MARKET_QUESTION,
  exchangeMarketQuestionSuggestions,
  marketQuestionAbsoluteKRW,
  marketQuestionDraftReducer,
  marketQuestionEvidenceLabel,
  marketQuestionEvidenceTimestamp,
  showMarketQuestionBithumbDrivers,
  showMarketQuestionOfficialReference,
} from "./market-question-presentation.ts";

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");
const component = read("../components/HermesMarketQuestion.tsx");

test("explicit and implicit Bithumb preserve drivers while every other target is isolated", () => {
  assert.equal(showMarketQuestionBithumbDrivers(undefined), true);
  assert.equal(showMarketQuestionBithumbDrivers(undefined, "bithumb"), true);
  assert.equal(
    showMarketQuestionBithumbDrivers({ exchange: "bithumb" }, "BITHUMB"),
    true
  );
  assert.equal(
    showMarketQuestionBithumbDrivers({ exchange: " BITHUMB " }),
    true
  );
  for (const exchange of ["upbit", "coinone", "korbit", "gopax", "unknown"]) {
    assert.equal(showMarketQuestionBithumbDrivers({ exchange }), false);
    assert.equal(showMarketQuestionBithumbDrivers(undefined, exchange), false);
    assert.equal(
      showMarketQuestionBithumbDrivers({ exchange: "bithumb" }, exchange),
      false
    );
    assert.equal(
      showMarketQuestionBithumbDrivers({ exchange }, "bithumb"),
      false
    );
  }
  assert.match(
    component,
    /showMarketQuestionBithumbDrivers\(\s*targetExchange,\s*answer\?\.query.exchange\s*\)/
  );
  assert.match(component, /\{showBithumbFacts &&\s*\(\(answer.facts.drivers/);
  assert.match(
    component,
    /if \(showBithumbFacts\) \{[\s\S]*?label: "국내 전체 거래금액 변화"/
  );
  // A current snapshot has no previous value or change to show.
  assert.match(
    component,
    /answer\.query\.comparison === "CURRENT_SNAPSHOT"\s*\? cells\.filter\(cell => !cell\.comparison\)/
  );
  assert.doesNotMatch(component, /!targetExchange &&/);
});

test("main copy scopes coin metrics to Bithumb and does not label every source as official", () => {
  const page = read("../pages/MarketShareLLM.tsx");
  assert.match(page, /mainDashboard\s*\? undefined\s*: PAGE_DESCRIPTION/);
  assert.match(
    component,
    /지원 기간의 거래소 점유율·거래금액이나 빗썸 기준 코인 비중·기여/
  );
  assert.match(
    component,
    /mainDashboard \? "확인된 자료 기반" : "공식 수치 기반"/
  );
  assert.match(
    component,
    /mainDashboard \? "검증된 수치 자동 요약" : "공식 수치 자동 요약"/
  );
  assert.match(component, /providerLabel\(answer, mainDashboard\)/);
  assert.match(
    component,
    /mainDashboard\s*\? "모델 응답을 사용할 수 없어 서버가 검증한 자료로 만든 자동 요약을 표시합니다\."/
  );
  const catalog = read("../../../../internal/marketquestion/catalog.go");
  for (const metric of [
    "MetricAssetCombined",
    "MetricAssetBithumbCapture",
    "MetricAssetShareWithinBithumb",
    "MetricAssetTotalContribution",
  ]) {
    assert.match(
      catalog,
      new RegExp(`ID:\\s*${metric},[^}]+Exchange:\\s*ExchangeEmptyOrBithumb`)
    );
  }
});

test("main evidence dates use Korea time across a UTC day boundary and reject invalid dates", () => {
  assert.equal(
    marketQuestionEvidenceTimestamp(Date.parse("2026-09-03T15:15:00Z")),
    "2026. 09. 04. 00:15 한국시간 기준"
  );
  for (const value of [0, -1, NaN, Infinity, 8.64e15 + 1]) {
    assert.equal(marketQuestionEvidenceTimestamp(value), "기준 시각 확인 필요");
  }
  assert.match(
    component,
    /mainDashboard\s*\? marketQuestionEvidenceTimestamp\(answer\.provider\.as_of_ms\)\s*: answerTimestamp\(answer\.provider\.as_of_ms\)/
  );
});

test("only complete current official snapshots omit duplicate ticker reference cards", () => {
  const currentLayer = "OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER";
  const complete = { current_volume_krw: 125, current_share_pct: 12.5 };
  assert.equal(
    showMarketQuestionOfficialReference(currentLayer, complete),
    false
  );
  assert.equal(
    showMarketQuestionOfficialReference(currentLayer, {
      current_volume_krw: 0,
      current_share_pct: 0,
    }),
    false
  );
  for (const current of [
    { current_volume_krw: null, current_share_pct: null },
    { current_volume_krw: 125, current_share_pct: null },
    { current_volume_krw: null, current_share_pct: 12.5 },
    { current_volume_krw: NaN, current_share_pct: 12.5 },
  ]) {
    assert.equal(
      showMarketQuestionOfficialReference(currentLayer, current),
      true
    );
  }
  for (const layer of [
    "WEBSOCKET_DIRECT_OBSERVATION",
    "WEBSOCKET_HISTORICAL_RANGE",
    "OFFICIAL_ALIGNED_24H_CURRENT",
    "UNKNOWN",
  ]) {
    assert.equal(showMarketQuestionOfficialReference(layer, complete), true);
  }
  assert.match(
    component,
    /!mainDashboard \|\|\s*\(targetExchange != null &&\s*showMarketQuestionOfficialReference\(\s*answer\.facts\.evidence_layer,\s*targetExchange\s*\)\)/
  );
  assert.match(
    component,
    /if \(showOfficialReference\) \{[\s\S]*?현재 공식 24시간 거래금액[\s\S]*?공식 24시간 국내 비중/
  );
  // The server builds current official values directly from the same ticker,
  // while Bithumb skips enrichment; a partial non-Bithumb denominator retains
  // the separately available ticker amount. Do not hide that partial evidence.
  const server = read("../../../../internal/app/market_question_v2.go");
  assert.match(
    server,
    /exchangeName == "" \|\| exchangeName == string\(domain\.ExchangeBithumb\) \{\s*return target/
  );
  assert.match(
    server,
    /target\.Official24hVolumeKRW = copyQuestionFloat\(snapshot\.VolumeKRW\)/
  );
  assert.match(
    server,
    /facts\.TargetExchange\.CurrentVolumeKRW = copyQuestionFloat\(snapshot\.VolumeKRW\)/
  );
  assert.match(
    read("../../../../internal/app/market_question_current_official_test.go"),
    /TestCurrentKorbitTickerVolumeSurvivesIncompleteDomesticDenominator/
  );
});

test("every emitted market evidence layer has a specific, truthful label", () => {
  const layers = new Set<string>(["DOMESTIC_OFFICIAL_NOTICE_7D_SNAPSHOT"]);
  for (const path of [
    "../../../../internal/app/market_question_v2.go",
    "../../../../internal/app/market_question_historical.go",
    "../../../../internal/app/market_question_daily_series.go",
  ]) {
    for (const match of read(path).matchAll(
      /EvidenceLayer:\s*"([A-Z_0-9]+)"/g
    )) {
      layers.add(match[1]);
    }
  }
  assert.equal(layers.size, 9);
  const labels = [...layers].map(marketQuestionEvidenceLabel);
  assert.equal(new Set(labels).size, layers.size);
  for (const label of labels) {
    assert.notEqual(label, "자료 종류 확인 필요");
    assert.notEqual(label, "현재 공식 24시간 자료");
  }
  assert.match(
    marketQuestionEvidenceLabel("WEBSOCKET_HISTORICAL_RANGE"),
    /과거.*체결/
  );
  assert.match(
    marketQuestionEvidenceLabel("OFFICIAL_ALIGNED_KST_CALENDAR_DAY"),
    /한국 날짜.*하루 마감/
  );
  assert.match(
    marketQuestionEvidenceLabel("OFFICIAL_ALIGNED_DAILY_ROLLING_24H_ENDPOINTS"),
    /날짜별.*24시간/
  );
  for (const layer of layers) {
    if (layer.startsWith("OFFICIAL_ALIGNED_")) {
      assert.match(marketQuestionEvidenceLabel(layer), /업비트·빗썸·코인원/);
    }
  }
  assert.equal(
    marketQuestionEvidenceLabel("UNRECOGNIZED_LAYER"),
    "자료 종류 확인 필요"
  );
  assert.equal(marketQuestionEvidenceLabel(""), "자료 종류 확인 필요");
  assert.match(
    component,
    /marketQuestionEvidenceLabel\(answer\.facts\.evidence_layer\)/
  );
});

test("absolute turnover has no change sign while deltas keep their sign", () => {
  assert.equal(marketQuestionAbsoluteKRW(638_000_000), "₩6.38억");
  assert.equal(marketQuestionAbsoluteKRW(12_345), "₩1.2만");
  assert.equal(marketQuestionAbsoluteKRW(0), "₩0");
  for (const value of [NaN, Infinity, -Infinity]) {
    assert.equal(marketQuestionAbsoluteKRW(value), "—");
  }
  assert.equal(formatKrwCompact(638_000_000), "+₩6.38억");
  for (const field of [
    "previous_volume_krw",
    "current_volume_krw",
    "official_24h_volume_krw",
  ]) {
    assert.match(component, new RegExp(`amount\\(target\\.${field},`));
    assert.doesNotMatch(component, new RegExp(`change\\(target\\.${field},`));
  }
  assert.match(
    component,
    /const amount = [^;]*marketQuestionAbsoluteKRW\(value\);/
  );
  assert.match(component, /const change = [^;]*formatKrwCompact\(value\);/);
  assert.match(component, /change\(target\.volume_delta_krw,/);
});

test("current coin contribution names its domestic-total denominator", () => {
  assert.equal(
    CURRENT_ASSET_TOTAL_SHARE_LABEL,
    "국내 전체 거래금액 중 빗썸의 이 코인 거래 비중"
  );
  assert.match(
    component,
    /CURRENT_ASSET_TOTAL_SHARE_LABEL[\s\S]*?current_total_share_contribution_pct/
  );
  assert.doesNotMatch(component, /현재 빗썸 점유율 기여 중 이 코인 비중/);
});

test("main recommendations follow all five domestic exchange selections", () => {
  const names: Record<DomesticExchange, string> = {
    bithumb: "빗썸",
    upbit: "업비트",
    coinone: "코인원",
    korbit: "코빗",
    gopax: "고팍스",
  };
  for (const exchange of Object.keys(names) as DomesticExchange[]) {
    const suggestions = exchangeMarketQuestionSuggestions(exchange);
    assert.equal(suggestions.length, exchange === "bithumb" ? 6 : 5);
    assert.equal(
      new Set(suggestions.map(item => item.key)).size,
      suggestions.length
    );
    for (const suggestion of suggestions) {
      assert.ok(suggestion.label.includes(names[exchange]));
      assert.ok(suggestion.question.includes(names[exchange]));
      for (const [other, name] of Object.entries(names)) {
        if (other !== exchange) assert.ok(!suggestion.question.includes(name));
      }
      assert.ok(suggestion.question.length <= 500);
    }
    assert.equal(
      suggestions.filter(item => item.key.startsWith("compare-")).length,
      1
    );
    assert.ok(suggestions.some(item => item.key === "notice-context"));
    assert.match(
      suggestions.find(item => item.key === "notice-context")!.question,
      /공지.*인과관계.*단정하지/
    );
    if (exchange === "bithumb") {
      // This wording is also a Go parser fixture: asking for both denominators
      // must not be narrowed by the single-metric "빗썸 비중" shortcut.
      assert.equal(
        suggestions.find(item => item.key === "asset-capture")!.question,
        "현재 BTC의 빗썸 거래금액을 BTC의 국내 전체 거래금액, 빗썸 전체 거래금액과 각각 비교해 두 비중을 구분해 설명해 주세요."
      );
      assert.match(
        suggestions.find(item => item.key === "orderbook")!.question,
        /BTC.*호가/
      );
    } else {
      assert.match(
        suggestions.find(item => item.key === "monthly-high")!.question,
        /최근 한 달.*점유율.*높았던/
      );
      assert.ok(
        suggestions.every(item => !/BTC|호가|코인 비중/.test(item.question))
      );
    }
  }
  assert.equal(
    exchangeMarketQuestionSuggestions("bithumb")[0].question,
    DEFAULT_MARKET_QUESTION
  );
});

test("exchange-specific examples fall back safely while free-form coin questions stay unchanged", () => {
  const example = exchangeMarketQuestionSuggestions("bithumb").find(
    item => item.key === "asset-capture"
  );
  assert.ok(example);
  const selected = marketQuestionDraftReducer(
    { question: "", suggestionKey: null },
    { type: "suggestion", suggestion: example }
  );
  const switched = marketQuestionDraftReducer(selected, {
    type: "exchange",
    exchange: "korbit",
  });
  assert.equal(switched.suggestionKey, "diagnosis");
  assert.equal(
    switched.question,
    exchangeMarketQuestionSuggestions("korbit")[0].question
  );
  const custom = marketQuestionDraftReducer(selected, {
    type: "edit",
    question: "빗썸 BTC 비중은 어느 정도야?",
  });
  assert.strictEqual(
    marketQuestionDraftReducer(custom, {
      type: "exchange",
      exchange: "korbit",
    }),
    custom
  );
});

test("exchange switches update untouched defaults and preserve the chosen example period", () => {
  let draft = {
    question: DEFAULT_MARKET_QUESTION,
    suggestionKey: "diagnosis" as string | null,
  };
  draft = marketQuestionDraftReducer(draft, {
    type: "exchange",
    exchange: "upbit",
  });
  assert.equal(
    draft.question,
    exchangeMarketQuestionSuggestions("upbit")[0].question
  );
  draft = marketQuestionDraftReducer(draft, {
    type: "suggestion",
    suggestion: exchangeMarketQuestionSuggestions("upbit")[2],
  });
  draft = marketQuestionDraftReducer(draft, {
    type: "exchange",
    exchange: "korbit",
  });
  assert.equal(draft.suggestionKey, "compare-30");
  assert.match(draft.question, /최근 30분 동안 코빗/);
});

test("custom and cleared drafts survive exchange changes and an earlier answer completing", () => {
  const initial = {
    question: DEFAULT_MARKET_QUESTION,
    suggestionKey: "diagnosis",
  };
  for (const question of [
    "지난 17분 XRP는 어땠어?",
    "현재 고팍스 거래금액은?",
    "",
  ]) {
    const custom = marketQuestionDraftReducer(initial, {
      type: "edit",
      question,
    });
    assert.equal(custom.suggestionKey, null);
    assert.strictEqual(
      marketQuestionDraftReducer(custom, {
        type: "exchange",
        exchange: "coinone",
      }),
      custom
    );
    assert.strictEqual(
      marketQuestionDraftReducer(custom, { type: "completed" }),
      custom
    );
    assert.equal(custom.question, question);
  }
  const nextExample = marketQuestionDraftReducer(initial, {
    type: "suggestion",
    suggestion: exchangeMarketQuestionSuggestions("gopax")[3],
  });
  assert.strictEqual(
    marketQuestionDraftReducer(nextExample, { type: "completed" }),
    nextExample
  );
});

test("main-only draft state is wired to the component and free questions pass through unchanged", () => {
  assert.match(component, /useReducer\(\s*marketQuestionDraftReducer/);
  assert.match(
    component,
    /if \(mainDashboard && !hasStandaloneAnswer\) \{\s*dispatchDraft\(\{ type: "exchange", exchange: selectedExchange \}\)/
  );
  assert.match(
    component,
    /if \(mainDashboard\) \{\s*dispatchDraft\(\{ type: "completed" \}\);\s*\} else \{\s*setQuestion\(task\.question\)/
  );
  assert.match(component, /type: "suggestion", suggestion: item/);
  assert.match(
    component,
    /onChange=\{event => setQuestion\(event\.target\.value\)\}/
  );
  assert.match(component, /const normalized = question\.trim\(\)/);
  assert.match(
    component,
    /startTask\(normalized, history, conversationScope\)/
  );
  assert.match(
    component,
    /task\.status !== "idle"\s*\? task\.question\s*: loadMarketQuestionConversation\(\)\.at\(-1\)\?\.question/
  );
  const page = read("../pages/MarketShareLLM.tsx");
  assert.match(page, /mainDashboard = false/);
  assert.match(page, /mainDashboard=\{mainDashboard\}/);
  assert.match(page, /selectedExchange="bithumb"/);
  assert.doesNotMatch(read("../../../unix_share/src/App.tsx"), /mainDashboard/);
  assert.doesNotMatch(read("../StandaloneApp.tsx"), /mainDashboard/);
});

test("single-response answers skip the staged contract headings", () => {
  // Without an evidence bundle there is no fulfillment or binding state to
  // explain, and the staged-contract wording reads as jargon.
  assert.match(
    component,
    /\{!evidenceBinding\?\.legacy && \(\s*<header className="hermes-market-result-heading">/
  );
  assert.match(
    component,
    /\{evidenceBinding && !evidenceBinding\.legacy && \(/
  );
  assert.match(
    component,
    /\{explanation\?\.state !== "legacy" && \(\s*<header className="hermes-market-result-heading">/
  );
});
