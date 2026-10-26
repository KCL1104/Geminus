// T-057: the web app's security headers in a browser (lib/security/headers.ts,
// proxy.ts; docs/SECURITY.md §3). Every route the suite visits, in both
// languages, signed out, and every signed-in view, signed in through the BFF
// and the test provider: the headers are there, the page's scripts carry the
// response's nonce, and nothing is reported (the fixture in fixtures.ts
// checks that for every test of the suite). The BFF's route handlers and
// static files carry the same headers; each response has its own nonce; and
// the policy refuses what it should (an inline event handler, a style
// element without the nonce, eval, another origin's image or fetch, a form
// to another origin, a <base>, being framed), which also shows the watch
// sees a violation.
import type { Page, Response } from "@playwright/test";
import { expect, expectedHeaders, headerProblems, nonceOf, test } from "./fixtures";
import { APP, locales, PROVIDER, routes, settled, signedInViews, signInAs } from "./helpers";

/** The nonce in the response's policy; its HTML's scripts and links carry it, and so does the page's csp-nonce meta. */
async function checkDocument(page: Page, response: Response | null, where: string): Promise<string> {
  expect(response, where).not.toBeNull();
  const headers = await response!.allHeaders();
  expect(headerProblems(headers, where)).toEqual([]);
  const nonce = nonceOf(headers["content-security-policy"]);
  expect(nonce, where).toBeDefined();
  const html = await response!.text();
  const tags = [...html.matchAll(/<(script|link)\b[^>]*>/g)].map((m) => m[0]).filter((t) => t.startsWith("<script") || /rel="(stylesheet|preload)"/.test(t));
  expect(tags.length, `${where}: scripts in the HTML`).toBeGreaterThan(3);
  expect(tags.filter((t) => !t.includes(` nonce="${nonce}"`)), `${where}: every script and stylesheet carries the nonce`).toEqual([]);
  // The browser hides the attribute; the property keeps it (React Aria reads it).
  expect(await page.evaluate(() => (document.querySelector('meta[name="csp-nonce"]') as HTMLMetaElement | null)?.nonce)).toBe(nonce);
  return nonce!;
}

for (const locale of locales) {
  test(`${locale}: every route carries the headers and its own nonce, and reports nothing`, async ({ page }) => {
    await page.context().addCookies([{ name: "geminus_locale", value: locale, url: APP }]);
    const nonces = new Set<string>();
    for (const path of routes) {
      const response = await page.goto(path);
      // As open() waits (helpers.ts): some previews stay busy on purpose (a run in progress).
      await expect(page.locator("html")).toHaveAttribute("lang", locale);
      await page.waitForLoadState("networkidle");
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      nonces.add(await checkDocument(page, response, path));
      // The page's scripts ran under the policy: React Aria added its style
      // element, with the nonce, once the page hydrated.
      await expect(page.locator("style#react-aria-pressable-style")).toHaveCount(1);
      expect(await page.evaluate(() => (document.getElementById("react-aria-pressable-style") as HTMLStyleElement).nonce)).not.toBe("");
    }
    expect(nonces.size, "a new nonce for every page").toBe(routes.length);
  });

  test(`${locale}: the signed-in views carry the headers, through the sign-in's redirects`, async ({ page }) => {
    const documents: Response[] = [];
    page.on("response", (r) => {
      if (r.request().resourceType() === "document" && r.url().startsWith(`${APP}/`)) documents.push(r);
    });
    for (const v of signedInViews) {
      documents.length = 0;
      await signInAs(page, v.subject, v.path, locale, { organization: v.organization });
      // The login's and the callback's 303s, then the page.
      expect(documents.map((d) => [d.status(), new URL(d.url()).pathname]), v.name).toEqual([[303, "/auth/login"], [303, "/auth/callback"], [200, v.path]]);
      for (const d of documents) expect(headerProblems(await d.allHeaders(), `${v.name}: ${d.url()}`)).toEqual([]);
      await checkDocument(page, documents.at(-1)!, v.name);
      await page.context().clearCookies();
    }
  });
}

