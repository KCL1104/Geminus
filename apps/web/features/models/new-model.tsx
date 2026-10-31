"use client";
// A new model from a template (H-045), on the project page for an editor
// or owner: every template the packs offer, under its domain's name with a
// one-line description, and a name that follows the chosen template until
// the person types one. The request goes to createModel with the reader's
// language for the template's default names; the server builds and
// validates the model, and the page moves on to its canvas. A retry of the
// same request reuses its key (D-011), so nothing is created twice; a
// refusal (a role that changed, a project that is gone) is the API's
// problem, shown as every page shows one.
//
// H-062: for a domain whose pack reads photos (PHOTO_DOMAINS, as the model
// page's photo button), the group also offers "Start from a photo or floor
// plan": the person picks a PNG or JPEG (the photo form's limit and notice,
// checked before anything is sent), the server creates the model from the
// pack's bare line (its photo template), then the photo goes to
// createImageProposal on the new model's version 1, as the model page's
// "Propose from a photo…" sends it, and the page opens the proposal. When
// the photo is not sent, the model exists: the page opens it with the
// refusal (photo-start.ts), and its photo button sends the photo again.
import { type FormEvent, useId, useRef, useState } from "react";
import { FileTrigger, Label, Radio, RadioGroup } from "react-aria-components";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import { useApi } from "../../lib/api/provider";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { TextField } from "../../ui/field";
import { CameraIcon, PlusIcon } from "../../ui/icons";
import { StatusRegion } from "../../ui/status";
import { advice, PHOTO_DOMAINS, PHOTO_TYPES, type PhotoRefusal, tooLarge } from "../ai-proposals/photo-form";
import styles from "./new-model.module.css";
import { notePhotoNotSent } from "./photo-start";
import { createRequest, domainName, FIRST_TEMPLATE, isPhotoTemplate, isTemplate, type Template, TEMPLATE_GROUPS, templateDescription, templateName } from "./templates";

/** The groups as the page offers them: a photo template only where the pack reads photos. */
const GROUPS = TEMPLATE_GROUPS.map((g) => ({ domain: g.domain, options: [...g.templates, ...(g.photo !== undefined && PHOTO_DOMAINS.includes(g.domain) ? [g.photo] : [])] as Template[] }));

