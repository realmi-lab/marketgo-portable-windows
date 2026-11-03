import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";

import {
  lonePointsPath,
  smoothAreaPath,
  smoothLinePath,
} from "@/lib/chart-curves";
import { chartLocalPoint, keyedIndex, tipPosition } from "@/lib/chart-interaction";
import { stableDomain } from "@/lib/live-chart";
import { flowChartTiming } from "@/lib/flow-chart-timing";
import { flowChartTimeTickStep } from "@/lib/flow-chart-time-axis";
import { labelledIndexes, placedLabels } from "@/lib/chart-labels";
import { exchangeLabel } from "@/lib/exchanges";
import { formatSharePercent } from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import { useMediaQuery } from "@/hooks/useMediaQuery";
import type { LiveChartExplorer } from "@/hooks/useLiveChartExplorer";
import { bucketLabel, bucketTick } from "@/lib/screen-copy";
import { exchangeColor, Legend, useHiddenSeries } from "./ui";

/*
 * SVG charts drawn at the container's real width (no stretched text). They
 * share one way of reading values: a pointer, a tap or the arrow keys pick a
 * point, a guide marks it and a tooltip lists its values. Every value comes
 * from the server; the charts only place it.
 */

/** The element's current width, at least `minimum`, for drawing at real size. */
export function useWidth<T extends HTMLElement>(fallback = 640, minimum = 240) {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const element = ref.current;
    if (!element) return;
    const update = () => setWidth(Math.max(minimum, element.clientWidth));
    update();
    const observer = new ResizeObserver(update);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  return { ref, width };
}

const isNumber = (value: unknown): value is number =>
  typeof value === "number" && Number.isFinite(value);

/** How a chart answers a pointer or the arrow keys. */
interface Pick {
  /** Points the arrow keys step through, in order. */
  count: number;
  /** The point nearest a position in the chart, or null. */
  nearest: (x: number, y: number) => number | null;
  /** Where a point's tooltip goes. */
  anchor: (index: number) => { x: number; y: number };
  /** The vertical extent of the guide line; no guide when absent. */
  guide?: { top: number; bottom: number };
}

/** The index in ascending `xs` nearest to `x`. */
function nearestX(xs: readonly number[], x: number): number | null {
  if (xs.length === 0) return null;
  let low = 0;
  let high = xs.length - 1;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (xs[middle] < x) low = middle + 1;
    else high = middle;
  }
  return low > 0 && Math.abs(xs[low - 1] - x) <= Math.abs(xs[low] - x)
    ? low - 1
    : low;
}

/**
 * The point a picked key stands for. A moment that has left the chart falls
 * back to the nearest one still shown, so the keys step on from there.
 */
function pickedIndex(
  keys: readonly (string | number)[],
  picked: string | number | null
): number | null {
  if (picked === null) return null;
  const index = keys.indexOf(picked);
  if (index >= 0) return index;
  return typeof picked === "number" && typeof keys[0] === "number"
    ? nearestX(keys as readonly number[], picked)
    : null;
}

/**
 * The frame every chart draws in. It measures the width, follows the pointer
 * and the keys, draws the guide and places the tooltip on the side with room.
 */
function Plot({
  label,
  height,
  keys,
  draw,
  tip,
  onChoose,
  small = false,
  layer,
  activeKey,
  onActiveChange,
  onNavigate,
}: {
  label: string;
  height: number;
  /**
   * A stable key per point (its time, its coin), so a picked point stays on
   * the same moment while newer points arrive. Times run in ascending order.
   */
  keys: readonly (string | number)[];
  draw: (
    width: number,
    active: number | null
  ) => { content: ReactNode; pick?: Pick };
  tip?: (index: number) => ReactNode;
  /** A click, a tap or Enter on a point. */
  onChoose?: (index: number) => void;
  /**
   * A chart too small to hold its tooltip (a coin tile's line): it takes
   * any width and puts the tooltip above itself, from its left edge.
   */
  small?: boolean;
  /**
   * HTML drawn behind the chart at its size, such as a line the browser
   * moves on its own (LiveLine).
   */
  layer?: ReactNode;
  activeKey?: string | number | null;
  onActiveChange?: (key: string | number | null) => void;
  onNavigate?: (key: string) => boolean;
}) {
  const { ref, width } = useWidth<HTMLDivElement>(640, small ? 0 : 240);
  const [picked, setPicked] = useState<string | number | null>(null);
  const pickAt = (index: number | null) => {
    const key = index === null ? null : (keys[index] ?? null);
    setPicked(key);
    onActiveChange?.(key);
  };
  // A point reached by the keys is read aloud once, as its tooltip read
  // then; a moving pointer and later updates are not read.
  const [keyMoves, setKeyMoves] = useState(0);
  const [spoken, setSpoken] = useState("");
  const tipRef = useRef<HTMLDivElement>(null);
  const active = pickedIndex(
    keys,
    activeKey === undefined ? picked : activeKey
  );
  const { content, pick } = draw(width, active);
  const shown = pick && active !== null && active < pick.count ? active : null;
  const anchor = shown === null ? null : pick!.anchor(shown);
  useEffect(() => {
    if (keyMoves > 0) setSpoken(tipRef.current?.innerText ?? "");
  }, [keyMoves]);
  // The tooltip is measured from the left edge, so its last spot does not
  // squeeze it, then placed beside its point inside the chart (above a
  // small one).
  useLayoutEffect(() => {
    const element = tipRef.current;
    if (!element || !anchor) return;
    element.style.left = "0px";
    element.style.top = "0px";
    const { left, top } = small
      ? { left: 0, top: -element.offsetHeight - 4 }
      : tipPosition(
          anchor,
          { width: element.offsetWidth, height: element.offsetHeight },
          { width, height }
        );
    element.style.left = `${left}px`;
    element.style.top = `${top}px`;
  });
  const locate = (event: {
    clientX: number;
    clientY: number;
    currentTarget: HTMLElement;
  }) => {
    const box = event.currentTarget.getBoundingClientRect();
    const point = chartLocalPoint(event.clientX, event.clientY, box, { width, height });
    return point ? pick?.nearest(point.x, point.y) ?? null : null;
  };
  return (
    <div
      ref={ref}
      className="mg-plot"
      data-small={small ? "" : undefined}
      role={pick ? "group" : "img"}
      aria-label={label}
      tabIndex={pick ? 0 : undefined}
      data-choosable={onChoose ? "" : undefined}
      onPointerMove={event => {
        if (pick) pickAt(locate(event));
      }}
      onPointerLeave={event => {
        // A touch keeps its tooltip until the reader taps elsewhere.
        if (event.pointerType === "mouse") pickAt(null);
      }}
      onClick={event => {
        const point = locate(event);
        if (point === null) return;
        pickAt(point);
        onChoose?.(point);
      }}
      onKeyDown={event => {
        if (onNavigate?.(event.key)) {
          event.preventDefault();
          event.stopPropagation();
          return;
        }
        if (event.key === "Escape") {
          pickAt(null);
          return;
        }
        if ((event.key === "Enter" || event.key === " ") && shown !== null) {
          event.preventDefault();
          onChoose?.(shown);
          return;
        }
        const next = pick ? keyedIndex(event.key, shown, pick.count) : null;
        if (next === null) return;
        event.preventDefault();
        pickAt(next);
        setKeyMoves(count => count + 1);
      }}
      onBlur={() => pickAt(null)}
    >
      {layer}
      <svg
        className="mg-chart"
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        fontSize={12}
        aria-hidden="true"
      >
        {content}
        {anchor && pick?.guide ? (
          <line
            className="mg-guide"
            x1={anchor.x}
            x2={anchor.x}
            y1={pick.guide.top}
            y2={pick.guide.bottom}
          />
        ) : null}
      </svg>
      {anchor && tip && shown !== null ? (
        <div ref={tipRef} className="mg-tip">
          {tip(shown)}
        </div>
      ) : null}
      <p className="mg-sr-only" aria-live="polite">
        {spoken}
      </p>
    </div>
  );
}

