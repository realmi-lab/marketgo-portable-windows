import type { ExchangeNotice } from "@/lib/market-share-types";

export function noticePipelineLabel(
  quality: ExchangeNotice["notice_quality_v2"]
) {
  if (!quality || quality.schema_version !== 2) return null;
  if (quality.summary_state === "COMPLETE") return "요약 검토 완료";
  if (quality.summary_state === "NEEDS_REVIEW") return "요약 재검토 필요";
  if (quality.summary_state === "SECTION_ONLY") return "일부 내용만 요약됨";
  if (quality.answerable_scope === "IMAGE_ONLY") return "이미지 내용 확인 필요";
  if (quality.answerable_scope === "LIST_ONLY") return "본문 확인 중";
  if (quality.summary_state === "WAITING_BUDGET") return "요약 대기 중";
  if (quality.summary_state === "WAITING_INPUT")
    return "추가 검토가 필요한 원문";
  if (quality.summary_state === "CANCELLED") return "요약 처리 중지";
  return "요약 확인 중";
}

export function NoticePipelineQuality({
  quality,
}: {
  quality: ExchangeNotice["notice_quality_v2"];
}) {
  if (!quality || quality.schema_version !== 2) return null;
  const summary = noticePipelineLabel(quality);
  const freshness =
    quality.freshness === "UPDATE_PENDING"
      ? "수정 여부 확인 중"
      : quality.freshness === "STALE"
        ? "원문 재확인 필요"
        : null;
  return (
    <div
      className="mt-2 rounded border border-border px-3 py-2 text-muted-foreground"
      data-testid="notice-pipeline-quality"
    >
      <p className="ui-caption">
        {summary}
        {freshness && ` · ${freshness}`}
      </p>
      {quality.body_state !== "COMPLETE" && (
        <p className="mt-1 ui-caption">원문을 모두 확인하지 못했습니다.</p>
      )}
      {quality.summary_state === "SECTION_ONLY" && (
        <p className="mt-1 ui-caption">
          전체 조건은 공식 원문을 확인해 주세요.
        </p>
      )}
      {quality.ocr_state && quality.ocr_state !== "COMPLETE" && (
        <p className="mt-1 ui-caption">
          이미지 내용을 모두 확인하지 못했습니다. 원본 이미지를 확인해 주세요.
        </p>
      )}
      {!!quality.section_summaries?.length && (
        <details className="mt-2">
          <summary className="cursor-pointer ui-body">확인된 내용 보기</summary>
          <ul className="mt-2 list-disc space-y-1 pl-5 ui-body">
            {quality.section_summaries.map((text, i) => (
              <li key={i} className="whitespace-pre-wrap break-words">
                {text}
              </li>
            ))}
          </ul>
        </details>
      )}
      {quality.required_conditions.length > 0 && (
        <details className="mt-2">
          <summary className="cursor-pointer ui-body">
            함께 확인할 조건 {quality.required_conditions.length}개
          </summary>
          <ul className="mt-2 list-disc space-y-1 pl-5 ui-body">
            {quality.required_conditions.map((condition, index) => (
              <li key={index} className="whitespace-pre-wrap break-words">
                {condition}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}
