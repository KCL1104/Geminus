"use client";
// The landing's one action (H-048, H-027: one primary per view). The server
// says only whether the request's session cookie opens (app/layout.tsx,
// hasSession) and the signed-in bootstrap provides the answer through the
// auth context (app/_components/auth-bootstrap.tsx): signed out, the sign-in,
// which returns to the projects; with a session, the projects. A session
// that turns out to have ended (/me answers 401) is signed out again. No
// account, credential or name is ever on this page: the judges' sign-in is
// in the hackathon submission only. Under the action, the hero's notes in one
// centred row that wraps (H-088): where the judges' account is (signed out
// only) and that the screenshots' data is synthetic.
import Link from "next/link";
import { useAuth } from "../../lib/auth/session";
import { useI18n } from "../../lib/i18n/provider";
import { signInHref } from "../../lib/return-to";
import styles from "./landing.module.css";

export function LandingAction() {
  const { t } = useI18n();
  const { account } = useAuth();
  const signedIn = account !== undefined && account.bootstrap.state !== "signed_out";
  return (
    <>
      <div className={styles.act}>
        {signedIn ? (
          <Link href="/projects" className={styles.primary} data-variant="primary">
            {t("landing.openProjects")}
          </Link>
        ) : (
          // A plain link: /auth/login is a route handler, not a page.
          <a href={signInHref("/projects")} className={styles.primary} data-variant="primary">
            {t("landing.signIn")}
          </a>
        )}
      </div>
      <div className={styles.notes}>
        {!signedIn && <p>{t("landing.judges")}</p>}
        <p>{t("landing.synthetic")}</p>
      </div>
    </>
  );
}
