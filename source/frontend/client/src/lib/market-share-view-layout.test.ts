import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { shouldShowExchangeRegistry } from "./market-share-view-layout.ts";

test("hides the exchange registry only on the combined summary surface", () => {
  assert.equal(shouldShowExchangeRegistry("summary"), false);
  assert.equal(shouldShowExchangeRegistry("detail"), true);
});

test("places the exchange overview directly below the contribution cards", () => {
  const source = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const summaryStart = source.indexOf("function SummaryView(");
  const summaryEnd = source.indexOf("type DriverView", summaryStart);
  const summaryView = source.slice(summaryStart, summaryEnd);
  const contributionCards = summaryView.indexOf("<SummaryHeadline");
  const exchangeOverview = summaryView.indexOf("<SummaryExchangeCards");
  const changeDetail = summaryView.indexOf("<SummaryChangeDecomposition");

  assert.ok(contributionCards >= 0);
  assert.ok(exchangeOverview > contributionCards);
  assert.ok(changeDetail > exchangeOverview);
});

test("keeps time-window flow off the summary page and on data quality", () => {
  const source = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const summaryStart = source.indexOf("function SummaryView(");
  const summaryEnd = source.indexOf("type DriverView", summaryStart);
  const qualityStart = source.indexOf("function QualityView(");
  const qualityEnd = source.indexOf(
    "export default function MarketShare(",
    qualityStart
  );

  assert.doesNotMatch(
    source.slice(summaryStart, summaryEnd),
    /<SummaryTimePersistence/
  );
  assert.match(
    source.slice(qualityStart, qualityEnd),
    /<SummaryTimePersistence/
  );
});

test("quality screen separates arithmetic checks from actual 24h observation and supplement conflicts", () => {
  const source = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const quality = source.slice(source.indexOf("function QualityView("));
  assert.match(quality, /report=\{quality\.numeric_integrity\}/);
  assert.match(quality, /components\.history_24h\.coverage_sec/);
  assert.match(quality, /components\.history_24h\.gap_seconds/);
  assert.match(quality, /official_minute_supplement_conflict_count/);
  assert.match(quality, /보완 합계에서 제외/);
  assert.match(
    quality,
    /official_minute_supplement_precision_difference_count/
  );
  assert.match(quality, /선택 구간 공식 분봉의 소수 정밀도 차이/);
  assert.match(quality, /원본 관측 금액을 유지하고\s+보완 금액을 더하지/);
  assert.match(quality, /누락\s+관측이 복구됐다는 뜻은 아닙니다/);
});

test("grounded question fact card keeps server volume and driver evidence visible", () => {
  const source = readFileSync(
    new URL("../components/HermesMarketQuestion.tsx", import.meta.url),
    "utf8"
  );

  for (const contractField of [
    "domestic_volume_delta_krw",
    "answer.facts.drivers",
    "answer.facts.exchange_drivers",
    "answer.facts.auxiliary",
    "noticePeriodLabel",
    "질문 구간 전체 해당",
    "질문 구간 일부 해당",
    "질문 구간 미해당",
    "answer.facts.path_summary",
    "공식 시간별 경로",
    "계산된 점유율 변화 기여",
  ]) {
    assert.ok(source.includes(contractField), `missing ${contractField}`);
  }
});

