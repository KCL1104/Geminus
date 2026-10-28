// IR → React Flow (ADR-007: the 2D canvas is a projection of the IR, never
// a second topology). Each IR node is a React Flow node with the node's ID,
// each flow an edge with the flow's ID and endpoints; positions are the
// IR's views.2d.positions, else the layered default the server also uses
// (canvasLayout: the whole line again after an AI edit added steps).
// Nothing the canvas holds is saved: edits become JSON Patch (edits.ts).
// The 3D viewer (T-017) is to take the same IDs and the same selection store.
import type { Edge, Node } from "@xyflow/react";
import type { ModelIR } from "../../lib/contracts";
import type { NodeType } from "./edits";
import { stepLabel } from "./edits";
import { canvasLayout } from "./layout";

export type Severity = "error" | "warning";

/** What a step's canvas node draws: its name, its generic type, its worst issue. */
export type StepData = { label: string; kind: NodeType; severity: Severity | null };
export type StepNode = Node<StepData, "step">;

/** The words the canvas needs, from the caller's catalog (the adapter knows no language). */
export type Describe = {
  /** A step's accessible name, by node ID. */
  step: (id: string) => string;
  /** A connection's accessible name. */
  edge: (fromLabel: string, toLabel: string) => string;
  /** What a node is called, instead of React Flow's English "node". */
  roleStep: string;
  /** What an edge is called, instead of React Flow's English "edge". */
  roleEdge: string;
  /** A decision branch's probability as text. */
  probability: (p: number) => string;
};

export type FlowOptions = {
  selected: string | null;
  severity: (nodeId: string) => Severity | null;
  /** Owners and editors move and connect; everyone else looks. */
  editable: boolean;
  describe: Describe;
};

export function toFlow(ir: ModelIR, opts: FlowOptions): { nodes: StepNode[]; edges: Edge[] } {
  const positions = canvasLayout(ir);
  const byId = new Map(ir.nodes.map((n) => [n.id, n]));
  const label = (id: string) => {
    const n = byId.get(id);
    return n === undefined ? id : stepLabel(n);
  };
  // Document order of the nodes is the Tab order: left to right, then top to bottom.
  const order = ir.nodes.map((n, i) => ({ n, i, p: positions[n.id]! })).sort((a, b) => a.p.x - b.p.x || a.p.y - b.p.y || a.i - b.i);
  const nodes: StepNode[] = order.map(({ n, p }) => ({
    id: n.id,
    type: "step",
    position: p,
    data: { label: stepLabel(n), kind: n.type, severity: opts.severity(n.id) },
    selected: n.id === opts.selected,
    draggable: opts.editable,
    connectable: opts.editable,
    // Removal goes through the editor's confirmation, never React Flow's key handler.
    deletable: false,
    ariaLabel: opts.describe.step(n.id),
    domAttributes: { "aria-roledescription": opts.describe.roleStep },
  }));
  const edges: Edge[] = ir.flows.map((f) => {
    const decision = byId.get(f.from)?.type === "decision";
    return {
      id: f.id,
      source: f.from,
      target: f.to,
      type: "smoothstep",
      // Connections are made and changed from a step's properties.
      selectable: false,
      focusable: false,
      deletable: false,
      ariaLabel: opts.describe.edge(label(f.from), label(f.to)),
      domAttributes: { "aria-roledescription": opts.describe.roleEdge },
      ...(decision ? { label: opts.describe.probability(f.routing.probability) } : {}),
    };
  });
  return { nodes, edges };
}
