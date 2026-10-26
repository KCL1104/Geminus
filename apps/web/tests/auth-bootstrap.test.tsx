// @vitest-environment happy-dom
// The signed-in bootstrap in a DOM (T-043 follow-up, D-045, H-029): the root
// layout mounts AuthBootstrap with the server's hint (a session cookie that
// opens) and the remembered organization; it loads /me through the BFF and
// provides the session, role and account through the auth context the pages
// already read (lib/auth/session.tsx; no second state model, ADR-016). Each
// state of the bootstrap, as the projects page and the strip show it, with
// the browser's fetch replaced by a fake BFF.
import { act, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { OrganizationSwitcher, SignOutButton } from "../app/_components/account";
import { AuthBootstrap } from "../app/_components/auth-bootstrap";
import ProjectsPage from "../app/projects/page";
import { canEdit, useAuth } from "../lib/auth/session";
import { I18nProvider } from "../lib/i18n/provider";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { mount, press, settle } from "./ui-harness";

const nav = vi.hoisted(() => ({ pathname: "/projects", search: "" }));
vi.mock("next/navigation", async (orig) => ({
  ...(await orig<typeof import("next/navigation")>()),
  usePathname: () => nav.pathname,
  useSearchParams: () => new URLSearchParams(nav.search),
  useRouter: () => ({ refresh: () => {}, push: () => {} }),
}));

const A = "org_01M4YDQK004J6EG1BW3SRYPDGD";
const B = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const USER = "usr_01M4YDQK0016KTK4JM6KAHZ6BR";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const PRJ_A = "prj_01M4YDQK00TA00000000000001";
const PRJ_B = "prj_01M4YDQK00TA00000000000002";

type Seen = { url: string; method: string; headers: Headers };
let seen: Seen[];
let answers: Record<string, (s: Seen) => Response | Promise<Response>>;
let went: string[];
const realFetch = globalThis.fetch;

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
const error = (status: number, code: string) => json(status, { error: { code, message: "m", request_id: REQ } });
const meOf = (memberships: { organization_id: string; organization_name: string; role: string }[], truncated = false) => () =>
  json(200, { user_id: USER, memberships, truncated });
const projects = (s: Seen) => {
  const org = s.headers.get("X-Organization-ID");
  const items = org === A ? [{ id: PRJ_A, name: "Line A" }] : org === B ? [{ id: PRJ_B, name: "Line B" }] : [];
  return json(200, { items: items.map((p) => ({ ...p, organization_id: org, created_at: "2026-10-26T00:00:00Z", created_by: USER })), next_cursor: null });
};

beforeEach(() => {
  seen = [];
  went = [];
  nav.pathname = "/projects";
  nav.search = "";
  answers = { "GET /api/v1/projects": projects };
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost:3000");
    const s = { url: url.pathname + url.search, method: init?.method ?? "GET", headers: new Headers(init?.headers) };
    seen.push(s);
    const answer = answers[`${s.method} ${url.pathname}`];
    if (!answer) throw new Error(`no fake answer for ${s.method} ${url.pathname}`);
    return answer(s);
  }) as typeof fetch;
  for (const c of document.cookie.split(/; */)) if (c) document.cookie = `${c.split("=")[0]}=; Path=/; Max-Age=0`;
});
afterEach(() => {
  globalThis.fetch = realFetch;
});

/** What a page shows the auth context as (role and whether it may edit). */
function AuthProbe() {
  const { session, role } = useAuth();
  return <p data-probe={`${session?.organizationId ?? "none"} ${role ?? "none"} ${canEdit(role)}`} />;
}

/** The app as the root layout frames it: the strip's account controls, then the page. */
function app(opts: { signedIn: boolean; organization?: string | null }, page: ReactNode = <ProjectsPage />) {
  return (
    <AuthBootstrap signedIn={opts.signedIn} organization={opts.organization ?? null} navigate={(url) => void went.push(url)}>
      <header>
        <OrganizationSwitcher />
        <SignOutButton />
      </header>
      <main>
        {page}
        <AuthProbe />
      </main>
    </AuthBootstrap>
  );
}