test("keeps the market-share LLM on its own dashboard tab", () => {
  const shell = readFileSync(
    new URL("../screens/AppShell.tsx", import.meta.url),
    "utf8"
  );
  const questionScreen = readFileSync(
    new URL("../screens/QuestionScreen.tsx", import.meta.url),
    "utf8"
  );
  const movement = readFileSync(
    new URL("../components/OfficialMovementPanel.tsx", import.meta.url),
    "utf8"
  );
  const page = readFileSync(
    new URL("../pages/MarketShareLLM.tsx", import.meta.url),
    "utf8"
  );
  const question = readFileSync(
    new URL("../components/HermesMarketQuestion.tsx", import.meta.url),
    "utf8"
  );

  assert.match(shell, /question: screen\("질문", \(\) => import\("\.\/QuestionScreen"\)\)/);
  assert.match(questionScreen, /<MarketShareLLM\b/);
  assert.match(page, /<HermesMarketQuestion\s+standalone/);
  assert.doesNotMatch(movement, /HermesMarketQuestion/);
  for (const exchange of ["빗썸", "업비트", "코인원", "코빗", "고팍스"]) {
    assert.ok(question.includes(exchange), `missing ${exchange} prompt`);
  }
  for (const markdownContract of [
    "점유율 분석 보고서",
    "ReactMarkdown",
    "remarkGfm",
    "보고서 원문",
    "buildMarketQuestionMarkdownDocument",
    "answer.prose.answer",
    "답변 완료",
    "답변 소요",
    "질문을 해석하고 계산 중",
    "화면에 다녀와도 이 요청은 계속됩니다",
    "서버 확정 정량 결과",
    "계산 방법과 답변 근거",
    "핵심 판단 근거",
    "사용 데이터",
  ]) {
    assert.ok(
      question.includes(markdownContract),
      `missing ${markdownContract}`
    );
  }
  assert.doesNotMatch(question, /\.md 저장/);
  for (const conversationContract of [
    "MAX_MARKET_QUESTION_HISTORY_TURNS",
    "sessionStorage",
    "최근 대화",
    "기억 지우기",
    "최근 15개 대화",
    "useMarketQuestionTask",
  ]) {
    assert.ok(
      question.includes(conversationContract),
      `missing conversation contract: ${conversationContract}`
    );
  }
  for (const feedbackContract of [
    "submitMarketQuestionFeedback",
    "WRONG_PERIOD",
    "WRONG_TARGET",
    "WRONG_METRIC",
    "WRONG_ANALYSIS",
    "원래 원한 질문을 한 문장으로 다시 적어 주세요",
    "문장과 시장 원자료는 저장하지 않습니다",
  ]) {
    assert.ok(
      question.includes(feedbackContract),
      `missing question feedback contract: ${feedbackContract}`
    );
  }
});

test("shows text labels beside icons in the desktop sidebar", () => {
  const layout = readFileSync(
    new URL("../components/DashboardLayout.tsx", import.meta.url),
    "utf8"
  );

  assert.match(layout, /app-sidebar hidden w-56/);
  assert.match(layout, /app-nav-item[^`]*w-full[^`]*text-left/);
  assert.match(
    layout,
    /<span className="truncate text-sm font-medium">\{label\}<\/span>/
  );
  assert.doesNotMatch(layout, /role="tooltip"/);
});

test("keeps analysis labels explicit about denominator and comparison order", () => {
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const intelligence = readFileSync(
    new URL("../pages/MarketIntelligence.tsx", import.meta.url),
    "utf8"
  );
  const question = readFileSync(
    new URL("../components/HermesMarketQuestion.tsx", import.meta.url),
    "utf8"
  );
  const publicAnalysisCopy = [marketShare, intelligence, question].join("\n");

  for (const requiredCopy of [
    "코인 거래금액 중 빗썸 비중",
    "코인·거래소 점유율 기여 관계도",
    "같은 종료 시각의 1시간봉",
  ]) {
    assert.ok(
      publicAnalysisCopy.includes(requiredCopy),
      `missing explicit analysis copy: ${requiredCopy}`
    );
  }

  for (const forbiddenCopy of [
    "포착률",
    "거래대금",
    "Unique symbol",
    "base 심볼",
    "코인 드라이버",
    "정렬 시간봉",
    "주문 체결기록",
  ]) {
    assert.ok(
      !publicAnalysisCopy.includes(forbiddenCopy),
      `ambiguous analysis copy exposed: ${forbiddenCopy}`
    );
  }
});

