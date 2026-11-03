import { useEffect, useId, useMemo, useRef, useState } from "react";
import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";
import type { NodeLabelDrawingFunction } from "sigma/rendering";
import type { CameraState } from "sigma/types";

import { getActiveNoticeNetwork } from "@/lib/api-client";
import { withParticle } from "@/lib/korean-particle";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatKRW,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import {
  activeNetworkContribution,
  activeNetworkPresentation,
} from "@/lib/active-network-presentation";
import {
  MARKET_SHARE_NETWORK_WINDOWS,
  marketShareNetworkWindowLabel,
  type MarketShareNetworkWindowSec,
} from "@/lib/market-share-config";
import type { MarketShareActiveNoticeNetwork } from "@/lib/market-share-types";

interface AssetExchangeShareNetworkProps {
  windowSec: MarketShareNetworkWindowSec;
  onWindowSecChange: (windowSec: MarketShareNetworkWindowSec) => void;
}

const EXCHANGES = ["upbit", "bithumb", "coinone", "korbit", "gopax"];
const NETWORK_GRAPH_TEXT_COLOR = "#ffffff";
const drawWhiteGraphNodeLabel: NodeLabelDrawingFunction = (
  context,
  data,
  settings
) => {
  if (!data.label) return;
  const x = data.x + data.size + 3;
  const y = data.y + settings.labelSize / 3;
  context.save();
  context.font = `${settings.labelWeight} ${settings.labelSize}px ${settings.labelFont}`;
  context.lineJoin = "round";
  context.strokeStyle = "rgb(0 0 0 / 82%)";
  context.lineWidth = 3;
  context.strokeText(data.label, x, y);
  context.fillStyle = NETWORK_GRAPH_TEXT_COLOR;
  context.fillText(data.label, x, y);
  context.restore();
};
const EXCHANGE_COLORS: Record<string, string> = {
  upbit: "#2563eb",
  bithumb: "#ff6c00",
  coinone: "#0ea5e9",
  korbit: "#8b5cf6",
  gopax: "#ef4444",
};
const retainedNetworkByWindow = new Map<
  MarketShareNetworkWindowSec,
  MarketShareActiveNoticeNetwork
>();
const EXCHANGE_POSITIONS: Record<string, { x: number; y: number }> = {
  bithumb: { x: 7, y: 0 },
  upbit: { x: 2.2, y: 6.6 },
  coinone: { x: -5.6, y: 4.1 },
  korbit: { x: -5.6, y: -4.1 },
  gopax: { x: 2.2, y: -6.6 },
};

function contributionColor(value: number | null): string {
  if (value === null || Math.abs(value) < 0.000005) return "#94a3b8";
  return value > 0 ? "#10b981" : "#ef4444";
}

function volumeSize(value: number, minimum: number, maximum: number): number {
  if (maximum <= minimum) return 14;
  const position =
    (Math.log10(Math.max(1, value)) - minimum) / (maximum - minimum);
  return 7 + Math.max(0, Math.min(1, position)) * 19;
}

function edgeSize(value: number): number {
  return Math.max(0.8, Math.min(5.5, Math.log10(Math.max(10, value)) / 2.3));
}

function share(value: number | null | undefined, digits = 2): string {
  return formatSharePercent(value, digits);
}

function contribution(value: number | null): string {
  return activeNetworkContribution(value);
}

