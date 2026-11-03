import { useEffect, useMemo, useRef, useState } from "react";
import { getIndexGrid, getIndexFilters, getRealtimeIndex, type IndexFiltersResponse } from "@/lib/api-client";
import { usePolled } from "@/screens/data";
import { INDEX_EXCHANGES, type IndexDayResponse } from "@/lib/index-day";

export const INDEX2_VENUES: Array<readonly [string, string]> = [
  ...INDEX_EXCHANGES.slice(0, 2), ["combined", "빗썸 + 업비트"], ...INDEX_EXCHANGES.slice(2),
];
type Sort = { venue: string; field: "total" | "share" | "name"; ascending: boolean } | null;
export function useIndex2Filters(data: IndexDayResponse | null, query: string, clearQuery: () => void, retry: number, showNoticeFilter = true, defaultDecimals = 4, showResultCount = true, enhancedControls = false, venueControl?: { value: string; onChange: (value: string) => void }) {
  const [localVenue, setLocalVenue] = useState("all"), [market, setMarket] = useState("all");
  const venue = venueControl?.value ?? localVenue;
  const setVenue = venueControl?.onChange ?? setLocalVenue;
  const [band, setBand] = useState("all"), [listing, setListing] = useState("all"), [notice, setNotice] = useState("all");
  const [decimals, setDecimals] = useState(defaultDecimals), [sort, setSort] = useState<Sort>(null);
  const [exchangeSort, setExchangeSort] = useState<Sort>(null);
  const clearSort = () => { setSort(null); setExchangeSort(null); };
  const [analysis, setAnalysis] = useState<IndexFiltersResponse | null>(null), [metadataError, setMetadataError] = useState("");
  const [filtered, setFiltered] = useState<{ key: string; source: IndexDayResponse; grid: NonNullable<IndexDayResponse["domestic"]> } | null>(null);
  const [failure, setFailure] = useState<{key: string; message: string} | null>(null);
  useEffect(() => {
    const c = new AbortController(); let active = true;
    const timer = setTimeout(() => c.abort(), 12000);
    getIndexFilters(c.signal).then(value => { if (active) { setAnalysis(value); setMetadataError(""); } })
      .catch(() => { if (active) setMetadataError("상장·공지 필터 자료를 불러오지 못했습니다."); })
      .finally(() => clearTimeout(timer));
    return () => { active = false; clearTimeout(timer); c.abort(); };
  }, [retry]);
  const active = venue !== "all" || market !== "all" || band !== "all" || listing !== "all" || notice !== "all" || query.trim() !== "";
  const metadata = useMemo(() => new Map(analysis?.assets.map(a => [a.asset, a]) ?? []), [analysis]);
  const notices = useMemo(() => new Set(analysis?.notice_assets ?? []), [analysis]);
  const normalizedQuery = query.trim().toLowerCase();
  const selected = useMemo(() => (data?.domestic?.coins ?? []).filter(coin => {
    if (!coin.base.toLowerCase().includes(normalizedQuery)) return false;
    const share = coin.cells.bithumb?.share;
    if (band !== "all" && (share == null || (band === "high" ? share < 50 : band === "mid" ? share < 20 || share >= 50 : share >= 20))) return false;
    if (listing !== "all") {
      const a = metadata.get(coin.base); if (!a) return false;
      const key = a.exclusive_on_bithumb ? "bithumb-only" : a.shared_bithumb_upbit ? "bithumb-upbit" : a.listed_on_bithumb ? "bithumb-other" : "bithumb-unlisted";
      if (listing !== key) return false;
    }
    return notice === "all" || Boolean(analysis && ((notice === "present") === notices.has(coin.base)));
  }), [data, normalizedQuery, band, listing, notice, metadata, notices, analysis]);
  const assets = useMemo(() => selected.map(c => c.base).sort().join(","), [selected]);
  const key = JSON.stringify([data?.day, data?.storage_mode, assets, market, venue]);
  const liveMode = data?.storage_mode === "REALTIME_WINDOW";
  const liveInput = useRef({ assets, window: data?.window_sec ?? 10 });
  liveInput.current = { assets, window: data?.window_sec ?? 10 };
  const liveKey = JSON.stringify([data?.window_sec, query, market, venue, band, listing, notice, retry]);
  const liveFiltered = usePolled(liveMode && active ? `live-index-filter:${liveKey}` : null, signal => getRealtimeIndex(liveInput.current.window, signal, {assets:liveInput.current.assets, quote:market, venue}), 1000);
  useEffect(() => {
    if (liveMode || !active || !data?.domestic) return;
    const c = new AbortController(); let live = true;
    const timeout = setTimeout(() => c.abort(), 12000);
    const debounce = setTimeout(() => {
      getIndexGrid(data.day, c.signal, { assets, quote: market, venue }, data.storage_mode === "TICKER_24H" ? "ticker_24h" : data.storage_mode === "ROLLING_24H_LATEST" ? "hourly_rolling" : undefined).then(result => {
        if (!result.domestic) throw Error("집계 자료 없음");
        if (live) { setFiltered({ key, source: data, grid: result.domestic }); setFailure(null); }
      }).catch(() => { if (live) setFailure({key, message: "필터 합계를 불러오지 못했습니다. 다시 시도해 주세요."}); })
        .finally(() => clearTimeout(timeout));
    }, 150);
    return () => { live = false; c.abort(); clearTimeout(timeout); clearTimeout(debounce); };
  }, [active, data, key, assets, market, venue, retry, liveMode]);
  const grid = liveMode && active ? liveFiltered.data?.domestic : active ? (filtered?.key === key && filtered.source === data ? filtered.grid : undefined) : data?.domestic;
  const rows = useMemo(() => {
  const rows = [...(grid?.coins ?? [])];
  if (sort) rows.sort((a, b) => {
    if (sort.field === "name") return (sort.ascending ? 1 : -1) * a.base.localeCompare(b.base);
    const x = a.cells[sort.venue]?.[sort.field], y = b.cells[sort.venue]?.[sort.field];
    if (x == null || y == null) return Number(x == null) - Number(y == null);
    return (sort.ascending ? 1 : -1) * (x - y) || a.base.localeCompare(b.base);
  });
  return rows;
  }, [grid, sort]);
  const sortBy = (venue: string, field: "total" | "share" | "name") => setSort(s => ({venue, field, ascending: s?.venue === venue && s.field === field ? !s.ascending : field === "name"}));
  const sortExchangesBy = (field: "total" | "share" | "name") => setExchangeSort(s => ({venue: "exchanges", field, ascending: s?.field === field ? !s.ascending : field === "name"}));
  const columns = venue === "all" ? INDEX_EXCHANGES : INDEX2_VENUES.filter(([id]) => id === venue);
  const exchangeRows = [...columns];
  if (exchangeSort) exchangeRows.sort(([a, an], [b, bn]) => {
    const direction = exchangeSort.ascending ? 1 : -1;
    if (exchangeSort.field === "name") return direction * an.localeCompare(bn, "ko");
    const x = grid?.totals[a]?.[exchangeSort.field], y = grid?.totals[b]?.[exchangeSort.field];
    if (x == null || y == null) return Number(x == null) - Number(y == null);
    return direction * (x - y) || an.localeCompare(bn, "ko");
  });
  const controls = <div className="index2-filters" aria-label="인덱스2 코인 필터">
    <Index2FilterButton showSelection={enhancedControls} label="거래소" value={venue} onChange={setVenue} options={[["all","거래소 전체"],...INDEX2_VENUES]} />
    <Index2FilterButton showSelection={enhancedControls} label="마켓" value={market} onChange={setMarket} options={[["all","마켓 전체"],["krw","원화"],["btc","BTC"],["usdt","USDT"]]} />
    <Index2FilterButton showSelection={enhancedControls} label="빗썸 점유율" value={band} onChange={setBand} options={[["all","전체 구간"],["high","50% 이상"],["mid","20 ~ 50%"],["low","20% 미만"]]} />
    <Index2FilterButton showSelection={enhancedControls} label="상장 구조" value={listing} onChange={setListing} disabled={!analysis} options={[["all","전체 구조"],["bithumb-only","빗썸 단독"],["bithumb-upbit","빗썸·업비트 동시상장"],["bithumb-other","빗썸·기타 동시상장"],["bithumb-unlisted","빗썸 미상장"]]} />
    {showNoticeFilter && <Index2FilterButton showSelection={enhancedControls} label="관련 공지" value={notice} onChange={setNotice} disabled={!analysis} options={[["all","전체"],["present","공지 있음"],["absent","공지 없음"]]} />}
    <Index2FilterButton showSelection={enhancedControls} label="거래금액 소수점" triggerLabel={enhancedControls ? `소수점 · ${decimals}자리` : undefined} defaultValue={String(defaultDecimals)} value={String(decimals)} onChange={v => setDecimals(Number(v))} options={[0,1,2,3,4].map(n => [String(n),`${n}자리`] as const)} />
    <button type="button" aria-label="필터·정렬 초기화" title="필터·정렬 초기화" disabled={enhancedControls && !active && !sort && !exchangeSort && decimals === defaultDecimals} onClick={() => { setVenue("all"); setMarket("all"); setBand("all"); setListing("all"); setNotice("all"); setDecimals(defaultDecimals); clearSort(); clearQuery(); }}>{enhancedControls ? "초기화" : "필터·정렬 초기화"}</button>
    <button type="button" onClick={clearSort} disabled={enhancedControls && !sort && !exchangeSort}>정렬 해제</button>
    {(showResultCount || (active && !grid)) && <span role="status">{active && !grid ? failure?.key === key ? failure.message : "필터 합계 계산 중…" : `필터 결과 ${rows.length}개`}</span>}
    {metadataError && <span role="alert">{metadataError}</span>}
  </div>;
  return { grid, rows, controls, sortBy, sort, exchangeSort, sortExchangesBy, exchangeRows, decimals, venue, pending: active && !grid, liveAsOf: active ? liveFiltered.data?.as_of_second : data?.as_of_second, livePartial: active ? liveFiltered.data?.status !== "COMPLETE" : data?.status !== "COMPLETE", liveError: liveMode && active ? liveFiltered.error : "", columns };
}
import { Index2FilterButton } from "./Index2FilterButton";
