// T-015 (hackathon slice): the 2D editor in a browser, on the review
// preview (/ui-gallery/editor: the real model page, API client and draft
// session against an in-page stand-in for the model API; no session or Go
// API is needed). The main flow with the keyboard only (TEST-033), the
// mouse on the canvas, the 409 path, publish, the language switch keeping
// unsaved edits (left to T-015 by ADR-020), and axe in each state, both
// languages, light and dark (contrast included). Not in the screenshot
// spec: the editor's look is reviewed from local preview shots first.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { focusIsVisible, type Locale, locales, open, schemes, violations } from "./helpers";

const preview = (state = "problems") => `/ui-gallery/editor?state=${state}`;
const focused = (page: Page) => page.locator(":focus");
const status = (page: Page) => page.locator("[data-draft-status]");
const node = (page: Page, id: string) => page.locator(`.react-flow__node[data-id="${id}"]`);
const panelHeading = (page: Page) => page.locator("[data-panel=properties] h2");

/** Presses Tab (or Shift+Tab) until the focused element's name or text matches; every stop shows its focus. */
async function tabTo(page: Page, name: RegExp | string, back = false): Promise<void> {
  const stops: string[] = [];
  for (let i = 0; i < 120; i++) {
    await page.keyboard.press(back ? "Shift+Tab" : "Tab");
    const where = await page.evaluate(() => document.activeElement?.outerHTML.slice(0, 160) ?? "");
    stops.push(where.slice(0, 60));
    expect(await focusIsVisible(page), `focus is visible on ${where} after ${stops.slice(-4).join(" | ")}`).toBe(true);
    const label = await page.evaluate(() => {
      const el = document.activeElement as HTMLInputElement | null;
      return el?.getAttribute("aria-label") ?? el?.labels?.[0]?.textContent ?? el?.innerText ?? "";
    });
    if (typeof name === "string" ? label.trim() === name : name.test(label)) return;
  }
  throw new Error(`never reached ${name}`);
}

test.describe("the editor with the keyboard only", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("an issue goes to its step; select, move, edit, connect, add, delete and save without a mouse", async ({ page }) => {
    await open(page, preview(), "en");
    await page.locator("main").focus();

    // The issue list goes to the step and its field.
    await tabTo(page, /The batch sizes or the waiting limit are not valid/);
    await page.keyboard.press("Enter");
    await expect(focused(page)).toHaveAttribute("data-id", "burn_in");
    expect(await focusIsVisible(page)).toBe(true);
    await expect(panelHeading(page)).toHaveText("Burn-in");
    await expect(page.getByLabel("Largest batch")).toHaveAttribute("aria-invalid", "true");

    // Arrow keys move the selected step; Escape deselects and keeps the focus; Enter selects.
    const before = await node(page, "burn_in").evaluate((el) => getComputedStyle(el).transform);
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("Shift+ArrowDown");
    await expect(status(page)).toHaveText("Unsaved changes. Draft revision 3.");
    expect(await node(page, "burn_in").evaluate((el) => getComputedStyle(el).transform)).not.toBe(before);
    await expect(page.locator("[id^=react-flow__aria-live]")).toHaveText(/^Moved down\. Position x -?\d+, y -?\d+\.$/);
    await page.keyboard.press("Escape");
    await expect(panelHeading(page)).toHaveText("Properties");
    await expect(focused(page)).toHaveAttribute("data-id", "burn_in");
    await page.keyboard.press("Enter");
    await expect(panelHeading(page)).toHaveText("Burn-in");

    // A field in the properties: the server's error stays until a save says otherwise.
    await tabTo(page, "Largest batch");
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("100");
    await page.keyboard.press("Enter");
    await expect(page.getByLabel("Largest batch")).toHaveValue("100");

    // Connect from the properties' menu.
    await tabTo(page, "Connect to");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("menu")).toBeVisible();
    await page.keyboard.type("Scrap");
    await page.keyboard.press("Enter");
    await expect(page.getByRole("status").filter({ hasText: "Connected to Scrap." })).toBeVisible();
    await expect(page.getByRole("button", { name: "Remove the connection to Scrap" })).toBeVisible();

    // Add a step: the menu over the canvas, then its name.
    await tabTo(page, "Add step", true);
    await page.keyboard.press("Enter");
    await page.keyboard.type("Queue");
    await page.keyboard.press("Enter");
    await expect(page.getByLabel("Name")).toBeFocused();
    await page.keyboard.type("Rework rack");
    await page.keyboard.press("Enter");
    await expect(node(page, "queue_1")).toContainText("Rework rack");

    // Delete it from the canvas: Delete asks first; Cancel is where the focus starts.
    await node(page, "queue_1").focus();
    await page.keyboard.press("Delete");
    const dialog = page.getByRole("alertdialog", { name: "Delete Rework rack?" });
    await expect(dialog).toBeVisible();
    await expect(focused(page)).toHaveText("Cancel");
    await page.keyboard.press("Tab");
    await expect(focused(page)).toHaveText("Delete");
    await page.keyboard.press("Enter");
    await expect(node(page, "queue_1")).toHaveCount(0);
    await expect(page.getByRole("status").filter({ hasText: "Rework rack deleted." })).toBeVisible();
    // The focus goes to Add step on the next animation frame, after the status.
    await expect(page.getByRole("button", { name: "Add step" })).toBeFocused();
    expect(await focusIsVisible(page)).toBe(true);

    // Save: one If-Match save of all of it; the issues are the server's again.
    await tabTo(page, "Save draft", true);
    await page.keyboard.press("Enter");
    await expect(status(page)).toHaveText("All changes saved. Draft revision 4.");
    await expect(focused(page)).toHaveText("Save draft");
    await expect(page.getByRole("button", { name: "Save draft" })).toHaveAttribute("aria-disabled", "true");
  });
});

