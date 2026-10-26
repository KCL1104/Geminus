"use client";
// The selected step's properties (H-027 Q2: the prototype's properties
// panel with the server's field errors). Editable in this slice: the name,
// a queue's capacity, the numbers of a step's own time distribution, a
// batch's sizes and waiting limit, a decision's probabilities, and the
// step's connections. Shown read-only: ID and type, the distribution's
// type, a release schedule, the resources a process needs with their
// breakdowns, shift calendar and changeovers, a sink's outcome and a
// failure's reason. Each server issue is shown at the field its JSON
// Pointer names, and what no field takes at the top of the panel.
import { type RefObject, useId } from "react";
import type { Distribution, ModelIR } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Button } from "../../ui/button";
import { ActionMenu } from "../../ui/menu";
import { type EditResult, setBatch, setProbability, setQueueCapacity, setTiming, stepLabel, timingOf } from "./edits";
import { IssueText, NameField, NumberField } from "./fields";
import { claim, type StepIssue } from "./issues";
import styles from "./editor.module.css";

type IRNode = ModelIR["nodes"][number];

type Param = "value" | "mean" | "min" | "mode" | "max" | "cv" | "shape" | "scale";
const params: Record<Distribution["type"], Param[]> = {
  constant: ["value"],
  exponential: ["mean"],
  triangular: ["min", "mode", "max"],
  lognormal: ["mean", "cv"],
  weibull: ["shape", "scale"],
  empirical: [],
};
function paramOf(d: Distribution, p: Param): number | undefined {
  const v = (d as unknown as Record<string, unknown>)[p];
  return typeof v === "number" ? v : undefined;
}

/** Parameters without a time unit. */
const unitless = new Set<Param>(["cv", "shape"]);

