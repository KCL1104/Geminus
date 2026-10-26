// REQ-022: the server components render in the reader's remembered
// language, English without one, and the document says which (html lang).
// H-013: the reader's remembered theme is on <html data-theme> from the
// server, so the first paint has it; without one the system's applies.
// next/headers is replaced by a cookie jar: outside a request Next.js has no
// cookies to read.
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined, theme: undefined as string | undefined }));
vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) => {
      const value = name === "geminus_locale" ? jar.locale : name === "geminus_theme" ? jar.theme : undefined;
      return value === undefined ? undefined : { name, value };
    },
  }),
}));
vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: () => {} }) }));

const { default: RootLayout, generateMetadata } = await import("../app/layout");
const { default: Home } = await import("../app/page");
const { default: NotFound, generateMetadata: notFoundMetadata } = await import("../app/not-found");
const { default: GlobalError } = await import("../app/global-error");

async function shell(): Promise<string> {
  return renderToStaticMarkup(await RootLayout({ children: await Home() }));
}

beforeEach(() => {
  jar.locale = undefined;
  jar.theme = undefined;
});

/** The text of the strip's Select for label: the visible value (the design system's Select, T-045). */
function chosen(out: string, label: string): string | undefined {
  const at = out.indexOf(`>${label}</span>`);
  return at < 0 ? undefined : /aria-haspopup="listbox"[^>]*><span[^>]*>(<span[^>]*>[^<]*<\/span>)/.exec(out.slice(at))?.[1];
}

describe("server shell", () => {
  it("is English without a remembered choice", async () => {
    const out = await shell();
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).toMatch(/<nav aria-label="Main"( class="[^"]*")?><a href="\/projects">Projects<\/a><\/nav>/);
    expect(out).toMatch(/<a href="#main"[^>]*>Skip to main content<\/a>/);
    expect(out).toMatch(/<main id="main" tabindex="-1"/);
    expect(out).toContain("Go to projects");
    // The language choice names each language in itself (ADR-020); the
    // chosen one is the Select's value. (Was a native <select>; it is the
    // design system's Select since the re-skin, T-045.)
    expect(chosen(out, "Language")).toBe('<span lang="en">English</span>');
    expect(out).toContain('<option value="zh-Hant">繁體中文</option>');
    expect(await generateMetadata()).toEqual({ title: "Geminus" });
  });

  it("is Traditional Chinese when that was chosen", async () => {
    jar.locale = "zh-Hant";
    const out = await shell();
    expect(out).toMatch(/^<html lang="zh-Hant">/);
    expect(out).toMatch(/<nav aria-label="主選單"( class="[^"]*")?><a href="\/projects">專案<\/a><\/nav>/);
    expect(out).toMatch(/<a href="#main"[^>]*>跳到主要內容<\/a>/);
    expect(out).toContain("前往專案");
    expect(chosen(out, "語言")).toBe('<span lang="zh-Hant">繁體中文</span>');
    expect(chosen(out, "主題")).toBe("<span>跟隨系統</span>");
    expect(out).not.toContain("Go to projects");
  });

  it.each(["zh-hant", "zh-Hans", "zh", "EN", "", "zh-Hant; Path=/"])("treats %j as no choice", async (value) => {
    jar.locale = value;
    expect(await shell()).toMatch(/^<html lang="en">/);
  });

  it("follows the system's theme without a remembered choice", async () => {
    const out = await shell();
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).not.toContain("data-theme");
    expect(chosen(out, "Theme")).toBe("<span>System</span>");
  });

  it.each(["light", "dark"] as const)("renders the remembered %s theme on <html>, so the first paint has it", async (theme) => {
    jar.theme = theme;
    const out = await shell();
    expect(out).toMatch(new RegExp(`^<html lang="en" data-theme="${theme}">`));
    expect(chosen(out, "Theme")).toBe(`<span>${theme === "dark" ? "Dark" : "Light"}</span>`);
  });

  it.each(["Dark", "DARK", "system", "", "dark; Path=/", "blue"])("treats theme %j as the system's", async (value) => {
    jar.theme = value;
    const out = await shell();
    expect(out).not.toContain("data-theme");
    expect(chosen(out, "Theme")).toBe("<span>System</span>");
  });

  it("frames one main region and no side panel (H-027)", async () => {
    const out = await shell();
    expect(out.match(/<main\b/g)).toHaveLength(1);
    expect(out).not.toMatch(/<aside\b/);
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

  it("replaces the whole document when the layout itself fails, in English and the system's theme until the browser reads the choices", () => {
    const out = renderToStaticMarkup(<GlobalError error={new Error("x")} retry={() => {}} />);
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).not.toContain("data-theme");
    expect(out).toContain("Something went wrong");
    expect(out).toContain("<title>Something went wrong</title>");
  });
});
