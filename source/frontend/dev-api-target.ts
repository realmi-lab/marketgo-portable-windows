/** Node-only Vite configuration; never import this module into browser code. */
export function apiProxyTarget(configured?: string): string {
  const parsed = new URL(configured?.trim() || "http://127.0.0.1:8000");
  if (!["http:", "https:"].includes(parsed.protocol)) {
    throw new Error("Development API target must use http or https");
  }
  if (parsed.username || parsed.password) {
    throw new Error("Development API target must not include credentials");
  }
  if (!["127.0.0.1", "localhost", "[::1]"].includes(parsed.hostname)) {
    throw new Error("Development API target must use a loopback address");
  }
  if (parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw new Error(
      "Development API target must be an origin without path, query or fragment"
    );
  }
  return parsed.origin;
}