/** A moment marked on a chart, such as a notice, and what it was. */
export interface ChartMark {
  ms: number;
  color: string;
  /** Its time and source in a few words: "21:27 빗썸". */
  label: string;
  /** Its full text, wrapped in the tooltip, never cut. */
  title: string;
}

/** How many marks a tooltip lists before counting the rest. */
const TIP_MARK_LIMIT = 4;

/** A tooltip: a heading, rows of label and value, and marks under them. */
function TipRows({
  title,
  rows,
  marks = [],
  hint,
}: {
  title: string;
  rows: ReadonlyArray<{ label: string; value: string; color?: string }>;
  marks?: readonly ChartMark[];
  hint?: string;
}) {
  return (
    <>
      <b className="mg-tip-title">{title}</b>
      <dl>
        {rows.map(row => (
          <div key={row.label}>
            <dt>
              {row.color ? (
                <i
                  className="mg-swatch"
                  style={{ background: row.color }}
                  aria-hidden="true"
                />
              ) : null}
              {row.label}
            </dt>
            <dd className="mg-num">{row.value}</dd>
          </div>
        ))}
      </dl>
      {marks.length ? (
        <ul className="mg-tip-marks">
          {marks.slice(0, TIP_MARK_LIMIT).map(mark => (
            <li key={`${mark.ms}:${mark.title}`}>
              <span className="mg-tip-mark-label">
                <i
                  className="mg-swatch"
                  style={{ background: mark.color }}
                  aria-hidden="true"
                />
                {mark.label}
              </span>
              {mark.title}
            </li>
          ))}
          {marks.length > TIP_MARK_LIMIT ? (
            <li className="mg-tip-mark-more">
              외 {marks.length - TIP_MARK_LIMIT}건
            </li>
          ) : null}
        </ul>
      ) : null}
      {hint ? <p className="mg-tip-hint">{hint}</p> : null}
    </>
  );
}

/** An empty chart keeps the chart's height, so its card does not move. */
function ChartEmpty({
  height,
  children,
}: {
  height: number;
  children: ReactNode;
}) {
  return (
    <p className="mg-empty mg-chart-empty" style={{ height }}>
      {children}
    </p>
  );
}

/** A line path through `points`, broken where a value is missing. */
function linePath(
  points: ReadonlyArray<{ x: number; y: number | null }>
): string {
  return points
    .map((point, index) => {
      if (point.y === null) return "";
      const move = index === 0 || points[index - 1].y === null ? "M" : "L";
      return `${move}${point.x.toFixed(1)},${point.y.toFixed(1)}`;
    })
    .join("");
}

export interface HourPoint {
  bucket_ms: number;
  share: number | null;
  /** The share is of the part of the bucket observed (관측범위기준). */
  partial?: boolean;
}

/**
 * Official hourly share with each hour's value written on the line. A shaded
 * span marks the server's rising or falling phase; choosing an hour (click,
 * tap or Enter) selects it.
 */
