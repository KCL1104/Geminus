"use client";
// A proposal's changes (T-031): one line per change, in human terms, read
// against the base version (changes.ts), then the exact JSON Patch and its
// hash one click away (H-027: what only a developer reads is disclosed).
// Every name and value is shown as text; nothing becomes a link or markup.
//
// The names and values are the model's and the patch's, so the AI's (review
// round 1, M1). Each is one data part of its line: its characters that
// show nothing or reorder text made visible (shown.ts), in its own <bdi>, so
// it cannot reorder the words around it; an entity that another one of its
// kind reads like, or that the patch adds, is named with its ID ("{name}
// ({id})", as the organization picker names organizations); and a change
// whose two sides would still read the same says so: the same value set
// again, or both values exactly (as ASCII JSON when even that reads alike).
import type { ReactNode } from "react";
import { useI18n } from "../../lib/i18n/provider";
import type { JsonPatch, ModelIR } from "../../lib/contracts";
import { nodeTypes, type NodeType } from "../model-editor/edits";
import { type Change, describeChanges, type Field, rawOf, type Ref, type Subject, type Value } from "./changes";
import { asciiJson, patchJson, readsAlike, visible } from "./shown";
import { data, flat, joined, type Rich, useRich, Words } from "./words";
import styles from "./proposal.module.css";

const params: Record<string, string[]> = { constant: ["value"], exponential: ["mean"], triangular: ["min", "mode", "max"], lognormal: ["mean", "cv"], weibull: ["shape", "scale"], empirical: [] };
const JSON_LIMIT = 200;

const sameJson = (a: unknown, b: unknown): boolean => {
  const canonical = (v: unknown): string => {
    if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
    if (v !== null && typeof v === "object") return `{${Object.keys(v).sort().map((k) => `${JSON.stringify(k)}:${canonical((v as Record<string, unknown>)[k])}`).join(",")}}`;
    return JSON.stringify(v) ?? "undefined";
  };
  return canonical(a) === canonical(b);
};

