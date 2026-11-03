import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test, { beforeEach } from "node:test";
import { runInNewContext } from "node:vm";
import { ModuleKind, ScriptTarget, transpileModule } from "typescript";

import * as transport from "./market-share-transport.ts";
import * as payload from "./market-share-payload.ts";
import * as flowStore from "./market-share-realtime-flow-store.ts";
import * as whaleAlerts from "./market-share-whale-alert.ts";
import { createMarketShareTickStore } from "./market-share-tick-store.ts";
import { marketShareReconnectMessage } from "./market-share-connection-copy.ts";
import * as coinShares from "./coin-shares-presentation.ts";
import * as reducerStore from "./reducer-store.ts";

// Compile the unchanged hook once; each case still gets a fresh VM and stores.
const compiledHook = transpileModule(
  readFileSync(new URL("../hooks/useMarketShare.ts", import.meta.url), "utf8"),
  {
    compilerOptions: {
      module: ModuleKind.CommonJS,
      target: ScriptTarget.ES2022,
    },
  }
).outputText;
// Session-storage persistence has its own suite. Keep these transport cases
// isolated and avoid waiting for its unrelated five-second flush timer.
beforeEach(() => payload.__resetMarketSharePayloadCacheForTests(null));

// Execute the real hook, payload normalization/merge and both stores. Only the
// React lifecycle, transport and notification clock are controlled here; the
// built-browser replay separately checks the actual ten-second consumer.
function streamHarness(online = true, deferTransitions = false) {
  type Host = {
    cursor: number;
    cells: any[];
    effects: Array<() => any>;
    cleanups: Array<() => void>;
  };
  let host: Host;
  let nextTimerId = 0;
  const timers = new Map<number, () => void>();
  let now = Date.now();
  const published: Array<{ connection?: string; lastMessageAt?: number }> = [];
  const publishLiveStatus = (status: (typeof published)[number]) => {
    published.push(status);
  };
  let intervalCallback = () => {};
  let inTransition = false;
  const transitionUpdates: Array<() => void> = [];
  const sockets: Socket[] = [];
  class Socket {
    static OPEN = 1;
    static CONNECTING = 0;
    readyState = 1;
    opened = false;
    onopen?: () => void;
    onmessage?: (event: { data: string }) => void;
    onclose?: (event: {
      code: number;
      reason: string;
      wasClean: boolean;
    }) => void;
    onerror?: () => void;
    constructor() {
      sockets.push(this);
    }
    close() {
      this.readyState = 3;
    }
    frame(value: unknown) {
      if (!this.opened) {
        this.opened = true;
        this.onopen?.();
      }
      this.onmessage!({ data: JSON.stringify(value) });
    }
  }
  const react = {
    startTransition: (callback: () => void) => {
      inTransition = true;
      try {
        callback();
      } finally {
        inTransition = false;
      }
    },
    useState(initial: any) {
      const owner = host,
        index = host.cursor++;
      if (!(index in owner.cells))
        owner.cells[index] =
          typeof initial === "function" ? initial() : initial;
      return [
        owner.cells[index],
        (next: any) => {
          const update = () => {
            owner.cells[index] =
              typeof next === "function" ? next(owner.cells[index]) : next;
          };
          if (inTransition && deferTransitions) transitionUpdates.push(update);
          else update();
        },
      ];
    },
    useRef(initial: unknown) {
      const index = host.cursor++;
      if (!(index in host.cells)) host.cells[index] = { current: initial };
      return host.cells[index];
    },
    useMemo(factory: () => unknown) {
      const index = host.cursor++;
      if (!(index in host.cells)) host.cells[index] = factory();
      return host.cells[index];
    },
    useEffect(effect: () => unknown) {
      host.effects.push(effect);
    },
  };
  const network = { onLine: online };
  const listeners = new Map<string, Set<() => void>>();
  const events = {
    addEventListener(type: string, callback: () => void) {
      if (!listeners.has(type)) listeners.set(type, new Set());
      listeners.get(type)!.add(callback);
    },
    removeEventListener(type: string, callback: () => void) {
      listeners.get(type)?.delete(callback);
    },
  };
  const module = { exports: {} as { useMarketShare: (...args: any[]) => any } };
  runInNewContext(compiledHook, {
    module,
    exports: module.exports,
    console,
    Date: class extends Date {
      static now() {
        return now;
      }
    },
    Math,
    WebSocket: Socket,
    setTimeout: (callback: () => void) => {
      const id = ++nextTimerId;
      timers.set(id, callback);
      return id;
    },
    clearTimeout: (id: number) => timers.delete(id),
    setInterval: (callback: () => void) => {
      intervalCallback = callback;
      return 1;
    },
    clearInterval() {},
    window: events,
    document: { ...events, visibilityState: "visible" },
    navigator: network,
    require(name: string) {
      if (name === "react") return react;
      if (name === "@/lib/market-share-transport") return transport;
      if (name === "@/contexts/LiveStatusContext")
        return { useLiveStatusPublisher: () => publishLiveStatus };
      if (name === "@/lib/api-client")
        return { getMarketShareWebSocketUrl: () => "ws://fixture.invalid" };
      if (name === "@/lib/market-share-connection-copy")
        return { marketShareReconnectMessage };
      if (name === "@/lib/market-share-payload") return payload;
      if (name === "@/lib/market-share-whale-alert") return whaleAlerts;
      if (name === "@/lib/market-share-realtime-flow-store") return flowStore;
      if (name === "@/lib/coin-shares-presentation") return coinShares;
      if (name === "@/lib/reducer-store") return reducerStore;
      if (name === "@/lib/market-share-tick-store")
        return {
          createMarketShareTickStore: (_scheduler: unknown, initial: any) =>
            createMarketShareTickStore(
              { request: () => 1, cancel() {} },
              initial
            ),
        };
      throw new Error(`Unexpected hook dependency: ${name}`);
    },
  });
  return {
    published,
    sockets,
    flushTransition: () => transitionUpdates.shift()?.(),
    setOnline(value: boolean) {
      network.onLine = value;
      listeners
        .get(value ? "online" : "offline")
        ?.forEach(callback => callback());
    },
    listenerCount: () =>
      [...listeners.values()].reduce((sum, set) => sum + set.size, 0),
    mount(windowSec = 60) {
      host = { cursor: 0, cells: [], effects: [], cleanups: [] };
      const owner = host;
      const result = module.exports.useMarketShare(
        windowSec,
        null,
        "KRW",
        "full"
      );
      const setup = () => {
        owner.cleanups = owner.effects
          .map(effect => effect())
          .filter(cleanup => typeof cleanup === "function");
      };
      const cleanup = () => owner.cleanups.forEach(dispose => dispose());
      setup();
      return {
        result,
        advance: (ms: number) => {
          now += ms;
          intervalCallback();
        },
        reconnect: () => {
          const previousSocket = sockets.at(-1)!;
          previousSocket.readyState = 3;
          previousSocket.onclose!({ code: 1006, reason: "", wasClean: false });
          assert.equal(timers.size, 1, "close schedules one reconnect");
          const [id, callback] = timers.entries().next().value!;
          timers.delete(id);
          callback();
          assert.notEqual(sockets.at(-1), previousSocket);
          return previousSocket;
        },
        read: () => {
          host = owner;
          owner.cursor = 0;
          const effects = owner.effects;
          owner.effects = [];
          const current = module.exports.useMarketShare(
            windowSec,
            null,
            "KRW",
            "full"
          );
          owner.effects = effects;
          return current;
        },
        socket: () => sockets.at(-1)!,
        retry: () => {
          assert.equal(timers.size, 1);
          const [id, callback] = timers.entries().next().value!;
          timers.delete(id);
          callback();
        },
        unmount: cleanup,
        restartEffects: () => {
          cleanup();
          setup();
        },
      };
    },
  };
}