export function NewModelForm({ projectId, onCreated, onProposal, onCancel }: {
  projectId: string;
  /** The server created the model: open it. */
  onCreated: (modelId: string) => void;
  /** H-062: the model was created and its photo sent as a proposal: open the proposal. */
  onProposal: (proposalId: string) => void;
  onCancel: () => void;
}) {
  const api = useApi();
  const { t, locale, gap } = useI18n();
  const id = useId();
  const [template, setTemplate] = useState<Template>(FIRST_TEMPLATE);
  // null: the chosen template's name, in the reader's language.
  const [typed, setTyped] = useState<string | null>(null);
  const [missing, setMissing] = useState(false);
  const [photo, setPhoto] = useState<File | null>(null);
  const [photoMissing, setPhotoMissing] = useState(false);
  const [problem, setProblem] = useState<PhotoRefusal | null>(null);
  const [step, setStep] = useState<"creating" | "sending" | null>(null);
  const busy = useRef(false);
  const sent = useRef<{ request: string; key: string } | null>(null);
  const fromPhoto = isPhotoTemplate(template);
  const name = typed ?? t(fromPhoto ? "newModel.photo.defaultName" : templateName(template));

  const choose = (files: FileList | null) => {
    const file = files?.[0];
    if (file === undefined || busy.current) return;
    const big = tooLarge(file);
    setProblem(big);
    if (big !== null) return;
    setPhoto(file);
    setPhotoMissing(false);
  };

  const create = async (e: FormEvent) => {
    e.preventDefault();
    if (busy.current) return;
    const trimmed = name.trim();
    const noPhoto = fromPhoto && photo === null;
    setMissing(trimmed === "");
    setPhotoMissing(noPhoto);
    if (trimmed === "" || noPhoto) return;
    setProblem(null);
    const body = createRequest(template, trimmed, locale);
    // The same request again (a retry) takes the same key; another request a new one.
    const request = `${projectId} ${JSON.stringify(body)}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    busy.current = true;
    setStep("creating");
    const r = await api.createModel(projectId, body, sent.current.key);
    if (!r.ok || !fromPhoto || photo === null) {
      busy.current = false;
      setStep(null);
      if (r.ok) onCreated(r.data.id);
      else setProblem({ problem: r.problem, guidance: undefined });
      return;
    }
    // The model exists; its photo goes on its version 1, once (the page leaves either way).
    setStep("sending");
    const p = await api.createImageProposal(r.data.id, { base_version_id: r.data.latest_version.id, output_locale: locale }, photo, newIdempotencyKey());
    busy.current = false;
    setStep(null);
    if (p.ok) {
      onProposal(p.data.proposal_id);
      return;
    }
    notePhotoNotSent(r.data.id, { problem: p.problem, guidance: advice(p.problem) });
    onCreated(r.data.id);
  };

  const photoIds = { hint: `${id}-photo-hint`, file: `${id}-photo-file`, error: `${id}-photo-error`, label: `${id}-photo-label` };
  const photoDescribedBy = [photo !== null ? photoIds.file : null, photoMissing ? photoIds.error : null, photoIds.hint].filter(Boolean).join(" ");
  const chooser = (
    <Button icon={<CameraIcon />} blocked={step !== null} aria-describedby={photoDescribedBy}>
      {t(photo === null ? "newModel.photo.choose" : "newModel.photo.again")}
    </Button>
  );

  return (
    <section className={styles.panel} aria-labelledby={`${id}-heading`} data-new-model>
      <h2 id={`${id}-heading`}>{t("newModel.title")}</h2>
      <p className={styles.intro}>{t("newModel.intro")}</p>
      <form onSubmit={create} noValidate aria-labelledby={`${id}-heading`}>
        <RadioGroup
          value={template}
          onChange={(v) => {
            if (!isTemplate(v)) return;
            setTemplate(v);
            setPhotoMissing(false);
          }}
          className={styles.templates}
        >
          <Label className={styles.label}>{t("newModel.template")}</Label>
          {GROUPS.map((g) => (
            <div key={g.domain} role="group" aria-labelledby={`${id}-${g.domain}`} className={styles.group}>
              <h3 id={`${id}-${g.domain}`} className={styles.domain}>
                {t(domainName(g.domain))}
              </h3>
              <div className={styles.options}>
                {g.options.map((tpl) => (
                  <Radio
                    key={tpl}
                    value={tpl}
                    autoFocus={tpl === FIRST_TEMPLATE}
                    aria-labelledby={`${id}-${tpl}-name`}
                    aria-describedby={`${id}-${tpl}-description`}
                    className={styles.option}
                    data-template={tpl}
                  >
                    <span className={styles.dot} aria-hidden="true" />
                    <span className={styles.text}>
                      <span id={`${id}-${tpl}-name`} className={styles.name}>
                        {t(templateName(tpl))}
                      </span>
                      <span id={`${id}-${tpl}-description`} className={styles.description}>
                        {t(templateDescription(tpl))}
                      </span>
                    </span>
                  </Radio>
                ))}
              </div>
            </div>
          ))}
        </RadioGroup>
        {fromPhoto && (
          // The photo to send once the model exists (H-062): the model page's limit and notice.
          <div role="group" aria-labelledby={photoIds.label} className={styles.photo} data-photo-start>
            <span id={photoIds.label} className={styles.label}>
              {t("newModel.photo.label")}
            </span>
            <div className={styles.photoRow}>
              {/* While the model is being created the button opens nothing: no file chooser behind it. */}
              {step !== null ? (
                chooser
              ) : (
                <FileTrigger acceptedFileTypes={PHOTO_TYPES} onSelect={choose}>
                  {chooser}
                </FileTrigger>
              )}
              {photo !== null && (
                <span id={photoIds.file} className={styles.file} data-photo-file>
                  <bdi>{photo.name}</bdi>
                </span>
              )}
            </div>
            <p id={photoIds.hint} className={styles.hint}>
              {t("photo.hint")}
              {gap}
              {t("ask.dataNotice")}
            </p>
            {photoMissing && (
              <p id={photoIds.error} className={styles.error}>
                {t("newModel.photo.required")}
              </p>
            )}
          </div>
        )}
        <TextField
          label={t("newModel.name")}
          value={name}
          onChange={(v) => {
            setTyped(v);
            if (missing && v.trim() !== "") setMissing(false);
          }}
          maxLength={200}
          autoComplete="off"
          isInvalid={missing}
          errorMessage={t("newModel.nameRequired")}
        />
        {problem && <ProblemView problem={problem.problem} headingLevel={3} guidance={problem.guidance} />}
        <div className={styles.actions}>
          <Button type="submit" variant="primary" icon={<PlusIcon />}>
            {t("newModel.create")}
          </Button>
          {/* Not while a create is in flight: a form opened again would send it once more under a new key. */}
          <Button onPress={onCancel} blocked={step !== null}>{t("newModel.cancel")}</Button>
        </div>
        <StatusRegion message={step === "creating" ? t("newModel.creating") : step === "sending" ? t("photo.sending") : null} />
      </form>
    </section>
  );
}
