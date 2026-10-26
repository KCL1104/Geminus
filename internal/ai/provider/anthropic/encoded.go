package anthropic

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strconv"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model"
)

// MaxEncodedValueBytes bounds the JSON text in one encoded string (the
// string's bytes once its own JSON escapes are read). Larger is an invalid
// answer; the whole answer is bounded by max_tokens before and by the
// budget's MaxOutputBytes after the decoding.
const MaxEncodedValueBytes = 64 << 10

// MaxEncodedValueDepth bounds how deeply the JSON text in one encoded
// string nests arrays and objects (review round 1, M1).
const MaxEncodedValueDepth = 64

// maxIssues bounds the issues of one answer, as provider.Client does.
const maxIssues = 20

// strict is how an answer and each JSON text in it are read: one value,
// valid UTF-8, no duplicate member names.
var strict = json.JoinOptions(jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false))

// asWritten writes the answer's values back as the model wrote them: its
// strings' escapes and its numbers unchanged (a rewritten object's members
// come in name order).
var asWritten = jsontext.PreserveRawStrings(true)

// Decode returns the answer with the JSON text at each encoded place (the
// projection's one rule that is not a removal, see Projection) replaced by
// the JSON value it holds, for provider.Client to check against the full
// schema. The places are matched by their path and the const members of
// the alternatives on the way, never by what a string looks like.
//
// An answer without an encoded place that applies comes back byte for
// byte; so does one that is not a single strict JSON document, which the
// client's check refuses. At an encoded place, a value that is not a
// string, or a string that is not exactly one JSON text (strict, at most
// MaxEncodedValueBytes, nothing but white space after it), makes the
// answer invalid: Decode returns no answer and an issue per place, with
// the place's JSON Pointer and a fixed message, never the text.
func (p Projection) Decode(text []byte) ([]byte, []model.ValidationIssue) {
	if len(p.Encoded) == 0 || !jsontext.Value(text).IsValid(strict) {
		return text, nil
	}
	doc := jsontext.Value(text)
	changed := false
	var issues []model.ValidationIssue
	for _, loc := range p.Encoded {
		out, ch, err := rewrite(doc, loc.Steps, "", &issues)
		if err != nil {
			return text, nil // cannot happen for a valid document; the client's check decides
		}
		if ch {
			doc, changed = out, true
		}
	}
	if len(issues) > 0 {
		return nil, issues[:min(len(issues), maxIssues)]
	}
	if !changed {
		return text, nil
	}
	return doc, nil
}

// rewrite applies the rest of a location's steps to v, found at ptr: at
// the end of the steps v is decoded. It reports whether v changed.
func rewrite(v jsontext.Value, steps []Step, ptr string, issues *[]model.ValidationIssue) (jsontext.Value, bool, error) {
	if len(steps) == 0 {
		decoded, msg := decodeText(v)
		if msg != "" {
			*issues = append(*issues, model.NewIssue(provider.OutputInvalid, ptr, msg, model.SeverityError))
			return v, false, nil
		}
		return decoded, true, nil
	}
	st := steps[0]
	if st.Items {
		if v.Kind() != '[' {
			return v, false, nil
		}
		var items []jsontext.Value
		if err := json.Unmarshal(v, &items, strict); err != nil {
			return nil, false, err
		}
		changed := false
		for i := range items {
			out, ch, err := rewrite(items[i], steps[1:], ptr+"/"+strconv.Itoa(i), issues)
			if err != nil {
				return nil, false, err
			}
			if ch {
				items[i], changed = out, true
			}
		}
		if !changed {
			return v, false, nil
		}
		out, err := json.Marshal(items, asWritten)
		return out, err == nil, err
	}
	if v.Kind() != '{' {
		return v, false, nil
	}
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(v, &obj, strict); err != nil {
		return nil, false, err
	}
	for _, c := range st.When {
		member, ok := obj[c.Member]
		var got any
		if !ok || json.Unmarshal(member, &got, strict) != nil || !reflect.DeepEqual(got, c.Const) {
			return v, false, nil // another alternative: not this place
		}
	}
	member, ok := obj[st.Member]
	if !ok {
		return v, false, nil
	}
	out, ch, err := rewrite(member, steps[1:], ptr+"/"+escapeToken(st.Member), issues)
	if err != nil || !ch {
		return v, false, err
	}
	obj[st.Member] = out
	data, err := json.Marshal(obj, asWritten, json.Deterministic(true))
	return data, err == nil, err
}

// decodeText is the JSON value held as a JSON text by the string v, or a
// fixed reason it holds none.
func decodeText(v jsontext.Value) (jsontext.Value, string) {
	if v.Kind() != '"' {
		return nil, "the projection asks for a string holding a JSON text here"
	}
	var s string
	if err := json.Unmarshal(v, &s, strict); err != nil {
		return nil, "the string does not decode"
	}
	if len(s) > MaxEncodedValueBytes {
		return nil, "the JSON text is larger than " + strconv.Itoa(MaxEncodedValueBytes) + " bytes"
	}
	inner := jsontext.Value(s)
	if !inner.IsValid(strict) {
		return nil, "the string is not exactly one JSON text"
	}
	if err := inner.Compact(); err != nil {
		return nil, "the string is not exactly one JSON text"
	}
	return inner, ""
}
