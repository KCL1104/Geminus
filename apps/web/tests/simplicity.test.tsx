// @vitest-environment happy-dom
// T-045 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): rules a rendered
// view can be checked against. One primary action per view, at most; no
// control that does nothing (no disabled-forever placeholder); and no two
// controls for one action: no two buttons (or links) with the same name,
// and no two links to the same place, except the pair the owner is asked
// about. The browser checks the routes as they render signed out
// (apps/web-e2e/tests/simplicity.spec.ts); the app mounts no session yet
// (app/layout.tsx), so the signed-in views (the project list, a project,
// a model, every draft state and a failed load) are checked here, with a
// fake API and session, each together with the app shell it renders in.
import { act, type ReactNode, Suspense } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CrashNotice } from "../app/_components/crash";
import { DraftStatus, type Loadable, RemoteView } from "../app/_components/states";
import { createApiClient } from "../lib/api/client";
import { draftETag } from "../lib/api/etag";
import { networkProblem } from "../lib/api/problem";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Draft, Model } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { openDraft, reduceDraft } from "../lib/state/draft";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { mount, settle } from "./ui-harness";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ refresh: () => {} }) }));
vi.mock("next/headers", () => ({
  cookies: async () => ({ get: (name: string) => (name === "geminus_locale" && jar.locale !== undefined ? { name, value: jar.locale } : undefined) }),
}));
vi.setConfig({ testTimeout: 180_000 });
beforeEach(() => {
  jar.locale = undefined;
});

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const PRJ = "prj_01M4YDQK00TA00000000000001";
const USR = "usr_01M4YDQK00TA00000000000001";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const viewer: Auth = { session: { organizationId: ORG }, role: "viewer" };
const draft = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as Draft;
const catalogs = { en, "zh-Hant": zhHant } as const;
const locales = Object.keys(catalogs) as Locale[];
const primaries = (el: ParentNode) => el.querySelectorAll('[data-variant="primary"]').length;
const disabled = (el: ParentNode) => el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]").length;
/** Blocked controls whose reason is not text on the page that aria-describedby names (DESIGN.md Buttons, Disabled). */
const unexplained = (el: ParentNode) =>
  [...el.querySelectorAll("[aria-disabled=true]")]
    .filter((b) => {
      const ids = (b.getAttribute("aria-describedby") ?? "").split(/\s+/).filter(Boolean);
      return !(b instanceof HTMLButtonElement) || ids.length === 0 || ids.some((id) => !(document.getElementById(id)?.textContent ?? "").trim());
    })
    .map((b) => b.textContent ?? "");

/** Each draft state an editor can see. */
function draftStates() {
  const edit = { op: "replace", path: "/name", value: "x" } as const;
  const clean = openDraft({ ...draft, revision: 3 });
  const dirty = reduceDraft(clean, { type: "edit", ops: [edit] });
  const saving = reduceDraft(dirty, { type: "saveStarted", revision: 3, ops: [edit] });
  const conflict = reduceDraft(saving, { type: "saveFailed", problem: { ...networkProblem(""), kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409 } });
  const error = reduceDraft(saving, { type: "saveFailed", problem: networkProblem("offline") });
  const outOfDate = openDraft({ ...draft, revision: 3, out_of_date: true });
  return { clean, dirty, saving, conflict, error, "out of date": outOfDate };
}

type Control = { role: "button" | "link"; name: string; href: string | null };

/** The accessible name, as far as these views need it: aria-labelledby, aria-label, else the text. */
function accessibleName(el: Element): string {
  const ids = el.getAttribute("aria-labelledby");
  const name = ids
    ? ids.split(/\s+/).map((id) => el.ownerDocument.getElementById(id)?.textContent ?? "").join(" ")
    : (el.getAttribute("aria-label") ?? el.textContent ?? "");
  return name.replace(/\s+/g, " ").trim();
}

/** The buttons and links a person or a screen reader meets in a rendered view. */
function controls(root: ParentNode): Control[] {
  const out: Control[] = [];
  for (const el of root.querySelectorAll("a[href], [role=link], button, [role=button]")) {
    if (el.closest("[hidden], [aria-hidden=true], [inert]")) continue;
    const role = el.getAttribute("role") ?? (el.tagName === "A" ? "link" : "button");
    if (role !== "button" && role !== "link") continue;
    const href = el.getAttribute("href");
    out.push({ role, name: accessibleName(el), href: href === null || href.startsWith("#") ? null : href });
  }
  return out;
}

