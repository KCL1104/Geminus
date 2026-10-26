// What each editor action does to the IR, as JSON Patch ops for the draft
// (REQ-004; TWIN_IR_SPEC §6). The editor never keeps another model: an edit
// is ops, the draft session queues them, Save sends them with If-Match.
// The ops follow the server's manual-draft policy (internal/model/patch):
// before an element of a catalog (nodes, flows, metrics, constraints) is
// changed or removed, the same patch tests its identity right there
// (D-020), so ops written against an older IR fail instead of changing the
// wrong element. Removing a step repairs what refers to it (its flows, the
// constraints and metrics that name them, its position) in the same edit.
// A layout the IR does not hold yet is written down first, so the nodes
// the person sees stay where they are.
import type { Distribution, ModelIR } from "../../lib/contracts";
import { layeredLayout, type Position } from "./layout";
import { type Op, pointer } from "./patch";

type IRNode = ModelIR["nodes"][number];
export type NodeType = IRNode["type"];
export const nodeTypes: readonly NodeType[] = ["source", "queue", "process", "decision", "delay", "failure", "rework", "sink"];

export type Refusal =
  | "unknown_step"
  | "unknown_connection"
  | "unknown_field"
  | "self_connection"
  | "from_sink"
  | "to_source"
  | "duplicate_connection"
  | "no_entity_type";

/** The ops of an edit, and the ID of what it created; or why it cannot be done. */
export type EditResult = { ok: true; ops: Op[]; id?: string } | { ok: false; refusal: Refusal };

const refuse = (refusal: Refusal): EditResult => ({ ok: false, refusal });
const done = (ops: Op[], id?: string): EditResult => (id === undefined ? { ok: true, ops } : { ok: true, ops, id });

const nodeAt = (ir: ModelIR, id: string) => ir.nodes.findIndex((n) => n.id === id);
const flowAt = (ir: ModelIR, id: string) => ir.flows.findIndex((f) => f.id === id);
const testNode = (i: number, id: string): Op => ({ op: "test", path: pointer("nodes", i, "id"), value: id });
const has = (o: object, k: string) => Object.hasOwn(o, k);

/** The step's name for people: its name, else its ID. */
export function stepLabel(n: IRNode): string {
  return n.name ?? n.id;
}

/** Ops that write down the positions the layered layout gives the nodes the IR has no position for (all but `except`). */
function materialize(ir: ModelIR, except?: string): Op[] {
  const layout = layeredLayout(ir);
  const positions = ir.views["2d"].positions;
  const missing = ir.nodes.filter((n) => n.id !== except && (positions === undefined || !has(positions, n.id)));
  if (positions === undefined) {
    return [{ op: "add", path: "/views/2d/positions", value: Object.fromEntries(missing.map((n) => [n.id, layout[n.id]])) }];
  }
  return missing.map((n) => ({ op: "add", path: pointer("views", "2d", "positions", n.id), value: layout[n.id] }));
}

const round = (p: Position): Position => ({ x: Math.round(p.x), y: Math.round(p.y) });

/** Moves a step on the 2D view (a view's data, not the model's meaning). */
export function moveStep(ir: ModelIR, id: string, to: Position): EditResult {
  if (nodeAt(ir, id) < 0) return refuse("unknown_step");
  const positions = ir.views["2d"].positions;
  const placed = positions !== undefined && has(positions, id);
  const ops = materialize(ir, id);
  ops.push({ op: placed ? "replace" : "add", path: pointer("views", "2d", "positions", id), value: round(to) });
  return done(ops);
}

/** Names a step; an empty name removes it (the ID is shown instead). */
export function renameStep(ir: ModelIR, id: string, name: string): EditResult {
  const i = nodeAt(ir, id);
  if (i < 0) return refuse("unknown_step");
  const node = ir.nodes[i]!;
  const next = name.trim();
  if (next === (node.name ?? "")) return done([]);
  if (next === "") return done([testNode(i, id), { op: "remove", path: pointer("nodes", i, "name") }]);
  return done([testNode(i, id), { op: node.name === undefined ? "add" : "replace", path: pointer("nodes", i, "name"), value: next }]);
}

/** The IDs one selection store may hold: nodes, resources and flows share one space (TEST-025). */
function usedIds(ir: ModelIR): Set<string> {
  return new Set([...ir.nodes.map((n) => n.id), ...ir.resources.map((r) => r.id), ...ir.flows.map((f) => f.id)]);
}

