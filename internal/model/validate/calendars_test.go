package validate_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kcl1104/geminus/internal/contracttest"
	"github.com/kcl1104/geminus/internal/model/validate"
)

// T-049: the rules of shift calendars (D-052; D-050 R2, R4) and release
// schedules (D-053), on the minimal model (start -> work(worker) -> end,
// minutes; worker has capacity 1). Paths are TWIN_IR_SPEC §4's.

func shift(start, end float64, label ...string) map[string]any {
	s := map[string]any{"start": start, "end": end}
	if len(label) > 0 {
		s["label"] = label[0]
	}
	return s
}

func exception(start, end float64, open bool) map[string]any {
	return map[string]any{"start": start, "end": end, "open": open}
}

func cal(id string, period float64, shifts []any, exceptions ...any) map[string]any {
	c := map[string]any{"id": id, "period": period, "shifts": shifts}
	if len(exceptions) > 0 {
		c["exceptions"] = exceptions
	}
	return c
}

// onCalendar adds calendars to the model and gives resource 0 (worker)
// calendar_id (and shift_capacity when given).
func onCalendar(id string, shiftCapacity map[string]any, calendars ...any) func(ir map[string]any) {
	return func(ir map[string]any) {
		ir["calendars"] = calendars
		w := obj(resources(ir)[0])
		w["calendar_id"] = id
		if shiftCapacity != nil {
			w["shift_capacity"] = shiftCapacity
		}
	}
}

func schedule(s map[string]any) func(ir map[string]any) {
	return func(ir map[string]any) {
		a := obj(obj(nodes(ir)[0])["arrival"])
		delete(a, "distribution")
		a["schedule"] = s
	}
}

func segments(period float64, process string, startRate ...float64) map[string]any {
	var segs []any
	for i := 0; i < len(startRate); i += 2 {
		segs = append(segs, map[string]any{"start": startRate[i], "rate": startRate[i+1]})
	}
	return map[string]any{"process": process, "period": period, "segments": segs}
}

func releases(period float64, atQuantity ...float64) map[string]any {
	var rows []any
	for i := 0; i < len(atQuantity); i += 2 {
		rows = append(rows, map[string]any{"at": atQuantity[i], "quantity": atQuantity[i+1]})
	}
	return map[string]any{"process": "table", "period": period, "releases": rows}
}

func checkCases(t *testing.T, cases []struct {
	name string
	edit func(ir map[string]any)
	want [][3]string
}) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := run(t, edit(t, c.edit))
			got := issuesOf(r)
			if len(got) != len(c.want) {
				t.Fatalf("issues %v, want %v", r.Issues, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("issue %d: %v, want %v (%s)", i, got[i], c.want[i], r.Issues[i].Message)
				}
			}
		})
	}
}

const day = 1440.0

