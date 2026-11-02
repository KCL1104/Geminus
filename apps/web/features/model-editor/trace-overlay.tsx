"use client";
// The 2D canvas's trace overlay (H-072; docs/design/H-072_TRACE_ANIMATION.md
// §6; DESIGN.md, The Trace Motion Rule): a run's sampled trace on the model
// canvas, driven by the page's playback clock (features/playback/clock.ts).
//
// - Dots in the run's ink (Baseline outlined, Scenario solid) slide along
//   the canvas's own links during a hop (trace-geometry.ts), under the step
//   cards; between its events a board is not drawn: a count badge on each
//   card says how many sampled boards wait there, and a 2 px ring around a
//   machine says its state (Ink busy, Ink 4 idle, Warning blocked or
//   changing over, Error down).
// - Without a run (no `playback`), chevrons on the links show the way the
//   line flows and nothing moves.
// - No React render per frame: the layer subscribes to the clock and writes
//   the dots, the badges, the rings and the step's state text into the DOM
//   itself, only where they change. React renders it again only when its
//   inputs change (another run, the draft, the language).
// - Assistive tech: the dots and the badges are hidden from it; the state is
//   the step card's description (its own text, added to the description
//   React Flow gives the card), so it is there when the card has the focus
//   and nothing is announced while the line plays (no live region).
import { BaseEdge, EdgeLabelRenderer, type EdgeProps, getSmoothStepPath, Panel, Position } from "@xyflow/react";
import { createContext, memo, useCallback, useContext, useEffect, useMemo, useRef } from "react";
import type { ModelIR } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { type PlaybackClock, useClockFrame } from "../playback/clock";
import type { Motion } from "../playback/motion";
import { packKey } from "../playback/playback-bar";
import styles from "./canvas.module.css";
import { stepLabel } from "./edits";
import { chevronPath, createRouter, DRAW_LIMIT, overlayFrame, pathPoints, type Point, type StepMark } from "./trace-geometry";

/** A run to play on the canvas: the page's clock, the run's motion (motion.ts, built once per trace), and whose run it is (its ink). */
export type CanvasPlayback = { readonly clock: PlaybackClock; readonly motion: Motion; readonly side: "baseline" | "scenario" };

/** A dot's radius in canvas units: 10 px across at zoom 1, the cell spacing. */
const DOT_RADIUS = 5;
const SVG = "http://www.w3.org/2000/svg";

/** What a card shows now; undefined (and -1) for "not known", so the next paint writes it. */
type Painted = { station: string | null | undefined; waiting: number; text: string | undefined };
type StepParts = {
  readonly card: HTMLElement;
  /** React Flow's node element, whose description gets the state. */
  readonly wrapper: HTMLElement | null;
  readonly badge: HTMLElement | null;
  readonly label: HTMLElement | null;
  /** The wrapper's own aria-describedby before the overlay added the state (undefined until then). */
  described: string | null | undefined;
  painted: Painted;
};

const blank = (): Painted => ({ station: null, waiting: 0, text: "" });

/** Takes everything the overlay wrote off a card: its ring, badge, state text and the description it added. */
function reset(parts: StepParts): void {
  parts.card.removeAttribute("data-station");
  if (parts.badge) parts.badge.textContent = "";
  if (parts.label) parts.label.textContent = "";
  if (parts.wrapper && parts.described !== undefined) {
    if (parts.described === null) parts.wrapper.removeAttribute("aria-describedby");
    else parts.wrapper.setAttribute("aria-describedby", parts.described);
  }
  parts.described = undefined;
  parts.painted = blank();
}

/** What the canvas's links and step cards tell the overlay: each link's drawn polyline and each card's elements. */
export type OverlayRegistry = {
  setRoute(flowId: string, points: readonly Point[]): void;
  deleteRoute(flowId: string, points: readonly Point[]): void;
  route(flowId: string): readonly Point[] | undefined;
  setStep(nodeId: string, card: HTMLElement): void;
  deleteStep(nodeId: string, card: HTMLElement): void;
  steps(): IterableIterator<[string, StepParts]>;
  /** Forgets what was painted (the next paint writes everything again). */
  repaint(): void;
  /** Called (once per task) after a link or a card changed. */
  listen(listener: () => void): () => void;
};

