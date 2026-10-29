# ADR-020 — UI language: a hand-written typed catalog, a cookie language preference, two-language default names for templates (T-044)

Status: ACCEPTED (chosen and implemented first by the T-044 agent; approved by the owner on 2026-10-18, who also decided that Chinese is Traditional `zh-Hant` only for now, H-006). Date: 2026-10-18. Requirements: REQ-022 (added by the owner, H-005), REQ-004. The T-044 scope requires "an i18n mechanism (hand-written or a library, under a separate ADR, version pinned)".

## Decision

### Catalog and translation function (`apps/web/lib/i18n/`, `apps/web/messages/`)

- **Hand-written**, with no added dependency. The catalog is a TypeScript object: `messages/en.ts` (`as const`) defines the keys; `messages/zh-Hant.ts` has the type `Catalog`, so a missing key, an extra key or a plural message written as a string fails to compile.
- `createTranslator(locale)`:
  - Interpolates `{name}`; numeric parameters are formatted per locale with `Intl.NumberFormat`.
  - A plural message is `{ one?, other }`, chosen by `params.count` with `Intl.PluralRules`; zh-Hant has only `other`.
  - A missing parameter, or a `count` that is not a number, throws: this is a programming error, and `{name}` is never shown. Only the params' own values are read (`Object.hasOwn`), not the prototype.
  - Identifiers such as revision numbers and version numbers are passed as strings, without thousands separators; only quantities (count) are grouped per language.
  - `gap`: the spacing between sentences, and between a label and a value; one space in English, none in Chinese (「已儲存。草稿修訂版 3。」, 「技術細節：…」).
