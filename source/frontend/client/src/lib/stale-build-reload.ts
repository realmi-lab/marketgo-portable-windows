// After a deploy, a tab opened on the previous build asks for code chunks
// that the server no longer has. Load the new build once instead of failing.

const RELOAD_KEY = "marketgo-stale-build-reload-at";
const RELOAD_GUARD_MS = 60_000;

/** A failed code/style download can mean a stale build or a network failure. */
export function isStaleBuildError(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error ?? "");
  return /dynamically imported module|Importing a module script failed|Unable to preload CSS for/i.test(
    message
  );
}

/**
 * Reloads the page at most once a minute so a missing chunk cannot cause a
 * reload loop. Returns false (and does nothing) when a reload just ran or the
 * guard cannot be stored. Offline failures wait for connectivity first.
 */
export function reloadForNewBuild(now = Date.now()): boolean {
  if (typeof navigator !== "undefined" && navigator.onLine === false)
    return false;
  try {
    const last = Number(sessionStorage.getItem(RELOAD_KEY) || 0);
    if (now - last < RELOAD_GUARD_MS) return false;
    sessionStorage.setItem(RELOAD_KEY, String(now));
  } catch {
    return false;
  }
  window.location.reload();
  return true;
}
