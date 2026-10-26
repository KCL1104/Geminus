// The signed-in bootstrap's rules (T-043 follow-up, D-045, H-029): what the
// UI makes of GET /v1/me and of the remembered organization, the sign-in
// link's return path, the organization cookie and the sign-out request.
// Pure functions; the components are tested in auth-bootstrap.test.tsx.
import { describe, expect, it } from "vitest";
import { type Bootstrap, bootstrapFrom, sharedNames } from "../lib/auth/bootstrap";
import { organizationCookie, organizationCookieValue, parseOrganization } from "../lib/auth/organization";
import { signOut } from "../lib/auth/sign-out";
import type { Me } from "../lib/contracts";
import { networkProblem, type Problem, problemFromResponse } from "../lib/api/problem";
import type { Remote } from "../lib/state/remote";
import { safeReturnTo, signInHref } from "../lib/return-to";
import { safeReturnTo as sessionSafeReturnTo } from "../lib/session/session";

const A = "org_01M4YDQK004J6EG1BW3SRYPDGD";
const B = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const C = "org_01M4YDQK00TA00000000000003";
const USER = "usr_01M4YDQK0016KTK4JM6KAHZ6BR";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";

const me = (memberships: Me["memberships"], truncated = false): Remote<Me> => ({ state: "loaded", data: { user_id: USER, memberships, truncated }, requestId: REQ });
const failed = (status: number, code: string): Remote<Me> => ({
  state: "failed",
  problem: problemFromResponse(status, new Headers({ "X-Request-ID": REQ }), { error: { code, message: "m", request_id: REQ } }),
});
const acme = { organization_id: A, organization_name: "Acme Delivery", role: "viewer" } as const;
const plant = { organization_id: B, organization_name: "Plant 2", role: "owner" } as const;
const lab = { organization_id: C, organization_name: "研發部", role: "editor" } as const;

const stateOf = (b: Bootstrap) => b.state;

