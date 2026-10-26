// @vitest-environment happy-dom
// T-045 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): rules a rendered
// view can be checked against. One primary action per view, at most; no
// control that does nothing (no disabled-forever placeholder); and every
// link in a view goes somewhere no other link in it goes, except the pairs
// the owner is asked about. The browser checks the routes themselves
// (apps/web-e2e/tests/simplicity.spec.ts); this covers the components and
// states the routes do not show signed out.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { CrashNotice } from "../app/_components/crash";
import { DraftStatus, RemoteView } from "../app/_components/states";
import { networkProblem } from "../lib/api/problem";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Draft } from "../lib/contracts";
import { openDraft, reduceDraft } from "../lib/state/draft";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { mount, settle } from "./ui-harness";

vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ refresh: () => {} }) }));
vi.mock("next/headers", () => ({ cookies: async () => ({ get: () => undefined }) }));
vi.setConfig({ testTimeout: 180_000 });

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const draft = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as Draft;
const primaries = (el: ParentNode) => el.querySelectorAll('[data-variant="primary"]').length;

/** Each draft state an editor can see. */
function draftStates() {
  const edit = { op: "replace", path: "/name", value: "x" } as const;
  const clean = openDraft({ ...draft, revision: 3 });
  const dirty = reduceDraft(clean, { type: "edit", ops: [edit] });
  const saving = reduceDraft(dirty, { type: "saveStarted", revision: 3, ops: [edit] });
  const conflict = reduceDraft(saving, { type: "saveFailed", problem: { ...networkProblem(""), kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409 } });
  const error = reduceDraft(saving, { type: "saveFailed", problem: networkProblem("offline") });
  return { clean, dirty, saving, conflict, error };
}

/** The hrefs that two or more links in the markup share. */
function sharedHrefs(html: string): string[] {
  const hrefs = [...html.matchAll(/<a [^>]*href="([^"]+)"/g)].map((m) => m[1]!).filter((h) => !h.startsWith("#"));
  return [...new Set(hrefs.filter((h, i) => hrefs.indexOf(h) !== i))];
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
      expect(el.querySelectorAll("[disabled], [aria-disabled=true], [data-disabled]").length, name).toBe(0);
    }
  });
});

describe("no two links to the same place in a view (H-027)", () => {
  // Kept until the owner decides (report, T-045 re-skin): the start page's
  // "Go to projects" goes where the strip's "Projects" goes. Tests of
  // T-044 assert it (layout.test.tsx, apps/web-e2e keyboard.spec.ts).
  const askedAbout = new Map([["/", ["/projects"]]]);

  it.each([
    ["/", async () => (await import("../app/page")).default()],
    ["not found", async () => (await import("../app/not-found")).default()],
    ["sign-in error", async () => (await import("../app/auth/error/page")).default({ searchParams: Promise.resolve({ reason: "expired" }) })],
  ] as const)("holds on %s, inside the app shell", async (view, page) => {
    const { default: RootLayout } = await import("../app/layout");
    const html = renderToStaticMarkup(await RootLayout({ children: await page() }));
    expect(sharedHrefs(html)).toEqual(askedAbout.get(view) ?? []);
  });
});
