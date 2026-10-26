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
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// DomainPack is one domain. Implementations must be safe for concurrent use,
// deterministic and stateless: the same IR gives the same issues, views and
// metrics, the same dataset gives the same inference result, and no call
// depends on an earlier one (a pack never carries one tenant's model into
// another's answer). IDs a proposal needs come from the server's allocation
// pool (D-016), so a pack never mints them.
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

// Locale is a language of the UI and of template default names
// (common.schema.json#/$defs/Locale, REQ-022). IDs, enums and paths never
// depend on it.
type Locale string

// The locales, the default first.
const (
	English            Locale = "en"
	TraditionalChinese Locale = "zh-Hant"
)

// Locales lists every Locale, the default first.
var Locales = []Locale{English, TraditionalChinese}

// TemplateProvider is implemented by a pack that offers create-model
// templates (http.schema.json CreateModel.template). A template is the same
// model in every Locale: only its default names differ, never an ID or the
// topology, and once created the names are user data (REQ-022). The
// conformance suite checks each template in each locale. It is a separate
// interface so DomainPack keeps the report's method set.
type TemplateProvider interface {
	Templates() []string
	// Template returns templateName as a valid IR for a new model with the
	// given ID and name, its default names in locale. An unknown template
	// or locale is an error.
	Template(templateName, modelID, name string, locale Locale) (*model.ModelIR, error)
}

// ModelMetricsProvider is implemented by a pack whose metrics need the model
// as well as the engine's Result (D-058, ELECTRONICS_LINE §5): which
// processes are stations, their resources' roles, calendars and routing
// probabilities are in the IR, not in the Result. The engine calls it with
// the effective IR it ran (the version with the scenario patch applied) and
// the generic Result before the digest, and stores the answer in
// metrics.domain (absent when empty, so results of packs without it keep
// their digest, D-047). Like ComputeMetrics it reads both and changes
// neither; a metric without the data it needs is unavailable with a
// reason. It is a separate interface so DomainPack keeps the report's
// method set.
type ModelMetricsProvider interface {
	ComputeModelMetrics(ir *model.ModelIR, result *simulation.Result) map[string]simulation.MetricSummary
}

// PhotoVocabulary is implemented by a pack whose lines can be read from a
// photo or floor plan (T-056, H-033): the short text prompts a segmenter
// looks for, the pack's machines, conveyors and workstations in its own
// words, in a fixed order (a box names its prompt by index). Static pack
// data, never a tenant's; a pack without it takes no photo proposals. It
// is a separate interface so DomainPack keeps the report's method set.
type PhotoVocabulary interface {
	PhotoPrompts() []string
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
// Decoding into this type is not validation: empty optional strings and nulls
// vanish and missing required members become "", so import and inference
// (T-016, T-027) must validate the raw bytes against the schema first.
type CanonicalEvent struct {
	CaseID       string         `json:"case_id"`
	EventType    string         `json:"event_type"`
	Timestamp    string         `json:"timestamp"`              // RFC 3339
	EndTimestamp string         `json:"end_timestamp,omitzero"` // RFC 3339; >= Timestamp (D-059, a data-quality rule)
	Stage        string         `json:"stage,omitzero"`
	DurationMS   *int64         `json:"duration_ms,omitzero"`
	ResourceID   string         `json:"resource_id,omitzero"`
	Outcome      string         `json:"outcome,omitzero"`
	Shift        string         `json:"shift,omitzero"`   // shift label (D-059)
	Product      string         `json:"product,omitzero"` // entity type; required for multi-product logs (D-059, D-055)
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

// NewRegistry registers packs. A nil pack, a duplicate or empty ID, or a
// pack whose type is not declared in its own direct subpackage of
// internal/domainpack is an error. One package holds one pack: the
// architecture rules keep those packages apart, which two packs of one
// package, a pack type declared in a pack's subpackage (such as a generic
// instantiated elsewhere) or one outside internal/domainpack would escape
// (T-042 third review L2).
func NewRegistry(packs ...DomainPack) (*Registry, error) { return newRegistry(true, packs...) }

func newRegistry(checkPackage bool, packs ...DomainPack) (*Registry, error) {
	r := &Registry{packs: map[string]DomainPack{}}
	byPackage := map[string]string{}
	for _, p := range packs {
		if p == nil {
			return nil, errors.New("domainpack: a pack is nil")
		}
		id := p.ID()
		if id == "" {
			return nil, errors.New("domainpack: a pack has an empty ID")
		}
		if _, dup := r.packs[id]; dup {
			return nil, fmt.Errorf("domainpack: %q registered twice", id)
		}
		if checkPackage {
			t := reflect.TypeOf(p)
			for t.Kind() == reflect.Pointer {
				t = t.Elem()
			}
			if err := checkPackPackage(t.PkgPath()); err != nil {
				return nil, fmt.Errorf("domainpack: %q: %w", id, err)
			}
			if other, dup := byPackage[t.PkgPath()]; dup {
				return nil, fmt.Errorf("domainpack: %q and %q are declared in one package, %s; one package holds one pack", other, id, t.PkgPath())
			}
			byPackage[t.PkgPath()] = id
		}
		r.packs[id] = p
	}
	return r, nil
}

// checkPackPackage accepts the import path of a direct subpackage of this
// package.
func checkPackPackage(pkg string) error {
	name, ok := strings.CutPrefix(pkg, reflect.TypeFor[Registry]().PkgPath()+"/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("the pack type is declared in %q, not in its own direct subpackage of internal/domainpack", pkg)
	}
	return nil
}

// Lookup returns the pack for a domain, or ErrUnsupportedDomain. A nil
// registry has no packs.
func (r *Registry) Lookup(domain string) (DomainPack, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDomain, domain)
	}
	if p, ok := r.packs[domain]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedDomain, domain)
}

// Domains lists the registered domain IDs in sorted order.
func (r *Registry) Domains() []string {
	if r == nil {
		return []string{}
	}
	ids := make([]string, 0, len(r.packs))
	for id := range r.packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return slices.Clip(ids)
}
