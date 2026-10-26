// T-045 / H-027 ("簡潔"; "只保留所有應該要包含的按鈕功能"): on every route, in
// both languages, at phone and desktop width: at most one primary action;
// no disabled-forever control outside the component gallery (which shows
// the disabled state on purpose); no two visible links to the same place
// and no two visible buttons with the same name, except the pair the owner
// is asked about; no side panel; and the app strip stays one 48 px row.
import { expect, type Page, test } from "@playwright/test";
import { locales, open, routes } from "./helpers";

// Kept until the owner decides (T-045 re-skin report): the start page's
// "Go to projects" goes where the strip's "Projects" goes; T-044's tests
// assert it (keyboard.spec.ts, apps/web/tests/layout.test.tsx).
const askedAbout: Record<string, string[]> = { "/": ["/projects"] };

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
    const disabled = [...document.querySelectorAll("button:disabled, [aria-disabled=true], [data-disabled]")].filter(visible).length;
    return { links, buttons, primary, disabled, asides: document.querySelectorAll("aside").length, strip: document.querySelector("header")!.getBoundingClientRect().height };
  });
}

const repeated = (xs: string[]) => [...new Set(xs.filter((x, i) => xs.indexOf(x) !== i))];

for (const locale of locales) {
  for (const width of [390, 1280]) {
    test.describe(`${locale} at ${width}px`, () => {
      test.use({ viewport: { width, height: 900 } });
      for (const route of routes) {
        test(`${route} keeps only the controls it needs`, async ({ page }) => {
          await open(page, route, locale);
          const c = await controls(page);
          expect(c.primary, "primary actions").toBeLessThanOrEqual(1);
          if (route !== "/ui-gallery") expect(c.disabled, "disabled controls").toBe(0);
          expect(repeated(c.links), "links to the same place").toEqual(askedAbout[route] ?? []);
          expect(repeated(c.buttons), "buttons with the same name").toEqual([]);
          expect(c.asides, "side panels").toBe(0);
          expect(c.strip, "the app strip is one row").toBe(48);
        });
      }
    });
  }
}
