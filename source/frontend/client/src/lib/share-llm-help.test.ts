import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");
const page = read("../pages/MarketShareLLM.tsx");

test("only the main app opts into consolidated LLM scope help", () => {
  assert.match(page, /scopePresentation\?: "cards" \| "help"/);
  assert.match(page, /scopePresentation = "cards"/);
  assert.match(
    read("../screens/QuestionScreen.tsx"),
    /<MarketShareLLM scopePresentation="help" mainDashboard\s*\/>/
  );
  assert.match(
    read("../../../unix_share/src/App.tsx"),
    /<MarketShareLLM\s*\/>/
  );
  assert.doesNotMatch(
    read("../../../unix_share/src/App.tsx"),
    /scopePresentation/
  );
  assert.match(
    read("../StandaloneApp.tsx"),
    /<MarketShareLLM embeddedAnswers=\{questionAnswers\}\s*\/>/
  );
  assert.doesNotMatch(read("../StandaloneApp.tsx"), /scopePresentation/);
});

test("main scope cards are conditionally absent rather than merely CSS-hidden", () => {
  const cardBranch = page.indexOf('{scopePresentation === "cards" && (');
  const cardGrid = page.indexOf('className="share-llm-scope-grid"');
  const questionForm = page.indexOf("<HermesMarketQuestion");
  assert.ok(
    cardBranch >= 0 && cardBranch < cardGrid && cardGrid < questionForm
  );
  assert.match(page.slice(cardBranch, questionForm), /REPORT_SCOPE\.map/);
  assert.match(
    page,
    /scopePresentation === "help" \? \(\s*<MobileHelp allSizes label="점유율 질문 사용 안내">/
  );
  assert.match(page, /<\/MobileHelp>\s*\) : undefined/);
});

test("one guide preserves the original introduction and all three scope explanations", () => {
  for (const text of [
    "거래소를 바꿔서 질문",
    "변화의 구조를 진단",
    "확인된 자료만 사용",
    "빗썸·업비트·코인원·코빗·고팍스의 거래금액과 국내 비중을 구분합니다.",
    "점유율 수준보다 대상 거래소·국내시장 효과, 주요 코인, 집중 시점을 먼저 짚습니다.",
    "질문에 필요한 저장 자료와 현재 자료만 확인하고, 비어 있는 기간은 그대로 알려드립니다.",
    "어느 국내 거래소든 현재 점유율, 기간별 변화, 거래금액, 코인 기여를 질문하면 결론부터 보고서 형태로 분석합니다.",
  ]) {
    assert.equal(page.split(JSON.stringify(text)).length - 1, 1, text);
  }
  assert.equal((page.match(/REPORT_SCOPE\.map/g) ?? []).length, 2);
  assert.match(page, /subtitle=\{pageDescription\}/);
  assert.match(page, /<p>\{pageDescription\}<\/p>/);
  assert.match(
    page,
    /const pageDescription = mainDashboard\s*\? undefined\s*: PAGE_DESCRIPTION/
  );
  assert.match(page, /<dl\b[^>]*>[\s\S]*REPORT_SCOPE\.map/);
  assert.match(page, /<dt\b[^>]*>\{title\}<\/dt>/);
  assert.match(page, /<dd\b[^>]*>\{detail\}<\/dd>/);
});

test("a custom title guide replaces the automatic subtitle trigger without duplicate help", () => {
  const header = read("../components/common/PageHeader.tsx");
  assert.match(header, /titleHelp\?: ReactNode/);
  assert.match(header, /\{titleHelp \?\?\s*\(subtitle && \(\s*<MobileHelp/);
  assert.equal((header.match(/<MobileHelp\b/g) ?? []).length, 1);
  assert.match(page, /titleHelp=\{/);
});

test("all-size guide styling stays out of Unix and standalone entrypoints", () => {
  assert.match(read("../main.tsx"), /import "\.\/help\.css"/);
  for (const path of [
    "../index.css",
    "../standalone-main.tsx",
    "../standalone.css",
    "../../../unix_share/src/main.tsx",
    "../../../unix_share/src/base.css",
    "../../../unix_share/src/sampler.css",
  ]) {
    assert.doesNotMatch(read(path), /help\.css/);
  }
  const css = read("../help.css").replace(/\s+/g, " ");
  assert.match(
    css,
    /:is\(\.mg, \.main-dashboard\) \.main-help-all-sizes-trigger\s*\{\s*display: inline-flex/
  );
  for (const match of css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .matchAll(/([^{}]+)\{/g)) {
    const selector = match[1].trim();
    assert.ok(
      selector.startsWith(":is(.mg, .main-dashboard)") ||
        selector === "@media (max-width: 767px)" ||
        selector === ".mg .mobile-help-trigger" ||
        selector.startsWith(".main-mobile-help-"),
      selector
    );
  }
});

test("scope help preserves embedded answers and explicitly scopes live draft behavior", () => {
  assert.match(
    page,
    /<HermesMarketQuestion\s+standalone\s+standaloneAnswer=\{embeddedAnswer\}\s+standaloneAnswers=\{embeddedAnswers\}\s+mainDashboard=\{mainDashboard\}\s+selectedExchange="bithumb"\s*\/>/
  );
  assert.doesNotMatch(
    page,
    /<details|<summary|fetch\(|new WebSocket|onSubmit=/
  );
});
