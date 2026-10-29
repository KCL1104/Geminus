// TEST-033: the main flows with the keyboard only, with a visible focus at
// every stop and a sensible order.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { focusIsVisible, open } from "./helpers";

async function tab(page: Page, times = 1, back = false): Promise<void> {
  for (let i = 0; i < times; i++) await page.keyboard.press(back ? "Shift+Tab" : "Tab");
  expect(await focusIsVisible(page), "focus is visible").toBe(true);
}

const focused = (page: Page) => page.locator(":focus");

// The language and theme choices are in the rail's settings menu (H-042):
// two radio groups, whose arrow keys move between the items; only a press
// changes the page.
test("the shell: skip link first, then the rail's brand and settings; the skip link moves focus to main", async ({ page }) => {
  await open(page, "/", "en");
  await tab(page);
  await expect(focused(page)).toHaveText("Skip to main content");
  await expect(focused(page)).toBeInViewport();
  await tab(page);
  // H-042: the rail: the brand (the projects), then the settings.
  await expect(focused(page)).toHaveAccessibleName("Projects");
  await tab(page);
  await expect(focused(page)).toHaveAccessibleName("Settings");
  await tab(page);
  await expect(focused(page)).toHaveText("Go to projects");
  for (let i = 0; i < 3; i++) await page.keyboard.press("Shift+Tab");
  await expect(focused(page)).toHaveText("Skip to main content");
  await page.keyboard.press("Enter");
  await expect(page.locator("main")).toBeFocused();
});

test("the language changes from the keyboard, only on a choice, and focus stays on the control", async ({ page }) => {
  await open(page, "/", "en");
  await tab(page, 3);
  await expect(focused(page)).toHaveAccessibleName("Settings");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  // The menu opens on the language's chosen item.
  await expect(focused(page)).toHaveText("English");
  // Moving between the languages does not change the language (WCAG 3.2.2).
  await page.keyboard.press("ArrowRight");
  await expect(focused(page)).toHaveText("繁體中文");
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await page.keyboard.press("Enter");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-Hant");
  await expect(page.getByRole("link", { name: "前往專案" })).toBeVisible();
  await expect(focused(page)).toHaveText("繁體中文");
  await expect(focused(page)).toHaveAttribute("aria-checked", "true");
  expect(await focusIsVisible(page)).toBe(true);
  // Nor does typing a letter or moving back without a press.
  await page.keyboard.press("e");
  await page.keyboard.press("ArrowLeft");
  await expect(page.locator("html")).toHaveAttribute("lang", "zh-Hant");
});

test("the theme changes from the keyboard and is remembered", async ({ page }) => {
  await open(page, "/", "en");
  await tab(page, 3);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  // From the language to the theme's chosen item, then along it to Dark.
  await page.keyboard.press("Tab");
  await expect(focused(page)).toHaveText("System");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await expect(focused(page)).toHaveText("Dark");
  await page.keyboard.press("Enter");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(focused(page)).toHaveAttribute("aria-checked", "true");
  expect(await focusIsVisible(page)).toBe(true);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test.describe("component gallery", () => {
  test.beforeEach(async ({ page }) => {
    await open(page, "/ui-gallery", "en");
    await page.locator("main").focus();
  });

  async function tabTo(page: Page, name: string): Promise<void> {
    for (let i = 0; i < 60; i++) {
      await tab(page);
      if ((await focused(page).getAttribute("aria-label")) === name || (await focused(page).innerText().catch(() => "")) === name) return;
    }
    throw new Error(`never reached ${name}`);
  }

  test("a status message after an action", async ({ page }) => {
    await tabTo(page, "Save draft");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("status").first()).toHaveText("Draft saved.");
    await expect(focused(page)).toHaveText("Save draft");
  });

  test("a confirmation dialog: focus on Cancel, trapped, Escape returns focus, confirm acts", async ({ page }) => {
    await tabTo(page, "Discard changes");
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("alertdialog", { name: "Discard your changes?" });
    await expect(dialog).toBeVisible();
    await expect(focused(page)).toHaveText("Keep editing");
    await tab(page);
    await expect(focused(page)).toHaveText("Discard");
    await tab(page);
    await expect(focused(page)).toHaveText("Keep editing");
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(focused(page)).toHaveText("Discard changes");
    await page.keyboard.press("Enter");
    await tab(page);
    await page.keyboard.press("Enter");
    await expect(dialog).toBeHidden();
    await expect(page.getByRole("status").first()).toHaveText("Changes discarded.");
    await expect(focused(page)).toHaveText("Discard changes");
  });

  test("a select: open, move, choose", async ({ page }) => {
    await tabTo(page, "Minutes");
    await page.keyboard.press("ArrowDown");
    await expect(page.getByRole("listbox")).toBeVisible();
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("listbox")).toBeHidden();
    await expect(focused(page)).toContainText("Hours");
  });

  test("a table: one tab stop, arrows move, Enter sorts, Space selects", async ({ page }) => {
    const grid = page.getByRole("grid", { name: "Validation issues", exact: true });
    for (let i = 0; i < 60 && !(await grid.evaluate((g) => g.contains(document.activeElement))); i++) await tab(page);
    await expect(grid).toContainText("/flows/f3/probability");
    await page.keyboard.press("ArrowDown");
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Space");
    await expect(grid.locator("[aria-selected=true]")).toHaveCount(1);
    // Up into the header row, then along it to "Path".
    for (let i = 0; i < 10 && (await focused(page).getAttribute("role")) !== "columnheader"; i++) await page.keyboard.press("ArrowUp");
    for (let i = 0; i < 10 && !(await focused(page).innerText()).includes("Path"); i++) await page.keyboard.press("ArrowRight");
    const header = grid.getByRole("columnheader", { name: /Path/ });
    await expect(header).toBeFocused();
    expect(await focusIsVisible(page)).toBe(true);
    await page.keyboard.press("Enter");
    await expect(header).toHaveAttribute("aria-sort", "descending");
    await tab(page);
    expect(await grid.evaluate((g) => g.contains(document.activeElement)), "Tab leaves the grid").toBe(false);
  });

  test("a long table is virtualized and reachable to its last row", async ({ page }) => {
    const grid = page.getByRole("grid", { name: "Validation issues, long list" });
    await expect(grid).toHaveAttribute("aria-rowcount", "501");
    expect(await grid.getByRole("row").count()).toBeLessThan(60);
    await grid.getByRole("row").nth(1).getByRole("gridcell").first().click();
    await page.keyboard.press("Control+End");
    await expect.poll(() => page.evaluate(() => document.activeElement?.closest("[role=row]")?.getAttribute("aria-rowindex"))).toBe("501");
  });
});