export function Properties({ ir, node, issues, editable, autoFocusName, connectRef, onEdit, onRename, onConnect, onDisconnect, onDelete }: {
  ir: ModelIR;
  node: IRNode | null;
  /** The issues located on this step (or on its flows and resources). */
  issues: readonly StepIssue[];
  editable: boolean;
  autoFocusName: boolean;
  connectRef: RefObject<HTMLDivElement | null>;
  onEdit: (r: EditResult) => void;
  onRename: (name: string) => void;
  onConnect: (to: string) => void;
  onDisconnect: (flowId: string) => void;
  onDelete: () => void;
}) {
  const { t, number, gap } = useI18n();
  const headingId = useId();
  if (node === null) {
    return (
      <section aria-labelledby={headingId} className={styles.panel} data-panel="properties">
        <h2 id={headingId}>{t("editor.properties")}</h2>
        <p className={styles.hint}>{t("editor.properties.none")}</p>
      </section>
    );
  }

  const unit = t(`editor.unit.${ir.time_unit}`);
  const inUnit = t("editor.inUnit", { unit });
  const own = issues.filter((i) => i.target?.kind === "node" && i.target.id === node.id);
  const timing = timingOf(node);
  const fields = ["/name", "/capacity", "/entity_type", "/outcome", "/reason", "/arrival/schedule", "/batch", "/batch/min_size", "/batch/max_size", "/batch/max_wait", "/requires"];
  if (timing) {
    const base = "/" + timing.tokens.join("/");
    fields.push(base, ...params[timing.distribution.type].map((p) => `${base}/${p}`));
  }
  const { byField, rest } = claim(own, fields);
  const at = (f: string) => byField.get(f) ?? [];
  const label = (id: string) => {
    const n = ir.nodes.find((x) => x.id === id);
    return n ? stepLabel(n) : id;
  };
  const describeDist = (d: Distribution) => {
    const values = params[d.type].map((p) => number(paramOf(d, p) ?? Number.NaN)).join(", ");
    return d.type === "empirical" ? t("editor.dist.empirical") : t("editor.dist.summary", { type: t(`editor.dist.${d.type}`), values });
  };
  const outgoing = ir.flows.filter((f) => f.from === node.id);
  const targets = node.type === "sink" ? [] : ir.nodes.filter((n) => n.id !== node.id && n.type !== "source" && !outgoing.some((f) => f.to === n.id));
  const readOnly = !editable;

  return (
    <section aria-labelledby={headingId} className={styles.panel} data-panel="properties">
      <h2 id={headingId}>{stepLabel(node)}</h2>
      <dl className={styles.facts}>
        <dt>{t("editor.field.id")}</dt>
        <dd>
          <code>{node.id}</code>
        </dd>
        <dt>{t("editor.field.type")}</dt>
        <dd>{t(`editor.type.${node.type}`)}</dd>
      </dl>
      {rest.length > 0 && (
        <div className={styles.stepProblems}>
          <h3>{t("editor.stepProblems")}</h3>
          <p className={styles.error}>
            <IssueText issues={rest} />
          </p>
        </div>
      )}
      <NameField key={`name:${node.id}`} value={node.name ?? ""} issues={at("/name")} readOnly={readOnly} autoFocus={autoFocusName} onCommit={onRename} />

      {node.type === "queue" && (
        <NumberField
          label={t("editor.field.capacity")}
          value={node.capacity}
          whole
          optional
          description={t("editor.field.capacityHint")}
          issues={at("/capacity")}
          readOnly={readOnly}
          onCommit={(v) => onEdit(setQueueCapacity(ir, node.id, v))}
        />
      )}

      {node.type === "source" && (
        <dl className={styles.facts}>
          <dt>{t("editor.field.entity")}</dt>
          <dd>{ir.entity_types.find((e) => e.id === node.entity_type)?.name ?? node.entity_type}</dd>
        </dl>
      )}

      {timing && (
        <fieldset className={styles.group}>
          <legend>{t(`editor.timing.${node.type as "process" | "delay" | "source"}`)}</legend>
          <p className={styles.hint}>{t(`editor.dist.${timing.distribution.type}`)}</p>
          {at("/" + timing.tokens.join("/")).length > 0 && (
            <p className={styles.error}>
              <IssueText issues={at("/" + timing.tokens.join("/"))} />
            </p>
          )}
          {timing.distribution.type === "empirical" ? (
            <p>
              {t("editor.param.samples_ref")}
              {gap}
              <code>{timing.distribution.samples_ref}</code>
            </p>
          ) : (
            params[timing.distribution.type].map((p) => (
              <NumberField
                key={`${node.id}:${p}`}
                label={t(`editor.param.${p}`)}
                value={paramOf(timing.distribution, p)}
                description={unitless.has(p) ? undefined : inUnit}
                issues={at(`/${timing.tokens.join("/")}/${p}`)}
                readOnly={readOnly}
                onCommit={(v) => v !== null && onEdit(setTiming(ir, node.id, p, v))}
              />
            ))
          )}
        </fieldset>
      )}

      {node.type === "source" && node.arrival.schedule && (
        <fieldset className={styles.group}>
          <legend>{t("editor.schedule")}</legend>
          <p>{t(`editor.schedule.${node.arrival.schedule.process}`, { period: number(node.arrival.schedule.period), unit })}</p>
          {at("/arrival/schedule").length > 0 && (
            <p className={styles.error}>
              <IssueText issues={at("/arrival/schedule")} />
            </p>
          )}
        </fieldset>
      )}

      {node.type === "process" && node.batch && (
        <fieldset className={styles.group}>
          <legend>{t("editor.batch")}</legend>
          {at("/batch").length > 0 && (
            <p className={styles.error}>
              <IssueText issues={at("/batch")} />
            </p>
          )}
          {(["min_size", "max_size"] as const).map((f) => (
            <NumberField key={`${node.id}:${f}`} label={t(`editor.batch.${f}`)} value={node.batch![f]} whole issues={at(`/batch/${f}`)} readOnly={readOnly} onCommit={(v) => v !== null && onEdit(setBatch(ir, node.id, f, v))} />
          ))}
          <NumberField
            key={`${node.id}:max_wait`}
            label={t("editor.batch.max_wait")}
            value={node.batch.max_wait}
            optional
            description={t("editor.batch.maxWaitHint", { unit })}
            issues={at("/batch/max_wait")}
            readOnly={readOnly}
            onCommit={(v) => onEdit(setBatch(ir, node.id, "max_wait", v))}
          />
        </fieldset>
      )}

      {node.type === "process" && node.requires.length > 0 && (
        <section className={styles.group} aria-labelledby={`${headingId}-res`}>
          <h3 id={`${headingId}-res`}>{t("editor.resources")}</h3>
          {at("/requires").length > 0 && (
            <p className={styles.error}>
              <IssueText issues={at("/requires")} />
            </p>
          )}
          <ul className={styles.rows}>
            {node.requires.map((req) => {
              const r = ir.resources.find((x) => x.id === req.resource_id);
              const resourceIssues = issues.filter((i) => i.target?.kind === "resource" && i.target.id === req.resource_id);
              const calendar = r?.calendar_id === undefined ? undefined : (ir.calendars ?? []).find((c) => c.id === r.calendar_id);
              const entity = (id: string) => ir.entity_types.find((e) => e.id === id)?.name ?? id;
              return (
                <li key={req.resource_id}>
                  <span>{t("editor.resource.use", { name: r?.name ?? req.resource_id, units: number(req.units), capacity: r ? number(r.capacity) : "?" })}</span>
                  {r?.breakdowns && <span className={styles.tag}>{t("editor.resource.breakdowns")}</span>}
                  {calendar && <span className={styles.tag}>{t("editor.resource.calendar", { calendar: calendar.name ?? calendar.id })}</span>}
                  {r?.setups && (
                    <div className={styles.sub}>
                      <span>{t("editor.resource.changeovers")}</span>
                      <ul>
                        {(r.setups.transitions ?? []).map((tr, k) => (
                          <li key={k}>{t("editor.resource.changeover", { from: entity(tr.from), to: entity(tr.to), time: describeDist(tr.distribution) })}</li>
                        ))}
                        {r.setups.default && <li>{t("editor.resource.changeoverDefault", { time: describeDist(r.setups.default) })}</li>}
                      </ul>
                    </div>
                  )}
                  {resourceIssues.length > 0 && (
                    <p className={styles.error}>
                      <IssueText issues={resourceIssues} />
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {node.type === "sink" && (
        <dl className={styles.facts}>
          <dt>{t("editor.outcome")}</dt>
          <dd>{t(node.outcome === "abandoned" ? "editor.outcome.abandoned" : "editor.outcome.completed")}</dd>
        </dl>
      )}
      {node.type === "failure" && node.reason && (
        <dl className={styles.facts}>
          <dt>{t("editor.reason")}</dt>
          <dd>{node.reason}</dd>
        </dl>
      )}

      {node.type !== "sink" && (
        <section className={styles.group} aria-labelledby={`${headingId}-next`}>
          <h3 id={`${headingId}-next`}>{t("editor.next")}</h3>
          {outgoing.length === 0 ? (
            <p className={styles.hint}>{t("editor.next.none")}</p>
          ) : (
            <ul className={styles.rows}>
              {outgoing.map((f) => {
                const flowIssues = issues.filter((i) => i.target?.kind === "flow" && i.target.id === f.id);
                const to = label(f.to);
                return (
                  <li key={f.id} className={styles.connection}>
                    {node.type === "decision" ? (
                      <NumberField
                        label={t("editor.next.probability", { to })}
                        value={f.routing.probability}
                        issues={flowIssues}
                        readOnly={readOnly}
                        onCommit={(v) => v !== null && onEdit(setProbability(ir, f.id, v))}
                      />
                    ) : (
                      <span className={styles.target}>{to}</span>
                    )}
                    {node.type !== "decision" && flowIssues.length > 0 && (
                      <p className={styles.error}>
                        <IssueText issues={flowIssues} />
                      </p>
                    )}
                    {editable && (
                      <Button variant="quiet" aria-label={t("editor.next.removeLabel", { to })} onPress={() => onDisconnect(f.id)}>
                        {t("editor.next.remove")}
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {editable && targets.length > 0 && (
            <div ref={connectRef}>
              <ActionMenu label={t("editor.connect")} items={targets.map((n) => ({ id: n.id, label: stepLabel(n) }))} onAction={onConnect} />
            </div>
          )}
        </section>
      )}

      {editable && (
        <div className={styles.danger}>
          <Button variant="danger" onPress={onDelete}>
            {t("editor.delete")}
          </Button>
        </div>
      )}
    </section>
  );
}
