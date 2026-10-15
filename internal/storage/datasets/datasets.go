// Package datasets is the dataset binding stage (docs/TWIN_IR_SPEC.md §4–§5):
// it turns a frozen manifest into hash-verified samples before a version is
// published or a run reaches the engine.
//
// Every binding names an immutable dataset version and the SHA-256 of its
// stored bytes (D-019). Resolve fetches those bytes through Store, checks the
// hash before parsing anything, then checks tenant, identity, unit, sample
// count and the D-029 duration cap, and finally reruns the zero-time rules
// with the empirical minima (D-033). Late data can only arrive as a new
// dataset version, so a resolved manifest never changes.
//
// Issue paths point into a ModelVersion-shaped document:
// /ir/... for the model and /dataset_bindings/<i>/... for the manifest in the
// order the caller passed it.
package datasets

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/simulation"
)

// MaxSnapshotBytes bounds one stored snapshot: 100,000 samples (the schema
// maximum) fit comfortably.
const MaxSnapshotBytes = 4 << 20

// ErrNotFound is what a Store returns for a dataset version that does not
// exist or is not visible to the organization; the two are deliberately
// indistinguishable.
var ErrNotFound = errors.New("datasets: dataset version not found")

// Store reads immutable stored snapshot objects. Implementations scope every
// read to organizationID (application filter + RLS, AGENTS.md §2) and return
// ErrNotFound rather than another tenant's object. Any other error is
// infrastructure trouble; Resolve passes it on instead of reporting an issue.
type Store interface {
	Snapshot(ctx context.Context, organizationID, datasetVersionID string) ([]byte, error)
}

// Snapshot is the stored object (schemas/dataset-snapshot.schema.json).
// Samples are in Unit; the engine quantizes them with model.Ticks.
type Snapshot struct {
	DatasetVersionID string         `json:"dataset_version_id"`
	OrganizationID   string         `json:"organization_id"`
	LogicalRef       string         `json:"logical_ref"`
	Unit             model.TimeUnit `json:"unit"`
	Samples          []float64      `json:"samples"`
}

// Resolution is the outcome of Resolve. Manifest, ManifestHash and Datasets
// are set only when Valid.
type Resolution struct {
	Manifest     []model.DatasetBinding // byte order of logical_ref (D-034)
	ManifestHash string                 // dataset_manifest_hash
	Datasets     map[string]simulation.ResolvedDataset
	Issues       []model.ValidationIssue
}

// Valid reports whether the manifest resolved without errors.
func (r Resolution) Valid() bool {
	return !slices.ContainsFunc(r.Issues, func(i model.ValidationIssue) bool { return i.Severity == model.SeverityError })
}

// Resolve checks manifest against ir and the stored snapshots of
// organizationID. ir must have passed validate.IR. Problems with the data are
// issues; only infrastructure failures and cancellation are errors.
//
// Stages stop at the first one with errors (D-023): manifest shape and
// coverage, then the stored snapshots, then the D-033 graph rules.
func Resolve(ctx context.Context, store Store, organizationID string, ir *model.ModelIR, manifest []model.DatasetBinding) (Resolution, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if issues, err := shape(ir, manifest); err != nil || len(issues) > 0 {
		return Resolution{Issues: validate.Sort(issues)}, err
	}
	var issues []model.ValidationIssue
	zeroRefs := map[string]bool{}
	datasets := make(map[string]simulation.ResolvedDataset, len(manifest))
	for i, b := range manifest {
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
		snap, found, err := load(ctx, store, organizationID, i, b)
		if err != nil {
			return Resolution{}, err
		}
		issues = append(issues, found...)
		if snap == nil {
			continue
		}
		if snap.zero {
			zeroRefs[b.LogicalRef] = true
		}
		datasets[b.LogicalRef] = simulation.ResolvedDataset{Binding: b, Samples: snap.Samples}
	}
	if len(issues) > 0 {
		return Resolution{Issues: validate.Sort(issues)}, nil
	}
	for _, i := range validate.Bindings(ir, zeroRefs) {
		i.Path = "/ir" + i.Path
		issues = append(issues, i)
	}
	if len(issues) > 0 {
		return Resolution{Issues: validate.Sort(issues)}, nil
	}
	sorted, err := hashing.SortManifest(manifest)
	if err != nil {
		return Resolution{}, fmt.Errorf("shape-checked manifest does not sort: %w", err)
	}
	digest, err := hashing.Manifest(sorted)
	if err != nil {
		return Resolution{}, fmt.Errorf("hashing a shape-checked manifest: %w", err)
	}
	return Resolution{Manifest: sorted, ManifestHash: digest, Datasets: datasets}, nil
}

func issue(code, path, message string) model.ValidationIssue {
	return model.ValidationIssue{Code: code, Path: path, Message: message, Severity: model.SeverityError}
}