export function HourlyShareChart({
  points,
  phase,
  marks = [],
  selectedMS,
  onSelect,
  label,
  height = 248,
  bucketMS = 3_600_000,
}: {
  points: readonly HourPoint[];
  phase?: { fromMS: number; toMS: number; direction: string } | null;
  /** Moments to mark with a thin vertical line, e.g. notices. */
  marks?: readonly ChartMark[];
  selectedMS?: number | null;
  onSelect?: (bucketMS: number) => void;
  label: string;
  height?: number;
  /** How long each point's bucket is: an hour unless said otherwise. */
  bucketMS?: number;
}) {
  const gradient = useId();
  const values = points.map(point => point.share);
  const present = values.filter(isNumber);
  if (points.length < 2 || present.length === 0) {
    return (
      <ChartEmpty height={height}>점유율 자료가 아직 없습니다.</ChartEmpty>
    );
  }
  const low = Math.floor(Math.min(...present) - 0.5);
  const high = Math.ceil(Math.max(...present) + 0.5);
  const first = points[0].bucket_ms;
  const last = points[points.length - 1].bucket_ms;
  const selected = points.findIndex(point => point.bucket_ms === selectedMS);
  return (
    <Plot
      label={label}
      height={height}
      keys={points.map(point => point.bucket_ms)}
      onChoose={
        onSelect ? index => onSelect(points[index].bucket_ms) : undefined
      }
      tip={index => {
        const point = points[index];
        const noted = marks.filter(
          mark =>
            mark.ms >= point.bucket_ms && mark.ms < point.bucket_ms + bucketMS
        );
        return (
          <TipRows
            title={bucketLabel(point.bucket_ms, bucketMS)}
            rows={[
              { label: "빗썸 점유율", value: formatSharePercent(point.share) },
              ...(point.partial
                ? [{ label: "관측", value: "일부만 관측(관측범위기준)" }]
                : []),
            ]}
            marks={noted}
            hint={onSelect ? "누르면 이 시각의 1시간 기여를 봅니다" : undefined}
          />
        );
      }}
      draw={(width, active) => {
        const left = 44;
        const right = 20;
        const top = 28;
        const bottom = 28;
        const plotWidth = width - left - right;
        const x = (ms: number) =>
          left + ((ms - first) / Math.max(1, last - first)) * plotWidth;
        const y = (value: number) =>
          top +
          ((high - value) / Math.max(0.01, high - low)) *
            (height - top - bottom);
        const xs = points.map(point => x(point.bucket_ms));
        const placed = points.map((point, index) => ({
          x: xs[index],
          y: point.share === null ? null : y(point.share),
        }));
        const line = linePath(placed);
        // The area under each unbroken run of the line.
        const area = line
          .split("M")
          .filter(Boolean)
          .map(run => {
            const coords = run.split("L");
            const start = coords[0].split(",")[0];
            const end = coords[coords.length - 1].split(",")[0];
            return `M${start},${height - bottom}L${run}L${end},${height - bottom}Z`;
          })
          .join("");
        const spacing = plotWidth / Math.max(1, points.length - 1);
        // A value sits above its point, or below it near the top edge. It is
        // centred on the point, or drawn inward where it would cross an edge.
        const labelY = (share: number) =>
          y(share) - 10 < 12 ? y(share) + 18 : y(share) - 10;
        // About 7.5px a character for the 12px semibold labels.
        const labelSize = (index: number) =>
          formatSharePercent(values[index]).length * 7.5;
        const anchorOf = (index: number) =>
          xs[index] - labelSize(index) / 2 < left
            ? "start"
            : xs[index] + labelSize(index) / 2 > width - right
              ? "end"
              : "middle";
        const labelled = labelledIndexes(
          values,
          index => {
            const size = labelSize(index);
            const anchor = anchorOf(index);
            const from =
              anchor === "start"
                ? xs[index]
                : anchor === "end"
                  ? xs[index] - size
                  : xs[index] - size / 2;
            const baseline = labelY(values[index] as number);
            return {
              left: from,
              right: from + size,
              top: baseline - 12,
              bottom: baseline + 3,
            };
          },
          { left, right: width - right },
          selected >= 0 ? selected : null
        );
        const hourEvery = Math.max(1, Math.ceil(56 / spacing));
        const ticks = [low, (low + high) / 2, high];
        return {
          content: (
            <>
              <defs>
                <linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1">
                  <stop
                    offset="0%"
                    stopColor="var(--exchange-bithumb)"
                    stopOpacity={0.28}
                  />
                  <stop
                    offset="100%"
                    stopColor="var(--exchange-bithumb)"
                    stopOpacity={0}
                  />
                </linearGradient>
              </defs>
              {phase ? (
                <rect
                  x={x(Math.max(first, phase.fromMS))}
                  y={top}
                  width={Math.max(
                    2,
                    x(Math.min(last, phase.toMS)) -
                      x(Math.max(first, phase.fromMS))
                  )}
                  height={height - top - bottom}
                  fill={
                    phase.direction === "DOWN" ? "var(--loss)" : "var(--profit)"
                  }
                  opacity={0.1}
                />
              ) : null}
              {ticks.map(tick => (
                <g key={tick}>
                  <line
                    x1={left}
                    x2={width - right}
                    y1={y(tick)}
                    y2={y(tick)}
                    stroke="var(--grid-stroke)"
                  />
                  <text x={left - 8} y={y(tick) + 4} textAnchor="end">
                    {tick.toFixed(1)}%
                  </text>
                </g>
              ))}
              {marks
                .filter(mark => mark.ms >= first && mark.ms <= last)
                .map(mark => (
                  <line
                    key={`${mark.ms}:${mark.title}`}
                    x1={x(mark.ms)}
                    x2={x(mark.ms)}
                    y1={top}
                    y2={height - bottom}
                    stroke={mark.color}
                    strokeWidth={1.5}
                    strokeDasharray="3 3"
                    opacity={0.8}
                  />
                ))}
              <path d={area} fill={`url(#${gradient})`} />
              <path
                d={line}
                fill="none"
                stroke="var(--exchange-bithumb)"
                strokeWidth={2}
                strokeLinejoin="round"
              />
              {points.map((point, index) =>
                point.share === null ? null : (
                  <circle
                    key={point.bucket_ms}
                    cx={xs[index]}
                    cy={y(point.share)}
                    r={index === selected ? 5.5 : index === active ? 4.5 : 2.5}
                    className={index === selected ? "mg-dot-chosen" : undefined}
                    // A partly observed bucket is a hollow dot.
                    fill={
                      point.partial ? "var(--card)" : "var(--exchange-bithumb)"
                    }
                    stroke={
                      point.partial ? "var(--exchange-bithumb)" : undefined
                    }
                    strokeWidth={point.partial ? 1.5 : undefined}
                  />
                )
              )}
              {points.map((point, index) =>
                point.share === null || !labelled.has(index) ? null : (
                  <text
                    key={`v${point.bucket_ms}`}
                    className="mg-value-label"
                    data-strong={index === selected ? "" : undefined}
                    x={xs[index]}
                    y={labelY(point.share)}
                    textAnchor={anchorOf(index)}
                  >
                    {formatSharePercent(point.share)}
                  </text>
                )
              )}
              {points.map((point, index) =>
                (points.length - 1 - index) % hourEvery === 0 ? (
                  <text
                    key={`h${point.bucket_ms}`}
                    x={xs[index]}
                    y={height - 8}
                    textAnchor="middle"
                  >
                    {bucketTick(point.bucket_ms, bucketMS)}
                  </text>
                ) : null
              )}
            </>
          ),
          pick: {
            count: points.length,
            nearest: px => nearestX(xs, px),
            anchor: index => ({
              x: xs[index],
              y: placed[index].y ?? top,
            }),
            guide: { top, bottom: height - bottom },
          },
        };
      }}
    />
  );
}

export interface SecondPoint {
  as_of_ms: number;
  /** Bithumb's one-second share; null leaves a gap in the line. */
  share?: number | null;
  /** Each exchange's value for that second; absent when it is incomplete. */
  values: Record<string, number> | undefined;
}

/**
 * The last minute second by second: stacked bars of each exchange's value
 * and, when given, a line of Bithumb's one-second share. A new second slides
 * in from the right; the legend turns exchanges on and off.
 */
