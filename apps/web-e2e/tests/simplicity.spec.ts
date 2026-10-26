// T-045 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): on every route, in
// both languages, at phone and desktop width: at most one primary action;
// no disabled-forever control outside the component gallery (which shows
// the disabled state on purpose); no two visible links to the same place
// and no two visible buttons with the same name, except the pair the owner
// is asked about; no side panel; and the app strip stays one 48 px row.
import { expect, type Page, test } from "@playwright/test";
import { locales, open, routes, signedInViews, signInAs } from "./helpers";

// Kept until the owner decides (T-045 re-skin report): the start page's
// "Go to projects" goes where the strip's "Projects" goes; T-044's tests
// assert it (keyboard.spec.ts, apps/web/tests/layout.test.tsx).
const askedAbout: Record<string, string[]> = { "/": ["/projects"] };
// The 2D editor (T-015) blocks Save draft while nothing is unsaved and
// Publish while the draft has errors or unsaved edits: each stays focusable
// as aria-disabled with its reason on screen beside it (DESIGN.md Buttons,
// Disabled). That is a step of a shipped flow, not a disabled-forever
// placeholder; the rule there is that every blocked control says why.
const blockedWithReason = new Set(["/ui-gallery/editor"]);

/** The visible controls of the page: links by where they go, buttons by name. */
async function controls(page: Page) {
  return page.evaluate(() => {
    const visible = (el: Element) => {
      const r = el.getBoundingClientRect();
      return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden";
    };
    const links = [...document.querySelectorAll<HTMLAnchorElement>("a[href]")].filter(visible).map((a) => a.getAttribute("href")!).filter((h) => !h.startsWith("#"));
    const buttons = [...document.querySelectorAll("button, [role=button]")].filter(visible).map((b) => b.getAttribute("aria-label") ?? (b.textContent ?? "").trim());
    const primary = [...document.querySelectorAll('[data-variant="primary"]')].filter(visible).length;
    const off = [...document.querySelectorAll("button:disabled, [aria-disabled=true], [data-disabled]")].filter(visible);
    // A blocked action whose reason is not on screen beside it (DESIGN.md Buttons: aria-disabled, its reason linked as its description).
    const unexplained = off
      .filter((el) => {
        const reason = document.getElementById(el.getAttribute("aria-describedby") ?? "");
        return !(el instanceof HTMLButtonElement && !el.disabled && reason && visible(reason) && (reason.textContent ?? "").trim() !== "");
      })
      .map((el) => (el.textContent ?? "").trim());
    // The strip's rows: its parts (product, navigation, tools, account) that lie below every part before them.
    const header = document.querySelector("header")!;
    const parts = [...header.children].filter(visible).map((el) => el.getBoundingClientRect()).sort((a, b) => a.top - b.top);
    let rows = 0;
    let bottom = -Infinity;
    for (const r of parts) {
      if (r.top >= bottom) rows++;
      bottom = Math.max(bottom, r.bottom);
    }
    const tops = parts.map((r) => Math.round(r.top));
    return { links, buttons, primary, disabled: off.length, unexplained, asides: document.querySelectorAll("aside").length, strip: header.getBoundingClientRect().height, rows, firstTop: Math.min(...tops), lastTop: Math.max(...tops) };
  });
}

const repeated = (xs: string[]) => [...new Set(xs.filter((x, i) => xs.indexOf(x) !== i))];

/** The transition and animation durations in use on the page, and whether the page sees reduced motion. */
async function motion(page: Page) {
  return page.evaluate(() => {
    const durations = new Set<string>();
    const easings = new Set<string>();
    let moving = 0;
    for (const el of document.querySelectorAll("*")) {
      const s = getComputedStyle(el);
      const ds = [...s.transitionDuration.split(","), ...s.animationDuration.split(",")].map((d) => d.trim()).filter((d) => parseFloat(d) > 0);
      if (ds.length === 0) continue;
      moving++;
      for (const d of ds) durations.add(d);
      // One easing per transitioned property, comma-separated (commas inside cubic-bezier() are not separators).
      if (s.transitionProperty !== "all") for (const e of s.transitionTimingFunction.split(/,(?![^(]*\))/)) easings.add(e.trim());
    }
    return { moving, durations: [...durations].sort(), easings: [...easings].sort(), reduce: matchMedia("(prefers-reduced-motion: reduce)").matches };
  });
}

