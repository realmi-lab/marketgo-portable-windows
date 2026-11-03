import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { apiProxyTarget } from "../../../dev-api-target.ts";

test("Docker build includes the shared Vite configuration dependency", () => {
  const docker = readFileSync(
    new URL("../../../Dockerfile", import.meta.url),
    "utf8"
  );
  const beforeBuild = docker.split("RUN pnpm check")[0];
  assert.match(beforeBuild, /^COPY [^\n]*\bdev-api-target\.ts\b[^\n]* \.\/$/m);
});

test("development proxy supports isolated gateway and replay ports", () => {
  assert.equal(apiProxyTarget(), "http://127.0.0.1:8000");
  for (const origin of [
    "http://127.0.0.1:28000",
    "http://127.0.0.1:18080",
    "http://localhost:28000",
    "http://[::1]:28000",
  ]) {
    assert.equal(apiProxyTarget(` ${origin}/ `), origin);
  }
});

test("development proxy rejects remote origins, credentials and URL suffixes", () => {
  for (const target of [
    "https://share.realmi.net",
    "http://127.0.0.1.example.com",
    "http://user:secret@127.0.0.1:28000",
    "ws://127.0.0.1:28000",
    "http://127.0.0.1:28000/api",
    "http://127.0.0.1:28000?token=secret",
    "http://127.0.0.1:28000#fragment",
  ]) {
    assert.throws(() => apiProxyTarget(target));
  }
});
