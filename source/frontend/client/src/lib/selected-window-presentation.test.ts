import assert from "node:assert/strict";
import test from "node:test";

import {
  selectedWindowPresentation,
  type SelectedWindowInput,
} from "./selected-window-presentation.ts";

// The 1-hour window as production served it right after a calculator restart:
// 2,194 of 3,600 seconds observed, and 62 trades whose KRW conversion is not
// yet resolved, so the server withheld the share.
function partialHour(overrides: Partial<SelectedWindowInput> = {}): SelectedWindowInput {
  return {
    status: "PARTIAL",
    coverageSec: 2194,
    requiredSec: 3600,
    unresolvedFxCount: 62,
    officialMinuteSupplementUsed: false,
    domesticVolumeKrw: 115_396_025_024.64,
    bithumbVolumeKrw: 20_433_728_077.93,
    sharePct: null,
    ...overrides,
  };
}

test("a partial window still shows the amounts that were actually observed", () => {
  const view = selectedWindowPresentation(partialHour());
  assert.equal(view.showsAmounts, true);
  assert.equal(view.amountsAreComplete, false);
  // The observed part is labelled as such, with what was received and what is missing.
  assert.match(view.amountsQualifier, /일부 관측/);
  assert.match(view.amountsQualifier, /36분 34초/);
  assert.match(view.amountsQualifier, /1시간/);
  assert.match(view.amountsQualifier, /23분 26초/);
});

test("a withheld share is never computed in the browser and says why", () => {
  const view = selectedWindowPresentation(partialHour());
  assert.equal(view.sharePct, null);
  assert.equal(view.showsShare, false);
  // Dividing the two observed amounts would print 17.71%, a number the server
  // deliberately did not publish. It must not appear anywhere in the copy.
  assert.doesNotMatch(view.shareReason, /17\.7/);
  assert.match(view.shareReason, /62건/);
  assert.match(view.shareReason, /원화 환산/);
});

test("a complete window with resolved conversion shows the server's share", () => {
  const view = selectedWindowPresentation(
    partialHour({
      status: "CONFIRMED",
      coverageSec: 3600,
      unresolvedFxCount: 0,
      sharePct: 17.71,
    })
  );
  assert.equal(view.showsAmounts, true);
  assert.equal(view.amountsAreComplete, true);
  assert.equal(view.amountsQualifier, "");
  assert.equal(view.showsShare, true);
  assert.equal(view.sharePct, 17.71);
  assert.equal(view.shareReason, "");
});

test("a confirmed window with unresolved conversion explains the missing share only", () => {
  const view = selectedWindowPresentation(
    partialHour({ status: "PROVISIONAL", coverageSec: 3600 })
  );
  assert.equal(view.showsAmounts, true);
  assert.equal(view.amountsAreComplete, true);
  assert.equal(view.showsShare, false);
  assert.match(view.shareReason, /62건/);
  assert.doesNotMatch(view.shareReason, /관측/);
});

test("a partial window the server left without a share says the server did not publish one", () => {
  const withheld = selectedWindowPresentation(partialHour({ unresolvedFxCount: 0 }));
  assert.equal(withheld.showsShare, false);
  assert.match(withheld.shareReason, /일부만 관측/);
  // The server did not calculate it, so the copy must not claim the browser or
  // the product chose to hide a number.
  assert.doesNotMatch(withheld.shareReason, /표시하지 않습니다/);
  assert.equal(withheld.shareBasis, null);
});

// Production served exactly this after the conversion count fell to zero: the
// server published 28.02% for a window it had observed for 846 of 3,600 seconds.
// That is the share of what was seen, not of the hour. Product decision: it is
// shown, and always named by its basis so it is not read as the hour's share.
test("a share published for a partly observed window is shown and named by its basis", () => {
  const view = selectedWindowPresentation(
    partialHour({ unresolvedFxCount: 0, sharePct: 28.020963079413413, coverageSec: 846 })
  );
  assert.equal(view.showsShare, true);
  assert.equal(view.sharePct, 28.020963079413413);
  assert.equal(view.shareBasis, "OBSERVED_RANGE");
  assert.equal(view.shareBasisLabel, "관측범위기준(웹소켓)");
  assert.match(view.shareBasisNote, /관측범위기준\(웹소켓\)/);
  assert.match(view.shareBasisNote, /구간 전체의 점유율과 다를 수 있습니다/);
  assert.equal(view.shareReason, "");
  // The amounts stay qualified with what was received and what is missing.
  assert.equal(view.amountsAreComplete, false);
  assert.match(view.amountsQualifier, /일부 관측 · 관측 14분 6초 \/ 필요 1시간/);
});