export function AssetExchangeShareNetwork({
  windowSec,
  onWindowSecChange,
}: AssetExchangeShareNetworkProps) {
  const windowLabel = marketShareNetworkWindowLabel(windowSec);
  const sliderID = useId();
  const selectedWindowIndex = Math.max(
    0,
    MARKET_SHARE_NETWORK_WINDOWS.findIndex(
      option => option.seconds === windowSec
    )
  );
  const [data, setData] = useState<MarketShareActiveNoticeNetwork | null>(
    () => retainedNetworkByWindow.get(windowSec) ?? null
  );
  const [selectedNode, setSelectedNode] = useState("");
  const [error, setError] = useState("");
  const containerRef = useRef<HTMLDivElement>(null);
  const rendererRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);
  const selectedRef = useRef("");
  const neighborsRef = useRef<Set<string>>(new Set());
  const cameraRef = useRef<CameraState | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setData(retainedNetworkByWindow.get(windowSec) ?? null);
    // Each response rebuilds the graph. Skip polls while the tab is hidden or a
    // slow request is still open, and catch up as soon as the tab is shown.
    let inFlight = false;
    const load = () => {
      if (inFlight || document.visibilityState === "hidden") return;
      inFlight = true;
      void getActiveNoticeNetwork(windowSec, controller.signal)
        .then(value => {
          retainedNetworkByWindow.set(windowSec, value);
          setData(value);
          setError("");
        })
        .catch(cause => {
          if (!controller.signal.aborted)
            setError(cause instanceof Error ? cause.message : String(cause));
        })
        .finally(() => {
          inFlight = false;
        });
    };
    load();
    const timer = window.setInterval(load, 10_000);
    document.addEventListener("visibilitychange", load);
    return () => {
      controller.abort();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", load);
    };
  }, [windowSec]);

  const rankedAssets = useMemo(
    () =>
      [...(data?.assets ?? [])]
        .sort(
          (left, right) =>
            Math.abs(right.contribution_pp ?? 0) -
              Math.abs(left.contribution_pp ?? 0) ||
            right.current_domestic_volume_krw - left.current_domestic_volume_krw
        )
        .slice(0, 20),
    [data]
  );

  useEffect(() => {
    setSelectedNode(current => {
      if (!rankedAssets.length) return "";
      if (
        current.startsWith("exchange:") ||
        rankedAssets.some(asset => `asset:${asset.asset}` === current)
      )
        return current;
      return `asset:${rankedAssets[0].asset}`;
    });
  }, [rankedAssets]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container || !data) return;
    const graph = new Graph();
    const volumeLogs = rankedAssets.map(asset =>
      Math.log10(Math.max(1, asset.current_domestic_volume_krw))
    );
    const minimumVolumeLog = Math.min(...volumeLogs);
    const maximumVolumeLog = Math.max(...volumeLogs);
    rankedAssets.forEach((asset, index) => {
      const angle = index * 2.399963;
      const radius = 1.2 + Math.sqrt(index) * 0.55;
      graph.addNode(`asset:${asset.asset}`, {
        x: Math.cos(angle) * radius,
        y: Math.sin(angle) * radius,
        size: volumeSize(
          asset.current_domestic_volume_krw,
          minimumVolumeLog,
          maximumVolumeLog
        ),
        color: contributionColor(asset.contribution_pp),
        label: `${asset.asset}  ${contribution(asset.contribution_pp)}`,
        forceLabel: index < 8,
        kind: "asset",
      });
    });
    EXCHANGES.forEach(exchange => {
      const position = EXCHANGE_POSITIONS[exchange] ?? { x: 0, y: 0 };
      graph.addNode(`exchange:${exchange}`, {
        x: position.x,
        y: position.y,
        size: exchange === "bithumb" ? 18 : 15,
        color: EXCHANGE_COLORS[exchange],
        label: exchangeLabel(exchange),
        forceLabel: true,
        kind: "exchange",
      });
    });
    data.edges.forEach(edge => {
      if (!graph.hasNode(`asset:${edge.asset}`)) return;
      graph.addEdgeWithKey(
        `market:${edge.asset}:${edge.exchange}`,
        `asset:${edge.asset}`,
        `exchange:${edge.exchange}`,
        {
          size: edgeSize(edge.current_volume_krw),
          weight: edgeSize(edge.current_volume_krw),
          color: `${EXCHANGE_COLORS[edge.exchange] ?? "#94a3b8"}88`,
          kind: "asset-exchange",
        }
      );
    });
    forceAtlas2.assign(graph, {
      iterations: 140,
      getEdgeWeight: "weight",
      settings: {
        edgeWeightInfluence: 1,
        gravity: 0.35,
        linLogMode: true,
        scalingRatio: 7,
        slowDown: 4,
      },
    });

    const renderer = new Sigma(graph, container, {
      labelFont: "Pretendard, sans-serif",
      labelColor: { color: NETWORK_GRAPH_TEXT_COLOR },
      defaultDrawNodeLabel: drawWhiteGraphNodeLabel,
      defaultDrawNodeHover: drawWhiteGraphNodeLabel,
      labelDensity: 0.08,
      labelRenderedSizeThreshold: 10,
      minCameraRatio: 0.08,
      maxCameraRatio: 2.5,
      nodeReducer: (node, display) => {
        if (!selectedRef.current || neighborsRef.current.has(node))
          return node === selectedRef.current
            ? { ...display, size: display.size * 1.18, highlighted: true }
            : display;
        return { ...display, size: display.size * 0.82, highlighted: false };
      },
      edgeReducer: (edge, display) => {
        if (!selectedRef.current) return display;
        const [source, target] = graph.extremities(edge);
        if (source === selectedRef.current || target === selectedRef.current)
          return { ...display, hidden: false };
        return { ...display, hidden: true };
      },
    });
    graphRef.current = graph;
    rendererRef.current = renderer;
    if (cameraRef.current) renderer.getCamera().setState(cameraRef.current);
    renderer.on("clickNode", ({ node }) => {
      setSelectedNode(node);
    });
    renderer.on("clickStage", () => setSelectedNode(""));
    return () => {
      cameraRef.current = renderer.getCamera().getState();
      renderer.kill();
      graphRef.current = null;
      rendererRef.current = null;
    };
  }, [data, rankedAssets]);

  useEffect(() => {
    selectedRef.current = selectedNode;
    const graph = graphRef.current;
    neighborsRef.current = new Set(
      graph && selectedNode && graph.hasNode(selectedNode)
        ? [selectedNode, ...graph.neighbors(selectedNode)]
        : []
    );
    rendererRef.current?.refresh({ skipIndexation: true });
  }, [selectedNode]);

  const selection = useMemo(() => {
    if (!data || !selectedNode) return null;
    if (selectedNode.startsWith("asset:"))
      return (
        data.assets.find(row => `asset:${row.asset}` === selectedNode) ?? null
      );
    const exchange = selectedNode.replace("exchange:", "");
    return EXCHANGES.includes(exchange) ? { exchange } : null;
  }, [data, selectedNode]);

  const visibleAssets = rankedAssets.slice(0, 4);
  const presentation = activeNetworkPresentation(data?.status, data?.reason);
  const selectedAssetEdges = useMemo(() => {
    if (!data || !(selection && "asset" in selection)) return [];
    return data.edges
      .filter(edge => edge.asset === selection.asset)
      .sort(
        (left, right) => right.current_volume_krw - left.current_volume_krw
      );
  }, [data, selection]);
  const selectedExchangeAssets = useMemo(() => {
    if (!data || !(selection && "exchange" in selection)) return [];
    return data.edges
      .filter(edge => edge.exchange === selection.exchange)
      .sort((left, right) => right.current_volume_krw - left.current_volume_krw)
      .slice(0, 8);
  }, [data, selection]);

  return (
    <div className="overflow-hidden">
      <div className="border-b border-border bg-card px-4 py-4 sm:px-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <label htmlFor={sliderID} className="ui-caption font-semibold">
            관계 분석 구간
          </label>
          <output
            htmlFor={sliderID}
            className="rounded-md border border-primary/35 bg-primary/10 px-3 py-1.5 font-mono text-base font-semibold text-primary"
          >
            {windowLabel}
          </output>
        </div>
        <input
          id={sliderID}
          type="range"
          min={0}
          max={MARKET_SHARE_NETWORK_WINDOWS.length - 1}
          step={1}
          value={selectedWindowIndex}
          aria-label="코인·거래소 관계 분석 구간"
          aria-valuetext={windowLabel}
          onChange={event => {
            const selected =
              MARKET_SHARE_NETWORK_WINDOWS[Number(event.currentTarget.value)];
            if (selected) onWindowSecChange(selected.seconds);
          }}
          className="mt-4 h-2 w-full cursor-pointer accent-primary"
        />
        <div className="mt-3 flex flex-wrap justify-between gap-x-2 gap-y-1">
          {MARKET_SHARE_NETWORK_WINDOWS.map(option => (
            <button
              key={option.seconds}
              type="button"
              aria-pressed={option.seconds === windowSec}
              onClick={() => onWindowSecChange(option.seconds)}
              className={`ui-caption rounded px-1.5 py-1 transition-colors ${
                option.seconds === windowSec
                  ? "bg-foreground font-semibold text-background"
                  : "text-muted-foreground hover:bg-secondary hover:text-foreground"
              }`}
            >
              {option.label}
            </button>
          ))}
        </div>
        <p className="ui-caption mt-3 text-muted-foreground">
          최근 {withParticle(windowLabel, "과/와")} 직전{" "}
          {withParticle(windowLabel, "을/를")} 같은 길이로 비교합니다. 24시간
          기여도는 연속 48시간 자료가 있을 때만 확정합니다.
        </p>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3 sm:px-5">
        <span className="ui-caption rounded-md bg-foreground px-3 py-1.5 font-semibold text-background">
          {windowLabel} · {presentation.label}
        </span>
        <div className="ui-caption flex items-center gap-4 tabular-nums text-muted-foreground">
          <span>
            빗썸 점유율 {share(data?.current_bithumb_share_pct ?? null)}
          </span>
          <span
            className={
              data?.share_delta_pp == null ||
              !Number.isFinite(data.share_delta_pp) ||
              data.share_delta_pp === 0
                ? "text-muted-foreground"
                : data.share_delta_pp < 0
                  ? "text-loss"
                  : "text-profit"
            }
          >
            {windowLabel} 변화{" "}
            {data?.share_delta_pp == null
              ? "—"
              : formatSignedPercentagePoints(data.share_delta_pp)}
          </span>
          <span>
            상위 기여 코인{" "}
            {data &&
            (data.status === "CONFIRMED" || data.status === "PROVISIONAL")
              ? `${rankedAssets.length}개`
              : "확인 중"}
          </span>
        </div>
      </div>
      {presentation.detail ? (
        <p className="ui-caption border-b border-border px-4 py-3 text-muted-foreground sm:px-5">
          {presentation.detail}
        </p>
      ) : null}
      {visibleAssets.length ? (
        <div className="border-b border-border bg-card px-4 py-3 sm:px-5">
          <p className="ui-caption mb-2 font-semibold text-muted-foreground">
            주요 기여 코인
          </p>
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
            {visibleAssets.map(asset => {
              const node = `asset:${asset.asset}`;
              const active = selectedNode === node;
              return (
                <button
                  key={asset.asset}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setSelectedNode(active ? "" : node)}
                  className={`rounded-lg border p-3 text-left transition-colors ${active ? "border-primary bg-primary/10" : "border-border bg-well hover:border-primary/40"}`}
                >
                  <div className="flex items-center justify-between gap-3">
                    <strong className="font-mono text-base">
                      {asset.asset}
                    </strong>
                    <span
                      className="ui-caption font-semibold tabular-nums"
                      style={{
                        color: contributionColor(asset.contribution_pp),
                      }}
                    >
                      {contribution(asset.contribution_pp)}
                    </span>
                  </div>
                  <div className="ui-caption mt-2 grid grid-cols-2 gap-2">
                    <span className="text-muted-foreground">국내 거래금액</span>
                    <span className="whitespace-nowrap text-right tabular-nums">
                      {formatKRW(asset.current_domestic_volume_krw)}
                    </span>
                    <span className="text-muted-foreground">빗썸 비중</span>
                    <span className="text-right tabular-nums">
                      {share(asset.current_bithumb_share_pct, 1)}
                    </span>
                  </div>
                </button>
              );
            })}
          </div>
        </div>
      ) : null}
      <div className="grid min-h-[560px] xl:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="relative min-h-[560px] overflow-hidden bg-inset">
          <div
            ref={containerRef}
            className="absolute inset-0"
            aria-label={`${windowLabel} 코인별 거래금액과 점유율 기여 네트워크`}
          />
          <div className="ui-caption pointer-events-none absolute left-3 top-3 z-10 flex max-w-[calc(100%-1.5rem)] flex-wrap gap-1.5">
            <span className="rounded-full bg-card px-2.5 py-1 text-profit shadow-sm">
              초록 · 점유율 상승
            </span>
            <span className="rounded-full bg-card px-2.5 py-1 text-loss shadow-sm">
              빨강 · 점유율 하락
            </span>
            <span className="rounded-full bg-card px-2.5 py-1 text-muted-foreground shadow-sm">
              원 크기 · 국내 거래금액
            </span>
            <span className="rounded-full bg-card px-2.5 py-1 text-muted-foreground shadow-sm">
              선 굵기 · 거래소 거래금액
            </span>
          </div>
          {!data?.assets.length ? (
            <div className="ui-caption pointer-events-none absolute inset-0 grid place-items-center text-muted-foreground">
              표시할 코인별 기여 자료가 없습니다.
            </div>
          ) : null}
          {error ? (
            <span className="ui-caption absolute bottom-3 left-3 rounded-md bg-loss/10 px-2 py-1 text-loss">
              갱신 지연
            </span>
          ) : null}
        </div>
        <aside className="border-t border-border bg-card p-4 xl:border-l xl:border-t-0">
          <p className="ui-caption font-semibold text-muted-foreground">
            선택 상세
          </p>
          {!selection ? (
            <p className="ui-caption mt-3 leading-5 text-muted-foreground">
              코인이나 거래소를 선택하세요.
            </p>
          ) : "asset" in selection ? (
            <div className="mt-3 space-y-3">
              <p className="font-mono text-2xl font-semibold">
                {selection.asset}
              </p>
              <dl className="ui-caption grid gap-2">
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">점유율 기여</dt>
                  <dd>{contribution(selection.contribution_pp)}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">국내 거래금액</dt>
                  <dd>{formatKRW(selection.current_domestic_volume_krw)}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">빗썸 거래금액</dt>
                  <dd>{formatKRW(selection.current_bithumb_volume_krw)}</dd>
                </div>
                <div className="flex justify-between gap-3">
                  <dt className="text-muted-foreground">빗썸 비중</dt>
                  <dd>{share(selection.current_bithumb_share_pct, 1)}</dd>
                </div>
              </dl>
              <div className="border-t border-border pt-3">
                <p className="ui-caption mb-2 font-semibold text-muted-foreground">
                  거래소별 거래금액
                </p>
                <div className="ui-caption space-y-2">
                  {selectedAssetEdges.map(edge => (
                    <div
                      key={edge.exchange}
                      className="grid grid-cols-[1fr_auto_auto] items-center gap-2"
                    >
                      <span>{exchangeLabel(edge.exchange)}</span>
                      <span className="tabular-nums">
                        {formatKRW(edge.current_volume_krw)}
                      </span>
                      <span className="w-12 text-right tabular-nums text-muted-foreground">
                        {share(edge.current_domestic_share_pct, 1)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          ) : (
            <div className="mt-3 space-y-2">
              <p className="text-xl font-semibold">
                {exchangeLabel(selection.exchange)}
              </p>
              <p className="ui-caption text-muted-foreground">
                거래금액 상위 연결 코인
              </p>
              <div className="ui-caption space-y-2 border-t border-border pt-3">
                {selectedExchangeAssets.map(edge => (
                  <div key={edge.asset} className="flex justify-between gap-3">
                    <span className="font-mono font-semibold">
                      {edge.asset}
                    </span>
                    <span className="tabular-nums">
                      {formatKRW(edge.current_volume_krw)}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </aside>
      </div>
    </div>
  );
}
