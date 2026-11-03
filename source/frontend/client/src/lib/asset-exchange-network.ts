export type AssetExchangeNetworkDirection =
  "INCREASE" | "DECREASE" | "UNCHANGED";

export type AssetExchangeNetworkFilter = "ALL" | "INCREASE" | "DECREASE";

export interface AssetExchangeNetworkLayoutNode {
  id: string;
  kind: "ASSET" | "EXCHANGE";
  current_volume_krw: number;
}

export interface AssetExchangeNetworkLayoutEdge {
  id: string;
  volume_direction: AssetExchangeNetworkDirection;
}

export interface AssetExchangeNetworkPoint {
  x: number;
  y: number;
}

export interface AssetExchangeNetworkLayout {
  width: number;
  height: number;
  positions: Readonly<Record<string, AssetExchangeNetworkPoint>>;
}

export const ASSET_EXCHANGE_NETWORK_WIDTH = 1_040;
export const ASSET_EXCHANGE_NETWORK_HEIGHT = 600;
export const ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH = 1.25;

function finiteNonNegative(value: number): number {
  return Number.isFinite(value) ? Math.max(0, value) : 0;
}

/**
 * A stable, presentation-only layout. Input order comes from the server and no
 * analytical value or ranking is recalculated here.
 */
export function buildAssetExchangeNetworkLayout(
  nodes: readonly AssetExchangeNetworkLayoutNode[]
): AssetExchangeNetworkLayout {
  const assets = nodes.filter(node => node.kind === "ASSET");
  const exchanges = nodes.filter(node => node.kind === "EXCHANGE");
  const positions: Record<string, AssetExchangeNetworkPoint> = {};
  const centerX = ASSET_EXCHANGE_NETWORK_WIDTH / 2;
  const centerY = ASSET_EXCHANGE_NETWORK_HEIGHT / 2;

  assets.forEach((node, index) => {
    const denominator = Math.max(assets.length, 1);
    const angle = -Math.PI / 2 + (index / denominator) * Math.PI * 2;
    positions[node.id] = {
      x: centerX + Math.cos(angle) * 430,
      y: centerY + Math.sin(angle) * 235,
    };
  });

  exchanges.forEach((node, index) => {
    if (exchanges.length === 1) {
      positions[node.id] = { x: centerX, y: centerY };
      return;
    }
    const angle = -Math.PI / 2 + (index / exchanges.length) * Math.PI * 2;
    positions[node.id] = {
      x: centerX + Math.cos(angle) * 128,
      y: centerY + Math.sin(angle) * 82,
    };
  });

  return {
    width: ASSET_EXCHANGE_NETWORK_WIDTH,
    height: ASSET_EXCHANGE_NETWORK_HEIGHT,
    positions,
  };
}

/** Line thickness encodes only the current trading amount. */
export function assetExchangeNetworkEdgeWidth(
  currentVolumeKRW: number,
  maximumCurrentVolumeKRW: number
): number {
  const current = finiteNonNegative(currentVolumeKRW);
  const maximum = finiteNonNegative(maximumCurrentVolumeKRW);
  if (current === 0 || maximum === 0) {
    return ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH;
  }
  const ratio = Math.min(1, current / maximum);
  return ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH + Math.sqrt(ratio) * 5.75;
}

export function filterAssetExchangeNetworkEdges<
  Edge extends AssetExchangeNetworkLayoutEdge,
>(edges: readonly Edge[], filter: AssetExchangeNetworkFilter): Edge[] {
  if (filter === "ALL") return [...edges];
  return edges.filter(edge => edge.volume_direction === filter);
}

function stableSignedUnit(value: string): number {
  let hash = 2_166_136_261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16_777_619);
  }
  return ((hash >>> 0) / 4_294_967_295) * 2 - 1;
}

/** Stable quadratic curve prevents identical links from visually collapsing. */
export function assetExchangeNetworkEdgePath(
  id: string,
  source: AssetExchangeNetworkPoint,
  target: AssetExchangeNetworkPoint
): string {
  const dx = target.x - source.x;
  const dy = target.y - source.y;
  const distance = Math.max(1, Math.hypot(dx, dy));
  const bend = stableSignedUnit(id) * Math.min(28, distance * 0.075);
  const normalX = -dy / distance;
  const normalY = dx / distance;
  const controlX = (source.x + target.x) / 2 + normalX * bend;
  const controlY = (source.y + target.y) / 2 + normalY * bend;
  return `M ${source.x.toFixed(2)} ${source.y.toFixed(2)} Q ${controlX.toFixed(2)} ${controlY.toFixed(2)} ${target.x.toFixed(2)} ${target.y.toFixed(2)}`;
}
