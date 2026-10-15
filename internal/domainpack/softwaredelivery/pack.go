// Package softwaredelivery is the P0 SoftwareDeliveryDomainPack (T-011): the
// change-delivery catalog (review, CI, test, deploy), its domain rule, the
// default template and DORA gating. Everything here is expressed with the
// generic IR primitives; the simulation kernel never imports this package.
package softwaredelivery

import (
	"context"
	"fmt"

	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/simulation"
)

// ID is the IR domain value this pack serves.
const ID = "software_delivery"

// DomainMismatch is the issue code for an IR of another domain (REQ-003: fail closed).
const DomainMismatch = "DOMAIN_MISMATCH"

// Pack is stateless; the zero value is ready to use.
type Pack struct{}

var _ domainpack.DomainPack = Pack{}

func (Pack) ID() string { return ID }

var nodes = []domainpack.NodeDefinition{
	{Kind: "change_source", Type: model.NodeSource, Label: "Change source", Description: "Changes (e.g. pull requests) entering the pipeline at a modelled interarrival time."},
	{Kind: "backlog_queue", Type: model.NodeQueue, Label: "Queue", Description: "An explicit waiting place in front of one process."},
	{Kind: "code_review", Type: model.NodeProcess, Label: "Code review", Description: "Review by people; requires human reviewers.", ResourceTypes: []string{"human"}},
	{Kind: "ci_pipeline", Type: model.NodeProcess, Label: "CI pipeline", Description: "Build and automated checks on CI runners.", ResourceTypes: []string{"machine"}},
	{Kind: "test_stage", Type: model.NodeProcess, Label: "Test", Description: "Testing in a test environment.", ResourceTypes: []string{"machine"}},
	{Kind: "deploy_stage", Type: model.NodeProcess, Label: "Deploy", Description: "Deployment through a limited number of deployment slots.", ResourceTypes: []string{"machine"}},
	{Kind: "wait_delay", Type: model.NodeDelay, Label: "Wait", Description: "Elapsed time without a resource, such as an approval window."},
	{Kind: "result_decision", Type: model.NodeDecision, Label: "Decision", Description: "Probabilistic routing such as pass/fail."},
	{Kind: "failure_observation", Type: model.NodeFailure, Label: "Failure", Description: "Counts a generic failure (e.g. a failed CI run). It is not a deployment failure for DORA."},
	{Kind: "rework_observation", Type: model.NodeRework, Label: "Rework", Description: "Counts rework before the change re-enters an earlier stage."},
	{Kind: "done_sink", Type: model.NodeSink, Label: "Done", Description: "Changes leaving the pipeline."},
}

// resources is the catalog TWIN_IR_SPEC §2 lists (a test keeps them equal).
var resources = []domainpack.ResourceDefinition{
	{Kind: "developer", Type: "human", Label: "Developers", Description: "People who write and rework changes."},
	{Kind: "reviewer", Type: "human", Label: "Reviewers", Description: "People who review changes."},
	{Kind: "ci_runner", Type: "machine", Label: "CI runners", Description: "Build/test executors."},
	{Kind: "test_environment", Type: "machine", Label: "Test environments", Description: "Environments that run test stages."},
	{Kind: "deployment_slot", Type: "machine", Label: "Deployment slots", Description: "Concurrent deployments the release process allows."},
	{Kind: "generic_resource", Type: "generic", Label: "Other resource", Description: "Any other constrained capacity, with no domain meaning."},
}

// NodeCatalog and ResourceCatalog return copies; callers may not change the pack.
func (Pack) NodeCatalog() []domainpack.NodeDefinition {
	out := make([]domainpack.NodeDefinition, len(nodes))
	for i, n := range nodes {
		n.ResourceTypes = append([]string(nil), n.ResourceTypes...)
		out[i] = n
	}
	return out
}

func (Pack) ResourceCatalog() []domainpack.ResourceDefinition {
	return append([]domainpack.ResourceDefinition(nil), resources...)
}

// Validate applies the pack's domain rule: the IR must belong to this domain.
// Structure, references, routing and distributions are the generic
// validator's job and are not repeated here.
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

// InferFromEvents is not provided by this pack yet (T-027).
func (Pack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, fmt.Errorf("%w: event inference for %s arrives with T-027", domainpack.ErrUnavailable, ID)
}

// ReasonMissingDORAMapping is the reason every DORA metric carries in P0, as
// in testdata/contracts/valid-result.json. Metric keys, units and reason
// codes follow that fixture until CB-23 settles them.
const ReasonMissingDORAMapping = "MISSING_DORA_EVENT_MAPPING"

// dora lists the DORA-oriented metrics and what each would need
// (docs/SIMULATION_SPEC.md §6): change lead time needs a commit-to-production
// deploy relation; deployment frequency identifiable production deployments
// and an observation period; change failure percentage a deployment
// denominator and failure classification; failed deployment recovery time
// paired failure and recovery events; deployment rework rate identifiable
// rework deployments. None is available without an approved mapping (D-010).
var dora = []struct {
	key, unit  string
	population simulation.Population
}{
	{"change_lead_time", "minute", simulation.PopulationEntity},
	{"deployment_frequency", "deployment_per_day", simulation.PopulationReplication},
	{"change_failure_percentage", "percent", simulation.PopulationReplication},
	{"failed_deployment_recovery_time", "minute", simulation.PopulationEntity},
	{"deployment_rework_rate", "percent", simulation.PopulationReplication},
}

// ComputeMetrics reports every DORA metric as unavailable: P0 has no approved
// deployment/failure/recovery mapping (D-010), and the generic failure and
// rework counts (e.g. a failed CI run) are not deployment failures, so the
// result is the same whatever the engine observed.
func (Pack) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	out := make(map[string]simulation.MetricSummary, len(dora))
	for _, m := range dora {
		reason := ReasonMissingDORAMapping
		out[m.key] = simulation.MetricSummary{Status: simulation.Unavailable, Population: m.population, Unit: m.unit, Reason: &reason}
	}
	return out
}

// PromptContext is static domain knowledge for the modeler and analyst prompts.
func (p Pack) PromptContext() domainpack.PromptContext {
	return domainpack.PromptContext{
		Domain:    ID,
		Summary:   "Software delivery: changes flow through review, CI, test and deploy stages that compete for people and machines.",
		Nodes:     p.NodeCatalog(),
		Resources: p.ResourceCatalog(),
		Rules: []string{
			"A day is 24 continuous hours; there is no working-hours calendar (D-017).",
			"Failure and rework nodes count generic observations; a failed CI run is not a production deployment failure.",
			"DORA metrics are unavailable unless deployment, failure and recovery events are explicitly mapped (D-010).",
			"Template parameters are synthetic assumptions, not measurements of any team.",
		},
	}
}

// DefaultViews keeps existing positions and lays out the rest left to right.
func (Pack) DefaultViews(ir *model.ModelIR) domainpack.ViewDefinitions {
	return domainpack.LayeredLayout(ir)
}
