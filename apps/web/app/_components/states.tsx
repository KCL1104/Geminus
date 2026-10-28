"use client";
import { usePathname, useSearchParams } from "next/navigation";
import { type ReactNode, useId, useState } from "react";
import type { Problem, ProblemKind } from "../../lib/api/problem";
import type { Bootstrap, Organization } from "../../lib/auth/bootstrap";
import { canEdit, type Role, useAuth } from "../../lib/auth/session";
import { signInHref } from "../../lib/return-to";
import { canPublish, type DraftSession, pendingOps } from "../../lib/state/draft";
import type { Remote } from "../../lib/state/remote";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import list from "../../ui/list.module.css";
import { useOrganizationLabels } from "./account";
import { ProblemView } from "./problem-view";

/** What a page loads; each has its own sentence in the catalog. */
export type Loadable = "projects" | "models" | "model" | "scenario" | "scenarios" | "runs" | "results" | "account" | "proposal";

export function Loading({ what }: { what: Loadable }) {
  const { t } = useI18n();
  return (
    <p role="status" aria-busy="true">
      {t(`loading.${what}`)}
    </p>
  );
}

const problemOf = (kind: ProblemKind): Problem => ({ kind, code: null, status: null, message: "", requestId: null, details: [], retryAfterSeconds: null });
const signedOut: Bootstrap = { state: "signed_out" };

/**
 * The sign-in (T-043 follow-up): one plain link to the BFF's login route
 * (a route handler, not a page), returning to this page; the BFF checks the
 * return path again (lib/return-to.ts).
 */
export function SignInLink() {
  const { t } = useI18n();
  const pathname = usePathname();
  const query = useSearchParams()?.toString() ?? "";
  return (
    <p>
      <a href={signInHref(pathname === null ? null : `${pathname}${query === "" ? "" : `?${query}`}`)}>{t("auth.signIn")}</a>
    </p>
  );
}

/** Several organizations and none chosen: one button each, its role beside it. */
function OrganizationChoice({ organizations, truncated, onChoose }: { organizations: Organization[]; truncated: boolean; onChoose: (id: string) => void }) {
  const { t } = useI18n();
  const label = useOrganizationLabels(organizations);
  const heading = useId();
  return (
    <section aria-labelledby={heading}>
      <h2 id={heading}>{t("organization.choose.title")}</h2>
      <p>{t("organization.choose.body")}</p>
      <ul className={list.list}>
        {organizations.map((o) => (
          <li key={o.id}>
            <Button onPress={() => onChoose(o.id)}>{label(o)}</Button>
            <span className={list.meta}>{t(o.role === null ? "role.unknown" : `role.${o.role}`)}</span>
          </li>
        ))}
      </ul>
      {truncated && <Callout tone="info">{t("organization.more", { count: organizations.length })}</Callout>}
    </section>
  );
}

/**
 * Renders children only with a session (an organization chosen); otherwise
 * what the sign-in bootstrap stands at, without calling a tenant API:
 * signed out (the sign-in), loading, a failed /me (retry), no access (403
 * or no membership, D-008), or the choice of organization.
 */
export function RequireSession({ children }: { children: ReactNode }) {
  const { session, account } = useAuth();
  if (session !== null) return <>{children}</>;
  const b = account?.bootstrap ?? signedOut;
  switch (b.state) {
    case "loading":
      return <Loading what="account" />;
    case "failed":
      return <RemoteView remote={{ state: "failed", problem: b.problem }} what="account" onRetry={account?.retry}>{() => null}</RemoteView>;
    case "no_access":
      return <ProblemView problem={b.problem ?? problemOf("forbidden")} guidance="access.none" />;
    case "choose":
      return <OrganizationChoice organizations={b.organizations} truncated={b.truncated} onChoose={(id) => account?.choose(id)} />;
    default:
      return (
        <>
          <ProblemView problem={problemOf("unauthenticated")} />
          <SignInLink />
        </>
      );
  }
}

/** Failures that can go away on their own; the others need a different request or role. */
const retryable: ReadonlySet<ProblemKind> = new Set(["network", "unavailable", "rate_limited", "internal", "protocol"]);

export function RemoteView<T>({ remote, what, onRetry, headingLevel, children }: {
  remote: Remote<T>;
  what: Loadable;
  onRetry?: () => void;
  headingLevel?: 2 | 3;
  children: (data: T) => ReactNode;
}) {
  const { t } = useI18n();
  if (remote.state === "loading") return <Loading what={what} />;
  if (remote.state === "failed") {
    return (
      <>
        <ProblemView problem={remote.problem} headingLevel={headingLevel} />
        {remote.problem.kind === "unauthenticated" && <SignInLink />}
        {onRetry && retryable.has(remote.problem.kind) && (
          <Button onPress={onRetry}>{t("retry")}</Button>
        )}
      </>
    );
  }
  return <>{children(remote.data)}</>;
}

/** Says so when a list has more pages than this build shows (no silent truncation). */
export function MorePages({ cursor, shown, what }: { cursor: string | null; shown: number; what: "projects" | "models" | "scenarios" }) {
  const { t } = useI18n();
  if (cursor === null) return null;
  return <Callout tone="info">{t(`morePages.${what}`, { count: shown })}</Callout>;
}

export function ReadOnlyNotice({ role }: { role: Role | null }) {
  const { t } = useI18n();
  if (canEdit(role)) return null;
  return <Callout tone="info">{t(role === null ? "readOnly.unknownRole" : "readOnly.viewer")}</Callout>;
}

/**
 * Draft state with the user's choices; a conflict is never resolved without
 * them. onReload answers with the problem when the reload failed.
 */
export function DraftStatus({ session, onReload, onDiscard }: {
  session: DraftSession;
  onReload: () => Promise<Problem | null>;
  onDiscard: () => void;
}) {
  const { role } = useAuth();
  const { t, gap } = useI18n();
  const [reloadProblem, setReloadProblem] = useState<Problem | null>(null);
  const pending = pendingOps(session).length;
  const reload = async () => setReloadProblem(await onReload());
  return (
    <section aria-label={t("draft.label")} data-draft-status={session.status}>
      <p role="status">
        {t(`draft.status.${session.status}`)}
        {gap}
        {t("draft.revision", { revision: String(session.remote.revision) })}
        {pending > 0 && (
          <>
            {gap}
            {t("draft.pending", { count: pending })}
          </>
        )}
      </p>
      {session.remote.out_of_date && <Callout tone="warning">{t("draft.outOfDate")}</Callout>}
      {session.problem && <ProblemView problem={session.problem} />}
      {reloadProblem && <ProblemView problem={reloadProblem} />}
      {(session.status === "conflict" || session.status === "error") && canEdit(role) && (
        <p>
          {t("draft.kept")}{" "}
          {session.status === "conflict" && (
            <>
              <Button variant="primary" onPress={reload}>
                {t("draft.reload")}
              </Button>{" "}
            </>
          )}
          <Button variant="danger" onPress={onDiscard}>
            {t("draft.discard")}
          </Button>
        </p>
      )}
      <ReadOnlyNotice role={role} />
      {canEdit(role) && !canPublish(session) && session.status === "clean" && (
        <Callout tone="info">{t("draft.publishUnavailable")}</Callout>
      )}
    </section>
  );
}
