// A proposal's JSON Patch in human terms (T-031, hackathon slice): what each
// op adds, removes or changes, named by the model's own names. The patch is
// read op by op against the base version, as the server applies it
// (internal/model/patch): each op against the model as it is right before
// it, so an index names the entity it named then, and a test is a guard,
// not a change. Nothing here is a second model: the base is the server's
// version, the ops are the server's canonical changes, and the candidate is
// only how they read. Names and values come from the model and the patch,
// never from the AI's prose, and are shown as text. What cannot be read
// safely (an op that does not apply to the base, a prototype member) is
// not guessed at: the caller falls back to the exact JSON Patch.
//
// A name in the patch is the AI's (review round 1, M1), so a name alone
// tells an entity apart only when no other entity of its kind reads the
// same (shown.ts nameKey). Each entity a change names is a Ref: its ID, its
// name, and whether its ID goes beside the name, because another entity of
// its kind reads the same in the base, as the patch leaves the model or in
// between, or because the patch adds it. A connection has no name: it is
// told by its two ends, and by its ID when another connection has the same
// two ends.
//
// A step added with a canvas position (as a photo proposal, T-056, adds
// them) is one "added" line that says it is placed; positions of steps
// that were there before are one "layout" line.
import type { Distribution, JsonPatch, ModelIR } from "../../lib/contracts";
import type { NodeType } from "../model-editor/edits";
import { applyPatch, parsePointer } from "../model-editor/patch";
import { nameKey } from "./shown";

type Op = JsonPatch[number];

export type Param = "value" | "mean" | "min" | "mode" | "max" | "cv" | "shape" | "scale";

/** An entity as a change names it: its ID, its name (else its ID), and whether its ID goes beside the name. */
export type Ref = { id: string; name: string; showId: boolean };

/** How a value reads: a number (plain, a percentage, or in the model's time unit), text, a distribution, another entity, or JSON. */
export type Value =
  | { kind: "absent" }
  | { kind: "number"; value: number; format: "plain" | "percent" | "time" }
  | { kind: "text"; value: string }
  | { kind: "distribution"; value: Distribution; time: boolean }
  | { kind: "ref"; ref: Ref }
  | { kind: "json"; value: unknown };

/** What a change is about, by the name the model gives it. */
export type Subject =
  | { kind: "step"; ref: Ref; type: NodeType }
  | { kind: "connection"; id: string; from: Ref; to: Ref; showId: boolean }
  | { kind: "resource"; ref: Ref }
  | { kind: "calendar"; ref: Ref }
  | { kind: "item"; ref: Ref }
  | { kind: "metric"; id: string }
  | { kind: "check"; step: Ref }
  | { kind: "model" };

export type TimeOf = "process" | "delay" | "source" | "time_to_failure" | "time_to_repair" | "changeover" | "changeoverDefault";

/** Which value of the subject changes. */
export type Field =
  | { key: "name" | "capacity" | "probability" | "from" | "to" | "requires" | "usesResource" | "outcome" | "reason" | "entity" | "calendar" | "shifts" | "exceptions" | "breakdowns" | "whole" }
  | { key: "time"; of: TimeOf; param: Param | null; from?: Ref; to?: Ref }
  | { key: "batch"; which: "min_size" | "max_size" | "max_wait" | null }
  | { key: "uses"; resource: Ref }
  | { key: "shiftCapacity"; label: string }
  | { key: "releaseRate" | "releaseQuantity"; n: number }
  | { key: "path"; path: string };

/** One value an added entity holds, as its field reads it. */
export type Holds = { field: Field; value: Value };

export type Change =
  /** placed: a new step with its canvas position; holds: what the new entity holds (a step's own time, the resources it uses, its batch or capacity; a resource's capacity; a decision's probability). */
  | { kind: "added"; subject: Subject; placed: boolean; holds: Holds[] }
  | { kind: "removed"; subject: Subject }
  | { kind: "changed"; subject: Subject; field: Field; before: Value; after: Value }
  | { kind: "layout"; steps: Ref[] }
  | { kind: "other"; path: string; before: Value; after: Value };

export type Described = { ok: true; changes: Change[] } | { ok: false };