for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });
      for (const route of routes) {
        test(`${route} keeps only the controls it needs`, async ({ page }) => {
          await open(page, route, locale);
          const c = await controls(page);
          expect(c.primary, "primary actions").toBeLessThanOrEqual(1);
          if (blockedWithReason.has(route)) expect(c.unexplained, "blocked controls without their reason on screen").toEqual([]);
          else if (route !== "/ui-gallery") expect(c.disabled, "disabled controls").toBe(0);
          expect(repeated(c.links), "links to the same place").toEqual(askedAbout[route] ?? []);
          expect(repeated(c.buttons), "buttons with the same name").toEqual([]);
          expect(c.asides, "side panels").toBe(0);
          expect(c.strip, "the app strip is one row").toBe(48);
          expect(c.rows, "the app strip is one row").toBe(1);
        });
      }
    });
  }
}

// The signed-in views (T-043 follow-up): the same rules with the strip's
// account controls (Sign out; the switcher with several organizations), on
// every page with data and the bootstrap's own views. The model page holds
// the editor, whose blocked actions say why (as on its preview above).
for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`signed in, ${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });
      for (const v of signedInViews) {
        test(`${v.name} keeps only the controls it needs`, async ({ page }) => {
          await signInAs(page, v.subject, v.path, locale, { organization: v.organization });
          const c = await controls(page);
          expect(c.buttons, "signed in").toContain(locale === "en" ? "Sign out" : "登出");
          expect(c.links.filter((h) => h.startsWith("/auth/login")), "no sign-in while signed in").toEqual([]);
          expect(c.primary, "primary actions").toBeLessThanOrEqual(1);
          if (v.path.startsWith("/models/")) expect(c.unexplained, "blocked controls without their reason on screen").toEqual([]);
          else expect(c.disabled, "disabled controls").toBe(0);
          expect(repeated(c.links), "links to the same place").toEqual([]);
          expect(repeated(c.buttons), "buttons with the same name").toEqual([]);
          expect(c.asides, "side panels").toBe(0);
          if (width === 390) {
            // A phone has no room left in the strip's row (8 to 14 px): the
            // account takes one row of its own under it, and the first row
            // stays as it is signed out (its parts from 8 px, the next row
            // from 48 px).
            expect(c.rows, "the strip's row and the account's").toBe(2);
            expect([c.firstTop, c.lastTop, c.strip], "the first row as signed out, the account's under it").toEqual([8, 48, 89]);
          } else {
            expect(c.rows, "the app strip is one row").toBe(1);
            expect(c.strip, "the app strip is one row").toBe(48);
          }
        });
      }
    });
  }
}

// H-027 rule 4 and DESIGN.md's Do's: the only motion is a control's 180 ms
// state-colour change, and a reader who asks for reduced motion gets none.
// The browser checks run with motion on, as the screenshot baselines were
// made; the reduced case asks for it itself (T-045 review 1 L1: the config's
// `use: { reducedMotion }` never reached the page).
test.describe("motion", () => {
  test("is only the 180 ms state-colour change of a control", async ({ page }) => {
    await open(page, "/ui-gallery", "en");
    const m = await motion(page);
    expect(m.reduce, "these checks run with motion on").toBe(false);
    expect(m.moving, "controls with a transition").toBeGreaterThan(5);
    expect(m.durations).toEqual(["0.18s"]);
    expect(m.easings).toEqual(["cubic-bezier(0.2, 0.8, 0.2, 1)"]);
  });

  test("stops for a reader who asks for reduced motion", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await open(page, "/ui-gallery", "en");
    const m = await motion(page);
    expect(m.reduce).toBe(true);
    expect(m.moving).toBeGreaterThan(5);
    // base.css: 0.01 ms, which Chromium reports as 1e-05s.
    for (const d of m.durations) expect(parseFloat(d), d).toBeLessThanOrEqual(0.00001);
    // The rule sets one duration for every property of every element.
    await expect(page.getByRole("button", { name: "Save", exact: true })).toHaveCSS("transition-duration", "1e-05s");
  });
});
