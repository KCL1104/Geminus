// @vitest-environment happy-dom
// H-073 (4): the run pair's reproducibility. Each run's fingerprints come
// from its Result (reproducibility), in Geist Mono, shortened, whole in the
// page and on hover or focus; "Same inputs, same result as N other runs"
// is counted from the model's completed runs whose inputs are equal and
// whose Results were read and compared, with capped reads that say so.
// Never more is claimed than was read. Through the app's own API client
// with a fake behind its fetch; no axe violation in any state.
import { act } from "react";
import { describe, expect, it } from "vitest";
import { createApiClient } from "../lib/api/client";
import { ApiProvider } from "../lib/api/provider";
import type { Result as SimulationResult, Run } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { ResultProof } from "../features/results/proof";
import { BASELINE_RUN, SCENARIO_RUN, boardBaselineRun, boardResults, boardScenarioRun } from "../features/results/fixtures/board-line";
import { key, mount, press, settle, violations } from "./ui-harness";

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const REQ = "req_01M4YDQK00V6H8S0ST4F7510KR";
const catalogs = { en, "zh-Hant": zhHant } as const;
const v1 = "/api/v1";
const MODEL = boardBaselineRun().model_id;
const LIST = `GET ${v1}/models/${MODEL}/simulations`;
const sim = (tail: string) => `sim_01M4YDQK00TA00000000${tail}`;
const sha = (c: string) => `sha256:${c.repeat(64)}`;

type Req = { method: string; path: string };
const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json", "X-Request-ID": REQ } });
const problem = (status: number, code: string) => json(status, { error: { code, message: "x", request_id: REQ } });

function fakeApi(table: Record<string, Response | Response[]>) {
  const calls: Req[] = [];
  const queues = new Map(Object.entries(table).map(([k, v]) => [k, Array.isArray(v) ? [...v] : [v]]));
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://app.test");
    const method = init?.method ?? "GET";
    calls.push({ method, path: url.pathname + url.search });
    const q = queues.get(`${method} ${url.pathname}`);
    if (!q || q.length === 0) throw new Error(`no fake answer for ${method} ${url.pathname}`);
    return (q.length > 1 ? q.shift()! : q[0]!).clone();
  }) as typeof globalThis.fetch;
  return { calls, client: createApiClient({ session: () => ({ organizationId: ORG }), fetch }) };
}

const baseline = { run: boardBaselineRun(), result: boardResults.baseline };
// As the server writes it, the what-if's run names the patched model's own hash.
const scenario = { run: boardScenarioRun({ effective_model_hash: sha("5") }), result: boardResults.scenario };
const page = (items: Run[], next: string | null = null) => json(200, { items, next_cursor: next });
const resultOf = (base: SimulationResult, id: string, repro: Partial<SimulationResult["reproducibility"]> = {}): SimulationResult => ({
  ...base, run_id: id, reproducibility: { ...base.reproducibility, ...repro },
});
const results = (id: string, r: SimulationResult) => ({ [`GET ${v1}/simulations/${id}/results`]: json(200, r) });
const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ").trim();
const reads = (api: ReturnType<typeof fakeApi>) => api.calls.filter((c) => c.path.endsWith("/results")).map((c) => c.path.split("/")[4]);
// The pointer comes from, and goes back to, the page around it (React reads enter and leave from over and out).
const hover = (el: Element) =>
  act(async () => {
    el.dispatchEvent(new PointerEvent("pointerover", { bubbles: true, relatedTarget: document.body, pointerType: "mouse" }));
    el.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body }));
  });
const unhover = (el: Element) =>
  act(async () => {
    el.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, relatedTarget: document.body, pointerType: "mouse" }));
    el.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body }));
  });

// Generous: these run beside other suites on a shared machine.
async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

async function proof(api: ReturnType<typeof fakeApi>, locale: Locale = "en", limits?: { pages: number; reads: number }, pair = { baseline, scenario }) {
  const el = await mount(
    <ApiProvider client={api.client}>
      <ResultProof baseline={pair.baseline} scenario={pair.scenario} limits={limits} />
    </ApiProvider>,
    locale,
  );
  await settle();
  return el;
}
const done = (el: Element) => el.querySelector("[data-reruns=done]") !== null;
const line = (el: Element, side: "baseline" | "scenario") => text(el.querySelector(`[data-reruns-for=${side}]`)!);

