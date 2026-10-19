// A deterministic in-process OIDC provider for the BFF tests (T-043): it
// answers the BFF's fetches to the token, revocation and JWKS endpoints, and
// issues authorization codes the way /authorize would. Tokens are random; ID
// tokens are RS256 JWTs. Each misbehaviour a test needs is a switch. Never
// a real provider (ADR-018).
import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto";

export const ISSUER = "https://id.test.invalid/";
export const ENDPOINTS = {
  authorization: "https://id.test.invalid/authorize",
  token: "https://id.test.invalid/oauth/token",
  revocation: "https://id.test.invalid/oauth/revoke",
  endSession: "https://id.test.invalid/v2/logout",
  jwks: "https://id.test.invalid/.well-known/jwks.json",
};
export const CLIENT_ID = "bff-client";
export const CLIENT_SECRET = "bff-client-secret-0123456789";
export const AUDIENCE = "https://api.test.invalid";

const b64url = (b: Buffer | string) => Buffer.from(b).toString("base64url");

type Code = { challenge: string; method: string; nonce: string; redirectUri: string; clientId: string; used: boolean };

export type IdTokenChange = Partial<{ iss: string; aud: string; nonce: string; sub: string | null; exp: number; iat: number; badSignature: boolean }>;

export class FakeProvider {
  private keys = generateKeyPairSync("rsa", { modulusLength: 2048 });
  private other = generateKeyPairSync("rsa", { modulusLength: 2048 });
  private codes = new Map<string, Code>();
  /** Refresh tokens: live, spent (rotated) or revoked; a spent one reused revokes its family. */
  private refresh = new Map<string, { state: "live" | "spent" | "revoked"; family: string }>();
  readonly calls: { url: string; body: URLSearchParams }[] = [];
  readonly revoked: string[] = [];
  now = () => Math.floor(Date.now() / 1000);
  idToken: IdTokenChange = {};
  expiresIn: number | undefined = 600;
  failRevocation = false;
  /** The token endpoint is down for refresh grants (503). */
  failRefresh = false;
  issueRefreshToken = true;

  /** What /authorize does after the user signs in: a code for these parameters. */
  authorize(authorizationUrl: string): { code: string; state: string; params: URLSearchParams } {
    const u = new URL(authorizationUrl);
    const p = u.searchParams;
    const code = b64url(randomBytes(16));
    this.codes.set(code, {
      challenge: p.get("code_challenge") ?? "",
      method: p.get("code_challenge_method") ?? "",
      nonce: p.get("nonce") ?? "",
      redirectUri: p.get("redirect_uri") ?? "",
      clientId: p.get("client_id") ?? "",
      used: false,
    });
    return { code, state: p.get("state") ?? "", params: p };
  }

  private jwt(claims: Record<string, unknown>, key = this.keys.privateKey): string {
    const head = b64url(JSON.stringify({ alg: "RS256", typ: "JWT", kid: "k1" }));
    const body = b64url(JSON.stringify(claims));
    const sig = sign("sha256", Buffer.from(`${head}.${body}`), key);
    return `${head}.${body}.${b64url(sig)}`;
  }

  private tokens(nonce: string | null): Record<string, unknown> {
    const now = this.now();
    const c = this.idToken;
    const family = b64url(randomBytes(8));
    const refresh = b64url(randomBytes(24));
    this.refresh.set(refresh, { state: "live", family });
    const out: Record<string, unknown> = { access_token: `at-${b64url(randomBytes(24))}`, token_type: "Bearer", scope: "openid offline_access" };
    if (this.expiresIn !== undefined) out.expires_in = this.expiresIn;
    if (this.issueRefreshToken) out.refresh_token = refresh;
    if (nonce !== null) {
      const claims: Record<string, unknown> = {
        iss: c.iss ?? ISSUER,
        aud: c.aud ?? CLIENT_ID,
        sub: c.sub === undefined ? "auth0|user-1" : c.sub,
        nonce: c.nonce ?? nonce,
        iat: c.iat ?? now,
        exp: c.exp ?? now + 600,
      };
      if (claims.sub === null) delete claims.sub;
      out.id_token = this.jwt(claims, c.badSignature ? this.other.privateKey : this.keys.privateKey);
    }
    return out;
  }

  private json(status: number, body: unknown): Response {
    return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", "cache-control": "no-store" } });
  }

  /** The fetch the BFF's OIDC client uses. */
  fetch = async (url: string, init: RequestInit): Promise<Response> => {
    const body = new URLSearchParams(typeof init.body === "string" ? init.body : init.body instanceof URLSearchParams ? init.body.toString() : "");
    this.calls.push({ url, body });
    if (url === ENDPOINTS.jwks) {
      const jwk = this.keys.publicKey.export({ format: "jwk" });
      return this.json(200, { keys: [{ ...jwk, kid: "k1", alg: "RS256", use: "sig" }] });
    }
    if (url === ENDPOINTS.token) {
      if (body.get("client_id") !== CLIENT_ID || body.get("client_secret") !== CLIENT_SECRET) return this.json(401, { error: "invalid_client" });
      if (body.get("grant_type") === "authorization_code") {
        const code = this.codes.get(body.get("code") ?? "");
        const verifier = body.get("code_verifier") ?? "";
        if (code === undefined || code.used || code.redirectUri !== body.get("redirect_uri") || code.clientId !== CLIENT_ID) return this.json(400, { error: "invalid_grant" });
        code.used = true;
        if (code.method !== "S256" || b64url(createHash("sha256").update(verifier).digest()) !== code.challenge) return this.json(400, { error: "invalid_grant", error_description: "PKCE verification failed" });
        return this.json(200, this.tokens(code.nonce));
      }
      if (body.get("grant_type") === "refresh_token") {
        if (this.failRefresh) return this.json(503, { error: "temporarily_unavailable" });
        const t = this.refresh.get(body.get("refresh_token") ?? "");
        if (t === undefined || t.state !== "live") {
          if (t?.state === "spent") for (const v of this.refresh.values()) if (v.family === t.family) v.state = "revoked";
          return this.json(400, { error: "invalid_grant" });
        }
        t.state = "spent";
        const out = this.tokens(null);
        const next = out.refresh_token as string | undefined;
        if (next !== undefined) (this.refresh.get(next) as { family: string }).family = t.family;
        return this.json(200, out);
      }
      return this.json(400, { error: "unsupported_grant_type" });
    }
    if (url === ENDPOINTS.revocation) {
      if (this.failRevocation) return this.json(503, { error: "temporarily_unavailable" });
      const token = body.get("token") ?? "";
      this.revoked.push(token);
      const t = this.refresh.get(token);
      if (t !== undefined) for (const v of this.refresh.values()) if (v.family === t.family) v.state = "revoked";
      return new Response(null, { status: 200 });
    }
    return new Response("not found", { status: 404 });
  };

  refreshState(token: string): "live" | "spent" | "revoked" | undefined {
    return this.refresh.get(token)?.state;
  }

  tokenCalls(grant: string): number {
    return this.calls.filter((c) => c.url === ENDPOINTS.token && c.body.get("grant_type") === grant).length;
  }
}
