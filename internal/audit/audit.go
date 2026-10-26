// Package audit is the catalogue of audit events (T-035, REQ-018, D-091):
// every action a business write records, who may record it, what it
// targets and which metadata it may hold, each value with the shape it
// must have. internal/storage/auditlog checks every event against it
// before the row is written, in the write's own transaction, so an event
// that does not fit fails the write instead of storing what it should
// not (fail closed).
//
// Metadata holds IDs, hashes, counts, flags, codes, closed enumerations and
// short labels only (SECURITY §9, AI_SPEC §10): there is no free-text
// shape, so a name, a comment, an intent, a column name or value, a file
// name, a patch, a prompt, a provider's answer or chain-of-thought cannot
// be recorded under any key; a label that holds a credential or an e-mail
// address is refused as well (observability.Sensitive). A refusal names
// the action and the field, never the value.
package audit

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/observability"
)

// ActorType is twin.audit_events.actor_type.
type ActorType string

const (
	User   ActorType = "user"
	Worker ActorType = "worker"
	System ActorType = "system"
)

// Event is one audit row as the server writes it.
type Event struct {
	ActorType  ActorType
	ActorID    string
	Action     string
	TargetType string
	TargetID   string
	RequestID  string
	Metadata   map[string]any
}

// ErrInvalid is the error of an event the catalogue refuses
// (*InvalidError wraps it).
var ErrInvalid = errors.New("audit: the event does not fit the catalogue")

// InvalidError says which part of an event did not fit: never its value.
type InvalidError struct {
	Action, Field, Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("audit: %s: %s: %s", e.Action, e.Field, e.Reason)
}

func (e *InvalidError) Unwrap() error { return ErrInvalid }

// MaxFields bounds an event's metadata.
const MaxFields = 32

// maxLabels bounds a label list (failed_over_from).
const maxLabels = 8

