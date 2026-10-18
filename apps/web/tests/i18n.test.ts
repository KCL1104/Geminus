// REQ-022 / TEST-031: the catalogs, the translator and the locale choice.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { en } from "../messages/en";
import { zhHant } from "../messages/zh-Hant";
import type { ProblemKind } from "../lib/api/problem";
import { i18nFor } from "../lib/i18n/provider";
import { cookieLocale, defaultLocale, localeCookie, localeCookieValue, locales, parseLocale } from "../lib/i18n/locale";
import { catalogs, createTranslator, formatDateTime, formatDuration, formatNumber, type Message, type MessageKey, placeholders } from "../lib/i18n/translate";

const common = JSON.parse(readFileSync(join(process.cwd(), "../../schemas/common.schema.json"), "utf8")) as {
  $defs: Record<string, { enum: string[] }>;
};
const keys = Object.keys(en) as MessageKey[];
const forms = (m: Message): string[] => (typeof m === "string" ? [m] : Object.values(m).filter((f): f is string => typeof f === "string"));

describe("catalogs", () => {
  it("are the contract's locales, en first", () => {
    expect([...locales]).toEqual(common.$defs.Locale?.enum);
    expect(defaultLocale).toBe("en");
  });

  it("have exactly the same keys", () => {
    expect(Object.keys(zhHant).sort()).toEqual([...keys].sort());
  });

  it.each(locales)("%s has no empty message", (l) => {
    for (const k of keys) for (const f of forms(catalogs[l][k])) expect(f.trim(), `${l} ${k}`).not.toBe("");
  });

  it("use the same placeholders and plural shape in both languages", () => {
    for (const k of keys) {
      expect(placeholders(zhHant[k]), k).toEqual(placeholders(en[k]));
      expect(typeof zhHant[k], k).toBe(typeof en[k]);
      const m = en[k];
      if (typeof m !== "string") {
        expect(Object.keys(m).sort(), `${k}: en plural forms`).toEqual(["one", "other"]);
        expect(placeholders(m), k).toContain("count");
      }
    }
  });

  it("translate everything except names that are the same in both", () => {
    const same = keys.filter((k) => JSON.stringify(zhHant[k]) === JSON.stringify(en[k]));
    expect(same.sort()).toEqual(["app.name", "home.title", "locale.en", "locale.zh-Hant"]);
  });

  it("cover every error code, validation code and problem kind", () => {
    for (const c of common.$defs.ErrorCode?.enum ?? []) expect(keys, c).toContain(`error.${c}`);
    for (const c of common.$defs.ValidationCode?.enum ?? []) expect(keys, c).toContain(`validation.${c}`);
    expect(common.$defs.ErrorCode?.enum.length).toBe(20);
    expect(common.$defs.ValidationCode?.enum.length).toBe(21);
    const kinds = { unauthenticated: 1, forbidden: 1, not_found: 1, conflict: 1, invalid: 1, precondition: 1, rate_limited: 1, unavailable: 1, internal: 1, network: 1, protocol: 1 } satisfies Record<ProblemKind, 1>;
    for (const k of Object.keys(kinds)) expect(keys, k).toContain(`problem.heading.${k}`);
    // No code is in a catalog that the contract does not have.
    expect(keys.filter((k) => k.startsWith("error.")).map((k) => k.slice(6)).sort()).toEqual([...(common.$defs.ErrorCode?.enum ?? [])].sort());
    expect(keys.filter((k) => k.startsWith("validation.")).map((k) => k.slice(11)).sort()).toEqual([...(common.$defs.ValidationCode?.enum ?? [])].sort());
  });
});

describe("translator", () => {
  const t = createTranslator("en");
  const zh = createTranslator("zh-Hant");

  it("fills placeholders and formats numbers for the locale", () => {
    expect(t("models.version", { version: "3" })).toBe("version 3");
    expect(t("morePages.projects", { count: 12345 })).toBe("Showing the first 12,345 projects. More exist; this build cannot page through them yet.");
    expect(zh("morePages.projects", { count: 12345 })).toBe("顯示前 12,345 個專案。還有更多專案，目前尚無法翻頁。");
    expect(t("projects.created", { date: "x" })).toBe("created x");
  });

  it("picks plural forms by count", () => {
    expect(t("draft.pending", { count: 1 })).toBe("1 change not saved.");
    expect(t("draft.pending", { count: 2 })).toBe("2 changes not saved.");
    expect(t("draft.pending", { count: 0 })).toBe("0 changes not saved.");
    expect(zh("draft.pending", { count: 1 })).toBe("1 項變更尚未儲存。");
    expect(zh("problem.retryAfter", { count: 30 })).toBe("請在 30 秒後重試。");
  });

  it("refuses a missing value instead of showing a placeholder", () => {
    expect(() => t("models.version")).toThrow(/version/);
    expect(() => t("draft.pending")).toThrow(/count/);
    expect(() => t("draft.pending", { count: "2" })).toThrow(/count/);
    // Only the params' own values count (T-044 review, info).
    expect(() => t("models.version", Object.create({ version: "3" }) as Record<string, string>)).toThrow(/version/);
  });
});

