import { useId, useRef, type PointerEvent } from "react";
import { Info, RotateCcw } from "lucide-react";

import type { LiveChartExplorer } from "@/hooks/useLiveChartExplorer";
import { liveChartTime } from "@/lib/live-chart-explorer";
import { useWidth } from "@/screens/charts";
import { ObservedRangeHelp } from "./common/ObservedRangeHelp";

export function LiveChartControls({
  explorer,
  receiving,
  showNavigatorHelp = true,
  compact = false,
  showCoverageNote = true,
  heading,
}: {
  explorer: LiveChartExplorer;
  receiving: boolean;
  showNavigatorHelp?: boolean;
  compact?: boolean;
  showCoverageNote?: boolean;
  heading?: string;
}) {
  const headingSplit = heading?.lastIndexOf(" · ") ?? -1;
  const help = `선 위에서 같은 시각을 함께 확인합니다. 클릭·터치로 고정${showNavigatorHelp ? "하고, 하단에서 구간을 확대하세요." : "할 수 있습니다."}`;
  const selected =
    explorer.visible[explorer.selectedIndex] ?? explorer.visible.at(-1);
  return (
    <div className={`mg-chart-controls${compact ? " mg-chart-controls-inline" : ""}`}>
      {heading && <h2 className="mg-chart-inline-heading" title={heading}><span className="mg-chart-inline-title">{headingSplit >= 0 ? heading.slice(0, headingSplit) : heading}</span>{headingSplit >= 0 && <span className="mg-chart-inline-window">{heading.slice(headingSplit)}</span>}</h2>}
      <div className="mg-chart-toolbar">
        <div className="mg-seg" role="group" aria-label="차트 표시 범위">
          {(
            [
              [30_000, "30초"],
              [60_000, "1분"],
              [120_000, "2분"],
              [null, "전체"],
            ] as const
          ).map(([duration, label]) => (
            <button
              key={label}
              type="button"
              aria-pressed={!explorer.held && explorer.durationMS === duration}
              onClick={() => explorer.follow(duration)}
            >
              {label}
            </button>
          ))}
        </div>
        <button
          type="button"
          className="mg-chart-follow"
          aria-label="최신 흐름으로"
          title="최신 흐름으로"
          onClick={() => explorer.follow()}
          disabled={!explorer.held && explorer.selectedAt === null}
        >
          {compact ? <RotateCcw size={16} aria-hidden="true" /> : "최신 흐름으로"}
        </button>
      </div>
      <div className="mg-chart-reading" data-testid="live-chart-reading">
        <span className="mg-chart-mode" data-held={explorer.held}>
          {explorer.pinned
            ? "시점 고정"
            : explorer.held
              ? "구간 고정"
              : "최신 흐름"}
        </span>
        <time>
          {liveChartTime(selected?.at ?? explorer.visible.at(-1)?.at)} KST
        </time>
        <span className="mg-muted">
          {receiving ? "수신 중" : "마지막 수신 자료"}
        </span>
        {selected?.gap ? (
          <span className="mg-attn">연결 공백 · 자료 없음</span>
        ) : null}
        {showCoverageNote && selected?.partial ? <ObservedRangeHelp /> : null}
      </div>
      {compact ? <span className="mg-chart-inline-help" role="img" tabIndex={0} aria-label={help} title={help}><Info size={16} aria-hidden="true" /></span> : <p className="mg-chart-help">{help}</p>}
    </div>
  );
}

