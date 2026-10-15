"use client";
import type { ReactNode } from "react";
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

export function RemoteView<T>({ remote, what, children }: { remote: Remote<T>; what: string; children: (data: T) => ReactNode }) {
  if (remote.state === "loading") return <Loading what={what} />;
  if (remote.state === "failed") return <ProblemView problem={remote.problem} />;
  return <>{children(remote.data)}</>;
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

/** Draft state with the user's choices; a conflict is never resolved without them. */
export function DraftStatus({ session, onReload, onDiscard }: { session: DraftSession; onReload: () => void; onDiscard: () => void }) {
  const { role } = useAuth();
  const pending = pendingOps(session).length;
  return (
    <section aria-label="Draft status" data-draft-status={session.status}>
      <p role="status">
        {statusText[session.status]} Draft revision {session.remote.revision}
        {pending > 0 && `, ${pending} change(s) not saved`}.
      </p>
      {session.remote.out_of_date && <p role="note">A newer version was published after this draft was based on it.</p>}
      {session.problem && <ProblemView problem={session.problem} />}
      {session.status === "conflict" && canEdit(role) && (
        <p>
          Your changes are kept in this browser.{" "}
          <button type="button" onClick={onReload}>
            Reload the current draft
          </button>{" "}
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
