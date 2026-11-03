import {
  lazy,
  Suspense,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Bell, ExternalLink, Moon, RefreshCw, Sun } from "lucide-react";
import { useTheme } from "@/contexts/ThemeContext";
import { useWholeNoticeArchive } from "@/hooks/useNoticeArchive";
import type { ExchangeNotice } from "@/lib/market-share-types";
import { useIndexRefresh } from "@/lib/use-index-refresh";
import {
  getIndexDay,
  getNoticePortal,
  type NoticePortalData,
} from "@/lib/api-client";
import { currentKSTDay, type IndexDayResponse } from "@/lib/index-day";
import {
  countPortalNotices,
  filterPortalNotices,
  noticeBodyText,
  noticeHourlyAmount,
  noticeHourlySummary,
  noticeLiveVolume,
  noticeQuantity,
} from "@/lib/notice-portal";
import { exchangeNameKo } from "@/lib/exchanges";
import { formatKRW } from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import { noticeTimestampLines } from "@/lib/market-share-reaction";
import { noticeAnalysisStatusLabel } from "@/lib/notice-analysis-status";
import {
  noticeCategoryLabel,
  noticeScheduleLabel,
} from "@/lib/market-share-labels";
import {
  formatNoticeScheduleRange,
  uniqueNoticeSchedules,
} from "@/lib/notice-time";
import "./notice-portal.css";

const Timeline = lazy(() =>
  import("@/components/NoticeTimeline").then(module => ({
    default: module.NoticeTimelinePanel,
  }))
);
const money = (value: number | null | undefined) =>
  value == null || !Number.isFinite(value) ? "—" : formatKRW(value);
const time = (value: number) => formatMarketTimeMS(value, "date-time");
const PAGE_SIZE = 20;

function NoticeCard({
  notice,
  selectedAsset,
  onSelectAsset,
}: {
  notice: ExchangeNotice;
  selectedAsset: string;
  onSelectAsset: (asset: string) => void;
}) {
  const body = noticeBodyText(notice);
  return (
    <article className="np-notice">
      <div className="np-notice-meta">
        <strong>{exchangeNameKo(notice.exchange)}</strong>
        <span className="np-tag">{noticeCategoryLabel(notice.category)}</span>
        <span>{noticeTimestampLines(notice).join(" · ")}</span>
      </div>
      <a
        className="np-notice-title"
        href={notice.url}
        target="_blank"
        rel="noreferrer"
      >
        {notice.title}
        <ExternalLink size={14} aria-label="공식 원문 새 창" />
      </a>
      {notice.schedules.length > 0 && (
        <ul className="np-schedules">
          {uniqueNoticeSchedules(notice.schedules).map((schedule, index) => (
            <li key={`${schedule.kind}-${schedule.start_ts_ms}-${index}`}>
              <span>{noticeScheduleLabel(schedule.kind)}</span>
              {formatNoticeScheduleRange(schedule)}
            </li>
          ))}
        </ul>
      )}
      <div className="notice-asset-actions">
        {notice.assets.length > 0 &&
          notice.assets.map(symbol => (
            <button
              type="button"
              key={symbol}
              aria-pressed={selectedAsset === symbol}
              aria-label={`${symbol} 거래량 보기`}
              onClick={() => onSelectAsset(symbol)}
            >
              {symbol}
              <span> 거래량</span>
            </button>
          ))}
      </div>
      <details>
        <summary>
          본문 보기
          <small>
            {noticeAnalysisStatusLabel(
              notice.analysis_status,
              notice.escalation_reason,
              notice.content_source
            )}
          </small>
        </summary>
        <p>
          {body || "아직 수집된 본문이 없습니다. 공식 원문을 확인해 주세요."}
        </p>
      </details>
    </article>
  );
}

