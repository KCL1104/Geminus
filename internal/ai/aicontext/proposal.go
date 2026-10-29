package aicontext

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kcl1104/geminus/internal/ai/idpool"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/domainpack"
	"github.com/kcl1104/geminus/internal/model"
)

// DataSummary is a bounded summary of a frozen dataset (for an inference
// proposal, T-027). Summary is untrusted JSON.
type DataSummary struct {
	OrganizationID   string
	DatasetVersionID string
	LogicalRef       string
	Summary          jsontext.Value
}

// Pool is the proposal's D-046 ID allocation pool, as stored with it.
type Pool struct {
	OrganizationID string
	ProposalID     string
	IDs            idpool.Pool
}

// ProposalInput is everything a proposal context is built from. The caller
// (the proposal job, T-025) read Base, Pool and DataSummaries in Scope and
// passes the client's intent, selected IDs and output_locale unchanged.
type ProposalInput struct {
	Scope             Scope
	Mode              provider.Mode
	Base              Version
	Domain            domainpack.PromptContext
	Intent            string
	SelectedEntityIDs []string
	DataSummaries     []DataSummary
	Pool              Pool
	OutputLocale      string
	Budget            provider.Budget
	// Research: the person asked for web research first (H-046 (2)). The
	// research template (prompts/research) follows the role's in the system
	// text, the prompt's template version names both
	// ("<role>-x.y.z+research-x.y.z"), and the prompt is sealed with
	// Research, so the client hands it only to a backend that may search.
	// Without it the prompt is exactly what it was before research existed.
	Research bool
}

func modeRules(mode provider.Mode) string {
	if mode == provider.ModeScenario {
		return "- SCENARIO_PATCH_ALLOWLIST (D-021, D-057): a scenario patch only replaces existing resource capacities, arrival, service and delay distributions " +
			"and routing probabilities, and sets, replaces or removes a queue's buffer capacity (/nodes/<i>/capacity: absent means unlimited), " +
			"after a test of the element's id. A smaller buffer, or a finite one where there was none, is at least medium risk; a buffer that lets " +
			"full buffers block each other in a cycle is refused (BLOCKING_CYCLE). For a resource that already has breakdowns it may replace " +
			"/resources/<i>/breakdowns/time_to_failure/distribution and /resources/<i>/breakdowns/time_to_repair/distribution (replace only: a scenario " +
			"never adds or removes breakdowns); any change to them, either way, is at least medium risk. For a resource on a calendar it may replace " +
			"/resources/<i>/calendar_id (only with the id of a calendar the model has) and /resources/<i>/shift_capacity/<label> (the units on shift " +
			"during that label's shifts, 0 to the capacity), and for a calendar /calendars/<i>/shifts and /calendars/<i>/exceptions (each a whole " +
			"array: shifts [start, end) within the period, in order, optionally labelled; exceptions [start, end) in absolute model time, open or " +
			"closed). For a source with a release schedule it may replace /nodes/<i>/arrival/schedule/segments/<j>/rate (expected releases per " +
			"model time unit, at least 0) and /nodes/<i>/arrival/schedule/releases/<j>/quantity (whole entities, at least 1). All of these are " +
			"replace only: a scenario never adds or removes a calendar, a resource's calendar, a shift_capacity label, a schedule, a segment or a " +
			"release, and never changes a period, a segment start or a release time. Any change to a calendar, its shifts or exceptions, a " +
			"resource's calendar or units on shift, a release rate or a quantity, either way, is at least medium risk; one after which a bundle's " +
			"resources are never on shift together is refused (CALENDAR_INVALID), and so is one that puts a resource that breaks down on shift " +
			"while a partner in its bundle is not (STALL_RISK). For a batch process it may replace /nodes/<i>/batch/min_size, " +
			"/nodes/<i>/batch/max_size and /nodes/<i>/batch/max_wait (replace only: a scenario never adds or removes a batch or a max_wait); " +
			"1 <= min_size <= max_size <= 10000, a batch that may start below max_size needs max_wait (in model time units), max_wait is at " +
			"most 365 days once quantized to 1 ms ticks, and a finite buffer in front of it must hold max_size, or the candidate is refused " +
			"(BATCH_INVALID); any change to a batch, either way, is at " +
			"least medium risk. For a resource with changeovers (setups) it may replace /resources/<i>/setups/default (when it has one) and " +
			"/resources/<i>/setups/transitions/<j>/distribution (replace only: a scenario never adds or removes setups, a default or a " +
			"transition, and never changes initial or a transition's from or to); any change to a changeover time, either way, is at least " +
			"medium risk. Every distribution a scenario may replace can be " +
			"constant, exponential, triangular, empirical (an existing samples_ref), lognormal (mean, cv with 0 < cv <= 10) or weibull (shape, scale " +
			"with 0.1 <= shape <= 20). It never adds or removes entities and never changes IDs, names, views or metadata.\n"
	}
	return "- New nodes, flows and resources take their IDs from ID_ALLOCATION_POOL, from the list of their own kind, each ID at most once; " +
		"never invent an ID. When the pool has too few IDs, answer needs_input.\n"
}