const flow = (as_of_ms: number, status = "NORMAL") => ({
  as_of_ms,
  status,
  recent_share_1s: [],
  windows: [],
  drivers: [],
  exchange_drivers: [],
  current_assets: [],
  data_quality: {},
});

test("stalled connecting socket retries once and fences late callbacks", () => {
  const app = streamHarness().mount();
  const expired = app.socket();
  expired.readyState = 0;
  for (let second = 0; second < 16; second++) app.advance(1000);
  assert.equal(expired.readyState, 3);
  assert.equal(app.read().connection, "offline");
  app.retry();
  assert.notEqual(app.socket(), expired);
  expired.onclose!({ code: 1006, reason: "late close", wasClean: false });
  expired.frame(tick(999));
  assert.equal(app.read().connection, "connecting");
  app.socket().frame(tick(200));
  assert.equal(app.read().connection, "live");
  app.unmount();
});

test("offline mount and network loss report offline and reconnect once online", () => {
  const harness = streamHarness(false);
  const app = harness.mount();
  try {
    assert.equal(app.read().connection, "offline");
    assert.equal(harness.sockets.length, 0);
    harness.setOnline(true);
    assert.equal(harness.sockets.length, 1);
    app.socket().frame(tick(200));
    const retained = app.result.tickStore.getSnapshot();
    const expired = app.socket();
    harness.setOnline(false);
    assert.equal(app.read().connection, "offline");
    assert.equal(expired.readyState, 3);
    expired.frame(tick(999));
    assert.equal(app.result.tickStore.getSnapshot(), retained);
    harness.setOnline(true);
    harness.setOnline(true);
    assert.equal(harness.sockets.length, 2);
    app.socket().frame(tick(100));
    assert.equal(app.read().connection, "live");
  } finally {
    app.unmount();
  }
  assert.equal(harness.listenerCount(), 0);
  harness.setOnline(true);
  assert.equal(harness.sockets.length, 2);
});

