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
// person publishes. Unticked, the form is what it was.
import { useId, useRef, useState } from "react";
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
import { TextField } from "../../ui/field";
import { Select } from "../../ui/select";
import { StatusRegion } from "../../ui/status";
import { PlannerChoice, RunSettings } from "../investigations/act-fields";
import { DEFAULT_PLANNER, investigationsOffered } from "../investigations/flags";
import { type ActIssue, fromRequest, OBJECTIVES, parseActRunForm } from "../investigations/request";
import { defaultRunForm, type RunField, type RunForm } from "../simulations/run-config";
import { DEFAULT_MODEL, ModelChoice, modelField } from "./model-choice";
import { PhotoForm } from "./photo-form";
import styles from "./proposal.module.css";

type Mode = ProposalRequest["mode"];

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
  const [intent, setIntent] = useState("");
  const [mode, setMode] = useState<Mode>("edit");
  const [research, setResearch] = useState(false);
  const [aiModel, setAiModel] = useState<ProposalModel>(DEFAULT_MODEL);
  const [missing, setMissing] = useState(false);
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
  const latest = model.latest_version;

  const send = async () => {
    if (busy.current) return;
    const words = intent.trim();
    if (words === "") {
      setMissing(true);
      return;
    }
    setMissing(false);
    setProblem(null);
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

  const reload = async () => {
    const failed = await onReload();
    setProblem(failed);
  };

  return (
    <section className={styles.ask} data-ask aria-labelledby={headingId}>
      <h2 id={headingId}>{t("ask.title")}</h2>
      <TextField
        multiline
        rows={3}
        label={t("ask.intent")}
        // H-061 A8 review round 1: while the AI acts on it, no word of a proposal's approval (REQ-019).
        description={t(acting ? "investigation.intentHint" : "ask.intentHint", { version: String(latest.version) })}
        notice={{ id: noticeId, text: t("ask.dataNotice") }}
        value={intent}
        onChange={(v) => {
          setIntent(v);
          if (missing && v.trim() !== "") setMissing(false);
        }}
        maxLength={20000}
        isInvalid={missing}
        errorMessage={t("ask.intentRequired")}
      />
      <div className={styles.askMode}>
        <Select
          label={t(acting ? "investigation.mode" : "ask.mode")}
          items={[
            { id: "edit", label: t("ask.mode.edit") },
            { id: "scenario", label: t("ask.mode.scenario") },
          ]}
          value={mode}
          onChange={(id) => setMode(id === "scenario" ? "scenario" : "edit")}
        />
        <p className={styles.hint}>
          {mode === "edit"
            ? t(acting ? "investigation.modeHint.edit" : "ask.modeHint.edit", { version: String(latest.version + 1) })
            : t(acting ? "investigation.modeHint.scenario" : "ask.modeHint.scenario", { version: String(latest.version) })}
        </p>
      </div>
      <ModelChoice value={aiModel} onChange={setAiModel} />
      {canAct && (
        // H-061 A8: the AI acts on it for the person, within the caps; off by default.
        <div className={styles.askResearch} data-act>
          <Checkbox isSelected={act} onChange={setAct} aria-describedby={actHintId}>
            {t("investigation.act")}
          </Checkbox>
          <p id={actHintId} className={styles.hint}>
            {t(mode === "edit" ? "investigation.actHint.edit" : "investigation.actHint.scenario")}
          </p>
        </div>
      )}
      {acting && mode === "scenario" && <RunSettings unit={timeUnit} form={runForm} onForm={setRunForm} issues={runIssues} objective={objective} onObjective={setObjective} />}
      {acting && <PlannerChoice value={planner} onChange={setPlanner} />}
      <div className={styles.askResearch}>
        <Checkbox isSelected={research} onChange={setResearch} aria-describedby={photo && !acting ? `${researchHintId} ${researchPhotoId}` : researchHintId} data-research>
          {t("ask.research")}
        </Checkbox>
        <p id={researchHintId} className={styles.hint}>
          {t("ask.researchHint")}
          {acting && (
            <>
              {gap}
              {t(mode === "edit" ? "investigation.research.edit" : "investigation.research")}
            </>
          )}
        </p>
        {photo && !acting && (
          <p id={researchPhotoId} className={styles.hint} data-research-photo>
            {t("ask.researchHint.photo")}
          </p>
        )}
      </div>
      <div className={styles.askRow}>
        <Button variant="ai" icon={<SendIcon />} blocked={blockedReason !== null} aria-describedby={blockedReason !== null ? reasonId : undefined} onPress={() => void send()}>
          {t(acting ? "investigation.start" : "ask.send")}
        </Button>
        {photo && !acting && <PhotoForm model={model} aiModel={aiModel} blockedReason={blockedReason} reasonId={reasonId} noticeId={noticeId} onCreated={onCreated} />}
        {blockedReason !== null && (
          <p id={reasonId} className={styles.reason}>
            {blockedReason}
          </p>
        )}
      </div>
      {problem && (
        <>
          <ProblemView problem={problem} headingLevel={3} guidance={advice(problem)} />
          {problem.code === "MODEL_VERSION_CONFLICT" && <Button onPress={() => void reload()}>{t("ask.reload")}</Button>}
        </>
      )}
      <StatusRegion message={sending ? t(acting ? "investigation.starting" : "ask.sending") : null} />
    </section>
  );
}
