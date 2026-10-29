"use client";
// The account's controls (T-043 follow-up): what the signed-in bootstrap
// lets a person do outside a page. The sign-in itself is on the page that
// needs it (states.tsx), so the strip changes only once there is a session:
// it gains Sign out, and with several organizations the switcher. Nothing
// shows while signed out or while /me loads (H-027: only what a view needs).
import { useState } from "react";
import { Dialog, DialogTrigger, Popover } from "react-aria-components";
import { type Organization, sharedNames } from "../../lib/auth/bootstrap";
import { useAuth } from "../../lib/auth/session";
import { useI18n } from "../../lib/i18n/provider";
import type { Profile } from "../../lib/auth/profile";
import type { Theme } from "../../lib/theme/theme";
import { Button } from "../../ui/button";
import { GearIcon } from "../../ui/icons";
import { Select } from "../../ui/select";
import shell from "../../ui/shell.module.css";
import { LocaleMenu } from "./locale-switcher";
import { ThemeSwitcher } from "./theme-switcher";

/** Each organization's label: its name, with its ID when another of them has the same name. */
export function useOrganizationLabels(organizations: Organization[]): (o: Organization) => string {
  const { t } = useI18n();
  const shared = sharedNames(organizations);
  return (o) => (shared.has(o.name) ? t("organization.named", { name: o.name, id: o.id }) : o.name);
}

/**
 * The organization the pages work in, when the user has several and has
 * chosen one (until then the page asks, states.tsx). A choice applies at
 * once: the pages load again in that organization, on the same route.
 */
export function OrganizationSwitcher() {
  const { account, session } = useAuth();
  const { t } = useI18n();
  const organizations = account?.bootstrap.state === "ready" ? account.bootstrap.organizations : [];
  const label = useOrganizationLabels(organizations);
  if (account === undefined || session === null || organizations.length < 2) return null;
  return (
    <Select
      clipValue
      label={t("organization.label")}
      value={session.organizationId}
      items={organizations.map((o) => ({ id: o.id, label: label(o) }))}
      onChange={(id) => {
        if (id !== session.organizationId) account.choose(id);
      }}
    />
  );
}

/** Sign out, once there is a session to end: with an organization, while choosing one, without access, or when /me failed. */
export function SignOutButton() {
  const { account } = useAuth();
  const { t } = useI18n();
  const [failed, setFailed] = useState(false);
  const state = account?.bootstrap.state;
  if (account === undefined || state === "signed_out" || state === "loading") return null;
  return (
    <>
      <Button
        variant="quiet"
        onPress={async () => {
          setFailed(false);
          setFailed((await account.signOut()) !== null);
        }}
      >
        {t("auth.signOut")}
      </Button>
      {failed && <span role="alert">{t("auth.signOutFailed")}</span>}
    </>
  );
}

/** Up to two letters for the avatar: the name's initials, else the email's, else the organization's. */
function initials(profile: Profile | null, organization: string | null): string {
  const source = profile?.name ?? profile?.email?.split("@")[0] ?? organization ?? "";
  const words = source.split(/[\s._-]+/).filter(Boolean);
  const letters = words.length > 1 ? [[...words[0]!][0], [...words[1]!][0]] : [...(words[0] ?? "")].slice(0, 2);
  return letters.join("").toUpperCase();
}

/**
 * The account and settings at the foot of the rail (H-042): signed in, an
 * avatar that opens who is signed in (name, email, organization and role),
 * the organization switcher, the language, the theme and Sign out; signed
 * out, a settings button with the language and the theme only. Nothing in
 * the chrome is a form any more.
 */
export function AccountMenu({ initialTheme, profile }: { initialTheme: Theme; profile: Profile | null }) {
  const { account } = useAuth();
  const { t } = useI18n();
  const bootstrap = account?.bootstrap;
  const signedIn = bootstrap !== undefined && bootstrap.state !== "signed_out" && bootstrap.state !== "loading";
  const organization = bootstrap?.state === "ready" ? bootstrap.organization : null;
  const label = signedIn ? t("account.open") : t("settings.open");
  const letters = signedIn ? initials(profile, organization?.name ?? null) : "";
  return (
    <DialogTrigger>
      <Button variant="quiet" className={shell.avatar} aria-label={label} data-signed-in={signedIn || undefined}>
        {signedIn && letters !== "" ? (
          <span className={shell.initials} aria-hidden="true">
            {letters}
          </span>
        ) : (
          <GearIcon />
        )}
      </Button>
      <Popover placement="right bottom" offset={12} className={shell.menu}>
        <Dialog aria-label={label} className={shell.menuBody}>
          {signedIn && (profile !== null || organization !== null) && (
            <div className={shell.who}>
              {profile?.name && <strong>{profile.name}</strong>}
              {profile?.email && <span>{profile.email}</span>}
              {organization && (
                <span>
                  {organization.name}
                  {" · "}
                  {t(organization.role === null ? "role.unknown" : `role.${organization.role}`)}
                </span>
              )}
            </div>
          )}
          <div className={shell.settings}>
            <OrganizationSwitcher />
            <div className={shell.setting}>
              <span className={shell.settingLabel}>{t("locale.label")}</span>
              {/* The menu opens on the language's chosen item, so focus is on a control with its ring, not the dialog (TEST-033). */}
              <LocaleMenu autoFocus />
            </div>
            <div className={shell.setting}>
              <span className={shell.settingLabel}>{t("theme.label")}</span>
              <ThemeSwitcher initial={initialTheme} />
            </div>
          </div>
          <div className={shell.signOut}>
            <SignOutButton />
          </div>
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}
