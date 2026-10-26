// Where a sign-in returns to (ADR-018, T-043): one rule for the BFF, which
// enforces it on /auth/login, and for the page that links there, which
// builds the link with it. Shared by server and browser code; it holds no
// secret.

/** The BFF's sign-in route (a route handler, not a page). */
export const LOGIN_PATH = "/auth/login";

/**
 * A same-origin path to return to after login, or "/" for anything else: it
 * must start with one "/", and must not be "//host", "/\\host" or carry a
 * scheme, a backslash a browser would read as "/", or anything but visible
 * ASCII (a path the browser sends is percent-encoded; a raw character
 * outside Latin-1 could not even be put in the Location header).
 */
export function safeReturnTo(path: string | null): string {
  if (path === null || path.length > 512 || !path.startsWith("/") || path.startsWith("//")) return "/";
  if (/[^\x21-\x7e]|\\/.test(path)) return "/";
  return path;
}

/**
 * The sign-in link for a page at `here` (its path and query): the BFF's
 * login, returning to `here` when the BFF would, else to the start page.
 */
export function signInHref(here: string | null): string {
  return `${LOGIN_PATH}?return_to=${encodeURIComponent(safeReturnTo(here))}`;
}
