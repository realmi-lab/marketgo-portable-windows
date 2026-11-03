export function noticeAnalysisStatusLabel(
  value: string,
  reason?: string,
  contentSource?: string
): string {
  if (value === "ANALYZED") return "본문 분석 완료";
  if (value === "ANALYSIS_NOT_REQUIRED") return "공식 원문 확인 가능";
  if (value === "IMAGE_BODY_CONFLICT") return "본문·이미지 내용 불일치";
  if (value === "ANALYSIS_DEFERRED_QUOTA") {
    if (reason === "RETRY_RUNTIME_UNAVAILABLE") return "AI 분석 일시 지연";
    if (reason === "RETRY_QUOTA_OR_RATE_LIMIT") return "AI 분석 일시 지연";
    if (
      reason === "RETRY_TIMEOUT_OR_PREEMPTION" ||
      reason === "RETRY_PROVIDER_TRANSIENT"
    )
      return "AI 분석 재시도 대기";
    return "본문·일정 분석 대기";
  }
  if (value === "PENDING") return "본문·일정 분석 대기";
  if (value === "TRANSLATION_PENDING") return "번역 대기";
  if (value === "ANALYSIS_FAILED") {
    if (reason === "ANALYSIS_VALIDATION_REJECTED")
      // Short, because every affected row shows it; the detail explains it.
      return "원문 불일치 · 재분석 예정";
    if (contentSource === "HERMES_RAW_DETAIL")
      return "공식 자료 확보 · AI 재분석 대기";
    if (contentSource === "HERMES_RAW_DETAIL_EMPTY")
      return "공식 상세 내용 없음";
    return "자동 요약 실패 · 원문 보기";
  }
  if (value === "ERROR") return "자동 요약 실패 · 원문 보기";
  if (value === "DETAIL_ERROR") return "본문 확인 오류";
  return "분석 상태 확인 필요";
}

export function noticeAnalysisStatusDetail(
  value: string,
  reason?: string,
  contentSource?: string
): string | null {
  if (value === "ANALYSIS_NOT_REQUIRED") {
    return "저장된 공식 원문을 확인하거나 이 공지에 대해 질문할 수 있습니다.";
  }
  if (value === "ANALYSIS_DEFERRED_QUOTA") {
    if (reason?.startsWith("RETRY_") && contentSource === "HERMES_RAW_DETAIL") {
      return "공식 본문·이미지는 확보됐으며, 일시적인 분석 실패가 해소된 뒤 다시 시도합니다.";
    }
    if (contentSource === "HERMES_RAW") {
      return "공식 공지를 발견했으며 상세 본문·이미지를 확인하고 있습니다.";
    }
    return "AI가 본문을 읽고 이벤트 기간·신청 기간·지급일과 이미지 문구를 추출하고 있습니다.";
  }
  if (value === "ANALYSIS_FAILED" && contentSource === "HERMES_RAW_DETAIL") {
    if (reason === "ANALYSIS_VALIDATION_REJECTED") {
      return "공식 원문과 맞지 않는 모델 결과를 저장하지 않았습니다. 확보된 원문은 질문 근거로 사용할 수 있으며 다음 검증 주기에 다시 분석합니다.";
    }
    return "확보된 공식 본문·이미지는 질문 근거로 사용할 수 있으며, AI 요약·일정은 다음 검증 주기에 다시 분석합니다.";
  }
  if (
    value === "ANALYSIS_FAILED" &&
    contentSource === "HERMES_RAW_DETAIL_EMPTY"
  ) {
    return "공식 상세 본문·이미지를 확보하지 못해 다음 검증 주기에 다시 확인합니다.";
  }
  return null;
}

/**
 * Notice summaries are stored by the notice worker, and earlier ones say
 * "미확보" for a time the notice did not state. Current text says
 * "정보 없음"; this rewrites the stored wording only.
 */
export function noticeSummaryDisplayText(text: string): string {
  return (
    text
      /* legacy server text: begin */
      .replaceAll("시작·종료 시각 미확보", "시작·종료 시각 정보 없음")
      .replaceAll("기간·조치 시각 미확보", "기간·조치 시각 정보 없음")
      .replaceAll("종료 시각 미확보", "종료 시각 정보 없음")
      .replaceAll("시작 시각 미확보", "시작 시각 정보 없음")
    /* legacy server text: end */
  );
}
