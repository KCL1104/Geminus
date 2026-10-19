// The BFF as the process builds it from its environment (ADR-018, T-043):
// once, and a deployment without a valid session configuration answers 503
// for every auth and API route, logging which variables are wrong but never
// their values.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CLIENT_ID, CLIENT_SECRET, ENDPOINTS, ISSUER } from "./fake-oidc";

const KEY = Buffer.alloc(32, 7).toString("base64url");
const complete: Record<string, string> = {
  BFF_APP_ORIGIN: "https://app.test.invalid",
  BFF_API_URL: "https://api.internal.test.invalid",
  BFF_OIDC_ISSUER: ISSUER,
  BFF_OIDC_AUTHORIZATION_URL: ENDPOINTS.authorization,
  BFF_OIDC_TOKEN_URL: ENDPOINTS.token,
  BFF_OIDC_REVOCATION_URL: ENDPOINTS.revocation,
  BFF_OIDC_END_SESSION_URL: ENDPOINTS.endSession,
  BFF_OIDC_JWKS_URL: ENDPOINTS.jwks,
  BFF_OIDC_CLIENT_ID: CLIENT_ID,
  BFF_OIDC_CLIENT_SECRET: CLIENT_SECRET,
  BFF_OIDC_AUDIENCE: "https://api.test.invalid",
  SESSION_KEYS: `k1:${KEY}`,
};

let logged: string[];

beforeEach(() => {
  vi.resetModules();
  logged = [];
  vi.spyOn(process.stdout, "write").mockImplementation((chunk) => {
    logged.push(String(chunk));
    return true;
  });
});

afterEach(() => {
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

async function withEnv(env: Record<string, string>) {
  for (const [k, v] of Object.entries(env)) vi.stubEnv(k, v);
  return (await import("../lib/session/server")).withBff;
}

describe("withBff", () => {
  it.each<[string, Record<string, string>, string]>([
    ["an invalid endpoint", { BFF_OIDC_TOKEN_URL: "ftp://id.test.invalid/token" }, "BFF_OIDC_TOKEN_URL"],
    ["a missing client secret", { BFF_OIDC_CLIENT_SECRET: "" }, "BFF_OIDC_CLIENT_SECRET"],
    ["a key of the wrong size", { SESSION_KEYS: `k1:${Buffer.alloc(16, 7).toString("base64url")}` }, "SESSION_KEYS"],
  ])("answers 503 with %s, naming the variable and not the values", async (_, change, name) => {
    const withBff = await withEnv({ ...complete, ...change });
    const run = vi.fn(async () => new Response("reached"));
    for (let i = 0; i < 2; i++) {
      const res = await withBff(run);
      expect(res.status).toBe(503);
      expect(((await res.json()) as { error: { code: string } }).error.code).toBe("SERVICE_UNAVAILABLE");
    }
    expect(run).not.toHaveBeenCalled();
    const log = logged.join("");
    expect(log).toContain('"event":"config.invalid"');
    expect(log).toContain(name);
    expect(log).not.toContain(CLIENT_SECRET);
    expect(log).not.toContain(KEY);
    // Built once: the problem is logged once, not per request.
    expect(logged.filter((l) => l.includes("config.invalid"))).toHaveLength(1);
  });

  it("runs the handlers of a complete configuration, built once", async () => {
    const withBff = await withEnv(complete);
    const seen = new Set<unknown>();
    for (let i = 0; i < 2; i++) {
      const res = await withBff(async (h) => {
        seen.add(h);
        return new Response("reached");
      });
      expect(await res.text()).toBe("reached");
    }
    expect(seen.size).toBe(1);
    expect(logged.join("")).toBe("");
  });
});
