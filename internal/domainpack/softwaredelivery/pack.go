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

var resources = []domainpack.ResourceDefinition{
	{Kind: "reviewer", Type: "human", Label: "Reviewers", Description: "People who review changes."},
	{Kind: "ci_runner", Type: "machine", Label: "CI runners", Description: "Build/test executors."},
	{Kind: "test_environment", Type: "machine", Label: "Test environments", Description: "Environments that run test stages."},
	{Kind: "deployment_slot", Type: "machine", Label: "Deployment slots", Description: "Concurrent deployments the release process allows."},
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
	if ir.Domain != ID {
		return []model.ValidationIssue{{Code: DomainMismatch, Path: "/domain", Severity: model.SeverityError,
			Message: fmt.Sprintf("domain %q is not %q", ir.Domain, ID)}}
	}
	return nil
}

// InferFromEvents is not provided by this pack yet (T-027).
func (Pack) InferFromEvents(context.Context, domainpack.CanonicalEventDataset) (*domainpack.InferenceResult, error) {
	return nil, fmt.Errorf("%w: event inference for %s arrives with T-027", domainpack.ErrUnavailable, ID)
}

// dora lists the DORA-oriented metrics and what each would need
// (docs/SIMULATION_SPEC.md §6).
var dora = []struct {
	key, unit, needs string
	population       simulation.Population
}{
	{"change_lead_time", "minutes", "a commit-to-production-deploy relation and its times", simulation.PopulationEntity},
	{"deployment_frequency", "deployments_per_day", "identifiable production deployments and an observation period", simulation.PopulationReplication},
	{"change_failure_percentage", "percent", "a production deployment denominator and failure classification", simulation.PopulationReplication},
	{"failed_deployment_recovery_time", "minutes", "paired production failure and recovery events", simulation.PopulationEntity},
	{"deployment_rework_rate", "percent", "identifiable rework deployments and a deployment denominator", simulation.PopulationReplication},
}

// ComputeMetrics reports every DORA metric as unavailable: P0 has no approved
// deployment/failure/recovery mapping (D-010), and the generic failure and
// rework counts (e.g. a failed CI run) are not deployment failures.
func (Pack) ComputeMetrics(*simulation.Result) map[string]simulation.MetricSummary {
	out := make(map[string]simulation.MetricSummary, len(dora))
	for _, m := range dora {
		reason := "Unavailable: requires " + m.needs + " from an approved event mapping (D-010); generic failure and rework counts are not DORA."
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
