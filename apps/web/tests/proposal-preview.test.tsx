// @vitest-environment happy-dom
// T-031: the proposal review's review page (/ui-gallery/proposal) is off
// unless the deployment turns the gallery on, like the gallery (T-045) and
// the results review page (T-032); each state renders the product's page
// (the real client and ProposalRoute over the in-page stand-in) with no axe
// violation. Its samples are the sample line's (the editor preview's) and
// follow the contract: each state is the schema's, each patch applies to
// the version it names, and each effective risk is the higher of the
// server's and the AI's (D-027).
import { createHash } from "node:crypto";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sampleLine } from "../app/ui-gallery/editor/sample-line";
import { sampleFor, states } from "../app/ui-gallery/proposal/samples";
import { applyPatch } from "../features/model-editor/patch";
import { createTranslator } from "../lib/i18n/translate";
import { mount, settle, violations } from "./ui-harness";

vi.mock("../lib/i18n/server", () => ({ requestLocale: async () => "en" }));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 120_000 });

afterEach(() => {
  delete process.env.GEMINUS_UI_GALLERY;
});

const en = createTranslator("en");
const rank = { low: 1, medium: 2, high: 3 } as const;

describe("the proposal review page", () => {
  it("answers 404 unless GEMINUS_UI_GALLERY=1, and takes only its own states", async () => {
    const { default: Page, generateMetadata } = await import("../app/ui-gallery/proposal/page");
    for (const value of [undefined, "", "0", "true"]) {
      if (value === undefined) delete process.env.GEMINUS_UI_GALLERY;
      else process.env.GEMINUS_UI_GALLERY = value;
      await expect(Page({ searchParams: Promise.resolve({}) }), String(value)).rejects.toThrow(/NEXT_HTTP_ERROR_FALLBACK;404|NEXT_NOT_FOUND/);
      expect(await generateMetadata()).toEqual({});
    }
    process.env.GEMINUS_UI_GALLERY = "1";
    expect(await generateMetadata()).toEqual({ title: en("galleryProposal.title") });
    const picked = await Promise.all(["scenario", "nonsense", ["failed"], "needs_input"].map(async (state) => ((await Page({ searchParams: Promise.resolve({ state }) })) as { props: { state: string } }).props.state));
    expect(picked).toEqual(["scenario", "ready", "failed", "needs_input"]);
  });

  it("has samples that follow the contract", () => {
    const statuses = new Set<string>();
    for (const state of states) {
      const { proposal, answer } = sampleFor(state);
      for (const p of [proposal, answer].filter((x) => x !== undefined)) {
        statuses.add(p.status);
        expect(p.requires_confirmation).toBe(true);
        expect(p.model_id).toBe(sampleLine.model_id);
        if (p.changes) expect(applyPatch(sampleLine, p.changes).ok, `${state}: the patch applies to version 1`).toBe(true);
        if (p.risk_level) expect(rank[p.risk_level], `${state}: D-027`).toBe(Math.max(rank[p.server_risk_level!], rank[p.provider_risk_claim!]));
        if (p.status === "needs_input") expect(p.changes).toBeUndefined();
        if (p.status === "invalid" || p.status === "failed") expect([p.summary, p.changes, p.risk_level]).toEqual([undefined, undefined, undefined]);
        expect(["approved", "rejected", "conflicted"].includes(p.status)).toBe(p.decided_at !== undefined);
      }
    }
    // Every status of proposal.schema.json has a sample.
    expect([...statuses].sort()).toEqual(["approved", "conflicted", "failed", "generating", "invalid", "needs_input", "ready", "rejected"]);
  });

  it("gives each sample patch its real patch_hash (SHA-256 of its RFC 8785 form, D-019)", () => {
    const jcs = (v: unknown): string => {
      if (Array.isArray(v)) return `[${v.map(jcs).join(",")}]`;
      if (v !== null && typeof v === "object") {
        const o = v as Record<string, unknown>;
        return `{${Object.keys(o).sort().map((k) => `${JSON.stringify(k)}:${jcs(o[k])}`).join(",")}}`;
      }
      return JSON.stringify(v);
    };
    const hashed = states.flatMap((s) => [sampleFor(s).proposal, sampleFor(s).answer]).filter((p) => p?.changes !== undefined);
    expect(hashed.length).toBeGreaterThan(5);
    for (const p of hashed) expect(p!.patch_hash, p!.summary).toBe(`sha256:${createHash("sha256").update(jcs(p!.changes)).digest("hex")}`);
  });

  it.each([...states.map((s) => [s, "en"] as const), ["ready", "zh-Hant"] as const, ["needs_input", "zh-Hant"] as const])("renders %s (%s) with no axe violation", async (state, locale) => {
    const { ProposalPreview } = await import("../app/ui-gallery/proposal/preview");
    const el = await mount(<ProposalPreview state={state} />, locale);
    const until = Date.now() + 10_000;
    while (el.querySelector("[data-proposal-status]") === null) {
      if (Date.now() > until) throw new Error("not rendered");
      await settle();
    }
    await settle();
    // The page names itself a review tool on sample data.
    expect(el.textContent).toContain(createTranslator(locale)("galleryProposal.intro"));
    expect(el.querySelectorAll("nav a").length).toBe(states.length);
    expect(await violations()).toEqual([]);
  });
});