export default function NoticePortal() {
  const { theme, toggleTheme } = useTheme();
  const archive = useWholeNoticeArchive();
  const [analysis, setAnalysis] = useState<NoticePortalData | null>(null);
  const [error, setError] = useState("");
  const monitor = analysis?.rolling_24h;
  const [asset, setAsset] = useState("");
  const [exchange, setExchange] = useState("all");
  const [category, setCategory] = useState("all");
  const [query, setQuery] = useState("");
  const [assetOnly, setAssetOnly] = useState(false);
  const [page, setPage] = useState(0);
  const [hourly, setHourly] = useState<{
    asset: string;
    data: IndexDayResponse;
  } | null>(null);
  const [hourError, setHourError] = useState("");
  const [hourLoading, setHourLoading] = useState(false);
  const [retry, setRetry] = useState(0);
  const [hourRetry, setHourRetry] = useState(0);
  const volumeRef = useRef<HTMLElement>(null);
  const current = hourly?.asset === asset ? hourly.data : null;
  const hourlySummary = noticeHourlySummary(current, asset);
  useIndexRefresh(setRetry);
  useIndexRefresh(setHourRetry, hourlySummary.refreshMS);
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    const deadline = setTimeout(() => controller.abort(), 12000);
    getNoticePortal(controller.signal)
      .then(value => {
        if (!active) return;
        setAnalysis(value);
        setError("");
      })
      .catch(() => {
        if (active) setError("거래량 자료를 갱신하지 못했습니다.");
      })
      .finally(() => clearTimeout(deadline));
    return () => {
      active = false;
      clearTimeout(deadline);
      controller.abort();
    };
  }, [retry]);
  const allNotices = useMemo(
    () => archive.data?.snapshot.items ?? [],
    [archive.data]
  );
  const filterAsset = assetOnly && asset ? asset : "all";
  // Typing stays responsive; the whole-archive body search follows at low priority.
  const searchQuery = useDeferredValue(query);
  const items = useMemo(
    () =>
      filterPortalNotices(allNotices, {
        exchange,
        category,
        query: searchQuery,
        asset: filterAsset,
      }),
    [allNotices, exchange, category, searchQuery, filterAsset]
  );
  // A changed condition starts again from its first page.
  useEffect(() => setPage(0), [exchange, category, searchQuery, filterAsset]);
  const pages = Math.max(1, Math.ceil(items.length / PAGE_SIZE));
  const currentPage = Math.min(page, pages - 1);
  const pageItems = items.slice(
    currentPage * PAGE_SIZE,
    (currentPage + 1) * PAGE_SIZE
  );
  const filtersActive =
    exchange !== "all" ||
    category !== "all" ||
    query.trim() !== "" ||
    assetOnly;
  const resetFilters = () => {
    setExchange("all");
    setCategory("all");
    setQuery("");
    setAssetOnly(false);
  };
  useEffect(() => {
    if (!asset) {
      const first = archive.data?.snapshot.items.find(
        notice => notice.assets.length
      )?.assets[0];
      if (first) setAsset(first);
    }
  }, [archive.data, asset]);
  useEffect(() => {
    document.title = "공지 타임라인 · MarketGO";
  }, []);
  useEffect(() => {
    if (!asset) return;
    const controller = new AbortController();
    let active = true;
    const deadline = setTimeout(() => controller.abort(), 12000);
    setHourLoading(true);
    setHourError("");
    getIndexDay(currentKSTDay(), controller.signal, {
      assets: asset,
      quote: "all",
      venue: "all",
    })
      .then(value => {
        if (active) setHourly({ asset, data: value });
      })
      .catch(() => {
        if (active) setHourError("공식 시간봉 자료를 불러오지 못했습니다.");
      })
      .finally(() => {
        clearTimeout(deadline);
        if (active) setHourLoading(false);
      });
    return () => {
      active = false;
      clearTimeout(deadline);
      controller.abort();
    };
  }, [asset, hourRetry]);
  const selectAsset = (next: string) => {
    setAsset(next);
    // The desktop panel stays in view (sticky); scroll only when it is not.
    const panel = volumeRef.current?.getBoundingClientRect();
    if (panel && (panel.top < 0 || panel.top > window.innerHeight * 0.6))
      volumeRef.current?.scrollIntoView({ block: "start", behavior: "smooth" });
  };
  const markets = hourlySummary.rows;
  const live = analysis?.assets?.find(row => row.asset === asset);
  const context = analysis?.context;
  const liveVolume = noticeLiveVolume(
    live,
    Boolean(context?.selected_window_complete)
  );
  const sources = Object.entries(archive.data?.snapshot.sources ?? {});
  const exchangeCounts = useMemo(
    () => countPortalNotices(allNotices, "exchange"),
    [allNotices]
  );
  const categoryCounts = useMemo(
    () => countPortalNotices(allNotices, "category"),
    [allNotices]
  );
  return (
    <div className="notice-portal">
      <header className="np-header">
        <a className="np-brand" href="/notices/">
          <Bell size={22} />
          <span>
            MarketGo <b>NOTICE</b>
          </span>
        </a>
        <div className="np-header-actions">
          <a href="https://share.realmi.net/#index">
            시장 인덱스 <ExternalLink size={14} />
          </a>
          <button
            type="button"
            aria-label={
              theme === "dark" ? "라이트 테마로 전환" : "다크 테마로 전환"
            }
            onClick={toggleTheme}
          >
            {theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
          </button>
        </div>
      </header>
      <main>
        <div className="np-source-bar">
          <span>
            최근 30일{" "}
            <strong>
              {archive.data?.total.toLocaleString("ko-KR") ?? "—"}건
            </strong>
          </span>
          {sources.map(([name, source]) => (
            <span
              key={name}
              className={source.error ? "np-source-error" : ""}
              title={
                source.error
                  ? "최신 공지를 가져오지 못했습니다. 공식 원문을 확인해 주세요."
                  : "최근 수집 확인"
              }
            >
              <i />
              {exchangeNameKo(name)} {source.error ? "확인 필요" : ""}
            </span>
          ))}
          <small>
            {archive.data
              ? `목록 조회 ${time(archive.data.as_of_ms)}`
              : "공지 불러오는 중"}
          </small>
          <button
            type="button"
            className="np-refresh"
            disabled={archive.loading}
            onClick={() => {
              archive.refresh();
              setRetry(value => value + 1);
              setHourRetry(value => value + 1);
            }}
          >
            <RefreshCw size={16} />
            새로고침
          </button>
        </div>
        {archive.error && (
          <p role="alert" className="np-alert">
            {archive.error}
          </p>
        )}
        <section className="np-timeline np-panel">
          {monitor ? (
            <Suspense
              fallback={
                <p className="np-placeholder">타임라인을 불러오는 중입니다.</p>
              }
            >
              <Timeline
                notices={monitor.evidence.domestic_notices}
                asOfMs={monitor.as_of_ms}
                onAssetSelect={selectAsset}
              />
            </Suspense>
          ) : (
            <p className="np-placeholder" role="status">
              {error || "저장된 공지 일정을 불러오는 중입니다."}
            </p>
          )}
        </section>
        <div className="np-workspace">
          <section className="np-panel np-notices">
            <div className="np-panel-heading">
              <div>
                <h2>최근 공식 공지</h2>
                <p>
                  최근 30일 공지 전체에서 거래소·분류·검색어로 찾을 수 있습니다.
                </p>
              </div>
            </div>
            <div className="np-exchange-chips" role="group" aria-label="거래소">
              <button
                type="button"
                aria-pressed={exchange === "all"}
                onClick={() => setExchange("all")}
              >
                전체 <span>{allNotices.length}</span>
              </button>
              {exchangeCounts.map(([name, count]) => (
                <button
                  type="button"
                  key={name}
                  aria-pressed={exchange === name}
                  onClick={() => setExchange(name)}
                >
                  {exchangeNameKo(name)} <span>{count}</span>
                </button>
              ))}
            </div>
            <div className="np-filters">
              <label>
                <span>분류</span>
                <select
                  aria-label="공지 분류"
                  value={category}
                  onChange={event => setCategory(event.target.value)}
                >
                  <option value="all">전체 분류</option>
                  {categoryCounts.map(([name, count]) => (
                    <option key={name} value={name}>
                      {noticeCategoryLabel(name)} ({count})
                    </option>
                  ))}
                </select>
              </label>
              <label className="np-search">
                <span>검색</span>
                <input
                  type="search"
                  aria-label="공지 검색"
                  placeholder="제목, 코인, 본문"
                  value={query}
                  onChange={event => setQuery(event.target.value)}
                />
              </label>
              <label className="np-check">
                <input
                  type="checkbox"
                  checked={assetOnly}
                  disabled={!asset}
                  onChange={event => setAssetOnly(event.target.checked)}
                />
                <span>{asset ? `${asset} 공지만` : "선택 코인 공지만"}</span>
              </label>
            </div>
            <p className="np-result-count" role="status">
              {archive.data
                ? `${items.length.toLocaleString("ko-KR")}건${filtersActive ? ` / 전체 ${allNotices.length.toLocaleString("ko-KR")}건` : ""}`
                : "공지 불러오는 중"}
              {filtersActive && (
                <button type="button" onClick={resetFilters}>
                  조건 지우기
                </button>
              )}
            </p>
            <div className="np-notice-list">
              {pageItems.map(notice => (
                <NoticeCard
                  key={`${notice.exchange}:${notice.notice_id}`}
                  notice={notice}
                  selectedAsset={asset}
                  onSelectAsset={selectAsset}
                />
              ))}
            </div>
            {!pageItems.length && (
              <p className="np-placeholder">
                {archive.loading
                  ? "공지를 불러오는 중입니다."
                  : "조건과 일치하는 공지가 없습니다."}
              </p>
            )}
            <footer className="np-pagination">
              <button
                type="button"
                disabled={currentPage === 0}
                onClick={() => setPage(currentPage - 1)}
              >
                이전
              </button>
              <span>
                {currentPage + 1} / {pages} 페이지
              </span>
              <button
                type="button"
                disabled={currentPage + 1 >= pages}
                onClick={() => setPage(currentPage + 1)}
              >
                다음
              </button>
            </footer>
          </section>
          <aside
            className="np-panel np-volume"
            ref={volumeRef}
            aria-label="선택한 코인 거래량"
          >
            <div className="np-panel-heading">
              <div>
                <span className="np-eyebrow">선택한 코인</span>
                <h2>
                  {asset || "코인을 선택하세요"}{" "}
                  {asset && <small>거래량</small>}
                </h2>
              </div>
              <span className={error ? "np-source-error" : "np-live"}>
                {error ? "갱신 확인 필요" : "1분마다 갱신"}
              </span>
            </div>
            <div className="np-volume-body">
              <h3>최근 10분 · 원화 마켓 관측값</h3>
              <div className="np-metrics">
                <div>
                  <span>거래 수량</span>
                  <strong>{noticeQuantity(liveVolume.quantity)}</strong>
                  <small>{asset || "코인"}</small>
                </div>
                <div>
                  <span>거래금액</span>
                  <strong>{money(liveVolume.amount)}</strong>
                </div>
              </div>
              <p className="np-caption">
                국내 거래소에서 직접 수집한 체결 기준입니다.
                {live &&
                  ` 관측 체결 ${live.domestic_trade_count.toLocaleString("ko-KR")}건.`}
              </p>
              {context && (
                <p className="np-caption">
                  {time(context.current_interval_start * 1000)} –{" "}
                  {time(context.current_interval_end * 1000)}
                  <br />
                  {context.selected_window_complete
                    ? "관측 구간 확인됨"
                    : `일부 체결만 관측 · 연속 확인 ${context.selected_window_coverage_sec} / ${context.selected_window_required_sec}초. 10분 전체 거래량이 아닙니다.`}
                </p>
              )}
              {liveVolume.quantity == null && asset && (
                <p className="np-caption">
                  선택한 코인의 현재 구간 체결 자료가 없습니다.
                </p>
              )}
              {error && (
                <p className="np-alert" role="alert">
                  {error} 마지막으로 확인한 자료를 표시합니다.
                </p>
              )}
              <div className="np-hourly">
                <h3>
                  {hourlySummary.partial
                    ? "공식 시간봉 · 확보된 거래금액"
                    : "최근 24시간 · 공식 거래금액"}
                </h3>
                <strong className="np-hourly-total">
                  {money(hourlySummary.amount)}
                </strong>
                <p className="np-caption">
                  {hourLoading
                    ? "시간봉 확인 중"
                    : hourError || hourlySummary.label}
                  {hourlySummary.partial &&
                    hourlySummary.amount != null &&
                    " · 24시간 전체 금액이 아닙니다."}
                  {hourlySummary.refreshMS === 15_000 &&
                    " 15초마다 갱신합니다."}
                </p>
                <p className="np-caption">
                  원화·BTC·USDT 마켓의 원화 환산 금액입니다. 거래 수량과는
                  다릅니다.
                </p>
                {current && (
                  <p className="np-caption">
                    {time(current.start * 1000)} – {time(current.end * 1000)}
                  </p>
                )}
                <div className="np-market-table">
                  <table>
                    <thead>
                      <tr>
                        <th>거래소 / 마켓</th>
                        <th>거래금액</th>
                        <th>시간봉</th>
                      </tr>
                    </thead>
                    <tbody>
                      {markets.map(row => (
                        <tr key={`${row.exchange}:${row.market}`}>
                          <th>
                            {exchangeNameKo(row.exchange)}
                            <small>{row.market}</small>
                          </th>
                          <td>{money(noticeHourlyAmount(row))}</td>
                          <td>
                            {row.amount_hours} / 24
                            {noticeHourlyAmount(row) == null && (
                              <small>금액 미확인</small>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {!markets.length && !hourLoading && (
                  <p className="np-caption">확인된 시간봉 마켓이 없습니다.</p>
                )}
              </div>
            </div>
          </aside>
        </div>
        <footer className="np-footer">
          공지는 거래소 공식 자료입니다. 거래량은 표시된 집계 기간의 값이며
          공지에 의한 변화로 단정하지 않습니다.
        </footer>
      </main>
    </div>
  );
}