test("silent open socket retries without waiting for close and rejects late frames", () => {
  const app = streamHarness().mount();
  try {
    const expired = app.socket();
    expired.frame(tick(200));
    const retained = app.result.tickStore.getSnapshot();
    for (let second = 0; second < 21; second++) app.advance(1000);
    assert.equal(expired.readyState, 3);
    app.retry();
    assert.notEqual(app.socket(), expired);
    expired.onclose!({ code: 4400, reason: "late close", wasClean: false });
    expired.frame(tick(999));
    assert.equal(app.result.tickStore.getSnapshot(), retained);
    assert.equal(app.read().connection, "connecting");
    app.socket().frame(tick(100));
    assert.equal(app.read().connection, "live");
  } finally {
    app.unmount();
  }
});

test("tick calculation failure survives unrelated transport messages until an accepted tick", () => {
  const app = streamHarness().mount();
  app.socket().frame(tick(200));
  app.socket().frame({ type: "market_share_tick_error" });
  for (const frame of [
    { type: "market_share_tick_references", references: {} },
    dedicated(2000),
    { type: "market_share_tick", heartbeat: true, generated_at: 300 },
    analysisFrame(200),
    {
      type: "market_share_whale_alerts",
      whale_alerts: { status: "UNAVAILABLE" },
    },
    tick(100),
  ]) {
    app.socket().frame(frame);
    assert.match(app.read().error, /실시간 점유율 계산/);
    assert.equal(app.read().connection, "stale");
  }
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 200);
  app.socket().frame(tick(201));
  assert.equal(app.read().error, "");
  assert.equal(app.read().connection, "live");
  app.unmount();
});

