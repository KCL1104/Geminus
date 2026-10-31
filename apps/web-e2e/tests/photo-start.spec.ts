// H-062: a new model from a photo or floor plan, in a real browser, signed
// in through the BFF and the test provider as an editor of Line Works,
// against the fake API (fake/servers.mjs): createModel builds the pack's
// photo_start template file as it is, and createImageProposal takes the
// photo after a client's checks and keeps it generating (nothing segments
// or proposes there; no AI is called). With the keyboard: choose "Start
// from a photo or floor plan", pick the demo floor plan, create; the page
// sends the model, then the photo on its version 1, and opens the proposal,
// which shows the photo it was asked for with. A file the API refuses
// leaves the model created: its page says the photo was not sent, and its
// own photo button sends one again. Both languages, axe at each step, the
// 390 px width. The fake's tenant data is put back before and after each
// test (POST /__reset).
import { fileURLToPath } from "node:url";
import type { APIRequestContext } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { APP, focusIsVisible, locales, PROVIDER, settled, signInAs, tenant, violations } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
type ApiCall = { method: string; path: string; key?: string | null; body?: unknown };
const apiCalls = async (request: APIRequestContext): Promise<ApiCall[]> => ((await (await request.get(`${PROVIDER}/__state`)).json()) as { apiCalls: ApiCall[] }).apiCalls;
const project = `/projects/${tenant.PRJ_LINE}`;
/** The synthetic demo floor plan (testdata/photos, 1200 × 520). */
const DEMO = fileURLToPath(new URL("../../../testdata/photos/electronics-line-demo.png", import.meta.url));

