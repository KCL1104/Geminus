"use client";
// The landing's one action (H-048, H-027: one primary per view). The server
// says only whether the request's session cookie opens (app/layout.tsx,
// hasSession) and the signed-in bootstrap provides the answer through the
// auth context (app/_components/auth-bootstrap.tsx): signed out, the sign-in,
// which returns to the projects; with a session, the projects. A session
// that turns out to have ended (/me answers 401) is signed out again. No
// account, credential or name is ever on this page: the judges' sign-in is
// in the hackathon submission only.
import Link from "next/link";
import { useAuth } from "../../lib/auth/session";
import { useI18n } from "../../lib/i18n/provider";
import { signInHref } from "../../lib/return-to";
import styles from "./landing.module.css";

export function LandingAction() {
  const { t } = useI18n();
  const { account } = useAuth();
  const signedIn = account !== undefined && account.bootstrap.state !== "signed_out";
  if (signedIn) {
    return (
      <div className={styles.act}>
        <Link href="/projects" className={styles.primary} data-variant="primary">
          {t("landing.openProjects")}
        </Link>
      </div>
    );
  }
  return (
    <div className={styles.act}>
      {/* A plain link: /auth/login is a route handler, not a page. */}
      <a href={signInHref("/projects")} className={styles.primary} data-variant="primary">
        {t("landing.signIn")}
      </a>
      <p className={styles.note}>{t("landing.judges")}</p>
    </div>
  );
}
