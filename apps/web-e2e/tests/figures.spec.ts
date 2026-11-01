// H-073 review: the electronics demo's lead times are about 1,000 min, so
// they are written with their hours ("1,028.79 min (17.1 h)"). On a phone
// (390 px) the Scenarios page's ranking and the results' Compare Table set
// two figures side by side in cells of about 130-160 px; there every figure
// stays whole and on screen, and nothing scrolls sideways (DESIGN.md H-039,
// H-073), in en and in zh-Hant, whose hours are wider.
//
// The fake API holds no runs, so the test answers the browser's reads of one
// model's scenarios, runs and results itself (page.route), with the
// electronics line's real engine output and comparison
// (apps/web/features/results/fixtures/electronics-line-*.json: the demo's
// "Fourth FCT tester" what-if beside its baseline). Only the envelopes around
// them (the model, its version, the scenario and the two runs) are built
// here; every other read goes to the fake as usual.
//
// Also on the results page: a fingerprint's whole value stays open while the
// pointer moves from the fingerprint onto it (WCAG 1.4.13 hoverable).
import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { cellOverflow, locales, open, signInAs, tenant } from "./helpers";

const fixture = <T>(name: string): T => JSON.parse(readFileSync(new URL(`../../web/features/results/fixtures/${name}`, import.meta.url), "utf8")) as T;
type Comparison = { baseline_run_id: string; scenario_run_id: string; metrics: { metric_path: string; baseline: number | null; scenario: number | null }[] };
type Result = { run_id: string; engine_version: string; reproducibility: { model_hash: string; effective_model_hash: string; config_hash: string; seed: number; replications: number } };
type IR = { model_id: string; name: string; domain: string; schema_version: string };
const comparison = fixture<Comparison>("electronics-line-comparison.json");
const ir = fixture<IR>("electronics-line-model.json");
const results = { baseline: fixture<Result>("electronics-line-baseline-result.json"), scenario: fixture<Result>("electronics-line-scenario-result.json") };

const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const USER = "usr_01M4YDQK00TA00000000000001";
const MODEL = ir.model_id;
// The version the comparison kernel was given (checks/evidence/2026-11-01-H-073-s3).
const VERSION = "mv_01M4YDQK00TA0000000000H073";
const SCENARIO = "scn_01M4YDQK00TA0000000000H073";
const [BASELINE_RUN, SCENARIO_RUN] = [comparison.baseline_run_id, comparison.scenario_run_id];
// The what-if as the generator applied it: resource 3, the FCT testers, from 3 to 4.
const patch = [
  { op: "test", path: "/resources/3/capacity", value: 3 },
  { op: "replace", path: "/resources/3/capacity", value: 4 },
];

const model = {
  id: MODEL, organization_id: tenant.LINE, project_id: tenant.PRJ_LINE, domain: ir.domain, name: ir.name,
  draft: { revision: 1, base_version_id: VERSION, out_of_date: false }, latest_version: { id: VERSION, version: 1 }, created_at: "2026-11-01T08:00:00Z",
};
const version = {
  id: VERSION, organization_id: tenant.LINE, model_id: MODEL, version: 1, schema_version: ir.schema_version, parent_version_id: null,
  ir, hash: results.baseline.reproducibility.model_hash, dataset_bindings: [], comment: null, created_by: USER, created_at: "2026-11-01T08:00:00Z",
};
const scenario = {
  id: SCENARIO, organization_id: tenant.LINE, model_id: MODEL, base_version_id: VERSION, name: "Fourth FCT tester", patch,
  patch_hash: `sha256:${"2".repeat(64)}`, source_proposal_id: null, created_by: USER, created_at: "2026-11-01T08:10:00Z",
};
function run(id: string, result: Result, name: string, over: Record<string, unknown>) {
  const r = result.reproducibility;
  return {
    id, organization_id: tenant.LINE, model_id: MODEL, model_version_id: VERSION, name, scenario_patch: [], scenario_id: null,
    // The lock's settings: 28 days, 7 of warm-up, 5 replications, seed 2026, no trace.
    config: { horizon: { value: 28, unit: "day" }, warmup: { value: 7, unit: "day" }, replications: r.replications, seed: r.seed, trace_level: "none" },
    config_hash: r.config_hash, model_hash: r.model_hash, effective_model_hash: r.effective_model_hash, engine_version: result.engine_version, dataset_bindings: [],
    execution_limits: { wall_timeout_seconds: 300, max_events: 10_000_000, max_active_entities: 100_000, max_entity_visits: 10_000, max_trace_events: 100_000 },
    status: "completed", progress: { completed_replications: r.replications, total_replications: r.replications, events_processed: 0, revision: 9 },
    cancellation_requested: false, created_at: "2026-11-01T08:20:00Z", started_at: "2026-11-01T08:20:01Z", finished_at: "2026-11-01T08:21:00Z", error: null,
    ...over,
  };
}
const runs = {
  [BASELINE_RUN]: run(BASELINE_RUN, results.baseline, "Baseline for Fourth FCT tester", {}),
  [SCENARIO_RUN]: run(SCENARIO_RUN, results.scenario, "Fourth FCT tester", { scenario_patch: patch, scenario_id: SCENARIO, created_at: "2026-11-01T08:20:02Z" }),
};
const page1 = (items: unknown[]) => ({ items, next_cursor: null });

