// @vitest-environment happy-dom
// H-073 (4): each piece of the AI explanation's evidence links to the row of
// the metrics table it was checked against. Following it opens the step
// measures' disclosure when the row is in it, brings the row into view and
// puts the focus there. A metric cited again is not a second link to the
// same row (DESIGN.md Simplicity 2); a path no row has stays a path.
import { act, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import { type Auth, AuthProvider } from "../lib/auth/session";
import type { AnalysisRecord } from "../lib/contracts";
import { en } from "../messages/en";
import { EvidenceCite, linkedCites, metricRowId } from "../features/results/evidence-link";
import { keyMetrics } from "../features/results/metrics";
import { MetricsTable } from "../features/results/metrics-table";
import {
  BASELINE_RUN,
  SCENARIO_RUN,
  boardBaselineRun,
  boardComparison,
  boardResults,
  boardScenario,
  boardScenarioRun,
  boardVersion,
} from "../features/results/fixtures/board-line";
import { electronicsComparison, electronicsModel, electronicsResults } from "../features/results/fixtures/electronics-line";
import { mount, press, settle, violations } from "./ui-harness";

vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ push: () => {}, refresh: () => {} }) }));
vi.setConfig({ testTimeout: 30_000 });

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const editor: Auth = { session: { organizationId: ORG }, role: "editor" };
const v1 = "/api/v1";
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ").trim();

function fakeApi(table: Record<string, Response>) {
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    const r = table[`${init?.method ?? "GET"} ${url.pathname}`];
    if (!r) throw new Error(`no fake answer for ${url.pathname}`);
    return r.clone();
  }) as typeof globalThis.fetch;
  return createApiClient({ session: () => editor.session, fetch });
}

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

describe("a metric row's ID", () => {
  it("is one per path, safe in an address, and never the same for two paths", () => {
    const paths = ["/metrics/resources/ict_fixture/utilization_mean", "/metrics/domain/blocking_ratio.aoi/mean", "/metrics/domain/blocking_ratio_aoi/mean", "/metrics/domain/x~1y/mean", "/metrics/domain/é/mean"];
    const ids = paths.map(metricRowId);
    expect(new Set(ids).size).toBe(paths.length);
    for (const id of ids) expect(id).toMatch(/^metric-[A-Za-z0-9_-]+$/);
    expect(metricRowId(paths[0]!)).toBe(metricRowId(paths[0]!));
  });
});

describe("the evidence's links (unit)", () => {
  const rows = keyMetrics(electronicsComparison, electronicsModel, electronicsResults);

  it("open the step measures' disclosure, bring the cited row into view and focus it", async () => {
    const path = "/metrics/domain/blocking_ratio.aoi/mean";
    const el = await mount(
      <>
        <MetricsTable rows={rows} />
        <p>
          <EvidenceCite path={path} rows={rows} run="baseline" link />
        </p>
      </>,
    );
    const details = el.querySelector("details")!;
    expect(details.open).toBe(false);
    const a = el.querySelector<HTMLAnchorElement>("p a")!;
    expect(a.getAttribute("href")).toBe(`#${metricRowId(path)}`);
    expect(text(a)).toBe("Blocked ratio: AOI");
    const row = document.getElementById(metricRowId(path))!;
    expect(row.tagName).toBe("TR");
    expect(details.contains(row)).toBe(true);
    const scrolled = vi.fn();
    row.scrollIntoView = scrolled;
    await press(a);
    expect(details.open).toBe(true);
    expect(document.activeElement).toBe(row);
    expect(scrolled).toHaveBeenCalledWith({ block: "center" });
    expect(await violations()).toEqual([]);
  });

  it("link a statistic's group to the row under it, cite a path no row has as a path, and link each row once", () => {
    const bottleneck = [
      { statement: "a", kind: "observation" as const, evidence: [{ run: "baseline" as const, metric_path: "/metrics/domain/work_ratio.fct" }, { run: "scenario" as const, metric_path: "/metrics/domain/work_ratio.fct/mean" }] },
      { statement: "b", kind: "inference" as const, evidence: [{ run: "scenario" as const, metric_path: "/metrics/no_such_row" }, { run: "baseline" as const, metric_path: "/metrics/domain/blocking_ratio.aoi/mean" }] },
    ];
    expect([...linkedCites(bottleneck, rows)].sort()).toEqual(["0/0", "1/1"]);
  });
});

