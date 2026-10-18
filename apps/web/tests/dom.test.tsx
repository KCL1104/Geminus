// @vitest-environment happy-dom
// Review of T-014: behaviour that needs effects to run, so it is rendered in
// a DOM (happy-dom) rather than with renderToStaticMarkup, which never runs
// effects and so could not see an API call or a load that never finishes.
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import ProjectsPage from "../app/projects/page";
import { DraftStatus, RemoteView } from "../app/_components/states";
import { useRemote } from "../app/_components/use-remote";
import { createApiClient } from "../lib/api/client";
import { networkProblem } from "../lib/api/problem";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { Draft } from "../lib/contracts";
import { openDraft, reduceDraft } from "../lib/state/draft";
import { en } from "../messages/en";
import { readFileSync } from "node:fs";
import { join } from "node:path";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const PRJ = "prj_01M4YDQK00TA00000000000001";
const signedIn: Auth = { session: { accessToken: "t", organizationId: ORG }, role: "editor" };
const signedOut: Auth = { session: null, role: null };
// import.meta.url is not a file URL under happy-dom; vitest runs from apps/web.
const draft = JSON.parse(readFileSync(join(process.cwd(), "../../testdata/contracts/valid-draft-with-issues.json"), "utf8")) as Draft;

let roots: Root[] = [];
afterEach(() => {
  for (const r of roots) act(() => r.unmount());
  roots = [];
  document.body.innerHTML = "";
});

async function mount(node: ReactNode): Promise<HTMLElement> {
  const el = document.createElement("div");
  document.body.append(el);
  const root = createRoot(el);
  roots.push(root);
  await act(async () => root.render(node));
  return el;
}

/** Lets pending fetches and effects finish. */
const settle = () => act(async () => { for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0)); });

const page = (items: { id: string; name: string }[], next_cursor: string | null = null) =>
  new Response(JSON.stringify({ items: items.map((p) => ({ ...p, organization_id: ORG, created_at: "2026-10-15T00:00:00Z", created_by: "usr_01M4YDQK00TA00000000000001" })), next_cursor }));

function projects(auth: Auth, respond: () => Response) {
  let calls = 0;
  // The client always has a session, so only the page's auth boundary can keep it from calling.
  const api = createApiClient({
    baseUrl: "https://api.test",
    session: () => signedIn.session,
    fetch: (async () => { calls++; return respond(); }) as typeof fetch,
  });
  return { calls: () => calls, node: <AuthProvider value={auth}><ApiProvider client={api}><ProjectsPage /></ApiProvider></AuthProvider> };
}

describe("auth boundary (effects run)", () => {
  it("does not call the API when signed out, and does when signed in", async () => {
    const out = projects(signedOut, () => page([]));
    const el = await mount(out.node);
    await settle();
    expect(out.calls()).toBe(0);
    expect(el.textContent).toContain(en["problem.heading.unauthenticated"]);

    const inside = projects(signedIn, () => page([{ id: PRJ, name: "Pipeline" }]));
    const el2 = await mount(inside.node);
    await settle();
    expect(inside.calls()).toBe(1);
    expect(el2.textContent).toContain("Pipeline");
  });
});

describe("loads that fail", () => {
  it("shows a body that breaks off as a failure with a retry, not as loading forever", async () => {
    let n = 0;
    const out = projects(signedIn, () =>
      n++ === 0
        ? new Response(new ReadableStream({ start: (c) => c.error(new TypeError("terminated")) }))
        : page([{ id: PRJ, name: "Pipeline" }]),
    );
    const el = await mount(out.node);
    await settle();
    expect(el.textContent).not.toContain(en["loading.projects"]);
    expect(el.querySelector('[role="alert"]')?.getAttribute("data-problem")).toBe("network");
    const retry = [...el.querySelectorAll("button")].find((b) => b.textContent === en.retry);
    expect(retry).toBeDefined();
    await act(async () => retry!.click());
    await settle();
    expect([out.calls(), el.textContent?.includes("Pipeline")]).toEqual([2, true]);
  });

  it("shows a load that rejects as a failure", async () => {
    function Rejecting() {
      const [remote] = useRemote<string>(() => Promise.reject(new Error("bug")), "x");
      return <RemoteView remote={remote} what="model">{(d) => <p>{d}</p>}</RemoteView>;
    }
    const el = await mount(<Rejecting />);
    await settle();
    expect(el.textContent).not.toContain(en["loading.model"]);
    expect(el.querySelector('[role="alert"]')).not.toBeNull();
  });

  it("says when a list has more pages than it shows", async () => {
    const out = projects(signedIn, () => page([{ id: PRJ, name: "Pipeline" }], "cursor-2"));
    const el = await mount(out.node);
    await settle();
    expect(el.querySelector('[role="note"]')?.textContent).toBe("Showing the first project. More exist; this build cannot page through them yet.");
  });
});

describe("draft conflict", () => {
  it("shows a reload that failed instead of swallowing it", async () => {
    const edit = { op: "replace", path: "/name", value: "x" } as const;
    let s = reduceDraft(openDraft({ ...draft, revision: 3 }), { type: "edit", ops: [edit] });
    s = reduceDraft(s, { type: "saveStarted", revision: 3, ops: [edit] });
    s = reduceDraft(s, { type: "saveFailed", problem: { ...networkProblem(""), kind: "conflict", code: "MODEL_VERSION_CONFLICT", status: 409 } });
    const el = await mount(
      <AuthProvider value={signedIn}>
        <DraftStatus session={s} onReload={async () => networkProblem("offline")} onDiscard={() => {}} />
      </AuthProvider>,
    );
    const before = el.querySelectorAll('[role="alert"]').length;
    const reload = [...el.querySelectorAll("button")].find((b) => b.textContent === en["draft.reload"])!;
    await act(async () => reload.click());
    await settle();
    expect(el.querySelectorAll('[role="alert"]').length).toBe(before + 1);
    expect(el.textContent).toContain(en["problem.heading.network"]);
  });
});