/** The answer to a GET of the API, or undefined to let the fake answer it. */
function answer(url: URL): unknown {
  const p = url.pathname.replace(/^\/api\/v1/, "");
  const runAt = /^\/simulations\/([^/]+)(\/results|\/analyses)?$/.exec(p);
  if (runAt && Object.hasOwn(runs, runAt[1]!)) {
    const id = runAt[1]!;
    if (runAt[2] === "/results") return id === BASELINE_RUN ? results.baseline : results.scenario;
    if (runAt[2] === "/analyses") return page1([]);
    return runs[id];
  }
  const table: Record<string, unknown> = {
    [`/models/${MODEL}`]: model,
    [`/models/${MODEL}/versions`]: page1([version]),
    [`/model-versions/${VERSION}`]: version,
    [`/model-versions/${VERSION}/scenarios`]: page1([scenario]),
    [`/models/${MODEL}/simulations`]: page1([runs[BASELINE_RUN], runs[SCENARIO_RUN]]),
    [`/models/${MODEL}/ai-proposals`]: page1([]),
    [`/models/${MODEL}/investigations`]: page1([]),
    [`/scenarios/${SCENARIO}`]: scenario,
  };
  if (p === "/simulation-comparisons") {
    const q = url.searchParams;
    return q.get("baseline_run_id") === BASELINE_RUN && q.get("scenario_run_id") === SCENARIO_RUN ? comparison : undefined;
  }
  return table[p];
}

async function answerElectronics(page: Page): Promise<void> {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const body = route.request().method() === "GET" ? answer(url) : undefined;
    if (body === undefined) return route.fallback();
    await route.fulfill({ status: 200, contentType: "application/json", headers: { "X-Request-ID": REQ }, body: JSON.stringify(body) });
  });
}

const catalogs = { en, "zh-Hant": zhHant } as const;
const median = comparison.metrics.find((m) => m.metric_path === "/metrics/lead_time_minutes/p50")!;
// What the page writes for the hours (the catalog's own words around the server's minutes / 60, to one decimal).
const hours = (locale: keyof typeof catalogs, minutes: number) =>
  catalogs[locale]["compare.hours"].replace("{minutes}", "").replace("{hours}", new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(minutes / 60)).trim();

/** Every figure in the table is one line (never broken), and inside the viewport; the table's own region does not scroll sideways. */
async function figuresFit(page: Page, table: import("@playwright/test").Locator, width: number) {
  const broken = await table.locator("data").evaluateAll((ds) => ds.filter((d) => d.getClientRects().length > 1).map((d) => d.textContent));
  expect(broken, "figures broken across lines").toEqual([]);
  const outside = await table.locator("data").evaluateAll((ds, w) => ds.filter((d) => d.getBoundingClientRect().right > w).map((d) => d.textContent), width);
  expect(outside, "figures past the screen's edge").toEqual([]);
  const region = await table.evaluate((t) => {
    const r = t.parentElement!;
    return { scroll: r.scrollWidth, client: r.clientWidth };
  });
  expect(region.scroll, "the table's region scrolls sideways").toBeLessThanOrEqual(region.client);
  expect(await cellOverflow(page), "table cells narrower than their text").toEqual({ overlaps: [], clipped: [] });
  expect(await page.evaluate(() => document.documentElement.scrollWidth), "page-level horizontal scroll").toBeLessThanOrEqual(width);
}

