import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");
const css = read("../mobile.css").replace(/\s+/g, " ");

test("mobile layout is loaded only by the main entry, never Unix shared styles", () => {
  assert.match(read("../main.tsx"), /import "\.\/mobile\.css"/);
  assert.doesNotMatch(read("../index.css"), /mobile\.css/);
  assert.doesNotMatch(read("../../../unix_share/src/main.tsx"), /mobile\.css/);
  assert.doesNotMatch(read("../../../unix_share/src/base.css"), /mobile\.css/);
  assert.match(css, /@media \(max-width: 767px\)/);
  // Every style rule (including each comma-separated selector) is main-only.
  for (const match of css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .matchAll(/([^{}]+)\{/g)) {
    const selector = match[1].trim();
    if (selector.startsWith("@media")) continue;
    assert.ok(
      selector.startsWith(".main-dashboard") ||
        selector.startsWith(".main-mobile-help-"),
      selector
    );
  }
});

test("mobile summary separates readable prose from primary metrics", () => {
  assert.match(
    css,
    /\.official-change-answer \.official-summary-copy\s*\{[^}]*font-size: var\(--type-body\)/
  );
  assert.match(
    css,
    /\.official-summary-card-share \.stat-value\s*\{[^}]*font-size: var\(--type-display\)/
  );
  assert.match(
    css,
    // Current value first, then the conclusion, then the two smaller cards.
    /\.official-change-primary-metrics\s*\{\s*display: contents;\s*\}[\s\S]*\.official-change-answer\s*\{[^}]*order: -1;[\s\S]*\.official-change-primary-metrics > :first-child\s*\{[^}]*order: -2;/
  );
  assert.match(
    css,
    /section\[aria-label="선택 거래소 관점"\] > \.grid\s*\{[^}]*repeat\(2, minmax\(0, 1fr\)\)/
  );
  assert.match(css, /\.metric-panel\s*\{[^}]*height: auto;/);
  assert.match(
    css,
    /\.stat-value > \.metric-value-settle\s*\{[^}]*max-inline-size: 100%/
  );
});

test("mobile touch and evidence remain usable without shrinking or hiding data", () => {
  assert.match(css, /min-height: 2\.75rem/);
  assert.match(
    css,
    /:is\(input, select, textarea\)\s*\{[^}]*font-size: var\(--type-body\) !important/
  );
  assert.match(
    css,
    /\.context-toolbar \.segmented-control\s*\{[^}]*overflow-x: auto/
  );
  assert.match(css, /표를 좌우로 밀어 나머지 항목을 확인하세요/);
  assert.match(
    css,
    /tr > :first-child:not\(\[colspan\]\)\s*\{[^}]*position: sticky/
  );
  assert.doesNotMatch(css, /overflow-x:\s*(?:hidden|clip)/);
  assert.doesNotMatch(css, /font-size:\s*(?:[0-9]|1[0-2])px/);
  assert.doesNotMatch(css, /\.metric-panel-detail\s*\{[^}]*display:\s*none/);
});

test("selected-window comparison stacks its nested grids and preserves every label and amount", () => {
  // The old two-card/two-column combination gave the operand column almost
  // no width on phones while the total kept a 6.5rem minimum. No overflow of
  // the page itself was necessary for the text inside the card to collide.
  assert.match(
    css,
    /\.summary-headline-effects, \.main-dashboard \.summary-headline-effect-decomposition\s*\{[^}]*grid-template-columns: minmax\(0, 1fr\)/
  );
  assert.match(
    css,
    /\.summary-headline-effect-total-inline\s*\{[^}]*align-items: flex-start;[^}]*border-top: 1px[^}]*border-left: 0;[^}]*padding-left: 0;[^}]*text-align: left/
  );
  assert.match(
    css,
    /\.summary-headline-effect-operands > strong, \.main-dashboard \.summary-headline-effect-nested > strong\s*\{[^}]*display: flex;[^}]*flex-direction: column/
  );
  for (const selector of [
    ".summary-headline-effect > span",
    ".summary-headline-effect-operands > span",
    ".summary-headline-effect-nested > span",
    ".summary-headline-effect-total-inline > span",
    ".summary-headline-share-card > small",
    ".summary-headline-share-change",
    ".summary-headline-effect-volume-inline",
  ]) {
    assert.ok(
      css.includes(selector),
      `missing complete text rule: ${selector}`
    );
  }
  assert.match(
    css,
    /\.summary-headline-effect-volume-inline\s*\{[^}]*overflow: visible;[^}]*text-overflow: clip;[^}]*white-space: normal;[^}]*overflow-wrap: anywhere/
  );
  const browserTest = read("../../../e2e-mobile/frontend-mobile.spec.ts");
  assert.match(browserTest, /name: "선택 구간 요약", exact: true/);
  assert.match(browserTest, /range\.getClientRects\(\)/);
  assert.match(browserTest, /comparison\.overlappingText/);
  assert.match(browserTest, /comparison\.clippedText/);
});