describe("bootstrapFrom: what /me means for the UI", () => {
  it("is loading while /me is on its way", () => {
    expect(bootstrapFrom({ state: "loading" }, null)).toEqual({ state: "loading" });
  });

  it("is signed out when the BFF says the session is missing or has ended (401, also after a refused refresh)", () => {
    expect(bootstrapFrom(failed(401, "UNAUTHENTICATED"), A)).toEqual({ state: "signed_out" });
  });

  it("is no access, with the API's problem, for an identity Geminus does not know (403, D-008: never provisioned here)", () => {
    const b = bootstrapFrom(failed(403, "FORBIDDEN"), A);
    expect(b.state).toBe("no_access");
    expect(b.state === "no_access" && b.problem?.requestId).toBe(REQ);
    expect(b.state === "no_access" && b.problem?.kind).toBe("forbidden");
  });

  it("is no access, without a problem, for a user with no memberships", () => {
    expect(bootstrapFrom(me([]), A)).toEqual({ state: "no_access", problem: null });
  });

  it.each([
    [503, "SERVICE_UNAVAILABLE", "unavailable"],
    [500, "INTERNAL_ERROR", "internal"],
    [429, "RATE_LIMITED", "rate_limited"],
    [400, "INVALID_REQUEST", "invalid"],
  ])("is failed, keeping the problem, for %i (e.g. the provider is down during a refresh)", (status, code, kind) => {
    const b = bootstrapFrom(failed(status, code), null);
    expect(b.state).toBe("failed");
    expect(b.state === "failed" && b.problem.kind).toBe(kind);
  });

  it("is failed for a network failure", () => {
    expect(bootstrapFrom({ state: "failed", problem: networkProblem("offline") }, null)).toMatchObject({ state: "failed", problem: { kind: "network" } });
  });

  it("chooses the only organization by itself, whatever is remembered", () => {
    for (const remembered of [null, A, B]) {
      expect(bootstrapFrom(me([acme]), remembered)).toEqual({
        state: "ready",
        organization: { id: A, name: "Acme Delivery", role: "viewer" },
        organizations: [{ id: A, name: "Acme Delivery", role: "viewer" }],
        truncated: false,
      });
    }
  });

  it("asks which one when there are several and none is remembered", () => {
    const b = bootstrapFrom(me([acme, plant, lab]), null);
    expect(b).toEqual({
      state: "choose",
      organizations: [
        { id: A, name: "Acme Delivery", role: "viewer" },
        { id: B, name: "Plant 2", role: "owner" },
        { id: C, name: "研發部", role: "editor" },
      ],
      truncated: false,
    });
  });

  it("takes the remembered one when it is still a membership", () => {
    const b = bootstrapFrom(me([acme, plant, lab]), B);
    expect(b.state).toBe("ready");
    expect(b.state === "ready" && b.organization).toEqual({ id: B, name: "Plant 2", role: "owner" });
  });

  it("asks again when the remembered one is not a membership (another user's, or removed)", () => {
    expect(stateOf(bootstrapFrom(me([acme, plant]), C))).toBe("choose");
  });

  it("keeps the API's order (name, then ID) and says when the list was cut at 100", () => {
    const b = bootstrapFrom(me([lab, acme], true), null);
    expect(b.state === "choose" && b.organizations.map((o) => o.id)).toEqual([C, A]);
    expect(b.state === "choose" && b.truncated).toBe(true);
  });

  it.each(["owner", "editor", "viewer"] as const)("maps the role %s as the API sent it", (role) => {
    const b = bootstrapFrom(me([{ ...acme, role }]), null);
    expect(b.state === "ready" && b.organization.role).toBe(role);
  });

  it("maps a role this build does not know to none (read-only), never to a known one", () => {
    const b = bootstrapFrom(me([{ ...acme, role: "admin" as never }]), null);
    expect(b.state === "ready" && b.organization.role).toBeNull();
  });

  it.each([
    ["memberships is not a list", { user_id: USER, memberships: {}, truncated: false }],
    ["a membership without an organization ID", { user_id: USER, memberships: [{ organization_name: "x", role: "owner" }], truncated: false }],
    ["an organization ID that is not one", { user_id: USER, memberships: [{ organization_id: "org_x", organization_name: "x", role: "owner" }], truncated: false }],
    ["a name that is not text", { user_id: USER, memberships: [{ organization_id: A, organization_name: 7, role: "owner" }], truncated: false }],
    ["the same organization twice", { user_id: USER, memberships: [acme, acme], truncated: false }],
    ["truncated is missing", { user_id: USER, memberships: [acme] }],
  ])("refuses an answer that breaks the contract (%s) as a protocol failure, never a guess", (_, data) => {
    const b = bootstrapFrom({ state: "loaded", data: data as unknown as Me, requestId: REQ }, A);
    expect(b.state).toBe("failed");
    expect(b.state === "failed" && b.problem.kind).toBe("protocol");
    expect(b.state === "failed" && b.problem.requestId).toBe(REQ);
  });
});

describe("sharedNames", () => {
  it("finds the names more than one organization has, so their labels can tell them apart", () => {
    const orgs = [
      { id: A, name: "Acme Delivery", role: "viewer" as const },
      { id: B, name: "Acme Delivery", role: "owner" as const },
      { id: C, name: "研發部", role: "editor" as const },
    ];
    expect([...sharedNames(orgs)]).toEqual(["Acme Delivery"]);
    expect([...sharedNames(orgs.slice(1))]).toEqual([]);
  });
});

describe("the organization cookie (a selector only, D-008)", () => {
  it("is named for what it holds", () => {
    expect(organizationCookie).toBe("geminus_organization");
  });

  it("reads an organization ID exactly, and nothing else", () => {
    expect(parseOrganization(A)).toBe(A);
    for (const v of [undefined, null, "", "org_x", A.toLowerCase(), ` ${A}`, `${A}; Path=/`, "prj_01M4YDQK004J6EG1BW3SRYPDGD", `${A}${A}`]) {
      expect(parseOrganization(v), String(v)).toBeNull();
    }
  });

  it("is remembered for the browser session only, for the whole app, Secure over https", () => {
    expect(organizationCookieValue(A, true)).toBe(`geminus_organization=${A}; Path=/; SameSite=Lax; Secure`);
    expect(organizationCookieValue(A, false)).toBe(`geminus_organization=${A}; Path=/; SameSite=Lax`);
    expect(organizationCookieValue(A, true)).not.toMatch(/Max-Age|Expires/);
  });

  it("is forgotten with Max-Age=0", () => {
    expect(organizationCookieValue(null, true)).toBe("geminus_organization=; Path=/; SameSite=Lax; Secure; Max-Age=0");
  });
});

