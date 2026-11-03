import {
  memo,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import {
  AlertTriangle,
  Bell,
  ChevronDown,
  ExternalLink,
  Radio,
} from "lucide-react";

import { MobileMoreToggle } from "@/components/common/MobileMoreToggle";
import { LLM_ENABLED } from "@/lib/app-features";
import { PageHeader } from "@/components/common/PageHeader";
import { HermesNoticeQuestion } from "@/components/HermesNoticeQuestion";
import {
  NoticePipelineQuality,
  noticePipelineLabel,
} from "@/components/NoticePipelineQuality";
import { useNoticeArchive } from "@/hooks/useNoticeArchive";
import { attachNoticeArchiveContext } from "@/lib/notice-archive";
import {
  useMarketShare,
  type MarketShareConnection,
  type MarketShareTickStore,
} from "@/hooks/useMarketShare";
import { exchangeNameKo as exchangeLabel } from "@/lib/exchanges";
import { noticeBodyText } from "@/lib/notice-portal";
import {
  formatNoticeScheduleRange as noticeScheduleRange,
  formatNoticeSourceTime as noticeSourceTime,
  uniqueNoticeSchedules,
} from "@/lib/notice-time";
import {
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import {
  MARKET_SHARE_NOTICE_WINDOWS,
  MARKET_SHARE_WINDOW_SEC,
} from "@/lib/market-share-config";
import {
  noticeAnalysisStatusDetail,
  noticeAnalysisStatusLabel,
  noticeSummaryDisplayText,
} from "@/lib/notice-analysis-status";
import {
  noticeCategoryLabel as sharedNoticeCategoryLabel,
  noticeHistoryStatusLabel,
  noticeScheduleLabel as sharedNoticeScheduleLabel,
} from "@/lib/market-share-labels";
import {
  noticeTimestampLines,
  sortNoticesNewestFirst,
} from "@/lib/market-share-reaction";
import { formatNoticeVolumeMultiplier } from "@/lib/notice-reaction-format";
import type {
  BithumbGapType,
  ExchangeNotice,
  MarketShareNoticeSnapshot,
  MarketShareRealtimeTick,
} from "@/lib/market-share-types";

const STUDY_STATUS_LABELS: Record<string, string> = {
  LIVE_ESTIMATE: "실시간 추정",
  MATURE: "반응 확인 완료",
  INCOMPLETE_CONTIGUOUS_COVERAGE: "연속 관측 부족",
  INCOMPLETE_FX_CONVERSION: "원화 환산 미완료",
};

function categoryLabel(value: string): string {
  return sharedNoticeCategoryLabel(value);
}

function scheduleLabel(value: string): string {
  return sharedNoticeScheduleLabel(value);
}

function studyStatusLabel(value: string): string {
  return STUDY_STATUS_LABELS[value] ?? "반응 상태 확인 필요";
}

type NoticeSourceState = MarketShareNoticeSnapshot["sources"][string];

function noticeSourcePending(source: NoticeSourceState): boolean {
  return source.error === "not_polled" || source.error_kind === "NOT_POLLED";
}

function noticeSourceHasError(source: NoticeSourceState): boolean {
  return Boolean(source.error) && !noticeSourcePending(source);
}

function noticeSourceStatusLabel(source: NoticeSourceState): string {
  if (noticeSourceHasError(source)) return "오류";
  if (noticeSourcePending(source)) return "공지를 처음 불러오는 중";
  if (["COMPLETE", "HERMES_30D_ACTIVE"].includes(source.history_status))
    return "공지 확인됨";
  return noticeHistoryStatusLabel(source.history_status);
}

function imageAnalysisStatusLabel(value: string): string {
  if (value === "ANALYZED") return "이미지 글자 읽기 완료";
  if (value === "OCR_UNAVAILABLE") return "이미지 글자 읽기 사용 불가";
  if (value === "OCR_ERROR") return "이미지 글자 읽기 오류";
  if (value === "NO_TEXT") return "텍스트 없음";
  if (value === "DOWNLOAD_FAILED" || value === "DOWNLOAD_ERROR")
    return "이미지 다운로드 실패";
  if (value === "VISION_FAILED") return "이미지 글자 읽기 실패";
  if (value === "UNSUPPORTED") return "지원하지 않는 이미지";
  if (value === "PENDING") return "분석 대기";
  return "이미지 확인 상태 미상";
}

function imageSemanticLabel(value: string | undefined): string {
  return (
    {
      CHART: "그래프·차트",
      TABLE: "표",
      BANNER: "이벤트 배너",
      TEXT_WITH_DATA: "수치 포함 문구",
      TEXT: "텍스트 이미지",
      UNKNOWN: "분류 불가",
    }[value ?? ""] ??
    value ??
    "분류 불가"
  );
}

function imageConsistencyLabel(
  value: ExchangeNotice["image_consistency"]
): string {
  if (!value) return "검증 정보 없음";
  return (
    {
      MATCHED: "본문·이미지 일치",
      PARTIAL: "일부 일치",
      CONFLICT: "본문·이미지 불일치",
      UNVERIFIED: "확인 불가",
    }[value.status] ?? value.status
  );
}

function imageSignalLabel(value: string): string {
  return (
    {
      date: "날짜·기간",
      time: "시각",
      asset: "자산",
      value: "금액·비율·수량",
      text: "본문 문구",
    }[value] ?? value
  );
}

function coverageStatusLabel(value: string | undefined): string {
  if (value === "CONFIRMED") return "전체 관측";
  if (value === "PROVISIONAL") return "확인 중";
  if (value === "PARTIAL") return "일부 누락";
  if (value === "UNAVAILABLE") return "계산 불가";
  return "연결 중";
}

function windowLabel(seconds: number, fallback: string): string {
  return (
    MARKET_SHARE_NOTICE_WINDOWS.find(window => window.seconds === seconds)
      ?.label ?? fallback
  );
}

function money(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwCompact(value).replace(/^\+/, "");
}

function percent(value: number | null | undefined, digits = 1): string {
  return formatSharePercent(value, digits);
}

function signedPoints(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return formatSignedPercentagePoints(value, 2);
}

function NoticeTimestamp({ notice }: { notice: ExchangeNotice }) {
  return (
    <>
      {noticeTimestampLines(notice).map(line => (
        <span key={line} className="block break-keep leading-5">
          {line}
        </span>
      ))}
    </>
  );
}

function gapTone(type: BithumbGapType): string {
  if (type === "UNLISTED" || type === "NO_RECENT_BITHUMB_VOLUME") {
    return "border-loss/30 bg-loss/10 text-loss";
  }
  if (type === "BELOW_VENUE_BASELINE") {
    return "border-gold/30 bg-gold/10 text-gold";
  }
  if (type === "AT_OR_ABOVE_BASELINE") {
    return "border-profit/30 bg-profit/10 text-profit";
  }
  return "border-border bg-secondary text-muted-foreground";
}

function NoticeReaction({ notice }: { notice: ExchangeNotice }) {
  const study = [...notice.reaction_studies]
    .filter(
      row =>
        row.status === "LIVE_ESTIMATE" ||
        row.status === "MATURE" ||
        row.status === "INCOMPLETE_CONTIGUOUS_COVERAGE" ||
        row.status === "INCOMPLETE_FX_CONVERSION"
    )
    .sort(
      (left, right) =>
        Number(right.status === "MATURE" || right.status === "LIVE_ESTIMATE") -
          Number(left.status === "MATURE" || left.status === "LIVE_ESTIMATE") ||
        (right.evidence_confidence_pct ?? 0) -
          (left.evidence_confidence_pct ?? 0)
    )[0];
  if (!study) return null;

  return (
    <details className="notice-reaction-summary mt-4 rounded-lg border border-primary/20 bg-primary/5 p-4">
      <summary className="cursor-pointer font-semibold text-primary">
        {study.asset} 공지 전후 시장 반응 · {studyStatusLabel(study.status)}
      </summary>
      <p className="mt-3 font-mono">
        거래금액 변화{" "}
        {formatNoticeVolumeMultiplier(study.exchange_volume_multiplier)}
        {study.bithumb_share_change_pp === null ||
        study.bithumb_share_change_pp === undefined
          ? ""
          : ` · 빗썸 점유율 변화 ${signedPoints(study.bithumb_share_change_pp)}`}
      </p>
      {study.status === "INCOMPLETE_FX_CONVERSION" && (
        <p className="mt-2 text-muted-foreground">
          원화 환산 미완료 체결: 기준 전{" "}
          {study.domestic_pre_unresolved_fx_count?.toLocaleString() ?? "미확인"}
          건 · 기준 후{" "}
          {study.domestic_post_unresolved_fx_count?.toLocaleString() ??
            "미확인"}
          건
        </p>
      )}
      <p className="mt-2 text-muted-foreground">
        {scheduleLabel(study.anchor_kind)} 전후의 관측값입니다. 공지가 변화의
        원인이라고 단정하지 않습니다.
      </p>
    </details>
  );
}

const WarmupNoticeRow = memo(function WarmupNoticeRow({
  notice,
}: {
  notice: ExchangeNotice;
}) {
  const [open, setOpen] = useState(false);
  return (
    <details
      className="group border-b border-border/70 last:border-b-0"
      onToggle={event => setOpen(event.currentTarget.open)}
    >
      <summary className="grid cursor-pointer list-none gap-2 px-4 py-3 transition-colors hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none md:grid-cols-[110px_1fr_auto] md:items-center">
        <div>
          <p className="text-xs font-semibold">
            {exchangeLabel(notice.exchange)}
          </p>
          <p className="mt-1 text-base text-muted-foreground">
            <NoticeTimestamp notice={notice} />
          </p>
        </div>
        <div className="min-w-0">
          <p className="truncate text-xs font-semibold">
            {notice.title || "제목 없음"}
          </p>
          <div className="mt-1 flex flex-wrap gap-1.5">
            <span className="border border-border bg-secondary px-2 py-0.5 text-base text-muted-foreground">
              {categoryLabel(notice.category)}
            </span>
            <span className="border border-border bg-secondary px-2 py-0.5 text-base text-muted-foreground">
              {noticePipelineLabel(notice.notice_quality_v2) ??
                noticeAnalysisStatusLabel(
                  notice.analysis_status,
                  notice.escalation_reason,
                  notice.content_source
                )}
            </span>
          </div>
        </div>
        <div className="flex items-center justify-end gap-2 text-base text-muted-foreground">
          <span>내용 보기</span>
          <ChevronDown
            className="h-4 w-4 shrink-0 transition-transform group-open:rotate-180"
            aria-hidden="true"
          />
        </div>
      </summary>
      {open && (
        <article className="border-t border-border/70 px-4 py-4 text-xs">
          {notice.url ? (
            <a
              href={notice.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 font-semibold hover:text-primary"
            >
              원문 열기 <ExternalLink className="h-3 w-3" />
            </a>
          ) : (
            <span className="font-semibold">원문 링크 없음</span>
          )}
          {notice.assets.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {notice.assets.map(asset => (
                <span
                  key={asset}
                  className="border border-border px-1.5 py-0.5 text-base"
                >
                  {asset}
                </span>
              ))}
            </div>
          )}
          <p className="mt-3 max-h-40 overflow-auto whitespace-pre-wrap border border-border/70 bg-inset px-3 py-3 leading-6 text-muted-foreground">
            {notice.body_excerpt ||
              (notice.summary && noticeSummaryDisplayText(notice.summary)) ||
              "본문 발췌 없음"}
          </p>
          {notice.schedules.length > 0 && (
            <div className="mt-2 grid gap-1.5 sm:grid-cols-2">
              {uniqueNoticeSchedules(notice.schedules).map(
                (schedule, index) => (
                  <div
                    key={`${schedule.kind}-${schedule.start_ts_ms}-${index}`}
                    className="border border-gold/20 bg-gold/10 px-2.5 py-2"
                  >
                    <p className="font-semibold text-gold">
                      {scheduleLabel(schedule.kind)}
                    </p>
                    <p className="mt-1 font-mono">
                      {noticeScheduleRange(schedule)}
                    </p>
                  </div>
                )
              )}
            </div>
          )}
          {notice.image_urls.length > 0 && (
            <p className="mt-2 text-muted-foreground">
              이미지 {notice.image_urls.length}개 · 글자 읽기{" "}
              {
                notice.image_analysis.filter(
                  image => image.status === "ANALYZED"
                ).length
              }
              개
            </p>
          )}
        </article>
      )}
    </details>
  );
});

const WarmupNoticeList = memo(function WarmupNoticeList({
  notices,
}: {
  notices: ExchangeNotice[];
}) {
  const sortedNotices = useMemo(
    () => sortNoticesNewestFirst(notices),
    [notices]
  );
  return sortedNotices.length > 0 ? (
    <div className="max-h-[44rem] overflow-auto border-t border-border">
      {sortedNotices.slice(0, 50).map(notice => (
        <WarmupNoticeRow
          key={`${notice.exchange}-${notice.notice_id}`}
          notice={notice}
        />
      ))}
    </div>
  ) : (
    <p className="border-t border-border px-4 py-4 text-xs text-muted-foreground">
      공식 공지 자료를 기다리는 중입니다.
    </p>
  );
});

function NoticeWarmupSourceState({
  snapshot,
}: {
  snapshot: MarketShareNoticeSnapshot | null;
}) {
  if (!snapshot) return null;
  const activeSources = Object.entries(snapshot.sources).filter(([, source]) =>
    noticeSourceHasError(source)
  );
  if (!activeSources.length && snapshot.active_error_count <= 0) return null;
  return (
    <div className="flex items-start gap-2 border-b border-loss/30 bg-loss/10 px-4 py-3 text-base leading-6 text-loss">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <div>
        <p className="font-semibold">공지 수집 오류·대기 상태가 있습니다.</p>
        <p className="mt-0.5">
          {activeSources.length
            ? activeSources
                .map(
                  ([exchange, source]) =>
                    `${exchangeLabel(exchange)}: ${source.error_kind === "BITHUMB_RATE_LIMIT" ? "요청 제한으로 재시도 대기" : "수집 오류"}`
                )
                .join(" · ")
            : `진행 중인 오류 ${snapshot.active_error_count}건`}
        </p>
      </div>
    </div>
  );
}

function NoticeWarmup({
  tickStore,
  noticeSnapshot,
  noticeMeta,
  connection,
  error,
  windowSec,
}: {
  tickStore: MarketShareTickStore;
  noticeSnapshot: ExchangeNotice[];
  noticeMeta: MarketShareNoticeSnapshot | null;
  connection: MarketShareConnection;
  error: string;
  windowSec: number;
}) {
  const tick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  ) as MarketShareRealtimeTick | null;
  const tickLive =
    connection === "live" &&
    Boolean(tick?.ws_realtime_complete) &&
    !["PARTIAL", "UNAVAILABLE"].includes(
      tick?.coverage_status ?? "UNAVAILABLE"
    );
  return (
    <div className="w-full space-y-6">
      <PageHeader
        title="거래소 공지·시장 반응"
        subtitle="공지 분석을 준비하는 동안 현재 확인 가능한 실시간 체결 흐름을 먼저 보여줍니다."
        icon={<Bell className="h-5 w-5" />}
      />
      {LLM_ENABLED && <HermesNoticeQuestion />}
      {error && (
        <div className="flex items-start gap-3 border border-gold/30 bg-gold/10 p-3 text-xs text-gold">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </div>
      )}
      <section className="glass-card overflow-hidden">
        <div className="border-b border-border px-4 py-4">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Radio className="h-4 w-4 text-muted-foreground" />
            공지·시장 반응 분석 준비 중
          </h3>
          <p className="mt-1 text-xs leading-6 text-muted-foreground">
            본문·일정·이미지 글자·공지 전후 반응을 계산하고 있습니다. 준비
            전에는 공지를 임의로 관련 있음으로 표시하지 않습니다.
          </p>
        </div>
        <div className="grid gap-px bg-border sm:grid-cols-3">
          <div className="bg-card p-4">
            <p className="text-xs text-muted-foreground">
              현재 거래금액 빗썸 몫
            </p>
            <p className="mt-2 text-2xl font-semibold">
              {percent(tickLive ? tick?.summary.bithumb_share_pct : null)}{" "}
            </p>
            <p className="mt-1 text-base text-muted-foreground">
              {tickLive
                ? money(tick?.summary.bithumb_volume_krw)
                : "확정 관측 대기"}{" "}
              · 선택{" "}
              {windowSec === 0
                ? "실시간·최근 10분"
                : windowLabel(windowSec, `${windowSec}초`)}{" "}
              관측
            </p>
          </div>
          <div className="bg-card p-4">
            <p className="text-xs text-muted-foreground">체결 건수</p>
            <p className="mt-2 text-2xl font-semibold">
              {tickLive
                ? `${tick?.summary.domestic_trade_count.toLocaleString("ko-KR")}회`
                : "—"}
            </p>
            <p className="mt-1 text-base text-muted-foreground">
              빗썸{" "}
              {tickLive
                ? tick?.summary.bithumb_trade_count.toLocaleString("ko-KR")
                : "—"}
              회 · 실시간 체결 {tickLive ? "정상" : "관측 중"}
            </p>
          </div>
          <div className="bg-card p-4">
            <p className="text-xs text-muted-foreground">분석 상태</p>
            <p className="mt-2 text-2xl font-semibold">
              {coverageStatusLabel(tick?.coverage_status)}
            </p>
            <p className="mt-1 text-base text-muted-foreground">
              {tick?.readiness
                ? `거래소 ${tick.readiness.ready_exchange_count}/${tick.readiness.configured_exchange_count} · 마켓 ${tick.readiness.connected_market_count.toLocaleString("ko-KR")}`
                : "실시간 체결 연결을 기다리는 중"}
            </p>
          </div>
        </div>
        <div className="overflow-x-auto">
          <table
            className="market-data-table w-full text-left text-xs"
            data-table-schema="label-numeric"
          >
            <thead>
              <tr>
                <th>거래소</th>
                <th className="text-right">거래금액</th>
                <th className="text-right">점유율</th>
                <th className="text-right">체결 건수</th>
                <th className="text-right">체결 건수 점유율</th>
              </tr>
            </thead>
            <tbody>
              {(tickLive ? (tick?.exchanges ?? []) : []).map(row => (
                <tr key={row.exchange}>
                  <td className="font-semibold">
                    {exchangeLabel(row.exchange)}
                  </td>
                  <td className="text-right">{money(row.volume_krw)}</td>
                  <td className="text-right">{percent(row.share_pct)}</td>
                  <td className="text-right">
                    {row.trade_count.toLocaleString("ko-KR")}회
                  </td>
                  <td className="text-right">
                    {percent(row.trade_count_share_pct)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <NoticeWarmupSourceState snapshot={noticeMeta} />
        <div className="border-t border-border">
          <div className="flex items-center justify-between gap-3 px-4 py-3">
            <h4 className="text-xs font-semibold">먼저 확보된 공지 원문</h4>
            <span className="text-base text-muted-foreground">
              {noticeSnapshot.length.toLocaleString("ko-KR")}건 · 기본 접힘
            </span>
          </div>
          <WarmupNoticeList notices={noticeSnapshot} />
        </div>
        <p className="border-t border-border px-4 py-3 text-base leading-6 text-muted-foreground">
          공지 목록은 전체 분석 응답이 준비된 뒤 최신 저장 원문과 본문 분석
          결과로 교체됩니다. 현재 체결 수치는 공지와의 관련성이나 인과관계를
          뜻하지 않습니다.
        </p>
      </section>
    </div>
  );
}

export default function MarketNotices({
  desktopWorkspace = false,
  archiveNotices = false,
  llmWorkspace = false,
}: {
  desktopWorkspace?: boolean;
  archiveNotices?: boolean;
  llmWorkspace?: boolean;
}) {
  const [windowSec, setWindowSec] = useState<number>(
    MARKET_SHARE_WINDOW_SEC.TEN_MINUTES
  );
  const [legacyNoticePage, setNoticePage] = useState(0);
  const archive = useNoticeArchive(archiveNotices);
  const noticePage = archiveNotices
    ? Math.floor((archive.data?.offset ?? 0) / 20)
    : legacyNoticePage;
  const [openNoticeIDs, setOpenNoticeIDs] = useState<Set<string>>(
    () => new Set()
  );
  const [sourceCardsExpanded, setSourceCardsExpanded] = useState(false);
  const liveMode = windowSec === MARKET_SHARE_WINDOW_SEC.REALTIME;
  const requestedWindowSec = liveMode
    ? MARKET_SHARE_WINDOW_SEC.TEN_MINUTES
    : windowSec;
  // Only the warm-up view shows tick values and subscribes to them itself;
  // the loaded notice page must not re-render on every market tick.
  const { data, tickStore, noticeSnapshot, noticeMeta, connection, error } =
    useMarketShare(requestedWindowSec, null, "KRW", "events", "none");
  const analysis = data?.analysis?.notices?.sources ? data.analysis : undefined;
  const noticeData = archiveNotices
    ? archive.data?.snapshot
    : analysis?.notices;
  const noticePageSize = 20;
  const noticeItems = useMemo(
    () =>
      archiveNotices
        ? archive.data
          ? attachNoticeArchiveContext(archive.data, analysis?.notices)
          : []
        : sortNoticesNewestFirst(analysis?.notices.items ?? []),
    [archiveNotices, archive.data, analysis?.notices]
  );
  const noticeCount = archiveNotices
    ? (archive.data?.total ?? 0)
    : noticeItems.length;
  const noticePageCount = Math.max(1, Math.ceil(noticeCount / noticePageSize));
  const noticeSources = noticeData ? Object.values(noticeData.sources) : [];
  const pendingNoticeSourceCount =
    noticeSources.filter(noticeSourcePending).length;
  const activeNoticeSourceErrorCount =
    noticeSources.filter(noticeSourceHasError).length;
  // Phones fold the per-source cards while every source is healthy; the
  // header badge above them still shows the source count and state.
  const sourcesNeedAttention =
    activeNoticeSourceErrorCount > 0 ||
    pendingNoticeSourceCount > 0 ||
    noticeData?.healthy_source_count !== noticeData?.source_count;
  const sourceCardsFolded = !sourcesNeedAttention && !sourceCardsExpanded;
  const visibleNotices = archiveNotices
    ? noticeItems
    : noticeItems.slice(
        noticePage * noticePageSize,
        (noticePage + 1) * noticePageSize
      );

  useEffect(() => {
    if (!archiveNotices) setNoticePage(0);
  }, [windowSec, archiveNotices]);
  useEffect(() => setOpenNoticeIDs(new Set()), [noticePage, windowSec]);
  useEffect(() => {
    if (!archiveNotices && noticePage >= noticePageCount)
      setNoticePage(noticePageCount - 1);
  }, [archiveNotices, noticePage, noticePageCount]);

  if (archiveNotices && !noticeData) {
    return (
      <div className="market-notices-page space-y-6">
        <PageHeader
          title={llmWorkspace ? "공지사항 LLM" : "거래소 공지·시장 반응"}
          subtitle="최근 30일의 전체 공식 공지를 확인합니다."
          icon={<Bell className="h-5 w-5" />}
        />
        {LLM_ENABLED && llmWorkspace && (
          <HermesNoticeQuestion answerScrollable />
        )}
        <section className="glass-card p-5" aria-busy={archive.loading}>
          <p role={archive.error ? "alert" : "status"}>
            {archive.error || "최근 30일 전체 공지를 불러오는 중입니다."}
          </p>
          {archive.error && (
            <button
              type="button"
              className="mt-3 rounded border border-border px-4 py-3"
              onClick={archive.refresh}
            >
              다시 확인
            </button>
          )}
        </section>
      </div>
    );
  }
  if (!noticeData) {
    return (
      <NoticeWarmup
        tickStore={tickStore}
        noticeSnapshot={noticeSnapshot}
        noticeMeta={noticeMeta}
        connection={connection}
        error={error}
        windowSec={windowSec}
      />
    );
  }

  return (
    <div className="market-notices-page w-full space-y-6">
      <PageHeader
        title={llmWorkspace ? "공지사항 LLM" : "공지·시장 이벤트"}
        subtitle={
          archiveNotices
            ? "최근 30일 공식 공지를 읽고 주요 일정을 확인하세요. 기간 선택은 시장 반응에 적용됩니다."
            : "공지와 일정 전후의 거래금액·빗썸 점유율 변화를 시간적 문맥으로 봅니다. 시장 반응은 인과관계 단정이 아닙니다."
        }
        icon={<Bell className="h-5 w-5" />}
        actions={
          <div className="segmented-control flex border border-border bg-inset p-1">
            {MARKET_SHARE_NOTICE_WINDOWS.map(window => (
              <button
                key={window.seconds}
                type="button"
                aria-pressed={windowSec === window.seconds}
                title={
                  window.seconds === 0
                    ? "최근 10분 점유율 변화와 이전 공지·이슈를 함께 추적합니다."
                    : undefined
                }
                onClick={() => setWindowSec(window.seconds)}
                className={`segmented-control-item px-3 py-1.5 text-xs transition-colors ${
                  windowSec === window.seconds
                    ? "segmented-control-item-active bg-background text-foreground"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {window.label}
              </button>
            ))}
          </div>
        }
      />

      {LLM_ENABLED && (
        <HermesNoticeQuestion
          answerScrollable={desktopWorkspace}
          compact={!llmWorkspace}
        />
      )}

      {archiveNotices && archive.error && (
        <div
          className="notice-page-error border border-gold/30 p-3 text-xs text-gold"
          role="alert"
        >
          {archive.error} 마지막으로 확인한 페이지를 유지합니다.{" "}
          <button
            type="button"
            onClick={archive.refresh}
            className="rounded border border-border px-3 py-2"
          >
            다시 확인
          </button>
        </div>
      )}

      {error && (
        <div
          className="notice-page-error flex items-start gap-3 rounded-xl border border-gold/30 bg-gold/10 p-4 text-xs"
          role={desktopWorkspace ? "region" : undefined}
          aria-label={desktopWorkspace ? "실시간 분석 연결 오류" : undefined}
          tabIndex={desktopWorkspace ? 0 : undefined}
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-gold" />
          <div>
            <p className="font-medium text-gold">
              실시간 분석 연결을 확인해 주세요.
            </p>
            <p className="mt-1 text-muted-foreground">{error}</p>
          </div>
        </div>
      )}

      <section className="notice-official-panel glass-card instrument-panel overflow-hidden">
        <div className="notice-official-header instrument-panel-header flex flex-wrap items-center justify-between gap-3 border-b border-border p-4">
          <div className="flex min-w-0 items-start gap-3">
            <span className="panel-title-icon" aria-hidden="true">
              <Bell className="h-4 w-4" />
            </span>
            <div>
              <h3 className="panel-title-with-icon text-base font-semibold">
                공식 공지
              </h3>
              <p className="mt-1 text-xs text-muted-foreground">
                거래소별 공지와 주요 일정을 확인하세요.
              </p>
            </div>
          </div>
          <span
            className={`rounded-lg border px-3 py-1.5 text-xs ${
              activeNoticeSourceErrorCount > 0
                ? "border-loss/30 bg-loss/10 text-loss"
                : pendingNoticeSourceCount > 0 ||
                    noticeData.healthy_source_count !== noticeData.source_count
                  ? "border-gold/30 bg-gold/10 text-gold"
                  : "border-profit/30 bg-profit/10 text-profit"
            }`}
          >
            {activeNoticeSourceErrorCount > 0
              ? "일부 거래소 공지 갱신 지연"
              : sourcesNeedAttention
                ? "일부 거래소 공지 확인 중"
                : "5개 거래소 공지 확인 완료"}
          </span>
        </div>

        <div
          className="notice-source-scroll"
          role={desktopWorkspace ? "region" : undefined}
          aria-label={desktopWorkspace ? "거래소별 공지 확인 상태" : undefined}
          tabIndex={desktopWorkspace ? 0 : undefined}
        >
          {!sourcesNeedAttention && (
            <MobileMoreToggle
              expanded={sourceCardsExpanded}
              onToggle={() => setSourceCardsExpanded(value => !value)}
              moreLabel="거래소별 확인 상태 보기"
            />
          )}
          <div
            className={`notice-source-grid grid gap-2 border-b border-border bg-inset p-4 sm:grid-cols-2 xl:grid-cols-4${sourceCardsFolded ? " mobile-hidden" : ""}`}
          >
            {Object.entries(noticeData.sources).map(([exchange, source]) => (
              <div
                key={exchange}
                data-notice-source={exchange}
                className="notice-source-panel p-3 text-base"
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="inline-flex items-center gap-2 font-semibold">
                    <Radio className="h-3.5 w-3.5 text-primary" />
                    {exchangeLabel(exchange)}
                  </span>
                  <span
                    className={`ui-caption shrink-0 text-right ${
                      noticeSourceHasError(source)
                        ? "loss-text"
                        : ["COMPLETE", "HERMES_30D_ACTIVE"].includes(
                              source.history_status
                            )
                          ? "profit-text"
                          : "gold-text"
                    }`}
                  >
                    {noticeSourceStatusLabel(source)}
                  </span>
                </div>
                {source.last_success_ts > 0 && (
                  <p className="mt-1 text-xs text-muted-foreground">
                    마지막 확인 {noticeSourceTime(source.last_success_ts)}
                  </p>
                )}
                {noticeSourceHasError(source) && (
                  <div className="mt-2 rounded border border-loss/25 bg-loss/10 p-2 text-loss">
                    <p className="font-semibold">
                      {source.error_kind === "BITHUMB_RATE_LIMIT"
                        ? "거래소 요청 제한으로 갱신 대기 중"
                        : "최신 공지를 가져오지 못했습니다."}
                    </p>
                    <p className="mt-1 break-words leading-6">
                      새 공지는 공식 원문에서 확인해 주세요.
                    </p>
                    {source.blocked_until_ts > 0 && (
                      <p className="mt-1">
                        재시도 예정 {noticeSourceTime(source.blocked_until_ts)}
                      </p>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {noticeCount > 0 ? (
          <>
            <div
              className="notice-list-scroll divide-y divide-border/70"
              role={desktopWorkspace ? "region" : undefined}
              aria-label={desktopWorkspace ? "공식 공지 목록" : undefined}
              tabIndex={desktopWorkspace ? 0 : undefined}
            >
              {visibleNotices.map(notice => {
                const noticeKey = `${notice.exchange}-${notice.notice_id}`;
                const open = openNoticeIDs.has(noticeKey);
                return (
                  <details
                    key={noticeKey}
                    open={open}
                    onToggle={event => {
                      const nextOpen = event.currentTarget.open;
                      setOpenNoticeIDs(current => {
                        if (current.has(noticeKey) === nextOpen) return current;
                        const next = new Set(current);
                        if (nextOpen) next.add(noticeKey);
                        else next.delete(noticeKey);
                        return next;
                      });
                    }}
                    className="group border-b border-border/70 last:border-b-0"
                  >
                    <summary className="notice-list-row grid min-w-0 cursor-pointer list-none gap-3 p-5 transition-colors hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none">
                      <div className="notice-list-meta flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1">
                        <p className="text-xs font-semibold">
                          {exchangeLabel(notice.exchange)}
                        </p>
                        <p className="ui-body text-muted-foreground">
                          <NoticeTimestamp notice={notice} />
                        </p>
                      </div>
                      <div className="min-w-0">
                        <p className="notice-list-title ui-title font-semibold">
                          {notice.title || "제목 없음"}
                        </p>
                        <div className="mt-1 flex flex-wrap gap-1.5">
                          <span className="rounded border border-border bg-secondary px-2 py-0.5 text-base text-muted-foreground">
                            {categoryLabel(notice.category)}
                          </span>
                          {notice.within_selected_window && (
                            <span className="rounded border border-primary/30 bg-primary/10 px-2 py-0.5 text-base text-primary">
                              선택한 기간의 공지·일정
                            </span>
                          )}
                        </div>
                      </div>
                      <div className="notice-list-state ui-body flex min-w-0 items-center justify-between gap-3 text-muted-foreground">
                        <span>
                          {noticePipelineLabel(notice.notice_quality_v2) ??
                            noticeAnalysisStatusLabel(
                              notice.analysis_status,
                              notice.escalation_reason,
                              notice.content_source
                            )}
                        </span>
                        <span className="inline-flex shrink-0 items-center gap-2">
                          {open ? "접기" : "자세히 보기"}
                          <ChevronDown
                            className="h-4 w-4 shrink-0 transition-transform group-open:rotate-180"
                            aria-hidden="true"
                          />
                        </span>
                      </div>
                    </summary>
                    {open && (
                      <article className="notice-list-article min-w-0 border-t border-border/70 p-5 ui-body">
                        <div className="min-w-0">
                          <a
                            href={notice.url}
                            target="_blank"
                            rel="noreferrer"
                            className="notice-original-link inline-flex items-center gap-2 font-semibold text-primary"
                          >
                            공식 원문 보기
                            <ExternalLink className="h-3 w-3 shrink-0" />
                          </a>
                          <div className="mt-2 flex flex-wrap gap-1.5">
                            <span className="rounded border border-border bg-secondary px-2 py-0.5 text-base text-muted-foreground">
                              {categoryLabel(notice.category)}
                            </span>
                            {notice.market_context.map(context => (
                              <span
                                key={context.asset}
                                className={`rounded border px-2 py-0.5 text-base ${gapTone(context.gap_type)}`}
                              >
                                {context.asset} · 빗썸{" "}
                                {percent(context.bithumb_capture_pct)} ·{" "}
                                {money(context.domestic_volume_krw)}
                              </span>
                            ))}
                          </div>
                          <NoticePipelineQuality
                            quality={notice.notice_quality_v2}
                          />
                          {notice.summary && (
                            <div className="mt-2 border border-border/80 bg-inset px-3 py-2.5">
                              <p className="text-base font-semibold">
                                공지 요약
                              </p>
                              <p className="notice-reading-text ui-body mt-3 whitespace-pre-line break-words text-muted-foreground">
                                {noticeSummaryDisplayText(notice.summary)}
                              </p>
                            </div>
                          )}
                          {!notice.summary &&
                            notice.analysis_status === "ANALYSIS_FAILED" &&
                            noticeAnalysisStatusDetail(
                              notice.analysis_status,
                              notice.escalation_reason,
                              notice.content_source
                            ) && (
                              <p className="mt-2 break-words text-xs leading-6 text-gold">
                                {noticeAnalysisStatusDetail(
                                  notice.analysis_status,
                                  notice.escalation_reason,
                                  notice.content_source
                                )}
                              </p>
                            )}
                          <details className="mt-2 border border-border/80 bg-inset">
                            <summary className="min-w-0 cursor-pointer list-none px-3 py-2 text-base font-semibold marker:hidden">
                              공지 본문 읽기
                            </summary>
                            <p className="notice-reading-text ui-body break-words border-t border-border px-4 py-4 whitespace-pre-wrap text-muted-foreground">
                              {noticeBodyText(notice) ||
                                "본문을 아직 확인하지 못했습니다. 공식 원문을 확인해 주세요."}
                            </p>
                          </details>
                          {notice.schedules.length > 0 && (
                            <div className="notice-schedule-list mt-2 divide-y divide-gold/15 overflow-hidden rounded-md border border-gold/20 bg-gold/10 text-xs">
                              {uniqueNoticeSchedules(notice.schedules).map(
                                (schedule, index) => (
                                  <div
                                    key={`${schedule.kind}-${schedule.start_ts_ms}-${index}`}
                                    className="grid min-w-0 gap-x-2 gap-y-0.5 px-2.5 py-1.5 sm:grid-cols-[4.5rem_auto_minmax(0,1fr)] sm:items-center"
                                  >
                                    <p className="font-semibold text-gold">
                                      {scheduleLabel(schedule.kind)}
                                    </p>
                                    <p className="min-w-0 font-mono">
                                      {noticeScheduleRange(schedule)}
                                    </p>
                                    <p
                                      className="min-w-0 truncate text-muted-foreground"
                                      title={`근거: ${schedule.evidence}`}
                                    >
                                      근거: {schedule.evidence}
                                    </p>
                                  </div>
                                )
                              )}
                            </div>
                          )}
                          {notice.image_urls.length > 0 && (
                            <div className="mt-2">
                              <p className="text-base text-muted-foreground">
                                본문 이미지 {notice.image_urls.length}개 · 글자
                                읽기{" "}
                                {
                                  notice.image_analysis.filter(
                                    image => image.status === "ANALYZED"
                                  ).length
                                }
                                개
                                {notice.image_text
                                  ? ` · ${notice.image_text.slice(0, 160)}`
                                  : " · 판독 상태를 확인하세요"}
                              </p>
                              {notice.image_consistency && (
                                <div
                                  className={
                                    "mt-2 border px-2.5 py-2 text-xs " +
                                    (notice.image_consistency.status ===
                                    "CONFLICT"
                                      ? "border-loss/50 bg-loss/10"
                                      : "border-border/70 bg-inset")
                                  }
                                >
                                  <p className="font-semibold">
                                    이미지·본문{" "}
                                    {imageConsistencyLabel(
                                      notice.image_consistency
                                    )}
                                    {notice.image_consistency.semantic_types
                                      .length > 0
                                      ? ` · ${notice.image_consistency.semantic_types.map(imageSemanticLabel).join(" · ")}`
                                      : ""}
                                  </p>
                                  <p className="ui-body mt-1 text-muted-foreground">
                                    {notice.image_consistency.reason}
                                  </p>
                                  {(notice.image_consistency.unverified_signals
                                    ?.length ?? 0) > 0 && (
                                    <p className="mt-1 leading-6 text-gold">
                                      숫자·문구 직접 확인 필요:{" "}
                                      {notice.image_consistency.unverified_signals
                                        ?.map(imageSignalLabel)
                                        .join(" · ")}
                                    </p>
                                  )}
                                </div>
                              )}
                              {notice.image_analysis.length > 0 && (
                                <details className="mt-2 border border-border/80 bg-inset">
                                  <summary className="cursor-pointer list-none px-3 py-2 text-base font-semibold marker:hidden">
                                    이미지 내용 보기 ·{" "}
                                    {notice.image_analysis.length}개
                                  </summary>
                                  <div className="divide-y divide-border border-t border-border">
                                    {notice.image_analysis.map(
                                      (image, index) => (
                                        <div
                                          key={`${image.url}-${index}`}
                                          className="space-y-1 px-3 py-3 text-xs"
                                        >
                                          <div className="flex flex-wrap items-center justify-between gap-2">
                                            {image.url ? (
                                              <a
                                                href={image.url}
                                                target="_blank"
                                                rel="noreferrer"
                                                className="font-semibold hover:text-primary"
                                              >
                                                이미지 {index + 1} 원문 열기{" "}
                                                <ExternalLink className="inline h-3 w-3" />
                                              </a>
                                            ) : (
                                              <span className="font-semibold">
                                                이미지 {index + 1} 링크 없음
                                              </span>
                                            )}
                                            <span className="text-muted-foreground">
                                              {imageAnalysisStatusLabel(
                                                image.status
                                              )}{" "}
                                            </span>
                                          </div>
                                          <p className="ui-body break-words whitespace-pre-wrap text-muted-foreground">
                                            {image.text ||
                                              "이미지에서 글자를 읽지 못했습니다."}
                                          </p>
                                          {image.failure_reason && (
                                            <p className="leading-6 text-loss">
                                              이미지 내용을 확인하지 못했습니다.
                                              원본 이미지를 확인해 주세요.
                                            </p>
                                          )}
                                        </div>
                                      )
                                    )}
                                  </div>
                                </details>
                              )}
                            </div>
                          )}
                          <NoticeReaction notice={notice} />
                        </div>
                      </article>
                    )}
                  </details>
                );
              })}
            </div>
            <div className="notice-pagination flex items-center justify-between border-t border-border px-4 py-3 text-xs text-muted-foreground">
              <span>
                {archiveNotices ? "최근 30일 전체" : "전체"}{" "}
                {noticeCount.toLocaleString()}건 · {noticePage + 1}/
                {noticePageCount} 페이지
              </span>
              <div className="flex gap-2">
                <button
                  type="button"
                  disabled={
                    noticePage === 0 || (archiveNotices && archive.loading)
                  }
                  onClick={() =>
                    archiveNotices
                      ? archive.goTo(noticePage - 1)
                      : setNoticePage(value => Math.max(0, value - 1))
                  }
                  className="rounded-md border border-border px-3 py-1.5 disabled:opacity-40"
                >
                  이전
                </button>
                <button
                  type="button"
                  disabled={
                    noticePage + 1 >= noticePageCount ||
                    (archiveNotices && archive.loading)
                  }
                  onClick={() =>
                    archiveNotices
                      ? archive.goTo(noticePage + 1)
                      : setNoticePage(value =>
                          Math.min(noticePageCount - 1, value + 1)
                        )
                  }
                  className="rounded-md border border-border px-3 py-1.5 disabled:opacity-40"
                >
                  다음
                </button>
              </div>
            </div>
          </>
        ) : (
          <p className="notice-list-empty p-6 text-center text-xs text-muted-foreground">
            {archiveNotices
              ? "현재 저장 자료에서 최근 30일에 게시된 공지가 확인되지 않았습니다. 소스별 수집 상태도 확인해 주세요."
              : `최근 ${noticeData.history_days}일 공지가 없거나 이전 공지의 최초 수집을 기다리고 있습니다.`}
          </p>
        )}
      </section>
    </div>
  );
}
