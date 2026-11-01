// @vitest-environment happy-dom
// T-032: the results view's review page (/ui-gallery/results) is off unless
// the deployment turns the gallery on, like the gallery (T-045); each of its
// states renders the product's components with no axe violation, and its
// one control does what it says.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { boardLine } from "../features/results/fixtures/board-line";
import { readElectronicsLine } from "./fixtures/electronics-line";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { mount, press, settle, violations } from "./ui-harness";

async function waitFor(check: () => boolean, what: string, ms = 10_000): Promise<void> {
  const until = Date.now() + ms;
  while (!check()) {
    if (Date.now() > until) throw new Error(`timed out waiting for ${what}`);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 5));
    });
  }
}

vi.mock("../lib/i18n/server", () => ({ requestLocale: async () => "en" }));
vi.setConfig({ testTimeout: 120_000 });

afterEach(() => {
  delete process.env.GEMINUS_UI_GALLERY;
});

describe("the results review page", () => {
  it("answers 404 unless GEMINUS_UI_GALLERY=1, and takes only its own states", async () => {
    const { default: Page, generateMetadata } = await import("../app/ui-gallery/results/page");
    for (const value of [undefined, "", "0", "true"]) {
      if (value === undefined) delete process.env.GEMINUS_UI_GALLERY;
      else process.env.GEMINUS_UI_GALLERY = value;
      await expect(Page({ searchParams: Promise.resolve({}) }), String(value)).rejects.toThrow(/NEXT_HTTP_ERROR_FALLBACK;404|NEXT_NOT_FOUND/);
      expect(await generateMetadata()).toEqual({});
    }
    process.env.GEMINUS_UI_GALLERY = "1";
    expect(await generateMetadata()).toEqual({ title: en["galleryResults.title"] });
    const states = await Promise.all(["running", "nonsense", ["failed"]].map(async (state) => ((await Page({ searchParams: Promise.resolve({ state }) })) as { props: { state: string } }).props.state));
    expect(states).toEqual(["running", "results", "results"]);
    // It hands the engine output over as read from its files, number for number.
    const page = (await Page({ searchParams: Promise.resolve({}) })) as { props: { data: typeof boardLine; line?: unknown } };
    expect(page.props.data).toEqual(boardLine);
    expect(page.props.line).toBeUndefined();
    // H-072: the electronics line's states get its engine output too, as read from its files.
    for (const state of ["line", "still"]) {
      const p = (await Page({ searchParams: Promise.resolve({ state }) })) as { props: { state: string; line?: unknown } };
      expect([p.props.state, p.props.line]).toEqual([state, readElectronicsLine()]);
    }
  });

  // The minifier of a production build rewrote a bundled JSON number to
  // another double (29.138691666666666 became 29.138691666666663; the
  // browser check of the review page caught it). Engine output reaches a
  // page as data parsed at run time, never as an imported JSON module.
  it("never bundles the engine output: no app module imports a fixture's JSON", () => {
    const root = process.cwd();
    const files = (dir: string): string[] =>
      readdirSync(dir).flatMap((name) => {
        const p = join(dir, name);
        if (statSync(p).isDirectory()) return name === "node_modules" || name.startsWith(".") ? [] : files(p);
        return /\.tsx?$/.test(name) ? [p] : [];
      });
    const sources = ["app", "features", "lib", "ui"].flatMap((d) => files(join(root, d)));
    expect(sources.length).toBeGreaterThan(40);
    const bundled = sources.filter((f) => /from\s+["'][^"']*\.json["']|import\(\s*["'][^"']*\.json["']/.test(readFileSync(f, "utf8"))).map((f) => relative(root, f));
    expect(bundled).toEqual([]);
  });

  it.each([
    ["results", "en"], ["incomparable", "en"], ["running", "en"], ["failed", "en"],
    ["results", "zh-Hant"], ["running", "zh-Hant"],
  ] as const)("renders %s (%s) with no axe violation", async (state, locale) => {
    const { ResultsPreview } = await import("../app/ui-gallery/results/preview");
    // No API is called: what the page reads, it reads from its stand-in.
    const fetched = vi.spyOn(globalThis, "fetch");
    const el = await mount(<ResultsPreview state={state} data={boardLine} />, locale);
    await settle();
    if (state === "results" || state === "incomparable") {
      expect(el.querySelector("svg[role=img]")).not.toBeNull();
      expect(el.querySelector("table")).not.toBeNull();
      // H-073 (4): the proof reads the model's runs from the page's stand-in, not the API: a real state, no problem.
      const t = locale === "zh-Hant" ? zhHant : en;
      await waitFor(() => el.querySelector("[data-reruns=done], [data-reruns=failed]") !== null, "the reruns' check");
      expect(el.querySelector("[data-reruns=done]")?.textContent).toContain(t["proof.reruns.none"]);
      expect(el.querySelector("[role=alert]")).toBeNull();
      // H-073 (3): the board line is a Software Delivery model, which has no work ratio: no constraint card.
      const headings = [...el.querySelectorAll("h2")].map((h) => h.textContent);
      expect(headings).not.toContain(t["constraint.title"]);
      expect(headings).toContain(t["proof.title"]);
    }
    if (state === "running") expect(el.querySelectorAll("[role=progressbar]")).toHaveLength(2);
    if (state === "failed") expect(el.querySelector("[role=alert]")?.getAttribute("data-problem")).toBe("invalid");
    expect(await violations()).toEqual([]);
    expect(fetched).not.toHaveBeenCalled();
    fetched.mockRestore();
  });

  // H-072: the electronics line template's baseline beside a fourth FCT tester, in motion, and held at Day 1, 22:00.
  it.each([["line", "en"], ["still", "en"], ["still", "zh-Hant"]] as const)("renders the electronics line %s (%s) with its two stages, with no axe violation", async (state, locale) => {
    const { ResultsPreview } = await import("../app/ui-gallery/results/preview");
    const el = await mount(<ResultsPreview state={state} data={boardLine} line={readElectronicsLine()} />, locale);
    await settle();
    expect([...el.querySelectorAll("section[data-stage]")].map((s) => s.getAttribute("data-stage"))).toEqual(["baseline", "scenario"]);
    expect(el.querySelectorAll("[role=group]")).toHaveLength(1);
    const toggle = el.querySelector("[role=group] button")!;
    const spoken = el.querySelector("input[type=range]")!.getAttribute("aria-valuetext");
    if (state === "still") {
      expect(toggle.getAttribute("aria-label")).toBe(locale === "en" ? en["playback.play"] : "播放");
      expect(spoken).toBe(locale === "en" ? "Day 1, 22:00 of 1 day" : "第 1 天 22:00／1 天");
    } else {
      expect(toggle.getAttribute("aria-label")).toBe(en["playback.pause"]);
    }
    expect(el.querySelector("svg[role=img]")).not.toBeNull();
    expect(await violations()).toEqual([]);
  });

  it("cancels its running runs when asked", async () => {
    const { ResultsPreview } = await import("../app/ui-gallery/results/preview");
    const el = await mount(<ResultsPreview state="running" data={boardLine} />);
    await press([...el.querySelectorAll("button")].find((b) => b.textContent === en["runs.cancel"]));
    await settle();
    expect(el.querySelectorAll("[role=progressbar]")).toHaveLength(0);
    expect(el.textContent).toContain(en["runs.noComparison"]);
    expect([...el.querySelectorAll("button")].filter((b) => b.textContent === en["runs.cancel"])).toHaveLength(0);
  });
});
