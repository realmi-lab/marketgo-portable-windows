import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  Bot,
  CornerDownLeft,
  ExternalLink,
  LoaderCircle,
  Sparkles,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { askHermesNoticeQuestion } from "@/lib/api-client";
import {
  NOTICE_QUESTION_REQUEST_TIMEOUT_MS,
  QUESTION_TIMEOUT_MESSAGE,
  QuestionRequestGate,
  questionRequestErrorMessage,
} from "@/lib/question-request";
import { noticeQuestionAnswerModeLabel } from "@/lib/market-share-labels";
import type {
  MarketShareHermesNoticeQuestionAnswer,
  NoticeQuestionPersona,
} from "@/lib/market-share-types";

const LLM_ENABLED = import.meta.env.VITE_LLM_ENABLED !== "false";

const PERSONAS: Array<{
  id: NoticeQuestionPersona;
  label: string;
  question: string;
}> = [
  {
    id: "AUTO",
    label: "자동",
    question: "최근 7일 국내 거래소 공지에서 중요한 변화만 정리해 줘.",
  },
  {
    id: "FACT_RESEARCHER",
    label: "사실 조회",
    question: "최근 7일 새로 거래지원된 코인과 입출금이 중단된 코인을 알려줘.",
  },
  {
    id: "VOLUME_ANALYST",
    label: "거래금액",
    question: "최근 7일 상장된 코인의 일별 국내 KRW 거래금액을 정리해 줘.",
  },
  {
    id: "EVENT_BRIEFER",
    label: "이벤트 요약",
    question: "최근 7일 국내 거래소 이벤트를 거래소별로 정리해 줘.",
  },
  {
    id: "BITHUMB_STYLE_WRITER",
    label: "빗썸 문체 초안",
    question:
      "빗썸 입출금 중단 공지의 문법과 유의사항을 반영한 검토용 초안을 써 줘.",
  },
];

function answerTimestamp(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "현재 수집 자료 기준";
  return `${new Date(value).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })} 기준`;
}

