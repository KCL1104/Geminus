// @vitest-environment happy-dom
// H-072: the playback bar of the line in motion (features/playback/playback-bar.tsx).
// It plays once there is a trace (autoplay) and Pause comes first (WCAG
// 2.2.2); under reduced motion it starts paused; the slider moves by the
// clock's step from the keyboard and pauses; the speed is a segment; it says
// the motion is sampled and where a cut-short trace stops, in both
// languages; and it does not re-render while it plays.
import { Profiler, type ReactNode } from "react";
import { act } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { TraceArtifact } from "../lib/contracts";
import type { Locale } from "../lib/i18n/locale";
import { type Scheduler, usePlaybackClock } from "../features/playback/clock";
import { type BarTrace, PlaybackBar } from "../features/playback/playback-bar";
import { fakeScheduler } from "./fake-scheduler";
import { key, mount, press, rerender, settle, violations } from "./ui-harness";

const DAY = 86_400_000;
const sampled = (over: Partial<TraceArtifact> = {}): TraceArtifact => ({
  format: "geminus.trace.v1",
  engine_version: "des-go-0.5.0",
  replication_index: 0,
  sampling: { method: "entity_id_sha256", rate: 0.125, max_events: 100_000 },
  truncated: false,
  events: [],
  ...over,
});
const whole: BarTrace[] = [{ side: "baseline", trace: sampled(), end: DAY }, { side: "scenario", trace: sampled(), end: DAY }];

let commits = 0;
function Stage({ duration, scheduler, traces = whole, domain = "electronics_line" }: { duration: number | null; scheduler: Scheduler; traces?: readonly BarTrace[]; domain?: string }) {
  const clock = usePlaybackClock(duration, { scheduler });
  return clock ? (
    <Profiler id="bar" onRender={() => void commits++}>
      <PlaybackBar clock={clock} unit="minute" domain={domain} traces={traces} />
    </Profiler>
  ) : null;
}

function reducedMotion(on: boolean) {
  vi.spyOn(window, "matchMedia").mockImplementation(
    (q: string) => ({ matches: on && q.includes("reduce"), media: q, addEventListener: () => {}, removeEventListener: () => {} }) as unknown as MediaQueryList,
  );
}
afterEach(() => vi.restoreAllMocks());

async function bar(node: ReactNode, locale: Locale = "en") {
  const el = await mount(node, locale);
  await settle();
  const group = el.querySelector<HTMLElement>("[role=group]")!;
  const slider = el.querySelector<HTMLInputElement>("input[type=range]")!;
  const toggle = group.querySelector<HTMLButtonElement>("button")!;
  return { el, group, slider, toggle };
}
const spoken = (slider: HTMLInputElement) => slider.getAttribute("aria-valuetext");
/** The playhead's time: the flag over the slider. */
const flag = (el: HTMLElement) => el.querySelector("input[type=range]")!.previousElementSibling!.textContent;

