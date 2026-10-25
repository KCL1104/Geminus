package review

import (
	"testing"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/model/patch"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// D-057 (T-049): calendars and release schedules through the whole review.
// A scenario may replace a calendar's shifts and exceptions, a resource's
// calendar and its units on shift, a schedule's rates and a table's
// quantities; any change to them, either way, is at least medium, and a
// candidate whose bundle is never on shift (R2) is invalid. An edit may
// change calendars but never add one: the pool allocates no calendar IDs
// (D-083, PROPOSED; T-046 review I3).
//
// The base is default-delivery.json after a person's edit (a draft
// publish): calendar two_shift (period 1440: day [360, 840), evening
// [840, 1320), closed [10080, 11520)), ci_runner (resource 1, capacity 3)
// on it with 2 units in the evening, and change_arrival (node 0) on a
// poisson schedule (rate 0.01 until 360, then 0.03).
func calendarBase(t *testing.T) []byte {
	t.Helper()
	edit := `[{"op":"add","path":"/calendars","value":[{"id":"two_shift","name":"Two shifts","period":1440,
			"shifts":[{"start":360,"end":840,"label":"day"},{"start":840,"end":1320,"label":"evening"}],
			"exceptions":[{"start":10080,"end":11520,"open":false}]}]},
		{"op":"test","path":"/resources/1/id","value":"ci_runner"},
		{"op":"add","path":"/resources/1/calendar_id","value":"two_shift"},
		{"op":"add","path":"/resources/1/shift_capacity","value":{"evening":2}},
		{"op":"test","path":"/nodes/0/id","value":"change_arrival"},
		{"op":"replace","path":"/nodes/0/arrival","value":{"schedule":{"process":"poisson","period":1440,
			"segments":[{"start":0,"rate":0.01},{"start":360,"rate":0.03}]}}}]`
	res, err := patch.Apply(base(t), []byte(edit), patch.ManualDraft)
	if err != nil || !res.Applied() {
		t.Fatalf("base edit: %v %v", err, res.PatchIssues)
	}
	if r := validateDoc(t, res.Candidate); !r.Valid() {
		t.Fatalf("base: %v", r.Issues)
	}
	return res.Candidate
}

func TestCalendarRisk(t *testing.T) {
	b := calendarBase(t)
	cal := `{"op":"test","path":"/calendars/0/id","value":"two_shift"}`
	ci := `{"op":"test","path":"/resources/1/id","value":"ci_runner"}`
	src := `{"op":"test","path":"/nodes/0/id","value":"change_arrival"}`
	for _, c := range []struct {
		name    string
		mode    provider.Mode
		changes string
		want    Risk
	}{
		{"a night shift added", provider.ModeScenario, `[` + cal + `,{"op":"replace","path":"/calendars/0/shifts","value":[{"start":360,"end":840,"label":"day"},` +
			`{"start":840,"end":1320,"label":"evening"},{"start":1320,"end":1440,"label":"night"}]}]`, Medium},
		{"a shorter day shift", provider.ModeScenario, `[` + cal + `,{"op":"replace","path":"/calendars/0/shifts","value":[{"start":420,"end":840,"label":"day"},` +
			`{"start":840,"end":1320,"label":"evening"}]}]`, Medium},
		{"a shift label renamed", provider.ModeScenario, `[` + cal + `,{"op":"replace","path":"/calendars/0/shifts","value":[{"start":360,"end":840,"label":"early"},` +
			`{"start":840,"end":1320,"label":"evening"}]}]`, Medium},
		{"the closed week opened", provider.ModeScenario, `[` + cal + `,{"op":"replace","path":"/calendars/0/exceptions","value":[{"start":10080,"end":11520,"open":true}]}]`, Medium},
		{"fewer units in the evening", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"/resources/1/shift_capacity/evening","value":1}]`, Medium},
		{"more units in the evening", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"/resources/1/shift_capacity/evening","value":3}]`, Medium},
		{"a higher release rate", provider.ModeScenario, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival/schedule/segments/1/rate","value":0.05}]`, Medium},
		{"a lower release rate", provider.ModeScenario, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival/schedule/segments/1/rate","value":0.02}]`, Medium},
		// The risk is the models': rewriting the same rate beside a higher
		// capacity changes no schedule.
		{"the same rate rewritten", provider.ModeScenario, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival/schedule/segments/1/rate","value":0.03},` +
			`{"op":"test","path":"/resources/0/id","value":"reviewer"},{"op":"replace","path":"/resources/0/capacity","value":3}]`, Low},
		{"ci_runner off its calendar", provider.ModeEdit, `[` + ci + `,{"op":"remove","path":"/resources/1/calendar_id"},{"op":"remove","path":"/resources/1/shift_capacity"}]`, Medium},
		{"a schedule replaced by an interarrival time", provider.ModeEdit, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival","value":{"distribution":{"type":"exponential","mean":48}}}]`, Medium},
		{"another schedule period", provider.ModeEdit, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival/schedule/period","value":720}]`, Medium},
		// A calendar's name is presentation.
		{"the calendar renamed", provider.ModeEdit, `[` + cal + `,{"op":"replace","path":"/calendars/0/name","value":"Day and evening"}]`, Low},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, Input{Mode: c.mode, Base: b, Pool: pool(t, b), Pack: input(t, c.mode, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")})
			if o.Status != Ready || o.ServerRisk != c.want || o.Risk != c.want {
				t.Fatalf("status %q, server risk %q, risk %q, want ready %q (%v)", o.Status, o.ServerRisk, o.Risk, c.want, codes(o))
			}
		})
	}
	for _, c := range []struct {
		name       string
		mode       provider.Mode
		changes    string
		code, path string
	}{
		// R2 in a candidate: ci needs a unit of ci_runner, which no shift
		// has any more.
		{"no unit on any shift", provider.ModeScenario, `[` + cal + `,{"op":"replace","path":"/calendars/0/shifts","value":[{"start":840,"end":1320,"label":"evening"}]},` +
			ci + `,{"op":"replace","path":"/resources/1/shift_capacity/evening","value":0}]`, validate.CalendarInvalid, "/nodes/2/requires"},
		{"an unknown calendar", provider.ModeScenario, `[` + ci + `,{"op":"replace","path":"/resources/1/calendar_id","value":"three_shift"}]`, validate.UnknownReference, "/resources/1/calendar_id"},
		// A scenario never adds or removes a calendar, a schedule or a label.
		{"a scenario removes the calendar", provider.ModeScenario, `[` + ci + `,{"op":"remove","path":"/resources/1/calendar_id"}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario adds a label", provider.ModeScenario, `[` + ci + `,{"op":"add","path":"/resources/1/shift_capacity/day","value":1}]`, patch.PathForbidden, "/changes/1/path"},
		{"a scenario moves a segment", provider.ModeScenario, `[` + src + `,{"op":"replace","path":"/nodes/0/arrival/schedule/segments/1/start","value":420}]`, patch.PathForbidden, "/changes/1/path"},
		// D-083: an edit never adds a calendar, whatever its ID.
		{"an edit adds a calendar", provider.ModeEdit, `[{"op":"add","path":"/calendars/-","value":{"id":"weekend","period":10080,"shifts":[{"start":0,"end":7200}]}},` +
			ci + `,{"op":"replace","path":"/resources/1/calendar_id","value":"weekend"},{"op":"remove","path":"/resources/1/shift_capacity"}]`, IDPoolViolation, "/calendars/1/id"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o := run(t, Input{Mode: c.mode, Base: b, Pool: pool(t, b), Pack: input(t, c.mode, proposed(nil, "low")).Pack,
				Output: proposed(ops(t, c.changes), "low")})
			wantInvalid(t, o, c.code, c.path)
		})
	}
	// Nor with an ID taken from the pool (the resource list).
	p := pool(t, b)
	o := run(t, Input{Mode: provider.ModeEdit, Base: b, Pool: p, Pack: input(t, provider.ModeEdit, proposed(nil, "low")).Pack,
		Output: proposed(ops(t, `[{"op":"add","path":"/calendars/-","value":{"id":"`+p.Resource[0]+`","period":60,"shifts":[{"start":0,"end":30}]}}]`), "low")})
	wantInvalid(t, o, IDPoolViolation, "/calendars/1/id")
}

// The numbers a proposal's expected effects may quote include the
// calendars' times and units on shift and the schedules' rates and
// quantities (values): an effect that names the new shift's end or the
// new rate is no invented number.
func TestCalendarValuesInEffects(t *testing.T) {
	b := calendarBase(t)
	scenario := func(changes string, effect string) Outcome {
		out := proposed(ops(t, changes), "low")
		out.ExpectedEffects = []string{effect}
		return run(t, Input{Mode: provider.ModeScenario, Base: b, Pool: pool(t, b), Pack: input(t, provider.ModeScenario, proposed(nil, "low")).Pack, Output: out})
	}
	night := `[{"op":"test","path":"/calendars/0/id","value":"two_shift"},{"op":"replace","path":"/calendars/0/shifts","value":[{"start":360,"end":840,"label":"day"},` +
		`{"start":840,"end":1320,"label":"evening"},{"start":1320,"end":1440,"label":"night"}]}]`
	if o := scenario(night, "A night shift from 1320 to 1440 keeps CI on shift longer; a simulation will tell how much CI waits."); o.Status != Ready {
		t.Errorf("the shift's own times: %q (%v)", o.Status, codes(o))
	}
	rate := `[{"op":"test","path":"/nodes/0/id","value":"change_arrival"},{"op":"replace","path":"/nodes/0/arrival/schedule/segments/1/rate","value":0.05}]`
	if o := scenario(rate, "Releasing 0.05 changes a minute instead of 0.03 from minute 360 loads CI more."); o.Status != Ready {
		t.Errorf("the rates: %q (%v)", o.Status, codes(o))
	}
	wantInvalid(t, scenario(rate, "Releasing 0.05 changes a minute makes CI wait 40% longer."), OutputInvalid, "/expected_effects/0")
}
