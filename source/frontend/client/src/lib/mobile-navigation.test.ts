import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const layout = readFileSync(
  new URL("../components/DashboardLayout.tsx", import.meta.url),
  "utf8"
);

test("isolates the mobile dashboard shell from the Unix frontend", () => {
  const unix = readFileSync(
    new URL("../../../unix_share/src/App.tsx", import.meta.url),
    "utf8"
  );

  assert.match(layout, /className="main-dashboard terminal-shell/);
  assert.match(layout, /data-section=\{activeSection\}/);
  assert.match(layout, /\bh-dvh\b/);
  for (const edge of ["top", "left", "right"]) {
    assert.ok(layout.includes(`env(safe-area-inset-${edge}, 0px)`));
  }
  const mobileStyles = readFileSync(
    new URL("../mobile.css", import.meta.url),
    "utf8"
  );
  assert.match(
    mobileStyles,
    /\.main-dashboard \.dashboard-content\s*\{[^}]*env\(safe-area-inset-bottom(?:,\s*0px)?\)/s
  );
  assert.doesNotMatch(layout, /paddingBottom:/);
  assert.doesNotMatch(unix, /main-dashboard/);
  assert.doesNotMatch(layout, /\bh-screen\b/);
});

test("keeps full accessible names and readable short mobile labels", () => {
  const sections = layout.slice(
    layout.indexOf("const SECTIONS = ["),
    layout.indexOf("] satisfies")
  );
  const expected = [
    ["summary", "요약", "요약"],
    ["share-llm", "점유율 질문", "점유율 질문"],
    ["intelligence", "시장 분석", "시장 분석"],
    ["drivers", "코인별 점유율 기여", "코인 기여"],
    ["events", "공지·시장 이벤트", "공지·이벤트"],
    ["notice-llm2", "공지 초안 작성", "공지 작성"],
    ["quality", "데이터 품질", "데이터 품질"],
    ["index", "거래소 인덱스", "거래소 인덱스"],
    ["index2", "코인 인덱스", "코인 인덱스"],
  ];
  assert.equal(sections.match(/mobileLabel:/g)?.length, expected.length);
  for (const [id, label, mobileLabel] of expected) {
    assert.match(
      sections,
      new RegExp(
        `id: "${id}",\\s*label: "${label}",\\s*mobileLabel: "${mobileLabel}"`
      )
    );
  }
  const mobile = layout.slice(
    layout.indexOf("ref={mobileNavigationRef}"),
    layout.indexOf("</nav>", layout.indexOf("ref={mobileNavigationRef}"))
  );
  assert.match(mobile, /mobile-navigation flex shrink-0/);
  assert.match(mobile, /overflow-x-auto overscroll-x-contain/);
  assert.match(mobile, /data-section-target=\{id\}/);
  assert.match(
    mobile,
    /aria-label=\{\s*mobileLabel === label \? label : `\$\{mobileLabel\} \(\$\{label\}\)`\s*\}/
  );
  assert.match(mobile, /aria-current=\{activeSection === id \? "page"/);
  assert.match(mobile, /mobile-navigation-item flex min-h-12 shrink-0/);
  assert.match(mobile, /whitespace-nowrap/);
  assert.match(mobile, /text-base/);
  assert.match(mobile, /className="mobile-navigation-label">\{mobileLabel\}/);
  assert.doesNotMatch(mobile, /grid-cols-7|hidden truncate|sm:inline/);
});

test("keeps the mobile title and touch-sized theme control without perspective menus", () => {
  const mobileContext = layout.slice(
    layout.indexOf('className="app-mobile-context'),
    layout.indexOf("ref={mobileNavigationRef}")
  );
  assert.match(mobileContext, /md:hidden/);
  assert.match(mobileContext, /app-mobile-section-title/);
  assert.match(mobileContext, /\{activeMobileLabel\}/);
  assert.match(
    layout,
    /activeMobileLabel = selectedSection\?\.mobileLabel \?\? activeLabel/
  );
  assert.match(mobileContext, /text-base/);
  assert.doesNotMatch(layout, /분석 관점 거래소|app-(?:mobile-)?exchange-control/);
  assert.match(layout, /size="lg"\s*className="app-theme-toggle md:size-8/);
  assert.match(layout, /app-sidebar hidden w-56/);
});

test("keeps route focus and active navigation visible without clock announcements", () => {
  assert.match(layout, /content\.scrollTo\(\{\s*top: 0/);
  assert.match(layout, /content\.focus\(\{ preventScroll: true \}\)/);
  assert.match(
    layout,
    /querySelector<HTMLElement>\('\[aria-current="page"\]'\)/
  );
  assert.match(layout, /scrollIntoView\(\{[\s\S]*?inline: "center"/);
  assert.match(layout, /role="status" aria-live="polite" aria-atomic="true"/);
  assert.match(
    layout,
    /className="app-live-time hidden md:inline"\s*aria-hidden="true"/
  );
  assert.match(layout, /aria-label=\{`\$\{activeLabel\} 화면`\}/);
});
