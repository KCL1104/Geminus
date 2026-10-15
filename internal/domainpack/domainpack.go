// Package domainpack is the DomainPack boundary (docs/ARCHITECTURE.md §3):
// everything domain-specific — node and resource catalogs, domain rules,
// default views, domain metrics, AI prompt context and event inference —
// lives behind this interface, so the generic kernel (internal/model,
// internal/simulation) never imports a domain. A pack reads the Generic Twin
// IR and returns issues, views or metrics; it never mutates the IR.
//
// The interface keeps the report's method set. Packs are registered
// explicitly by the process (cmd/), not through init side effects.
package domainpack

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// DomainPack is one domain. Implementations must be safe for concurrent use
// and deterministic: the same IR gives the same issues, views and metrics.
type DomainPack interface {
	ID() string
	NodeCatalog() []NodeDefinition
	ResourceCatalog() []ResourceDefinition
	// Validate returns domain-rule issues only; the generic validator
	// (internal/model/validate) has already checked structure and semantics.
	Validate(ir *model.ModelIR) []model.ValidationIssue
	// InferFromEvents proposes a model from a frozen canonical event dataset.
	// It returns ErrUnavailable until a pack implements inference (T-027).
	InferFromEvents(ctx context.Context, events CanonicalEventDataset) (*InferenceResult, error)
	// ComputeMetrics derives domain metrics (e.g. DORA) from an engine result.
	// A metric without the data it needs is "unavailable" with a reason,
	// never a number borrowed from a generic metric (D-010).
	ComputeMetrics(result *simulation.Result) map[string]simulation.MetricSummary
	PromptContext() PromptContext
	// DefaultViews returns 2D positions for every node; positions already in
	// the IR are kept, missing ones are laid out deterministically.
	DefaultViews(ir *model.ModelIR) ViewDefinitions
}

// NodeDefinition is one palette entry: a domain kind built on a generic node type.
type NodeDefinition struct {
	Kind        string         // stable catalog key, e.g. "code_review"
	Type        model.NodeType // the generic primitive it instantiates
	Label       string
	Description string
	// ResourceTypes a node of this kind normally requires (process kinds).
	ResourceTypes []string
}

// ResourceDefinition is one resource palette entry.
type ResourceDefinition struct {
	Kind        string
	Type        string // human | machine | generic (model-ir.schema.json)
	Label       string
	Description string
}

// PromptContext is the domain knowledge an AI prompt may include. It is
// static pack data: never customer data, credentials or another tenant's model.
type PromptContext struct {
	Domain    string
	Summary   string
	Nodes     []NodeDefinition
	Resources []ResourceDefinition
	Rules     []string
}

// ViewDefinitions is a 2D layout keyed by node ID (views.2d.positions). 3D
// always derives from 2D (layout_mode=derived_from_2d), so packs never supply
// a second topology.
type ViewDefinitions struct {
	Positions map[string]model.Position
}

// CanonicalEvent is one row of a canonical event dataset
// (schemas/canonical-event.schema.json). Metadata is untrusted data.
type CanonicalEvent struct {
	CaseID       string         `json:"case_id"`
	EventType    string         `json:"event_type"`
	Timestamp    string         `json:"timestamp"` // RFC 3339
	Stage        string         `json:"stage,omitzero"`
	DurationMS   *int64         `json:"duration_ms,omitzero"`
	ResourceID   string         `json:"resource_id,omitzero"`
	Outcome      string         `json:"outcome,omitzero"`
	DeploymentID string         `json:"deployment_id,omitzero"`
	IsFailure    *bool          `json:"is_failure,omitzero"`
	IsRework     *bool          `json:"is_rework,omitzero"`
	Metadata     jsontext.Value `json:"metadata,omitzero"` // untrusted; never interpreted by the kernel
}

// CanonicalEventDataset is a frozen dataset version's events.
type CanonicalEventDataset struct {
	DatasetVersionID string
	Events           []CanonicalEvent
}

// InferenceResult is a proposed model with its stated assumptions; it is
// input to the proposal pipeline, never a canonical change (D-009).
type InferenceResult struct {
	Model       *model.ModelIR
	Assumptions []string
	Issues      []model.ValidationIssue
}

var (
	// ErrUnavailable: the pack does not provide this capability (yet).
	ErrUnavailable = errors.New("domainpack: capability unavailable")
	// ErrUnsupportedDomain: no pack is registered for the IR's domain; callers fail closed (REQ-003).
	ErrUnsupportedDomain = errors.New("domainpack: unsupported domain")
)

// Registry maps domain IDs to packs.
type Registry struct {
	packs map[string]DomainPack
}

// NewRegistry registers packs; a duplicate or empty ID is an error.
func NewRegistry(packs ...DomainPack) (*Registry, error) {
	r := &Registry{packs: map[string]DomainPack{}}
	for _, p := range packs {
		id := p.ID()
		if id == "" {
			return nil, errors.New("domainpack: a pack has an empty ID")
		}
		if _, dup := r.packs[id]; dup {
			return nil, fmt.Errorf("domainpack: %q registered twice", id)
		}
		r.packs[id] = p
	}
	return r, nil
}

// Lookup returns the pack for a domain, or ErrUnsupportedDomain.
func (r *Registry) Lookup(domain string) (DomainPack, error) {
	if p, ok := r.packs[domain]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedDomain, domain)
}

// Domains lists the registered domain IDs in sorted order.
func (r *Registry) Domains() []string {
	ids := make([]string, 0, len(r.packs))
	for id := range r.packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return slices.Clip(ids)
}
