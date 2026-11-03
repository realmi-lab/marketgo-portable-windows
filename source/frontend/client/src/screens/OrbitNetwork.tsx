import { useEffect, useId, useMemo, useRef, useState } from "react";

import { useTheme } from "@/contexts/ThemeContext";
import { useMediaQuery } from "@/hooks/useMediaQuery";
import { cycledId } from "@/lib/chart-interaction";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatKRW,
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import type {
  MarketShareAssetExchangeNetwork,
  MarketShareAssetExchangeNetworkEdge,
  MarketShareAssetExchangeNetworkNode,
} from "@/lib/market-share-types";
import { linkPath, orbitLayout, pointOnLink } from "@/lib/orbit-layout";
import { useWidth } from "./charts";
import { exchangeColor, Legend } from "./ui";

/*
 * The coin–exchange orbit: exchanges on the inner ring, coins on the outer
 * orbit, one curve per coin traded on an exchange. A curve's width is that
 * trading, its colour says whether it raised (green) or lowered (red)
 * Bithumb's share, and points flow along it faster where trading is heavier.
 * Each coin's ring shows Bithumb's part of that coin, as the server gives it.
 */

const EXCHANGE_ORDER = ["bithumb", "upbit", "coinone", "korbit", "gopax"];

/** A stage this wide keeps a column on its right for the key and the coin. */
const SIDE_COLUMN_MIN_WIDTH = 760;
const SIDE_COLUMN = 300;
/** A narrower stage leaves this much of the card for the key and the hint. */
const NARROW_ROOM = 110;

const KEY = [
  {
    key: "up",
    label: "빗썸 점유율을 올린 거래",
    color: "var(--profit)",
  },
  { key: "down", label: "내린 거래", color: "var(--loss)" },
  {
    key: "ring",
    label: "코인 둘레: 그 코인 거래 중 빗썸 몫",
    color: "var(--exchange-bithumb)",
  },
];

type Focus = { kind: "coin" | "exchange"; id: string };

/** A curve's colour token: green raised Bithumb's share, red lowered it. */
function effectToken(edge: MarketShareAssetExchangeNetworkEdge | undefined) {
  if (edge?.share_effect === "RAISES_BITHUMB_SHARE") return "--profit";
  if (edge?.share_effect === "LOWERS_BITHUMB_SHARE") return "--loss";
  return "--muted-foreground";
}