test("same-connection ticks cannot rewind, while equal-time corrections and reconnect reset are allowed", () => {
  const app = streamHarness().mount();
  app.socket().frame(tick(200));
  app.socket().frame(tick(100));
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 200);
  app.socket().frame({ ...tick(200), version: 1, status: "PARTIAL" });
  assert.equal(app.result.tickStore.getSnapshot().status, "PARTIAL");
  const previousSocket = app.socket();
  app.restartEffects();
  app.socket().frame(tick(100));
  previousSocket.frame(tick(300));
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 100);
  app.unmount();
});
// Ported from the independent review's actual onclose → timer → connect probe.
test("actual close/timer reconnect preserves errors and rejects obsolete callbacks", () => {
  const app = streamHarness().mount();
  app.socket().frame(tick(200));
  app.socket().frame({ type: "market_share_tick_error" });
  const previousSocket = app.reconnect();
  app.socket().frame({ type: "market_share_tick", heartbeat: true });
  assert.match(app.read().error, /실시간 점유율 계산/);

  previousSocket.frame(tick(900));
  previousSocket.onerror!();
  previousSocket.onclose!({ code: 4400 });
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 200);
  assert.equal(app.read().connection, "stale");

  app.socket().frame({
    ...tick(100),
    status: "PROVISIONAL",
    summary: { domestic_volume_krw: null },
  });
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 100);
  assert.equal(app.result.tickStore.getSnapshot().status, "PROVISIONAL");
  assert.equal(
    app.result.tickStore.getSnapshot().summary.domestic_volume_krw,
    null
  );
  assert.equal(app.read().error, "");
  app.socket().frame({ ...tick(100), status: "PARTIAL" });
  assert.equal(app.result.tickStore.getSnapshot().status, "PARTIAL");
  app.socket().frame(tick(99, flow(9000)));
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 100);
  assert.notEqual(app.result.realtimeFlowStore.getSnapshot()?.as_of_ms, 9000);

  const replacementSocket = app.socket();
  app.unmount();
  for (const socket of [previousSocket, replacementSocket]) {
    socket.frame(tick(1000));
    socket.onerror!();
    socket.onclose!({ code: 1006, reason: "", wasClean: false });
  }
  assert.equal(app.result.tickStore.getSnapshot(), null);
});

const tick = (
  generated_at: number,
  realtime_flow?: ReturnType<typeof flow>
) => ({
  type: "market_share_tick",
  generated_at,
  window_sec: 60,
  market_scope: "KRW",
  exchanges: [],
  ...(realtime_flow ? { realtime_flow } : {}),
});
const dedicated = (asOf: number, status?: string) => ({
  type: "market_share_realtime_flow",
  realtime_flow: flow(asOf, status),
});

for (const [name, frame] of [
  ["rejected old tick", tick(100)],
  [
    "unchanged tick heartbeat",
    { type: "market_share_tick", heartbeat: true, generated_at: 201 },
  ],
] as const) {
  test(`${name} keeps transport alive without refreshing frozen data`, () => {
    const harness = streamHarness();
    const app = harness.mount();
    app.socket().frame(tick(200));
    const received = harness.published
      .filter(status => "lastMessageAt" in status)
      .at(-1);
    app.advance(6_000);
    assert.equal(app.read().connection, "stale");
    for (let i = 0; i < 5; i++) {
      app.socket().frame(frame);
      assert.equal(app.read().connection, "stale");
      app.advance(5_000);
    }
    assert.equal(
      harness.sockets.length,
      1,
      "active transport must not reconnect"
    );
    assert.equal(app.result.tickStore.getSnapshot().generated_at, 200);
    assert.deepEqual(
      harness.published.filter(status => "lastMessageAt" in status).at(-1),
      received
    );
    app.socket().frame({ ...tick(200), status: "PARTIAL" });
    assert.equal(
      app.read().connection,
      "live",
      "equal-time corrections are data"
    );
    app.unmount();
  });
}

