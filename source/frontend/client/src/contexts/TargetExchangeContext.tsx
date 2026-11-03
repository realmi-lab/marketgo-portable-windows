import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import type { DomesticExchange } from "@/lib/market-share-config";
import {
  DEFAULT_TARGET_EXCHANGE,
  normalizeTargetExchange,
  TARGET_EXCHANGE_STORAGE_KEY,
} from "@/lib/target-exchange";

type TargetExchangeContextValue = {
  targetExchange: DomesticExchange;
  setTargetExchange: (exchange: DomesticExchange) => void;
};

const TargetExchangeContext = createContext<TargetExchangeContextValue | null>(
  null
);

function initialTargetExchange(): DomesticExchange {
  if (typeof window === "undefined") return DEFAULT_TARGET_EXCHANGE;
  try {
    return normalizeTargetExchange(
      window.localStorage.getItem(TARGET_EXCHANGE_STORAGE_KEY)
    );
  } catch {
    return DEFAULT_TARGET_EXCHANGE;
  }
}

export function TargetExchangeProvider({ children }: { children: ReactNode }) {
  const [targetExchange, setTargetExchange] = useState<DomesticExchange>(
    initialTargetExchange
  );

  useEffect(() => {
    try {
      window.localStorage.setItem(TARGET_EXCHANGE_STORAGE_KEY, targetExchange);
    } catch {
      // Private browsing or a storage policy may deny persistence. The in-memory
      // selection remains valid for the current session.
    }
  }, [targetExchange]);

  const value = useMemo(
    () => ({ targetExchange, setTargetExchange }),
    [targetExchange]
  );
  return (
    <TargetExchangeContext.Provider value={value}>
      {children}
    </TargetExchangeContext.Provider>
  );
}

export function useTargetExchange(): TargetExchangeContextValue {
  const value = useContext(TargetExchangeContext);
  if (!value) {
    throw new Error("useTargetExchange must be used inside TargetExchangeProvider");
  }
  return value;
}
