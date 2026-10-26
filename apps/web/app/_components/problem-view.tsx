"use client";
// How every API failure is shown (T-014): one alert per Problem, with the
// server's request ID when it sent one. 404 says the same for "missing" and
// "another tenant's" so the UI never confirms that a foreign resource exists:
// neither its message nor its details are shown.
//
// What the UI says comes from the catalog by error and validation code
// (REQ-022). The technical message (the server's, or the client's own
// diagnostic) and an unknown code's reason are English data: shown as they
// came and marked lang="en".
import type { Problem, ProblemKind } from "../../lib/api/problem";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { en } from "../../messages/en";
import { Callout } from "../../ui/callout";

const guidance: Partial<Record<ProblemKind, MessageKey>> = {
  unauthenticated: "problem.guidance.unauthenticated",
  forbidden: "problem.guidance.forbidden",
  not_found: "problem.guidance.not_found",
  precondition: "problem.guidance.precondition",
};

// A 409 means different things; the code says which (D-030). Every code
// has a message, so a known code always explains itself.
function guidanceFor(p: Problem): MessageKey | undefined {
  return p.code !== null ? `error.${p.code}` : guidance[p.kind];
}

/** The catalog key for a validation code, or null for one this build does not know (e.g. a pack's own). */
export function validationKey(code: string | undefined): MessageKey | null {
  const key = `validation.${code}`;
  return code !== undefined && Object.hasOwn(en, key) ? (key as MessageKey) : null;
}

export function ProblemView({ problem, headingLevel = 2 }: { problem: Problem; headingLevel?: 2 | 3 }) {
  const { t, gap } = useI18n();
  const advice = guidanceFor(problem);
  const shown = problem.kind !== "not_found";
  const technical = Boolean(problem.message) && shown;
  // What a person needs to act stays in view; what only support or a
  // developer reads (the technical message, the request ID) is one click
  // away (H-027).
  return (
    <Callout tone="danger" heading={t(`problem.heading.${problem.kind}`)} headingLevel={headingLevel} data-problem={problem.kind}>
      {advice && <p>{t(advice)}</p>}
      {problem.details.length > 0 && shown && (
        <ul>
          {problem.details.map((d, i) => {
            const key = validationKey(d.code);
            return (
              <li key={i}>
                <code>{d.path || "/"}</code> {key ? t(key) : <span lang="en">{d.reason}</span>}
              </li>
            );
          })}
        </ul>
      )}
      {problem.retryAfterSeconds !== null && <p>{t("problem.retryAfter", { count: problem.retryAfterSeconds })}</p>}
      {(technical || problem.requestId) && (
        <details>
          <summary>{t("problem.details")}</summary>
          {technical && (
            <p>
              {t("problem.technical")}
              {gap}
              <span lang="en">{problem.message}</span>
            </p>
          )}
          {problem.requestId && (
            <p>
              {t("problem.requestId")}
              {gap}
              <code>{problem.requestId}</code>
            </p>
          )}
        </details>
      )}
    </Callout>
  );
}
