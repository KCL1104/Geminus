// @vitest-environment happy-dom
// T-045: the component gallery is off unless the deployment turns it on,
// and every component state in it passes axe (WCAG 2.0–2.2 A/AA; contrast
// and target size are the browser checks').
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount, settle, violations } from "./ui-harness";

vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ refresh: () => {} }) }));
vi.mock("../lib/i18n/server", () => ({ requestLocale: async () => "en" }));
vi.setConfig({ testTimeout: 180_000 });

afterEach(() => {
  delete process.env.GEMINUS_UI_GALLERY;
});

describe("component gallery", () => {
  it("answers 404 unless GEMINUS_UI_GALLERY=1", async () => {
    const { default: GalleryPage, generateMetadata } = await import("../app/ui-gallery/page");
    for (const value of [undefined, "", "0", "true", "yes"]) {
      if (value === undefined) delete process.env.GEMINUS_UI_GALLERY;
      else process.env.GEMINUS_UI_GALLERY = value;
      expect(() => GalleryPage(), String(value)).toThrow(/NEXT_HTTP_ERROR_FALLBACK;404|NEXT_NOT_FOUND/);
      expect(await generateMetadata()).toEqual({});
    }
    process.env.GEMINUS_UI_GALLERY = "1";
    expect(GalleryPage()).toBeTruthy();
    expect(await generateMetadata()).toEqual({ title: "Component gallery" });
  });

  it.each(["en", "zh-Hant"] as const)("has every state and no axe violation (%s)", async (locale) => {
    const { Gallery } = await import("../app/ui-gallery/gallery");
    const el = await mount(<Gallery />, locale);
    await settle();
    expect(el.querySelectorAll("[role=grid]").length).toBe(3);
    expect(el.querySelectorAll("[data-problem]").length).toBe(4);
    expect(el.querySelectorAll("[data-tone]").length).toBeGreaterThanOrEqual(9);
    expect(el.querySelector("[role=status][aria-busy=true]")).not.toBeNull();
    expect(await violations()).toEqual([]);
  });
});
