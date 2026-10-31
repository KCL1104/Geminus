package generator

import (
	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/repair"
	"github.com/kcl1104/geminus/internal/audit"
)

// One repair after the server's check (H-057, internal/ai/repair): when the
// server refuses the first answer for what it says, the attempt asks the
// model once more (generateAsked, and photopipeline.Propose for a photo)
// and writes the second answer's outcome. The row's provider metadata stays
// the first call's (its template, input hash, web search and sources; for a
// photo, its image and segmentation) and gains "repair": what came of it
// and the second call's metadata. Neither answer is kept: the second
// answer's content is the proposal's own only in the states that keep what
// the provider wrote (needs_input, ready), as for any first answer.

// repairMeta is what the stored metadata says of a repair: what came of it
// (ready, needs_input or invalid: the second answer's review; failed: the
// second call failed, code says how, and the outcome is the first one's),
// how many issues the follow-up listed and left out, and the second call's
// metadata (no prompt, no answer, no key).
type repairMeta struct {
	Outcome   string             `json:"outcome"`
	Code      string             `json:"code,omitzero"`
	Issues    int                `json:"issues"`
	NotListed int                `json:"issues_not_listed,omitzero"`
	Call      *provider.Metadata `json:"call,omitzero"`
}

// repairMetadata is base, a generated row's provider metadata, with the
// repair of rec ("repair"); base itself when there was none.
func repairMetadata(base []byte, rec *repair.Record) ([]byte, error) {
	if rec == nil {
		return base, nil
	}
	var doc map[string]jsontext.Value
	if err := json.Unmarshal(base, &doc); err != nil {
		return nil, err
	}
	r, err := json.Marshal(repairMeta{Outcome: rec.Outcome, Code: rec.Code, Issues: rec.Issues, NotListed: rec.NotListed, Call: rec.Call})
	if err != nil {
		return nil, err
	}
	doc["repair"] = r
	return json.Marshal(doc, json.Deterministic(true))
}

// repairAuditMeta adds to an audit event's metadata what it records of a
// repair (the catalogue's repairCall fields): what came of it, a failed
// call's code, how many issues it listed, and the second call's template,
// hashes, tokens, latency, provider request ID and spend; never the prompt
// or an answer.
func repairAuditMeta(out map[string]any, rec *repair.Record) {
	if rec == nil {
		return
	}
	if rec.Outcome != "" {
		out["repair"] = rec.Outcome
	}
	out["repair_issues"] = rec.Issues
	if rec.Code != "" {
		out["repair_code"] = rec.Code
	}
	c := rec.Call
	if c == nil {
		return
	}
	out["repair_input_tokens"], out["repair_output_tokens"], out["repair_latency_ms"] = c.Usage.InputTokens, c.Usage.OutputTokens, c.LatencyMillis
	for k, v := range map[string]string{"repair_prompt_template_version": audit.Label(c.TemplateVersion), "repair_input_hash": c.InputHash,
		"repair_output_hash": c.OutputHash, "repair_provider_request_id": audit.Label(c.ProviderRequestID)} {
		if v != "" {
			out[k] = v
		}
	}
	if c.SpendReservation != "" {
		out["repair_cost_nano_usd"], out["repair_spend_reservation_id"], out["repair_spend_unsettled"] = c.CostNanoUSD, c.SpendReservation, c.SpendUnsettled
	}
}