describe("locale choice", () => {
  it("accepts exactly the supported values and defaults to en", () => {
    expect(parseLocale("zh-Hant")).toBe("zh-Hant");
    expect(parseLocale("en")).toBe("en");
    for (const v of [undefined, null, "", "zh-hant", "zh-TW", "zh-Hans", "EN", "en-US", "zh-Hant;", " en"]) expect(parseLocale(v), String(v)).toBe("en");
  });

  it("is remembered in a cookie that holds only the language", () => {
    expect(localeCookie).toBe("geminus_locale");
    expect(localeCookieValue("zh-Hant", true)).toBe("geminus_locale=zh-Hant; Path=/; Max-Age=31536000; SameSite=Lax; Secure");
    expect(localeCookieValue("en", false)).toBe("geminus_locale=en; Path=/; Max-Age=31536000; SameSite=Lax");
  });

  it("reads the choice from a Cookie header or document.cookie, first value wins", () => {
    expect(cookieLocale("a=b; geminus_locale=zh-Hant; c=d")).toBe("zh-Hant");
    expect(cookieLocale("geminus_locale=zh-Hant; geminus_locale=en")).toBe("zh-Hant");
    for (const v of ["", "geminus_locale=zh-hant", "xgeminus_locale=zh-Hant", "geminus_locale = zh-Hant", "geminus_locale"]) expect(cookieLocale(v), v).toBe("en");
  });
});

describe("joining", () => {
  it("puts a space between sentences in English and none in Chinese (T-044 review L7)", () => {
    expect(i18nFor("en").gap).toBe(" ");
    expect(i18nFor("zh-Hant").gap).toBe("");
  });
});

describe("formatting", () => {
  it("writes dates the locale's way", () => {
    const iso = "2026-10-18T09:30:00Z";
    expect(formatDateTime("en", iso)).toMatch(/2026/);
    expect(formatDateTime("zh-Hant", iso)).toMatch(/2026年/);
    expect(formatDateTime("en", iso)).not.toBe(formatDateTime("zh-Hant", iso));
    expect(() => formatDateTime("en", "not a date")).toThrow();
    // Only RFC 3339, the contract's date-time: Date.parse accepts far more.
    expect(() => formatDateTime("en", "10/18/2026")).toThrow();
    expect(() => formatDateTime("en", "2026-10-18")).toThrow();
    // Dates that do not exist are not rolled over (T-044 review L4).
    for (const v of ["2026-02-30T00:00:00Z", "2026-02-29T00:00:00Z", "2026-13-01T00:00:00Z", "2026-00-10T00:00:00Z", "2026-10-00T00:00:00Z", "2026-10-18T24:00:00Z", "2026-10-18T10:60:00Z", "2026-10-18T10:00:61Z", "2026-10-18T10:00:00+24:00", "2026-10-18T10:00:00+08:60"])
      expect(() => formatDateTime("en", v), v).toThrow();
    expect(formatDateTime("en", "2028-02-29T12:00:00Z")).toMatch(/2028/);
    // RFC 3339 allows lowercase t and z.
    expect(formatDateTime("en", "2026-10-18t09:30:00z")).toBe(formatDateTime("en", "2026-10-18T09:30:00Z"));
    expect(formatDateTime("en", "2026-10-18T09:30:00.123+08:00")).toMatch(/2026/);
  });

  it("shows a server value that is not a date-time as it came, instead of failing the page", () => {
    expect(i18nFor("en").dateTime("2026-13-99")).toBe("2026-13-99");
    // A value of the wrong type shows nothing, never "null" or "undefined" (T-044 review M2).
    for (const v of [null, undefined, 42]) expect(i18nFor("zh-Hant").dateTime(v as unknown as string)).toBe("");
    expect(i18nFor("zh-Hant").dateTime("2026-10-18T09:30:00Z")).toBe(formatDateTime("zh-Hant", "2026-10-18T09:30:00Z"));
  });

  it("writes numbers and durations the locale's way", () => {
    expect(formatNumber("en", 1234.5)).toBe("1,234.5");
    expect(formatNumber("zh-Hant", 0.25, { style: "percent" })).toBe("25%");
    expect(formatDuration("en", 3, "hour")).toBe("3 hours");
    expect(formatDuration("zh-Hant", 3, "hour")).toBe("3 小時");
    expect(formatDuration("en", 1, "minute")).toBe("1 minute");
  });
});
