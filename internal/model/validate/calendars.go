package validate

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/calendar"
)

// The rules of shift calendars (ADR-026 D-052; D-050 R2 and R4) and
// release schedules (D-053), T-049. Every time is quantized with
// model.Ticks first, as the engine does (SIMULATION_SPEC §2): a calendar
// that is in order in model time but not in ticks is refused, so the
// kernel never sees one.

// calendarShapes is D-052's shape rules (the distribution and domain
// semantics stage): a period within (0, 365 days]; shifts that start
// before they end, in order of start, disjoint, within the period;
// exceptions likewise within 365 days; shift_capacity labels the
// resource's calendar has, of at most the resource's capacity.
func calendarShapes(ir *model.ModelIR, out *issues) {
	unit := model.TimeUnit(ir.TimeUnit)
	for c, cal := range ir.Calendars {
		base := Pointer("calendars", idx(c))
		period, err := model.Ticks(cal.Period, unit)
		periodOK := err == nil
		if !periodOK {
			out.add(CalendarInvalid, base+"/period", "a calendar's period must be at most 365 days")
		}
		end := int64(0)
		for k, s := range cal.Shifts {
			start, err1 := model.Ticks(s.Start, unit)
			stop, err2 := model.Ticks(s.End, unit)
			path := base + "/shifts/" + idx(k)
			switch {
			case err1 != nil || err2 != nil:
				out.add(CalendarInvalid, path, "a shift must lie within the calendar's period, at most 365 days")
				continue
			case stop <= start:
				out.add(CalendarInvalid, path, "a shift must end after it starts, in 1 ms ticks")
			case k > 0 && start < end:
				out.add(CalendarInvalid, path, "shifts must be in order of start and must not overlap")
			case periodOK && stop > period:
				out.add(CalendarInvalid, path, "a shift must end within the calendar's period")
			}
			end = max(end, stop)
		}
		end = 0
		for k, e := range cal.Exceptions {
			start, err1 := model.Ticks(e.Start, unit)
			stop, err2 := model.Ticks(e.End, unit)
			path := base + "/exceptions/" + idx(k)
			switch {
			case err1 != nil || err2 != nil:
				out.add(CalendarInvalid, path, "an exception must lie within 365 days of t=0")
				continue
			case stop <= start:
				out.add(CalendarInvalid, path, "an exception must end after it starts, in 1 ms ticks")
			case k > 0 && start < end:
				out.add(CalendarInvalid, path, "exceptions must be in order of start and must not overlap")
			}
			end = max(end, stop)
		}
	}
	byID := map[string]*model.Calendar{}
	for c := range ir.Calendars {
		if _, dup := byID[ir.Calendars[c].ID]; !dup {
			byID[ir.Calendars[c].ID] = &ir.Calendars[c]
		}
	}
	for r, res := range ir.Resources {
		cal := byID[res.CalendarID]
		if res.ShiftCapacity == nil || cal == nil {
			continue // shift_capacity needs calendar_id (schema), which the reference stage checked
		}
		labels := map[string]bool{}
		for _, s := range cal.Shifts {
			labels[s.Label] = s.Label != ""
		}
		for _, label := range slices.Sorted(maps.Keys(res.ShiftCapacity)) {
			path := Pointer("resources", idx(r), "shift_capacity", label)
			switch units := res.ShiftCapacity[label]; {
			case !labels[label]:
				out.add(CalendarInvalid, path, "the resource's calendar has no shift with this label")
			case units > res.Capacity:
				out.add(CalendarInvalid, path, fmt.Sprintf("%d units on shift, more than the resource's capacity of %d", units, res.Capacity))
			}
		}
	}
}

// releaseSchedules is D-053's rules (the distribution and domain semantics
// stage): a period within (0, 365 days]; a rate schedule's segments start
// at 0, then in strictly increasing order before the period's end, at most
// one release a tick each, at least one positive; a table's release times
// strictly increasing in ticks and before the period's end.
func releaseSchedules(ir *model.ModelIR, out *issues) {
	unit := model.TimeUnit(ir.TimeUnit)
	ms, _ := unit.Millis()
	for i, n := range ir.Nodes {
		if n.Arrival == nil || n.Arrival.Schedule == nil {
			continue
		}
		s := n.Arrival.Schedule
		base := Pointer("nodes", idx(i), "arrival", "schedule")
		period, err := model.Ticks(s.Period, unit)
		periodOK := err == nil
		if !periodOK {
			out.add(ReleaseScheduleInvalid, base+"/period", "a release schedule's period must be at most 365 days")
		}
		if s.Process == model.ReleaseTable {
			last := int64(-1)
			for k, r := range s.Releases {
				at, err := model.Ticks(r.At, unit)
				path := base + "/releases/" + idx(k) + "/at"
				switch {
				case err != nil:
					out.add(ReleaseScheduleInvalid, path, "a release must lie within the period, at most 365 days")
					continue
				case at <= last:
					out.add(ReleaseScheduleInvalid, path, "release times must be strictly increasing, in 1 ms ticks")
				case periodOK && at >= period:
					out.add(ReleaseScheduleInvalid, path, "a release must come before the period's end, in 1 ms ticks")
				}
				last = max(last, at)
			}
			continue
		}
		positive := false
		for k, seg := range s.Segments {
			path := base + "/segments/" + idx(k)
			switch {
			case k == 0 && seg.Start != 0:
				out.add(ReleaseScheduleInvalid, path+"/start", "the first segment must start at 0")
			case k > 0 && seg.Start <= s.Segments[k-1].Start:
				out.add(ReleaseScheduleInvalid, path+"/start", "segment starts must be strictly increasing")
			case seg.Start >= s.Period:
				out.add(ReleaseScheduleInvalid, path+"/start", "a segment must start before the period's end")
			}
			if seg.Rate > float64(ms) {
				out.add(ReleaseScheduleInvalid, path+"/rate", fmt.Sprintf("a rate may be at most one release a tick (%d a %s)", ms, unit))
			}
			positive = positive || seg.Rate > 0
		}
		if !positive {
			out.add(ReleaseScheduleInvalid, base+"/segments", "at least one segment needs a positive rate")
		}
	}
}

