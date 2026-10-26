"use client";
import { type ReactNode, useState } from "react";
import type { Problem, ProblemKind } from "../../lib/api/problem";
import { canEdit, type Role, useAuth } from "../../lib/auth/session";
import { canPublish, type DraftSession, pendingOps } from "../../lib/state/draft";
import type { Remote } from "../../lib/state/remote";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { Callout } from "../../ui/callout";
import { ProblemView } from "./problem-view";

/** What a page loads; each has its own sentence in the catalog. */
export type Loadable = "projects" | "models" | "model" | "scenario" | "runs" | "results";

export function Loading({ what }: { what: Loadable }) {
  const { t } = useI18n();
  return (
    <p role="status" aria-busy="true">
      {t(`loading.${what}`)}
    </p>
  );
}

/** Renders children only with a session; otherwise the sign-in notice, without calling the API. */
export function RequireSession({ children }: { children: ReactNode }) {
  const { session } = useAuth();
  if (session === null) {
    return (
      <ProblemView
        problem={{ kind: "unauthenticated", code: null, status: null, message: "", requestId: null, details: [], retryAfterSeconds: null }}
      />
    );
  }
  return <>{children}</>;
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
        {onRetry && retryable.has(remote.problem.kind) && (
          <Button onPress={onRetry}>{t("retry")}</Button>
        )}
      </>
    );
  }
  return <>{children(remote.data)}</>;
}

/** Says so when a list has more pages than this build shows (no silent truncation). */
export function MorePages({ cursor, shown, what }: { cursor: string | null; shown: number; what: "projects" | "models" }) {
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
