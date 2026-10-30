"use client";
// The AI proposal review page (T-031, hackathon slice; H-027 Q2 flow from
// the 10-19 prototype, Marey look, DESIGN.md Simplicity). It shows what the
// server holds and nothing else: the proposal's state, read again while the
// AI writes it; the diff in human terms against the base version, with the
// exact JSON Patch behind a disclosure; the AI's summary, assumptions and
// expectations as the AI's; the server's checks and risk (the authority)
// beside the AI's own claim and confidence, each labelled; the AI's
// questions when it needs input. A person decides: approving an edit makes
// a version, approving a scenario saves a scenario to run (the two are
// separate actions with separate outcomes, by the proposal's mode), and
// rejecting takes an optional reason; each is confirmed in place. A base
// that moved (409) keeps the page and says what changed; nothing is
// rebased or asked again by itself. After a decision the page reads the
// proposal and the model again, so what it says is the server's. An
// approval the server refuses on its own recheck (422: the checks fail
// again; 409 at /risk_level or /server_risk_level: the risk is not the one
// reviewed) leaves the proposal ready, but it can never be approved as it
// is (D-069 (b), B1): Approve is not offered again, a note says why and that
// the way on is to reject it and ask again (review round 1, L4).
//
// The AI's words (summary, assumptions, expectations, questions, names in
// the patch) are data: always text, never markup or links, with what would
// hide or reorder text shown (shown.ts; names and values isolated in a
// <bdi>, words.tsx). The one exception is a researched proposal's sources
// (H-046 (2), sources.tsx): pages the server kept because the call opened
// them, each an https link to a new tab with no referrer or opener, its
// title text, and the Written-by line says how many searches the call made.
import Link from "next/link";
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { ProblemView, validationKey } from "../../app/_components/problem-view";
import { RemoteView } from "../../app/_components/states";
import { useRemote } from "../../app/_components/use-remote";
import { newIdempotencyKey } from "../../lib/api/client";
import { errorCodeStatus, type Problem, problemFromResponse, protocolProblem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { canEdit, useAuth } from "../../lib/auth/session";
import type { Approval, ErrorCode, Proposal, ValidationIssue } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { ApproveIcon, CloseIcon } from "../../ui/icons";
import { PageHeader } from "../../ui/page-header";
import { useReader, useWriterWithSearch } from "./generated-by";
import { RailModel } from "../../app/_components/rail";
import { Callout } from "../../ui/callout";
import { TextField } from "../../ui/field";
import { StatusRegion } from "../../ui/status";
import { ChangeList } from "./change-list";
import { loadProposalPage, type ProposalPage } from "./load";
import { Sources } from "./sources";
import { visible } from "./shown";
import { data, useRich, Words } from "./words";
import styles from "./proposal.module.css";

/** Where the page's links go: the product's routes, or the review page's own. */
export type ProposalHrefs = { model: (modelId: string) => string; scenario: (scenarioId: string) => string };
const productHrefs: ProposalHrefs = { model: (id) => `/models/${id}`, scenario: (id) => `/scenarios/${id}` };

/** Loads the proposal page and shows it. */
export function ProposalRoute({ proposalId, pollMs = 1500, hrefs = productHrefs }: { proposalId: string; pollMs?: number; hrefs?: ProposalHrefs }) {
  const api = useApi();
  const [remote, retry] = useRemote((signal) => loadProposalPage(api, proposalId, signal), api, proposalId);
  return (
    <RemoteView remote={remote} what="proposal" onRetry={retry}>
      {(page) => <ProposalView key={page.proposal.proposal_id} initial={page} pollMs={pollMs} hrefs={hrefs} />}
    </RemoteView>
  );
}

type Decision = "idle" | "confirmApprove" | "confirmReject" | "approving" | "rejecting";
type Outcome = { kind: "approved"; approval: Approval } | { kind: "rejected" } | { kind: "conflict" } | null;
type Focus = "approve" | "reject" | "confirm" | "outcome" | "refused" | null;
/** Why the server refused the approval on its own recheck: its checks fail, or its risk is not the reviewed one. */
type Refused = "checks" | "risk" | null;

/** A failed proposal's error envelope as the Problem the UI shows for any API failure. */
function failureProblem(p: Proposal): Problem {
  const code = p.error?.error.code;
  if (code === undefined || !Object.hasOwn(errorCodeStatus, code)) return protocolProblem(null, "The proposal failed without an API error.");
  return problemFromResponse(errorCodeStatus[code as ErrorCode], new Headers(), p.error);
}

/** An approval the server refused on its own recheck (D-069 (b)): the proposal stays ready, but this approval can never succeed. */
function refusedBy(problem: Problem): Refused {
  if (problem.code === "MODEL_VALIDATION_FAILED") return "checks";
  if (problem.code === "LIFECYCLE_CONFLICT" && problem.details.some((d) => d.path === "/risk_level" || d.path === "/server_risk_level")) return "risk";
  return null;
}

function ProposalView({ initial, pollMs, hrefs }: { initial: ProposalPage; pollMs: number; hrefs: ProposalHrefs }) {
  const api = useApi();
  const { role } = useAuth();
  const { t, dateTime, number, gap } = useI18n();
  const rich = useRich();
  const [page, setPage] = useState(initial);
  const [pollProblem, setPollProblem] = useState<Problem | null>(null);
  const [pollAttempt, setPollAttempt] = useState(0);
  const [decision, setDecision] = useState<Decision>("idle");
  const [outcome, setOutcome] = useState<Outcome>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [reason, setReason] = useState("");
  const [refused, setRefused] = useState<Refused>(null);
  const [focus, setFocus] = useState<Focus>(null);
  const busy = useRef(false);
  const approveKey = useRef<string | null>(null);
  const rejectKey = useRef<{ comment: string; key: string } | null>(null);
  const approveRef = useRef<HTMLButtonElement>(null);
  const rejectRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const outcomeRef = useRef<HTMLElement>(null);
  const refusedRef = useRef<HTMLDivElement>(null);
  const ids = { question: useId(), status: useId() };

  const p = page.proposal;
  // H-043: which Meta model (or other provider) wrote it, and what read the photo; H-046 (2): whether it searched the web.
  const writer = useWriterWithSearch(p.generated_by);
  const reader = useReader(p.generated_by);
  const id = p.proposal_id;

  // While the AI writes it, read the proposal again; stop on any answer that is not generating, or a failure (until retried).
  useEffect(() => {
    if (p.status !== "generating") return;
    const ctl = new AbortController();
    const timer = setTimeout(async () => {
      const r = await api.getProposal(id, { signal: ctl.signal });
      if (ctl.signal.aborted) return;
      if (!r.ok) return setPollProblem(r.problem);
      if (r.data.proposal_id !== id || r.data.model_id !== p.model_id || r.data.base_version_id !== p.base_version_id) {
        return setPollProblem(protocolProblem(200, "The API answered with another proposal.", r.requestId));
      }
      setPage((s) => ({ ...s, proposal: r.data }));
    }, pollMs);
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, [api, id, p, pollMs, pollAttempt]);

  // Focus follows the person's step: the confirmation, back to what opened it, or the outcome.
  useEffect(() => {
    if (focus === null) return;
    ({ approve: approveRef, reject: rejectRef, confirm: confirmRef, outcome: outcomeRef, refused: refusedRef })[focus].current?.focus();
  }, [focus, decision, outcome, refused]);

  /** Reads the proposal and the model again: after a decision the page says what the server holds. */
  const reread = useCallback(async () => {
    const r = await loadProposalPage(api, id);
    if (r.ok) setPage(r.data);
    else setProblem(r.problem);
  }, [api, id]);

  const approve = async () => {
    if (busy.current) return;
    busy.current = true;
    setDecision("approving");
    setProblem(null);
    // One key for this proposal's approval: a retry is the same request (D-011).
    const key = (approveKey.current ??= newIdempotencyKey());
    const r = await api.approveProposal(id, { expected_base_version_id: p.base_version_id }, key);
    busy.current = false;
    setDecision("idle");
    if (r.ok) {
      setOutcome({ kind: "approved", approval: r.data });
      setFocus("outcome");
    } else if (r.problem.code === "MODEL_VERSION_CONFLICT") {
      setOutcome({ kind: "conflict" });
      setFocus("outcome");
    } else {
      setProblem(r.problem);
      // Refused on the server's recheck: never approvable as it is, so no Approve again; otherwise (503 …) a retry may succeed.
      const why = refusedBy(r.problem);
      setRefused(why);
      setFocus(why === null ? "approve" : "refused");
      // Decided or no longer ready elsewhere: show what the server holds now.
      if (r.problem.code !== "PROPOSAL_ALREADY_DECIDED" && r.problem.code !== "LIFECYCLE_CONFLICT") return;
    }
    await reread();
  };

  const reject = async () => {
    if (busy.current) return;
    busy.current = true;
    setDecision("rejecting");
    setProblem(null);
    const comment = reason.trim();
    if (rejectKey.current?.comment !== comment) rejectKey.current = { comment, key: newIdempotencyKey() };
    const r = await api.rejectProposal(id, comment === "" ? {} : { comment }, rejectKey.current.key);
    busy.current = false;
    setDecision("idle");
    if (r.ok) {
      setOutcome({ kind: "rejected" });
      setPage((s) => ({ ...s, proposal: r.data }));
      setFocus("outcome");
      return;
    }
    setProblem(r.problem);
    setFocus("reject");
    if (r.problem.code === "PROPOSAL_ALREADY_DECIDED" || r.problem.code === "LIFECYCLE_CONFLICT") await reread();
  };

  const baseVersion = page.base.version;
  const latestVersion = page.model.latest_version;
  const stale = latestVersion.id !== p.base_version_id;
  // A ready proposal that can be approved: its base is the latest, and the server has not refused it on a recheck.
  const approvable = p.status === "ready" && !stale && refused === null;
  const conflicted = p.status === "conflicted" || outcome?.kind === "conflict";
  const title = rich(p.mode === "edit" ? "proposal.title.edit" : "proposal.title.scenario", { model: data(page.model.name) });
  const approveLabel = p.mode === "edit" ? t("proposal.approve.edit", { version: String(baseVersion + 1) }) : t("proposal.approve.scenario");
  const decidable = canEdit(role) && outcome === null && (p.status === "ready" || p.status === "needs_input");
  // A ready proposal on an older version is not ready for review: it can never be approved (H-039).
  const statusText =
    p.status === "approved" || p.status === "rejected"
      ? t(`proposal.status.${p.status}`, { date: p.decided_at ? dateTime(p.decided_at) : "" })
      : p.status === "ready" && stale
        ? t("proposal.status.stale")
        : t(`proposal.status.${p.status}`);
  const hasContent = p.changes !== undefined && p.status !== "needs_input";

  return (
    <>
    <RailModel id={p.model_id} />
    <PageHeader
      trail={[]}
      title={<Words parts={title} />}
      facts={[
        {
          label: t("band.status"),
          value: (
            <p role="status" id={ids.status} className={styles.state} data-proposal-status={p.status}>
              {statusText}
            </p>
          ),
        },
        ...(writer === null ? [] : [{ label: t("ai.writtenBy"), value: writer }]),
        ...(reader === null ? [] : [{ label: t("ai.photoReadBy"), value: reader }]),
      ]}
    />
    <article className={styles.page}>
      <div className={styles.main}>
      <p className={styles.meta}>
        {t("proposal.asked", { date: dateTime(p.created_at), version: String(baseVersion) })}
        {gap}
        {stale ? t("proposal.latest.newer", { version: String(latestVersion.version) }) : t("proposal.latest.same", { version: String(baseVersion) })}
      </p>
      {p.source_image && (
        // T-056: asked for with a photo; the server keeps its pixels only and names it by type and size.
        <p className={styles.meta} data-source="image">
          {t("proposal.fromPhoto", {
            type: p.source_image.media_type === "image/png" ? "PNG" : "JPEG",
            width: String(p.source_image.width),
            height: String(p.source_image.height),
          })}
        </p>
      )}
      {p.status === "generating" && <p>{t("proposal.generatingHint")}</p>}
      {approvable && (
        <p>{t(p.mode === "edit" ? "proposal.effect.edit" : "proposal.effect.scenario", { version: String(p.mode === "edit" ? baseVersion + 1 : baseVersion) })}</p>
      )}
      {pollProblem && (
        <>
          <ProblemView problem={pollProblem} />
          <Button
            onPress={() => {
              setPollProblem(null);
              setPollAttempt((n) => n + 1);
            }}
          >
            {t("retry")}
          </Button>
        </>
      )}

      {p.status === "failed" && (
        <>
          <ProblemView problem={failureProblem(p)} />
          <p>{t("proposal.askAgain")}</p>
        </>
      )}

      {p.summary !== undefined && (
        <section className={styles.section} data-section="summary" aria-labelledby={`${id}-summary`}>
          <h2 id={`${id}-summary`}>{t("proposal.summary")}</h2>
          <p className={styles.words}>{visible(p.summary, "prose")}</p>
        </section>
      )}

      {p.status === "needs_input" && (
        <section className={styles.section} data-section="questions" aria-labelledby={`${id}-questions`}>
          <h2 id={`${id}-questions`}>{t("proposal.questions")}</h2>
          <ol className={styles.words}>
            {(p.questions ?? []).map((q, i) => (
              <li key={i}>{visible(q, "prose")}</li>
            ))}
          </ol>
          <p>{t("proposal.questionsHint")}</p>
        </section>
      )}

      {hasContent && (
        <section className={styles.section} data-section="changes" aria-labelledby={`${id}-changes`}>
          <h2 id={`${id}-changes`}>{t("proposal.changes", { version: String(baseVersion) })}</h2>
          <ChangeList base={page.base.ir} version={baseVersion} changes={p.changes!} patchHash={p.patch_hash} />
        </section>
      )}

      {p.assumptions !== undefined && (hasContent || p.assumptions.length > 0) && (
        <section className={styles.section} data-section="assumptions" aria-labelledby={`${id}-assumptions`}>
          <h2 id={`${id}-assumptions`}>{t("proposal.assumptions")}</h2>
          {p.assumptions.length === 0 ? (
            <p>{t("proposal.assumptions.none")}</p>
          ) : (
            <ol className={styles.words}>
              {p.assumptions.map((a, i) => (
                <li key={i}>{visible(a, "prose")}</li>
              ))}
            </ol>
          )}
        </section>
      )}

      {p.expected_effects !== undefined && p.expected_effects.length > 0 && (
        <section className={styles.section} data-section="effects" aria-labelledby={`${id}-effects`}>
          <h2 id={`${id}-effects`}>{t("proposal.effects")}</h2>
          <p className={styles.hint}>{t("proposal.effectsHint")}</p>
          <ul className={styles.words}>
            {p.expected_effects.map((e, i) => (
              <li key={i}>{visible(e, "prose")}</li>
            ))}
          </ul>
        </section>
      )}

      {/* H-046 (2): the pages the AI read, when it was asked to research on the web. */}
      <Sources sources={p.sources} search={p.generated_by?.web_search} />

      {/* What was decided, when the page opened on a decided proposal. */}
      {outcome === null && p.status === "approved" && (
        <section className={styles.section} data-decided="approved">
          {p.mode === "edit" && page.created && <p>{t("proposal.decided.version", { version: String(page.created.version) })}</p>}
          {p.mode === "scenario" && p.scenario_id && (
            <p>
              <Link href={hrefs.scenario(p.scenario_id)}>{t("proposal.outcome.openScenario")}</Link>
            </p>
          )}
          <Comment text={p.decision_comment} />
        </section>
      )}
      {outcome?.kind !== "rejected" && p.status === "rejected" && (
        <section className={styles.section} data-decided="rejected">
          <Comment text={p.decision_comment} />
        </section>
      )}

      {conflicted && (
        <section ref={outcomeRef} tabIndex={-1} className={styles.conflict} data-conflict aria-labelledby={`${id}-conflict`}>
          <h2 id={`${id}-conflict`}>{t("proposal.conflict.title")}</h2>
          <p>{t("proposal.conflict.body", { base: String(baseVersion), latest: String(latestVersion.version) })}</p>
          {page.latest && (
            <>
              <p>{t("proposal.conflict.latest", { latest: String(page.latest.version), date: dateTime(page.latest.created_at) })}</p>
              <Comment text={page.latest.comment ?? undefined} />
            </>
          )}
          <p>{t("proposal.conflict.next", { latest: String(latestVersion.version) })}</p>
        </section>
      )}

      {outcome?.kind === "approved" && (
        <section ref={outcomeRef} tabIndex={-1} className={styles.outcome} data-outcome="approved" aria-labelledby={`${id}-outcome`}>
          {outcome.approval.mode === "edit" ? (
            <>
              <h2 id={`${id}-outcome`}>{t("proposal.outcome.edit", { version: String(outcome.approval.version) })}</h2>
              <p>{t("proposal.outcome.editBody", { version: String(outcome.approval.version), base: String(baseVersion) })}</p>
              {outcome.approval.draft_out_of_date && <p>{t("proposal.outcome.draftKept")}</p>}
            </>
          ) : (
            <>
              <h2 id={`${id}-outcome`}>{t("proposal.outcome.scenario")}</h2>
              <p>{t("proposal.outcome.scenarioBody", { base: String(baseVersion) })}</p>
              <p>
                <Link href={hrefs.scenario(outcome.approval.scenario_id)}>{t("proposal.outcome.openScenario")}</Link>
              </p>
            </>
          )}
        </section>
      )}
      {outcome?.kind === "rejected" && (
        <section ref={outcomeRef} tabIndex={-1} className={styles.outcome} data-outcome="rejected" aria-labelledby={`${id}-outcome`}>
          <h2 id={`${id}-outcome`}>{t("proposal.outcome.rejected")}</h2>
          <Comment text={p.decision_comment} />
        </section>
      )}

      </div>
      {/* H-041: the risk and the decision beside the proposal, where the reviewer decides; a refused
          decision's problem and the read-only note sit there too (review M3). */}
      <div className={styles.aside}>
        {problem && <ProblemView problem={problem} />}
        {(p.status === "ready" || p.status === "needs_input") && outcome === null && !canEdit(role) && (
          <Callout tone="info">{t(role === null ? "proposal.readOnly.unknownRole" : "proposal.readOnly.viewer")}</Callout>
        )}
        {(hasContent || p.status === "invalid") && (
          <section className={styles.section} data-section="risk" aria-labelledby={`${id}-risk`}>
            <h2 id={`${id}-risk`}>{t("proposal.risk")}</h2>
            <div className={styles.claims}>
              <div className={styles.authority} data-cell="server">
                <h3>{t("proposal.risk.server")}</h3>
                {p.server_risk_level && <p className={styles.level}>{t(`proposal.risk.level.${p.server_risk_level}`)}</p>}
                <Checks issues={p.validation?.issues ?? []} valid={p.validation?.valid === true} version={baseVersion} />
              </div>
              {p.provider_risk_claim && (
                <div className={styles.claim} data-cell="ai">
                  <h3>{t("proposal.risk.ai")}</h3>
                  <p className={styles.level}>{t(`proposal.risk.level.${p.provider_risk_claim}`)}</p>
                  {p.confidence !== undefined && <p>{t("proposal.risk.confidence", { percent: number(p.confidence, { style: "percent", maximumFractionDigits: 0 }) })}</p>}
                  <p className={styles.hint}>{t("proposal.risk.confidenceHint")}</p>
                </div>
              )}
            </div>
            {p.risk_level && <p>{t(`proposal.risk.counted.${p.risk_level}`)}</p>}
            {p.status === "invalid" && <p>{t("proposal.askAgain")}</p>}
          </section>
        )}

      {decidable && (
        <section className={styles.decision} data-section="decision" aria-labelledby={`${id}-decision`}>
          <h2 id={`${id}-decision`}>{t("proposal.decision")}</h2>
          {p.mode === "edit" && approvable && <p className={styles.hint}>{t("proposal.draftNote")}</p>}
          {decision === "confirmApprove" || decision === "approving" ? (
            <div role="group" aria-labelledby={ids.question} className={styles.confirm}>
              <p id={ids.question} className={styles.question}>
                {p.mode === "edit" ? t("proposal.approve.question.edit", { version: String(baseVersion + 1), base: String(baseVersion) }) : t("proposal.approve.question.scenario", { base: String(baseVersion) })}
              </p>
              <div className={styles.actions}>
                {/* Worded apart from Approve, so a second Enter is not taken for the first (H-039). */}
                <Button ref={confirmRef} variant="primary" icon={<ApproveIcon />} onPress={() => void approve()}>
                  {p.mode === "edit" ? t("proposal.approve.confirm.edit", { version: String(baseVersion + 1) }) : t("proposal.approve.confirm.scenario")}
                </Button>
                <Button
                  onPress={() => {
                    if (busy.current) return;
                    setDecision("idle");
                    setFocus("approve");
                  }}
                >
                  {t("proposal.cancel")}
                </Button>
              </div>
            </div>
          ) : decision === "confirmReject" || decision === "rejecting" ? (
            <div role="group" aria-labelledby={ids.question} className={styles.confirm}>
              <p id={ids.question} className={styles.question}>
                {t("proposal.reject.question")}
              </p>
              <TextField multiline label={t("proposal.reject.reason")} description={t("proposal.reject.reasonHint")} value={reason} onChange={setReason} maxLength={2000} />
              <div className={styles.actions}>
                <Button ref={confirmRef} variant="danger" icon={<CloseIcon />} onPress={() => void reject()}>
                  {t("proposal.reject.confirm")}
                </Button>
                <Button
                  onPress={() => {
                    if (busy.current) return;
                    setDecision("idle");
                    setFocus("reject");
                  }}
                >
                  {t("proposal.cancel")}
                </Button>
              </div>
            </div>
          ) : (
            <>
              {/* A ready proposal whose base is no longer the latest can never be approved: say so, offer no Approve. */}
              {stale && p.status === "ready" && <Callout tone="warning">{t("proposal.stale", { base: String(baseVersion), latest: String(latestVersion.version) })}</Callout>}
              {/* Refused on the server's recheck (D-069 (b) B1): it stays ready but cannot be approved as it is; reject and ask again. */}
              {!stale && p.status === "ready" && refused !== null && (
                <div ref={refusedRef} tabIndex={-1} className={styles.refused} data-refused={refused}>
                  <Callout tone="warning">{t(refused === "checks" ? "proposal.refused.checks" : "proposal.refused.risk")}</Callout>
                </div>
              )}
              <div className={styles.actions}>
                {approvable && (
                  <Button
                    ref={approveRef}
                    variant="primary"
                    icon={<ApproveIcon />}
                    onPress={() => {
                      setProblem(null);
                      setDecision("confirmApprove");
                      setFocus("confirm");
                    }}
                  >
                    {approveLabel}
                  </Button>
                )}
                <Button
                  ref={rejectRef}
                  variant="danger"
                  icon={<CloseIcon />}
                  onPress={() => {
                    setProblem(null);
                    setDecision("confirmReject");
                    setFocus("confirm");
                  }}
                >
                  {t("proposal.reject")}
                </Button>
              </div>
            </>
          )}
          <StatusRegion message={decision === "approving" ? t("proposal.approving") : decision === "rejecting" ? t("proposal.rejecting") : null} />
        </section>
      )}
      </div>
    </article>
    </>
  );
}

/** The server's validation of the candidate: passes, passes with warnings, or the checks that failed. */
function Checks({ issues, valid, version }: { issues: readonly ValidationIssue[]; valid: boolean; version: number }) {
  const { t } = useI18n();
  if (valid && issues.length === 0) return <p>{t("proposal.checks.pass", { version: String(version) })}</p>;
  return (
    <>
      <p>{valid ? t("proposal.checks.warnings", { count: issues.length }) : t("proposal.checks.failed", { count: issues.length })}</p>
      <ul className={styles.issues}>
        {issues.map((i, n) => {
          const key = validationKey(i.code);
          return (
            <li key={n}>
              {key ? t(key) : <span lang="en">{i.message}</span>} <code>{i.path || "/"}</code>
            </li>
          );
        })}
      </ul>
    </>
  );
}

/** A person's comment, shown as they wrote it. */
function Comment({ text }: { text: string | undefined }) {
  const { t, gap } = useI18n();
  if (text === undefined || text === "") return null;
  return (
    <p>
      {t("proposal.decided.comment")}
      {gap}
      <span className={styles.words}>{visible(text, "prose")}</span>
    </p>
  );
}