/** What two controls of one view share: a role and a name, or (links) where they go. */
function duplicates(cs: Control[]): string[] {
  const keys = [...cs.map((c) => `${c.role} "${c.name}"`), ...cs.flatMap((c) => (c.href === null ? [] : [`link to ${c.href}`]))];
  return [...new Set(keys.filter((k, i) => keys.indexOf(k) !== i))].sort();
}

/** The app shell (skip link, strip, navigation, language, theme) around a page, rendered by the root layout. */
async function shell(locale: Locale, children: ReactNode = null): Promise<Document> {
  jar.locale = locale;
  const { default: RootLayout } = await import("../app/layout");
  const html = renderToStaticMarkup(await RootLayout({ children }));
  return new DOMParser().parseFromString(`<!doctype html>${html}`, "text/html");
}

/** The controls of a view: the shell's and the page's, which a reader meets together. */
async function view(locale: Locale, page: HTMLElement): Promise<Control[]> {
  const frame = controls(await shell(locale));
  expect(frame.map((c) => c.role), "the shell's controls were found").toContain("link");
  return [...frame, ...controls(page)];
}

describe("one primary action per view (H-027)", () => {
  it("holds in the component gallery, which shows every variant once", async () => {
    const { Gallery } = await import("../app/ui-gallery/gallery");
    const el = await mount(<Gallery />);
    await settle();
    expect(primaries(el)).toBe(1);
  });

  it.each(Object.entries(draftStates()))("holds for a draft that is %s", async (_, session) => {
    const el = await mount(
      <AuthProvider value={editor}>
        <DraftStatus session={session} onReload={async () => null} onDiscard={() => {}} />
      </AuthProvider>,
    );
    expect(primaries(el)).toBeLessThanOrEqual(1);
  });

  it("holds for a failed load and a crashed page, whose retry is the only action and not primary", async () => {
    const failed = await mount(<RemoteView remote={{ state: "failed", problem: networkProblem("") }} what="projects" onRetry={() => {}}>{() => null}</RemoteView>);
    const crashed = await mount(<CrashNotice error={new Error("x")} retry={() => {}} />);
    for (const el of [failed, crashed]) {
      expect(el.querySelectorAll("button").length).toBe(1);
      expect(primaries(el)).toBe(0);
    }
  });
});

describe("no control that does nothing (H-027)", () => {
  it("disables nothing in a product state: a disabled control appears only in the gallery", async () => {
    for (const [name, session] of Object.entries(draftStates())) {
      const el = await mount(
        <AuthProvider value={editor}>
          <DraftStatus session={session} onReload={async () => null} onDiscard={() => {}} />
        </AuthProvider>,
      );
      expect(disabled(el), name).toBe(0);
    }
  });
});