test("the sign-in link works with no Referer sent, to the app or to the provider", async ({ page, request }) => {
  await request.post(`${PROVIDER}/__subject`, { data: signedInViews[0]!.subject });
  const referers: [string, string | undefined][] = [];
  page.on("request", (r) => {
    if (r.isNavigationRequest()) void r.allHeaders().then((h) => referers.push([new URL(r.url()).pathname, h.referer]));
  });
  await page.goto("/projects");
  await settled(page, "en");
  await page.getByRole("link", { name: "Sign in", exact: true }).click();
  await page.waitForURL(`${APP}/projects`);
  await settled(page, "en");
  await expect(page.getByRole("link", { name: "SMT line 1" })).toBeVisible();
  await expect.poll(() => referers.map(([p]) => p)).toEqual(["/projects", "/auth/login", "/authorize", "/auth/callback", "/projects"]);
  expect(referers.filter(([, r]) => r !== undefined), "Referrer-Policy: no-referrer").toEqual([]);
});

test("the BFF's route handlers, error pages and static files carry the same headers", async ({ page, request }) => {
  await page.goto("/");
  const script = await page.evaluate(() => (document.querySelector("script[src^='/_next/static/']") as HTMLScriptElement).getAttribute("src"));
  const icon = await page.evaluate(() => document.querySelector("link[rel=icon]")?.getAttribute("href"));
  const cases: [string, () => Promise<import("@playwright/test").APIResponse>, number][] = [
    ["GET /auth/login", () => request.get("/auth/login?return_to=/projects", { maxRedirects: 0 }), 303],
    ["GET /auth/callback without a login", () => request.get("/auth/callback?code=x&state=y", { maxRedirects: 0 }), 303],
    ["POST /auth/logout cross-site", () => request.post("/auth/logout"), 403],
    ["GET /api/v1/me signed out", () => request.get("/api/v1/me"), 401],
    ["POST /api/v1/projects cross-site", () => request.post("/api/v1/projects", { data: "{}", headers: { Origin: "https://evil.example" } }), 403],
    ["GET /auth/error", () => request.get("/auth/error?reason=expired"), 200],
    ["GET a missing page", () => request.get("/no-such-page"), 404],
    ["GET the icon", () => request.get(icon!), 200],
    ["GET a script", () => request.get(script!), 200],
  ];
  for (const [name, send, status] of cases) {
    const r = await send();
    expect(r.status(), name).toBe(status);
    expect(headerProblems(r.headers(), name)).toEqual([]);
  }
  // A client's own nonce and policy are replaced, never used.
  const forged = await request.get("/", { headers: { "x-nonce": "AAECAwQFBgcICQoLDA0ODw==", "content-security-policy": "script-src 'nonce-AAECAwQFBgcICQoLDA0ODw=='" } });
  const html = await forged.text();
  expect(html).not.toContain("AAECAwQFBgcICQoLDA0ODw==");
  expect(html).toContain(`nonce="${nonceOf(forged.headers()["content-security-policy"])}"`);
  // Two requests, two nonces, each on its own HTML.
  const [a, b] = [await request.get("/"), await request.get("/")];
  const [na, nb] = [nonceOf(a.headers()["content-security-policy"]), nonceOf(b.headers()["content-security-policy"])];
  expect(na).not.toBe(nb);
  expect(await a.text()).toContain(`nonce="${na}"`);
  expect(await b.text()).not.toContain(na!);
  expect(Object.keys(expectedHeaders)).toHaveLength(5);
});

