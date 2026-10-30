// Package productionlogistics is the production and logistics DomainPack
// (H-045, docs/domains/PRODUCTION_LOGISTICS.md): a small catalog of steps
// and resource roles shared by traditional factories and warehouses, and
// four synthetic templates (a sneaker factory, a bread bakery, a corrugated
// box plant and a distribution center) whose magnitudes follow cited public
// sources (H-046). Everything is expressed with the generic IR primitives
// (finite buffers, batches, shifts, breakdowns, setups, yields and
// rework); the pack adds no rule and no metric of its own, and the
// simulation kernel never imports this package.
package productionlogistics

import (
	"context"
	"fmt"
	"slices"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// ID is the IR domain value this pack serves.
const ID = "production_logistics"

// Name is the domain as people call it; the UI's catalogs have it as
// domain.production_logistics.name in both languages (a test keeps them
// equal).
const Name = "Production and logistics"

// DomainMismatch is the issue code for an IR of another domain (REQ-003:
// fail closed); every pack has it.
const DomainMismatch = "DOMAIN_MISMATCH"

// Pack is stateless; the zero value is ready to use.
type Pack struct{}

var (
	_ domainpack.DomainPack       = Pack{}
	_ domainpack.TemplateProvider = Pack{}
)

func (Pack) ID() string { return ID }

// nodes is PRODUCTION_LOGISTICS §2; a test keeps it equal to the doc, and
// the UI's catalogs (apps/web/messages) have each label in both languages.
var nodes = []domainpack.NodeDefinition{
	{Kind: "order_release", Type: model.NodeSource, Label: "Order release", Description: "Orders, lots or inbound trucks entering: an interarrival distribution or a release table."},
	{Kind: "buffer", Type: model.NodeQueue, Label: "Buffer", Description: "WIP, a staging area or storage locations before one step; a capacity makes it finite, so a full buffer blocks the step before it."},
	{Kind: "work_step", Type: model.NodeProcess, Label: "Work step", Description: "A manual or machine step on its resource, such as cutting, stitching, makeup, folding and gluing, picking or packing.", ResourceTypes: []string{"human", "machine"}},
	{Kind: "batch_step", Type: model.NodeProcess, Label: "Batch step", Description: "A step that serves several items at once, such as an autoclave, a mixer, a pallet trip or a trailer load (min, max and the longest wait to fill a batch).", ResourceTypes: []string{"machine"}},
	{Kind: "inspection_step", Type: model.NodeProcess, Label: "Inspection", Description: "A quality check or inspection, followed by a quality result.", ResourceTypes: []string{"human", "machine"}},
	{Kind: "repair_step", Type: model.NodeProcess, Label: "Repair or correction", Description: "Rework by people before the item goes back to inspection or onward.", ResourceTypes: []string{"human"}},
	{Kind: "quality_result", Type: model.NodeDecision, Label: "Quality result", Description: "Pass, repair or scrap after an inspection; the probabilities are model inputs (yields), not predictions."},
	{Kind: "defect_observation", Type: model.NodeFailure, Label: "Defect", Description: "Counts a defect, a reject or a pick error (a generic failure observation, not a machine breakdown)."},
	{Kind: "rework_observation", Type: model.NodeRework, Label: "Reworked", Description: "Counts an item repaired or corrected before it goes on."},
	{Kind: "wait_step", Type: model.NodeDelay, Label: "Wait (no resource)", Description: "Time that holds no resource, such as floor time, proofing, cooling or transport."},
	{Kind: "completed", Type: model.NodeSink, Label: "Completed", Description: "Items leaving packed, palletized or shipped (a completed sink): the output."},
	{Kind: "scrap", Type: model.NodeSink, Label: "Scrap", Description: "Items scrapped or wasted (an abandoned sink); not output."},
}

// resources is PRODUCTION_LOGISTICS §2; the templates' resources take
// their role from it.
var resources = []domainpack.ResourceDefinition{
	{Kind: "machine", Type: "machine", Label: "Machine", Description: "A production machine, such as an autoclave, a mixer, an oven, a corrugator or a folder-gluer."},
	{Kind: "operator", Type: "human", Label: "Operators", Description: "People who work at a step, such as cutters, stitchers, receivers, pickers or packers."},
	{Kind: "inspector", Type: "human", Label: "Inspectors", Description: "People who check quality or correct errors."},
	{Kind: "inspection_equipment", Type: "machine", Label: "Inspection equipment", Description: "An automatic check, such as a metal detector, a checkweigher or a scanner."},
	{Kind: "material_handling", Type: "machine", Label: "Material handling", Description: "Reach trucks, strappers, palletizers or conveyors that move material."},
	{Kind: "dock", Type: "machine", Label: "Dock doors", Description: "Doors where trucks are loaded or unloaded."},
	{Kind: "generic", Type: "generic", Label: "Other resource", Description: "Any other constrained capacity, with no domain meaning."},
}

// NodeCatalog and ResourceCatalog return copies; callers may not change the pack.
func (Pack) NodeCatalog() []domainpack.NodeDefinition {
	out := make([]domainpack.NodeDefinition, len(nodes))
	for i, n := range nodes {
		n.ResourceTypes = slices.Clone(n.ResourceTypes)
		out[i] = n
	}
	return out
}

func (Pack) ResourceCatalog() []domainpack.ResourceDefinition { return slices.Clone(resources) }

// Validate applies the pack's one rule: the IR must belong to this domain
// (fail closed). Structure, references, routing, distributions and the
// line-flow rules (D-050) are the generic validator's and are not repeated
// here.
func (Pack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir == nil {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "", "no model to validate", model.SeverityError)}
	}
	if ir.Domain != ID {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "/domain",
			fmt.Sprintf("domain %q is not %q", ir.Domain, ID), model.SeverityError)}
	}
	return nil
}

