// T-015: the editor's JSON Patch helpers. The working draft the editor shows
// is the saved draft with the pending ops applied (RFC 6902, the P0 profile
// of D-014: add, remove, replace, test); the server applies the same ops to
// the same document (internal/model/patch), so the two must agree: atomic,
// one decode of each pointer, "-" only to append, test by JSON value.
import { describe, expect, it } from "vitest";
import { applyPatch, jsonEqual, parsePointer, pointer } from "../features/model-editor/patch";

describe("JSON Pointer (RFC 6901)", () => {
  it("escapes ~ and / once each, and decodes them back", () => {
    expect(pointer("views", "2d", "positions", "a/b~c")).toBe("/views/2d/positions/a~1b~0c");
    expect(parsePointer("/views/2d/positions/a~1b~0c")).toEqual(["views", "2d", "positions", "a/b~c"]);
    expect(pointer("nodes", 3, "name")).toBe("/nodes/3/name");
    // ~01 is "~1" (decode ~1 first would give "/"): one decode only.
    expect(parsePointer("/a~01")).toEqual(["a~1"]);
  });

  it("refuses what is not a pointer", () => {
    expect(parsePointer("nodes/0")).toBeNull();
    expect(parsePointer("/a~2")).toBeNull();
    expect(parsePointer("/a~")).toBeNull();
    expect(parsePointer("")).toEqual([]);
  });
});

describe("applyPatch", () => {
  const doc = () => ({ name: "m", nodes: [{ id: "a" }, { id: "b" }], views: { "2d": { positions: { a: { x: 0, y: 0 } } } } });

  it("adds, replaces, removes and tests on a copy; the input never changes", () => {
    const before = doc();
    const r = applyPatch(before, [
      { op: "test", path: "/nodes/1/id", value: "b" },
      { op: "replace", path: "/nodes/1/id", value: "c" },
      { op: "add", path: "/nodes/-", value: { id: "d" } },
      { op: "add", path: "/nodes/0", value: { id: "z" } },
      { op: "add", path: "/views/2d/positions/d", value: { x: 1, y: 2 } },
      { op: "remove", path: "/views/2d/positions/a" },
      { op: "replace", path: "/name", value: "n" },
    ]);
    expect(r).toEqual({
      ok: true,
      doc: { name: "n", nodes: [{ id: "z" }, { id: "a" }, { id: "c" }, { id: "d" }], views: { "2d": { positions: { d: { x: 1, y: 2 } } } } },
    });
    expect(before).toEqual(doc());
  });

  it("is atomic: the first failing op names its index and nothing is applied", () => {
    expect(applyPatch(doc(), [{ op: "replace", path: "/name", value: "x" }, { op: "test", path: "/nodes/0/id", value: "b" }])).toEqual({ ok: false, index: 1, reason: "test_failed" });
    expect(applyPatch(doc(), [{ op: "replace", path: "/missing", value: 1 }])).toEqual({ ok: false, index: 0, reason: "target_missing" });
    expect(applyPatch(doc(), [{ op: "remove", path: "/nodes/2" }])).toEqual({ ok: false, index: 0, reason: "target_missing" });
    expect(applyPatch(doc(), [{ op: "add", path: "/nodes/3", value: {} }])).toEqual({ ok: false, index: 0, reason: "target_missing" });
    expect(applyPatch(doc(), [{ op: "add", path: "/a/b", value: 1 }])).toEqual({ ok: false, index: 0, reason: "target_missing" });
  });

  it("refuses the root, a bad pointer, a non-index into an array and '-' anywhere but an append", () => {
    expect(applyPatch(doc(), [{ op: "replace", path: "", value: {} }])).toEqual({ ok: false, index: 0, reason: "invalid_path" });
    expect(applyPatch(doc(), [{ op: "replace", path: "name", value: 1 }])).toEqual({ ok: false, index: 0, reason: "invalid_path" });
    expect(applyPatch(doc(), [{ op: "replace", path: "/nodes/01/id", value: "x" }])).toEqual({ ok: false, index: 0, reason: "invalid_path" });
    expect(applyPatch(doc(), [{ op: "replace", path: "/nodes/-", value: {} }])).toEqual({ ok: false, index: 0, reason: "invalid_path" });
    expect(applyPatch(doc(), [{ op: "remove", path: "/nodes/-/id" }])).toEqual({ ok: false, index: 0, reason: "invalid_path" });
  });

  it("compares test values as JSON: member order does not matter, types do", () => {
    const d = { a: { x: 1, y: [1, { b: true }] } };
    expect(applyPatch(d, [{ op: "test", path: "/a", value: { y: [1, { b: true }], x: 1 } }]).ok).toBe(true);
    expect(applyPatch(d, [{ op: "test", path: "/a/x", value: "1" }]).ok).toBe(false);
    expect(applyPatch(d, [{ op: "test", path: "/a/y", value: [{ b: true }, 1] }]).ok).toBe(false);
    expect(jsonEqual({ a: 1, b: 2 }, { b: 2, a: 1 })).toBe(true);
    expect(jsonEqual({ a: 1 }, { a: 1, b: undefined })).toBe(false);
    expect(jsonEqual(null, {})).toBe(false);
  });

  it("does not share values between the patch and the result", () => {
    const value = { x: 1, y: 1 };
    const r = applyPatch(doc(), [{ op: "add", path: "/views/2d/positions/b", value }]);
    value.x = 99;
    expect(r.ok && (r.doc as ReturnType<typeof doc>).views["2d"].positions).toEqual({ a: { x: 0, y: 0 }, b: { x: 1, y: 1 } });
  });
});