export function ChangeList({ base, version, changes, patchHash }: { base: ModelIR; version: number; changes: JsonPatch; patchHash: string | undefined }) {
  const { t, number, duration, locale } = useI18n();
  const rich = useRich();
  const described = describeChanges(base, changes);
  const unit = t(`editor.unit.${base.time_unit}`);

  /** An entity by its name, with its ID when the name does not tell it apart. */
  const ref = (r: Ref, quoted = false): Rich => {
    const name = quoted ? rich("proposal.value.text", { text: data(r.name) }) : data(r.name);
    return r.showId && r.name !== r.id ? rich("proposal.named", { name, id: data(r.id) }) : name;
  };
  const typeLabel = (type: string): Rich => ((nodeTypes as readonly string[]).includes(type) ? [t(`editor.type.${type as NodeType}`)] : data(String(type)));
  const list = (refs: Ref[]): Rich => {
    const parts = new Intl.ListFormat(locale, { type: "conjunction" }).formatToParts(refs.map((_, i) => String(i)));
    let i = 0;
    return joined(parts.flatMap((p) => (p.type === "element" ? ref(refs[i++]!) : [p.value])));
  };

  const element = (s: Subject): Rich => {
    switch (s.kind) {
      case "step":
        return rich("proposal.change.step", { name: ref(s.ref), type: typeLabel(s.type) });
      case "connection": {
        const line = rich("proposal.change.connection", { from: ref(s.from), to: ref(s.to) });
        return s.showId ? rich("proposal.named", { name: line, id: data(s.id) }) : line;
      }
      case "resource":
        return rich("proposal.change.resource", { name: ref(s.ref) });
      case "calendar":
        return rich("proposal.change.calendar", { name: ref(s.ref) });
      case "item":
        return rich("proposal.change.item", { name: ref(s.ref) });
      case "metric":
        return rich("proposal.change.metric", { id: data(s.id) });
      case "check":
        return rich("proposal.change.check", { name: ref(s.step) });
      case "model":
        return rich("proposal.change.model");
    }
  };
  /** A changed entity is named by its name alone; an added or removed one says what it is. */
  const named = (s: Subject): Rich => (s.kind === "step" || s.kind === "resource" || s.kind === "calendar" || s.kind === "item" ? ref(s.ref) : element(s));

  const fieldLabel = (f: Field): Rich => {
    switch (f.key) {
      case "time": {
        const what =
          f.of === "process" || f.of === "delay" || f.of === "source"
            ? rich(`editor.timing.${f.of}`)
            : f.of === "changeover"
              ? rich("proposal.field.changeover", { from: f.from ? ref(f.from) : "", to: f.to ? ref(f.to) : "" })
              : rich(`proposal.field.${f.of}`);
        return f.param === null ? what : rich("proposal.field.param", { field: what, param: t(`editor.param.${f.param}`) });
      }
      case "batch":
        return rich(f.which === null ? "editor.batch" : `editor.batch.${f.which}`);
      case "uses":
        return rich("proposal.field.uses", { resource: ref(f.resource) });
      case "shiftCapacity":
        return rich("proposal.field.shiftCapacity", { label: data(f.label) });
      case "releaseRate":
      case "releaseQuantity":
        return rich(`proposal.field.${f.key}`, { n: String(f.n) });
      case "path":
        return data(f.path);
      default:
        return rich(`proposal.field.${f.key}`);
    }
  };

  const json = (v: unknown) => {
    const s = JSON.stringify(v) ?? "";
    return s.length > JSON_LIMIT ? `${s.slice(0, JSON_LIMIT)}…` : s;
  };
  const value = (v: Value, noLimit = false): Rich => {
    switch (v.kind) {
      case "absent":
        return rich(noLimit ? "proposal.value.noLimit" : "proposal.value.absent");
      case "number":
        if (v.format === "percent") return [number(v.value, { style: "percent", maximumFractionDigits: 2 })];
        if (v.format === "time") return [duration(v.value, base.time_unit)];
        return [number(v.value)];
      case "text":
        return rich("proposal.value.text", { text: data(v.value) });
      case "ref":
        return ref(v.ref, true);
      case "distribution": {
        const d = v.value as unknown as Record<string, unknown>;
        const text =
          v.value.type === "empirical"
            ? t("editor.dist.empirical")
            : t("editor.dist.summary", { type: t(`editor.dist.${v.value.type}`), values: (params[v.value.type] ?? []).map((p) => (typeof d[p] === "number" ? number(d[p]) : "?")).join(", ") });
        return v.time ? rich("proposal.value.distribution", { distribution: text, unit }) : [text];
      }
      case "json":
        return data(json(v.value));
    }
  };
  // A step's capacity (a queue's buffer) and a batch's waiting limit that are not set mean no limit.
  const noLimit = (s: Subject | null, f: Field | null) => (s?.kind === "step" && f?.key === "capacity") || (f?.key === "batch" && f.which === "max_wait");

  /** A value's exact JSON: as it is, or in ASCII when even that reads like the other side's. */
  const exact = (v: Value, other: Value, limitless: boolean): Rich => {
    if (v.kind === "absent") return value(v, limitless);
    const shown = (x: Value) => (x.kind === "absent" ? "" : visible(JSON.stringify(rawOf(x)) ?? ""));
    return readsAlike(shown(v), shown(other)) ? data(asciiJson(rawOf(v))) : data(JSON.stringify(rawOf(v)) ?? "");
  };
  /** What a value changes from and to; said explicitly when the two would read the same. */
  const change = (label: Rich, subject: Subject | null, field: Field | null, from: Value, to: Value, key: "proposal.change.value" | "proposal.change.other"): Rich => {
    const limitless = noLimit(subject, field);
    const before = value(from, limitless);
    const after = value(to, limitless);
    const slot = key === "proposal.change.value" ? "field" : "path";
    if (!readsAlike(flat(before), flat(after))) return rich(key, { [slot]: label, before, after });
    if (sameJson(rawOf(from), rawOf(to))) return rich("proposal.change.same", { field: label, value: before });
    return rich("proposal.change.exact", { field: label, before: exact(from, to, limitless), after: exact(to, from, limitless) });
  };

  const line = (c: Change, i: number): ReactNode => {
    switch (c.kind) {
      case "added":
      case "removed":
        return (
          <li key={i} data-change={c.kind}>
            <span className={styles.tag} data-tag={c.kind}>
              {t(c.kind === "added" ? "proposal.change.added" : "proposal.change.removed")}
            </span>{" "}
            <Words parts={element(c.subject)} />
            {c.kind === "added" && c.placed && <> {t("proposal.change.placed")}</>}
            {c.kind === "added" && c.holds.length > 0 && (
              <ul className={styles.holds}>
                {c.holds.map((h, k) => (
                  <li key={k}>
                    <Words parts={rich("proposal.change.hold", { field: fieldLabel(h.field), value: value(h.value, noLimit(c.subject, h.field)) })} />
                  </li>
                ))}
              </ul>
            )}
          </li>
        );
      case "changed":
        return (
          <li key={i} data-change="changed">
            <strong>
              <Words parts={named(c.subject)} />
            </strong>{" "}
            <Words parts={change(fieldLabel(c.field), c.subject, c.field, c.before, c.after, "proposal.change.value")} />
          </li>
        );
      case "layout":
        return (
          <li key={i} data-change="layout">
            <Words parts={rich("proposal.change.layout", { names: list(c.steps) })} />
          </li>
        );
      case "other":
        return (
          <li key={i} data-change="other">
            <Words parts={change(data(c.path), null, null, c.before, c.after, "proposal.change.other")} />
          </li>
        );
    }
  };

  return (
    <>
      {described.ok ? <ul className={styles.changes}>{described.changes.map(line)}</ul> : <p>{t("proposal.changes.unreadable", { version: String(version) })}</p>}
      <details className={styles.patch} open={!described.ok}>
        <summary>{t("proposal.changes.patch")}</summary>
        <pre>{patchJson(changes)}</pre>
        {patchHash && (
          <p>
            {t("proposal.changes.hash")} <code>{patchHash}</code>
          </p>
        )}
      </details>
    </>
  );
}
