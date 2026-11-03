import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { extname, join, relative } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const sourceRoot = fileURLToPath(new URL("../", import.meta.url));

function source(relativePath: string): string {
  return readFileSync(new URL(relativePath, import.meta.url), "utf8");
}

function productionSources(
  directory = sourceRoot
): Array<{ file: string; text: string }> {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const absolutePath = join(directory, entry.name);
    if (entry.isDirectory()) {
      return productionSources(absolutePath);
    }
    if (
      ![".css", ".ts", ".tsx"].includes(extname(entry.name)) ||
      /\.(?:test|spec)\.[^.]+$/.test(entry.name)
    ) {
      return [];
    }
    return [
      {
        file: relative(sourceRoot, absolutePath),
        text: readFileSync(absolutePath, "utf8"),
      },
    ];
  });
}

test("uses only the 14, 16, 18, 24, and 32 pixel typography steps", () => {
  const styles = source("../index.css");

  assert.match(styles, /--text-xs:\s*14px;/);
  assert.match(styles, /--text-(?:sm|base):\s*16px;/);
  assert.match(styles, /--text-(?:lg|xl|2xl):\s*24px;/);
  assert.match(styles, /--text-(?:3xl|4xl|5xl):\s*32px;/);
  assert.match(styles, /--type-caption:\s*0\.875rem;/);
  assert.match(styles, /--type-body:\s*1rem;/);
  assert.match(styles, /--type-subtitle:\s*1\.125rem;/);
  assert.match(styles, /--type-title:\s*1\.5rem;/);
  assert.match(styles, /--type-display:\s*2rem;/);
});

test("rejects typography sizes that bypass the semantic steps", () => {
  const violations: string[] = [];
  const allowedCssSize =
    /^var\(--type-(?:caption|body|subtitle|title|display-fluid|display)\)(?:\s*!important)?$/;

  for (const file of productionSources()) {
    if (file.file.endsWith(".css")) {
      // A declaration also ends at the closing brace. Requiring a semicolon
      // made minified rules swallow the following selectors, which reported
      // compliant values as violations and hid the real ones.
      for (const match of file.text.matchAll(/font-size:\s*([^;}]+)/g)) {
        const value = match[1].trim();
        if (!allowedCssSize.test(value)) {
          violations.push(`${file.file}: font-size ${value}`);
        }
      }
    }

    for (const match of file.text.matchAll(
      /\btext-\[(\d+(?:\.\d+)?(?:px|rem))\]/g
    )) {
      violations.push(`${file.file}: ${match[0]}`);
    }
  }

  assert.deepEqual(violations, []);
});

test("uses only the 400, 500, 600 and 700 font weights", () => {
  // Weights between these steps (650, 680, 720...) cannot be told apart, so
  // they blur the hierarchy instead of adding to it.
  const violations: string[] = [];
  for (const file of productionSources()) {
    if (file.file.endsWith(".css")) {
      for (const match of file.text.matchAll(/font-weight:\s*([^;}]+)/g)) {
        const value = match[1].replace(/\s*!important$/, "").trim();
        if (!/^(?:[4-7]00|inherit|normal)$/.test(value)) {
          violations.push(`${file.file}: font-weight ${value}`);
        }
      }
    }
    for (const match of file.text.matchAll(
      /\bfont-(?:thin|extralight|light|extrabold|black|\[\d+\])(?![\w-])/g
    )) {
      violations.push(`${file.file}: ${match[0]}`);
    }
  }
  assert.deepEqual(violations, []);
});

