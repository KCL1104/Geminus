import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

export type Locale = "en" | "zh-Hant";

/**
 * H-051, H-065: whether this run's build offers Muse Wonder 1.0
 * (GEMINUS_E2E_OFFER_WONDER=1 builds it with NEXT_PUBLIC_GEMINUS_OFFER_WONDER=1,
 * playwright.config.ts); production's build does not.
 */
export const offersWonder = process.env.GEMINUS_E2E_OFFER_WONDER === "1";

/** The results view's review page (T-032, gallery only), one address per state. */
export const resultsPreviews = ["/ui-gallery/results", "/ui-gallery/results?state=incomparable", "/ui-gallery/results?state=running", "/ui-gallery/results?state=failed"];
/** The AI proposal review's review page (T-031, gallery only): the samples whose look differs (the others are in proposal.spec.ts). */
export const proposalPreviews = [
  "/ui-gallery/proposal", "/ui-gallery/proposal?state=scenario", "/ui-gallery/proposal?state=new-steps", "/ui-gallery/proposal?state=photo", "/ui-gallery/proposal?state=needs_input",
  "/ui-gallery/proposal?state=invalid", "/ui-gallery/proposal?state=failed", "/ui-gallery/proposal?state=stale", "/ui-gallery/proposal?state=conflicted",
];
export const locales: Locale[] = ["en", "zh-Hant"];

/** The system colour schemes the app follows when the reader chose no theme (H-013). */
export const schemes = ["light", "dark"] as const;
export type Scheme = (typeof schemes)[number];
/** What DESIGN.md's Ground (the page under the cards, H-041) computes to in each theme. */
export const sheet = { light: "rgb(250, 250, 250)", dark: "rgb(10, 10, 10)" } as const;

/** The routes there are; pages that need a session show their signed-out state. */
export const routes = [
  "/", "/projects", "/projects/prj_01M4YDQK00TA00000000000001", "/models/mdl_01M4YDQK00TA00000000000001", "/no-such-page", "/ui-gallery", "/ui-gallery/editor", "/auth/error?reason=expired",
  // T-032: a scenario and its runs' results (signed out), and the results view's review page in each state.
  "/scenarios/scn_01M4YDQK00TA00000000000001",
  "/scenarios/scn_01M4YDQK00TA00000000000001/results?baseline=sim_01M4YDQK00TA00000000000001&scenario=sim_01M4YDQK00TA00000000000002",
  ...resultsPreviews,
  // T-031: a proposal (signed out), and the proposal review's review page in its samples.
  "/proposals/aip_01M4YDQK00TA00000000000001",
  ...proposalPreviews,
];

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

export const APP = "http://127.0.0.1:3100";
export const PROVIDER = "http://127.0.0.1:3101";

/**
 * The fake API's tenant data (fake/servers.mjs): a subject per kind of
 * user, their organizations, a project each, and in Line Works a model (the
 * contract fixture valid-draft-with-issues.json).
 */
export const tenant = {
  editor: "auth0|e2e-editor",
  viewer: "auth0|e2e-viewer",
  multi: "auth0|e2e-multi",
  noMember: "auth0|e2e-nomember",
  unknown: "auth0|unknown",
  LINE: "org_01M4YDQK00TA00000000000010",
  BAY: "org_01M4YDQK00TA00000000000020",
  PRJ_LINE: "prj_01M4YDQK00TA00000000000011",
  PRJ_BAY: "prj_01M4YDQK00TA00000000000021",
  MODEL: "mdl_01M4YDQK004AGVNEGJQJ1NMM0N",
  modelName: "Synthetic Software Delivery Pipeline",
} as const;

/** The signed-in views the browser checks run on (subject, path): every page with data, and the bootstrap's own. */
export const signedInViews: { name: string; subject: string; path: string; organization?: string }[] = [
  { name: "projects", subject: tenant.editor, path: "/projects" },
  { name: "project", subject: tenant.editor, path: `/projects/${tenant.PRJ_LINE}` },
  { name: "model (editor)", subject: tenant.editor, path: `/models/${tenant.MODEL}` },
  { name: "model (viewer)", subject: tenant.viewer, path: `/models/${tenant.MODEL}` },
  // H-042: the rail's other sections of the model.
  { name: "model scenarios", subject: tenant.editor, path: `/models/${tenant.MODEL}/scenarios` },
  { name: "model AI proposals", subject: tenant.editor, path: `/models/${tenant.MODEL}/proposals` },
  { name: "model runs", subject: tenant.editor, path: `/models/${tenant.MODEL}/runs` },
  { name: "choose an organization", subject: tenant.multi, path: "/projects" },
  { name: "projects with the switcher", subject: tenant.multi, path: "/projects", organization: tenant.LINE },
  { name: "no access", subject: tenant.noMember, path: "/projects" },
];

/**
 * Signs in as subject through the BFF and the test provider (never a real
 * one), in a language (and a remembered theme or organization if given),
 * and lands on path once the bootstrap has loaded /me and the page its data.
 */
export async function signInAs(page: Page, subject: string, path: string, locale: Locale = "en", opts: { theme?: Scheme; organization?: string } = {}): Promise<void> {
  await page.request.post(`${PROVIDER}/__subject`, { data: subject });
  await page.context().addCookies([{ name: "geminus_locale", value: locale, url: APP }]);
  if (opts.theme) await page.context().addCookies([{ name: "geminus_theme", value: opts.theme, url: APP }]);
  if (opts.organization) await page.context().addCookies([{ name: "geminus_organization", value: opts.organization, url: APP }]);
  await page.goto(`/auth/login?return_to=${encodeURIComponent(path)}`);
  await page.waitForURL(APP + path);
  await settled(page, locale);
}

/** The page in its language, its loads done (nothing busy) and its fonts in. */
export async function settled(page: Page, locale: Locale): Promise<void> {
  await expect(page.locator("html")).toHaveAttribute("lang", locale);
  await page.waitForLoadState("networkidle");
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready.then(() => undefined));
}

/**
 * Loads every picture the page shows now, lazy ones included (the landing's
 * product screens, H-048), and waits until each is decoded; returns the
 * shown pictures' file names. A picture CSS hides (the other theme's) is
 * left alone, so it is never fetched.
 */
export async function loadPictures(page: Page): Promise<string[]> {
  return page.evaluate(async () => {
    const shown = [...document.querySelectorAll("img")].filter((i) => i.getBoundingClientRect().width > 0);
    for (const img of shown) img.loading = "eager";
    await Promise.all(shown.map((i) => i.decode()));
    return shown.map((i) => new URL(i.currentSrc).pathname);
  });
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

/** H-042: opens the rail's account menu (signed in) and returns its dialog: who is signed in, the organizations, the language, the theme and Sign out. */
export async function openAccount(page: Page, locale: Locale = "en") {
  await page.getByRole("button", { name: locale === "en" ? "Account and settings" : "帳號與設定" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  return dialog;
}
