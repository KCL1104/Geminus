"use client";
// The model page's one way to ask the AI for a change (T-031, hackathon
// slice, H-029 item 4): the person's words, what to make (a change to the
// model or a scenario to simulate: the proposal's mode, which the API fixes
// for it), and one send button. The request goes to createProposal on the
// model's latest version, with the reader's language for the AI's prose;
// the server answers with the proposal it stored (generating), and the page
// moves on to it. Nothing here proposes, decides or changes the model.
// Retrying the same request reuses its key (D-011); a newer version since
// the page loaded (409) keeps the words, and the model is read again only
// when the person asks. For a domain whose pack reads photos the actions
// also offer a photo of the line (T-056, PhotoForm), blocked for the same
// reason. The line under the field also says that what is typed or uploaded
// goes to an AI service, so only synthetic data belongs there (T-057, H-035
// (1)); the field and the photo button name it as their description. The form
// stands at the top of the editor's side panel (H-040): the field, what to
// make with its hint, then the buttons. H-046 (2): one opt-in, off by default
// and asked per request, lets the AI research the real process on the web
// first (Meta's search grounding); its line says the description is used for
// a web search. Only a ticked box adds research: true to the request, so a
// request without it is byte for byte what it was; a deployment that cannot
// research refuses it (503, details[0].code AI_RESEARCH_UNSUPPORTED) and the
// form says to send without it. A photo is never researched (createImageProposal
// takes no research): where the photo button is offered, the opt-in's
// description says it applies to what is sent in words only (review round 1,
// L3), and the photo request is sent as it always was. H-049: the person
// chooses the model (ModelChoice: Muse Spark 1.3, the default, or Muse Wonder
// 1.0), for what is sent in words and for a photo alike; only Muse Wonder adds
// model to a request, and a deployment that does not offer it refuses it (503,
// details[0].code AI_MODEL_NOT_APPROVED), the form saying to choose the default.
// H-061 A8 (PROPOSED): in a build with investigations on, and where the page
// can follow one, a box "Let the AI act on it" (off by default) turns the
// request into an AI investigation (createInvestigation): the hint says the
// caps and that the AI never publishes; a scenario takes the settings of the
// AI's runs and its aim; research is for the first what-if only; the planner
// is a choice only where Muse Wonder is offered as planner. The photo is not
// offered then (no photo inside an investigation, H-056 §17). Ticked, the
// form says nothing of a proposal's approval (review round 1): the intent's
// hint, the mode's label and its hint say what the AI does and that only a
// person publishes. Unticked, the form is what it was. H-073 (5): a request
// an approved photo proposal's page left for this model (station-research.ts)
// is taken once when the form opens: the field holds it and has the focus,
// research is ticked, a change to the model is chosen, and a line above the
// field (also the field's description) says where it came from; nothing is
// sent until the person sends it. Without one, the form is what it was.
// The owner's decision of 2026-11-02: the form is a mainstream prompt
// composer. One bordered box holds the request (about three lines, growing
// to ten, then scrolling inside itself) and, along its bottom edge, a tool
// row: what the AI proposes and the model as compact select chips, "Let the
// AI act on it" (where it exists) and research as toggle chips, and the
// send key at the right, an icon key named "Send to the AI" (or "Start the
// AI" while acting), the panel's one AI action. An option's line shows
// under the box only while it is on, as its description; the version line
// and the data notice are always under it. Enter sends and Shift+Enter is a
// new line; Enter while an input method composes never sends; an empty
// request or one over 20,000 characters is not sent and the box says why
// (nothing is cut while typing), with the focus on the request; a second
// Enter while sending does nothing; the composer's description says the
// keys. Above it, the model's earlier requests (EarlierRequests). The photo
// path stays a request of its own under the composer, since it takes no
// words. What is sent, and when, is what it was. H-079: an earlier request's
// Reuse puts its words, its mode and its model (only while this build still
// offers it; a request that named none, the default) back in the box, sends
// nothing, says so to a screen reader and moves the focus to the request,
// at the end of its words; what was in the box is replaced. Where the
// primary pointer is coarse (useCoarsePointer: a touch keyboard has no
// Shift+Enter), Enter makes a new line and only the send key sends; the
// keyboard's Enter key says so (enterKeyHint) and so does the description.
import { type KeyboardEvent, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { Label, TextArea, TextField as AriaTextField } from "react-aria-components";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import type { InvestigationObjectiveMetric, InvestigationRequest, Model, ModelTimeUnit, PlannerModel, ProposalModel, ProposalRequest } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { SendIcon } from "../../ui/icons";
import { Select } from "../../ui/select";
import { StatusRegion } from "../../ui/status";
import { PlannerChoice, RunSettings } from "../investigations/act-fields";
import { DEFAULT_PLANNER, investigationsOffered } from "../investigations/flags";
import { type ActIssue, fromRequest, OBJECTIVES, parseActRunForm } from "../investigations/request";
import { defaultRunForm, type RunField, type RunForm } from "../simulations/run-config";
import { useCoarsePointer } from "./coarse-pointer";
import { EarlierRequests } from "./earlier-requests";
import { DEFAULT_MODEL, ModelChip, modelField, ModelHint, offeredModels } from "./model-choice";
import { PhotoForm } from "./photo-form";
import type { Reused } from "./request-log";
import { askPrefill, clearAskPrefill } from "./station-research";
import styles from "./ask.module.css";

type Mode = ProposalRequest["mode"];

/** The most characters a request takes (http.schema.json ProposalRequest.intent and InvestigationFromRequest.intent: maxLength 20,000, counted in code points). */
export const MAX_INTENT = 20_000;

/**
 * Enter sends (the composer, 2026-11-02): not with Shift (a new line), never
 * while an input method is composing, and (H-079) never where the primary
 * pointer is coarse (touch): there Enter is a new line and the send key sends.
 */
export function sendsOnEnter(e: Pick<KeyboardEvent, "key" | "shiftKey" | "keyCode" | "nativeEvent">, touch = false): boolean {
  if (touch || e.key !== "Enter" || e.shiftKey) return false;
  return !(e.nativeEvent.isComposing || e.keyCode === 229 || e.nativeEvent.keyCode === 229);
}

/** What the form says for a refusal it knows: web research this deployment cannot do (H-046 (2)), a model it does not offer (H-049), and why an investigation could not start (H-061 A8). */
export function advice(p: Problem): MessageKey | undefined {
  switch (p.details[0]?.code) {
    case "AI_RESEARCH_UNSUPPORTED":
      return "ask.research.unavailable";
    case "AI_MODEL_NOT_APPROVED":
      return "ask.model.unavailable";
    case "INVESTIGATION_RUNNING":
      return "investigation.refused.running";
    case "INVESTIGATION_DAILY_LIMIT":
      return "investigation.refused.daily";
    case "INVESTIGATIONS_OFF":
      return "investigation.refused.off";
    case "AI_ROLE_NOT_APPROVED":
      return "investigation.refused.notApproved";
    case "INVESTIGATION_PLANNER_NOT_OFFERED":
      return "investigation.refused.planner";
    case "INVESTIGATION_RESEARCH_OVER_CEILING":
      return "investigation.refused.research";
  }
  if (p.details[0]?.path === "/from_analysis_id") return "investigation.refused.baseline";
  return undefined;
}

export function AskForm({ model, blockedReason, onCreated, onReload, photo = false, onInvestigation, timeUnit = "minute" }: {
  model: Model;
  /** Also offer a photo of the line (T-056: the model's pack reads photos). */
  photo?: boolean;
  /** Why sending must wait (unsaved edits the page would lose), or null. */
  blockedReason: string | null;
  /** The server stored the proposal: follow it. */
  onCreated: (proposalId: string) => void;
  /** Reads the model again; answers with the problem when that failed. */
  onReload: () => Promise<Problem | null>;
  /** H-061 A8: the server started an investigation: follow it. Without it (or in a build without investigations) the form offers none. */
  onInvestigation?: (investigationId: string) => void;
  /** The model's time unit, for the AI's run settings (H-047's week in it). */
  timeUnit?: ModelTimeUnit;
}) {
  const api = useApi();
  const { t, locale, gap } = useI18n();
  const headingId = useId();
  const reasonId = useId();
  const noticeId = useId();
  const researchHintId = useId();
  const researchPhotoId = useId();
  const prefillId = useId();
  const intentHintId = useId();
  const modeHintId = useId();
  const modelHintId = useId();
  const keysId = useId();
  const errorId = useId();
  // H-073 (5): the request left for this model, read once when the form opens and then forgotten.
  const [prefill] = useState(() => askPrefill(model.id));
  useEffect(() => clearAskPrefill(model.id), [model.id]);
  const [intent, setIntent] = useState(prefill?.intent ?? "");
  const [mode, setMode] = useState<Mode>(prefill?.mode ?? "edit");
  const [research, setResearch] = useState(prefill?.research ?? false);
  const [aiModel, setAiModel] = useState<ProposalModel>(DEFAULT_MODEL);
  const [missing, setMissing] = useState(false);
  // The request's length in characters when it was over MAX_INTENT at the last send, or null.
  const [tooLong, setTooLong] = useState<number | null>(null);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [sending, setSending] = useState(false);
  // H-061 A8: the AI acts on it (an investigation), its run settings, aim and planner.
  const canAct = onInvestigation !== undefined && investigationsOffered();
  const [act, setAct] = useState(false);
  const [runForm, setRunForm] = useState<RunForm>(() => defaultRunForm(timeUnit));
  const [runIssues, setRunIssues] = useState<Partial<Record<RunField, ActIssue>>>({});
  const [objective, setObjective] = useState<InvestigationObjectiveMetric>(OBJECTIVES[0]!);
  const [planner, setPlanner] = useState<PlannerModel>(DEFAULT_PLANNER);
  const acting = canAct && act;
  const actHintId = useId();
  const busy = useRef(false);
  const sent = useRef<{ request: string; key: string } | null>(null);
  const field = useRef<HTMLTextAreaElement>(null);
  // H-079: a touch keyboard's Enter is a new line; an earlier request put back by Reuse.
  const touch = useCoarsePointer();
  const [reusedNote, setReusedNote] = useState(false);
  // Each Reuse, counted, so the same request reused twice still takes the focus.
  const [reuses, setReuses] = useState(0);
  const focusAfterReuse = useRef(false);
  const latest = model.latest_version;
  const blocked = blockedReason !== null;

  // The request grows with its words up to the box's cap (CSS max-height), then scrolls inside itself.
  useLayoutEffect(() => {
    const el = field.current;
    if (el === null) return;
    el.style.height = "auto";
    if (el.scrollHeight > 0) el.style.height = `${el.scrollHeight}px`;
    // Reused: the focus on the request, at the end of the words put back.
    if (focusAfterReuse.current) {
      focusAfterReuse.current = false;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [intent, reuses]);

  /** Reuse (H-079): the earlier request's words, mode and model in the box; nothing is sent. */
  const reuse = (r: Reused) => {
    setIntent(r.intent);
    setMode(r.mode);
    const named = r.model ?? DEFAULT_MODEL;
    if (offeredModels().includes(named)) setAiModel(named);
    setMissing(false);
    setTooLong(null);
    setReusedNote(true);
    setReuses((n) => n + 1);
    focusAfterReuse.current = true;
  };

  const send = async () => {
    if (busy.current || blocked) return;
    const words = intent.trim();
    if (words === "") {
      setMissing(true);
      setTooLong(null);
      field.current?.focus();
      return;
    }
    const length = [...words].length;
    if (length > MAX_INTENT) {
      setMissing(false);
      setTooLong(length);
      field.current?.focus();
      return;
    }
    setMissing(false);
    setTooLong(null);
    setProblem(null);
    setReusedNote(false);
    let body: ProposalRequest | InvestigationRequest = { base_version_id: latest.id, mode, intent: words, output_locale: locale, ...(research ? { research: true } : {}), ...modelField(aiModel) };
    if (acting) {
      // A scenario's run settings are checked at their fields first; an edit takes none.
      const parsed = mode === "scenario" ? parseActRunForm(runForm, timeUnit) : null;
      if (parsed !== null && !parsed.ok) {
        setRunIssues(parsed.issues);
        return;
      }
      setRunIssues({});
      body = fromRequest({ base: latest.id, mode, intent: words, locale, research, model: aiModel, planner, config: parsed?.config ?? null, baselineName: t("investigation.baselineName"), objective });
    }
    // The same request again (a retry) takes the same key; another request a new one.
    const request = `${acting ? "investigation" : "proposal"} ${model.id} ${JSON.stringify(body)}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    busy.current = true;
    setSending(true);
    if (acting) {
      const r = await api.createInvestigation(model.id, body as InvestigationRequest, sent.current.key);
      busy.current = false;
      setSending(false);
      if (r.ok) onInvestigation!(r.data.investigation_id);
      else setProblem(r.problem);
      return;
    }
    const r = await api.createProposal(model.id, body as ProposalRequest, sent.current.key);
    busy.current = false;
    setSending(false);
    if (r.ok) onCreated(r.data.proposal_id);
    else setProblem(r.problem);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!sendsOnEnter(e, touch)) return;
    // Enter never makes a new line here: it sends, or, while sending waits, does nothing.
    e.preventDefault();
    void send();
  };

  const reload = async () => {
    const failed = await onReload();
    setProblem(failed);
  };

  const invalid = missing || tooLong !== null;
  // What the request says of itself: where a filled-in request came from, why sending waits, the version, the notice, the keys.
  const describedBy = [prefill !== null ? prefillId : null, blocked ? reasonId : null, invalid ? errorId : null, intentHintId, noticeId, keysId].filter(Boolean).join(" ");
  const researchDescribedBy = research ? (photo && !acting ? `${researchHintId} ${researchPhotoId}` : researchHintId) : undefined;

  return (
    <section className={styles.ask} data-ask aria-labelledby={headingId}>
      <h2 id={headingId}>{t("ask.title")}</h2>
      <EarlierRequests model={model} investigations={canAct} onReuse={reuse} />
      {prefill !== null && (
        <p id={prefillId} className={`${styles.hint} ${styles.prefill}`} data-prefilled>
          {t("ask.prefill.stationTimes.note")}
        </p>
      )}
      <div className={styles.composer} data-composer data-invalid={invalid || undefined}>
        <AriaTextField
          className={styles.field}
          value={intent}
          onChange={(v) => {
            setIntent(v);
            setReusedNote(false);
            if (missing && v.trim() !== "") setMissing(false);
            if (tooLong !== null && [...v.trim()].length <= MAX_INTENT) setTooLong(null);
          }}
          isInvalid={invalid}
          validationBehavior="aria"
          autoFocus={prefill !== null}
          aria-describedby={describedBy}
        >
          <Label className={styles.offscreen}>{t("ask.intent")}</Label>
          <TextArea ref={field} className={styles.input} rows={3} placeholder={t("ask.placeholder")} enterKeyHint={touch ? "enter" : "send"} onKeyDown={onKeyDown} />
        </AriaTextField>
        <span id={keysId} className={styles.offscreen}>
          {t(touch ? "ask.keys.touch" : "ask.keys")}
        </span>
        <div className={styles.tools} data-tools>
          <Select
            variant="chip"
            label={t(acting ? "investigation.mode" : "ask.mode")}
            items={[
              { id: "edit", label: t("ask.mode.edit") },
              { id: "scenario", label: t("ask.mode.scenario") },
            ]}
            value={mode}
            onChange={(id) => setMode(id === "scenario" ? "scenario" : "edit")}
            aria-describedby={modeHintId}
          />
          <ModelChip value={aiModel} onChange={setAiModel} hintId={modelHintId} />
          {/* A toggle chip shows the start or the end of its option's name, which stays its whole name (WCAG 2.5.3). */}
          {canAct && (
            // H-061 A8: the AI acts on it for the person, within the caps; off by default.
            <Checkbox variant="chip" isSelected={act} onChange={setAct} aria-label={t("investigation.act")} aria-describedby={act ? actHintId : undefined} data-act>
              {t("investigation.act.chip")}
            </Checkbox>
          )}
          <Checkbox variant="chip" isSelected={research} onChange={setResearch} aria-label={t("ask.research")} aria-describedby={researchDescribedBy} data-research>
            {t("ask.research.chip")}
          </Checkbox>
          <Button
            variant="ai"
            className={styles.send}
            icon={<SendIcon />}
            aria-label={t(acting ? "investigation.start" : "ask.send")}
            blocked={blocked}
            aria-describedby={blocked ? reasonId : undefined}
            onPress={() => void send()}
          />
        </div>
      </div>
      {invalid && (
        <p id={errorId} className={styles.error}>
          {tooLong !== null ? t("ask.intentTooLong", { count: tooLong, max: MAX_INTENT }) : t("ask.intentRequired")}
        </p>
      )}
      {blocked && (
        <p id={reasonId} className={styles.reason}>
          {blockedReason}
        </p>
      )}
      <div className={styles.notes}>
        <ModelHint value={aiModel} id={modelHintId} className={styles.hint} />
        {acting && (
          <p id={actHintId} className={styles.hint}>
            {t(mode === "edit" ? "investigation.actHint.edit" : "investigation.actHint.scenario")}
          </p>
        )}
        {research && (
          <p id={researchHintId} className={styles.hint}>
            {t("ask.researchHint")}
            {acting && (
              <>
                {gap}
                {t(mode === "edit" ? "investigation.research.edit" : "investigation.research")}
              </>
            )}
          </p>
        )}
        {research && photo && !acting && (
          <p id={researchPhotoId} className={styles.hint} data-research-photo>
            {t("ask.researchHint.photo")}
          </p>
        )}
        {acting && mode === "scenario" && <RunSettings unit={timeUnit} form={runForm} onForm={setRunForm} issues={runIssues} objective={objective} onObjective={setObjective} />}
        {acting && <PlannerChoice value={planner} onChange={setPlanner} />}
        {/* H-061 A8 review round 1: while the AI acts on it, no word of a proposal's approval (REQ-019). */}
        <p className={styles.hint} data-version-line>
          <span id={intentHintId}>{t(acting ? "investigation.intentHint" : "ask.intentHint", { version: String(latest.version) })}</span>
          {gap}
          <span id={modeHintId}>
            {mode === "edit"
              ? t(acting ? "investigation.modeHint.edit" : "ask.modeHint.edit", { version: String(latest.version + 1) })
              : t(acting ? "investigation.modeHint.scenario" : "ask.modeHint.scenario", { version: String(latest.version) })}
          </span>
        </p>
        <p id={noticeId} className={styles.hint}>
          {t("ask.dataNotice")}
        </p>
      </div>
      {photo && !acting && (
        <div className={styles.photoRow}>
          <PhotoForm model={model} aiModel={aiModel} blockedReason={blockedReason} reasonId={reasonId} noticeId={noticeId} onCreated={onCreated} />
        </div>
      )}
      {problem && (
        <div className={styles.problem}>
          <ProblemView problem={problem} headingLevel={3} guidance={advice(problem)} />
          {problem.code === "MODEL_VERSION_CONFLICT" && <Button onPress={() => void reload()}>{t("ask.reload")}</Button>}
        </div>
      )}
      <StatusRegion message={sending ? t(acting ? "investigation.starting" : "ask.sending") : null} />
      {/* Reused: said to a screen reader, since the box shows it already. */}
      <StatusRegion quiet message={reusedNote ? t("ask.log.reused") : null} />
    </section>
  );
}