describe("the playback bar", () => {
  it("plays once there is a trace, with Pause first, the time, the speed and the sampling", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { el, group, slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    expect(group.getAttribute("aria-label")).toBe("Playback");
    expect(toggle.getAttribute("aria-label")).toBe("Pause");
    expect(toggle.textContent).toBe(""); // an icon key: the name is its label
    // Pause/Play first, then the slider, then the speed.
    const controls = [...group.querySelectorAll("button, input")];
    expect(controls[0]).toBe(toggle);
    expect(controls[1]).toBe(slider);
    expect(slider.getAttribute("aria-label")).toBe("Time in the run");
    expect(spoken(slider)).toBe("Day 1, 00:00 of 1 day");
    const speed = el.querySelector("[aria-label='Playback speed']")!;
    expect([...speed.querySelectorAll("button")].map((b) => [b.textContent, b.getAttribute("aria-checked")])).toEqual([["1×", "true"], ["2×", "false"], ["4×", "false"]]);
    expect(el.querySelector("p")!.textContent).toBe("Sampled: 1 board in 8, replication 1.");
    expect(await violations()).toEqual([]);
  });

  it("moves the slider and the playhead's time with the clock, without rendering React", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { el, slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    const before = commits;
    await act(async () => f.run(15_000));
    expect(commits).toBe(before);
    expect(Number(slider.value)).toBe(DAY / 2);
    expect(flag(el)).toBe("Day 1, 12:00");
    // While playing, the spoken value is not rewritten every frame (at most once a second).
    expect(spoken(slider)).toBe("Day 1, 00:00 of 1 day");
    await press(toggle);
    expect(toggle.getAttribute("aria-label")).toBe("Play");
    expect(spoken(slider)).toBe("Day 1, 12:00 of 1 day");
    await act(async () => f.run(5_000));
    expect(Number(slider.value)).toBe(DAY / 2);
  });

  it("moves by the clock's step from the keyboard, and pauses", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { el, slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    await key(slider, "ArrowRight");
    expect(toggle.getAttribute("aria-label")).toBe("Play");
    expect(spoken(slider)).toBe("Day 1, 00:30 of 1 day");
    await key(slider, "PageUp");
    expect(spoken(slider)).toBe("Day 1, 05:30 of 1 day");
    await key(slider, "ArrowLeft");
    expect(spoken(slider)).toBe("Day 1, 05:00 of 1 day");
    await key(slider, "End");
    expect(spoken(slider)).toBe("Day 1, 24:00 of 1 day");
    expect(flag(el)).toBe("Day 1, 24:00");
    await key(slider, "Home");
    expect(spoken(slider)).toBe("Day 1, 00:00 of 1 day");
  });

  it("seeks where the slider is dragged, and pauses", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(slider, String(DAY / 4));
      slider.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(toggle.getAttribute("aria-label")).toBe("Play");
    expect(spoken(slider)).toBe("Day 1, 06:00 of 1 day");
  });

  it("changes the speed", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { el, slider } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    await press([...el.querySelectorAll("button")].find((b) => b.textContent === "4×"));
    await act(async () => f.run(7_500));
    expect(Number(slider.value)).toBe(DAY);
  });

  it("starts paused under reduced motion; Play then jumps a step at a time", async () => {
    reducedMotion(true);
    const f = fakeScheduler();
    const { slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    expect(toggle.getAttribute("aria-label")).toBe("Play");
    await act(async () => f.run(5_000));
    expect(Number(slider.value)).toBe(0);
    await press(toggle);
    expect(toggle.getAttribute("aria-label")).toBe("Pause");
    await act(async () => f.run(650 * 2));
    expect(Number(slider.value)).toBe(60 * 60_000);
  });

  it("stays paused for the visit when another run comes", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} />);
    await press(toggle);
    await rerender(<Stage duration={2 * DAY} scheduler={f.scheduler} />);
    await settle();
    expect(document.querySelector("[role=group] button")!.getAttribute("aria-label")).toBe("Play");
  });

  it("shows nothing while there is nothing to play", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const el = await mount(<Stage duration={null} scheduler={f.scheduler} />);
    await settle();
    expect(el.querySelector("[role=group]")).toBeNull();
    expect(f.pending()).toBe(0);
  });

  it("says where a cut-short trace stops, and the generic sampling for another pack", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const traces: BarTrace[] = [
      { side: "baseline", trace: sampled({ truncated: true }), end: 13 * 3_600_000 + 20 * 60_000 },
      { side: "scenario", trace: sampled(), end: DAY },
    ];
    const { el } = await bar(<Stage duration={DAY} scheduler={f.scheduler} traces={traces} domain="software_delivery" />);
    expect(el.querySelector("p")!.textContent).toBe("Sampled: 1 work item in 8, replication 1. The baseline's trace stops at Day 1, 13:20.");
  });

  it("says a single run's stop without naming a side", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const { el } = await bar(<Stage duration={DAY} scheduler={f.scheduler} traces={[{ side: null, trace: sampled({ truncated: true }), end: 3_600_000 }]} />);
    expect(el.querySelector("p")!.textContent).toBe("Sampled: 1 board in 8, replication 1. The trace stops at Day 1, 01:00.");
  });

  it("is in Traditional Chinese", async () => {
    reducedMotion(false);
    const f = fakeScheduler();
    const traces: BarTrace[] = [{ side: "scenario", trace: sampled({ truncated: true }), end: DAY / 2 }];
    const { el, slider, toggle } = await bar(<Stage duration={DAY} scheduler={f.scheduler} traces={traces} />, "zh-Hant");
    expect(el.querySelector("[role=group]")!.getAttribute("aria-label")).toBe("播放");
    expect(toggle.getAttribute("aria-label")).toBe("暫停");
    expect(slider.getAttribute("aria-label")).toBe("執行中的時間");
    expect(spoken(slider)).toBe("第 1 天 00:00／1 天");
    expect([...el.querySelectorAll("[aria-label='播放速度'] button")].map((b) => b.textContent)).toEqual(["1 倍", "2 倍", "4 倍"]);
    expect(el.querySelector("p")!.textContent).toBe("抽樣：每 8 片電路板取 1 片，第 1 次重複。情境的追蹤停在第 1 天 12:00。");
    expect(await violations()).toEqual([]);
  });
});
