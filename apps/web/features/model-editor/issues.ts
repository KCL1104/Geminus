// Where the server's issues belong (D-023: a code, a JSON Pointer path and a
// severity). A path points into the IR it was found in: a saved draft's
// issues into that draft, a refused save's details into the candidate the
// server built. It is resolved there, once, to the entity it is under and
// what is left of the path, so later local edits (which shift indices)
// cannot move an issue to another step. The editor shows each at the
// field it names, and the issue list goes to its step.
import type { ModelIR, ValidationIssue } from "../../lib/contracts";
import { parsePointer, pointer } from "./patch";

export type Target = { kind: "node" | "flow" | "resource"; id: string } | null;
export type Located = { target: Target; suffix: string };

const INDEX = /^(0|[1-9][0-9]*)$/;

function at<T>(list: readonly T[], token: string | undefined): T | undefined {
  return token !== undefined && INDEX.test(token) ? list[Number(token)] : undefined;
}

/** The entity a path is under, and the rest of the path below it ("" for the entity itself). */
export function locate(ir: ModelIR, path: string): Located {
  const t = parsePointer(path);
  const none: Located = { target: null, suffix: path };
  if (t === null || t.length < 2) return none;
  const rest = pointer(...t.slice(2));
  switch (t[0]) {
    case "nodes": {
      const n = at(ir.nodes, t[1]);
      return n ? { target: { kind: "node", id: n.id }, suffix: rest } : none;
    }
    case "flows": {
      const f = at(ir.flows, t[1]);
      return f ? { target: { kind: "flow", id: f.id }, suffix: rest } : none;
    }
    case "resources": {
      const r = at(ir.resources, t[1]);
      return r ? { target: { kind: "resource", id: r.id }, suffix: rest } : none;
    }
    case "constraints": {
      const c = at(ir.constraints, t[1]);
      return c && ir.nodes.some((n) => n.id === c.node_id) ? { target: { kind: "node", id: c.node_id }, suffix: "" } : none;
    }
    case "metrics": {
      const m = at(ir.metrics, t[1]);
      if (m === undefined) return none;
      const node = "node_id" in m ? m.node_id : "sink_id" in m ? m.sink_id : undefined;
      if (node !== undefined && ir.nodes.some((n) => n.id === node)) return { target: { kind: "node", id: node }, suffix: "" };
      if ("flow_id" in m && ir.flows.some((f) => f.id === m.flow_id)) return { target: { kind: "flow", id: m.flow_id }, suffix: "" };
      if ("resource_id" in m && ir.resources.some((r) => r.id === m.resource_id)) return { target: { kind: "resource", id: m.resource_id }, suffix: "" };
      return none;
    }
    case "views": {
      const id = t[1] === "2d" && t[2] === "positions" ? t[3] : undefined;
      return id !== undefined && ir.nodes.some((n) => n.id === id) ? { target: { kind: "node", id }, suffix: "" } : none;
    }
    default:
      return none;
  }
}

/** The step the editor goes to for a target: the node, a flow's start, the first step that needs a resource. */
export function stepOf(ir: ModelIR, target: Target): string | null {
  if (target === null) return null;
  switch (target.kind) {
    case "node":
      return target.id;
    case "flow":
      return ir.flows.find((f) => f.id === target.id)?.from ?? null;
    case "resource":
      return ir.nodes.find((n) => n.type === "process" && n.requires.some((r) => r.resource_id === target.id))?.id ?? null;
  }
}

export type StepIssue = ValidationIssue & Located & { step: string | null };

/** The issues, each resolved against the IR it was found in. */
export function locateIssues(ir: ModelIR, issues: readonly ValidationIssue[]): StepIssue[] {
  return issues.map((i) => {
    const l = locate(ir, i.path);
    return { ...i, ...l, step: stepOf(ir, l.target) };
  });
}

/**
 * Gives each field the issues under its path, the most specific field
 * first; what no field takes is the step's own. Fields are suffixes below
 * one entity ("/capacity", "/batch/max_size").
 */
export function claim<T extends { suffix: string }>(issues: readonly T[], fields: readonly string[]): { byField: Map<string, T[]>; rest: T[] } {
  const byField = new Map<string, T[]>();
  const rest: T[] = [];
  const longestFirst = [...fields].sort((a, b) => b.length - a.length);
  for (const i of issues) {
    const f = longestFirst.find((f) => f !== "" && (i.suffix === f || i.suffix.startsWith(f + "/")));
    if (f === undefined) rest.push(i);
    else byField.set(f, [...(byField.get(f) ?? []), i]);
  }
  return { byField: new Map(fields.filter((f) => byField.has(f)).map((f) => [f, byField.get(f)!])), rest };
}

/** The worst severity among issues. */
export function worst(issues: readonly { severity: "error" | "warning" }[]): "error" | "warning" | null {
  return issues.some((i) => i.severity === "error") ? "error" : issues.length > 0 ? "warning" : null;
}
