// Package corpus runs the AI eval corpus (testdata/ai-evals) through the
// context builder and the deterministic fake provider (make ai-eval).
// TestFakeProviderEval (T-024) checks the context each case would send
// (tenant filter, untrusted data kept out of the rules, redaction, the ID
// pool, budget, output_locale) and that the provider boundary refuses
// trusted fields and unsafe answers. TestReviewEval (T-025) runs every
// modeler case's hand-written reference answer and unsafe answers through
// the checked client and the server's review (internal/ai/review): golden
// edits reach ready at or above their risk floor from the server alone,
// needs_input keeps no change, unsafe answers are never ready. Neither
// scores a model's own answers, database effects or analyst grounding
// (T-026, T-030, T-038), and neither calls a live provider: fake results
// only (TEST_PLAN §3).
package corpus
