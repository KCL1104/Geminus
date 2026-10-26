// JSON Pointer (RFC 6901) and JSON Patch (RFC 6902) as the P0 profile
// allows them (D-014: add, remove, replace, test). The editor shows the
// saved draft with the pending ops applied, the same document the server
// computes when it applies them (internal/model/patch): on a copy, op by op,
// all or nothing; each pointer decoded once; "-" only to append; tests by
// JSON value. There is no other copy of the model: the IR is the data.
import type { JsonPatch } from "../../lib/contracts";

export type Op = JsonPatch[number];

/** A pointer from tokens: "/" before each, "~" and "/" escaped. */
export function pointer(...tokens: (string | number)[]): string {
  return tokens.map((t) => "/" + String(t).replace(/~/g, "~0").replace(/\//g, "~1")).join("");
}

/** The tokens of a pointer, or null when it is not one ("" is the root). */
export function parsePointer(p: string): string[] | null {
  if (p === "") return [];
  if (!p.startsWith("/")) return null;
  const out: string[] = [];
  for (const raw of p.slice(1).split("/")) {
    if (/~(?![01])/.test(raw)) return null;
    out.push(raw.replace(/~1/g, "/").replace(/~0/g, "~"));
  }
  return out;
}

const INDEX = /^(0|[1-9][0-9]*)$/;

/** Equal as JSON values: member order does not matter; types and array order do. */
export function jsonEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((x, i) => jsonEqual(x, b[i]));
  }
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  return ka.length === kb.length && ka.every((k) => Object.hasOwn(b, k) && jsonEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}

export type ApplyFailure = { ok: false; index: number; reason: "invalid_path" | "target_missing" | "test_failed" };
export type ApplyResult<T> = { ok: true; doc: T } | ApplyFailure;

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const copy = <T,>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T));

/** Applies ops to a copy of doc; the first op that fails names itself and nothing is applied. */
export function applyPatch<T>(doc: T, ops: readonly Op[]): ApplyResult<T> {
  const root = copy(doc);
  for (const [index, op] of ops.entries()) {
    const reason = applyOne(root, op);
    if (reason !== null) return { ok: false, index, reason };
  }
  return { ok: true, doc: root };
}

function applyOne(root: unknown, op: Op): ApplyFailure["reason"] | null {
  const tokens = parsePointer(op.path);
  // The root is not a target in the P0 profile.
  if (tokens === null || tokens.length === 0) return "invalid_path";
  let parent: unknown = root;
  for (const t of tokens.slice(0, -1)) {
    if (Array.isArray(parent)) {
      if (!INDEX.test(t)) return "invalid_path";
      if (Number(t) >= parent.length) return "target_missing";
      parent = parent[Number(t)];
    } else if (isRecord(parent)) {
      if (!Object.hasOwn(parent, t)) return "target_missing";
      parent = parent[t];
    } else {
      return "target_missing";
    }
  }
  const key = tokens[tokens.length - 1]!;
  const value = "value" in op ? copy(op.value) : undefined;
  if (Array.isArray(parent)) {
    if (key === "-") {
      if (op.op !== "add") return "invalid_path";
      parent.push(value);
      return null;
    }
    if (!INDEX.test(key)) return "invalid_path";
    const n = Number(key);
    if (n > parent.length || (op.op !== "add" && n === parent.length)) return "target_missing";
    switch (op.op) {
      case "add":
        parent.splice(n, 0, value);
        return null;
      case "replace":
        parent[n] = value;
        return null;
      case "remove":
        parent.splice(n, 1);
        return null;
      case "test":
        return jsonEqual(parent[n], op.value) ? null : "test_failed";
    }
  }
  if (!isRecord(parent)) return "target_missing";
  const exists = Object.hasOwn(parent, key);
  if (!exists && op.op !== "add") return "target_missing";
  switch (op.op) {
    case "add":
    case "replace":
      parent[key] = value;
      return null;
    case "remove":
      delete parent[key];
      return null;
    case "test":
      return jsonEqual(parent[key], op.value) ? null : "test_failed";
  }
}
