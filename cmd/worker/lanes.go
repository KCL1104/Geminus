package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/worker/handler"
)

// Worker lanes (WORKER_LANES, H-056 design §9, W1). Single (the default)
// runs one internal/worker loop for every type the build handles, as
// before. Split runs two loops in this process, on the same organizations
// and the same pool: the sim lane claims only simulation jobs and the rest
// lane every other type, so a simulation never waits behind an AI job of
// 50-90 s. jobs.Claim takes only the types a loop has handlers for, and
// each lane holds its leases under its own ID (<WORKER_ID>:sim,
// <WORKER_ID>:rest), so the lease fence (locked_by, lease_token) and the
// reaper work across lanes as across processes (ADR-024). The handlers are
// built once and shared: one AI client, one spend meter, and the handlers'
// own audit events keep WORKER_ID as their actor.

// lane is one loop's plan: the ID it holds leases and writes the loop's
// audit events under, and the handlers of the types it claims.
type lane struct {
	id       string
	handlers map[jobs.Type]handler.Handler
}

// plan is the lanes of mode over the build's handlers hs.
func plan(mode config.Lanes, id string, hs map[jobs.Type]handler.Handler) ([]lane, error) {
	if mode != config.LanesSplit {
		return []lane{{id: id, handlers: hs}}, nil
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
	return []lane{{id: id + config.SimLaneSuffix, handlers: sim}, {id: id + config.RestLaneSuffix, handlers: rest}}, nil
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