/** Small timestamp-based brush, independent of the live chart's render store. */
function LiveChartBrush({ explorer }: { explorer: LiveChartExplorer }) {
  const { ref, width } = useWidth<HTMLDivElement>();
  const drag = useRef<{
    kind: "start" | "end" | "pan";
    from: number;
    range: readonly [number, number];
  } | null>(null);
  const rows = explorer.source;
  const pad = 16;
  const inner = Math.max(1, width - pad * 2);
  const from = rows[0]?.at ?? 0;
  const span = Math.max(1, (rows.at(-1)?.at ?? from) - from);
  const x = (index: number) =>
    pad + (((rows[index]?.at ?? from) - from) / span) * inner;
  const indexAt = (clientX: number, svg: SVGSVGElement) => {
    const at =
      from +
      ((clientX - svg.getBoundingClientRect().left - pad) / inner) * span;
    let picked = 0;
    for (let i = 1; i < rows.length; i++) {
      if (Math.abs(rows[i].at - at) < Math.abs(rows[picked].at - at))
        picked = i;
    }
    return picked;
  };
  const startDrag = (
    event: PointerEvent<SVGElement>,
    kind: "start" | "end" | "pan"
  ) => {
    if (event.button !== 0) return;
    const svg = event.currentTarget.ownerSVGElement!;
    drag.current = {
      kind,
      from: indexAt(event.clientX, svg),
      range: explorer.range,
    };
    explorer.zoom(...explorer.range, true);
    svg.setPointerCapture(event.pointerId);
  };
  let drawing = false;
  const path = rows
    .map((row, index) => {
      const value = row.share_bithumb;
      if (row.gap || value == null || !Number.isFinite(value)) {
        drawing = false;
        return "";
      }
      const segment = `${drawing ? "L" : "M"}${x(index)},${52 - (value / 100) * 40}`;
      drawing = true;
      return segment;
    })
    .join(" ");
  const left = x(explorer.range[0]);
  const right = x(explorer.range[1]);
  return (
    <div
      ref={ref}
      className="mg-chart-brush"
      aria-label="점유율과 거래 흐름 구간 선택"
    >
      <svg
        width="100%"
        height="80"
        viewBox={`0 0 ${Math.max(1, width)} 80`}
        onPointerMove={event => {
          const current = drag.current;
          if (!current) return;
          const picked = indexAt(event.clientX, event.currentTarget);
          const [first, last] = current.range;
          if (current.kind === "start")
            explorer.zoom(Math.min(picked, last - 1), last);
          else if (current.kind === "end")
            explorer.zoom(first, Math.max(picked, first + 1));
          else {
            const start = Math.max(
              0,
              Math.min(
                rows.length - 1 - (last - first),
                first + picked - current.from
              )
            );
            explorer.zoom(start, start + last - first);
          }
        }}
        onPointerUp={event => {
          drag.current = null;
          if (event.currentTarget.hasPointerCapture(event.pointerId))
            event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
        onLostPointerCapture={() => {
          drag.current = null;
        }}
      >
        <rect
          x={pad}
          y={8}
          width={inner}
          height={48}
          fill="var(--background)"
          stroke="var(--border)"
          rx={4}
        />
        <path
          d={path}
          fill="none"
          stroke="var(--exchange-bithumb)"
          strokeWidth={1.5}
        />
        <rect
          className="mg-chart-brush-pan"
          x={left}
          y={8}
          width={Math.max(0, right - left)}
          height={48}
          fill="var(--foreground)"
          fillOpacity={0.08}
          onPointerDown={event => startDrag(event, "pan")}
        />
        {(["start", "end"] as const).map((kind, position) => {
          const index = explorer.range[position];
          return (
            <g
              key={kind}
              className="mg-chart-brush-handle"
              role="slider"
              tabIndex={0}
              aria-label={kind === "start" ? "구간 시작" : "구간 끝"}
              aria-valuemin={kind === "start" ? 0 : explorer.range[0] + 1}
              aria-valuemax={
                kind === "start" ? explorer.range[1] - 1 : rows.length - 1
              }
              aria-valuenow={index}
              aria-valuetext={`${liveChartTime(rows[index]?.at)} KST`}
              onPointerDown={event => startDrag(event, kind)}
              onKeyDown={event => {
                if (
                  !["ArrowLeft", "ArrowRight", "Home", "End"].includes(
                    event.key
                  )
                )
                  return;
                event.preventDefault();
                const min = kind === "start" ? 0 : explorer.range[0] + 1;
                const max =
                  kind === "start" ? explorer.range[1] - 1 : rows.length - 1;
                const picked =
                  event.key === "Home"
                    ? min
                    : event.key === "End"
                      ? max
                      : Math.max(
                          min,
                          Math.min(
                            max,
                            index + (event.key === "ArrowRight" ? 1 : -1)
                          )
                        );
                explorer.zoom(
                  kind === "start" ? picked : explorer.range[0],
                  kind === "end" ? picked : explorer.range[1],
                  true
                );
              }}
            >
              <rect
                x={x(index) - 12}
                y={4}
                width={24}
                height={56}
                rx={4}
                fill="var(--card)"
                stroke="var(--muted-foreground)"
              />
              <path
                d={`M${x(index) - 2},24v12 M${x(index) + 2},24v12`}
                stroke="var(--foreground)"
              />
            </g>
          );
        })}
      </svg>
    </div>
  );
}

/** Overview + detail: a small brush changes both charts, using timestamps. */
export function LiveChartNavigator({
  explorer,
  showText = true,
}: {
  explorer: LiveChartExplorer;
  showText?: boolean;
}) {
  const inputID = useId();
  const first = explorer.visible[0]?.at;
  const last = explorer.visible.at(-1)?.at;
  const count = explorer.visible.length;
  return (
    <div className="mg-chart-navigator">
      <div className="mg-chart-range-caption">
        <b>구간 탐색</b>
        <span>
          {liveChartTime(first)} – {liveChartTime(last)} KST
        </span>
      </div>
      {explorer.source.length > 1 ? (
        <LiveChartBrush explorer={explorer} />
      ) : (
        <p className="mg-muted">
          구간 탐색에 필요한 실시간 자료를 모으고 있습니다.
        </p>
      )}
      <div className="mg-chart-scrubber">
        <label htmlFor={inputID} className={showText ? undefined : "mg-sr-only"}>시점 탐색</label>
        <button
          type="button"
          onClick={() => explorer.step(-1)}
          disabled={count < 2 || explorer.selectedIndex === 0}
          aria-label="이전 시점"
        >
          ←
        </button>
        <input
          id={inputID}
          type="range"
          min={0}
          max={Math.max(0, count - 1)}
          value={
            explorer.selectedIndex < 0
              ? Math.max(0, count - 1)
              : explorer.selectedIndex
          }
          disabled={count < 2}
          aria-valuetext={liveChartTime(explorer.selectedAt ?? last)}
          onChange={event => {
            const row = explorer.visible[Number(event.currentTarget.value)];
            if (row) explorer.pin(row.at);
          }}
        />
        <button
          type="button"
          onClick={() => explorer.step(1)}
          disabled={count < 2 || explorer.selectedIndex === count - 1}
          aria-label="다음 시점"
        >
          →
        </button>
      </div>
      {showText && <p className="mg-chart-help">
        양끝 손잡이를 끌어 확대하고 가운데를 끌어 이동합니다. 방향키로도 조절할
        수 있습니다. 수신한 최근 5분까지만 보관합니다.
      </p>}
    </div>
  );
}
