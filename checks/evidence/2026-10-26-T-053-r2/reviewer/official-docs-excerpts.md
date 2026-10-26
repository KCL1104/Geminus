# Official Anthropic pages read for the T-053 round-2 review (2026-10-26)

Fetched with the web-fetch tool on 2026-10-26 (page text converted to Markdown). English quotes, verbatim
where quoted. Nothing here was sent to the Messages API; no key was used.

## https://platform.claude.com/docs/en/manage-claude/data-residency

- `inference_geo` values: `"global"` — "Default. Inference may run in any available geography for optimal
  performance and availability."; `"us"` — "Inference runs only in US-based infrastructure."
- "The response `usage` object includes an `inference_geo` field indicating where inference ran"
  (example `"inference_geo": "us"`).
- "The `inference_geo` parameter is supported on Claude 4.6 and later models."
- "`allowed_inference_geos`: Restricts which geos a workspace can use. If a request specifies an
  `inference_geo` not in this list, the API returns an error."
- "`default_inference_geo`: Sets the fallback geo when `inference_geo` is omitted from a request."
- Pricing: "Claude 4.6 and later models: US-only inference (`inference_geo: "us"`) is priced at 1.1x the
  standard rate across all token pricing categories (input tokens, output tokens, cache writes, and cache
  reads). On Claude Haiku 5.5, the multiplier also applies to the higher prices for prompts over 100,000
  tokens" … "Global routing (`inference_geo: "global"`): Standard pricing applies."
- Migration: "If your organization previously opted out of global routing … your workspace has been
  automatically configured with `allowed_inference_geos: ["us"]` and `default_inference_geo: "us"`."
- "Current limitations … Inference geo: Only `"us"` and `"global"` are available."

## https://platform.claude.com/docs/en/about-claude/pricing

- Model table, Claude Opus 5.5: "$4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok | $20 / MTok" (base
  input, 5m cache writes, 1h cache writes, cache hits and refreshes, output).
- "Data residency pricing: For Claude 4.6 and later models, specifying US-only inference through the
  `inference_geo` parameter incurs a 1.1x multiplier on all token pricing categories, including input
  tokens, output tokens, cache writes, and cache reads. … Global routing (the default) uses standard
  pricing."
- "Long context pricing: Claude 4.6 and later models (except Claude Haiku 5.5) … include the full 1M token
  context window at standard pricing."
- Fast mode (Opus 5.5 $8 / $40 per MTok) is a premium a request opts into; the adapter's request does not
  ask for it.

## https://platform.claude.com/docs/en/api/errors

- "400 - `invalid_request_error` … The API also returns a 400 when usage reaches an organization or
  workspace spend limit you set, except limits on the Claude Code workspace, which can return a 429
  instead."
- "402 - `billing_error`: There's an issue with your billing or payment information."
- "429 - `rate_limit_error`: Your organization has hit a rate limit, reached its usage tier's monthly spend
  cap, or reached a spend limit on the Claude Code workspace."
- "500 - `api_error`: An unexpected error has occurred internal to Anthropic's systems."
- "504 - `timeout_error`: The request timed out while processing."
- "529 - `overloaded_error`: The API is temporarily overloaded."
- "The official SDK automatically retries transient failures (such as connection errors, rate limits, and
  5xx server errors) with exponential backoff, twice by default".
- No statement on these pages says whether a request answered with an error status (in particular 504
  `timeout_error`, "timed out while processing") is billed: NOT VERIFIED either way.
