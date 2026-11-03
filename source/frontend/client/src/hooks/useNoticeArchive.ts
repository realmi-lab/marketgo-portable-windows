import { useEffect, useState } from "react";
import { getNoticeArchive } from "@/lib/api-client";
import {
  loadWholeNoticeArchive,
  type NoticeArchivePage,
  type NoticeArchiveRequest,
} from "@/lib/notice-archive";

export function useNoticeArchive(enabled: boolean) {
  const [request, setRequest] = useState<NoticeArchiveRequest>({ offset: 0 });
  const [data, setData] = useState<NoticeArchivePage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!enabled) return;
    let active = true;
    let refresh: ReturnType<typeof setTimeout> | undefined;
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 8000);
    setLoading(true);
    getNoticeArchive(request, controller.signal)
      .then(page => {
        if (!active) return;
        setData(page);
        setError("");
      })
      .catch(reason => {
        if (!active) return;
        if (
          reason instanceof Error &&
          reason.message === "NOTICE_ARCHIVE_CHANGED"
        ) {
          setRequest({ offset: 0 });
          setError("공지 목록이 갱신되어 첫 페이지를 다시 확인합니다.");
        } else setError("전체 공지를 불러오지 못했습니다. 다시 확인해 주세요.");
      })
      .finally(() => {
        clearTimeout(deadline);
        if (!active) return;
        setLoading(false);
        // Do not replace the page the user is reading. Refresh first-page data
        // only after this bounded request ends; subsequent pages are versioned.
        if (request.offset === 0)
          refresh = setTimeout(() => setRequest({ offset: 0 }), 60000);
      });
    return () => {
      active = false;
      clearTimeout(deadline);
      clearTimeout(refresh);
      controller.abort();
    };
  }, [enabled, request]);
  return {
    data,
    loading,
    error,
    refresh: () => setRequest({ offset: 0 }),
    goTo: (page: number) => {
      if (!data || loading) return;
      setRequest({
        offset: Math.max(0, page) * data.limit,
        version: data.snapshot.version,
        asOfMS: data.as_of_ms,
      });
    },
  };
}

/**
 * The whole 30-day archive, so filters and search cover every notice instead
 * of one 20-item page. A one-item probe each minute reloads only when the
 * server's notice version changed.
 */
export function useWholeNoticeArchive() {
  const [data, setData] = useState<NoticeArchivePage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const deadline = setTimeout(() => controller.abort(), 15000);
    const fetchPage = (request: NoticeArchiveRequest) =>
      getNoticeArchive(request, controller.signal);
    setLoading(true);
    loadWholeNoticeArchive(fetchPage)
      // A new ingest between pages (409) restarts once from the first page.
      .catch(reason =>
        reason instanceof Error && reason.message === "NOTICE_ARCHIVE_CHANGED"
          ? loadWholeNoticeArchive(fetchPage)
          : Promise.reject(reason)
      )
      .then(page => {
        if (!active) return;
        setData(page);
        setError("");
      })
      .catch(() => {
        if (active) setError("공지를 불러오지 못했습니다. 다시 확인해 주세요.");
      })
      .finally(() => {
        clearTimeout(deadline);
        if (active) setLoading(false);
      });
    return () => {
      active = false;
      clearTimeout(deadline);
      controller.abort();
    };
  }, [reload]);
  const version = data?.snapshot.version;
  useEffect(() => {
    if (version === undefined || loading) return;
    let active = true;
    let pending: AbortController | null = null;
    let deadline: ReturnType<typeof setTimeout> | undefined;
    const probe = () => {
      if (document.visibilityState !== "visible" || pending) return;
      const controller = new AbortController();
      pending = controller;
      deadline = setTimeout(() => controller.abort(), 8000);
      getNoticeArchive({ offset: 0, limit: 1 }, controller.signal)
        .then(probe => {
          if (active && !controller.signal.aborted && probe.snapshot.version !== version)
            setReload(value => value + 1);
        })
        .catch(() => undefined)
        .finally(() => {
          clearTimeout(deadline);
          pending = null;
        });
    };
    const timer = setInterval(probe, 60000);
    document.addEventListener("visibilitychange", probe);
    return () => {
      active = false;
      clearInterval(timer);
      clearTimeout(deadline);
      document.removeEventListener("visibilitychange", probe);
      pending?.abort();
    };
  }, [version, loading]);
  return {
    data,
    loading,
    error,
    refresh: () => setReload(value => value + 1),
  };
}
