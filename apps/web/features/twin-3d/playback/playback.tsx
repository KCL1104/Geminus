"use client";
// 3D playback of a run's sampled trace (T-034; REQ-017, REQ-023): play,
// pause and a time slider over the run, the baseline or the scenario, and a
// column on each step for the sampled work items there (occupancy.ts). The
// trace is the one the results page already loaded with the run's
// authorization; without one there is nothing to play and nothing is made
// up. The view redraws at most every UPDATE_MS while playing, and the page
// only re-renders when the shown time changes.
import { useEffect, useMemo, useRef, useState } from "react";
import type { ModelIR, Result } from "../../../lib/contracts";
import { useI18n } from "../../../lib/i18n/provider";
import { formatDuration } from "../../../lib/i18n/translate";
import type { Locale } from "../../../lib/i18n/locale";
import { Button } from "../../../ui/button";
import { Segmented } from "../../../ui/segmented";
import type { TraceState } from "../../results/load";
import { type LoadView, Twin3D } from "../viewer";
import styles from "../twin-3d.module.css";
import { timeline } from "./occupancy";

type Side = "baseline" | "scenario";
export type PlaybackRun = { result: Result; trace: TraceState };

/** A whole run plays in this many seconds; the slider reaches any time directly. */
const PLAY_SECONDS = 30;
const UPDATE_MS = 100;
const SLIDER_STEPS = 1000;

// Ticks are milliseconds (SIMULATION_SPEC §1). The run's length picks one
// unit for both times, named in words ("5 hours of 10 hours"), to a tenth.
const units = [["day", 86_400_000], ["hour", 3_600_000], ["minute", 60_000], ["second", 1_000]] as const;

/** The time and the run's end in the largest unit the end has at least 2 of. */
export function clock(locale: Locale, ticks: number, end: number): { time: string; end: string } {
  const [unit, ms] = units.find(([, ms]) => end >= 2 * ms) ?? units[3];
  const say = (t: number) => formatDuration(locale, Math.round((t / ms) * 10) / 10, unit);
  return { time: say(ticks), end: say(end) };
}

const playable = (s: TraceState) => (s.state === "loaded" && s.trace.events.length > 0 ? s.trace : null);

export function Playback({ ir, runs, loadView }: { ir: ModelIR; runs: Record<Side, PlaybackRun>; loadView?: LoadView }) {
  const { t, locale } = useI18n();
  const sides = (["baseline", "scenario"] as const).filter((s) => playable(runs[s].trace) !== null);
  const [run, setRun] = useState<Side>(sides.includes("scenario") ? "scenario" : (sides[0] ?? "scenario"));
  const [selected, setSelected] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  const [playing, setPlaying] = useState(false);
  const nodes = useMemo(() => new Set(ir.nodes.map((n) => n.id)), [ir]);
  const trace = playable(runs[run].trace);
  const horizon = runs[run].result.measurement_window.horizon_ticks;
  const tl = useMemo(() => (trace === null ? null : timeline(trace, nodes, horizon)), [trace, nodes, horizon]);
  const end = tl?.end ?? 0;
  // Another run's trace may end sooner (truncated): the time shown stays inside this one (review L4).
  const shownTick = Math.min(tick, end);
  const counts = useMemo(() => (end > 0 ? (tl?.at(shownTick) ?? null) : null), [tl, shownTick, end]);
  const occupancy = useMemo(() => (counts === null ? null : { counts, run }), [counts, run]);

  // Playing: time advances with the clock, the shown time at most every UPDATE_MS; it stops at the end.
  const from = useRef({ tick: 0, at: 0 });
  useEffect(() => {
    if (!playing || end === 0) return;
    from.current = { tick: tick >= end ? 0 : tick, at: performance.now() };
    let frame = 0;
    let shown = 0;
    const advance = (now: number) => {
      // A frame's timestamp can be a little before the effect's performance.now() (review L5).
      const next = Math.min(end, from.current.tick + (Math.max(0, now - from.current.at) / (PLAY_SECONDS * 1000)) * end);
      if (next >= end) {
        setTick(end);
        setPlaying(false);
        return;
      }
      if (now - shown >= UPDATE_MS) {
        shown = now;
        setTick(next);
      }
      frame = requestAnimationFrame(advance);
    };
    frame = requestAnimationFrame(advance);
    return () => cancelAnimationFrame(frame);
    // The start is read once per press of Play; the ticks it sets must not restart it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [playing, end]);

  const total = counts === null ? 0 : Object.values(counts).reduce((a, b) => a + b, 0);
  const name = (id: string) => ir.nodes.find((n) => n.id === id)?.name ?? id;
  const at = clock(locale, shownTick, end);
  // Why a run cannot be played: its trace failed to load, or none was recorded (review M2).
  const unplayable = (["baseline", "scenario"] as const).filter((s) => !sides.includes(s));
  const why =
    sides.length === 0 && unplayable.every((s) => runs[s].trace.state !== "failed")
      ? [t("playback.none")]
      : unplayable.map((s) => (runs[s].trace.state === "failed" ? t(`marey.failed.${s}`) : t(`playback.missing.${s}`)));
  return (
    <div className={styles.twin}>
      <Twin3D ir={ir} results={runs[run].result} selected={selected} onSelect={setSelected} occupancy={occupancy} loadView={loadView} />
      {why.length > 0 && <p className={styles.hint}>{why.join(" ")}</p>}
      {trace === null || end === 0 ? null : (
        <div className={styles.playback}>
          <div className={styles.controls}>
            {sides.length > 1 && (
              <Segmented
                label={t("playback.run")}
                items={sides.map((s) => ({ id: s, label: t(`compare.${s}`) }))}
                value={run}
                onChange={(s) => {
                  setPlaying(false);
                  setRun(s);
                }}
              />
            )}
            <Button onPress={() => setPlaying((p) => !p)}>{playing ? t("playback.pause") : t("playback.play")}</Button>
            <label className={styles.time}>
              <span>{t("playback.time")}</span>
              <input
                type="range"
                min={0}
                max={SLIDER_STEPS}
                value={Math.round((shownTick / end) * SLIDER_STEPS)}
                aria-valuetext={t("playback.at", at)}
                onChange={(e) => {
                  setPlaying(false);
                  setTick((Number(e.currentTarget.value) / SLIDER_STEPS) * end);
                }}
              />
              {/* Not an <output>: a status region would announce every update while playing; the slider's value text says it (review M1). */}
              <span className={styles.clock} aria-hidden="true">
                {t("playback.at", at)}
              </span>
            </label>
          </div>
          <p className={styles.hint}>
            {t("playback.total", { count: total })}{" "}
            {selected !== null && t("playback.here", { name: name(selected), count: counts?.[selected] ?? 0 })}
          </p>
          <p className={styles.hint}>
            {t("playback.legend")} {t("marey.sampling", { replication: trace.replication_index + 1, every: Math.round(1 / trace.sampling.rate) })}
            {trace.truncated && (
              <>
                {" "}
                {t("playback.truncated")}
              </>
            )}
          </p>
        </div>
      )}
    </div>
  );
}