const requests = (path: string) => seen.filter((s) => s.url.split("?")[0] === path);
const buttonNamed = (el: ParentNode, name: string) => [...el.querySelectorAll("button")].find((b) => b.textContent?.trim() === name);
const probe = (el: ParentNode) => el.querySelector("[data-probe]")?.getAttribute("data-probe");
const signInLink = (el: ParentNode) => [...el.querySelectorAll("a")].find((a) => a.textContent === en["auth.signIn"]);

describe("signed out (no session cookie)", () => {
  it("asks nothing of the API and offers one sign-in that returns to this page", async () => {
    nav.pathname = "/projects/prj_01M4YDQK00TA00000000000001";
    const el = await mount(app({ signedIn: false }));
    await settle();
    expect(seen).toEqual([]);
    expect(el.querySelector('[data-problem="unauthenticated"]')?.textContent).toContain(en["problem.heading.unauthenticated"]);
    const links = [...el.querySelectorAll("a")].filter((a) => a.textContent === en["auth.signIn"]);
    expect(links).toHaveLength(1);
    expect(links[0]?.getAttribute("href")).toBe("/auth/login?return_to=%2Fprojects%2Fprj_01M4YDQK00TA00000000000001");
    expect(buttonNamed(el, en["auth.signOut"])).toBeUndefined();
    expect(el.textContent).not.toContain(en["organization.label"]);
    expect(probe(el)).toBe("none none false");
  });

  it("returns to the page with its query, and never to another site", async () => {
    nav.search = "?view=list";
    const el = await mount(app({ signedIn: false }));
    expect(signInLink(el)?.getAttribute("href")).toBe("/auth/login?return_to=%2Fprojects%3Fview%3Dlist");
    nav.pathname = "//evil.example/x";
    nav.search = "";
    const other = await mount(app({ signedIn: false }));
    expect(signInLink(other)?.getAttribute("href")).toBe("/auth/login?return_to=%2F");
  });

  it("is signed out from the server's first paint, never loading; with a session the first paint is loading, never the sign-in", () => {
    const first = (signedIn: boolean) => renderToStaticMarkup(<I18nProvider locale="en">{app({ signedIn })}</I18nProvider>);
    const out = first(false);
    expect(out).toContain(en["problem.heading.unauthenticated"]);
    expect(out).toContain(`>${en["auth.signIn"]}</a>`);
    expect(out).not.toContain(en["loading.account"]);
    const inside = first(true);
    expect(inside).toContain(en["loading.account"]);
    expect(inside).not.toContain(en["problem.heading.unauthenticated"]);
    expect(inside).not.toContain(`>${en["auth.signIn"]}</a>`);
    expect(seen, "rendering asks nothing").toEqual([]);
  });

  it("is in Traditional Chinese when that is the reader's language", async () => {
    const el = await mount(app({ signedIn: false }), "zh-Hant");
    const link = [...el.querySelectorAll("a")].find((a) => a.textContent === zhHant["auth.signIn"]);
    expect(link?.getAttribute("href")).toBe("/auth/login?return_to=%2Fprojects");
  });
});

