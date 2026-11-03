import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { JsxEmit, ModuleKind, ScriptTarget, transpileModule } from "typescript";

import { isStaleBuildError, reloadForNewBuild } from "./stale-build-reload.ts";

test("recognizes missing-chunk errors from Chrome, Firefox and Safari", () => {
  for (const message of [
    "Failed to fetch dynamically imported module: https://share.realmi.net/assets/OfficialTrendCharts-s34soaft.js",
    "error loading dynamically imported module: https://share.realmi.net/assets/a.js",
    "Importing a module script failed.",
    "Unable to preload CSS for /assets/NoticePortal.css",
  ]) {
    assert.equal(isStaleBuildError(new TypeError(message)), true, message);
  }
  assert.equal(
    isStaleBuildError(new Error("Cannot read properties of undefined")),
    false
  );
});

function recoveryHarness(online: boolean) {
  const stored = new Map<string, string>();
  let reloads = 0;
  const window = Object.assign(new EventTarget(), {
    location: { reload: () => reloads++ },
  });
  const navigator = { onLine: online };
  const context = {
    window,
    navigator,
    Error,
    sessionStorage: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => stored.set(key, value),
    },
    console: { error: () => {} },
    document: { getElementById: () => ({}) },
  };
  const modules: Record<string, any> = {};
  const evaluate = (path: string) => {
    const compiled = transpileModule(
      readFileSync(new URL(path, import.meta.url), "utf8"),
      {
        compilerOptions: {
          module: ModuleKind.CommonJS,
          target: ScriptTarget.ES2022,
          jsx: JsxEmit.ReactJSX,
        },
      }
    ).outputText;
    const exports: Record<string, any> = {};
    runInNewContext(compiled, {
      ...context,
      exports,
      require: (name: string) =>
        name.endsWith("stale-build-reload")
          ? modules.recovery
          : name === "react"
            ? { Component: class {} }
            : name === "react/jsx-runtime"
              ? { jsx: () => null }
              : name === "react-dom/client"
                ? { createRoot: () => ({ render: () => {} }) }
                : {},
    });
    return exports;
  };
  modules.recovery = evaluate("./stale-build-reload.ts");
  return { evaluate, navigator, window, stored, reloads: () => reloads };
}

test("a background preload failure keeps the mounted app and does not consume its recovery guard", () => {
  const h = recoveryHarness(true);
  h.evaluate("../main.tsx");
  const event = new Event("vite:preloadError", { cancelable: true });
  Object.assign(event, {
    payload: new TypeError("Failed to fetch dynamically imported module"),
  });
  h.window.dispatchEvent(event);
  assert.equal(h.reloads(), 0);
  assert.equal(h.stored.size, 0);
  assert.equal(
    event.defaultPrevented,
    false,
    "the actual loader still receives its error"
  );
});

test("a required chunk failure offline waits for reconnect and reloads only once", () => {
  const h = recoveryHarness(false);
  const Boundary = h.evaluate("../components/ErrorBoundary.tsx").default;
  const boundary = new Boundary({ children: null });
  boundary.componentDidMount();
  const error = new Error("Unable to preload CSS for /assets/screen.css");
  boundary.state = Boundary.getDerivedStateFromError(error);
  boundary.componentDidCatch(error);
  assert.equal(h.reloads(), 0);
  assert.equal(
    h.stored.size,
    0,
    "offline failure must not consume the reload guard"
  );
  h.navigator.onLine = true;
  h.window.dispatchEvent(new Event("online"));
  h.window.dispatchEvent(new Event("online"));
  assert.equal(h.reloads(), 1);
  boundary.componentWillUnmount();
  h.stored.clear();
  h.window.dispatchEvent(new Event("online"));
  assert.equal(
    h.reloads(),
    1,
    "unmounted boundaries remove their retry listener"
  );
});

test("ordinary rendering errors do not reload after connectivity returns", () => {
  const h = recoveryHarness(true);
  const Boundary = h.evaluate("../components/ErrorBoundary.tsx").default;
  const boundary = new Boundary({ children: null });
  boundary.componentDidMount();
  const error = new Error("Cannot read properties of undefined");
  boundary.state = Boundary.getDerivedStateFromError(error);
  boundary.componentDidCatch(error);
  h.window.dispatchEvent(new Event("online"));
  assert.equal(h.reloads(), 0);
  boundary.componentWillUnmount();
});

test("reloads at most once a minute and never without a stored guard", () => {
  const stored = new Map<string, string>();
  let reloads = 0;
  Object.assign(globalThis, {
    sessionStorage: {
      getItem: (key: string) => stored.get(key) ?? null,
      setItem: (key: string, value: string) => stored.set(key, value),
    },
    window: { location: { reload: () => (reloads += 1) } },
  });

  assert.equal(reloadForNewBuild(1_000_000), true);
  assert.equal(reloadForNewBuild(1_030_000), false);
  assert.equal(reloadForNewBuild(1_061_000), true);
  assert.equal(reloads, 2);

  Object.assign(globalThis, {
    sessionStorage: {
      getItem: () => {
        throw new Error("blocked");
      },
    },
  });
  assert.equal(reloadForNewBuild(2_000_000), false);
  assert.equal(reloads, 2);
});