const UNSAFE = new Set(["__proto__", "constructor", "prototype"]);
const INDEX = /^(0|[1-9][0-9]*)$/;
const DISTRIBUTIONS = new Set(["constant", "exponential", "triangular", "empirical", "lognormal", "weibull"]);
const UNITLESS = new Set<Param>(["cv", "shape"]);
const PARAMS = new Set<string>(["value", "mean", "min", "mode", "max", "cv", "shape", "scale"]);

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

function valueAt(doc: unknown, tokens: readonly string[]): unknown {
  let at: unknown = doc;
  for (const t of tokens) {
    if (Array.isArray(at)) at = INDEX.test(t) ? at[Number(t)] : undefined;
    else if (isRecord(at)) at = Object.hasOwn(at, t) ? at[t] : undefined;
    else return undefined;
  }
  return at;
}

const isDistribution = (v: unknown): v is Distribution => isRecord(v) && typeof v.type === "string" && DISTRIBUTIONS.has(v.type);

/** The JSON value a Value reads (an entity's is its ID). */
export function rawOf(v: Value): unknown {
  switch (v.kind) {
    case "absent":
      return undefined;
    case "ref":
      return v.ref.id;
    default:
      return v.value;
  }
}

/** A value as it reads, given how its field reads numbers. */
function read(v: unknown, format: "plain" | "percent" | "time" = "plain"): Value {
  if (v === undefined) return { kind: "absent" };
  if (typeof v === "number") return { kind: "number", value: v, format };
  if (typeof v === "string") return { kind: "text", value: v };
  if (isDistribution(v)) return { kind: "distribution", value: v, time: format === "time" };
  return { kind: "json", value: v };
}

/** The catalogs whose entities have names. */
type Named = "nodes" | "resources" | "calendars" | "entity_types";
const NAMED: readonly Named[] = ["nodes", "resources", "calendars", "entity_types"];

const idOf = (e: unknown): string | null => (isRecord(e) && typeof e.id === "string" ? e.id : null);
/** An entity's name as the model gives it, else its ID. */
const nameOf = (e: Record<string, unknown>, id: string) => (typeof e.name === "string" ? e.name : id);
const entities = (d: ModelIR, catalog: string): unknown[] => {
  const list = (d as unknown as Record<string, unknown>)[catalog];
  return Array.isArray(list) ? list : [];
};
const add = (m: Map<string, Set<string>>, key: string, id: string) => m.set(key, (m.get(key) ?? new Set()).add(id));

/**
 * Which IDs a change shows beside a name, over every state of the model the
 * patch passes through (the base, then after each op): an entity whose name
 * reads the same as another one's of its kind, one the patch adds, and a
 * connection whose two ends another connection has too.
 */
function shownIds(docs: readonly ModelIR[]) {
  const shown = new Set<string>();
  const shared = (catalog: string, groups: Map<string, Set<string>>) => {
    for (const ids of groups.values()) if (ids.size > 1) for (const id of ids) shown.add(`${catalog} ${id}`);
  };
  for (const catalog of NAMED) {
    const before = new Set(entities(docs[0]!, catalog).map(idOf));
    const byName = new Map<string, Set<string>>();
    for (const d of docs) {
      for (const e of entities(d, catalog)) {
        const id = idOf(e);
        if (id === null) continue;
        if (!before.has(id)) shown.add(`${catalog} ${id}`);
        add(byName, nameKey(nameOf(e as Record<string, unknown>, id)), id);
      }
    }
    shared(catalog, byName);
  }
  const byEnds = new Map<string, Set<string>>();
  for (const d of docs) {
    for (const f of entities(d, "flows")) {
      const id = idOf(f);
      if (id !== null) add(byEnds, JSON.stringify([(f as Record<string, unknown>).from, (f as Record<string, unknown>).to]), id);
    }
  }
  shared("flows", byEnds);
  return (catalog: Named | "flows", id: string) => shown.has(`${catalog} ${id}`);
}
type ShownIds = ReturnType<typeof shownIds>;

