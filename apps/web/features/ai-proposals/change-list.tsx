"use client";
// A proposal's changes (T-031): one line per change, in human terms, read
// against the base version (changes.ts), then the exact JSON Patch and its
// hash one click away (H-027: what only a developer reads is disclosed).
// Every name and value is shown as text; nothing becomes a link or markup.
import { useI18n } from "../../lib/i18n/provider";
import type { JsonPatch, ModelIR } from "../../lib/contracts";
import { nodeTypes, type NodeType } from "../model-editor/edits";
import { type Change, describeChanges, type Field, type Subject, type Value } from "./changes";
import styles from "./proposal.module.css";

const params: Record<string, string[]> = { constant: ["value"], exponential: ["mean"], triangular: ["min", "mode", "max"], lognormal: ["mean", "cv"], weibull: ["shape", "scale"], empirical: [] };
const JSON_LIMIT = 200;

export function ChangeList({ base, version, changes, patchHash }: { base: ModelIR; version: number; changes: JsonPatch; patchHash: string | undefined }) {
  const { t, number, duration, locale } = useI18n();
  const described = describeChanges(base, changes);
  const unit = t(`editor.unit.${base.time_unit}`);

  const typeLabel = (type: string) => ((nodeTypes as readonly string[]).includes(type) ? t(`editor.type.${type as NodeType}`) : type);
  const list = (names: string[]) => new Intl.ListFormat(locale, { type: "conjunction" }).format(names);

  const element = (s: Subject): string => {
    switch (s.kind) {
      case "step":
        return t("proposal.change.step", { name: s.name, type: typeLabel(s.type) });
      case "connection":
        return t("proposal.change.connection", { from: s.from, to: s.to });
      case "resource":
        return t("proposal.change.resource", { name: s.name });
      case "calendar":
        return t("proposal.change.calendar", { name: s.name });
      case "item":
        return t("proposal.change.item", { name: s.name });
      case "metric":
        return t("proposal.change.metric", { id: s.id });
      case "check":
        return t("proposal.change.check", { name: s.step });
      case "model":
        return t("proposal.change.model");
    }
  };
  /** A changed entity is named by its name alone; an added or removed one says what it is. */
  const named = (s: Subject): string => (s.kind === "step" || s.kind === "resource" || s.kind === "calendar" || s.kind === "item" ? s.name : element(s));

  const fieldLabel = (f: Field): string => {
    switch (f.key) {
      case "time": {
        const what =
          f.of === "process" || f.of === "delay" || f.of === "source"
            ? t(`editor.timing.${f.of}`)
            : f.of === "changeover"
              ? t("proposal.field.changeover", { from: f.from ?? "", to: f.to ?? "" })
              : t(`proposal.field.${f.of}`);
        return f.param === null ? what : t("proposal.field.param", { field: what, param: t(`editor.param.${f.param}`) });
      }
      case "batch":
        return f.which === null ? t("editor.batch") : t(`editor.batch.${f.which}`);
      case "uses":
        return t("proposal.field.uses", { resource: f.resource });
      case "shiftCapacity":
        return t("proposal.field.shiftCapacity", { label: f.label });
      case "releaseRate":
      case "releaseQuantity":
        return t(`proposal.field.${f.key}`, { n: String(f.n) });
      case "path":
        return f.path;
      default:
        return t(`proposal.field.${f.key}`);
    }
  };

  const json = (v: unknown) => {
    const s = JSON.stringify(v) ?? "";
    return s.length > JSON_LIMIT ? `${s.slice(0, JSON_LIMIT)}…` : s;
  };
  const value = (v: Value, noLimit = false): string => {
    switch (v.kind) {
      case "absent":
        return t(noLimit ? "proposal.value.noLimit" : "proposal.value.absent");
      case "number":
        if (v.format === "percent") return number(v.value, { style: "percent", maximumFractionDigits: 2 });
        if (v.format === "time") return duration(v.value, base.time_unit);
        return number(v.value);
      case "text":
        return t("proposal.value.text", { text: v.value });
      case "distribution": {
        const d = v.value as unknown as Record<string, unknown>;
        const text =
          v.value.type === "empirical"
            ? t("editor.dist.empirical")
            : t("editor.dist.summary", { type: t(`editor.dist.${v.value.type}`), values: (params[v.value.type] ?? []).map((p) => (typeof d[p] === "number" ? number(d[p]) : "?")).join(", ") });
        return v.time ? t("proposal.value.distribution", { distribution: text, unit }) : text;
      }
      case "json":
        return json(v.value);
    }
  };
  // A step's capacity (a queue's buffer) and a batch's waiting limit that are not set mean no limit.
  const noLimit = (s: Subject, f: Field) => (s.kind === "step" && f.key === "capacity") || (f.key === "batch" && f.which === "max_wait");

  const line = (c: Change, i: number) => {
    switch (c.kind) {
      case "added":
      case "removed":
        return (
          <li key={i} data-change={c.kind}>
            <span className={styles.tag} data-tag={c.kind}>
              {t(c.kind === "added" ? "proposal.change.added" : "proposal.change.removed")}
            </span>{" "}
            {element(c.subject)}
            {c.kind === "added" && c.placed && <> {t("proposal.change.placed")}</>}
            {c.kind === "added" && c.holds.length > 0 && (
              <ul className={styles.holds}>
                {c.holds.map((h, k) => (
                  <li key={k}>{t("proposal.change.hold", { field: fieldLabel(h.field), value: value(h.value, noLimit(c.subject, h.field)) })}</li>
                ))}
              </ul>
            )}
          </li>
        );
      case "changed":
        return (
          <li key={i} data-change="changed">
            <strong>{named(c.subject)}</strong> {t("proposal.change.value", { field: fieldLabel(c.field), before: value(c.before, noLimit(c.subject, c.field)), after: value(c.after, noLimit(c.subject, c.field)) })}
          </li>
        );
      case "layout":
        return (
          <li key={i} data-change="layout">
            {t("proposal.change.layout", { names: list(c.steps) })}
          </li>
        );
      case "other":
        return (
          <li key={i} data-change="other">
            {t("proposal.change.other", { path: c.path, before: value(c.before), after: value(c.after) })}
          </li>
        );
    }
  };

  return (
    <>
      {described.ok ? <ul className={styles.changes}>{described.changes.map(line)}</ul> : <p>{t("proposal.changes.unreadable", { version: String(version) })}</p>}
      <details className={styles.patch} open={!described.ok}>
        <summary>{t("proposal.changes.patch")}</summary>
        <pre>{JSON.stringify(changes, null, 2)}</pre>
        {patchHash && (
          <p>
            {t("proposal.changes.hash")} <code>{patchHash}</code>
          </p>
        )}
      </details>
    </>
  );
}
