"use client";
// The read-only 3D view of a model (T-033, H-072; ADR-008, REQ-017): the
// same steps as the 2D canvas, by the same IDs, selected through the same
// store, each drawn as its equipment (scene.ts). It authors nothing: a click
// selects a step, and the camera moves. Drag to orbit, Shift+drag or the
// right button to pan, the wheel to zoom; from the keyboard, the arrows
// orbit, + and − zoom, 0 resets, Escape clears the selection. Steps are
// chosen from the keyboard in the 2D view, which has every step in Tab
// order: the 3D view is a projection, not the only way in.
//
// The line in motion (H-072, DESIGN.md The Trace Motion Rule): given a
// stage (a run's sampled trace, its motion and whose run it is) and the
// page's clock, the view draws the run's boards and the stations' lights at
// the clock's time (boards.ts). It never re-renders React per frame: the
// clock hands the time to the view, which draws on its next animation frame;
// the notes under the stage (the selected step's state, "+N not drawn") are
// written into the page there, and nothing is announced while it plays.
// Without a stage the line stands still: its conveyors show their direction
// and nothing moves.
//
// PlayCanvas is imported only when the view opens. If it cannot run (no
// WebGL, the import fails), the view says so and nothing else is affected:
// the 2D view, the editor and the results never wait for it.
import { type KeyboardEvent, type PointerEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import type { ModelIR, Result, TraceArtifact } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { Translator } from "../../lib/i18n/translate";
import { Callout } from "../../ui/callout";
import { type PlaybackClock, useClockFrame } from "../playback/clock";
import type { Frame, Motion } from "../playback/motion";
import { packKey } from "../playback/playback-bar";
import { BOARD_LIMIT, batchCells, type Placement, placeBoards } from "./boards";
import type { Palette, Playback, Side, View } from "./engine";
export type { Playback, Side, View } from "./engine";
import { buildScene, type Scene, stageAspect } from "./scene";
import styles from "./twin-3d.module.css";

/** Makes the view on a canvas; the default loads PlayCanvas (a test passes its own). */
export type LoadView = (canvas: HTMLCanvasElement, palette: Palette) => Promise<View>;

const loadPlayCanvas: LoadView = async (canvas, palette) => {
  const [pc, { createView }] = await Promise.all([import("playcanvas"), import("./engine")]);
  return createView(pc, canvas, palette);
};

/** A run in motion on this stage (H-072): whose it is (its boards' ink), its sampled trace and the motion the page built from it once (motion.ts). */
export type Stage = { readonly side: Side; readonly trace: TraceArtifact; readonly motion: Motion };

type Props = {
  ir: ModelIR;
  /** A run's results: processes coloured by utilization, queues raised by length (T-033); not drawn while a stage plays. */
  results?: Result | null;
  selected: string | null;
  onSelect: (id: string | null) => void;
  /** Playback (T-034): the sampled items at each step at the time shown, and whose run they are. */
  occupancy?: { counts: Readonly<Record<string, number>>; run: Side } | null;
  /** The line in motion (H-072): the run playing here, on the page's clock (usePlaybackClock). Both, or the line stands still. */
  stage?: Stage | null;
  clock?: PlaybackClock | null;
  /** The canvas's name (default: "3D view of the line: N steps"); a page with two stages names each. */
  label?: string;
  /** The key to the station lights under a playing stage (default on; a page with two stages shows it under one). */
  legend?: boolean;
  loadView?: LoadView;
};

// The tokens as #rrggbb, the one form PlayCanvas parses: the built CSS
// writes #ffffff as #fff, which it would read as blue. The browser's 2D
// context normalizes any CSS colour it is given.
function palette(el: Element): Palette {
  const css = getComputedStyle(el);
  const v = (name: string) => {
    const raw = css.getPropertyValue(`--gm-color-${name}`).trim();
    // A fresh context per token, so a value it cannot read leaves its default, not the last token.
    const ctx = document.createElement("canvas").getContext("2d");
    if (!ctx) return raw;
    ctx.fillStyle = raw;
    return String(ctx.fillStyle);
  };
  return {
    sheet: v("sheet"), ink: v("ink"), ink2: v("ink-2"), ink4: v("ink-4"), edge: v("edge"), rule: v("rule"), ruleStrong: v("rule-strong"),
    baseline: v("baseline"), scenario: v("scenario"), busy: v("warning-edge"), warning: v("warning-mark"), error: v("error"),
  };
}

/** What the selected step is doing at the time shown, for the line under the stage (read with the canvas, never announced). */
export function stepNow(t: Translator, scene: Scene, id: string, frame: Frame | null): string {
  const n = scene.nodes.find((x) => x.id === id);
  const name = n?.label ?? id;
  const f = frame?.nodes[id];
  if (!n || !f) return t("twin3d.selected", { name });
  if (n.type === "queue") return t("twin3d.selected.queue", { name, count: f.waiting });
  if (n.type === "process") return t("twin3d.selected.station", { name, state: t(`playback.state.${f.state}`), count: f.inService });
  return t("twin3d.selected", { name });
}

const KEY_ORBIT = 10;
/** A press that moves less than this far from where it went down is a click (review r1 M2). */
const CLICK_SLOP = 4;

export function Twin3D({ ir, results, selected, onSelect, occupancy = null, stage = null, clock = null, label: name, legend = true, loadView = loadPlayCanvas }: Props) {
  const { t } = useI18n();
  const hintId = useId();
  const lightsId = useId();
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const view = useRef<View | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  const [hover, setHover] = useState<{ id: string; x: number; y: number } | null>(null);
  const playing = stage !== null && clock !== null;
  // The line in motion is drawn without the results' tint and columns: its lights say what each station does.
  const scene = useMemo(() => buildScene(ir, playing ? null : results), [ir, results, playing]);
  // The stage takes the line's shape as the first view frames it (H-039): a long, flat line gets a low stage.
  const shape = useMemo(() => (scene.nodes.length === 0 ? undefined : { aspectRatio: `${stageAspect(scene)} / 1`, height: "auto" }), [scene]);
  const drag = useRef<{ x0: number; y0: number; x: number; y: number; moved: boolean; pan: boolean } | null>(null);

  // The notes written per draw, outside React: the selected step's state, "+N not drawn", the boards at steps this model lacks.
  const now = useRef<HTMLSpanElement>(null);
  const capped = useRef<HTMLSpanElement>(null);
  const lost = useRef<HTMLParagraphElement>(null);
  const last = useRef<Placement | null>(null);
  const write = useRef<(p: Placement | null) => void>(() => {});
  useEffect(() => {
    write.current = (p) => {
      last.current = p;
      const set = (el: HTMLElement | null, text: string) => {
        if (!el) return;
        if (el.textContent !== text) el.textContent = text;
        el.hidden = text === "";
      };
      set(now.current, selected === null ? "" : stepNow(t, scene, selected, p?.frame ?? null));
      set(capped.current, p && p.notDrawn > 0 ? t("playback.notDrawn", { count: p.notDrawn }) : "");
      set(lost.current, p && p.hidden > 0 ? t("playback.hidden", { count: p.hidden }) : "");
    };
    // A new selection (or language) is written at once from the last frame drawn; the next draw keeps it current.
    write.current(last.current);
  });

  // Each batch keeps its oven cell (boards.ts), worked out once per trace and scene (a new stage object with the same trace costs nothing).
  const trace = stage?.trace ?? null;
  const moving = stage?.motion ?? null;
  const side = stage?.side ?? null;
  const cells = useMemo(() => (trace ? batchCells(trace, scene) : null), [trace, scene]);
  const play = useMemo<Playback | null>(() => {
    if (!moving || !side || !clock) return null;
    return {
      side,
      at: (tick) => placeBoards(scene, moving.at(tick, { hop: clock.hopTicks(), limit: BOARD_LIMIT }), cells ?? undefined),
      onDraw: (p) => write.current(p),
    };
  }, [moving, side, clock, scene, cells]);

  useEffect(() => {
    let cancelled = false;
    let observer: ResizeObserver | undefined;
    let themes: MutationObserver | undefined;
    const scheme = window.matchMedia?.("(prefers-color-scheme: dark)");
    // The page's theme can change while the view is open (review r1 M4).
    const retheme = () => wrap.current && view.current?.setPalette(palette(wrap.current));
    // The wheel zooms the scene, not the page (a passive React listener could not stop it).
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      view.current?.zoom(Math.min(1.5, Math.max(0.66, Math.exp(e.deltaY * 0.0015))));
    };
    const el = canvas.current;
    el?.addEventListener("wheel", wheel, { passive: false });
    void (async () => {
      try {
        if (!el || !wrap.current) return;
        const v = await loadView(el, palette(wrap.current));
        if (cancelled || !wrap.current) {
          v.destroy();
          return;
        }
        view.current = v;
        const fit = () => wrap.current && v.resize(wrap.current.clientWidth, wrap.current.clientHeight);
        fit();
        if (typeof ResizeObserver !== "undefined") {
          observer = new ResizeObserver(fit);
          observer.observe(wrap.current);
        }
        themes = new MutationObserver(retheme);
        themes.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
        scheme?.addEventListener("change", retheme);
        setState("ready"); // the effects below draw the scene, the selection and the playback now
      } catch {
        if (!cancelled) setState("failed");
      }
    })();
    return () => {
      cancelled = true;
      el?.removeEventListener("wheel", wheel);
      observer?.disconnect();
      themes?.disconnect();
      scheme?.removeEventListener("change", retheme);
      view.current?.destroy();
      view.current = null;
    };
  }, [loadView]);
  useEffect(() => {
    view.current?.setScene(scene);
  }, [scene, state]);
  useEffect(() => {
    view.current?.setSelected(selected);
  }, [selected, state]);
  useEffect(() => {
    view.current?.setOccupancy(occupancy?.counts ?? null, occupancy?.run ?? "scenario");
  }, [occupancy, scene, state]);
  useEffect(() => {
    view.current?.setPlayback(play);
    if (play && clock) view.current?.setTime(clock.now());
    if (!play) last.current = null;
  }, [play, clock, state]);
  // The clock hands each time to the view, which draws it on its next frame: no React render.
  useClockFrame(playing ? clock : null, (tick) => view.current?.setTime(tick));

  if (state === "failed") return <Callout tone="info">{t("twin3d.unavailable")}</Callout>;

  const at = (e: PointerEvent<HTMLCanvasElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  const label = (id: string) => scene.nodes.find((n) => n.id === id)?.label ?? id;
  const keys = (e: KeyboardEvent<HTMLCanvasElement>) => {
    const v = view.current;
    // The browser's own shortcuts (Ctrl/Cmd + and −: page zoom, WCAG 1.4.4; Alt+←) stay the browser's (review r1 M1).
    if (!v || e.ctrlKey || e.metaKey || e.altKey) return;
    const act: Record<string, () => void> = {
      ArrowLeft: () => v.orbit(-KEY_ORBIT, 0), ArrowRight: () => v.orbit(KEY_ORBIT, 0),
      ArrowUp: () => v.orbit(0, KEY_ORBIT), ArrowDown: () => v.orbit(0, -KEY_ORBIT),
      "+": () => v.zoom(0.8), "=": () => v.zoom(0.8), "-": () => v.zoom(1.25), "0": () => v.reset(), Escape: () => onSelect(null),
    };
    const f = act[e.key];
    if (f) {
      e.preventDefault();
      f();
    }
  };

  return (
    <div className={styles.twin}>
      <div ref={wrap} className={styles.stage} style={shape}>
        <canvas
          ref={canvas}
          className={styles.canvas}
          tabIndex={0}
          role="img"
          aria-label={name ?? t("twin3d.label", { count: scene.nodes.length })}
          aria-describedby={hintId}
          onKeyDown={keys}
          onContextMenu={(e) => e.preventDefault()}
          onPointerDown={(e) => {
            e.currentTarget.setPointerCapture?.(e.pointerId);
            const p = at(e);
            drag.current = { x0: p.x, y0: p.y, ...p, moved: false, pan: e.button === 2 || e.shiftKey };
            setHover(null);
          }}
          onPointerMove={(e) => {
            const p = at(e);
            const d = drag.current;
            if (d) {
              const dx = p.x - d.x;
              const dy = p.y - d.y;
              if (Math.hypot(p.x - d.x0, p.y - d.y0) > CLICK_SLOP) d.moved = true;
              if (d.pan) view.current?.pan(dx, dy);
              else view.current?.orbit(dx * 0.3, dy * 0.3);
              d.x = p.x;
              d.y = p.y;
              return;
            }
            const id = view.current?.stepAt(p.x, p.y) ?? null;
            setHover(id === null ? null : { id, ...p });
          }}
          onPointerUp={(e) => {
            const d = drag.current;
            drag.current = null;
            if (d && !d.moved && e.button === 0) onSelect(view.current?.stepAt(d.x0, d.y0) ?? null);
          }}
          onPointerCancel={() => (drag.current = null)}
          onLostPointerCapture={() => (drag.current = null)}
          onPointerLeave={() => setHover(null)}
        />
        {hover && (
          <span className={styles.tip} style={{ left: hover.x + 12, top: hover.y + 12 }} aria-hidden="true">
            {label(hover.id)}
          </span>
        )}
        {state === "loading" && <p className={styles.loading}>{t("twin3d.loading")}</p>}
        {/* "+N not drawn": the boards past the stage's 2,000 (brief §5), written per draw. */}
        {playing && <span ref={capped} className={styles.cap} hidden />}
      </div>
      <p id={hintId} className={styles.hint}>
        {selected !== null &&
          (playing ? <span ref={now} className={styles.selected} hidden /> : <span className={styles.selected}>{t("twin3d.selected", { name: label(selected) })}</span>)}
        {t("twin3d.hint")}
      </p>
      {playing && legend && (
        <div className={styles.legend}>
          <span id={lightsId} className={styles.lightsLabel}>
            {t("twin3d.lights")}
          </span>
          <ul className={styles.lights} aria-labelledby={lightsId}>
            <li>
              <span className={`${styles.lamp} ${styles.busy}`} aria-hidden="true" />
              {t("playback.state.busy")}
            </li>
            <li>
              <span className={`${styles.lamp} ${styles.idle}`} aria-hidden="true" />
              {t("playback.state.idle")}
            </li>
            <li>
              <span className={`${styles.lamp} ${styles.held}`} aria-hidden="true" />
              {t("twin3d.lights.held")}
            </li>
          </ul>
          <span className={styles.caveat}>{t(packKey(ir.domain, "playback.statesSampled"))}</span>
        </div>
      )}
      {playing && <p ref={lost} className={styles.hint} hidden />}
      {results && !playing && <p className={styles.hint}>{t("twin3d.legend")}</p>}
    </div>
  );
}