describe("the sign-in link's return path", () => {
  it("is the BFF's rule, shared: lib/session uses the same function", () => {
    expect(sessionSafeReturnTo).toBe(safeReturnTo);
  });

  it.each([
    ["/projects", "/auth/login?return_to=%2Fprojects"],
    ["/projects/prj_01M4YDQK00TA00000000000001", "/auth/login?return_to=%2Fprojects%2Fprj_01M4YDQK00TA00000000000001"],
    ["/models/mdl_01M4YDQK004AGVNEGJQJ1NMM0N?scenario=a%20b", "/auth/login?return_to=%2Fmodels%2Fmdl_01M4YDQK004AGVNEGJQJ1NMM0N%3Fscenario%3Da%2520b"],
    // Anything the BFF would not return to is the start page.
    [null, "/auth/login?return_to=%2F"],
    ["", "/auth/login?return_to=%2F"],
    ["//evil.example/x", "/auth/login?return_to=%2F"],
    ["/\\evil.example", "/auth/login?return_to=%2F"],
    ["https://evil.example/", "/auth/login?return_to=%2F"],
    ["/projects/中", "/auth/login?return_to=%2F"],
    [`/${"a".repeat(600)}`, "/auth/login?return_to=%2F"],
  ])("%s → %s", (here, href) => {
    expect(signInHref(here)).toBe(href);
  });

  it("decodes on the BFF to the path it was given", () => {
    const here = "/models/mdl_01M4YDQK004AGVNEGJQJ1NMM0N?scenario=a%20b";
    expect(new URL(signInHref(here), "https://app.test").searchParams.get("return_to")).toBe(here);
  });
});

describe("signOut", () => {
  type Sent = { url: string; init: RequestInit };
  function deps(respond: () => Response | Promise<Response>) {
    const sent: Sent[] = [];
    const went: string[] = [];
    let forgotten = 0;
    return {
      sent,
      went,
      forgotten: () => forgotten,
      d: {
        fetch: (async (url: RequestInfo | URL, init?: RequestInit) => {
          sent.push({ url: String(url), init: init ?? {} });
          return respond();
        }) as typeof fetch,
        navigate: (url: string) => void went.push(url),
        forget: () => void forgotten++,
      },
    };
  }

  it("posts to the BFF's logout with the CSRF header, then forgets the organization and goes to the provider's logout", async () => {
    const x = deps(() => new Response(JSON.stringify({ redirect: "https://id.example/v2/logout?client_id=c" }), { status: 200, headers: { "Content-Type": "application/json" } }));
    expect(await signOut(x.d)).toBeNull();
    expect(x.sent).toHaveLength(1);
    expect(x.sent[0]?.url).toBe("/auth/logout");
    expect(x.sent[0]?.init).toMatchObject({ method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error" });
    expect(new Headers(x.sent[0]?.init.headers).get("X-Geminus-Request")).toBe("1");
    expect(x.forgotten()).toBe(1);
    expect(x.went).toEqual(["https://id.example/v2/logout?client_id=c"]);
  });

  it.each([
    ["no redirect", {}],
    ["a redirect that is not text", { redirect: 7 }],
    ["a script URL", { redirect: "javascript:alert(1)" }],
    ["not a URL", { redirect: "not a url" }],
  ])("goes to the start page when the answer has %s (the session is gone either way)", async (_, body) => {
    const x = deps(() => new Response(JSON.stringify(body), { status: 200 }));
    expect(await signOut(x.d)).toBeNull();
    expect(x.went).toEqual(["/"]);
    expect(x.forgotten()).toBe(1);
  });

  it("answers with the problem, stays and remembers the organization when the BFF refuses", async () => {
    const x = deps(() => new Response(JSON.stringify({ error: { code: "FORBIDDEN", message: "cross-site request refused", request_id: REQ } }), { status: 403 }));
    const p = (await signOut(x.d)) as Problem;
    expect(p.kind).toBe("forbidden");
    expect(p.requestId).toBe(REQ);
    expect(x.went).toEqual([]);
    expect(x.forgotten()).toBe(0);
  });

  it("answers with a network problem when the request fails", async () => {
    const x = deps(() => Promise.reject(new TypeError("offline")));
    expect((await signOut(x.d))?.kind).toBe("network");
    expect([x.went, x.forgotten()]).toEqual([[], 0]);
  });
});