export function SecondFlowChart({
  points,
  exchanges,
  label,
  formatValue,
  height = 200,
}: {
  points: readonly SecondPoint[];
  exchanges: readonly string[];
  label: string;
  /** How one exchange's value reads in the tooltip. */
  formatValue: (value: number) => string;
  height?: number;
}) {
  const { hidden, toggle } = useHiddenSeries();
  const hasShare = points.some(point => isNumber(point.share));
  const legend = (
    <Legend
      items={[
        ...exchanges.map(exchange => ({
          key: exchange,
          label: exchangeLabel(exchange),
          color: exchangeColor(exchange),
        })),
        ...(hasShare
          ? [
              {
                key: "share",
                label: "빗썸 1초 점유율",
                color: "var(--foreground)",
              },
            ]
          : []),
      ]}
      hidden={hidden}
      onToggle={toggle}
    />
  );
  if (points.length === 0) {
    return (
      <>
        <ChartEmpty height={height}>
          1초 자료를 아직 받지 못했습니다.
        </ChartEmpty>
        {legend}
      </>
    );
  }
  const shown = exchanges.filter(exchange => !hidden.has(exchange));
  const withShare = hasShare && !hidden.has("share");
  // The bar scale is for drawing only; no value is derived from it.
  const totals = points.map(point =>
    shown.reduce((sum, exchange) => sum + (point.values?.[exchange] ?? 0), 0)
  );
  const peak = Math.max(1, ...totals);
  const newest = points[points.length - 1].as_of_ms;
  return (
    <>
      <Plot
        label={label}
        height={height}
        keys={points.map(point => point.as_of_ms)}
        tip={index => {
          const point = points[index];
          return (
            <TipRows
              title={formatMarketTimeMS(point.as_of_ms, "time-seconds")}
              rows={
                point.values
                  ? [
                      ...shown.map(exchange => ({
                        label: exchangeLabel(exchange),
                        color: exchangeColor(exchange),
                        value: isNumber(point.values?.[exchange])
                          ? formatValue(point.values![exchange])
                          : "—",
                      })),
                      ...(withShare
                        ? [
                            {
                              label: "빗썸 1초 점유율",
                              value: formatSharePercent(point.share),
                            },
                          ]
                        : []),
                    ]
                  : [{ label: "이 1초", value: "자료가 완전하지 않음" }]
              }
            />
          );
        }}
        draw={(width, active) => {
          const left = 8;
          const right = withShare ? 44 : 8;
          const top = 8;
          const bottom = 24;
          const plot = height - top - bottom;
          const slot = (width - left - right) / Math.max(1, points.length);
          const barWidth = Math.max(1, slot - 2);
          const middle = (index: number) => left + slot * index + slot / 2;
          const shareY = (share: number) => top + ((100 - share) / 100) * plot;
          const line = linePath(
            points.map((point, index) => ({
              x: middle(index),
              y: isNumber(point.share) ? shareY(point.share) : null,
            }))
          );
          return {
            content: (
              <>
                {withShare
                  ? [0, 50, 100].map(tick => (
                      <g key={tick}>
                        <line
                          x1={left}
                          x2={width - right}
                          y1={shareY(tick)}
                          y2={shareY(tick)}
                          stroke="var(--grid-stroke)"
                        />
                        <text x={width - right + 6} y={shareY(tick) + 4}>
                          {tick}%
                        </text>
                      </g>
                    ))
                  : null}
                {/* A new key restarts the slide each time a second arrives. */}
                <g
                  key={newest}
                  className="mg-slide"
                  style={{ "--mg-slide": `${slot}px` } as CSSProperties}
                >
                  {points.map((point, index) => {
                    let base = top + plot;
                    return (
                      <g
                        key={point.as_of_ms}
                        opacity={active === null || active === index ? 1 : 0.55}
                      >
                        {shown.map(exchange => {
                          const value = point.values?.[exchange] ?? 0;
                          if (value <= 0) return null;
                          const barHeight = (value / peak) * plot;
                          base -= barHeight;
                          return (
                            <rect
                              key={exchange}
                              x={left + slot * index + 1}
                              y={base}
                              width={barWidth}
                              height={barHeight}
                              fill={exchangeColor(exchange)}
                              opacity={0.75}
                            />
                          );
                        })}
                      </g>
                    );
                  })}
                  {withShare ? (
                    <path
                      d={line}
                      fill="none"
                      stroke="var(--foreground)"
                      strokeWidth={1.5}
                      strokeLinejoin="round"
                    />
                  ) : null}
                </g>
                <text x={left} y={height - 6}>
                  {formatMarketTimeMS(points[0].as_of_ms, "time-seconds")}
                </text>
                <text x={width - right} y={height - 6} textAnchor="end">
                  {formatMarketTimeMS(newest, "time-seconds")}
                </text>
              </>
            ),
            pick: {
              count: points.length,
              nearest: px =>
                Math.min(
                  points.length - 1,
                  Math.max(0, Math.floor((px - left) / slot))
                ),
              anchor: index => ({ x: middle(index), y: top + plot / 3 }),
              guide: { top, bottom: top + plot },
            },
          };
        }}
      />
      {legend}
    </>
  );
}

/** A share as a bar with a marker at a reference share (e.g. official 24h). */
export function ShareBar({
  share,
  reference,
  color = "var(--exchange-bithumb)",
  max = 60,
}: {
  share: number | null;
  reference?: number | null;
  color?: string;
  max?: number;
}) {
  const scale = (value: number) => `${Math.min(100, (value / max) * 100)}%`;
  return (
    <div className="mg-bar" aria-hidden="true">
      {typeof share === "number" ? (
        <span
          style={{
            left: 0,
            width: scale(share),
            background: color,
            opacity: 0.85,
          }}
        />
      ) : null}
      {typeof reference === "number" ? (
        <span
          style={{
            left: scale(reference),
            width: 2,
            background: "var(--foreground)",
          }}
        />
      ) : null}
    </div>
  );
}

/** A centred bar for a signed value: right of the middle is up, left is down. */
export function DeltaBar({
  value,
  max,
}: {
  value: number | null;
  max: number;
}) {
  if (typeof value !== "number" || max <= 0)
    return <div className="mg-bar" aria-hidden="true" />;
  const width = `${Math.min(50, (Math.abs(value) / max) * 50)}%`;
  return (
    <div className="mg-bar" aria-hidden="true">
      <span
        style={{
          left: value >= 0 ? "50%" : undefined,
          right: value < 0 ? "50%" : undefined,
          width,
          background: value >= 0 ? "var(--profit)" : "var(--loss)",
        }}
      />
      <span
        style={{ left: "50%", width: 1, background: "var(--muted-foreground)" }}
      />
    </div>
  );
}

export interface LineSeries {
  key: string;
  label: string;
  color: string;
  dashed?: boolean;
  points: ReadonlyArray<{ t: number; v: number | null }>;
}

