"use client";
// The read-only 3D view of a model (T-033; ADR-008, REQ-017): the same steps
// as the 2D canvas, by the same IDs, selected through the same store. It
// authors nothing: a click selects a step, and the camera moves. Drag to
// orbit, Shift+drag or the right button to pan, the wheel to zoom; from the
// keyboard, the arrows orbit, + and − zoom, 0 resets, Escape clears the
// selection. Steps are chosen from the keyboard in the 2D view, which has
// every step in Tab order: the 3D view is a projection, not the only way in.
//
// PlayCanvas is imported only when the view opens. If it cannot run (no
// WebGL, the import fails), the view says so and nothing else is affected:
// the 2D view, the editor and the results never wait for it.
import { type KeyboardEvent, type PointerEvent, useEffect, useId, useMemo, useRef, useState } from "react";
import type { ModelIR, Result } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import { Callout } from "../../ui/callout";
import type { Palette, View } from "./engine";
export type { View } from "./engine";
import { buildScene } from "./scene";
import styles from "./twin-3d.module.css";

/** Makes the view on a canvas; the default loads PlayCanvas (a test passes its own). */
export type LoadView = (canvas: HTMLCanvasElement, palette: Palette) => Promise<View>;

const loadPlayCanvas: LoadView = async (canvas, palette) => {
  const [pc, { createView }] = await Promise.all([import("playcanvas"), import("./engine")]);
  return createView(pc, canvas, palette);
};

type Props = {
  ir: ModelIR;
  /** A run's results: processes coloured by utilization, queues raised by length (T-033 scope). */
  results?: Result | null;
  selected: string | null;
  onSelect: (id: string | null) => void;
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
  return { sheet: v("sheet"), ink: v("ink"), edge: v("edge"), rule: v("rule"), baseline: v("baseline"), scenario: v("scenario"), busy: v("warning-edge") };
}

const KEY_ORBIT = 10;
/** A press that moves less than this far from where it went down is a click (review r1 M2). */
const CLICK_SLOP = 4;

export function Twin3D({ ir, results, selected, onSelect, loadView = loadPlayCanvas }: Props) {
  const { t } = useI18n();
  const hintId = useId();
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const view = useRef<View | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  const [hover, setHover] = useState<{ id: string; x: number; y: number } | null>(null);
  const scene = useMemo(() => buildScene(ir, results), [ir, results]);
  const drag = useRef<{ x0: number; y0: number; x: number; y: number; moved: boolean; pan: boolean } | null>(null);

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
        setState("ready"); // the effects below draw the scene and the selection now
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
      <div ref={wrap} className={styles.stage}>
        <canvas
          ref={canvas}
          className={styles.canvas}
          tabIndex={0}
          role="img"
          aria-label={t("twin3d.label", { count: scene.nodes.length })}
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
      </div>
      <p id={hintId} className={styles.hint}>
        {selected !== null && <span className={styles.selected}>{t("twin3d.selected", { name: label(selected) })}</span>}
        {t("twin3d.hint")}
      </p>
      {results && <p className={styles.hint}>{t("twin3d.legend")}</p>}
    </div>
  );
}
