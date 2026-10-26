"use client";
// The 2D canvas: React Flow drawing the working draft's IR (flow.ts). It
// holds no model: moving, connecting, selecting and deleting are reported
// to the editor, which turns them into JSON Patch (edits.ts); what React
// Flow keeps itself is only how big each node measured and where a node is
// while it is being dragged. Keyboard: Tab reaches each step (in reading
// order), Enter or Space selects it, the arrow keys move it (Shift: four
// times farther), Delete asks to remove it, Escape deselects and keeps the
// focus there (React Flow would drop it). Connections are made from a
// step's properties (a menu), or by dragging between the handles.
import {
  type Connection,
  Handle,
  MarkerType,
  type NodeChange,
  type NodeProps,
  type NodeSelectionChange,
  Position,
  ReactFlow,
} from "@xyflow/react";
import { type KeyboardEvent, useCallback, useMemo, useState } from "react";
import type { ModelIR } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import styles from "./canvas.module.css";
import { connectSteps, stepLabel } from "./edits";
import { type Severity, type StepNode, toFlow } from "./flow";
import type { Position as Point } from "./layout";
import "./vendor.css";

function StepNodeView({ data, selected, isConnectable }: NodeProps<StepNode>) {
  const { t } = useI18n();
  return (
    <div className={styles.node} data-kind={data.kind} data-severity={data.severity ?? undefined} data-selected={selected || undefined}>
      {data.kind !== "source" && <Handle type="target" position={Position.Left} isConnectable={isConnectable} className={styles.handle} />}
      <div className={styles.name}>{data.label}</div>
      <div className={styles.kind}>{t(`editor.type.${data.kind}`)}</div>
      {data.kind !== "sink" && <Handle type="source" position={Position.Right} isConnectable={isConnectable} className={styles.handle} />}
    </div>
  );
}

const nodeTypes = { step: StepNodeView };
const marker = { type: MarkerType.Arrow, width: 18, height: 18 };
/** How much of the line, in canvas units, the first view shows: about four layers. */
const START_WIDTH = 960;
/** The smallest zoom at which a step's name is still legible. */
export const READABLE_ZOOM = 0.75;
type Size = { width: number; height: number };

/** The step a keyboard event happened on, if any. */
const stepOfEvent = (e: KeyboardEvent) => (e.target as Element).closest?.(".react-flow__node")?.getAttribute("data-id") ?? null;

