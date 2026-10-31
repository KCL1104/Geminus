package investigator

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"slices"
	"sync"
	"time"

	reg "github.com/kcl1104/geminus/internal/ai/investigator"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
)

// memLedger is an in-memory Ledger held to migration 0019's rules for the
// worker (the insert trigger's strict sequence, the settled earlier calls,
// the running investigation before its deadline, the mode's tools, the
// observation once on a resolved call, the end once), with the API's
// executor played by resolve: each pending call is carried out at the
// first poll after `after` polls of it.
type memLedger struct {
	mu    sync.Mutex
	st    State
	now   time.Time
	calls []inv.Call
	// resolve carries a pending call out: its result and what it made
	// (nil leaves it pending).
	resolve func(c inv.Call) *resolution
	// children is what each done call made, by seq; ready reports whether
	// it is settled at the n-th read.
	children map[int]Children
	reads    map[int]int
	// grow makes a call's children ready only after this many reads.
	grow int
	// stopAt ends the investigation (stopped_by_person) at that poll,
	// counted once a call is recorded (or from the first poll with
	// stopWhilePlanning).
	stopAt, polls     int
	stopWhilePlanning bool
	inserts           []inv.NewCall
	observed          []int
	ending            *Ending
	finishErr         error
	// clockStep advances the database's now at every poll.
	clockStep time.Duration
	// elsewhere is an observation another attempt writes first, by seq.
	elsewhere map[int][]byte
}

type resolution struct {
	status   inv.CallStatus
	effect   inv.Effect
	err      *inv.ToolError
	children Children
}

var errRefused = errors.New("mem: refused by the ledger's rules")

func (m *memLedger) Load(context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	st.Calls = slices.Clone(m.calls)
	st.Now = m.now
	return st, nil
}

func (m *memLedger) Poll(_ context.Context, seq int) (Polled, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) > 0 || m.stopWhilePlanning {
		m.polls++
	}
	m.now = m.now.Add(m.clockStep)
	if m.stopAt > 0 && m.polls >= m.stopAt && m.st.Investigation.Status == inv.Running {
		r := inv.StoppedByPerson
		m.st.Investigation.Status, m.st.Investigation.Reason = inv.Stopped, &r
	}
	i := m.st.Investigation
	p := Polled{Status: i.Status, Reason: i.Reason, Now: m.now}
	if seq > 0 && seq <= len(m.calls) {
		c := &m.calls[seq-1]
		if c.Status == inv.Pending && m.resolve != nil && i.Status == inv.Running {
			if r := m.resolve(*c); r != nil {
				c.Status, c.Effect, c.Error = r.status, r.effect, r.err
				t := m.now
				c.ResolvedAt = &t
				if m.children == nil {
					m.children = map[int]Children{}
				}
				m.children[seq] = r.children
			}
		}
		cc := *c
		p.Call = &cc
	}
	return p, nil
}

func (m *memLedger) Children(_ context.Context, c inv.Call) (Children, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reads == nil {
		m.reads = map[int]int{}
	}
	m.reads[c.Seq]++
	if m.reads[c.Seq] <= m.grow {
		return Children{}, nil
	}
	return m.children[c.Seq], nil
}

func (m *memLedger) Insert(_ context.Context, c inv.NewCall, observation []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.st.Investigation
	if i.Status != inv.Running || !m.now.Before(i.DeadlineAt) || c.Seq != len(m.calls)+1 {
		return errRefused
	}
	for _, x := range m.calls {
		if x.Status == inv.Pending || x.ObservedAt == nil {
			return errRefused
		}
	}
	want := inv.Pending
	switch {
	case c.Tool == inv.InvalidCall:
		want = inv.Refused
	case !reg.Tool(c.Tool).Offered(reg.Mode(i.Mode)) || reg.Tool(c.Tool) == reg.Finish:
		return errRefused
	}
	if c.Status != want || (c.Tool == inv.InvalidCall) != (c.Error != nil) {
		return errRefused
	}
	m.inserts = append(m.inserts, c)
	call := inv.Call{OrganizationID: i.OrganizationID, InvestigationID: i.ID, Seq: c.Seq, Tool: c.Tool, Arguments: jsonb(c.Arguments),
		ArgumentsHash: c.ArgumentsHash, Note: c.Note, Status: c.Status, Error: c.Error, RequestedAt: m.now}
	if c.Status != inv.Pending {
		t := m.now
		call.ResolvedAt = &t
	}
	if observation != nil {
		t := m.now
		call.Observation, call.ObservedAt = jsonb(observation), &t
		m.observed = append(m.observed, c.Seq)
	}
	m.calls = append(m.calls, call)
	return nil
}

func (m *memLedger) Observe(_ context.Context, seq int, observation []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.elsewhere[seq]; ok && seq <= len(m.calls) && m.calls[seq-1].ObservedAt == nil {
		// An earlier attempt, still finishing, observed it first.
		t := m.now
		m.calls[seq-1].Observation, m.calls[seq-1].ObservedAt = o, &t
		return inv.ErrObserved
	}
	if seq < 1 || seq > len(m.calls) || m.calls[seq-1].Status == inv.Pending || m.calls[seq-1].ObservedAt != nil {
		return inv.ErrObserved
	}
	t := m.now
	m.calls[seq-1].Observation, m.calls[seq-1].ObservedAt = jsonb(observation), &t
	m.observed = append(m.observed, seq)
	return nil
}

func (m *memLedger) Finish(_ context.Context, e Ending) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.finishErr != nil {
		err := m.finishErr
		m.finishErr = nil
		return err
	}
	if m.ending != nil {
		return errRefused // the job ends once
	}
	i := &m.st.Investigation
	if !e.Already {
		if i.Status != inv.Running {
			return errRefused
		}
		i.Status, i.Reason = inv.StatusOf(e.Reason), &e.Reason
	}
	if e.Finish != nil {
		if e.Already || e.Finish.Seq != len(m.calls)+1 {
			return errRefused
		}
		m.calls = append(m.calls, inv.Call{Seq: e.Finish.Seq, Tool: e.Finish.Tool, Arguments: e.Finish.Arguments, Status: inv.Done})
	}
	m.ending = &e
	return nil
}

func (m *memLedger) snapshot() ([]inv.Call, *Ending) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls), m.ending
}

func (m *memLedger) stop(r inv.Reason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st.Investigation.Status, m.st.Investigation.Reason = inv.StatusOf(r), &r
}

// jsonb is b as PostgreSQL's jsonb prints it back: the same values, with a
// space after every colon and comma (and its own member order, which the
// canonical form undoes as well).
func jsonb(b []byte) []byte {
	if b == nil {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, err := json.Marshal(v, jsontext.SpaceAfterColon(true), jsontext.SpaceAfterComma(true))
	if err != nil {
		return b
	}
	return out
}
