import {
  Children,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useMediaQuery } from "@/hooks/useMediaQuery";
import { canonicalExchangeId, exchangeLabel } from "@/lib/exchanges";
import {
  formatKRW,
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { ANALYSIS_WINDOWS } from "@/lib/market-share-config";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type { Tone } from "@/lib/screen-copy";

/* Small building blocks shared by every dashboard screen. */

type Span = 3 | 4 | 5 | 6 | 7 | 8 | 9 | 12;

/**
 * A card's height. Cards keep their height while data arrives, updates or
 * the window changes; longer content scrolls inside the card. "fit" is for
 * cards whose content never changes length. On phones, where cards stack one
 * per row, every card is as tall as its content instead (screens.css).
 */
export type CardSize = "s" | "m" | "l" | "xl" | "fit";

export function Card({
  title,
  note,
  actions,
  span = 12,
  size = "m",
  id,
  children,
}: {
  title: string;
  note?: ReactNode;
  actions?: ReactNode;
  span?: Span;
  size?: CardSize;
  id?: string;
  children: ReactNode;
}) {
  const headingId = useId();
  return (
    <section
      className={`mg-card mg-span-${span}`}
      data-size={size}
      id={id}
      aria-labelledby={headingId}
    >
      <header className="mg-card-head">
        <div className="mg-card-title">
          <h2 id={headingId}>{title}</h2>
          {note ? (
            <p title={typeof note === "string" ? note : undefined}>{note}</p>
          ) : null}
        </div>
        {actions ? <div className="mg-card-actions">{actions}</div> : null}
      </header>
      <div className="mg-card-body">{children}</div>
    </section>
  );
}

export function Foot({ children }: { children: ReactNode }) {
  return <p className="mg-foot">{children}</p>;
}

type ReadState = { stale: boolean; error: string; receivedAtMS: number };

/**
 * That a card shows an earlier read because the latest one failed, and when
 * that read arrived; "" while the data is current.
 */
export function staleNoteText(read: ReadState): string {
  return read.stale
    ? `갱신하지 못해 ${formatMarketTimeMS(read.receivedAtMS, "time-seconds")}에 받은 값을 보입니다. ${read.error}`
    : "";
}

/** The stale-read note as a line of its own; nothing while data is current. */
export function StaleNote({ read }: { read: ReadState }) {
  const text = staleNoteText(read);
  if (!text) return null;
  return (
    <p className="mg-attn" role="status">
      {text}
    </p>
  );
}

/** A missing value says why it is missing; it is never shown as 0. */
export function Empty({ children }: { children: ReactNode }) {
  return <p className="mg-empty">{children}</p>;
}

/** A figure tile. `tag` names where the figure comes from. */
export function Kpi({
  label,
  value,
  detail,
  tag,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  tag?: string;
}) {
  return (
    <div className="mg-kpi">
      <span className="mg-kpi-label">
        {label}
        {tag ? <i className="mg-tag">{tag}</i> : null}
      </span>
      <strong className="mg-num">{value}</strong>
      {detail ? <em>{detail}</em> : null}
    </div>
  );
}

/**
 * A live figure that briefly lights up green or red when the server sends a
 * higher or lower value, the way a trading screen marks a change.
 */
export function LiveValue({
  value,
  children,
}: {
  value: number | null | undefined;
  children: ReactNode;
}) {
  const previous = useRef(value);
  const [flash, setFlash] = useState<{ up: boolean; count: number } | null>(
    null
  );
  useEffect(() => {
    const before = previous.current;
    previous.current = value;
    if (typeof before !== "number" || typeof value !== "number") return;
    if (before === value) return;
    setFlash(current => ({
      up: value > before,
      count: (current?.count ?? 0) + 1,
    }));
  }, [value]);
  return (
    // A new key restarts the flash for every change.
    <span
      key={flash?.count ?? 0}
      className="mg-live"
      data-flash={flash ? (flash.up ? "up" : "down") : undefined}
    >
      {children}
    </span>
  );
}

export function Chip({
  tone = "muted",
  href,
  children,
}: {
  tone?: Tone;
  href?: string;
  children: ReactNode;
}) {
  return href ? (
    <a className="mg-chip" data-tone={tone} href={href}>
      {children}
    </a>
  ) : (
    <span className="mg-chip" data-tone={tone}>
      {children}
    </span>
  );
}

/** A signed share change; colour repeats what the sign already says. */
export function Pp({ value }: { value: number | null | undefined }) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return <span className="mg-num mg-muted">—</span>;
  }
  const tone = value > 0 ? "mg-up" : value < 0 ? "mg-down" : "";
  return (
    <span className={`mg-num ${tone}`}>
      {formatSignedPercentagePoints(value)}
    </span>
  );
}

