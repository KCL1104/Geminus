// @vitest-environment happy-dom
// H-042: the rail (the brand, a model's four sections, the account at the
// foot), the account and settings menu, the model's list pages; H-043: the
// proposal page names the Meta models that wrote and read it.
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AuthBootstrap } from "../app/_components/auth-bootstrap";
import { AppRail, RailModel, RailProvider } from "../app/_components/rail";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { ProposalsPage, RunsPage, ScenariosPage } from "../features/models/model-lists";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { Profile } from "../lib/auth/profile";
import { en } from "../messages/en";
import { createProposalStandIn } from "../app/ui-gallery/proposal/stand-in";
import { boardBaselineRun, boardScenarioRun } from "../features/results/fixtures/board-line";
import { mount, press, settle, violations } from "./ui-harness";

const nav = vi.hoisted(() => ({ pathname: "/projects" }));
vi.mock("next/navigation", async (orig) => ({
  ...(await orig<typeof import("next/navigation")>()),
  usePathname: () => nav.pathname,
  useSearchParams: () => new URLSearchParams(),
  useRouter: () => ({ refresh: () => {}, push: () => {} }),
}));

const ORG = "org_01M4YDQK004J6EG1BW3SRYPDGD";
const MODEL = "mdl_01M4YDQK00TA0000000000H042";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });

