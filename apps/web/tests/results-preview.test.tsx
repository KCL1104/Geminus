// @vitest-environment happy-dom
// T-032: the results view's review page (/ui-gallery/results) is off unless
// the deployment turns the gallery on, like the gallery (T-045); each of its
// states renders the product's components with no axe violation, and its
// one control does what it says.
import { afterEach, describe, expect, it, vi } from "vitest";
import { en } from "../messages/en";
import { mount, press, settle, violations } from "./ui-harness";

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
  });

  it.each([
    ["results", "en"], ["incomparable", "en"], ["running", "en"], ["failed", "en"],
    ["results", "zh-Hant"], ["running", "zh-Hant"],
  ] as const)("renders %s (%s) with no axe violation", async (state, locale) => {
    const { ResultsPreview } = await import("../app/ui-gallery/results/preview");
    const el = await mount(<ResultsPreview state={state} />, locale);
    await settle();
    if (state === "results" || state === "incomparable") {
      expect(el.querySelector("svg[role=img]")).not.toBeNull();
      expect(el.querySelector("table")).not.toBeNull();
    }
    if (state === "running") expect(el.querySelectorAll("[role=progressbar]")).toHaveLength(2);
    if (state === "failed") expect(el.querySelector("[role=alert]")?.getAttribute("data-problem")).toBe("invalid");
    expect(await violations()).toEqual([]);
  });

  it("cancels its running runs when asked", async () => {
    const { ResultsPreview } = await import("../app/ui-gallery/results/preview");
    const el = await mount(<ResultsPreview state="running" />);
    await press([...el.querySelectorAll("button")].find((b) => b.textContent === en["runs.cancel"]));
    await settle();
    expect(el.querySelectorAll("[role=progressbar]")).toHaveLength(0);
    expect(el.textContent).toContain(en["runs.noComparison"]);
    expect([...el.querySelectorAll("button")].filter((b) => b.textContent === en["runs.cancel"])).toHaveLength(0);
  });
});
