// Package electronicsline is the P0 electronics assembly and test line
// DomainPack (H-016, ADR-025, docs/domains/ELECTRONICS_LINE.md; T-051): the
// catalog of stations and resource roles (§3), the pack's rules (§3.4),
// the synthetic full template (§4) and the line metrics (§5, D-058).
// Everything is expressed with the generic IR primitives; the pack reads
// the station, pass and result_of domain attributes (§3.3), which the
// generic kernel never does, and the simulation kernel never imports this
// package.
package electronicsline

import (
	"context"
	"fmt"
	"slices"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// ID is the IR domain value this pack serves.
const ID = "electronics_line"

// Name is the domain as people call it; the UI's catalogs have it as
// domain.electronics_line.name in both languages (a test keeps them equal).
const Name = "Electronics assembly and test line"

// DomainMismatch is the issue code for an IR of another domain (REQ-003:
// fail closed); every pack has it.
const DomainMismatch = "DOMAIN_MISMATCH"

// Pack is stateless; the zero value is ready to use.
type Pack struct{}

var (
	_ domainpack.DomainPack           = Pack{}
	_ domainpack.TemplateProvider     = Pack{}
	_ domainpack.ModelMetricsProvider = Pack{}
)

func (Pack) ID() string { return ID }

// nodes is ELECTRONICS_LINE §3.1; a test keeps it equal to the spec, and
// the UI's catalogs (apps/web/messages) have each label in both languages.
var nodes = []domainpack.NodeDefinition{
	{Kind: "board_release", Type: model.NodeSource, Label: "Board release", Description: "Boards entering the line: an interarrival distribution or a release schedule, such as whole lots per product family (D-053)."},
	{Kind: "line_buffer", Type: model.NodeQueue, Label: "Line buffer", Description: "A WIP rack or conveyor before one station; a capacity makes it finite, so a full buffer blocks the station before it (D-049)."},
	{Kind: "smt_station", Type: model.NodeProcess, Label: "SMT line", Description: "Solder paste printing, SPI, placement and reflow as one server on the smt_line resource; the balance inside the line is not modelled.", ResourceTypes: []string{"machine"}},
	{Kind: "aoi_station", Type: model.NodeProcess, Label: "AOI station", Description: "Automated optical inspection on the aoi machine, followed by a test result decision.", ResourceTypes: []string{"machine"}},
	{Kind: "ict_station", Type: model.NodeProcess, Label: "ICT station", Description: "In-circuit test, first pass, on the ict_tester resource, followed by a test result decision.", ResourceTypes: []string{"machine"}},
	{Kind: "fct_station", Type: model.NodeProcess, Label: "FCT station", Description: "Functional test, first pass, on the fct_tester resource, followed by a test result decision.", ResourceTypes: []string{"machine"}},
	{Kind: "pack_station", Type: model.NodeProcess, Label: "Pack and ship", Description: "Packing and shipping on the pack_station resource, before the shipped sink.", ResourceTypes: []string{"human"}},
	{Kind: "retest_station", Type: model.NodeProcess, Label: "Retest", Description: "A retest after debug, sharing the first test's tester (pass retest).", ResourceTypes: []string{"machine"}},
	{Kind: "debug_station", Type: model.NodeProcess, Label: "Debug and repair", Description: "Diagnosis, touch-up or repair by debug_operator people, followed by a debug result.", ResourceTypes: []string{"human"}},
	{Kind: "burn_in_station", Type: model.NodeProcess, Label: "Burn-in (batch)", Description: "Burn-in in chambers as a batch process (min, max and the longest wait to fill a batch, D-054); one batch takes one chamber.", ResourceTypes: []string{"machine"}},
	{Kind: "test_result", Type: model.NodeDecision, Label: "Test result", Description: "Pass or fail of the test it follows (result_of): exactly one flow goes to a failure node, the fail branch."},
	{Kind: "debug_result", Type: model.NodeDecision, Label: "Debug result", Description: "Repaired or scrapped after debug."},
	{Kind: "defect_observation", Type: model.NodeFailure, Label: "Defect", Description: "Counts a failed test (a generic failure observation, not a machine breakdown)."},
	{Kind: "rework_observation", Type: model.NodeRework, Label: "Repaired", Description: "Counts a repaired board before it goes back to a test."},
	{Kind: "transport", Type: model.NodeDelay, Label: "Transport or wait", Description: "Handling or waiting that takes time but no resource."},
	{Kind: "shipped", Type: model.NodeSink, Label: "Shipped", Description: "Boards leaving the line shipped (a completed sink): the line's output."},
	{Kind: "scrap", Type: model.NodeSink, Label: "Scrap", Description: "Boards scrapped (an abandoned sink); not output."},
}

// resources is ELECTRONICS_LINE §3.2.
var resources = []domainpack.ResourceDefinition{
	{Kind: "smt_line", Type: "machine", Label: "SMT line", Description: "One SMT line, as one server."},
	{Kind: "aoi", Type: "machine", Label: "AOI machine", Description: "An automated optical inspection machine."},
	{Kind: "ict_tester", Type: "machine", Label: "ICT tester", Description: "An in-circuit tester; first tests and retests share it."},
	{Kind: "fct_tester", Type: "machine", Label: "FCT tester or fixture", Description: "A functional tester or test fixture; first tests and retests share it."},
	{Kind: "burn_in_chamber", Type: "machine", Label: "Burn-in chamber", Description: "A burn-in chamber; one batch takes one unit."},
	{Kind: "debug_operator", Type: "human", Label: "Debug and repair operator", Description: "People who touch up, debug and repair boards."},
	{Kind: "pack_station", Type: "human", Label: "Packing station", Description: "A packing and shipping place with its people."},
	{Kind: "test_slot", Type: "machine", Label: "Rack test slot", Description: "A test slot of the rack integration variant (§4.3)."},
	{Kind: "assembler", Type: "human", Label: "Rack assembler", Description: "People who assemble racks (§4.3)."},
	{Kind: "debug_engineer", Type: "human", Label: "Debug engineer", Description: "Engineers who debug racks (§4.3)."},
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

// Validate applies the pack's rules: the IR must belong to this domain
// (fail closed), then ELECTRONICS_LINE §3.4 (rules.go). Structure,
// references, routing, distributions and the line-flow rules (D-050) are
// the generic validator's and are not repeated here.
func (Pack) Validate(ir *model.ModelIR) []model.ValidationIssue {
	if ir == nil {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "", "no model to validate", model.SeverityError)}
	}
	if ir.Domain != ID {
		return []model.ValidationIssue{model.NewIssue(DomainMismatch, "/domain",
			fmt.Sprintf("domain %q is not %q", ir.Domain, ID), model.SeverityError)}
	}
	return rules(ir)
}

