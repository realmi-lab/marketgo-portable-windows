import { useEffect, useState } from 'react';
import { Moon, Sun, RefreshCw } from 'lucide-react';
import { useTheme } from '@/contexts/ThemeContext';
import MarketIndex2 from '@/pages/MarketIndex2';
import { Index2FilterButton } from '@/pages/Index2FilterButton';
import { ANALYSIS_WINDOWS } from '@/lib/market-share-config';
import { exchangeLabel } from '@/lib/exchanges';
import { formatSharePercent, formatKRW } from '@/lib/formatters';
import { formatMarketTimeMS } from '@/lib/market-time-format';
import { windowLabel } from '@/lib/screen-copy';
import { appendLiveHistoryPoint, type LiveChartHistoryPoint } from '@/lib/live-chart';
import { useLiveChartExplorer } from '@/hooks/useLiveChartExplorer';
import { LiveChartControls } from '@/components/LiveChartNavigation';
import { LiveExchangeChartContent } from '@/components/LiveExchangeChart';
import { usePolled } from './data';
import { Band, Ex, LiveValue } from './ui';
import './portable.css';

type Venue = {exchange:string;volume_krw:number|null;share_pct:number|null;trade_count:number|null;trade_count_share_pct:number|null;partial:boolean};
type State = {clock?:{mode:string;offset_seconds:number;sources:string[];error:string};at:number;window:number;partial:boolean;exchanges:Venue[];point:LiveChartHistoryPoint;strip:{window:number;share:number|null;partial:boolean}[];changes:{exchange:string;amount:number|null;delta:number|null;before:number|null;after:number|null;share_delta:number|null}[];connections:{exchange:string;ticker_at:number;ticker_error:string;feeds:{state:string;error:string;since:number}[]}[]};
const money=(n:number|null)=>n==null?'—':formatKRW(n);
async function readState(windowSec:number,signal:AbortSignal):Promise<State>{
 const response=await fetch(`/api/portable/state?window=${windowSec}`,{signal});
 const value=await response.json();
 if(!response.ok)throw Error(value.detail||'로컬 수집 서버에 연결할 수 없습니다.');
 if(value.window!==windowSec||!Array.isArray(value.exchanges)||!value.point)throw Error('수집 응답 형식이 올바르지 않습니다.');
 return value;
}
export default function StandaloneShell(){
 const {theme,toggleTheme}=useTheme();
 const [windowSec,setWindow]=useState(10);
 const [selectedExchange,setExchange]=useState('all');const [requestKey,setRequestKey]=useState(0);
 const [refresh,setRefresh]=useState(0);const [refreshing,setRefreshing]=useState(false);
 const [host,setHost]=useState<HTMLDivElement|null>(null);
 const feed=usePolled(`standalone:${windowSec}`,signal=>readState(windowSec,signal),500);
 const current=feed.data;
 const [chart,setChart]=useState<{window:number;history:LiveChartHistoryPoint[]}>({window:10,history:[]});
 useEffect(()=>{document.title='MarketGO Portable';},[]);
 useEffect(()=>{
  if(!current||current.window!==windowSec)return;
  setChart(old=>({window:windowSec,history:appendLiveHistoryPoint(old.window===windowSec?old.history:[],current.point)}));
 },[current,windowSec]);
 const choose=(value:string)=>{setExchange(old=>old===value?'all':value);setRequestKey(old=>old+1)};
 const clockNotice=current?.clock && (current.clock.mode !== 'server' ? '서버 시각을 확인하지 못했습니다. PC의 자동 시각 동기화를 확인하고 다시 실행하세요.' : Math.abs(current.clock.offset_seconds) >= 2 ? `수집 시각 자동 보정 적용 · PC와 ${current.clock.offset_seconds.toFixed(1)}초 차이` : '');
 const errors=current?.connections.filter(c=>c.ticker_error||c.feeds.some(f=>f.state==='ERROR'))??[];
 return <div className="mg mg-portable" data-screen="portable">
  <header className="portable-header"><strong>MarketGO Portable</strong><div className="portable-index-toolbar" ref={setHost}/><button className="portable-theme-button" onClick={toggleTheme} aria-label={theme==='dark'?'밝은 화면으로':'어두운 화면으로'}>{theme==='dark'?<Sun size={18}/>:<Moon size={18}/>}</button></header>
  <section className="mg-strip" aria-label="실시간 요약"><span className="mg-strip-now"><span className="mg-pulse" data-off={!current||feed.error?'':undefined}/><span>실시간</span><span className="mg-strip-clock mg-num">{formatMarketTimeMS(current?current.at*1000:undefined,'time-seconds')}</span></span>
   {[10,30,60,600,1800,3600].map(seconds=>{const row=current?.strip.find(r=>r.window===seconds);return <span key={seconds} className="mg-strip-wide" title={row?.partial?'현재 PC에서 관측한 구간의 값입니다.':'최근 구간 체결 점유율'}>{seconds===10?'빗썸 ':''}{windowLabel(seconds)} <b className="mg-num"><LiveValue value={row?.share}>{formatSharePercent(row?.share)}</LiveValue></b></span>})}
   <span className="mg-strip-fill"/><button className="mg-button" onClick={()=>setRefresh(n=>n+1)} disabled={refreshing}><RefreshCw size={16}/>{refreshing?'갱신 중':'새로고침'}</button>
  </section>
  {(feed.error||errors.length>0||clockNotice)&&<details className="portable-connection-status"><summary>{clockNotice||`수집 연결 확인 · ${feed.error?'로컬 서버 응답 지연':errors.map(c=>exchangeLabel(c.exchange)).join(', ')}`}</summary>{current?.clock?.error&&<p>{current.clock.error}</p>}{feed.error&&<p role="alert">{feed.error}</p>}{errors.map(c=><p key={c.exchange}>{exchangeLabel(c.exchange)}: {c.ticker_error||c.feeds.find(f=>f.error)?.error}</p>)}<p>실행 창을 유지하고 인터넷 연결을 확인하세요. 자세한 기록: logs/runtime.log</p></details>}
  <main className="portable-columns" aria-label="포터블 대시보드"><section className="portable-index" aria-label="코인 인덱스">{current ? <MarketIndex2 referenceNowMS={(current.at+1)*1000} toolbarHost={host} realtimeWindow={windowSec} exchangeControl={{value:selectedExchange,onChange:setExchange,requestKey}} refreshVersion={refresh} onLoadingChange={setRefreshing} showVisual={false} showStatus={false} showShareSort={false} showNoticeFilter={false} showTableIntro={false} defaultDecimals={1} showAmountUnit showCompositionInitials fitColumns lookbackDays={8} storageNote=""/> : <p className="index2-live-status">수집 기준 시각 확인 중…</p>}</section>
  <section className="portable-right" aria-label="거래소 점유율">
   <section className="mg-card" data-size="fit"><div className="mg-card-head"><div className="mg-card-title"><h2>거래소 구성 · {windowLabel(windowSec)}</h2><p>국내 5개 거래소 실시간 체결</p></div><div className="mg-card-actions portable-window-picker"><Index2FilterButton label="분석 구간" triggerLabel={windowLabel(windowSec)} value={String(windowSec)} onChange={v=>setWindow(Number(v))} options={ANALYSIS_WINDOWS.map(w=>[String(w.seconds),w.label] as const)}/></div></div>
    <div className="mg-card-body"><Band label="거래소 구성" parts={(current?.exchanges??[]).map(r=>({key:r.exchange,share:r.share_pct,color:`var(--exchange-${r.exchange})`}))}/><div className="mg-table-wrap"><table className="mg-table"><thead><tr><th>거래소</th><th className="mg-num">점유율</th><th className="mg-num">거래금액</th><th className="mg-num">체결</th><th className="mg-num">체결 비중</th></tr></thead><tbody>{(current?.exchanges??[]).map(r=><tr key={r.exchange} className={r.exchange==='bithumb'?'mg-me':undefined} data-selectable="" data-selected={selectedExchange===r.exchange?'':undefined} onClick={()=>choose(r.exchange)}><td><button className="mg-link portable-exchange-filter" aria-label={`${exchangeLabel(r.exchange)} 코인 보기`} aria-pressed={selectedExchange===r.exchange}><Ex exchange={r.exchange}/></button></td><td className="mg-num"><LiveValue value={r.share_pct}>{formatSharePercent(r.share_pct)}</LiveValue></td><td className="mg-num">{money(r.volume_krw)}</td><td className="mg-num">{r.trade_count==null?'—':`${r.trade_count.toLocaleString()}건`}</td><td className="mg-num">{formatSharePercent(r.trade_count_share_pct)}</td></tr>)}</tbody></table></div></div>
   </section>
   <StandaloneChart key={windowSec} windowSec={windowSec} history={chart.window===windowSec?chart.history:[]} receiving={!!current&&!feed.error}/>
   <section className="mg-card" data-size="fit"><div className="mg-card-head"><div className="mg-card-title"><h2>{windowLabel(windowSec)} 거래소 비교</h2><p>직전 같은 구간과 비교</p></div></div><div className="mg-table-wrap"><table className="mg-table"><thead><tr><th>거래소</th><th className="mg-num">거래금액</th><th className="mg-num">변화</th><th className="mg-num">점유율</th><th className="mg-num">점유율 변화</th></tr></thead><tbody>{(current?.changes??[]).map(r=><tr key={r.exchange}><td><Ex exchange={r.exchange}/></td><td className="mg-num">{money(r.amount)}</td><td className="mg-num">{r.delta==null?'—':`${r.delta>0?'+':''}${money(r.delta)}`}</td><td className="mg-num">{formatSharePercent(r.before)} → {formatSharePercent(r.after)}</td><td className="mg-num">{r.share_delta==null?'—':`${r.share_delta>0?'+':''}${r.share_delta.toFixed(2)}%p`}</td></tr>)}</tbody></table></div></section>
  </section></main>
 </div>;
}
function StandaloneChart({windowSec,history,receiving}:{windowSec:number;history:LiveChartHistoryPoint[];receiving:boolean}){
 const explorer=useLiveChartExplorer(history,120000);
 return <section className="mg-card portable-flow-card" data-size="fit" aria-label="실시간 점유율"><LiveChartControls explorer={explorer} receiving={receiving} showNavigatorHelp={false} showCoverageNote={false} compact heading={`실시간 점유율 · ${windowLabel(windowSec)}`}/><LiveExchangeChartContent history={explorer.visible} metric="share" explorer={explorer} flowing={{running:receiving,sampleIntervalMS:1000,pauseOnHover:false}} showValues={false} singleChart/></section>;
}