describe("the run pair's fingerprints (H-073)", () => {
  it.each(["en", "zh-Hant"] as Locale[])("are each run's Result's, shortened, whole in the page (%s)", async (locale) => {
    const m = catalogs[locale];
    const el = await proof(fakeApi({ [LIST]: page([baseline.run, scenario.run]) }), locale);
    await waitFor(() => done(el), "the reruns");
    expect(el.querySelector("h2")?.textContent).toBe(m["proof.title"]);
    const rows = [...el.querySelectorAll("tbody tr")];
    expect(rows.map((r) => text(r.querySelector("th")!))).toEqual(
      [m["proof.model_hash"], m["proof.effective_model_hash"], m["proof.config_hash"], m["proof.dataset_manifest_hash"], m["proof.result_digest"]],
    );
    const fields = ["model_hash", "effective_model_hash", "config_hash", "dataset_manifest_hash", "result_digest"] as const;
    rows.forEach((r, i) => {
      const cells = [...r.querySelectorAll("td")];
      expect(cells.map((c) => c.querySelector("code")?.textContent)).toEqual([baseline.result.reproducibility[fields[i]!], scenario.result.reproducibility[fields[i]!]]);
      // The short form is the first 8 hex digits, for the eye only; the whole value is what is read and copied.
      const short = cells.map((c) => c.querySelector("[aria-hidden=true]")?.textContent);
      expect(short).toEqual(cells.map((c) => `${c.querySelector("code")!.textContent!.slice(7, 15)}…`));
      // The short form is data, not words (strings.spec reads only the catalog's words): a <data> whose value is the whole fingerprint.
      expect(cells.map((c) => { const d = c.querySelector("[aria-hidden=true]")!; return [d.tagName, d.getAttribute("value")]; }))
        .toEqual(cells.map((c) => ["DATA", c.querySelector("code")!.textContent]));
      for (const c of cells) expect(c.querySelector("[tabindex='0']")).not.toBeNull();
    });
    expect(await violations()).toEqual([]);
  });

  it("shows a value whole on hover or focus, and hides it again on Escape", async () => {
    const el = await proof(fakeApi({ [LIST]: page([baseline.run, scenario.run]) }));
    const hash = el.querySelector<HTMLElement>("td [tabindex='0']")!;
    await act(async () => hash.focus());
    expect(hash.hasAttribute("data-dismissed")).toBe(false);
    await key(hash, "Escape");
    expect(hash.hasAttribute("data-dismissed")).toBe(true);
    await act(async () => hash.blur());
    expect(hash.hasAttribute("data-dismissed")).toBe(false);
  });

  // WCAG 1.4.13: the chip opens over the rows below it, so a pointer that only
  // rests on a fingerprint must be able to close it without moving. A browser
  // sends the key to the focused element (here the page), not to the hovered one.
  it("hides a value opened by hover on Escape, wherever the key goes, and lets other Escapes through", async () => {
    const el = await proof(fakeApi({ [LIST]: page([baseline.run, scenario.run]) }));
    const hash = el.querySelector<HTMLElement>("td [tabindex='0']")!;
    const seen: string[] = [];
    const listener = (e: KeyboardEvent) => seen.push(e.key);
    document.addEventListener("keydown", listener);
    try {
      // Not hovered: the page's Escape is not the chip's.
      await key(document.body, "Escape");
      expect(hash.hasAttribute("data-dismissed")).toBe(false);
      expect(seen).toEqual(["Escape"]);
      await hover(hash);
      expect(document.activeElement).not.toBe(hash);
      await key(document.body, "Escape");
      expect(hash.hasAttribute("data-dismissed")).toBe(true);
      // That Escape closed the chip and nothing else; the next one is the page's again.
      expect(seen).toEqual(["Escape"]);
      await key(document.body, "Escape");
      expect(seen).toEqual(["Escape", "Escape"]);
      await unhover(hash);
      expect(hash.hasAttribute("data-dismissed")).toBe(false);
      // Once the pointer has left, Escape is the page's again.
      await key(document.body, "Escape");
      expect(seen).toEqual(["Escape", "Escape", "Escape"]);
    } finally {
      document.removeEventListener("keydown", listener);
    }
  });

  // WCAG 1.4.13 (hoverable), H-073 review: the chip opens under the fingerprint's row, so the pointer crosses the rest
  // of the cell to reach it. The whole cell holds the chip open (proof.module.css), so the pointer anywhere on it, or
  // on the chip, is the pointer on the fingerprint: Escape closes the chip from there.
  it("takes the pointer anywhere on the fingerprint's cell, or on its chip, as on the fingerprint", async () => {
    const el = await proof(fakeApi({ [LIST]: page([baseline.run, scenario.run]) }));
    const cell = el.querySelector<HTMLElement>("tbody td")!;
    const hash = cell.querySelector<HTMLElement>("[tabindex='0']")!;
    for (const target of [cell, cell.querySelector("code")!]) {
      await hover(target);
      await key(document.body, "Escape");
      expect(hash.hasAttribute("data-dismissed"), target.tagName).toBe(true);
      await unhover(target);
      expect(hash.hasAttribute("data-dismissed"), target.tagName).toBe(false);
    }
  });

  it("keeps a value hidden after Escape until neither the pointer nor the focus is on it", async () => {
    const el = await proof(fakeApi({ [LIST]: page([baseline.run, scenario.run]) }));
    const hash = el.querySelector<HTMLElement>("td [tabindex='0']")!;
    await hover(hash);
    await act(async () => hash.focus());
    await key(hash, "Escape");
    expect(hash.hasAttribute("data-dismissed")).toBe(true);
    // Still focused: the chip must not open again through focus when the pointer leaves.
    await unhover(hash);
    expect(hash.hasAttribute("data-dismissed")).toBe(true);
    await act(async () => hash.blur());
    expect(hash.hasAttribute("data-dismissed")).toBe(false);
    // And the other way round: still hovered when the focus leaves.
    await hover(hash);
    await act(async () => hash.focus());
    await key(hash, "Escape");
    await act(async () => hash.blur());
    expect(hash.hasAttribute("data-dismissed")).toBe(true);
    await unhover(hash);
    expect(hash.hasAttribute("data-dismissed")).toBe(false);
  });
});

