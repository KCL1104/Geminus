// One selectedEntityId shared by the 2D editor and the 3D viewer
// (docs/ARCHITECTURE.md §7, TEST-025). It holds an IR ID and nothing else, so
// switching renderers never needs a second topology; an ID that is not in the
// current IR is refused, and a selection disappears with its entity.
import type { ModelIR } from "../contracts";

export type SelectionStore = {
  get(): string | null;
  /** Selects id (or clears with null); false when id is not an entity of the current model. */
  select(id: string | null): boolean;
  /** Replaces the model the IDs refer to; a selection that no longer exists is cleared. */
  setModel(ir: ModelIR | null): void;
  subscribe(listener: () => void): () => void;
};

/** The IDs a renderer may select: nodes, resources and flows. */
export function entityIds(ir: ModelIR): Set<string> {
  return new Set<string>([...ir.nodes.map((n) => n.id), ...ir.resources.map((r) => r.id), ...ir.flows.map((f) => f.id)]);
}

export function createSelectionStore(ir: ModelIR | null = null): SelectionStore {
  let ids = ir === null ? new Set<string>() : entityIds(ir);
  let selected: string | null = null;
  const listeners = new Set<() => void>();
  const set = (next: string | null) => {
    if (next === selected) return;
    selected = next;
    for (const l of [...listeners]) l();
  };
  return {
    get: () => selected,
    select(id) {
      if (id !== null && !ids.has(id)) return false;
      set(id);
      return true;
    },
    setModel(next) {
      ids = next === null ? new Set<string>() : entityIds(next);
      if (selected !== null && !ids.has(selected)) set(null);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}
