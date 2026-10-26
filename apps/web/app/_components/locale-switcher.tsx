"use client";
// The language choice (REQ-022). It is remembered in the locale cookie,
// which holds the language and nothing else; the server components then
// render again in that language without a route change. It is the design
// system's Select (T-045): the arrow keys open the list and move in it, and
// only a choice changes the language (WCAG 3.2.2); the control stays
// mounted, so focus stays on it while the page renders again.
import { useRouter } from "next/navigation";
import { useState } from "react";
import { type Locale, localeCookieValue, locales, parseLocale } from "../../lib/i18n/locale";
import { useI18n } from "../../lib/i18n/provider";
import { Select } from "../../ui/select";

/** Each language is named in itself, so a reader finds theirs whatever the current one is. */
export function LocaleSwitcher({ onChanged }: { onChanged: () => void }) {
  const { locale, t } = useI18n();
  // Shown at once; the page catches up when the server has rendered it.
  const [chosen, setChosen] = useState<Locale | null>(null);
  const current = chosen ?? locale;
  return (
    <Select
      variant="strip"
      label={t("locale.label")}
      value={current}
      items={locales.map((l) => ({ id: l, label: t(`locale.${l}`), lang: l }))}
      onChange={(id) => {
        const next = parseLocale(id);
        if (next === current) return;
        setChosen(next);
        document.cookie = localeCookieValue(next, location.protocol === "https:");
        onChanged();
      }}
    />
  );
}

/** The switcher in the app shell: a choice re-renders the server components in the new language. */
export function LocaleMenu() {
  const router = useRouter();
  return <LocaleSwitcher onChanged={() => router.refresh()} />;
}