test("a complete or supplemented window's share carries no basis label", () => {
  const complete = selectedWindowPresentation(
    partialHour({ status: "CONFIRMED", coverageSec: 3600, unresolvedFxCount: 0, sharePct: 17.7 })
  );
  assert.equal(complete.shareBasis, "WINDOW");
  assert.equal(complete.shareBasisLabel, "");
  assert.equal(complete.shareBasisNote, "");
  const supplemented = selectedWindowPresentation(
    partialHour({ unresolvedFxCount: 0, sharePct: 18.2, officialMinuteSupplementUsed: true })
  );
  assert.equal(supplemented.shareBasis, "WINDOW");
  assert.equal(supplemented.shareBasisLabel, "");
});

test("no share means no basis", () => {
  const view = selectedWindowPresentation(partialHour());
  assert.equal(view.showsShare, false);
  assert.equal(view.shareBasis, null);
  assert.equal(view.shareBasisLabel, "");
  assert.equal(view.shareBasisNote, "");
});

// A conversion that did not finish makes the denominator incomplete even inside
// the observed range, so a share the server should not have sent stays hidden.
test("an observed-range share is never shown while conversion is unresolved", () => {
  const view = selectedWindowPresentation(
    partialHour({ unresolvedFxCount: 3, sharePct: 28.02 })
  );
  assert.equal(view.showsShare, false);
  assert.equal(view.sharePct, null);
  assert.match(view.shareReason, /3건/);
});

test("official minute supplements make a partial window's published share displayable", () => {
  const supplied = selectedWindowPresentation(
    partialHour({ unresolvedFxCount: 0, sharePct: 18.2, officialMinuteSupplementUsed: true })
  );
  assert.equal(supplied.showsShare, true);
  assert.equal(supplied.sharePct, 18.2);
  assert.equal(supplied.amountsAreComplete, true);
});

test("an unavailable window shows nothing and never invents an amount", () => {
  const view = selectedWindowPresentation(
    partialHour({
      status: "UNAVAILABLE",
      coverageSec: 0,
      unresolvedFxCount: 0,
      domesticVolumeKrw: 0,
      bithumbVolumeKrw: 0,
    })
  );
  assert.equal(view.showsAmounts, false);
  assert.equal(view.showsShare, false);
  assert.match(view.shareReason, /관측값이 없어/);
});

test("observed zero coverage is not presented as a partial observation", () => {
  const view = selectedWindowPresentation(partialHour({ coverageSec: 0 }));
  assert.equal(view.showsAmounts, false);
});

test("non-finite amounts are not shown as observed values", () => {
  const view = selectedWindowPresentation(
    partialHour({ domesticVolumeKrw: Number.NaN, bithumbVolumeKrw: Number.POSITIVE_INFINITY })
  );
  assert.equal(view.showsAmounts, false);
});

test("a duration longer than required never reports a negative missing span", () => {
  const view = selectedWindowPresentation(partialHour({ coverageSec: 4000 }));
  assert.doesNotMatch(view.amountsQualifier, /-/);
});

// ── source contract for the components that render the two selected-window tabs ──
// The server's change_attribution block holds residual values when its
// comparison is UNAVAILABLE (production served current_bithumb_volume_krw = 0
// next to a domestic total of 115 billion won). Those must never be shown as
// observed amounts, so the observed-amount gate may only feed values read from
// analysis.summary or analysis.exchanges.
import { readFileSync } from "node:fs";

const marketShareSource = readFileSync(
  new URL("../pages/MarketShare.tsx", import.meta.url),
  "utf8"
);

function componentSource(name: string): string {
  const start = marketShareSource.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `missing component ${name}`);
  const next = marketShareSource.slice(start + 1).search(/\nfunction [A-Z]\w*\(/);
  return marketShareSource.slice(start, next < 0 ? undefined : start + 1 + next);
}

