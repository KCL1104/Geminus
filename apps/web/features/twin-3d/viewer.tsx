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
import { buildScene } from "./scene";
import styles from "./twin-3d.module.css";

type Props = {
  ir: ModelIR;
  /** A run's results: processes coloured by utilization, queues raised by length (T-033 scope). */
  results?: Result | null;
  selected: string | null;
  onSelect: (id: string | null) => void;
};

function palette(el: Element): Palette {
  const css = getComputedStyle(el);
  const v = (name: string) => css.getPropertyValue(`--gm-color-${name}`).trim();
  return { sheet: v("sheet"), ink: v("ink"), edge: v("edge"), rule: v("rule"), baseline: v("baseline"), scenario: v("scenario"), busy: v("warning-edge") };
}

const KEY_ORBIT = 10;

export function Twin3D({ ir, results, selected, onSelect }: Props) {
  const { t } = useI18n();
  const hintId = useId();
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const view = useRef<View | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "failed">("loading");
  const [hover, setHover] = useState<{ id: string; x: number; y: number } | null>(null);
  const scene = useMemo(() => buildScene(ir, results), [ir, results]);
  const drag = useRef<{ x: number; y: number; moved: boolean; pan: boolean } | null>(null);

  useEffect(() => {
    let cancelled = false;
    let observer: ResizeObserver | undefined;
    void (async () => {
      try {
        const pc = await import("playcanvas");
        const { createView } = await import("./engine");
        if (cancelled || !canvas.current || !wrap.current) return;
        const v = createView(pc, canvas.current, palette(wrap.current));
        view.current = v;
        const fit = () => wrap.current && v.resize(wrap.current.clientWidth, wrap.current.clientHeight);
        fit();
        observer = new ResizeObserver(fit);
        observer.observe(wrap.current);
        setState("ready"); // the effects below draw the scene and the selection now
      } catch {
        if (!cancelled) setState("failed");
      }
    })();
    return () => {
      cancelled = true;
      observer?.disconnect();
      view.current?.destroy();
      view.current = null;
    };
  }, []);
  useEffect(() => view.current?.setScene(scene), [scene, state]);
  useEffect(() => view.current?.setSelected(selected), [selected, state]);

  if (state === "failed") return <Callout tone="info">{t("twin3d.unavailable")}</Callout>;

  const at = (e: PointerEvent<HTMLCanvasElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  const label = (id: string) => scene.nodes.find((n) => n.id === id)?.label ?? id;
  const keys = (e: KeyboardEvent<HTMLCanvasElement>) => {
    const v = view.current;
    if (!v) return;
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
            e.currentTarget.setPointerCapture(e.pointerId);
            drag.current = { ...at(e), moved: false, pan: e.button === 2 || e.shiftKey };
          }}
          onPointerMove={(e) => {
            const p = at(e);
            const d = drag.current;
            if (d) {
              const dx = p.x - d.x;
              const dy = p.y - d.y;
              if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true;
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
            if (d && !d.moved && e.button === 0) onSelect(view.current?.stepAt(d.x, d.y) ?? null);
          }}
          onPointerLeave={() => setHover(null)}
          onWheel={(e) => view.current?.zoom(e.deltaY > 0 ? 1.1 : 0.9)}
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
