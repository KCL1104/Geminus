// H-045: a new model from a template on the project page, in a real
// browser, signed in through the BFF and the test provider as an editor
// of Line Works, against the fake API's createModel (fake/servers.mjs: the
// pack's template file as it is, nothing built or validated as the server
// does). With the keyboard only: open the form, move to a template with
// the arrows, keep its name, create, and land on the new model's canvas
// with its domain named; in both languages, with axe on the open form.
// At the phone width the form fits without sideways scrolling. A viewer
// is not offered it. The fake's tenant data is put back before and after
// each test (POST /__reset), so the other specs see one model.
import type { APIRequestContext, Locator, Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { APP, focusIsVisible, locales, PROVIDER, settled, signInAs, tenant, violations } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
type ApiCall = { method: string; path: string; key?: string | null; body?: unknown };
const apiCalls = async (request: APIRequestContext): Promise<ApiCall[]> => ((await (await request.get(`${PROVIDER}/__state`)).json()) as { apiCalls: ApiCall[] }).apiCalls;
const project = `/projects/${tenant.PRJ_LINE}`;

/** Moves focus with Tab until target has it, the focus visible at every stop. */
async function keyTo(page: Page, target: Locator) {
  for (let i = 0; i < 60; i++) {
    await page.keyboard.press("Tab");
    if (await target.evaluate((el) => el === document.activeElement)) {
      expect(await focusIsVisible(page), "focus is visible").toBe(true);
      return;
    }
  }
  throw new Error(`Tab never reached ${await target.evaluate((el) => el.outerHTML.slice(0, 120))}`);
}

test.describe("a new model from a template, signed in, on the fake API", () => {
  test.beforeEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
    await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
  });
  test.afterEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
  });

  for (const locale of locales) {
    test(`${locale}: creates the shoe factory with the keyboard only and opens its canvas`, async ({ page, request }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, project, locale);
      const before = (await apiCalls(request)).length;

      await page.locator("main").focus();
      await keyTo(page, page.getByRole("button", { name: t["newModel.open"] }));
      await page.keyboard.press("Enter");
      const form = page.locator("[data-new-model]");
      await expect(form.getByRole("heading", { level: 2 })).toHaveText(t["newModel.title"]);
      for (const d of ["electronics_line", "production_logistics", "software_delivery"] as const) {
        await expect(form.getByRole("group", { name: t[`domain.${d}.name`] })).toBeVisible();
      }
      const first = form.getByRole("radio", { name: t["template.electronics_assembly_test_line.name"] });
      await expect(first).toBeFocused();
      await expect(first).toBeChecked();
      expect(await focusIsVisible(page), "the chosen template's focus is visible").toBe(true);
      expect(await violations(page), "the open form").toEqual([]);

      // H-062: the electronics line's photo option comes before the next group's templates.
      await page.keyboard.press("ArrowDown");
      await expect(form.getByRole("radio", { name: t["template.photo_start.name"] })).toBeFocused();
      await page.keyboard.press("ArrowDown");
      const shoes = form.getByRole("radio", { name: t["template.shoe_factory.name"] });
      await expect(shoes).toBeFocused();
      await expect(shoes).toBeChecked();
      await expect(shoes).toHaveAccessibleDescription(t["template.shoe_factory.description"]);
      const name = form.getByLabel(t["newModel.name"]);
      await expect(name).toHaveValue(t["template.shoe_factory.name"]);
      await keyTo(page, form.getByRole("button", { name: t["newModel.create"] }));
      await page.keyboard.press("Enter");

      await page.waitForURL(new RegExp(`^${APP}/models/mdl_[0-9A-Z]{26}$`));
      await settled(page, locale);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(t["template.shoe_factory.name"]);
      await expect(page.locator("main header:has(h1) dl")).toContainText(t["domain.production_logistics.name"]);
      // The 2D canvas has the template's steps, named in the reader's language.
      await expect(page.locator(".react-flow__node[data-id=stitching]")).toContainText(locale === "en" ? "Stitching (closing)" : "針車（鞋面縫合）");
      expect(await violations(page), "the new model's canvas").toEqual([]);

      const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
      expect(posts).toHaveLength(1);
      expect(posts[0]!.path).toBe(`/v1/projects/${tenant.PRJ_LINE}/models`);
      expect(posts[0]!.body).toEqual({ name: t["template.shoe_factory.name"], domain: "production_logistics", template: "shoe_factory", locale });
      expect(posts[0]!.key).toMatch(/^[\x21-\x7e]{8,128}$/);
    });
  }

  test("Cancel closes the form and gives the focus back to the action; the list stays", async ({ page }) => {
    await signInAs(page, tenant.editor, project, "en");
    await page.getByRole("button", { name: en["newModel.open"] }).click();
    await page.locator("[data-new-model]").getByRole("button", { name: en["newModel.cancel"] }).click();
    await expect(page.locator("[data-new-model]")).toHaveCount(0);
    await expect(page.getByRole("button", { name: en["newModel.open"] })).toBeFocused();
    await expect(page.getByRole("link", { name: tenant.modelName })).toBeVisible();
  });

  for (const locale of locales) {
    test(`${locale}: at 390 px the open form fits the width, nothing scrolls sideways`, async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, project, locale);
      await page.getByRole("button", { name: t["newModel.open"] }).click();
      const form = page.locator("[data-new-model]");
      // Six templates and the photo option (H-062).
      await expect(form.getByRole("radio")).toHaveCount(7);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      for (const box of await form.locator("label").evaluateAll((els) => els.map((e) => e.getBoundingClientRect().right))) expect(box).toBeLessThanOrEqual(390);
      expect(await violations(page), "the open form at 390 px").toEqual([]);
    });
  }

  test("a viewer is not offered a new model", async ({ page }) => {
    await signInAs(page, tenant.viewer, project, "en");
    await expect(page.getByRole("link", { name: tenant.modelName })).toBeVisible();
    await expect(page.getByRole("button", { name: en["newModel.open"] })).toHaveCount(0);
  });
});