describe("no two controls for one action in a view (H-027)", () => {
  // Kept until the owner decides (report, T-045 re-skin; H-028 kept it): the
  // start page's "Go to projects" goes where the strip's "Projects" goes.
  // Tests of T-044 assert it (layout.test.tsx, apps/web-e2e keyboard.spec.ts).
  const askedAbout = new Map([["/", ["link to /projects"]]]);

  it("finds a repeated name or destination, so the rule can fail", () => {
    const twice = (role: Control["role"], name: string, href: string | null = null) => [{ role, name, href }, { role, name, href }];
    expect(duplicates(twice("button", "Discard my changes"))).toEqual(['button "Discard my changes"']);
    expect(duplicates([{ role: "link", name: "M1", href: "/models/a" }, { role: "link", name: "v1", href: "/models/a" }])).toEqual(["link to /models/a"]);
    expect(duplicates([{ role: "button", name: "Details", href: null }, { role: "link", name: "Details", href: "/x" }])).toEqual([]);
  });

  for (const locale of locales) {
    describe(locale, () => {
      it.each([
        ["/", async () => (await import("../app/page")).default()],
        ["not found", async () => (await import("../app/not-found")).default()],
        ["sign-in error", async () => (await import("../app/auth/error/page")).default({ searchParams: Promise.resolve({ reason: "expired" }) })],
      ] as const)("holds on %s, inside the app shell", async (route, page) => {
        jar.locale = locale;
        const doc = await shell(locale, await page());
        expect(duplicates(controls(doc))).toEqual(askedAbout.get(route) ?? []);
      });

      it.each(Object.entries(draftStates()))("holds for a draft that is %s, inside the app shell", async (state, session) => {
        const el = await mount(
          <AuthProvider value={editor}>
            <DraftStatus session={session} onReload={async () => networkProblem("offline")} onDiscard={() => {}} />
          </AuthProvider>,
          locale,
        );
        const c = await view(locale, el);
        // The choices a conflict or a failed save offers are really there.
        if (state === "conflict") expect(c.map((x) => x.name)).toEqual(expect.arrayContaining([catalogs[locale]["draft.reload"], catalogs[locale]["draft.discard"]]));
        if (state === "error") expect(c.map((x) => x.name)).toContain(catalogs[locale]["draft.discard"]);
        expect(duplicates(c)).toEqual([]);
      });

      it("holds for a conflict whose reload failed: two problems, still one of each choice", async () => {
        const el = await mount(
          <AuthProvider value={editor}>
            <DraftStatus session={draftStates().conflict} onReload={async () => networkProblem("offline")} onDiscard={() => {}} />
          </AuthProvider>,
          locale,
        );
        const reload = [...el.querySelectorAll("button")].find((b) => b.textContent === catalogs[locale]["draft.reload"]);
        await act(async () => reload!.click());
        await settle();
        expect(el.querySelectorAll('[role="alert"]').length).toBe(2);
        expect(duplicates(await view(locale, el))).toEqual([]);
      });

      it.each(["projects", "models", "model"] as const)("holds for a failed load of %s, whose retry is its one button", async (what: Loadable) => {
        const el = await mount(
          <RemoteView remote={{ state: "failed", problem: networkProblem("") }} what={what} onRetry={() => {}}>{() => null}</RemoteView>,
          locale,
        );
        const c = await view(locale, el);
        expect(c.filter((x) => x.name === catalogs[locale].retry)).toHaveLength(1);
        expect(duplicates(c)).toEqual([]);
      });
    });
  }
});