test.describe("the editor with a mouse", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("dragging a step moves it, dragging between handles connects two steps", async ({ page }) => {
    await open(page, preview("clean"), "en");
    // In a 900 px window the canvas starts low on the page (the ask form
    // above it has its data notice since T-057, and the blocked actions'
    // reason appears after the first edit): the person scrolls to it before
    // using the mouse on it.
    await page.locator(".react-flow").scrollIntoViewIfNeeded();
    const smt = node(page, "smt");
    const box = (await smt.boundingBox())!;
    await page.mouse.move(box.x + box.width / 2, box.y + 10);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 + 40, box.y + 90, { steps: 8 });
    await page.mouse.up();
    await expect(status(page)).toHaveText("Unsaved changes. Draft revision 3.");
    const after = (await smt.boundingBox())!;
    expect(after.y).toBeGreaterThan(box.y + 40);

    const from = (await node(page, "aoi").locator(".react-flow__handle.source").boundingBox())!;
    const to = (await node(page, "q_smt").locator(".react-flow__handle.target").boundingBox())!;
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2, { steps: 12 });
    await page.mouse.up();
    await expect(page.getByRole("status").filter({ hasText: "Connected to Before SMT." })).toBeVisible();
    await expect(page.locator('.react-flow__edge[data-id="aoi_q_smt"]')).toHaveCount(1);

    await page.getByRole("button", { name: "Save draft" }).click();
    await expect(status(page)).toHaveText("All changes saved. Draft revision 4.");
  });
});

// T-033: the read-only 3D view of the same steps, by the same IDs and the same selection.
test.describe("the editor's 3D view", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("shows the line read-only, shares the selection with 2D, and goes back to 2D", async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await open(page, preview(), "en");
    await node(page, "burn_in").click();
    await expect(panelHeading(page)).toHaveText("Burn-in");
    await page.getByRole("radio", { name: "3D" }).click();
    // No authoring in 3D: no Add step, no fit; the 2D canvas is gone.
    await expect(page.getByRole("button", { name: "Add step" })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Show all steps" })).toHaveCount(0);
    await expect(page.locator(".react-flow")).toHaveCount(0);
    // WebGL in this browser or not, the view answers once it has loaded: the scene, or why it cannot run.
    await expect(page.getByText("Loading the 3D view…")).toHaveCount(0, { timeout: 30_000 });
    const scene = page.getByRole("img", { name: /^3D view of the line: \d+ steps$/ });
    const unavailable = page.getByText("The 3D view cannot run in this browser.", { exact: false });
    await expect(scene.or(unavailable)).toBeVisible();
    if (await scene.isVisible()) {
      // The selection made in 2D is the 3D view's too.
      await expect(page.getByText("Selected: Burn-in.")).toBeVisible();
      // Something is drawn, and the keys turn the camera: the picture changes (review r1 M5).
      const before = await scene.screenshot();
      await scene.focus();
      for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowLeft");
      const after = await scene.screenshot();
      expect(after.equals(before)).toBe(false);
      // Escape on the scene clears the selection for both views.
      await page.keyboard.press("Escape");
      await expect(panelHeading(page)).toHaveText("Properties");
    }
    expect(await violations(page)).toEqual([]);
    await page.getByRole("radio", { name: "2D" }).click();
    await expect(node(page, "burn_in")).toBeVisible();
    expect(errors).toEqual([]);
  });
});

