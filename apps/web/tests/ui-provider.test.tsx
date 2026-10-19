// @vitest-environment happy-dom
// T-045 / ADR-021: React Aria's own strings follow the app's locale. React
// Aria needs "zh-TW" for Traditional Chinese: given "zh-Hant" it falls back
// to its Simplified strings, so UiProvider maps the app's locale.
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { Button, I18nProvider as AriaI18nProvider, Input, SearchField } from "react-aria-components";
import { afterEach, describe, expect, it } from "vitest";
import { ariaLocale } from "../lib/i18n/aria-locale";
import { I18nProvider } from "../lib/i18n/provider";
import { UiProvider } from "../ui/provider";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let roots: Root[] = [];
afterEach(() => {
  for (const r of roots) act(() => r.unmount());
  roots = [];
  document.body.innerHTML = "";
});

async function mount(node: ReactNode): Promise<HTMLElement> {
  const el = document.createElement("div");
  document.body.append(el);
  const root = createRoot(el);
  roots.push(root);
  await act(async () => root.render(node));
  return el;
}

// A React Aria control whose button gets a library string.
const search = (
  <SearchField aria-label="q" defaultValue="x">
    <Input />
    <Button>×</Button>
  </SearchField>
);

describe("UiProvider", () => {
  it("maps the app's locales to React Aria's", () => {
    expect(ariaLocale("en")).toBe("en-US");
    expect(ariaLocale("zh-Hant")).toBe("zh-TW");
  });

  it("gives React Aria Traditional Chinese for zh-Hant and English for en", async () => {
    const zh = await mount(<I18nProvider locale="zh-Hant"><UiProvider>{search}</UiProvider></I18nProvider>);
    expect(zh.querySelector("button")?.getAttribute("aria-label")).toBe("清除搜尋條件");
    const en = await mount(<I18nProvider locale="en"><UiProvider>{search}</UiProvider></I18nProvider>);
    expect(en.querySelector("button")?.getAttribute("aria-label")).toBe("Clear search");
  });

  it("is needed: React Aria given zh-Hant itself answers in Simplified Chinese", async () => {
    const el = await mount(<AriaI18nProvider locale="zh-Hant">{search}</AriaI18nProvider>);
    expect(el.querySelector("button")?.getAttribute("aria-label")).toBe("清除搜索");
  });
});
