import { useEffect, useState } from "react";

import { isNoticeDraftTool } from "@/lib/notice-draft-probe";

const WORKSPACE_PATH = "/notice-llm2/";

/** "unrouted": the dashboard's own page answered, so the tool is not set up here. */
type Availability = "checking" | "ready" | "unrouted" | number;

/**
 * The writing tool is a separate service. Probe it before embedding so a
 * stopped service shows a Korean status with a retry instead of the proxy's
 * raw "502 Bad Gateway" page inside the frame.
 */
export default function NoticeLLM2Workspace() {
  const [availability, setAvailability] = useState<Availability>("checking");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setAvailability("checking");
    fetch(WORKSPACE_PATH, { cache: "no-store", signal: controller.signal })
      .then(async response => {
        if (!response.ok) {
          setAvailability(response.status);
          return;
        }
        const page = await response.text();
        setAvailability(
          isNoticeDraftTool(response.headers, page) ? "ready" : "unrouted"
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) setAvailability(0);
      });
    return () => controller.abort();
  }, [attempt]);

  if (availability === "ready") {
    return (
      <iframe
        src={WORKSPACE_PATH}
        title="빗썸 공지 초안 작성"
        className="w-full rounded-xl border-0"
        style={{ height: "calc(100dvh - 150px)", minHeight: 520 }}
      />
    );
  }
  return (
    <section
      className="glass-card flex flex-col items-start gap-3 p-6 text-sm"
      role="status"
    >
      <h2 className="text-base font-semibold">공지 초안 작성</h2>
      <p className="text-muted-foreground">
        {availability === "checking"
          ? "작성 도구 연결을 확인하고 있습니다."
          : availability === "unrouted"
            ? "공지 초안 도구가 이 서버에 연결되어 있지 않습니다. 운영 설정에서 도구 연결을 확인해 주세요."
            : "공지 초안 도구가 지금 중단되어 있습니다. 잠시 후 다시 확인해 주세요."}
      </p>
      {availability !== "checking" && (
        <button
          type="button"
          className="rounded-md border border-border px-3 py-2 font-semibold hover:border-primary/40 hover:text-primary"
          onClick={() => setAttempt(value => value + 1)}
        >
          다시 확인
        </button>
      )}
    </section>
  );
}