// InferFromEvents is not provided by this pack (T-027 is electronics first).
func (Pack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, fmt.Errorf("%w: event inference for %s is not provided", domainpack.ErrUnavailable, ID)
}

// ComputeMetrics adds nothing: the pack has no domain metrics, so a run
// shows the engine's generic ones (lead time, throughput, utilization,
// queue waits, failure and rework counts) and metrics.dora is the engine's.
func (Pack) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary { return nil }

// PromptContext is static domain knowledge for the modeler and analyst prompts.
func (p Pack) PromptContext() domainpack.PromptContext {
	return domainpack.PromptContext{
		Domain:    ID,
		Summary:   Name + ": items such as pairs of sneakers, lots of 40 loaves, loads of 1,000 boxes or warehouse cases flow through work steps, batch steps (autoclaves, mixers, pallets, trailer loads), inspections with repair or scrap, and buffers; steps compete for machines and people on shift calendars, with finite buffers, breakdowns and changeovers between orders or product families.",
		Nodes:     p.NodeCatalog(),
		Resources: p.ResourceCatalog(),
		Rules: []string{
			"The time unit is the minute; t=0 is Monday 00:00 model time, and calendars have no time zone (D-052).",
			"Routing is by probability, never by product: product families (entity types) differ only in the changeovers (setups) of the resources they share.",
			"Yields, repair and scrap rates are routing probabilities: model inputs, not simulated predictions.",
			"A batch step serves min_size to max_size items at once and waits at most max_wait to fill once min_size are waiting; a resource with setups cannot serve a batch step (D-054, D-055).",
			"Breakdowns are finish-first and their clock counts on-shift time only (D-051); shift ends and changeovers are finish-first too (D-052, D-055).",
			"Buffers before repair or correction are unlimited, so full buffers cannot block in a loop (D-050).",
			"Shelf life, temperature, recipes and pick paths are not modelled: they are approximated with setups, scheduled downtime (calendar gaps), shifts, batches, yields and rework.",
			"Template parameters are synthetic, for demonstration: realistic magnitudes from public sources, scaled, not measurements of any plant or warehouse.",
		},
	}
}

// DefaultViews keeps existing positions and lays out the rest left to
// right by flow distance. The templates carry their own positions: the
// main line on the top row, inspection loops and scrap below it.
func (Pack) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	return domainpack.LayeredLayout(ir)
}
