import { createRoot } from "react-dom/client";
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";

import StandaloneApp from "./StandaloneApp";
import type { EmbeddedMarketQuestionAnswer } from "./components/HermesMarketQuestion";
import "./index.css";
import "./standalone.css";

interface StandaloneSnapshot {
  contract: "MARKET_SHARE_FRONTEND_STANDALONE_V1";
  capturedAt: string;
  dataAsOfMS: number;
  timezone: string;
  source: string;
  live: false;
  socketMessages: Array<Record<string, unknown>>;
  rest: Record<string, unknown>;
  questionAnswers: EmbeddedMarketQuestionAnswer[];
}

declare const __STANDALONE_SNAPSHOT__: StandaloneSnapshot;

const snapshot = __STANDALONE_SNAPSHOT__;
const replayMessageTypes = new Set([
  "market_share_tick",
  "market_share_realtime_flow",
]);
const serializedInitialMessages = snapshot.socketMessages
  .filter(message => !replayMessageTypes.has(String(message.type ?? "")))
  .map(message => JSON.stringify(message));
const serializedReplayMessages = snapshot.socketMessages
  .filter(message => replayMessageTypes.has(String(message.type ?? "")))
  .map(message => JSON.stringify(message));
let deliveredReplayMessageCount = 0;
let openedSocketCount = 0;

class SnapshotWebSocket extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;

  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSING = 2;
  readonly CLOSED = 3;
  readonly url: string;
  readonly protocol = "";
  readonly extensions = "";
  binaryType: BinaryType = "blob";
  bufferedAmount = 0;
  readyState = SnapshotWebSocket.CONNECTING;
  onopen: ((this: WebSocket, event: Event) => unknown) | null = null;
  onmessage: ((this: WebSocket, event: MessageEvent) => unknown) | null = null;
  onerror: ((this: WebSocket, event: Event) => unknown) | null = null;
  onclose: ((this: WebSocket, event: CloseEvent) => unknown) | null = null;
  private timers: number[] = [];
  private replayIndex = 0;

  constructor(url: string | URL) {
    super();
    this.url = String(url);
    this.timers.push(
      window.setTimeout(() => {
        if (this.readyState !== SnapshotWebSocket.CONNECTING) return;
        this.readyState = SnapshotWebSocket.OPEN;
        openedSocketCount += 1;
        this.emit("open", new Event("open"));
        serializedInitialMessages.forEach((data, index) => {
          this.timers.push(
            window.setTimeout(() => {
              if (this.readyState !== SnapshotWebSocket.OPEN) return;
              this.emit("message", new MessageEvent("message", { data }));
            }, 8 + index * 8)
          );
        });
        this.scheduleReplay(80 + serializedInitialMessages.length * 8);
      }, 0)
    );
  }

  private scheduleReplay(delayMS = 650) {
    if (serializedReplayMessages.length === 0) return;
    this.timers.push(
      window.setTimeout(() => {
        if (this.readyState !== SnapshotWebSocket.OPEN) return;
        const data = serializedReplayMessages[this.replayIndex];
        this.replayIndex =
          (this.replayIndex + 1) % serializedReplayMessages.length;
        deliveredReplayMessageCount += 1;
        this.emit("message", new MessageEvent("message", { data }));
        this.scheduleReplay();
      }, delayMS)
    );
  }

  private emit(
    type: "open" | "message" | "error" | "close",
    event: Event | MessageEvent | CloseEvent
  ) {
    this.dispatchEvent(event);
    const handler = this[`on${type}`];
    if (typeof handler === "function") {
      handler.call(this as unknown as WebSocket, event as never);
    }
  }

  send() {
    // The standalone snapshot is read-only.
  }

  close(code = 1000, reason = "standalone snapshot closed") {
    if (this.readyState === SnapshotWebSocket.CLOSED) return;
    this.readyState = SnapshotWebSocket.CLOSING;
    this.timers.forEach(timer => window.clearTimeout(timer));
    this.timers = [];
    this.readyState = SnapshotWebSocket.CLOSED;
    this.emit(
      "close",
      new CloseEvent("close", { code, reason, wasClean: true })
    );
  }
}

