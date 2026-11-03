export const SHARE_Y_AXIS_TICK_STEP = 0.5;

const MIN_SHARE_PCT = 0;
const MAX_SHARE_PCT = 100;
const MIN_DOMAIN_SPAN = SHARE_Y_AXIS_TICK_STEP * 2;
const TICKS_PER_PERCENT = 1 / SHARE_Y_AXIS_TICK_STEP;

export type ShareYAxis = {
  domain: [number, number];
  ticks: number[];
};

function clampShare(value: number): number {
  return Math.min(MAX_SHARE_PCT, Math.max(MIN_SHARE_PCT, value));
}

function roundToTick(value: number): number {
  return Math.round(value * TICKS_PER_PERCENT) / TICKS_PER_PERCENT;
}

function ticksBetween(start: number, end: number): number[] {
  const startTick = Math.round(start * TICKS_PER_PERCENT);
  const endTick = Math.round(end * TICKS_PER_PERCENT);
  return Array.from(
    { length: endTick - startTick + 1 },
    (_, index) => (startTick + index) / TICKS_PER_PERCENT
  );
}

/**
 * Builds a stable Recharts Y axis for percentage-share series.
 *
 * Finite inputs are constrained to the valid 0-100% range. The visible domain
 * includes at least one 0.5 percentage-point interval beyond each data edge
 * whenever the percentage boundary permits it. At 0% or 100%, the unavailable
 * padding is moved to the other side so the chart still spans at least 1%p.
 */
export function buildShareYAxis(
  values: ReadonlyArray<number | null | undefined>
): ShareYAxis {
  const finiteValues = values
    .filter(
      (value): value is number =>
        typeof value === "number" && Number.isFinite(value)
    )
    .map(clampShare);

  if (finiteValues.length === 0) {
    return {
      domain: [MIN_SHARE_PCT, MIN_DOMAIN_SPAN],
      ticks: ticksBetween(MIN_SHARE_PCT, MIN_DOMAIN_SPAN),
    };
  }

  const dataMin = Math.min(...finiteValues);
  const dataMax = Math.max(...finiteValues);
  let domainMin = clampShare(
    Math.floor((dataMin - SHARE_Y_AXIS_TICK_STEP) * TICKS_PER_PERCENT) /
      TICKS_PER_PERCENT
  );
  let domainMax = clampShare(
    Math.ceil((dataMax + SHARE_Y_AXIS_TICK_STEP) * TICKS_PER_PERCENT) /
      TICKS_PER_PERCENT
  );

  if (domainMax - domainMin < MIN_DOMAIN_SPAN) {
    if (domainMin === MIN_SHARE_PCT) {
      domainMax = MIN_DOMAIN_SPAN;
    } else if (domainMax === MAX_SHARE_PCT) {
      domainMin = MAX_SHARE_PCT - MIN_DOMAIN_SPAN;
    } else {
      domainMax = Math.min(MAX_SHARE_PCT, domainMin + MIN_DOMAIN_SPAN);
      domainMin = Math.max(MIN_SHARE_PCT, domainMax - MIN_DOMAIN_SPAN);
    }
  }

  domainMin = roundToTick(domainMin);
  domainMax = roundToTick(domainMax);

  return {
    domain: [domainMin, domainMax],
    ticks: ticksBetween(domainMin, domainMax),
  };
}
