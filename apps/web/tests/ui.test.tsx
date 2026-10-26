// T-014: what each state looks like, rendered with react-dom/server. Static
// rendering never runs effects, so behaviour that depends on them (API calls,
// loads, retries) is tested in a DOM in dom.test.tsx. Covers the auth
// boundary markup, the TEST-009 presentation of 403/404, the read-only role,
// and the conflict choices.
import { readFileSync } from "node:fs";
import type { ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import ProjectsPage from "../app/projects/page";
import { ProblemView } from "../app/_components/problem-view";
import { DraftStatus, RemoteView, RequireSession } from "../app/_components/states";
import { createApiClient } from "../lib/api/client";
import { draftETag } from "../lib/api/etag";
import { type Problem, type ProblemKind, problemFromResponse } from "../lib/api/problem";
import { type Auth, AuthProvider, canEdit } from "../lib/auth/session";
import type { Draft, Model } from "../lib/contracts";
import { loadModelPage } from "../lib/pages";
import { openDraft, reduceDraft } from "../lib/state/draft";
import { en } from "../messages/en";

const fixture = <T,>(name: string): T =>
  JSON.parse(readFileSync(new URL(`../../../testdata/contracts/${name}`, import.meta.url), "utf8")) as T;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const viewer: Auth = { session: { organizationId: ORG }, role: "viewer" };
const html = (node: ReactNode, auth: Auth | null = null) =>
  renderToStaticMarkup(auth === null ? node : <AuthProvider value={auth}>{node}</AuthProvider>);
const problemOf = (kind: ProblemKind, extra: Partial<Problem> = {}): Problem => ({
  kind, code: null, status: null, message: "", requestId: null, details: [], retryAfterSeconds: null, ...extra,
});
const draft = fixture<Draft>("valid-draft-with-issues.json");
// Rendered without an I18nProvider, so in the default locale, English.
const heading = (kind: ProblemKind) => en[`problem.heading.${kind}`];
const kinds = { unauthenticated: 1, forbidden: 1, not_found: 1, conflict: 1, invalid: 1, precondition: 1, rate_limited: 1, unavailable: 1, internal: 1, network: 1, protocol: 1 } satisfies Record<ProblemKind, 1>;

describe("auth boundary", () => {
  it("renders signed out without a provider, and never the protected content", () => {
    const out = html(<RequireSession><p>tenant data</p></RequireSession>);
    expect(out).toContain(heading("unauthenticated"));
    expect(out).not.toContain("tenant data");
  });

  // T-043 follow-up (impeccable audit 2026-10-19 P0): the notice has its
  // action, one plain link to the BFF's sign-in (a route handler, not a
  // page), returning to the start page when there is no page to return to.
  it("offers one sign-in, to the BFF's /auth/login", () => {
    const out = html(<RequireSession><p>tenant data</p></RequireSession>);
    expect(out.match(/<a /g)).toHaveLength(1);
    expect(out).toContain(`<a href="/auth/login?return_to=%2F">${en["auth.signIn"]}</a>`);
  });

  it("offers the sign-in when a load finds the session ended (401), and only then", () => {
    const signIn = `>${en["auth.signIn"]}</a>`;
    expect(html(<RemoteView remote={{ state: "failed", problem: problemOf("unauthenticated") }} what="projects" onRetry={() => {}}>{() => null}</RemoteView>)).toContain(signIn);
    for (const kind of Object.keys(kinds).filter((k) => k !== "unauthenticated") as ProblemKind[]) {
      expect(html(<RemoteView remote={{ state: "failed", problem: problemOf(kind) }} what="projects" onRetry={() => {}}>{() => null}</RemoteView>), kind).not.toContain(signIn);
    }
  });

  // Whether a signed-out page calls the API needs effects to run; that is
  // tested in a DOM in dom.test.tsx (static rendering never runs effects).

  it("shows protected content and a loading state with a session", () => {
    const out = html(<RequireSession><ProjectsPage /></RequireSession>, editor);
    expect(out).toContain('role="status"');
    expect(out).toContain(en["loading.projects"]);
  });

  it("offers editing only to owners and editors; an unknown role is read-only", () => {
    expect([canEdit("owner"), canEdit("editor"), canEdit("viewer"), canEdit(null)]).toEqual([true, true, false, false]);
  });
});

describe("problems (TEST-009 presentation)", () => {
  it("renders every kind as an alert with its heading", () => {
    for (const kind of Object.keys(kinds) as ProblemKind[]) {
      const out = html(<ProblemView problem={problemOf(kind, { message: "server says" })} />);
      expect(out).toContain('role="alert"');
      expect(out).toContain(`data-problem="${kind}"`);
      expect(out).toContain(heading(kind));
    }
  });

  it("says the same for a missing resource and another tenant's, without echoing the server", () => {
    const missing = html(<ProblemView problem={problemFromResponse(404, new Headers(), {
      error: { code: "NOT_FOUND", message: "model mdl_x of org_y", request_id: REQ, details: [{ path: "/organizations/org_y", reason: "owned by org_y" }] },
    })} />);
    expect(missing).toContain(heading("not_found"));
    expect(missing).not.toContain("org_y");
    expect(missing).toContain(REQ);
  });

  it("explains a 403 and shows validation details and Retry-After", () => {
    expect(html(<ProblemView problem={problemOf("forbidden")} />)).toContain("does not allow this action");
    const invalid = html(<ProblemView problem={problemOf("invalid", { details: [{ path: "/nodes/1", reason: "bad <b>", code: "X" }] })} />);
    expect(invalid).toContain("<code>/nodes/1</code>");
    expect(invalid).toContain("bad &lt;b&gt;"); // server text is escaped, never HTML
    expect(html(<ProblemView problem={problemOf("rate_limited", { retryAfterSeconds: 9 })} />)).toContain("Try again in 9 seconds.");
    expect(html(<ProblemView problem={problemOf("rate_limited", { retryAfterSeconds: 1 })} />)).toContain("Try again in 1 second.");
    expect(html(<ProblemView problem={problemOf("internal")} />)).not.toContain("Request ID");
  });

  it("RemoteView shows loading, the problem or the data", () => {
    const render = (r: Parameters<typeof RemoteView<string>>[0]["remote"]) => html(<RemoteView remote={r} what="models">{(d) => <p>{d}</p>}</RemoteView>);
    expect(render({ state: "loading" })).toContain(en["loading.models"]);
    expect(render({ state: "failed", problem: problemOf("forbidden") })).toContain(heading("forbidden"));
    expect(render({ state: "loaded", data: "hello", requestId: null })).toContain("<p>hello</p>");
  });
});

describe("draft status", () => {
  const noop = () => {};
  const reloadOk = async () => null;
  const edit = { op: "replace", path: "/name", value: "x" } as const;
  const conflicted = reduceDraft(
    reduceDraft(reduceDraft(openDraft({ ...draft, revision: 3 }), { type: "edit", ops: [edit] }), { type: "saveStarted", revision: 3, ops: [edit] }),
    { type: "saveFailed", problem: problemOf("conflict", { code: "MODEL_VERSION_CONFLICT" }) },
  );

  it("gives an editor both choices on a conflict and keeps the changes", () => {
    const out = html(<DraftStatus session={conflicted} onReload={reloadOk} onDiscard={noop} />, editor);
    expect(out).toContain('data-draft-status="conflict"');
    expect(out).toContain("Draft revision 3. 1 change not saved.");
    expect(out).toContain("Reload the current draft");
    expect(out).toContain("Discard my changes");
    expect(out).toContain(heading("conflict"));
  });

  it("gives a viewer no edit actions", () => {
    const out = html(<DraftStatus session={conflicted} onReload={reloadOk} onDiscard={noop} />, viewer);
    expect(out).not.toContain("<button");
    expect(out).toContain("You can view this model but not change it.");
  });

  it("explains why a clean draft cannot be published", () => {
    const withErrors = openDraft({ ...draft, issues: [{ code: "NO_REACHABLE_SINK", path: "/nodes/1", message: "x", severity: "error" }] });
    expect(html(<DraftStatus session={withErrors} onReload={reloadOk} onDiscard={noop} />, editor)).toContain("Publishing is unavailable");
    expect(html(<DraftStatus session={openDraft({ ...draft, out_of_date: true })} onReload={reloadOk} onDiscard={noop} />, editor)).toContain(
      "A newer version was published",
    );
  });
});

describe("model page loader", () => {
  const model: Model = {
    id: draft.model_id, organization_id: ORG, project_id: "prj_01M4YDQK00TA00000000000001", domain: "software_delivery", name: "M",
    draft: { revision: draft.revision, base_version_id: draft.base_version_id, out_of_date: false },
    latest_version: { id: draft.base_version_id, version: 1 }, created_at: "2026-10-15T08:00:00Z",
  };
  const client = (...responses: Response[]) =>
    createApiClient({ session: () => editor.session, fetch: (async () => responses.shift()!) as typeof fetch });
  const ok = (body: unknown, headers: Record<string, string> = {}) => new Response(JSON.stringify(body), { status: 200, headers });

  it("loads model and draft together", async () => {
    const r = await loadModelPage(client(ok(model), ok(draft, { ETag: draftETag(draft.revision) })), draft.model_id);
    expect(r.ok && [r.data.model.id, r.data.draft.revision]).toEqual([draft.model_id, draft.revision]);
  });

  it("shows nothing half-loaded: any failure fails the page", async () => {
    const forbidden = new Response(JSON.stringify({ error: { code: "FORBIDDEN", message: "no", request_id: REQ } }), { status: 403 });
    const r = await loadModelPage(client(ok(model), forbidden), draft.model_id);
    expect(!r.ok && r.problem.kind).toBe("forbidden");
    const foreign = await loadModelPage(client(ok({ ...model, id: "mdl_01M4YDQK00TA00000000000009" }), ok(draft, { ETag: draftETag(draft.revision) })), "mdl_01M4YDQK00TA00000000000009");
    expect(!foreign.ok && foreign.problem.kind).toBe("protocol");
  });
});
