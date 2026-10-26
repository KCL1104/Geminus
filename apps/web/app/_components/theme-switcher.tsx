"use client";
// The theme choice (H-013): the system's, light or dark, remembered in the
// theme cookie (lib/theme/theme.ts). A choice applies at once by setting
// <html data-theme>; the server renders the same attribute from the cookie
// on the next page, so nothing flashes and nothing needs to re-render.
import { useState, useSyncExternalStore } from "react";
import { useI18n } from "../../lib/i18n/provider";
import { cookieTheme, defaultTheme, parseTheme, type Theme, themeAttribute, themeCookieValue, themes } from "../../lib/theme/theme";
import { Select } from "../../ui/select";

/** Sets the document's theme attribute: none for the system's. */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
  const value = themeAttribute(theme);
  if (value === undefined) delete root.dataset.theme;
  else root.dataset.theme = value;
}

export function ThemeSwitcher({ initial }: { initial: Theme }) {
  const { t } = useI18n();
  const [theme, setTheme] = useState(initial);
  return (
    <Select
      variant="strip"
      label={t("theme.label")}
      value={theme}
      items={themes.map((th) => ({ id: th, label: t(`theme.${th}`) }))}
      onChange={(id) => {
        const next = parseTheme(id);
        setTheme(next);
        document.cookie = themeCookieValue(next, location.protocol === "https:");
        applyTheme(next);
      }}
    />
  );
}

const noSubscription = () => () => {};

/**
 * The remembered choice as the browser sees it, for the page that replaces
 * a failed layout (global-error): the system's on the server, the cookie's
 * in the browser.
 */
export function useRememberedTheme(): Theme {
  return useSyncExternalStore(noSubscription, () => cookieTheme(document.cookie), () => defaultTheme);
}
