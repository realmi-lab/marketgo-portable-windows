/*
 * Geometry of the coin–exchange orbit: the exchanges sit on an inner ring,
 * the coins on an outer orbit, and each trading link is a curve between
 * them. Sizes only scale the server's amounts for drawing; no figure shown on
 * screen is derived here.
 */

export interface OrbitPoint {
  x: number;
  y: number;
}

export interface OrbitNode extends OrbitPoint {
  id: string;
  /** Radius in px. */
  r: number;
  /** Position on its ring, in radians; -π/2 is the top. */
  angle: number;
}

export interface OrbitLink {
  id: string;
  coin: string;
  exchange: string;
  from: OrbitPoint;
  via: OrbitPoint;
  to: OrbitPoint;
  /** Stroke width in px. */
  width: number;
  /** 0–1: this link's amount against the largest link, for emphasis. */
  weight: number;
}

export interface OrbitLayout {
  center: OrbitPoint;
  inner: number;
  outer: number;
  exchanges: OrbitNode[];
  coins: OrbitNode[];
  links: OrbitLink[];
}

interface Sized {
  id: string;
  volume: number;
}

export function orbitLayout(input: {
  width: number;
  height: number;
  /** In ring order; the first sits at the top. */
  exchanges: readonly Sized[];
  coins: readonly Sized[];
  links: ReadonlyArray<Sized & { coin: string; exchange: string }>;
}): OrbitLayout {
  const center = { x: input.width / 2, y: input.height / 2 };
  const size = Math.min(input.width, input.height);
  // Coin names sit outside the orbit, so it leaves them 60px to the edge.
  const outer = Math.min(size * 0.36, size / 2 - 60);
  const inner = outer * 0.47;
  // Nodes and lines shrink with a small orbit (a phone) so they never crowd.
  const zoom = Math.min(1, Math.max(0.55, size / 560));
  const top = -Math.PI / 2;
  const scale = (value: number, max: number) =>
    Math.sqrt(Math.max(0, value) / Math.max(max, 1));
  const at = (radius: number, angle: number) => ({
    x: center.x + radius * Math.cos(angle),
    y: center.y + radius * Math.sin(angle),
  });

  const largestExchange = Math.max(0, ...input.exchanges.map(e => e.volume));
  const exchanges = input.exchanges.map((exchange, index) => {
    const angle = top + (index / input.exchanges.length) * 2 * Math.PI;
    return {
      id: exchange.id,
      angle,
      r: (10 + 14 * scale(exchange.volume, largestExchange)) * zoom,
      ...at(inner, angle),
    };
  });
  const exchangeIndex = new Map(
    exchanges.map((node, index) => [node.id, index])
  );

  // Each coin joins the cluster of the exchange where it trades most, so its
  // links stay short and the orbit reads as groups around the exchanges.
  const home = new Map<string, number>();
  const homeVolume = new Map<string, number>();
  for (const link of input.links) {
    const index = exchangeIndex.get(link.exchange);
    if (index === undefined) continue;
    if (link.volume > (homeVolume.get(link.coin) ?? -1)) {
      home.set(link.coin, index);
      homeVolume.set(link.coin, link.volume);
    }
  }
  const homeOf = (coin: string) => home.get(coin) ?? exchanges.length;
  const ordered = [...input.coins].sort(
    (left, right) =>
      homeOf(left.id) - homeOf(right.id) || right.volume - left.volume
  );
  const step = (2 * Math.PI) / Math.max(1, ordered.length);
  // Centre the first cluster on its exchange; the rest follow around.
  const firstHome = ordered.length ? homeOf(ordered[0].id) : 0;
  const firstCluster = ordered.filter(coin => homeOf(coin.id) === firstHome);
  const start =
    (exchanges[firstHome]?.angle ?? top) -
    ((firstCluster.length - 1) / 2) * step;
  const largestCoin = Math.max(0, ...input.coins.map(coin => coin.volume));
  const coins = ordered.map((coin, index) => {
    const angle = start + index * step;
    return {
      id: coin.id,
      angle,
      r: (5 + 11 * scale(coin.volume, largestCoin)) * zoom,
      ...at(outer, angle),
    };
  });

  const coinNode = new Map(coins.map(node => [node.id, node]));
  const exchangeNode = new Map(exchanges.map(node => [node.id, node]));
  const largestLink = Math.max(0, ...input.links.map(link => link.volume));
  const links = input.links.flatMap(link => {
    const from = exchangeNode.get(link.exchange);
    const to = coinNode.get(link.coin);
    if (!from || !to) return [];
    // The curve bows toward the centre, like a path between two orbits.
    const mid = { x: (from.x + to.x) / 2, y: (from.y + to.y) / 2 };
    const via = {
      x: mid.x + (center.x - mid.x) * 0.35,
      y: mid.y + (center.y - mid.y) * 0.35,
    };
    const weight = scale(link.volume, largestLink);
    return [
      {
        id: link.id,
        coin: link.coin,
        exchange: link.exchange,
        from: { x: from.x, y: from.y },
        via,
        to: { x: to.x, y: to.y },
        width: (0.75 + 4.25 * weight) * zoom,
        weight,
      },
    ];
  });
  return { center, inner, outer, exchanges, coins, links };
}

/** The SVG path of a link. */
export function linkPath(link: OrbitLink): string {
  const f = (value: number) => value.toFixed(1);
  return `M${f(link.from.x)},${f(link.from.y)}Q${f(link.via.x)},${f(link.via.y)} ${f(link.to.x)},${f(link.to.y)}`;
}

/** A point `t` (0–1) of the way along a link, from its exchange end. */
export function pointOnLink(link: OrbitLink, t: number): OrbitPoint {
  const u = 1 - t;
  return {
    x: u * u * link.from.x + 2 * u * t * link.via.x + t * t * link.to.x,
    y: u * u * link.from.y + 2 * u * t * link.via.y + t * t * link.to.y,
  };
}
