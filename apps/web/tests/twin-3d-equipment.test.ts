// H-072: what each step is drawn as in the 3D stage (features/twin-3d/equipment.ts).
// The electronics line's processes take their equipment from the resource
// they require (by ID, else by the pack's role; a machine's before a
// person's), sized by its capacity; every other step and every other pack
// gets the generic shape of its node type.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import type { ModelIR } from "../lib/contracts";
import { equipmentFor, equipmentOf, GENERIC, hasOwnEquipment } from "../features/twin-3d/equipment";
import { readElectronicsLine } from "./fixtures/electronics-line";

const repo = join(process.cwd(), "../..");
const template = (path: string) => JSON.parse(readFileSync(join(repo, path), "utf8")) as ModelIR;

describe("the electronics line's equipment", () => {
  const ir = readElectronicsLine().model;
  const kinds = equipmentOf(ir);

  it("draws each station as what it is, one unit per resource unit", () => {
    const at = (id: string) => kinds.get(id);
    expect(at("smt")).toEqual({ kind: "smt", resource: "smt_line", units: 1 });
    expect(at("aoi")).toEqual({ kind: "aoi", resource: "aoi_machine", units: 1 });
    expect(at("ict")).toEqual({ kind: "tester", resource: "ict_tester", units: 2 });
    expect(at("ict_retest")).toEqual({ kind: "tester", resource: "ict_tester", units: 2 });
    expect(at("fct")).toEqual({ kind: "tester", resource: "fct_tester", units: 3 });
    expect(at("fct_retest")).toEqual({ kind: "tester", resource: "fct_tester", units: 3 });
    expect(at("burn_in")).toEqual({ kind: "oven", resource: "burn_in_chamber", units: 9 });
    expect(at("pack_ship")).toEqual({ kind: "table", resource: "pack_station", units: 2 });
    for (const id of ["aoi_touchup", "ict_debug", "fct_debug"]) expect(at(id)).toEqual({ kind: "bench", resource: "debug_operator", units: 4 });
  });

  it("draws the other steps in the generic shapes", () => {
    const byKind = (k: string) => ir.nodes.filter((n) => kinds.get(n.id)!.kind === k).map((n) => n.id);
    expect(byKind("dock")).toEqual(["release_a", "release_b"]);
    expect(byKind("shipping")).toEqual(["shipped", "scrap"]);
    expect(byKind("reject")).toEqual(["aoi_defect", "ict_fail", "fct_fail", "burnin_fail"]);
    expect(byKind("return")).toEqual(["aoi_rework", "ict_rework", "fct_rework"]);
    expect(byKind("junction")).toHaveLength(8);
    expect(byKind("buffer")).toEqual(ir.nodes.filter((n) => n.type === "queue").map((n) => n.id));
    expect(kinds.get("q_fct")).toEqual({ kind: "buffer", resource: null, units: 10 });
    expect(kinds.get("q_smt")).toEqual({ kind: "buffer", resource: null, units: null }); // no limit
    expect(kinds.size).toBe(ir.nodes.length);
  });

  it("shows a fourth FCT tester as a fourth cabinet", () => {
    const scenario = { ...ir, resources: ir.resources.map((r) => (r.id === "fct_tester" ? { ...r, capacity: 4 } : r)) };
    expect(equipmentOf(scenario).get("fct")).toEqual({ kind: "tester", resource: "fct_tester", units: 4 });
  });

  it("knows a resource by its role when its ID is another, and prefers the machine to the person", () => {
    const model = {
      domain: "electronics_line",
      resources: [
        { id: "operator", type: "human", role: "debug_operator", name: "Operator", capacity: 2, queue_discipline: "fifo" },
        { id: "fct_b", type: "machine", role: "fct_tester", name: "FCT B", capacity: 5, queue_discipline: "fifo" },
      ],
    } satisfies Pick<ModelIR, "domain" | "resources">;
    expect(equipmentFor(model, { id: "x", type: "process", requires: [{ resource_id: "operator" }, { resource_id: "fct_b" }] })).toEqual({ kind: "tester", resource: "fct_b", units: 5 });
    expect(equipmentFor(model, { id: "y", type: "process", requires: [{ resource_id: "operator" }] })).toEqual({ kind: "bench", resource: "operator", units: 2 });
    // A resource the pack does not know: a generic machine.
    expect(equipmentFor({ ...model, resources: [{ id: "glue", type: "machine", name: "Glue", capacity: 3, queue_discipline: "fifo" }] }, { id: "z", type: "process", requires: [{ resource_id: "glue" }] })).toEqual({ kind: "machine", resource: "glue", units: 3 });
  });
});

describe("the generic shapes for every other pack", () => {
  it("draws a software delivery pipeline by its node types", () => {
    const ir = template("internal/domainpack/softwaredelivery/templates/default_delivery_pipeline.json");
    expect(hasOwnEquipment(ir.domain)).toBe(false);
    const kinds = equipmentOf(ir);
    for (const n of ir.nodes) {
      const e = kinds.get(n.id)!;
      expect(e.kind, n.id).toBe(GENERIC[n.type]);
      if (n.type === "process") {
        const r = ir.resources.find((x) => x.id === n.requires[0]?.resource_id);
        expect(e, n.id).toEqual({ kind: "machine", resource: r?.id ?? null, units: r?.capacity ?? 1 });
      }
    }
  });

  it("draws a warehouse the same way, even with resources named like the electronics line's", () => {
    const ir = template("internal/domainpack/productionlogistics/templates/carton_plant.json");
    const fake = { ...ir, resources: ir.resources.map((r, i) => (i === 0 ? { ...r, id: "smt_line", role: "smt_line" } : r)) };
    const processes = fake.nodes.filter((n) => n.type === "process");
    expect(processes.length).toBeGreaterThan(0);
    for (const n of processes) expect(equipmentFor(fake, n).kind).toBe("machine");
  });

  it("has a shape for every node type", () => {
    expect(GENERIC).toEqual({ process: "machine", queue: "buffer", source: "dock", sink: "shipping", delay: "conveyor", decision: "junction", failure: "reject", rework: "return" });
    expect(equipmentFor({ domain: "x", resources: [] }, { id: "p", type: "process", requires: [{ resource_id: "missing" }] })).toEqual({ kind: "machine", resource: null, units: 1 });
  });
});
