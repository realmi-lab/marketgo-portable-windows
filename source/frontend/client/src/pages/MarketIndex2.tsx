import {
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { createPortal } from "react-dom";

import { usePolled } from "@/screens/data";
import { windowLabel } from "@/lib/screen-copy";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import { getIndexDay, getRealtimeIndex } from "@/lib/api-client";
import { useFollowKSTToday, useIndexRefresh } from "@/lib/use-index-refresh";
import { indexRefreshInterval } from "@/lib/index-refresh";
import {
  INDEX_EXCHANGES,
  currentKSTDay,
  indexCellAmount,
  indexCellShare,
  type DomesticIndexCell,
  type IndexDayResponse,
} from "@/lib/index-day";
import "./market-index2.css";
import { useIndex2Filters } from "./index2-filters";
import { formatSharePercent } from "@/lib/formatters";
import { IndexDataStatus } from "@/components/IndexDataStatus";
import { indexHasConfirmedAmount } from "@/lib/index-status";
import { useLiveRowMotion } from "./use-live-row-motion";

const EXCHANGE_NAME = new Map(INDEX_EXCHANGES);
const FIRST_INDEX_DAY = "2026-09-01";
const TEXTURES = ["solid", "outline", "slash", "backslash", "dots"] as const;

function SortHeading({ label, direction, onClick, className }: {
  label: string; direction?: boolean; onClick: () => void; className?: string;
}) {
  return <th className={className} aria-sort={direction == null ? "none" : direction ? "ascending" : "descending"}>
    <button type="button" onClick={onClick}><span className="index2-sort-label">{label}</span> <span className="index2-sort-arrow">{direction == null ? "↕" : direction ? "↑" : "↓"}</span></button>
  </th>;
}

function formatKRW(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value >= 1e12) return `${(value / 1e12).toFixed(3)}조 원`;
  if (value >= 1e8) return `${(value / 1e8).toLocaleString("ko-KR", { maximumFractionDigits: 2 })}억 원`;
  if (value > 0 && value < 10_000) return "1만 원 미만";
  return `${Math.round(value).toLocaleString("ko-KR")}원`;
}

// Two decimals, three only when two would show 0.00; a share above 100% is
// shown as inconsistent data (formatSharePercent).
function formatShare(value: number | null | undefined): string {
  return formatSharePercent(value);
}

// Rows drawn at first and per "더 보기"; all 600+ rows made ~14,000 elements.
const INDEX2_ROW_STEP = 100;

type BarSegment = {
  exchange: string;
  share: number | null;
};

function TextureBar({
  segments,
  inverse = false,
  label,
  large = false,
  initials = false,
}: {
  segments: BarSegment[];
  inverse?: boolean;
  label: string;
  large?: boolean;
  initials?: boolean;
}) {
  const visible = segments.filter(
    segment => segment.share != null && segment.share > 0
  );
  return (
    <div
      className={`index2-bar${large ? " index2-bar-large" : ""}${inverse ? " index2-bar-inverse" : ""}`}
      role="img"
      data-initials={initials ? "" : undefined}
      aria-label={label}
    >
      {visible.map(segment => {
        const textureIndex = INDEX_EXCHANGES.findIndex(
          ([exchange]) => exchange === segment.exchange
        );
        return (
          <span
            key={segment.exchange}
            data-exchange={segment.exchange}
            className={`index2-texture index2-texture-${TEXTURES[Math.max(0, textureIndex)]}${inverse ? " index2-texture-inverse" : ""}`}
            style={{ width: `${segment.share}%`, ...(initials ? { "--index2-exchange-color": `var(--exchange-${segment.exchange})` } : {}) }}
            title={`${EXCHANGE_NAME.get(segment.exchange as (typeof INDEX_EXCHANGES)[number][0]) ?? segment.exchange} ${formatShare(segment.share)}`}
          >
            {initials && <span className="index2-segment-initial" aria-hidden="true">{(EXCHANGE_NAME.get(segment.exchange as (typeof INDEX_EXCHANGES)[number][0]) ?? segment.exchange).slice(0, 1)}</span>}
          </span>
        );
      })}
      {!visible.length && <span className="index2-bar-empty">자료 확인 중</span>}
    </div>
  );
}

type Index2Asset = {
  base: string;
  volume_krw: number | null;
  share_pct: number | null;
  status: string;
  exchanges: Array<{ exchange: string; share_pct: number | null; cell?: DomesticIndexCell }>;
};

