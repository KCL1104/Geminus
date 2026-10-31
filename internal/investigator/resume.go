package investigator

import (
	"context"
	"errors"
	"fmt"

	"github.com/kcl1104/geminus/internal/ai/aicontext"
	reg "github.com/kcl1104/geminus/internal/ai/investigator"
	agent "github.com/kcl1104/geminus/internal/aigraph/investigator"
	"github.com/kcl1104/geminus/internal/model/hashing"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
)

// Resume (H-061 §5.7): an attempt starts from the stored ledger. Every
// settled call (resolved and observed) is in the transcript as stored, so
// what the planner is shown again is what it saw; the last call, if an
// earlier attempt left it pending or unobserved, is settled first (waited
// for, its observation written), never sent again.

// mirror is the ledger's settled calls as the transcript reads them.
func mirror(calls []inv.Call) ([]reg.LedgerStep, error) {
	out := make([]reg.LedgerStep, 0, len(calls))
	for _, c := range calls {
		if c.Status == inv.Pending || c.ObservedAt == nil {
			return nil, fmt.Errorf("investigator: ledger call %d is not settled", c.Seq)
		}
		out = append(out, stepOf(c))
	}
	return out, nil
}

// stepOf is a stored call as the transcript reads it: its arguments and
// observation in RFC 8785 form again (jsonb prints them with its own
// spacing and member order; the values are what the worker wrote, so the
// canonical form is too).
func stepOf(c inv.Call) reg.LedgerStep {
	s := reg.LedgerStep{Seq: c.Seq, Tool: c.Tool, Arguments: canonical(c.Arguments), Status: reg.StepStatus(c.Status),
		Observation: canonical(c.Observation)}
	if c.Error != nil {
		s.Error = &reg.ToolError{Code: reg.Code(c.Error.Code), Path: c.Error.Path}
	}
	return s
}

// BriefSteps is the ledger mirror as the brief's digest takes it
// (aicontext does not link the registry).
func BriefSteps(ledger []reg.LedgerStep) []aicontext.InvestigationStep {
	out := make([]aicontext.InvestigationStep, 0, len(ledger))
	for _, s := range ledger {
		d := aicontext.InvestigationStep{Seq: s.Seq, Tool: s.Tool, Arguments: s.Arguments, Status: string(s.Status), Observation: s.Observation}
		if s.Error != nil {
			d.ErrorCode, d.ErrorPath = string(s.Error.Code), s.Error.Path
		}
		out = append(out, d)
	}
	return out
}

// settle waits until the call seq is carried out, refused or failed (polls
// every Poll), then for what a done call made (every ChildPoll), writes its
// observation once and returns the call as stored with it. It ends with an
// *agent.End when the investigation ends first (its own reason), or when
// its deadline passes with the call still pending or waiting (time_limit).
func (s *session) settle(ctx context.Context, seq int) (inv.Call, error) {
	for {
		p, err := s.l.Poll(ctx, seq)
		if err != nil {
			return inv.Call{}, err
		}
		if p.Status != inv.Running {
			return inv.Call{}, s.stopped(p.Reason)
		}
		if p.Call == nil {
			return inv.Call{}, fmt.Errorf("investigator: ledger call %d is gone", seq)
		}
		c := *p.Call
		if c.ObservedAt != nil {
			return c, nil
		}
		late := !p.Now.Before(s.inv.DeadlineAt)
		var observation []byte
		switch {
		case c.Status == inv.Pending:
			if late {
				return inv.Call{}, &agent.End{Reason: string(inv.TimeLimit)}
			}
			if err := s.h.cfg.Sleep(ctx, s.h.cfg.Poll); err != nil {
				return inv.Call{}, err
			}
			continue
		case c.Status != inv.Done:
			observation, err = s.errorObservation(c)
			if err != nil {
				return inv.Call{}, err
			}
		default:
			ch, err := s.l.Children(ctx, c)
			if err != nil {
				return inv.Call{}, err
			}
			if !settled(reg.Tool(c.Tool), ch) {
				if late {
					return inv.Call{}, &agent.End{Reason: string(inv.TimeLimit)}
				}
				if err := s.h.cfg.Sleep(ctx, s.h.cfg.ChildPoll); err != nil {
					return inv.Call{}, err
				}
				continue
			}
			if observation, err = observe(reg.Tool(c.Tool), ch, s.indexWith(c), s.objective(), s.budgetWith(c)); err != nil {
				return inv.Call{}, err
			}
		}
		if err := s.l.Observe(ctx, seq, observation); err != nil {
			if errors.Is(err, inv.ErrObserved) {
				continue // observed meanwhile (an earlier attempt still finishing): read it
			}
			return inv.Call{}, err
		}
		c.Observation, c.ObservationHash = observation, ptr(hashOf(observation))
		c.ObservedAt = &p.Now
		return c, nil
	}
}

func ptr[T any](v T) *T { return &v }

// canonical is b in RFC 8785 form (b itself when it is not JSON: the
// transcript then refuses it), nil for none.
func canonical(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	c, err := hashing.Canonical(b)
	if err != nil {
		return b
	}
	return c
}

// stopped is the end of an investigation a poll found ended.
func (s *session) stopped(r *inv.Reason) error {
	reason := inv.AIFailed
	if r != nil {
		reason = *r
	}
	s.ended = &reason
	return &agent.End{Reason: string(reason)}
}

// errorObservation is a refused or failed call's observation: its error
// and the budget after it.
func (s *session) errorObservation(c inv.Call) ([]byte, error) {
	if c.Error == nil {
		return nil, fmt.Errorf("investigator: ledger call %d is %s without its error", c.Seq, c.Status)
	}
	return reg.ErrorObservation(reg.ToolError{Code: reg.Code(c.Error.Code), Path: c.Error.Path}, s.budgetWith(c))
}

// budgetWith is the budget after the call c (counted with the calls before
// it and c as resolved).
func (s *session) budgetWith(c inv.Call) reg.Budget {
	saved := s.calls
	defer func() { s.calls = saved }()
	var with []inv.Call
	for _, x := range saved {
		if x.Seq < c.Seq {
			with = append(with, x)
		}
	}
	s.calls = append(with, c)
	return s.budget()
}

// indexWith is the index of the proposal the done call c made.
func (s *session) indexWith(c inv.Call) int {
	saved := s.calls
	defer func() { s.calls = saved }()
	var with []inv.Call
	for _, x := range saved {
		if x.Seq < c.Seq {
			with = append(with, x)
		}
	}
	s.calls = append(with, c)
	return s.index(c.Seq)
}
