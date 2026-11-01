// H-072: the playback clock of the line in motion (features/playback/clock.ts;
// DESIGN.md, The Trace Motion Rule). A whole run plays in 30 s at 1×, loops,
// pauses (and the pause holds), under reduced motion starts paused and then
// jumps a round step every 650 ms with no slide, and tells its listeners the
// time without React. Driven by a fake scheduler: no real time passes.
import { describe, expect, it } from "vitest";
import { createClock, HOP_MS, PLAY_SECONDS, STEP_MS, stepFor } from "../features/playback/clock";
import { dayAndClock } from "../features/playback/time";
import { fakeScheduler } from "./fake-scheduler";

const DAY = 86_400_000;
const MINUTE = 60_000;

describe("the playback clock", () => {
  it("plays a whole run in 30 s at 1×, then starts again from 0", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    expect(clock.state()).toEqual({ playing: true, speed: 1, reduced: false });
    f.run(15_000);
    expect(clock.now()).toBeCloseTo(DAY / 2, -3);
    f.run(15_000);
    expect(clock.now()).toBe(DAY); // the end itself is shown
    f.run(16);
    expect(clock.now()).toBe(0); // then it loops
    expect(PLAY_SECONDS).toBe(30);
  });

  it("plays 2× and 4× faster, and its hop's slide covers 300 ms of playback", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler, speed: 2 });
    f.run(5_000);
    expect(clock.now()).toBeCloseTo(DAY / 3, -3);
    expect(clock.hopTicks()).toBeCloseTo((DAY * 2 * HOP_MS) / 30_000, 6); // 28.8 minutes of the run
    clock.setSpeed(4);
    expect(clock.hopTicks()).toBeCloseTo((DAY * 4 * HOP_MS) / 30_000, 6);
  });

  it("never skips more than a short frame's worth after a long gap (a hidden tab)", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    f.run(10_000, 10_000);
    expect(clock.now()).toBeCloseTo((100 / 30_000) * DAY, -2);
  });

  it("pauses, and a paused clock does not start again by itself", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    f.run(1_000);
    clock.pause();
    const at = clock.now();
    expect(f.pending()).toBe(0);
    f.run(5_000);
    expect(clock.now()).toBe(at);
    clock.autoplay();
    expect(clock.state().playing).toBe(false);
    clock.setDuration(2 * DAY); // another run: still paused, from 0
    clock.autoplay();
    expect(clock.state().playing).toBe(false);
    expect(clock.now()).toBe(0);
    clock.toggle();
    expect(clock.state().playing).toBe(true);
    clock.halt(); // an unmount: not a pause
    clock.autoplay();
    expect(clock.state().playing).toBe(true);
  });

  it("tells its listeners each new time, and nothing once they leave", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler, playing: false });
    const seen: number[] = [];
    const off = clock.subscribe((t) => seen.push(t));
    clock.seek(DAY / 4);
    clock.seek(DAY / 4); // no change, no call
    clock.seek(2 * DAY); // clamped to the end
    clock.setSpeed(2); // the slide's length changed: drawn again
    expect(seen).toEqual([DAY / 4, DAY, DAY]);
    off();
    clock.seek(0);
    expect(seen).toHaveLength(3);
  });

  it("keeps playing when a listener throws", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    clock.subscribe(() => {
      throw new Error("a renderer failed");
    });
    expect(() => f.run(16)).toThrow("a renderer failed");
    expect(f.pending()).toBe(1);
  });

  it("calls its watchers only when its state changes", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    let calls = 0;
    clock.watch(() => calls++);
    f.run(1_000);
    expect(calls).toBe(0); // frames are not state
    clock.pause();
    clock.pause();
    clock.setSpeed(4);
    expect(calls).toBe(2);
  });

  it("plays nothing for a run of no length", () => {
    const f = fakeScheduler();
    const clock = createClock(0, { scheduler: f.scheduler });
    expect(clock.state().playing).toBe(false);
    clock.play();
    expect(f.pending()).toBe(0);
  });
});

describe("the clock under reduced motion", () => {
  it("starts paused, then jumps one round step every 650 ms with no slide", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler, reduced: true });
    expect(clock.state()).toEqual({ playing: false, speed: 1, reduced: true });
    f.run(5_000);
    expect(clock.now()).toBe(0);
    expect(clock.hopTicks()).toBe(0);
    expect(clock.stepTicks()).toBe(30 * MINUTE); // 650 ms of 1× is 31.2 minutes of a day: 30 minutes
    clock.play();
    f.run(STEP_MS - 1);
    expect(clock.now()).toBe(0);
    f.run(1);
    expect(clock.now()).toBe(30 * MINUTE);
    f.run(3 * STEP_MS);
    expect(clock.now()).toBe(120 * MINUTE);
    expect(dayAndClock(clock.now(), "minute")).toEqual({ day: 1, clock: "02:00" });
  });

  it("pauses when the reader turns reduced motion on, and plays smoothly when it is turned off", () => {
    const f = fakeScheduler();
    const clock = createClock(DAY, { scheduler: f.scheduler });
    f.run(1_000);
    clock.setReduced(true);
    expect(clock.state()).toMatchObject({ playing: false, reduced: true });
    clock.play();
    clock.setReduced(false);
    expect(clock.state()).toMatchObject({ playing: true, reduced: false });
    expect(clock.hopTicks()).toBeGreaterThan(0);
  });

  it("rounds its step down to a round time for any run", () => {
    expect([600 * MINUTE, DAY, 7 * DAY, 28 * DAY].map((d) => stepFor(d, 1) / MINUTE)).toEqual([10, 30, 180, 720]);
    expect(stepFor(7 * DAY, 4) / MINUTE).toBe(720);
    expect(stepFor(1_000, 1)).toBe(22); // a run of a second: no round step fits, so the plain share
  });
});