export function OrbitNetwork({
  network,
  bithumbShare,
  centre,
  selected,
  onSelect,
  height: tallest = 560,
}: {
  network: MarketShareAssetExchangeNetwork;
  /** Bithumb's part of each coin's trading, from the server. */
  bithumbShare: ReadonlyMap<string, number | null>;
  /** What the middle of the orbit says while nothing is picked. */
  centre: { label: string; value: string; detail: string };
  selected: string | null;
  onSelect: (asset: string | null) => void;
  /** The stage's height on a wide card. */
  height?: number;
}) {
  const { ref, width } = useWidth<HTMLDivElement>();
  const wide = width >= SIDE_COLUMN_MIN_WIDTH;
  // A narrow stage keeps room below for the key, and on a phone it is about
  // as tall as it is wide.
  const height = Math.min(
    wide ? tallest : tallest - NARROW_ROOM,
    Math.max(320, Math.round(width * 0.9))
  );
  const [hover, setHover] = useState<Focus | null>(null);
  // After a pin scrolls the card, a coin sliding under a resting pointer is
  // not hovered until the pointer moves again.
  const hoverPaused = useRef(false);
  const hoverOn = (focus: Focus) => {
    if (!hoverPaused.current) setHover(focus);
  };
  // The coin the arrow keys are on, by name: the order changes with the data.
  const [keyCoin, setKeyCoin] = useState<string | null>(null);
  // What a screen reader heard for that coin, read when the keys moved.
  const [spoken, setSpoken] = useState("");
  const canvas = useRef<HTMLCanvasElement>(null);
  const side = useRef<HTMLDivElement>(null);
  const glowId = useId();
  const coreId = useId();
  const reducedMotion = useMediaQuery("(prefers-reduced-motion: reduce)");
  const { theme } = useTheme();

  const exchangeNodes = network.nodes.filter(node => node.kind === "EXCHANGE");
  const coinNodes = network.nodes.filter(node => node.kind === "ASSET");
  const edges = network.edges;
  const layout = useMemo(
    () =>
      orbitLayout({
        // The orbit sits left of the side column on a wide stage.
        width: wide ? width - SIDE_COLUMN : width,
        height,
        exchanges: EXCHANGE_ORDER.flatMap(id => {
          const node = exchangeNodes.find(item => item.label === id);
          return node ? [{ id, volume: node.current_volume_krw }] : [];
        }),
        coins: coinNodes.map(node => ({
          id: node.label,
          volume: node.current_volume_krw,
        })),
        links: edges.map(edge => ({
          id: edge.id,
          coin: edge.asset,
          exchange: edge.exchange,
          volume: edge.current_volume_krw,
        })),
      }),
    [network, width, height]
  );
  const edgeById = new Map(edges.map(edge => [edge.id, edge]));
  const nodeOf = (kind: Focus["kind"], id: string) =>
    network.nodes.find(
      node =>
        node.kind === (kind === "coin" ? "ASSET" : "EXCHANGE") &&
        node.label === id
    );
  const keyFocus: Focus | null =
    keyCoin && layout.coins.some(coin => coin.id === keyCoin)
      ? { kind: "coin", id: keyCoin }
      : null;
  const focus: Focus | null =
    hover ?? keyFocus ?? (selected ? { kind: "coin", id: selected } : null);
  const focusKey = focus ? `${focus.kind}:${focus.id}` : "";
  // What stays lit while a coin or an exchange is picked.
  const linkLit = (coin: string, exchange: string) =>
    !focus ||
    (focus.kind === "coin" ? coin === focus.id : exchange === focus.id);
  const traded = (coin: string, exchange: string) =>
    edges.some(edge => edge.asset === coin && edge.exchange === exchange);
  const coinLit = (coin: string) =>
    !focus ||
    (focus.kind === "coin" ? coin === focus.id : traded(coin, focus.id));
  const exchangeLit = (exchange: string) =>
    !focus ||
    (focus.kind === "exchange"
      ? exchange === focus.id
      : traded(focus.id, exchange));

  // Points flowing from each coin toward its exchanges. They run only while
  // the orbit is on screen and the tab is shown, never for readers who asked
  // for less motion, and take the theme's colours again when it changes.
  useEffect(() => {
    const element = canvas.current;
    const context = element?.getContext("2d");
    if (!element || !context) return;
    const ratio = window.devicePixelRatio || 1;
    element.width = Math.round(width * ratio);
    element.height = Math.round(height * ratio);
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    context.clearRect(0, 0, width, height);
    if (reducedMotion) return;
    const styles = getComputedStyle(element);
    const particles = layout.links
      .filter(link => linkLit(link.coin, link.exchange))
      .flatMap(link => {
        const count = 1 + Math.round(link.weight * 3);
        return Array.from({ length: count }, (_, index) => ({
          link,
          phase: index / count,
          speed: 0.08 + 0.22 * link.weight,
          size: 1.25 + link.weight * 1.75,
          colour: styles
            .getPropertyValue(effectToken(edgeById.get(link.id)))
            .trim(),
        }));
      });
    const alpha = focusKey ? 0.95 : 0.7;
    let frame = 0;
    let running = false;
    const draw = (time: number) => {
      if (!running) return;
      context.clearRect(0, 0, width, height);
      if (!document.hidden) {
        for (const particle of particles) {
          // From the coin (t = 1) toward the exchange (t = 0).
          const t = 1 - ((particle.phase + (time / 1000) * particle.speed) % 1);
          const point = pointOnLink(particle.link, t);
          context.fillStyle = particle.colour;
          // A soft halo, then the point: a glow without a blur filter.
          context.globalAlpha = alpha * 0.22;
          context.beginPath();
          context.arc(point.x, point.y, particle.size * 2.6, 0, Math.PI * 2);
          context.fill();
          context.globalAlpha = alpha;
          context.beginPath();
          context.arc(point.x, point.y, particle.size, 0, Math.PI * 2);
          context.fill();
        }
      }
      frame = requestAnimationFrame(draw);
    };
    const observer = new IntersectionObserver(entries => {
      const entry = entries[entries.length - 1];
      if (entry.isIntersecting && !running) {
        running = true;
        frame = requestAnimationFrame(draw);
      } else if (!entry.isIntersecting) {
        running = false;
        cancelAnimationFrame(frame);
      }
    });
    observer.observe(element);
    return () => {
      running = false;
      cancelAnimationFrame(frame);
      observer.disconnect();
    };
    // A new focus restarts the loop with the links it lights.
  }, [layout, width, height, focusKey, reducedMotion, theme]);

  const roomy = layout.inner >= 72;
  /** What the middle of the orbit, or a screen reader, says about a node. */
  const summary = (node: MarketShareAssetExchangeNetworkNode) => ({
    label: node.kind === "ASSET" ? node.label : exchangeLabel(node.label),
    value:
      node.kind === "ASSET"
        ? `빗썸 ${formatSharePercent(bithumbShare.get(node.label))}`
        : formatKRW(node.current_volume_krw),
    detail: `기여 ${formatSignedPercentagePoints(node.contribution_pp)}`,
  });
  const focusNode = focus ? nodeOf(focus.kind, focus.id) : undefined;
  const middle = focusNode ? summary(focusNode) : centre;
  const moveKey = (step: 1 | -1) => {
    const next = cycledId(
      layout.coins.map(coin => coin.id),
      keyFocus?.id ?? null,
      step
    );
    const node = next ? nodeOf("coin", next) : undefined;
    setKeyCoin(next);
    if (node) {
      const said = summary(node);
      setSpoken(`${said.label}, ${said.value}, ${said.detail}`);
    }
  };
  const detailCoin = selected && nodeOf("coin", selected) ? selected : null;
  const detailEdges = detailCoin
    ? EXCHANGE_ORDER.flatMap(id =>
        edges.filter(edge => edge.asset === detailCoin && edge.exchange === id)
      )
    : [];
  // Below a narrow stage a pinned coin's exchanges are scrolled into view.
  useEffect(() => {
    if (detailCoin && !wide) {
      hoverPaused.current = true;
      setHover(null);
      side.current?.scrollIntoView({
        block: "nearest",
        behavior: reducedMotion ? "auto" : "smooth",
      });
    }
  }, [detailCoin]);

  return (
    <div className="mg-orbit" data-wide={wide ? "" : undefined}>
      <div
        ref={ref}
        className="mg-orbit-stage"
        style={{ height }}
        role="group"
        aria-label="코인과 거래소의 거래 연결. 화살표로 코인을 고르고 Enter로 자세히 봅니다."
        tabIndex={0}
        onKeyDown={event => {
          if (event.key === "ArrowRight" || event.key === "ArrowDown") {
            event.preventDefault();
            moveKey(1);
          } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
            event.preventDefault();
            moveKey(-1);
          } else if (event.key === "Enter" && keyFocus) {
            event.preventDefault();
            onSelect(keyFocus.id === selected ? null : keyFocus.id);
          } else if (event.key === "Escape") {
            setKeyCoin(null);
            onSelect(null);
          }
        }}
        onBlur={() => setKeyCoin(null)}
        onPointerMove={event => {
          if (event.movementX || event.movementY) hoverPaused.current = false;
        }}
      >
        <svg
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          aria-hidden="true"
          fontSize={12}
        >
          <defs>
            <radialGradient id={coreId}>
              <stop
                offset="0%"
                stopColor="var(--exchange-bithumb)"
                stopOpacity={0.14}
              />
              <stop
                offset="100%"
                stopColor="var(--exchange-bithumb)"
                stopOpacity={0}
              />
            </radialGradient>
            <filter id={glowId} x="-50%" y="-50%" width="200%" height="200%">
              <feGaussianBlur stdDeviation="4" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          <circle
            cx={layout.center.x}
            cy={layout.center.y}
            r={layout.inner * 1.1}
            fill={`url(#${coreId})`}
          />
          <circle
            className="mg-orbit-ring"
            cx={layout.center.x}
            cy={layout.center.y}
            r={layout.inner}
          />
          <circle
            className="mg-orbit-ring"
            data-outer=""
            cx={layout.center.x}
            cy={layout.center.y}
            r={layout.outer}
          />
          {layout.links.map(link => {
            const lit = linkLit(link.coin, link.exchange);
            return (
              <path
                key={link.id}
                d={linkPath(link)}
                fill="none"
                stroke={`var(${effectToken(edgeById.get(link.id))})`}
                strokeWidth={link.width}
                strokeLinecap="round"
                opacity={focus ? (lit ? 0.9 : 0.05) : 0.18 + 0.42 * link.weight}
                filter={focus && lit ? `url(#${glowId})` : undefined}
              />
            );
          })}
          <circle
            className="mg-orbit-hub"
            cx={layout.center.x}
            cy={layout.center.y}
            r={layout.inner * 0.62}
          />
          {layout.coins.map(coin => {
            const share = bithumbShare.get(coin.id);
            const ring = coin.r + 4;
            const circumference = 2 * Math.PI * ring;
            // Labels stay level, just outside the coin, away from the orbit.
            const cos = Math.cos(coin.angle);
            const sin = Math.sin(coin.angle);
            const labelRadius = layout.outer + coin.r + 10;
            return (
              <g
                key={coin.id}
                className="mg-orbit-coin"
                data-picked={coin.id === selected ? "" : undefined}
                data-keyed={coin.id === keyFocus?.id ? "" : undefined}
                opacity={coinLit(coin.id) ? 1 : 0.25}
                onPointerEnter={() => hoverOn({ kind: "coin", id: coin.id })}
                onPointerLeave={() => setHover(null)}
                onClick={() => onSelect(coin.id === selected ? null : coin.id)}
              >
                <circle
                  cx={coin.x}
                  cy={coin.y}
                  r={ring + 6}
                  fill="transparent"
                />
                <circle
                  cx={coin.x}
                  cy={coin.y}
                  r={ring}
                  fill="none"
                  stroke="var(--border)"
                  strokeWidth={3}
                />
                {typeof share === "number" ? (
                  <circle
                    cx={coin.x}
                    cy={coin.y}
                    r={ring}
                    fill="none"
                    stroke="var(--exchange-bithumb)"
                    strokeWidth={3}
                    strokeDasharray={`${(Math.min(100, Math.max(0, share)) / 100) * circumference} ${circumference}`}
                    transform={`rotate(-90 ${coin.x} ${coin.y})`}
                  />
                ) : null}
                <circle
                  className="mg-orbit-core"
                  cx={coin.x}
                  cy={coin.y}
                  r={coin.r}
                />
                <text
                  className="mg-orbit-label"
                  x={layout.center.x + labelRadius * cos}
                  y={layout.center.y + labelRadius * sin}
                  dy={sin < -0.5 ? 0 : sin > 0.5 ? "0.75em" : "0.35em"}
                  textAnchor={
                    cos > 0.5 ? "start" : cos < -0.5 ? "end" : "middle"
                  }
                >
                  {coin.id}
                </text>
              </g>
            );
          })}
          {layout.exchanges.map(node => {
            // Names sit outward from the ring, clear of the middle.
            const cos = Math.cos(node.angle);
            const sin = Math.sin(node.angle);
            const nameRadius = layout.inner + node.r + 8;
            return (
              <g
                key={node.id}
                className="mg-orbit-exchange"
                opacity={exchangeLit(node.id) ? 1 : 0.3}
                onPointerEnter={() =>
                  hoverOn({ kind: "exchange", id: node.id })
                }
                onPointerLeave={() => setHover(null)}
              >
                <circle
                  cx={node.x}
                  cy={node.y}
                  r={node.r}
                  fill={exchangeColor(node.id)}
                  fillOpacity={0.22}
                  stroke={exchangeColor(node.id)}
                  strokeWidth={2}
                  filter={node.id === "bithumb" ? `url(#${glowId})` : undefined}
                />
                <text
                  className="mg-orbit-name"
                  x={layout.center.x + nameRadius * cos}
                  y={layout.center.y + nameRadius * sin}
                  dy={sin < -0.5 ? 0 : sin > 0.5 ? "0.75em" : "0.35em"}
                  textAnchor={
                    cos > 0.5 ? "start" : cos < -0.5 ? "end" : "middle"
                  }
                >
                  {exchangeLabel(node.id)}
                </text>
              </g>
            );
          })}
          {/* A small orbit (a phone) leaves its middle empty. */}
          {roomy ? (
            <>
              <text
                className="mg-orbit-centre-label"
                x={layout.center.x}
                y={layout.center.y - 16}
                textAnchor="middle"
              >
                {middle.label}
              </text>
              <text
                className="mg-orbit-centre-value"
                x={layout.center.x}
                y={layout.center.y + 8}
                textAnchor="middle"
              >
                {middle.value}
              </text>
              <text
                className="mg-orbit-centre-label"
                x={layout.center.x}
                y={layout.center.y + 28}
                textAnchor="middle"
              >
                {middle.detail}
              </text>
            </>
          ) : null}
        </svg>
        <canvas
          ref={canvas}
          className="mg-orbit-flow"
          style={{ width, height }}
          aria-hidden="true"
        />
      </div>
      {/* Read aloud only when the arrow keys reach a coin. */}
      <p className="mg-sr-only" aria-live="polite">
        {spoken}
      </p>
      <div ref={side} className="mg-orbit-side">
        <Legend items={KEY} />
        {detailCoin ? (
          <div className="mg-orbit-detail">
            <div className="mg-orbit-detail-head">
              <b>
                {detailCoin} · 빗썸{" "}
                {formatSharePercent(bithumbShare.get(detailCoin))}
              </b>
              <button
                type="button"
                className="mg-button"
                onClick={() => onSelect(null)}
              >
                닫기
              </button>
            </div>
            <table
              className="mg-table"
              aria-label={`${detailCoin} 거래소별 거래`}
            >
              <thead>
                <tr>
                  <th>거래소</th>
                  <th className="mg-num">
                    거래금액
                    <div className="mg-muted">직전 대비</div>
                  </th>
                  <th className="mg-num">기여</th>
                </tr>
              </thead>
              <tbody>
                {detailEdges.map(edge => (
                  <tr key={edge.id}>
                    <td>
                      <span className="mg-ex">
                        <i
                          style={{ background: exchangeColor(edge.exchange) }}
                          aria-hidden="true"
                        />
                        {exchangeLabel(edge.exchange)}
                      </span>
                    </td>
                    <td className="mg-num">
                      {formatKRW(edge.current_volume_krw)}
                      <div className="mg-muted">
                        {formatKrwCompact(edge.volume_change_krw)}
                      </div>
                    </td>
                    <td
                      className={`mg-num ${edge.contribution_pp > 0 ? "mg-up" : edge.contribution_pp < 0 ? "mg-down" : ""}`}
                    >
                      {formatSignedPercentagePoints(edge.contribution_pp)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="mg-muted">
            선이 굵을수록 거래가 많습니다. 코인을 누르면 그 코인의 연결이
            밝아지고 거래소별 거래가 여기에 나옵니다. 화살표와 Enter로도 고를 수
            있습니다.
          </p>
        )}
      </div>
    </div>
  );
}
