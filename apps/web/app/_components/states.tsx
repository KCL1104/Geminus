"use client";
import { type ReactNode, useState } from "react";
import type { Problem, ProblemKind } from "../../lib/api/problem";
import { canEdit, type Role, useAuth } from "../../lib/auth/session";
import { canPublish, type DraftSession, pendingOps } from "../../lib/state/draft";
import type { Remote } from "../../lib/state/remote";
import { ProblemView } from "./problem-view";

export function Loading({ what }: { what: string }) {
  return (
    <p role="status" aria-busy="true">
      Loading {what}…
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

export function RemoteView<T>({ remote, what, onRetry, children }: {
  remote: Remote<T>;
  what: string;
  onRetry?: () => void;
  children: (data: T) => ReactNode;
}) {
  if (remote.state === "loading") return <Loading what={what} />;
  if (remote.state === "failed") {
    return (
      <>
        <ProblemView problem={remote.problem} />
        {onRetry && retryable.has(remote.problem.kind) && (
          <button type="button" onClick={onRetry}>
            Try again
          </button>
        )}
      </>
    );
  }
  return <>{children(remote.data)}</>;
}

/** Says so when a list has more pages than this build shows (no silent truncation). */
export function MorePages({ cursor, shown, what }: { cursor: string | null; shown: number; what: string }) {
  if (cursor === null) return null;
  return <p role="note">Showing the first {shown} {what}. More exist; this build cannot page through them yet.</p>;
}

export function ReadOnlyNotice({ role }: { role: Role | null }) {
  if (canEdit(role)) return null;
  return <p role="note">{role === null ? "Your role is not known yet, so editing is off." : "You can view this model but not change it."}</p>;
}

const statusText: Record<DraftSession["status"], string> = {
  clean: "All changes saved.",
  dirty: "Unsaved changes.",
  saving: "Saving…",
  conflict: "Not saved: the draft changed on the server.",
  error: "Not saved.",
};

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
  const [reloadProblem, setReloadProblem] = useState<Problem | null>(null);
  const pending = pendingOps(session).length;
  const reload = async () => setReloadProblem(await onReload());
  return (
    <section aria-label="Draft status" data-draft-status={session.status}>
      <p role="status">
        {statusText[session.status]} Draft revision {session.remote.revision}
        {pending > 0 && `, ${pending} change(s) not saved`}.
      </p>
      {session.remote.out_of_date && <p role="note">A newer version was published after this draft was based on it.</p>}
      {session.problem && <ProblemView problem={session.problem} />}
      {reloadProblem && <ProblemView problem={reloadProblem} />}
      {(session.status === "conflict" || session.status === "error") && canEdit(role) && (
        <p>
          Your changes are kept on this page until you leave or reload it.{" "}
          {session.status === "conflict" && (
            <>
              <button type="button" onClick={reload}>
                Reload the current draft
              </button>{" "}
            </>
          )}
          <button type="button" onClick={onDiscard}>
            Discard my changes
          </button>
        </p>
      )}
      <ReadOnlyNotice role={role} />
      {canEdit(role) && !canPublish(session) && session.status === "clean" && (
        <p role="note">Publishing is unavailable until the draft is current and has no errors.</p>
      )}
    </section>
  );
}