test("retired slippage estimator has no menu, view or custom notional requests", () => {
  const source = readFileSync(new URL("../pages/MarketShare.tsx", import.meta.url), "utf8");
  const menu = readFileSync(new URL("../components/DashboardLayout.tsx", import.meta.url), "utf8");
  assert.doesNotMatch(menu, /id: "liquidity"/);
  assert.doesNotMatch(source, /LiquidityView|applyNotional|SlippageAssetComparison|view === "liquidity"/);
  // The fifth argument only picks the realtime-flow stream; notional stays null.
  assert.match(
    source,
    /useMarketShare\(requestedWindowSec, null, marketScope, view(?:, "none")?\)/
  );
});

test("keeps every dashboard page on one responsive desktop alignment grid", () => {
  const layout = readFileSync(
    new URL("../components/DashboardLayout.tsx", import.meta.url),
    "utf8"
  );
  const intelligence = readFileSync(
    new URL("../pages/MarketIntelligence.tsx", import.meta.url),
    "utf8"
  );
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const noticeQuestion = readFileSync(
    new URL("../components/HermesNoticeQuestion.tsx", import.meta.url),
    "utf8"
  );
  const notices = readFileSync(
    new URL("../pages/MarketNotices.tsx", import.meta.url),
    "utf8"
  );
  const styles = readFileSync(new URL("../index.css", import.meta.url), "utf8");

  assert.match(layout, /dashboard-page mx-auto min-w-0 w-full/);
  assert.match(
    styles,
    /\.dashboard-page\s*\{\s*max-width:\s*max\(80%, 74rem\)/s
  );
  assert.match(
    styles,
    /@media \(min-width: 1101px\) and \(max-width: 1599px\)[\s\S]*?\.official-change-overview\s*\{\s*grid-template-columns:\s*minmax\(0, 1fr\)/
  );
  assert.match(layout, /focus:outline-none/);
  assert.doesNotMatch(intelligence, /max-w-\[1500px\]/);
  const bestBooks = readFileSync(
    new URL("../components/PriorityBestOrderBooks.tsx", import.meta.url),
    "utf8"
  );
  assert.match(
    bestBooks,
    /grid-cols-\[minmax\(0,1fr\)_7rem_minmax\(0,1fr\)\]/
  );
  assert.match(bestBooks, /best-book-liquidity-value ui-caption/);
  assert.match(marketShare, /flex min-w-0 flex-col bg-card p-4/);
  assert.match(marketShare, /mt-auto border-t border-border pt-2/);
  assert.match(marketShare, /2xl:grid-cols-2/);
  assert.match(noticeQuestion, /hermes-notice-question/);
  assert.match(notices, /data\?\.analysis\?\.notices\?\.sources/);
});

test("keeps expanded notice schedules and reactions compact", () => {
  const notices = readFileSync(
    new URL("../pages/MarketNotices.tsx", import.meta.url),
    "utf8"
  );

  assert.match(notices, /notice-schedule-list/);
  assert.match(notices, /notice-reaction-summary/);
  assert.match(notices, /title={`근거: \${schedule\.evidence}`}/);
});

test("keeps notice reaction values inside the visible summary card", () => {
  const styles = readFileSync(new URL("../index.css", import.meta.url), "utf8");
  const rule = styles.match(/\.notice-evidence-panel\s*\{([^}]*)\}/s)?.[1];

  assert.match(rule ?? "", /width:\s*100%/);
  assert.match(rule ?? "", /max-width:\s*100%/);
  assert.match(rule ?? "", /margin-inline:\s*auto/);
  assert.doesNotMatch(
    styles,
    /\.notice-evidence-panel\s*\{[^}]*width:\s*200%/s
  );
  assert.doesNotMatch(styles, /\.notice-evidence-panel\s*\{[^}]*-50%/s);
});

test("keeps every selected-hour summary and top list at full panel width", () => {
  const styles = readFileSync(new URL("../index.css", import.meta.url), "utf8");

  for (const className of [
    "official-selected-hour-story",
    "official-contribution-insight",
    "official-contribution-landscape",
  ]) {
    const rule = styles.match(
      new RegExp(`\\.${className}\\s*\\{([^}]*)\\}`, "s")
    )?.[1];
    assert.match(rule ?? "", /width:\s*100%/);
    assert.match(rule ?? "", /margin-inline:\s*auto/);
  }
});

test("keeps the coin contribution page focused on two decision views", () => {
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const driverStart = marketShare.indexOf("function ContributionLeaderCard(");
  const driverEnd = marketShare.indexOf("function LiquidityView(", driverStart);
  const driverView = marketShare.slice(driverStart, driverEnd);

  assert.match(driverView, /지금 점유율을 움직인 코인/);
  assert.match(driverView, /점유율 상승 기여 TOP 5/);
  assert.match(driverView, /점유율 하락 기여 TOP 5/);
  assert.match(driverView, /빗썸 거래금액 급증 TOP 5/);
  assert.match(driverView, /경쟁 거래소 거래금액 급증 TOP 5/);
  assert.match(driverView, /\["impact", "점유율 변화 기여"\]/);
  assert.match(driverView, /\["capture", "코인별 빗썸 비중"\]/);
  assert.doesNotMatch(driverView, /\["execution",/);
  assert.doesNotMatch(driverView, /\["binance",/);
  assert.doesNotMatch(driverView, /\["quality",/);
});

test("places exchange registry on data quality and removes global reference from market analysis", () => {
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const intelligence = readFileSync(
    new URL("../pages/MarketIntelligence.tsx", import.meta.url),
    "utf8"
  );
  const binanceReference = readFileSync(
    new URL("../components/BinanceReferencePanel.tsx", import.meta.url),
    "utf8"
  );

  assert.match(
    marketShare,
    /view === "quality" && shouldShowExchangeRegistry\(surface\)/
  );
  assert.doesNotMatch(intelligence, /BinanceReferencePanel/);
  assert.match(binanceReference, /빗썸 USDT/);
  assert.match(binanceReference, /바이낸스의 좋은 매수·매도 가격/);
  assert.match(binanceReference, /수수료 적용 전 가격 차이/);
  assert.match(binanceReference, /수수료 적용 후 가격 차이/);
});

test("removes the redundant selected-exchange perspective explanation", () => {
  const perspective = readFileSync(
    new URL(
      "../components/TargetExchangePerspectivePanel.tsx",
      import.meta.url
    ),
    "utf8"
  );

  assert.doesNotMatch(
    perspective,
    /기존 단일 국내시장 분석에서 선택 거래소 행만 표시합니다\./
  );
});

test("omits Bithumb logos from the dashboard and exchange labels", () => {
  const layout = readFileSync(
    new URL("../components/DashboardLayout.tsx", import.meta.url),
    "utf8"
  );
  const exchangeLabel = readFileSync(
    new URL("../components/ExchangeBrandLabel.tsx", import.meta.url),
    "utf8"
  );
  assert.doesNotMatch(layout, /app-brand-lockup|app-sidebar-brand|\/brands\/bithumb-/);
  assert.doesNotMatch(exchangeLabel, /<img|\/brands\/bithumb-/);
});

test("keeps connection status only in the global dashboard topbar", () => {
  const layout = readFileSync(
    new URL("../components/DashboardLayout.tsx", import.meta.url),
    "utf8"
  );
  const rolling = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );

  assert.match(layout, /app-live-status/);
  assert.doesNotMatch(rolling, /title="빗썸 점유율 변화"[\s\S]{0,320}actions=/);
  assert.doesNotMatch(rolling, /function RealtimeConnectionStatus/);
});

test("removes the rolling exchange observation table while keeping the coin table", () => {
  const rolling = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );
  const pageStart = rolling.indexOf("function Summary2Page(");
  const pageEnd = rolling.indexOf(
    "function OfficialTickerTrendPanel(",
    pageStart
  );
  const page = rolling.slice(pageStart, pageEnd);

  assert.match(page, /들어오고 빠진 거래가 확인된 코인/);
  assert.doesNotMatch(page, /들어오고 빠진 거래가 확인된 거래소/);
  assert.doesNotMatch(page, /kind="exchange"/);
});

test("removes the official purpose header and keeps explanations behind help icons", () => {
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const officialMovement = readFileSync(
    new URL("../components/OfficialMovementPanel.tsx", import.meta.url),
    "utf8"
  );
  const rolling = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );
  const realtime = readFileSync(
    new URL("../components/RealtimeFlowPanel.tsx", import.meta.url),
    "utf8"
  );
  const liveExchangeChart = readFileSync(
    new URL("../components/LiveExchangeChart.tsx", import.meta.url),
    "utf8"
  );
  const inlineHelp = readFileSync(
    new URL("../components/common/InlineHelp.tsx", import.meta.url),
    "utf8"
  );
  const pageStart = rolling.indexOf("function Summary2Page(");
  const pageEnd = rolling.indexOf(
    "function OfficialTickerTrendPanel(",
    pageStart
  );
  const page = rolling.slice(pageStart, pageEnd);

  assert.doesNotMatch(rolling, /title="공식 24시간 점유율 변화"/);
  assert.doesNotMatch(rolling, /purposeCardsOpen|official-purpose-map/);
  assert.doesNotMatch(rolling, /카드 접기|카드 펼치기/);
  assert.match(rolling, /<InlineHelp text="매시간 24시간 범위에 새로 들어오고 빠진 거래가/);
  assert.match(
    rolling,
    /helpText="국내 5곳의 최신 공식 24시간 자료를 대표값으로 사용합니다\. 아래 시간 비교값이 집계하는 국내시장과 다릅니다\."/
  );
  assert.match(
    rolling,
    /오른·내린 코인 수[\s\S]*<InlineHelp[\s\S]*전체 \$\{contribution\.asset_drivers\.length\.toLocaleString\("ko-KR"\)\}개 코인 중 점유율을 올린 코인과 내린 코인 수/
  );
  assert.match(rolling, /오른 값·내린 값 합계/);
  assert.match(
    rolling,
    /각 코인이 점유율을 올리거나 내린 값을 방향별로 각각 더한 결과/
  );
  assert.doesNotMatch(rolling, /양·음 총기여|순변화 전에 서로 반대 방향/);
  assert.doesNotMatch(
    rolling,
    /직접 관측 24시간 범위|rollingCoverageLabel|coverageNote/
  );
  assert.doesNotMatch(
    rolling,
    /미완성 분석 상태|직접 관측 24시간 맥락을 쌓고 있습니다|official-rolling-progress/
  );
  assert.match(
    rolling,
    /공지와 시간상 반응[\s\S]*공지와 시장 변화가 시간상 겹쳐도 원인을 뜻하지는 않습니다\./
  );
  assert.doesNotMatch(
    rolling,
    /official-data-trust|자료 신뢰도|직접 관측한 체결만|timestamp\(monitor\.as_of_ms\)|timestamp\(lastMessageAt\)|최근 수신/
  );
  assert.match(
    rolling,
    /상위 5개 코인의 영향[\s\S]*<InlineHelp text="점유율을 올린 쪽과 내린 쪽에서 영향이 큰 5개 코인이 차지한 비중" \/>/
  );
  assert.match(
    rolling,
    /상쇄율[\s\S]*<InlineHelp[\s\S]*올린 값과 내린 값이 서로 상쇄된 비율 · 최종 변화 \$\{points\(selectedHour\.shareDeltaPP\)\}/
  );
  assert.match(
    rolling,
    /<InlineHelp text="업비트·빗썸 공지 중 분석 시점에 진행 중인 일정만 보여줍니다\.[^"]+인과관계 확정이 아닙니다\." \/>/
  );
  assert.match(
    rolling,
    /description="빗썸 쪽 거래 증가와 경쟁 거래소 쪽 감소를 함께 계산한 양\(\+\)의 기여입니다\."/
  );
  assert.match(
    rolling,
    /description="빗썸 쪽 거래 감소와 경쟁 거래소 쪽 증가를 함께 계산한 음\(-\)의 기여입니다\."/
  );
  assert.match(rolling, /<InlineHelp text=\{description\} \/>/);
  assert.match(
    realtime,
    /<InlineHelp text="최근 완료된 같은 시각의 3개 거래소 공식 1시간 방향과 국내 5개 거래소의 실제 10초 방향을 비교합니다\. 두 값은 별도로 계산합니다\." \/>/
  );
  assert.match(liveExchangeChart, /<InlineHelp text=\{description\} \/>/);
  assert.match(
    liveExchangeChart,
    /국내 5개 거래소의 선택 구간 점유율입니다\. Y축은 확인된 최솟값과 최댓값에 맞춰 확대합니다\. 전체 구간이 확보되지 않으면 실제로 확인한 실시간 체결만 선에 표시합니다\./
  );
  assert.match(
    marketShare,
    /주요 상승·하락 영향[\s\S]*<InlineHelp[\s\S]*국내 5개 거래소의 비교 구간이 확정되지 않아 코인별 변화 기여는 계산하지 않습니다\./
  );
  assert.match(
    marketShare,
    /점유율 변화 계산 근거[\s\S]*<InlineHelp text="점유율 변화를 빗썸과 경쟁 거래소의 거래금액 변화로 나눠 계산합니다\. 호가·상장·공지는 원인으로 확정하지 않고 별도 참고 항목으로 표시합니다\." \/>/
  );
  assert.match(
    marketShare,
    /보고용 점유율 대조표[\s\S]*<InlineHelp text="선택 구간과 직전 동일 구간을 보고서 양식과 같은 열 구조로 맞춘 확정 계산표입니다\. 표시 코인은 상승·하락 각 방향 누적 80%를 설명하는 전체 항목입니다\." \/>/
  );
  assert.doesNotMatch(marketShare, /exchange-slippage-panel/);
  assert.match(
    officialMovement,
    /공식 24시간 변화 요약[\s\S]*<InlineHelp text="현재 수준은 국내 거래소의 공식 24시간 자료로, 시간 변화는 같은 종료 시각의 완료된 1시간 자료로 계산합니다\." \/>/
  );
  assert.match(
    inlineHelp,
    /onClick=\{\(\) => \{[\s\S]*?setMainDashboard\(!!buttonRef\.current\?\.closest\("\.main-dashboard"\)\);[\s\S]*?setOpen\(current => !current\);/
  );
  assert.match(inlineHelp, /aria-expanded=\{open\}/);
  assert.match(inlineHelp, /createPortal\(/);
  assert.match(inlineHelp, /role="tooltip"/);
  assert.match(inlineHelp, /event\.key === "Escape"/);
  assert.match(
    page,
    /helpText="최근 완료된 공식 1시간 방향과 현재 10초 방향의 일치·반대·중립을 먼저 판정하고, 국내 5곳의 거래소 표는 아래 근거로 보여줍니다\."/
  );
  assert.match(
    page,
    /helpText="자료 품질은 한 줄로 요약합니다\. 24시간 자료가 아직 다 쌓이지 않았으면 분석 대신 쌓인 정도를 보여줍니다\."/
  );
  assert.match(rolling, /helpText && <InlineHelp text=\{helpText\} \/>/);
});

test("removes the rolling share interpretation-principles panel", () => {
  const rolling = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );

  assert.doesNotMatch(rolling, /점유율 변화 해석 원칙/);
  assert.doesNotMatch(rolling, /측정값과 설명 후보를 섞지 않습니다/);
  assert.doesNotMatch(rolling, /관측 공백은 0으로 채우지 않고/);
});

test("removes the provisional rolling KPI strip", () => {
  const rolling = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );
  const pageStart = rolling.indexOf("function Summary2Page(");
  const pageEnd = rolling.indexOf(
    "function OfficialTickerTrendPanel(",
    pageStart
  );
  const page = rolling.slice(pageStart, pageEnd);

  assert.doesNotMatch(page, /<LiveRollingShareKpi/);
  assert.doesNotMatch(page, /최근 10분 신규 거래/);
  assert.doesNotMatch(page, /24시간 전 만료 구간/);
  assert.doesNotMatch(page, /주요 관측 흐름/);
  assert.doesNotMatch(rolling, /function RollingWarmupObservation/);
});

test("keeps live quotes exact while shared metric motion remains bounded", () => {
  const marketShare = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const bestBooks = readFileSync(
    new URL("../components/PriorityBestOrderBooks.tsx", import.meta.url),
    "utf8"
  );
  const tween = readFileSync(
    new URL("../hooks/useAnimatedMetrics.ts", import.meta.url),
    "utf8"
  );

  assert.match(
    marketShare,
    /import \{ useAnimatedMetrics \} from "@\/hooks\/useAnimatedMetrics"/
  );
  assert.doesNotMatch(marketShare, /function useAnimatedMetrics\(/);
  const rowStart = bestBooks.indexOf(
    "function PriorityBestOrderBookExchangeRow("
  );
  assert.ok(rowStart >= 0);
  const row = bestBooks.slice(
    rowStart,
    bestBooks.indexOf("\n/**", rowStart + 1)
  );
  assert.doesNotMatch(row, /useAnimatedMetrics\(/);
  assert.match(row, /best-book-direction-mark/);
  assert.match(row, /best-book-update-sweep/);
  assert.match(tween, /durationMS = 420/);
  assert.match(tween, /subscribeSharedAnimationFrame\(tick\)/);
  assert.match(tween, /prefers-reduced-motion: reduce/);
});

test("renders one contribution network with a one-minute to 24-hour slider", () => {
  const network = readFileSync(
    new URL("../components/ActiveNoticeShareNetwork.tsx", import.meta.url),
    "utf8"
  );
  const intelligence = readFileSync(
    new URL("../pages/MarketIntelligence.tsx", import.meta.url),
    "utf8"
  );

  assert.match(network, /getActiveNoticeNetwork\(windowSec/);
  assert.match(network, /retainedNetworkByWindow/);
  assert.match(network, /type="range"/);
  assert.match(network, /MARKET_SHARE_NETWORK_WINDOWS/);
  assert.match(network, /aria-valuetext=\{windowLabel\}/);
  assert.match(
    network,
    /activeNetworkPresentation\(data\?\.status, data\?\.reason\)/
  );
  assert.match(network, /\{windowLabel\} · \{presentation\.label\}/);
  assert.match(intelligence, /networkWindowSec/);
  assert.match(intelligence, /onWindowSecChange=\{setNetworkWindowSec\}/);
  assert.doesNotMatch(
    intelligence,
    /AssetExchangeShareNetwork windowSec=\{60\}/
  );
  assert.doesNotMatch(
    intelligence,
    /AssetExchangeShareNetwork windowSec=\{600\}/
  );
  assert.doesNotMatch(intelligence, /공지·점유율 변화 관계도/);
  assert.doesNotMatch(intelligence, /AssetExchangeNetworkGraph/);
  assert.doesNotMatch(intelligence, /코인·거래소 거래금액 관계도/);
});

test("renders coin-exchange graph labels in white", () => {
  const contributionNetwork = readFileSync(
    new URL("../components/ActiveNoticeShareNetwork.tsx", import.meta.url),
    "utf8"
  );

  assert.match(
    contributionNetwork,
    /const NETWORK_GRAPH_TEXT_COLOR = "#ffffff"/
  );
  assert.match(
    contributionNetwork,
    /labelColor: \{ color: NETWORK_GRAPH_TEXT_COLOR \}/
  );
  assert.match(
    contributionNetwork,
    /defaultDrawNodeLabel: drawWhiteGraphNodeLabel/
  );
});
