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
import { type FormEvent, useId, useRef, useState } from "react";
import { Label, Radio, RadioGroup } from "react-aria-components";
import { ProblemView } from "../../app/_components/problem-view";
import { newIdempotencyKey } from "../../lib/api/client";
import type { Problem } from "../../lib/api/problem";
import { useApi } from "../../lib/api/provider";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { TextField } from "../../ui/field";
import { PlusIcon } from "../../ui/icons";
import { StatusRegion } from "../../ui/status";
import styles from "./new-model.module.css";
import { createRequest, domainName, FIRST_TEMPLATE, isTemplate, type Template, TEMPLATE_GROUPS, templateDescription, templateName } from "./templates";

export function NewModelForm({ projectId, onCreated, onCancel }: {
  projectId: string;
  /** The server created the model: open it. */
  onCreated: (modelId: string) => void;
  onCancel: () => void;
}) {
  const api = useApi();
  const { t, locale } = useI18n();
  const id = useId();
  const [template, setTemplate] = useState<Template>(FIRST_TEMPLATE);
  // null: the chosen template's name, in the reader's language.
  const [typed, setTyped] = useState<string | null>(null);
  const [missing, setMissing] = useState(false);
  const [problem, setProblem] = useState<Problem | null>(null);
  const [creating, setCreating] = useState(false);
  const busy = useRef(false);
  const sent = useRef<{ request: string; key: string } | null>(null);
  const name = typed ?? t(templateName(template));

  const create = async (e: FormEvent) => {
    e.preventDefault();
    if (busy.current) return;
    const trimmed = name.trim();
    if (trimmed === "") {
      setMissing(true);
      return;
    }
    setMissing(false);
    setProblem(null);
    const body = createRequest(template, trimmed, locale);
    // The same request again (a retry) takes the same key; another request a new one.
    const request = `${projectId} ${JSON.stringify(body)}`;
    if (sent.current?.request !== request) sent.current = { request, key: newIdempotencyKey() };
    busy.current = true;
    setCreating(true);
    const r = await api.createModel(projectId, body, sent.current.key);
    busy.current = false;
    setCreating(false);
    if (r.ok) onCreated(r.data.id);
    else setProblem(r.problem);
  };

  return (
    <section className={styles.panel} aria-labelledby={`${id}-heading`} data-new-model>
      <h2 id={`${id}-heading`}>{t("newModel.title")}</h2>
      <p className={styles.intro}>{t("newModel.intro")}</p>
      <form onSubmit={create} noValidate aria-labelledby={`${id}-heading`}>
        <RadioGroup value={template} onChange={(v) => isTemplate(v) && setTemplate(v)} className={styles.templates}>
          <Label className={styles.label}>{t("newModel.template")}</Label>
          {TEMPLATE_GROUPS.map((g) => (
            <div key={g.domain} role="group" aria-labelledby={`${id}-${g.domain}`} className={styles.group}>
              <h3 id={`${id}-${g.domain}`} className={styles.domain}>
                {t(domainName(g.domain))}
              </h3>
              <div className={styles.options}>
                {g.templates.map((tpl) => (
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
        {problem && <ProblemView problem={problem} headingLevel={3} />}
        <div className={styles.actions}>
          <Button type="submit" variant="primary" icon={<PlusIcon />}>
            {t("newModel.create")}
          </Button>
          {/* Not while a create is in flight: a form opened again would send it once more under a new key. */}
          <Button onPress={onCancel} blocked={creating}>{t("newModel.cancel")}</Button>
        </div>
        <StatusRegion message={creating ? t("newModel.creating") : null} />
      </form>
    </section>
  );
}
