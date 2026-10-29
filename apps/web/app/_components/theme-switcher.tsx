"use client";
// The theme choice (H-013): the system's, light or dark, remembered in the
// theme cookie (lib/theme/theme.ts). A choice applies at once by setting
// <html data-theme>; the server renders the same attribute from the cookie
// on the next page, so nothing flashes and nothing needs to re-render.
import { useState, useSyncExternalStore } from "react";
import { useI18n } from "../../lib/i18n/provider";
import { cookieTheme, defaultTheme, parseTheme, type Theme, themeAttribute, themeCookie, themeCookieValue, themes } from "../../lib/theme/theme";
import { Segmented } from "../../ui/segmented";

/** Sets the document's theme attribute: none for the system's. */
export function applyTheme(theme: Theme, root: HTMLElement = document.documentElement): void {
  const value = themeAttribute(theme);
  if (value === undefined) delete root.dataset.theme;
  else root.dataset.theme = value;
}

/**
 * The theme as a three-way choice in the account menu (H-042). The menu
 * mounts it each time it opens, so it starts from the remembered choice
 * when there is one, else from the page's.
 */
export function ThemeSwitcher({ initial }: { initial: Theme }) {
  const { t } = useI18n();
  const [theme, setTheme] = useState<Theme>(() =>
    typeof document !== "undefined" && new RegExp(`(?:^|;\\s*)${themeCookie}=`).test(document.cookie) ? cookieTheme(document.cookie) : initial,
  );
  return (
    <Segmented
      label={t("theme.label")}
      value={theme}
      items={themes.map((th) => ({ id: th, label: t(`theme.${th}`) }))}
      onChange={(next) => {
        setTheme(parseTheme(next));
        document.cookie = themeCookieValue(parseTheme(next), location.protocol === "https:");
        applyTheme(parseTheme(next));
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