/** Several percentage lines over one time axis, each one switchable. */
export function LinesChart({
  series,
  label,
  height = 220,
}: {
  series: readonly LineSeries[];
  label: string;
  height?: number;
}) {
  const { hidden, toggle } = useHiddenSeries();
  const legend = <Legend items={series} hidden={hidden} onToggle={toggle} />;
  const visible = series.filter(line => !hidden.has(line.key));
  const values = visible.flatMap(line =>
    line.points.map(point => point.v).filter(isNumber)
  );
  const times = [
    ...new Set(series.flatMap(line => line.points.map(point => point.t))),
  ].sort((left, right) => left - right);
  if (values.length === 0 || times.length < 2) {
    return (
      <>
        <ChartEmpty height={height}>그릴 기록이 아직 없습니다.</ChartEmpty>
        {legend}
      </>
    );
  }
  const low = Math.floor(Math.min(...values) - 1);
  const high = Math.ceil(Math.max(...values) + 1);
  const first = times[0];
  const last = times[times.length - 1];
  const valueAt = (line: LineSeries, t: number) =>
    line.points.find(point => point.t === t)?.v ?? null;
  return (
    <>
      <Plot
        label={label}
        height={height}
        keys={times}
        tip={index => (
          <TipRows
            title={formatMarketTimeMS(times[index], "time-seconds")}
            rows={visible.map(line => ({
              label: line.label,
              color: line.color,
              value: formatSharePercent(valueAt(line, times[index])),
            }))}
          />
        )}
        draw={(width, active) => {
          const left = 44;
          const right = 12;
          const top = 10;
          const bottom = 24;
          const x = (t: number) =>
            left +
            ((t - first) / Math.max(1, last - first)) * (width - left - right);
          const y = (v: number) =>
            top +
            ((high - v) / Math.max(0.01, high - low)) * (height - top - bottom);
          const xs = times.map(x);
          return {
            content: (
              <>
                {[low, (low + high) / 2, high].map(tick => (
                  <g key={tick}>
                    <line
                      x1={left}
                      x2={width - right}
                      y1={y(tick)}
                      y2={y(tick)}
                      stroke="var(--grid-stroke)"
                    />
                    <text x={left - 8} y={y(tick) + 4} textAnchor="end">
                      {tick.toFixed(1)}%
                    </text>
                  </g>
                ))}
                {visible.map(line => (
                  <path
                    key={line.key}
                    d={linePath(
                      line.points.map(point => ({
                        x: x(point.t),
                        y: point.v === null ? null : y(point.v),
                      }))
                    )}
                    fill="none"
                    stroke={line.color}
                    strokeWidth={line.dashed ? 1.5 : 2}
                    strokeDasharray={line.dashed ? "4 3" : undefined}
                    strokeLinejoin="round"
                  />
                ))}
                {active !== null
                  ? visible.map(line => {
                      const value = valueAt(line, times[active]);
                      return value === null ? null : (
                        <circle
                          key={line.key}
                          cx={xs[active]}
                          cy={y(value)}
                          r={4}
                          fill={line.color}
                        />
                      );
                    })
                  : null}
                <text x={left} y={height - 6}>
                  {formatMarketTimeMS(first, "time")}
                </text>
                <text x={width - right} y={height - 6} textAnchor="end">
                  {formatMarketTimeMS(last, "time")}
                </text>
              </>
            ),
            pick: {
              count: times.length,
              nearest: px => nearestX(xs, px),
              anchor: index => ({ x: xs[index], y: top + 24 }),
              guide: { top, bottom: height - bottom },
            },
          };
        }}
      />
      {legend}
    </>
  );
}

/**
 * A small line of recent values, newest at the right end. With `minSpan` the
 * height stands for at least that range of values, so small moves stay small.
 */
export function Sparkline({
  values,
  color,
  height = 40,
  minSpan = 0,
}: {
  values: readonly (number | null)[];
  color: string;
  height?: number;
  minSpan?: number;
}) {
  const present = values.filter(isNumber);
  if (present.length < 2) {
    return <div className="mg-spark" style={{ height }} aria-hidden="true" />;
  }
  const low = Math.min(...present);
  const high = Math.max(...present);
  // The values sit in the middle of a range at least minSpan wide.
  const span = Math.max(high - low, minSpan);
  const top = (low + high + span) / 2;
  const width = 200;
  const x = (index: number) => (index / Math.max(1, values.length - 1)) * width;
  // A flat series runs through the middle rather than along the top.
  const y = (value: number) =>
    span < 1e-9 ? height / 2 : 4 + ((top - value) / span) * (height - 8);
  const last = values.length - 1;
  return (
    <svg
      className="mg-spark"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      style={{ height }}
      aria-hidden="true"
    >
      <path
        d={linePath(
          values.map((value, index) => ({
            x: x(index),
            y: isNumber(value) ? y(value) : null,
          }))
        )}
        fill="none"
        stroke={color}
        strokeWidth={1.75}
        vectorEffect="non-scaling-stroke"
        strokeLinejoin="round"
      />
      {isNumber(values[last]) ? (
        <circle
          className="mg-spark-head"
          cx={x(last)}
          cy={y(values[last] as number)}
          r={3}
          fill={color}
        />
      ) : null}
    </svg>
  );
}

/** The room, in px, a live line leaves at its right end (screens.css .mg-live-flow). */
const LIVE_LINE_END_ROOM = 12;

/**
 * A coin tile's live line (코인 인덱스 2), after Liveline's live charts: the
 * last `span` seconds, one point a second, as a smooth line over a fill down
 * to the bottom of a fixed scale, with faint lines at its bottom, middle and
 * top. Each new second slides in from the right and its point pulses once,
 * so the line rests when seconds stop coming. The line is an HTML layer,
 * drawn again once a second and slid by the browser in between. A pointer, a
 * tap or the arrow keys pick a second and a tooltip lists its values. A
 * second without a value stays a gap.
 */
export function LiveLine({
  label,
  seconds,
  values,
  rows,
  span,
  color,
  domain,
  height = 56,
}: {
  label: string;
  /** Each point's second (Unix seconds), one per second, oldest first. */
  seconds: readonly number[];
  values: readonly (number | null)[];
  /** The tooltip's rows for the point at an index. */
  rows: (index: number) => ReadonlyArray<{ label: string; value: string }>;
  /** The seconds the line spans; the newest point sits at the right end. */
  span: number;
  color: string;
  /** The values the height spans, bottom to top. */
  domain: readonly [number, number];
  height?: number;
}) {
  const gradient = useId();
  const present = values.filter(isNumber);
  const last = values.length - 1;
  const [low, high] = domain;
  const pad = 6;
  const y = (value: number) =>
    pad + ((high - value) / (high - low)) * (height - 2 * pad);
  // Across, the line counts seconds: the newest at span - 1, the layer's
  // right end. The layer stretches it to its width.
  const points = values.map((value, index) => ({
    x: span - 1 - (last - index),
    y: isNumber(value) ? y(value) : null,
  }));
  const newest = values[last];
  return (
    <Plot
      small
      label={label}
      height={height}
      keys={seconds}
      tip={index => (
        <TipRows
          title={formatMarketTimeMS(seconds[index] * 1000, "time-seconds")}
          rows={rows(index)}
        />
      )}
      layer={
        <>
          <svg
            className="mg-live-grid"
            width="100%"
            height={height}
            aria-hidden="true"
          >
            {[low, (low + high) / 2, high].map(value => (
              <line key={value} x1="0" x2="100%" y1={y(value)} y2={y(value)} />
            ))}
          </svg>
          {present.length ? (
            // Each new second flips data-odd, which slides the line in again
            // and pulses its point without rebuilding them.
            <div
              className="mg-live-flow"
              aria-hidden="true"
              data-odd={seconds[last] % 2 ? "" : undefined}
              style={
                { "--mg-slide": `calc(100% / ${span - 1})` } as CSSProperties
              }
            >
              <svg
                viewBox={`0 0 ${span - 1} ${height}`}
                preserveAspectRatio="none"
                width="100%"
                height={height}
              >
                <defs>
                  <linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1">
                    <stop offset="0%" stopColor={color} stopOpacity={0.28} />
                    <stop offset="100%" stopColor={color} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <path
                  d={smoothAreaPath(points, y(low))}
                  fill={`url(#${gradient})`}
                />
                <path
                  className="mg-live-line"
                  d={smoothLinePath(points)}
                  fill="none"
                  stroke={color}
                  strokeWidth={2}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  vectorEffect="non-scaling-stroke"
                />
                <path
                  d={lonePointsPath(points)}
                  fill="none"
                  stroke={color}
                  strokeWidth={5}
                  strokeLinecap="round"
                  vectorEffect="non-scaling-stroke"
                />
              </svg>
              {isNumber(newest) ? (
                <span
                  className="mg-live-dot"
                  style={{ top: y(newest), color }}
                />
              ) : null}
            </div>
          ) : null}
        </>
      }
      draw={(width, active) => {
        // The layer leaves room on the right for the newest point's pulse.
        const right = width - LIVE_LINE_END_ROOM;
        const step = right / Math.max(1, span - 1);
        const x = (index: number) => right - (last - index) * step;
        const xs = values.map((_, index) => x(index));
        const picked = active === null ? null : values[active];
        return {
          content:
            active !== null && isNumber(picked) ? (
              <circle
                className="mg-dot-chosen"
                cx={x(active)}
                cy={y(picked)}
                r={4}
                fill={color}
              />
            ) : null,
          pick: {
            count: values.length,
            nearest: px => nearestX(xs, px),
            anchor: index => ({ x: xs[index], y: 0 }),
            guide: { top: 0, bottom: height },
          },
        };
      }}
    />
  );
}

