import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const shell = readFileSync(new URL("../../index.html", import.meta.url), "utf8");

test("production shell has no third-party runtime resources", () => {
  assert.doesNotMatch(shell, /(?:src|href)=["']https?:\/\//);
  assert.doesNotMatch(shell, /mcp\.figma|cdn\.jsdelivr/);
  assert.match(shell, /src="\/src\/main\.tsx"/);
});
