import Link from "next/link";
import type { Metadata } from "next";
import { cookies } from "next/headers";
import type { ReactNode } from "react";
import { organizationCookie, parseOrganization } from "../lib/auth/organization";
import { I18nProvider } from "../lib/i18n/provider";
import { requestLocale } from "../lib/i18n/server";
import { createTranslator } from "../lib/i18n/translate";
import { hasSession } from "../lib/session/server";
import { SESSION_COOKIE } from "../lib/session/session";
import { requestTheme } from "../lib/theme/server";
import { themeAttribute } from "../lib/theme/theme";
import { UiProvider } from "../ui/provider";
import "../ui/global-styles";
import shell from "../ui/shell.module.css";
import { OrganizationSwitcher, SignOutButton } from "./_components/account";
import { AuthBootstrap } from "./_components/auth-bootstrap";
import { LocaleMenu } from "./_components/locale-switcher";
import { ThemeSwitcher } from "./_components/theme-switcher";

export async function generateMetadata(): Promise<Metadata> {
  return { title: createTranslator(await requestLocale())("app.name") };
}

// The signed-in bootstrap (T-043 follow-up) wraps the strip and the page:
// the server only says whether the session cookie holds a session (the BFF
// opens it; nothing of it reaches the page) and which organization was
// chosen before, and the browser loads /me through the BFF. Without a
// session every route renders signed out. The locale comes from the
// reader's remembered choice, else English (REQ-022); the theme from theirs,
// else the system's (H-013), rendered on <html> so the first paint has it.
export default async function RootLayout({ children }: { children: ReactNode }) {
  const locale = await requestLocale();
  const theme = await requestTheme();
  const jar = await cookies();
  const sealed = jar.get(SESSION_COOKIE)?.value;
  const signedIn = sealed !== undefined && (await hasSession(sealed));
  const organization = parseOrganization(jar.get(organizationCookie)?.value);
  const t = createTranslator(locale);
  return (
    <html lang={locale} data-theme={themeAttribute(theme)}>
      <body>
        <I18nProvider locale={locale}>
          <UiProvider>
            <AuthBootstrap signedIn={signedIn} organization={organization}>
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
                  <ThemeSwitcher initial={theme} />
                </div>
                {/* Empty, and not shown, until there is a session (account.tsx). */}
                <div className={shell.account}>
                  <OrganizationSwitcher />
                  <SignOutButton />
                </div>
              </header>
              <main id="main" tabIndex={-1} className={shell.main}>
                {children}
              </main>
            </AuthBootstrap>
          </UiProvider>
        </I18nProvider>
      </body>
    </html>
  );
}
