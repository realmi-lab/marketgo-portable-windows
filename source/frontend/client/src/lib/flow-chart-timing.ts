/** Official ticker frames arrive about every ten seconds, unlike trade ticks. */
export function flowChartTiming(sampleIntervalMS = 1000) {
  const interval = Number.isFinite(sampleIntervalMS) ? Math.max(1000, sampleIntervalMS) : 1000;
  return {
    delayMS: interval === 1000 ? 1000 : interval + 5000,
    aheadMS: Math.max(5000, interval * 2),
    dotFadeMS: Math.max(300, interval / 5),
  };
}