const indexRule = "- Array indices in JSON Patch paths are the real indices, as ENTITY_INDEX lists them. An array element shown as " +
	"{\"id\": …, \"omitted\": \"budget\"} is a real entity whose details were left out to fit the budget.\n"

// entityIndex lists every entity with its kind, real index and a few
// bounded fields: the references a slice may not show.
func entityIndex(ir *model.ModelIR, z *sanitizer) []map[string]any {
	var out []map[string]any
	name := func(s string) string { return z.textBound(s, maxNameRunes) }
	for i, e := range ir.EntityTypes {
		out = append(out, map[string]any{"kind": "entity_type", "index": i, "id": z.text(e.ID), "name": name(e.Name)})
	}
	for i, r := range ir.Resources {
		out = append(out, map[string]any{"kind": "resource", "index": i, "id": z.text(r.ID), "type": string(r.Type), "name": name(r.Name), "capacity": r.Capacity})
	}
	for i, n := range ir.Nodes {
		e := map[string]any{"kind": "node", "index": i, "id": z.text(n.ID), "type": string(n.Type)}
		if n.Name != "" {
			e["name"] = name(n.Name)
		}
		out = append(out, e)
	}
	for i, f := range ir.Flows {
		out = append(out, map[string]any{"kind": "flow", "index": i, "id": z.text(f.ID), "from": z.text(f.From), "to": z.text(f.To),
			"probability": f.Routing.Probability})
	}
	for i, m := range ir.Metrics {
		out = append(out, map[string]any{"kind": "metric", "index": i, "id": z.text(m.ID), "type": string(m.Type)})
	}
	return out
}

func entityIDs(ir *model.ModelIR) map[string]bool {
	out := map[string]bool{}
	for _, e := range ir.EntityTypes {
		out[e.ID] = true
	}
	for _, r := range ir.Resources {
		out[r.ID] = true
	}
	for _, n := range ir.Nodes {
		out[n.ID] = true
	}
	for _, f := range ir.Flows {
		out[f.ID] = true
	}
	for _, m := range ir.Metrics {
		out[m.ID] = true
	}
	return out
}

// Levels of the model IR block, from the whole model to none.
const (
	levelFull     = iota // the IR without identity and views; oversized metadata cut
	levelNoExtras        // also without metadata, domain attributes and data summaries
	levelSlice           // only the selected entities' neighbourhood in full
	levelIndex           // no IR block: ENTITY_INDEX only
)

// neighbourhood is the set of entity IDs shown in full at levelSlice: the
// selected entities, the nodes that use a selected resource, the flows
// touching any of those nodes and the nodes at their other end, the
// resources and entity types those nodes use, and the metrics on them.
func neighbourhood(ir *model.ModelIR, selected []string) map[string]bool {
	keep := map[string]bool{}
	for _, id := range selected {
		keep[id] = true
	}
	core := map[string]bool{}
	for _, n := range ir.Nodes {
		if keep[n.ID] {
			core[n.ID] = true
		}
		for _, r := range n.Requires {
			if keep[r.ResourceID] {
				core[n.ID] = true
			}
		}
	}
	for _, f := range ir.Flows {
		if keep[f.ID] || core[f.From] || core[f.To] {
			keep[f.ID], keep[f.From], keep[f.To] = true, true, true
		}
	}
	for id := range core {
		keep[id] = true
	}
	for _, n := range ir.Nodes {
		if keep[n.ID] {
			for _, r := range n.Requires {
				keep[r.ResourceID] = true
			}
			if n.EntityType != "" {
				keep[n.EntityType] = true
			}
		}
	}
	for _, m := range ir.Metrics {
		if keep[m.NodeID] || keep[m.ResourceID] || keep[m.SinkID] {
			keep[m.ID] = true
		}
	}
	return keep
}

