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
  cleanEmail,
  cleanName,
  MAX_NAME,
  profileFromClaims,
  readSession,
  SESSION_COOKIE,
  type Session,
  safeReturnTo,
  SKEW_MS,
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

  it("seals under the current key after a rotation, never the previous one", async () => {
    // Retiring k1 later must not end sessions sealed since the rotation, and
    // rotating away from a leaked k1 must stop using it at once.
    const sealed = await seal(await keysOf(`k2:${key(2)},k1:${key(1)}`), SESSION_COOKIE, "x");
    expect(sealed.split(".")[1]).toBe("k2");
    expect(await unseal(await keysOf(`k2:${key(2)}`), SESSION_COOKIE, sealed)).toBe("x");
    expect(await unseal(await keysOf(`k1:${key(1)}`), SESSION_COOKIE, sealed)).toBeNull();
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
    // A clock a little behind the one that sealed it (an NTP step, another
    // instance) keeps the session; one far behind does not trust it.
    expect(await readSession(keys, value, session.lastSeenAt - SKEW_MS)).not.toBeNull();
    expect(await readSession(keys, value, session.lastSeenAt - SKEW_MS - 1)).toBeNull();
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
    // As for sessions: a little clock skew is tolerated, more is not.
    expect(await readLogin(keys, value, login.createdAt - SKEW_MS)).toEqual(login);
    expect(await readLogin(keys, value, login.createdAt - SKEW_MS - 1)).toBeNull();
  });

  it("refuses a sealed login of the wrong shape", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    for (const bad of [
      { ...login, extra: 1 },
      { ...login, state: "" },
      { ...login, nonce: "x".repeat(129) },
      { ...login, codeVerifier: "with space" },
      { ...login, createdAt: 0 },
      Object.fromEntries(Object.entries(login).filter(([k]) => k !== "nonce")),
    ]) {
      expect(await readLogin(keys, await seal(keys, LOGIN_COOKIE, bad), now)).toBeNull();
    }
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
    ["/x y", "/"],
    // Decoded from return_to: a raw non-ASCII path (not Latin-1 either) could
    // not be put in the callback's Location header; its percent-encoding can.
    ["/projects/\u4e2d", "/"],
    ["/projects/\u00e9", "/"],
    ["/projects/%E4%B8%AD", "/projects/%E4%B8%AD"],
    [`/${"a".repeat(512)}`, "/"],
  ])("%s → %s", (input, want) => {
    expect(safeReturnTo(input)).toBe(want);
  });
});

describe("the session's profile (H-042)", () => {
  it("keeps a name and an email from the ID token, cleaned, and nothing else", () => {
    expect(profileFromClaims({ sub: "auth0|x", name: "  Judge   One ", email: "judge@geminus-demo.invalid", roles: ["admin"] })).toEqual({ name: "Judge One", email: "judge@geminus-demo.invalid" });
    // The nickname stands in for a missing name; neither usable is no profile.
    expect(profileFromClaims({ nickname: "judge", email: 7 })).toEqual({ name: "judge", email: null });
    expect(profileFromClaims({ name: "", email: "not an address" })).toBeNull();
  });

  it("drops what shows nothing or reorders text from a name, and caps its length", () => {
    expect(cleanName("Admin\u202e\u200bnimdA\u0007")).toBe("AdminnimdA");
    expect(cleanName("\u200b\u2066")).toBeNull();
    expect([...cleanName("名".repeat(500))!].length).toBe(MAX_NAME);
    expect(cleanName(["a"])).toBeNull();
  });

  // Review M1: a cut that ends on a space left it there, so the session it
  // sealed failed its own check on the next request: a silent sign-in loop.
  it("cleans a name that is already clean to itself, a cut on a space included, so its session reads back", async () => {
    const onSpace = `${"a".repeat(MAX_NAME - 1)} ${"b".repeat(20)}`;
    expect(cleanName(onSpace)).toBe("a".repeat(MAX_NAME - 1));
    for (const v of [onSpace, " x ", "名".repeat(500), `${"名 ".repeat(100)}`, "a\u3000\u3000b"]) {
      const once = cleanName(v);
      expect(cleanName(once), JSON.stringify(v)).toBe(once);
    }
    const keys = await keysOf(`k1:${key(1)}`);
    const signedIn = { ...session, profile: profileFromClaims({ name: onSpace, email: "judge@geminus-demo.invalid" }) };
    expect(await readSession(keys, valueOf(await sessionCookie(keys, signedIn, now)), now)).toEqual(signedIn);
  });

  it("takes an email only as one printable address", () => {
    expect(cleanEmail("a@b.c")).toBe("a@b.c");
    for (const bad of ["a b@c", "a@@b", "@b", "a@", "a@b\u202e", `${"a".repeat(250)}@b.cd`, 1]) expect(cleanEmail(bad), String(bad)).toBeNull();
  });

  it("reads a session with a profile, one sealed before H-042 without it, and refuses a malformed profile", async () => {
    const keys = await keysOf(`k1:${key(1)}`);
    const withProfile = { ...session, profile: { name: "Judge One", email: "judge@geminus-demo.invalid" } };
    expect(await readSession(keys, valueOf(await sessionCookie(keys, withProfile, now)), now)).toEqual(withProfile);
    expect(await readSession(keys, valueOf(await sessionCookie(keys, { ...session, profile: null }, now)), now)).toEqual({ ...session, profile: null });
    expect(await readSession(keys, valueOf(await sessionCookie(keys, session, now)), now)).toEqual(session);
    for (const profile of [{ name: "x" }, { name: "a\u202eb", email: null }, { name: null, email: "nope" }, { name: null, email: null, extra: 1 }, "Judge"]) {
      const sealed = valueOf(await sessionCookie(keys, { ...session, profile } as unknown as Session, now));
      expect(await readSession(keys, sealed, now), JSON.stringify(profile)).toBeNull();
    }
  });
});
