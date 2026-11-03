import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  DASHBOARD_PAGE_NAME,
  isNoticeDraftTool,
} from "./notice-draft-probe.ts";

const shell = readFileSync(
  new URL("../../index.html", import.meta.url),
  "utf8"
);

test("the dashboard's page carries the name the probe looks for", () => {
  assert.match(
    shell,
    new RegExp(`<meta name="application-name" content="${DASHBOARD_PAGE_NAME}"`)
  );
});

test("tells the notice draft tool from the dashboard answering in its place", () => {
  const tool = '<!doctype html><title>공지 초안</title><div id="app"></div>';
  // The routed tool: framable by this site.
  assert.equal(
    isNoticeDraftTool(new Headers({ "X-Frame-Options": "SAMEORIGIN" }), tool),
    true
  );
  // nginx without the route: the dashboard, refusing frames.
  assert.equal(
    isNoticeDraftTool(new Headers({ "X-Frame-Options": "DENY" }), shell),
    false
  );
  assert.equal(
    isNoticeDraftTool(
      new Headers({ "Content-Security-Policy": "frame-ancestors 'none'" }),
      tool
    ),
    false
  );
  // A server without frame headers (the Go frontend server, Vite) that
  // answers with the dashboard's own page.
  assert.equal(isNoticeDraftTool(new Headers(), shell), false);
});