test.describe("at 390px", () => {
  test.use({ viewport: { width: 390, height: 900 } });

  for (const locale of locales) {
    const t = catalogs[locale];

    test(`${locale}: the Scenarios ranking keeps lead times of about 1,000 min whole and on screen, hours and all`, async ({ page }) => {
      await answerElectronics(page);
      await signInAs(page, tenant.editor, `/models/${MODEL}/scenarios`, locale);
      const table = page.getByRole("table", { name: t["scenarios.ranking.caption"] });
      await expect(table).toBeVisible();
      // The premise: both medians are written with their hours.
      await expect(table.getByRole("cell").filter({ hasText: hours(locale, median.baseline!) })).toHaveCount(1);
      await expect(table.getByRole("cell").filter({ hasText: hours(locale, median.scenario!) })).toHaveCount(1);
      await expect(table.locator(`data[value="${median.baseline}"]`)).toBeVisible();
      await figuresFit(page, table, 390);
    });

    test(`${locale}: the results' Compare Table keeps lead times of about 1,000 min whole and on screen, hours and all`, async ({ page }) => {
      await answerElectronics(page);
      await signInAs(page, tenant.editor, `/scenarios/${SCENARIO}/results?baseline=${BASELINE_RUN}&scenario=${SCENARIO_RUN}`, locale);
      const table = page.getByRole("table", { name: t["compare.caption"] });
      await expect(table).toBeVisible();
      await expect(table.getByRole("cell").filter({ hasText: hours(locale, median.baseline!) }).first()).toBeVisible();
      await expect(table.locator(`data[value="${median.scenario}"]`)).toBeVisible();
      await figuresFit(page, table, 390);
    });
  }
});

// WCAG 1.4.13 (hoverable): the chip opens under the fingerprint's row, so the
// pointer crosses the rest of the cell to reach it; the chip stays open on the
// way and once the pointer is on it, and Escape still closes it there.
for (const width of [390, 1280]) {
  test(`at ${width}px: a fingerprint's whole value stays open while the pointer moves onto it`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, "/ui-gallery/results", "en");
    const proof = page.getByRole("table", { name: en["proof.caption"] });
    await expect(proof).toBeVisible();
    // Model version (one line) and Model as run (a label that wraps in the side panel, so a taller row).
    for (const label of [en["proof.model_hash"], en["proof.effective_model_hash"]]) {
      for (const column of [0, 1]) {
        const cell = proof.getByRole("row", { name: new RegExp(`^${label.replace(/[()]/g, "\\$&")}`) }).getByRole("cell").nth(column);
        const hash = cell.locator("[tabindex='0']");
        const chip = cell.locator("code");
        await hash.scrollIntoViewIfNeeded();
        const from = (await hash.boundingBox())!;
        await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
        await expect.poll(async () => (await chip.boundingBox())!.width, `${label} ${column}: opens on hover`).toBeGreaterThan(100);
        const to = (await chip.boundingBox())!;
        // Down to the chip a pixel at a time, as a hand would move.
        const x = from.x + from.width / 2;
        const target = to.y + Math.min(6, to.height / 2);
        await page.mouse.move(x, target, { steps: Math.ceil(target - (from.y + from.height / 2)) });
        expect((await chip.boundingBox())!.width, `${label} ${column}: still open with the pointer on it`).toBeGreaterThan(100);
        expect(await chip.evaluate((c, [px, py]) => c.contains(document.elementFromPoint(px!, py!)), [x, target]), `${label} ${column}: the pointer is on the chip`).toBe(true);
        // Dismissible there too.
        await page.keyboard.press("Escape");
        await expect.poll(async () => (await chip.boundingBox())!.width, `${label} ${column}: Escape closes it`).toBeLessThanOrEqual(1);
        await page.mouse.move(0, 0);
      }
    }
  });
}