test("keeps CSS padding, margin and gap on the 4px grid", () => {
  // 5.6px, 10.4px and 14px steps made neighbouring cards look misaligned.
  // Hairlines under 3px (1px, 2px) stay allowed.
  const violations: string[] = [];
  const spacing =
    /(?<![\w-])(?:(?:padding|margin|scroll-padding)(?:-[a-z-]+)?|(?:row-|column-)?gap)\s*:\s*([^;{}]+)/g;
  for (const file of productionSources()) {
    if (!file.file.endsWith(".css")) continue;
    for (const match of file.text.matchAll(spacing)) {
      for (const length of match[1].matchAll(
        /(?<![\w.#-])(\d*\.?\d+)(px|rem)\b/g
      )) {
        const px = Number(length[1]) * (length[2] === "rem" ? 16 : 1);
        if (px >= 3 && px % 4 !== 0) {
          violations.push(`${file.file}: ${match[0].trim()}`);
          break;
        }
      }
    }
  }
  assert.deepEqual(violations, []);
});

test("keeps common controls readable and supporting copy compact", () => {
  const pageHeader = source("../components/common/PageHeader.tsx");
  const panel = source("../components/common/Panel.tsx");
  const kpi = source("../components/common/KpiCard.tsx");
  const controls = source("../components/common/SelectionControl.tsx");

  assert.match(pageHeader, /ui-caption/);
  assert.match(pageHeader, /ui-title/);
  assert.match(panel, /ui-caption panel-description/);
  assert.match(panel, /ui-title/);
  assert.match(kpi, /ui-caption metric-panel-detail/);
  assert.doesNotMatch(controls, /text-xs/);
  assert.equal(controls.match(/text-(?:sm|base)/g)?.length, 2);
});

test("separates supporting analysis copy from monetary values", () => {
  const styles = source("../index.css");
  const marketShare = source("../pages/MarketShare.tsx");

  assert.match(styles, /text-muted-foreground:not\(\.ui-body\)/);
  assert.doesNotMatch(styles, /:not\(\.text-base\)/);
  assert.match(
    styles,
    /\.asset-impact-value\s*\{[^}]*font-variant-numeric:\s*tabular-nums/s
  );
  assert.match(
    marketShare,
    /market-brief-copy ui-caption[^>]*>[\s\S]*?assetImpactInterpretation\(primaryCoin\)/
  );
  assert.equal(marketShare.match(/asset-impact-value ui-body/g)?.length, 2);
  assert.match(marketShare, /asset-impact-copy ui-caption/);
});

test("keeps chart interpretation guidance at the caption step", () => {
  const marketIntelligence = source("../pages/MarketIntelligence.tsx");

  assert.match(
    marketIntelligence,
    /<p className="ui-caption mt-2 text-muted-foreground">\s*거래금액 상위 코인을 표시합니다\./
  );
});

test("keeps the dashboard mode explanation at the caption step", () => {
  const dashboard = source("../pages/MarketShareDashboard.tsx");

  assert.match(
    dashboard,
    /<p className="ui-caption mt-1 text-muted-foreground mobile-description-copy">\s*공식 24시간 변화와 선택한 구간의 요약·상세 분석을 확인합니다\./
  );
});

test("keeps evidence explanations compact while preserving primary values", () => {
  const styles = source("../index.css");
  const marketShare = source("../pages/MarketShare.tsx");
  const officialMovement = source("../components/OfficialMovementPanel.tsx");

  assert.match(
    styles,
    /\.summary-evidence-layer-header p\s*\{[^}]*font-size:\s*var\(--type-caption\)/s
  );
  assert.match(
    styles,
    /\.summary-evidence-basis\s*\{[^}]*font-size:\s*var\(--type-caption\)/s
  );
  assert.match(
    styles,
    /\.summary-evidence-value\s*\{[^}]*font-size:\s*var\(--type-body\)/s
  );
  assert.match(marketShare, /ui-caption font-normal text-muted-foreground/);
  assert.match(
    officialMovement,
    /ui-caption mt-1 text-muted-foreground">\s*공식 변화 시각 직전/
  );
});

test("keeps legacy read-only metadata compact without shrinking controls", () => {
  const styles = source("../index.css");
  const intelligenceStyles = source("../market-intelligence-experience.css");
  const marketShare = source("../pages/MarketShare.tsx");

  assert.match(
    styles,
    /:where\(div, span, caption, thead\)\.text-muted-foreground\.text-base:not\(\s*\.ui-body\s*\)/s
  );
  assert.doesNotMatch(styles, /:where\(button, a\)\.text-muted-foreground/);
  assert.match(
    styles,
    /\.terminal-shell :where\(dt, th\):not\(\.ui-body\)[^{]*\{[^}]*font-size:\s*var\(--type-caption\)/s
  );
  assert.match(
    intelligenceStyles,
    /\.mi-chapter-progress,[\s\S]*?\.mi-chapter-selected-asset\s*\{[^}]*font-size:\s*var\(--type-caption\)/
  );
  assert.match(
    intelligenceStyles,
    /\.mi-section-description\s*\{[^}]*font-size:\s*var\(--type-caption\)/
  );
  assert.match(
    marketShare,
    /ui-caption text-muted-foreground">\s*\{orderbookDiagnosisLabel/
  );
});
