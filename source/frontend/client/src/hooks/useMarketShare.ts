import { marketShareTransportHealth } from "@/lib/market-share-transport";
import { startTransition, useEffect, useMemo, useRef, useState } from "react";
import {
  useLiveStatusPublisher,
  type LiveConnection,
} from "@/contexts/LiveStatusContext";
import {
  getMarketShareWebSocketUrl,
  type MarketShareRealtimeFlowStream,
} from "@/lib/api-client";
import {
  EMPTY_LIVE_COIN_VIEW,
  nextLiveCoinView,
} from "@/lib/coin-shares-presentation";
import { marketShareReconnectMessage } from "@/lib/market-share-connection-copy";
import {
  createMarketShareTickStore,
  type MarketShareTickStore,
} from "@/lib/market-share-tick-store";
import {
  createMarketShareRealtimeFlowStore,
  type MarketShareRealtimeFlowStore,
} from "@/lib/market-share-realtime-flow-store";
import {
  cachedMarketSharePayload,
  marketShareTickReferences,
  mergeMarketSharePayload,
  mergeMarketShareTickReferences,
  normalizeMarketShareCollections,
  rememberMarketSharePayload,
  type KeyedMarketSharePayload,
  type MarketShareTickReferences,
} from "@/lib/market-share-payload";
import type {
  ExchangeNotice,
  MarketShareAnalysis,
  MarketShareNoticeSnapshot,
  MarketShareReadiness,
  MarketShareRealtimeFlowState,
  MarketShareRealtimeTick,
  MarketShareSocketPayload,
  MarketShareWhaleAlertState,
  MarketScope,
} from "@/lib/market-share-types";
import { isUsableWhaleAlertState } from "@/lib/market-share-whale-alert";
import { createReducerStore } from "@/lib/reducer-store";

export type MarketShareConnection = LiveConnection;
export type { MarketShareTickStore } from "@/lib/market-share-tick-store";
export type { MarketShareRealtimeFlowStore } from "@/lib/market-share-realtime-flow-store";

let latestMarketRegistry: MarketShareAnalysis["registry"] | null = null;
const RETAINED_STREAM_REQUEST_LIMIT = 8;
const latestTickByRequest = new Map<string, MarketShareRealtimeTick>();
const latestRealtimeFlowByRequest = new Map<
  string,
  MarketShareRealtimeFlowState
>();
let latestWhaleAlerts: MarketShareWhaleAlertState | null = null;

function rememberStreamValue<T>(map: Map<string, T>, key: string, value: T) {
  map.delete(key);
  map.set(key, value);
  while (map.size > RETAINED_STREAM_REQUEST_LIMIT) {
    const oldestKey = map.keys().next().value;
    if (typeof oldestKey !== "string") break;
    map.delete(oldestKey);
  }
}

function marketRegistrySignature(
  registry: MarketShareAnalysis["registry"]
): string {
  const exchanges = Object.entries(registry.exchanges)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([exchange, row]) => [
      exchange,
      row.market_count,
      row.asset_count,
      row.public_fee_market_count,
      row.error,
      Object.entries(row.quotes).sort(([left], [right]) =>
        left.localeCompare(right)
      ),
    ]);
  return JSON.stringify([
    registry.market_count,
    registry.asset_count,
    registry.ws_enabled_market_count,
    registry.ws_enabled_asset_count,
    Array.isArray(registry.ws_enabled_exchanges)
      ? [...registry.ws_enabled_exchanges].sort()
      : [],
    exchanges,
  ]);
}