/** Entities by ID, looked up first in the model as it is now, then as the patch leaves it, then as the base had it. */
function namer(shown: ShownIds, ...docs: ModelIR[]) {
  const ref = (catalog: Named, id: string): Ref => {
    for (const d of docs) {
      const e = entities(d, catalog).find((x) => idOf(x) === id);
      if (e !== undefined) return { id, name: nameOf(e as Record<string, unknown>, id), showId: shown(catalog, id) };
    }
    return { id, name: id, showId: shown(catalog, id) };
  };
  return {
    shown,
    /** An element as the op's model or value has it. */
    of: (catalog: Named, element: Record<string, unknown>): Ref => {
      const id = typeof element.id === "string" ? element.id : "";
      return { id, name: nameOf(element, id), showId: shown(catalog, id) };
    },
    step: (id: string) => ref("nodes", id),
    type: (id: string): string | undefined => {
      for (const d of docs) {
        const n = d.nodes.find((x) => idOf(x) === id);
        if (n !== undefined) return n.type;
      }
      return undefined;
    },
    resource: (id: string) => ref("resources", id),
    entity: (id: string) => ref("entity_types", id),
    calendar: (id: string) => ref("calendars", id),
  };
}
type Namer = ReturnType<typeof namer>;

function subjectOf(catalog: string, element: unknown, names: Namer): Subject | null {
  if (!isRecord(element)) return null;
  const id = typeof element.id === "string" ? element.id : "";
  switch (catalog) {
    case "nodes":
      return { kind: "step", ref: names.of("nodes", element), type: element.type as NodeType };
    case "flows":
      return { kind: "connection", id, from: names.step(String(element.from)), to: names.step(String(element.to)), showId: names.shown("flows", id) };
    case "resources":
      return { kind: "resource", ref: names.of("resources", element) };
    case "calendars":
      return { kind: "calendar", ref: names.of("calendars", element) };
    case "entity_types":
      return { kind: "item", ref: names.of("entity_types", element) };
    case "metrics":
      return { kind: "metric", id };
    case "constraints":
      return { kind: "check", step: names.step(String(element.node_id)) };
    default:
      return null;
  }
}

const timeTokens: Record<string, TimeOf> = { service_time: "process", delay_time: "delay", arrival: "source" };

/** The field a path below one element names, and how its numbers read; null when it has no words here. */
function fieldOf(catalog: string, rest: readonly string[], element: unknown, names: Namer): { field: Field; format: "plain" | "percent" | "time" } | null {
  const [a, b, c, d, e] = rest;
  const el = isRecord(element) ? element : {};
  const param = (p: string | undefined): Param | null | undefined => (p === undefined ? null : PARAMS.has(p) ? (p as Param) : undefined);
  const timeField = (of: TimeOf, p: string | undefined, extra: { from?: Ref; to?: Ref } = {}) => {
    const pr = param(p);
    if (pr === undefined) return null;
    return { field: { key: "time" as const, of, param: pr, ...extra }, format: pr !== null && UNITLESS.has(pr) ? ("plain" as const) : ("time" as const) };
  };
  if (rest.length === 1 && a === "name") return { field: { key: "name" }, format: "plain" };
  if (catalog === "nodes") {
    if (rest.length === 1 && a === "capacity") return { field: { key: "capacity" }, format: "plain" };
    if (rest.length === 1 && (a === "outcome" || a === "reason")) return { field: { key: a }, format: "plain" };
    if (rest.length === 1 && a === "entity_type") return { field: { key: "entity" }, format: "plain" };
    if (a !== undefined && Object.hasOwn(timeTokens, a) && b === "distribution" && rest.length <= 3) return timeField(timeTokens[a]!, c);
    if (a === "arrival" && b === "schedule" && c === "segments" && d !== undefined && INDEX.test(d) && e === "rate" && rest.length === 5) return { field: { key: "releaseRate", n: Number(d) + 1 }, format: "plain" };
    if (a === "arrival" && b === "schedule" && c === "releases" && d !== undefined && INDEX.test(d) && e === "quantity" && rest.length === 5) return { field: { key: "releaseQuantity", n: Number(d) + 1 }, format: "plain" };
    if (a === "batch" && rest.length === 1) return { field: { key: "batch", which: null }, format: "plain" };
    if (a === "batch" && (b === "min_size" || b === "max_size") && rest.length === 2) return { field: { key: "batch", which: b }, format: "plain" };
    if (a === "batch" && b === "max_wait" && rest.length === 2) return { field: { key: "batch", which: "max_wait" }, format: "time" };
    if (a === "requires" && rest.length === 1) return { field: { key: "requires" }, format: "plain" };
    if (a === "requires" && b !== undefined && INDEX.test(b) && rest.length === 3 && (c === "units" || c === "resource_id")) {
      const req = (Array.isArray(el.requires) ? el.requires : [])[Number(b)];
      const resource = names.resource(isRecord(req) && typeof req.resource_id === "string" ? req.resource_id : "");
      return c === "units" ? { field: { key: "uses", resource }, format: "plain" } : { field: { key: "usesResource" }, format: "plain" };
    }
  }
  if (catalog === "flows") {
    if (a === "routing" && b === "probability" && rest.length === 2) return { field: { key: "probability" }, format: "percent" };
    if ((a === "from" || a === "to") && rest.length === 1) return { field: { key: a }, format: "plain" };
  }
  if (catalog === "resources") {
    if (rest.length === 1 && a === "capacity") return { field: { key: "capacity" }, format: "plain" };
    if (rest.length === 1 && a === "calendar_id") return { field: { key: "calendar" }, format: "plain" };
    if (a === "shift_capacity" && b !== undefined && rest.length === 2) return { field: { key: "shiftCapacity", label: b }, format: "plain" };
    if (a === "breakdowns" && rest.length === 1) return { field: { key: "breakdowns" }, format: "plain" };
    if (a === "breakdowns" && (b === "time_to_failure" || b === "time_to_repair") && c === "distribution" && rest.length <= 4) return timeField(b, d);
    if (a === "setups" && b === "default" && rest.length <= 3) return timeField("changeoverDefault", c);
    if (a === "setups" && b === "transitions" && c !== undefined && INDEX.test(c) && d === "distribution" && rest.length <= 5) {
      const setups = isRecord(el.setups) ? el.setups : {};
      const tr = (Array.isArray(setups.transitions) ? setups.transitions : [])[Number(c)];
      if (!isRecord(tr)) return null;
      return timeField("changeover", e, { from: names.entity(String(tr.from)), to: names.entity(String(tr.to)) });
    }
  }
  if (catalog === "calendars" && rest.length === 1 && (a === "shifts" || a === "exceptions")) return { field: { key: a }, format: "plain" };
  return null;
}

