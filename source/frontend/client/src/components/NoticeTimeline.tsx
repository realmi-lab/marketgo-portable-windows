import { useEffect, useMemo, useRef } from "react";
import { InlineHelp } from "@/components/common/InlineHelp";
import { exchangeLabel } from "@/lib/exchanges";
import {
  dedupeNoticeRows,
  marketShareTimelineFocusScrollLeft,
  marketShareNoticeIdentity,
  mergeTimelinePeriods,
  type MarketShareTimelinePeriod,
} from "@/lib/market-share-timeline";
import type { MarketShareRollingNotice } from "@/lib/market-share-types";
import "./notice-timeline.css";

// Shared by the summary page and the notice portal, which loads only this
// timeline instead of the whole summary page.
export const NOTICE_EXCHANGES = [
  { id: "bithumb", label: "빗썸" },
  { id: "upbit", label: "업비트" },
] as const;

function finite(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

export function noticeExchangeLabel(exchange: string): string {
  return (
    NOTICE_EXCHANGES.find(item => item.id === exchange)?.label ??
    exchangeLabel(exchange)
  );
}

type NoticeTimelineRow = {
  notice: MarketShareRollingNotice;
  scheduleKey: string;
  periods: MarketShareTimelinePeriod[];
  startKey: string;
  endKey: string;
  startMs: number;
  endMs: number;
  periodCapped: boolean;
};

type NoticeTimelineMonth = {
  key: string;
  label: string;
  span: number;
};

export const NOTICE_PREVIEW_LIMIT = 30;
const TIMELINE_MAX_ROWS = NOTICE_EXCHANGES.length * NOTICE_PREVIEW_LIMIT;
const TIMELINE_DAY_WIDTH = 28;
const LISTING_TIMELINE_MAX_DAYS = 14;
const DAY_MS = 24 * 60 * 60 * 1_000;
const TIMELINE_FIXED_COLUMNS = {
  exchange: 96,
  title: 240,
  summary: 200,
};

// Each rendered period reads several date keys; build the formatters once.
const TIMELINE_DAY_FORMAT = new Intl.DateTimeFormat("en-CA", {
  timeZone: "Asia/Seoul",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});
const TIMELINE_MONTH_FORMAT = new Intl.DateTimeFormat("en-CA", {
  timeZone: "Asia/Seoul",
  year: "numeric",
  month: "2-digit",
});

function timelineDateKey(value: number): string {
  const parts = TIMELINE_DAY_FORMAT.formatToParts(new Date(value));
  const part = (type: string) =>
    parts.find(item => item.type === type)?.value ?? "00";
  return `${part("year")}-${part("month")}-${part("day")}`;
}

function timelinePreviousMonthStartKey(asOfMs: number): string {
  const parts = TIMELINE_MONTH_FORMAT.formatToParts(new Date(asOfMs));
  let year = Number(parts.find(item => item.type === "year")?.value ?? 0);
  let month = Number(parts.find(item => item.type === "month")?.value ?? 0);
  if (month === 1) {
    year -= 1;
    month = 12;
  } else {
    month -= 1;
  }
  return `${year}-${String(month).padStart(2, "0")}-01`;
}

function timelineDateFromKey(key: string): Date {
  const [year, month, day] = key.split("-").map(Number);
  return new Date(Date.UTC(year, month - 1, day));
}

function timelineDays(startKey: string, endKey: string): string[] {
  const days: string[] = [];
  const cursor = timelineDateFromKey(startKey);
  const end = timelineDateFromKey(endKey).getTime();
  while (cursor.getTime() <= end && days.length < 366) {
    days.push(cursor.toISOString().slice(0, 10));
    cursor.setUTCDate(cursor.getUTCDate() + 1);
  }
  return days;
}

function timelineDayLabel(key: string): string {
  return String(Number(key.slice(8, 10)));
}

function timelineMonthLabel(key: string): string {
  return `${key.slice(0, 4)}년 ${Number(key.slice(5, 7))}월`;
}

function timelineDateLabel(value: number): string {
  const key = timelineDateKey(value);
  return `${Number(key.slice(5, 7))}/${Number(key.slice(8, 10))}`;
}

function timelineMonths(days: string[]): NoticeTimelineMonth[] {
  const months: NoticeTimelineMonth[] = [];
  for (const day of days) {
    const key = day.slice(0, 7);
    const previous = months[months.length - 1];
    if (previous?.key === key) {
      previous.span += 1;
    } else {
      months.push({ key, label: timelineMonthLabel(day), span: 1 });
    }
  }
  return months;
}

function timelineText(value: string | null | undefined): string | null {
  const text = value?.trim();
  return text &&
    text !== "—" &&
    // Placeholders from older and current server text are not timeline content.
    text !== "자료 미확보" &&
    text !== "자료 없음" &&
    text !== "본문 미확보" &&
    text !== "본문 없음"
    ? text
    : null;
}

function timelineSummary(notice: MarketShareRollingNotice): string {
  const lineValue = (label: string) =>
    timelineText(notice.brief.lines.find(line => line.label === label)?.value);
  const period = lineValue("이벤트 기간") ?? lineValue("조치 기간");
  const target = lineValue("이벤트 대상자");
  const affected = lineValue("영향 대상");
  const coin = lineValue("이벤트 코인");
  const affectedCoin = lineValue("대상 코인");
  const reward = lineValue("이벤트 상금");
  const operation = lineValue("유의·입출금");
  const meaningfulOperation =
    operation && operation !== "해당 내용 없음" ? operation : null;
  const parts = [
    notice.brief.kind === "EVENT"
      ? target && `대상 ${target}`
      : period && `기간 ${period}`,
    notice.brief.kind === "EVENT"
      ? coin && `코인 ${coin}`
      : (affectedCoin ?? affected) && `대상 ${affectedCoin ?? affected}`,
    notice.brief.kind === "EVENT"
      ? reward && `상금 ${reward}`
      : meaningfulOperation && `조치 ${meaningfulOperation}`,
  ].filter((value): value is string => Boolean(value));
  if (parts.length) return parts.join(" · ");
  return (
    notice.brief.lines.find(line => timelineText(line.value))?.value ??
    "본문 요약 확인"
  );
}

function isNewListingNotice(notice: MarketShareRollingNotice): boolean {
  const category = notice.category.toUpperCase();
  return category === "LISTING" || category === "MARKET_ADDITION";
}

function timelinePeriodForValues(
  notice: MarketShareRollingNotice,
  startMs: number | null | undefined,
  rawEndMs: number | null | undefined,
  precision: string | null | undefined
): { startMs: number; endMs: number; periodCapped: boolean } | null {
  if (precision === "RECURRING" || !finite(startMs) || startMs <= 0)
    return null;
  const hasEnd = finite(rawEndMs) && rawEndMs > 0;
  if (!isNewListingNotice(notice)) {
    if (!hasEnd || rawEndMs < startMs) return null;
    return { startMs, endMs: rawEndMs, periodCapped: false };
  }
  const maxEndMs = startMs + LISTING_TIMELINE_MAX_DAYS * DAY_MS;
  const endMs = hasEnd ? Math.min(rawEndMs, maxEndMs) : maxEndMs;
  if (endMs < startMs) return null;
  return { startMs, endMs, periodCapped: !hasEnd || rawEndMs > maxEndMs };
}

function timelinePeriodLabel(period: MarketShareTimelinePeriod): string {
  const start = timelineDateLabel(period.startMs);
  const end = timelineDateLabel(period.endMs);
  const range = start === end ? start : `${start}–${end}`;
  return `${range}${period.periodCapped ? " · 최대 14일" : ""}`;
}

// A label wider than its bar showed only a sliver of text; such bars keep the
// dates in their tooltip and in the notice column instead.
function timelineBarFitsLabel(barWidth: number, label: string): boolean {
  return barWidth >= label.length * 8 + 16;
}

function timelinePeriods(
  notice: MarketShareRollingNotice
): MarketShareTimelinePeriod[] {
  const sourceSchedules = notice.brief.timeline_schedules?.length
    ? notice.brief.timeline_schedules
    : (notice.brief.schedules ?? []);
  const scheduledPeriods = sourceSchedules
    .map(schedule =>
      timelinePeriodForValues(
        notice,
        schedule.start_ts_ms,
        schedule.end_ts_ms,
        schedule.precision
      )
    )
    .filter((period): period is MarketShareTimelinePeriod => Boolean(period));
  const uniqueScheduledPeriods = mergeTimelinePeriods(
    scheduledPeriods,
    timelineDateKey
  );
  if (uniqueScheduledPeriods.length) return uniqueScheduledPeriods;
  const fallback = timelinePeriodForValues(
    notice,
    notice.brief.period_start_ts_ms,
    notice.brief.period_end_ts_ms,
    notice.brief.period_precision
  );
  return fallback ? [fallback] : [];
}

export function buildNoticeTimeline(
  notices: MarketShareRollingNotice[],
  asOfMs: number
): {
  rows: NoticeTimelineRow[];
  incompleteCount: number;
  datedCount: number;
  outOfRangeCount: number;
  totalCount: number;
  relatedCount: number;
} {
  const relatedNotices = dedupeNoticeRows(
    notices.filter(notice => notice.related)
  );
  const timelineStartKey = timelinePreviousMonthStartKey(asOfMs);
  const timelineStartMs = timelineDateFromKey(timelineStartKey).getTime();
  const records = relatedNotices.map((notice, index) => ({
    notice,
    identity: marketShareNoticeIdentity(notice) ?? `row:${index}`,
    periods: timelinePeriods(notice),
  }));
  const datedRecords = records.filter(record => record.periods.length > 0);
  const incompleteCount = records.length - datedRecords.length;
  const outOfRangeCount = datedRecords.filter(record =>
    record.periods.every(period => period.endMs < timelineStartMs)
  ).length;
  const candidates = datedRecords.flatMap(record => {
    const periods = record.periods.filter(
      period => period.endMs >= timelineStartMs
    );
    if (!periods.length) return [];
    const startMs = Math.min(...periods.map(period => period.startMs));
    const endMs = Math.max(...periods.map(period => period.endMs));
    return [
      {
        notice: record.notice,
        periods,
        scheduleKey: record.identity,
        startMs,
        endMs,
        periodCapped: periods.some(period => period.periodCapped),
        startKey: timelineDateKey(startMs),
        endKey: timelineDateKey(endMs),
      },
    ];
  });
  const selected = [...candidates]
    .sort(
      (left, right) =>
        Number(right.notice.schedule_active_now) -
          Number(left.notice.schedule_active_now) ||
        Number(right.notice.content_schedule_related) -
          Number(left.notice.content_schedule_related) ||
        Number(right.notice.content_priority_score) -
          Number(left.notice.content_priority_score) ||
        right.startMs - left.startMs
    )
    .slice(0, TIMELINE_MAX_ROWS)
    .sort(
      (left, right) => left.startMs - right.startMs || left.endMs - right.endMs
    );
  return {
    rows: selected,
    incompleteCount,
    datedCount: datedRecords.length,
    outOfRangeCount,
    totalCount: candidates.length,
    relatedCount: relatedNotices.length,
  };
}

const NOTICE_TIMELINE_HELP =
  "현재 월 기준 지난달 1일부터 본문에서 확인된 이벤트·입출금·주의·점검 일정의 시작일·종료일을 월·일 축에 맞춰 표시합니다. 한 공지는 한 행으로 묶고 시작일이 다른 일정만 같은 행 안의 여러 막대로 표시합니다. 막대는 시간상 맥락만 보여주며 원인을 확정하지 않습니다.";

export function NoticeTimeline({
  timeline,
  asOfMs,
  onAssetSelect,
  compact = false,
}: {
  timeline: ReturnType<typeof buildNoticeTimeline>;
  asOfMs: number;
  onAssetSelect?: (asset: string) => void;
  compact?: boolean;
}) {
  const {
    rows,
    incompleteCount,
    datedCount,
    outOfRangeCount,
    totalCount,
    relatedCount,
  } = timeline;
  const firstDay = timelinePreviousMonthStartKey(asOfMs);
  const todayKey = timelineDateKey(asOfMs);
  const lastDay = rows.reduce(
    (current, row) => (current > row.endKey ? current : row.endKey),
    todayKey
  );
  const days = timelineDays(firstDay, lastDay);
  const months = timelineMonths(days);
  const dayIndexByKey = new Map(days.map((day, index) => [day, index]));
  const timelineWidth = Math.max(560, days.length * TIMELINE_DAY_WIDTH);
  const dayWidth = timelineWidth / days.length;
  const todayIndex = days.indexOf(todayKey);
  const todayLineLeft =
    todayIndex >= 0 ? todayIndex * dayWidth + dayWidth / 2 : null;
  const dayColumns = `repeat(${days.length}, ${dayWidth}px)`;
  const rowColumns = compact
    ? `var(--notice-timeline-info-width) ${timelineWidth}px`
    : `${TIMELINE_FIXED_COLUMNS.exchange}px ${TIMELINE_FIXED_COLUMNS.title}px ${TIMELINE_FIXED_COLUMNS.summary}px ${timelineWidth}px`;
  const firstColumnStyle = { left: 0, width: TIMELINE_FIXED_COLUMNS.exchange };
  const secondColumnStyle = {
    left: TIMELINE_FIXED_COLUMNS.exchange,
    width: TIMELINE_FIXED_COLUMNS.title,
  };
  const thirdColumnStyle = {
    left: TIMELINE_FIXED_COLUMNS.exchange + TIMELINE_FIXED_COLUMNS.title,
    width: TIMELINE_FIXED_COLUMNS.summary,
  };
  const fixedWidth =
    TIMELINE_FIXED_COLUMNS.exchange +
    TIMELINE_FIXED_COLUMNS.title +
    TIMELINE_FIXED_COLUMNS.summary;
  const timelineScrollRef = useRef<HTMLDivElement>(null);
  const autoFocusedTimelineKeyRef = useRef("");
  const timelineFocusKey = `${todayKey}:${days.length}:${rows.length}`;
  // A row can hold past, current and future periods; count it once, by its
  // most current period, so 진행 중 + 예정 + 종료 equals the rows shown.
  const rowStatus = (row: (typeof rows)[number]) =>
    row.periods.some(
      period =>
        timelineDateKey(period.startMs) <= todayKey &&
        timelineDateKey(period.endMs) >= todayKey
    )
      ? "active"
      : row.periods.some(period => timelineDateKey(period.startMs) > todayKey)
        ? "upcoming"
        : "ended";
  const statusCount = (status: string) =>
    rows.filter(row => rowStatus(row) === status).length;
  const activeCount = statusCount("active");
  const upcomingCount = statusCount("upcoming");
  const endedCount = statusCount("ended");

  const scrollTimelineToDay = (
    dayIndex: number,
    behavior: ScrollBehavior = "smooth"
  ) => {
    const scroller = timelineScrollRef.current;
    if (!scroller || dayIndex < 0) return;
    const firstColumn = scroller.querySelector(
      compact ? ".notice-timeline-info" : ".notice-timeline-sticky"
    );
    const columnsScrollWithDates = !!(
      (compact || scroller.closest(".main-dashboard")) &&
      firstColumn &&
      window.getComputedStyle(firstColumn).position === "static"
    );
    const scrollerStyle = columnsScrollWithDates
      ? window.getComputedStyle(scroller)
      : null;
    const horizontalPadding = scrollerStyle
      ? (parseFloat(scrollerStyle.paddingLeft) || 0) +
        (parseFloat(scrollerStyle.paddingRight) || 0)
      : 0;
    scroller.scrollTo({
      left: marketShareTimelineFocusScrollLeft({
        fixedWidth:
          compact && firstColumn
            ? firstColumn.getBoundingClientRect().width
            : fixedWidth,
        timelineWidth,
        viewportWidth: scroller.clientWidth - horizontalPadding,
        focusLeft: dayIndex * dayWidth + dayWidth / 2,
        fixedColumnsPinned: !columnsScrollWithDates,
      }),
      behavior,
    });
  };

  useEffect(() => {
    if (
      !rows.length ||
      todayIndex < 0 ||
      autoFocusedTimelineKeyRef.current === timelineFocusKey
    ) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      scrollTimelineToDay(todayIndex, "auto");
      autoFocusedTimelineKeyRef.current = timelineFocusKey;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [timelineFocusKey, todayIndex]);

  if (!rows.length) {
    const message =
      relatedCount === 0
        ? "현재 선택한 공지 중 시간상 연결된 일정이 없습니다."
        : datedCount === 0
          ? `관련 있음 ${relatedCount}건 중 시작일·종료일이 모두 확인된 일정이 없습니다. 날짜가 없는 공지는 막대를 추정하지 않습니다.`
          : outOfRangeCount === datedCount
            ? `관련 있음 ${relatedCount}건의 일정은 확인됐지만 모두 지난달 1일 이전에 종료되어 현재 타임라인 범위에 표시할 일정이 없습니다.`
            : "확인된 일정이 현재 타임라인 범위와 겹치지 않습니다.";
    const detail = [
      incompleteCount
        ? `기간 미확인 또는 반복 일정 ${incompleteCount}건은 아래 공지에서 확인할 수 있습니다.`
        : "",
      outOfRangeCount
        ? `지난달 이전 종료 ${outOfRangeCount}건은 표시 범위에서 제외했습니다.`
        : "",
    ]
      .filter(Boolean)
      .join(" ");
    return (
      <div className="border-b border-border bg-inset px-4 py-4 text-xs text-muted-foreground">
        <p className="inline-help-heading font-semibold text-foreground">
          공지사항 타임라인 그래프
          <InlineHelp text={NOTICE_TIMELINE_HELP} />
        </p>
        <p className="mt-1 leading-6">
          {message}
          {detail ? ` ${detail}` : ""}
        </p>
      </div>
    );
  }

  return (
    <section
      className={`border-b border-border bg-inset${compact ? " notice-timeline-compact" : ""}`}
      aria-label="이벤트 타임라인"
    >
      <div className="notice-timeline-toolbar flex flex-wrap items-end justify-between gap-3 px-4 pb-3 pt-4">
        <div>
          <h4 className="inline-help-heading text-sm font-semibold">
            {compact ? "공지 일정 타임라인" : "공지사항 타임라인 그래프"}
            <InlineHelp text={NOTICE_TIMELINE_HELP} />
          </h4>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2 text-base text-muted-foreground">
          <span className="border border-profit/30 bg-profit/10 px-2 py-1 text-profit">
            진행 중 {activeCount}
          </span>
          <span className="border border-info/30 bg-info/10 px-2 py-1 text-info">
            예정 {upcomingCount}
          </span>
          <span className="border border-border bg-inset px-2 py-1">
            종료 {endedCount}
          </span>
          <button
            type="button"
            className="border border-border bg-card px-2.5 py-1 font-semibold text-foreground transition-colors hover:border-primary/40 hover:text-primary"
            onClick={() =>
              timelineScrollRef.current?.scrollTo({
                left: 0,
                behavior: "smooth",
              })
            }
          >
            {compact ? "처음" : "처음으로"}
          </button>
          <button
            type="button"
            className="border border-primary/40 bg-primary/10 px-2.5 py-1 font-semibold text-primary transition-colors hover:border-primary"
            onClick={() => scrollTimelineToDay(todayIndex)}
          >
            {compact ? "오늘" : "오늘로 이동"}
          </button>
        </div>
      </div>
      <div className="notice-timeline-count px-4 pb-3 text-base text-muted-foreground">
        관련 공지 {relatedCount}건 중 {rows.length}건 표시
        {totalCount > rows.length
          ? ` · 최대 ${TIMELINE_MAX_ROWS}건까지 표시`
          : ""}
        {incompleteCount ? ` · 기간 미확인 ${incompleteCount}건` : ""}
        {outOfRangeCount ? ` · 지난달 이전 종료 ${outOfRangeCount}건` : ""}
      </div>
      <div
        ref={timelineScrollRef}
        className="notice-timeline-scroll overflow-x-auto px-4 pb-4"
      >
        <div
          className="min-w-max border border-border bg-card"
          style={{
            width: compact
              ? `calc(var(--notice-timeline-info-width) + ${timelineWidth}px)`
              : TIMELINE_FIXED_COLUMNS.exchange +
                TIMELINE_FIXED_COLUMNS.title +
                TIMELINE_FIXED_COLUMNS.summary +
                timelineWidth,
          }}
        >
          <div
            className="notice-timeline-columns grid border-b border-border bg-inset text-base font-semibold"
            style={{ gridTemplateColumns: rowColumns }}
          >
            {compact ? (
              <div className="notice-timeline-info notice-timeline-info-heading">
                공지 · 관련 코인
              </div>
            ) : (
              <>
                <div
                  className="notice-timeline-sticky sticky z-20 flex h-16 items-end border-r border-border bg-inset px-3 pb-3"
                  style={firstColumnStyle}
                >
                  거래소
                </div>
                <div
                  className="notice-timeline-sticky sticky z-20 flex h-16 items-end border-r border-border bg-inset px-3 pb-3"
                  style={secondColumnStyle}
                >
                  이벤트명
                </div>
                <div
                  className="notice-timeline-sticky sticky z-20 flex h-16 items-end border-r border-border bg-inset px-3 pb-3"
                  style={thirdColumnStyle}
                >
                  이벤트 요약
                </div>
              </>
            )}
            {/* Month and day numbers are an axis, not headings. */}
            <div
              className="relative h-16 font-normal"
              style={{ width: timelineWidth }}
            >
              {todayLineLeft !== null && (
                <div
                  className="pointer-events-none absolute inset-y-0 z-30"
                  style={{ left: todayLineLeft }}
                  aria-label={`오늘 ${timelineDateLabel(asOfMs)}`}
                >
                  {/* Page-colored text: white on the lighter dark-theme red was 2.9:1. */}
                  {!compact && (
                    <span className="absolute left-1/2 top-0 -translate-x-1/2 whitespace-nowrap bg-loss px-1.5 py-0.5 text-base font-semibold text-background">
                      오늘 {timelineDateLabel(asOfMs)}
                    </span>
                  )}
                  <span className="absolute inset-y-0 left-0 w-0.5 bg-loss" />
                </div>
              )}
              <div
                className="grid h-8 border-b border-border"
                style={{ gridTemplateColumns: dayColumns }}
              >
                {months.map(month => (
                  <div
                    key={month.key}
                    className="flex items-center justify-center whitespace-nowrap border-r border-border px-1 text-base"
                    style={{ gridColumn: `span ${month.span}` }}
                  >
                    {compact ? (
                      <span className="notice-timeline-month-label">
                        {month.label}
                      </span>
                    ) : (
                      month.label
                    )}
                  </div>
                ))}
              </div>
              <div
                className="grid h-8"
                style={{ gridTemplateColumns: dayColumns }}
              >
                {days.map(day => (
                  <div
                    key={day}
                    className="flex items-center justify-center whitespace-nowrap border-r border-border/70 text-base text-muted-foreground"
                    style={
                      compact && day === todayKey
                        ? {
                            color: "var(--loss)",
                            background:
                              "color-mix(in srgb, var(--loss) 16%, transparent)",
                            fontWeight: 700,
                          }
                        : undefined
                    }
                  >
                    {timelineDayLabel(day)}
                  </div>
                ))}
              </div>
            </div>
          </div>
          {rows.map(row => {
            const barColor = `var(--exchange-${row.notice.exchange})`;
            const rowHeight = Math.max(68, row.periods.length * 30 + 16);
            const periodLabels = row.periods
              .map(timelinePeriodLabel)
              .join(" · ");
            return (
              <div
                key={row.notice.exchange + "-" + row.scheduleKey}
                className="notice-timeline-row grid border-b border-border last:border-b-0"
                style={{
                  gridTemplateColumns: rowColumns,
                  minHeight: rowHeight,
                }}
              >
                {compact ? (
                  <div className="notice-timeline-info">
                    <div className="notice-timeline-meta">
                      <span
                        style={{
                          color: `var(--exchange-${row.notice.exchange}-text, ${barColor})`,
                        }}
                      >
                        {noticeExchangeLabel(row.notice.exchange)}
                      </span>
                      {row.notice.assets.map(asset =>
                        onAssetSelect ? (
                          <button
                            key={asset}
                            type="button"
                            onClick={() => onAssetSelect(asset)}
                            aria-label={`${asset} 거래량 보기`}
                          >
                            {asset}
                          </button>
                        ) : (
                          <span key={asset} className="notice-timeline-asset">
                            {asset}
                          </span>
                        )
                      )}
                    </div>
                    <details className="notice-timeline-detail">
                      <summary>{row.notice.title || "제목 없음"}</summary>
                      <div>
                        <p>{timelineSummary(row.notice)}</p>
                        <p>{periodLabels}</p>
                        {row.notice.url && (
                          <a
                            href={row.notice.url}
                            target="_blank"
                            rel="noreferrer"
                          >
                            공식 공지 원문 ↗
                          </a>
                        )}
                      </div>
                    </details>
                  </div>
                ) : (
                  <>
                    <div
                      className="notice-timeline-sticky sticky z-10 border-r border-border bg-card px-3 py-3"
                      style={{ ...firstColumnStyle, minHeight: rowHeight }}
                    >
                      <div className="flex items-center gap-2 text-xs font-semibold">
                        <span
                          className="h-2 w-2 shrink-0"
                          style={{ backgroundColor: barColor }}
                        />
                        {noticeExchangeLabel(row.notice.exchange)}
                      </div>
                      <span
                        className={
                          "mt-1 inline-block text-base " +
                          (row.notice.related
                            ? "text-gold"
                            : "text-muted-foreground")
                        }
                      >
                        {row.notice.related_label}
                      </span>
                    </div>
                    <div
                      className="notice-timeline-sticky sticky z-10 border-r border-border bg-card px-3 py-3"
                      style={{ ...secondColumnStyle, minHeight: rowHeight }}
                      title={row.notice.title || "제목 없음"}
                    >
                      <p className="whitespace-normal break-words text-xs font-semibold text-foreground">
                        {row.notice.title || "제목 없음"}
                      </p>
                      {onAssetSelect && (
                        <div className="notice-asset-actions">
                          {row.notice.assets.map(asset => (
                            <button
                              key={asset}
                              type="button"
                              onClick={() => onAssetSelect(asset)}
                              aria-label={`${asset} 거래량 보기`}
                            >
                              {asset}
                            </button>
                          ))}
                        </div>
                      )}
                      <p className="mt-1 whitespace-normal break-words text-base text-muted-foreground">
                        {periodLabels}
                      </p>
                    </div>
                    <div
                      className="notice-timeline-sticky sticky z-10 border-r border-border bg-card px-3 py-3"
                      style={{ ...thirdColumnStyle, minHeight: rowHeight }}
                      title={timelineSummary(row.notice)}
                    >
                      <p className="whitespace-normal break-words text-base leading-6 text-foreground/80">
                        {timelineSummary(row.notice)}
                      </p>
                    </div>
                  </>
                )}
                <div
                  className="notice-timeline-dates relative"
                  style={{ width: timelineWidth, minHeight: rowHeight }}
                >
                  <div
                    aria-hidden="true"
                    className="pointer-events-none absolute inset-0"
                    style={{
                      backgroundImage: `repeating-linear-gradient(to right, transparent 0, transparent ${TIMELINE_DAY_WIDTH - 1}px, var(--border) ${TIMELINE_DAY_WIDTH - 1}px, var(--border) ${TIMELINE_DAY_WIDTH}px)`,
                      opacity: 0.5,
                    }}
                  />
                  {todayLineLeft !== null && (
                    <span
                      aria-hidden="true"
                      className="pointer-events-none absolute inset-y-0 z-20 w-0.5 bg-loss"
                      style={{ left: todayLineLeft }}
                    />
                  )}
                  <div className="relative" style={{ minHeight: rowHeight }}>
                    {row.periods.map((period, index) => {
                      const periodStartKey = timelineDateKey(period.startMs);
                      const periodEndKey = timelineDateKey(period.endMs);
                      const startIndex = dayIndexByKey.get(periodStartKey) ?? -1;
                      const endIndex = dayIndexByKey.get(periodEndKey) ?? -1;
                      const visibleStart = Math.max(0, startIndex);
                      const visibleEnd = Math.min(
                        days.length - 1,
                        endIndex < 0 ? days.length - 1 : endIndex
                      );
                      if (visibleEnd < visibleStart) return null;
                      const barLeft = visibleStart * dayWidth + 4;
                      const barWidth = Math.max(
                        dayWidth - 8,
                        (visibleEnd - visibleStart + 1) * dayWidth - 8
                      );
                      const label = timelinePeriodLabel(period);
                      const periodState =
                        periodStartKey <= todayKey && periodEndKey >= todayKey
                          ? "active"
                          : periodStartKey > todayKey
                            ? "upcoming"
                            : "ended";
                      return (
                        <span
                          key={`${row.scheduleKey}-${period.startMs}-${period.endMs}-${index}`}
                          className={`notice-timeline-period notice-timeline-period-${periodState} absolute flex h-6 items-center overflow-hidden whitespace-nowrap rounded-sm border px-2 text-base font-semibold`}
                          style={{
                            left: barLeft,
                            top: 8 + index * 28,
                            width: barWidth,
                            backgroundColor: `color-mix(in srgb, ${barColor} 22%, transparent)`,
                            borderColor: barColor,
                            color: `var(--exchange-${row.notice.exchange}-text, ${barColor})`,
                          }}
                          title={`${row.notice.title || "제목 없음"} · ${label}`}
                        >
                          {timelineBarFitsLabel(barWidth, label) && (
                            <span className="truncate">{label}</span>
                          )}
                        </span>
                      );
                    })}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}

export function NoticeTimelinePanel({
  notices,
  asOfMs,
  onAssetSelect,
}: {
  notices: MarketShareRollingNotice[];
  asOfMs: number;
  onAssetSelect: (asset: string) => void;
}) {
  const timeline = useMemo(
    () => buildNoticeTimeline(notices, asOfMs),
    [notices, asOfMs]
  );
  return (
    <NoticeTimeline
      timeline={timeline}
      asOfMs={asOfMs}
      onAssetSelect={onAssetSelect}
      compact
    />
  );
}
