// @vitest-environment happy-dom
// H-057: when the server refused the AI's first answer and asked it once
// more, the proposal holds the second answer and the API says so
// (generated_by.revised_after_check). The proposal page shows one short line
// for it, in either language, on a ready proposal and on one the server
// refused again; without the flag, no line. The stand-in
// (app/ui-gallery/proposal/stand-in.ts) answers with the sample and these
// fields; no AI is called.
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { SAMPLE_PROPOSAL_ID } from "../app/ui-gallery/proposal/samples";
import { createProposalStandIn } from "../app/ui-gallery/proposal/stand-in";
import { ProposalRoute } from "../features/ai-proposals/proposal-view";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { AuthProvider } from "../lib/auth/session";
import type { GeneratedBy } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { mount, settle, unmountAll, violations } from "./ui-harness";

vi.setConfig({ testTimeout: 120_000 });

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

async function open(sample: "ready" | "invalid", generatedBy: GeneratedBy, locale: Locale = "en") {
  const api = createProposalStandIn({ sample, proposal: { generated_by: generatedBy } });
  const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
  const el = await mount(
    <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
      <ApiProvider client={client}>
        <ProposalRoute proposalId={api.proposalId ?? SAMPLE_PROPOSAL_ID} pollMs={25} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  await waitFor(() => el.querySelector("h1, [role=alert]") !== null, "the proposal page");
  return el;
}

const revised: GeneratedBy = { provider: "meta", model: "muse-spark-1.3", revised_after_check: true };

describe("a proposal revised once after the server's check", () => {
  it.each(["en", "zh-Hant"] as const)("says so in one line in %s, and the page has no accessibility violation", async (locale) => {
    const t = createTranslator(locale);
    const el = await open("ready", revised, locale);
    const line = el.querySelector("[data-revised]");
    expect(text(line)).toBe(t("proposal.revisedAfterCheck"));
    expect(el.querySelectorAll("[data-revised]")).toHaveLength(1);
    expect(await violations()).toEqual([]);
    unmountAll();
  });

  it("says so on a proposal the server refused again, as the gallery's invalid sample is", async () => {
    const el = await open("invalid", revised);
    expect(text(el.querySelector("[data-revised]"))).toBe(createTranslator("en")("proposal.revisedAfterCheck"));
    unmountAll();
    const api = createProposalStandIn({ sample: "invalid" });
    const client = createApiClient({ session: () => ({ organizationId: api.organizationId }), fetch: api.fetch });
    const gallery = await mount(
      <AuthProvider value={{ session: { organizationId: api.organizationId }, role: "editor" }}>
        <ApiProvider client={client}>
          <ProposalRoute proposalId={api.proposalId ?? SAMPLE_PROPOSAL_ID} pollMs={25} />
        </ApiProvider>
      </AuthProvider>,
      "en",
    );
    await settle();
    await waitFor(() => gallery.querySelector("h1, [role=alert]") !== null, "the gallery's invalid sample");
    expect(text(gallery.querySelector("[data-revised]"))).toBe(createTranslator("en")("proposal.revisedAfterCheck"));
    unmountAll();
  });

  it("shows no line when the first answer stood", async () => {
    for (const g of [{ provider: "meta", model: "muse-spark-1.3" }, { provider: "meta", model: "muse-spark-1.3", revised_after_check: false }] satisfies GeneratedBy[]) {
      const el = await open("ready", g);
      expect(el.querySelector("[data-revised]")).toBeNull();
      expect(text(el)).not.toContain(createTranslator("en")("proposal.revisedAfterCheck"));
      unmountAll();
    }
  });
});