- Dates, numbers, durations: `formatDateTime`, `formatNumber`, `formatDuration` (`Intl.NumberFormat` unit style).
  - `formatDateTime` accepts only an RFC 3339 date-time (the contract's format, `T`/`Z` in either case), and the date and the time must exist: `Date.parse` accepts a much wider range and rolls 2026-02-30 or 24:00 over into another time (T-044 review L4). The check does not depend on V8: other browsers' `Date` parsing differs.
  - Components use it through `useI18n().dateTime`: a string from the API that is not a date-time is shown as is; a value that is not a string (a missing field, `null`) is shown as empty and that piece of text is skipped, so the whole page does not fail (T-044 review M2).
- `tests/i18n.test.ts` locks:
  - the two languages have the same keys and no empty values; placeholders and plural shapes are the same;
  - text that is the same in both languages is limited to `app.name`, `home.title` and the two language names;
  - every `ErrorCode`, `ValidationCode` and `ProblemKind` has a message.
- No ICU MessageFormat or library (next-intl and FormatJS are both MIT and would work):
  - The messages currently need only interpolation and one/other.
  - next-intl is built around route prefixes and middleware; this project adds no prefix.
  - The cost of hand-writing is that we maintain plurals and formatting.
  - When select, gender, nesting, a third language or an external translation flow (extraction tools) is needed, switch to a library under a separate ADR.

### Locale and persistence

- The locales are only `en` (default, primary) and `zh-Hant` (H-005; H-006 decided not to add `zh-Hans` for now).
  - The contract is `common.schema.json#/$defs/Locale`; Go `domainpack.Locales` and TS `locales` are locked to the same values by tests.
- The preference is stored in the cookie `geminus_locale`:
  - The value can only be `en` or `zh-Hant`; `Path=/; Max-Age=31536000; SameSite=Lax`, plus `Secure` under https.
  - Not HttpOnly: it is written by the client's language switch, and its content is not secret.
  - It holds no identity or session and plays no part in authorization. The Go API does not read cookies (ADR-018).
  - It is compared exactly when read; any other value (including `zh-hant`, `zh-Hans`, `EN`) is treated as not chosen, and `en` is used.
  - When there are two cookies with the same name (another path or domain; the app sets only one), the server (Next.js `cookies().get`) and the client (`cookieLocale`) both take the last one, so the page and global-error use the same language (T-044 review 2 N5).
- **No Accept-Language negotiation**: H-005 makes English the primary language; even when the browser's language is Chinese, the UI starts in `en`, and the user switches.
- **No language prefix on routes**: the root layout reads the cookie on the server and sets `<html lang>` and `I18nProvider`.
  - Cost: every route becomes dynamic rendering (`next build` marks ƒ). ADR-018's BFF session needs rendering per request anyway.
- Switching: `LocaleSwitcher` writes the cookie, then `router.refresh()`; server components re-render in the new language without a URL change.
  - According to the Next.js documentation, `router.refresh()` keeps client state, so unsaved draft edits should be kept. T-044 had no page with an editable draft, so this was not verified then; T-015's `apps/web-e2e/tests/editor.spec.ts` ("switching the language keeps the unsaved edits") confirmed it in a browser: after switching the language, unsaved edits and state are kept (2026-10-26).
  - Each language name is shown in its own language (`English`, `繁體中文`, each with its `lang`). Since the T-045 re-skin the switcher is the design system's `Select` (ADR-021); the arrow keys only open the list, and the language switches only on selection.

### API errors and data

- The API returns only a language-neutral `code` and an English `message` (a developer message); the UI displays text by code.
  - `error.<ErrorCode>` is the explanation; `problem.heading.<kind>` is the heading.
  - `message` (the server's, or a network/protocol diagnostic the client produces itself) is placed after "Technical details/技術細節", marked `lang="en"`, and not translated.
  - A 404 still shows no message and no details (TEST-009).
- Validation details: a known `ValidationCode` shows `validation.<code>`; an unknown code (a pack's own) shows the server's `reason`, marked `lang="en"`.
- New contract `common.schema.json#/$defs/ValidationCode` (21 codes).
  - A Go test locks it to the constants declared in `internal/model/validate` and `internal/model/patch` plus the P0 pack's `DOMAIN_MISMATCH`.
  - `ValidationIssue.code` is still a string, so a pack can have its own codes.
- Names, labels and dataset content entered by users are data and are not translated. IDs, JSON Pointers, enums and metric paths are never translated.

### Two-language default names for templates (`internal/domainpack/`)

- New optional interface `domainpack.TemplateProvider`. `DomainPack` keeps the original report's method set.
  - `Templates()`.
  - `Template(name, modelID, modelName, locale)`: an unknown template or locale returns an error.
- The conformance suite adds a `templates` step, which checks for each template:
  - the list is non-empty, without duplicates or empty names;
  - each locale produces a model that passes the generic validator and the pack's rules, with the right domain, IDs and name;
  - two calls give the same result;
  - apart from names, all locales are identical;
  - an unknown locale or template only returns an error: locales include near values such as `zh`, `zh-TW`, `zh-Hant-TW`, `en-US`, and templates include names differing in letter case or with leading or trailing spaces.
  - Each caller gets its own model: request again with another model ID and name, and change every value it can reach (strings, numbers, booleans, every slice element and map entry, and add a key to every map); the first model is unchanged (`templates/copies`; T-044 review 2 N1: originally only the name was changed, so a pack that copied shallowly and shared flows or positions would pass).
  - `Template` is also called concurrently in the `concurrent` step; data races are caught by `make backend-race` (T-044 review M1).
- SoftwareDelivery: `templates/<template>.zh-Hant.names.json` gives names by ID, matched strictly.
  - A missing or extra name, a blank name or one containing control characters, a name over the IR limit (200 characters) and an unknown member are all internal errors, so that no language mixes two languages; they never become the `InvalidError` that means "the caller's input is wrong" (T-044 review L6).
  - IDs and all other content are the same in every language.
- The `CreateModel` request gains an optional `locale` (omitted means `en`). The handler that creates a model (T-012/T-013) passes it in. After creation the name is user data, and switching the language does not change it.

### Lint (`apps/web/eslint.config.mjs`, tests excluded; the text rules apply to `**/*.tsx`, dialog and title to `.ts` and `.tsx`)

- `react/jsx-no-literals`: noStrings, ignoreProps; whitespace and a few punctuation marks are allowed. It only looks at bare strings in JSX.
- `no-restricted-syntax` (widened by T-044 review M3):
  - "Contains letters" is judged by `\p{L}` (any script, including accented letters and the CJK extension blocks).
  - Text props: `aria-label`, `aria-description`, `aria-placeholder`, `aria-roledescription`, `aria-valuetext`, `title`, `alt`, `placeholder`, `label`, and the props components commonly use: `text`, `message`, `description`, `heading`, `caption`, `tooltip`, `hint`, `helperText`, `errorMessage`. String values containing letters are blocked on all of them, also through up to three levels of "passing". Passing means the conditionals `&&`/`||`/`??`/`?:`, `+` concatenation, `as`/`satisfies`/`!`, and arrays. `alt=""` (a decorative image) is allowed.
  - Child expressions of an element or fragment: a string or template literal containing letters after one to three levels of passing; a string returned by a callback (`xs.map(() => "item")`) also counts.
  - `<input type="submit|button|reset" value="…">` and `value={"…"}`; text fields of an object in a JSX prop (`problem={{ message: "…" }}`).
  - `document.title = …` and `window.document.title = …` (use metadata), blocked in `.ts` too.
- `no-restricted-globals`/`no-restricted-properties`: `alert`, `confirm`, `prompt` (including `window.`, `globalThis.`, `self.`); use an in-page dialog instead. Blocked in `.ts` too, since a hook can open a dialog just as well (T-044 review 2 N3).
- A function call is not passing: the key in `t(a ? "k.x" : "k.y")`, the argument in `onClick={() => set("edit")}` and the comparison in `m === "edit"` do not count.
- `tests/lint-guards.test.ts` has positive and negative examples for each kind of code, and mutation shows that every selector is guarded by a test.
- **Limits** (need human review):
  - strings built outside JSX (`const s = "Hello"; … {s}`), and strings that reach JSX only through a function call (`String("x")`, `label("x")`, `.join("and")`);
  - text props with other names;
  - four or more levels of passing;
  - text with only symbols and no letters (`→`, `©`);
  - English diagnostics produced in `lib/` (kept on purpose as technical messages);
  - the `metadata` object;
  - spread attributes (`{...{"aria-label": "Main"}}`), text in object props inside a conditional (`problem={a ? { message: "…" } : x}`), and `window` obtained through a variable (T-044 review 2 N3).
- **False positives** (T-044 review 2 N2):
  - The names of the text props above are reserved for translated text. Passing a key or a token to these props (`message="validation.required"`, `heading="h2"`, `text="sm"`) is also blocked; use another prop name (`messageKey`, `level`, `size`).
  - The callback rule covers the return value of an arrow in any call inside a child expression: besides `{xs.map((x) => <li className={cx(() => "row")} />)}`, a callback that only returns data, such as `groupBy(xs, () => "k")`, is also blocked, and so is a template literal that holds only data (`` xs.map((x) => `${x}%`) ``). Move them to a variable or function outside the JSX.
  - False positives were chosen over loosening: loosening would miss real text returned by callbacks.

### Error and not-found pages (T-044 review M2)

- `app/not-found.tsx`: any URL without a page is shown in the reader's language (including `<title>`). Next.js's built-in 404 page is English only and would be marked `lang="zh-Hant"`.
- `app/error.tsx`: when a route fails to render, it shows catalog text inside the root layout, with a retry; when the server provides a `digest`, it is shown as a reference number; the error message is not shown (it may contain internal information).
- `app/global-error.tsx`: replaces the whole document when the root layout itself fails. It has no server locale, so it first renders in English, and switches to the remembered language once the browser reads the cookie (`useRememberedLocale`).

### Not in this ADR

- The `Label`/`Description` of a pack's `NodeCatalog`/`ResourceCatalog` are currently English pack data. The T-015 palette should show both languages by kind from the UI catalog (follow-up).
- AI text in the user's language: T-025/T-030, gated by D-018 and the data policy.

## Consequences

- No new dependency and no license change; the translation and formatting functions are about 70 lines, protected by unit and mutation tests.
- The zh-Hant copy was written by the agent without a native-speaker review. Before a formal release, the owner or a native speaker should review `messages/zh-Hant.ts`.
- All pages use dynamic rendering; deployment and CDN caching (T-039) must vary on the cookie, or not cache HTML.
- T-044 completes only the catalog, lint, switching and persistence, Problem/validation code and Intl parts of TEST-031. The rest is completed by later tasks:
  - "no new sign-in needed": T-043, when the sign-in session exists;
  - AI prose: T-025/T-030;
  - the main E2E flow run once in each language: T-040.
