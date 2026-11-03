import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { ModuleKind, ScriptTarget, transpileModule } from "typescript";

const compiled = transpileModule(
  readFileSync(new URL("../screens/data.ts", import.meta.url), "utf8"),
  {
    compilerOptions: {
      module: ModuleKind.CommonJS,
      target: ScriptTarget.ES2022,
    },
  }
).outputText;

function pollHarness(intervalMS: number, online = true) {
  const document = Object.assign(new EventTarget(), {
    visibilityState: "visible",
  });
  const window = new EventTarget();
  const navigator = { onLine: online };
  const timers = new Map<number, () => void>();
  let timerID = 0;
  let unsubscribe: (() => void) | undefined;
  const exports: Record<string, any> = {};
  runInNewContext(compiled, {
    exports,
    document,
    window,
    navigator,
    AbortController,
    setTimeout: (callback: () => void) => {
      timers.set(++timerID, callback);
      return timerID;
    },
    clearTimeout: (id: number) => timers.delete(id),
    require: (name: string) =>
      name === "react"
        ? {
            useCallback: (callback: unknown) => callback,
            useRef: (value: unknown) => ({ current: value }),
            useSyncExternalStore: (
              subscribe: (reader: () => void) => () => void,
              snapshot: () => unknown
            ) => {
              unsubscribe ??= subscribe(() => {});
              return snapshot();
            },
          }
        : name.endsWith("screen-copy")
          ? {
              readFailureReason: (error: Error) => error.message,
            }
          : {},
  });
  const pending: { signal: AbortSignal; resolve: (value: unknown) => void }[] =
    [];
  const load = (signal: AbortSignal) =>
    new Promise(resolve => pending.push({ signal, resolve }));
  return {
    read: () => exports.usePolled("resource", load, intervalMS),
    pending,
    timers,
    network: (online: boolean) => {
      navigator.onLine = online;
      window.dispatchEvent(new Event(online ? "online" : "offline"));
    },
    visibility: (visible: boolean) => {
      document.visibilityState = visible ? "visible" : "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
    },
    close: () => unsubscribe?.(),
  };
}

const settled = () => new Promise(setImmediate);

test("long-interval reads retain data offline and refresh immediately once online", async () => {
  const h = pollHarness(5 * 60_000);
  h.read();
  const value = { missing: null, zero: 0 };
  h.pending[0].resolve(value);
  await settled();
  const receivedAtMS = h.read().receivedAtMS;
  h.network(false);
  assert.equal(h.read().data, value);
  assert.equal(h.read().receivedAtMS, receivedAtMS);
  assert.equal(h.read().stale, true);
  assert.equal(h.timers.size, 0);
  assert.equal(h.pending.length, 1);
  h.network(true);
  h.network(true);
  assert.equal(
    h.pending.length,
    2,
    "reconnect starts exactly one read without waiting five minutes"
  );
  h.pending[1].resolve({ missing: null, zero: 0, newer: true });
  await settled();
  assert.equal(h.read().data.newer, true);
  assert.equal(h.read().stale, false);
  assert.equal(h.read().error, "");
  h.close();
});

test("offline cancels in-flight reads and ignores late completion after reconnect", async () => {
  const h = pollHarness(0);
  h.read();
  h.network(false);
  assert.equal(h.pending[0].signal.aborted, true);
  assert.equal(h.read().loading, false);
  h.network(true);
  h.pending[1].resolve("current");
  await settled();
  h.pending[0].resolve("obsolete");
  await settled();
  assert.equal(h.read().data, "current");
  assert.equal(h.timers.size, 0);
  h.close();
});

test("a one-time read opened offline recovers when visible and online", async () => {
  const h = pollHarness(0, false);
  assert.equal(h.read().loading, false);
  assert.notEqual(h.read().error, "");
  assert.equal(h.pending.length, 0);
  h.visibility(false);
  h.network(true);
  assert.equal(h.pending.length, 0, "hidden screens stay paused");
  h.visibility(true);
  assert.equal(h.pending.length, 1);
  h.pending[0].resolve(null);
  await settled();
  assert.equal(h.read().data, null);
  assert.equal(h.read().error, "");
  h.close();
  h.network(false);
  h.network(true);
  assert.equal(h.pending.length, 1, "inactive cached resources do not fetch");
});
