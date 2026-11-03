import { Clock3, RefreshCw } from "lucide-react";
import type { IndexDayResponse } from "@/lib/index-day";
import { indexStatus } from "@/lib/index-status";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import "./index-data-status.css";

const periodTime = (seconds: number) => new Intl.DateTimeFormat("ko-KR", {
  timeZone: "Asia/Seoul", month: "2-digit", day: "2-digit",
  hour: "2-digit", minute: "2-digit", hour12: false,
}).format(new Date(seconds * 1000));

export function IndexDataStatus({ data, loading, error, updated, onRefresh, compact = false }: {
  data: IndexDayResponse | null; loading: boolean; error: string; updated: number;
  onRefresh: () => void; compact?: boolean;
}) {
  const status = indexStatus(data, error);
  return <div className={`index-data-status${compact ? " is-compact" : ""}`} aria-label="인덱스 자료 상태">
    <div className="ids-explanation">
      <span className={`ids-badge is-${status.tone}`}><i aria-hidden="true"/>{status.label}</span>
      <p>{status.detail}</p>
    </div>
    {data?.storage_mode === "ROLLING_24H_LATEST" && (data.failed_markets ?? 0) > 0 && <p className="ids-coverage">24시간 시간봉이 아직 다 모이지 않은 마켓 {data.failed_markets?.toLocaleString("ko-KR")}개</p>}
    {data && <div className="ids-period"><Clock3 size={14} aria-hidden="true"/><span>집계 기간 <strong>{periodTime(data.start)} – {periodTime(data.end)}</strong> <span>한국시간</span></span></div>}
    <div className="ids-update">
      <span>{loading ? (data ? "새 자료 확인 중" : "불러오는 중") : updated && data ? `최근 조회 ${formatMarketTimeMS(updated, "time-seconds")}` : ""}</span>
      <button type="button" aria-label="날짜 자료 새로고침" onClick={onRefresh} disabled={loading}><RefreshCw size={15} aria-hidden="true"/>새로고침</button>
    </div>
  </div>;
}
