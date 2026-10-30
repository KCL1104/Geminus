// @vitest-environment happy-dom
// H-048: the landing at /. Its one action follows the session the server
// saw (signed out: the sign-in, which returns to the projects; signed in:
// the projects; a session that ended: the sign-in again); it reads in both
// languages; it carries no credential or address of any kind; its pictures
// are the committed product screens, from the app's own origin, one per
// theme where both exist; and axe finds nothing (contrast and layout are
// the browser's: apps/web-e2e).
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import type { Bootstrap } from "../lib/auth/bootstrap";
import { type Account, type Auth, AuthProvider } from "../lib/auth/session";
import { I18nProvider } from "../lib/i18n/provider";
import type { Locale } from "../lib/i18n/locale";
import { createTranslator } from "../lib/i18n/translate";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import { Landing } from "../features/landing/landing";
import { pictures, type ShotName } from "../features/landing/shot";
import { shots } from "../features/landing/shots";
import { mount, violations } from "./ui-harness";
import { templateName } from "../features/models/templates";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined }));
vi.mock("next/headers", () => ({
  cookies: async () => ({ get: (name: string) => (name === "geminus_locale" && jar.locale !== undefined ? { name, value: jar.locale } : undefined) }),
}));
vi.mock("next/navigation", async (orig) => ({ ...(await orig<typeof import("next/navigation")>()), useRouter: () => ({ refresh: () => {}, push: () => {} }), usePathname: () => "/" }));

const ORG = "org_01M4YDQK00Y5H2929M9WWZWTVH";
const account = (bootstrap: Bootstrap): Account => ({ bootstrap, choose: () => {}, retry: () => {}, signOut: async () => null });
const organization = { id: ORG, name: "Line A", role: "editor" as const };
const auth = {
  /** No provider's account: what a page sees outside the app's bootstrap, and signed out. */
  none: { session: null, role: null } satisfies Auth,
  signedOut: { session: null, role: null, account: account({ state: "signed_out" }) } satisfies Auth,
  loading: { session: null, role: null, account: account({ state: "loading" }) } satisfies Auth,
  ready: { session: { organizationId: ORG }, role: "editor", account: account({ state: "ready", organizations: [organization], organization, truncated: false }) } satisfies Auth,
};
const catalogs = { en, "zh-Hant": zhHant } as const;

async function page(value: Auth, locale: Locale = "en") {
  return mount(
    <AuthProvider value={value}>
      <Landing t={createTranslator(locale)} />
    </AuthProvider>,
    locale,
  );
}

const links = (el: Element) => [...el.querySelectorAll("a")].map((a) => [a.textContent, a.getAttribute("href")]);
const text = (el: Element) => (el.textContent ?? "").replace(/\s+/g, " ").trim();

describe("the landing's one action (H-048)", () => {
  it.each([["no bootstrap", auth.none], ["signed out", auth.signedOut]] as const)("%s: the sign-in, back to the projects, and where the judges' account is", async (_, value) => {
    const el = await page(value);
    expect(links(el)).toEqual([[en["landing.signIn"], "/auth/login?return_to=%2Fprojects"]]);
    expect(text(el)).toContain(en["landing.judges"]);
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
  });

  it.each([["loading /me", auth.loading], ["ready", auth.ready]] as const)("with a session (%s): the projects, and no sign-in", async (_, value) => {
    const el = await page(value);
    expect(links(el)).toEqual([[en["landing.openProjects"], "/projects"]]);
    expect(text(el)).not.toContain(en["landing.judges"]);
    expect(el.querySelectorAll('[data-variant="primary"]')).toHaveLength(1);
  });

  it("is the server's start page: the root route renders it", async () => {
    jar.locale = undefined;
    const { default: Home, generateMetadata } = await import("../app/page");
    const out = renderToStaticMarkup(<I18nProvider locale="en">{await Home()}</I18nProvider>);
    expect(out).toContain(`<h1 id="landing-title">${en["landing.title"].replace(/'/g, "&#x27;")}</h1>`);
    expect(out).toContain('href="/auth/login?return_to=%2Fprojects"');
    expect(await generateMetadata()).toEqual({ description: en["landing.lede"] });
    jar.locale = "zh-Hant";
    expect(renderToStaticMarkup(<I18nProvider locale="zh-Hant">{await Home()}</I18nProvider>)).toContain(`<h1 id="landing-title">${zhHant["landing.title"]}</h1>`);
  });
});

