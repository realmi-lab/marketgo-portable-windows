import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

test("market answer identifies Astra and preserves nonwrapping main report cells", () => {
  const source = readFileSync(new URL("../components/HermesMarketQuestion.tsx", import.meta.url), "utf8");
  assert.match(source, /provider\.model === "gpt-6-astra"/);
  assert.match(source, /AI · GPT-6 Astra 답변/);
  assert.match(source, /components=\{\s*mainDashboard\s*\?/);
  assert.match(source, /<th \{\.\.\.props\} className="whitespace-nowrap"/);
  assert.match(source, /<td \{\.\.\.props\} className="whitespace-nowrap"/);
});
