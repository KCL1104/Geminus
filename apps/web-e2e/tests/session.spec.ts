// TEST-032 in a browser (T-043, ADR-018): sign-in through the BFF against the
// test provider (fake/servers.mjs), the session cookie's attributes, no token
// the page can read, the API proxy, CSRF refusals, refresh, logout and an
// unknown identity. Never a real provider.
import type { APIRequestContext, Page } from "@playwright/test";
import { expect, test } from "./fixtures";

const APP = "http://127.0.0.1:3100";
const PROVIDER = "http://127.0.0.1:3101";
const ORG = "org_01M4YDQK00TA00000000000000";

type ProviderState = {
  revoked: string[];
  refreshGrants: number;
  codeGrants: number;
  logouts: number;
  apiCalls: { method: string; path: string; cookie: string | null; org: string | null; hasAuth: boolean }[];
  refreshStates: Record<string, "live" | "spent" | "revoked">;
};

/**
 * The app's cookies. Not context.cookies(APP): Playwright leaves Secure
 * cookies out for an http URL, though Chromium sends them to 127.0.0.1.
 */
const appCookies = async (page: Page) => (await page.context().cookies()).filter((c) => c.domain === "127.0.0.1");

const providerState = async (request: APIRequestContext): Promise<ProviderState> => (await request.get(`${PROVIDER}/__state`)).json();

test.beforeEach(async ({ request }) => {
  await request.post(`${PROVIDER}/__subject`, { data: "auth0|e2e-user" });
  await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
});

async function signIn(page: Page) {
  await page.goto("/auth/login?return_to=/projects");
  await page.waitForURL(`${APP}/projects`);
  // The page's own calls (the bootstrap's /me, then the project list) are
  // done before a test counts or inspects what the API saw.
  await page.waitForLoadState("networkidle");
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

/** fetch from the page, as the app's client would. */
function inPage(page: Page, path: string, init: { method?: string; headers?: Record<string, string>; body?: string } = {}) {
  return page.evaluate(
    async ([p, i]) => {
      const r = await fetch(p, { ...i, credentials: "same-origin" });
      return { status: r.status, text: await r.text() };
    },
    [path, init] as const,
  );
}

test("sign-in leaves one HttpOnly __Host- cookie and nothing a script can read", async ({ page }) => {
  await signIn(page);
  const cookies = await appCookies(page);
  const session = cookies.find((c) => c.name === "__Host-geminus_session");
  expect(session).toMatchObject({ httpOnly: true, secure: true, sameSite: "Lax", path: "/", domain: "127.0.0.1" });
  expect(cookies.find((c) => c.name === "__Host-geminus_login")).toBeUndefined();
  const visible = await page.evaluate(() => ({ cookie: document.cookie, local: JSON.stringify({ ...localStorage }), session: JSON.stringify({ ...sessionStorage }) }));
  expect(visible.cookie).not.toContain("geminus_session");
  expect(`${visible.local}${visible.session}`).not.toMatch(/at-|rt-|eyJ/);
});

test("the proxy adds the token, sends no cookie to the API and returns no token", async ({ page, request }) => {
  await signIn(page);
  const r = await inPage(page, "/api/v1/projects", { headers: { "X-Organization-ID": ORG } });
  expect(r).toEqual({ status: 200, text: '{"items":[],"next_cursor":null}' });
  const seen = (await providerState(request)).apiCalls.at(-1);
  expect(seen).toMatchObject({ method: "GET", path: "/v1/projects", cookie: null, org: ORG, hasAuth: true });
  // The API's Set-Cookie never reaches the browser.
  expect((await appCookies(page)).map((c) => c.name).sort()).toEqual(["__Host-geminus_session"]);
});

// T-045 review 1 I2 (T-043's page): a reason the page does not know, even
// a name every object inherits, is the generic failure, not a server error.
test("a sign-in error page with an unknown reason says the sign-in failed, never a 500", async ({ request }) => {
  for (const reason of ["toString", "constructor", "__proto__", "hasOwnProperty", "valueOf", "nonsense"]) {
    const r = await request.get(`/auth/error?reason=${reason}`);
    expect(r.status(), reason).toBe(200);
    expect(await r.text(), reason).toContain("The identity provider&#x27;s answer could not be verified.");
  }
  expect(await (await request.get("/auth/error?reason=expired")).text()).toContain("The sign-in took too long or was started in another browser.");
});

test("without a session the proxy answers 401 in the API's envelope", async ({ page }) => {
  await page.goto("/");
  const r = await inPage(page, "/api/v1/projects");
  expect(r.status).toBe(401);
  expect(JSON.parse(r.text).error.code).toBe("UNAUTHENTICATED");
});

test("a write without the app's header, from another origin or from a cross-site form is refused", async ({ page, request }) => {
  await signIn(page);
  const before = (await providerState(request)).apiCalls.length;
  expect((await inPage(page, "/api/v1/projects", { method: "POST", body: "{}", headers: { "Content-Type": "application/json" } })).status).toBe(403);
  const evil = await page.request.post(`${APP}/api/v1/projects`, { data: "{}", headers: { Origin: "https://evil.example", "X-Geminus-Request": "1" } });
  expect(evil.status()).toBe(403);
  // A form on another site (served here by routing http://evil.test): the
  // browser posts it cross-site, without the SameSite=Lax session cookie,
  // and the BFF refuses it. The same holds for a cross-site logout.
  await page.route("http://evil.test/**", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: `<form method="post" action="${APP}/api/v1/projects"><button id="write">write</button></form>
             <form method="post" action="${APP}/auth/logout"><button id="logout">logout</button></form>`,
    }),
  );
  for (const [button, target] of [
    ["#write", `${APP}/api/v1/projects`],
    ["#logout", `${APP}/auth/logout`],
  ] as const) {
    await page.goto("http://evil.test/");
    const [res] = await Promise.all([page.waitForResponse(target), page.click(button)]);
    expect(res.status()).toBe(403);
    const sent = await res.request().allHeaders();
    // Chromium sends no Sec-Fetch-Site from this http page; Origin and the
    // missing X-Geminus-Request are what the BFF refuses on.
    expect(sent.origin).toBe("http://evil.test");
    expect(sent.cookie ?? "").not.toContain("__Host-geminus_session");
  }
  expect((await providerState(request)).apiCalls.length).toBe(before);
  // The cross-site logout ended nothing.
  await page.goto("/");
  expect((await inPage(page, "/api/v1/projects", { headers: { "X-Organization-ID": ORG } })).status).toBe(200);
});

