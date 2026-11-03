import {
  createContext,
  useCallback,
  useContext,
  useState,
  type ReactNode,
} from "react";

export type LiveConnection = "connecting" | "live" | "stale" | "offline";

type LiveStatus = {
  connection: LiveConnection;
  lastMessageAt: number;
};

type LiveStatusPublisher = (status: Partial<LiveStatus>) => void;

const LiveStatusContext = createContext<LiveStatus>({
  connection: "connecting",
  lastMessageAt: 0,
});
// Publishers (the stream pages) read only this stable function, so the
// once-per-second receive time re-renders the status badges, not the pages.
const LiveStatusPublisherContext = createContext<LiveStatusPublisher>(
  () => undefined
);

export function LiveStatusProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<LiveStatus>({
    connection: "connecting",
    lastMessageAt: 0,
  });
  const publish = useCallback<LiveStatusPublisher>(next => {
    setStatus(current => {
      const connection = next.connection ?? current.connection;
      const lastMessageAt = next.lastMessageAt ?? current.lastMessageAt;
      return current.connection === connection &&
        current.lastMessageAt === lastMessageAt
        ? current
        : { connection, lastMessageAt };
    });
  }, []);

  return (
    <LiveStatusPublisherContext.Provider value={publish}>
      <LiveStatusContext.Provider value={status}>
        {children}
      </LiveStatusContext.Provider>
    </LiveStatusPublisherContext.Provider>
  );
}

export function useLiveStatus() {
  return useContext(LiveStatusContext);
}

export function useLiveStatusPublisher() {
  return useContext(LiveStatusPublisherContext);
}