test.describe("a new model from a photo or floor plan, signed in, on the fake API", () => {
  test.beforeEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
    await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
  });
  test.afterEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
  });

  for (const locale of locales) {
    test(`${locale}: creates the model, sends the floor plan on its version 1 and opens the proposal with the photo`, async ({ page, request }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, project, locale);
      const before = (await apiCalls(request)).length;

      await page.getByRole("button", { name: t["newModel.open"] }).click();
      const form = page.locator("[data-new-model]");
      await expect(form.getByRole("radio", { name: t["template.electronics_assembly_test_line.name"] })).toBeFocused();
      await page.keyboard.press("ArrowDown");
      const option = form.getByRole("radio", { name: t["template.photo_start.name"] });
      await expect(option).toBeFocused();
      await expect(option).toBeChecked();
      await expect(option).toHaveAccessibleDescription(t["template.photo_start.description"]);
      expect(await focusIsVisible(page)).toBe(true);
      const field = form.locator("[data-photo-start]");
      await expect(field.getByRole("button", { name: t["newModel.photo.choose"] })).toBeVisible();
      await expect(field).toContainText(t["photo.hint"]);
      await expect(field).toContainText(t["ask.dataNotice"]);
      await expect(form.getByLabel(t["newModel.name"])).toHaveValue(t["newModel.photo.defaultName"]);
      expect(await violations(page), "the photo option").toEqual([]);

      // Review round 1: Create model without a photo moves the focus to the chooser, which names the error.
      await form.getByRole("button", { name: t["newModel.create"] }).focus();
      await page.keyboard.press("Enter");
      const chooser = field.getByRole("button", { name: t["newModel.photo.choose"] });
      await expect(chooser).toBeFocused();
      const describedBy = await chooser.evaluate((b) => (b.getAttribute("aria-describedby") ?? "").split(" ").map((id) => document.getElementById(id)?.textContent ?? ""));
      expect(describedBy[0], "the chooser's description starts with the error").toBe(t["newModel.photo.required"]);
      expect(await focusIsVisible(page), "the chooser's focus is visible").toBe(true);
      expect((await apiCalls(request)).slice(before).filter((c) => c.method === "POST"), "nothing is sent without a photo").toEqual([]);
      expect(await violations(page), "a photo missing").toEqual([]);

      await field.locator("input[type=file]").setInputFiles(DEMO);
      await expect(field.locator("[data-photo-file]")).toHaveText("electronics-line-demo.png");
      await expect(field.getByRole("button", { name: t["newModel.photo.again"] })).toBeVisible();
      expect(await violations(page), "a photo chosen").toEqual([]);
      const createdModel = page.waitForResponse((r) => r.url() === `${APP}/api/v1/projects/${tenant.PRJ_LINE}/models` && r.request().method() === "POST");
      await form.getByRole("button", { name: t["newModel.create"] }).focus();
      await page.keyboard.press("Enter");
      const created = (await (await createdModel).json()) as { id: string; latest_version: { id: string; version: number } };
      expect(created.latest_version.version).toBe(1);

      await page.waitForURL(new RegExp(`^${APP}/proposals/aip_[0-9A-Z]{26}$`));
      await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "generating");
      await expect(page.locator("[data-source=image]")).toContainText("1200");
      // The photo it was asked for with, read back through the BFF (H-052).
      const img = page.locator("main img");
      await expect(img).toBeVisible();
      expect(await img.evaluate((i: HTMLImageElement) => [i.naturalWidth, i.naturalHeight, i.src.startsWith("blob:")])).toEqual([1200, 520, true]);
      expect(await violations(page), "the proposal page").toEqual([]);

      const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
      expect(posts.map((c) => c.path)).toEqual([`/v1/projects/${tenant.PRJ_LINE}/models`, expect.stringMatching(/^\/v1\/models\/mdl_[0-9A-Z]{26}\/ai-proposals\/image$/)]);
      expect(posts[0]!.body).toEqual({ name: t["newModel.photo.defaultName"], domain: "electronics_line", template: "photo_start", locale });
      expect(posts[1]!.path).toBe(`/v1/models/${created.id}/ai-proposals/image`);
      expect(posts[1]!.body).toEqual({ parts: ["base_version_id", "output_locale", "file"], base_version_id: created.latest_version.id, output_locale: locale, model: null, file: { size: expect.any(Number) } });
      for (const c of posts) expect(c.key).toMatch(/^[\x21-\x7e]{8,128}$/);
      expect(posts[0]!.key).not.toBe(posts[1]!.key);
    });
  }

  for (const locale of locales) {
    test(`${locale}: a photo the API refuses leaves the model, whose page says so and sends the photo again`, async ({ page, request }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, project, locale);
      const before = (await apiCalls(request)).length;
      await page.getByRole("button", { name: t["newModel.open"] }).click();
      const form = page.locator("[data-new-model]");
      await form.locator("[data-template=photo_start]").click();
      // A PNG by its name and type, not by its bytes: the API refuses it (415 IMAGE_FORMAT_REJECTED).
      await form.locator("[data-photo-start] input[type=file]").setInputFiles({ name: "line.png", mimeType: "image/png", buffer: Buffer.from("not an image") });
      await form.getByRole("button", { name: t["newModel.create"] }).click();

      await page.waitForURL(new RegExp(`^${APP}/models/mdl_[0-9A-Z]{26}$`));
      await settled(page, locale);
      const notice = page.locator("[data-photo-not-sent]");
      await expect(notice.getByRole("alert").first()).toContainText(t["modelPhoto.notSent.title"]);
      await expect(notice).toContainText(t["modelPhoto.notSent.body"].replace("{button}", t["photo.choose"]));
      await expect(notice.locator("[data-problem=invalid]")).toContainText(t["photo.problem.format"]);
      await expect(page.getByRole("heading", { level: 1 })).toHaveText(t["newModel.photo.defaultName"]);
      expect(await violations(page), "the model page with the notice").toEqual([]);

      const retry = page.locator("[data-photo] input[type=file]");
      await retry.setInputFiles(DEMO);
      await page.waitForURL(new RegExp(`^${APP}/proposals/aip_[0-9A-Z]{26}$`));
      await expect(page.locator("[data-proposal-status]")).toHaveAttribute("data-proposal-status", "generating");
      // The API saw the model once and the photo twice: the refused file, then the floor plan from the model page.
      const posts = (await apiCalls(request)).slice(before).filter((c) => c.method === "POST");
      expect(posts.map((c) => (c.path.endsWith("/ai-proposals/image") ? "photo" : "model"))).toEqual(["model", "photo", "photo"]);
      expect(posts.map((c) => (c.body as { file?: { size: number } }).file?.size ?? null)).toEqual([null, 12, expect.any(Number)]);
      // Back on the model, the notice is gone: it was shown once.
      await page.goBack();
      await page.waitForURL(new RegExp(`^${APP}/models/mdl_[0-9A-Z]{26}$`));
      await settled(page, locale);
      await expect(page.locator("[data-photo-not-sent]")).toHaveCount(0);
    });
  }

  for (const locale of locales) {
    test(`${locale}: at 390 px the photo option fits the width, nothing scrolls sideways`, async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 844 });
      const t = catalogs[locale];
      await signInAs(page, tenant.editor, project, locale);
      await page.getByRole("button", { name: t["newModel.open"] }).click();
      const form = page.locator("[data-new-model]");
      const option = form.locator("[data-template=photo_start]");
      await option.click();
      // Review round 1: the field is right under the option that opens it, on screen, before the next group.
      const field = form.locator("[data-photo-start]");
      await expect(field).toBeInViewport();
      const [o, f, next] = await Promise.all([option, field, form.getByRole("heading", { level: 3, name: t["domain.production_logistics.name"] })].map((l) => l.boundingBox()));
      expect(f!.y - (o!.y + o!.height), "the field's top is just under the option").toBeGreaterThanOrEqual(0);
      expect(f!.y - (o!.y + o!.height)).toBeLessThanOrEqual(24);
      expect(next!.y, "the next group starts after the field").toBeGreaterThan(f!.y + f!.height);
      // Create model without a photo: the focus moves to the chooser, on screen.
      await form.getByRole("button", { name: t["newModel.create"] }).click();
      const chooser = field.getByRole("button", { name: t["newModel.photo.choose"] });
      await expect(chooser).toBeFocused();
      await expect(chooser).toBeInViewport();
      await expect(field).toContainText(t["newModel.photo.required"]);
      // A photo over 5 MB is refused in the field, on screen, not after the name below the other groups.
      await field.locator("input[type=file]").setInputFiles({ name: "huge.png", mimeType: "image/png", buffer: Buffer.alloc(5 * 1024 * 1024 + 1) });
      await expect(field.locator("[data-problem=invalid]")).toContainText(t["photo.problem.tooLarge"]);
      await expect(field.locator("[data-problem=invalid]")).toBeInViewport();
      await field.locator("input[type=file]").setInputFiles(DEMO);
      await expect(field.locator("[data-problem]")).toHaveCount(0);
      await expect(form.locator("[data-photo-file]")).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      for (const right of await form.locator("[data-photo-start] *").evaluateAll((els) => els.map((e) => e.getBoundingClientRect().right))) expect(right).toBeLessThanOrEqual(390);
      expect(await violations(page), "the photo option at 390 px").toEqual([]);
    });
  }

  test("a viewer is not offered a new model from a photo", async ({ page }) => {
    await signInAs(page, tenant.viewer, project, "en");
    await expect(page.getByRole("button", { name: en["newModel.open"] })).toHaveCount(0);
    await expect(page.locator("[data-template=photo_start]")).toHaveCount(0);
  });
});