test("a tampered state or a replayed callback lands on the sign-in error page", async ({ page, request }) => {
  const login = await page.request.get(`${APP}/auth/login`, { maxRedirects: 0 });
  const authorize = await request.get(login.headers().location as string, { maxRedirects: 0 });
  const callback = new URL(authorize.headers().location as string);
  callback.searchParams.set("state", "tampered");
  await page.goto(callback.toString());
  await expect(page).toHaveURL(`${APP}/auth/error?reason=failed`);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Sign-in did not complete");
  expect((await appCookies(page)).find((c) => c.name === "__Host-geminus_session")).toBeUndefined();
  // The login transaction is gone, so the untampered callback fails too.
  callback.searchParams.set("state", new URL(authorize.headers().location as string).searchParams.get("state") as string);
  await page.goto(callback.toString());
  await expect(page).toHaveURL(`${APP}/auth/error?reason=expired`);
});

test("a token close to expiry is refreshed on the server, and the old refresh token is spent", async ({ page, request }) => {
  await request.post(`${PROVIDER}/__access-seconds`, { data: "30" });
  await signIn(page);
  const before = await providerState(request);
  expect((await inPage(page, "/api/v1/projects", { headers: { "X-Organization-ID": ORG } })).status).toBe(200);
  const after = await providerState(request);
  expect(after.refreshGrants).toBe(before.refreshGrants + 1);
  expect(Object.values(after.refreshStates).filter((s) => s === "spent").length).toBe(Object.values(before.refreshStates).filter((s) => s === "spent").length + 1);
});

// The refreshed session reaches the browser even when the API's answer is
// cut after its headers: otherwise its next request would spend the rotated
// refresh token again, which rotation answers by revoking the session.
test("a refresh is kept when the API's answer is cut after its headers", async ({ page, request }) => {
  await request.post(`${PROVIDER}/__access-seconds`, { data: "30" });
  await signIn(page);
  const cookie = async () => (await appCookies(page)).find((c) => c.name === "__Host-geminus_session")?.value;
  const before = { cookie: await cookie(), state: await providerState(request) };
  await request.post(`${PROVIDER}/__api-cut`, { data: "headers" });
  try {
    const cut = await page.evaluate(async () => {
      try {
        const r = await fetch("/api/v1/projects", { headers: { "X-Organization-ID": "org_01M4YDQK00TA00000000000000" } });
        await r.text();
        return "answered";
      } catch {
        return "failed";
      }
    });
    expect(cut).toBe("failed");
  } finally {
    await request.post(`${PROVIDER}/__api-cut`, { data: "" });
  }
  const mid = await providerState(request);
  expect(mid.refreshGrants).toBe(before.state.refreshGrants + 1);
  expect(await cookie()).not.toBe(before.cookie);
  // The new cookie carries the rotated token: no second refresh, no reuse.
  await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
  expect((await inPage(page, "/api/v1/projects", { headers: { "X-Organization-ID": ORG } })).status).toBe(200);
  const after = await providerState(request);
  expect(Object.values(after.refreshStates).filter((s) => s === "revoked").length).toBe(Object.values(before.state.refreshStates).filter((s) => s === "revoked").length);
});

test("logout revokes the refresh token, clears the cookie and signs out at the provider", async ({ page, request }) => {
  await signIn(page);
  const before = await providerState(request);
  const out = await page.evaluate(async () => {
    const r = await fetch("/auth/logout", { method: "POST", headers: { "X-Geminus-Request": "1" } });
    return { status: r.status, body: (await r.json()) as { redirect: string } };
  });
  expect(out.status).toBe(200);
  expect((await appCookies(page)).find((c) => c.name === "__Host-geminus_session")).toBeUndefined();
  const after = await providerState(request);
  expect(after.revoked.length).toBe(before.revoked.length + 1);
  expect(after.refreshStates[after.revoked.at(-1) as string]).toBe("revoked");
  await page.goto(out.body.redirect);
  await page.waitForURL(`${APP}/`);
  expect((await providerState(request)).logouts).toBe(before.logouts + 1);
  expect((await inPage(page, "/api/v1/projects")).status).toBe(401);
});

test("an identity provisioning does not know gets the API's 403", async ({ page, request }) => {
  await request.post(`${PROVIDER}/__subject`, { data: "auth0|unknown" });
  await signIn(page);
  const r = await inPage(page, "/api/v1/projects", { headers: { "X-Organization-ID": ORG } });
  expect(r.status).toBe(403);
  expect(JSON.parse(r.text).error.code).toBe("FORBIDDEN");
});
