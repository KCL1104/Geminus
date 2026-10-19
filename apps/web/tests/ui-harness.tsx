// Shared by the DOM tests of ui/: mount inside the app's providers, run
// axe-core, and press keys the way React Aria listens for them. happy-dom
// does no layout, so axe cannot judge colour contrast or target size here;
// tests/tokens.test.ts and the browser checks (apps/web-e2e) cover those.
import axe from "axe-core";
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach } from "vitest";
import type { Locale } from "../lib/i18n/locale";
import { I18nProvider } from "../lib/i18n/provider";
import { UiProvider } from "../ui/provider";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let roots: Root[] = [];
afterEach(() => {
  for (const r of roots) act(() => r.unmount());
  roots = [];
  document.body.innerHTML = "";
});

export async function mount(node: ReactNode, locale: Locale = "en"): Promise<HTMLElement> {
  document.documentElement.lang = locale;
  // A real page's title comes from Next.js metadata; axe wants one.
  document.title = "ui test";
  const main = document.createElement("main");
  document.body.append(main);
  const root = createRoot(main);
  roots.push(root);
  await act(async () => root.render(<I18nProvider locale={locale}><UiProvider>{node}</UiProvider></I18nProvider>));
  return main;
}

export async function rerender(node: ReactNode, locale: Locale = "en"): Promise<void> {
  const root = roots.at(-1);
  if (!root) throw new Error("nothing mounted");
  await act(async () => root.render(<I18nProvider locale={locale}><UiProvider>{node}</UiProvider></I18nProvider>));
}

const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

/** axe-core's WCAG 2.0–2.2 A/AA violations on the whole document. */
export async function violations(): Promise<string[]> {
  const r = await axe.run(document, { runOnly: { type: "tag", values: tags }, rules: { "color-contrast": { enabled: false } } });
  return r.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
}

export async function press(el: Element | null | undefined): Promise<void> {
  if (!el) throw new Error("nothing to press");
  await act(async () => {
    el.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true, pointerType: "mouse", button: 0 }));
    el.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, cancelable: true, pointerType: "mouse", button: 0 }));
    el.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 }));
  });
}

export async function key(el: Element | null | undefined, k: string): Promise<void> {
  if (!el) throw new Error("no element for the key");
  await act(async () => {
    el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
    el.dispatchEvent(new KeyboardEvent("keyup", { key: k, bubbles: true, cancelable: true }));
  });
}

export const settle = () => act(async () => { for (let i = 0; i < 5; i++) await new Promise((r) => setTimeout(r, 0)); });
