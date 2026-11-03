/** Browser request ownership; abort alone does not fence a late completion. */
export class QuestionRequestGate {
  private generation = 0;
  private active: {
    generation: number;
    controller: AbortController;
    timer: ReturnType<typeof setTimeout>;
  } | null = null;

  start(timeoutMS: number, onTimeout: () => void) {
    if (this.active) return null;
    const generation = ++this.generation;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      if (this.active?.generation !== generation) return;
      this.cancel();
      onTimeout();
    }, timeoutMS);
    this.active = { generation, controller, timer };
    return {
      signal: controller.signal,
      finish: (): boolean => {
        if (this.active?.generation !== generation) return false;
        clearTimeout(this.active.timer);
        this.active = null;
        return true;
      },
    };
  }

  cancel(): boolean {
    const active = this.active;
    this.active = null;
    ++this.generation;
    if (!active) return false;
    clearTimeout(active.timer);
    active.controller.abort();
    return true;
  }
}

// The server owns one five-minute budget for the whole question. Allow ten
// seconds for its terminal response while still fencing late completions.
export function marketQuestionRequestTimeoutMS(_development: boolean): number {
  return 310_000;
}
export const MARKET_QUESTION_REQUEST_TIMEOUT_MS =
  marketQuestionRequestTimeoutMS(import.meta.env?.DEV === true);
export const NOTICE_QUESTION_REQUEST_TIMEOUT_MS = 310_000;
export const QUESTION_TIMEOUT_MESSAGE =
  "답변 대기 시간이 길어 요청을 중단했습니다. 질문을 짧게 나누어 다시 시도해 주세요.";

export class QuestionRequestError extends Error {
  readonly status: number;
  readonly detail: string;

  constructor(status: number, detail = "") {
    super(questionHTTPErrorMessage(status, detail));
    this.name = "QuestionRequestError";
    this.status = status;
    this.detail = detail;
  }
}

function questionHTTPErrorMessage(status: number, detail: string): string {
  if (
    status === 422 &&
    detail.includes("data-completeness questions are restricted")
  ) {
    return "수집 누락이나 내부 저장 구조는 이 질문 화면에서 확인할 수 없습니다. 거래소·코인·기간의 점유율이나 공식 공지 내용으로 질문을 바꿔 주세요.";
  }
  if (status === 400 || status === 413 || status === 422) {
    return "질문 형식이나 범위를 확인해 주세요. 거래소·코인·기간을 분명히 적고 2~500자로 질문해 주세요. 계속 거절되면 기억을 지운 뒤 새 질문으로 시작해 주세요.";
  }
  if (status === 408 || status === 504) return QUESTION_TIMEOUT_MESSAGE;
  if (status === 429) {
    return "요청이 많아 잠시 질문을 받을 수 없습니다. 잠시 뒤 다시 시도해 주세요.";
  }
  if (status === 401 || status === 403) {
    return "이 질문 기능에 접근할 수 없습니다. 연결이나 접근 설정을 확인해 주세요.";
  }
  return "답변 서비스에 일시적인 문제가 있습니다. 잠시 뒤 다시 시도해 주세요.";
}

export async function readQuestionRequestError(
  response: Response
): Promise<QuestionRequestError> {
  let detail = "";
  try {
    const payload: unknown = await response.json();
    if (
      payload &&
      typeof payload === "object" &&
      "detail" in payload &&
      typeof payload.detail === "string"
    ) {
      detail = payload.detail.slice(0, 1_000);
    }
  } catch {
    // A proxy's non-JSON error still has useful HTTP status semantics.
  }
  return new QuestionRequestError(response.status, detail);
}

export function questionRequestErrorMessage(error: unknown): string {
  if (error instanceof QuestionRequestError) return error.message;
  if (
    error instanceof Error &&
    error.message.startsWith("Grounded question")
  )
    return "답변 단계가 서로 맞지 않아 설명을 표시하지 않았습니다. 먼저 받은 계산 결과가 있으면 그대로 유지합니다.";
  return "답변 서버와 연결하지 못했습니다. 연결을 확인한 뒤 다시 시도해 주세요.";
}
