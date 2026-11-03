type NoticeScheduleTime = {
  start_ts_ms: number;
  end_ts_ms: number;
  precision?: string;
  timezone?: string;
};

const KOREAN_TIME_ZONE = "Asia/Seoul";

/**
 * A notice's extracted schedules can repeat the same period (one row per
 * sentence that states it). Show each kind and period once.
 */
export function uniqueNoticeSchedules<
  T extends NoticeScheduleTime & { kind: string },
>(schedules: readonly T[] | null | undefined): T[] {
  const seen = new Set<string>();
  return (schedules ?? []).filter(schedule => {
    const key = `${schedule.kind}|${schedule.start_ts_ms}|${schedule.end_ts_ms}|${schedule.precision ?? ""}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function noticeTimeZone(value?: string): string {
  if (!value || value === "KST") return KOREAN_TIME_ZONE;
  try {
    new Intl.DateTimeFormat("ko-KR", { timeZone: value });
    return value;
  } catch {
    return KOREAN_TIME_ZONE;
  }
}

export function formatNoticeTime(
  value: number,
  dayOnly = false,
  timeZone = KOREAN_TIME_ZONE
): string {
  if (
    !Number.isFinite(value) ||
    value <= 0 ||
    !Number.isFinite(new Date(value).getTime())
  )
    return "미정";
  const options = { timeZone: noticeTimeZone(timeZone) };
  return dayOnly
    ? new Date(value).toLocaleDateString("ko-KR", options)
    : // Explicit fields: ko-KR with only hour12 prints "12시 0분 0초".
      new Date(value).toLocaleString("ko-KR", {
        ...options,
        year: "numeric",
        month: "numeric",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      });
}

/** Source retry/error times are Unix seconds; notice publication times are ms. */
export function formatNoticeSourceTime(
  value: number | null | undefined
): string {
  if (value === null || value === undefined) return "기록 없음";
  const formatted = formatNoticeTime(value * 1_000);
  return formatted === "미정" ? "기록 없음" : formatted;
}

function formatScheduleEndpoint(
  value: number,
  precision: string | undefined,
  zone: string
): string {
  if (precision !== "MINUTE" && precision !== "HOUR") {
    return formatNoticeTime(value, precision === "DAY", zone);
  }
  if (
    !Number.isFinite(value) ||
    value <= 0 ||
    !Number.isFinite(new Date(value).getTime())
  ) {
    return "미정";
  }
  return new Date(value).toLocaleString("ko-KR", {
    timeZone: zone,
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    ...(precision === "MINUTE" ? { minute: "2-digit" as const } : {}),
    hour12: false,
  });
}

export function formatNoticeScheduleRange(
  schedule: NoticeScheduleTime
): string {
  const dayOnly = schedule.precision === "DAY";
  const zone = noticeTimeZone(schedule.timezone);
  const start = formatScheduleEndpoint(
    schedule.start_ts_ms,
    schedule.precision,
    zone
  );
  const end = formatScheduleEndpoint(
    schedule.end_ts_ms,
    schedule.precision,
    zone
  );
  const endOnly =
    (schedule.precision === "MINUTE" || schedule.precision === "HOUR") &&
    !(schedule.start_ts_ms > 0) &&
    schedule.end_ts_ms > 0;
  const range = endOnly
    ? end
    : !(schedule.end_ts_ms > 0) || (dayOnly && start === end)
      ? start
      : `${start} → ${end}`;
  // A source-local time must not look like the surrounding Korean timestamps.
  return zone === KOREAN_TIME_ZONE || range === "미정"
    ? range
    : `${range} (${zone})`;
}
