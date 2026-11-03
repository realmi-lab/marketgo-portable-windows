import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronLeft, ChevronRight, Search, SlidersHorizontal, X } from "lucide-react";
import { IndexDataStatus } from "@/components/IndexDataStatus";
import { getIndexDay, getIndexGrid, getIndexFilters, type IndexFiltersResponse } from "@/lib/api-client";
import { useFollowKSTToday, useIndexRefresh } from "@/lib/use-index-refresh";
import { indexRefreshInterval } from "@/lib/index-refresh";
import { indexHasConfirmedAmount } from "@/lib/index-status";
import type { CoinListingFilter, CoinNoticeFilter } from "@/lib/coin-table-query";
import { currentKSTDay, indexCellAmount, indexCellShare, indexCellValue, lastClosedKSTDay, type DomesticIndexCell, type IndexDayResponse } from "@/lib/index-day";
import "./market-index.css";
import "./market-index-layout.css";
type Field = "total" | "krw" | "btc" | "usdt" | "share";
const venues = [
 {id:"bithumb",name:"빗썸",fields:["total","krw","btc","share"]},
 {id:"upbit",name:"업비트",fields:["total","krw","btc","usdt","share"]},
 {id:"combined",name:"빗썸 + 업비트",fields:["total","krw","btc","usdt","share"]},
 {id:"coinone",name:"코인원",fields:["total","share"]},
 {id:"korbit",name:"코빗",fields:["total","share"]},
 {id:"gopax",name:"고팍스",fields:["total","share"]},
 {id:"domestic",name:"국내 5개 거래소",fields:["total","share"]},
];
const labels: Record<Field,string> = {total:"전체",krw:"원화",btc:"BTC",usdt:"USDT",share:"점유율"};
const percent = (n:number|null|undefined) => n==null?"—":`${n.toFixed(2)}%`;
const INDEX_ROW_STEP = 100;
export default function MarketIndex() {
 const [day,setDay]=useState(currentKSTDay),[draftDay,setDraftDay]=useState(currentKSTDay);
 const [decimals,setDecimals]=useState(4);
 const amountFormatter = useMemo(()=>new Intl.NumberFormat("ko-KR",{minimumFractionDigits:decimals,maximumFractionDigits:decimals}),[decimals]);
 const amount = (n:number|null|undefined) => n==null?"—":amountFormatter.format(n/1e8);
 // Korbit and GOPAX publish no hourly turnover; say so instead of a bare "—".
 const venueAmount = (c:DomesticIndexCell|undefined) => c?.unavailable&&indexCellAmount(c)==null?"공식 거래대금 미제공":`${amount(indexCellAmount(c))} 억 원`;
 const [data,setData]=useState<IndexDayResponse|null>(null),[error,setError]=useState("");
 const [loading,setLoading]=useState(true),[retry,setRetry]=useState(0),[updated,setUpdated]=useState(0);
 const [filterRetry,setFilterRetry]=useState(0);
 const [filtersOpen,setFiltersOpen]=useState(false);
 useIndexRefresh(setFilterRetry);
 const [query,setQuery]=useState(""),[venue,setVenue]=useState("all"),[market,setMarket]=useState("all");
 const [listing,setListing]=useState<CoinListingFilter>("all"),[notice,setNotice]=useState<CoinNoticeFilter>("all");
 const [band,setBand]=useState("all"),[filterData,setFilterData]=useState<IndexFiltersResponse|null>(null),[filterError,setFilterError]=useState("");
 useEffect(()=>{const c=new AbortController();const timer=setTimeout(()=>c.abort(),12000);getIndexFilters(c.signal).then(d=>{if(!c.signal.aborted){setFilterData(d);setFilterError("");}}).catch(()=>{if(!c.signal.aborted)setFilterError("상장·공지 필터 자료를 불러오지 못했습니다.");else setFilterError("상장·공지 필터 조회 시간이 초과되었습니다.");}).finally(()=>clearTimeout(timer));return()=>{clearTimeout(timer);c.abort();};},[filterRetry]);
 const [sort,setSort]=useState<{id:string;field:Field;asc:boolean}|null>(null);
 // Rows drawn at first and per "더 보기"; all 600+ rows made ~15,000 elements.
 const [rowLimit,setRowLimit]=useState(INDEX_ROW_STEP);
 useEffect(()=>setRowLimit(INDEX_ROW_STEP),[day,query,venue,market,listing,notice,band,sort]);
 const scroll=useRef<HTMLDivElement>(null);
 useEffect(()=>{
  const c=new AbortController();let active=true;setLoading(true);setError("");
  const timer=setTimeout(()=>c.abort(),12000);
  getIndexDay(day,c.signal).then(d=>{if(active){setData(d);setUpdated(Date.now());}})
   .catch(e=>{if(active)setError(c.signal.aborted?"조회 시간이 길어졌습니다. 다시 조회해 주세요.":e.message);})
   .finally(()=>{clearTimeout(timer);if(active)setLoading(false);});
  return()=>{active=false;clearTimeout(timer);c.abort();};
 },[day,retry]);
 useIndexRefresh(setRetry,indexRefreshInterval(data?.day===day?data:null));
 const current=data?.day===day?data:null,grid=current?.domestic;
 const hasAmount=indexHasConfirmedAmount(current);
 const groups=venues.filter(v=>venue==="all"||v.id===venue||v.id==="domestic").map(v=>({...v,fields:v.fields.filter(f=>market==="all"||f===market||f==="share"||(v.fields.length===2&&f==="total")) as Field[]}));
 const coins=useMemo(()=>{
  const assets=new Map(filterData?.assets.map(a=>[a.asset,a])??[]);
  const notices=new Set(filterData?.notice_assets??[]);
  const rows=(grid?.coins??[]).filter(c=>{
   if(!c.base.toLowerCase().includes(query.trim().toLowerCase()))return false;
   const share=c.cells.bithumb?.share;
   if(band!=="all"&&(share==null||(band==="high"?share<50:band==="mid"?share<20||share>=50:share>=20)))return false;
   if(listing!=="all"){const a=assets.get(c.base);if(!a)return false;const key=a.exclusive_on_bithumb?"bithumb-only":a.shared_bithumb_upbit?"bithumb-upbit":a.listed_on_bithumb?"bithumb-other":"bithumb-unlisted";if(key!==listing)return false;}
   if(notice!=="all"&&(!filterData||(notice==="present")!==notices.has(c.base)))return false;
   return true;
  });
  return rows;
 },[grid,query,listing,notice,band,filterData]);
 const filterActive=listing!=="all"||notice!=="all"||band!=="all"||query.trim()!==""||market!=="all"||venue!=="all";
 const activeFilters=[listing!=="all",notice!=="all",band!=="all",market!=="all",venue!=="all"].filter(Boolean).length;
 const chooseDay=(value:string)=>{setDay(value);setDraftDay(value);};
 useFollowKSTToday(day,chooseDay);
 const resetFilters=()=>{setListing("all");setNotice("all");setBand("all");setQuery("");setVenue("all");setMarket("all");setSort(null);};
 const assetKeys=useMemo(()=>coins.map(c=>c.base).sort().join(","),[coins]);
 const filterKey=JSON.stringify([day,assetKeys,market,venue,updated]);
 const [filtered,setFiltered]=useState<{key:string;grid:NonNullable<IndexDayResponse["domestic"]>}|null>(null),[totalError,setTotalError]=useState("");
 useEffect(()=>{if(!filterActive)return;let active=true;const c=new AbortController();setTotalError("");const timer=setTimeout(()=>{getIndexGrid(day,c.signal,{assets:assetKeys,quote:market,venue}).then(d=>{if(active&&d.domestic)setFiltered({key:filterKey,grid:d.domestic});}).catch(()=>{if(active)setTotalError("필터 합계를 조회하지 못했습니다. 다시 조회해 주세요.");});},150);const timeout=setTimeout(()=>c.abort(),12000);return()=>{active=false;clearTimeout(timer);clearTimeout(timeout);c.abort();};},[filterActive,filterKey]);
 const displayGrid=filterActive?(filtered?.key===filterKey?filtered.grid:undefined):grid;
 const unprovidedVenues=venues.filter(v=>{const c=displayGrid?.totals[v.id];return c?.unavailable&&indexCellAmount(c)==null&&!["combined","domestic"].includes(v.id);}).map(v=>v.name);
 const displayCoins=useMemo(()=>{
  let rows=[...coins];
  if(filterActive){
   const byBase=new Map(displayGrid?.coins.map(row=>[row.base,row])??[]);
   rows=coins.map(c=>byBase.get(c.base)).filter((c):c is NonNullable<typeof c>=>!!c);
  }
  // Sort the cells on view: quote/venue filtering can change their order.
  if(sort)rows.sort((a,b)=>{const x=a.cells[sort.id]?.[sort.field],y=b.cells[sort.id]?.[sort.field];if(x==null||y==null)return Number(x==null)-Number(y==null);return (sort.asc?1:-1)*(x-y)||a.base.localeCompare(b.base);});
  return rows;
 },[filterActive,coins,displayGrid,sort]);
 const paint=(cell:DomesticIndexCell|undefined,field:Field)=>{const value=hasAmount?indexCellValue(cell,field):null;return field==="share"?percent(value):amount(value);};
 const sortBy=(id:string,field:Field)=>setSort(s=>({id,field,asc:s?.id===id&&s.field===field?!s.asc:false}));
 return <section className="domestic-index di-refined" aria-labelledby="market-index-title">
  <header className="di-heading"><div><span className="di-eyebrow">국내 시장 · 공식 시간봉</span><h2 id="market-index-title">거래소 점유율 현황</h2></div><span className="di-brand">MarketGo</span></header>
  <div className="di-overview">
   <div className="di-grand"><span>{filterActive?"선택한 조건의 거래대금":"국내 거래대금 합계"}</span><b>{amount(hasAmount?displayGrid?.totals.domestic?.total:null)}<small> 억 원</small></b><span>{unprovidedVenues.length?`확인된 금액 기준 · ${unprovidedVenues.join("·")} 미제공`:"확인된 금액 기준"}</span></div>
   <div className="di-date-controls"><span className="di-date-label">조회 날짜</span><div className="di-filters"><input type="date" aria-label="조회 날짜" value={draftDay} min="2026-08-01" max={currentKSTDay()} onClick={e=>{try{e.currentTarget.showPicker?.();}catch{/* Keep native date entry available when the browser blocks the picker. */}}} onChange={e=>{const next=e.target.value;setDraftDay(next);if(/^\d{4}-\d{2}-\d{2}$/.test(next)&&next>="2026-08-01"&&next<=currentKSTDay())setDay(next);}}/>
   <button onClick={()=>{if(/^\d{4}-\d{2}-\d{2}$/.test(draftDay)&&draftDay>="2026-08-01"&&draftDay<=currentKSTDay()){setDay(draftDay);setRetry(n=>n+1);}}}>조회</button>
   </div><div className="di-quick-days"><button aria-pressed={day===currentKSTDay()} onClick={()=>chooseDay(currentKSTDay())}>오늘</button><button aria-pressed={day===lastClosedKSTDay()} onClick={()=>chooseDay(lastClosedKSTDay())}>어제</button></div></div>
  </div>
  <div className="di-cards">{venues.slice(0,3).map(v=>{const c=displayGrid?.totals[v.id];return <article key={v.id} className={`di-card di-${v.id}`}>
   <h3>{v.name}</h3><p>{percent(indexCellShare(c))}</p><small>{venueAmount(c)}</small>
  </article>;})}<article className="di-card di-stacked" aria-label="코인원·코빗·고팍스 점유율">{venues.slice(3,6).map(v=>{const c=displayGrid?.totals[v.id];return <section key={v.id} className={`di-stacked-venue di-${v.id}`}><h3>{v.name} <strong>{percent(indexCellShare(c))}</strong></h3><small>{venueAmount(c)}</small></section>;})}</article></div>
  <div className="di-data-status"><IndexDataStatus data={current} loading={loading} error={error} updated={updated} onRefresh={()=>{setRetry(n=>n+1);setFilterRetry(n=>n+1);}}/></div>
  {error&&<p role="alert" className="di-error">{error}</p>}{!loading&&!error&&!grid&&<p role="status">집계 자료를 준비하고 있습니다.</p>}
  {filterActive&&<p role="status" className="di-meta">{totalError||(displayGrid?"선택한 필터 기준 거래금액·점유율":"필터 합계 계산 중…")}</p>}
  <div className="di-coin-toolbar">
   <div className="di-table-heading"><h3>코인별 거래금액</h3><span>{coins.length.toLocaleString("ko-KR")}개 코인</span></div>
   <div className="di-search-actions"><label className="di-search"><Search size={17} aria-hidden="true"/><input aria-label="자산명 검색" placeholder="코인 검색 · BTC, ETH" value={query} onChange={e=>setQuery(e.target.value)}/>{query&&<button aria-label="검색어 지우기" onClick={()=>setQuery("")}><X size={16}/></button>}</label><button className="di-filter-toggle" aria-expanded={filtersOpen} aria-controls="di-filter-options" onClick={()=>setFiltersOpen(open=>!open)}><SlidersHorizontal size={16} aria-hidden="true"/>필터{activeFilters>0&&<span>{activeFilters}</span>}</button></div>
  </div>
  <div id="di-filter-options" className="di-index-filters" aria-label="인덱스 코인 필터" hidden={!filtersOpen}>
   <label>거래소<select aria-label="거래소 표시" value={venue} onChange={e=>setVenue(e.target.value)}><option value="all">거래소 전체</option>{venues.filter(v=>v.id!=="domestic").map(v=><option key={v.id} value={v.id}>{v.name}</option>)}</select></label>
   <label>마켓<select aria-label="마켓 표시" value={market} onChange={e=>setMarket(e.target.value)}><option value="all">마켓 전체</option><option value="krw">원화</option><option value="btc">BTC</option><option value="usdt">USDT</option></select></label>
   <label>빗썸 점유율<select aria-label="빗썸 점유율 구간" value={band} onChange={e=>setBand(e.target.value)}><option value="all">전체 구간</option><option value="high">50% 이상</option><option value="mid">20 ~ 50%</option><option value="low">20% 미만</option></select></label>
   <label>상장 구조<select aria-label="상장 구조" value={listing} disabled={!filterData} onChange={e=>setListing(e.target.value as CoinListingFilter)}><option value="all">전체 구조</option><option value="bithumb-only">빗썸 단독</option><option value="bithumb-upbit">빗썸·업비트 동시상장</option><option value="bithumb-other">빗썸·기타 동시상장</option><option value="bithumb-unlisted">빗썸 미상장</option></select></label>
   <label>관련 공지<select aria-label="관련 공지" value={notice} disabled={!filterData} onChange={e=>setNotice(e.target.value as CoinNoticeFilter)}><option value="all">전체</option><option value="present">공지 있음</option><option value="absent">공지 없음</option></select></label>
   <label>거래금액 소수점<select aria-label="거래금액 소수점" value={decimals} onChange={e=>setDecimals(Number(e.target.value))}>{[0,1,2,3,4].map(n=><option key={n} value={n}>{n}자리</option>)}</select></label>
   <button onClick={resetFilters}>필터·정렬 초기화</button><span>필터 결과 {coins.length}개</span>
   {filterError&&<small>{filterError}</small>}
  </div>
  <div className="di-table-tools"><span>단위: 억 원 · 좌우로 이동해 거래소 비교</span>{sort&&<button onClick={()=>setSort(null)}>정렬 해제</button>}{filterActive&&<button onClick={resetFilters}>조건 지우기</button>}<button aria-label="왼쪽으로 스크롤" onClick={()=>scroll.current?.scrollBy({left:-500,behavior:"smooth"})}><ChevronLeft size={18}/></button><button aria-label="오른쪽으로 스크롤" onClick={()=>scroll.current?.scrollBy({left:500,behavior:"smooth"})}><ChevronRight size={18}/></button></div>
  <div className="di-table-scroll" ref={scroll} tabIndex={0} aria-label="코인 거래 표 가로 스크롤"><table><thead><tr><th rowSpan={2} className="di-asset di-legend"><button aria-pressed={band==="high"} onClick={()=>setBand(band==="high"?"all":"high")}><span className="di-swatch di-swatch-high" aria-hidden="true"/>빗썸 50% 이상</button><button aria-pressed={band==="mid"} onClick={()=>setBand(band==="mid"?"all":"mid")}><span className="di-swatch di-swatch-mid" aria-hidden="true"/>빗썸 20–50%</button><button aria-pressed={band==="low"} onClick={()=>setBand(band==="low"?"all":"low")}><span className="di-swatch di-swatch-low" aria-hidden="true"/>빗썸 20% 미만</button></th>{groups.map(g=><th className={`di-group di-${g.id}`} key={g.id} colSpan={g.fields.length}>{g.name}{!["combined","domestic"].includes(g.id)&&displayGrid?` (${displayGrid.coins.filter(c=>c.cells[g.id]!==undefined).length})`:""}</th>)}</tr><tr>{groups.flatMap(g=>g.fields.map(f=><th key={`${g.id}:${f}`} className={`di-${g.id}`}><button aria-label={`${g.name} ${labels[f]} 정렬`} onClick={()=>sortBy(g.id,f)}>{f==="total"&&g.fields.length===2?"거래금액":labels[f]} <span>{sort?.id===g.id&&sort.field===f?(sort.asc?"↑":"↓"):"↕"}</span></button></th>))}</tr></thead>
   <tbody><tr className="di-total"><th className="di-asset">전체 <small>(단위: 억 원)</small></th>{groups.flatMap(g=>g.fields.map(f=><td key={`${g.id}:${f}`}>{paint(displayGrid?.totals[g.id],f)}</td>))}</tr>
   {displayCoins.slice(0,rowLimit).map(coin=><tr key={coin.base}><th className={`di-asset di-band-${(coin.cells.bithumb?.share??0)>=50?"high":(coin.cells.bithumb?.share??0)>=20?"mid":"low"}`}>{coin.base}</th>{groups.flatMap(g=>g.fields.map(f=>{const c=coin.cells[g.id];return <td key={`${g.id}:${f}`} className={f==="share"?"di-share":""} title={c?.unavailable?"거래금액 미제공":c?.partial?"수집된 시간 구간의 금액 · 추가 수집 중":c?"선택한 기간 확인":"해당 거래소에 확인된 마켓 없음"}>{paint(c,f)}</td>;}))}</tr>)}
   {!coins.length&&<tr><td colSpan={1+groups.reduce((s,g)=>s+g.fields.length,0)} className="di-empty">{loading?"자료를 불러오는 중입니다.":error?"조회하지 못했습니다. 다시 시도해 주세요.":"검색 결과가 없습니다."}</td></tr>}</tbody></table></div>
  {displayCoins.length>rowLimit&&<div className="di-more"><span>{displayCoins.length.toLocaleString("ko-KR")}개 중 {rowLimit.toLocaleString("ko-KR")}개 표시</span><button type="button" onClick={()=>setRowLimit(n=>n+INDEX_ROW_STEP)}>{INDEX_ROW_STEP}개 더 보기</button><button type="button" onClick={()=>setRowLimit(displayCoins.length)}>전체 보기</button></div>}
 </section>;
}