func bindingPath(i int, member string) string {
	if member == "" {
		return validate.Pointer("dataset_bindings", strconv.Itoa(i))
	}
	return validate.Pointer("dataset_bindings", strconv.Itoa(i), member)
}

// shape checks each binding against the schema (which enforces the D-034
// logical_ref charset), rejects a logical_ref bound twice, and requires a
// binding for every empirical distribution in ir. A binding the model does
// not use is allowed: a run copies its version's manifest even when the
// scenario patch replaced an empirical distribution.
func shape(ir *model.ModelIR, manifest []model.DatasetBinding) ([]model.ValidationIssue, error) {
	var out []model.ValidationIssue
	bound := map[string]bool{}
	for i, b := range manifest {
		data, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("encoding binding %d: %w", i, err)
		}
		found, err := validate.Document(data, "common.schema.json#/$defs/DatasetBinding", validate.DatasetBindingInvalid)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			f.Path = bindingPath(i, "") + f.Path
			out = append(out, f)
		}
		if bound[b.LogicalRef] {
			out = append(out, issue(validate.DatasetBindingInvalid, bindingPath(i, "logical_ref"), b.LogicalRef+" is bound twice"))
		}
		bound[b.LogicalRef] = true
	}
	for i := range ir.Nodes {
		for _, d := range ir.Nodes[i].Distributions() {
			if d.Distribution.Type == model.DistEmpirical && !bound[d.Distribution.SamplesRef] {
				out = append(out, issue(validate.DatasetBindingInvalid,
					validate.Pointer("ir", "nodes", strconv.Itoa(i), d.Member, "distribution", "samples_ref"),
					d.Distribution.SamplesRef+" has no dataset binding"))
			}
		}
	}
	return out, nil
}

// loaded is a verified snapshot; zero reports a sample of 0 ticks (D-033).
type loaded struct {
	Snapshot
	zero bool
}

// load fetches and verifies the snapshot of binding i. A nil snapshot with
// issues means the binding is unusable; a non-nil error is infrastructure.
func load(ctx context.Context, store Store, org string, i int, b model.DatasetBinding) (*loaded, []model.ValidationIssue, error) {
	bad := func(member, message string) (*loaded, []model.ValidationIssue, error) {
		return nil, []model.ValidationIssue{issue(validate.DatasetBindingInvalid, bindingPath(i, member), message)}, nil
	}
	raw, err := store.Snapshot(ctx, org, b.DatasetVersionID)
	switch {
	case errors.Is(err, ErrNotFound):
		return bad("dataset_version_id", "no immutable snapshot of this dataset version is visible to the organization")
	case err != nil:
		return nil, nil, fmt.Errorf("reading dataset version %s: %w", b.DatasetVersionID, err)
	case len(raw) > MaxSnapshotBytes:
		return bad("", fmt.Sprintf("stored snapshot is %d bytes; the limit is %d", len(raw), MaxSnapshotBytes))
	}
	// Hash first: nothing from an object that is not the frozen one is parsed.
	if got := hashing.ContentHash(raw); got != b.ContentHash {
		return bad("content_hash", "stored bytes hash to "+got)
	}
	found, err := validate.Document(raw, "dataset-snapshot.schema.json", validate.DatasetBindingInvalid)
	if err != nil {
		return nil, nil, err
	}
	if len(found) > 0 {
		return bad("", fmt.Sprintf("stored object is not a valid dataset snapshot (%s at %q)", found[0].Code, found[0].Path))
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap, json.RejectUnknownMembers(true)); err != nil {
		return nil, nil, fmt.Errorf("schema-valid snapshot does not decode: %w", err)
	}
	switch {
	case snap.OrganizationID != org:
		// A Store must never return this; answer exactly as for a missing
		// version so that no other tenant's data is confirmed to exist.
		return bad("dataset_version_id", "no immutable snapshot of this dataset version is visible to the organization")
	case snap.DatasetVersionID != b.DatasetVersionID:
		return bad("dataset_version_id", "stored snapshot belongs to dataset version "+snap.DatasetVersionID)
	case snap.LogicalRef != b.LogicalRef:
		return bad("logical_ref", "stored snapshot is "+snap.LogicalRef)
	case snap.Unit != b.Unit:
		return bad("unit", fmt.Sprintf("stored samples are in %s", snap.Unit))
	case len(snap.Samples) != b.SampleCount:
		return bad("sample_count", fmt.Sprintf("stored snapshot has %d samples", len(snap.Samples)))
	}
	out := &loaded{Snapshot: snap}
	for k, s := range snap.Samples {
		t, err := model.Ticks(s, snap.Unit)
		if err != nil {
			return nil, []model.ValidationIssue{issue(validate.LimitExceeded, bindingPath(i, ""),
				fmt.Sprintf("sample %d: %v", k, err))}, nil
		}
		out.zero = out.zero || t == 0
	}
	return out, nil, nil
}