export function Canvas({ ir, selected, severity, editable, onSelect, onMove, onConnect, onDelete }: {
  ir: ModelIR;
  selected: string | null;
  severity: (id: string) => Severity | null;
  editable: boolean;
  onSelect: (id: string | null) => void;
  onMove: (id: string, to: Point) => void;
  onConnect: (from: string, to: string) => void;
  onDelete: (id: string) => void;
}) {
  const { t, number } = useI18n();
  const [measured, setMeasured] = useState<Record<string, Size>>({});
  const [dragging, setDragging] = useState<{ id: string; position: Point } | null>(null);

  const describe = useMemo(() => {
    const byId = new Map(ir.nodes.map((n) => [n.id, n]));
    return {
      step: (id: string) => {
        const n = byId.get(id)!;
        const s = severity(id);
        const key = s === "error" ? "editor.step.labelError" : s === "warning" ? "editor.step.labelWarning" : "editor.step.label";
        return t(key, { name: stepLabel(n), type: t(`editor.type.${n.type}`) });
      },
      edge: (from: string, to: string) => t("editor.canvas.edge", { from, to }),
      roleStep: t("editor.canvas.step"),
      roleEdge: t("editor.canvas.connection"),
      probability: (p: number) => number(p, { style: "percent", maximumFractionDigits: 2 }),
    };
  }, [ir, severity, t, number]);

  const flow = useMemo(() => toFlow(ir, { selected, severity, editable, describe }), [ir, selected, severity, editable, describe]);
  // React Flow re-measures a node it gets without its size; give back what it measured.
  const nodes = useMemo(
    () => flow.nodes.map((n) => {
      const m = measured[n.id];
      const at = dragging?.id === n.id ? dragging.position : n.position;
      return m === undefined && at === n.position ? n : { ...n, position: at, ...(m ? { measured: m } : {}) };
    }),
    [flow, measured, dragging],
  );
  const edges = useMemo(() => flow.edges.map((e) => ({ ...e, markerEnd: marker })), [flow]);
  // The first view: the start of the line at a size people can read (a
  // production line runs wide); "Show all steps" fits the whole of it.
  const [firstView] = useState(() => {
    const left = Math.min(...flow.nodes.map((n) => n.position.x));
    return { padding: 0.1, maxZoom: 1, nodes: flow.nodes.filter((n) => n.position.x <= left + START_WIDTH).map((n) => ({ id: n.id })) };
  });

  const onNodesChange = useCallback((changes: NodeChange<StepNode>[]) => {
    // Selecting a node also deselects the one before, in the same batch: the selection wins.
    const chosen = changes.find((c): c is NodeSelectionChange => c.type === "select" && c.selected);
    if (chosen !== undefined) onSelect(chosen.id);
    for (const c of changes) {
      if (c.type === "dimensions" && c.dimensions) {
        const size = c.dimensions;
        setMeasured((m) => (m[c.id]?.width === size.width && m[c.id]?.height === size.height ? m : { ...m, [c.id]: size }));
      } else if (c.type === "position" && c.position) {
        if (c.dragging) {
          setDragging({ id: c.id, position: c.position });
        } else {
          setDragging(null);
          onMove(c.id, c.position);
        }
      } else if (c.type === "select" && !c.selected && chosen === undefined && c.id === selected) {
        onSelect(null);
      }
    }
  }, [onMove, onSelect, selected]);

  const isValidConnection = useCallback((c: Connection | { source: string; target: string }) => connectSteps(ir, c.source, c.target).ok, [ir]);

  // Before React Flow's own key handling on the node: Escape keeps the focus,
  // Delete goes through the editor's confirmation.
  const onKeyDownCapture = (e: KeyboardEvent<HTMLDivElement>) => {
    const id = stepOfEvent(e);
    if (id === null) return;
    if (e.key === "Escape") {
      e.stopPropagation();
      onSelect(null);
    } else if (editable && (e.key === "Delete" || e.key === "Backspace")) {
      e.stopPropagation();
      e.preventDefault();
      onDelete(id);
    }
  };

  const ariaLabelConfig = useMemo(() => ({
    "node.a11yDescription.default": t(editable ? "editor.canvas.help" : "editor.canvas.helpReadOnly"),
    "node.a11yDescription.keyboardDisabled": t(editable ? "editor.canvas.help" : "editor.canvas.helpReadOnly"),
    "node.a11yDescription.ariaLiveMessage": ({ direction, x, y }: { direction: string; x: number; y: number }) =>
      t("editor.canvas.moved", { direction: t(`editor.direction.${direction as "left" | "right" | "up" | "down"}`), x: String(x), y: String(y) }),
    "edge.a11yDescription.default": t("editor.canvas.edgeHelp"),
    "handle.ariaLabel": t("editor.canvas.handle"),
  }), [t, editable]);

  return (
    <div className={styles.canvas} onKeyDownCapture={onKeyDownCapture}>
      <ReactFlow<StepNode>
        aria-label={t("editor.canvas")}
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onConnect={(c) => onConnect(c.source, c.target)}
        isValidConnection={isValidConnection}
        onPaneClick={() => onSelect(null)}
        nodesDraggable={editable}
        nodesConnectable={editable}
        edgesFocusable={false}
        edgesReconnectable={false}
        deleteKeyCode={null}
        selectionKeyCode={null}
        multiSelectionKeyCode={null}
        zoomOnDoubleClick={false}
        minZoom={0.2}
        maxZoom={2}
        fitView
        fitViewOptions={firstView}
        ariaLabelConfig={ariaLabelConfig}
      />
    </div>
  );
}
