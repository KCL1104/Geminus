package audit

import "testing"

// Every action can record all the fields its catalogue entry declares: a
// ready photo proposal's proposal.generate (the provider's call, the photo's
// segmentation and the risk) failed live with "more than 32 fields" and was
// retried with the AI called again each time.
func TestEveryDeclaredFieldFits(t *testing.T) {
	for action, s := range catalogue {
		if len(s.fields) > MaxFields {
			t.Errorf("%s declares %d fields, more than MaxFields %d", action, len(s.fields), MaxFields)
		}
	}
}
