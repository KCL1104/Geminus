// The browser session's sealed cookies (ADR-018, T-043; TEST-032).
import { describe, expect, it } from "vitest";
import { MAX_COOKIE_BYTES, parseSealKeys, SealedTooLarge, SessionConfigError, seal, unseal } from "../lib/session/seal";
import {
  ABSOLUTE_MS,
  clearCookie,
  IDLE_MS,
  LOGIN_COOKIE,
  LOGIN_MS,
  type Login,
  loginCookie,
  readLogin,
  readSession,
  SESSION_COOKIE,
  type Session,
  safeReturnTo,
  sessionCookie,
} from "../lib/session/session";

const key = (byte: number) => Buffer.alloc(32, byte).toString("base64url");
const keysOf = (raw: string) => parseSealKeys(raw);

/** Another base64url spelling of the same bytes: only the last character's unused bits differ. */
function nonCanonical(b64: string): string {
  const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  const want = Buffer.from(b64, "base64url");
  for (const c of abc) {
    const other = b64.slice(0, -1) + c;
    if (other !== b64 && Buffer.from(other, "base64url").equals(want)) return other;
  }
  throw new Error("no spare bits in " + b64);
}
const now = Date.UTC(2026, 9, 19, 12);

const session: Session = {
  accessToken: "at.header.payload.signature",
  refreshToken: "rt-0123456789",
  accessExpiresAt: now + 600_000,
  createdAt: now - 1000,
  lastSeenAt: now - 500,
};
const login: Login = { state: "state-abc", nonce: "nonce-def", codeVerifier: "verifier-ghi", returnTo: "/projects", createdAt: now - 1000 };

const valueOf = (setCookie: string) => setCookie.slice(setCookie.indexOf("=") + 1, setCookie.indexOf(";"));

describe("SESSION_KEYS", () => {
  it("takes a current key and at most one previous key", async () => {
    const keys = await keysOf(`k2:${key(2)}, k1:${key(1)}`);
    expect(keys.current.id).toBe("k2");
    expect(keys.previous?.id).toBe("k1");
    expect((await keysOf(`k1:${key(1)}`)).previous).toBeNull();
  });

  it.each([
    ["missing", undefined],
    ["empty", ""],
    ["three keys", `a:${key(1)},b:${key(2)},c:${key(3)}`],
    ["an empty entry", `a:${key(1)},`],
    ["no ID", `:${key(1)}`],
    ["no separator", key(1)],
    ["an ID with capitals", `Key:${key(1)}`],
    ["a long ID", `${"a".repeat(17)}:${key(1)}`],
    ["a short key", `a:${Buffer.alloc(16, 1).toString("base64url")}`],
    ["a long key", `a:${Buffer.alloc(33, 1).toString("base64url")}`],
    ["standard base64", `a:${Buffer.alloc(32, 0xfb).toString("base64")}`],
    ["padding", `a:${key(1)}=`],
    ["two keys with one ID", `a:${key(1)},a:${key(2)}`],
    // The same 32 bytes spelled with other spare bits in the last character.
    ["a non-canonical spelling", `a:${nonCanonical(key(1))}`],
  ])("refuses %s without quoting the key", async (_, raw) => {
    const err = await keysOf(raw as string).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(SessionConfigError);
    expect(String(err)).not.toContain(key(1));
  });
});

describe("seal", () => {
  it("opens what it sealed, for that cookie only", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const value = { marker: "plaintext-marker" };
    const sealed = await seal(keys, SESSION_COOKIE, value);
    expect(sealed).toMatch(/^v1\.k1\.[A-Za-z0-9_-]{16}\.[A-Za-z0-9_-]+$/);
    expect(sealed).not.toContain("plaintext-marker");
    expect(sealed).not.toContain(Buffer.from(JSON.stringify(value)).toString("base64url").slice(0, 12));
    expect(await unseal(keys, SESSION_COOKIE, sealed)).toEqual(value);
    expect(await unseal(keys, LOGIN_COOKIE, sealed)).toBeNull();
  });

  it("never seals the same value to the same bytes", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    expect(await seal(keys, SESSION_COOKIE, "x")).not.toBe(await seal(keys, SESSION_COOKIE, "x"));
  });

  it("opens values of the previous key, not of a retired or unknown one", async () => {
    const old = await keysOf(`k1:${key(1)}`);
    const sealed = await seal(old, SESSION_COOKIE, "x");
    expect(await unseal(await keysOf(`k2:${key(2)},k1:${key(1)}`), SESSION_COOKIE, sealed)).toBe("x");
    expect(await unseal(await keysOf(`k3:${key(3)},k2:${key(2)}`), SESSION_COOKIE, sealed)).toBeNull();
    // Same ID, other key material: the tag does not verify.
    expect(await unseal(await keysOf(`k1:${key(9)}`), SESSION_COOKIE, sealed)).toBeNull();
  });

  it("refuses anything tampered with or malformed", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const sealed = await seal(keys, SESSION_COOKIE, { a: 1 });
    const [v, id, iv, data] = sealed.split(".") as [string, string, string, string];
    const flip = (s: string, i: number) => s.slice(0, i) + (s[i] === "A" ? "B" : "A") + s.slice(i + 1);
    for (const bad of [
      undefined,
      "",
      "v1",
      `v2.${id}.${iv}.${data}`,
      `${v}.${id}.${iv}.${data}.x`,
      `${v}.${id}.${flip(iv, 3)}.${data}`,
      `${v}.${id}.${iv}.${flip(data, 5)}`,
      `${v}.${id}.${iv}.${flip(data, data.length - 2)}`,
      `${v}.${id}.${iv.slice(1)}.${data}`,
      `${v}.${id}.${iv}.${data}=`,
      `${v}.${id}.${iv}.${data.slice(0, 10)}`,
      `${v}.${id}.${iv}.${nonCanonical(data)}`,
      `${v}.${id}.${iv}.${"A".repeat(MAX_COOKIE_BYTES)}`,
    ]) {
      expect(await unseal(keys, SESSION_COOKIE, bad)).toBeNull();
    }
  });

  it("refuses a value too large for one cookie", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    await expect(seal(keys, SESSION_COOKIE, "x".repeat(MAX_COOKIE_BYTES))).rejects.toBeInstanceOf(SealedTooLarge);
    // The limit is on name=value: a value that fits alone may not fit with the name.
    const fits = await seal(keys, SESSION_COOKIE, "x".repeat(2500));
    expect(SESSION_COOKIE.length + 1 + fits.length).toBeLessThanOrEqual(MAX_COOKIE_BYTES);
  });
});

