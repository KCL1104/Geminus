// @vitest-environment happy-dom
// H-061 A8 review round 1: what keeps AI investigations hidden while the
// build's flag is off (NEXT_PUBLIC_GEMINUS_INVESTIGATIONS, off in production)
// and the labels on what the AI made, which show whatever the flag. The
// product's /investigations/<id> is not found unless the flag is "1"; the
// review page /ui-gallery/investigation is not found unless the gallery is
// on (GEMINUS_UI_GALLERY=1), like the proposal and results review pages; on
// a flag-off build no page links to an investigation, while a proposal, the
// draft and a scenario the AI made still say they are the AI's (H-056 §13,
// REQ-019).
import type { ReactElement } from "react";
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SAMPLE_MODEL_ID } from "../app/ui-gallery/editor/sample-line";
import { sampleInvestigation, states } from "../app/ui-gallery/investigation/samples";
import { USER } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { InvestigationRoute } from "../features/investigations/investigation-view";
import { ModelView } from "../features/model-editor/model-view";
import { SimulateView } from "../features/simulations/simulate-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { ModelVersion, Scenario } from "../lib/contracts";
import { createTranslator } from "../lib/i18n/translate";
import { RequireSession } from "../app/_components/states";
import { mount, settle, violations } from "./ui-harness";

vi.mock("../lib/i18n/server", () => ({ requestLocale: async () => "en" }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 60_000 });
afterEach(() => {
  vi.unstubAllEnvs();
  delete process.env.GEMINUS_UI_GALLERY;
});

const t = createTranslator("en");
const FLAG = "NEXT_PUBLIC_GEMINUS_INVESTIGATIONS";
const NOT_FOUND = /NEXT_HTTP_ERROR_FALLBACK;404|NEXT_NOT_FOUND/;
const INV = sampleInvestigation("running").id;

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}
const text = (el: Element | null | undefined) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();
const facts = (el: Element) => [...el.querySelectorAll("header dl > div")].map((d) => [text(d.querySelector("dt")), text(d.querySelector("dd"))]);
const fact = (el: Element, label: string) => facts(el).find(([l]) => l === label)?.[1];
/** Every link on the page to an investigation's page. */
const investigationLinks = (el: ParentNode) => [...el.querySelectorAll('a[href^="/investigations/"]')].map((a) => a.getAttribute("href"));

describe("the investigation page's route (H-061 A8)", () => {
  it("is not found unless the build turns investigations on, and shows the investigation with it", async () => {
    const { default: Page } = await import("../app/investigations/[investigationId]/page");
    const params = () => ({ params: Promise.resolve({ investigationId: INV }) });
    for (const value of [undefined, "", "0", "true", " 1"]) {
      vi.stubEnv(FLAG, value);
      await expect(Page(params()), String(value)).rejects.toThrow(NOT_FOUND);
    }
    vi.stubEnv(FLAG, "1");
    const page = (await Page(params())) as ReactElement<{ children: ReactElement<{ investigationId: string }> }>;
    expect(page.type).toBe(RequireSession);
    expect(page.props.children.type).toBe(InvestigationRoute);
    expect(page.props.children.props.investigationId).toBe(INV);
  });
});

describe("the investigation page's review page (/ui-gallery/investigation)", () => {
  it("answers 404 unless GEMINUS_UI_GALLERY=1, and takes only its own states", async () => {
    const { default: Page, generateMetadata } = await import("../app/ui-gallery/investigation/page");
    for (const value of [undefined, "", "0", "true"]) {
      if (value === undefined) delete process.env.GEMINUS_UI_GALLERY;
      else process.env.GEMINUS_UI_GALLERY = value;
      await expect(Page({ searchParams: Promise.resolve({}) }), String(value)).rejects.toThrow(NOT_FOUND);
      expect(await generateMetadata()).toEqual({});
    }
    process.env.GEMINUS_UI_GALLERY = "1";
    expect(await generateMetadata()).toEqual({ title: t("galleryInvestigation.title") });
    const picked = await Promise.all(["finished", "nonsense", ["stopped"], "edit"].map(async (state) => ((await Page({ searchParams: Promise.resolve({ state }) })) as { props: { state: string } }).props.state));
    expect(picked).toEqual(["finished", "running", "stopped", "edit"]);
    expect(states).toContain("viewer");
  });
});

