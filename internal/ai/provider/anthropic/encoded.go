package anthropic

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
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
// string nests arrays and objects ([] and {} are one level, as
// validate.MaxContainerDepth counts them). Deeper is an invalid answer.
//
// Why 64 (review round 1, M1): a JSON Patch value is a part of a model IR,
// and a valid IR is at most 11 levels deep: 7 for its own structure, and
// a node's domain_attributes (an object at level 4) may itself be
// validate.MaxContainerDepth = 8 levels deep (measured from
// schemas/model-ir.schema.json: checks/evidence/2026-10-26-T-053-schema-r1/
// fixes/ir-depth.log). A patch value sits below the root, so none is deeper
// than 10. 64 leaves room for the IR to grow and is far below jsontext's
// own limit of 10000. That limit is the point: Decode puts the value back
// into the answer (at /changes/i/value, three levels down) and re-encodes
// it, and before the bound a JSON text valid on its own at 9998 levels was
// deeper than the encoder takes once placed there. With the bound the
// rewritten proposal answer is at most 3 + 64 levels deep.
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
// A strict JSON answer without an encoded place that applies comes back
// byte for byte. At an encoded place, a value that is not a string, or a
// string that is not exactly one JSON text (strict, at most
// MaxEncodedValueBytes, nested at most MaxEncodedValueDepth levels,
// nothing but white space after it), makes the answer invalid: Decode
// returns no answer and an issue per place (at most maxIssues), with the
// place's JSON Pointer and a fixed message, never the text.
//
// Decode fails closed (review round 1, M1). An answer that is not one
// strict JSON document, and one it cannot rewrite, give no answer and an
// issue at the root (after the issues already found at the places), never
// the undecoded text: the full schema could take that text as it is, since
// a patch value accepts any JSON value, a string the model did not encode
// included.
func (p Projection) Decode(text []byte) ([]byte, []model.ValidationIssue) {
	if len(p.Encoded) == 0 {
		return text, nil // nothing to decode: the client checks the answer
	}
	if !jsontext.Value(text).IsValid(strict) {
		return nil, []model.ValidationIssue{model.NewIssue(provider.OutputInvalid, "", "the answer is not one strict JSON document", model.SeverityError)}
	}
	doc := jsontext.Value(text)
	changed := false
	var issues []model.ValidationIssue
	for _, loc := range p.Encoded {
		out, ch, err := rewrite(doc, loc.Steps, "", &issues)
		if err != nil {
			// With MaxEncodedValueDepth no output schema's place reaches
			// this; whatever the cause, nothing undecoded goes on.
			cannot := model.NewIssue(provider.OutputInvalid, "", "the answer cannot be decoded", model.SeverityError)
			return nil, append(issues[:min(len(issues), maxIssues-1)], cannot)
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
	if deeper(inner, MaxEncodedValueDepth) {
		return nil, "the JSON text is nested deeper than " + strconv.Itoa(MaxEncodedValueDepth) + " levels"
	}
	return inner, ""
}

// deeper reports whether the valid JSON text v nests arrays and objects
// more than limit levels. A read error other than the end, which cannot
// happen for a valid text, counts as deeper: fail closed.
func deeper(v jsontext.Value, limit int) bool {
	dec := jsontext.NewDecoder(bytes.NewReader(v), strict)
	for {
		if _, err := dec.ReadToken(); err != nil {
			return !errors.Is(err, io.EOF)
		}
		if dec.StackDepth() > limit {
			return true
		}
	}
}
