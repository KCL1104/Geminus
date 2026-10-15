// Package hashing defines every content hash the platform stores or compares
// (D-006, D-019, D-034): RFC 8785 JSON Canonicalization Scheme (JCS) followed
// by SHA-256, written "sha256:<64 lowercase hex>". docs/TWIN_IR_SPEC.md §5
// lists which bytes each hash covers.
//
// Canonicalization is github.com/gowebpki/jcs (ADR-014), a fork of the
// reference implementation the RFC's author published. Go's json.Marshal,
// sorted maps or Python's sort_keys are not JCS and must not stand in for it.
package hashing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/gowebpki/jcs"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// Prefix starts every hash string.
const Prefix = "sha256:"

// MaxSafeInteger is the largest integer every I-JSON (RFC 7493 §2.2) reader
// holds exactly; seeds and counters are capped at it by the schemas, and
// NormalizeConfig refuses a seed above it.
const MaxSafeInteger = 1<<53 - 1

// ErrInvalid wraps every reason an input cannot be hashed.
var ErrInvalid = errors.New("hashing: input cannot be canonicalized")

// Canonical returns the RFC 8785 canonical UTF-8 form of exactly one JSON
// value. JCS itself rejects lone surrogates and numbers outside binary64; on
// top of it duplicate object member names (also after unescaping) and invalid
// UTF-8 are rejected, never resolved by picking one.
//
// Numbers are binary64 values, as RFC 8785 defines: documents that differ only
// beyond binary64 precision (9007199254740993 and 9007199254740992, or
// 0.30000000000000001 and 0.3) canonicalize, and hash, the same. Members where
// exact integers matter are capped at MaxSafeInteger by the schemas. Rejecting
// such literals here would make Canonical reject its own output (1e17 is
// written 100000000000000000), and with it every re-marshaled document.
//
// Errors carry at most a byte offset, never text from the input: hashed
// documents may hold customer data (member names in free-form metadata
// included), and errors end up in logs.
func Canonical(data []byte) ([]byte, error) {
	if err := profile(data); err != nil {
		return nil, err
	}
	out, err := jcs.Transform(data)
	if err != nil {
		// The profile already rejected malformed JSON, so this is a number
		// outside binary64 (e.g. 1e400) or an invalid string escape. jcs
		// quotes the offending token, so its message is not passed on.
		return nil, fmt.Errorf("%w: a number or string has no RFC 8785 form", ErrInvalid)
	}
	return out, nil
}

// profile checks the duplicate-name and UTF-8 rules of Canonical and that data
// holds exactly one value. jcs enforces the same rules (a differential fuzz of
// 1.6M inputs found nothing profile rejects that jcs accepts); this second,
// independent parser keeps them from resting on one parser, and it is what
// gives errors a kind and a byte offset without quoting the input.
func profile(data []byte) error {
	dec := jsontext.NewDecoder(bytes.NewReader(data)) // rejects duplicate names and invalid UTF-8 by default
	for done := false; ; {
		_, err := dec.ReadToken()
		if err == io.EOF {
			if !done {
				return fmt.Errorf("%w: no JSON value", ErrInvalid)
			}
			return nil
		}
		if err != nil {
			return syntaxError(err)
		}
		if done {
			return fmt.Errorf("%w: data after the first JSON value", ErrInvalid)
		}
		done = dec.StackDepth() == 0
	}
}

// syntaxError reduces a jsontext error to its kind and byte offset. jsontext
// messages and JSON Pointers quote member names and characters of the input.
func syntaxError(err error) error {
	var se *jsontext.SyntacticError
	if !errors.As(err, &se) {
		return fmt.Errorf("%w: unreadable JSON", ErrInvalid)
	}
	what := "invalid JSON"
	if errors.Is(err, jsontext.ErrDuplicateName) {
		what = "duplicate member name"
	}
	return fmt.Errorf("%w: %s at byte %d", ErrInvalid, what, se.ByteOffset)
}

// Sum formats the SHA-256 of b. It does not canonicalize: callers pass
// Canonical output, or raw stored bytes for ContentHash.
func Sum(b []byte) string {
	h := sha256.Sum256(b)
	return Prefix + hex.EncodeToString(h[:])
}

// Of is Sum(Canonical(data)).
func Of(data []byte) (string, error) {
	c, err := Canonical(data)
	if err != nil {
		return "", err
	}
	return Sum(c), nil
}

// Model is model_hash: JCS over the whole IR document, views and metadata
// included, array order preserved. effective_model_hash is the same function
// over the patched candidate (internal/model/patch.Result.Candidate), so a
// baseline run's effective hash equals its model hash.
func Model(ir []byte) (string, error) { return Of(ir) }

