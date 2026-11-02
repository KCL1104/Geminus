"use client";
// H-079: whether the primary pointer is coarse (matchMedia "(pointer:
// coarse)": a phone or a tablet without a mouse), following its change (a
// keyboard and trackpad docked or undocked). Ask AI's composer reads it: a
// touch keyboard has no Shift+Enter, so there Enter makes a new line and only
// the send button sends. On the server, and where matchMedia is missing, the
// pointer is taken as fine (desktop), and the first render on the client
// follows it.
import { useSyncExternalStore } from "react";

const QUERY = "(pointer: coarse)";

function list(): MediaQueryList | null {
  return typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(QUERY) : null;
}

function subscribe(onChange: () => void): () => void {
  const l = list();
  if (l === null) return () => {};
  if (typeof l.addEventListener === "function") {
    l.addEventListener("change", onChange);
    return () => l.removeEventListener("change", onChange);
  }
  // Safari before 14 has only the older listener methods.
  l.addListener(onChange);
  return () => l.removeListener(onChange);
}

/** The primary pointer is coarse; false on the server. */
export function useCoarsePointer(): boolean {
  return useSyncExternalStore(subscribe, () => list()?.matches ?? false, () => false);
}
