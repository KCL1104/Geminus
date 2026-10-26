// T-057 (public demo): the web app's response headers. lib/security/headers.ts
// builds them; proxy.ts puts them on every response (pages, the BFF's route
// handlers, static files) with a fresh nonce per request, and hands the
// nonce to Next.js (which puts it on its scripts) and to the root layout
// (the csp-nonce meta React Aria reads for the style elements it adds).
// The browser side (every page the e2e suite visits, no violation) is
// apps/web-e2e/tests/security-headers.spec.ts.
import { NextRequest } from "next/server";
import { unstable_doesMiddlewareMatch } from "next/experimental/testing/server";
import { afterEach, describe, expect, it } from "vitest";
import { contentSecurityPolicy, HSTS, isNonce, newNonce, NONCE_HEADER, PERMISSIONS_POLICY, securityHeaders, servedOverHttps } from "../lib/security/headers";
import * as proxyModule from "../proxy";

const NONCE = "AAECAwQFBgcICQoLDA0ODw==";

/** The policy as directive → sources, in order; a directive given twice fails. */
function directives(policy: string): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const part of policy.split(";")) {
    const [name, ...sources] = part.trim().split(/\s+/);
    if (!name) throw new Error(`empty directive in ${policy}`);
    if (out.has(name)) throw new Error(`${name} given twice`);
    out.set(name, sources);
  }
  return out;
}

const production = (https = false) => directives(contentSecurityPolicy({ nonce: NONCE, https, development: false }));
const headerMap = (list: [string, string][]) => {
  const names = list.map(([n]) => n.toLowerCase());
  expect(new Set(names).size, "no header twice").toBe(names.length);
  return new Map(list.map(([n, v]) => [n.toLowerCase(), v]));
};

describe("the nonce", () => {
  it("is 16 random bytes in base64, new every time", () => {
    const seen = new Set<string>();
    for (let i = 0; i < 2000; i++) {
      const n = newNonce();
      expect(n).toMatch(/^[A-Za-z0-9+/]{22}==$/);
      expect(Buffer.from(n, "base64")).toHaveLength(16);
      seen.add(n);
    }
    expect(seen.size).toBe(2000);
  });

  it("is accepted only in that shape, so it can never end a source or a directive", () => {
    expect(isNonce(NONCE)).toBe(true);
    expect(isNonce(newNonce())).toBe(true);
    for (const bad of [null, undefined, "", "abc", `${NONCE} `, `${NONCE.slice(0, -2)}'; script-src *`, "AAECAwQFBgcICQoLDA0ODw=", "AAECAwQFBgcICQoLDA0ODwAA", `AAECAwQFBgcICQoLDA0OD"==`, "AAECAwQFBgcICQoLDA0OD-=="]) {
      expect(isNonce(bad), String(bad)).toBe(false);
    }
    expect(() => contentSecurityPolicy({ nonce: "x'; script-src *", https: false, development: false })).toThrow(/nonce/);
  });
});

