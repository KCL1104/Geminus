package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
)

// PostgreSQL text refuses U+0000 (SQLSTATE 22021) and jsonb refuses
// \u0000 (22P05), and an empty summary breaks 0012's content checks: an
// answer that holds either is not a proposal the server can store, so the
// review makes it invalid at the place it is, with a fixed message, instead
// of a ready or needs_input outcome whose write fails and is retried,
// calling the provider again each time (H-003 review M2).
func TestUnstorableText(t *testing.T) {
	raise := `[{"op":"test","path":"/resources/1/id","value":"ci_runner"},{"op":"replace","path":"/resources/1/capacity","value":6}]`
	for name, c := range map[string]struct {
		edit func(*provider.ProposalOutput)
		path string
	}{
		"NUL in the summary":        {func(o *provider.ProposalOutput) { o.Summary = "a\x00b" }, "/summary"},
		"empty summary":             {func(o *provider.ProposalOutput) { o.Summary = "" }, "/summary"},
		"NUL in an assumption":      {func(o *provider.ProposalOutput) { o.Assumptions = []string{"fine", "c\x00d"} }, "/assumptions/1"},
		"empty assumption":          {func(o *provider.ProposalOutput) { o.Assumptions = []string{""} }, "/assumptions/0"},
		"NUL in an expected effect": {func(o *provider.ProposalOutput) { o.ExpectedEffects = []string{"\x00"} }, "/expected_effects/0"},
		"empty expected effect":     {func(o *provider.ProposalOutput) { o.ExpectedEffects = []string{"Waiting may fall.", ""} }, "/expected_effects/1"},
		"NUL in a patch value": {func(o *provider.ProposalOutput) {
			o.Changes = ops(t, `[{"op":"replace","path":"/name","value":"x\u0000y"}]`)
		}, "/changes/0/value"},
		"NUL deep in a patch value": {func(o *provider.ProposalOutput) {
			o.Changes = ops(t, `[{"op":"test","path":"/nodes/2/id","value":"ci"},{"op":"replace","path":"/nodes/2/service_time/distribution","value":{"type":"constant","value":1,"`+marker+`":["\u0000"]}}]`)
		}, "/changes/1/value"},
		"NUL in a member name": {func(o *provider.ProposalOutput) {
			o.Changes = ops(t, `[{"op":"add","path":"/metadata","value":{"a\u0000b":1}}]`)
		}, "/changes/0/value"},
		"NUL in a patch path": {func(o *provider.ProposalOutput) {
			o.Changes = ops(t, `[{"op":"replace","path":"/name\u0000","value":"x"}]`)
		}, "/changes/0/path"},
	} {
		t.Run(name, func(t *testing.T) {
			out := proposed(ops(t, raise), "low")
			c.edit(&out)
			o := run(t, input(t, provider.ModeEdit, out))
			wantInvalid(t, o, OutputInvalid, c.path)
		})
	}
	for name, c := range map[string]struct {
		edit func(*provider.ProposalOutput)
		path string
	}{
		"NUL in a question":         {func(o *provider.ProposalOutput) { o.Questions = []string{"Which\x00step?"} }, "/questions/0"},
		"empty question":            {func(o *provider.ProposalOutput) { o.Questions = []string{"Which step?", ""} }, "/questions/1"},
		"empty needs_input summary": {func(o *provider.ProposalOutput) { o.Summary = "" }, "/summary"},
		"NUL in its summary":        {func(o *provider.ProposalOutput) { o.Summary = "\x00" }, "/summary"},
	} {
		t.Run(name, func(t *testing.T) {
			out := provider.ProposalOutput{Status: "needs_input", Summary: "Which step?", Assumptions: []string{},
				Questions: []string{"Which step should change?"}, RequiresConfirmation: true}
			c.edit(&out)
			wantInvalid(t, run(t, input(t, provider.ModeEdit, out)), OutputInvalid, c.path)
		})
	}
}