let answers: Record<string, () => Response> = {};
beforeEach(() => {
  nav.pathname = "/projects";
  answers = { "GET /api/v1/me": () => json(200, { user_id: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", memberships: [{ organization_id: ORG, organization_name: "Line A", role: "editor" }], truncated: false }) };
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    const answer = answers[`${init?.method ?? "GET"} ${url.pathname}`];
    if (!answer) throw new Error(`no fake answer for ${url.pathname}`);
    return answer();
  }) as typeof fetch;
});

function shell(signedIn: boolean, profile: Profile | null, page: ReactNode = null) {
  return (
    <AuthBootstrap signedIn={signedIn} organization={null} navigate={() => {}}>
      <RailProvider>
        <AppRail initialTheme="system" profile={profile} />
        <main>{page}</main>
      </RailProvider>
    </AuthBootstrap>
  );
}

const rail = (el: Element) => el.querySelector("nav")!;
const links = (el: Element) => [...rail(el).querySelectorAll("a")].map((a) => [a.getAttribute("aria-label"), a.getAttribute("href"), a.getAttribute("aria-current")]);
const menuButton = (el: Element) => rail(el).querySelector<HTMLButtonElement>("button")!;
const dialog = () => document.querySelector("[role=dialog]");

describe("the rail (H-042)", () => {
  it("signed out: the brand to the projects and the settings with only the language and the theme", async () => {
    const el = await mount(shell(false, null));
    await settle();
    expect(rail(el).getAttribute("aria-label")).toBe(en["rail.label"]);
    expect(links(el)).toEqual([[en["nav.projects"], "/projects", null]]);
    expect(menuButton(el).getAttribute("aria-label")).toBe(en["settings.open"]);
    await press(menuButton(el));
    await settle();
    const d = dialog()!;
    expect([...d.querySelectorAll("[role=radiogroup]")].map((g) => g.getAttribute("aria-label"))).toEqual([en["locale.label"], en["theme.label"]]);
    expect(d.textContent).not.toContain(en["auth.signOut"]);
    expect(await violations()).toEqual([]);
  });

  it("inside a model: its four sections, the page's own marked, and the account with who signed in", async () => {
    nav.pathname = `/models/${MODEL}/runs`;
    const el = await mount(shell(true, { name: "Judge One", email: "judge@geminus-demo.invalid" }));
    await settle();
    expect(links(el)).toEqual([
      [en["nav.projects"], "/projects", null],
      [en["rail.canvas"], `/models/${MODEL}`, null],
      [en["rail.scenarios"], `/models/${MODEL}/scenarios`, null],
      [en["rail.proposals"], `/models/${MODEL}/proposals`, null],
      [en["rail.runs"], `/models/${MODEL}/runs`, "page"],
    ]);
    const avatar = menuButton(el);
    expect(avatar.getAttribute("aria-label")).toBe(en["account.open"]);
    expect(avatar.textContent).toBe("JO");
    await press(avatar);
    await settle();
    const d = dialog()!;
    expect(d.textContent).toContain("Judge One");
    expect(d.textContent).toContain("judge@geminus-demo.invalid");
    expect(d.textContent).toContain(`Line A · ${en["role.editor"]}`);
    expect([...d.querySelectorAll("button")].map((b) => b.textContent)).toContain(en["auth.signOut"]);
    expect(await violations()).toEqual([]);
  });

  it("a page outside /models names its model, and the rail marks the section it belongs to", async () => {
    nav.pathname = "/proposals/aip_01M4YDQK00TA0000000000H042";
    const el = await mount(shell(true, null, <RailModel id={MODEL} />));
    await settle();
    expect(links(el).find(([, href]) => href === `/models/${MODEL}/proposals`)?.[2]).toBe("page");
    // Without a profile the avatar shows the organization's initials.
    expect(menuButton(el).textContent).toBe("LA");
  });

  // Review L2: a word's first character, not its first UTF-16 unit.
  it("takes whole characters for the initials", async () => {
    nav.pathname = "/projects";
    const el = await mount(shell(true, { name: "\u{1F600} Ann", email: null }));
    await settle();
    expect(menuButton(el).textContent).toBe("\u{1F600}A");
  });

  it("offers no section on a page that is not inside a model", async () => {
    nav.pathname = "/projects/prj_01M4YDQK00TA00000000000001";
    const el = await mount(shell(true, null));
    await settle();
    expect(links(el)).toHaveLength(1);
  });

  it("is named in the reader's language", async () => {
    const el = await mount(shell(false, null), "zh-Hant");
    await settle();
    expect(rail(el).getAttribute("aria-label")).toBe("工作區");
    expect(menuButton(el).getAttribute("aria-label")).toBe("設定");
  });
});

describe("the model's list pages (H-042)", () => {
  const model = { id: MODEL, organization_id: ORG, project_id: "prj_01M4YDQK00TA00000000000001", domain: "electronics_line", name: "Board line",
    draft: { revision: 1, base_version_id: "mv_01M4YDQK00TA0000000000H042", out_of_date: false }, latest_version: { id: "mv_01M4YDQK00TA0000000000H042", version: 1 },
    created_at: "2026-10-29T08:00:00Z" };
  const client = () => createApiClient({ session: () => ({ organizationId: ORG }), fetch: globalThis.fetch });
  const page = (node: ReactNode) => mount(<AuthProvider value={{ session: { organizationId: ORG }, role: "editor" }}><ApiProvider client={client()}>{node}</ApiProvider></AuthProvider>);

  it("lists the AI's proposals in the server's order (oldest first), each with its short state, kind and time", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    const base = { organization_id: ORG, model_id: MODEL, base_version_id: model.latest_version.id, created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", requires_confirmation: true };
    answers[`GET /api/v1/models/${MODEL}/ai-proposals`] = () => json(200, { items: [
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A1", mode: "scenario", status: "ready", summary: "A third ICT tester.", created_at: "2026-10-29T09:00:00Z" },
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A2", mode: "edit", status: "generating", created_at: "2026-10-29T08:30:00Z" },
    ], next_cursor: null });
    const el = await page(<ProposalsPage modelId={MODEL} />);
    await settle();
    expect(el.querySelector("h1")?.textContent).toBe(en["proposals.title"]);
    const rows = [...el.querySelectorAll("li")];
    expect(rows.map((r) => r.querySelector("a")?.getAttribute("href"))).toEqual(["/proposals/aip_01M4YDQK00TA0000000000H0A1", "/proposals/aip_01M4YDQK00TA0000000000H0A2"]);
    expect(rows[0]!.textContent).toContain("A third ICT tester.");
    expect(rows[0]!.textContent).toContain(en["proposals.state.ready"]);
    expect(rows[0]!.textContent).toContain(en["proposals.mode.scenario"]);
    expect(rows[1]!.textContent).toContain(en["proposals.noSummary"]);
    expect(rows[1]!.textContent).toContain(en["proposals.state.generating"]);
    // The model is named, not linked: the rail's Canvas is the way back.
    expect(el.textContent).toContain("Board line");
    expect(el.querySelector(`a[href="/models/${MODEL}"]`)).toBeNull();
    expect(await violations()).toEqual([]);
  });

  // H-052: the first look is the proposals to review; the ones that did not
  // work are counted and folded away below them, still one click from open.
  it("folds the failed and refused proposals away below the others, counted, and closed", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    const base = { organization_id: ORG, model_id: MODEL, base_version_id: model.latest_version.id, created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", requires_confirmation: true, created_at: "2026-10-29T09:00:00Z" };
    answers[`GET /api/v1/models/${MODEL}/ai-proposals`] = () => json(200, { items: [
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A1", mode: "scenario", status: "failed" },
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A2", mode: "scenario", status: "approved", summary: "A third ICT tester." },
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A3", mode: "edit", status: "invalid", summary: "Bad patch." },
    ], next_cursor: null });
    const el = await page(<ProposalsPage modelId={MODEL} />);
    await settle();
    const hrefs = (root: Element) => [...root.querySelectorAll(":scope > ul > li > a")].map((a) => a.getAttribute("href"));
    const folded = el.querySelector("details")!;
    expect(folded.open).toBe(false);
    expect(folded.querySelector("summary")?.textContent).toBe("2 proposals that did not work");
    expect(hrefs(folded)).toEqual(["/proposals/aip_01M4YDQK00TA0000000000H0A1", "/proposals/aip_01M4YDQK00TA0000000000H0A3"]);
    expect([...el.querySelectorAll("li a")].map((a) => a.getAttribute("href"))[0]).toBe("/proposals/aip_01M4YDQK00TA0000000000H0A2");
    expect(el.textContent).not.toContain(en["proposals.empty"]);
    expect(await violations()).toEqual([]);
  });

  it("with only proposals that did not work, folds them all and never says there are none", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    const base = { organization_id: ORG, model_id: MODEL, base_version_id: model.latest_version.id, created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", requires_confirmation: true, created_at: "2026-10-29T09:00:00Z" };
    answers[`GET /api/v1/models/${MODEL}/ai-proposals`] = () => json(200, { items: [
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A1", mode: "scenario", status: "failed" },
    ], next_cursor: null });
    const el = await page(<ProposalsPage modelId={MODEL} />);
    await settle();
    expect(el.querySelector("details summary")?.textContent).toBe("1 proposal that did not work");
    expect(el.querySelectorAll("li")).toHaveLength(1);
    expect(el.textContent).not.toContain(en["proposals.empty"]);
  });

  // Review M3: the summary is the AI's prose, which the server does not keep
  // to visible characters; in the list it is shown as on the proposal page.
  it("shows a summary's hidden or reordering characters, isolated from the row, and says when more proposals or runs exist", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    const base = { organization_id: ORG, model_id: MODEL, base_version_id: model.latest_version.id, created_by: "usr_01M4YDQK0016KTK4JM6KAHZ6BR", requires_confirmation: true, created_at: "2026-10-29T09:00:00Z" };
    answers[`GET /api/v1/models/${MODEL}/ai-proposals`] = () => json(200, { items: [
      { ...base, proposal_id: "aip_01M4YDQK00TA0000000000H0A1", mode: "edit", status: "ready", summary: "Safe\u202e change\u200b" },
    ], next_cursor: "c1" });
    const el = await page(<ProposalsPage modelId={MODEL} />);
    await settle();
    const link = el.querySelector("li a")!;
    expect(link.querySelector("bdi")?.textContent).toBe("Safe⟨U+202E⟩ change⟨U+200B⟩");
    expect(link.textContent).not.toMatch(/[\u202e\u200b]/);
    expect(el.textContent).toContain(en["morePages.proposals"].one);
    answers[`GET /api/v1/models/${MODEL}/simulations`] = () => json(200, { items: [boardScenarioRun()], next_cursor: "c2" });
    const runs = await page(<RunsPage modelId={MODEL} />);
    await settle();
    expect(runs.textContent).toContain(en["morePages.runs"].one);
  });

  it("says so when there are no proposals, runs or scenarios", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    answers[`GET /api/v1/models/${MODEL}/ai-proposals`] = () => json(200, { items: [], next_cursor: null });
    answers[`GET /api/v1/models/${MODEL}/simulations`] = () => json(200, { items: [], next_cursor: null });
    answers[`GET /api/v1/models/${MODEL}/versions`] = () => json(200, { items: [], next_cursor: null });
    expect((await page(<ProposalsPage modelId={MODEL} />)).textContent).toContain(en["proposals.empty"]);
    expect((await page(<RunsPage modelId={MODEL} />)).textContent).toContain(en["runs.list.empty"]);
    const scenarios = await page(<ScenariosPage modelId={MODEL} />);
    await settle();
    expect(scenarios.textContent).toContain(en["scenarios.empty"]);
  });

  it("lists the runs with their state and replications, a scenario's linked to it", async () => {
    answers[`GET /api/v1/models/${MODEL}`] = () => json(200, model);
    const scenarioRun = boardScenarioRun();
    answers[`GET /api/v1/models/${MODEL}/simulations`] = () => json(200, { items: [boardBaselineRun({ status: "running", progress: { completed_replications: 2, total_replications: 5 } as never }), scenarioRun], next_cursor: null });
    const el = await page(<RunsPage modelId={MODEL} />);
    await settle();
    const rows = [...el.querySelectorAll("li")];
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain(en["runs.status.running"]);
    expect(rows[1]!.querySelector("a")?.getAttribute("href")).toBe(`/scenarios/${scenarioRun.scenario_id}`);
    expect(await violations()).toEqual([]);
  });
});

