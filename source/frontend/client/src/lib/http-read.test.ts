import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import test from "node:test";
import { fetchRead } from "./http-read.ts";

test("read deadline covers headers and body, preserves caller cancellation and normal JSON", async () => {
  const server = createServer((req, res) => {
    if (req.url === "/headers") return;
    res.writeHead(200, { "Content-Type": "application/json" });
    res.flushHeaders();
    if (req.url === "/body") { res.write('{"pending":'); return; }
    res.end('{"value":null,"zero":0}');
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  assert.ok(address && typeof address !== "string");
  const base = `http://127.0.0.1:${address.port}`;
  try {
    assert.deepEqual(await (await fetchRead(base)).json(), { value: null, zero: 0 });
    await assert.rejects(fetchRead(`${base}/headers`, {}, 50), { name: "TimeoutError" });
    const response = await fetchRead(`${base}/body`, {}, 100);
    await assert.rejects(response.json(), error => error instanceof Error && /abort|timeout/i.test(error.name));
    const controller = new AbortController();
    const canceled = fetchRead(`${base}/headers`, { signal: controller.signal });
    controller.abort();
    await assert.rejects(canceled, { name: "AbortError" });
  } finally {
    server.closeAllConnections();
    server.close();
    await once(server, "close");
  }
});