describe("signed in: /me through the BFF", () => {
  it("shows that it is loading, and calls no tenant API before it knows the organization", async () => {
    let release: () => void = () => {};
    answers["GET /api/v1/me"] = () => new Promise((r) => (release = () => r(meOf([{ organization_id: A, organization_name: "Acme", role: "editor" }])())));
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.querySelector('[role="status"]')?.textContent).toBe(en["loading.account"]);
    expect(requests("/api/v1/projects")).toEqual([]);
    expect(buttonNamed(el, en["auth.signOut"])).toBeUndefined();
    await act(async () => release());
    await settle();
    expect(el.textContent).toContain("Line A");
  });

  it("asks /me without any organization (the API refuses one there), with the CSRF header and no token", async () => {
    answers["GET /api/v1/me"] = meOf([{ organization_id: A, organization_name: "Acme", role: "editor" }]);
    await mount(app({ signedIn: true, organization: B }));
    await settle();
    const [m] = requests("/api/v1/me");
    expect(m?.headers.has("X-Organization-ID")).toBe(false);
    expect(m?.headers.get("X-Geminus-Request")).toBe("1");
    expect(m?.headers.has("Authorization")).toBe(false);
  });

  it("is signed out when the session has ended (a refused refresh is 401): the sign-in, no tenant call, no sign-out", async () => {
    answers["GET /api/v1/me"] = () => error(401, "UNAUTHENTICATED");
    const el = await mount(app({ signedIn: true, organization: A }));
    await settle();
    expect(el.querySelector('[data-problem="unauthenticated"]')).not.toBeNull();
    expect(signInLink(el)?.getAttribute("href")).toBe("/auth/login?return_to=%2Fprojects");
    expect(requests("/api/v1/projects")).toEqual([]);
    expect(buttonNamed(el, en["auth.signOut"])).toBeUndefined();
  });

  it("shows a failure it can retry (the provider down during a refresh is 503), and recovers", async () => {
    let n = 0;
    answers["GET /api/v1/me"] = () => (n++ === 0 ? error(503, "SERVICE_UNAVAILABLE") : meOf([{ organization_id: A, organization_name: "Acme", role: "owner" }])());
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.querySelector('[data-problem="unavailable"]')).not.toBeNull();
    expect(requests("/api/v1/projects")).toEqual([]);
    expect(buttonNamed(el, en["auth.signOut"]), "the session is still there to end").toBeDefined();
    await press(buttonNamed(el, en.retry));
    await settle();
    expect(requests("/api/v1/me")).toHaveLength(2);
    expect(el.textContent).toContain("Line A");
    expect(probe(el)).toBe(`${A} owner true`);
  });

  it("shows the permission state for an identity Geminus does not know (403), with the request ID, and offers sign-out", async () => {
    answers["GET /api/v1/me"] = () => error(403, "FORBIDDEN");
    const el = await mount(app({ signedIn: true, organization: A }));
    await settle();
    const alert = el.querySelector('[data-problem="forbidden"]');
    expect(alert?.textContent).toContain(en["problem.heading.forbidden"]);
    expect(alert?.textContent).toContain(en["access.none"]);
    expect(alert?.textContent).toContain(REQ);
    expect(requests("/api/v1/projects")).toEqual([]);
    expect(signInLink(el)).toBeUndefined();
    expect(buttonNamed(el, en["auth.signOut"])).toBeDefined();
    expect(probe(el)).toBe("none none false");
  });

  it("shows the same permission state for a user without memberships", async () => {
    answers["GET /api/v1/me"] = meOf([]);
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.querySelector('[data-problem="forbidden"]')?.textContent).toContain(en["access.none"]);
    expect(requests("/api/v1/projects")).toEqual([]);
    expect(buttonNamed(el, en["auth.signOut"])).toBeDefined();
  });

  it("chooses the only organization by itself and sends it as X-Organization-ID (D-008: a selector only)", async () => {
    answers["GET /api/v1/me"] = meOf([{ organization_id: A, organization_name: "Acme", role: "editor" }]);
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.textContent).toContain("Line A");
    expect(requests("/api/v1/projects").map((s) => s.headers.get("X-Organization-ID"))).toEqual([A]);
    expect(el.textContent, "no choice to make with one organization").not.toContain(en["organization.label"]);
    expect(buttonNamed(el, en["auth.signOut"])).toBeDefined();
    expect(probe(el)).toBe(`${A} editor true`);
  });

  it.each([
    ["viewer", `${A} viewer false`],
    ["editor", `${A} editor true`],
    ["owner", `${A} owner true`],
    ["admin", `${A} none false`],
  ])("maps the role %s for the pages (an unknown role is read-only)", async (role, want) => {
    answers["GET /api/v1/me"] = meOf([{ organization_id: A, organization_name: "Acme", role }]);
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(probe(el)).toBe(want);
  });
});