/** A value of a field, read: one that names another entity is that entity. */
function readField(field: Field, v: unknown, format: "plain" | "percent" | "time", names: Namer): Value {
  if (typeof v === "string") {
    switch (field.key) {
      case "from":
      case "to":
        return { kind: "ref", ref: names.step(v) };
      case "usesResource":
        return { kind: "ref", ref: names.resource(v) };
      case "calendar":
        return { kind: "ref", ref: names.calendar(v) };
      case "entity":
        return { kind: "ref", ref: names.entity(v) };
    }
  }
  return read(v, format);
}

const CATALOGS = new Set(["nodes", "flows", "resources", "calendars", "entity_types", "metrics", "constraints"]);

/** What a new step, resource or connection holds, read as the fields of a change read. */
function holdsOf(catalog: string, element: unknown, names: Namer): Holds[] {
  if (!isRecord(element)) return [];
  const out: Holds[] = [];
  if (catalog === "nodes") {
    for (const [token, of] of Object.entries(timeTokens)) {
      const timing = element[token];
      if (isRecord(timing) && timing.distribution !== undefined) out.push({ field: { key: "time", of, param: null }, value: read(timing.distribution, "time") });
    }
    if (element.type === "queue") out.push({ field: { key: "capacity" }, value: read(element.capacity) });
    for (const req of Array.isArray(element.requires) ? element.requires : []) {
      if (isRecord(req) && typeof req.resource_id === "string") out.push({ field: { key: "uses", resource: names.resource(req.resource_id) }, value: read(req.units) });
    }
    if (isRecord(element.batch)) {
      for (const which of ["min_size", "max_size", "max_wait"] as const) {
        if (element.batch[which] !== undefined) out.push({ field: { key: "batch", which }, value: read(element.batch[which], which === "max_wait" ? "time" : "plain") });
      }
    }
  }
  if (catalog === "resources") out.push({ field: { key: "capacity" }, value: read(element.capacity) });
  if (catalog === "flows" && names.type(String(element.from)) === "decision" && isRecord(element.routing)) {
    out.push({ field: { key: "probability" }, value: read(element.routing.probability, "percent") });
  }
  return out;
}

/**
 * The changes a JSON Patch makes to base, in order, without its tests; or
 * not ok when it cannot be read safely (the caller then shows the exact
 * patch only).
 */
