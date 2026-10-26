"use client";
// The saved draft's issues as the server found them (D-028): severity, what
// is wrong, where (its step and JSON Pointer). An issue on a step, a
// connection or a resource a step needs goes to that step when activated
// (H-027 Q2); one that is about the model as a whole is text only.
import { useId } from "react";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import styles from "./editor.module.css";
import { IssueText } from "./fields";
import type { StepIssue } from "./issues";

export function IssueList({ issues, revision, unsaved, stepName, onGo }: {
  issues: readonly StepIssue[];
  revision: number;
  /** There are edits the server has not checked yet. */
  unsaved: boolean;
  /** The step's name in the working draft, or null when it is not there (any more). */
  stepName: (id: string) => string | null;
  onGo: (stepId: string) => void;
}) {
  const { t, gap } = useI18n();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={styles.issues}>
      <h2 id={headingId}>{t("editor.issues")}</h2>
      <p className={styles.hint}>{t(unsaved ? "editor.issues.unsaved" : "editor.issues.checked", { revision: String(revision) })}</p>
      {issues.length === 0 ? (
        <p>{t("editor.issues.none")}</p>
      ) : (
        <ul className={styles.issueRows}>
          {issues.map((i, n) => {
            const name = i.step === null ? null : stepName(i.step);
            const content = (
              <>
                <span className={styles.chip} data-severity={i.severity}>
                  {t(`editor.severity.${i.severity}`)}
                </span>
                <span>
                  <IssueText issues={[i]} />
                  {name !== null && (
                    <>
                      {gap}
                      <strong>{name}</strong>
                    </>
                  )}
                </span>
                <code className={styles.path}>{i.path || "/"}</code>
              </>
            );
            return (
              <li key={n}>
                {name !== null && i.step !== null ? (
                  <Button variant="quiet" className={styles.issueButton} onPress={() => onGo(i.step!)}>
                    {content}
                  </Button>
                ) : (
                  <div className={styles.issueText}>{content}</div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