// The signed-in pages, rendered as the routes render them, with a fake API
// behind the app's own client and a session from AuthProvider (as
// dom.test.tsx and ui.test.tsx do): the project list, a project's models
// and a model with its draft, loaded and failed.
describe("the signed-in pages keep only the controls they need (H-027)", () => {
  const model = (n: number, outOfDate = false): Model => ({
    id: n === 1 ? draft.model_id : `mdl_01M4YDQK00TA0000000000000${n}`, organization_id: ORG, project_id: PRJ, domain: "software_delivery", name: `Line ${n}`,
    draft: { revision: draft.revision, base_version_id: draft.base_version_id, out_of_date: outOfDate },
    latest_version: { id: draft.base_version_id, version: n }, created_at: "2026-10-15T08:00:00Z",
  });
  const project = (n: number) => ({ id: `prj_01M4YDQK00TA0000000000000${n}`, organization_id: ORG, name: `Plant ${n}`, created_at: "2026-10-15T00:00:00Z", created_by: USR });
  const json = (body: unknown, headers: Record<string, string> = {}) => new Response(JSON.stringify(body), { status: 200, headers });
  const unavailable = () => new Response(JSON.stringify({ error: { code: "SERVICE_UNAVAILABLE", message: "down", request_id: "req_01M4YDQK00V6H8S0ST4F7510KR" } }), { status: 503 });

  /** A client whose fetch answers each API path from the table, and fails the test on any other. */
  function api(answers: Record<string, () => Response>) {
    return createApiClient({
      session: () => editor.session,
      fetch: (async (input: RequestInfo | URL) => {
        const path = new URL(String(input), "http://app.test").pathname;
        const answer = answers[path];
        if (!answer) throw new Error(`no fake answer for ${path}`);
        return answer();
      }) as typeof fetch,
    });
  }

  const routes: Record<string, () => Promise<ReactNode>> = {
    "/projects": async () => {
      const { default: ProjectsPage } = await import("../app/projects/page");
      return <ProjectsPage />;
    },
    [`/projects/${PRJ}`]: async () => {
      const { default: ProjectPage } = await import("../app/projects/[projectId]/page");
      return <ProjectPage params={Promise.resolve({ projectId: PRJ })} />;
    },
    [`/models/${draft.model_id}`]: async () => {
      const { default: ModelPage } = await import("../app/models/[modelId]/page");
      return <ModelPage params={Promise.resolve({ modelId: draft.model_id })} />;
    },
  };
  const loaded: Record<string, () => Response> = {
    "/api/v1/projects": () => json({ items: [project(1), project(2)], next_cursor: "cursor-2" }),
    [`/api/v1/projects/${PRJ}/models`]: () => json({ items: [model(1), model(2, true)], next_cursor: "cursor-2" }),
    [`/api/v1/models/${draft.model_id}`]: () => json(model(1)),
    [`/api/v1/models/${draft.model_id}/draft`]: () => json(draft, { ETag: draftETag(draft.revision) }),
  };
  const failing = Object.fromEntries(Object.keys(loaded).map((p) => [p, unavailable]));

  async function render(route: string, auth: Auth, answers: Record<string, () => Response>, locale: Locale) {
    const page = routes[route];
    if (!page) throw new Error(`no route ${route}`);
    const el = await mount(
      <AuthProvider value={auth}>
        <ApiProvider client={api(answers)}>
          <Suspense fallback={null}>{await page()}</Suspense>
        </ApiProvider>
      </AuthProvider>,
      locale,
    );
    await settle();
    return el;
  }

  for (const locale of locales) {
    const t = catalogs[locale];
    it.each(Object.keys(routes))(`${locale}: %s, loaded, inside the app shell`, async (route) => {
      for (const auth of [editor, viewer]) {
        const el = await render(route, auth, loaded, locale);
        expect(el.querySelector("h1"), "the page rendered").not.toBeNull();
        expect(el.textContent, "signed in").not.toContain(t["problem.heading.unauthenticated"]);
        const c = await view(locale, el);
        // What each page offers is really there, so the rule is not vacuous.
        const links = c.filter((x) => x.role === "link").map((x) => x.href);
        if (route === "/projects") expect(links).toEqual(expect.arrayContaining([`/projects/${project(1).id}`, `/projects/${project(2).id}`]));
        if (route.startsWith("/projects/")) expect(links).toEqual(expect.arrayContaining([`/models/${model(1).id}`, `/models/${model(2).id}`]));
        if (route.startsWith("/models/")) {
          expect(links).toContain(`/projects/${PRJ}`);
          expect(el.querySelector("[data-draft-status]"), "the draft status").not.toBeNull();
        }
        expect(duplicates(c), `${route} as ${auth.role}`).toEqual([]);
        expect(primaries(el), "primary actions").toBeLessThanOrEqual(1);
        if (route.startsWith("/models/")) {
          // The 2D editor (T-015) blocks Save draft while nothing is unsaved
          // and Publish while the draft cannot be published: each stays
          // focusable as aria-disabled with its reason on screen beside it
          // (DESIGN.md Buttons, Disabled; the editor's own rule in
          // editor.test.tsx and the browser's in simplicity.spec.ts). Nothing
          // is disabled outright, and nothing is blocked without saying why.
          expect(el.querySelectorAll("[disabled], [data-disabled]").length, "disabled controls").toBe(0);
          if (auth === editor) expect(el.querySelectorAll("[aria-disabled=true]").length, "the editor's blocked actions are there to check").toBeGreaterThan(0);
          expect(unexplained(el), "blocked controls without their reason on screen").toEqual([]);
        } else expect(disabled(el), "disabled controls").toBe(0);
      }
    });

    it.each(Object.keys(routes))(`${locale}: %s, failed to load, inside the app shell`, async (route) => {
      const el = await render(route, editor, failing, locale);
      expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("unavailable");
      const c = await view(locale, el);
      expect(c.filter((x) => x.name === t.retry)).toHaveLength(1);
      expect(duplicates(c)).toEqual([]);
      expect(primaries(el)).toBe(0);
      expect(disabled(el)).toBe(0);
    });
  }
});