export function createRegistry(): OverlayRegistry {
  const routes = new Map<string, readonly Point[]>();
  const steps = new Map<string, StepParts>();
  let listener: (() => void) | null = null;
  let queued = false;
  const changed = () => {
    if (queued || listener === null) return;
    queued = true;
    queueMicrotask(() => {
      queued = false;
      listener?.();
    });
  };
  return {
    setRoute(id, points) {
      routes.set(id, points);
      changed();
    },
    deleteRoute(id, points) {
      if (routes.get(id) !== points) return;
      routes.delete(id);
      changed();
    },
    route: (id) => routes.get(id),
    setStep(id, card) {
      const old = steps.get(id);
      if (old) reset(old);
      steps.set(id, {
        card,
        wrapper: card.closest<HTMLElement>(".react-flow__node"),
        badge: card.querySelector<HTMLElement>("[data-trace-badge]"),
        label: card.querySelector<HTMLElement>("[data-trace-state]"),
        described: undefined,
        painted: blank(),
      });
      changed();
    },
    deleteStep(id, card) {
      const parts = steps.get(id);
      if (parts?.card !== card) return;
      reset(parts);
      steps.delete(id);
    },
    steps: () => steps.entries(),
    repaint() {
      for (const parts of steps.values()) parts.painted = { station: undefined, waiting: -1, text: undefined };
    },
    listen(l) {
      listener = l;
      return () => {
        if (listener === l) listener = null;
      };
    },
  };
}

/** The canvas's overlay state for its links and step cards: whether a run plays, and where they report to. */
export const OverlayContext = createContext<{ readonly active: boolean; readonly registry: OverlayRegistry | null }>({ active: false, registry: null });

/**
 * A flow on the canvas: React Flow's smoothstep link, drawn as its built-in
 * edge draws it (the same path and arrow), with chevrons along it while no
 * run plays, and its polyline reported to the overlay while one does.
 *
 * Its label (a decision's probability) is not in the link's own SVG group,
 * where a link drawn later would cross it, but in React Flow's edge-label
 * layer: above every link and under the cards, on a backing of the canvas's
 * ground (canvas.module.css .edgeLabel), at the point React Flow gives it.
 * The link is an image to assistive tech (role="img" with its name), so the
 * label is hidden from it, as its SVG text was; the step's properties say
 * the probability.
 */
export const FlowEdge = memo(function FlowEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition = Position.Bottom,
  targetPosition = Position.Top,
  label,
  style,
  markerEnd,
  markerStart,
  interactionWidth,
}: EdgeProps) {
  const { active, registry } = useContext(OverlayContext);
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition });
  const points = useMemo(() => pathPoints(path), [path]);
  useEffect(() => {
    if (!active || registry === null) return;
    registry.setRoute(id, points);
    return () => registry.deleteRoute(id, points);
  }, [active, registry, id, points]);
  const chevrons = active ? "" : chevronPath(points);
  return (
    <>
      {/* Under the link; the label, in the layer above every link, covers a chevron behind it. */}
      {chevrons !== "" && <path className={styles.chevrons} d={chevrons} data-chevrons="" />}
      <BaseEdge path={path} style={style} markerEnd={markerEnd} markerStart={markerStart} interactionWidth={interactionWidth} />
      {label !== undefined && label !== null && label !== "" && (
        <EdgeLabelRenderer>
          <div className={styles.edgeLabel} style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }} aria-hidden="true" data-edge-label="">
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
});

/** Draws the dots: one circle per dot, reused; the rest are hidden. */
function paintDots(svg: SVGSVGElement, dots: readonly Point[]): void {
  const circles = svg.children;
  for (let i = 0; i < dots.length; i++) {
    let c = circles[i];
    if (c === undefined) {
      c = document.createElementNS(SVG, "circle");
      c.setAttribute("r", String(DOT_RADIUS));
      c.setAttribute("class", styles.dot ?? "");
      svg.append(c);
    }
    c.setAttribute("cx", String(Math.round(dots[i]!.x * 10) / 10));
    c.setAttribute("cy", String(Math.round(dots[i]!.y * 10) / 10));
    if (c.hasAttribute("display")) c.removeAttribute("display");
  }
  for (let i = dots.length; i < circles.length; i++) {
    const c = circles[i]!;
    if (c.getAttribute("display") !== "none") c.setAttribute("display", "none");
  }
}