/** base_1, base_2, ... (numbered); or base, then base_2, base_3, ... */
function freshId(used: Set<string>, base: string, numbered: boolean): string {
  if (!numbered && !used.has(base)) return base;
  for (let n = numbered ? 1 : 2; ; n++) {
    const id = `${base}_${n}`;
    if (!used.has(id)) return id;
  }
}

/**
 * A new step of a generic type, unconnected. Steps that need a time get one
 * constant time unit, there to be changed in the properties; a source makes
 * the model's first entity type.
 */
export function addStep(ir: ModelIR, type: NodeType): EditResult {
  const id = freshId(usedIds(ir), type, true);
  let node: IRNode;
  switch (type) {
    case "source": {
      const entity = ir.entity_types[0];
      if (entity === undefined) return refuse("no_entity_type");
      node = { id, type, entity_type: entity.id, arrival: { distribution: { type: "exponential", mean: 1 } } };
      break;
    }
    case "process":
      node = { id, type, service_time: { distribution: { type: "constant", value: 1 } }, requires: [] };
      break;
    case "delay":
      node = { id, type, delay_time: { distribution: { type: "constant", value: 1 } } };
      break;
    case "sink":
      node = { id, type, outcome: "completed" };
      break;
    default:
      node = { id, type };
  }
  const ops = materialize(ir);
  const positions = { ...layeredLayout(ir) };
  const withNode: ModelIR = { ...ir, nodes: [...ir.nodes, node], views: { ...ir.views, "2d": { positions } } };
  const at = layeredLayout(withNode)[id]!;
  ops.push({ op: "add", path: "/nodes/-", value: node });
  ops.push({ op: "add", path: pointer("views", "2d", "positions", id), value: at });
  return done(ops, id);
}

type Catalog = "nodes" | "flows" | "metrics" | "constraints";
const identity: Record<Catalog, string> = { nodes: "id", flows: "id", metrics: "id", constraints: "node_id" };

/** Removes the elements of a catalog that match, last first, each right after the test of its identity. */
function removeWhere<T extends object>(list: readonly T[], catalog: Catalog, match: (x: T) => boolean): Op[] {
  const out: Op[] = [];
  for (let i = list.length - 1; i >= 0; i--) {
    const x = list[i]!;
    if (!match(x)) continue;
    out.push({ op: "test", path: pointer(catalog, i, identity[catalog]), value: (x as Record<string, unknown>)[identity[catalog]] });
    out.push({ op: "remove", path: pointer(catalog, i) });
  }
  return out;
}

type Metric = ModelIR["metrics"][number];
const metricNames = (m: Metric, nodes: Set<string>, flows: Set<string>): boolean =>
  ("node_id" in m && nodes.has(m.node_id)) || ("sink_id" in m && m.sink_id !== undefined && nodes.has(m.sink_id)) || ("flow_id" in m && flows.has(m.flow_id));

/** The connections a step's removal takes with it. */
export function connectionsOf(ir: ModelIR, id: string) {
  return ir.flows.filter((f) => f.from === id || f.to === id);
}

/** Removes a step with everything that refers to it. */
export function deleteStep(ir: ModelIR, id: string): EditResult {
  const i = nodeAt(ir, id);
  if (i < 0) return refuse("unknown_step");
  const nodes = new Set([id]);
  const flows = new Set(connectionsOf(ir, id).map((f) => f.id));
  const ops = materialize(ir, id);
  ops.push(...removeWhere(ir.flows, "flows", (f) => flows.has(f.id)));
  ops.push(...removeWhere(ir.metrics, "metrics", (m) => metricNames(m, nodes, flows)));
  ops.push(...removeWhere(ir.constraints, "constraints", (c) => c.node_id === id));
  ops.push(testNode(i, id), { op: "remove", path: pointer("nodes", i) });
  const positions = ir.views["2d"].positions;
  if (positions !== undefined && has(positions, id)) ops.push({ op: "remove", path: pointer("views", "2d", "positions", id) });
  return done(ops);
}

/**
 * Connects two steps with a new flow. Its probability is 1, except a
 * decision's further ways out, which start at 0 so the decision's
 * probabilities still add up; the person sets them in the properties.
 * What the server's rules say about the graph (one way out of a process,
 * a reachable end) it checks and reports on the saved draft.
 */