func TestCalendarRules(t *testing.T) {
	two := []any{shift(360, 840, "day"), shift(840, 1320, "evening")}
	checkCases(t, []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		{"a valid calendar", onCalendar("c", nil, cal("c", day, two)), nil},
		{"a calendar no resource uses", func(ir map[string]any) { ir["calendars"] = []any{cal("c", day, two)} }, nil},
		{"adjacent shifts, one covering the period", onCalendar("c", nil, cal("c", day, []any{shift(0, 600), shift(600, day)})), nil},
		{"shift capacity within capacity", func(ir map[string]any) {
			obj(resources(ir)[0])["capacity"] = 2
			onCalendar("c", map[string]any{"day": 2, "evening": 1}, cal("c", day, two))(ir)
		}, nil},
		{"exceptions in order", onCalendar("c", nil, cal("c", day, two, exception(7200, 8640, false), exception(9000, 9300, true))), nil},
		// D-052 shape, the distribution/domain semantics stage.
		{"a period over 365 days", onCalendar("c", nil, cal("c", 366*day, []any{shift(0, 600)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/period", "error"}}},
		{"overlapping shifts", onCalendar("c", nil, cal("c", day, []any{shift(0, 600), shift(500, 900)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/shifts/1", "error"}}},
		{"shifts out of order", onCalendar("c", nil, cal("c", day, []any{shift(600, 900), shift(0, 500)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/shifts/1", "error"}}},
		{"a shift past the period", onCalendar("c", nil, cal("c", day, []any{shift(0, 1441)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/shifts/0", "error"}}},
		{"a shift that ends where it starts", onCalendar("c", nil, cal("c", day, []any{shift(10, 10)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/shifts/0", "error"}}},
		// 1e-6 and 2e-6 minutes are 0.06 and 0.12 ms: both quantize to 1 tick.
		{"a shift empty in ticks", onCalendar("c", nil, cal("c", day, []any{shift(1e-6, 2e-6), shift(10, 20)})),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/shifts/0", "error"}}},
		{"overlapping exceptions", onCalendar("c", nil, cal("c", day, two, exception(100, 200, false), exception(150, 300, true))),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/exceptions/1", "error"}}},
		{"exceptions out of order", onCalendar("c", nil, cal("c", day, two, exception(500, 600, false), exception(100, 200, true))),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/exceptions/1", "error"}}},
		{"an exception past 365 days", onCalendar("c", nil, cal("c", day, two, exception(365*day-10, 365*day+10, false))),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/exceptions/0", "error"}}},
		{"an exception that ends where it starts", onCalendar("c", nil, cal("c", day, two, exception(100, 100, false))),
			[][3]string{{validate.CalendarInvalid, "/calendars/0/exceptions/0", "error"}}},
		{"a shift_capacity label the calendar does not have", onCalendar("c", map[string]any{"night": 1}, cal("c", day, two)),
			[][3]string{{validate.CalendarInvalid, "/resources/0/shift_capacity/night", "error"}}},
		{"a shift_capacity over the capacity", onCalendar("c", map[string]any{"day": 2}, cal("c", day, two)),
			[][3]string{{validate.CalendarInvalid, "/resources/0/shift_capacity/day", "error"}}},
		// D-052: no time zone; the ID and reference rules of every catalog.
		{"two calendars with one ID", onCalendar("c", nil, cal("c", day, two), cal("c", 7*day, two)),
			[][3]string{{validate.DuplicateID, "/calendars/1/id", "error"}}},
		{"an unknown calendar", onCalendar("other", nil, cal("c", day, two)),
			[][3]string{{validate.UnknownReference, "/resources/0/calendar_id", "error"}}},
	})
}

func TestReleaseScheduleRules(t *testing.T) {
	checkCases(t, []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		{"a deterministic schedule", schedule(segments(60, "deterministic", 0, 0.5, 30, 0)), nil},
		{"a poisson schedule", schedule(segments(day, "poisson", 0, 0.05, 360, 0.5, 1320, 0.01)), nil},
		{"a table", schedule(releases(day, 0, 20, 840, 20)), nil},
		{"a rate of one a tick", schedule(segments(1, "poisson", 0, 60000)), nil},
		// D-053, the distribution/domain semantics stage.
		{"a first segment after 0", schedule(segments(60, "deterministic", 10, 0.5)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/segments/0/start", "error"}}},
		{"segment starts not increasing", schedule(segments(60, "poisson", 0, 1, 30, 1, 30, 2)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/segments/2/start", "error"}}},
		{"a segment at the period's end", schedule(segments(60, "poisson", 0, 1, 60, 2)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/segments/1/start", "error"}}},
		{"every rate 0", schedule(segments(60, "poisson", 0, 0, 30, 0)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/segments", "error"}}},
		{"more than one a tick", schedule(segments(60, "deterministic", 0, 1, 30, 60001)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/segments/1/rate", "error"}}},
		{"a period over 365 days", schedule(segments(366*day, "poisson", 0, 1)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/period", "error"}}},
		{"a table period over 365 days", schedule(releases(366*day, 0, 1)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/period", "error"}}},
		{"release times not increasing", schedule(releases(day, 600, 1, 600, 2)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/releases/1/at", "error"}}},
		{"a release at the period's end", schedule(releases(day, 0, 1, day, 2)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/releases/1/at", "error"}}},
		// 0.9999999999 minutes is 59999.999994 ms: the period's own tick.
		{"a release that rounds to the period's end", schedule(releases(1, 0, 1, 0.9999999999, 1)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/releases/1/at", "error"}}},
		// 1e-6 and 2e-6 minutes are one tick each.
		{"two releases in one tick", schedule(releases(1, 1e-6, 1, 2e-6, 1)),
			[][3]string{{validate.ReleaseScheduleInvalid, "/nodes/0/arrival/schedule/releases/1/at", "error"}}},
	})
}

// D-050 R2 and R4, the bundle feasibility stage (after the distribution
// and domain semantics stage, as R3).
func TestBundleFeasibility(t *testing.T) {
	early, late := cal("early", day, []any{shift(0, 480)}), cal("late", day, []any{shift(480, day)})
	helper := func(id string, extra map[string]any) func(ir map[string]any) {
		return func(ir map[string]any) {
			r := map[string]any{"id": "helper", "type": "human", "name": "Helper", "capacity": 1, "queue_discipline": "fifo", "calendar_id": id}
			for k, v := range extra {
				r[k] = v
			}
			ir["resources"] = append(resources(ir), r)
			w := obj(nodes(ir)[1])
			w["requires"] = append(w["requires"].([]any), map[string]any{"resource_id": "helper", "units": 1})
		}
	}
	both := func(fs ...func(ir map[string]any)) func(ir map[string]any) {
		return func(ir map[string]any) {
			for _, f := range fs {
				f(ir)
			}
		}
	}
	good := breakdowns(map[string]any{"type": "exponential", "mean": 600}, constantDist(30))
	breaks := func(ir map[string]any) { obj(resources(ir)[0])["breakdowns"] = good }
	checkCases(t, []struct {
		name string
		edit func(ir map[string]any)
		want [][3]string
	}{
		// R2: some tick of the periods' LCM fits every resource of the bundle.
		{"R2: calendars that never meet", both(onCalendar("early", nil, early, late), helper("late", nil)),
			[][3]string{{validate.CalendarInvalid, "/nodes/1/requires", "error"}}},
		{"R2: calendars that meet", both(onCalendar("early", nil, early, cal("late", day, []any{shift(400, day)})), helper("late", nil)), nil},
		{"R2: periods whose LCM is over 365 days", both(onCalendar("season", nil, cal("season", 300*day, []any{shift(0, 150*day)}),
			cal("week", 7*day, []any{shift(0, 5*day)})), helper("week", nil)),
			[][3]string{{validate.CalendarInvalid, "/calendars", "error"}}},
		{"R2: an unused calendar counts in the LCM", func(ir map[string]any) {
			ir["calendars"] = []any{cal("season", 300*day, []any{shift(0, 150*day)}), cal("week", 7*day, []any{shift(0, 5*day)})}
		}, [][3]string{{validate.CalendarInvalid, "/calendars", "error"}}},
		{"R2: coprime periods that meet late", both(onCalendar("a", nil, cal("a", 100, []any{shift(1, 2)}), cal("b", 101, []any{shift(0, 1)})),
			helper("b", nil)), nil}, // the first common tick is minute 101 (LCM 10,100)
		{"R2: coprime periods that never meet", both(onCalendar("a", nil, cal("a", 4, []any{shift(0, 1), shift(2, 3)}), cal("b", 2, []any{shift(1, 2)})),
			helper("b", nil)), [][3]string{{validate.CalendarInvalid, "/nodes/1/requires", "error"}}},
		{"R2: two units, never two on shift", func(ir map[string]any) {
			obj(resources(ir)[0])["capacity"] = 2
			obj(obj(nodes(ir)[1])["requires"].([]any)[0])["units"] = 2
			onCalendar("c", map[string]any{"day": 1}, cal("c", day, []any{shift(360, 840, "day"), shift(840, 1320)}))(ir)
		}, nil}, // the unlabeled shift has both units
		{"R2: two units, at most one on shift", func(ir map[string]any) {
			obj(resources(ir)[0])["capacity"] = 2
			obj(obj(nodes(ir)[1])["requires"].([]any)[0])["units"] = 2
			onCalendar("c", map[string]any{"day": 1, "night": 1}, cal("c", day, []any{shift(360, 840, "day"), shift(840, 1320, "night")}))(ir)
		}, [][3]string{{validate.CalendarInvalid, "/nodes/1/requires", "error"}}},
		{"R2: only an open exception fits (the period alone counts)", both(onCalendar("early", nil, early,
			cal("late", day, []any{shift(480, day)}, exception(100, 200, true))), helper("late", nil)),
			[][3]string{{validate.CalendarInvalid, "/nodes/1/requires", "error"}}},
		{"R2: a closed exception over the only fit (the period alone counts)", both(onCalendar("early", nil, early,
			cal("late", day, []any{shift(400, day)}, exception(400, 480, false))), helper("late", nil)), nil},
		// R4: while a resource that breaks down is on shift, its partners are.
		{"R4: a machine without a calendar, an operator with one", both(breaks, helper("c", nil), func(ir map[string]any) {
			ir["calendars"] = []any{cal("c", day, []any{shift(360, 840), shift(840, 1320)})}
		}), [][3]string{{validate.StallRisk, "/nodes/1/requires", "error"}}},
		{"R4: the machine on the operator's calendar", both(breaks, onCalendar("c", nil, cal("c", day, []any{shift(360, 840), shift(840, 1320)})),
			helper("c", nil)), nil},
		{"R4: the machine's shift longer than the operator's", both(breaks, onCalendar("m", nil, cal("m", day, []any{shift(0, 600)}),
			cal("o", day, []any{shift(0, 480)})), helper("o", nil)),
			[][3]string{{validate.StallRisk, "/nodes/1/requires", "error"}}},
		{"R4: the operator's shift longer than the machine's", both(breaks, onCalendar("m", nil, cal("m", day, []any{shift(0, 480)}),
			cal("o", day, []any{shift(0, 600)})), helper("o", nil)), nil},
		{"R4: the exceptions do not count", both(breaks, onCalendar("m", nil, cal("m", day, []any{shift(0, 480)}),
			cal("o", day, []any{shift(0, 480)}, exception(day, day+480, false))), helper("o", nil)), nil},
		{"R4: a partner's units on shift too few", both(breaks, onCalendar("c", nil, cal("c", day, []any{shift(0, 480, "a"), shift(480, 960, "b")})),
			helper("c", map[string]any{"capacity": 2, "shift_capacity": map[string]any{"b": 1}}), func(ir map[string]any) {
				obj(obj(nodes(ir)[1])["requires"].([]any)[1])["units"] = 2
			}), [][3]string{{validate.StallRisk, "/nodes/1/requires", "error"}}},
		{"R4 is not checked where R2 already fails", both(breaks, onCalendar("early", nil, early, late), helper("late", nil)),
			[][3]string{{validate.CalendarInvalid, "/nodes/1/requires", "error"}}},
	})
}

// R4's message names the resource that breaks down and its partner, and
// says how to fix it (D-050: give it the partner's calendar).
func TestStallRiskMessage(t *testing.T) {
	r := run(t, contracttest.ReadFile(t, filepath.Join(contracttest.Root(t), "testdata/contracts/semantic-stall-risk.json")))
	if len(r.Issues) != 1 || r.Issues[0].Code != validate.StallRisk {
		t.Fatalf("%v", r.Issues)
	}
	for _, w := range []string{`"machine"`, `"operator"`, "calendar", "D-050 R4"} {
		if !strings.Contains(r.Issues[0].Message, w) {
			t.Errorf("the message %q does not say %s", r.Issues[0].Message, w)
		}
	}
}
