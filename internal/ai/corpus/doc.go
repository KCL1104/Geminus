// Package corpus runs the AI eval corpus (testdata/ai-evals) through the
// context builder and the deterministic fake provider (make ai-eval,
// T-024). It checks what T-024 owns: the context each case would send
// (tenant filter, untrusted data kept out of the rules, redaction, the
// ID pool, budget, output_locale) and that the provider boundary refuses
// trusted fields and unsafe answers. It does not score proposals, risk,
// database effects or analyst grounding (T-025, T-026, T-030, T-038), and
// it never calls a live provider: fake results only (TEST_PLAN §3).
package corpus
