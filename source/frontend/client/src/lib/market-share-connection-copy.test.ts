import assert from "node:assert/strict";
import test from "node:test";
import { marketShareReconnectMessage } from "./market-share-connection-copy.ts";

test("reconnect copy distinguishes retained values from awaiting first data", () => {
  assert.equal(
    marketShareReconnectMessage(true),
    "화면 연결 복구 중 · 마지막 수신값을 표시하고 있습니다. 연결되면 자동으로 갱신됩니다."
  );
  assert.equal(
    marketShareReconnectMessage(false),
    "화면 연결 복구 중입니다. 연결되면 새 자료를 표시합니다."
  );
  for (const retained of [false, true]) {
    const copy = marketShareReconnectMessage(retained);
    assert.doesNotMatch(copy, /수집 중단|수집 정상|수신 중|수신중|실시간 연결을 다시/);
  }
});
