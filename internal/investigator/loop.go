package investigator

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"time"

	reg "github.com/kcl1104/geminus/internal/ai/investigator"
	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/aigraph/einomodel"
	agent "github.com/kcl1104/geminus/internal/aigraph/investigator"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
)

type jsontextValue = jsontext.Value

// The ledger loop (H-061 §5.5): the agent runs in a goroutine the handler
// starts, with recover; its tool bodies send each call over a channel to
// the handler's goroutine, which owns every Checkpoint, and select on their
// context. Between calls the handler polls the investigation every Poll, so
// a stop (or role_lost, or the API's time_limit) cancels a turn in flight
// within one poll.

// run is one ADK run of the agent with steps left.
func (s *session) run(ctx context.Context, steps int) agent.Outcome {
	planner, err := einomodel.NewPlanner(s.h.cfg.Planner)
	if err != nil {
		return agent.Outcome{Kind: agent.Internal, Err: err}
	}
	actx, cancel := context.WithDeadline(ctx, s.clock.until(s.inv.DeadlineAt).Add(-deadlineMargin))
	defer cancel()
	s.reqs = make(chan request)
	s.runTurns = 0
	e := &env{s: s}
	done := make(chan agent.Outcome, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- agent.Outcome{Kind: agent.Internal, Err: fmt.Errorf("investigator: the agent's goroutine panicked with a %T", v)}
			}
		}()
		done <- agent.Run(actx, agent.Config{Planner: planner, Tools: s.tools, StepsLeft: steps, Env: e})
	}()
	tick := time.NewTicker(s.h.cfg.Poll)
	defer tick.Stop()
	for {
		select {
		case o := <-done:
			if o.Kind == agent.Cancelled && s.ended == nil && ctx.Err() == nil && actx.Err() == nil {
				// Cancelled for a reason the handler did not see: not the
				// deadline, not a stop.
				o.Kind = agent.Internal
			}
			return o
		case r := <-s.reqs:
			if r.next {
				pc, err := s.next()
				r.reply <- reply{pc: pc, err: err}
				continue
			}
			obs, err := s.carry(r.ctx, r.res)
			r.reply <- reply{obs: obs, err: err}
		case <-tick.C:
			p, err := s.l.Poll(ctx, 0)
			if err == nil && p.Status != inv.Running {
				_ = s.stopped(p.Reason)
				cancel()
			}
		}
	}
}

// env is the agent's environment over the session: every method is a
// request to the handler's goroutine, which owns the transcript and every
// Checkpoint.
type env struct{ s *session }

// Next is the turn's context, from the transcript, with the budget's
// tools; the investigation's end, the 14 steps or the 20 turns end the
// run.
func (e *env) Next(ctx context.Context) (*provider.PlanContext, error) {
	r, err := e.ask(ctx, request{ctx: ctx, next: true})
	if err != nil {
		return nil, err
	}
	return r.pc, r.err
}

// Carry sends the call to the handler's goroutine and waits for its
// observation.
func (e *env) Carry(ctx context.Context, res provider.PlanResult) (agent.Observed, error) {
	r, err := e.ask(ctx, request{ctx: ctx, res: res})
	if err != nil {
		return agent.Observed{}, err
	}
	return r.obs, r.err
}

func (e *env) ask(ctx context.Context, q request) (reply, error) {
	q.reply = make(chan reply, 1)
	select {
	case e.s.reqs <- q:
	case <-ctx.Done():
		return reply{}, ctx.Err()
	}
	select {
	case r := <-q.reply:
		return r, nil
	case <-ctx.Done():
		return reply{}, ctx.Err()
	}
}

// next is a turn's context (the handler's goroutine).
func (s *session) next() (*provider.PlanContext, error) {
	b := s.budget()
	switch {
	case s.ended != nil:
		return nil, &agent.End{Reason: string(*s.ended)}
	case b.StepsLeft <= 0 || s.turns+s.runTurns >= maxPlannerTurns:
		return nil, &agent.End{Reason: string(inv.LimitReached)}
	}
	s.runTurns++
	pc, err := s.tr.Next(s.inv.OrganizationID, s.attr, s.inv.PlannerModel, b)
	if err != nil {
		return nil, err
	}
	return &pc, nil
}