describe("the Content-Security-Policy", () => {
  it("allows only the app's own origin, and scripts only with the request's nonce", () => {
    const p = production();
    expect([...p.keys()]).toEqual([
      "default-src", "script-src", "style-src", "style-src-elem", "style-src-attr", "img-src", "font-src", "connect-src",
      "object-src", "base-uri", "form-action", "frame-ancestors",
    ]);
    expect(p.get("default-src")).toEqual(["'none'"]);
    // Next.js reads the nonce from the first directive starting with
    // script-src; 'strict-dynamic' trusts what those scripts load ('self'
    // only for browsers without it). No eval, no inline script.
    expect(p.get("script-src")).toEqual(["'self'", `'nonce-${NONCE}'`, "'strict-dynamic'"]);
    expect(p.get("style-src-elem")).toEqual(["'self'", `'nonce-${NONCE}'`]);
    // Style attributes (React Aria's hidden helpers, the table's height on
    // the server): no nonce can reach an attribute.
    expect(p.get("style-src-attr")).toEqual(["'unsafe-inline'"]);
    // Only for browsers without the two above: there a nonce would turn
    // 'unsafe-inline' off for the attributes too.
    expect(p.get("style-src")).toEqual(["'self'", "'unsafe-inline'"]);
    expect(p.get("img-src")).toEqual(["'self'"]);
    expect(p.get("font-src")).toEqual(["'self'"]);
    expect(p.get("connect-src")).toEqual(["'self'"]);
    expect(p.get("object-src")).toEqual(["'none'"]);
    expect(p.get("base-uri")).toEqual(["'none'"]);
    expect(p.get("form-action")).toEqual(["'self'"]);
    expect(p.get("frame-ancestors")).toEqual(["'none'"]);
  });

  it("never allows eval, inline script, a wildcard or a scheme in production", () => {
    for (const https of [false, true]) {
      const policy = contentSecurityPolicy({ nonce: NONCE, https, development: false });
      expect(policy).not.toMatch(/unsafe-eval|wasm-unsafe-eval|unsafe-hashes|\*|data:|blob:|https?:|ws:|report-/);
      const p = directives(policy);
      for (const [name, sources] of p) {
        if (name.startsWith("script-src")) expect(sources, name).not.toContain("'unsafe-inline'");
      }
      expect([...p].filter(([, s]) => s.includes("'unsafe-inline'")).map(([n]) => n)).toEqual(["style-src", "style-src-attr"]);
    }
  });

  it("upgrades insecure requests only when the app is served over https", () => {
    expect(production(false).has("upgrade-insecure-requests")).toBe(false);
    const p = production(true);
    expect(p.get("upgrade-insecure-requests")).toEqual([]);
    expect([...p.keys()].at(-1)).toBe("upgrade-insecure-requests");
  });

  it("adds eval for next dev only (React's debugging aids)", () => {
    const dev = directives(contentSecurityPolicy({ nonce: NONCE, https: false, development: true }));
    expect(dev.get("script-src")).toEqual(["'self'", `'nonce-${NONCE}'`, "'strict-dynamic'", "'unsafe-eval'"]);
    const prod = production();
    for (const [name, sources] of dev) if (name !== "script-src") expect(sources, name).toEqual(prod.get(name));
  });

  it("carries the nonce it is given, and Next.js finds it there", () => {
    const other = newNonce();
    const policy = contentSecurityPolicy({ nonce: other, https: false, development: false });
    expect(directives(policy).get("script-src")).toContain(`'nonce-${other}'`);
    expect(policy).not.toContain(NONCE);
    // What next/dist/server/app-render/get-script-nonce-from-header.js does.
    const directive = policy.split(";").map((d) => d.trim()).find((d) => d.startsWith("script-src"));
    expect(directive?.split(/\s+/).slice(1).map((s) => /^'nonce-([A-Za-z0-9+/_-]+={0,2})'$/.exec(s)?.[1]).find(Boolean)).toBe(other);
  });
});

describe("the other headers", () => {
  it("are the policy, nosniff, no referrer, no powerful features and no framing", () => {
    const h = headerMap(securityHeaders({ nonce: NONCE, https: false, development: false }));
    expect([...h.keys()]).toEqual(["content-security-policy", "x-content-type-options", "referrer-policy", "permissions-policy", "x-frame-options"]);
    expect(h.get("content-security-policy")).toBe(contentSecurityPolicy({ nonce: NONCE, https: false, development: false }));
    expect(h.get("x-content-type-options")).toBe("nosniff");
    expect(h.get("referrer-policy")).toBe("no-referrer");
    expect(h.get("x-frame-options")).toBe("DENY");
  });

  it("deny every powerful feature, the camera, microphone and location first (the photo is a file input)", () => {
    expect(PERMISSIONS_POLICY).toBe(securityHeaders({ nonce: NONCE, https: false, development: false }).find(([n]) => n === "Permissions-Policy")?.[1]);
    const features = PERMISSIONS_POLICY.split(", ");
    for (const f of features) expect(f).toMatch(/^[a-z-]+=\(\)$/);
    const names = features.map((f) => f.slice(0, -3));
    expect(new Set(names).size).toBe(names.length);
    expect(names.slice(0, 3)).toEqual(["camera", "microphone", "geolocation"]);
    expect(names).toEqual(expect.arrayContaining(["display-capture", "usb", "serial", "hid", "payment", "midi", "accelerometer", "gyroscope", "magnetometer", "browsing-topics"]));
  });

  it("add Strict-Transport-Security only when the app is served over https", () => {
    expect(headerMap(securityHeaders({ nonce: NONCE, https: false, development: false })).has("strict-transport-security")).toBe(false);
    const h = headerMap(securityHeaders({ nonce: NONCE, https: true, development: false }));
    expect(h.get("strict-transport-security")).toBe(HSTS);
    expect(HSTS).toBe("max-age=31536000");
    expect(h.get("content-security-policy")).toContain("upgrade-insecure-requests");
  });
});