export function Pct({ value }: { value: number | null | undefined }) {
  return <span className="mg-num">{formatSharePercent(value)}</span>;
}

export function Won({
  value,
  signed = false,
}: {
  value: number | null | undefined;
  signed?: boolean;
}) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return <span className="mg-num mg-muted">—</span>;
  }
  return (
    <span className="mg-num">
      {signed ? formatKrwCompact(value) : formatKRW(value)}
    </span>
  );
}

export function exchangeColor(exchange: string): string {
  return `var(--exchange-${canonicalExchangeId(exchange)})`;
}

export function Ex({ exchange }: { exchange: string }) {
  return (
    <span className="mg-ex">
      <i style={{ background: exchangeColor(exchange) }} aria-hidden="true" />
      {exchangeLabel(exchange)}
    </span>
  );
}

export interface LegendItem {
  key: string;
  label: string;
  color: string;
  /** Drawn as a dashed line, or as a thin upright mark like a bar's marker. */
  dashed?: boolean;
  tick?: boolean;
}

/** A chart's key. With `onToggle`, each item shows or hides its series. */
export function Legend({
  items,
  hidden,
  onToggle,
}: {
  items: readonly LegendItem[];
  hidden?: ReadonlySet<string>;
  onToggle?: (key: string) => void;
}) {
  return (
    <div
      className="mg-legend"
      role={onToggle ? "group" : undefined}
      aria-label={onToggle ? "그래프에 표시할 항목" : undefined}
    >
      {items.map(item => {
        const swatch = (
          <i
            className="mg-swatch"
            data-dashed={item.dashed ? "" : undefined}
            data-tick={item.tick ? "" : undefined}
            style={{ background: item.color, color: item.color }}
            aria-hidden="true"
          />
        );
        return onToggle ? (
          <button
            key={item.key}
            type="button"
            aria-pressed={!hidden?.has(item.key)}
            onClick={() => onToggle(item.key)}
          >
            {swatch}
            {item.label}
          </button>
        ) : (
          <span key={item.key}>
            {swatch}
            {item.label}
          </span>
        );
      })}
    </div>
  );
}

/** Series a reader turned off in a chart's legend. */
export function useHiddenSeries() {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());
  const toggle = (key: string) =>
    setHidden(current => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  return { hidden, toggle };
}

export interface SegOption<T> {
  value: T;
  label: string;
}

export function Seg<T extends string | number>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: readonly SegOption<T>[];
  value: T;
  onChange: (value: T) => void;
}) {
  return (
    <div className="mg-seg" role="group" aria-label={label}>
      {options.map(option => (
        <button
          key={String(option.value)}
          type="button"
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

/**
 * The analysis window picker. Phones, and card headers with little room
 * (`compact`), get a select instead of 12 buttons.
 */
export function WindowSeg({
  value,
  onChange,
  windows = ANALYSIS_WINDOWS,
  compact: narrow = false,
}: {
  value: number;
  onChange: (seconds: number) => void;
  windows?: readonly SegOption<number>[] | typeof ANALYSIS_WINDOWS;
  compact?: boolean;
}) {
  const phone = useMediaQuery("(max-width: 900px)");
  const compact = narrow || phone;
  const options = windows.map(window =>
    "seconds" in window
      ? { value: window.seconds, label: window.label }
      : window
  );
  if (compact) {
    return (
      <select
        className="mg-button"
        aria-label="분석 구간"
        value={value}
        onChange={event => onChange(Number(event.target.value))}
      >
        {options.map(option => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    );
  }
  return (
    <Seg
      label="분석 구간"
      options={options}
      value={value}
      onChange={onChange}
    />
  );
}

/** A composition bar of server-given percentages. */
export function Band({
  parts,
  label,
}: {
  parts: ReadonlyArray<{ key: string; share: number | null; color: string }>;
  label: string;
}) {
  return (
    <div className="mg-band" role="img" aria-label={label}>
      {parts.map(part =>
        typeof part.share === "number" && part.share > 0 ? (
          <span
            key={part.key}
            data-part={part.key}
            style={{ width: `${part.share}%`, background: part.color }}
          />
        ) : null
      )}
    </div>
  );
}

export function Table({
  label,
  head,
  children,
}: {
  label: string;
  head: ReactNode;
  children: ReactNode;
}) {
  // A long table scrolls inside its card under a header that stays.
  const long = Children.count(children) > 8;
  return (
    <div className="mg-table-wrap" data-long={long ? "" : undefined}>
      <table className="mg-table" aria-label={label}>
        <thead>{head}</thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}
