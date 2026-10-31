package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Worker lanes (WORKER_LANES, H-056 design §9, W1; WORKER_REST_LANES).
// Single (the default) runs one internal/worker loop for every type the
// build handles, as before. Split runs a sim lane and WORKER_REST_LANES
// rest lanes (1 by default) in this process, on the same organizations and
// the same pool: the sim lane claims only simulation jobs, so a simulation
// never waits behind an AI job, and each rest lane every other type, so one
// long AI job (a Muse Wonder proposal, up to 30 minutes: H-065) leaves the
// other rest lanes to the other jobs. jobs.Claim takes only the types a
// loop has handlers for and skips a job another transaction holds (FOR
// UPDATE SKIP LOCKED), and each lane holds its leases under its own ID
// (<WORKER_ID>:sim, <WORKER_ID>:rest, <WORKER_ID>:rest2, ...), so the
// lease fence (locked_by, lease_token) and the reaper work across lanes as
// across processes (ADR-024): rest lanes claiming the same types are as
// safe as several workers. The handlers are built once and shared: one AI
// client, one spend meter, and the handlers' own audit events keep
// WORKER_ID as their actor. H-061's agent lanes (WORKER_AGENT_LANES, not
// built) would be further lanes of plan with their own types.

// lane is one loop's plan: the ID it holds leases and writes the loop's
// audit events under, and the handlers of the types it claims.
type lane struct {
	id       string
	handlers map[jobs.Type]handler.Handler
}

// plan is the lanes of w over the build's handlers hs: in single mode one,
// under WORKER_ID, with every handler; with split the sim lane, then the
// rest lanes in order, each with its own map of the same handlers.
func plan(w config.Worker, hs map[jobs.Type]handler.Handler) ([]lane, error) {
	if w.Lanes != config.LanesSplit {
		return []lane{{id: w.ID, handlers: hs}}, nil
	}
	if w.RestLanes < 1 || w.RestLanes > config.MaxRestLanes {
		return nil, errors.New("WORKER_REST_LANES must be 1 to 4 with WORKER_LANES=split")
	}
	sim, rest := map[jobs.Type]handler.Handler{}, map[jobs.Type]handler.Handler{}
	for typ, h := range hs {
		if typ == jobs.Simulation {
			sim[typ] = h
		} else {
			rest[typ] = h
		}
	}
	if len(sim) == 0 || len(rest) == 0 {
		return nil, errors.New("WORKER_LANES=split needs a simulation handler and a handler of another type in this build")
	}
	lanes := []lane{{id: w.ID + config.SimLaneSuffix, handlers: sim}}
	for i := 1; i <= w.RestLanes; i++ {
		lanes = append(lanes, lane{id: w.ID + config.RestLaneSuffixOf(i), handlers: maps.Clone(rest)})
	}
	return lanes, nil
}

// checkRestLanes refuses more rest lanes than perOrganization, the AI
// calls one organization may have in flight in this process
// (provider.Budget.MaxConcurrentPerOrganization, of
// AI_MAX_CONCURRENT_PER_ORGANIZATION; 2 by default). Every rest lane shares
// the worker's AI clients (the text client of proposals and analyses, and
// the photo path's judge and segmenter, all under that budget), and a
// client refuses a call beyond the limit with concurrency_limited, which
// ends a Muse Wonder or research proposal (one job attempt). A rest lane
// runs one job at a time and a job makes its calls one after another, so
// with at most that many rest lanes no call of this worker's is refused for
// another of its own. Single mode, and the sim lane, make no concurrent AI
// call.
func checkRestLanes(w config.Worker, perOrganization int) error {
	if w.RestLanes <= perOrganization {
		return nil
	}
	return &config.Error{Var: "WORKER_REST_LANES", Code: config.CodeInvalid, Msg: fmt.Sprintf(
		"at most %d, the AI calls one organization may have in flight in this worker (AI_MAX_CONCURRENT_PER_ORGANIZATION, 2 by default): every rest lane shares the AI clients, and a call beyond that limit is refused (concurrency_limited), which ends a Muse Wonder or research proposal",
		perOrganization)}
}

// loop is a lane's job loop (*worker.Worker): Run returns once ctx ends.
type loop interface{ Run(ctx context.Context) }

// running is a lane's loop under the lane's ID.
type running struct {
	id   string
	loop loop
}

// runLanes runs every lane's loop until ctx ends and returns once each
// has returned, so the process never exits with a lane still recording
// its attempt. A lane whose loop panics (a handler's panic is the loop's
// to recover and record; this is one outside it) or returns while ctx
// has not ended is reported with its lane's ID, and stops the other
// lanes, which end their attempts as at a stop (WORKER_STOPPED): runLanes
// then returns false, and the worker exits non-zero, as a crashed process
// would. One lane never goes on alone unnoticed.
func runLanes(ctx context.Context, log *slog.Logger, lanes []running) bool {
	lctx, stop := context.WithCancel(ctx)
	defer stop()
	var failed atomic.Bool
	var all sync.WaitGroup
	for _, l := range lanes {
		all.Go(func() {
			defer func() {
				if v := recover(); v != nil {
					failed.Store(true)
					stop()
					log.Error("a worker lane panicked; the worker stops", "worker_id", l.id, "code", "WORKER_LANE_PANICKED",
						"panic", panicText(v), "stack", string(debug.Stack()))
				}
			}()
			l.loop.Run(lctx)
			if lctx.Err() == nil {
				failed.Store(true)
				stop()
				log.Error("a worker lane ended while the worker runs; the worker stops", "worker_id", l.id, "code", "WORKER_LANE_ENDED")
			}
		})
	}
	all.Wait()
	return !failed.Load()
}

// panicText is what the log keeps of a lane's panic, as internal/worker
// keeps of a handler's: a runtime error's message, otherwise only the
// value's type (the value may be data the loop was working on).
func panicText(v any) string {
	if re, ok := observability.RuntimeError(v); ok {
		return "lane panicked: " + re.Error()
	}
	return fmt.Sprintf("lane panicked with a %T", v)
}