describe("the evidence's links on the results page", () => {
  const ANA = "ana_01M4YDQK00TA0000000000H073";
  const record: AnalysisRecord = {
    id: ANA, organization_id: ORG, run_id: SCENARIO_RUN, baseline_run_id: BASELINE_RUN, status: "completed", created_at: "2026-10-28T09:00:00Z", error: null,
    analysis: {
      baseline: { summary: "One ICT fixture held boards back." },
      scenario: { summary: "A second ICT fixture shortened the lead time." },
      delta: [],
      bottleneck: [
        { statement: "ICT was the busiest station.", kind: "observation", evidence: [{ run: "baseline", metric_path: "/metrics/resources/ict_fixture" }, { run: "scenario", metric_path: "/metrics/no_such_row" }] },
        { statement: "ICT stayed busy.", kind: "inference", evidence: [{ run: "scenario", metric_path: "/metrics/resources/ict_fixture/utilization_mean" }] },
      ],
      assumptions: [], uncertainty: [], suggested_next_experiments: [],
    },
  };
  const api = fakeApi({
    [`GET ${v1}/scenarios/${boardScenario.id}`]: json(200, boardScenario),
    [`GET ${v1}/model-versions/${boardVersion.id}`]: json(200, boardVersion),
    [`GET ${v1}/simulations/${BASELINE_RUN}`]: json(200, boardBaselineRun()),
    [`GET ${v1}/simulations/${SCENARIO_RUN}`]: json(200, boardScenarioRun()),
    [`GET ${v1}/simulation-comparisons`]: json(200, boardComparison),
    [`GET ${v1}/simulations/${BASELINE_RUN}/results`]: json(200, { ...boardResults.baseline, trace: { ...boardResults.baseline.trace, available: false } }),
    [`GET ${v1}/simulations/${SCENARIO_RUN}/results`]: json(200, { ...boardResults.scenario, trace: { ...boardResults.scenario.trace, available: false } }),
    [`GET ${v1}/analyses/${ANA}`]: json(200, record),
  });

  it("go to the row each was checked against, once per row", async () => {
    const { RunResults } = await import("../features/results/run-results");
    function Page() {
      const [id, setId] = useState<string | null>(ANA);
      return <RunResults scenarioId={boardScenario.id} baselineId={BASELINE_RUN} scenarioRunId={SCENARIO_RUN} pollMs={25} analysis={{ id, onChange: setId }} />;
    }
    const el = await mount(
      <AuthProvider value={editor}>
        <ApiProvider client={api}>
          <Page />
        </ApiProvider>
      </AuthProvider>,
    );
    await settle();
    await waitFor(() => text(el).includes("ICT stayed busy."), "the explanation");
    const section = el.querySelector('section[aria-labelledby="analysis-heading"]')!;
    const links = [...section.querySelectorAll<HTMLAnchorElement>('a[href^="#"]')];
    const target = metricRowId("/metrics/resources/ict_fixture/utilization_mean");
    // The group path and the row's own path cite one row: one link, the second citation plain text.
    expect(links.map((a) => a.getAttribute("href"))).toEqual([`#${target}`]);
    expect(text(links[0]!)).toBe(`${en["metric.utilization"].replace("{name}", "ICT fixture")}`);
    expect(text(section)).toContain(`${en["analysis.evidence"]}`);
    expect([...section.querySelectorAll("code")].map((c) => c.textContent)).toContain("/metrics/no_such_row");
    const row = document.getElementById(target)!;
    row.scrollIntoView = () => {};
    await press(links[0]);
    expect(document.activeElement).toBe(row);
    expect(await violations()).toEqual([]);
  });
});