export interface FlowSeries {
  key: string;
  label: string;
  color: string;
  /** One value per time, null where there is none. */
  values: readonly (number | null)[];
}

const FLOW_RIGHT = 16;
const FLOW_TOP = 10;
const FLOW_BOTTOM = 26;

/**
 * Lines that keep flowing, like a strip chart: the last `spanMs` scroll left
 * at the clock's pace, delayed according to the source cadence so each new
 * value slides in from the right edge instead of jumping. A dot rides each
 * line's right end. The scale moves only when the values leave it or shrink
 * well inside it (stableDomain). The motion holds while a pointer or the
 * keys read a value, while `running` is false (paused, disconnected) and for
 * readers who ask for reduced motion.
 */
export function FlowChart({
  label,
  times,
  series,
  format,
  axisFormat = format,
  zoom = false,
  running,
  spanMs = 120_000,
  height = 300,
  explorer,
  sampleIntervalMS,
  pauseOnHover = true,
}: {
  label: string;
  /** Each value's time (Unix ms), ascending. */
  times: readonly number[];
  series: readonly FlowSeries[];
  /** A value as the tooltip writes it, and the axis unless axisFormat. */
  format: (value: number) => string;
  axisFormat?: (value: number) => string;
  /** Fit the scale to the values (shares) instead of starting at 0. */
  zoom?: boolean;
  running: boolean;
  spanMs?: number;
  height?: number;
  explorer?: LiveChartExplorer;
  sampleIntervalMS?: number;
  pauseOnHover?: boolean;
}) {
  const { delayMS, aheadMS, dotFadeMS } = flowChartTiming(sampleIntervalMS);
  const reduced = useMediaQuery("(prefers-reduced-motion: reduce)");
  const { ref: chartRef, width: chartWidth } = useWidth<HTMLDivElement>();
  const newest = times.at(-1) ?? 0;
  // The server's clock, as this browser's clock plus an offset measured when
  // each newest value arrives.
  const offset = useRef({ newest: 0, ms: 0 });
  if (offset.current.newest !== newest) {
    offset.current = { newest, ms: newest - Date.now() };
  }
  const [held, setHeld] = useState<number | null>(null);
  const shownAt = () =>
    held ??
    (running && !reduced
      ? Date.now() + offset.current.ms - delayMS
      : newest);
  const scale = useRef<[number, number] | null>(null);
  scale.current = stableDomain(
    scale.current,
    series.flatMap(line => line.values.filter(isNumber)),
    zoom
  );
  const [low, high] = scale.current;
  const levels = [low, (low + high) / 2, high];
  const axisLabels = levels.map(axisFormat);
  // Room on the left for the longest scale label, about 7.5px a character.
  const left = Math.max(
    40,
    Math.ceil(Math.max(...axisLabels.map(text => text.length)) * 7.5) + 14
  );
  const y = (value: number) =>
    FLOW_TOP +
    ((high - value) / Math.max(1e-9, high - low)) *
      (height - FLOW_TOP - FLOW_BOTTOM);
  // Values that have scrolled out are left out, a little past the left edge.
  const from = times.findIndex(time => time >= newest - spanMs - 5_000);
  const kept = from < 0 ? 0 : from;
  // The lines' layer and the times' layer, moved together.
  const moving = useRef<(HTMLDivElement | null)[]>([]);
  const dots = useRef<(HTMLSpanElement | null)[]>([]);
  // The browser moves the lines and dots on its own between values (the
  // Web Animations API, off the main thread for the lines). Each render
  // starts the motion again from where it is, before the browser paints, so
  // redrawn lines and their place show together.
  useLayoutEffect(() => {
    const at = shownAt();
    const flowing = running && !reduced && held === null;
    const started: Animation[] = [];
    const place = (time: number) =>
      `translateX(${((newest - time) / spanMs) * 100}%)`;
    for (const layer of moving.current) {
      if (!layer) continue;
      layer.style.transform = place(at);
      if (flowing) {
        started.push(
          layer.animate(
            [
              { transform: place(at) },
              { transform: place(at + aheadMS) },
            ],
            { duration: aheadMS, easing: "linear", fill: "forwards" }
          )
        );
      }
    }
    series.forEach((line, index) => {
      const dot = dots.current[index];
      if (!dot) return;
      const value = valueAt(times, line.values, at);
      dot.style.display = value === null ? "none" : "";
      if (value === null) return;
      dot.style.transform = `translateY(${y(value)}px)`;
      if (!flowing || newest <= at) return;
      // Through each value still ahead of the right edge, then out of
      // sight a moment after the newest, should no new value come.
      const duration = newest - at + dotFadeMS;
      const ahead = times.flatMap((time, timeIndex) => {
        const next = line.values[timeIndex];
        return time > at && isNumber(next)
          ? [
              {
                transform: `translateY(${y(next)}px)`,
                offset: (time - at) / duration,
              },
            ]
          : [];
      });
      const last = ahead.at(-1)?.transform ?? dot.style.transform;
      started.push(
        dot.animate(
          [
            { transform: dot.style.transform, opacity: 1, offset: 0 },
            ...ahead.map(frame => ({ ...frame, opacity: 1 })),
            { transform: last, opacity: 0, offset: 1 },
          ],
          { duration, easing: "linear", fill: "forwards" }
        )
      );
    });
    return () => started.forEach(animation => animation.cancel());
  });
  const tickStep = flowChartTimeTickStep(spanMs, chartWidth - left - FLOW_RIGHT);
  const ticks: number[] = [];
  for (
    let tick = Math.ceil((newest - spanMs - tickStep) / tickStep) * tickStep;
    tick <= newest + 2 * tickStep;
    tick += tickStep
  ) {
    ticks.push(tick);
  }
  const hold = () => setHeld(shownAt());
  if (series.every(line => !line.values.some(isNumber))) {
    return (
      <div ref={chartRef}>
        <ChartEmpty height={height}>그릴 값이 아직 없습니다.</ChartEmpty>
      </div>
    );
  }
  return (
    <div
      ref={chartRef}
      onPointerEnter={pauseOnHover ? hold : undefined}
      onPointerLeave={event => {
        if (event.pointerType === "mouse") setHeld(null);
      }}
      onFocus={pauseOnHover ? hold : undefined}
      onBlur={() => setHeld(null)}
    >
      <Plot
        label={label}
        height={height}
        keys={times}
        activeKey={explorer?.selectedAt}
        onActiveChange={
          explorer
            ? key => explorer.inspect(typeof key === "number" ? key : null)
            : undefined
        }
        onChoose={explorer ? index => explorer.pin(times[index]) : undefined}
        onNavigate={
          explorer
            ? key => {
                if (
                  ![
                    "ArrowLeft",
                    "ArrowRight",
                    "Home",
                    "End",
                    "Escape",
                  ].includes(key)
                )
                  return false;
                if (key === "Escape") {
                  setHeld(null);
                  explorer.follow();
                } else if (key === "Home" && times.length)
                  explorer.pin(times[0]);
                else if (key === "End" && times.length)
                  explorer.pin(times[times.length - 1]);
                else explorer.step(key === "ArrowLeft" ? -1 : 1);
                return true;
              }
            : undefined
        }
        tip={index => (
          <TipRows
            title={formatMarketTimeMS(times[index], "time-seconds")}
            rows={series.map(line => ({
              label: line.label,
              color: line.color,
              value: isNumber(line.values[index])
                ? format(line.values[index] as number)
                : "—",
            }))}
          />
        )}
        layer={
          <div
            className="mg-flow"
            style={{ left, right: FLOW_RIGHT, height }}
            aria-hidden="true"
          >
            <svg className="mg-flow-grid" width="100%" height={height}>
              {levels.map(value => (
                <line
                  key={value}
                  x1="0"
                  x2="100%"
                  y1={y(value)}
                  y2={y(value)}
                />
              ))}
            </svg>
            <div className="mg-flow-clip">
              <div
                ref={element => {
                  moving.current[0] = element;
                }}
                className="mg-flow-move"
              >
                <svg
                  viewBox={`${-spanMs} 0 ${spanMs} ${height}`}
                  preserveAspectRatio="none"
                  width="100%"
                  height={height}
                >
                  {series.map(line => {
                    const points = times.slice(kept).map((time, index) => {
                      const value = line.values[kept + index];
                      return {
                        x: time - newest,
                        y: isNumber(value) ? y(value) : null,
                      };
                    });
                    return (
                      <g key={line.key} stroke={line.color} fill="none">
                        <path
                          d={smoothLinePath(points)}
                          strokeWidth={2}
                          strokeLinejoin="round"
                          vectorEffect="non-scaling-stroke"
                        />
                        <path
                          d={lonePointsPath(points)}
                          strokeWidth={5}
                          strokeLinecap="round"
                          vectorEffect="non-scaling-stroke"
                        />
                      </g>
                    );
                  })}
                </svg>
              </div>
            </div>
            <div className="mg-flow-clip" data-times="">
              <div
                ref={element => {
                  moving.current[1] = element;
                }}
                className="mg-flow-move"
              >
                {ticks.map(tick => (
                  <span
                    key={tick}
                    className="mg-flow-time"
                    style={{
                      left: `${((tick - newest + spanMs) / spanMs) * 100}%`,
                    }}
                  >
                    {formatMarketTimeMS(tick, "time-seconds")}
                  </span>
                ))}
              </div>
            </div>
            {series.map((line, index) => (
              <span
                key={line.key}
                ref={element => {
                  dots.current[index] = element;
                }}
                className="mg-flow-dot"
                style={{ color: line.color }}
              />
            ))}
          </div>
        }
        draw={(width, active) => {
          const at = shownAt();
          const plotWidth = width - left - FLOW_RIGHT;
          const x = (time: number) =>
            left + plotWidth - ((at - time) / spanMs) * plotWidth;
          const xs = times.map(x);
          return {
            content: (
              <>
                {levels.map((value, index) => (
                  <text
                    key={value}
                    x={left - 8}
                    y={y(value) + 4}
                    textAnchor="end"
                  >
                    {axisLabels[index]}
                  </text>
                ))}
                {active !== null
                  ? series.map(line => {
                      const value = line.values[active];
                      return isNumber(value) ? (
                        <circle
                          key={line.key}
                          cx={xs[active]}
                          cy={y(value)}
                          r={4}
                          fill={line.color}
                        />
                      ) : null;
                    })
                  : null}
              </>
            ),
            pick: {
              count: times.length,
              nearest: px =>
                px < left || px > width - FLOW_RIGHT ? null : nearestX(xs, px),
              anchor: index => ({ x: xs[index], y: FLOW_TOP + 24 }),
              guide: { top: FLOW_TOP, bottom: height - FLOW_BOTTOM },
            },
          };
        }}
      />
    </div>
  );
}

