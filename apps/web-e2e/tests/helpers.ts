import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

export type Locale = "en" | "zh-Hant";
export const locales: Locale[] = ["en", "zh-Hant"];

/** The system colour schemes the app follows when the reader chose no theme (H-013). */
export const schemes = ["light", "dark"] as const;
export type Scheme = (typeof schemes)[number];
/** What DESIGN.md's Timetable White (the sheet) computes to in each theme. */
export const sheet = { light: "rgb(255, 255, 255)", dark: "rgb(23, 23, 23)" } as const;

/** The routes there are; pages that need a session show their signed-out state. */
export const routes = ["/", "/projects", "/projects/prj_01M4YDQK00TA00000000000001", "/models/mdl_01M4YDQK00TA00000000000001", "/no-such-page", "/ui-gallery", "/auth/error?reason=expired"];

/** Opens a route in a language, and in a remembered theme if one is given (else the system's). */
export async function open(page: Page, path: string, locale: Locale, theme?: Scheme): Promise<void> {
  await page.context().addCookies([{ name: "geminus_locale", value: locale, url: "http://127.0.0.1:3100" }]);
  if (theme) await page.context().addCookies([{ name: "geminus_theme", value: theme, url: "http://127.0.0.1:3100" }]);
  await page.goto(path);
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  // Long tables render after hydration (ADR-021); the web fonts load as text needs them.
  await page.waitForLoadState("networkidle");
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
}

/** axe-core's WCAG 2.0–2.2 A/AA violations, contrast and target size included. */
export async function violations(page: Page): Promise<string[]> {
  const r = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  return r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}

/** The focused element has a visible outline of at least 2 px, or is inside an element that does. */
export async function focusIsVisible(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    for (let el: Element | null = document.activeElement; el && el !== document.body; el = el.parentElement) {
      const s = getComputedStyle(el);
      if (s.outlineStyle !== "none" && parseFloat(s.outlineWidth) >= 2) return true;
    }
    return false;
  });
}

/**
 * Table cells whose content is wider than the cell: "overlaps" paint over
 * the next cell, "clipped" are cut off by the cell (only the row's text
 * value has all of it).
 */
export async function cellOverflow(page: Page): Promise<{ overlaps: string[]; clipped: string[] }> {
  return page.evaluate(() => {
    const overlaps: string[] = [];
    const clipped: string[] = [];
    for (const cell of document.querySelectorAll<HTMLElement>("[role=gridcell], [role=rowheader], [role=columnheader]")) {
      if (cell.scrollWidth <= cell.clientWidth + 1) continue;
      const text = (cell.textContent ?? "").trim();
      (getComputedStyle(cell).overflowX === "visible" ? overlaps : clipped).push(text);
    }
    return { overlaps, clipped };
  });
}