describe("a build with investigations off (production): the AI's labels stay, no link to an investigation", () => {
  it("says on an applied AI proposal and on a ready one what the AI does, with no link to the investigation", async () => {
    vi.stubEnv(FLAG, undefined);
    const applied = createProposalStandIn({ sample: "scenario", proposal: { status: "applied", investigation: { id: INV, index: 1 }, scenario_id: "scn_01M4YDQK00E1ECTR0N1C550001" } });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: applied.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: applied.organizationId }), fetch: applied.fetch })}>
          <ProposalRoute proposalId={applied.proposalId!} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("[data-decided='applied']") !== null, "the applied outcome");
    expect(text(el.querySelector("[data-decided='applied']"))).toContain(t("proposal.applied.scenario"));
    expect(el.querySelector('a[href="/scenarios/scn_01M4YDQK00E1ECTR0N1C550001"]')).not.toBeNull();
    expect(investigationLinks(el)).toEqual([]);
    expect(text(el)).not.toContain(t("aiMark.seeInvestigation"));
    expect(await violations()).toEqual([]);

    const ready = createProposalStandIn({ sample: "scenario", proposal: { investigation: { id: INV, index: 1 } } });
    const el2 = await mount(
      <AuthProvider value={{ session: { organizationId: ready.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: ready.organizationId }), fetch: ready.fetch })}>
          <ProposalRoute proposalId={ready.proposalId!} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el2.querySelector("[data-by-ai]") !== null, "the AI note");
    expect(text(el2.querySelector("[data-by-ai]"))).toContain(t("proposal.byAi.ready"));
    expect(investigationLinks(el2)).toEqual([]);
  });

  it("calls out the AI's change in the draft and marks the version, with no link to the investigation", async () => {
    vi.stubEnv(FLAG, undefined);
    const api = createProposalStandIn({ sample: null, aiEdit: { investigation: sampleInvestigation("edit").id, proposal: "aip_01M4YDQK00E1ECTR0N1C5A0002" } });
    const el = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch })}>
          <ModelView modelId={SAMPLE_MODEL_ID} onProposal={() => {}} />
        </ApiProvider>
      </AuthProvider>,
    );
    await waitFor(() => el.querySelector("[data-ai-edit]") !== null, "the draft callout");
    expect(text(el.querySelector("[data-ai-edit]"))).toBe(t("aiEdit.draft"));
    await waitFor(() => fact(el, t("aiEdit.versionLabel", { version: "1" })) !== undefined, "the version fact");
    expect(fact(el, t("aiEdit.versionLabel", { version: "1" }))).toBe(t("aiEdit.version"));
    expect(investigationLinks(el)).toEqual([]);
  });
});

describe("the scenario page of a scenario the AI saved (H-056 §13)", () => {
  const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
  const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
  const version = { id: "mv_01M4YDQK00E1ECTR0N1C500001", organization_id: ORG, model_id: SAMPLE_MODEL_ID, version: 1, schema_version: "0.1", parent_version_id: null,
    ir: { name: "Board line", time_unit: "minute" }, hash: `sha256:${"3".repeat(64)}`, dataset_bindings: [], comment: null, created_by: USER, created_at: "2026-10-26T09:11:00Z" } as unknown as ModelVersion;
  const made = { investigation_id: INV, requested_by: USER, generated_by: { provider: "meta" as const, model: "muse-spark-1.3" as const } };
  const scenario = (over: Partial<Scenario> = {}): Scenario => ({ id: "scn_01M4YDQK00E1ECTR0N1C550001", organization_id: ORG, model_id: SAMPLE_MODEL_ID, base_version_id: version.id, name: "Second ICT tester",
    patch: [], patch_hash: `sha256:${"0".repeat(64)}`, source_proposal_id: null, created_by: USER, created_at: "2026-11-01T06:00:26Z", ...over });
  const client = () =>
    createApiClient({
      session: () => ({ organizationId: ORG }),
      fetch: (async (input: RequestInfo | URL) => {
        const path = new URL(String(input), "http://app.test").pathname;
        if (path !== `/api/v1/models/${SAMPLE_MODEL_ID}/simulations`) throw new Error(`no fake answer for ${path}`);
        return new Response(JSON.stringify({ items: [], next_cursor: null }), { status: 200, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
      }) as typeof fetch,
    });
  const open = (s: Scenario, userId: string | null) =>
    mount(
      <AuthProvider value={{ session: { organizationId: ORG }, role: "editor", userId }}>
        <ApiProvider client={client()}>
          <SimulateView scenario={s} version={version} onStarted={() => {}} />
        </ApiProvider>
      </AuthProvider>,
    );

  it("says in its header that the AI saved it, for whom, and links the investigation only in a build with its page", async () => {
    vi.stubEnv(FLAG, undefined);
    const el = await open(scenario({ made_by_ai: made }), USER);
    await settle();
    expect(fact(el, t("aiMark.label"))).toBe(`${t("aiMark.saved")} ${t("aiMark.for.you")}`);
    expect(el.querySelector("header [data-ai-mark='saved']")).not.toBeNull();
    expect(investigationLinks(el)).toEqual([]);
    expect(await violations()).toEqual([]);

    vi.stubEnv(FLAG, "1");
    const other = await open(scenario({ made_by_ai: made }), "usr_01M4YDQK00E1ECTR0N1C500001");
    await settle();
    expect(fact(other, t("aiMark.label"))).toBe(`${t("aiMark.saved")} ${t("aiMark.for.other")} · ${t("aiMark.seeInvestigation")}`);
    expect(investigationLinks(other)).toEqual([`/investigations/${INV}`]);
  });

  it("puts no label on a scenario a person saved", async () => {
    vi.stubEnv(FLAG, "1");
    const el = await open(scenario(), USER);
    await settle();
    expect(fact(el, t("aiMark.label"))).toBeUndefined();
    expect(el.querySelector("[data-ai-mark]")).toBeNull();
  });
});