// irView is the sanitized IR at a level, with what it leaves out.
func irView(ir *model.ModelIR, level int, selected []string, z *sanitizer, om *omissions) (any, error) {
	if level == levelIndex {
		om.add("model_ir", len(entityIDs(ir)), ReasonBudget)
		return nil, nil
	}
	tree, err := raw(ir)
	if err != nil {
		return nil, err
	}
	doc := tree.(map[string]any)
	delete(doc, "model_id")
	om.add("model_ir.model_id", 1, ReasonServerBound)
	delete(doc, "views")
	om.add("model_ir.views", 1, ReasonPresentation)
	if len(ir.Metadata) > 0 {
		switch {
		case level >= levelNoExtras:
			delete(doc, "metadata")
			om.add("model_ir.metadata", 1, ReasonBudget)
		case len(ir.Metadata) > maxMetadataBytes:
			delete(doc, "metadata")
			om.add("model_ir.metadata", 1, ReasonTooLarge)
		}
	}
	if nodes, ok := doc["nodes"].([]any); ok {
		for i, n := range nodes {
			node := n.(map[string]any)
			attrs := ir.Nodes[i].DomainAttributes
			if len(attrs) == 0 {
				continue
			}
			switch {
			case level >= levelNoExtras:
				delete(node, "domain_attributes")
				om.add("model_ir.nodes.domain_attributes", 1, ReasonBudget)
			case len(attrs) > maxAttrBytes:
				delete(node, "domain_attributes")
				om.add("model_ir.nodes.domain_attributes", 1, ReasonTooLarge)
			}
		}
	}
	if level == levelSlice {
		keep := neighbourhood(ir, selected)
		left := 0
		for _, member := range []string{"entity_types", "resources", "nodes", "flows", "metrics"} {
			items, _ := doc[member].([]any)
			for i, it := range items {
				obj := it.(map[string]any)
				id, _ := obj["id"].(string)
				if !keep[id] {
					items[i] = map[string]any{"id": id, "omitted": string(ReasonBudget)}
					left++
				}
			}
		}
		if cons, ok := doc["constraints"].([]any); ok {
			var kept []any
			for _, c := range cons {
				if keep[c.(map[string]any)["node_id"].(string)] {
					kept = append(kept, c)
				} else {
					left++
				}
			}
			doc["constraints"] = append([]any{}, kept...)
		}
		om.add("model_ir", left, ReasonBudget)
	}
	return z.value(doc), nil
}

