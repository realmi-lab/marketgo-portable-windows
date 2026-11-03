import type {
  MarketShareOfficialMovementEvent,
  MarketShareOfficialTicker1mState,
} from "./market-share-types.ts";

export function selectOfficialContributions(
  events: MarketShareOfficialMovementEvent[],
  selectedID: string,
  trend: MarketShareOfficialTicker1mState | null
) {
  const selected = events.find(event => event.event_id === selectedID) ?? null;
  const ready = trend?.status === "LIVE" && trend.previous && trend.current;
  return {
    selected,
    drivers: selected?.drivers ?? (ready ? trend.drivers : []),
    exchangeRows:
      selected?.exchange_drivers ?? (ready ? trend.exchange_drivers : []),
  };
}