describe("the landing's content (H-048)", () => {
  it.each(["en", "zh-Hant"] as const)("%s: the promise, the loop in five steps, the Meta models, the industries and the safety rules, in order", async (locale) => {
    const c = catalogs[locale];
    const el = await page(auth.signedOut, locale);
    // Review round 1, M3: the product's name stands above the promise.
    expect(text(el.querySelector("section p")!)).toBe(c["app.name"]);
    expect(text(el.querySelector("h1")!)).toBe(c["landing.title"]);
    expect([...el.querySelectorAll("h2")].map(text)).toEqual([c["landing.loop.title"], c["landing.meta.title"], c["landing.industries.title"], c["landing.safe.title"]]);
    expect([...el.querySelectorAll("ol > li h3")].map(text)).toEqual(
      (["describe", "propose", "approve", "simulate", "explain"] as const).map((s) => c[`landing.step.${s}.title`]),
    );
    // Who acts in each step: the person, the Meta models by name, the Geminus server, the engine.
    const who = [...el.querySelectorAll("ol > li")].map((li) => [...li.querySelectorAll(`ul[aria-label="${c["landing.who"]}"] > li`)].map(text));
    const meta = c["ai.provider.meta"];
    expect(who).toEqual([
      [c["landing.actor.you"]],
      [`${meta} Muse Spark 1.3`, `${meta} SAM 3.1`, c["landing.actor.geminus"]],
      [c["landing.actor.you"]],
      [c["landing.actor.you"], c["landing.actor.engine"], c["landing.actor.geminus"]],
      [`${meta} Muse Spark 1.3`, c["landing.actor.geminus"]],
    ]);
    // A Meta model's pill has the accent's edge (the AI), Geminus's the ink (its own check).
    expect([...el.querySelectorAll("ol > li")][1]!.querySelectorAll('[data-kind="ai"]')).toHaveLength(2);
    expect([...el.querySelectorAll("ol > li")][1]!.querySelectorAll('[data-kind="server"]')).toHaveLength(1);
    // Review round 1, M1: the Meta models (H-049: three, Muse Wonder 1.0 the optional one for proposals), and web search as a tool of the Meta Model API that the Muse models use, not a model.
    expect([...el.querySelectorAll("dl")[0]!.querySelectorAll("dt")].map(text)).toEqual([`Muse Spark 1.3 ${meta}`, `Muse Wonder 1.0 ${meta}`, `SAM 3.1 ${meta}`, `${c["landing.meta.searchName"]} ${c["landing.meta.tool"]}`]);
    // Review round 1, M2: the decision's screen is Muse Spark's own proposal from the live demo, which names its writer.
    expect([...el.querySelectorAll("ol > li")][2]!.querySelector("img")?.getAttribute("src")).toBe("/landing/proposed-1188.webp");
    expect([...el.querySelectorAll("ol > li")][2]!.querySelector("figcaption")?.textContent).toBe(c["landing.shot.proposed.caption"]);
    for (const k of ["electronics_assembly_test_line", "shoe_factory", "food_plant", "carton_plant", "warehouse"] as const) expect(text(el)).toContain(c[templateName(k)]);
    expect([...el.querySelectorAll("dl")[1]!.querySelectorAll("dt")].map(text)).toEqual(
      (["proposes", "checks", "person", "versions", "synthetic"] as const).map((k) => c[`landing.safe.${k}`]),
    );
    expect(await violations()).toEqual([]);
  });

  it("names no account, address, password or key in either language, signed out or in", async () => {
    for (const locale of ["en", "zh-Hant"] as const) {
      for (const value of [auth.signedOut, auth.ready]) {
        const el = await page(value, locale);
        const all = `${text(el)} ${[...el.querySelectorAll("img")].map((i) => i.alt).join(" ")}`;
        expect(all).not.toMatch(/@|password|passcode|密碼|\bsecret\b|api[_ -]?key|token/i);
        expect(all).not.toMatch(/judge@|geminus-demo|auth0/i);
      }
    }
    // The catalog's landing messages themselves, too.
    const landing = (c: Record<string, unknown>) => Object.entries(c).filter(([k]) => k.startsWith("landing.")).map(([, v]) => String(v)).join(" ");
    for (const c of [en, zhHant]) expect(landing(c)).not.toMatch(/@|password|密碼|token|api[_ -]?key/i);
  });
});