/** Writes a card's ring, badge and state text where they changed. */
function paintStep(parts: StepParts, mark: StepMark | null, text: string, format: (n: number) => string): void {
  const { card, painted } = parts;
  const station = mark?.state ?? null;
  if (painted.station !== station) {
    if (station === null) card.removeAttribute("data-station");
    else card.setAttribute("data-station", station);
    painted.station = station;
  }
  const waiting = mark?.waiting ?? 0;
  if (painted.waiting !== waiting) {
    if (parts.badge) parts.badge.textContent = waiting > 0 ? format(waiting) : "";
    painted.waiting = waiting;
  }
  if (painted.text !== text) {
    const { label, wrapper } = parts;
    if (label) label.textContent = text;
    if (label && wrapper) {
      if (parts.described === undefined) parts.described = wrapper.getAttribute("aria-describedby");
      const ids = [text === "" ? null : label.id, parts.described].filter((x): x is string => x !== null && x !== "").join(" ");
      if (ids === "") wrapper.removeAttribute("aria-describedby");
      else if (wrapper.getAttribute("aria-describedby") !== ids) wrapper.setAttribute("aria-describedby", ids);
    }
    painted.text = text;
  }
}

/**
 * The overlay's layer, a child of <ReactFlow> (it uses its store): the dots
 * under the step cards (React Flow's edge-label layer, in canvas units), the
 * count of boards not drawn in a corner, and the paint of the cards.
 */
export function TraceLayer({ playback, ir, registry }: { playback: CanvasPlayback; ir: ModelIR; registry: OverlayRegistry }) {
  const { t, number, gap } = useI18n();
  const { clock, motion } = playback;
  const svg = useRef<SVGSVGElement | null>(null);
  const note = useRef<HTMLParagraphElement | null>(null);
  const steps = useMemo(() => ir.nodes.map((n) => ({ id: n.id, type: n.type, name: stepLabel(n) })), [ir.nodes]);
  const names = useMemo(() => new Map(steps.map((s) => [s.id, s.name])), [steps]);
  const router = useMemo(() => createRouter(ir.flows, registry.route), [ir.flows, registry]);

  const describe = (name: string, mark: StepMark): string => {
    if (mark.batch !== null) return t("playback.step.batch", { name, count: mark.batch });
    if (mark.state !== null) return t("playback.step", { name, state: t(`playback.state.${mark.state}`), count: mark.waiting });
    return mark.waiting > 0 ? t("playback.step.waiting", { name, count: mark.waiting }) : "";
  };

  const draw = (tick: number) => {
    const frame = motion.at(tick, { hop: clock.hopTicks(), limit: DRAW_LIMIT });
    const view = overlayFrame(frame, steps, router.route);
    if (svg.current) paintDots(svg.current, view.dots);
    for (const [id, parts] of registry.steps()) {
      const mark = view.steps.get(id) ?? null;
      const name = names.get(id);
      paintStep(parts, mark, mark !== null && name !== undefined ? describe(name, mark) : "", (n) => number(n));
    }
    if (note.current) {
      const lines = [
        ...(view.notDrawn > 0 ? [t(packKey(ir.domain, "playback.notDrawn"), { count: view.notDrawn })] : []),
        ...(view.hidden > 0 ? [t(packKey(ir.domain, "playback.hidden"), { count: view.hidden })] : []),
      ].join(gap);
      if (note.current.textContent !== lines) note.current.textContent = lines;
    }
  };
  const latest = useRef(draw);
  useEffect(() => {
    latest.current = draw;
  });
  useClockFrame(clock, draw);
  // Another run, draft or language: everything is written again.
  useEffect(() => {
    registry.repaint();
    latest.current(clock.now());
  }, [registry, clock, motion, steps, router, t, number, gap]);
  // A link drawn again (a step moved) or a card that came or went.
  useEffect(
    () =>
      registry.listen(() => {
        router.clear();
        latest.current(clock.now());
      }),
    [registry, router, clock],
  );
  // On its way out (no run any more), the cards lose the ring, the badge and the state.
  useEffect(
    () => () => {
      for (const [, parts] of registry.steps()) reset(parts);
    },
    [registry],
  );
  const attach = useCallback(
    (el: SVGSVGElement | null) => {
      svg.current = el;
      if (el) latest.current(clock.now());
    },
    [clock],
  );

  return (
    <>
      <EdgeLabelRenderer>
        <svg ref={attach} className={styles.dots} aria-hidden="true" focusable="false" data-trace-dots="" />
      </EdgeLabelRenderer>
      <Panel position="bottom-left" className={styles.tracePanel}>
        <p ref={note} className={styles.traceNote} data-trace-note="" />
      </Panel>
    </>
  );
}
