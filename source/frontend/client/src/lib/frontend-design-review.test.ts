import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");
const css = read("../index.css").replace(/\s+/g, " ");

test("timeline sticky columns depend on actual main-app container width", () => {
  assert.match(
    css,
    /\.main-dashboard \.notice-timeline-scroll \{[^}]*container-type: inline-size;[^}]*container-name: main-notice-timeline;/
  );
  assert.match(
    css,
    /@container main-notice-timeline \(max-width: 800px\) \{ \.main-dashboard \.notice-timeline-sticky \{ position: static !important;/
  );
  assert.doesNotMatch(
    read("../../../unix_share/src/base.css"),
    /main-notice-timeline/
  );
});

test("main help measures content, listens for visible viewport changes, and scrolls long text", () => {
  const help = read("../components/common/InlineHelp.tsx");
  assert.match(help, /closest\("\.main-dashboard"\)/);
  assert.match(help, /if \(mainDashboard\) \{[\s\S]*helpPopoverPosition\(/);
  assert.match(help, /resizeObserver\.observe\(popupRef\.current\)/);
  assert.match(
    help,
    /visualViewport\?\.addEventListener\("resize", updatePosition\)/
  );
  assert.match(
    help,
    /visualViewport\?\.addEventListener\("scroll", updatePosition\)/
  );
  assert.match(
    help,
    /setPosition\(\{ left, top: rect\.bottom \+ 8, width \}\)/
  );
  assert.match(
    css,
    /\.main-inline-help-popup \{[^}]*overflow-y: auto;[^}]*overflow-wrap: anywhere;/
  );
});

test("notice titles wrap at reading size without changing Unix titles", () => {
  const notices = read("../notices.css").replace(/\s+/g, " ");
  assert.match(
    read("../pages/MarketNotices.tsx"),
    /notice-list-title ui-title font-semibold/
  );
  assert.match(
    notices,
    /\.main-dashboard\[data-section="events"\] \.notice-list-title \{[^}]*white-space: normal;[^}]*font-size: var\(--type-title\);/
  );
  assert.doesNotMatch(notices, /-webkit-line-clamp|line-clamp:/);
  assert.doesNotMatch(read("../../../unix_share/src/main.tsx"), /notices\.css/);
});

test("desktop main summary separates body-sized prose from card stretching", () => {
  assert.match(
    css,
    /\.main-dashboard \.official-change-overview \{ align-items: start;/
  );
  assert.match(
    css,
    /\.main-dashboard \.official-change-answer \.official-summary-copy \{ font-size: var\(--type-body\);/
  );
  assert.match(
    css,
    /\.main-dashboard \.official-change-primary-metrics \.metric-panel \{ height: auto; min-height: 10rem;/
  );
});

test("KPI cards grow with their caption instead of clipping it", () => {
  // A card with a fixed height under overflow: hidden cuts its last caption
  // lines off with no sign that text is missing. That hid the basis label and
  // the observed / missing span of a partly observed window on the selected
  // window detail screen. The card itself may only set a minimum height.
  const rules = read("../index.css")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ");
  const cardRules = [
    ...rules.matchAll(/(?:^|[{};]) ?\.metric-panel \{([^}]*)\}/g),
  ].map(match => match[1]);
  assert.ok(cardRules.length > 0, "the base .metric-panel rule was not found");
  for (const declarations of cardRules) {
    assert.doesNotMatch(
      declarations,
      /(?:^|[; ])height: (?!auto\b)/,
      `.metric-panel must not pin its height: ${declarations}`
    );
  }
  assert.match(cardRules[0], /(?:^|[; ])height: auto;/);
  assert.match(cardRules[0], /(?:^|[; ])min-height: 9\.375rem;/);
});

test("KPI card captions wrap at spaces on desktop as they do on phones", () => {
  // The caption under a partly observed window's card wraps onto a second line.
  // Without keep-all it split "누락" across the two lines.
  const rules = read("../index.css")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ");
  const caption = rules.match(/(?:^|[{};]) ?\.metric-panel-detail \{([^}]*)\}/);
  assert.ok(caption, "the base .metric-panel-detail rule was not found");
  assert.match(caption[1], /(?:^|[; ])word-break: keep-all;/);
  assert.match(caption[1], /(?:^|[; ])overflow-wrap: anywhere;/);
});

test("boxes that hold live text grow or share columns instead of cutting it off", () => {
  // Each of these cut text off in production on 2026-09-29 with no sign that
  // anything was missing. They are pinned here because the live gate only sees
  // them when the data happens to be long enough.
  const rules = read("../index.css")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ");
  const index2 = read("../pages/market-index2.css")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ");
  const page = read("../pages/MarketShare.tsx");

  // Collapsed exchange cards: badges that wrap at about 1180px pushed the coin
  // count below a fixed 7.875rem card.
  const collapsed = [
    ...rules.matchAll(/\.exchange-panel-collapsed \{([^}]*)\}/g),
  ].map(match => match[1]);
  assert.equal(collapsed.length, 2, "both collapsed-card rules are expected");
  for (const declarations of collapsed) {
    assert.doesNotMatch(declarations, /(?:^|[; ])height:/, declarations);
    assert.match(declarations, /(?:^|[; ])min-height:/, declarations);
  }

  // Animated values: the reserved width was wider than the official summary
  // card at tablet widths; phones had the limit, tablets did not.
  assert.match(
    rules,
    /(?:^|[{};]) ?\.animated-metric-value, \.stat-value > \.metric-value-settle \{ max-inline-size: 100%; \}/
  );

  // The realtime-flow detail grid sized its only column to its content and
  // pushed the driver cards past the panel edge at 820px.
  assert.match(
    rules,
    /\.realtime-flow-detail-grid > \* \{ min-width: 0; \}|\.realtime-flow-detail-grid > \*,[^{]*\{ min-width: 0; \}|, \.realtime-flow-detail-grid > \* \{ min-width: 0; \}/
  );

  // Contribution ranks share one set of columns, so a symbol is not cut to
  // "CASHCA…" and every bar is drawn on the same track.
  assert.match(
    rules,
    /@media \(min-width: 768px\) \{ \.official-contribution-rank-list \{ display: grid; grid-template-columns: [^;]*minmax\(4\.5rem, max-content\)[^;]*;/
  );
  assert.match(
    rules,
    /\.official-contribution-rank-list li \{ grid-column: 1 \/ -1; grid-template-columns: subgrid;/
  );

  // Index2 placeholder: the caption line box was taller than the 14px bar.
  const empty = index2.match(/\.index2-bar-empty \{([^}]*)\}/);
  assert.ok(empty, "the .index2-bar-empty rule was not found");
  assert.match(empty[1], /(?:^|[; ])line-height: 1;/);

  // The two stacked comparison panels were fixed at 443px on wide screens and
  // cut their last row off when the description wrapped.
  assert.doesNotMatch(page, /xl:h-\[443px\]/);

  // The exchange table scrolls sideways at every width, not only on phones;
  // at about 820px the panel cut its last column off.
  assert.match(
    rules,
    /(?:^|[{};]) ?\.official-exchange-table-scroll \{ max-width: 100%; overflow-x: auto; \}/
  );
});