/** A WebP file's width and height, from its RIFF header (VP8, VP8L or VP8X). */
function webpSize(buf: Buffer): { width: number; height: number } {
  if (buf.toString("ascii", 0, 4) !== "RIFF" || buf.toString("ascii", 8, 12) !== "WEBP") throw new Error("not a WebP file");
  const chunk = buf.toString("ascii", 12, 16);
  if (chunk === "VP8 ") return { width: buf.readUInt16LE(26) & 0x3fff, height: buf.readUInt16LE(28) & 0x3fff };
  if (chunk === "VP8L") {
    const b = buf.readUInt32LE(21);
    return { width: (b & 0x3fff) + 1, height: ((b >> 14) & 0x3fff) + 1 };
  }
  if (chunk === "VP8X") return { width: buf.readUIntLE(24, 3) + 1, height: buf.readUIntLE(27, 3) + 1 };
  throw new Error(`unknown WebP chunk ${chunk}`);
}

describe("the landing's pictures (H-048)", () => {
  const dir = join(process.cwd(), "public/landing");
  const names = Object.keys(shots) as ShotName[];

  it("are the committed screens, at the sizes the page says, 1x and 2x per theme of the gallery's screens", () => {
    expect(names.sort()).toEqual(["canvas", "explained", "marey", "proposed", "results"]);
    const used = new Set<string>();
    for (const name of names) {
      const s = shots[name] as { width: number; height: number; themes: Record<string, string[]> };
      // The gallery's screens exist in both themes; the two from the live demo were captured in the light theme only.
      expect(Object.keys(s.themes).sort(), name).toEqual(name === "explained" || name === "proposed" ? ["light"] : ["dark", "light"]);
      for (const files of Object.values(s.themes)) {
        files.forEach((f, i) => {
          const size = webpSize(readFileSync(join(dir, f)));
          expect(size.width, f).toBe(s.width * (i + 1));
          expect(Math.abs(size.height - s.height * (i + 1)), f).toBeLessThanOrEqual(1);
          used.add(f);
        });
      }
    }
    // Nothing else is in public/landing: no stray or stale picture is served.
    expect(readdirSync(dir).sort()).toEqual([...used].sort());
  });

  it("are served from the app's own origin, each with its alt text, lazily, and one per theme", async () => {
    const el = await page(auth.signedOut);
    const imgs = [...el.querySelectorAll("img")];
    expect(imgs).toHaveLength(names.reduce((n, name) => n + pictures(name).length, 0));
    for (const img of imgs) {
      expect(img.getAttribute("src")).toMatch(/^\/landing\/[a-z]+(-light|-dark)?-\d+\.webp$/);
      for (const candidate of (img.getAttribute("srcset") ?? "").split(", ")) expect(candidate).toMatch(/^\/landing\/[a-z]+(-light|-dark)?-\d+\.webp \d+w$/);
      expect(img.getAttribute("alt")?.length).toBeGreaterThan(40);
      expect(img.getAttribute("loading")).toBe("lazy");
      expect(Number(img.getAttribute("width"))).toBeGreaterThan(0);
      expect(Number(img.getAttribute("height"))).toBeGreaterThan(0);
    }
    // Both themes' pictures of a screen share its alt text; CSS shows one (landing.module.css).
    const results = imgs.filter((i) => /\/landing\/results-/.test(i.getAttribute("src")!));
    expect(results.map((i) => i.getAttribute("src"))).toEqual(["/landing/results-light-968.webp", "/landing/results-dark-968.webp"]);
    expect(new Set(results.map((i) => i.alt)).size).toBe(1);
    // Review round 1, L9: each screen is framed as a screenshot, its caption a bar above the picture.
    for (const figure of el.querySelectorAll("figure")) expect(figure.firstElementChild?.tagName).toBe("FIGCAPTION");
    // L6: the two live-demo screens (light only) are marked so the dark theme can dim them.
    expect([...el.querySelectorAll('figure[data-live] img')].map((i) => i.getAttribute("src"))).toEqual(["/landing/proposed-1188.webp", "/landing/explained-408.webp"]);
  });

  // Review round 1, L5, left as it is: a picture that is not lazy makes React put a preload link in
  // the head ahead of the CSP nonce meta (layout.test.tsx pins the head), so the first view's stays lazy.
  it("puts nothing in the head: no preload of a picture ahead of the CSP nonce meta", async () => {
    const { default: Home } = await import("../app/page");
    const out = renderToStaticMarkup(<I18nProvider locale="en">{await Home()}</I18nProvider>);
    expect(out).toMatch(/^<div class="[^"]*"><section/);
    expect(out).not.toContain('rel="preload"');
    expect(out).not.toContain('loading="eager"');
  });
});
