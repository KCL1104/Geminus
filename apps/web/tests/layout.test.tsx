// REQ-022: the server components render in the reader's remembered
// language, English without one, and the document says which (html lang).
// H-013: the reader's remembered theme is on <html data-theme> from the
// server, so the first paint has it; without one the system's applies.
// next/headers is replaced by a cookie jar: outside a request Next.js has no
// cookies to read.
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined, theme: undefined as string | undefined, nonce: undefined as string | undefined }));
vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) => {
      const value = name === "geminus_locale" ? jar.locale : name === "geminus_theme" ? jar.theme : undefined;
      return value === undefined ? undefined : { name, value };
    },
  }),
  // The request header proxy.ts sets (T-057): the request's nonce.
  headers: async () => new Headers(jar.nonce === undefined ? {} : { "x-nonce": jar.nonce }),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: () => {} }), usePathname: () => "/" }));

const { default: RootLayout, generateMetadata } = await import("../app/layout");
const { default: Home } = await import("../app/page");
const { default: NotFound, generateMetadata: notFoundMetadata } = await import("../app/not-found");
const { default: GlobalError } = await import("../app/global-error");
const { default: AuthError } = await import("../app/auth/error/page");
const { en } = await import("../messages/en");

async function shell(): Promise<string> {
  return renderToStaticMarkup(await RootLayout({ children: await Home() }));
}

beforeEach(() => {
  jar.locale = undefined;
  jar.theme = undefined;
  jar.nonce = undefined;
});

/** The rail (H-042): its navigation landmark's markup. */
function rail(out: string): string {
  const at = out.indexOf("<nav");
  return at < 0 ? "" : out.slice(at, out.indexOf("</nav>", at) + 6);
}

