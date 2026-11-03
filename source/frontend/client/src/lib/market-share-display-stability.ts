import { realtimeFlowTenSecondSample } from "./market-share-realtime-flow.ts";
import type {
  MarketShareOfficialDomestic24h,
  MarketShareOfficialTicker1mState,
  MarketShareRealtimeFlowState,
} from "./market-share-types.ts";

const DOMESTIC_EXCHANGE_IDS = new Set([
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
]);

export interface StableExchangeObservation {
  exchange: string;
  share_pct: number | null;
  volume_krw: number;
  trade_count: number;
  trade_count_share_pct: number | null;
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function normalizedTimestampMS(value: number): number {
  return value > 0 && value < 10_000_000_000 ? value * 1_000 : value;
}

export function officialDomesticSnapshotIsComplete(
  snapshot: MarketShareOfficialDomestic24h | null | undefined
): snapshot is MarketShareOfficialDomestic24h {
  return Boolean(
    snapshot?.status === "LIVE" &&
    snapshot.ready_exchange_count === snapshot.expected_exchange_count &&
    finite(snapshot.total_volume_krw) &&
    finite(snapshot.bithumb_volume_krw) &&
    finite(snapshot.bithumb_share_pct) &&
    snapshot.exchanges?.bithumb?.status === "LIVE" &&
    finite(snapshot.exchanges.bithumb.volume_krw)
  );
}

export function selectStableOfficialDomestic(
  current: MarketShareOfficialDomestic24h | null | undefined,
  retained: MarketShareOfficialDomestic24h
): MarketShareOfficialDomestic24h {
  if (!officialDomesticSnapshotIsComplete(current)) return retained;
  if (!officialDomesticSnapshotIsComplete(retained)) return current;
  return normalizedTimestampMS(current.as_of) >=
    normalizedTimestampMS(retained.as_of)
    ? current
    : retained;
}

export function officialTrendSnapshotIsComplete(
  snapshot: MarketShareOfficialTicker1mState | null | undefined
): snapshot is MarketShareOfficialTicker1mState {
  return Boolean(
    snapshot?.status === "LIVE" &&
    snapshot.previous &&
    snapshot.current &&
    finite(snapshot.previous.bithumb_share_pct) &&
    finite(snapshot.current.bithumb_share_pct) &&
    finite(snapshot.share_delta_pp) &&
    Array.isArray(snapshot.trend) &&
    snapshot.trend.length > 0
  );
}

export function selectStableOfficialTrend(
  current: MarketShareOfficialTicker1mState | null | undefined,
  retained: MarketShareOfficialTicker1mState | null
): MarketShareOfficialTicker1mState | null {
  if (!officialTrendSnapshotIsComplete(current))
    return retained ?? current ?? null;
  if (!officialTrendSnapshotIsComplete(retained)) return current;
  return current.generated_at_ms >= retained.generated_at_ms
    ? current
    : retained;
}

export function domesticExchangeFrameIsComplete(
  rows: readonly StableExchangeObservation[] | null | undefined,
  readyExchanges?: readonly string[]
): boolean {
  const observed = new Set(
    (rows ?? [])
      .map(row => row.exchange)
      .filter(exchange => DOMESTIC_EXCHANGE_IDS.has(exchange))
  );
  if (observed.size !== DOMESTIC_EXCHANGE_IDS.size) return false;
  if (!readyExchanges) return true;
  const ready = new Set(
    readyExchanges.filter(exchange => DOMESTIC_EXCHANGE_IDS.has(exchange))
  );
  return ready.size === DOMESTIC_EXCHANGE_IDS.size;
}

export function selectStableExchangeFrame(
  current: StableExchangeObservation[] | null | undefined,
  retained: StableExchangeObservation[],
  readyExchanges?: readonly string[],
  transportIsLive = true
): StableExchangeObservation[] {
  return transportIsLive &&
    domesticExchangeFrameIsComplete(current, readyExchanges)
    ? current!
    : retained.length
      ? retained
      : (current ?? []);
}

export function selectStableRealtimeFlowState(
  current: MarketShareRealtimeFlowState | null | undefined,
  retained: MarketShareRealtimeFlowState | null | undefined
): MarketShareRealtimeFlowState | null {
  const currentSample = realtimeFlowTenSecondSample(current);
  const retainedSample = realtimeFlowTenSecondSample(retained);
  if (currentSample && retainedSample) {
    return current!.as_of_ms >= retained!.as_of_ms ? current! : retained!;
  }
  if (currentSample) return current!;
  if (retainedSample) return retained!;
  return current ?? retained ?? null;
}
