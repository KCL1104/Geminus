package investigator

import (
	"context"
	"encoding/json/v2"
	"time"

	"github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	inv "github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/storage/scenarios"
	"github.com/kcl1104/geminus/internal/tenant"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Ledger is every database access of one attempt (H-061 §5.5): each method
// is one short transaction under the job's lease (a handler.Run
// Checkpoint, or its end), run only from the handler's goroutine, never
// from Eino's. The attempt's loop is written against it; dbLedger is the
// database's, and the tests hold an in-memory one to the same rules.
type Ledger interface {
	// Load reads what an attempt starts from (H-061 §5.2): the
	// investigation, its ledger, the database's now, its planner turns so
	// far, its base version and, from an analysis (entry B), that
	// analysis's suggested experiments.
	Load(ctx context.Context) (State, error)
	// Poll reads the investigation's state and the database's now, and the
	// call seq when seq > 0.
	Poll(ctx context.Context, seq int) (Polled, error)
	// Children reads what the resolved call made.
	Children(ctx context.Context, c inv.Call) (Children, error)
	// Insert records the call (pending; or, for invalid output, refused
	// with its error, and then its observation in the same transaction).
	Insert(ctx context.Context, c inv.NewCall, observation []byte) error
	// Observe records what the planner is shown of the resolved call seq.
	Observe(ctx context.Context, seq int, observation []byte) error
	// Finish ends the attempt's job as completed with the investigation's
	// end (H-061 §5.8): the finish call when the planner chose finish, the
	// investigation's end unless it has ended already, and the worker's
	// audit event investigation.end, in one transaction.
	Finish(ctx context.Context, e Ending) error
}

// State is what an attempt starts from.
type State struct {
	Investigation inv.Investigation
	Calls         []inv.Call
	Now           time.Time
	PlannerTurns  int
	Version       models.Version
	// Suggestions are the source analysis's suggested experiments (entry
	// B), in its order; nil otherwise.
	Suggestions []Suggestion
}

// Suggestion is one suggested experiment of an analysis (AI text).
type Suggestion struct {
	Name, Hypothesis string
}

// Polled is what a poll reads.
type Polled struct {
	Status inv.Status
	Reason *inv.Reason
	Now    time.Time
	Call   *inv.Call
}

// Ending is how an attempt ends its investigation.
type Ending struct {
	// Reason is the end's (the investigation's own when it has ended
	// already: Already).
	Reason inv.Reason
	// Already: the investigation has ended (the API's stop, role_lost or
	// time_limit); only the audit event and the job's end are written.
	Already bool
	// Finish is the planner's finish call (seq, canonical arguments, note)
	// when it chose finish; Outcome its claimed outcome.
	Finish  *inv.NewCall
	Outcome *string
	// Steps and PlannerTurns are the investigation.end event's counts.
	Steps, PlannerTurns int
}

// dbLedger is the database's Ledger over one attempt's handler.Run.
type dbLedger struct {
	run       handler.Run
	ender     handler.Ender
	org, id   string
	requestID string
	actor     string
}

func (l *dbLedger) checkpoint(ctx context.Context, fn func(ctx context.Context, tx tenant.Tx) error) error {
	return l.run.Checkpoint(ctx, fn)
}

func (l *dbLedger) Load(ctx context.Context) (State, error) {
	var st State
	err := l.checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		if st.Investigation, st.Calls, st.Now, err = inv.Snapshot(ctx, tx, l.org, l.id); err != nil {
			return err
		}
		if st.PlannerTurns, err = inv.PlannerTurns(ctx, tx, l.org, l.id); err != nil {
			return err
		}
		if st.Version, err = models.GetVersion(ctx, tx, l.org, st.Investigation.BaseVersionID); err != nil {
			return err
		}
		if src := st.Investigation.SourceAnalysisID; src != nil {
			a, err := analyses.Get(ctx, tx, l.org, *src)
			if err != nil {
				return err
			}
			st.Suggestions = suggestionsOf(a)
		}
		return nil
	})
	return st, err
}