window.WebSocket = SnapshotWebSocket as unknown as typeof WebSocket;

const nativeFetch = window.fetch.bind(window);
const routeForPath = (pathname: string): string | null => {
  if (pathname.endsWith("/official-aligned-history"))
    return "official-aligned-history";
  if (pathname.endsWith("/official-aligned-contribution"))
    return "official-aligned-contribution";
  if (pathname.endsWith("/realtime-history")) return "realtime-history";
  if (pathname.endsWith("/realtime-summary")) return "realtime-summary";
  if (pathname.endsWith("/active-notice-network"))
    return "active-notice-network";
  if (pathname.endsWith("/registry-history")) return "registry-history";
  return null;
};

window.fetch = async (input, init) => {
  const rawURL =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.toString()
        : input.url;
  const url = new URL(rawURL, "https://standalone.snapshot.local");
  const route = routeForPath(url.pathname);
  if (route && route in snapshot.rest) {
    return new Response(JSON.stringify(snapshot.rest[route]), {
      status: 200,
      headers: { "Content-Type": "application/json; charset=utf-8" },
    });
  }
  if (url.pathname.startsWith("/api/")) {
    return new Response(
      JSON.stringify({
        detail:
          "이 단독 HTML은 캡처된 시장 자료만 제공하며 새로운 서버 요청은 수행하지 않습니다.",
      }),
      {
        status: 503,
        headers: { "Content-Type": "application/json; charset=utf-8" },
      }
    );
  }
  return nativeFetch(input, init);
};

const runtimeStatus = document.querySelector<HTMLElement>(
  "[data-standalone-runtime-status]"
);
let firstRuntimeError = "";

const showRuntimeFailure = (detail: string) => {
  if (!runtimeStatus || runtimeStatus.dataset.state === "ok") return;
  runtimeStatus.dataset.state = "error";
  runtimeStatus.textContent = "대시보드를 표시하지 못했습니다";
  runtimeStatus.title = detail;
};

window.addEventListener("error", event => {
  firstRuntimeError ||= event.message || "알 수 없는 실행 오류";
  showRuntimeFailure(firstRuntimeError);
});
window.addEventListener("unhandledrejection", event => {
  const reason =
    event.reason instanceof Error ? event.reason.message : String(event.reason);
  firstRuntimeError ||= reason || "처리되지 않은 실행 오류";
  showRuntimeFailure(firstRuntimeError);
});

createRoot(document.getElementById("root")!).render(
  <StandaloneApp questionAnswers={snapshot.questionAnswers} />
);

const runtimeCheckStartedAt = Date.now();
const runtimeCheck = window.setInterval(() => {
  if (firstRuntimeError) {
    window.clearInterval(runtimeCheck);
    return;
  }

  const shell = document.querySelector(".terminal-shell");
  const dashboardPage = document.querySelector(".dashboard-page");
  const navigation = document.querySelector('[aria-label="화면 전환"]');
  const dashboardText = dashboardPage?.textContent ?? "";
  const dashboardIsVisible = Boolean(
    shell &&
      dashboardPage &&
      navigation &&
      dashboardText.includes("통합 점유율 분석")
  );
  const replayIsRunning =
    serializedReplayMessages.length === 0 || deliveredReplayMessageCount >= 2;

  if (dashboardIsVisible && openedSocketCount > 0 && replayIsRunning) {
    window.clearInterval(runtimeCheck);
    if (runtimeStatus) {
      runtimeStatus.dataset.state = "ok";
      runtimeStatus.textContent = "대시보드 표시됨 · 저장 데이터 반복 재생 중";
      runtimeStatus.title = `${deliveredReplayMessageCount}개 재생 메시지 전달 확인`;
    }
    return;
  }

  if (Date.now() - runtimeCheckStartedAt > 8000) {
    window.clearInterval(runtimeCheck);
    showRuntimeFailure(
      `화면=${dashboardIsVisible ? "정상" : "없음"}, 연결=${openedSocketCount}, 재생=${deliveredReplayMessageCount}`
    );
  }
}, 200);
