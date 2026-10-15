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

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  { rules: { "no-restricted-imports": ["error", { patterns: forbiddenImports }] } },
  globalIgnores([".next/**", "out/**", "coverage/**", "next-env.d.ts"]),
]);
