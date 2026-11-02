// The owner's canvas-layout decision of 2026-11-02, in a real browser: on
// the canvas of a model made from a template (the fake API's createModel
// hands back the pack's template file as it is), a decision's probability
// is drawn in React Flow's edge-label layer, above every link, on a backing
// of the canvas's ground, and no label box touches a card, a handle or
// another label. The unit test (apps/web/tests/canvas-routes.test.ts) checks
// every template's routes and labels from the stylesheet's sizes; this
// checks that the browser draws those sizes: a card is 168 × 64.8 canvas
// units, and a label is no wider than that test's bound for its text and
// 16.8 units tall.
import type { Page } from "@playwright/test";
import { expect, test } from "./fixtures";
import { en } from "../../web/messages/en";
import { zhHant } from "../../web/messages/zh-Hant";
import { APP, PROVIDER, settled, signInAs, tenant } from "./helpers";

const catalogs = { en, "zh-Hant": zhHant } as const;
const project = `/projects/${tenant.PRJ_LINE}`;
/** tests/canvas-routes.test.ts: upper bounds of Geist's advance at 12 px, plus 4 px of padding each side. */
const bound = (text: string) => [...text].reduce((s, c) => s + (/\d/.test(c) ? 8 : c === "%" ? 12 : c === "." || c === "," ? 4 : 12), 0) + 8;

type Box = { x: number; y: number; width: number; height: number };
const overlaps = (a: Box, b: Box) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

async function newModel(page: Page, locale: "en" | "zh-Hant", template: "default_delivery_pipeline" | "electronics_assembly_test_line") {
  const t = catalogs[locale];
  await signInAs(page, tenant.editor, project, locale);
  await page.getByRole("button", { name: t["newModel.open"] }).click();
  const form = page.locator("[data-new-model]");
  const option = form.getByRole("radio", { name: t[`template.${template}.name`] });
  // The form opens on its first template, focused; the arrows move the choice, as in new-model.spec.ts.
  await expect(form.getByRole("radio", { name: t["template.electronics_assembly_test_line.name"] })).toBeFocused();
  for (let i = 0; i < 10 && !(await option.isChecked()); i++) await page.keyboard.press("ArrowDown");
  await expect(option).toBeChecked();
  await form.getByRole("button", { name: t["newModel.create"] }).click();
  await page.waitForURL(new RegExp(`^${APP}/models/mdl_[0-9A-Z]{26}$`));
  await settled(page, locale);
}

test.describe("a decision's probability on the canvas of a new model", () => {
  test.beforeEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
    await request.post(`${PROVIDER}/__access-seconds`, { data: "600" });
  });
  test.afterEach(async ({ request }) => {
    await request.post(`${PROVIDER}/__reset`);
  });

  for (const [locale, template, decisions] of [
    ["en", "default_delivery_pipeline", 2],
    ["zh-Hant", "electronics_assembly_test_line", 16],
  ] as const) {
    test(`${locale}, ${template}: every label is above the links, on the ground, clear of cards, handles and labels`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await newModel(page, locale, template);
      const labels = page.locator(".react-flow__edgelabel-renderer [data-edge-label]");
      await expect(labels).toHaveCount(decisions);
      // Nothing of a label is left inside the links' SVG.
      await expect(page.locator(".react-flow__edges text")).toHaveCount(0);

      const drawn = await page.evaluate(() => {
        const box = (e: Element) => {
          const r = e.getBoundingClientRect();
          return { x: r.x, y: r.y, width: r.width, height: r.height };
        };
        // The canvas (canvas.module.css .canvas) is the ground the links and labels lie on.
        const ground = getComputedStyle(document.querySelector(".react-flow")!.parentElement!).backgroundColor;
        return {
          cards: [...document.querySelectorAll(".react-flow__node")].map((e) => ({ id: e.getAttribute("data-id")!, ...box(e) })),
          handles: [...document.querySelectorAll(".react-flow__handle")].map(box),
          labels: [...document.querySelectorAll("[data-edge-label]")].map((e) => ({ text: e.textContent ?? "", backing: getComputedStyle(e).backgroundColor, ...box(e) })),
          ground,
        };
      });
      // The scale the canvas is drawn at: a card is 168 canvas units wide.
      const zoom = drawn.cards[0]!.width / 168;
      for (const c of drawn.cards) {
        expect(c.width / zoom, `${c.id} width`).toBeCloseTo(168, 0);
        expect(c.height / zoom, `${c.id} height`).toBeCloseTo(64.8, 0);
      }
      for (const l of drawn.labels) {
        expect(l.backing, `${l.text} is on the canvas's ground`).toBe(drawn.ground);
        expect(l.width / zoom, `${l.text} fits the unit test's bound`).toBeLessThanOrEqual(bound(l.text) + 0.5);
        expect(l.height / zoom, `${l.text} height`).toBeCloseTo(16.8, 0);
        for (const c of drawn.cards) expect(overlaps(l, c), `${l.text} on ${c.id}`).toBe(false);
        for (const h of drawn.handles) expect(overlaps(l, h), `${l.text} on a handle at ${Math.round(h.x)},${Math.round(h.y)}`).toBe(false);
        for (const o of drawn.labels) if (o !== l) expect(overlaps(l, o), `${l.text} on ${o.text}`).toBe(false);
      }
    });
  }
});
