import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

export type Locale = "en" | "zh-Hant";
export const locales: Locale[] = ["en", "zh-Hant"];

/** The routes there are; pages that need a session show their signed-out state. */
export const routes = ["/", "/projects", "/projects/prj_01M4YDQK00TA00000000000001", "/models/mdl_01M4YDQK00TA00000000000001", "/no-such-page", "/ui-gallery"];

export async function open(page: Page, path: string, locale: Locale): Promise<void> {
  await page.context().addCookies([{ name: "geminus_locale", value: locale, url: "http://127.0.0.1:3100" }]);
  await page.goto(path);
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  // Long tables render after hydration (ADR-021).
  await page.waitForLoadState("networkidle");
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
