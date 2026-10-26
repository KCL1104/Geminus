"use client";
// The root layout itself failed, so this replaces the whole document and
// has no server locale or theme: it starts in English and the system's
// theme, and switches to the reader's remembered choices once the browser
// can read the cookies. It loads the global styles itself (the layout's
// are not there).
import { I18nProvider, useRememberedLocale } from "../lib/i18n/provider";
import { createTranslator } from "../lib/i18n/translate";
import { themeAttribute } from "../lib/theme/theme";
import "../ui/global-styles";
import shell from "../ui/shell.module.css";
import { CrashNotice } from "./_components/crash";
import { useRememberedTheme } from "./_components/theme-switcher";

export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  const locale = useRememberedLocale();
  const theme = useRememberedTheme();
  return (
    <html lang={locale} data-theme={themeAttribute(theme)}>
      <body>
        <title>{createTranslator(locale)("crash.title")}</title>
        <I18nProvider locale={locale}>
          <main className={shell.main}>
            <CrashNotice error={error} retry={retry} />
          </main>
        </I18nProvider>
      </body>
    </html>
  );
}
