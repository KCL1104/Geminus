// The signed-in UI bootstrap in a browser (T-043 follow-up, D-045, H-029):
// sign in through the BFF and the test-only provider (fake/servers.mjs,
// never a real one), the bootstrap's GET /v1/me, the organization (the only
// one by itself; of several, a choice remembered for the browser session)
// sent as X-Organization-ID, the pages with the API stand-in's data up to
// the model in the 2D editor, viewer and editor, sign-out, an ended session
// and no access. Accessibility, simplicity and strings of the signed-in
// views are in a11y.spec.ts, simplicity.spec.ts and strings.spec.ts.
import type { APIRequestContext, Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { APP, focusIsVisible, open, PROVIDER, settled, signInAs, tenant } from "./helpers";

type ApiCall = { method: string; path: string; org: string | null; cookie: string | null; hasAuth: boolean };
type ProviderState = { revoked: string[]; logouts: number; apiCalls: ApiCall[]; refreshStates: Record<string, "live" | "spent" | "revoked"> };
const providerState = async (request: APIRequestContext): Promise<ProviderState> => (await request.get(`${PROVIDER}/__state`)).json();
/** The app's cookies (Playwright leaves Secure cookies out of cookies(APP) on http). */
const appCookies = async (page: Page) => (await page.context().cookies()).filter((c) => c.domain === "127.0.0.1");
const sessionCookie = async (page: Page) => (await appCookies(page)).find((c) => c.name === "__Host-geminus_session");

test.beforeEach(async ({ request }) => {
  await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
});

test.describe("at 1280px", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("sign in from the signed-out page, the only organization by itself, then projects, a project and the model in the editor", async ({ page, request }) => {
    await request.post(`${PROVIDER}/__subject`, { data: tenant.editor });
    await open(page, "/projects", "en");
    await expect(page.getByRole("heading", { name: "Sign in required" })).toBeVisible();
    const signIn = page.getByRole("link", { name: "Sign in", exact: true });
    await expect(signIn).toHaveCount(1);
    await expect(signIn).toHaveAttribute("href", "/auth/login?return_to=%2Fprojects");
    const before = (await providerState(request)).apiCalls.length;
    await signIn.click();
    await page.waitForURL(`${APP}/projects`);
    await settled(page, "en");

    await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    // One organization: no choice to make, no switcher.
    await expect(page.getByRole("heading", { name: "Choose an organization" })).toHaveCount(0);
    await expect(page.locator("header [aria-haspopup=listbox]")).toHaveCount(2); // language and theme only

    await page.getByRole("link", { name: "SMT line 1" }).click();
    await page.waitForURL(`${APP}/projects/${tenant.PRJ_LINE}`);
    await page.getByRole("link", { name: tenant.modelName }).click();
    await page.waitForURL(`${APP}/models/${tenant.MODEL}`);
    await settled(page, "en");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(tenant.modelName);
    // The editor shows the draft's steps, and an editor can change them.
    await expect(page.locator(".react-flow__node")).not.toHaveCount(0);
    await expect(page.locator("[data-draft-status]")).toHaveText("All changes saved. Draft revision 3.");
    await expect(page.getByRole("button", { name: "Add step" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Save draft" })).toBeVisible();
    await expect(page.getByText("You can view this model but not change it.")).toHaveCount(0);

    // /me selected no organization; every tenant call selected Line Works,
    // with the token the BFF added and no cookie.
    const calls = (await providerState(request)).apiCalls.slice(before);
    expect(calls.filter((c) => c.path === "/v1/me").map((c) => c.org)).toEqual([null]);
    const scoped = calls.filter((c) => c.path !== "/v1/me");
    expect(scoped.map((c) => c.path)).toEqual(expect.arrayContaining(["/v1/projects", `/v1/projects/${tenant.PRJ_LINE}/models`, `/v1/models/${tenant.MODEL}`, `/v1/models/${tenant.MODEL}/draft`]));
    for (const c of scoped) expect(c, c.path).toMatchObject({ org: tenant.LINE, cookie: null, hasAuth: true });
    // No token reaches anything a script reads.
    const visible = await page.evaluate(() => ({ html: document.documentElement.outerHTML, cookie: document.cookie, local: JSON.stringify({ ...localStorage }), session: JSON.stringify({ ...sessionStorage }) }));
    expect(`${visible.html}${visible.cookie}${visible.local}${visible.session}`).not.toMatch(/\b(at|rt)-[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{10,}/);
    expect(visible.cookie).not.toContain("geminus_session");
  });

  test("a viewer gets the model read-only", async ({ page }) => {
    await signInAs(page, tenant.viewer, `/models/${tenant.MODEL}`);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(tenant.modelName);
    await expect(page.locator(".react-flow__node")).not.toHaveCount(0);
    await expect(page.getByText("You can view this model but not change it.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Add step" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Save draft" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Publish version" })).toHaveCount(0);
  });

  test("the sign-in returns to the page it was offered on", async ({ page, request }) => {
    await request.post(`${PROVIDER}/__subject`, { data: tenant.editor });
    await open(page, `/projects/${tenant.PRJ_LINE}`, "en");
    await page.getByRole("link", { name: "Sign in", exact: true }).click();
    await page.waitForURL(`${APP}/projects/${tenant.PRJ_LINE}`);
    await settled(page, "en");
    await expect(page.getByRole("link", { name: tenant.modelName })).toBeVisible();
  });

  test("a return path to another site is the start page", async ({ page, request }) => {
    await request.post(`${PROVIDER}/__subject`, { data: tenant.editor });
    for (const to of ["//evil.test/x", "https://evil.test/", "/\\evil.test", "javascript:alert(1)"]) {
      await page.goto(`/auth/login?return_to=${encodeURIComponent(to)}`);
      await page.waitForURL(`${APP}/`);
      expect(new URL(page.url()).origin, to).toBe(APP);
    }
  });

  test("several organizations: a choice first, sent and remembered for the session, then switched from the strip", async ({ page, request }) => {
    await signInAs(page, tenant.multi, "/projects");
    await expect(page.getByRole("heading", { name: "Choose an organization" })).toBeVisible();
    const before = (await providerState(request)).apiCalls;
    expect(before.at(-1)?.path, "nothing of a tenant before the choice").toBe("/v1/me");
    await expect(page.locator("header [aria-haspopup=listbox]")).toHaveCount(2);
    await expect(page.getByRole("listitem").filter({ hasText: "Line Works" })).toContainText("Owner");
    await expect(page.getByRole("listitem").filter({ hasText: "Test Bay" })).toContainText("Viewer");

    await page.getByRole("button", { name: "Test Bay" }).click();
    await expect(page.getByRole("link", { name: "Burn-in bay" })).toBeVisible();
    expect((await providerState(request)).apiCalls.at(-1)).toMatchObject({ path: "/v1/projects", org: tenant.BAY });
    expect((await appCookies(page)).find((c) => c.name === "geminus_organization")).toMatchObject({ value: tenant.BAY, path: "/", expires: -1 });

    // Remembered: the next page load goes straight to it.
    await page.reload();
    await settled(page, "en");
    await expect(page.getByRole("link", { name: "Burn-in bay" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Choose an organization" })).toHaveCount(0);

    // The strip's switcher: a choice applies at once, on the same page.
    const switcher = page.getByRole("button", { name: /Organization$/ });
    await expect(switcher).toHaveAccessibleName("Test Bay Organization");
    await switcher.click();
    await page.getByRole("option", { name: "Line Works" }).click();
    await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
    await expect(page).toHaveURL(`${APP}/projects`);
    expect((await providerState(request)).apiCalls.at(-1)).toMatchObject({ path: "/v1/projects", org: tenant.LINE });
    await expect(switcher).toHaveAccessibleName("Line Works Organization");
  });

  test("sign-out ends the session at the BFF and the provider, and the page offers the sign-in again", async ({ page, request }) => {
    await signInAs(page, tenant.multi, "/projects", "en", { organization: tenant.LINE });
    await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
    const before = await providerState(request);
    await page.getByRole("button", { name: "Sign out" }).click();
    await page.waitForURL(`${APP}/`);
    const after = await providerState(request);
    expect(after.logouts).toBe(before.logouts + 1);
    expect(after.revoked.length).toBe(before.revoked.length + 1);
    expect(after.refreshStates[after.revoked.at(-1) as string]).toBe("revoked");
    expect(await sessionCookie(page)).toBeUndefined();
    expect((await appCookies(page)).find((c) => c.name === "geminus_organization"), "the organization is forgotten").toBeUndefined();
    await expect(page.getByRole("button", { name: "Sign out" })).toHaveCount(0);
    await page.goto("/projects");
    await settled(page, "en");
    await expect(page.getByRole("heading", { name: "Sign in required" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Sign in", exact: true })).toBeVisible();
  });

  test("an ended session (the refresh is refused) shows the sign-in, and signing in again brings the data back", async ({ page, request }) => {
    // Short access tokens: the BFF refreshes before each call.
    await request.post(`${PROVIDER}/__access-seconds`, { data: "30" });
    await signInAs(page, tenant.editor, "/projects");
    await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
    // The provider ends the session: every live refresh token is revoked.
    for (const [token, s] of Object.entries((await providerState(request)).refreshStates)) {
      if (s === "live") await request.post(`${PROVIDER}/oauth/revoke`, { form: { token } });
    }
    await page.reload();
    await settled(page, "en");
    await expect(page.getByRole("heading", { name: "Sign in required" })).toBeVisible();
    await expect(page.getByRole("link", { name: "SMT line 1" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Sign out" })).toHaveCount(0);
    expect(await sessionCookie(page), "the BFF cleared the ended session").toBeUndefined();
    await page.getByRole("link", { name: "Sign in", exact: true }).click();
    await page.waitForURL(`${APP}/projects`);
    await settled(page, "en");
    await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
  });

  for (const [who, subject] of [["a user without memberships", tenant.noMember], ["an identity Geminus does not know (403)", tenant.unknown]] as const) {
    test(`${who} sees the permission state, can sign out, and nothing of a tenant is asked`, async ({ page, request }) => {
      const before = (await providerState(request)).apiCalls.length;
      await signInAs(page, subject, "/projects");
      const alert = page.locator('[data-problem="forbidden"]');
      await expect(alert).toContainText("You do not have permission to do this");
      await expect(alert).toContainText("This sign-in has no access to any organization in Geminus.");
      await expect(page.getByRole("link", { name: "Sign in", exact: true })).toHaveCount(0);
      await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
      expect((await providerState(request)).apiCalls.slice(before).map((c) => c.path)).toEqual(["/v1/me"]);
    });
  }
});

test.describe("with the keyboard only", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  async function tabTo(page: Page, name: string): Promise<void> {
    for (let i = 0; i < 30; i++) {
      await page.keyboard.press("Tab");
      expect(await focusIsVisible(page), "focus is visible").toBe(true);
      const label = await page.evaluate(() => (document.activeElement as HTMLElement | null)?.innerText?.trim() ?? "");
      if (label === name) return;
    }
    throw new Error(`never reached ${name}`);
  }

  test("sign in, choose an organization and sign out", async ({ page, request }) => {
    await request.post(`${PROVIDER}/__subject`, { data: tenant.multi });
    await open(page, "/projects", "en");
    await page.locator("main").focus();
    await tabTo(page, "Sign in");
    await page.keyboard.press("Enter");
    await page.waitForURL(`${APP}/projects`);
    await settled(page, "en");
    await page.locator("main").focus();
    await tabTo(page, "Test Bay");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("link", { name: "Burn-in bay" })).toBeVisible();
    await page.locator("main").focus();
    await tabTo(page, "Burn-in bay");
    // The strip: back from the page to its last control.
    await page.keyboard.press("Shift+Tab");
    for (let i = 0; i < 6 && (await page.evaluate(() => (document.activeElement as HTMLElement | null)?.innerText?.trim())) !== "Sign out"; i++) {
      await page.keyboard.press("Shift+Tab");
    }
    await expect(page.locator(":focus")).toHaveText("Sign out");
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    await page.waitForURL(`${APP}/`);
    expect(await sessionCookie(page)).toBeUndefined();
  });
});
