// The signed-in bootstrap's rules (T-043 follow-up, D-045): what GET /v1/me,
// loaded through the BFF, and the remembered organization mean for the UI.
// Pure, so every state is testable without React; app/_components/
// auth-bootstrap.tsx provides the result through the auth context.
//
// The role only shapes the UI (canEdit); the API authorizes every request.
// The organization is a selector only (D-008), sent as X-Organization-ID.
import type { Problem } from "../api/problem";
import { isID } from "../api/ids";
import type { Me, OrganizationID } from "../contracts";
import type { Remote } from "../state/remote";
import type { Role } from "./session";

/** One of the signed-in user's organizations; a role this build does not know is null (read-only). */
export type Organization = { id: OrganizationID; name: string; role: Role | null };

export type Bootstrap =
  /** No session (none, or it ended: the BFF answered 401): the sign-in. */
  | { state: "signed_out" }
  /** The session's /me is on its way. */
  | { state: "loading" }
  /** /me failed otherwise (provider or API unavailable, network, a broken answer): retry. */
  | { state: "failed"; problem: Problem }
  /** Signed in, but Geminus has no user for the identity (403, D-008: never created here) or no membership. */
  | { state: "no_access"; problem: Problem | null }
  /** Several organizations and none chosen yet. */
  | { state: "choose"; organizations: Organization[]; truncated: boolean }
  /** An organization: the only one, the remembered one or the chosen one. */
  | { state: "ready"; organizations: Organization[]; truncated: boolean; organization: Organization };

const roles: ReadonlySet<string> = new Set<Role>(["owner", "editor", "viewer"]);
const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/** The organizations /me lists, in its order, or null when the answer does not follow the contract. */
function organizationsOf(me: unknown): { organizations: Organization[]; truncated: boolean } | null {
  if (!isObject(me) || !Array.isArray(me.memberships) || typeof me.truncated !== "boolean") return null;
  const organizations: Organization[] = [];
  for (const m of me.memberships as unknown[]) {
    if (!isObject(m) || typeof m.organization_id !== "string" || !isID("org", m.organization_id) || typeof m.organization_name !== "string") return null;
    if (organizations.some((o) => o.id === m.organization_id)) return null;
    organizations.push({ id: m.organization_id, name: m.organization_name, role: typeof m.role === "string" && roles.has(m.role) ? (m.role as Role) : null });
  }
  return { organizations, truncated: me.truncated };
}

/**
 * The bootstrap for the state of /me and the organization chosen (or
 * remembered) so far. One organization is chosen by itself; of several, the
 * chosen one only while it is still a membership.
 */
export function bootstrapFrom(me: Remote<Me>, chosen: OrganizationID | null): Bootstrap {
  if (me.state === "loading") return { state: "loading" };
  if (me.state === "failed") {
    if (me.problem.kind === "unauthenticated") return { state: "signed_out" };
    if (me.problem.kind === "forbidden") return { state: "no_access", problem: me.problem };
    return { state: "failed", problem: me.problem };
  }
  const listed = organizationsOf(me.data);
  if (listed === null) {
    return {
      state: "failed",
      problem: { kind: "protocol", code: null, status: 200, message: "The /me answer does not follow the contract.", requestId: me.requestId, details: [], retryAfterSeconds: null },
    };
  }
  const { organizations, truncated } = listed;
  if (organizations.length === 0) return { state: "no_access", problem: null };
  const only = organizations.length === 1 ? organizations[0] : undefined;
  const organization = only ?? organizations.find((o) => o.id === chosen);
  return organization === undefined ? { state: "choose", organizations, truncated } : { state: "ready", organizations, truncated, organization };
}

/** The names more than one of the organizations has: their labels add the ID, so a person can tell them apart. */
export function sharedNames(organizations: Organization[]): Set<string> {
  const seen = new Set<string>();
  const shared = new Set<string>();
  for (const o of organizations) (seen.has(o.name) ? shared : seen).add(o.name);
  return shared;
}
