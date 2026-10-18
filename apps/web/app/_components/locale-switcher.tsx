"use client";
// The language choice (REQ-022). It is remembered in the locale cookie,
// which holds the language and nothing else; the server components then
// render again in that language without a route change.
import { useRouter } from "next/navigation";
import { useId } from "react";
import { localeCookieValue, locales, parseLocale } from "../../lib/i18n/locale";
import { useI18n } from "../../lib/i18n/provider";

/** Each language is named in itself, so a reader finds theirs whatever the current one is. */
export function LocaleSwitcher({ onChanged }: { onChanged: () => void }) {
  const { locale, t } = useI18n();
  const id = useId();
  return (
    <>
      <label htmlFor={id}>{t("locale.label")}</label>{" "}
      <select
        id={id}
        key={locale}
        defaultValue={locale}
        onChange={(e) => {
          document.cookie = localeCookieValue(parseLocale(e.target.value), location.protocol === "https:");
          onChanged();
        }}
      >
        {locales.map((l) => (
          <option key={l} value={l} lang={l}>
            {t(`locale.${l}`)}
          </option>
        ))}
      </select>
    </>
  );
}

/** The switcher in the app shell: a choice re-renders the server components in the new language. */
export function LocaleMenu() {
  const router = useRouter();
  return <LocaleSwitcher onChanged={() => router.refresh()} />;
}