export function useMarketShare(
  windowSec: number,
  notionalKrw: number | null,
  marketScope: MarketScope,
  view: string,
  realtimeFlow: MarketShareRealtimeFlowStream = "full",
  coinShares = false
) {
  const publishLiveStatus = useLiveStatusPublisher();
  const requestKey = `${windowSec}:${notionalKrw ?? "default"}:${marketScope}:${view}${realtimeFlow === "full" ? "" : `:${realtimeFlow}`}${coinShares ? ":coin-shares" : ""}`;
  const [dataState, setDataState] = useState<KeyedMarketSharePayload | null>(
    () => {
      const cached = cachedMarketSharePayload(requestKey);
      return cached ? { requestKey, payload: cached } : null;
    }
  );
  const [streamRequestKey, setStreamRequestKey] = useState(requestKey);
  const [noticeRequestKey, setNoticeRequestKey] = useState(requestKey);
  const [connection, setConnection] =
    useState<MarketShareConnection>("connecting");
  const [transportError, setTransportError] = useState("");
  const [analysisError, setAnalysisError] = useState("");
  const [messageError, setMessageError] = useState("");
  const [tickError, setTickError] = useState("");
  const [registry, setRegistry] = useState<
    MarketShareAnalysis["registry"] | null
  >(latestMarketRegistry);
  const [readiness, setReadiness] = useState<MarketShareReadiness | null>(null);
  const [noticeSnapshot, setNoticeSnapshot] = useState<ExchangeNotice[]>([]);
  const [noticeMeta, setNoticeMeta] =
    useState<MarketShareNoticeSnapshot | null>(null);
  const [whaleAlerts, setWhaleAlerts] =
    useState<MarketShareWhaleAlertState | null>(latestWhaleAlerts);
  const [whaleAlertsUnavailable, setWhaleAlertsUnavailable] = useState(
    Boolean(latestWhaleAlerts)
  );
  const registrySignatureRef = useRef(
    latestMarketRegistry ? marketRegistrySignature(latestMarketRegistry) : ""
  );
  const lastMessageRef = useRef(0);
  const renderedMessageAtRef = useRef(0);
  const tickReferencesRef = useRef<MarketShareTickReferences | null>(null);
  const tickStore = useMemo(
    () =>
      createMarketShareTickStore(
        undefined,
        latestTickByRequest.get(requestKey) ?? null
      ),
    [requestKey]
  );
  const realtimeFlowStore = useMemo(
    () =>
      createMarketShareRealtimeFlowStore(
        latestRealtimeFlowByRequest.get(requestKey) ?? null
      ),
    [requestKey]
  );
  // The coins' live shares and the seconds their lines are drawn from, kept
  // from the first frame of this stream.
  const coinSharesStore = useMemo(
    () => createReducerStore(EMPTY_LIVE_COIN_VIEW, nextLiveCoinView),
    [requestKey]
  );

  useEffect(() => {
    let active = true;
    let socket: WebSocket | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay = 1_000;
    let reconnectAllowed = true;
    let openedAt = 0;
    let connectingAt = 0;
    let lastTransportAt = 0;
    const cachedPayload = cachedMarketSharePayload(requestKey);
    let acceptedPayload: KeyedMarketSharePayload | null = cachedPayload
      ? { requestKey, payload: cachedPayload }
      : null;
    // A heartbeat or isolated tick can arrive before the page has an analysis
    // to render. Only a retained full payload proves values remain on screen.
    const reconnectMessage = () =>
      marketShareReconnectMessage(
        Boolean(cachedMarketSharePayload(requestKey))
      );

    const publishRealtimeFlow = (incoming: MarketShareRealtimeFlowState) => {
      realtimeFlowStore.publish(incoming);
      const current = realtimeFlowStore.getSnapshot();
      if (!current) return false;
      // Cache and tick references must retain the accepted frame too, otherwise
      // a page remount or the next compact tick can restore the delayed analysis.
      rememberStreamValue(latestRealtimeFlowByRequest, requestKey, current);
      tickReferencesRef.current = {
        ...tickReferencesRef.current,
        realtime_flow: current,
      };
      return current === incoming;
    };

    const markMessage = (nextConnection: MarketShareConnection = "live") => {
      setStreamRequestKey(requestKey);
      const now = Date.now();
      openedAt = 0;
      reconnectDelay = 1_000;
      lastMessageRef.current = now;
      lastTransportAt = now;
      // The receive time is shown only by the live-status badges. Publishing it
      // there, not as hook state, keeps the page from rendering every second.
      if (
        !renderedMessageAtRef.current ||
        now - renderedMessageAtRef.current >= 1_000
      ) {
        renderedMessageAtRef.current = now;
        publishLiveStatus({ lastMessageAt: now });
      }
      setConnection(nextConnection);
      setTransportError("");
      setMessageError("");
    };

    const markTransport = () => {
      lastTransportAt = Date.now();
      setTransportError("");
      // A recovered connection can still be holding an old tick. Keep that
      // evidence stale until an accepted data frame arrives.
      const retained = Boolean(
        tickStore.getSnapshot() || cachedMarketSharePayload(requestKey)
      );
      setConnection(current =>
        current === "connecting" || current === "offline"
          ? retained
            ? "stale"
            : "live"
          : current
      );
    };

    const markOffline = () => {
      openedAt = 0;
      connectingAt = 0;
      lastMessageRef.current = 0;
      setConnection("offline");
      setTransportError(reconnectMessage());
    };

    const closeCurrentSocket = (reason: string) => {
      const expired = socket;
      // Fence callbacks before close: the browser may deliver them only after
      // a new connection has started, or never finish the close handshake.
      socket = null;
      expired?.close(4000, reason);
    };

    const scheduleReconnect = () => {
      markOffline();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      reconnectTimer = null;
      if (!active || !reconnectAllowed || !navigator.onLine) return;
      const jitter = Math.round(reconnectDelay * (0.8 + Math.random() * 0.4));
      reconnectTimer = setTimeout(connect, jitter);
      reconnectDelay = Math.min(30_000, reconnectDelay * 2);
    };

    const connect = () => {
      reconnectTimer = null;
      if (!active || !reconnectAllowed) return;
      if (!navigator.onLine) {
        markOffline();
        return;
      }
      if (
        socket &&
        (socket.readyState === WebSocket.OPEN ||
          socket.readyState === WebSocket.CONNECTING)
      )
        return;
      setConnection("connecting");
      connectingAt = Date.now();
      const currentSocket = new WebSocket(
        getMarketShareWebSocketUrl(
          windowSec,
          notionalKrw,
          marketScope,
          view,
          realtimeFlow,
          coinShares
        )
      );
      // Scope ordering to this transport generation. A replacement calculator
      // may reset version counters; a new connection must admit its first tick.
      let lastAcceptedTickAt = 0;
      let lastAcceptedAnalysisAt = 0;
      let connectionID = "";
      let connectedAt = 0;
      socket = currentSocket;
      currentSocket.onopen = () => {
        if (!active || socket !== currentSocket) return;
        openedAt = Date.now();
        connectingAt = 0;
        connectedAt = openedAt;
        lastTransportAt = openedAt;
      };
      currentSocket.onmessage = event => {
        if (!active || socket !== currentSocket) return;
        let messageType = "unknown";
        try {
          const message = normalizeMarketShareCollections(
            JSON.parse(String(event.data))
          ) as {
            type?: string;
            connection_id?: string;
            data?: MarketShareSocketPayload;
            generated_at?: number;
            version?: number;
            status?: string;
            error_code?: string;
            detail?: string;
            heartbeat?: boolean;
            references?: MarketShareTickReferences;
            realtime_flow?: MarketShareAnalysis["realtime_flow"];
            whale_alerts?: MarketShareWhaleAlertState;
            coin_shares?: unknown;
            readiness?: MarketShareReadiness;
            exchanges?: Array<
              Pick<
                MarketShareAnalysis["exchanges"][number],
                | "exchange"
                | "share_pct"
                | "volume_krw"
                | "trade_count"
                | "trade_count_share_pct"
              >
            >;
            summary?: Pick<
              MarketShareAnalysis["summary"],
              | "domestic_volume_krw"
              | "bithumb_volume_krw"
              | "bithumb_share_pct"
              | "domestic_trade_count"
              | "bithumb_trade_count"
              | "bithumb_trade_count_share_pct"
            >;
          };
          messageType = String(message.type ?? "unknown");
          if (message.type === "market_share_transport_heartbeat") {
            markTransport();
            connectionID = String(message.connection_id ?? "").slice(0, 80);
            return; // Keep data timestamp, values, coverage and tick errors unchanged.
          }
          if (
            message.type === "market_share_update" &&
            message.data?.analysis
          ) {
            const incoming = message.data;
            if (
              typeof incoming.generated_at !== "number" ||
              !Number.isFinite(incoming.generated_at) ||
              incoming.generated_at <= 0 ||
              incoming.generated_at < lastAcceptedAnalysisAt
            ) {
              markTransport();
              return;
            }
            const next = mergeMarketSharePayload(
              acceptedPayload,
              requestKey,
              incoming
            );
            if (!next) {
              markTransport();
              return;
            }
            acceptedPayload = next;
            lastAcceptedAnalysisAt = incoming.generated_at;
            rememberMarketSharePayload(requestKey, next.payload);
            tickReferencesRef.current = {
              ...tickReferencesRef.current,
              ...marketShareTickReferences(incoming.analysis),
            };
            if (incoming.analysis.registry?.exchanges) {
              const nextRegistry = incoming.analysis.registry;
              const nextRegistrySignature =
                marketRegistrySignature(nextRegistry);
              if (registrySignatureRef.current !== nextRegistrySignature) {
                registrySignatureRef.current = nextRegistrySignature;
                latestMarketRegistry = nextRegistry;
                setRegistry(nextRegistry);
              }
            }
            if (incoming.analysis.realtime_flow) {
              publishRealtimeFlow(incoming.analysis.realtime_flow);
            }
            startTransition(() => {
              if (!active || socket !== currentSocket) return;
              setDataState(current => {
                // React can process a transition after the socket is replaced.
                // Obsolete updaters must not overwrite the new generation.
                return active && socket === currentSocket ? next : current;
              });
            });
            setNoticeSnapshot(current => (current.length === 0 ? current : []));
            setNoticeMeta(current => (current === null ? current : null));
            setAnalysisError("");
            markMessage();
            return;
          }
          if (
            message.type === "market_share_tick_references" &&
            message.references
          ) {
            tickReferencesRef.current = {
              ...tickReferencesRef.current,
              ...message.references,
            };
            if (message.references.realtime_flow) {
              publishRealtimeFlow(message.references.realtime_flow);
            }
            markTransport();
            return;
          }
          if (
            message.type === "market_share_realtime_flow" &&
            message.realtime_flow
          ) {
            if (publishRealtimeFlow(message.realtime_flow)) markMessage();
            else markTransport();
            return;
          }
          if (message.type === "market_share_coin_shares") {
            const previous = coinSharesStore.getSnapshot();
            coinSharesStore.publish(message.coin_shares);
            const current = coinSharesStore.getSnapshot();
            if (current !== previous && !current.unavailable) markMessage();
            else markTransport();
            return;
          }
          if (message.type === "market_share_whale_alerts") {
            if (!isUsableWhaleAlertState(message.whale_alerts)) {
              setWhaleAlertsUnavailable(true);
              markTransport();
              return;
            }
            latestWhaleAlerts = message.whale_alerts;
            setWhaleAlerts(message.whale_alerts);
            setWhaleAlertsUnavailable(false);
            markTransport();
            return;
          }
          if (message.type === "market_share_notice_snapshot") {
            const snapshot = (
              message as { notices?: Partial<MarketShareNoticeSnapshot> }
            ).notices;
            const items = snapshot?.items;
            if (Array.isArray(items)) {
              setStreamRequestKey(requestKey);
              setNoticeRequestKey(requestKey);
              setNoticeSnapshot(items.slice(0, 100));
              setNoticeMeta({
                version: Number(snapshot?.version ?? 0),
                poll_interval_sec: Number(snapshot?.poll_interval_sec ?? 0),
                history_days: Number(snapshot?.history_days ?? 0),
                healthy_source_count: Number(
                  snapshot?.healthy_source_count ?? 0
                ),
                complete_history_source_count: Number(
                  snapshot?.complete_history_source_count ?? 0
                ),
                active_error_count: Number(snapshot?.active_error_count ?? 0),
                source_count: Number(snapshot?.source_count ?? 0),
                error_history: Array.isArray(snapshot?.error_history)
                  ? snapshot.error_history
                  : [],
                sources: snapshot?.sources ?? {},
                items: [],
              });
              markTransport();
            }
            return;
          }
          if (message.type === "market_share_analysis_error") {
            markTransport();
            setAnalysisError(
              "전체 분석을 갱신하지 못했습니다. 실시간 연결은 계속 재시도합니다."
            );
            return;
          }
          if (message.type === "market_share_unavailable") {
            markTransport();
            setConnection("connecting");
            setTransportError("점유율 분석 서비스를 준비하고 있습니다.");
            return;
          }
          if (message.type === "market_share_tick_error") {
            // The socket and shared producer are alive, but this tick could not
            // be calculated. Count the frame as transport activity so a
            // summary2 client does not reconnect every ten seconds, while
            // retaining the last valid tick and showing degraded freshness.
            markTransport();
            setTickError(
              "실시간 점유율 계산을 갱신하지 못했습니다. 마지막 정상 수치를 유지합니다."
            );
            return;
          }
          if (message.type === "market_share_tick" && message.heartbeat) {
            markTransport();
            return;
          }
          if (
            message.type === "market_share_tick" &&
            typeof message.generated_at === "number" &&
            Number.isFinite(message.generated_at) &&
            message.generated_at > 0 &&
            Array.isArray(message.exchanges)
          ) {
            if (message.generated_at < lastAcceptedTickAt) {
              markTransport();
              return;
            }
            // Equal-time corrections remain admissible. Ignore old ticks
            // before their embedded references/flow can mutate any store.
            if (message.realtime_flow) {
              publishRealtimeFlow(message.realtime_flow);
            }
            const mergedTick = mergeMarketShareTickReferences(
              message as MarketShareRealtimeTick,
              tickReferencesRef.current
            );
            // Legacy full ticks can carry a delayed flow. Preserve all other
            // tick fields, but use the same accepted flow as the one-second chart.
            const currentFlow = realtimeFlowStore.getSnapshot();
            const tick = currentFlow
              ? { ...mergedTick, realtime_flow: currentFlow }
              : mergedTick;
            const nextReadiness = tick.readiness;
            if (nextReadiness) {
              setReadiness(current => {
                if (
                  current &&
                  current.phase === nextReadiness.phase &&
                  current.ready_exchange_count ===
                    nextReadiness.ready_exchange_count &&
                  current.connected_market_count ===
                    nextReadiness.connected_market_count &&
                  current.registered_market_count ===
                    nextReadiness.registered_market_count &&
                  current.blocking_exchanges.join(",") ===
                    nextReadiness.blocking_exchanges.join(",")
                ) {
                  return current;
                }
                return nextReadiness;
              });
            }
            rememberStreamValue(latestTickByRequest, requestKey, tick);
            if (!message.heartbeat) tickStore.publish(tick);
            lastAcceptedTickAt = message.generated_at;
            setTickError("");
            markMessage();
            return;
          }
        } catch (caughtError) {
          console.error(
            `Market-share WebSocket message handling failed (${messageType})`,
            caughtError
          );
          setMessageError("점유율 메시지를 해석하지 못했습니다.");
        }
      };
      currentSocket.onerror = () => {
        if (!active || socket !== currentSocket) return;
        setConnection("offline");
        setTransportError(reconnectMessage());
      };
      currentSocket.onclose = event => {
        if (!active || socket !== currentSocket) return;
        console.warn("Market share WebSocket closed", {
          connection_id: connectionID,
          code: event.code,
          reason: event.reason.slice(0, 256),
          clean: event.wasClean,
          duration_ms: connectedAt ? Date.now() - connectedAt : null,
          transport_age_ms: lastTransportAt
            ? Date.now() - lastTransportAt
            : null,
          data_age_ms: lastMessageRef.current
            ? Date.now() - lastMessageRef.current
            : null,
        });
        socket = null;
        openedAt = 0;
        lastMessageRef.current = 0;
        setConnection("offline");
        if (event.code === 4400) {
          reconnectAllowed = false;
          setTransportError("지원하지 않는 분석 구간입니다.");
          return;
        }
        scheduleReconnect();
      };
    };

    lastMessageRef.current = 0;
    renderedMessageAtRef.current = 0;
    setStreamRequestKey(requestKey);
    publishLiveStatus({ lastMessageAt: 0 });
    setTickError("");
    setReadiness(null);
    setNoticeSnapshot([]);
    setNoticeMeta(null);
    setDataState(cachedPayload ? { requestKey, payload: cachedPayload } : null);
    setConnection("connecting");
    tickReferencesRef.current = null;
    // A compact tick may precede the first analysis/flow after reentry. Restore
    // this request's retained frame, including after an effect cleanup/setup.
    const retainedFlow =
      realtimeFlowStore.getSnapshot() ??
      latestRealtimeFlowByRequest.get(requestKey);
    if (retainedFlow) publishRealtimeFlow(retainedFlow);
    setTransportError("");
    setAnalysisError("");
    setMessageError("");
    connect();
    let lastWatchdogAt = Date.now();
    let resumeGraceUntil = 0;
    const staleTimer = setInterval(() => {
      const now = Date.now();
      // A suspended/throttled event loop cannot distinguish network silence
      // from queued message callbacks. Keep freshness stale, but allow delivery
      // after resuming before deciding that the socket itself must be replaced.
      if (now - lastWatchdogAt > 5_000 || now < lastWatchdogAt) {
        resumeGraceUntil = now + 20_000;
      }
      lastWatchdogAt = now;
      if (
        socket?.readyState === WebSocket.CONNECTING &&
        now - connectingAt > 15_000 &&
        now >= resumeGraceUntil
      ) {
        // A stalled handshake may never emit close. Fence this generation and
        // own the retry so a late open/close cannot replace the new socket.
        closeCurrentSocket("market share connection timeout");
        scheduleReconnect();
        return;
      }
      if (socket?.readyState === WebSocket.OPEN) {
        const health = marketShareTransportHealth(
          now,
          openedAt,
          lastMessageRef.current,
          lastTransportAt,
          resumeGraceUntil
        );
        if (health.stale) setConnection("stale");
        if (health.reconnect) {
          closeCurrentSocket("market share transport timeout");
          scheduleReconnect();
        }
      }
    }, 1_000);
    const reconnectNow = () => {
      if (
        !active ||
        !reconnectAllowed ||
        document.visibilityState === "hidden" ||
        !navigator.onLine
      )
        return;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      reconnectTimer = null;
      if (socket?.readyState === WebSocket.OPEN) return;
      connect();
    };
    const disconnectOffline = () => {
      if (!active || !reconnectAllowed) return;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      reconnectTimer = null;
      closeCurrentSocket("market share network offline");
      markOffline();
    };
    window.addEventListener("online", reconnectNow);
    window.addEventListener("offline", disconnectOffline);
    document.addEventListener("visibilitychange", reconnectNow);

    return () => {
      active = false;
      tickStore.reset();
      realtimeFlowStore.reset();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearInterval(staleTimer);
      window.removeEventListener("online", reconnectNow);
      window.removeEventListener("offline", disconnectOffline);
      document.removeEventListener("visibilitychange", reconnectNow);
      socket?.close(1000, "market share view changed");
    };
  }, [
    coinShares,
    coinSharesStore,
    marketScope,
    notionalKrw,
    publishLiveStatus,
    realtimeFlow,
    realtimeFlowStore,
    requestKey,
    tickStore,
    view,
    windowSec,
  ]);

  const streamStateIsCurrent = streamRequestKey === requestKey;
  const noticeStateIsCurrent = noticeRequestKey === requestKey;
  const visibleError = streamStateIsCurrent
    ? transportError || messageError || tickError || analysisError
    : "";
  const errorSeverity: "error" | "warning" | null = !visibleError
    ? null
    : transportError
      ? connection === "offline"
        ? "error"
        : "warning"
      : messageError
        ? "error"
        : "warning";
  const visibleConnection: MarketShareConnection = streamStateIsCurrent
    ? tickError && connection === "live"
      ? "stale"
      : connection
    : "connecting";

  useEffect(() => {
    publishLiveStatus({ connection: visibleConnection });
  }, [publishLiveStatus, visibleConnection]);

  return {
    data: dataState?.requestKey === requestKey ? dataState.payload : null,
    tickStore,
    realtimeFlowStore,
    coinSharesStore,
    whaleAlerts,
    whaleAlertsUnavailable,
    connection: visibleConnection,
    readiness: streamStateIsCurrent ? readiness : null,
    registry,
    noticeSnapshot: noticeStateIsCurrent ? noticeSnapshot : [],
    noticeMeta: noticeStateIsCurrent ? noticeMeta : null,
    error: visibleError,
    errorSeverity,
  };
}
