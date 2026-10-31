// T-057: every test of the suite runs with the web app's security headers
// watched. Each spec takes `test` and `expect` from here instead of
// @playwright/test; an automatic fixture then fails the test when
//   - the browser reports a Content-Security-Policy violation (the
//     securitypolicyviolation event, which an init script turns into a
//     console error, or the browser's own console message), or a
//     Permissions-Policy error (an unknown feature name), on any page of
//     the test's context, or
//   - a document the app served (pages, redirects, error pages) lacks a
//     security header (lib/security/headers.ts), carries X-Powered-By, or
//     carries Strict-Transport-Security (the suite runs over http).
// A test that provokes a violation on purpose says which with csp.allow.
import { test as base, expect } from "@playwright/test";

export { expect };

const APP = "http://127.0.0.1:3100";

/** The headers every response of the app carries over http (proxy.ts). */
export const expectedHeaders: Record<string, string | RegExp> = {
  "content-security-policy": new RegExp(
    "^default-src 'none'; script-src 'self' 'nonce-([A-Za-z0-9+/]{22}==)' 'strict-dynamic'; style-src 'self' 'unsafe-inline'; " +
      "style-src-elem 'self' 'nonce-\\1'; style-src-attr 'unsafe-inline'; img-src 'self' blob:; font-src 'self'; connect-src 'self'; " +
      "object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'$",
  ),
  "x-content-type-options": "nosniff",
  "referrer-policy": "no-referrer",
  "permissions-policy": /^camera=\(\), microphone=\(\), geolocation=\(\)(, [a-z-]+=\(\))+$/,
  "x-frame-options": "DENY",
};

/** What is wrong with a response's headers (names lower-cased), as readable lines. */
export function headerProblems(headers: Record<string, string>, where: string): string[] {
  const out: string[] = [];
  for (const [name, want] of Object.entries(expectedHeaders)) {
    const got = headers[name];
    if (got === undefined) out.push(`${where}: no ${name}`);
    else if (typeof want === "string" ? got !== want : !want.test(got)) out.push(`${where}: ${name} is ${got}`);
  }
  for (const name of ["x-powered-by", "strict-transport-security"]) if (headers[name] !== undefined) out.push(`${where}: ${name} is ${headers[name]}`);
  return out;
}

/** The nonce in a Content-Security-Policy value. */
export const nonceOf = (policy: string | undefined) => /'nonce-([^']+)'/.exec(policy ?? "")?.[1];

export type SecurityWatch = {
  /** CSP and Permissions-Policy reports so far, one line each. */
  reports: string[];
  /** Reports matching pattern are expected (a test that provokes them). */
  allow: (pattern: RegExp) => void;
};

const REPORT = /Content.Security.Policy|Permissions.Policy|securitypolicyviolation/i;

export const test = base.extend<{ csp: SecurityWatch }>({
  csp: [
    async ({ context }, use) => {
      const reports: string[] = [];
      const allowed: RegExp[] = [];
      const checks: Promise<string[]>[] = [];
      await context.addInitScript(() => {
        window.addEventListener(
          "securitypolicyviolation",
          (e) => console.error(`securitypolicyviolation: ${e.effectiveDirective} blocked ${e.blockedURI || "(inline)"} at ${e.sourceFile || document.URL}:${e.lineNumber}`),
          true,
        );
      });
      context.on("console", (m) => {
        const text = m.text();
        if (REPORT.test(text)) reports.push(`${m.page()?.url() ?? "?"}: ${text}`);
      });
      context.on("response", (r) => {
        if (r.request().resourceType() === "document" && r.url().startsWith(`${APP}/`)) {
          checks.push(r.allHeaders().then((h) => headerProblems(h, `${r.status()} ${r.url()}`), () => []));
        }
      });
      await use({ reports, allow: (p) => void allowed.push(p) });
      // A round trip to each page delivers the console messages sent before it.
      for (const p of context.pages()) await p.evaluate(() => 0).catch(() => undefined);
      expect(reports.filter((r) => !allowed.some((p) => p.test(r))), "no Content-Security-Policy or Permissions-Policy report").toEqual([]);
      expect((await Promise.all(checks)).flat(), "every document the app served has its security headers").toEqual([]);
    },
    { auto: true },
  ],
});
