// The root layout's hand-off to the signed-in bootstrap (T-043 follow-up,
// ADR-018): the server reads the session cookie only to say whether it holds
// a session (lib/session/server hasSession), and the organization cookie only
// as an organization ID; the client component gets a boolean and that ID,
// never the cookie, a token or anything sealed in it. next/headers is a
// cookie jar here, the BFF's check a stand-in, and AuthBootstrap a probe.
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const SEALED = "v1.k1.c2VhbGVk.c2VjcmV0LXRva2VuLWluc2lkZQ";
const ORG = "org_01M4YDQK004J6EG1BW3SRYPDGD";
const jar = vi.hoisted(() => ({ cookies: {} as Record<string, string> }));
const asked = vi.hoisted(() => ({ values: [] as (string | undefined)[], profiles: [] as (string | undefined)[], open: true }));
const handed = vi.hoisted(() => ({ props: [] as Record<string, unknown>[] }));

vi.mock("next/headers", () => ({
  cookies: async () => ({ get: (name: string) => (name in jar.cookies ? { name, value: jar.cookies[name] } : undefined) }),
  // The request headers (T-057: the nonce proxy.ts sets; none here).
  headers: async () => new Headers(),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: () => {} }), usePathname: () => "/" }));
vi.mock("../lib/session/server", () => ({
  hasSession: async (value: string | undefined) => {
    asked.values.push(value);
    return value !== undefined && asked.open;
  },
  // H-042: the account menu's name and email, asked for only with a session.
  sessionProfile: async (value: string | undefined) => {
    asked.profiles.push(value);
    return { name: "Judge One", email: "judge@geminus-demo.invalid" };
  },
}));
vi.mock("../app/_components/auth-bootstrap", () => ({
  AuthBootstrap: ({ children, ...props }: { children: unknown }) => {
    handed.props.push(props);
    return children;
  },
}));

const { default: RootLayout } = await import("../app/layout");
const { default: Home } = await import("../app/page");

beforeEach(() => {
  jar.cookies = {};
  asked.values = [];
  asked.profiles = [];
  asked.open = true;
  handed.props = [];
});

const render = async () => renderToStaticMarkup(await RootLayout({ children: await Home() }));

describe("the root layout's session hint", () => {
  it("is signed out without a session cookie, and does not ask the BFF", async () => {
    await render();
    expect(asked.values).toEqual([]);
    expect(asked.profiles).toEqual([]);
    expect(handed.props).toEqual([{ signedIn: false, organization: null }]);
  });

  it("asks the BFF whether the cookie holds a session, and hands over only the answer", async () => {
    jar.cookies["__Host-geminus_session"] = SEALED;
    const out = await render();
    expect(asked.values).toEqual([SEALED]);
    // H-042: the profile is read only with a session, and nothing of the session is in the page but what the rail shows.
    expect(asked.profiles).toEqual([SEALED]);
    expect(handed.props).toEqual([{ signedIn: true, organization: null }]);
    expect(out).not.toContain(SEALED);
    expect(out).not.toContain("c2VjcmV0");
  });

  it("is signed out when the BFF cannot open the cookie or the session has ended", async () => {
    jar.cookies["__Host-geminus_session"] = SEALED;
    asked.open = false;
    await render();
    expect(asked.profiles).toEqual([]);
    expect(handed.props).toEqual([{ signedIn: false, organization: null }]);
  });

  it("hands over a remembered organization only as an organization ID", async () => {
    jar.cookies.geminus_organization = ORG;
    await render();
    expect(handed.props.at(-1)).toEqual({ signedIn: false, organization: ORG });
    for (const value of ["org_x", `${ORG}; Path=/`, "<script>", ""]) {
      jar.cookies.geminus_organization = value;
      await render();
      expect(handed.props.at(-1), value).toEqual({ signedIn: false, organization: null });
    }
  });
});