describe("server shell", () => {
  it("is English without a remembered choice", async () => {
    const out = await shell();
    expect(out).toMatch(/^<html lang="en">/);
    // H-042: the rail is the navigation: the brand is the way to the
    // projects, and signed out its foot is the settings (language and
    // theme, which open in a menu: tests/shell-h042.test.tsx).
    expect(rail(out)).toMatch(/^<nav aria-label="Workspace"/);
    expect(rail(out)).toMatch(/<a [^>]*aria-label="Projects"[^>]*href="\/projects"|<a [^>]*href="\/projects"[^>]*aria-label="Projects"/);
    expect(rail(out)).toContain('aria-label="Settings"');
    expect(out).toMatch(/<a href="#main"[^>]*>Skip to main content<\/a>/);
    expect(out).toMatch(/<main id="main" tabindex="-1"/);
    // H-048: the start page is the landing; signed out, its action is the sign-in.
    expect(out).toContain(en["landing.title"].replace(/'/g, "&#x27;"));
    expect(out).toContain(`>${en["landing.signIn"]}</a>`);
    expect(await generateMetadata()).toEqual({ title: "Geminus" });
  });

  it("is Traditional Chinese when that was chosen", async () => {
    jar.locale = "zh-Hant";
    const out = await shell();
    expect(out).toMatch(/^<html lang="zh-Hant">/);
    expect(rail(out)).toMatch(/^<nav aria-label="工作區"/);
    expect(rail(out)).toContain('aria-label="專案"');
    expect(rail(out)).toContain('aria-label="設定"');
    expect(out).toMatch(/<a href="#main"[^>]*>跳到主要內容<\/a>/);
    expect(out).toContain("先在工廠的數位分身上試一個改變，再動真正的產線。");
    expect(out).toContain(">登入試用</a>");
    expect(out).not.toContain(en["landing.title"].replace(/'/g, "&#x27;"));
    expect(out).not.toContain(en["landing.signIn"]);
  });

  it.each(["zh-hant", "zh-Hans", "zh", "EN", "", "zh-Hant; Path=/"])("treats %j as no choice", async (value) => {
    jar.locale = value;
    expect(await shell()).toMatch(/^<html lang="en">/);
  });

  it("follows the system's theme without a remembered choice", async () => {
    const out = await shell();
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).not.toContain("data-theme");
  });

  it.each(["light", "dark"] as const)("renders the remembered %s theme on <html>, so the first paint has it", async (theme) => {
    jar.theme = theme;
    const out = await shell();
    expect(out).toMatch(new RegExp(`^<html lang="en" data-theme="${theme}">`));
  });

  it.each(["Dark", "DARK", "system", "", "dark; Path=/", "blue"])("treats theme %j as the system's", async (value) => {
    jar.theme = value;
    const out = await shell();
    expect(out).not.toContain("data-theme");
  });

  it("frames one main region beside the rail and no side panel (H-027, H-042)", async () => {
    const out = await shell();
    expect(out.match(/<main\b/g)).toHaveLength(1);
    expect(out.match(/<nav\b/g)).toHaveLength(1);
    expect(out).not.toMatch(/<aside\b/);
    expect(out).not.toMatch(/<header\b/);
  });
});

// T-057: React Aria adds style elements of its own in the browser, which
// the policy's style-src-elem allows only with the request's nonce; it reads
// the nonce from this meta. Only a value in the nonce's shape is rendered.
describe("the request's nonce", () => {
  it("is handed to React Aria in a csp-nonce meta in the head", async () => {
    jar.nonce = "AAECAwQFBgcICQoLDA0ODw==";
    expect(await shell()).toMatch(/^<html lang="en"><head><meta name="csp-nonce" nonce="AAECAwQFBgcICQoLDA0ODw=="\/><\/head><body>/);
    jar.theme = "dark";
    jar.locale = "zh-Hant";
    expect(await shell()).toMatch(/^<html lang="zh-Hant" data-theme="dark"><head><meta name="csp-nonce" nonce="AAECAwQFBgcICQoLDA0ODw=="\/><\/head>/);
  });

  it.each([undefined, "", "AAECAwQFBgcICQoLDA0ODw", 'AAECAwQFBgcICQoLDA0OD"==', 'x" onload="alert(1)', "AAECAwQFBgcICQoLDA0ODw==x"])("renders no meta for %j", async (value) => {
    jar.nonce = value;
    const out = await shell();
    expect(out).not.toContain("csp-nonce");
    expect(out).not.toContain("nonce=");
  });
});

describe("pages for what failed (T-044 review M2)", () => {
  it("says a page was not found in the reader's language", async () => {
    jar.locale = "zh-Hant";
    const out = renderToStaticMarkup(await RootLayout({ children: await NotFound() }));
    expect(out).toMatch(/^<html lang="zh-Hant">/);
    expect(out).toContain("<h1>找不到此頁面</h1>");
    expect(out).not.toMatch(/could not be found/);
    expect(await notFoundMetadata()).toEqual({ title: "找不到此頁面" });
    jar.locale = undefined;
    expect(renderToStaticMarkup(await NotFound())).toContain("<h1>Page not found</h1>");
  });

  // T-045 review 1 I2 (T-043 code): the reason is looked up as the page's
  // own key only; a name Object.prototype has (toString, constructor, …)
  // made the page throw, which the server answered with a 500.
  it("says a sign-in failed for a reason it does not know, even one named like an object's own property", async () => {
    const page = async (reason?: string | string[]) =>
      renderToStaticMarkup(await AuthError({ searchParams: Promise.resolve(reason === undefined ? {} : { reason }) }));
    const text = (key: "authError.expired" | "authError.refused" | "authError.failed") => en[key].replace(/'/g, "&#x27;");
    expect(await page("expired")).toContain(text("authError.expired"));
    expect(await page("refused")).toContain(text("authError.refused"));
    for (const reason of ["failed", "toString", "constructor", "__proto__", "hasOwnProperty", "valueOf", "isPrototypeOf", "propertyIsEnumerable", "toLocaleString", "nonsense", "", undefined, ["expired", "refused"]]) {
      const out = await page(reason);
      expect(out, String(reason)).toContain(text("authError.failed"));
      expect(out).toContain('<a href="/auth/login">Sign in again</a>');
    }
  });

  it("replaces the whole document when the layout itself fails, in English and the system's theme until the browser reads the choices", () => {
    const out = renderToStaticMarkup(<GlobalError error={new Error("x")} retry={() => {}} />);
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).not.toContain("data-theme");
    expect(out).toContain("Something went wrong");
    expect(out).toContain("<title>Something went wrong</title>");
  });
});
