"use client";
// The page's playback clock (clock.ts) as the line in motion's pages use it
// (H-072): it plays once there is a run to play (autoplay), paused instead
// under reduced motion. A review page that needs a fixed picture (the
// ui-gallery's screenshots, docs/design/H-072_TRACE_ANIMATION.md §7) wraps
// the page in <StillPlayback at={tick}>: the clock then starts paused at
// that time of the run, and Play still plays it.
import { createContext, type ReactNode, useContext, useEffect } from "react";
import { type PlaybackClock, usePlaybackClock } from "./clock";

const Still = createContext<number | null>(null);

/** Holds every playback inside it paused at `at` (ticks into the run) until Play. */
export function StillPlayback({ at, children }: { at: number; children: ReactNode }) {
  return <Still.Provider value={at}>{children}</Still.Provider>;
}

/** The page's one clock for a run of `duration` ticks (null: nothing to play); see usePlaybackClock. */
export function usePageClock(duration: number | null): PlaybackClock | null {
  const still = useContext(Still);
  const clock = usePlaybackClock(duration, { autoplay: still === null });
  // After usePlaybackClock's own effect, which sets the run's length (and so the time to 0).
  useEffect(() => {
    if (clock === null || still === null) return;
    clock.pause();
    clock.seek(still);
  }, [clock, still, duration]);
  return clock;
}
