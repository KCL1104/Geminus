// Package structured is the provider's view of a canonical output schema
// (AI_SPEC §2): the projection of a schema in schemas/ into the
// structured-output subset a live provider takes (Project, Projection) and
// the strict, fail-closed decoding of the projection's encoded values
// (Projection.Decode), which runs on every answer before provider.Client
// checks it against the full 2020-12 schema.
//
// T-055 moved projection.go and encoded.go here unchanged from
// internal/ai/provider/anthropic (T-053, reviewed), with their tests, so
// that the Meta adapter (internal/ai/provider/meta) sends the same schema
// and decodes answers by the same rules without linking the Anthropic SDK.
// The Anthropic adapter uses this package through aliases
// (internal/ai/provider/anthropic/structured.go). It is computation only:
// no network, file or process access (architecture rule
// ai-is-pure-but-the-adapter).
package structured
