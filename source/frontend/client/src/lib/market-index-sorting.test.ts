import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { JsxEmit, ModuleKind, ScriptTarget, transpileModule } from "typescript";
import * as indexDay from "./index-day.ts";
import type { DomesticIndexCell, IndexDayResponse } from "./index-day.ts";

const compiled = transpileModule(
  readFileSync(new URL("../pages/MarketIndex.tsx", import.meta.url), "utf8"),
  { compilerOptions: { jsx: JsxEmit.ReactJSX, module: ModuleKind.CommonJS, target: ScriptTarget.ES2022 } }
).outputText;

type Element = { type: unknown; props: Record<string, any> };
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  if (!node || typeof node !== "object" || !node.props) return [];
  return [node, ...elements(node.props.children)];
}

// Exercise the actual page's filter, sorting, effects and rendered row order.
// HTTP and React commits are controlled; no server or DOM is involved.
function pageHarness(base: IndexDayResponse, filtered: NonNullable<IndexDayResponse["domestic"]>) {
  const cells: any[] = [];
  const timers = new Map<number, { callback: () => void; delay: number }>();
  let timerID = 0;
  let cursor = 0;
  let effects: Array<() => void> = [];
  const react = {
    useState(initial: any) {
      const index = cursor++;
      if (!(index in cells)) cells[index] = typeof initial === "function" ? initial() : initial;
      return [cells[index], (next: any) => { cells[index] = typeof next === "function" ? next(cells[index]) : next; }];
    },
    useRef(initial: unknown) {
      const index = cursor++;
      cells[index] ??= { current: initial };
      return cells[index];
    },
    useMemo(make: () => unknown) { cursor++; return make(); },
    useEffect(setup: () => (() => void) | undefined, dependencies: unknown[]) {
      const index = cursor++;
      const previous = cells[index];
      if (previous && dependencies.every((value, i) => Object.is(value, previous.dependencies[i]))) return;
      effects.push(() => {
        previous?.cleanup?.();
        cells[index] = { dependencies, cleanup: setup() };
      });
    },
  };
  const setTimeout = (callback: () => void, delay: number) => {
    timers.set(++timerID, { callback, delay });
    return timerID;
  };
  const clearTimeout = (id: number) => timers.delete(id);
  const exports: Record<string, any> = {};
  runInNewContext(compiled, {
    exports, AbortController, setTimeout, clearTimeout,
    window: { setTimeout, clearTimeout },
    require(name: string) {
      if (name === "react") return react;
      if (name === "react/jsx-runtime") return {
        jsx: (type: unknown, props: Record<string, unknown>) => ({ type, props }),
        jsxs: (type: unknown, props: Record<string, unknown>) => ({ type, props }),
      };
      if (name === "@/lib/index-day") return indexDay;
      if (name === "@/lib/api-client") return {
        getIndexDay: async () => base,
        getIndexFilters: async () => ({ assets: [], notice_assets: [] }),
        getIndexGrid: async () => ({ domestic: filtered }),
      };
      if (name === "@/lib/use-index-refresh") return { useIndexRefresh() {}, useFollowKSTToday() {} };
      if (name === "@/lib/index-refresh") return { indexRefreshInterval: () => 60_000 };
      if (name === "@/lib/index-status") return { indexHasConfirmedAmount: () => true };
      if (name === "lucide-react" || name === "@/components/IndexDataStatus" || name.endsWith(".css")) return {};
      throw Error(`Unexpected page dependency: ${name}`);
    },
  });
  const render = () => {
    cursor = 0;
    const result = exports.default();
    const batch = effects;
    effects = [];
    batch.forEach(effect => effect());
    return result;
  };
  return {
    render,
    control(label: string) {
      const control = elements(render()).find(element => element.props["aria-label"] === label);
      assert.ok(control, `Missing control: ${label}`);
      return control.props;
    },
    rows() {
      return elements(render()).filter(element => element.type === "th" && String(element.props.className).startsWith("di-asset di-band-")).map(element => element.props.children);
    },
    async settle() {
      render();
      for (const [id, timer] of timers) if (timer.delay === 150) { timers.delete(id); timer.callback(); }
      await new Promise(setImmediate);
      render();
    },
  };
}

function cell(total: number | null): DomesticIndexCell {
  return { total, share: total, krw: total, btc: null, usdt: null, partial: total === null, unavailable: false };
}
function grid(values: [string, number | null][]): NonNullable<IndexDayResponse["domestic"]> {
  return { totals: {}, coins: values.map(([base, value]) => ({ base, cells: { bithumb: cell(value) } })) };
}

test("index sorting follows the displayed filtered values, with null last and zero retained", async () => {
  const original = grid([["BTC", 10], ["ETH", 90], ["ZERO", 70], ["MISSING", 80]]);
  const filtered = grid([["BTC", 80], ["ETH", 20], ["ZERO", 0], ["MISSING", null]]);
  const before = JSON.stringify({ original, filtered });
  const page = pageHarness({ day: indexDay.currentKSTDay(), domestic: original } as IndexDayResponse, filtered);
  await page.settle();
  page.control("빗썸 점유율 정렬").onClick();
  assert.deepEqual(page.rows(), ["ETH", "MISSING", "ZERO", "BTC"]);
  page.control("마켓 표시").onChange({ target: { value: "krw" } });
  await page.settle();
  assert.deepEqual(page.rows(), ["BTC", "ETH", "ZERO", "MISSING"]);
  page.control("빗썸 점유율 정렬").onClick();
  assert.deepEqual(page.rows(), ["ZERO", "ETH", "BTC", "MISSING"]);
  page.control("마켓 표시").onChange({ target: { value: "all" } });
  assert.deepEqual(page.rows(), ["BTC", "ZERO", "MISSING", "ETH"]);
  assert.equal(JSON.stringify({ original, filtered }), before);
});
