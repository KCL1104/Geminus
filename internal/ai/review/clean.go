package review

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/schemas"
)

// What an issue of a review outcome may say (D-062 (c), AI_SPEC §4,
// SECURITY §7; H-003 review M1). The stages the review runs, and the
// provider client before it, word their messages for a person checking a
// model of their own, so they quote what they refuse: an ID, a member name,
// a value, a path, a samples_ref. A provider's answer is untrusted text that
// must never be stored with a proposal, so every issue the review returns
// (invalid or a ready proposal's warning) is cleaned:
//
//   - its message is a fixed sentence of the server's: the review's own
//     sentences are kept, any other message is replaced with the sentence of
//     its code (sentences), or of an unknown code (otherSentence);
//   - its path keeps only array indexes and the member names the contract
//     schemas declare, up to the first other token: a path such as
//     /views/2d/positions/<a node ID> or /nodes/2/<a member the schema does
//     not have> is cut to its deepest known ancestor.
//
// The code and the severity stay; a code is the server's constant.

// The review's own sentences: fixed text that quotes nothing.
const (
	sentNotAProposal   = "the provider's answer is not an acceptable proposal"
	sentConfirmation   = "every proposal requires a person's confirmation"
	sentChanges        = "a proposal has at least one change"
	sentConfidence     = "a proposal states a confidence from 0 to 1"
	sentRiskClaim      = "the risk claim is low, medium or high"
	sentNoQuestions    = "a proposal asks no questions; one that needs input says so"
	sentQuestions      = "a request for input asks at least one question"
	sentInputNoChanges = "a request for input carries no changes"
	sentStatus         = "the answer is proposed or needs_input"
	sentEmpty          = "the text is empty"
	sentNUL            = "the text holds U+0000, which cannot be stored"
	sentNoOp           = "the changes leave the model as it is"
	sentEffectNumber   = "states a number that is not a value of the base model or the candidate: expected effects are qualitative until a simulation runs (AI_SPEC §5)"
)

// otherSentence replaces the message of a code the catalogue does not have
// (a later pack's).
const otherSentence = "the proposal does not pass the server's review"

// sentences are, for each code of the catalogue
// (common.schema.json#/$defs/ValidationCode), the messages an issue of that
// code may keep; the first replaces any other.
var sentences = map[string][]string{
	validate.JSONInvalid:                {"the document is not valid JSON"},
	validate.IRSchemaInvalid:            {"a value has the wrong shape or type"},
	validate.LimitExceeded:              {"a size, depth or count limit is exceeded"},
	validate.DuplicateID:                {"an ID is used more than once"},
	validate.UnknownReference:           {"it refers to an entity that does not exist"},
	validate.ViewReferenceInvalid:       {"the layout refers to a node that does not exist"},
	validate.RoutingInvalid:             {"the outgoing routing probabilities are not valid"},
	validate.NoReachableSink:            {"no sink can be reached from this node"},
	validate.ZeroTimeCycle:              {"a reachable cycle takes no time"},
	validate.UnreachableNode:            {"no source reaches this node"},
	validate.ResourceRequirementInvalid: {"the resource requirement is not valid"},
	validate.DistributionInvalid:        {"the distribution is not valid"},
	validate.ConstraintFailed:           {"a model constraint is not met"},
	validate.DatasetBindingInvalid:      {"the dataset binding is missing, not allowed or does not match its stored snapshot"},
	"DOMAIN_MISMATCH":                   {"the model does not belong to the domain pack's domain"}, // the P0 pack's code
	patch.SchemaInvalid:                 {"the change is not an operation of the patch profile"},
	patch.PathInvalid:                   {"the change's path is not a valid JSON Pointer into the model"},
	patch.PathForbidden:                 {"the change's path is not one this mode may change (D-020, D-021, D-022)"},
	patch.TestRequired:                  {"the change must first test the ID of the entity it edits (D-020)"},
	patch.TargetMissing:                 {"the change's target does not exist"},
	patch.TestFailed:                    {"a test operation does not match the base model"},
	IDPoolViolation: {poolReasons[idpool.NotInPool], poolReasons[idpool.WrongKind], poolReasons[idpool.UsedInBase],
		poolReasons[idpool.Missing], poolReasons[idpool.NoCalendarIDs]},
	OutputInvalid: {sentNotAProposal, sentConfirmation, sentChanges, sentConfidence, sentRiskClaim, sentNoQuestions,
		sentQuestions, sentInputNoChanges, sentStatus, sentEmpty, sentNUL, sentNoOp, sentEffectNumber},
	// The line-flow codes (ADR-026, T-046 phase 2). Their rules land in
	// T-047–T-051; the primitive gate's messages are fixed and quote
	// nothing, so they are kept (D-063, proposed).
	validate.BlockingCycle:          {"full buffers could block each other in a cycle"},
	validate.CalendarInvalid:        {"a shift calendar is not valid, or a bundle's resources are never on shift together"},
	validate.ReleaseScheduleInvalid: {"the release schedule is not valid"},
	validate.BatchInvalid:           {"the batch sizes or the waiting limit are not valid"},
	validate.SetupInvalid:           {"the changeover settings are not valid"},
	validate.StallRisk:              {"a resource that breaks down can be on shift while a partner in its bundle is not"},
	validate.PrimitiveNotSupported:  gateSentences(),
}

