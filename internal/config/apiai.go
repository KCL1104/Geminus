package config

import (
	"errors"

	"github.com/kcl1104/geminus/internal/config/aiapproval"
)

// APIAI is what the API knows of AI (T-053 I5, the T-055 follow-up; the
// coordinator's decision): which provider the proposals ask, and whether
// the API accepts them for a live one. The API never calls a provider (the
// worker does, with the keys only it holds), so it decides from non-secret
// settings only: AI_PROVIDER, AI_POLICY_APPROVAL, AI_MODEL and
// GEMINUS_AI_LIVE, through the worker's own live gate but for the keys
// (liveGate: an approval this build records, of the provider, lacking
// nothing; AI_MODEL its model, with a pinned price; the switch on). It never
// reads or requires a key, and any AI key variable in its environment
// (apiKeyVars) stops it (CodeKeyForbidden).
//
// So the API and a worker with the same settings agree on whether live AI
// is on and on why it is off. A worker that cannot ask the provider all the
// same (the switch on without its key: it refuses to start; its key
// refused) fails each proposal with a typed code, or leaves it waiting for
// a worker that starts; the API has written only the proposal and its job.
//
// The worker's other AI settings (the photo path, the spend limits, the
// budget) are not the API's and are not read.
type APIAI struct {
	Provider AIProvider
	// Approval is the recorded approval AI_POLICY_APPROVAL names once the
	// API accepts live proposals; the zero value otherwise. Only
	// aiapproval.Recorded makes one.
	Approval aiapproval.Approval
	// Model is AI_MODEL, the approval's model, once the API accepts live
	// proposals; empty otherwise.
	Model string
	// LiveOff is why the API refuses the live provider's proposals: as
	// AI.LiveOff, the reason the worker's live AI is off with the same
	// settings (a LiveOff* reason or what the record lacks). Empty when it
	// accepts them or no live provider is selected.
	LiveOff string
}

// LiveEnabled reports whether the API accepts proposals for the live
// provider: a live provider with a valid approval of its own.
func (a APIAI) LiveEnabled() bool {
	return a.Provider.live() && a.Approval.Valid() && a.Approval.Provider() == string(a.Provider)
}

// CodeKeyForbidden: an AI key variable is set in the API's environment
// (T-053 I5).
const CodeKeyForbidden = "key_forbidden"

// apiKeyVars are the AI key variables the API refuses (T-053 I5): the live
// adapters' keys, the Meta key's earlier name, the legacy AI_API_KEY, and
// the defaults of the provider SDKs and documentation, which nothing in this
// build reads (D-018). scripts/check_deploy.py reads this list:
// deploy/railway/variables.json forbids each of them on the API.
var apiKeyVars = []string{
	"GEMINUS_ANTHROPIC_API_KEY_A", "GEMINUS_ANTHROPIC_API_KEY_B", "GEMINUS_MUSE_API_KEY", "GEMINUS_META_API_KEY",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AI_API_KEY", "MODEL_API_KEY",
}

// LoadAPIAI reads the API's AI settings (APIAI). All problems are reported
// together, as in Load: a key variable set, then AI_PROVIDER, the switch
// and the approval as LoadAI reports them. With the switch on, a setting
// the worker's live gate refuses is an error (the API refuses to start, as
// the worker does); with it off, the API starts and LiveOff says why.
//
// A key variable is set when it holds a value: getenv's answer is compared
// with "" and dropped, never kept, compared with anything else or printed;
// the error names the variable only.
func LoadAPIAI(getenv func(string) string) (APIAI, error) {
	var errs []error
	for _, name := range apiKeyVars {
		if getenv(name) != "" {
			errs = append(errs, &Error{name, CodeKeyForbidden,
				"the API never holds an AI key: only the worker asks a provider (T-053 I5); remove the variable from the API's environment"})
		}
	}
	env := getenv("APP_ENV")
	approval := getenv("AI_POLICY_APPROVAL")
	p, perrs := aiProvider(getenv, env == "development" || env == "test")
	errs = append(errs, perrs...)
	on, serrs := switchOn(getenv, p, approval)
	errs = append(errs, serrs...)

	a := APIAI{Provider: p}
	switch {
	case approval == "" && p.live():
		a.LiveOff = LiveOffNoApproval
	case approval == "":
	case !p.live():
		errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeInvalid, "must be empty unless AI_PROVIDER is anthropic or meta"})
	default:
		model, why, gerrs := liveGate(getenv, p, approval, "AI_POLICY_APPROVAL", "AI_MODEL", liveSwitch, on, false)
		errs = append(errs, gerrs...)
		a.LiveOff = why
		if why == "" && len(gerrs) == 0 {
			rec, ok := aiapproval.Recorded(approval)
			if !ok { // the gate passed: Recorded agrees, unless the record changed meanwhile
				errs = append(errs, &Error{"AI_POLICY_APPROVAL", CodeNotApproved, "the recorded approval cannot be used"})
			}
			a.Approval, a.Model = rec, model
		}
	}
	if len(errs) > 0 {
		return APIAI{}, errors.Join(errs...)
	}
	return a, nil
}