/**
 * The value a line passes through at `at`, between the two values around it
 * (where the dot riding its end sits), or null in a gap or past its end.
 */
function valueAt(
  times: readonly number[],
  values: readonly (number | null)[],
  at: number
): number | null {
  const after = times.findIndex(time => time >= at);
  if (after < 0) return null; // the line ended before `at`
  const next = values[after];
  if (after === 0 || times[after] === at) return isNumber(next) ? next : null;
  const before = values[after - 1];
  if (!isNumber(before) || !isNumber(next)) return null;
  const share = (at - times[after - 1]) / (times[after] - times[after - 1]);
  return before + (next - before) * share;
}

/** Background for a heat-map cell: stronger colour for a larger share effect. */
export function heatStyle(value: number | null | undefined, max: number) {
  if (typeof value !== "number" || max <= 0 || value === 0) return undefined;
  const strength = Math.round(Math.min(1, Math.abs(value) / max) * 40) + 6;
  const color = value > 0 ? "var(--profit)" : "var(--loss)";
  return {
    background: `color-mix(in srgb, ${color} ${strength}%, transparent)`,
  };
}

export interface MapPoint {
  key: string;
  /** Rows of the coin's tooltip. */
  details: ReadonlyArray<{ label: string; value: string }>;
  x: number;
  y: number;
  /** Signed effect: colour by sign, radius by size. */
  value: number;
}

