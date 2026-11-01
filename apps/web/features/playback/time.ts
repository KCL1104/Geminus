// The run's time as the line in motion shows it (H-072): "Day 2, 14:30",
// counted from the run's start (tick 0 is day 1, 00:00; the electronics
// line's t=0 is Monday 00:00 model time, D-052) in ticks of 1 ms
// (SIMULATION_SPEC §2). A model in seconds shows the seconds too. The end
// of a whole day reads as that day's 24:00, not the next day's 00:00.
import type { ModelTimeUnit } from "../../lib/contracts";
import type { Locale } from "../../lib/i18n/locale";
import { formatDuration, type Translator } from "../../lib/i18n/translate";

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const two = (n: number) => String(n).padStart(2, "0");

/** The day (from 1) and the clock ("14:30", or "14:30:05" for a model in seconds) at a tick. */
export function dayAndClock(tick: number, unit: ModelTimeUnit): { day: number; clock: string } {
  const t = Math.max(0, Math.floor(tick));
  const endOfDay = t > 0 && t % DAY === 0;
  const day = endOfDay ? t / DAY : Math.floor(t / DAY) + 1;
  const into = endOfDay ? DAY : t % DAY;
  const h = Math.floor(into / HOUR);
  const m = Math.floor((into % HOUR) / MINUTE);
  const s = Math.floor((into % MINUTE) / SECOND);
  return { day, clock: unit === "second" ? `${two(h)}:${two(m)}:${two(s)}` : `${two(h)}:${two(m)}` };
}

/** "Day 2, 14:30" (zh-Hant 「第 2 天 14:30」). */
export function runTime(t: Translator, tick: number, unit: ModelTimeUnit): string {
  const { day, clock } = dayAndClock(tick, unit);
  return t("playback.clock", { day, time: clock });
}

const units = [["day", DAY], ["hour", HOUR], ["minute", MINUTE], ["second", SECOND]] as const;

/** A run's length in the largest unit it has at least one of, to a tenth: "1 day", "10 hours", "1.5 hours". */
export function runLength(locale: Locale, ticks: number): string {
  const [unit, ms] = units.find(([, ms]) => ticks >= ms) ?? units[3];
  return formatDuration(locale, Math.round((ticks / ms) * 10) / 10, unit);
}
