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
// reason. One line under the field says that what is typed or uploaded goes
// to an AI service, so only synthetic data belongs there (T-057, H-035 (1));
// the field and the photo button name it as their description.
import { useId, useRef, useState } from "react";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import type { Model, ProposalRequest } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { TextField } from "../../ui/field";
import { Select } from "../../ui/select";
import { StatusRegion } from "../../ui/status";
import { PhotoForm } from "./photo-form";
import styles from "./proposal.module.css";

type Mode = ProposalRequest["mode"];

export function AskForm({ model, blockedReason, onCreated, onReload, photo = false }: {
  model: Model;
  /** Also offer a photo of the line (T-056: the model's pack reads photos). */
  photo?: boolean;
  /** Why sending must wait (unsaved edits the page would lose), or null. */
  blockedReason: string | null;
  /** The server stored the proposal: follow it. */
  onCreated: (proposalId: string) => void;
  /** Reads the model again; answers with the problem when that failed. */
  onReload: () => Promise<Problem | null>;
}) {
  const api = useApi();
  const { t, locale } = useI18n();
  const headingId = useId();
  const reasonId = useId();
  const noticeId = useId();
  const [intent, setIntent] = useState("");
  const [mode, setMode] = useState<Mode>("edit");
  const [missing, setMissing] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [sending, setSending] = useState(false);
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
    const body: ProposalRequest = { base_version_id: latest.id, mode, intent: words, output_locale: locale };
    // The same request again (a retry) takes the same key; another request a new one.
    const request = `${model.id} ${JSON.stringify(body)}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    busy.current = true;
    setSending(true);
    const r = await api.createProposal(model.id, body, sent.current.key);
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
        label={t("ask.intent")}
        description={t("ask.intentHint", { version: String(latest.version) })}
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
      <div className={styles.askRow}>
        <Select
          label={t("ask.mode")}
          items={[
            { id: "edit", label: t("ask.mode.edit") },
            { id: "scenario", label: t("ask.mode.scenario") },
          ]}
          value={mode}
          onChange={(id) => setMode(id === "scenario" ? "scenario" : "edit")}
        />
        <p className={styles.hint}>{mode === "edit" ? t("ask.modeHint.edit", { version: String(latest.version + 1) }) : t("ask.modeHint.scenario", { version: String(latest.version) })}</p>
      </div>
      <div className={styles.actions}>
        <Button blocked={blockedReason !== null} aria-describedby={blockedReason !== null ? reasonId : undefined} onPress={() => void send()}>
          {t("ask.send")}
        </Button>
        {photo && <PhotoForm model={model} blockedReason={blockedReason} reasonId={reasonId} noticeId={noticeId} onCreated={onCreated} />}
        {blockedReason !== null && (
          <p id={reasonId} className={styles.reason}>
            {blockedReason}
          </p>
        )}
      </div>
      {problem && (
        <>
          <ProblemView problem={problem} headingLevel={3} />
          {problem.code === "MODEL_VERSION_CONFLICT" && <Button onPress={() => void reload()}>{t("ask.reload")}</Button>}
        </>
      )}
      <StatusRegion message={sending ? t("ask.sending") : null} />
    </section>
  );
}
