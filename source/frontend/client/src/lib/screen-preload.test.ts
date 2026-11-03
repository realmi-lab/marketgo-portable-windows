import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { JsxEmit, ModuleKind, ScriptTarget, transpileModule } from "typescript";

// Exercise the real screen factory without mounting its unrelated UI.
const compiled = transpileModule(
  `${readFileSync(new URL("../screens/AppShell.tsx", import.meta.url), "utf8")}\nexport { screen };`,
  {
    compilerOptions: {
      module: ModuleKind.CommonJS,
      target: ScriptTarget.ES2022,
      jsx: JsxEmit.ReactJSX,
    },
  }
).outputText;

function screenFactory() {
  const exports: Record<string, any> = {};
  runInNewContext(compiled, {
    exports,
    require: (name: string) =>
      name === "react"
        ? { lazy: (load: () => Promise<unknown>) => ({ renderLoad: load }) }
        : {},
  });
  return exports.screen;
}

test("prefetch and rendering share one fulfilled screen request", async () => {
  let loads = 0;
  const component = { default: () => null };
  const screen = screenFactory()("screen", async () => {
    loads++;
    return component;
  });
  assert.equal(await screen.load(), component);
  assert.equal(await screen.component.renderLoad(), component);
  assert.equal(loads, 1);
});

test("a rejected prefetch is surfaced on navigation without skipping the failed stylesheet", async () => {
  let loads = 0;
  const error = new Error("Unable to preload CSS for /assets/screen.css");
  const screen = screenFactory()("screen", async () => {
    // A subsequent Vite loader call could skip its failed CSS dependency.
    if (++loads === 1) throw error;
    return { default: () => null };
  });
  await screen.load().catch(() => {});
  await assert.rejects(
    screen.component.renderLoad(),
    received => received === error
  );
  assert.equal(loads, 1);
});
