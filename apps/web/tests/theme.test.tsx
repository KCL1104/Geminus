// @vitest-environment happy-dom
// H-013 / T-045: the theme follows the system unless the reader chose one;
// the choice applies at once (<html data-theme>) and is remembered in the
// theme cookie, which holds "light" or "dark" and nothing else. The server
// side (the layout renders the remembered theme on <html>) is in
// tests/layout.test.tsx; the browser checks are apps/web-e2e/tests/theme.spec.ts.
import { RequestCookies } from "next/dist/compiled/@edge-runtime/cookies";
import { afterEach, describe, expect, it } from "vitest";
import { applyTheme, ThemeSwitcher, useRememberedTheme } from "../app/_components/theme-switcher";
import { cookieTheme, parseTheme, themeAttribute, themeCookie, themeCookieValue, themes } from "../lib/theme/theme";
import { mount, press, settle, violations } from "./ui-harness";

afterEach(() => {
  document.cookie = `${themeCookie}=; Max-Age=0; Path=/`;
  delete document.documentElement.dataset.theme;
});


describe("theme values", () => {
  it("are the system's, light and dark; only exactly light or dark is a choice", () => {
    expect(themes).toEqual(["system", "light", "dark"]);
    expect(["light", "dark"].map(parseTheme)).toEqual(["light", "dark"]);
    for (const v of [undefined, null, "", "system", "Dark", "LIGHT", " dark", "dark ", "blue", "dark; Path=/"]) expect(parseTheme(v), String(v)).toBe("system");
  });

  it("read the last cookie of the name, as the locale does", () => {
    expect(cookieTheme("")).toBe("system");
    expect(cookieTheme("geminus_locale=zh-Hant; geminus_theme=dark")).toBe("dark");
    expect(cookieTheme("geminus_theme=dark; geminus_theme=light")).toBe("light");
    expect(cookieTheme("xgeminus_theme=dark")).toBe("system");
  });

  // T-045 review 1 I3: the server reads the cookie with Next.js
  // cookies().get, which percent-decodes the value and skips a pair that
  // does not decode; the browser (global-error) must read it the same way.
  it("read the cookie as the server does: percent-decoded, a value that does not decode skipped", () => {
    expect(cookieTheme("geminus_theme=%64ark")).toBe("dark");
    expect(cookieTheme("geminus_theme=light; geminus_theme=dark%")).toBe("light");
    const headers = [
      "", "geminus_theme=dark", "geminus_theme=%64ark", "geminus_theme=%6C%69%67%68%74", "geminus_theme=dark; geminus_theme=light",
      "geminus_theme=dark;geminus_theme=light", "geminus_theme=light; geminus_theme=dark%", "geminus_theme=dark%; geminus_theme=light",
      "geminus_theme=%E0%A4%A", "geminus_theme", "geminus_theme=dark; geminus_theme", "geminus_theme = dark", "geminus_theme=dark=1",
      'geminus_theme="dark"', "geminus_theme=%20dark", "a=%; geminus_theme=dark", "xgeminus_theme=dark", "geminus_theme=DARK",
      "geminus_theme=dark;; geminus_theme=light", "geminus_theme=%2564ark",
    ];
    for (const header of headers) {
      const server = parseTheme(new RequestCookies(new Headers({ cookie: header })).get(themeCookie)?.value);
      expect(cookieTheme(header), header).toBe(server);
    }
  });

  it("are remembered for a year, or forgotten when the reader goes back to the system's", () => {
    expect(themeCookieValue("dark", false)).toBe("geminus_theme=dark; Max-Age=31536000; Path=/; SameSite=Lax");
    expect(themeCookieValue("light", true)).toBe("geminus_theme=light; Max-Age=31536000; Path=/; SameSite=Lax; Secure");
    expect(themeCookieValue("system", true)).toBe("geminus_theme=; Path=/; SameSite=Lax; Secure; Max-Age=0");
    expect(themes.map(themeAttribute)).toEqual([undefined, "light", "dark"]);
  });

  it("set the document's attribute, none for the system's", () => {
    const html = document.documentElement;
    applyTheme("dark");
    expect(html.dataset.theme).toBe("dark");
    applyTheme("light");
    expect(html.dataset.theme).toBe("light");
    applyTheme("system");
    expect(html.hasAttribute("data-theme")).toBe(false);
  });
});

describe("ThemeSwitcher", () => {
  // H-042: a three-way choice in the account menu (a radio group): each press applies and remembers.
  const radios = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>("[role=radio]")];
  const checked = (el: HTMLElement) => radios(el).find((r) => r.getAttribute("aria-checked") === "true")?.textContent;
  const pick = async (el: HTMLElement, name: string) => {
    await press(radios(el).find((r) => r.textContent === name));
    await settle();
  };

  it("is a labelled three-way choice that applies and remembers each choice", async () => {
    const el = await mount(<ThemeSwitcher initial="system" />);
    expect(el.querySelector("[role=radiogroup]")?.getAttribute("aria-label")).toBe("Theme");
    expect(radios(el).map((r) => r.textContent)).toEqual(["System", "Light", "Dark"]);
    expect(checked(el)).toBe("System");
    await pick(el, "Dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(cookieTheme(document.cookie)).toBe("dark");
    expect(checked(el)).toBe("Dark");

    await pick(el, "Light");
    expect([document.documentElement.dataset.theme, cookieTheme(document.cookie)]).toEqual(["light", "light"]);

    await pick(el, "System");
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    expect(document.cookie).not.toContain("geminus_theme");
    expect(await violations()).toEqual([]);
  });

  it("starts from the theme the server rendered, in the reader's language", async () => {
    const el = await mount(<ThemeSwitcher initial="dark" />, "zh-Hant");
    expect(checked(el)).toBe("深色");
    expect(el.querySelector("[role=radiogroup]")?.getAttribute("aria-label")).toBe("主題");
  });

  it("starts from the remembered choice when the menu opens again later (H-042)", async () => {
    document.cookie = "geminus_theme=light; Path=/";
    const el = await mount(<ThemeSwitcher initial="dark" />);
    expect(checked(el)).toBe("Light");
  });

  it("is read back in the browser for the page that replaces a failed layout", async () => {
    function Probe() {
      return <p>{useRememberedTheme()}</p>;
    }
    expect((await mount(<Probe />)).textContent).toBe("system");
    document.cookie = "geminus_theme=dark; Path=/";
    expect((await mount(<Probe />)).textContent).toBe("dark");
  });
});