// Proposal builds the context of an edit or scenario proposal.
func Proposal(in ProposalInput) (provider.ProposalContext, error) {
	loc, err := parseLocale(in.OutputLocale)
	if err != nil {
		return provider.ProposalContext{}, err
	}
	role := in.Mode.Role()
	if role == "" {
		return provider.ProposalContext{}, fail(InvalidInput, "/mode")
	}
	if err := in.Budget.Validate(); err != nil {
		return provider.ProposalContext{}, &Error{Code: InvalidInput, Path: "/budget", cause: err}
	}
	// Cross-tenant hard filter: every input is the request's organization's
	// and the request's model's, or nothing is built.
	if err := checkScope(in.Scope, true); err != nil {
		return provider.ProposalContext{}, err
	}
	if in.Base.OrganizationID != in.Scope.OrganizationID || in.Base.ModelID != in.Scope.ModelID {
		return provider.ProposalContext{}, fail(CrossTenant, "/base")
	}
	base := in.Base.IR
	if base == nil {
		return provider.ProposalContext{}, fail(InvalidInput, "/base/ir")
	}
	if base.ModelID != in.Scope.ModelID {
		return provider.ProposalContext{}, fail(CrossTenant, "/base/ir/model_id")
	}
	if in.Pool.OrganizationID != in.Scope.OrganizationID {
		return provider.ProposalContext{}, fail(CrossTenant, "/pool")
	}
	for i, s := range in.DataSummaries {
		if s.OrganizationID != in.Scope.OrganizationID {
			return provider.ProposalContext{}, fail(CrossTenant, fmt.Sprintf("/data_summaries/%d", i))
		}
	}
	if in.Domain.Domain != base.Domain {
		return provider.ProposalContext{}, fail(DomainMismatch, "/domain")
	}
	if err := in.Pool.IDs.Validate(); err != nil {
		var pe *idpool.Error
		path := "/pool"
		if errors.As(err, &pe) {
			path += pe.Path
		}
		return provider.ProposalContext{}, &Error{Code: InvalidInput, Path: path, cause: err}
	}
	used := entityIDs(base)
	for _, k := range idpool.Kinds {
		for _, id := range in.Pool.IDs.IDs(k) {
			if used[id] {
				return provider.ProposalContext{}, fail(InvalidInput, "/pool")
			}
		}
	}
	if strings.TrimSpace(in.Intent) == "" || utf8.RuneCountInString(in.Intent) > maxIntentRunes {
		return provider.ProposalContext{}, fail(InvalidInput, "/intent")
	}
	if len(in.SelectedEntityIDs) > maxSelected {
		return provider.ProposalContext{}, fail(InvalidInput, "/context/selected_entity_ids")
	}
	for i, id := range in.SelectedEntityIDs {
		if !entityID.MatchString(id) {
			return provider.ProposalContext{}, fail(InvalidInput, fmt.Sprintf("/context/selected_entity_ids/%d", i))
		}
	}
	var selected []string
	unknown := 0
	for _, id := range in.SelectedEntityIDs {
		switch {
		case !used[id]:
			unknown++
		case !slices.Contains(selected, id):
			selected = append(selected, id)
		}
	}

	text, version, err := template(role)
	if err != nil {
		return provider.ProposalContext{}, err
	}
	modeText := " in mode " + string(in.Mode)
	system := text + serverRules(role, modeText, loc) + modeRules(in.Mode) + indexRule + domainRules(in.Domain)
	if in.Research {
		research, rversion, err := template("research")
		if err != nil {
			return provider.ProposalContext{}, err
		}
		system += "\n" + research
		version += "+" + rversion
	}

	levels := []int{levelFull, levelNoExtras, levelSlice, levelIndex}
	if len(selected) == 0 {
		levels = []int{levelFull, levelNoExtras, levelIndex}
	}
	for _, level := range levels {
		z := newSanitizer(maxStringRunes)
		z.exempt(modelIDs(in.Base.IR))
		om := &omissions{}
		om.add("selected_entity_ids", unknown, ReasonNotInModel)
		var blocks []provider.Block
		add := func(kind provider.BlockKind, name string, v any) error {
			b, err := block(kind, name, v)
			if err == nil {
				blocks = append(blocks, b)
			}
			return err
		}
		if in.Mode == provider.ModeEdit {
			if err := add(provider.ServerData, "ID_ALLOCATION_POOL", in.Pool.IDs); err != nil {
				return provider.ProposalContext{}, err
			}
		}
		view, err := irView(base, level, selected, z, om)
		if err != nil {
			return provider.ProposalContext{}, err
		}
		if view != nil {
			if err := add(provider.UntrustedData, "CURRENT_MODEL_IR", view); err != nil {
				return provider.ProposalContext{}, err
			}
		}
		if err := add(provider.UntrustedData, "ENTITY_INDEX", entityIndex(base, z)); err != nil {
			return provider.ProposalContext{}, err
		}
		if len(selected) > 0 {
			shown := make([]string, len(selected))
			for i, id := range selected {
				shown[i] = z.text(id)
			}
			if err := add(provider.UntrustedData, "SELECTED_ENTITIES", shown); err != nil {
				return provider.ProposalContext{}, err
			}
		}
		if len(in.DataSummaries) > 0 {
			var sums []any
			for _, s := range in.DataSummaries {
				switch {
				case level >= levelNoExtras:
					om.add("data_summaries", 1, ReasonBudget)
				case len(s.Summary) > maxSummaryBytes:
					om.add("data_summaries", 1, ReasonTooLarge)
				default:
					v, err := z.json(s.Summary)
					if err != nil {
						return provider.ProposalContext{}, &Error{Code: InvalidInput, Path: "/data_summaries", cause: err}
					}
					sums = append(sums, map[string]any{"logical_ref": z.text(s.LogicalRef), "summary": v})
				}
			}
			if len(sums) > 0 {
				if err := add(provider.UntrustedData, "OPTIONAL_DATA_SUMMARY", sums); err != nil {
					return provider.ProposalContext{}, err
				}
			}
		}
		if err := add(provider.UntrustedData, "USER_INTENT", z.textBound(in.Intent, maxIntentRunes)); err != nil {
			return provider.ProposalContext{}, err
		}
		p, err := assemble(in.Scope.OrganizationID, role, loc, system, version, blocks, om, z)
		if err != nil {
			return provider.ProposalContext{}, err
		}
		if in.Research {
			// Sealed again with Research, which the hash covers.
			p.Research = true
			if p, err = provider.Seal(p); err != nil {
				return provider.ProposalContext{}, err
			}
		}
		if fits(p, in.Budget) {
			return provider.ProposalContext{OrganizationID: in.Scope.OrganizationID, Mode: in.Mode, Prompt: p}, nil
		}
	}
	return provider.ProposalContext{}, fail(TooLarge, "")
}
