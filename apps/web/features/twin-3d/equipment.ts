// What each step of a model is drawn as in the 3D stage (H-072; brief §3,
// §7): presentation only, keyed by the pack's ID and the resource's ID in
// the web's domain registry, so the kernel stays neutral (AGENTS.md §2).
// The electronics line pack (electronics_line, P0) has its own equipment,
// chosen by the resource a process requires; every other step, and every
// other pack, gets the generic shape of its node type. Moving this into the
// DomainPack's API is open (brief §7), not done here.
//
// `units` is the capacity of the resource that chose the kind (one test
// cabinet per FCT tester, a seat per operator on shift); a step without one
// has 1. A queue's buffer has its capacity in `units` when it has one.
import type { ModelIR } from "../../lib/contracts";

/** The electronics line's equipment, then the generic shapes. */
export type EquipmentKind =
  | "smt" | "aoi" | "tester" | "oven" | "bench" | "table"
  | "machine" | "buffer" | "dock" | "shipping" | "conveyor" | "junction" | "reject" | "return";

export type Equipment = {
  readonly kind: EquipmentKind;
  /** The resource that chose the kind, or for a generic machine its first; null for a step without one. */
  readonly resource: string | null;
  /** That resource's capacity (a queue: its capacity); 1 without one, null for a queue without a limit. */
  readonly units: number | null;
};

type Kinds = Readonly<Record<string, EquipmentKind>>;

/** The packs with their own equipment: resource ID → kind, and the pack's resource roles (TWIN_IR_SPEC role) → kind for resources with another ID. */
const PACKS: Readonly<Record<string, { readonly byId: Kinds; readonly byRole: Kinds }>> = {
  electronics_line: {
    byId: {
      smt_line: "smt", aoi_machine: "aoi", ict_tester: "tester", fct_tester: "tester",
      burn_in_chamber: "oven", debug_operator: "bench", pack_station: "table",
    },
    byRole: {
      smt_line: "smt", aoi: "aoi", ict_tester: "tester", fct_tester: "tester",
      burn_in_chamber: "oven", debug_operator: "bench", pack_station: "table",
    },
  },
};

/** The generic shape of each node type. */
export const GENERIC: Readonly<Record<ModelIR["nodes"][number]["type"], EquipmentKind>> = {
  process: "machine", queue: "buffer", source: "dock", sink: "shipping",
  delay: "conveyor", decision: "junction", failure: "reject", rework: "return",
};

type Model = Pick<ModelIR, "domain" | "resources">;
type Node = { readonly id: string; readonly type: ModelIR["nodes"][number]["type"]; readonly requires?: readonly { readonly resource_id: string }[]; readonly capacity?: number };

/** Whether a pack draws its own equipment (the others are all generic). */
export const hasOwnEquipment = (domain: string) => Object.hasOwn(PACKS, domain);

/**
 * What a node is drawn as. For a process of a pack with its own equipment:
 * the kind of the first required resource the pack knows, a machine's before
 * a person's (an FCT step staffed by an operator is still a tester); by its
 * ID, else by its role. Otherwise the generic shape of the node's type.
 */
export function equipmentFor(model: Model, node: Node): Equipment {
  const resources = new Map(model.resources.map((r) => [r.id, r]));
  const required = (node.requires ?? []).flatMap((q) => (resources.has(q.resource_id) ? [resources.get(q.resource_id)!] : []));
  if (node.type === "process") {
    const pack = Object.hasOwn(PACKS, model.domain) ? PACKS[model.domain]! : null;
    if (pack) {
      const known = required
        .map((r) => ({ r, kind: (Object.hasOwn(pack.byId, r.id) ? pack.byId[r.id] : r.role !== undefined && Object.hasOwn(pack.byRole, r.role) ? pack.byRole[r.role] : undefined) }))
        .filter((k): k is { r: (typeof required)[number]; kind: EquipmentKind } => k.kind !== undefined);
      const chosen = known.find((k) => k.r.type === "machine") ?? known[0];
      if (chosen) return { kind: chosen.kind, resource: chosen.r.id, units: chosen.r.capacity };
    }
    const first = required[0];
    return { kind: GENERIC.process, resource: first?.id ?? null, units: first?.capacity ?? 1 };
  }
  if (node.type === "queue") return { kind: GENERIC.queue, resource: null, units: node.capacity ?? null };
  return { kind: GENERIC[node.type], resource: null, units: 1 };
}

/** Every node's equipment, by node ID, in the model's order. */
export function equipmentOf(model: Model & Pick<ModelIR, "nodes">): ReadonlyMap<string, Equipment> {
  return new Map(model.nodes.map((n) => [n.id, equipmentFor(model, n as Node)]));
}
