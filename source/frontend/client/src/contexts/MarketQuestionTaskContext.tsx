import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import {
  askGroundedMarketQuestionStream,
  type MarketQuestionConversationTurn,
} from "@/lib/api-client";
import type {
  GroundedMarketQuestionConversationScope,
  GroundedMarketQuestionResponse,
  GroundedMarketQuestionStageEvent,
} from "@/lib/market-share-types";
import {
  MARKET_QUESTION_REQUEST_TIMEOUT_MS,
  QUESTION_TIMEOUT_MESSAGE,
  QuestionRequestGate,
  questionRequestErrorMessage,
} from "@/lib/question-request";

export type MarketQuestionTaskState =
  | { status: "idle" }
  | {
      status: "pending";
      task_id: string;
      question: string;
      started_at_ms: number;
      stage?: GroundedMarketQuestionStageEvent["stage"];
      stage_status?: GroundedMarketQuestionStageEvent["status"];
      response?: GroundedMarketQuestionResponse;
    }
  | {
      status: "completed";
      task_id: string;
      question: string;
      response: GroundedMarketQuestionResponse;
      elapsed_ms: number;
      completed_at_ms: number;
      terminal_status: GroundedMarketQuestionStageEvent["status"];
      legacy: boolean;
    }
  | {
      status: "failed";
      elapsed_ms: number;
      task_id: string;
      question: string;
      completed_at_ms: number;
      message: string;
      response?: GroundedMarketQuestionResponse;
      terminal_status?: GroundedMarketQuestionStageEvent["status"];
    };

interface MarketQuestionTaskContextValue {
  task: MarketQuestionTaskState;
  startTask: (
    question: string,
    history: MarketQuestionConversationTurn[],
    conversationScope?: GroundedMarketQuestionConversationScope | null
  ) => void;
  cancelTask: () => void;
  clearTask: () => void;
}

const MarketQuestionTaskContext =
  createContext<MarketQuestionTaskContextValue | null>(null);

function newTaskID(): string {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function MarketQuestionTaskProvider({
  children,
}: {
  children: ReactNode;
}) {
  const [task, setTask] = useState<MarketQuestionTaskState>({ status: "idle" });
  const requests = useRef(new QuestionRequestGate());

  useEffect(
    () => () => {
      requests.current.cancel();
    },
    []
  );

  const clearTask = useCallback(() => {
    requests.current.cancel();
    setTask({ status: "idle" });
  }, []);

  const cancelTask = useCallback(() => {
    if (!requests.current.cancel()) return;
    setTask(previous =>
      previous.status === "pending"
        ? {
            ...previous,
            status: "failed",
            completed_at_ms: Date.now(),
            elapsed_ms: Date.now() - previous.started_at_ms,
            message:
              "요청을 취소했습니다. 질문을 바꾸거나 다시 보낼 수 있습니다.",
            terminal_status: "CANCELED",
          }
        : previous
    );
  }, []);

  const startTask = useCallback(
    (
      question: string,
      history: MarketQuestionConversationTurn[],
      conversationScope: GroundedMarketQuestionConversationScope | null = null
    ) => {
      const taskID = newTaskID();
      const startedAtMS = Date.now();
      const request = requests.current.start(
        MARKET_QUESTION_REQUEST_TIMEOUT_MS,
        () => {
          setTask(previous => ({
            status: "failed",
            task_id: taskID,
            question,
            completed_at_ms: Date.now(),
            elapsed_ms: Date.now() - startedAtMS,
            message: QUESTION_TIMEOUT_MESSAGE,
            response:
              previous.status === "pending" && previous.task_id === taskID
                ? previous.response
                : undefined,
            terminal_status: "TIMEOUT",
          }));
        }
      );
      if (!request) return;
      setTask({
        status: "pending",
        task_id: taskID,
        question,
        started_at_ms: startedAtMS,
      });
      void askGroundedMarketQuestionStream(
        question,
        history,
        conversationScope,
        event => {
          setTask(previous =>
            previous.status === "pending" && previous.task_id === taskID
              ? {
                  ...previous,
                  stage: event.stage,
                  stage_status: event.status,
                  response: event.response,
                }
              : previous
          );
        },
        request.signal
      ).then(
        result => {
          if (!request.finish()) return;
          const completedAtMS = Date.now();
          if (["FAILED", "TIMEOUT", "CANCELED"].includes(result.status)) {
            setTask({
              status: "failed",
              task_id: taskID,
              question,
              completed_at_ms: completedAtMS,
              elapsed_ms: completedAtMS - startedAtMS,
              message:
                result.status === "TIMEOUT"
                  ? QUESTION_TIMEOUT_MESSAGE
                  : result.status === "CANCELED"
                    ? "요청을 취소했습니다. 계산 결과가 있으면 그대로 유지합니다."
                    : "설명을 완료하지 못했습니다. 확인된 계산 결과는 그대로 표시합니다.",
              response: result.response,
              terminal_status: result.status,
            });
            return;
          }
          setTask({
            status: "completed",
            task_id: taskID,
            question,
            response: result.response,
            elapsed_ms: completedAtMS - startedAtMS,
            completed_at_ms: completedAtMS,
            terminal_status: result.status,
            legacy: result.legacy,
          });
        },
        error => {
          if (!request.finish()) return;
          setTask(previous => ({
            status: "failed",
            task_id: taskID,
            question,
            completed_at_ms: Date.now(),
            message: questionRequestErrorMessage(error),
            elapsed_ms: Date.now() - startedAtMS,
            response:
              previous.status === "pending" && previous.task_id === taskID
                ? previous.response
                : undefined,
            terminal_status:
              previous.status === "pending" && previous.task_id === taskID
                ? previous.stage_status
                : undefined,
          }));
        }
      );
    },
    []
  );

  const value = useMemo(
    () => ({ task, startTask, cancelTask, clearTask }),
    [startTask, task, cancelTask, clearTask]
  );
  return (
    <MarketQuestionTaskContext.Provider value={value}>
      {children}
    </MarketQuestionTaskContext.Provider>
  );
}

export function useMarketQuestionTask(): MarketQuestionTaskContextValue {
  const value = useContext(MarketQuestionTaskContext);
  if (!value) {
    throw new Error(
      "useMarketQuestionTask requires MarketQuestionTaskProvider"
    );
  }
  return value;
}
