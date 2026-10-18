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
// JSX text and strings in JSX, and the attributes people read or hear.
// Punctuation and spacing between translated parts are allowed. Strings
// built outside JSX are not seen by this rule; review covers them.
const textAttributes = "/^(aria-label|aria-description|aria-placeholder|aria-roledescription|aria-valuetext|title|alt|placeholder|label)$/";
const letters = "/[A-Za-z\\u3400-\\u9fff]/";
const condition = ":matches(LogicalExpression, ConditionalExpression)";
// A child expression of an element or fragment, then one to three nested conditions.
const renderedByCondition = [1, 2, 3].map(
  (depth) => `:matches(JSXElement, JSXFragment) > JSXExpressionContainer${` > ${condition}`.repeat(depth)}`,
);
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
        { selector: `JSXAttribute[name.name=${textAttributes}] > Literal`, message: catalogText },
        { selector: `JSXAttribute[name.name=${textAttributes}] > JSXExpressionContainer > Literal`, message: catalogText },
        { selector: `JSXAttribute[name.name=${textAttributes}] > JSXExpressionContainer > TemplateLiteral`, message: catalogText },
        // jsx-no-literals does not look inside `a && "text"` or `a ? "x" : "y"`.
        // Only strings the condition renders count, so the path from the
        // child expression runs through conditions alone: a key chosen for
        // t(a ? "k.x" : "k.y") is an argument, not text.
        ...renderedByCondition.flatMap((path) => [
          { selector: `${path} > Literal[value=${letters}]`, message: catalogText },
          { selector: `${path} > TemplateLiteral`, message: catalogText },
        ]),
      ],
    },
  },
  globalIgnores([".next/**", "out/**", "coverage/**", "next-env.d.ts"]),
]);
