// A scheduler for the playback clock's tests: frames and intervals run only
// when a test lets wall time pass (no real time passes).
import type { Scheduler } from "../features/playback/clock";

export function fakeScheduler() {
  let now = 0;
  let id = 0;
  const frames = new Map<number, (n: number) => void>();
  const timers = new Map<number, { cb: () => void; ms: number; next: number }>();
  const scheduler: Scheduler = {
    now: () => now,
    frame: (cb) => (frames.set(++id, cb), id),
    cancelFrame: (i) => void frames.delete(i),
    every: (cb, ms) => (timers.set(++id, { cb, ms, next: now + ms }), id),
    cancelEvery: (i) => void timers.delete(i),
  };
  return {
    scheduler,
    /** Lets `ms` of wall time pass, in frames of `frame` ms. */
    run(ms: number, frame = 16) {
      const end = now + ms;
      while (now < end) {
        now = Math.min(end, now + frame);
        const due = [...frames.values()];
        frames.clear();
        for (const cb of due) cb(now);
        for (const [key, t] of [...timers]) {
          while (timers.has(key) && t.next <= now) {
            t.next += t.ms;
            t.cb();
          }
        }
      }
    },
    pending: () => frames.size + timers.size,
  };
}
