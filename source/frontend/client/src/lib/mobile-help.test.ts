import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

function source(path: string): string {
  return readFileSync(new URL(path, import.meta.url), "utf8");
}

const help = source("../components/common/MobileHelp.tsx");

test("opens supporting explanations at every size only in the main dashboard", () => {
  assert.match(help, /export function MobileHelp/);
  assert.match(help, /children: ReactNode/);
  assert.match(help, /label: string/);
  assert.match(help, /mobile-help-trigger hidden/);
  assert.match(help, /allSizes = true/);
  assert.match(help, /buttonRef\.current\?\.closest\("\.mg, \.main-dashboard"\)/);
  assert.match(help, /window\.matchMedia\("\(max-width: 767px\)"\)\.matches/);
  assert.match(help, /onClick=\{\(\) =>/);
  assert.doesNotMatch(help, /onMouseEnter|onMouseLeave|onPointerEnter/);
  assert.doesNotMatch(help, /inline-help-popup|role="tooltip"/);
});

test("uses a labelled dialog with a touch close control and intact rich content", () => {
  assert.match(help, /aria-haspopup="dialog"/);
  assert.match(help, /aria-expanded=\{open\}/);
  assert.match(help, /aria-controls=\{popupId\}/);
  assert.match(help, /createPortal\(/);
  assert.match(help, /id=\{popupId\}/);
  assert.match(help, /className="main-mobile-help-popup"/);
  assert.match(help, /role="dialog"\s*aria-labelledby=\{titleId\}/);
  assert.match(help, /className="main-mobile-help-title" id=\{titleId\}/);
  assert.match(help, /aria-label="설명 닫기"\s*onClick=\{close\}/);
  assert.match(help, /className="main-mobile-help-content">\{children\}/);
  assert.doesNotMatch(help, /String\(children\)|dangerouslySetInnerHTML/);
});

test("closes on outside touch and Escape and restores a visible focus target", () => {
  assert.match(
    help,
    /document\.addEventListener\("pointerdown", closeOnOutsidePointer\)/
  );
  assert.match(help, /!buttonRef\.current\?\.contains\(target\)/);
  assert.match(help, /!popupRef\.current\?\.contains\(target\)/);
  assert.match(help, /event\.key === "Escape"/);
  assert.match(help, /button\.getClientRects\(\)\.length > 0/);
  assert.match(help, /button\.focus\(\{ preventScroll: true \}\)/);
  assert.match(help, /querySelector<HTMLElement>\("\.mg-main, \.dashboard-content"\)/);
  assert.match(
    help,
    /closeButtonRef\.current\?\.focus\(\{ preventScroll: true \}\)/
  );
  assert.match(help, /open && position.visibility === "visible"/);
  assert.match(help, /\[open, position.visibility\]/);
  assert.match(
    help,
    /if \(!allSizes && !mobileViewport\.matches\)\s*\{\s*close\(\)/
  );
});

test("clamps the popup to the visual viewport and cleans up positioning observers", () => {
  for (const field of ["offsetLeft", "offsetTop", "width", "height"]) {
    assert.ok(help.includes(`visualViewport?.${field}`));
  }
  assert.match(help, /position: "fixed"/);
  assert.match(
    help,
    /width = Math\.min\(360, Math\.max\(0, viewportWidth - margin \* 2\)\)/
  );
  assert.match(
    help,
    /maxHeight = Math\.max\(0, viewportHeight - margin \* 2\)/
  );
  assert.match(
    help,
    /top: Math\.max\(minTop, Math\.min\(preferredTop, maxTop\)\)/
  );
  assert.match(help, /left: Math\.max\(\s*minLeft,/);
  assert.match(help, /ResizeObserver\(updatePosition\)/);
  assert.match(help, /resizeObserver\.disconnect\(\)/);
  assert.match(
    help,
    /visualViewport\?\.removeEventListener\("resize", updatePosition\)/
  );
  assert.match(
    help,
    /visualViewport\?\.removeEventListener\("scroll", updatePosition\)/
  );
  assert.match(
    help,
    /document\.removeEventListener\("pointerdown", closeOnOutsidePointer\)/
  );
  assert.match(
    help,
    /document\.removeEventListener\("keydown", closeOnEscape\)/
  );
});

test("moves only PageHeader and Panel descriptions into help without collapsing content", () => {
  for (const [path, description] of [
    ["../components/common/PageHeader.tsx", "subtitle"],
    ["../components/common/Panel.tsx", "description"],
  ]) {
    const component = source(path);
    assert.match(component, /mobile-help-heading/);
    assert.match(component, /mobile-description-copy/);
    assert.match(
      component,
      new RegExp(
        `<MobileHelp[\\s\\S]*?>\\s*\\{${description}\\}\\s*</MobileHelp>`
      )
    );
    assert.match(
      component,
      new RegExp(`<p[^>]*mobile-description-copy[^>]*>\\s*\\{${description}\\}`)
    );
    assert.doesNotMatch(component, /<details|<summary|aria-expanded|useState/);
  }
  const panel = source("../components/common/Panel.tsx");
  assert.match(panel, /\{children\}\s*<\/section>/);
  const kpi = source("../components/common/KpiCard.tsx");
  assert.doesNotMatch(kpi, /MobileHelp|mobile-description-copy/);
  const unix = source("../../../unix_share/src/App.tsx");
  assert.doesNotMatch(unix, /main-dashboard|mobile\.css/);
});

test("keeps unavailable comparison warnings visible instead of putting them in help", () => {
  const panel = source("../components/common/Panel.tsx");
  assert.match(panel, /descriptionRole\?: "help" \| "status"/);
  assert.match(panel, /description && descriptionRole === "help"/);
  assert.match(
    panel,
    /descriptionRole === "help" \? "mobile-description-copy" : ""/
  );
  const page = source("../pages/MarketShare.tsx");
  assert.match(page, /title="거래소 현황 요약"\s*descriptionRole="status"/);
  assert.match(
    page,
    /title="점유율 변화 계산"\s*descriptionRole=\{complete \? "help" : "status"\}/
  );
  assert.match(
    page,
    /title="주요 자산 기여"\s*descriptionRole=\{attributionReady \? "help" : "status"\}/
  );
  assert.match(
    source("../components/RollingTrendChart.tsx"),
    /descriptionRole=\{hasRollingSeries \? "help" : "status"\}/
  );
  assert.match(
    source("../pages/MarketIntelligence.tsx"),
    /description=\{`[^`]*contribution.source[^`]*`\}\s*descriptionRole="status"/
  );
  assert.match(
    source("../pages/RollingShareSummary2.tsx"),
    /title="실시간 체결 · 점유율 변화 세부 근거"\s*descriptionRole="status"/
  );
});
