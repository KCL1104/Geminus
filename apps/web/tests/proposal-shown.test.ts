// T-031 review round 1 (M1): what the AI wrote into a proposal (names and
// values in its patch, its prose) is shown so a person sees every character
// that is there. Characters that show nothing or reorder the text around
// them (bidi controls, zero-width and other format characters, controls,
// separators, fillers, spaces other than U+0020 in a name) become a visible
// ⟨U+XXXX⟩; a name's key for "another entity reads the same" sets case,
// width, spacing and invisible characters aside; two shown values that read
// alike are told apart by their exact JSON, ASCII-escaped when even that
// reads alike; the exact JSON Patch keeps its value and escapes them.
import { describe, expect, it } from "vitest";
import { asciiJson, nameKey, patchJson, readsAlike, visible } from "../features/ai-proposals/shown";

// Everything the DOM must never receive raw from data: controls (but a tab
// or newline in prose), format characters, lone surrogates, separators.
const RAW = /[\p{Cf}\p{Cs}\p{Zl}\p{Zp}\u0000-\u0008\u000B-\u001F\u007F-\u009F]/u;

describe("visible: every character that is there can be seen", () => {
  it("marks bidi controls, zero-width and other format characters, controls, separators and fillers in a name", () => {
    const cases: [string, string][] = [
      ["Burn-in‮84 → 02", "Burn-in⟨U+202E⟩84 → 02"],
      ["ICT tester​", "ICT tester⟨U+200B⟩"],
      ["a؜b‎c‏d", "a⟨U+061C⟩b⟨U+200E⟩c⟨U+200F⟩d"],
      ["‪a‫b‬c‭d", "⟨U+202A⟩a⟨U+202B⟩b⟨U+202C⟩c⟨U+202D⟩d"],
      ["⁦a⁧b⁨c⁩", "⟨U+2066⟩a⟨U+2067⟩b⟨U+2068⟩c⟨U+2069⟩"],
      ["a‌b‍c⁠d﻿e­f", "a⟨U+200C⟩b⟨U+200D⟩c⟨U+2060⟩d⟨U+FEFF⟩e⟨U+00AD⟩f"],
      ["ok\u{E0041}\u{E007F}", "ok⟨U+E0041⟩⟨U+E007F⟩"],
      ["x\uD800y", "x⟨U+D800⟩y"],
      ["a b c", "a⟨U+2028⟩b⟨U+2029⟩c"],
      ["tab\there\nnew\u0085line\u007F", "tab⟨U+0009⟩here⟨U+000A⟩new⟨U+0085⟩line⟨U+007F⟩"],
      ["ICT tester　x y", "ICT⟨U+00A0⟩tester⟨U+3000⟩x⟨U+2009⟩y"],
      ["ㅤᅟ⠀", "⟨U+3164⟩⟨U+115F⟩⟨U+2800⟩"],
      ["e️x͏", "e⟨U+FE0F⟩x⟨U+034F⟩"],
    ];
    for (const [raw, shown] of cases) {
      expect(visible(raw), JSON.stringify(raw)).toBe(shown);
      expect(RAW.test(visible(raw)), JSON.stringify(raw)).toBe(false);
    }
  });

  it("leaves letters of any script, right-to-left ones included, digits, punctuation, emoji and plain spaces as they are", () => {
    for (const s of ["ICT tester", "燒機 第 2 站", "בדיקה 2", "اختبار", "Café ①", "<img src=x onerror=alert(1)>", "🔥 hot", "a — b · c"]) expect(visible(s)).toBe(s);
  });

  it("in prose keeps line breaks, tabs and spaces, and still marks what reorders or hides text", () => {
    expect(visible("Line one\nline\ttwo end", "prose")).toBe("Line one\nline\ttwo end");
    expect(visible("Raise it to 20‮%01 now​.", "prose")).toBe("Raise it to 20⟨U+202E⟩%01 now⟨U+200B⟩.");
    expect(RAW.test(visible("a⁦b\u0000c\u{E0020}", "prose"))).toBe(false);
  });
});

describe("nameKey: two names that read the same", () => {
  it("are the same key once case, width, spacing and invisible characters are set aside", () => {
    const key = nameKey("ICT tester");
    for (const s of ["ICT tester​", " ICT  Tester ", "ＩＣＴ tester", "ICT tester", "I‍CT tester﻿", "ICT\ttester", "⁨ICT tester⁩"]) expect(nameKey(s), JSON.stringify(s)).toBe(key);
  });

  it("differ when a letter differs", () => {
    expect(nameKey("ICT testers")).not.toBe(nameKey("ICT tester"));
    expect(nameKey("ICT tester 2")).not.toBe(nameKey("ICT tester"));
  });
});

describe("readsAlike and the exact JSON", () => {
  it("says canonically equivalent text reads alike, and different text does not", () => {
    expect(readsAlike("Café", "Café")).toBe(true);
    expect(readsAlike("95%", "95%")).toBe(true);
    expect(readsAlike("Burn-in", "Burn in")).toBe(false);
  });

  it("escapes every non-ASCII character in the ASCII JSON, which still means the same value", () => {
    expect(asciiJson("Café")).toBe('"Caf\\u00e9"');
    expect(asciiJson("Café")).toBe('"Cafe\\u0301"');
    expect(asciiJson({ name: "燒機‮" })).toBe('{"name":"\\u71d2\\u6a5f\\u202e"}');
    for (const v of ["Café", { a: ["\u{E0041}", 1.5, null] }, 0.950001]) expect(JSON.parse(asciiJson(v))).toEqual(v);
    expect(/^[\x20-\x7e]*$/.test(asciiJson({ x: "ok\u{E0041} \uD800" }))).toBe(true);
  });

  it("keeps the exact JSON Patch's value, with what would hide or reorder it escaped inside its strings", () => {
    const patch = [
      { op: "replace", path: "/nodes/3/name", value: "Burn-in‮84 → 02" },
      { op: "add", path: "/resources/-", value: { id: "resource_x", name: "ICT tester​ \u0085", capacity: 9 } },
    ];
    const text = patchJson(patch);
    expect(JSON.parse(text)).toEqual(patch);
    expect(RAW.test(text)).toBe(false);
    expect(text).toContain("Burn-in\\u202e84 → 02");
    expect(text).toContain("ICT tester\\u200b\\u00a0\\u0085");
    // The layout is JSON.stringify's, two spaces per level.
    expect(text.split("\n")[1]).toBe("  {");
  });
});