// gateSentences are the primitive gate's own fixed messages, after a
// sentence for any other.
func gateSentences() []string {
	out := []string{"the model uses a line-flow member this build does not run yet"}
	for _, p := range validate.Primitives {
		out = append(out, validate.NotSupported(validate.Use{Primitive: p}).Message)
	}
	return out
}

// moreIssues is capped's message: a count the server made.
var moreIssues = regexp.MustCompile(`^[0-9]{1,9} more issues are not listed$`)

// members are the member names the contract schemas of the documents the
// review's issue paths point into declare: the candidate model
// (model-ir.schema.json and the common definitions), the proposal's changes
// (json-patch.schema.json), the provider's answer
// (proposal-output.schema.json) and the proposal with its dataset manifest
// (proposal.schema.json).
var members = schemaMembers("model-ir.schema.json", "common.schema.json", "json-patch.schema.json",
	"proposal-output.schema.json", "proposal.schema.json")

func schemaMembers(files ...string) map[string]bool {
	out := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name := range props {
					out[name] = true
				}
			}
			for _, x := range v {
				collect(x)
			}
		case []any:
			for _, x := range v {
				collect(x)
			}
		}
	}
	for _, f := range files {
		data, err := schemas.FS.ReadFile(f)
		if err != nil {
			panic(fmt.Sprintf("review: the embedded %s: %v", f, err)) // a build without the contract
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			panic(fmt.Sprintf("review: the embedded %s: %v", f, err))
		}
		collect(doc)
	}
	return out
}

var arrayIndex = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})$`)

// knownPath is path up to its first token that is neither an array index
// nor a member name of the schemas; "" for a path that is not a JSON
// Pointer.
func knownPath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	tokens := strings.Split(path[1:], "/")
	n := 0
	for _, tok := range tokens {
		if !arrayIndex.MatchString(tok) && !members[tok] {
			break
		}
		n++
	}
	if n == 0 {
		return ""
	}
	return "/" + strings.Join(tokens[:n], "/")
}

// Clean is issue as the review stores it: a fixed message of the server's
// and a path of known tokens. An issue the review returned is its own
// Clean.
func Clean(is model.ValidationIssue) model.ValidationIssue {
	message := is.Message
	allowed, known := sentences[is.Code]
	switch {
	case is.Code == validate.LimitExceeded && moreIssues.MatchString(message):
	case known && slices.Contains(allowed, message):
	case known:
		message = allowed[0]
	default:
		message = otherSentence
	}
	return model.NewIssue(is.Code, knownPath(is.Path), message, is.Severity)
}

// nulAt is the JSON Pointer of the first string of doc, a valid JSON
// document, that holds U+0000: the value's, or for a member name the
// object's (so that the pointer never holds the name).
func nulAt(doc []byte) (string, bool) {
	if !bytes.Contains(doc, []byte(`\u0000`)) {
		return "", false
	}
	dec := jsontext.NewDecoder(bytes.NewReader(doc))
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			return "", false
		}
		if tok.Kind() == '"' && strings.ContainsRune(tok.String(), 0) {
			p := dec.StackPointer()
			if kind, n := dec.StackIndex(dec.StackDepth()); kind == '{' && n%2 == 1 {
				p = p.Parent()
			}
			return string(p), true
		}
	}
}

func cleaned(issues []model.ValidationIssue) []model.ValidationIssue {
	out := make([]model.ValidationIssue, len(issues))
	for i, is := range issues {
		out[i] = Clean(is)
	}
	return out
}
