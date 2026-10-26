// The organization the reader chose (T-043 follow-up, D-045), remembered for
// the browser session in a cookie like the language and the theme (ADR-020,
// H-013), so the server can hand it to the bootstrap on the next page. It is
// a selector only (D-008): the bootstrap uses it only when /me lists it, and
// the API authorizes every request against the token's membership.
import type { OrganizationID } from "../contracts";
import { isID } from "../api/ids";

export const organizationCookie = "geminus_organization";

/** The organization ID a stored value is, exactly; anything else is none. */
export function parseOrganization(value: string | null | undefined): OrganizationID | null {
  return typeof value === "string" && isID("org", value) ? value : null;
}

/** The Set-Cookie value that remembers id until the browser session ends, or forgets it (null). */
export function organizationCookieValue(id: OrganizationID | null, secure: boolean): string {
  const tail = `; Path=/; SameSite=Lax${secure ? "; Secure" : ""}`;
  return id === null ? `${organizationCookie}=${tail}; Max-Age=0` : `${organizationCookie}=${id}${tail}`;
}