describe("the proposal names the Meta models (H-043)", () => {
  it("shows which model wrote it and what read the photo, as data from the server", async () => {
    const api = createProposalStandIn({ sample: "photo", proposal: { generated_by: { provider: "meta", model: "muse-spark-1.3", segmenter: { provider: "meta", model: "sam-3.1", objects: 12 } } } });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={api.proposalId!} pollMs={60_000} />
        </ApiProvider>
      </AuthProvider>,
    );
    for (let i = 0; i < 20 && el.querySelector("[data-proposal-status]") === null; i++) await settle();
    const facts = [...el.querySelectorAll("header dl > div")].map((d) => d.textContent);
    expect(facts).toContain(`${en["ai.writtenBy"]}Muse Spark 1.3 (Meta)`);
    expect(facts).toContain(`${en["ai.photoReadBy"]}SAM 3.1 · 12 objects`);
    expect(await violations()).toEqual([]);
  });

  it("shows an unknown model by its own ID, never as markup", async () => {
    const api = createProposalStandIn({ sample: "ready", proposal: { generated_by: { provider: "acme", model: "next-1" } } });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={api.proposalId!} pollMs={60_000} />
        </ApiProvider>
      </AuthProvider>,
    );
    for (let i = 0; i < 20 && el.querySelector("[data-proposal-status]") === null; i++) await settle();
    expect([...el.querySelectorAll("header dl > div")].map((d) => d.textContent)).toContain(`${en["ai.writtenBy"]}next-1 (acme)`);
  });
});
