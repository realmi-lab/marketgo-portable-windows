export const DASHBOARD_SECTION_IDS = [
  "summary",
  "share-llm",
  "intelligence",
  "drivers",
  "events",
  "notice-llm2",
  "quality",
  "index",
  "index2",
] as const;

export type DashboardSection = (typeof DASHBOARD_SECTION_IDS)[number];

export type DashboardPage =
  "index2" | "index" | "summary" | "share-llm" | "intelligence" | "events" | "notice-llm2" | "market-share";

export function dashboardSectionFromHash(hash: string): DashboardSection {
  const section = hash.replace(/^#/, "");
  if (section === "market-share") return "summary";
  if (section === "notices" || section === "notice-llm") return "events";
  return DASHBOARD_SECTION_IDS.includes(section as DashboardSection)
    ? (section as DashboardSection)
    : "summary";
}

export function dashboardPageForSection(
  section: DashboardSection
): DashboardPage {
  if (section === "index2") return "index2";
  if (section === "index") return "index";
  if (section === "summary") return "summary";
  if (section === "share-llm") return "share-llm";
  if (section === "intelligence") return "intelligence";
  if (section === "events") return "events";
  if (section === "notice-llm2") return "notice-llm2";
  return "market-share";
}

/*
 * The main dashboard's screens, in menu order. "status" (자료 상태) sits
 * outside the eleven menus, behind the data-status chip. The two indexes keep
 * the addresses they had before the redesign (#index, #index2).
 */
export const SCREEN_IDS = [
  "brief",
  "live",
  "change",
  "why",
  "coins",
  "exchanges",
  "index",
  "index2",
  "coin-shares",
  "notices",
  "question",
  "status",
] as const;

export type ScreenId = (typeof SCREEN_IDS)[number];

/** A screen and, optionally, the part of it a link points at. */
export interface ScreenRoute {
  screen: ScreenId;
  focus?: "notice-draft";
}

// Links saved before the redesign keep working: each old section opens the
// screen that now holds its content.
const LEGACY_SCREEN_ROUTES: Record<string, ScreenRoute> = {
  summary: { screen: "brief" },
  "market-share": { screen: "brief" },
  liquidity: { screen: "exchanges" },
  intelligence: { screen: "change" },
  drivers: { screen: "coins" },
  events: { screen: "notices" },
  notices: { screen: "notices" },
  "notice-llm": { screen: "notices" },
  "notice-llm2": { screen: "notices", focus: "notice-draft" },
  quality: { screen: "status" },
  "share-llm": { screen: "question" },
};

// The notice draft has its own address, so a reload keeps it open.
const NOTICE_DRAFT_HASH = "notice-draft";

export function screenRouteFromHash(hash: string): ScreenRoute {
  const id = hash.replace(/^#/, "");
  if ((SCREEN_IDS as readonly string[]).includes(id)) {
    return { screen: id as ScreenId };
  }
  if (id === NOTICE_DRAFT_HASH) return { screen: "notices", focus: "notice-draft" };
  return LEGACY_SCREEN_ROUTES[id] ?? { screen: "brief" };
}

/** The address a route keeps: its screen's, or the notice draft's. */
export function screenHash(route: ScreenRoute): string {
  return route.focus === "notice-draft" ? `#${NOTICE_DRAFT_HASH}` : `#${route.screen}`;
}