describe("session cookie", () => {
  it("round-trips with the cookie attributes ADR-018 requires", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const set = await sessionCookie(keys, session, now);
    expect(set.startsWith(`${SESSION_COOKIE}=v1.`)).toBe(true);
    const attrs = set.split("; ").slice(1);
    expect(attrs).toEqual(["Path=/", "HttpOnly", "Secure", "SameSite=Lax", `Max-Age=${Math.floor((session.lastSeenAt + IDLE_MS - now) / 1000)}`]);
    expect(set).not.toMatch(/Domain=/i);
    expect(set).not.toContain(session.accessToken);
    expect(await readSession(keys, valueOf(set), now)).toEqual(session);
  });

  it("lives until the earlier of its idle and absolute limits", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const old = { ...session, createdAt: now - ABSOLUTE_MS + 60_000, lastSeenAt: now };
    expect(await sessionCookie(keys, old, now)).toMatch(/; Max-Age=60$/);
  });

  it("ends after 12 hours idle or 7 days in all", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const value = valueOf(await sessionCookie(keys, session, now));
    expect(await readSession(keys, value, session.lastSeenAt + IDLE_MS - 1)).not.toBeNull();
    expect(await readSession(keys, value, session.lastSeenAt + IDLE_MS)).toBeNull();
    const busy = { ...session, lastSeenAt: session.createdAt + ABSOLUTE_MS - 1 };
    const busyValue = valueOf(await sessionCookie(keys, busy, busy.lastSeenAt));
    expect(await readSession(keys, busyValue, busy.lastSeenAt)).not.toBeNull();
    expect(await readSession(keys, busyValue, session.createdAt + ABSOLUTE_MS)).toBeNull();
    // A session from the future (a clock moved back) is not trusted either.
    expect(await readSession(keys, value, session.createdAt - 1)).toBeNull();
  });

  it("refuses a sealed value of the wrong shape", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    for (const bad of [
      null,
      [],
      "token",
      { ...session, extra: 1 },
      { ...session, accessToken: "" },
      { ...session, accessToken: "with space" },
      { ...session, accessToken: "x".repeat(2049) },
      { ...session, refreshToken: 5 },
      { ...session, accessExpiresAt: 1.5 },
      { ...session, createdAt: -1 },
      { ...session, lastSeenAt: session.createdAt - 1 },
      Object.fromEntries(Object.entries(session).filter(([k]) => k !== "refreshToken")),
    ]) {
      expect(await readSession(keys, await seal(keys, SESSION_COOKIE, bad), now)).toBeNull();
    }
    // A login transaction does not open as a session.
    expect(await readSession(keys, valueOf(await loginCookie(keys, login)), now)).toBeNull();
  });

  it("clears with the same attributes", () => {
    expect(clearCookie(SESSION_COOKIE)).toBe(`${SESSION_COOKIE}=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0`);
  });
});

describe("login transaction", () => {
  it("round-trips for ten minutes", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const set = await loginCookie(keys, login);
    expect(set.split("; ").slice(1)).toEqual(["Path=/", "HttpOnly", "Secure", "SameSite=Lax", `Max-Age=${LOGIN_MS / 1000}`]);
    const value = valueOf(set);
    expect(await readLogin(keys, value, now)).toEqual(login);
    expect(await readLogin(keys, value, login.createdAt + LOGIN_MS)).toBeNull();
    expect(await readSession(keys, value, now)).toBeNull();
  });

  it("refuses a return path it would not have stored", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    for (const returnTo of ["//evil.example", "https://evil.example", "/\\evil", ""]) {
      expect(await readLogin(keys, await seal(keys, LOGIN_COOKIE, { ...login, returnTo }), now)).toBeNull();
    }
  });
});

describe("safeReturnTo", () => {
  it.each([
    ["/projects?x=1#y", "/projects?x=1#y"],
    ["/", "/"],
    [null, "/"],
    ["", "/"],
    ["projects", "/"],
    ["//evil.example/x", "/"],
    ["/\\evil.example", "/"],
    ["https://evil.example", "/"],
    ["/x\ny", "/"],
    ["/x\ty", "/"],
    [`/${"a".repeat(512)}`, "/"],
  ])("%s → %s", (input, want) => {
    expect(safeReturnTo(input)).toBe(want);
  });
});