describe("served over https", () => {
  it("is the configured app origin's scheme, never the request's", () => {
    expect(servedOverHttps("https://geminus.up.railway.app")).toBe(true);
    expect(servedOverHttps("HTTPS://Geminus.example")).toBe(true);
    for (const v of [undefined, "", "  ", "http://127.0.0.1:3100", "http://localhost:3000", "not a url", "geminus.example", "ftp://x.example", "httpsx://x"]) {
      expect(servedOverHttps(v), String(v)).toBe(false);
    }
  });
});

describe("proxy.ts", () => {
  const saved = { origin: process.env.BFF_APP_ORIGIN };
  afterEach(() => {
    if (saved.origin === undefined) delete process.env.BFF_APP_ORIGIN;
    else process.env.BFF_APP_ORIGIN = saved.origin;
  });

  /** The response's headers, and the request headers Next.js renders with. */
  function run(url: string, headers: Record<string, string> = {}) {
    const res = proxyModule.proxy(new NextRequest(url, { headers }));
    const overridden = (res.headers.get("x-middleware-override-headers") ?? "").split(",");
    const request = (name: string) => (overridden.includes(name) ? res.headers.get(`x-middleware-request-${name}`) : null);
    return { res, request };
  }
  const nonceIn = (policy: string | null) => /'nonce-([^']+)'/.exec(policy ?? "")?.[1];

  it("runs for every path: pages, the BFF's routes and static files", () => {
    expect("config" in proxyModule, "no matcher").toBe(false);
    const config = (proxyModule as { config?: Parameters<typeof unstable_doesMiddlewareMatch>[0]["config"] }).config ?? {};
    for (const url of ["/", "/projects", "/api/v1/me", "/auth/login", "/auth/callback", "/_next/static/chunks/a.js", "/icon.svg", "/no-such-page"]) {
      expect(unstable_doesMiddlewareMatch({ config, url }), url).toBe(true);
    }
  });

  it("puts the headers on the response and the same policy and nonce on the request for Next.js and the layout", () => {
    delete process.env.BFF_APP_ORIGIN;
    const { res, request } = run("http://127.0.0.1:3100/projects");
    const nonce = nonceIn(res.headers.get("content-security-policy"));
    expect(isNonce(nonce)).toBe(true);
    for (const [name, value] of securityHeaders({ nonce: nonce!, https: false, development: false })) expect(res.headers.get(name), name).toBe(value);
    expect(request("content-security-policy")).toBe(res.headers.get("content-security-policy"));
    expect(request(NONCE_HEADER)).toBe(nonce);
    expect(res.headers.get("strict-transport-security")).toBeNull();
    expect(res.headers.get("x-middleware-next")).toBe("1");
  });

  it("makes a new nonce for every request and replaces one the client sent", () => {
    const a = run("http://127.0.0.1:3100/", { [NONCE_HEADER]: NONCE, "content-security-policy": `script-src 'nonce-${NONCE}'` });
    const b = run("http://127.0.0.1:3100/");
    const na = nonceIn(a.res.headers.get("content-security-policy"));
    expect(na).not.toBe(NONCE);
    expect(a.request(NONCE_HEADER)).toBe(na);
    expect(a.request("content-security-policy")).not.toContain(NONCE);
    expect(nonceIn(b.res.headers.get("content-security-policy"))).not.toBe(na);
  });

  it("sends Strict-Transport-Security when the configured origin is https, whatever the request says", () => {
    process.env.BFF_APP_ORIGIN = "https://geminus.example";
    const s = run("http://10.0.0.5:3000/projects");
    expect(s.res.headers.get("strict-transport-security")).toBe(HSTS);
    expect(s.res.headers.get("content-security-policy")).toContain("upgrade-insecure-requests");
    process.env.BFF_APP_ORIGIN = "http://127.0.0.1:3100";
    const p = run("https://127.0.0.1:3100/projects", { "x-forwarded-proto": "https" });
    expect(p.res.headers.get("strict-transport-security")).toBeNull();
    expect(p.res.headers.get("content-security-policy")).not.toContain("upgrade-insecure-requests");
  });
});
