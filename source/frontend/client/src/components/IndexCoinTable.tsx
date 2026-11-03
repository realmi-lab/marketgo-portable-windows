import { useMemo, useState } from "react";
import { indexDisplayAmount, indexTurnoverUnsupported, indexRowStatus, type IndexDayRow } from "@/lib/index-day";

const venues = [
  ["bithumb", "빗썸"],
  ["coinone", "코인원"],
  ["upbit", "업비트"],
  ["korbit", "코빗"],
  ["gopax", "고팍스"],
] as const;
export function formatIndexEok(value: number | null) {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value > 0 && value < 10000) return "0.0001억 원 미만";
  return `${(value / 100000000).toLocaleString("ko-KR", { maximumFractionDigits: 4 })}억 원`;
}
export default function IndexCoinTable({
  rows,
  loading,
  error,
}: {
  rows: IndexDayRow[];
  loading: boolean;
  error: string;
}) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const coins = useMemo(() => {
    const grouped = new Map<string, IndexDayRow[]>();
    for (const row of rows)
      grouped.set(row.base, [...(grouped.get(row.base) ?? []), row]);
    return [...grouped]
      .filter(([base, markets]) =>
        `${base} ${markets.map(r => r.market).join(" ")}`
          .toLowerCase()
          .includes(query.toLowerCase())
      )
      .sort(([a], [b]) => a.localeCompare(b));
  }, [rows, query]);
  const pages = Math.max(1, Math.ceil(coins.length / 100));
  const activePage = Math.min(page, pages - 1);
  return (
    <section
      aria-label="코인별 하루 거래 표"
      className="overflow-hidden rounded-xl border border-border bg-card"
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border p-4">
        <div>
          <h3 className="ui-title font-semibold">코인별 거래금액</h3>
          <p className="ui-caption mt-1 text-muted-foreground">
            원화 환산 · 단위: 억 원 · {coins.length.toLocaleString()}개 코인
          </p>
        </div>
        <input
          aria-label="코인 또는 마켓 검색"
          placeholder="코인·마켓 검색"
          value={query}
          onChange={e => {
            setQuery(e.target.value);
            setPage(0);
          }}
          className="min-h-11 w-40 rounded-lg border border-border bg-background px-3 text-base"
        />
      </div>
      <div
        className="overflow-x-auto"
        tabIndex={0}
        aria-label="코인 거래 표 가로 스크롤"
      >
        <table className="w-full min-w-[1600px] text-left">
          <thead className="bg-secondary">
            <tr>
              <th
                rowSpan={2}
                scope="col"
                className="sticky left-0 z-10 bg-card px-4"
              >
                코인
              </th>
              {venues.map(([id, name]) => (
                <th
                  key={id}
                  scope="colgroup"
                  colSpan={2}
                  className="border-l border-border px-4 py-3 text-center text-base"
                >
                  {name}
                </th>
              ))}
            </tr>
            <tr>
              {venues.flatMap(([id, name]) =>
                ["원화 마켓", "기타 마켓"].map(label => (
                  <th
                    key={`${id}:${label}`}
                    scope="col"
                    aria-label={`${name} ${label}`}
                    className="px-4 py-3 text-right text-base font-medium"
                  >
                    {label}
                  </th>
                ))
              )}
            </tr>
          </thead>
          <tbody>
            {coins
              .slice(activePage * 100, (activePage + 1) * 100)
              .map(([base, markets]) => (
                <tr key={base} className="border-t border-border/60">
                  <th
                    scope="row"
                    className="sticky left-0 bg-card px-4 py-4 text-base"
                  >
                    {base}
                  </th>
                  {venues.flatMap(([id]) =>
                    [true, false].map(krw => {
                      const selected = markets
                        .filter(
                          r => r.exchange === id && (r.quote === "KRW") === krw
                        )
                        .sort((a, b) => a.quote.localeCompare(b.quote));
                      return (
                        <td
                          key={`${id}:${krw}`}
                          className="px-4 py-4 text-right align-top text-base tabular-nums"
                        >
                          {selected.length ? (
                            selected.map(row => (
                              <div key={row.market} className="mb-2 last:mb-0">
                                <p className="whitespace-nowrap font-medium">
                                  {formatIndexEok(indexDisplayAmount(row.observed_volume_krw ?? row.volume_krw, row.status === "TURNOVER_UNAVAILABLE" || (indexTurnoverUnsupported(row.exchange) && row.hours > 0 && row.amount_hours === 0)))}
                                </p>
                                <p className="ui-caption text-muted-foreground">
                                  {row.quote}
                                  {row.observed_amount_hours != null && row.observed_amount_hours > 0 ? ` · ${row.observed_amount_hours}/24시간` : ""}
                                  {indexTurnoverUnsupported(row.exchange) && row.hours > 0 && row.amount_hours === 0 && row.status !== "TURNOVER_UNAVAILABLE" ? " · 거래금액 미제공" : ""}
                                  {row.volume_krw == null ||
                                  row.status === "COMPLETE_ESTIMATED"
                                    ? ` · ${indexRowStatus(row.status)}`
                                    : ""}
                                </p>
                              </div>
                            ))
                          ) : (
                            <span aria-label="확인된 마켓 없음">—</span>
                          )}
                        </td>
                      );
                    })
                  )}
                </tr>
              ))}
            {!coins.length && (
              <tr>
                <td
                  colSpan={11}
                  className="p-8 text-base text-muted-foreground"
                >
                  {loading
                    ? "선택한 날짜의 자료를 불러오는 중입니다."
                    : error
                      ? "자료를 불러오지 못했습니다. 다시 시도해 주세요."
                      : "선택한 조건에 맞는 자료가 없습니다."}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-border p-3">
        <span className="ui-caption">
          {activePage + 1} / {pages} 페이지
        </span>
        <div className="flex gap-2">
          {[
            [-1, "이전"],
            [1, "다음"],
          ].map(([step, label]) => (
            <button
              key={label}
              type="button"
              disabled={
                Number(step) < 0 ? activePage === 0 : activePage + 1 >= pages
              }
              onClick={() => setPage(activePage + Number(step))}
              className="min-h-11 rounded-lg border border-border px-4 text-base disabled:opacity-40"
            >
              {label}
            </button>
          ))}
        </div>
      </div>
    </section>
  );
}
