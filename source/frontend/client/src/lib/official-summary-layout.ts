export const OFFICIAL_SUMMARY_CARD_ORDER = [
  "share",
  "bithumb-volume",
  "domestic-volume",
  "collection",
  "event",
  "asset",
  "evidence",
] as const;

export type OfficialSummaryCardID =
  (typeof OFFICIAL_SUMMARY_CARD_ORDER)[number];

export const OFFICIAL_SUMMARY_LIGHT_GROUPS = {
  hero: OFFICIAL_SUMMARY_CARD_ORDER.slice(0, 1),
  secondary: OFFICIAL_SUMMARY_CARD_ORDER.slice(1, 4),
  facts: OFFICIAL_SUMMARY_CARD_ORDER.slice(4),
} as const satisfies Record<string, readonly OfficialSummaryCardID[]>;

export const OFFICIAL_CHANGE_FLOW = ["focus", "question", "details"] as const;

export const OFFICIAL_CHANGE_CARD_GROUPS = {
  focus: ["share", "event", "asset"],
  details: ["bithumb-volume", "domestic-volume"],
  trust: ["collection", "evidence"],
} as const satisfies Record<string, readonly OfficialSummaryCardID[]>;