export function describeChanges(base: ModelIR, changes: JsonPatch): Described {
  const parsed: string[][] = [];
  for (const op of changes) {
    const tokens = parsePointer(op.path);
    if (tokens === null || tokens.length === 0 || tokens.some((t) => UNSAFE.has(t))) return { ok: false };
    parsed.push(tokens);
  }
  // The model before each op, and as the patch leaves it (the last).
  const docs: ModelIR[] = [base];
  for (const op of changes) {
    const next = applyPatch(docs[docs.length - 1]!, [op]);
    if (!next.ok) return { ok: false };
    docs.push(next.doc);
  }
  const final = docs[docs.length - 1]!;
  const shown = shownIds(docs);

  const out: Change[] = [];
  const added = new Map<string, Extract<Change, { kind: "added" }>>();
  const moved: string[] = [];
  let layoutAt = -1;
  for (const [i, op] of changes.entries()) {
    const doc = docs[i]!;
    if (op.op !== "test") describeOp(op, parsed[i]!, doc, namer(shown, doc, final, base), out, added, moved, () => {
      if (layoutAt < 0) layoutAt = out.length;
    });
  }
  if (moved.length > 0) {
    const finalNames = namer(shown, final, base);
    out.splice(layoutAt, 0, { kind: "layout", steps: [...new Set(moved)].map((id) => finalNames.step(id)) });
  }
  return { ok: true, changes: out };
}

function describeOp(
  op: Exclude<Op, { op: "test" }>,
  tokens: readonly string[],
  doc: ModelIR,
  names: Namer,
  out: Change[],
  added: Map<string, Extract<Change, { kind: "added" }>>,
  moved: string[],
  markLayout: () => void,
): void {
  const before = valueAt(doc, tokens);
  const after = op.op === "remove" ? undefined : op.value;
  const [catalog, index] = tokens;

  // A canvas position: part of a step's addition, or one layout line for the steps that were there.
  if (catalog === "views" && tokens[1] === "2d" && tokens[2] === "positions" && tokens.length <= 4) {
    const ids = tokens.length === 4 ? [tokens[3]!] : Object.keys(isRecord(after) ? after : isRecord(before) ? before : {});
    for (const id of ids) {
      const step = added.get(id);
      if (step !== undefined && op.op !== "remove") step.placed = true;
      else if (step === undefined && doc.nodes.some((n) => n.id === id)) {
        markLayout();
        moved.push(id);
      }
    }
    return;
  }

  if (catalog !== undefined && CATALOGS.has(catalog) && index !== undefined) {
    const list = (doc as unknown as Record<string, unknown>)[catalog];
    const items = Array.isArray(list) ? list : [];
    if (tokens.length === 2) {
      if (op.op === "add") {
        const subject = subjectOf(catalog, after, names);
        if (subject === null) return void out.push({ kind: "other", path: op.path, before: read(before), after: read(after) });
        const change = { kind: "added" as const, subject, placed: false, holds: holdsOf(catalog, after, names) };
        if (subject.kind === "step") added.set(subject.ref.id, change);
        out.push(change);
        return;
      }
      const subject = subjectOf(catalog, before, names);
      if (subject === null) return void out.push({ kind: "other", path: op.path, before: read(before), after: read(after) });
      if (op.op === "remove") out.push({ kind: "removed", subject });
      else out.push({ kind: "changed", subject, field: { key: "whole" }, before: read(before), after: read(after) });
      return;
    }
    const element = INDEX.test(index) ? items[Number(index)] : undefined;
    const subject = subjectOf(catalog, element, names);
    if (subject !== null) {
      const rest = tokens.slice(2);
      const f = fieldOf(catalog, rest, element, names);
      if (f === null) {
        out.push({ kind: "changed", subject, field: { key: "path", path: "/" + rest.map((t) => t.replace(/~/g, "~0").replace(/\//g, "~1")).join("/") }, before: read(before), after: read(after) });
        return;
      }
      out.push({ kind: "changed", subject, field: f.field, before: readField(f.field, before, f.format, names), after: readField(f.field, after, f.format, names) });
      return;
    }
  }

  if (tokens.length === 1 && catalog === "name") {
    out.push({ kind: "changed", subject: { kind: "model" }, field: { key: "name" }, before: read(before), after: read(after) });
    return;
  }
  out.push({ kind: "other", path: op.path, before: read(before), after: read(after) });
}
