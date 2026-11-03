// @vitest-environment happy-dom
// H-086 (R): when the server's grounding check refused the AI's first
// explanation and asked it once more, the analysis is the second answer and
// the API says so (generated_by.revised_after_check). The explanation shows
// one short line for it, in either language, the same words a revised
// proposal shows (H-057); without the flag, no line. The answers come from a
// fake fetch; no AI is called.
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { AnalysisRecord, GeneratedBy } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { BASELINE_RUN, SCENARIO_RUN, boardBaselineRun, boardComparison, boardResults, boardScenarioRun, boardVersion } from "../features/results/fixtures/board-line";
import { keyMetrics } from "../features/results/metrics";
import { SavedExplanation } from "../features/results/saved-explanation";
import { mount, settle, unmountAll, violations } from "./ui-harness";

vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 30_000 });
afterEach(() => unmountAll());

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const ANA = "ana_01M4YDQK00TA0000000000H086";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const listPath = `/api/v1/simulations/${SCENARIO_RUN}/analyses`;
const lead = boardComparison.metrics.find((m) => m.metric_path === "/metrics/lead_time_minutes/p50")!;

const record = (generatedBy: GeneratedBy): AnalysisRecord => ({
  id: ANA, organization_id: ORG, run_id: SCENARIO_RUN, baseline_run_id: BASELINE_RUN, status: "completed",
  created_at: "2026-11-03T06:00:00Z", error: null, generated_by: generatedBy, output_locale: "en",
  analysis: {
    baseline: { summary: "One ICT fixture held boards back." },
    scenario: { summary: "A second ICT fixture shortened the lead time." },
    delta: [{ metric_path: lead.metric_path, absolute_delta: lead.absolute_delta, relative_delta: lead.relative_delta, reason: "Less waiting before ICT." }],
    bottleneck: [{ statement: "ICT was the busiest station.", kind: "observation", evidence: [{ run: "baseline", metric_path: "/metrics/resources/ict_fixture" }] }],
    assumptions: [],
    uncertainty: ["5 replications leave some spread."],
    suggested_next_experiments: [{ name: "Faster AOI", hypothesis: "AOI may become the next constraint.", requires_confirmation: true }],
  },
});

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

/** The saved explanation of the pair, as a finished comparison shows it, over a fetch that lists this one record. */
async function open(generatedBy: GeneratedBy, locale: Locale = "en") {
  const fetch = (async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://app.test");
    if (url.pathname !== listPath) throw new Error(`no fake answer for ${url.pathname}`);
    return new Response(JSON.stringify({ items: [record(generatedBy)], next_cursor: null }), { status: 200, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
  }) as typeof globalThis.fetch;
  const client = createApiClient({ session: () => editor.session, fetch });
  const rows = keyMetrics(boardComparison, boardVersion.ir, { baseline: boardResults.baseline, scenario: boardResults.scenario });
  const el = await mount(
    <AuthProvider value={editor}>
      <ApiProvider client={client}>
        <SavedExplanation baseline={boardBaselineRun()} scenario={boardScenarioRun()} rows={rows} pollMs={25} analysisId={null} onAnalysis={() => {}} />
      </ApiProvider>
    </AuthProvider>,
    locale,
  );
  await settle();
  await waitFor(() => text(el).includes("A second ICT fixture"), "the saved explanation");
  return el;
}

const revised: GeneratedBy = { provider: "meta", model: "muse-spark-1.3", revised_after_check: true };

describe("an explanation revised once after the server's check (H-086 (R))", () => {
  it.each([
    ["en", en["proposal.revisedAfterCheck"]],
    ["zh-Hant", zhHant["proposal.revisedAfterCheck"]],
  ] as const)("says so in one line in %s, and the page has no accessibility violation", async (locale, words) => {
    const el = await open(revised, locale);
    const lines = el.querySelectorAll("[data-revised]");
    expect(lines).toHaveLength(1);
    expect(text(lines[0])).toBe(words);
    // The page's own words: never marked with the language the AI wrote in.
    expect(lines[0]!.hasAttribute("lang")).toBe(false);
    expect(words).toBe(locale === "en" ? "Revised once after DryLine's check." : "經 DryLine 檢查後修正過一次。");
    expect(await violations()).toEqual([]);
  });

  it("says nothing of it when the first explanation passed", async () => {
    const el = await open({ provider: "meta", model: "muse-spark-1.3" });
    expect(el.querySelector("[data-revised]")).toBeNull();
    expect(text(el)).not.toContain(en["proposal.revisedAfterCheck"]);
  });
});