function cellStatus(cell: DomesticIndexCell | undefined): string {
  return !cell ? "자료 없음" : cell.unavailable ? "미제공 포함" : cell.partial ? "일부 자료" : "확인됨";
}

function assetSegments(asset: Index2Asset): BarSegment[] {
  return asset.exchanges.map(row => ({
    exchange: row.exchange,
    share: row.share_pct,
  }));
}

export default function MarketIndex2({ showVisual = true, showStatus = true, showShareSort = true, showNoticeFilter = true, showTableIntro = true, defaultDecimals = 4, showAmountUnit = false, showCompositionInitials = false, fitColumns = false, refreshVersion = 0, onLoadingChange, realtimeWindow, referenceNowMS, exchangeControl, lookbackDays, toolbarHost, storageNote }: { showVisual?: boolean; showStatus?: boolean; showShareSort?: boolean; showNoticeFilter?: boolean; showTableIntro?: boolean; defaultDecimals?: number; showAmountUnit?: boolean; showCompositionInitials?: boolean; fitColumns?: boolean; refreshVersion?: number; onLoadingChange?: (loading: boolean) => void; realtimeWindow?: number; referenceNowMS?: number; exchangeControl?: { value: string; onChange: (value: string) => void; requestKey: number }; lookbackDays?: number; toolbarHost?: HTMLElement | null; storageNote?: string } = {}) {
  const [day, setDay] = useState(() => currentKSTDay(referenceNowMS ?? Date.now()));
  const [data, setData] = useState<IndexDayResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [retry, setRetry] = useState(0);
  const [updated, setUpdated] = useState(0);
  const [filterRetry, setFilterRetry] = useState(0);
  useIndexRefresh(setFilterRetry);
  useFollowKSTToday(day, setDay, referenceNowMS === undefined ? undefined : currentKSTDay(referenceNowMS));
  const [view, setView] = useState<"coins" | "exchanges">("coins");
  const liveMode = realtimeWindow !== undefined && view === "exchanges";
  const live = usePolled(liveMode ? `realtime-index:${realtimeWindow}:${refreshVersion}` : null, signal => getRealtimeIndex(realtimeWindow ?? 10, signal), 1000);
  useEffect(() => { onLoadingChange?.(liveMode ? live.loading : loading); }, [liveMode, live.loading, loading, onLoadingChange]);
  const [query, setQuery] = useState("");
  const [showAll, setShowAll] = useState(false);
  const [rowLimit, setRowLimit] = useState(INDEX2_ROW_STEP);
  const tableScrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (exchangeControl?.requestKey === undefined) return;
    if (realtimeWindow === undefined) setView("coins");
    setRowLimit(INDEX2_ROW_STEP);
    tableScrollRef.current?.scrollTo({ top: 0 });
  }, [exchangeControl?.requestKey]);
  const today = currentKSTDay(referenceNowMS ?? Date.now());
  const isToday = day === today;
  const firstDay = lookbackDays == null ? FIRST_INDEX_DAY : currentKSTDay((referenceNowMS ?? Date.now()) - (lookbackDays - 1) * 86_400_000);
  useEffect(() => {
    if (day < firstDay) setDay(firstDay);
  }, [day, firstDay]);

  useEffect(() => {
    if (liveMode) return;
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setError("");
    const timeout = window.setTimeout(() => controller.abort("timeout"), 12_000);
    getIndexDay(day, controller.signal, undefined, isToday ? realtimeWindow !== undefined ? "ticker_24h" : "hourly_rolling" : undefined)
      .then(value => {
        if (active) {
          setData(value);
          setUpdated(Date.now());
        }
      })
      .catch(caught => {
        if (!active) return;
        setError(
          controller.signal.reason === "timeout"
            ? "조회 시간이 길어졌습니다. 다시 시도해 주세요."
            : caught instanceof Error
              ? caught.message
              : "인덱스 자료를 불러오지 못했습니다."
        );
      })
      .finally(() => {
        window.clearTimeout(timeout);
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [day, retry, isToday, refreshVersion, liveMode, realtimeWindow]);

  useIndexRefresh(setRetry, indexRefreshInterval(data?.day === day ? data : null));

  const current = liveMode ? live.data ?? null : data?.day === day ? data : null;
  const filters = useIndex2Filters(current, query, () => setQuery(""), filterRetry + refreshVersion, showNoticeFilter, defaultDecimals, showTableIntro, fitColumns, exchangeControl);
  const grid = filters.grid;
  const amountFormatter = useMemo(() => new Intl.NumberFormat("ko-KR", { minimumFractionDigits: filters.decimals, maximumFractionDigits: filters.decimals }), [filters.decimals]);
  const amount = (value: number | null | undefined) => value == null ? "—" : liveMode ? value >= 1e8 ? `${amountFormatter.format(value / 1e8)}억` : value >= 1e4 ? `${amountFormatter.format(value / 1e4)}만` : value > 0 && value < 1 ? "1원 미만" : `${Math.round(value).toLocaleString("ko-KR")}원` : `${amountFormatter.format(value / 1e8)}${showAmountUnit ? "억" : ""}`;
  const exchanges = INDEX_EXCHANGES.map(([exchange]) => ({
    exchange,
    volume_krw: grid?.totals[exchange]?.total,
    share_pct: indexCellShare(grid?.totals[exchange]),
    status: cellStatus(grid?.totals[exchange]),
  }));
  const totalVolume = indexHasConfirmedAmount(current) ? grid?.totals.domestic?.total : null;
  const exchangeSegments = exchanges.map(exchange => ({
    exchange: exchange.exchange,
    share: exchange.share_pct,
  }));
  const assets: Index2Asset[] = filters.rows.map(coin => ({
    base: coin.base,
    volume_krw: coin.cells.domestic?.total ?? null,
    share_pct: coin.cells.domestic?.share ?? null,
    status: cellStatus(coin.cells.domestic),
    exchanges: INDEX_EXCHANGES.map(([exchange]) => ({
      exchange,
      share_pct: indexCellShare(coin.cells[exchange]),
      cell: coin.cells[exchange],
    })),
  }));
  const filteredAssets = assets;
  const visibleAssets = filteredAssets.slice(0, rowLimit);
  useLiveRowMotion(tableScrollRef, liveMode, visibleAssets.map(asset => asset.base).join(","));
  const gridAssets = showAll ? filteredAssets : filteredAssets.slice(0, 8);

  const dateControl = (<label className="index2-date">
            <span>조회일</span>
            <input
              type="date"
              aria-label="인덱스2 조회 날짜"
              value={liveMode ? today : day}
              disabled={liveMode}
              title={liveMode ? "실시간 보기는 현재 선택한 구간을 사용합니다." : undefined}
              min={firstDay}
              max={today}
              onInput={event => {
                const value = event.currentTarget.value;
                if (value && (value < firstDay || value > today)) event.currentTarget.value = day;
              }}
              onChange={event => {
                if (event.target.value >= firstDay && event.target.value <= today) {
                  setDay(event.target.value);
                  setRowLimit(INDEX2_ROW_STEP);
                  setShowAll(false);
                } else {
                  event.currentTarget.value = day;
                }
              }}
            />
          </label>);
  return (
    <>
    {toolbarHost && createPortal(<>
      {dateControl}
      <div className="portable-total" title={current?.storage_mode === "TICKER_24H" ? `5개 거래소 공식 KRW 마켓 최근 24시간 티커 · ${formatMarketTimeMS(current.end * 1000, "time-seconds")} 기준${current.status !== "COMPLETE" ? " · 일부 거래소·마켓 미확인" : ""}` : undefined}><span>{liveMode ? `${windowLabel(realtimeWindow ?? 10)} 실시간 거래금액` : current?.storage_mode === "TICKER_24H" ? "국내 24시간 거래대금" : "국내 수집 거래금액"}</span><strong>{amount(totalVolume)}{showAmountUnit ? "" : " 억 원"}</strong></div>
      <ul className="portable-exchange-shares" aria-label="거래소별 점유율">
        {INDEX_EXCHANGES.map(([exchange, name]) => {
          const row = exchanges.find(row => row.exchange === exchange);
          const share = row?.share_pct;
          return <li key={exchange} data-exchange={exchange} data-unavailable={share == null ? "" : undefined}>
            <span className="portable-exchange-name"><i aria-hidden="true" style={{ backgroundColor: `var(--exchange-${exchange})` }} />{name}</span>
            <b>{formatShare(share)}</b>
            <span className="portable-exchange-volume" title={`${name} 거래금액`} aria-label={`${name} 거래금액 ${amount(row?.volume_krw)}`}>{amount(row?.volume_krw)}</span>
          </li>;
        })}
      </ul>
    </>, toolbarHost)}
    <section className="index2-terminal" aria-label={toolbarHost !== undefined ? "코인 인덱스" : undefined} aria-labelledby={toolbarHost === undefined ? "market-index2-title" : undefined}>
      {toolbarHost === undefined && <aside className="index2-pane index2-overview">
        <div className="index2-scroll index2-overview-content">
          <h2 id="market-index2-title" className="index2-ascii" aria-label="MarketGo 인덱스 2">
            {`/ \\ / \\ / \\ / \\ / \\\n+ M A R K E T G O\n+ I N D E X  0 2\n+\\_/ \\_/ \\_/ \\_/ \\_/`}
          </h2>
          <p className="index2-copy">
            국내 5개 거래소의 공식 거래금액과 코인별 분포입니다.{" "}
            {isToday ? "최근 공식 1시간봉 24개의 합계를 1시간마다 갱신합니다." : "선택한 날짜의 공식 시간봉 집계값을 사용합니다."}
          </p>
          {dateControl}
          {storageNote !== "" && <p className="index2-source">{storageNote ?? "2026년 9월 1일부터 조회할 수 있습니다."}</p>}
          <div className="index2-divider" aria-hidden="true">----------------------------------------</div>
          <div className="index2-summary-heading">
            <span>국내 수집 거래금액</span>
            <strong>{amount(totalVolume)}{showAmountUnit ? "" : " 억 원"}</strong>
          </div>
          <TextureBar
            segments={exchangeSegments}
            label="거래소별 점유율"
            large
          />
          <ul className="index2-legend" aria-label="거래소별 점유율 범례">
            {INDEX_EXCHANGES.map(([exchange, name], index) => {
              const row = exchanges.find(item => item.exchange === exchange);
              return (
                <li key={exchange}>
                  <span className={`index2-legend-box index2-texture index2-texture-${TEXTURES[index]}`} />
                  <span>{formatShare(row?.share_pct)}</span>
                  <span>{name}</span>
                </li>
              );
            })}
          </ul>
          {showStatus && <>
            <div className="index2-divider" aria-hidden="true">----------------------------------------</div>
            <IndexDataStatus data={current} loading={loading} error={error} updated={updated} onRefresh={() => setRetry(value => value + 1)} compact />
          </>}
        </div>
      </aside>}

      <section className="index2-pane index2-detail" aria-label="인덱스2 상세 표">
        <div className="index2-scroll">
          <div className="index2-toolbar">
            <div className="index2-view-switch" aria-label="표 보기 방식">
              <button type="button" className={view === "coins" ? "is-selected" : ""} aria-pressed={view === "coins"} onClick={() => setView("coins")}>
                코인 보기
              </button>
              <button type="button" className={view === "exchanges" ? "is-selected" : ""} aria-pressed={view === "exchanges"} onClick={() => setView("exchanges")}>
                {realtimeWindow !== undefined ? "실시간 보기" : "거래소 보기"}
              </button>
            </div>
            <label className="index2-search">
              <span>[ 코인 검색 ]</span>
              <input
                type="search"
                aria-label="인덱스2 코인 검색"
                value={query}
                onChange={event => { setQuery(event.target.value); setRowLimit(INDEX2_ROW_STEP); }}
                disabled={view === "exchanges" && !liveMode}
                placeholder={fitColumns ? "BTC" : undefined}
                onKeyDown={event => { if (event.key === "Escape") { setQuery(""); setRowLimit(INDEX2_ROW_STEP); } }}
              />
            </label>
          </div>
          {filters.controls}
          {liveMode && <div className="index2-live-status" role="status">{windowLabel(realtimeWindow ?? 10)} 실시간 · {formatMarketTimeMS((filters.liveAsOf ?? 0) * 1000, "time-seconds")}{live.error || filters.liveError ? " · 갱신 지연" : ""}</div>}
          {showTableIntro && <p className="index2-copy index2-detail-copy">
            {view === "coins"
              ? "코인별 원화 환산 거래금액과 거래소 구성을 표시합니다. 일부 자료일 때 막대는 확인된 금액 안의 구성입니다."
              : "국내 거래소별 공식 거래금액과 서버가 계산한 점유율을 표시합니다."}
          </p>}
          {(liveMode ? live.error || filters.liveError : error) && <p className="index2-alert" role="alert">{liveMode ? live.error || filters.liveError : error}{current ? " 마지막으로 받은 자료를 표시합니다." : ""}</p>}
          <div ref={tableScrollRef} className="index2-table-wrap" tabIndex={0} aria-label="인덱스2 표 가로 스크롤">
            {view === "coins" || liveMode ? (
              <table className="index2-table index2-coin-table" data-fit={fitColumns ? "" : undefined} data-live={liveMode ? "" : undefined}>
                {fitColumns && <colgroup>
                  <col className="index2-col-coin" />
                  <col className="index2-col-amount" />
                  <col className="index2-col-share" />
                  <col className="index2-col-composition" />
                  {filters.columns.map(([id]) => <col key={id} className={!liveMode && current?.storage_mode !== "TICKER_24H" && (id === "korbit" || id === "gopax") ? "index2-col-unavailable" : id === "combined" ? "index2-col-combined" : "index2-col-venue"} />)}
                </colgroup>}
                <thead>
                  <tr>
                    <SortHeading label="코인" direction={filters.sort?.field === "name" ? filters.sort.ascending : undefined} onClick={() => filters.sortBy("domestic", "name")} />
                    <SortHeading className="num" label="거래금액" direction={filters.sort?.venue === "domestic" && filters.sort.field === "total" ? filters.sort.ascending : undefined} onClick={() => filters.sortBy("domestic", "total")} />
                    <SortHeading className="num" label="국내 비중" direction={filters.sort?.venue === "domestic" && filters.sort.field === "share" ? filters.sort.ascending : undefined} onClick={() => filters.sortBy("domestic", "share")} />
                    <th className="breakdown">거래소 구성</th>
                    {filters.columns.map(([id, name]) => <th key={id} className="num index2-venue-cell" aria-sort={filters.sort?.venue === id ? filters.sort.ascending ? "ascending" : "descending" : "none"}>{name}<div>{(showShareSort ? (["total", "share"] as const) : (["total"] as const)).map(field => <button key={field} type="button" aria-label={`${name} ${field === "total" ? "거래금액" : "점유율"} 정렬`} onClick={() => filters.sortBy(id,field)}>{field === "total" ? "금액" : "%"} <span className="index2-sort-arrow">{filters.sort?.venue === id && filters.sort.field === field ? filters.sort.ascending ? "↑" : "↓" : "↕"}</span></button>)}</div></th>)}
                  </tr>
                </thead>
                <tbody>
                  {visibleAssets.map(asset => (
                    <tr key={asset.base} data-coin={asset.base}>
                      <th scope="row" title={asset.base}>{asset.base}</th>
                      <td className="num" title={liveMode ? "실시간 거래금액 (원화 환산)" : "거래금액 (억 원)"}>{amount(asset.volume_krw)}</td>
                      <td className="num">{formatShare(asset.share_pct)}</td>
                      <td className="breakdown">
                        <TextureBar
                          segments={assetSegments(asset)}
                          initials={showCompositionInitials}
                          label={`${asset.base} ${asset.status === "COMPLETE" ? "거래소별 비중" : "확인된 금액의 거래소 구성"}`}
                        />
                      </td>
                      {filters.columns.map(([exchange]) => { const cell = asset.exchanges.find(entry => entry.exchange === exchange)?.cell; return (
                        <td key={exchange} className="num index2-venue-cell" title={cellStatus(cell)}>
                          <span>{formatShare(indexCellShare(cell))}</span>
                          <small title={liveMode ? "실시간 거래금액 (원화 환산)" : "거래금액 (억 원)"}>{amount(indexCellAmount(cell))}</small>
                        </td>
                      ); })}
                    </tr>
                  ))}
                  {!filteredAssets.length && (
                    <tr>
                      <td colSpan={4 + filters.columns.length} className="index2-empty">
                        {loading || filters.pending ? "코인 자료를 불러오는 중입니다." : "표시할 코인 자료가 없습니다."}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            ) : (
              <table className="index2-table">
                <thead>
                  <tr>
                    <SortHeading label="거래소" direction={filters.exchangeSort?.field === "name" ? filters.exchangeSort.ascending : undefined} onClick={() => filters.sortExchangesBy("name")} />
                    <SortHeading className="num" label="거래금액" direction={filters.exchangeSort?.field === "total" ? filters.exchangeSort.ascending : undefined} onClick={() => filters.sortExchangesBy("total")} />
                    <SortHeading className="num" label="국내 점유율" direction={filters.exchangeSort?.field === "share" ? filters.exchangeSort.ascending : undefined} onClick={() => filters.sortExchangesBy("share")} />
                    <th className="breakdown">점유율 막대</th>
                  </tr>
                </thead>
                <tbody>
                  {filters.exchangeRows.map(([exchange, name]) => {
                    const index = INDEX_EXCHANGES.findIndex(([id]) => id === exchange);
                    const cell = grid?.totals[exchange];
                    const row = { volume_krw: indexCellAmount(cell), share_pct: indexCellShare(cell) };
                    return (
                      <tr key={exchange}>
                        <th scope="row">{name}</th>
                        <td className="num" title={liveMode ? "실시간 거래금액 (원화 환산)" : "거래금액 (억 원)"}>{amount(row?.volume_krw)}</td>
                        <td className="num">{formatShare(row?.share_pct)}</td>
                        <td className="breakdown">
                          <TextureBar
                            segments={[{ exchange, share: row?.share_pct ?? null }]}
                            label={`${name} 국내 점유율 ${formatShare(row?.share_pct)}`}
                          />
                          <span className={`index2-exchange-mark index2-texture index2-texture-${TEXTURES[index]}`} aria-hidden="true" />
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
          </div>
          {(view === "coins" || liveMode) && filteredAssets.length > visibleAssets.length && (
            <div className="index2-more">
              <span>
                {filteredAssets.length.toLocaleString("ko-KR")}개 중{" "}
                {visibleAssets.length.toLocaleString("ko-KR")}개 표시
              </span>
              <button type="button" onClick={() => setRowLimit(limit => limit + INDEX2_ROW_STEP)}>
                {INDEX2_ROW_STEP}개 더 보기
              </button>
              <button type="button" onClick={() => setRowLimit(filteredAssets.length)}>
                전체 보기
              </button>
            </div>
          )}
          {current && !fitColumns && (
            <div className="index2-table-end" aria-hidden="true">
              ........................................<br />표 끝
            </div>
          )}
        </div>
      </section>

      {showVisual && <aside className="index2-pane index2-visual" aria-label="거래금액 상위 코인">
        <div className="index2-scroll index2-visual-scroll">
          <div className="index2-visual-heading">
            <span>거래금액 상위 코인</span>
            <button type="button" onClick={() => setShowAll(value => !value)}>
              {showAll ? "줄이기" : "전체 보기"}
            </button>
          </div>
          {/* Above the grid: below it, the legend moved when the cards arrived. */}
          <div className="index2-visual-legend">
            [ 그리드 범례 ]<br />높이 = 확인된 거래소 구성비<br />왼쪽부터 빗썸 · 업비트 · 코인원 · 코빗 · 고팍스
          </div>
          <div className="index2-grid">
            {gridAssets.map(asset => (
              <article className="index2-grid-card" key={asset.base}>
                <div className="index2-grid-title">
                  <strong>{asset.base}</strong>
                  <span title={liveMode ? "실시간 거래금액 (원화 환산)" : "거래금액 (억 원)"}>{amount(asset.volume_krw)}</span>
                </div>
                <div className="index2-mini-chart" role="img" aria-label={`${asset.base} 거래소 구성`}>
                  {asset.exchanges.map((exchange, index) => {
                    const share = exchange.share_pct;
                    return share != null && share > 0 ? (
                      <span
                        key={exchange.exchange}
                        className={`index2-texture index2-texture-${TEXTURES[index]} index2-texture-inverse`}
                        style={{ height: `${Math.max(4, share)}%` }}
                        title={`${EXCHANGE_NAME.get(exchange.exchange as (typeof INDEX_EXCHANGES)[number][0]) ?? exchange.exchange} ${formatShare(share)}`}
                      />
                    ) : null;
                  })}
                </div>
                <small>{asset.status}</small>
              </article>
            ))}
            {!gridAssets.length && <p className="index2-visual-empty">코인 자료 연결 중</p>}
          </div>
        </div>
      </aside>}
    </section>
    </>
  );
}
