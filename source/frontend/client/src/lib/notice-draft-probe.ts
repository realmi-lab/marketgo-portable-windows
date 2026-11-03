/** The name the dashboard's own page carries (application-name in index.html). */
export const DASHBOARD_PAGE_NAME = "MarketGo dashboard";

/**
 * Whether the page at the notice draft address is the tool itself. Without
 * the tool's route a server answers with the dashboard's own page instead,
 * which nginx also marks as not to be shown in a frame.
 */
export function isNoticeDraftTool(headers: Headers, page: string): boolean {
  const options = headers.get("x-frame-options") ?? "";
  const policy = headers.get("content-security-policy") ?? "";
  const framable =
    !/deny/i.test(options) && !/frame-ancestors\s+'none'/i.test(policy);
  return framable && !page.includes(`content="${DASHBOARD_PAGE_NAME}"`);
}
