package main

import "testing"

// H-061 (approved by the owner in H-063; docs/design/H-061_BACKGROUND_INVESTIGATOR.md
// §4 and slice A1, whose rule changes fold into E0 while E0 is unmerged):
// the investigation's API side (internal/investigations, its background
// executor), the API's one organization-only scope
// (internal/tenant/actingfor) and the effects shared with approval
// (internal/proposals/effect) are linked only by the API; the worker's
// investigation handler (internal/investigator) only by the worker. Each
// probe adds the package it names through a go build overlay, beside the
// real one where it exists (A3: actingfor; A4: effect; A6: the handler; A7:
// the service and the executor).
func TestInvestigationPlacement(t *testing.T) {
	m := func(p string) string { return module + "/" + p }
	service := blankImports("investigations")
	executor := blankImports("executor")
	actingfor := blankImports("actingfor", m("internal/tenant"))
	effect := blankImports("effect")
	handler := blankImports("investigator")
	assertPlacements(t, []placement{
		// investigations-only-in-the-api
		{"the worker links the investigation service", map[string]string{
			"internal/investigations/a.go": service,
			"cmd/worker/zz_probe.go":       blankImports("main", m("internal/investigations")),
		}, "investigations-only-in-the-api", "./cmd/worker", "./internal/investigations", ""},
		{"the investigation handler links the executor", map[string]string{
			"internal/investigations/executor/a.go": executor,
			"internal/investigator/a.go":            blankImports("investigator", m("internal/investigations/executor")),
		}, "investigations-only-in-the-api", "./internal/investigator", "./internal/investigations/executor", ""},
		{"a service links the investigation service", map[string]string{
			"internal/investigations/a.go": service,
			"internal/runs/zz_probe.go":    blankImports("runs", m("internal/investigations")),
		}, "investigations-only-in-the-api", "./internal/runs", "./internal/investigations", ""},
		// H-061 A7: the executor now exists and links the services, which
		// link the HTTP transport, so internal/httpapi can no longer link it
		// (an import cycle); another service probes the same rule.
		{"another service links the executor", map[string]string{
			"internal/investigations/executor/a.go": executor,
			"internal/project/zz_probe.go":          blankImports("project", m("internal/investigations/executor")),
		}, "investigations-only-in-the-api", "./internal/project", "./internal/investigations/executor", ""},
		// acting-for-only-in-the-executor
		{"the worker links the acting-for scope", map[string]string{
			"internal/tenant/actingfor/a.go": actingfor,
			"cmd/worker/zz_probe.go":         blankImports("main", m("internal/tenant/actingfor")),
		}, "acting-for-only-in-the-executor", "./cmd/worker", "./internal/tenant/actingfor", ""},
		{"the investigation service links the acting-for scope", map[string]string{
			"internal/tenant/actingfor/a.go": actingfor,
			"internal/investigations/a.go":   blankImports("investigations", m("internal/tenant/actingfor")),
		}, "acting-for-only-in-the-executor", "./internal/investigations", "./internal/tenant/actingfor", ""},
		{"the route assembly links the acting-for scope", map[string]string{
			"internal/tenant/actingfor/a.go": actingfor,
			"internal/api/zz_probe.go":       blankImports("api", m("internal/tenant/actingfor")),
		}, "acting-for-only-in-the-executor", "./internal/api", "./internal/tenant/actingfor", ""},
		{"the shared effects link the acting-for scope", map[string]string{
			"internal/tenant/actingfor/a.go": actingfor,
			"internal/proposals/effect/a.go": blankImports("effect", m("internal/tenant/actingfor")),
		}, "acting-for-only-in-the-executor", "./internal/proposals/effect", "./internal/tenant/actingfor", ""},
		// proposal-effects-only-in-the-api
		{"the investigation handler links the shared effects", map[string]string{
			"internal/proposals/effect/a.go": effect,
			"internal/investigator/a.go":     blankImports("investigator", m("internal/proposals/effect")),
		}, "proposal-effects-only-in-the-api", "./internal/investigator", "./internal/proposals/effect", ""},
		{"the worker links the shared effects", map[string]string{
			"internal/proposals/effect/a.go": effect,
			"cmd/worker/zz_probe.go":         blankImports("main", m("internal/proposals/effect")),
		}, "proposal-effects-only-in-the-api", "./cmd/worker", "./internal/proposals/effect", ""},
		{"the proposal generator links the shared effects", map[string]string{
			"internal/proposals/effect/a.go":           effect,
			"internal/proposals/generator/zz_probe.go": blankImports("generator", m("internal/proposals/effect")),
		}, "proposal-effects-only-in-the-api", "./internal/proposals/generator", "./internal/proposals/effect", ""},
		// job-handlers-only-in-the-worker, and the handler is an application
		// layer the AI packages and the role graphs never reach
		{"the API links the investigation handler", map[string]string{
			"internal/investigator/a.go": handler,
			"cmd/api/zz_probe.go":        blankImports("main", m("internal/investigator")),
		}, "job-handlers-only-in-the-worker", "./cmd/api", "./internal/investigator", ""},
		{"the investigation service links the investigation handler", map[string]string{
			"internal/investigator/a.go":   handler,
			"internal/investigations/a.go": blankImports("investigations", m("internal/investigator")),
		}, "job-handlers-only-in-the-worker", "./internal/investigations", "./internal/investigator", ""},
		// H-061 A6: the handler now exists and links the context builder and
		// the investigator agent (the planner's context, its ADK run), so
		// neither can link it (an import cycle the compiler refuses); an AI
		// package and a role graph it does not link probe the same rules.
		{"the analyst's checks link the investigation handler", map[string]string{
			"internal/investigator/a.go":      handler,
			"internal/ai/analyst/zz_probe.go": blankImports("analyst", m("internal/investigator")),
		}, "ai-suggests-only", "./internal/ai/analyst", "./internal/investigator", ""},
		{"a role graph links the investigation handler", map[string]string{
			"internal/investigator/a.go":            handler,
			"internal/aigraph/analysis/zz_probe.go": blankImports("analysis", m("internal/investigator")),
		}, "aigraph-has-no-storage", "./internal/aigraph/analysis", "./internal/investigator", ""},
	})
}