export function HermesNoticeQuestion({
  answerScrollable = false,
  compact = false,
}: {
  answerScrollable?: boolean;
  compact?: boolean;
}) {
  const [persona, setPersona] = useState<NoticeQuestionPersona>("AUTO");
  const [question, setQuestion] = useState(PERSONAS[0].question);
  const [answer, setAnswer] =
    useState<MarketShareHermesNoticeQuestionAnswer | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const requests = useRef(new QuestionRequestGate());
  const contextSourceIDs = useRef<string[]>([]);
  const [submittedQuestion, setSubmittedQuestion] = useState("");
  const [answerQuestion, setAnswerQuestion] = useState("");

  useEffect(
    () => () => {
      requests.current.cancel();
    },
    []
  );

  const cancelRequest = () => {
    if (!requests.current.cancel()) return;
    setPending(false);
    setSubmittedQuestion("");
    setError("요청을 취소했습니다. 질문을 바꾸거나 다시 보낼 수 있습니다.");
  };

  const selectPersona = (next: (typeof PERSONAS)[number]) => {
    if (!LLM_ENABLED) return;
    // A changed persona starts a new draft. Never publish the abandoned
    // request or let its sources replace the last completed source context.
    requests.current.cancel();
    setPending(false);
    setSubmittedQuestion("");
    setPersona(next.id);
    setQuestion(next.question);
    // A persona changes how the next question is answered, not which verified
    // notices the conversation refers to. Keep the last official-source set
    // available for cross-persona follow-ups such as fact lookup -> volume.
    setAnswer(null);
    setAnswerQuestion("");
    setError("");
  };

  const uniqueKeyPoints =
    answer?.key_points.filter(point => !answer.answer.includes(point)) ?? [];

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!LLM_ENABLED) return;
    const normalized = question.trim();
    if (normalized.length < 2 || pending) return;
    const request = requests.current.start(
      NOTICE_QUESTION_REQUEST_TIMEOUT_MS,
      () => {
        setPending(false);
        setSubmittedQuestion("");
        setError(QUESTION_TIMEOUT_MESSAGE);
      }
    );
    if (!request) return;
    setPending(true);
    setSubmittedQuestion(normalized);
    setError("");
    try {
      const nextAnswer = await askHermesNoticeQuestion(
        persona,
        normalized,
        contextSourceIDs.current,
        request.signal
      );
      const nextSourceIDs = nextAnswer.sources.map(
        source => `${source.exchange}:${source.notice_id}`
      );
      if (!request.finish()) return;
      contextSourceIDs.current = nextSourceIDs;
      setAnswer(nextAnswer);
      setAnswerQuestion(normalized);
      setPending(false);
      setSubmittedQuestion("");
    } catch (error) {
      if (!request.finish()) return;
      setError(questionRequestErrorMessage(error));
      setPending(false);
      setSubmittedQuestion("");
    }
  };

  return (
    <section
      className="hermes-market-question hermes-notice-question"
      aria-labelledby="hermes-notice-question-title"
      tabIndex={answerScrollable ? 0 : undefined}
    >
      <div className="hermes-market-question-heading">
        <span className="hermes-market-question-icon" aria-hidden="true">
          <Bot className="h-4 w-4" />
        </span>
        <div>
          <div className="flex items-center gap-2">
            <h3 id="hermes-notice-question-title">
              {LLM_ENABLED
                ? compact
                  ? "공지에 질문하기"
                  : "국내 공지 답변"
                : "공지 답변 비활성화"}
            </h3>
            <span className="hermes-market-question-badge">
              <Sparkles className="h-3 w-3" /> 국내 5개 거래소 근거
            </span>
          </div>
          <p>
            {LLM_ENABLED
              ? compact
                ? "공식 공지를 바탕으로 답합니다. 이전 공지는 날짜를 함께 입력해 주세요."
                : "업비트·빗썸·코인원·코빗·고팍스의 공식 공지와 확인된 거래금액을 사용합니다. 기본 검색은 최근 30일입니다. 이전 공지는 게시 날짜나 기간을 지정해 주세요. 공지 문안을 요청하면 게시 전에 검토할 초안으로 작성합니다."
              : "공식 공지 데이터는 계속 수집하지만 LLM 질문과 답변 생성은 사용하지 않습니다."}
          </p>
        </div>
      </div>

      <div
        className="hermes-market-question-suggestions"
        aria-label="답변 역할"
      >
        {PERSONAS.map(item => (
          <button
            key={item.id}
            type="button"
            disabled={!LLM_ENABLED}
            aria-pressed={persona === item.id}
            onClick={() => selectPersona(item)}
          >
            {item.label}
          </button>
        ))}
      </div>

      <form className="hermes-market-question-form" onSubmit={submit}>
        <label className="sr-only" htmlFor="hermes-notice-question-input">
          국내 거래소 공지에 대해 질문
        </label>
        <textarea
          id="hermes-notice-question-input"
          value={question}
          disabled={!LLM_ENABLED}
          onChange={event => setQuestion(event.target.value)}
          maxLength={500}
          rows={2}
          placeholder="예: 최근 7일 입출금이 중단된 코인은?"
        />
        <button
          type="submit"
          disabled={!LLM_ENABLED || pending || question.trim().length < 2}
        >
          {pending ? (
            <LoaderCircle className="h-4 w-4 animate-spin" />
          ) : (
            <CornerDownLeft className="h-4 w-4" />
          )}
          {!LLM_ENABLED ? "사용 안 함" : pending ? "근거 확인 중" : "질문하기"}
        </button>
      </form>

      {pending && (
        <div
          className="hermes-market-question-progress"
          role="status"
          aria-live="polite"
        >
          <div>
            <strong>제출한 공지 질문을 처리하고 있습니다.</strong>
            <p>“{submittedQuestion}”</p>
            <button
              type="button"
              onClick={cancelRequest}
              className="ui-body mt-2 underline underline-offset-4"
            >
              요청 취소
            </button>
          </div>
        </div>
      )}

      {error && <p className="hermes-market-question-error">{error}</p>}

      {answer && (
        <div
          className="hermes-market-answer"
          aria-live="polite"
          role={answerScrollable ? "region" : undefined}
          aria-label={
            answerScrollable ? "국내 공지 답변과 확인한 근거" : undefined
          }
          tabIndex={answerScrollable ? 0 : undefined}
        >
          <div className="hermes-market-answer-meta">
            <strong>국내 공지 답변</strong>
            <div className="hermes-market-answer-meta-detail">
              <span>{noticeQuestionAnswerModeLabel(answer.source)}</span>
              <span>{answerTimestamp(answer.as_of_ms)}</span>
            </div>
          </div>
          <p className="ui-caption mb-3 break-words">
            답변한 질문: {answerQuestion}
            <br />
            답변 역할:{" "}
            {PERSONAS.find(item => item.id === answer.persona)?.label ??
              "역할 확인 필요"}
          </p>
          {answer.source === "SERVER_NOTICE_FALLBACK" && (
            <p className="ui-caption mb-3 break-words">
              상세 AI 답변을 제공하지 못해, 확인 가능한 범위만 안내합니다.
            </p>
          )}
          <div
            className="hermes-market-markdown"
            aria-label="국내 공지 분석 답변"
          >
            <ReactMarkdown
              remarkPlugins={[remarkGfm]}
              components={{
                // Only the server-verified sources below are navigable. Model
                // links and images retain their visible text without requests.
                a: ({ children }) => <span>{children}</span>,
                img: ({ alt }) => <span>{alt ?? "이미지 설명 없음"}</span>,
              }}
            >
              {answer.answer}
            </ReactMarkdown>
          </div>
          {uniqueKeyPoints.length > 0 && (
            <ul>
              {uniqueKeyPoints.map((point, index) => (
                <li key={`${index}-${point}`}>{point}</li>
              ))}
            </ul>
          )}
          {answer.sources.length > 0 && (
            <div className="hermes-notice-sources">
              <strong>확인한 공식 공지</strong>
              {answer.sources.map(source => (
                <a
                  key={`${source.exchange}:${source.notice_id}`}
                  href={source.url}
                  target="_blank"
                  rel="noreferrer"
                >
                  {source.exchange.toUpperCase()} · {source.title}
                  <ExternalLink className="h-3 w-3" />
                </a>
              ))}
            </div>
          )}
          <p className="hermes-market-answer-caveat">{answer.caveat}</p>
        </div>
      )}
    </section>
  );
}
