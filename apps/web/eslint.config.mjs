import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// The browser never talks to an LLM provider or to PostgreSQL: AI runs in the
// Go worker and all data goes through the Go REST API (AGENTS.md §2,
// docs/ARCHITECTURE.md §1). This catches static imports only; it is a guard
// rail, not the security boundary.
const forbiddenImports = [
  {
    group: [
      "@anthropic-ai/*", "openai", "openai/*", "ai", "@ai-sdk/*", "@google/genai",
      "@google/generative-ai", "@mistralai/*", "cohere-ai", "ollama", "langchain",
      "@langchain/*",
    ],
    message: "LLM SDKs are server-side only (internal/ai in the Go worker).",
  },
  {
    group: [
      "pg", "pg-*", "postgres", "@neondatabase/*", "@vercel/postgres", "drizzle-orm",
      "drizzle-orm/*", "@prisma/client", "kysely",
    ],
    message: "The web app must not access PostgreSQL; use the Go REST API.",
  },
];

// User-visible text comes from the message catalogs (REQ-022, ADR-020):
// JSX text and strings in JSX, and the attributes and props people read or
// hear. Punctuation and spacing between translated parts are allowed.
// Strings built outside JSX, or passed through a function call into JSX,
// are not seen by this rule; review covers them (ADR-020).
const textAttributes =
  "/^(aria-label|aria-description|aria-placeholder|aria-roledescription|aria-valuetext|title|alt|placeholder|label|text|message|description|heading|caption|tooltip|hint|helperText|errorMessage)$/";
const letters = "/\\p{L}/u";
// Expressions that pass a string through to what is shown: a condition, a
// fallback, a concatenation, a type assertion or an array. A call is not
// one: t(a ? "k.x" : "k.y") chooses a key.
const passThrough =
  ':matches(LogicalExpression, ConditionalExpression, BinaryExpression[operator="+"], TSAsExpression, TSSatisfiesExpression, TSNonNullExpression, ArrayExpression)';
const through = (from, min, max) => Array.from({ length: max - min + 1 }, (_, i) => from + ` > ${passThrough}`.repeat(min + i));
const child = ":matches(JSXElement, JSXFragment) > JSXExpressionContainer";
// Up to three pass-through steps from where a string is rendered: a child
// expression (its bare string is jsx-no-literals'), what a callback in one
// returns (xs.map(() => "item")), or a text attribute's value.
const renderedPaths = [
  ...through(child, 1, 3),
  ...through(`${child} CallExpression > ArrowFunctionExpression`, 0, 3),
  ...through(`JSXAttribute[name.name=${textAttributes}] > JSXExpressionContainer`, 0, 3),
];
const catalogText = "User-visible text comes from the message catalog (messages/en.ts, messages/zh-Hant.ts; REQ-022).";

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  { rules: { "no-restricted-imports": ["error", { patterns: forbiddenImports }] } },
  {
    files: ["**/*.tsx"],
    ignores: ["tests/**"],
    rules: {
      "react/jsx-no-literals": ["error", { noStrings: true, ignoreProps: true, allowedStrings: [" ", "/", "…", "—", "·", ":", ",", ".", "(", ")"] }],
      "no-restricted-syntax": [
        "error",
        // jsx-no-literals only sees a bare string in JSX (and ignores props).
        ...[...new Set(renderedPaths)].flatMap((path) => [
          { selector: `${path} > Literal[value=${letters}]`, message: catalogText },
          { selector: `${path} > TemplateLiteral`, message: catalogText },
        ]),
        { selector: `JSXAttribute[name.name=${textAttributes}] > Literal[value=${letters}]`, message: catalogText },
        {
          selector: `JSXOpeningElement[name.name="input"]:has(JSXAttribute[name.name="type"][value.value=/^(submit|button|reset)$/]) > JSXAttribute[name.name="value"] > Literal[value=${letters}]`,
          message: catalogText,
        },
        {
          selector: `JSXAttribute > JSXExpressionContainer > ObjectExpression > Property[key.name=${textAttributes}] > :matches(Literal[value=${letters}], TemplateLiteral)`,
          message: catalogText,
        },
        { selector: 'AssignmentExpression > MemberExpression.left[object.name="document"][property.name="title"]', message: "Set the page title through metadata (REQ-022)." },
      ],
      // Browser dialogs show text the catalog cannot reach and that the UI cannot style or test.
      "no-restricted-globals": ["error", ...["alert", "confirm", "prompt"].map((name) => ({ name, message: "Use an in-page dialog with catalog text (REQ-022)." }))],
      "no-restricted-properties": ["error", ...["alert", "confirm", "prompt"].map((property) => ({ object: "window", property, message: "Use an in-page dialog with catalog text (REQ-022)." }))],
    },
  },
  globalIgnores([".next/**", "out/**", "coverage/**", "next-env.d.ts"]),
]);
