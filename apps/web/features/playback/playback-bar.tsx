"use client";
// The playback bar of the line in motion (H-072; DESIGN.md, The Trace Motion
// Rule): Pause/Play first (a 32 px icon button with its name for screen
// readers, WCAG 2.2.2), the time slider with the run's time on its playhead
// ("Day 2, 14:30"), the speed (1× / 2× / 4×), then what the motion is drawn
// from: "Sampled: 1 board in 8, replication 1", and where a cut-short trace
// stops. One bar drives every stage of a page through its clock (clock.ts).
//
// It does not re-render per frame: it subscribes to the clock and writes
// the slider's value and the playhead's time itself. The slider's spoken
// value follows the time at most once a second while playing, and at once
// when paused, sought or focused, so a screen reader is not flooded. Moving
// the slider (pointer or keys) pauses; the arrow keys move one step of the
// clock (stepTicks), Page Up and Page Down ten, Home and End to the ends.
import { type KeyboardEvent, useEffect, useRef } from "react";
import type { ModelTimeUnit, TraceArtifact } from "../../lib/contracts";
import { useI18n } from "../../lib/i18n/provider";
import type { MessageKey } from "../../lib/i18n/translate";
import { en } from "../../messages/en";
import { Button } from "../../ui/button";
import { PauseIcon, PlayIcon } from "../../ui/icons";
import { Segmented } from "../../ui/segmented";
import { type PlaybackClock, SPEEDS, type Speed, useClockFrame, useClockState } from "./clock";
import styles from "./playback-bar.module.css";
import { runLength, runTime } from "./time";

/** A trace a stage plays: whose run it is (null on a page with one run) and where its motion ends (motion.ts `end`). */
export type BarTrace = { readonly side: "baseline" | "scenario" | null; readonly trace: TraceArtifact; readonly end: number };

/** The slider thumb's width, for centring the playhead's time over it (the browsers' default is about 16 px). */
const THUMB = 16;
/** While playing, the slider's spoken value changes at most this often. */
const SPOKEN_MS = 1000;

/** A pack's own words for a message (domain.<pack>.<key>), else the generic one. */
export function packKey(domain: string, key: MessageKey): MessageKey {
  const k = `domain.${domain}.${key}`;
  return (Object.hasOwn(en, k) ? k : key) as MessageKey;
}

export function PlaybackBar({ clock, unit, domain, traces }: { clock: PlaybackClock; unit: ModelTimeUnit; domain: string; traces: readonly BarTrace[] }) {
  const { t, locale, gap } = useI18n();
  const state = useClockState(clock);
  const slider = useRef<HTMLInputElement>(null);
  const flag = useRef<HTMLSpanElement>(null);
  const scrub = useRef<HTMLDivElement>(null);
  const shown = useRef({ text: "", length: -1, flagWidth: 0, width: 0, spoken: "", spokenAt: Number.NEGATIVE_INFINITY });

  const at = (tick: number) => runTime(t, tick, unit);
  const spoken = (tick: number) => t("playback.at", { time: at(tick), end: runLength(locale, clock.duration) });

  // Writes the time into the slider and onto its playhead; `now` speaks it at once.
  const draw = (tick: number, now = false) => {
    const s = shown.current;
    const input = slider.current;
    const label = flag.current;
    if (!input || !label) return;
    input.value = String(tick);
    const text = at(tick);
    if (text !== s.text) {
      label.textContent = text;
      s.text = text;
      if (text.length !== s.length) {
        s.length = text.length;
        s.flagWidth = label.offsetWidth;
      }
    }
    const p = clock.duration > 0 ? tick / clock.duration : 0;
    const x = Math.min(Math.max(0, p * (s.width - THUMB) + THUMB / 2 - s.flagWidth / 2), Math.max(0, s.width - s.flagWidth));
    label.style.transform = `translateX(${Math.round(x)}px)`;
    const wall = performance.now();
    if (now || !clock.state().playing || wall - s.spokenAt >= SPOKEN_MS) {
      const words = spoken(tick);
      if (words !== s.spoken) input.setAttribute("aria-valuetext", words);
      s.spoken = words;
      s.spokenAt = wall;
    }
  };
  useClockFrame(clock, (tick) => draw(tick));
  // A pause, a language change or another run: the spoken value at once.
  useEffect(() => {
    shown.current.text = "";
    draw(clock.now(), true);
  });
  // The playhead's track width, for centring the time over the thumb.
  useEffect(() => {
    const el = scrub.current;
    if (!el) return;
    const measure = () => {
      shown.current.width = el.clientWidth;
      shown.current.length = -1;
      shown.current.text = "";
      draw(clock.now(), true);
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
    // The width is measured once per size; draw reads the clock itself.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clock]);

  const seek = (tick: number) => {
    clock.pause();
    clock.seek(tick);
    draw(clock.now(), true);
  };
  const keys = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const step = clock.stepTicks();
    const by: Record<string, number> = { ArrowRight: step, ArrowUp: step, ArrowLeft: -step, ArrowDown: -step, PageUp: 10 * step, PageDown: -10 * step };
    let target: number | null = null;
    if (e.key in by) target = clock.now() + by[e.key]!;
    else if (e.key === "Home") target = 0;
    else if (e.key === "End") target = clock.duration;
    if (target === null) return;
    e.preventDefault();
    seek(target);
  };

  const samplings = [...new Map(traces.map(({ trace }) => {
    const every = Math.round(1 / trace.sampling.rate);
    // Replications are counted from 1 on screen, as everywhere else in the app (the Marey chart's note).
    const replication = trace.replication_index + 1;
    return [`${every}/${replication}`, t(packKey(domain, "playback.sampled"), { every, replication })] as const;
  })).values()];
  const stops = traces
    .filter(({ trace }) => trace.truncated)
    .map(({ side, end }) => t(side === null ? "playback.stops" : `playback.stops.${side}`, { time: at(end) }));

  return (
    <div role="group" aria-label={t("playback.bar")} className={styles.bar}>
      <div className={styles.controls}>
        <Button
          className={styles.toggle}
          aria-label={state.playing ? t("playback.pause") : t("playback.play")}
          icon={state.playing ? <PauseIcon /> : <PlayIcon />}
          onPress={() => clock.toggle()}
        />
        <div ref={scrub} className={styles.scrub}>
          <span ref={flag} className={styles.flag} aria-hidden="true" />
          <input
            ref={slider}
            className={styles.slider}
            type="range"
            min={0}
            max={clock.duration}
            step="any"
            defaultValue={0}
            aria-label={t("playback.time")}
            onChange={(e) => seek(Number(e.currentTarget.value))}
            onKeyDown={keys}
            onFocus={() => draw(clock.now(), true)}
          />
        </div>
        <Segmented
          label={t("playback.speed")}
          items={SPEEDS.map((s) => ({ id: String(s), label: t("playback.speedValue", { speed: s }) }))}
          value={String(state.speed)}
          onChange={(v) => clock.setSpeed(Number(v) as Speed)}
        />
      </div>
      <p className={styles.note}>{[...samplings, ...stops].join(gap)}</p>
    </div>
  );
}