export function connectSteps(ir: ModelIR, from: string, to: string): EditResult {
  const source = ir.nodes[nodeAt(ir, from)];
  const target = ir.nodes[nodeAt(ir, to)];
  if (source === undefined || target === undefined) return refuse("unknown_step");
  if (from === to) return refuse("self_connection");
  if (source.type === "sink") return refuse("from_sink");
  if (target.type === "source") return refuse("to_source");
  if (ir.flows.some((f) => f.from === from && f.to === to)) return refuse("duplicate_connection");
  const used = usedIds(ir);
  const base = `${from}_${to}`;
  const id = base.length <= 120 ? freshId(used, base, false) : freshId(used, "flow", true);
  const first = !ir.flows.some((f) => f.from === from);
  const probability = source.type === "decision" && !first ? 0 : 1;
  const ops = materialize(ir);
  ops.push({ op: "add", path: "/flows/-", value: { id, from, to, routing: { type: "probability", probability } } });
  return done(ops, id);
}

/** Removes a flow and the flow counts that name it. */
export function disconnect(ir: ModelIR, flowId: string): EditResult {
  if (flowAt(ir, flowId) < 0) return refuse("unknown_connection");
  const flows = new Set([flowId]);
  const ops = materialize(ir);
  ops.push(...removeWhere(ir.metrics, "metrics", (m) => metricNames(m, new Set(), flows)));
  ops.push(...removeWhere(ir.flows, "flows", (f) => f.id === flowId));
  return done(ops);
}

/** The probability of one way out of a step. */
export function setProbability(ir: ModelIR, flowId: string, p: number): EditResult {
  const j = flowAt(ir, flowId);
  if (j < 0) return refuse("unknown_connection");
  return done([{ op: "test", path: pointer("flows", j, "id"), value: flowId }, { op: "replace", path: pointer("flows", j, "routing", "probability"), value: p }]);
}

/** Sets, adds or (null) removes an optional member of a step, after testing the step. */
function setMember(ir: ModelIR, i: number, tokens: string[], value: number | null, present: boolean): EditResult {
  const id = ir.nodes[i]!.id;
  const path = pointer("nodes", i, ...tokens);
  if (value === null) return done(present ? [testNode(i, id), { op: "remove", path }] : []);
  return done([testNode(i, id), { op: present ? "replace" : "add", path, value }]);
}

/** A queue's capacity (the finite buffer of D-049); null is no limit. */
export function setQueueCapacity(ir: ModelIR, id: string, capacity: number | null): EditResult {
  const i = nodeAt(ir, id);
  const node = ir.nodes[i];
  if (node === undefined) return refuse("unknown_step");
  if (node.type !== "queue") return refuse("unknown_field");
  return setMember(ir, i, ["capacity"], capacity, node.capacity !== undefined);
}

/** Where a step keeps its own time: a process's service, a delay's wait, a source's time between arrivals. */
export function timingOf(node: IRNode): { tokens: string[]; distribution: Distribution } | null {
  switch (node.type) {
    case "process":
      return { tokens: ["service_time", "distribution"], distribution: node.service_time.distribution };
    case "delay":
      return { tokens: ["delay_time", "distribution"], distribution: node.delay_time.distribution };
    case "source":
      return node.arrival.distribution === undefined ? null : { tokens: ["arrival", "distribution"], distribution: node.arrival.distribution };
    default:
      return null;
  }
}

/** One number of the step's own time distribution (its type stays). */
export function setTiming(ir: ModelIR, id: string, param: string, value: number): EditResult {
  const i = nodeAt(ir, id);
  const node = ir.nodes[i];
  if (node === undefined) return refuse("unknown_step");
  const timing = timingOf(node);
  if (timing === null || param === "type" || typeof (timing.distribution as Record<string, unknown>)[param] !== "number") return refuse("unknown_field");
  return setMember(ir, i, [...timing.tokens, param], value, true);
}

/** A batch process's sizes (D-054); max_wait may be removed (null). The batch itself is not added or removed here. */
export function setBatch(ir: ModelIR, id: string, field: "min_size" | "max_size" | "max_wait", value: number | null): EditResult {
  const i = nodeAt(ir, id);
  const node = ir.nodes[i];
  if (node === undefined) return refuse("unknown_step");
  if (node.type !== "process" || node.batch === undefined || (value === null && field !== "max_wait")) return refuse("unknown_field");
  return setMember(ir, i, ["batch", field], value, node.batch[field] !== undefined);
}
