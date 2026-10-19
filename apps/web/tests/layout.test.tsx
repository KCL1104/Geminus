// REQ-022: the server components render in the reader's remembered
// language, English without one, and the document says which (html lang).
// next/headers is replaced by a cookie jar: outside a request Next.js has no
// cookies to read.
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";

const jar = vi.hoisted(() => ({ locale: undefined as string | undefined }));
vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) => (name === "geminus_locale" && jar.locale !== undefined ? { name, value: jar.locale } : undefined),
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
});

describe("server shell", () => {
  it("is English without a remembered choice", async () => {
    const out = await shell();
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).toMatch(/<nav aria-label="Main"( class="[^"]*")?><a href="\/projects">Projects<\/a><\/nav>/);
    expect(out).toMatch(/<a href="#main"[^>]*>Skip to main content<\/a>/);
    expect(out).toMatch(/<main id="main" tabindex="-1"/);
    expect(out).toContain("Go to projects");
    expect(out).toContain('<option value="en" lang="en" selected="">English</option>');
    expect(await generateMetadata()).toEqual({ title: "Geminus" });
  });

  it("is Traditional Chinese when that was chosen", async () => {
    jar.locale = "zh-Hant";
    const out = await shell();
    expect(out).toMatch(/^<html lang="zh-Hant">/);
    expect(out).toMatch(/<nav aria-label="主選單"( class="[^"]*")?><a href="\/projects">專案<\/a><\/nav>/);
    expect(out).toMatch(/<a href="#main"[^>]*>跳到主要內容<\/a>/);
    expect(out).toContain("前往專案");
    expect(out).toContain('<option value="zh-Hant" lang="zh-Hant" selected="">繁體中文</option>');
    expect(out).not.toContain("Go to projects");
  });

  it.each(["zh-hant", "zh-Hans", "zh", "EN", "", "zh-Hant; Path=/"])("treats %j as no choice", async (value) => {
    jar.locale = value;
    expect(await shell()).toMatch(/^<html lang="en">/);
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

  it("replaces the whole document when the layout itself fails, in English until the browser reads the choice", () => {
    const out = renderToStaticMarkup(<GlobalError error={new Error("x")} retry={() => {}} />);
    expect(out).toMatch(/^<html lang="en">/);
    expect(out).toContain("Something went wrong");
    expect(out).toContain("<title>Something went wrong</title>");
  });
});
