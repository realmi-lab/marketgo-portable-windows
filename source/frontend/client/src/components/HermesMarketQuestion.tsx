import { useEffect, useReducer, useState, type FormEvent } from "react";
import {
  Bot,
  CheckCircle2,
  CircleAlert,
  Code2,
  Copy,
  CornerDownLeft,
  Database,
  FileText,
  History,
  LoaderCircle,
  MessageSquareWarning,
  ShieldCheck,
  Sparkles,
  Trash2,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { MobileHelp } from "@/components/common/MobileHelp";

import {
  MAX_MARKET_QUESTION_HISTORY_TURNS,
  submitMarketQuestionFeedback,
  type MarketQuestionFeedbackVerdict,
  type MarketQuestionConversationTurn,
} from "@/lib/api-client";
import { useMarketQuestionTask } from "@/contexts/MarketQuestionTaskContext";
import {
  formatKrwCompact,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import type {
  GroundedMarketQuestionConversationScope,
  GroundedMarketQuestionNoticeFact,
  GroundedMarketQuestionResponse,
} from "@/lib/market-share-types";
import { marketQuestionAnswerUsesModel } from "@/lib/market-share-labels";
import type { DomesticExchange } from "@/lib/market-share-config";
import {
  CURRENT_ASSET_TOTAL_SHARE_LABEL,
  DEFAULT_MARKET_QUESTION,
  exchangeMarketQuestionSuggestions,
  marketQuestionAbsoluteKRW,
  marketQuestionDraftReducer,
  marketQuestionEvidenceLabel,
  marketQuestionEvidenceTimestamp,
  showMarketQuestionBithumbDrivers,
  showMarketQuestionOfficialReference,
  type MarketQuestionDraft,
} from "@/lib/market-question-presentation";
import {
  buildMarketQuestionMarkdownDocument,
  humanizeMarketQuestionMarkdown,
  humanizeMarketQuestionPlainText,
} from "@/lib/market-question-markdown";
import {
  marketQuestionEvidenceBinding,
  marketQuestionExplanationPresentation,
  marketQuestionFulfillmentPresentation,
} from "@/lib/market-question-delivery";
import { isGroundedMarketQuestionConversationScope } from "@/lib/market-question-stream";

const DEFAULT_QUESTION = DEFAULT_MARKET_QUESTION;
const LLM_ENABLED = import.meta.env.VITE_LLM_ENABLED !== "false";
const MARKET_QUESTION_HISTORY_STORAGE_KEY = "market-share:llm-conversation:v1";
const MARKET_QUESTION_SCOPE_STORAGE_KEY =
  "market-share:llm-conversation-scope:v1";

const WRONG_FEEDBACK_OPTIONS: Array<{
  verdict: Exclude<MarketQuestionFeedbackVerdict, "CORRECT">;
  label: string;
}> = [
  { verdict: "WRONG_PERIOD", label: "기간이 다름" },
  { verdict: "WRONG_TARGET", label: "거래소·코인이 다름" },
  { verdict: "WRONG_METRIC", label: "지표가 다름" },
  { verdict: "WRONG_ANALYSIS", label: "분석 방향이 다름" },
];

export interface EmbeddedMarketQuestionAnswer {
  question: string;
  label: string;
  response: GroundedMarketQuestionResponse;
}

interface StoredMarketQuestionTurn {
  task_id?: string;
  question: string;
  response: GroundedMarketQuestionResponse;
  elapsed_ms?: number;
  completed_at_ms?: number;
  conversation_scope?: GroundedMarketQuestionConversationScope;
}

const QUICK_QUESTIONS = [
  {
    label: "빗썸 핵심 진단",
    question: DEFAULT_QUESTION,
  },
  {
    label: "빗썸 현재 비중",
    question: "현재 빗썸 점유율과 거래금액을 핵심만 분석해 주세요.",
  },
  {
    label: "빗썸 30분 변화",
    question: "최근 30분 빗썸 점유율과 거래금액을 직전 30분과 비교해 주세요.",
  },
  {
    label: "빗썸 1시간 변화",
    question: "빗썸의 최근 1시간 점유율과 거래금액 변화를 분석해 주세요.",
  },
  {
    label: "빗썸 기여 코인",
    question: "최근 1시간 빗썸 점유율 변화에 기여한 코인을 분석해 주세요.",
  },
] as const;

function answerTimestamp(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "현재 공식 분석 기준";
  return `${new Date(value).toLocaleString("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  })} 기준`;
}

function completedTimestamp(value: number | null): string {
  if (value == null || !Number.isFinite(value) || value <= 0)
    return "완료 시각 기록 없음";
  return `답변 완료 ${new Intl.DateTimeFormat("ko-KR", {
    timeZone: "Asia/Seoul",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(new Date(value))}`;
}

function explanationEndedTimestamp(value: number | null): string {
  if (value == null || !Number.isFinite(value) || value <= 0)
    return "설명 종료 시각 기록 없음";
  return `설명 종료 ${new Intl.DateTimeFormat("ko-KR", {
    timeZone: "Asia/Seoul",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).format(new Date(value))}`;
}

function elapsedLabel(value: number | null): string {
  if (value == null || !Number.isFinite(value) || value < 0)
    return "소요시간 기록 없음";
  if (value < 1_000) return `답변 소요 ${Math.max(1, Math.round(value))}ms`;
  return `답변 소요 ${(value / 1_000).toFixed(1)}초`;
}

function percentage(value: number | null | undefined): string {
  return typeof value === "number" && Number.isFinite(value)
    ? `${value.toFixed(2)}%`
    : "확정 전";
}

interface MarketQuestionFactCell {
  label: string;
  value: string;
  comparison?: boolean;
}

/** Answer fact cells; a current snapshot has no previous or change values. */
function marketQuestionFactCells(
  answer: GroundedMarketQuestionResponse,
  showOfficialReference: boolean,
  showBithumbFacts: boolean
): MarketQuestionFactCell[] {
  const { facts } = answer;
  const target = facts.target_exchange;
  const amount = (value: number | null | undefined, missing: string) =>
    value == null ? missing : marketQuestionAbsoluteKRW(value);
  const change = (value: number | null | undefined, missing: string) =>
    value == null ? missing : formatKrwCompact(value);
  const points = (value: number | null | undefined, missing: string) =>
    value == null ? missing : formatSignedPercentagePoints(value);
  const cells: MarketQuestionFactCell[] = [];
  if (target) {
    const name = exchangeLabel(target.exchange);
    cells.push(
      {
        label: `직전 ${name} 거래금액`,
        value: amount(target.previous_volume_krw, "해당 없음"),
        comparison: true,
      },
      {
        label: `현재 ${name} 거래금액`,
        value: amount(target.current_volume_krw, "확정 전"),
      },
      {
        label: `${name} 거래금액 변화`,
        value: change(target.volume_delta_krw, "해당 없음"),
        comparison: true,
      },
      {
        label: "직전 국내 비중",
        value: percentage(target.previous_share_pct),
        comparison: true,
      },
      { label: "현재 국내 비중", value: percentage(target.current_share_pct) },
      {
        label: "비중 변화폭",
        value: points(target.share_delta_pp, "해당 없음"),
        comparison: true,
      }
    );
    if (showOfficialReference) {
      cells.push(
        {
          label: "현재 공식 24시간 거래금액",
          value: amount(target.official_24h_volume_krw, "확정 전"),
        },
        {
          label: "공식 24시간 국내 비중",
          value: percentage(target.official_24h_share_pct),
        }
      );
    }
  } else {
    cells.push(
      {
        label: "직전 빗썸 점유율",
        value: percentage(facts.previous?.bithumb_share_pct),
        comparison: true,
      },
      {
        label: "현재 빗썸 점유율",
        value: percentage(facts.current?.bithumb_share_pct),
      },
      {
        label: "변화폭",
        value: points(facts.share_delta_pp, "확정 전"),
        comparison: true,
      },
      {
        label: "빗썸 거래금액 변화",
        value: change(facts.bithumb_volume_delta_krw, "확정 전"),
        comparison: true,
      }
    );
  }
  if (showBithumbFacts) {
    cells.push({
      label: "국내 전체 거래금액 변화",
      value: change(facts.domestic_volume_delta_krw, "확정 전"),
      comparison: true,
    });
  }
  return answer.query.comparison === "CURRENT_SNAPSHOT"
    ? cells.filter(cell => !cell.comparison)
    : cells;
}

function asOfKindLabel(value: string): string {
  const labels: Record<string, string> = {
    OFFICIAL_ALIGNED_KST_CALENDAR_DAY_CLOSE: "한국 날짜 기준 공식 마감",
    LAST_CLOSED_MINUTE_BOUNDARY: "마지막으로 끝난 1분",
    OFFICIAL_ALIGNED_HOUR_ENDPOINT: "같은 시각의 공식 1시간 자료",
    OFFICIAL_ALIGNED_SAME_KST_HOUR_ENDPOINT_SERIES:
      "같은 한국 시각의 공식 시간별 자료",
    EXACT_USER_KST_RANGE_END: "지정한 기간의 끝",
    WEBSOCKET_ROLLING_WINDOW_END: "실시간 관측 구간의 끝",
    WEBSOCKET_DIRECT_OBSERVATION: "실시간 체결 관측",
    OFFICIAL_DOMESTIC_5_EXCHANGE_ROLLING_24H_TICKER:
      "국내 5개 거래소 공식 최근 24시간 자료",
    WEBSOCKET_ROLLING_24H_ASSET_SNAPSHOT: "실시간 체결 기준 코인 자료",
    OFFICIAL_ALIGNED_24H_ENDPOINTS: "같은 종료 시각의 공식 24시간 자료",
  };
  return labels[value] ?? "답변에 사용한 자료의 끝 시각";
}

function displayTimezoneLabel(value: string): string {
  return value === "Asia/Seoul" ? "한국 시간" : "표시 시간대 확인 필요";
}

function providerLabel(
  answer: GroundedMarketQuestionResponse,
  mainDashboard = false
): string {
  if (answer.provider.source === "STANDALONE_SNAPSHOT")
    return "저장된 화면 자료로 분석";
  if (
    answer.provider.source === "HERMES" &&
    answer.provider.model === "gpt-6-astra"
  )
    return "AI · GPT-6 Astra 답변";
  if (
    answer.provider.source === "HERMES" &&
    answer.provider.model === "gpt-5.6-sol"
  )
    return "AI · GPT-5.6 Sol 답변";
  if (answer.provider.source === "HERMES") return "AI 답변";
  if (answer.provider.source === "HERMES_SOL_FALLBACK")
    return "AI · GPT-5.6 Sol 답변";
  return mainDashboard ? "검증된 수치 자동 요약" : "공식 수치 자동 요약";
}

function exchangeLabel(value: string): string {
  const labels: Record<string, string> = {
    BITHUMB: "빗썸",
    UPBIT: "업비트",
    COINONE: "코인원",
    KORBIT: "코빗",
    GOPAX: "고팍스",
  };
  const normalized = value.trim().toUpperCase();
  return labels[normalized] ?? normalized;
}

function isLiveAuxiliaryStatus(value: string): boolean {
  return value === "LIVE" || value === "COMPLETE";
}

function auxiliaryStatusLabel(value: string): string {
  if (value === "LIVE" || value === "COMPLETE") return "자료 확인";
  if (value === "PARTIAL" || value === "PROVISIONAL") return "일부 자료 확인";
  if (value === "STALE") return "자료 갱신 지연";
  return "자료 확인 필요";
}

function auxiliaryDirectionLabel(value: string): string {
  if (value === "RISING" || value === "UP") return "상승";
  if (value === "FALLING" || value === "DOWN") return "하락";
  if (value === "FLAT") return "변화 없음";
  if (value === "MIXED") return "상승·하락 혼재";
  return "방향 확인 필요";
}

/** The direction belongs to the signal's own window, not the 10-second key point. */
function realtimeSignalWindowLabel(seconds: number): string {
  return seconds > 0 ? `${seconds}초 신호 ` : "";
}

function noticePeriodTimestamp(value: number): string {
  return new Intl.DateTimeFormat("ko-KR", {
    timeZone: "Asia/Seoul",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).format(new Date(value));
}

function noticePeriodLabel(
  period: NonNullable<GroundedMarketQuestionNoticeFact["period"]>
): string {
  const range = `공지 기간 ${noticePeriodTimestamp(period.start_ms)}~${noticePeriodTimestamp(period.end_ms)} KST`;
  if (period.relation === "QUESTION_WINDOW_WITHIN_NOTICE_PERIOD")
    return `${range} · 질문 구간 전체 해당`;
  if (period.relation === "NOTICE_PERIOD_WITHIN_QUESTION_WINDOW")
    return `${range} · 공지 기간 전체가 질문 구간에 해당`;
  if (period.relation === "NOTICE_PERIOD_OVERLAPS_QUESTION_WINDOW")
    return `${range} · 질문 구간 일부 해당`;
  return `${range} · 질문 구간 미해당`;
}

function pathDirectionLabel(value: string): string {
  if (value === "RISING") return "상승만 관측";
  if (value === "FALLING") return "하락만 관측";
  if (value === "FLAT") return "보합";
  if (value === "MIXED") return "상승·하락 혼재";
  return value;
}

function reportTitle(answer: GroundedMarketQuestionResponse): string {
  if (answer.facts.evidence_layer === "DOMESTIC_OFFICIAL_NOTICE_7D_SNAPSHOT")
    return "최근 7일 국내 거래소 주요 공지 보고서";
  const exchange =
    answer.facts.target_exchange?.exchange ?? answer.query.exchange;
  const target = exchange ? exchangeLabel(exchange) : "빗썸";
  return `${target} 점유율 분석 보고서`;
}

function loadMarketQuestionConversation(): StoredMarketQuestionTurn[] {
  if (typeof window === "undefined") return [];
  try {
    const raw = window.sessionStorage.getItem(
      MARKET_QUESTION_HISTORY_STORAGE_KEY
    );
    if (!raw) return [];
    const parsed = JSON.parse(raw) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter(
        (turn): turn is StoredMarketQuestionTurn =>
          typeof turn === "object" &&
          turn !== null &&
          typeof (turn as StoredMarketQuestionTurn).question === "string" &&
          (turn as StoredMarketQuestionTurn).response?.contract ===
            "GROUNDED_MARKET_QUESTION_V2"
      )
      .slice(-MAX_MARKET_QUESTION_HISTORY_TURNS);
  } catch {
    return [];
  }
}

function loadMarketQuestionConversationScope(): GroundedMarketQuestionConversationScope | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(
      MARKET_QUESTION_SCOPE_STORAGE_KEY
    );
    if (!raw) return null;
    const parsed = JSON.parse(raw) as GroundedMarketQuestionConversationScope;
    return isGroundedMarketQuestionConversationScope(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function conversationAnswer(response: GroundedMarketQuestionResponse): string {
  return [
    response.prose.answer,
    ...response.prose.key_points.map(point => `핵심 근거: ${point}`),
    response.prose.caveat
      ? `사용 데이터: ${humanizeMarketQuestionPlainText(response.prose.caveat)}`
      : "",
  ]
    .filter(Boolean)
    .join("\n")
    .slice(0, 40_000);
}

export function HermesMarketQuestion({
  standalone = false,
  standaloneAnswer,
  standaloneAnswers = [],
  mainDashboard = false,
  selectedExchange = "bithumb",
}: {
  standalone?: boolean;
  standaloneAnswer?: GroundedMarketQuestionResponse;
  standaloneAnswers?: EmbeddedMarketQuestionAnswer[];
  mainDashboard?: boolean;
  selectedExchange?: DomesticExchange;
}) {
  const { task, startTask, cancelTask, clearTask } = useMarketQuestionTask();
  const embeddedAnswers =
    standaloneAnswers.length > 0
      ? standaloneAnswers
      : standaloneAnswer
        ? [
            {
              question: DEFAULT_QUESTION,
              label: "저장된 분석",
              response: standaloneAnswer,
            },
          ]
        : [];
  const initialEmbeddedAnswer = embeddedAnswers[0];
  const hasStandaloneAnswer = standalone && embeddedAnswers.length > 0;
  const [draft, dispatchDraft] = useReducer(
    marketQuestionDraftReducer,
    undefined,
    (): MarketQuestionDraft => {
      if (mainDashboard && !hasStandaloneAnswer) {
        const restoredQuestion =
          task.status !== "idle"
            ? task.question
            : loadMarketQuestionConversation().at(-1)?.question;
        return restoredQuestion != null
          ? { question: restoredQuestion, suggestionKey: null }
          : {
              question:
                exchangeMarketQuestionSuggestions(selectedExchange)[0].question,
              suggestionKey: "diagnosis",
            };
      }
      return {
        question: initialEmbeddedAnswer?.question ?? DEFAULT_QUESTION,
        suggestionKey: null,
      };
    }
  );
  const question = draft.question;
  const setQuestion = (value: string) =>
    dispatchDraft({ type: "edit", question: value });
  const quickQuestions = mainDashboard
    ? exchangeMarketQuestionSuggestions(selectedExchange)
    : QUICK_QUESTIONS;
  const [conversation, setConversation] = useState<StoredMarketQuestionTurn[]>(
    () => (hasStandaloneAnswer ? [] : loadMarketQuestionConversation())
  );
  const [conversationScope, setConversationScope] =
    useState<GroundedMarketQuestionConversationScope | null>(() =>
      hasStandaloneAnswer ? null : loadMarketQuestionConversationScope()
    );
  const [answer, setAnswer] = useState<GroundedMarketQuestionResponse | null>(
    () =>
      initialEmbeddedAnswer?.response ??
      loadMarketQuestionConversation().at(-1)?.response ??
      null
  );
  const [answerElapsedMS, setAnswerElapsedMS] = useState<number | null>(
    () => loadMarketQuestionConversation().at(-1)?.elapsed_ms ?? null
  );
  const [answerCompletedAtMS, setAnswerCompletedAtMS] = useState<number | null>(
    () =>
      initialEmbeddedAnswer?.response.provider.as_of_ms ??
      loadMarketQuestionConversation().at(-1)?.completed_at_ms ??
      null
  );
  const pending = !hasStandaloneAnswer && task.status === "pending";
  const [pendingElapsedSec, setPendingElapsedSec] = useState(0);
  const [error, setError] = useState("");
  const [feedbackVerdict, setFeedbackVerdict] =
    useState<MarketQuestionFeedbackVerdict | null>(null);
  const [feedbackCorrection, setFeedbackCorrection] = useState("");
  const [feedbackPending, setFeedbackPending] = useState(false);
  const [feedbackSaved, setFeedbackSaved] = useState(false);
  const [feedbackError, setFeedbackError] = useState("");
  const [reportMode, setReportMode] = useState<"preview" | "markdown">(
    "preview"
  );
  const [markdownCopied, setMarkdownCopied] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const targetExchange = answer?.facts.target_exchange;
  const showBithumbFacts = showMarketQuestionBithumbDrivers(
    targetExchange,
    answer?.query.exchange
  );
  const answerUsesModel = answer
    ? marketQuestionAnswerUsesModel(answer.provider.source)
    : false;
  const noticeReport =
    answer?.facts.evidence_layer === "DOMESTIC_OFFICIAL_NOTICE_7D_SNAPSHOT";
  const answerTitle = answer ? reportTitle(answer) : "점유율 분석 보고서";
  const answerMarkdown = answer
    ? buildMarketQuestionMarkdownDocument(answerTitle, answer)
    : "";
  const answerBodyMarkdown = answer
    ? humanizeMarketQuestionMarkdown(answer.prose.answer)
    : "";
  const evidenceBinding = answer ? marketQuestionEvidenceBinding(answer) : null;
  const fulfillment = answer
    ? marketQuestionFulfillmentPresentation(answer)
    : null;
  const explanation = answer
    ? marketQuestionExplanationPresentation(
        answer,
        task.status === "failed" && task.response === answer
          ? task.terminal_status === "TIMEOUT" ||
            task.terminal_status === "CANCELED"
            ? task.terminal_status
            : "FAILED"
          : undefined
      )
    : null;
  const activeAnswerPending =
    task.status === "pending" && task.response === answer;
  const activeAnswerFailed =
    task.status === "failed" && task.response === answer;

  useEffect(() => {
    if (mainDashboard && !hasStandaloneAnswer) {
      dispatchDraft({ type: "exchange", exchange: selectedExchange });
    }
  }, [mainDashboard, hasStandaloneAnswer, selectedExchange]);

  useEffect(() => {
    setFeedbackVerdict(null);
    setFeedbackCorrection("");
    setFeedbackPending(false);
    setFeedbackSaved(false);
    setFeedbackError("");
    setReportMode("preview");
    setMarkdownCopied(false);
  }, [answer?.request_id]);

  const copyMarkdown = async () => {
    if (!answerMarkdown) return;
    try {
      await navigator.clipboard.writeText(answerMarkdown);
      setMarkdownCopied(true);
      window.setTimeout(() => setMarkdownCopied(false), 1_500);
    } catch {
      setMarkdownCopied(false);
    }
  };

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      if (conversation.length === 0) {
        window.sessionStorage.removeItem(MARKET_QUESTION_HISTORY_STORAGE_KEY);
      } else {
        window.sessionStorage.setItem(
          MARKET_QUESTION_HISTORY_STORAGE_KEY,
          JSON.stringify(conversation.slice(-MAX_MARKET_QUESTION_HISTORY_TURNS))
        );
      }
    } catch {
      // Session storage is an enhancement; the in-memory 15-turn context
      // remains available when a browser blocks or exhausts storage.
    }
  }, [conversation]);

  useEffect(() => {
    if (typeof window === "undefined" || hasStandaloneAnswer) return;
    try {
      if (!conversationScope) {
        window.sessionStorage.removeItem(MARKET_QUESTION_SCOPE_STORAGE_KEY);
      } else {
        window.sessionStorage.setItem(
          MARKET_QUESTION_SCOPE_STORAGE_KEY,
          JSON.stringify(conversationScope)
        );
      }
    } catch {
      // The validated server token remains in component state when session
      // storage is blocked. Never synthesize a replacement client-side.
    }
  }, [conversationScope, hasStandaloneAnswer]);

  useEffect(() => {
    if (hasStandaloneAnswer || task.status === "idle") return;
    if (task.status === "pending") {
      if (!task.response) return;
      setAnswer(task.response);
      setAnswerElapsedMS(Date.now() - task.started_at_ms);
      setAnswerCompletedAtMS(null);
      setError("");
      if (task.response.conversation_scope)
        setConversationScope(task.response.conversation_scope);
      return;
    }
    if (task.status === "failed") {
      setAnswerElapsedMS(task.elapsed_ms);
      if (task.response) {
        setAnswer(task.response);
        setAnswerCompletedAtMS(task.completed_at_ms);
        if (task.response.conversation_scope)
          setConversationScope(task.response.conversation_scope);
      }
      setError(task.message);
      return;
    }
    if (mainDashboard) {
      dispatchDraft({ type: "completed" });
    } else {
      setQuestion(task.question);
    }
    setAnswer(task.response);
    setAnswerElapsedMS(task.elapsed_ms);
    setAnswerCompletedAtMS(task.completed_at_ms);
    if (task.response.conversation_scope)
      setConversationScope(task.response.conversation_scope);
    setError("");
    setConversation(previous => {
      if (previous.some(turn => turn.task_id === task.task_id)) return previous;
      return [
        ...previous,
        {
          task_id: task.task_id,
          question: task.question,
          response: task.response,
          elapsed_ms: task.elapsed_ms,
          completed_at_ms: task.completed_at_ms,
          conversation_scope: task.response.conversation_scope,
        },
      ].slice(-MAX_MARKET_QUESTION_HISTORY_TURNS);
    });
  }, [mainDashboard, hasStandaloneAnswer, task]);

  useEffect(() => {
    if (!pending || task.status !== "pending") {
      setPendingElapsedSec(0);
      return;
    }
    const update = () =>
      setPendingElapsedSec(
        Math.max(0, Math.floor((Date.now() - task.started_at_ms) / 1_000))
      );
    update();
    const timer = window.setInterval(update, 1_000);
    return () => window.clearInterval(timer);
  }, [pending, task]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const normalized = question.trim();
    if (normalized.length < 2 || pending) return;
    if (hasStandaloneAnswer) {
      const normalizeEmbeddedQuestion = (value: string) =>
        value
          .trim()
          .replace(/\s+/g, " ")
          .replace(/[.!?。！？]+$/g, "");
      const matched = embeddedAnswers.find(
        item =>
          normalizeEmbeddedQuestion(item.question) ===
          normalizeEmbeddedQuestion(normalized)
      );
      if (!matched) {
        setError(
          `이 단독 HTML에는 저장된 질문 ${embeddedAnswers.length}개만 답할 수 있습니다. 위 저장 질문을 선택해 주세요.`
        );
        return;
      }
      setAnswer(matched.response);
      setAnswerElapsedMS(0);
      setAnswerCompletedAtMS(matched.response.provider.as_of_ms);
      setError("");
      return;
    }
    if (!LLM_ENABLED) return;
    setError("");
    const history: MarketQuestionConversationTurn[] = conversation.map(
      turn => ({
        question: turn.question,
        answer: conversationAnswer(turn.response),
      })
    );
    startTask(normalized, history, conversationScope);
  };

  const saveFeedback = async (
    verdict: MarketQuestionFeedbackVerdict,
    correctedQuestion = ""
  ) => {
    if (!answer?.request_id || feedbackPending) return;
    setFeedbackPending(true);
    setFeedbackError("");
    try {
      await submitMarketQuestionFeedback(
        answer.request_id,
        verdict,
        correctedQuestion
      );
      setFeedbackVerdict(verdict);
      setFeedbackSaved(true);
    } catch {
      setFeedbackError(
        "피드백을 저장하지 못했습니다. 잠시 뒤 다시 시도해 주세요."
      );
    } finally {
      setFeedbackPending(false);
    }
  };

  const helpText = hasStandaloneAnswer
    ? `이 HTML을 만들 때 저장한 실제 자료 기반 답변 ${embeddedAnswers.length}건입니다. 저장 질문을 선택하면 서버 연결 없이 같은 결과를 표시합니다.`
    : LLM_ENABLED
      ? `${mainDashboard ? "추천 질문 외에도 지원 기간의 거래소 점유율·거래금액이나 빗썸 기준 코인 비중·기여를 직접 질문할 수 있습니다. 거래소 선택은 추천 문장에만 반영하며 직접 작성한 질문은 유지합니다. " : ""}서버가 확정한 정량 결과를 먼저 표시하고, 같은 근거로 검증한 설명만 이어서 표시합니다. 최근 15개 대화의 확정 조건을 후속 질문에 이어 쓰며, AI 설명이 실패해도 정량 결과는 유지합니다.`
      : "이 화면에서는 AI 질문을 사용할 수 없습니다.";

  return (
    <section
      className={`hermes-market-question ${standalone ? "hermes-market-question-standalone" : ""}`}
      aria-labelledby="hermes-question-title"
    >
      <div className="hermes-market-question-heading">
        <span className="hermes-market-question-icon" aria-hidden="true">
          <Bot className="h-4 w-4" />
        </span>
        <div>
          <div className="flex items-center gap-2">
            <h3 id="hermes-question-title">
              {hasStandaloneAnswer
                ? "저장된 점유율 분석 보고서"
                : LLM_ENABLED
                  ? "점유율 분석 보고서 만들기"
                  : "LLM 질문 비활성화"}
            </h3>
            <MobileHelp label="점유율 질문 안내">{helpText}</MobileHelp>
            <span className="hermes-market-question-badge">
              <Sparkles className="h-3 w-3" />{" "}
              {mainDashboard ? "확인된 자료 기반" : "공식 수치 기반"}
            </span>
            <button
              type="button"
              className="hermes-market-question-help-button mobile-description-copy"
              aria-label={helpOpen ? "분석 안내 닫기" : "분석 안내 보기"}
              aria-expanded={helpOpen}
              aria-controls="hermes-market-question-help"
              onClick={() => setHelpOpen(open => !open)}
            >
              ?
            </button>
          </div>
          {helpOpen && (
            <p
              id="hermes-market-question-help"
              className="mobile-description-copy"
            >
              {helpText}
            </p>
          )}
        </div>
      </div>

      {hasStandaloneAnswer ? (
        <div
          className="hermes-market-question-suggestions"
          aria-label="저장 질문"
        >
          {embeddedAnswers.map(item => (
            <button
              key={item.question}
              type="button"
              onClick={() => {
                setQuestion(item.question);
                setAnswer(item.response);
                setAnswerElapsedMS(0);
                setAnswerCompletedAtMS(item.response.provider.as_of_ms);
                setError("");
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      ) : (
        <div
          className="hermes-market-question-suggestions"
          aria-label="추천 질문"
        >
          {quickQuestions.map(item => (
            <button
              key={item.label}
              type="button"
              disabled={!LLM_ENABLED}
              onClick={() => {
                if (mainDashboard && "key" in item) {
                  dispatchDraft({ type: "suggestion", suggestion: item });
                } else {
                  setQuestion(item.question);
                }
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}

      {conversation.length > 0 && (
        <div className="hermes-market-question-history" aria-label="최근 대화">
          <div className="hermes-market-question-history-heading">
            <span>
              <History className="h-3.5 w-3.5" /> 최근 대화{" "}
              {conversation.length}/{MAX_MARKET_QUESTION_HISTORY_TURNS}
            </span>
            <button
              type="button"
              onClick={() => {
                clearTask();
                setConversation([]);
                setConversationScope(null);
                setAnswer(null);
                setAnswerElapsedMS(null);
                setAnswerCompletedAtMS(null);
                setError("");
              }}
            >
              <Trash2 className="h-3.5 w-3.5" /> 기억 지우기
            </button>
          </div>
          <div className="hermes-market-question-history-list">
            {conversation.map((turn, index) => (
              <button
                key={`${index}-${turn.question}`}
                type="button"
                title={turn.question}
                onClick={() => {
                  setQuestion(turn.question);
                  setAnswer(turn.response);
                  setAnswerElapsedMS(turn.elapsed_ms ?? null);
                  setAnswerCompletedAtMS(turn.completed_at_ms ?? null);
                  if (turn.conversation_scope)
                    setConversationScope(turn.conversation_scope);
                }}
              >
                {index + 1}. {turn.question}
              </button>
            ))}
          </div>
        </div>
      )}

      <form className="hermes-market-question-form" onSubmit={submit}>
        <label className="sr-only" htmlFor="hermes-market-question-input">
          거래소 점유율 질문 입력
        </label>
        <textarea
          id="hermes-market-question-input"
          value={question}
          disabled={!LLM_ENABLED && !hasStandaloneAnswer}
          onChange={event => setQuestion(event.target.value)}
          maxLength={500}
          rows={hasStandaloneAnswer ? 10 : 2}
          placeholder="예: 최근 1시간 빗썸 점유율과 거래금액은 왜 변했나요?"
        />
        <button
          type="submit"
          disabled={
            (!LLM_ENABLED && !hasStandaloneAnswer) ||
            pending ||
            question.trim().length < 2
          }
        >
          {pending ? (
            <LoaderCircle className="h-4 w-4 animate-spin" />
          ) : (
            <CornerDownLeft className="h-4 w-4" />
          )}
          {hasStandaloneAnswer
            ? "내장 답변 보기"
            : !LLM_ENABLED
              ? "사용 안 함"
              : pending
                ? "분석 중"
                : "질문하기"}
        </button>
      </form>

      {pending && task.status === "pending" && (
        <div
          className="hermes-market-question-progress"
          role="status"
          aria-live="polite"
        >
          <span
            className="hermes-market-question-progress-icon"
            aria-hidden="true"
          >
            <LoaderCircle className="h-4 w-4 animate-spin" />
          </span>
          <div>
            <strong>
              {task.response
                ? `정량 결과 확인 · 설명 검증 중 · ${pendingElapsedSec}초 경과`
                : `질문을 해석하고 계산 중 · ${pendingElapsedSec}초 경과`}
            </strong>
            <p>“{task.question}”</p>
            <small>
              {task.response
                ? "서버가 확정한 수치는 먼저 표시했습니다. 설명은 같은 근거로 검증을 마친 뒤 표시합니다."
                : "질문 조건을 확정하고 필요한 근거를 조회·계산합니다."}{" "}
              다른 화면에 다녀와도 이 요청은 계속됩니다.
            </small>
            <button
              type="button"
              onClick={cancelTask}
              className="ui-body mt-2 underline underline-offset-4"
            >
              요청 취소
            </button>
          </div>
        </div>
      )}

      {error && <p className="hermes-market-question-error">{error}</p>}

      {/* Before the first question the screen was an empty field below the
          form. Say what can be asked and how answers are built. */}
      {!answer && !pending && !error && !hasStandaloneAnswer && LLM_ENABLED && (
        <div className="hermes-market-question-empty">
          <strong>질문하면 확인된 자료로 답합니다</strong>
          <p>
            거래소 점유율·거래금액이나 빗썸의 코인별 비중·기여를 물어보세요. 위
            추천 질문으로 시작해도 됩니다. 확정 수치를 먼저 보여 주고, 설명이
            실패해도 수치는 남습니다.
          </p>
        </div>
      )}

      {answer && (
        <div className="hermes-market-answer" aria-live="polite">
          <div className="hermes-market-answer-meta">
            <strong>
              {activeAnswerPending
                ? "서버 계산 결과"
                : activeAnswerFailed
                  ? "서버 계산 결과 · 설명 미완료"
                  : providerLabel(answer, mainDashboard)}
            </strong>
            <div className="hermes-market-answer-meta-detail">
              <span>
                자료{" "}
                {mainDashboard
                  ? marketQuestionEvidenceTimestamp(answer.provider.as_of_ms)
                  : answerTimestamp(answer.provider.as_of_ms)}
              </span>
              <span>
                {activeAnswerPending
                  ? "설명 처리 중"
                  : activeAnswerFailed
                    ? explanationEndedTimestamp(answerCompletedAtMS)
                    : completedTimestamp(answerCompletedAtMS)}
              </span>
              <span>{elapsedLabel(answerElapsedMS)}</span>
            </div>
          </div>

          {explanation?.showProse &&
            !answerUsesModel &&
            answer.provider.source !== "STANDALONE_SNAPSHOT" && (
              <p
                className="hermes-market-question-error hermes-market-explanation-fallback"
                role="status"
              >
                {mainDashboard
                  ? "모델 응답을 사용할 수 없어 서버가 검증한 자료로 만든 자동 요약을 표시합니다."
                  : "모델 응답을 사용할 수 없어 서버가 검증한 공식 수치 자동 요약을 표시합니다."}
              </p>
            )}

          {answer.request_id && !hasStandaloneAnswer && (
            <section
              className="hermes-market-question-feedback"
              aria-label="질문 이해 정확성 피드백"
            >
              {feedbackSaved ? (
                <p className="hermes-market-question-feedback-saved">
                  <CheckCircle2 className="h-3.5 w-3.5" /> 피드백을
                  저장했습니다. 검토를 통과한 사례만 이후 개선에 사용합니다.
                </p>
              ) : (
                <>
                  <div className="hermes-market-question-feedback-heading">
                    <span>질문의 기간·대상·지표를 정확히 이해했나요?</span>
                    <button
                      type="button"
                      disabled={feedbackPending}
                      onClick={() => void saveFeedback("CORRECT")}
                    >
                      <CheckCircle2 className="h-3.5 w-3.5" /> 정확함
                    </button>
                  </div>
                  <div className="hermes-market-question-feedback-options">
                    {WRONG_FEEDBACK_OPTIONS.map(option => (
                      <button
                        key={option.verdict}
                        type="button"
                        aria-pressed={feedbackVerdict === option.verdict}
                        disabled={feedbackPending}
                        onClick={() => {
                          setFeedbackVerdict(option.verdict);
                          setFeedbackError("");
                        }}
                      >
                        <MessageSquareWarning className="h-3.5 w-3.5" />
                        {option.label}
                      </button>
                    ))}
                  </div>
                  {feedbackVerdict && feedbackVerdict !== "CORRECT" && (
                    <form
                      className="hermes-market-question-feedback-correction"
                      onSubmit={event => {
                        event.preventDefault();
                        void saveFeedback(
                          feedbackVerdict,
                          feedbackCorrection.trim()
                        );
                      }}
                    >
                      <label htmlFor="market-question-feedback-correction">
                        원래 원한 질문을 한 문장으로 다시 적어 주세요.
                      </label>
                      <div>
                        <input
                          id="market-question-feedback-correction"
                          value={feedbackCorrection}
                          onChange={event =>
                            setFeedbackCorrection(event.target.value)
                          }
                          minLength={2}
                          maxLength={500}
                          required
                        />
                        <button
                          type="submit"
                          disabled={
                            feedbackPending ||
                            feedbackCorrection.trim().length < 2
                          }
                        >
                          {feedbackPending ? "저장 중" : "수정 저장"}
                        </button>
                      </div>
                    </form>
                  )}
                  <small>
                    질문 내용과 질문을 해석한 기록은 90일 뒤 삭제되며, 답변
                    문장과 시장 원자료는 저장하지 않습니다.
                  </small>
                  {feedbackError && (
                    <p className="hermes-market-question-error" role="status">
                      {feedbackError}
                    </p>
                  )}
                </>
              )}
            </section>
          )}

          <section
            className="hermes-market-quantitative-results"
            aria-label="서버 확정 정량 결과"
          >
            {!evidenceBinding?.legacy && (
              <header className="hermes-market-result-heading">
                <div>
                  <span>
                    <Database className="h-3.5 w-3.5" /> 서버 확정 정량 결과
                  </span>
                  <strong>{fulfillment?.label}</strong>
                  <small>{fulfillment?.detail}</small>
                </div>
                <span
                  className={`hermes-market-result-status hermes-market-result-status-${fulfillment?.state ?? "legacy"}`}
                >
                  {fulfillment?.state === "fulfilled" ? (
                    <CheckCircle2 className="h-3.5 w-3.5" />
                  ) : (
                    <CircleAlert className="h-3.5 w-3.5" />
                  )}
                  {evidenceBinding?.scopeLabel}
                </span>
              </header>
            )}
            {evidenceBinding && !evidenceBinding.legacy && (
              <p className="hermes-market-evidence-binding">
                <span title={evidenceBinding.id || undefined}>
                  근거 {evidenceBinding.shortID}
                </span>
                <span>
                  {evidenceBinding.asOfMS == null
                    ? "분석 기준 시각 미확정"
                    : answerTimestamp(evidenceBinding.asOfMS)}
                </span>
                {answer.evidence_bundle?.time.actual.start_ms != null &&
                  answer.evidence_bundle.time.actual.end_ms != null && (
                    <span>
                      실제 구간{" "}
                      {noticePeriodTimestamp(
                        answer.evidence_bundle.time.actual.start_ms
                      )}
                      ~
                      {noticePeriodTimestamp(
                        answer.evidence_bundle.time.actual.end_ms
                      )}{" "}
                      KST
                    </span>
                  )}
              </p>
            )}
            {answer.evidence_bundle?.scope.status === "SUBSTITUTED_PARTIAL" && (
              <p className="hermes-market-scope-warning" role="status">
                <strong>분석 범위 변경</strong>
                {" · 포함 "}
                {(answer.evidence_bundle.scope.included_exchanges ?? [])
                  .map(exchangeLabel)
                  .join(", ") || "확인 필요"}
                {(answer.evidence_bundle.scope.excluded_exchanges ?? [])
                  .length > 0
                  ? ` · 제외 ${(
                      answer.evidence_bundle.scope.excluded_exchanges ?? []
                    )
                      .map(
                        item =>
                          `${exchangeLabel(item.exchange)}: ${item.reason}`
                      )
                      .join(", ")}`
                  : ""}
              </p>
            )}

            <div className="hermes-market-facts" aria-label="확인된 계산 근거">
              <div className="hermes-market-facts-heading">
                <span>
                  <Database className="h-3.5 w-3.5" /> 확인된 계산 근거
                </span>
                <span
                  className={
                    answer.quality.status === "LIVE"
                      ? "hermes-quality-live"
                      : "hermes-quality-unavailable"
                  }
                >
                  <ShieldCheck className="h-3 w-3" />{" "}
                  {answer.quality.status === "LIVE"
                    ? "자료 확인"
                    : "자료 일부 부족"}
                </span>
              </div>
              <p className="hermes-market-evidence-label">
                {marketQuestionEvidenceLabel(answer.facts.evidence_layer)}
              </p>
              {!noticeReport && (
                <div className="hermes-market-fact-grid">
                  {marketQuestionFactCells(
                    answer,
                    !mainDashboard ||
                      (targetExchange != null &&
                        showMarketQuestionOfficialReference(
                          answer.facts.evidence_layer,
                          targetExchange
                        )),
                    showBithumbFacts
                  ).map(cell => (
                    <div key={cell.label}>
                      <span>{cell.label}</span>
                      <strong>{cell.value}</strong>
                    </div>
                  ))}
                </div>
              )}
              {answer.facts.path_summary?.status === "COMPLETE" && (
                <div
                  className="hermes-market-path-facts"
                  aria-label="공식 시간별 경로 요약"
                >
                  <strong>공식 시간별 경로</strong>
                  <span>
                    {pathDirectionLabel(answer.facts.path_summary.direction)} ·
                    상승 {answer.facts.path_summary.rising_step_count}/하락{" "}
                    {answer.facts.path_summary.falling_step_count}/보합{" "}
                    {answer.facts.path_summary.flat_step_count}
                  </span>
                  <span>
                    최저 {percentage(answer.facts.path_summary.min_share_pct)} ·
                    최고 {percentage(answer.facts.path_summary.max_share_pct)}
                  </span>
                  <span>
                    최대 1시간{" "}
                    {answer.facts.path_summary.largest_step_delta_pp == null
                      ? "확정 전"
                      : formatSignedPercentagePoints(
                          answer.facts.path_summary.largest_step_delta_pp
                        )}
                    {answer.facts.path_summary.largest_step_abs_share_pct ==
                    null
                      ? ""
                      : ` · 절대 변화의 ${answer.facts.path_summary.largest_step_abs_share_pct.toFixed(1)}% 집중`}
                  </span>
                </div>
              )}
              {answer.facts.asset_metrics && (
                <div className="hermes-market-asset-facts">
                  <strong>{answer.facts.asset_metrics.asset}</strong>
                  <span>
                    {answer.query.comparison === "CURRENT_SNAPSHOT" ? (
                      <>
                        현재 코인 거래금액 중 빗썸 비중{" "}
                        {percentage(
                          answer.facts.asset_metrics.current_bithumb_capture_pct
                        )}
                      </>
                    ) : (
                      <>
                        코인 거래금액 중 빗썸 비중{" "}
                        {percentage(
                          answer.facts.asset_metrics
                            .previous_bithumb_capture_pct
                        )}{" "}
                        →{" "}
                        {percentage(
                          answer.facts.asset_metrics.current_bithumb_capture_pct
                        )}
                      </>
                    )}
                  </span>
                  <span>
                    {answer.query.comparison === "CURRENT_SNAPSHOT" ? (
                      <>
                        {CURRENT_ASSET_TOTAL_SHARE_LABEL}{" "}
                        {percentage(
                          answer.facts.asset_metrics
                            .current_total_share_contribution_pct
                        )}
                      </>
                    ) : (
                      <>
                        전체 점유율 변화 기여{" "}
                        {answer.facts.asset_metrics.change_attribution_pp ==
                        null
                          ? "확정 전"
                          : formatSignedPercentagePoints(
                              answer.facts.asset_metrics.change_attribution_pp
                            )}
                      </>
                    )}
                  </span>
                  {(answer.facts.asset_metrics.previous_aggressor_buy_pct !=
                    null ||
                    answer.facts.asset_metrics.current_aggressor_buy_pct !=
                      null) && (
                    <span>
                      국내 매수 체결 비중{" "}
                      {percentage(
                        answer.facts.asset_metrics.previous_aggressor_buy_pct
                      )}{" "}
                      →{" "}
                      {percentage(
                        answer.facts.asset_metrics.current_aggressor_buy_pct
                      )}
                    </span>
                  )}
                  {(answer.facts.asset_metrics
                    .previous_bithumb_aggressor_buy_pct != null ||
                    answer.facts.asset_metrics
                      .current_bithumb_aggressor_buy_pct != null) && (
                    <span>
                      빗썸 매수 체결 비중{" "}
                      {percentage(
                        answer.facts.asset_metrics
                          .previous_bithumb_aggressor_buy_pct
                      )}{" "}
                      →{" "}
                      {percentage(
                        answer.facts.asset_metrics
                          .current_bithumb_aggressor_buy_pct
                      )}
                    </span>
                  )}
                  {answer.facts.asset_metrics
                    .current_directional_coverage_pct != null && (
                    <span>
                      매수·매도 방향을 분류할 수 있는 거래금액 비중{" "}
                      {percentage(
                        answer.facts.asset_metrics
                          .current_directional_coverage_pct
                      )}
                    </span>
                  )}
                  <small>
                    {answer.facts.asset_metrics.contract_identity_verified
                      ? "코인 식별 확인"
                      : "심볼 기준 연결 · 계약 주소 미확인"}
                  </small>
                </div>
              )}
              {showBithumbFacts &&
                ((answer.facts.drivers?.length ?? 0) > 0 ||
                  (answer.facts.exchange_drivers?.length ?? 0) > 0) && (
                  <div
                    className="hermes-market-driver-facts"
                    aria-label="계산된 점유율 변화 기여"
                  >
                    {(answer.facts.drivers?.length ?? 0) > 0 && (
                      <div>
                        <strong>코인별 점유율 변화 기여</strong>
                        {answer.facts.drivers.slice(0, 3).map(driver => (
                          <span key={driver.asset}>
                            {driver.asset}{" "}
                            {formatSignedPercentagePoints(
                              driver.contribution_pp
                            )}
                          </span>
                        ))}
                      </div>
                    )}
                    {(answer.facts.exchange_drivers?.length ?? 0) > 0 && (
                      <div>
                        <strong>거래소별 점유율 변화 기여</strong>
                        {answer.facts.exchange_drivers
                          .slice(0, 3)
                          .map(driver => (
                            <span key={driver.exchange}>
                              {exchangeLabel(driver.exchange)}{" "}
                              {formatSignedPercentagePoints(
                                driver.contribution_pp
                              )}
                            </span>
                          ))}
                      </div>
                    )}
                  </div>
                )}
              {answer.facts.auxiliary && (
                <div
                  className="hermes-market-auxiliary-facts"
                  aria-label="요청한 보조 근거"
                >
                  <strong>보조 근거</strong>
                  {answer.facts.auxiliary.realtime && (
                    <span>
                      실시간 흐름{" "}
                      {auxiliaryStatusLabel(
                        answer.facts.auxiliary.realtime.status
                      )}
                      {answer.facts.auxiliary.realtime.status === "LIVE" &&
                      answer.facts.auxiliary.realtime.direction
                        ? ` · ${realtimeSignalWindowLabel(answer.facts.auxiliary.realtime.primary_window_sec)}${auxiliaryDirectionLabel(answer.facts.auxiliary.realtime.direction)}`
                        : ""}
                    </span>
                  )}
                  {answer.facts.auxiliary.orderbook && (
                    <span>
                      {answer.facts.auxiliary.orderbook.asset || "선택 자산"}{" "}
                      호가{" "}
                      {auxiliaryStatusLabel(
                        answer.facts.auxiliary.orderbook.status
                      )}
                      {!isLiveAuxiliaryStatus(
                        answer.facts.auxiliary.orderbook.status
                      ) || answer.facts.auxiliary.orderbook.spread_bps == null
                        ? ""
                        : ` · 매수·매도 가격 차이 ${(answer.facts.auxiliary.orderbook.spread_bps / 100).toFixed(3)}%`}
                      {!isLiveAuxiliaryStatus(
                        answer.facts.auxiliary.orderbook.status
                      ) || answer.facts.auxiliary.orderbook.imbalance == null
                        ? ""
                        : ` · 호가 잔량 차이 지수 ${answer.facts.auxiliary.orderbook.imbalance.toFixed(2)}`}
                    </span>
                  )}
                  {answer.facts.auxiliary.notices && (
                    <>
                      <span>
                        구간·자산 일치 공지{" "}
                        {answer.facts.auxiliary.notices.matches.length}건 · 인과
                        단정 안 함
                      </span>
                      {answer.facts.auxiliary.notices.matches.map(match => (
                        <span key={`${match.exchange}:${match.notice_id}`}>
                          {exchangeLabel(match.exchange)} {match.title}
                          {match.period
                            ? ` · ${noticePeriodLabel(match.period)}`
                            : ""}
                        </span>
                      ))}
                    </>
                  )}
                </div>
              )}
              {answer.quality.warnings.length > 0 && (
                <ul className="hermes-market-quality-warnings">
                  {answer.quality.warnings.map(warning => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              )}
            </div>
          </section>

          <section
            className={`hermes-market-explanation hermes-market-explanation-${explanation?.state ?? "legacy"}`}
            aria-label="검증된 설명"
          >
            {explanation?.state !== "legacy" && (
              <header className="hermes-market-result-heading">
                <div>
                  <span>
                    {explanation?.state === "pending" ? (
                      <LoaderCircle className="h-3.5 w-3.5 animate-spin" />
                    ) : explanation?.showProse ? (
                      <ShieldCheck className="h-3.5 w-3.5" />
                    ) : (
                      <CircleAlert className="h-3.5 w-3.5" />
                    )}
                    설명
                  </span>
                  <strong>{explanation?.label}</strong>
                  <small>{explanation?.detail}</small>
                </div>
                {evidenceBinding && (
                  <span
                    className="hermes-market-result-status"
                    title={evidenceBinding.id || undefined}
                  >
                    같은 근거 {evidenceBinding.shortID}
                    {evidenceBinding.asOfMS == null
                      ? " · 기준 시각 미확정"
                      : ` · ${answerTimestamp(evidenceBinding.asOfMS)}`}
                  </span>
                )}
              </header>
            )}

            {explanation?.showProse && (
              <article className="hermes-market-markdown-document">
                <header className="hermes-market-markdown-header">
                  <div>
                    <span>
                      {answer.provider.source === "STANDALONE_SNAPSHOT"
                        ? "저장된 분석 보고서"
                        : answerUsesModel
                          ? "검증된 분석 설명"
                          : "검증된 자동 요약"}
                    </span>
                    <h4>{answerTitle}</h4>
                  </div>
                  <div className="hermes-market-markdown-actions">
                    <div
                      className="hermes-market-markdown-modes"
                      aria-label="보고서 표시 방식"
                    >
                      <button
                        type="button"
                        aria-pressed={reportMode === "preview"}
                        onClick={() => setReportMode("preview")}
                      >
                        <FileText className="h-3.5 w-3.5" /> 보고서 보기
                      </button>
                      <button
                        type="button"
                        aria-pressed={reportMode === "markdown"}
                        onClick={() => setReportMode("markdown")}
                      >
                        <Code2 className="h-3.5 w-3.5" /> 보고서 원문
                      </button>
                    </div>
                    <button type="button" onClick={() => void copyMarkdown()}>
                      {markdownCopied ? (
                        <CheckCircle2 className="h-3.5 w-3.5" />
                      ) : (
                        <Copy className="h-3.5 w-3.5" />
                      )}
                      {markdownCopied ? "복사됨" : "복사"}
                    </button>
                  </div>
                </header>
                {reportMode === "preview" ? (
                  <div
                    className="hermes-market-markdown"
                    aria-label={
                      answerUsesModel
                        ? "검증된 생성형 점유율 분석"
                        : "검증된 서버 점유율 자동 요약"
                    }
                  >
                    <ReactMarkdown
                      remarkPlugins={[remarkGfm]}
                      components={
                        mainDashboard
                          ? {
                              th: ({ node: _node, ...props }) => (
                                <th {...props} className="whitespace-nowrap" />
                              ),
                              td: ({ node: _node, ...props }) => (
                                <td {...props} className="whitespace-nowrap" />
                              ),
                            }
                          : undefined
                      }
                    >
                      {answerBodyMarkdown}
                    </ReactMarkdown>
                  </div>
                ) : (
                  <textarea
                    className="hermes-market-markdown-source"
                    aria-label="보고서 원문"
                    readOnly
                    spellCheck={false}
                    value={answerMarkdown}
                  />
                )}
              </article>
            )}
          </section>

          <details className="hermes-market-evidence-details">
            <summary>
              <Database className="h-3.5 w-3.5" /> 계산 방법과 답변 근거 보기
            </summary>

            {answer.prose.key_points.length > 0 && (
              <div className="hermes-market-key-points">
                <strong>핵심 판단 근거</strong>
                <ul>
                  {answer.prose.key_points.map((point, index) => (
                    <li key={`${index}-${point}`}>
                      {humanizeMarketQuestionPlainText(point)}
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className="hermes-market-answer-caveat">
              <strong>사용 데이터</strong>
              <p>{humanizeMarketQuestionPlainText(answer.prose.caveat)}</p>
            </div>
            <p className="hermes-market-provenance">
              {asOfKindLabel(answer.provenance.as_of_kind)} ·{" "}
              {displayTimezoneLabel(answer.provenance.display_timezone)}
              {answer.provenance.retrieval_strategy
                ? " · 필요한 자료만 안전하게 조회"
                : ""}
              {answer.provenance.answer_policy_version
                ? ` · 답변 규칙 ${answer.provenance.answer_policy_version}`
                : ""}
              {answer.provider.model ? ` · ${answer.provider.model}` : ""}
            </p>
          </details>
        </div>
      )}
    </section>
  );
}
