"use client";
// The account's controls (T-043 follow-up): what the signed-in bootstrap
// lets a person do outside a page. The sign-in itself is on the page that
// needs it (states.tsx), so the strip changes only once there is a session:
// it gains Sign out, and with several organizations the switcher. Nothing
// shows while signed out or while /me loads (H-027: only what a view needs).
import { useState } from "react";
import { type Organization, sharedNames } from "../../lib/auth/bootstrap";
import { useAuth } from "../../lib/auth/session";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { Select } from "../../ui/select";

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
      variant="strip"
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