// carry carries the turn's call out (the handler's goroutine): finish is
// kept for the end; any other call is recorded under the lease, settled
// (resolved by the API's executor, its children waited for, observed) and
// recorded in the transcript.
func (s *session) carry(ctx context.Context, res provider.PlanResult) (agent.Observed, error) {
	if reg.Tool(res.Call.Name) == reg.Finish {
		// Recorded with the end (the agent returns directly after it).
		return agent.Observed{}, nil
	}
	if s.ended != nil {
		return agent.Observed{}, &agent.End{Reason: string(*s.ended)}
	}
	seq := len(s.calls) + 1
	args := []byte(res.Call.Input)
	hash := hashOf(args)
	nc := inv.NewCall{Seq: seq, Tool: res.Call.Name, Arguments: args, ArgumentsHash: &hash, Note: noteOf(res.Note), Status: inv.Pending}
	if err := s.l.Insert(ctx, nc, nil); err != nil {
		return agent.Observed{}, s.refusedInsert(ctx, err)
	}
	s.calls = append(s.calls, inv.Call{OrganizationID: s.inv.OrganizationID, InvestigationID: s.inv.ID, Seq: seq, Tool: nc.Tool,
		Arguments: args, ArgumentsHash: &hash, Note: nc.Note, Status: inv.Pending})
	c, err := s.settle(ctx, seq)
	if err != nil {
		return agent.Observed{}, err
	}
	s.calls[seq-1] = c
	if err := s.tr.Record(res, stepOf(c)); err != nil {
		return agent.Observed{}, err
	}
	s.retries, s.invalidRow = 0, 0
	if c.Status == inv.Done {
		s.refusedRow = 0
	} else {
		s.refusedRow++
	}
	switch {
	case s.refusedRow >= 3:
		return agent.Observed{}, &agent.End{Reason: string(inv.InvalidAnswer)}
	case reg.Tool(c.Tool) == reg.WriteToDraft && c.Status == inv.Done:
		return agent.Observed{}, &agent.End{Reason: string(inv.DraftWritten)}
	}
	return agent.Observed{Content: string(c.Observation), IsError: c.Status != inv.Done}, nil
}

// refusedInsert is why the database refused to record a call: the
// investigation has ended or its deadline has passed (the end), or the
// error itself (a lost lease, a database failure: the worker records it).
func (s *session) refusedInsert(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	p, perr := s.l.Poll(ctx, 0)
	switch {
	case perr != nil:
		return err
	case p.Status != inv.Running:
		return s.stopped(p.Reason)
	case !p.Now.Before(s.inv.DeadlineAt):
		return &agent.End{Reason: string(inv.TimeLimit)}
	}
	return err
}

// invalid records an invalid turn (H-061 §7): a recorded one as a refused
// invalid_call step with its error observation (written with it), then a
// new run from the digest.
func (s *session) invalid(ctx context.Context, it *provider.InvalidTurn) error {
	s.invalidRow++
	if it == nil || !it.Recorded() {
		return s.tr.Invalid(nil)
	}
	seq := len(s.calls) + 1
	e := &inv.ToolError{Code: it.Code, Path: it.Path}
	c := inv.Call{OrganizationID: s.inv.OrganizationID, InvestigationID: s.inv.ID, Seq: seq, Tool: inv.InvalidCall, Status: inv.Refused, Error: e}
	obs, err := reg.ErrorObservation(reg.ToolError{Code: reg.Code(e.Code), Path: e.Path}, s.budgetWith(c))
	if err != nil {
		return err
	}
	if err := s.l.Insert(ctx, inv.NewCall{Seq: seq, Tool: inv.InvalidCall, Status: inv.Refused, Error: e}, obs); err != nil {
		return s.refusedInsert(ctx, err)
	}
	c.Observation, c.ObservationHash = obs, ptr(hashOf(obs))
	now := time.Now()
	c.ObservedAt = &now
	s.calls = append(s.calls, c)
	s.refusedRow++
	step := stepOf(c)
	return s.tr.Invalid(&step)
}
