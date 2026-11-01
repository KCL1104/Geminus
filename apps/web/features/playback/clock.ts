// The playback clock of the line in motion (H-072; DESIGN.md, The Trace
// Motion Rule): one clock drives every stage of a page (the results page's
// baseline and scenario, the model page's 2D and 3D), so they show one time.
// It holds the run time shown, in ticks (ms of model time, SIMULATION_SPEC
// §2), and never re-renders React per frame: renderers subscribe and draw
// from motion.ts on each time change; React reads only the rare state
// (playing, speed, reduced motion) through useClockState.
//
// - A whole run plays in PLAY_SECONDS at 1×; 2× and 4× for long runs.
// - Playing, it loops: after the end it starts again from 0.
// - It starts playing once there is something to play (autoplay), unless the
//   reader prefers reduced motion: then it starts paused, and Play jumps one
//   step every STEP_MS (the run time 650 ms of 1× playback covers, rounded
//   down to a round unit: 30 minutes of a 1-day run) with no slide
//   (hopTicks() is 0). This is the only continuous motion beyond the 180 ms
//   state transitions, and Pause stops it (WCAG 2.2.2).
// - hopTicks(): the run time a 300 ms slide (HOP_MS) covers at this speed;
//   motion.at(t, { hop }) draws each hop over it.
// - A pause holds for the page visit: usePlaybackClock keeps one clock per
//   page, and once paused (by the reader, or by the page while editing),
//   another run or a reload of its trace does not start it again; Play does.
import { useEffect, useRef, useState, useSyncExternalStore } from "react";

/** A whole run plays in this many seconds at 1×. */
export const PLAY_SECONDS = 30;
/** A hop's slide, in ms of playback. */
export const HOP_MS = 300;
/** Under reduced motion, Play jumps one step this often (DESIGN.md's playhead rule). */
export const STEP_MS = 650;
export const SPEEDS = [1, 2, 4] as const;
export type Speed = (typeof SPEEDS)[number];

/** A frame's wall time can lag behind (a hidden tab, a long task): playback never skips more than this at once. */
const MAX_FRAME_MS = 100;

/** The timers the clock runs on (the browser's by default; a test passes its own). */
export type Scheduler = {
  now(): number;
  frame(callback: (now: number) => void): number;
  cancelFrame(id: number): void;
  every(callback: () => void, ms: number): number;
  cancelEvery(id: number): void;
};

export const browserScheduler: Scheduler = {
  now: () => performance.now(),
  frame: (callback) => requestAnimationFrame(callback),
  cancelFrame: (id) => cancelAnimationFrame(id),
  every: (callback, ms) => window.setInterval(callback, ms),
  cancelEvery: (id) => window.clearInterval(id),
};

export type ClockState = { readonly playing: boolean; readonly speed: Speed; readonly reduced: boolean };

export type PlaybackClock = {
  /** The run's length in ticks (the slider's end); 0 plays nothing. */
  readonly duration: number;
  /** The run time shown, 0..duration. */
  now(): number;
  /** The run time a hop's 300 ms slide covers at this speed; 0 under reduced motion (no slide). */
  hopTicks(): number;
  /** The run time one reduced-motion step (and one arrow key on the slider) moves. */
  stepTicks(): number;
  state(): ClockState;
  /** Called with the time on every change of it: each frame while playing, each step, seek or speed change. Returns the unsubscribe. */
  subscribe(listener: (tick: number) => void): () => void;
  /** Called when state() changes (for useSyncExternalStore). */
  watch(listener: () => void): () => void;
  play(): void;
  /** Pauses; the clock remembers it, so autoplay() no longer starts it (until play()). Editing pauses through this too. */
  pause(): void;
  toggle(): void;
  seek(tick: number): void;
  setSpeed(speed: Speed): void;
  /** The reader's reduced-motion preference: on, it pauses; off, a stepping playback plays on smoothly. */
  setReduced(reduced: boolean): void;
  /** Another run to play: its length, from 0. Playing stays as it was. */
  setDuration(duration: number): void;
  /** Plays, unless it was paused (by the reader or the page) or the reader prefers reduced motion. */
  autoplay(): void;
  /** Stops its timers without counting as a pause (the page unmounted, or its run changes). */
  halt(): void;
};

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const ROUND = [
  SECOND, 2 * SECOND, 5 * SECOND, 10 * SECOND, 15 * SECOND, 30 * SECOND,
  MINUTE, 2 * MINUTE, 5 * MINUTE, 10 * MINUTE, 15 * MINUTE, 30 * MINUTE,
  HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY,
];

/** The run time playback covers per ms of wall time at a speed. */
export const ticksPerMs = (duration: number, speed: Speed) => (duration * speed) / (PLAY_SECONDS * 1000);

/** One reduced-motion step: what 650 ms of playback covers at this speed, rounded down to a round time (at least 1 tick). */
export function stepFor(duration: number, speed: Speed): number {
  const raw = ticksPerMs(duration, speed) * STEP_MS;
  let step = 0;
  for (const r of ROUND) if (r <= raw) step = r;
  return step > 0 ? step : Math.max(1, Math.round(raw));
}