// suggestionsOf is a completed analysis's suggested experiments.
func suggestionsOf(a analyses.Analysis) []Suggestion {
	var o struct {
		Experiments []struct {
			Name       string `json:"name"`
			Hypothesis string `json:"hypothesis"`
		} `json:"suggested_next_experiments"`
	}
	if a.Status != analyses.Completed || json.Unmarshal(a.Analysis, &o) != nil {
		return nil
	}
	out := make([]Suggestion, 0, len(o.Experiments))
	for _, e := range o.Experiments {
		out = append(out, Suggestion{Name: e.Name, Hypothesis: e.Hypothesis})
	}
	return out
}

func (l *dbLedger) Poll(ctx context.Context, seq int) (Polled, error) {
	var p Polled
	err := l.checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		i, err := inv.Get(ctx, tx, l.org, l.id)
		if err != nil {
			return err
		}
		p.Status, p.Reason = i.Status, i.Reason
		if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&p.Now); err != nil {
			return err
		}
		if seq > 0 {
			calls, err := inv.Calls(ctx, tx, l.org, l.id)
			if err != nil {
				return err
			}
			for _, c := range calls {
				if c.Seq == seq {
					p.Call = &c
				}
			}
		}
		return nil
	})
	return p, err
}

func (l *dbLedger) Children(ctx context.Context, c inv.Call) (Children, error) {
	var ch Children
	err := l.checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		var err error
		ch, err = childrenOf(ctx, c, txRows{tx: tx, org: l.org})
		return err
	})
	return ch, err
}

// txRows reads a done call's rows in the attempt's Checkpoint, each scoped
// to the job's organization (the read and RLS).
type txRows struct {
	tx  tenant.Tx
	org string
}

func (r txRows) proposal(ctx context.Context, id string) (proposals.Proposal, error) {
	return proposals.Get(ctx, r.tx, r.org, id)
}

func (r txRows) scenario(ctx context.Context, id string) (scenarios.Scenario, error) {
	return scenarios.Get(ctx, r.tx, r.org, id)
}

func (r txRows) analysis(ctx context.Context, id string) (analyses.Analysis, error) {
	return analyses.Get(ctx, r.tx, r.org, id)
}

// run is a run and, once completed, its stored result.
func (r txRows) run(ctx context.Context, id string) (*runs.Run, []byte, error) {
	run, err := runs.Get(ctx, r.tx, r.org, id)
	if err != nil {
		return nil, nil, err
	}
	if run.Status != runs.Completed {
		return &run, nil, nil
	}
	res, err := runs.GetResult(ctx, r.tx, r.org, id)
	if err != nil {
		return nil, nil, err
	}
	return &run, res.Result, nil
}

func (l *dbLedger) Insert(ctx context.Context, c inv.NewCall, observation []byte) error {
	job := l.run.Job()
	c.OrganizationID, c.InvestigationID, c.JobID, c.LeaseToken = l.org, l.id, job.ID, job.LeaseToken
	return l.checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := inv.InsertCall(ctx, tx, c); err != nil {
			return err
		}
		if observation == nil {
			return nil
		}
		return inv.Observe(ctx, tx, l.org, l.id, c.Seq, observation, hashOf(observation))
	})
}

func (l *dbLedger) Observe(ctx context.Context, seq int, observation []byte) error {
	return l.checkpoint(ctx, func(ctx context.Context, tx tenant.Tx) error {
		return inv.Observe(ctx, tx, l.org, l.id, seq, observation, hashOf(observation))
	})
}

func (l *dbLedger) Finish(ctx context.Context, e Ending) error {
	job := l.run.Job()
	before := func(ctx context.Context, tx tenant.Tx) error {
		if e.Finish == nil {
			return nil
		}
		c := *e.Finish
		c.OrganizationID, c.InvestigationID, c.JobID, c.LeaseToken = l.org, l.id, job.ID, job.LeaseToken
		_, err := inv.InsertCall(ctx, tx, c)
		return err
	}
	after := func(ctx context.Context, tx tenant.Tx) error {
		if !e.Already {
			if err := inv.End(ctx, tx, l.org, l.id, e.Reason, e.Outcome); err != nil {
				return err
			}
		}
		return auditlog.AppendWorker(ctx, tx, l.org, l.actor, auditlog.Event{Action: "investigation.end", TargetType: "investigation",
			TargetID: l.id, RequestID: l.requestID, Metadata: map[string]any{"reason": string(e.Reason), "steps": e.Steps,
				"planner_turns": e.PlannerTurns, "attempt": job.Attempt}})
	}
	return l.ender.FinishThen(ctx, jobs.Completed, before, after)
}