// Patch is patch_hash over a JSON Patch array (D-019). The baseline patch []
// has a hash like any other; a document that is not an array is rejected.
func Patch(patch []byte) (string, error) {
	c, err := Canonical(patch)
	if err != nil {
		return "", err
	}
	if c[0] != '[' {
		return "", fmt.Errorf("%w: a patch is a JSON array", ErrInvalid)
	}
	return Sum(c), nil
}

// ContentHash is a dataset's content_hash: SHA-256 of the exact stored object
// bytes (D-019). The bytes are not parsed or canonicalized first, and the
// snapshot does not contain its own hash.
func ContentHash(raw []byte) string { return Sum(raw) }

// SortManifest returns a copy of bindings ordered by the bytes of logical_ref
// (D-034; SQL uses ORDER BY logical_ref COLLATE "C"). A repeated logical_ref
// is an error: a manifest maps each reference to exactly one dataset version.
func SortManifest(bindings []model.DatasetBinding) ([]model.DatasetBinding, error) {
	out := slices.Clone(bindings)
	slices.SortStableFunc(out, func(a, b model.DatasetBinding) int { return strings.Compare(a.LogicalRef, b.LogicalRef) })
	for i := 1; i < len(out); i++ {
		if out[i].LogicalRef == out[i-1].LogicalRef {
			return nil, fmt.Errorf("%w: logical_ref is bound twice in the manifest", ErrInvalid)
		}
	}
	if out == nil {
		out = []model.DatasetBinding{}
	}
	return out, nil
}

// Manifest is dataset_manifest_hash: JCS over the sorted binding array. A
// model without empirical distributions has the hash of [].
func Manifest(bindings []model.DatasetBinding) (string, error) {
	sorted, err := SortManifest(bindings)
	if err != nil {
		return "", err
	}
	return marshalOf(sorted)
}

// ConfigPayload is the normalized run configuration config_hash covers
// (D-019): durations become ticks, so equal configurations written in
// different units share a hash.
type ConfigPayload struct {
	HorizonTicks int64                 `json:"horizon_ticks"`
	WarmupTicks  int64                 `json:"warmup_ticks"`
	Replications int                   `json:"replications"`
	Seed         int64                 `json:"seed"`
	TraceLevel   simulation.TraceLevel `json:"trace_level"`
}

// NormalizeConfig quantizes cfg's durations with model.Ticks. It only refuses
// what cannot be hashed (bad units, negative or non-finite durations, the
// 365 day cap, a seed outside 0..MaxSafeInteger); range rules such as
// warmup < horizon belong to the run API.
func NormalizeConfig(cfg simulation.RunConfig) (ConfigPayload, error) {
	horizon, err := model.Ticks(cfg.Horizon.Value, cfg.Horizon.Unit)
	if err != nil {
		return ConfigPayload{}, fmt.Errorf("%w: horizon: %v", ErrInvalid, err)
	}
	warmup, err := model.Ticks(cfg.Warmup.Value, cfg.Warmup.Unit)
	if err != nil {
		return ConfigPayload{}, fmt.Errorf("%w: warmup: %v", ErrInvalid, err)
	}
	if cfg.Seed < 0 || cfg.Seed > MaxSafeInteger {
		return ConfigPayload{}, fmt.Errorf("%w: seed is outside 0..%d", ErrInvalid, int64(MaxSafeInteger))
	}
	return ConfigPayload{
		HorizonTicks: horizon, WarmupTicks: warmup,
		Replications: cfg.Replications, Seed: cfg.Seed, TraceLevel: cfg.TraceLevel,
	}, nil
}

// Config is config_hash: JCS over NormalizeConfig(cfg).
func Config(cfg simulation.RunConfig) (string, error) {
	p, err := NormalizeConfig(cfg)
	if err != nil {
		return "", err
	}
	return marshalOf(p)
}

// resultMembers are the Result members result_digest covers
// (docs/SIMULATION_SPEC.md §9). run_id, status and trace are excluded, and so
// is reproducibility.result_digest itself.
var resultMembers = []string{"engine_version", "metrics", "replication_summaries", "measurement_window", "reproducibility"}

// Result is result_digest over r. Numbers keep their full binary64 values. A
// non-finite metric cannot be marshaled and is an error, never a digest.
func Result(r simulation.Result) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var all map[string]jsontext.Value
	if err := json.Unmarshal(data, &all); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	var repro map[string]jsontext.Value
	if err := json.Unmarshal(all["reproducibility"], &repro); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	delete(repro, "result_digest")
	payload := make(map[string]any, len(resultMembers))
	for _, m := range resultMembers {
		payload[m] = all[m]
	}
	payload["reproducibility"] = repro
	return marshalOf(payload)
}

// marshalOf hashes a Go value. json.Marshal output is only an intermediate
// form; the hash is always taken over its JCS canonicalization.
func marshalOf(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return Of(data)
}
