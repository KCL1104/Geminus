"use client";
import { useSyncExternalStore } from "react";
import type { SelectionStore } from "./selection";

/** The selected entity ID, re-rendering on change; the server render has no selection. */
export function useSelectedEntity(store: SelectionStore): string | null {
  return useSyncExternalStore(store.subscribe, store.get, () => null);
}
