// Transport liveness must not turn delayed market data into fresh evidence.
export function marketShareTransportHealth(
  now: number,
  openedAt: number,
  lastDataAt: number,
  lastTransportAt: number,
  resumeGraceUntil: number
) {
  const transportAt = lastTransportAt || openedAt;
  return {
    reconnect:
      transportAt > 0 && now - transportAt > 20_000 && now >= resumeGraceUntil,
    stale:
      lastDataAt > 0
        ? now - lastDataAt > 5_000
        : openedAt > 0 && now - openedAt > 20_000,
  };
}