// patterns returns every resource's S(t) by index (D-052): its calendar's,
// or the whole capacity without one. The shape stage passed, so every
// calendar quantizes.
func patterns(ir *model.ModelIR) ([]*calendar.Pattern, []int64, error) {
	byID := map[string]*model.Calendar{}
	for c := range ir.Calendars {
		byID[ir.Calendars[c].ID] = &ir.Calendars[c]
	}
	out, periods := make([]*calendar.Pattern, len(ir.Resources)), make([]int64, len(ir.Resources))
	for i, r := range ir.Resources {
		cal := byID[r.CalendarID]
		if cal == nil {
			out[i] = calendar.Always(r.Capacity)
			continue
		}
		p, err := calendar.FromModel(*cal, model.TimeUnit(ir.TimeUnit), r.Capacity, r.ShiftCapacity)
		if err != nil {
			return nil, nil, err
		}
		out[i], periods[i] = p, p.Period()
	}
	return out, periods, nil
}

// calendarFeasibility is D-050 R2 and R4 (the bundle feasibility stage),
// on the periodic part of the calendars only (the exceptions are B2's
// X_end): the least common multiple L of all the calendars' periods is at
// most 365 days; every bundle has a tick in its own period (a divisor of
// L) at which each of its resources has the units it needs on shift (R2);
// in a bundle R2 and R3 accept, while a resource that breaks down is on
// shift (S >= 1) every other resource has the units the bundle needs on
// shift (R4). r3 holds the bundles R3 refused.
func calendarFeasibility(ir *model.ModelIR, r3 map[int]bool, out *issues) {
	if len(ir.Calendars) == 0 {
		return
	}
	unit := model.TimeUnit(ir.TimeUnit)
	lcm := int64(1)
	for _, c := range ir.Calendars {
		p, _ := model.Ticks(c.Period, unit)
		var ok bool
		if lcm, ok = calendar.LCM(lcm, p, model.MaxDurationTicks); !ok {
			out.add(CalendarInvalid, "/calendars", "the least common multiple of the calendars' periods is over 365 days (D-050 R2)")
			return
		}
	}
	pats, periods, err := patterns(ir)
	if err != nil {
		out.add(CalendarInvalid, "/calendars", "a calendar does not quantize to 1 ms ticks")
		return
	}
	res := map[string]int{}
	for i, r := range ir.Resources {
		res[r.ID] = i
	}
	for i, n := range ir.Nodes {
		path := Pointer("nodes", idx(i), "requires")
		var bundle []calendar.Need
		period, calendars := int64(1), false
		for _, q := range n.Requires {
			r := res[q.ResourceID]
			bundle = append(bundle, calendar.Need{Pattern: pats[r], Units: q.Units})
			if periods[r] > 0 {
				calendars = true
				period, _ = calendar.LCM(period, periods[r], model.MaxDurationTicks) // divides lcm
			}
		}
		if !calendars {
			continue // every resource is on shift with its whole capacity at every tick
		}
		if _, fit := calendar.NextFit(bundle, 0, period-1, false); !fit {
			out.add(CalendarInvalid, path, "the resources this process needs are never on shift together with the units it needs (D-050 R2)")
			continue
		}
		if r3[i] {
			continue
		}
		reqs := slices.Clone(n.Requires)
		slices.SortFunc(reqs, func(a, b model.ResourceRequirement) int { return strings.Compare(a.ResourceID, b.ResourceID) })
		for _, q := range reqs {
			r := res[q.ResourceID]
			if ir.Resources[r].Breakdowns == nil {
				continue
			}
			var short []string
			for _, o := range reqs {
				if o.ResourceID == q.ResourceID {
					continue
				}
				p, partner := pats[r], pats[res[o.ResourceID]]
				span, _ := calendar.LCM(p.Period(), partner.Period(), model.MaxDurationTicks)
				if _, bad := calendar.Leapfrog(0, span-1,
					func(t int64) (int64, bool) { return p.NextAtLeast(t, 1, false) },
					func(t int64) (int64, bool) { return partner.NextBelow(t, o.Units, false) }); bad {
					short = append(short, strconv.Quote(o.ResourceID))
				}
			}
			if len(short) > 0 {
				who := strings.Join(short, ", ")
				out.add(StallRisk, path, fmt.Sprintf("%q breaks down and is on shift at times when %s has fewer units on shift than this process needs (D-050 R4); give %q a calendar within the shifts of %s, such as the operator's calendar",
					q.ResourceID, who, q.ResourceID, who))
			}
		}
	}
}