// InferFromEvents is not provided by this pack yet (T-027).
func (Pack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, fmt.Errorf("%w: event inference for %s arrives with T-027", domainpack.ErrUnavailable, ID)
}

// ComputeMetrics has nothing to add from the Result alone: the line's
// metrics need the model (ComputeModelMetrics), and the pack has no DORA
// metrics, so metrics.dora is the engine's.
func (Pack) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary { return nil }

// PromptContext is static domain knowledge for the modeler and analyst prompts.
func (p Pack) PromptContext() domainpack.PromptContext {
	return domainpack.PromptContext{
		Domain:    ID,
		Summary:   Name + ": boards flow through SMT, AOI, ICT, FCT, burn-in and packing, with test failures, debug, retest and scrap; stations compete for machines and people on shift calendars, with finite buffers, breakdowns, batches and changeovers.",
		Nodes:     p.NodeCatalog(),
		Resources: p.ResourceCatalog(),
		Rules: []string{
			"The time unit is the minute; t=0 is Monday 00:00 model time, and calendars have no time zone (D-052).",
			"A main-line station step is a process with domain_attributes station (smt, aoi, ict, fct, burn_in or pack) and pass (first or retest); it needs exactly one resource whose role is its station's (smt_line, aoi, ict_tester, fct_tester, burn_in_chamber or pack_station), and may add operators.",
			"A retest (pass retest) uses the same tester as its station's first test; debug goes back to the station it came from.",
			"A test result is a decision with domain_attributes result_of (the tested step) and exactly one flow to a failure node, the fail branch.",
			"First-pass yields are model inputs (the routing probabilities), not simulated predictions; utilization is not OEE, and no metric is called OEE.",
			"Breakdowns are finish-first and their clock counts on-shift time only (D-051); shift ends and changeovers are finish-first too (D-052, D-055).",
			"Buffers before debug and retest are unlimited, so full buffers cannot block in a loop (D-050).",
			"The simulated first-test pass ratio, retest ratio, scrap rate and line WIP are unavailable in this build: they need the declared metrics of D-058.",
			"Template parameters are synthetic, for demonstration: not measurements of any plant.",
		},
	}
}

// DefaultViews keeps existing positions and lays out the rest left to
// right by flow distance. The template carries its own positions: the
// main line on the top row, each station's debug and retest loop below
// it, so the process steps in order of x, then y, are the Marey strip's
// rows (SMT, AOI, touch-up, ICT, its debug and retest, FCT, its debug and
// retest, burn-in, packing).
func (Pack) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	return domainpack.LayeredLayout(ir)
}