describe("same inputs, same result (H-073)", () => {
  const twin = (tail: string, over: Partial<Run> = {}) => boardBaselineRun({ id: sim(tail), created_at: "2026-10-27T09:00:00Z", ...over });

  it("counts the model's other completed runs with equal inputs whose result digest is the same, reading only those", async () => {
    const list = [baseline.run, scenario.run, twin("H073R1"), twin("H073R2"), twin("H073Q1", { status: "queued", finished_at: null }),
      twin("H073C1", { config_hash: sha("9") }), twin("H073E1", { engine_version: "des-go-0.4.0" }), twin("H073F1", { status: "failed" })];
    const api = fakeApi({ [LIST]: page(list), ...results(sim("H073R1"), resultOf(baseline.result, sim("H073R1"))), ...results(sim("H073R2"), resultOf(baseline.result, sim("H073R2"))) });
    const el = await proof(api);
    await waitFor(() => done(el), "the reruns");
    expect(line(el, "baseline")).toContain(en["proof.reruns.same"].other.replace("{count}", "2"));
    expect(line(el, "scenario")).toContain(en["proof.reruns.none"]);
    // The queued, failed and differently set up runs were never read.
    expect(reads(api).sort()).toEqual([sim("H073R1"), sim("H073R2")]);
    expect(api.calls.filter((c) => c.path.startsWith(`${v1}/models/`))).toHaveLength(1);
    expect(text(el)).not.toContain(en["proof.reruns.listCapped"].other.split("{count}")[0]!);
    expect(await violations()).toEqual([]);
  });

  it("says plainly when a run with the same inputs gave a different result, and does not count another dataset as the same inputs", async () => {
    const api = fakeApi({
      [LIST]: page([baseline.run, scenario.run, twin("H073R1"), twin("H073D1"), twin("H073R2")]),
      ...results(sim("H073R1"), resultOf(baseline.result, sim("H073R1"), { result_digest: sha("e") })),
      ...results(sim("H073D1"), resultOf(baseline.result, sim("H073D1"), { dataset_manifest_hash: sha("d") })),
      ...results(sim("H073R2"), resultOf(baseline.result, sim("H073R2"))),
    });
    const el = await proof(api);
    await waitFor(() => done(el), "the reruns");
    const b = el.querySelector("[data-reruns-for=baseline]")!;
    expect(text(b)).toContain(en["proof.reruns.same"].one.replace("{count}", "1"));
    expect(text(b)).toContain(en["proof.reruns.differ"].one.replace("{count}", "1"));
    // A broken promise of reproducibility is a signal, not a footnote.
    expect(b.querySelector("[data-tone=warning]")).not.toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("caps its reads and says what it did not read; it never claims more than it read", async () => {
    const api = fakeApi({
      [LIST]: page([baseline.run, scenario.run, twin("H073R1"), twin("H073R2"), twin("H073R3")], "next"),
      ...results(sim("H073R1"), resultOf(baseline.result, sim("H073R1"))),
    });
    const el = await proof(api, "en", { pages: 1, reads: 1 });
    await waitFor(() => done(el), "the reruns");
    expect(line(el, "baseline")).toContain(en["proof.reruns.same"].one.replace("{count}", "1"));
    expect(line(el, "baseline")).toContain(en["proof.reruns.unread"].other.replace("{count}", "2"));
    expect(text(el)).toContain(en["proof.reruns.listCapped"].other.replace("{count}", "5"));
    expect(reads(api)).toEqual([sim("H073R1")]);
    expect(api.calls.filter((c) => c.path.startsWith(`${v1}/models/`))).toHaveLength(1);
  });

  it("follows the list's pages up to its cap", async () => {
    const api = fakeApi({
      [LIST]: [page([baseline.run, twin("H073R1")], "p2"), page([scenario.run, twin("H073R2")])],
      ...results(sim("H073R1"), resultOf(baseline.result, sim("H073R1"))),
      ...results(sim("H073R2"), resultOf(baseline.result, sim("H073R2"))),
    });
    const el = await proof(api);
    await waitFor(() => done(el), "the reruns");
    expect(line(el, "baseline")).toContain(en["proof.reruns.same"].other.replace("{count}", "2"));
    const lists = api.calls.filter((c) => c.path.startsWith(`${v1}/models/`)).map((c) => c.path);
    expect(lists).toHaveLength(2);
    expect(lists[1]).toContain("cursor=p2");
  });

  it("says when the runs could not be read and offers one retry", async () => {
    const api = fakeApi({ [LIST]: [problem(503, "SERVICE_UNAVAILABLE"), page([baseline.run, scenario.run])] });
    const el = await proof(api);
    await waitFor(() => el.querySelector("[data-reruns=failed]") !== null, "the failure");
    expect(text(el)).toContain(en["proof.reruns.failed"]);
    const retry = [...el.querySelectorAll("button")].filter((b) => text(b) === en.retry);
    expect(retry).toHaveLength(1);
    expect(await violations()).toEqual([]);
    await press(retry[0]);
    await waitFor(() => done(el), "the retried reruns");
    expect(line(el, "baseline")).toContain(en["proof.reruns.none"]);
  });

  it("is written in Traditional Chinese too", async () => {
    const api = fakeApi({ [LIST]: page([baseline.run, scenario.run, twin("H073R1")]), ...results(sim("H073R1"), resultOf(baseline.result, sim("H073R1"))) });
    const el = await proof(api, "zh-Hant");
    await waitFor(() => done(el), "the reruns");
    expect(line(el, "baseline")).toContain(zhHant["proof.reruns.same"].other.replace("{count}", "1"));
    expect(line(el, "scenario")).toContain(zhHant["proof.reruns.none"]);
    expect(await violations()).toEqual([]);
  });

  it("keeps the pair's own runs out of the count", async () => {
    // A what-if that changes nothing: both runs have the same inputs. Neither is read or counted as the other's rerun.
    const same = { run: boardScenarioRun(), result: boardResults.scenario };
    expect([same.run.model_hash, same.run.effective_model_hash, same.run.config_hash]).toEqual([baseline.run.model_hash, baseline.run.effective_model_hash, baseline.run.config_hash]);
    const api = fakeApi({ [LIST]: page([baseline.run, same.run]) });
    const el = await proof(api, "en", undefined, { baseline, scenario: same });
    await waitFor(() => done(el), "the reruns");
    expect(reads(api)).toEqual([]);
    expect([BASELINE_RUN, SCENARIO_RUN].every((id) => !reads(api).includes(id))).toBe(true);
  });
});