describe("several organizations", () => {
  const two = meOf([
    { organization_id: A, organization_name: "Acme", role: "viewer" },
    { organization_id: B, organization_name: "Plant 2", role: "owner" },
  ]);

  it("asks which one, calls no tenant API until then, and remembers the choice for the browser session", async () => {
    answers["GET /api/v1/me"] = two;
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.querySelector("h2")?.textContent).toBe(en["organization.choose.title"]);
    expect(requests("/api/v1/projects")).toEqual([]);
    // One way to choose at a time: the strip's switcher waits until there is a choice.
    expect(el.querySelector("header")?.textContent).not.toContain(en["organization.label"]);
    const choice = buttonNamed(el, "Plant 2");
    expect(choice?.closest("li")?.textContent).toContain(en["role.owner"]);
    expect(buttonNamed(el, "Acme")?.closest("li")?.textContent).toContain(en["role.viewer"]);
    await press(choice);
    await settle();
    expect(el.textContent).toContain("Line B");
    expect(requests("/api/v1/projects").map((s) => s.headers.get("X-Organization-ID"))).toEqual([B]);
    expect(document.cookie).toContain(`geminus_organization=${B}`);
    expect(probe(el)).toBe(`${B} owner true`);
    expect(el.querySelector("header")?.textContent).toContain(en["organization.label"]);
    expect(el.querySelector("header")?.textContent).toContain("Plant 2");
  });

  it("uses the remembered one at once, without asking", async () => {
    answers["GET /api/v1/me"] = two;
    const el = await mount(app({ signedIn: true, organization: B }));
    await settle();
    expect(el.textContent).not.toContain(en["organization.choose.title"]);
    expect(requests("/api/v1/projects").map((s) => s.headers.get("X-Organization-ID"))).toEqual([B]);
  });

  it("asks again when the remembered one is not among the memberships", async () => {
    answers["GET /api/v1/me"] = two;
    const el = await mount(app({ signedIn: true, organization: "org_01M4YDQK00TA00000000000003" }));
    await settle();
    expect(el.querySelector("h2")?.textContent).toBe(en["organization.choose.title"]);
    expect(requests("/api/v1/projects")).toEqual([]);
  });

  it("switches from the strip: the pages load again in the other organization", async () => {
    answers["GET /api/v1/me"] = two;
    const el = await mount(app({ signedIn: true, organization: B }));
    await settle();
    await press(el.querySelector("header button[aria-haspopup=listbox]"));
    await settle();
    await press([...document.querySelectorAll("[role=option]")].find((o) => o.textContent === "Acme"));
    await settle();
    expect(requests("/api/v1/projects").map((s) => s.headers.get("X-Organization-ID"))).toEqual([B, A]);
    expect(el.textContent).toContain("Line A");
    expect(document.cookie).toContain(`geminus_organization=${A}`);
    expect(probe(el)).toBe(`${A} viewer false`);
  });

  it("tells two organizations with the same name apart by their IDs", async () => {
    answers["GET /api/v1/me"] = meOf([
      { organization_id: A, organization_name: "Acme", role: "viewer" },
      { organization_id: B, organization_name: "Acme", role: "owner" },
    ]);
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(buttonNamed(el, `Acme (${A})`)).toBeDefined();
    expect(buttonNamed(el, `Acme (${B})`)).toBeDefined();
  });

  it("says when the list was cut at 100", async () => {
    answers["GET /api/v1/me"] = meOf(
      [
        { organization_id: A, organization_name: "Acme", role: "viewer" },
        { organization_id: B, organization_name: "Plant 2", role: "owner" },
      ],
      true,
    );
    const el = await mount(app({ signedIn: true }));
    await settle();
    expect(el.querySelector('[role="note"]')?.textContent).toBe("Showing the first 2 organizations.");
  });
});

describe("sign-out", () => {
  beforeEach(() => {
    answers["GET /api/v1/me"] = meOf([{ organization_id: A, organization_name: "Acme", role: "editor" }]);
  });

  it("posts to /auth/logout, forgets the organization and goes to the provider's logout", async () => {
    answers["POST /auth/logout"] = () => json(200, { redirect: "https://id.example/v2/logout" });
    document.cookie = `geminus_organization=${A}; Path=/`;
    const el = await mount(app({ signedIn: true, organization: A }));
    await settle();
    await press(buttonNamed(el, en["auth.signOut"]));
    await settle();
    const [out] = requests("/auth/logout");
    expect(out?.method).toBe("POST");
    expect(out?.headers.get("X-Geminus-Request")).toBe("1");
    expect(went).toEqual(["https://id.example/v2/logout"]);
    expect(document.cookie).not.toContain("geminus_organization");
  });

  it("says so when it did not complete, and stays signed in", async () => {
    answers["POST /auth/logout"] = () => error(503, "SERVICE_UNAVAILABLE");
    const el = await mount(app({ signedIn: true, organization: A }));
    await settle();
    await press(buttonNamed(el, en["auth.signOut"]));
    await settle();
    expect(el.querySelector("header [role=alert]")?.textContent).toBe(en["auth.signOutFailed"]);
    expect(went).toEqual([]);
    expect(el.textContent).toContain("Line A");
    expect(buttonNamed(el, en["auth.signOut"])).toBeDefined();
  });
});
