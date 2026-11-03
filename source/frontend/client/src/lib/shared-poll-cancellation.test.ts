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

test("a stopped shared poll cannot overwrite a newer response after visibility resumes", async () => {
  const visibility = {
    visibilityState: "visible",
    addEventListener: (_: string, callback: () => void) => {
      changed = callback;
    },
  };
  let changed = () => {};
  let unsubscribe: (() => void) | undefined;
  let notifications = 0;
  const timers = new Map<number, () => void>();
  let timerID = 0;
  const exports: Record<string, any> = {};
  runInNewContext(compiled, {
    exports,
    document: visibility,
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
              unsubscribe ??= subscribe(() => notifications++);
              return snapshot();
            },
          }
        : name.endsWith("screen-copy")
          ? { readFailureReason: (error: Error) => error.message }
          : {},
  });
  const pending: { signal: AbortSignal; resolve: (value: string) => void }[] =
    [];
  const load = (signal: AbortSignal) =>
    new Promise<string>(resolve => pending.push({ signal, resolve }));
  const read = () => exports.usePolled("same-key", load, 10_000);
  assert.equal(read().loading, true);
  visibility.visibilityState = "hidden";
  changed();
  assert.equal(pending[0].signal.aborted, true);
  visibility.visibilityState = "visible";
  changed();
  assert.equal(pending.length, 2);
  pending[1].resolve("newer");
  await new Promise(setImmediate);
  const receivedAt = read().receivedAtMS;
  // Some transports finish body decoding after cancellation. Its old success
  // still must not be admitted into the shared page cache or notify readers.
  pending[0].resolve("obsolete");
  await new Promise(setImmediate);
  assert.equal(read().data, "newer");
  assert.equal(read().receivedAtMS, receivedAt);
  assert.equal(notifications, 1);
  assert.equal(timers.size, 1);
  unsubscribe?.();
});
