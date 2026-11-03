import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

for (const file of ["nginx.conf", "nginx.notice-llm2.conf"]) {
  test(`${file} resolves the gateway without requiring DNS at startup`, () => {
    const config = readFileSync(new URL(`../../../deploy/${file}`, import.meta.url), "utf8");
    assert.match(config, /resolver 127\.0\.0\.11 valid=5s ipv6=off;/);
    assert.match(config, /set \$marketgo_backend http:\/\/router:8000;/);
    assert.doesNotMatch(config, /proxy_pass http:\/\/router:/);
    assert.equal((config.match(/proxy_pass \$marketgo_backend;/g) ?? []).length, 4);
  });
}

test("notice draft proxy covers the 120 second model deadline on both routes", () => {
 const config = readFileSync(new URL("../../../deploy/nginx.notice-llm2.conf", import.meta.url), "utf8");
 for (const route of ["/notice-llm2/api/market-share/notice-draft", "/api/market-share/notice-draft"]) {
  const block=config.split(`location = ${route} {`)[1]?.split("\n    }")[0];
  assert.ok(block);
  assert.match(block,/proxy_read_timeout 130s;/);
  assert.match(block,/proxy_buffering off;/);
 }
});