test("receive time reaches the live-status badge once per second and resets with the stream", () => {
  const harness = streamHarness();
  const receiveTimes = () =>
    harness.published
      .filter(status => "lastMessageAt" in status)
      .map(status => status.lastMessageAt);
  const app = harness.mount();
  assert.deepEqual(receiveTimes(), [0], "a new stream starts unreceived");
  for (let generatedAt = 1; generatedAt <= 5; generatedAt++)
    app.socket().frame(tick(generatedAt));
  app.socket().frame(dedicated(2000));
  assert.equal(receiveTimes().length, 2, "a burst publishes one receive time");
  const first = receiveTimes()[1]!;
  assert.ok(first > 0);
  app.advance(1_000);
  app.socket().frame(tick(6));
  assert.deepEqual(receiveTimes().slice(1), [first, first + 1_000]);
  // The hook no longer re-renders its page for the receive clock.
  assert.equal("lastMessageAt" in app.read(), false);
  app.restartEffects();
  assert.equal(
    receiveTimes().at(-1),
    0,
    "a replaced stream is not shown as received"
  );
  app.unmount();
});

test("dedicated flow subscribers see an observation before the next market tick", () => {
  const app = streamHarness().mount();
  app.socket().frame(tick(1));
  const received: number[] = [];
  const unsubscribe = app.result.realtimeFlowStore.subscribe(() => {
    received.push(app.result.realtimeFlowStore.getSnapshot().as_of_ms);
  });
  app.socket().frame(dedicated(2000));
  assert.deepEqual(received, [2000]);
  assert.equal(app.result.tickStore.getSnapshot().realtime_flow, undefined);
  const panel = readFileSync(
    new URL("../components/RealtimeFlowPanel.tsx", import.meta.url),
    "utf8"
  );
  assert.match(
    panel,
    /useSyncExternalStore\(\s*realtimeFlowStore.subscribe,\s*realtimeFlowStore.getSnapshot/
  );
  unsubscribe();
  app.unmount();
});

test("live coin shares reach their own store, a point a second, and count as data", () => {
  const app = streamHarness().mount();
  const coinFrame = (second: number) => ({
    type: "market_share_coin_shares",
    coin_shares: {
      window_sec: 60,
      as_of_second: second,
      window_complete: true,
      coverage_sec: 60,
      ranking: "OFFICIAL_24H_TICKER",
      coins: [
        {
          asset: "BTC",
          status: "AVAILABLE",
          domestic_volume_krw: 1_000,
          bithumb_volume_krw: 200,
          bithumb_share_pct: 20,
        },
      ],
    },
  });
  let told = 0;
  const unsubscribe = app.result.coinSharesStore.subscribe(() => (told += 1));
  app.socket().frame(coinFrame(100));
  app.socket().frame(coinFrame(101));
  const view = app.result.coinSharesStore.getSnapshot();
  assert.equal(told, 2);
  assert.deepEqual(
    view.seconds.map((second: { second: number }) => second.second),
    [100, 101]
  );
  assert.equal(view.shares.coins[0].bithumb_share_pct, 20);
  assert.equal(app.read().connection, "live");
  app.socket().frame({
    type: "market_share_coin_shares",
    coin_shares: { status: "UNAVAILABLE", detail: "analysis read failed" },
  });
  assert.equal(app.result.coinSharesStore.getSnapshot().unavailable, true);
  assert.equal(app.result.coinSharesStore.getSnapshot().seconds.length, 2);
  unsubscribe();
  app.unmount();
});

function expectBoth(
  app: ReturnType<ReturnType<typeof streamHarness>["mount"]>,
  asOf: number,
  status = "NORMAL"
) {
  assert.equal(app.result.realtimeFlowStore.getSnapshot()?.as_of_ms, asOf);
  assert.equal(
    app.result.tickStore.getSnapshot()?.realtime_flow?.as_of_ms,
    asOf
  );
  assert.equal(
    app.result.tickStore.getSnapshot()?.realtime_flow?.status,
    status
  );
  assert.equal(
    app.result.realtimeFlowStore.getSnapshot(),
    app.result.tickStore.getSnapshot()?.realtime_flow
  );
}

test("legacy full ticks share admission with dedicated frames and retain tick fields", () => {
  const harness = streamHarness();
  let app = harness.mount();
  app.socket().frame(dedicated(2000));
  app.socket().frame(tick(3, flow(1000)));
  expectBoth(app, 2000);
  assert.equal(app.result.tickStore.getSnapshot().generated_at, 3);
  app.unmount();
  app = harness.mount();
  expectBoth(app, 2000);
  app.socket().frame(tick(4, flow(3000)));
  expectBoth(app, 3000);
  app.socket().frame(tick(5));
  expectBoth(app, 3000);
  app.unmount();
});

test("first compact tick after reentry inherits the retained flow before analysis arrives", () => {
  const harness = streamHarness();
  let app = harness.mount();
  app.socket().frame(dedicated(2000));
  app.unmount();
  app = harness.mount();
  app.socket().frame(tick(3));
  expectBoth(app, 2000);
  app.socket().frame({
    type: "market_share_update",
    data: { ...analysisFrame(3).data, analysis: { realtime_flow: flow(1000) } },
  });
  app.socket().frame(tick(4));
  expectBoth(app, 2000);
  app.socket().frame(analysisFrame(4));
  app.socket().frame(tick(5));
  expectBoth(app, 2000);
  app.unmount();
});

test("same-time quality corrections in full ticks reach both stores", () => {
  const app = streamHarness().mount();
  app.socket().frame(dedicated(2000));
  app.socket().frame(tick(3, flow(2000, "DEGRADED")));
  expectBoth(app, 2000, "DEGRADED");
  app.socket().frame(tick(4));
  expectBoth(app, 2000, "DEGRADED");
  app.unmount();
});

test("effect restart restores retained flow without leaking into another request", () => {
  const harness = streamHarness();
  let app = harness.mount();
  app.socket().frame(dedicated(2000));
  app.restartEffects();
  app.socket().frame(tick(3));
  expectBoth(app, 2000);
  app.unmount();
  app = harness.mount(30);
  app.socket().frame(tick(4));
  assert.equal(app.result.realtimeFlowStore.getSnapshot(), null);
  assert.equal(app.result.tickStore.getSnapshot().realtime_flow, undefined);
  app.unmount();
});

test("suspended browser timer keeps stale data but gives queued frames a chance before closing", () => {
  const app = streamHarness().mount();
  try {
    app
      .socket()
      .frame({ type: "market_share_tick", heartbeat: true, generated_at: 300 });
    app.advance(30000);
    assert.equal(
      app.socket().readyState,
      1,
      "event-loop suspension must not immediately close a live socket"
    );
    assert.equal(
      app.read().connection,
      "stale",
      "suspension must not label retained data live"
    );
    for (let i = 0; i < 21; i++) app.advance(1000);
    assert.equal(
      app.socket().readyState,
      3,
      "genuine silence after resume still closes"
    );
  } finally {
    app.unmount();
  }
});

test("real hook keeps a silent calculation connected while transport heartbeats arrive", () => {
  const app = streamHarness().mount();
  try {
    app.socket().frame(tick(100));
    const saved = app.result.tickStore.getSnapshot();
    for (let i = 1; i <= 35; i++) {
      if (i % 5 === 0)
        app.socket().frame({
          type: "market_share_transport_heartbeat",
          connection_id: "fixture",
        });
      app.advance(1000);
    }
    assert.equal(app.socket().readyState, 1);
    assert.equal(app.read().connection, "stale");
    assert.equal(app.result.tickStore.getSnapshot(), saved);
    app.socket().frame(tick(101));
    assert.equal(app.read().connection, "live");
    for (let i = 0; i < 21; i++) app.advance(1000);
    assert.equal(
      app.socket().readyState,
      3,
      "no transport activity must still reconnect"
    );
  } finally {
    app.unmount();
  }
});
test("transport heartbeats do not clear calculation failures", () => {
  const app = streamHarness().mount();
  try {
    app.socket().frame(tick(100));
    app.socket().frame({ type: "market_share_tick_error" });
    const error = app.read().error;
    app.socket().frame({
      type: "market_share_transport_heartbeat",
      connection_id: "fixture",
    });
    assert.equal(app.read().error, error);
    assert.equal(app.read().connection, "stale");
  } finally {
    app.unmount();
  }
});

test("heartbeats without data establish transport without inventing a receive time", () => {
  const harness = streamHarness();
  const app = harness.mount();
  try {
    app.socket().frame({ type: "market_share_transport_heartbeat" });
    assert.equal(app.read().connection, "live");
    assert.equal(app.result.tickStore.getSnapshot(), null);
    assert.deepEqual(
      harness.published.map(
        status => status.lastMessageAt ?? status.connection
      ),
      [0, "connecting"]
    );
  } finally {
    app.unmount();
  }
});

test("errors, rejected flow and ancillary frames do not refresh frozen market data", () => {
  const harness = streamHarness();
  const app = harness.mount();
  try {
    app.socket().frame(tick(200, flow(2000)));
    const received = harness.published
      .filter(item => "lastMessageAt" in item)
      .at(-1);
    for (let second = 0; second < 6; second++) app.advance(1000);
    assert.equal(app.read().connection, "stale");
    for (const frame of [
      { type: "market_share_analysis_error" },
      { type: "market_share_tick_error" },
      dedicated(1000),
      {
        type: "market_share_tick_references",
        references: { realtime_flow: flow(1000) },
      },
      { type: "market_share_notice_snapshot", notices: { items: [] } },
      {
        type: "market_share_whale_alerts",
        whale_alerts: { status: "UNAVAILABLE" },
      },
      {
        type: "market_share_coin_shares",
        coin_shares: { status: "UNAVAILABLE" },
      },
    ]) {
      app.socket().frame(frame);
      assert.equal(app.read().connection, "stale");
      assert.deepEqual(
        harness.published.filter(item => "lastMessageAt" in item).at(-1),
        received
      );
      app.advance(1000);
    }
    assert.equal(harness.sockets.length, 1);
    assert.equal(app.result.tickStore.getSnapshot().generated_at, 200);
    assert.equal(app.result.realtimeFlowStore.getSnapshot().as_of_ms, 2000);
    app.socket().frame({ type: "market_share_unavailable" });
    assert.deepEqual(
      harness.published.filter(item => "lastMessageAt" in item).at(-1),
      received
    );
    app.socket().frame(tick(201));
    assert.equal(app.read().connection, "live");
  } finally {
    app.unmount();
  }
});

const analysisFrame = (generatedAt: number, partial = false) => ({
  type: "market_share_update",
  data: {
    version: generatedAt,
    generated_at: generatedAt,
    running: true,
    partial_analysis: partial,
    analysis: { context: { analysis_version: generatedAt } },
  },
});

test("analysis admission rejects old frames before their references or flow mutate", () => {
  const app = streamHarness().mount();
  try {
    app.socket().frame({
      ...analysisFrame(200),
      data: {
        ...analysisFrame(200).data,
        analysis: {
          ...analysisFrame(200).data.analysis,
          official_domestic_24h: { status: "LIVE" },
        },
      },
    });
    app.socket().frame({
      ...analysisFrame(100),
      data: {
        ...analysisFrame(100).data,
        analysis: {
          official_domestic_24h: { status: "STALE" },
          realtime_flow: flow(9000),
        },
      },
    });
    app.socket().frame(tick(200));
    assert.equal(app.read().data.generated_at, 200);
    assert.equal(
      app.result.tickStore.getSnapshot().official_domestic_24h.status,
      "LIVE"
    );
    assert.equal(app.result.realtimeFlowStore.getSnapshot(), null);
    app.socket().frame(analysisFrame(200, true));
    app.socket().frame(tick(201));
    assert.equal(
      app.result.tickStore.getSnapshot().official_domestic_24h.status,
      "LIVE"
    );
    app.reconnect();
    app.socket().frame(analysisFrame(100));
    assert.equal(app.read().data.generated_at, 100);
  } finally {
    app.unmount();
  }
});

test("queued analysis from a closed socket cannot commit into its replacement", () => {
  const harness = streamHarness(true, true);
  const app = harness.mount();
  try {
    app.socket().frame(analysisFrame(200));
    app.reconnect();
    app.socket().frame(analysisFrame(100));
    harness.flushTransition();
    assert.equal(app.read().data, null);
    assert.equal(
      payload.cachedMarketSharePayload("60:default:KRW:full")?.generated_at,
      100
    );
    harness.flushTransition();
    assert.equal(app.read().data.generated_at, 100);
  } finally {
    app.unmount();
  }
});

test("analysis requires a finite positive envelope time before any state or freshness mutation", () => {
  const harness = streamHarness();
  const app = harness.mount();
  try {
    app.socket().frame(analysisFrame(200));
    app.socket().frame(tick(200));
    const received = harness.published
      .filter(item => "lastMessageAt" in item)
      .at(-1);
    for (let second = 0; second < 6; second++) app.advance(1000);
    for (const generated_at of [
      undefined,
      null,
      "201",
      0,
      -1,
      Number.NaN,
      Number.POSITIVE_INFINITY,
    ]) {
      app.socket().frame({
        type: "market_share_update",
        data: { generated_at, analysis: { realtime_flow: flow(9000) } },
      });
      assert.equal(app.read().data.generated_at, 200);
      assert.equal(app.result.realtimeFlowStore.getSnapshot(), null);
      assert.equal(app.read().connection, "stale");
      assert.equal(
        payload.cachedMarketSharePayload("60:default:KRW:full")?.generated_at,
        200
      );
      assert.deepEqual(
        harness.published.filter(item => "lastMessageAt" in item).at(-1),
        received
      );
    }
  } finally {
    app.unmount();
  }
});

test("partial analysis without a baseline remains unavailable until a full frame arrives", () => {
  const harness = streamHarness(true, true);
  const app = harness.mount();
  try {
    app.socket().frame(analysisFrame(100, true));
    harness.flushTransition();
    assert.equal(app.read().data, null);
    assert.equal(app.read().connection, "live");
    assert.equal(payload.cachedMarketSharePayload("60:default:KRW:full"), null);
    app.socket().frame({
      ...analysisFrame(200),
      data: {
        ...analysisFrame(200).data,
        analysis: {
          ...analysisFrame(200).data.analysis,
          notices: [{ id: "kept" }],
        },
      },
    });
    app.socket().frame(analysisFrame(201, true));
    harness.flushTransition();
    harness.flushTransition();
    assert.equal(app.read().data.generated_at, 201);
    assert.equal(app.read().data.analysis.notices[0].id, "kept");
  } finally {
    app.unmount();
  }
});

test("reconnect retains the flow event-time fence and subscribers until a current observation", () => {
  const app = streamHarness().mount();
  try {
    app.socket().frame(tick(200, flow(2000)));
    let notifications = 0;
    app.result.realtimeFlowStore.subscribe(() => notifications++);
    const previousSocket = app.reconnect();
    assert.equal(app.result.realtimeFlowStore.getSnapshot().as_of_ms, 2000);
    app.socket().frame({ type: "market_share_transport_heartbeat" });
    assert.equal(app.read().connection, "stale");
    app.socket().frame(dedicated(1000));
    assert.equal(app.read().connection, "stale");
    expectBoth(app, 2000);
    assert.equal(notifications, 0);
    app.socket().frame(tick(100, flow(3000)));
    expectBoth(app, 3000);
    assert.equal(app.result.tickStore.getSnapshot().generated_at, 100);
    assert.equal(notifications, 1);
    app.socket().frame(dedicated(900));
    previousSocket.frame(tick(999, flow(9999)));
    expectBoth(app, 3000);
    assert.equal(notifications, 1);
  } finally {
    app.unmount();
  }
});