test.describe("saving against the server's revision", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("a 409 keeps the edits; they go onto the current draft only when the person asks", async ({ page }) => {
    await open(page, preview("conflict"), "en");
    await node(page, "q_aoi").click();
    await page.getByLabel("Capacity").fill("25");
    await page.getByLabel("Capacity").press("Enter");
    await page.getByRole("button", { name: "Save draft" }).click();
    await expect(status(page)).toHaveText("Not saved: the draft changed on the server. Draft revision 3.");
    await expect(page.getByText("Your changes are kept on this page until you leave or reload it.")).toBeVisible();
    await expect(page.getByLabel("Capacity")).toHaveValue("25");
    await page.getByRole("button", { name: "Load the current draft and reapply my changes" }).click();
    await expect(status(page)).toHaveText("Unsaved changes. Draft revision 4.");
    await expect(node(page, "smt")).toContainText("SMT (line 2)");
    await expect(page.getByRole("button", { name: "Save draft" })).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(status(page)).toHaveText("All changes saved. Draft revision 5.");
  });

  test("publish asks first, then names the new version; Publish then waits for a change", async ({ page }) => {
    await open(page, preview("clean"), "en");
    const publish = page.getByRole("button", { name: "Publish version" });
    await publish.click();
    const dialog = page.getByRole("alertdialog", { name: "Publish version 2?" });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Publish" }).click();
    await expect(page.getByRole("status").filter({ hasText: "Version 2 published." })).toBeVisible();
    await expect(publish).toHaveAttribute("aria-disabled", "true");
    await expect(page.getByText("No changes since version 2.")).toBeVisible();
  });

  test("switching the language keeps the unsaved edits (ADR-020)", async ({ page }) => {
    await open(page, preview("clean"), "en");
    await node(page, "smt").click();
    await page.getByLabel("Name").fill("SMT line 1");
    await page.getByLabel("Name").press("Enter");
    await expect(status(page)).toHaveText("Unsaved changes. Draft revision 3.");
    await page.getByRole("button", { name: "Settings" }).click();
    await page.getByRole("radiogroup", { name: "Language" }).getByRole("radio", { name: "繁體中文" }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "zh-Hant");
    await expect(status(page)).toHaveText("有尚未儲存的變更。草稿修訂版 3。");
    await expect(node(page, "smt")).toContainText("SMT line 1");
    await expect(page.getByRole("button", { name: "儲存草稿" })).not.toHaveAttribute("aria-disabled", "true");
  });
});

// TEST-033: axe in the states the editor has, both languages, light and dark.
const words: Record<Locale, Record<string, string>> = {
  en: { add: "Add step", connect: "Connect to", del: "Delete step", publish: "Publish version", save: "Save draft", capacity: "Capacity" },
  "zh-Hant": { add: "新增步驟", connect: "連接到", del: "刪除步驟", publish: "發布版本", save: "儲存草稿", capacity: "容量" },
};

for (const scheme of schemes) for (const locale of locales) {
  test.describe(`editor states, ${scheme}, ${locale}`, () => {
    test.use({ viewport: { width: 1280, height: 900 }, colorScheme: scheme });
    const w = words[locale];

    test("problems, a step selected, its menus and dialog have no violation", async ({ page }) => {
      await open(page, preview(), locale);
      await page.locator('li button:has-text("Burn-in")').click();
      await expect(panelHeading(page)).toHaveText("Burn-in");
      expect(await violations(page), "a step selected").toEqual([]);
      await page.getByRole("button", { name: w.add }).click();
      await expect(page.getByRole("menu")).toBeVisible();
      expect(await violations(page), "step menu open").toEqual([]);
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: w.connect }).click();
      await expect(page.getByRole("menu")).toBeVisible();
      expect(await violations(page), "connect menu open").toEqual([]);
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: w.del }).click();
      await expect(page.getByRole("alertdialog")).toBeVisible();
      expect(await violations(page), "delete dialog open").toEqual([]);
    });

    test("ready to publish, the publish dialog, a viewer and a conflict have no violation", async ({ page }) => {
      await open(page, preview("clean"), locale);
      expect(await violations(page), "ready to publish").toEqual([]);
      await page.getByRole("button", { name: w.publish }).click();
      await expect(page.getByRole("alertdialog")).toBeVisible();
      expect(await violations(page), "publish dialog open").toEqual([]);

      await open(page, preview("viewer"), locale);
      await node(page, "q_aoi").click();
      expect(await violations(page), "viewer").toEqual([]);

      await open(page, preview("conflict"), locale);
      await node(page, "q_aoi").click();
      await page.getByLabel(w.capacity).fill("25");
      await page.getByLabel(w.capacity).press("Enter");
      await page.getByRole("button", { name: w.save }).click();
      await expect(status(page)).toHaveAttribute("data-draft-status", "conflict");
      expect(await violations(page), "conflict").toEqual([]);
    });
  });
}
