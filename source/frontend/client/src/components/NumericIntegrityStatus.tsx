import { Panel } from "@/components/common/Panel";
import {
  numericIntegrityPresentation,
  type NumericIntegrityReport,
} from "@/lib/numeric-integrity-presentation";

export type { NumericIntegrityReport } from "@/lib/numeric-integrity-presentation";

export function NumericIntegrityStatus({
  report,
  observationComplete,
}: {
  report?: NumericIntegrityReport | null;
  observationComplete?: boolean;
}) {
  const state = numericIntegrityPresentation(report);
  return (
    <Panel
      title="수치 계산 확인"
      aria-label="수치 계산 확인"
      description="현재 화면의 국내 거래금액 합계와 점유율 계산을 같은 자료 안에서 대조합니다."
      actions={
        <span className={`ui-body font-semibold ${state.className}`}>
          {state.label}
        </span>
      }
    >
      <div className="space-y-2 px-4 py-3 sm:px-5">
        <p className="ui-body">
          {state.scope} · {state.period}
          {state.count !== null ? ` · ${state.count}개 조건 검사` : ""}
        </p>
        <p className="ui-caption text-muted-foreground">
          검사 대상: 거래소별 금액 합계 · 빗썸 금액 · 점유율 계산 · 점유율 합계
          {state.comparison
            ? " · 직전 구간 대비 변화"
            : ". 직전 구간 대비 변화는 검사하지 않았습니다."}
        </p>
        <p
          className={`ui-caption ${observationComplete === false ? "text-gold" : "text-muted-foreground"}`}
        >
          {observationComplete === false
            ? "관측 누락이 있습니다. 계산 일치와 자료의 완전성은 별개입니다."
            : "관측 누락·공식 분봉 보완 여부는 별도 확인합니다."}{" "}
          이 검사는 원본 거래가 빠졌거나 중복 저장됐는지까지 확인하지는
          않습니다.
        </p>
        {state.issues.length > 0 ? (
          <ul className="ui-caption list-disc space-y-1 pl-5 text-gold">
            {state.issues.map(message => (
              <li key={message}>{message}</li>
            ))}
          </ul>
        ) : null}
      </div>
    </Panel>
  );
}
