// H-013 / T-045: the theme follows the system unless the reader chose one;
// the choice is remembered (the geminus_theme cookie) and the server sends
// it on <html data-theme>, so the first paint is already in that theme: no
// script runs before paint and there is no flash of the other one. The
// fonts load from the app's own origin.
import { expect, test } from "@playwright/test";
import { open, sheet } from "./helpers";

const origin = "http://127.0.0.1:3100";
const body = (page: import("@playwright/test").Page) => page.locator("body");

test("follows the system's light or dark when the reader chose none", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await open(page, "/", "en");
  await expect(page.locator("html")).not.toHaveAttribute("data-theme");
  await expect(body(page)).toHaveCSS("background-color", sheet.dark);
  await page.emulateMedia({ colorScheme: "light" });
  await expect(body(page)).toHaveCSS("background-color", sheet.light);
});

test("a choice overrides the system, applies at once and is remembered", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await open(page, "/", "en");
  await page.getByRole("button", { name: "System Theme" }).click();
  await page.getByRole("option", { name: "Light" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(body(page)).toHaveCSS("background-color", sheet.light);
  const cookie = (await page.context().cookies(origin)).find((c) => c.name === "geminus_theme");
  expect([cookie?.value, cookie?.sameSite, cookie?.httpOnly, cookie?.path]).toEqual(["light", "Lax", false, "/"]);
  // Another page, and a reload: the server renders the choice.
  await page.getByRole("link", { name: "Projects", exact: true }).click();
  await expect(page.locator("h1")).toHaveText("Projects");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(body(page)).toHaveCSS("background-color", sheet.light);
});

test("going back to the system's forgets the choice", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await open(page, "/", "en", "light");
  await page.getByRole("button", { name: "Light Theme" }).click();
  await page.getByRole("option", { name: "System" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme");
  await expect(body(page)).toHaveCSS("background-color", sheet.dark);
  expect((await page.context().cookies(origin)).find((c) => c.name === "geminus_theme")).toBeUndefined();
});

test("the first paint has the remembered theme: it is in the server's HTML, with the stylesheets in <head>", async ({ page }) => {
  await page.context().addCookies([{ name: "geminus_theme", value: "dark", url: origin }]);
  const html = await (await page.request.get("/")).text();
  expect(html).toMatch(/^<!DOCTYPE html><html lang="en" data-theme="dark">/);
  const head = html.slice(0, html.indexOf("</head>"));
  expect(head).toMatch(/<link rel="stylesheet"/);
  // No inline script runs before paint: the head has only Next.js's own
  // async chunks, so no script of ours needs a CSP nonce or hash.
  expect(head).not.toMatch(/<script(?![^>]*\bsrc=)[^>]*>/);
});

test("the fonts come from the app's own origin, and the Chinese face only for what a page shows", async ({ page }) => {
  const fonts: string[] = [];
  page.on("request", (r) => {
    if (r.resourceType() === "font") fonts.push(r.url());
  });
  await open(page, "/", "zh-Hant");
  expect(fonts.length).toBeGreaterThan(0);
  expect(fonts.filter((u) => !u.startsWith(`${origin}/_next/static/media/`))).toEqual([]);
  expect(fonts.some((u) => /archivo-latin-standard-normal/.test(u))).toBe(true);
  const chinese = fonts.filter((u) => /noto-sans-tc-\d+-wght-normal/.test(u));
  expect(chinese.length).toBeGreaterThan(0);
  expect(chinese.length).toBeLessThan(40);
  expect(await page.evaluate(() => document.fonts.check('13.5px "Archivo Variable"'))).toBe(true);
});