export function createClock(
  duration: number,
  options: { readonly playing?: boolean; readonly speed?: Speed; readonly reduced?: boolean; readonly scheduler?: Scheduler } = {},
): PlaybackClock {
  const scheduler = options.scheduler ?? browserScheduler;
  let length = Math.max(0, Number.isFinite(duration) ? duration : 0);
  let tick = 0;
  let state: ClockState = { playing: false, speed: options.speed ?? 1, reduced: options.reduced ?? false };
  let held = false; // paused by pause()
  const listeners = new Set<(tick: number) => void>();
  const watchers = new Set<() => void>();
  let frame: number | null = null;
  let timer: number | null = null;
  let last = 0;

  const emit = () => {
    for (const l of [...listeners]) l(tick);
  };
  const setState = (next: Partial<ClockState>) => {
    const was = state;
    state = { ...state, ...next };
    if (state.playing !== was.playing || state.speed !== was.speed || state.reduced !== was.reduced) for (const w of [...watchers]) w();
  };
  const stop = () => {
    if (frame !== null) scheduler.cancelFrame(frame);
    if (timer !== null) scheduler.cancelEvery(timer);
    frame = null;
    timer = null;
  };
  // Past the end, playback starts again from 0; it shows the end itself once.
  const advance = (by: number) => {
    tick = tick >= length ? 0 : Math.min(length, tick + by);
    emit();
  };
  const onFrame = (now: number) => {
    // The next frame first: a listener that throws does not stop the playback.
    frame = scheduler.frame(onFrame);
    const dt = Math.min(MAX_FRAME_MS, Math.max(0, now - last));
    last = now;
    advance(dt * ticksPerMs(length, state.speed));
  };
  const run = () => {
    stop();
    if (!state.playing) return;
    if (state.reduced) {
      timer = scheduler.every(() => advance(stepFor(length, state.speed)), STEP_MS);
    } else {
      last = scheduler.now();
      frame = scheduler.frame(onFrame);
    }
  };

  const clock: PlaybackClock = {
    get duration() {
      return length;
    },
    now: () => tick,
    hopTicks: () => (state.reduced ? 0 : HOP_MS * ticksPerMs(length, state.speed)),
    stepTicks: () => stepFor(length, state.speed),
    state: () => state,
    subscribe(listener) {
      listeners.add(listener);
      return () => void listeners.delete(listener);
    },
    watch(listener) {
      watchers.add(listener);
      return () => void watchers.delete(listener);
    },
    play() {
      held = false;
      if (state.playing || length <= 0) return;
      setState({ playing: true });
      run();
    },
    pause() {
      held = true;
      if (!state.playing) return;
      stop();
      setState({ playing: false });
    },
    toggle() {
      if (state.playing) clock.pause();
      else clock.play();
    },
    seek(t) {
      const next = Math.min(Math.max(Number.isFinite(t) ? t : 0, 0), length);
      if (next === tick) return;
      tick = next;
      emit();
    },
    setSpeed(speed) {
      if (speed === state.speed) return;
      setState({ speed });
      emit(); // the hop's ticks changed
    },
    setReduced(reduced) {
      if (reduced === state.reduced) return;
      if (reduced) stop();
      setState(reduced ? { reduced, playing: false } : { reduced });
      run();
      emit(); // a slide appears or goes
    },
    setDuration(next) {
      const d = Math.max(0, Number.isFinite(next) ? next : 0);
      if (d === length) return;
      length = d;
      tick = 0;
      if (length <= 0) {
        stop();
        setState({ playing: false });
      } else run();
      emit();
    },
    autoplay() {
      if (!held && !state.reduced) clock.play();
    },
    halt() {
      stop();
      setState({ playing: false });
    },
  };
  if (options.playing ?? true) clock.autoplay();
  return clock;
}

const REDUCED = "(prefers-reduced-motion: reduce)";

/**
 * The page's one clock, for a run of `duration` ticks; null while there is
 * nothing to play (no trace yet, or an empty one). It plays once there is
 * something to play (autoplay), paused instead under reduced motion or when
 * the reader (or the page, while editing) paused it earlier in this visit.
 * Pass it to every stage and to the PlaybackBar.
 */
export function usePlaybackClock(duration: number | null, options: { readonly autoplay?: boolean; readonly scheduler?: Scheduler } = {}): PlaybackClock | null {
  const [clock] = useState(() => createClock(0, { playing: false, scheduler: options.scheduler }));
  const autoplay = options.autoplay ?? true;
  useEffect(() => {
    const media = typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(REDUCED) : null;
    clock.setReduced(media?.matches ?? false);
    const onMedia = (e: MediaQueryListEvent) => clock.setReduced(e.matches);
    media?.addEventListener?.("change", onMedia);
    return () => media?.removeEventListener?.("change", onMedia);
  }, [clock]);
  useEffect(() => {
    clock.setDuration(duration ?? 0);
    if (autoplay) clock.autoplay();
    return () => clock.halt();
  }, [clock, duration, autoplay]);
  return duration !== null && duration > 0 ? clock : null;
}

const stopped: ClockState = { playing: false, speed: 1, reduced: false };
const none = () => () => {};

/** The clock's state for React (re-renders on play, pause, speed and reduced-motion changes only). */
export function useClockState(clock: PlaybackClock | null): ClockState {
  return useSyncExternalStore(
    clock ? clock.watch : none,
    () => (clock ? clock.state() : stopped),
    () => (clock ? clock.state() : stopped),
  );
}

/** Calls draw(tick) now and on every change of the clock's time, until the component unmounts or the clock changes; no React render. */
export function useClockFrame(clock: PlaybackClock | null, draw: (tick: number) => void): void {
  const latest = useRef(draw);
  useEffect(() => {
    latest.current = draw;
  });
  useEffect(() => {
    if (!clock) return;
    latest.current(clock.now());
    return clock.subscribe((t) => latest.current(t));
  }, [clock]);
}
