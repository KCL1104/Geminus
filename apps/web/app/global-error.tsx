"use client";
// The root layout itself failed, so this replaces the whole document and
// has no server locale: it starts in English and switches to the reader's
// remembered choice once the browser can read the cookie.
import { I18nProvider, useRememberedLocale } from "../lib/i18n/provider";
import { createTranslator } from "../lib/i18n/translate";
import { CrashNotice } from "./_components/crash";

export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const locale = useRememberedLocale();
  return (
    <html lang={locale}>
      <body>
        <title>{createTranslator(locale)("crash.title")}</title>
        <I18nProvider locale={locale}>
          <CrashNotice error={error} retry={retry} />
        </I18nProvider>
      </body>
    </html>
  );
}
