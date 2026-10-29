// Deployment configuration of the browser session (ADR-018, T-043). Server-
// side only. Provider endpoints come from here and nowhere else: no runtime
// discovery, nothing from a request. Missing or invalid values make the whole
// configuration invalid; errors name the variable, never its value, since
// several carry credentials.

export class BffConfigError extends Error {
  constructor(readonly problems: string[]) {
    super(`invalid browser session configuration: ${problems.join("; ")}`);
  }
}

export type BffConfig = {
  /** The app's own origin, e.g. https://app.example: CSRF checks and redirect URIs. */
  appOrigin: string;
  /** Base URL of the Go API the proxy forwards to (no trailing slash). */
  apiUrl: string;
  oidc: {
    issuer: string;
    authorizationEndpoint: string;
    tokenEndpoint: string;
    revocationEndpoint: string;
    endSessionEndpoint: string;
    jwksUri: string;
    clientId: string;
    clientSecret: string;
    /** The API identifier the access token must be issued for (ADR-017). */
    audience: string;
    scopes: string;
  };
  /**
   * Every URL above is plain http on loopback: a local or test setup. Only
   * then does the OIDC client allow http (openid-client refuses it otherwise).
   */
  loopbackHttp: boolean;
};

const loopback = new Set(["localhost", "127.0.0.1", "[::1]"]);
const visible = /^[\x21-\x7e]+$/;

type Checked = { ok: true; url: URL } | { ok: false; why: string };

function checkUrl(raw: string | undefined, kind: "origin" | "endpoint" | "base"): Checked {
  if (raw === undefined || raw.trim() === "") return { ok: false, why: "is required" };
  let url: URL;
  try {
    url = new URL(raw.trim());
  } catch {
    return { ok: false, why: "is not a URL" };
  }
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback.has(url.hostname))) return { ok: false, why: "must be https (http only on loopback)" };
  if (url.username !== "" || url.password !== "" || url.hash !== "" || raw.includes("#")) return { ok: false, why: "must not carry credentials or a fragment" };
  if (kind !== "endpoint" && (url.search !== "" || raw.includes("?"))) return { ok: false, why: "must not carry a query" };
  if (kind === "origin" && url.pathname !== "/") return { ok: false, why: "must be an origin, without a path" };
  if (url.pathname.includes("//")) return { ok: false, why: "must not have an empty path segment" };
  return { ok: true, url };
}

/** Reads the BFF_* variables and SESSION_KEYS' presence (seal.ts parses the keys). */
export function loadBffConfig(env: Record<string, string | undefined>): BffConfig {
  const problems: string[] = [];
  const urls: Record<string, URL> = {};
  const url = (name: string, kind: "origin" | "endpoint" | "base") => {
    const r = checkUrl(env[name], kind);
    if (r.ok) urls[name] = r.url;
    else problems.push(`${name} ${r.why}`);
  };
  url("BFF_APP_ORIGIN", "origin");
  url("BFF_API_URL", "base");
  url("BFF_OIDC_ISSUER", "base");
  for (const name of ["BFF_OIDC_AUTHORIZATION_URL", "BFF_OIDC_TOKEN_URL", "BFF_OIDC_REVOCATION_URL", "BFF_OIDC_END_SESSION_URL", "BFF_OIDC_JWKS_URL"]) url(name, "endpoint");
  const text = (name: string, max: number): string => {
    const v = env[name]?.trim() ?? "";
    if (v === "" || v.length > max || !visible.test(v)) problems.push(`${name} must be 1-${max} visible ASCII characters`);
    return v;
  };
  const clientId = text("BFF_OIDC_CLIENT_ID", 256);
  const clientSecret = text("BFF_OIDC_CLIENT_SECRET", 512);
  const audience = text("BFF_OIDC_AUDIENCE", 512);
  // profile and email give the account menu the person's name and email (H-042).
  const scopes = (env.BFF_OIDC_SCOPES?.trim() || "openid profile email offline_access").split(/\s+/);
  if (!scopes.includes("openid") || scopes.some((s) => !/^[\x21\x23-\x5b\x5d-\x7e]{1,64}$/.test(s)) || new Set(scopes).size !== scopes.length) {
    problems.push("BFF_OIDC_SCOPES must be distinct scope tokens including openid");
  }
  if (env.SESSION_KEYS === undefined || env.SESSION_KEYS.trim() === "") problems.push("SESSION_KEYS is required");
  if (problems.length > 0) throw new BffConfigError(problems);

  const all = Object.values(urls);
  const loopbackHttp = all.every((u) => u.protocol === "http:");
  if (!loopbackHttp && all.some((u) => u.protocol === "http:")) {
    throw new BffConfigError(["plain http is only for a setup where every URL is on loopback"]);
  }
  const at = (name: string) => (urls[name] as URL).toString();
  return {
    appOrigin: (urls.BFF_APP_ORIGIN as URL).origin,
    apiUrl: at("BFF_API_URL").replace(/\/$/, ""),
    oidc: {
      // The issuer is compared exactly with the ID token's iss, so it keeps
      // the configured spelling (a trailing slash matters).
      issuer: env.BFF_OIDC_ISSUER?.trim() as string,
      authorizationEndpoint: at("BFF_OIDC_AUTHORIZATION_URL"),
      tokenEndpoint: at("BFF_OIDC_TOKEN_URL"),
      revocationEndpoint: at("BFF_OIDC_REVOCATION_URL"),
      endSessionEndpoint: at("BFF_OIDC_END_SESSION_URL"),
      jwksUri: at("BFF_OIDC_JWKS_URL"),
      clientId,
      clientSecret,
      audience,
      scopes: scopes.join(" "),
    },
    loopbackHttp,
  };
}