var (
	// actorRef is twin.actor_ref (0001): a worker's or the system's name
	// (D-092).
	actorRef = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
	// serverID is a D-001 ID; its first submatch the kind prefix.
	serverID = regexp.MustCompile(`^([a-z]{2,4})_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	hashRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	codeRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	labelRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,127}$`)
	keyRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ValidActor reports whether s may name a worker or the system in an
// audit event (D-092): what the database's twin.actor_ref allows.
func ValidActor(s string) bool { return actorRef.MatchString(s) }

// validID reports whether s is a D-001 ID of kind prefix.
func validID(prefix, s string) bool {
	m := serverID.FindStringSubmatch(s)
	return m != nil && m[1] == prefix
}

// shape is a metadata value's allowed form.
type shape uint8

const (
	id     shape = iota + 1 // a D-001 ID of field.prefix
	hash                    // sha256:<64 hex>, or "" when there is none
	count                   // an integer ≥ 0
	flag                    // a boolean
	code                    // an UPPER_SNAKE code
	enum                    // one of field.values
	label                   // a short name that is no credential, or ""
	labels                  // up to maxLabels labels
)

type field struct {
	shape  shape
	prefix string
	values []string
}

func idOf(prefix string) field     { return field{shape: id, prefix: prefix} }
func oneOf(values ...string) field { return field{shape: enum, values: values} }
func fieldsOf(m ...map[string]field) map[string]field {
	out := map[string]field{}
	for _, x := range m {
		for k, v := range x {
			out[k] = v
		}
	}
	return out
}

var (
	roles   = oneOf("owner", "editor", "viewer")
	modes   = oneOf("edit", "scenario")
	locales = oneOf("en", "zh-Hant")
	risks   = oneOf("low", "medium", "high")
	// providerCall is what the generator records of a provider call
	// (provider.Metadata): labels, hashes and counts, never the prompt, the
	// answer or the key (D-018, AI_SPEC §10); empty when the client refused
	// the call before the provider saw it.
	providerCall = map[string]field{
		"provider": {shape: label}, "role": oneOf("modeler", "scenario", "analyst", "importer", ""),
		"prompt_template_version": {shape: label}, "schema": {shape: label}, "output_locale": oneOf("en", "zh-Hant", ""),
		"input_hash": {shape: hash}, "input_tokens": {shape: count}, "output_tokens": {shape: count}, "latency_ms": {shape: count},
		"model": {shape: label}, "credential_label": {shape: label}, "output_hash": {shape: hash},
		"failed_over_from": {shape: labels}, "provider_request_id": {shape: label},
		// T-053: what a metered call cost and the reservation it was
		// charged to.
		"cost_nano_usd": {shape: count}, "spend_reservation_id": idOf("asr"), "spend_unsettled": {shape: flag},
	}
	aiRoles = oneOf("modeler", "scenario", "analyst", "importer")
	// spendTargets are what a live AI call is for (provider.Attribution).
	spendTargets = []string{"ai_proposal", "ai_analysis", "import"}
)

// targets are the target types and the ID kind each names.
var targets = map[string]string{
	"project": "prj", "membership": "usr", "model": "mdl", "model_version": "mv", "import": "imp", "ai_proposal": "aip",
	"scenario": "scn", "simulation": "sim", "organization": "org", "ai_analysis": "ana",
}

// spec is one action: who records it, what it targets, what it may hold.
type spec struct {
	actor   ActorType
	targets []string
	fields  map[string]field
}

// catalogue is every audited action (D-091 (a)).
var catalogue = map[string]spec{
	// API writes: the scoped user, in the request's transaction.
	"project.create":     {User, []string{"project"}, nil},
	"membership.create":  {User, []string{"membership"}, map[string]field{"role": roles}},
	"membership.update":  {User, []string{"membership"}, map[string]field{"role": roles, "previous_role": roles}},
	"membership.delete":  {User, []string{"membership"}, map[string]field{"role": roles}},
	"model.create":       {User, []string{"model"}, map[string]field{"template": {shape: label}, "locale": locales, "version_id": idOf("mv")}},
	"model.draft.update": {User, []string{"model"}, map[string]field{"revision": {shape: count}}},
	"model.draft.reset":  {User, []string{"model"}, map[string]field{"revision": {shape: count}, "version_id": idOf("mv")}},
	"model.publish":      {User, []string{"model_version"}, map[string]field{"version": {shape: count}, "model_id": idOf("mdl")}},
	"import.upload": {User, []string{"import"}, map[string]field{"kind": oneOf("csv_events", "json_events", "json_model"),
		"model_id": idOf("mdl"), "base_version_id": idOf("mv"), "size_bytes": {shape: count}, "upload_content_hash": {shape: hash}}},
	"import.map": {User, []string{"import"}, map[string]field{"mapping_revision": {shape: count}, "columns_imported": {shape: count},
		"columns_left_out": {shape: count}, "metadata_columns": {shape: count}, "confirmed_low_confidence": {shape: count},
		"validations_cancelled": {shape: count}}},
	"proposal.create": {User, []string{"ai_proposal"}, map[string]field{"model_id": idOf("mdl"), "base_version_id": idOf("mv"), "mode": modes,
		"output_locale": locales, "selected_entities": {shape: count}}},
	"proposal.conflict": {User, []string{"ai_proposal"}, map[string]field{"mode": modes, "model_id": idOf("mdl"), "base_version_id": idOf("mv"),
		"latest_version_id": idOf("mv")}},
	"proposal.approve": {User, []string{"ai_proposal"}, map[string]field{"mode": modes, "model_id": idOf("mdl"), "base_version_id": idOf("mv"),
		"patch_hash": {shape: hash}, "server_risk_level": risks, "risk_level": risks, "new_version_id": idOf("mv"), "version": {shape: count},
		"draft_out_of_date": {shape: flag}, "scenario_id": idOf("scn")}},
	"proposal.reject": {User, []string{"ai_proposal"}, map[string]field{"mode": modes, "model_id": idOf("mdl"), "base_version_id": idOf("mv"),
		"from": oneOf("ready", "needs_input")}},
	"scenario.create": {User, []string{"scenario"}, map[string]field{"model_id": idOf("mdl"), "base_version_id": idOf("mv"),
		"patch_hash": {shape: hash}, "operations": {shape: count}}},
	"simulation.create": {User, []string{"simulation"}, map[string]field{"model_version_id": idOf("mv"), "replications": {shape: count},
		"engine_version": {shape: label}, "model_hash": {shape: hash}, "effective_model_hash": {shape: hash}, "config_hash": {shape: hash},
		"dataset_manifest_hash": {shape: hash}, "scenario_id": idOf("scn")}},
	"simulation.cancel": {User, []string{"simulation"}, map[string]field{"effect": oneOf("cancelled", "cancellation_requested")}},
	// Job handlers: the worker, in the attempt's Finish (or Checkpoint)
	// transaction, under the request that queued the job.
	"import.profile": {Worker, []string{"import"}, map[string]field{"attempt": {shape: count}, "status": oneOf("FAILED", "AWAITING_MAPPING"),
		"code": {shape: code}, "rows": {shape: count}, "invalid_rows": {shape: count}, "columns": {shape: count}, "error_report": {shape: flag}}},
	"import.validate": {Worker, []string{"import"}, map[string]field{"attempt": {shape: count}, "mapping_revision": {shape: count},
		"outcome": oneOf("stale", "FAILED", "VALIDATED", "AWAITING_MAPPING"), "code": {shape: code}, "rows": {shape: count},
		"dataset_size_bytes": {shape: count}, "invalid_rows": {shape: count}, "issues": {shape: count},
		"warnings": {shape: count}}}, // T-052: the pack's data-quality warnings (D-059)
	"proposal.generate": {Worker, []string{"ai_proposal"}, fieldsOf(providerCall, map[string]field{"attempt": {shape: count},
		"status": oneOf("ready", "needs_input", "invalid"), "issues": {shape: count}, "patch_hash": {shape: hash},
		"server_risk_level": risks, "provider_risk_claim": oneOf("low", "medium", "high", ""), "risk_level": risks})},
	"proposal.attempt_failed": {Worker, []string{"ai_proposal"}, fieldsOf(providerCall, map[string]field{"attempt": {shape: count},
		"code": {shape: code}, "retryable": {shape: flag}})},
	"simulation.complete":  {Worker, []string{"simulation"}, map[string]field{"attempt": {shape: count}, "result_digest": {shape: hash}, "events": {shape: count}}},
	"simulation.cancelled": {Worker, []string{"simulation"}, map[string]field{"attempt": {shape: count}}},
	// The job loop and its reaper (D-091 (b)): an attempt that ended
	// without a result, in the transaction that records it, on the job's
	// resource. uncorrelated: the job's payload named no request, so the
	// event has a request ID of its own.
	"job.attempt_failed": {Worker, []string{"simulation", "ai_proposal", "import", "ai_analysis"}, map[string]field{"job_id": idOf("job"),
		"job_type": oneOf("simulation", "model_proposal", "import_profile", "import_validate", "import_mapping", "analysis"),
		"attempt":  {shape: count}, "max_attempts": {shape: count}, "code": {shape: code}, "retryable": {shape: flag},
		"outcome": oneOf("requeued", "failed"), "uncorrelated": {shape: flag}}},
	// The spend ledger of live AI (T-053, internal/storage/aispend): the
	// worker, in the transaction that reserves or settles, on what the call
	// is for, under the request that queued it. Amounts in nano-USD;
	// never the prompt, the answer or a key; no deployment-wide total (that
	// would tell one tenant of the others' use).
	"ai_spend.reserve": {Worker, spendTargets, map[string]field{"reservation_id": idOf("asr"), "role": aiRoles,
		"model": {shape: label}, "reserved_nano_usd": {shape: count}, "organization_day_committed_nano_usd": {shape: count}}},
	"ai_spend.settle": {Worker, spendTargets, map[string]field{"reservation_id": idOf("asr"), "role": aiRoles,
		"model": {shape: label}, "reserved_nano_usd": {shape: count}, "settled_nano_usd": {shape: count},
		"outcome": oneOf("usage", "none", "unknown"), "input_tokens": {shape: count}, "output_tokens": {shape: count},
		"cache_write_tokens": {shape: count}, "cache_read_tokens": {shape: count}, "unknown_attempts": {shape: count},
		"credential_label": {shape: label},
		// T-053 review round 1: the geo the answer was priced at (M2), and a
		// settlement above its reservation, recorded at what was billed (L1).
		"inference_geo": oneOf("global", "us", "unknown"), "over_reservation": {shape: flag}}},
	// A refused reservation writes nothing but this event (best effort:
	// the call is refused whether or not it is recorded).
	"ai_spend.refuse": {Worker, spendTargets, map[string]field{"role": aiRoles, "model": {shape: label},
		"charge_nano_usd": {shape: count}, "limit": oneOf("organization_day", "day", "month")}},
	// Provisioning (cmd/provision-tenant, D-042).
	"organization.provision": {System, []string{"organization"}, map[string]field{"owner_user_id": idOf("usr")}},
}

// Actions is the catalogue's action names, sorted.
func Actions() []string {
	out := make([]string, 0, len(catalogue))
	for a := range catalogue {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// Check returns nil when e fits the catalogue, otherwise an *InvalidError
// (errors.Is ErrInvalid).
func Check(e Event) error {
	s, ok := catalogue[e.Action]
	action := e.Action
	if !ok {
		if !actionRe.MatchString(action) {
			action = "<not an action name>"
		}
		return &InvalidError{action, "action", "not in the catalogue"}
	}
	bad := func(field, reason string) error { return &InvalidError{action, field, reason} }
	switch {
	case e.ActorType != s.actor:
		return bad("actor_type", "this action is recorded by a "+string(s.actor)+" actor")
	case e.ActorType == User && !validID("usr", e.ActorID):
		return bad("actor_id", "a user actor is a user ID")
	case e.ActorType != User && !ValidActor(e.ActorID):
		return bad("actor_id", "a worker or system actor is a name of letters, digits, '_', '.', ':' and '-' starting with a letter, at most 128")
	case !slices.Contains(s.targets, e.TargetType):
		return bad("target_type", "not a target of this action")
	case !validID(targets[e.TargetType], e.TargetID):
		return bad("target_id", "not an ID of the target type")
	case !validID("req", e.RequestID):
		return bad("request_id", "not a request ID")
	case len(e.Metadata) > MaxFields:
		return bad("metadata", fmt.Sprintf("more than %d fields", MaxFields))
	}
	for k, v := range e.Metadata {
		name := k
		if !keyRe.MatchString(k) {
			name = "<not a field name>"
		}
		f, ok := s.fields[k]
		if !ok {
			return bad(name, "not a field of this action")
		}
		if reason := f.check(v); reason != "" {
			return bad(name, reason)
		}
	}
	return nil
}

var actionRe = regexp.MustCompile(`^[a-z][a-z_]{0,31}(\.[a-z_]{1,32}){1,2}$`)

// check is "" when v has f's shape, else why not (never v itself).
func (f field) check(v any) string {
	r := reflect.ValueOf(v)
	switch f.shape {
	case count:
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if r.Int() >= 0 {
				return ""
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return ""
		}
		return "not a non-negative integer"
	case flag:
		if r.Kind() == reflect.Bool {
			return ""
		}
		return "not a boolean"
	case labels:
		if r.Kind() != reflect.Slice || r.Type().Elem().Kind() != reflect.String {
			return "not a list of labels"
		}
		if r.Len() > maxLabels {
			return fmt.Sprintf("more than %d labels", maxLabels)
		}
		for i := range r.Len() {
			if s := r.Index(i).String(); s == "" || !isLabel(s) {
				return "not a list of labels"
			}
		}
		return ""
	}
	if r.Kind() != reflect.String {
		return "not a string"
	}
	s := r.String()
	switch f.shape {
	case id:
		if validID(f.prefix, s) {
			return ""
		}
		return "not an ID of kind " + f.prefix
	case hash:
		if s == "" || hashRe.MatchString(s) {
			return ""
		}
		return "not a sha256 hash"
	case code:
		if codeRe.MatchString(s) {
			return ""
		}
		return "not a code"
	case enum:
		if slices.Contains(f.values, s) {
			return ""
		}
		return "not one of the field's values"
	case label:
		if s == "" || isLabel(s) {
			return ""
		}
		return "not a label (letters, digits and ._:/+-, at most 128, no credential)"
	}
	return "unknown shape"
}

// Label is s when a label field accepts it, "" otherwise: for a value the
// server does not choose (a provider's model name or request ID), so an
// odd one is left out of the event instead of failing the write it records.
func Label(s string) string {
	if isLabel(s) {
		return s
	}
	return ""
}

func isLabel(s string) bool {
	return utf8.ValidString(s) && labelRe.MatchString(s) && !observability.Sensitive(s)
}
