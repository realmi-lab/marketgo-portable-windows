import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { ModuleKind, ScriptTarget, transpileModule } from "typescript";
import * as history from "./live-chart.ts";
import * as overview from "./market-share-overview-point.ts";
import type {
  MarketScope,
  MarketShareClosedSeconds,
  MarketShareRealtimeTick,
} from "./market-share-types.ts";

const compiled = transpileModule(
  readFileSync(
    new URL("../hooks/useLiveOverviewHistory.ts", import.meta.url),
    "utf8"
  ),
  {
    compilerOptions: {
      module: ModuleKind.CommonJS,
      target: ScriptTarget.ES2022,
    },
  }
).outputText;

// Run the actual hook and point builders with controlled React commit/cleanup
// and HTTP completion, including a transport that resolves after cancellation.
function harness() {
  const cells: any[] = [];
  let cursor = 0;
  let effects: Array<() => void> = [];
  const pending: Array<{
    signal: AbortSignal;
    resolve: (value: MarketShareClosedSeconds) => void;
  }> = [];
  const react = {
    useState(initial: any) {
      const index = cursor++;
      if (!(index in cells))
        cells[index] = typeof initial === "function" ? initial() : initial;
      return [
        cells[index],
        (next: any) => {
          cells[index] = typeof next === "function" ? next(cells[index]) : next;
        },
      ];
    },
    useRef(initial: unknown) {
      const index = cursor++;
      cells[index] ??= { current: initial };
      return cells[index];
    },
    useEffect(setup: () => (() => void) | undefined, dependencies: unknown[]) {
      const index = cursor++;
      const previous = cells[index];
      if (
        previous &&
        dependencies.every((value, i) =>
          Object.is(value, previous.dependencies[i])
        )
      )
        return;
      effects.push(() => {
        previous?.cleanup?.();
        cells[index] = { dependencies, cleanup: setup() };
      });
    },
  };
  const exports: Record<string, any> = {};
  runInNewContext(compiled, {
    exports,
    AbortController,
    require(name: string) {
      if (name === "react") return react;
      if (name === "@/lib/live-chart") return history;
      if (name === "@/lib/market-share-overview-point") return overview;
      if (name === "@/lib/api-client")
        return {
          getClosedSeconds: (
            _: number,
            _scope: MarketScope,
            signal: AbortSignal
          ) => new Promise(resolve => pending.push({ signal, resolve })),
        };
      throw new Error(`Unexpected hook dependency: ${name}`);
    },
  });
  const props = {
    tick: null as MarketShareRealtimeTick | null,
    connection: "live",
    marketScope: "KRW" as MarketScope,
    requestedWindowSec: 10,
  };
  const commit = () => {
    const batch = effects;
    effects = [];
    batch.forEach(run => run());
  };
  return {
    props,
    pending,
    commit,
    read(commitEffects = true): history.LiveChartHistoryPoint[] {
      cursor = 0;
      const result = exports.useLiveOverviewHistory(props);
      if (commitEffects) commit();
      return result;
    },
  };
}

function answer(
  windowSec: number,
  at: number,
  marketScope: MarketScope = "KRW"
): MarketShareClosedSeconds {
  return {
    window_sec: windowSec,
    market_scope: marketScope,
    seconds: [
      {
        as_of_second: at,
        window_complete: true,
        coverage_sec: windowSec,
        exchanges: [
          {
            exchange: "bithumb",
            share_pct: 30,
            volume_krw: 100,
            trade_count: 10,
            trade_count_share_pct: 30,
          },
        ],
      },
    ],
  };
}

test("a changed window never renders the previous window or admits its late HTTP seed", async () => {
  const app = harness();
  app.read();
  app.pending[0].resolve(answer(10, 100));
  await new Promise(setImmediate);
  assert.equal(app.read()[0].rate_bithumb, 10);
  app.props.requestedWindowSec = 30;
  assert.equal(
    app.read(false).length,
    0,
    "hide old-window values before effects commit"
  );
  app.commit();
  assert.equal(app.pending[0].signal.aborted, true);
  app.props.requestedWindowSec = 60;
  app.read();
  app.pending[2].resolve(answer(60, 120));
  await new Promise(setImmediate);
  app.pending[1].resolve(answer(30, 110));
  await new Promise(setImmediate);
  const rows = app.read();
  assert.equal(
    rows.length,
    1,
    "an aborted 30-second response cannot prefix the 60-second history"
  );
  assert.equal(rows[0].at, 121_000);
  assert.equal(rows[0].rate_bithumb, 100 / 60);
});

test("a seed for a different response window or scope is not charted", async () => {
  for (const wrong of [
    answer(30, 100),
    answer(10, 100, "ALL_DOMESTIC_CONVERTED"),
  ]) {
    const app = harness();
    app.read();
    app.pending[0].resolve(wrong);
    await new Promise(setImmediate);
    assert.equal(app.read().length, 0);
  }
});

test("a canceled seed cannot prefix the current window after its replacement has loaded", async () => {
  const app = harness();
  app.read();
  app.props.requestedWindowSec = 30;
  app.read();
  app.pending[1].resolve(answer(30, 110));
  await new Promise(setImmediate);
  app.pending[0].resolve(answer(10, 100));
  await new Promise(setImmediate);
  const rows = app.read();
  assert.equal(rows.length, 1);
  assert.equal(rows[0].rate_bithumb, 100 / 30);
});

test("reconnection keeps its explicit break until a genuinely newer chart point arrives", () => {
  const app = harness();
  const tick = (at: number) =>
    ({
      generated_at: at + 1,
      market_scope: "KRW",
      window_sec: 10,
      coverage_sec: 10,
      ws_realtime_complete: true,
      closed_second: answer(10, at).seconds[0],
    }) as MarketShareRealtimeTick;
  app.props.tick = tick(100);
  app.read();
  app.props.connection = "stale";
  app.read();
  const gap = app.read().at(-1)!;
  assert.equal(gap.gap, 1);
  app.props.connection = "live";
  app.read();
  assert.equal(app.read().at(-1), gap);
  app.props.tick = tick(102);
  app.read();
  const rows = app.read();
  assert.equal(rows.length, 3);
  assert.equal(rows[1], gap);
  assert.equal(rows[2].at, 103_000);
});
