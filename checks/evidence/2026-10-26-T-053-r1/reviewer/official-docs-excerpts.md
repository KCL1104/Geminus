# Official documentation excerpts (fetched 2026-10-26 by the reviewer)

Fetched with WebFetch (documentation pages only; no API call). Verbatim lines.

## https://platform.claude.com/docs/en/about-claude/pricing.md

Model table header and the Opus 5.5 row:

    | Model | Base input tokens | 5m cache writes | 1h cache writes | Cache hits and refreshes | Output tokens |
    | Claude Opus 5.5 | $4 / MTok | $5 / MTok | $8 / MTok | $0.20 / MTok<sup>2</sup> | $20 / MTok |

    2 Cache hits and refreshes on Claude Opus 5.5 and Claude Sonnet 5.5 are priced at 0.05x the base input price.

Long context:

    Claude 4.6 and later models (except Claude Haiku 5.5) and Claude Mythos Preview include the full 1M token
    context window at standard pricing.

Data residency:

    For Claude 4.6 and later models, specifying US-only inference through the `inference_geo` parameter incurs a
    1.1x multiplier on all token pricing categories, including input tokens, output tokens, cache writes, and cache
    reads. ... Global routing (the default) uses standard pricing.

## https://platform.claude.com/docs/en/manage-claude/data-residency

    * **Inference geo:** Controls where model inference runs, on a per-request basis. Set through the
      `inference_geo` API parameter or as a workspace default.

    * **`default_inference_geo`:** Sets the fallback geo when `inference_geo` is omitted from a request.
      Individual requests can override this by setting `inference_geo` explicitly.

    If your organization previously opted out of global routing to keep inference in the US, your workspace has
    been automatically configured with `allowed_inference_geos: ["us"]` and `default_inference_geo: "us"`.

    The response `usage` object includes an `inference_geo` field indicating where inference ran.

## https://platform.claude.com/docs/en/manage-claude/api-and-data-retention

    Conversation content (your prompts and Claude's outputs) is not retained by default; the exception is
    Covered Models, which require 30-day retention.

    ZDR is enabled per organization; each new organization requires ZDR to be enabled separately by your account
    team, and enablement does not automatically extend to ot[her organizations ...]

    * **Claude Console:** Any usage in the Claude Console, including playground.   (listed as outside ZDR)

    Your prompts and Claude's outputs are not stored. Only the JSON schema is cached, for up to 24 hours since last
    us[e.]   (structured outputs row)

    ... if a chat or session is flagged, Anthropic may retain inputs and outputs for up to 2 years.

    Claude Fable 5.1, Claude Mythos 5.1, Claude Fable 5, and Claude Mythos 5 are designated Covered Models ...
    (Claude Opus 5.5 is not listed.)