test("observed-amount gates never guard change_attribution values", () => {
  // JSX splits a gate and its value across lines, so the check reads the whole
  // conditional expression that follows each gate, not a single line.
  const gate = /\b(?:hasSelectedData|hasWindowData|windowView\.showsAmounts)\b/g;
  const attributionValue = /\battribution\.(?:current|previous)_[a-z_]+/;
  for (const name of [
    "SummaryHeadline",
    "FinalMarketReport",
    "ExchangeSummaryCard",
    "Overview",
  ]) {
    const source = componentSource(name);
    for (const match of source.matchAll(gate)) {
      const start = match.index ?? 0;
      // The gated branch runs from the gate to the end of its JSX expression.
      const branch = source.slice(start, start + 260);
      const expression = branch.split(/\n\s*\n|\/>|<\/(?:td|dd|span|p)>/)[0] ?? branch;
      assert.doesNotMatch(
        expression,
        attributionValue,
        `${name} shows an attribution value behind the observed-amount gate: ${expression.replace(/\s+/g, " ").slice(0, 160)}`
      );
    }
  }
});

test("interval-over-interval changes stay on the complete-window gate", () => {
  const card = componentSource("ExchangeSummaryCard");
  assert.match(card, /completeWindow && previous/);
  assert.doesNotMatch(card, /hasWindowData && previous/);
  // The change decomposition compares two intervals and is left untouched.
  const decomposition = componentSource("SummaryChangeDecomposition");
  assert.match(decomposition, /selectedWindowValuesAvailable\(analysis\)/);
  assert.doesNotMatch(decomposition, /selectedWindowView\(/);
});

test("the selected-window tabs explain a withheld share instead of only a dash", () => {
  for (const name of ["SummaryHeadline", "FinalMarketReport", "Overview"]) {
    assert.match(
      componentSource(name),
      /windowView\.shareReason/,
      `${name} does not say why the share is missing`
    );
  }
  assert.doesNotMatch(
    componentSource("SummaryHeadline"),
    /점유율은 계산할 수 없습니다/
  );
});

test("the browser never derives a share from the two observed amounts", () => {
  for (const name of ["SummaryHeadline", "FinalMarketReport", "Overview"]) {
    assert.doesNotMatch(
      componentSource(name),
      /bithumb_volume_krw\s*\/\s*[\w.]*domestic_volume_krw/,
      `${name} divides observed amounts into a share`
    );
  }
});

// ── the missing span must come from values the server actually sends ──
import { selectedWindowGapSeconds } from "./selected-window-presentation.ts";

// Production never sends context.data_source.gap_seconds, so every place that
// read it printed "공백 기록 없음" and "확인하지 못한 —은" for a window that was
// missing 47 minutes. The span is required minus observed, both of which the
// server does send.
test("the missing span is derived from the required and observed seconds", () => {
  assert.equal(selectedWindowGapSeconds(3600, 2194), 1406);
  assert.equal(selectedWindowGapSeconds(3600, 846), 2754);
  assert.equal(selectedWindowGapSeconds(3600, 3600), 0);
});

test("the missing span is never negative and never invented from bad input", () => {
  assert.equal(selectedWindowGapSeconds(3600, 4000), 0);
  assert.equal(selectedWindowGapSeconds(3600, Number.NaN), null);
  assert.equal(selectedWindowGapSeconds(Number.POSITIVE_INFINITY, 10), null);
  assert.equal(selectedWindowGapSeconds(undefined, 10), null);
  assert.equal(selectedWindowGapSeconds(3600, null), null);
});

test("no component reads the gap from a field the server does not send", () => {
  const source = readFileSync(new URL("../pages/MarketShare.tsx", import.meta.url), "utf8");
  assert.doesNotMatch(source, /data_source\.gap_seconds/);
  // A partly observed window must never be labelled as having no recorded gap.
  assert.doesNotMatch(source, /marketShareGapLabel\(context\.data_source/);
});

import { unobservedSpanSentence, currentShareSentence } from "./selected-window-presentation.ts";

test("the unobserved-span sentence names the span, or omits it, but never prints a dash", () => {
  const withSpan = unobservedSpanSentence({ gapSec: 2809, supplemented: false });
  assert.match(withSpan, /46분 49초/);
  assert.match(withSpan, /0원으로 넣지 않았습니다/);
  assert.doesNotMatch(withSpan, /—/);

  const supplemented = unobservedSpanSentence({ gapSec: 2809, supplemented: true });
  assert.match(supplemented, /공식 분 단위 자료/);
  assert.doesNotMatch(supplemented, /—/);

  for (const supplementedFlag of [true, false]) {
    const unknown = unobservedSpanSentence({ gapSec: null, supplemented: supplementedFlag });
    assert.doesNotMatch(unknown, /—/);
    assert.doesNotMatch(unknown, /null|undefined|NaN/);
  }
  assert.match(unobservedSpanSentence({ gapSec: null, supplemented: false }), /0원으로 넣지 않았습니다/);
});

test("a complete window has no unobserved-span sentence", () => {
  assert.equal(unobservedSpanSentence({ gapSec: 0, supplemented: false }), "");
});

test("the current-share sentence never ends a missing value with 입니다", () => {
  const shown = currentShareSentence({ windowLabel: "1시간", sharePercent: "17.71%" });
  assert.equal(shown, "최근 1시간 빗썸 점유율은 17.71%입니다.");
  const missing = currentShareSentence({ windowLabel: "1시간", sharePercent: null });
  assert.doesNotMatch(missing, /—/);
  assert.doesNotMatch(missing, /입니다/);
  assert.match(missing, /1시간/);
  assert.match(missing, /표시하지 않습니다|없습니다/);
});

// ── exchange mix: amounts are observations, the share is a ratio ──
function railSource(): string {
  const start = marketShareSource.indexOf("function SummaryMarketRail(");
  assert.ok(start >= 0);
  const next = marketShareSource.slice(start + 1).search(/\nfunction [A-Z]\w*\(/);
  return marketShareSource.slice(start, next < 0 ? undefined : start + 1 + next);
}

test("the exchange mix keeps observed amounts visible when the share is withheld", () => {
  const rail = railSource();
  // Both amount slots used to fall back to "계산 불가" whenever the share was
  // withheld, hiding volumes the server had observed per exchange.
  assert.doesNotMatch(rail, /money\(\w+(?:\.row)?\.volume_krw\)\s*:\s*"계산 불가"/);
  assert.doesNotMatch(rail, /available && (?:activeEntry\.)?observed && (?:activeEntry\.)?row\s*\?\s*money\(/);
  assert.match(rail, /windowView\.showsAmounts/);
});

test("nothing calls a server-withheld share uncalculable", () => {
  const rail = railSource();
  assert.doesNotMatch(rail, /계산 불가/);
});

// ── the exchange mix does not depend on a comparison ──
function summaryViewSource(): string {
  const start = marketShareSource.indexOf("function SummaryView(");
  assert.ok(start >= 0);
  const next = marketShareSource.slice(start + 1).search(/\nfunction [A-Z]\w*\(/);
  return marketShareSource.slice(start, next < 0 ? undefined : start + 1 + next);
}

test("the exchange mix reads only exchanges, so it is not held behind the comparison", () => {
  // It used to sit inside hasConfirmedDisplay, so the 1-hour and 24-hour views
  // lost the whole panel whenever the previous interval was missing, and the
  // 24-hour view lost it permanently because it never compares.
  assert.doesNotMatch(railSource(), /change_attribution|attribution\./);
  const view = summaryViewSource();
  const gated = view.match(/\{hasConfirmedDisplay && \([\s\S]*?\n      \)\}/g) ?? [];
  assert.ok(gated.length >= 1, "expected comparison-gated blocks to remain");
  for (const block of gated) {
    assert.doesNotMatch(block, /<SummaryMarketRail/, "the exchange mix is inside a comparison gate");
  }
  assert.match(view, /<SummaryMarketRail/);
});

test("panels that need the previous interval stay behind the comparison", () => {
  const view = summaryViewSource();
  const gated = (view.match(/\{hasConfirmedDisplay && \([\s\S]*?\n      \)\}/g) ?? []).join("\n");
  for (const panel of ["SummaryChangeDecomposition", "ShareBridge", "SummaryDrivers"]) {
    assert.match(gated, new RegExp(`<${panel}`), `${panel} must stay comparison-gated`);
  }
});

// The previous gate only let known statuses through. A status the browser does
// not know (a newer server, or a stale bundle) must not be described as a
// partial observation or have its amounts shown: nothing says it is one.
test("an unknown window status shows nothing and does not claim a partial observation", () => {
  for (const status of ["WARMING", "STALE", "", "confirmed", "partial", "DEGRADED"]) {
    const view = selectedWindowPresentation(partialHour({ status }));
    assert.equal(view.showsAmounts, false, `status ${JSON.stringify(status)} showed amounts`);
    assert.equal(view.showsShare, false, `status ${JSON.stringify(status)} showed a share`);
    assert.equal(view.amountsQualifier, "", `status ${JSON.stringify(status)} claimed a partial observation`);
    assert.doesNotMatch(view.shareReason, /일부/);
    assert.notEqual(view.shareReason, "");
  }
});

test("the three statuses the server produces keep their behaviour", () => {
  assert.equal(selectedWindowPresentation(partialHour({ status: "CONFIRMED", coverageSec: 3600, unresolvedFxCount: 0, sharePct: 20 })).showsShare, true);
  assert.equal(selectedWindowPresentation(partialHour({ status: "PROVISIONAL", coverageSec: 3600, sharePct: null })).showsAmounts, true);
  assert.match(selectedWindowPresentation(partialHour()).amountsQualifier, /일부 관측/);
});

import { analysisVersionLabel } from "./selected-window-presentation.ts";

// The report footer printed `data_source.bar_type · data_source.aggregation_version`.
// The server sends neither there (the version is context.aggregation_version and
// nothing sends a bar type), so production showed a footer ending in a bare " ·".
test("the footer version label comes from the value the server sends and never prints a bare separator", () => {
  assert.equal(analysisVersionLabel(75373), "계산 번호 75,373");
  assert.equal(analysisVersionLabel(0), "");
  for (const missing of [undefined, null, Number.NaN, -1, "75373" as unknown as number]) {
    assert.equal(analysisVersionLabel(missing), "", `printed something for ${String(missing)}`);
  }
});

test("no component prints data_source fields the server does not send", () => {
  assert.doesNotMatch(marketShareSource, /data_source\.bar_type/);
  assert.doesNotMatch(marketShareSource, /data_source\.aggregation_version/);
});

import { selectedWindowFromAnalysis, OBSERVED_RANGE_BASIS_LABEL } from "./selected-window-presentation.ts";

test("the lead sentence names the basis of an observed-range share and never hides it", () => {
  const sentence = currentShareSentence({
    windowLabel: "1시간",
    sharePercent: "26.06%",
    basisLabel: OBSERVED_RANGE_BASIS_LABEL,
  });
  assert.match(sentence, /점유율은 26\.06%입니다\./);
  assert.match(sentence, /관측범위기준\(웹소켓\)/);
  assert.match(sentence, /구간 전체의 점유율과 다를 수 있습니다/);
  // Without a basis the sentence is unchanged.
  assert.equal(
    currentShareSentence({ windowLabel: "1시간", sharePercent: "17.71%" }),
    "최근 1시간 빗썸 점유율은 17.71%입니다."
  );
});

test("the label is the exact text the product asked for", () => {
  assert.equal(OBSERVED_RANGE_BASIS_LABEL, "관측범위기준(웹소켓)");
});

// The perspective cards at the top of the page take the same decision from the
// analysis object, so it is made in one place.
function analysisLike(overrides: Record<string, unknown> = {}, sharePct: number | null = 26.06) {
  return {
    context: {
      selected_window_status: "PARTIAL",
      selected_window_coverage_sec: 2386,
      selected_window_required_sec: 3600,
      data_source: { official_minute_supplement_used: false },
      ...overrides,
    },
    summary: {
      domestic_volume_krw: 5.5e10,
      bithumb_volume_krw: 1.4e10,
      bithumb_share_pct: sharePct,
      domestic_unresolved_fx_count: 0,
    },
  };
}

test("the analysis adapter reads the server fields and applies the same rule", () => {
  const view = selectedWindowFromAnalysis(analysisLike() as never);
  assert.equal(view.showsShare, true);
  assert.equal(view.shareBasisLabel, "관측범위기준(웹소켓)");
  assert.match(view.amountsQualifier, /관측 39분 46초 \/ 필요 1시간 · 누락 20분 14초/);
  // A compact frame without data_source is treated as not supplemented.
  const compact = selectedWindowFromAnalysis(
    analysisLike({ data_source: undefined }) as never
  );
  assert.equal(compact.shareBasis, "OBSERVED_RANGE");
});

// ── every screen that shows a share names the basis of an observed-range one ──
function screen(name: string): string {
  const start = marketShareSource.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `missing component ${name}`);
  const next = marketShareSource.slice(start + 1).search(/\nfunction [A-Z]\w*\(/);
  return marketShareSource.slice(start, next < 0 ? undefined : start + 1 + next);
}

test("each screen that shows the selected window's share also names an observed-range basis", () => {
  for (const name of [
    "SummaryHeadline",
    "FinalMarketReport",
    "Overview",
    "ExchangeSummaryCard",
    "SummaryMarketRail",
    "SummaryExchangeCards",
    "ShareBridge",
  ]) {
    assert.match(
      screen(name),
      /shareBasis(?:Label|Note)/,
      `${name} shows a share without saying what it covers`
    );
  }
  const panel = readFileSync(
    new URL("../components/TargetExchangePerspectivePanel.tsx", import.meta.url),
    "utf8"
  );
  assert.match(panel, /windowShareBasis/);
});

test("the pages decide the share from one place", () => {
  assert.match(marketShareSource, /selectedWindowFromAnalysis\(analysis\)/);
  assert.doesNotMatch(marketShareSource, /selectedWindowPresentation\(/);
});

test("the confirmed calculation table keeps requiring a complete window", () => {
  // It is named a confirmed table, so an observed-range share does not belong in it.
  const table = screen("ExecutiveReportTable");
  assert.match(table, /shareDenominatorAvailable\(analysis\)/);
  assert.doesNotMatch(table, /shareBasis/);
});

// ── the compact frames ─────────────────────────────────────────────────────────
// The market intelligence page receives view=full, whose analysis has context,
// exchanges and liquidity but no summary. The first deployment of the labelled
// share read analysis.summary unconditionally, threw while rendering
// TargetExchangePerspectivePanel and left that page empty until it was rolled
// back. The fixture is the key set of a real production frame.
const compactFrame = {
  context: {
    market_scope: "KRW",
    window_sec: 60,
    selected_window_status: "CONFIRMED",
    selected_window_coverage_sec: 60,
    selected_window_required_sec: 60,
    data_source: {},
  },
  exchanges: [],
  liquidity: { exchanges: [] },
};

test("a compact frame without a summary never throws and shows nothing it cannot support", () => {
  const view = selectedWindowFromAnalysis(compactFrame as never);
  assert.equal(view.showsShare, false);
  assert.equal(view.showsAmounts, false);
  assert.equal(view.sharePct, null);
  assert.equal(view.shareBasisLabel, "");
  // It must not claim the window has no observations: the frame simply has no summary.
  assert.doesNotMatch(view.shareReason, /관측값이 없어/);
});

test("a frame missing context pieces does not throw either", () => {
  for (const analysis of [
    { context: { ...compactFrame.context, data_source: undefined } },
    { context: { ...compactFrame.context }, summary: undefined },
    { context: { ...compactFrame.context }, summary: null },
    { context: undefined, summary: undefined },
  ]) {
    assert.doesNotThrow(() => selectedWindowFromAnalysis(analysis as never));
  }
});

test("the perspective panel only reads the window decision from a frame that has a summary", () => {
  const panel = readFileSync(
    new URL("../components/TargetExchangePerspectivePanel.tsx", import.meta.url),
    "utf8"
  );
  assert.doesNotMatch(
    panel,
    /const windowView = selectedWindowFromAnalysis\(analysis\);/,
    "the panel calls the decision on every frame, including ones without a summary"
  );
  assert.match(panel, /analysis\.summary/);
});

test("a window with no frame yet is loading, not in doubt", () => {
  assert.equal(
    selectedWindowFromAnalysis({}).shareReason,
    "이 구간 자료를 받는 중입니다."
  );
  assert.equal(
    selectedWindowFromAnalysis({
      context: { selected_window_status: "MYSTERY" },
    } as never).shareReason,
    "이 구간의 자료 상태를 확인할 수 없어 표시하지 않습니다."
  );
});