test("the policy refuses inline code without the nonce, eval, other origins, forms elsewhere, <base> and framing", async ({ page, csp }) => {
  await page.goto("/");
  await settled(page, "en");
  const nonce = await page.evaluate(() => (document.querySelector('meta[name="csp-nonce"]') as HTMLMetaElement).nonce);
  csp.allow(/script-src|style-src-elem|img-src|connect-src|form-action|base-uri|frame-ancestors|Refused to (execute|apply|load|connect|send form|set the document's base|frame)|eval/);
  const icon = await page.evaluate(() => document.querySelector("link[rel=icon]")!.getAttribute("href")!);
  const outcome = await page.evaluate(async ([n, src]) => {
    const w = window as unknown as Record<string, unknown>;
    // An inline event handler, what injected markup would carry, does not
    // run (the image itself is the app's own and loads). ('strict-dynamic'
    // trusts a script element a running script adds; the nonce is for the
    // scripts in the HTML.)
    document.body.insertAdjacentHTML("beforeend", `<img id="csp-probe" alt="" src="${src}" onload="window.__inline = 'ran'">`);
    const probe = document.getElementById("csp-probe") as HTMLImageElement;
    await new Promise((resolve) => (probe.complete ? resolve(null) : probe.addEventListener("load", resolve)));
    probe.remove();
    // A script with the nonce runs; eval is refused (in a later task:
    // DevTools allows eval while it evaluates this function itself).
    const s = document.createElement("script");
    s.nonce = n;
    s.textContent = "window.__nonced = 'ran'; setTimeout(() => { try { window.__eval = eval('1 + 1'); } catch (e) { window.__eval = e.name; } }, 0);";
    document.head.append(s);
    await new Promise((resolve) => setTimeout(resolve, 50));
    const style = (nonce?: string) => {
      const s = document.createElement("style");
      if (nonce) s.nonce = nonce;
      s.textContent = nonce ? "#main { outline-offset: 7px; }" : "#main { outline-offset: 5px; }";
      document.head.append(s);
      return getComputedStyle(document.getElementById("main")!).outlineOffset;
    };
    const unstyled = style();
    const styled = style(n);
    const img = await new Promise<string>((resolve) => {
      const i = new Image();
      i.onload = () => resolve("loaded");
      i.onerror = () => resolve("refused");
      i.src = "http://127.0.0.1:3101/.well-known/jwks.json";
    });
    const fetched = await fetch("http://127.0.0.1:3101/.well-known/jwks.json").then(() => "fetched", (e: Error) => e.name);
    const base = document.createElement("base");
    base.href = "http://127.0.0.1:3101/";
    document.head.append(base);
    return { inline: w.__inline ?? null, nonced: w.__nonced ?? null, unstyled, styled, img, fetched, baseURI: document.baseURI };
  }, [nonce, icon] as const);
  expect(outcome).toEqual({ inline: null, nonced: "ran", unstyled: "0px", styled: "7px", img: "refused", fetched: "TypeError", baseURI: `${APP}/` });
  expect(await page.waitForFunction(() => (window as unknown as { __eval?: unknown }).__eval).then((h) => h.jsonValue())).toBe("EvalError");
  // A form to another origin is not sent (the sign-in is a link and a 303, not a form).
  let posted = false;
  page.on("request", (r) => {
    if (r.url().startsWith(PROVIDER) && r.method() === "POST") posted = true;
  });
  await page.evaluate(() => {
    const f = document.createElement("form");
    f.method = "post";
    f.action = "http://127.0.0.1:3101/oauth/token";
    document.body.append(f);
    f.submit();
  });
  await expect.poll(() => csp.reports.some((r) => r.includes("form-action"))).toBe(true);
  expect(posted).toBe(false);
  expect(page.url()).toBe(`${APP}/`);
  // Another page cannot frame the app.
  const framer = await page.context().newPage();
  const framed = framer.waitForResponse(`${APP}/`);
  await framer.setContent(`<iframe src="${APP}/"></iframe>`);
  expect((await framed).status()).toBe(200);
  await expect.poll(() => csp.reports.some((r) => r.includes("frame-ancestors"))).toBe(true);
  const frame = framer.frames().find((f) => f !== framer.mainFrame());
  expect(frame, "the frame is there").toBeDefined();
  expect(await frame!.evaluate(() => document.documentElement.lang).catch(() => "no document"), "the app did not render in the frame").not.toBe("en");
  for (const p of [page, framer]) await p.evaluate(() => 0);
  // The watch saw each refusal.
  for (const report of [/script-src-attr blocked inline/, /script-src blocked eval/, /style-src-elem blocked inline/, /img-src blocked http:\/\/127\.0\.0\.1:3101\//, /connect-src blocked http:\/\/127\.0\.0\.1:3101\//, /form-action/, /base-uri/, /frame-ancestors/]) {
    expect(csp.reports.some((r) => report.test(r)), `reported: ${report} in ${JSON.stringify(csp.reports, null, 1)}`).toBe(true);
  }
});
