import Link from "next/link";
import type { Metadata } from "next";
import type { ReactNode } from "react";
import { I18nProvider } from "../lib/i18n/provider";
import { requestLocale } from "../lib/i18n/server";
import { createTranslator } from "../lib/i18n/translate";
import { UiProvider } from "../ui/provider";
import "../ui/tokens.css";
import "../ui/base.css";
import shell from "../ui/shell.module.css";
import { LocaleMenu } from "./_components/locale-switcher";

export async function generateMetadata(): Promise<Metadata> {
  return { title: createTranslator(await requestLocale())("app.name") };
}

// No AuthProvider is mounted until T-008 supplies a real session, so every
// route renders signed out (lib/auth/session.tsx). The locale comes from the
// reader's remembered choice, else English (REQ-022).
export default async function RootLayout({ children }: { children: ReactNode }) {
  const locale = await requestLocale();
  const t = createTranslator(locale);
  return (
    <html lang={locale}>
      <body>
        <I18nProvider locale={locale}>
          <UiProvider>
            <a href="#main" className={shell.skip}>
              {t("nav.skip")}
            </a>
            <header className={shell.header}>
              <span className={shell.brand}>{t("app.name")}</span>
              <nav aria-label={t("nav.label")} className={shell.nav}>
                <Link href="/projects">{t("nav.projects")}</Link>
              </nav>
              <div className={shell.tools}>
                <LocaleMenu />
              </div>
            </header>
            <main id="main" tabIndex={-1} className={shell.main}>
              {children}
            </main>
          </UiProvider>
        </I18nProvider>
      </body>
    </html>
  );
}