/** Coins placed by two server percentages; labels on the largest ones. */
export function CoinMap({
  points,
  xLabel,
  yLabel,
  label,
  height = 320,
}: {
  points: readonly MapPoint[];
  xLabel: string;
  yLabel: string;
  label: string;
  height?: number;
}) {
  if (points.length === 0)
    return <ChartEmpty height={height}>표시할 코인이 없습니다.</ChartEmpty>;
  const maxX = Math.max(1, ...points.map(point => point.x));
  const largest = Math.max(
    0.0001,
    ...points.map(point => Math.abs(point.value))
  );
  // Names for the eight largest effects, where they fit.
  const named = [...points.keys()]
    .sort(
      (left, right) =>
        Math.abs(points[right].value) - Math.abs(points[left].value)
    )
    .slice(0, 8);
  return (
    <Plot
      label={label}
      height={height}
      keys={points.map(point => point.key)}
      tip={index => (
        <TipRows title={points[index].key} rows={points[index].details} />
      )}
      draw={(width, active) => {
        // The largest circle's radius is kept free at every edge.
        const left = 44 + 17;
        const right = 16 + 17;
        const top = 16 + 17;
        const bottom = 36;
        // A square-root axis keeps the many small coins apart from the few large ones.
        const x = (value: number) =>
          left + Math.sqrt(Math.max(0, value) / maxX) * (width - left - right);
        const y = (value: number) =>
          top +
          ((100 - Math.min(100, Math.max(0, value))) / 100) *
            (height - top - bottom);
        const placed = points.map(point => ({
          x: x(point.x),
          y: y(point.y),
          r: 3 + Math.sqrt(Math.abs(point.value) / largest) * 14,
        }));
        // The point being read is named first, then the largest effects.
        const names = placedLabels(
          [...(active === null ? [] : [active]), ...named].map(index => ({
            key: points[index].key,
            ...placed[index],
          })),
          // Reserve the percentage-axis gutter even when a name flips left.
          { left: 44, right: width - 16 }
        );
        return {
          content: (
            <>
              {[0, 25, 50, 75, 100].map(tick => (
                <g key={tick}>
                  <line
                    x1={44}
                    x2={width - 16}
                    y1={y(tick)}
                    y2={y(tick)}
                    stroke="var(--grid-stroke)"
                  />
                  <text x={36} y={y(tick) + 4} textAnchor="end">
                    {tick}%
                  </text>
                </g>
              ))}
              <text x={44} y={height - 8}>
                {xLabel} →
              </text>
              <text x={50} y={12}>
                ↑ {yLabel}
              </text>
              {points.map((point, index) => {
                const { x: cx, y: cy, r } = placed[index];
                const color =
                  point.value >= 0 ? "var(--profit)" : "var(--loss)";
                return (
                  <g key={point.key}>
                    <circle
                      cx={cx}
                      cy={cy}
                      r={r}
                      fill={color}
                      fillOpacity={index === active ? 0.6 : 0.3}
                      stroke={color}
                      strokeWidth={index === active ? 2 : 1}
                    />
                    {names.has(point.key) ? (
                      <text
                        className="mg-value-label"
                        x={names.get(point.key)!.x}
                        y={cy + 4}
                        textAnchor={names.get(point.key)!.anchor}
                      >
                        {point.key}
                      </text>
                    ) : null}
                  </g>
                );
              })}
            </>
          ),
          pick: {
            count: points.length,
            nearest: (px, py) => {
              let best: number | null = null;
              let bestDistance = 28;
              placed.forEach((spot, index) => {
                const distance = Math.hypot(spot.x - px, spot.y - py) - spot.r;
                if (distance < bestDistance) {
                  best = index;
                  bestDistance = distance;
                }
              });
              return best;
            },
            anchor: index => placed[index],
          },
        };
      }}
    />
  );
}

export interface GapSpan {
  place: string;
  start: number;
  end: number;
  color: string;
  title: string;
}

/** One row per place over a time range; each gap is a coloured span. */
export function GapMap({
  rows,
  spans,
  fromSecond,
  toSecond,
  label,
}: {
  rows: ReadonlyArray<{ place: string; label: string }>;
  spans: readonly GapSpan[];
  fromSecond: number;
  toSecond: number;
  label: string;
}) {
  const rowHeight = 28;
  const height = rows.length * rowHeight + 28;
  const rowOf = new Map(rows.map((row, index) => [row.place, index]));
  const shown = spans.filter(span => rowOf.has(span.place));
  return (
    <Plot
      label={label}
      height={height}
      keys={shown.map(span => `${span.place}:${span.start}`)}
      tip={index => (
        <TipRows
          title={rows[rowOf.get(shown[index].place)!].label}
          rows={[{ label: "끊김", value: shown[index].title }]}
        />
      )}
      draw={(width, active) => {
        const left = 72;
        const right = 8;
        const x = (second: number) =>
          left +
          ((Math.min(toSecond, Math.max(fromSecond, second)) - fromSecond) /
            Math.max(1, toSecond - fromSecond)) *
            (width - left - right);
        return {
          content: (
            <>
              {rows.map((row, index) => (
                <g key={row.place}>
                  <text
                    x={0}
                    y={index * rowHeight + 18}
                    style={{ fill: "var(--foreground)" }}
                  >
                    {row.label}
                  </text>
                  <rect
                    x={left}
                    y={index * rowHeight + 6}
                    width={width - left - right}
                    height={16}
                    fill="var(--secondary)"
                    rx={4}
                  />
                </g>
              ))}
              {shown.map((span, index) => (
                <rect
                  key={`${span.place}:${span.start}:${index}`}
                  x={x(span.start)}
                  y={rowOf.get(span.place)! * rowHeight + 6}
                  width={Math.max(3, x(span.end) - x(span.start))}
                  height={16}
                  fill={span.color}
                  stroke={index === active ? "var(--foreground)" : undefined}
                  rx={2}
                />
              ))}
              {[0, 0.25, 0.5, 0.75, 1].map(step => {
                const second = fromSecond + step * (toSecond - fromSecond);
                return (
                  <text
                    key={step}
                    x={x(second)}
                    y={height - 6}
                    textAnchor={
                      step === 0 ? "start" : step === 1 ? "end" : "middle"
                    }
                  >
                    {formatMarketTimeMS(second * 1000, "time")}
                  </text>
                );
              })}
            </>
          ),
          pick: {
            count: shown.length,
            nearest: (px, py) => {
              const row = Math.floor(py / rowHeight);
              let best: number | null = null;
              let bestDistance = 24;
              shown.forEach((span, index) => {
                if (rowOf.get(span.place) !== row) return;
                const from = x(span.start);
                const to = Math.max(from + 3, x(span.end));
                const distance = px < from ? from - px : px > to ? px - to : 0;
                if (distance < bestDistance) {
                  best = index;
                  bestDistance = distance;
                }
              });
              return best;
            },
            anchor: index => ({
              x: (x(shown[index].start) + x(shown[index].end)) / 2,
              y: rowOf.get(shown[index].place)! * rowHeight + 14,
            }),
          },
        };
      }}
    />
  );
}
