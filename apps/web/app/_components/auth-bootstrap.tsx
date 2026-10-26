"use client";
// The signed-in bootstrap (T-043 follow-up, D-045, H-029), mounted by the
// root layout around the strip and the page. The server says only whether
// the request carried a session cookie that opens (signedIn) and which
// organization the reader chose before (an ID, or null); no token or sealed
// value reaches the browser (ADR-018). With a session it loads GET /v1/me
// through the BFF, which refreshes the access token on the server when it
// must (a rotated token can be stored only there, in the response's
// Set-Cookie), and provides the result through the auth context the pages
// read: the chosen organization as the session, its role, and the account.
import { type ReactNode, useCallback, useMemo, useState } from "react";
import type { Result } from "../../lib/api/client";
import { unauthenticatedProblem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { type Bootstrap, bootstrapFrom } from "../../lib/auth/bootstrap";
import { organizationCookieValue, parseOrganization } from "../../lib/auth/organization";
import { type Account, type Auth, AuthProvider } from "../../lib/auth/session";
import { signOut } from "../../lib/auth/sign-out";
import type { Me, OrganizationID } from "../../lib/contracts";
import { useRemote } from "./use-remote";

const signedOut: Bootstrap = { state: "signed_out" };
const notAsked: Result<Me> = { ok: false, problem: unauthenticatedProblem() };
const secure = () => location.protocol === "https:";

export function AuthBootstrap({ signedIn, organization, navigate, children }: {
  /** The request carried a session cookie the BFF opens (lib/session/server hasSession). */
  signedIn: boolean;
  /** The organization remembered from before, if any; used only while /me lists it. */
  organization: OrganizationID | null;
  /** Leaves the page after sign-out; location.assign unless a test gives its own. */
  navigate?: (url: string) => void;
  children: ReactNode;
}) {
  // Outside any session: GET /me selects no organization (D-045).
  const api = useApi();
  const [chosen, setChosen] = useState<OrganizationID | null>(organization);
  // Without a session nothing is asked: the page is signed out from the first paint.
  const [me, retry] = useRemote((signal) => (signedIn ? api.getMe({ signal }) : Promise.resolve(notAsked)), api, signedIn);
  const bootstrap = useMemo(() => (signedIn ? bootstrapFrom(me, chosen) : signedOut), [signedIn, me, chosen]);

  const ready = bootstrap.state === "ready" ? bootstrap.organization : null;
  const organizationId = ready?.id ?? null;
  // One session object per organization: a new one (a switch) makes every page load again.
  const session = useMemo(() => (organizationId === null ? null : { organizationId }), [organizationId]);
  const role = ready?.role ?? null;

  const choose = useCallback((id: string) => {
    const org = parseOrganization(id);
    if (org === null) return;
    document.cookie = organizationCookieValue(org, secure());
    setChosen(org);
  }, []);
  const end = useCallback(
    () =>
      signOut({
        fetch: (input, init) => globalThis.fetch(input, init),
        navigate: navigate ?? ((url) => location.assign(url)),
        forget: () => {
          document.cookie = organizationCookieValue(null, secure());
        },
      }),
    [navigate],
  );
  const account = useMemo<Account>(() => ({ bootstrap, choose, retry, signOut: end }), [bootstrap, choose, retry, end]);
  const value = useMemo<Auth>(() => ({ session, role, account }), [session, role, account]);
  return <AuthProvider value={value}>{children}</AuthProvider>;
}
