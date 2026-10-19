package runs

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Route is one implemented operation of openapi.yaml.
type Route struct {
	Method, Path, OperationID string
	Roles                     []tenant.Role // x-required-roles
}

// Routes are the simulation operations this package serves. A contract
// test compares them with planning/api-operations.json; their roles are
// the ones the service requires. compareSimulations is T-029's.
var Routes = []Route{
	{"POST", "/v1/simulations", "createSimulation", writeRoles},
	{"GET", "/v1/models/{id}/simulations", "listSimulations", readRoles},
	{"GET", "/v1/simulations/{id}", "getSimulation", readRoles},
	{"POST", "/v1/simulations/{id}/cancel", "cancelSimulation", writeRoles},
	{"GET", "/v1/simulations/{id}/results", "getSimulationResults", readRoles},
	{"GET", "/v1/simulations/{id}/events", "streamSimulationEvents", readRoles},
	{"GET", "/v1/simulations/{id}/trace", "downloadSimulationTrace", readRoles},
}

// Stream is how the progress stream behaves (zero values: the defaults).
type Stream struct {
	// Poll is how often the stream rereads the run, rechecking the
	// caller's membership and role each time (1 s).
	Poll time.Duration
	// KeepAlive is how long the stream may stay silent before it sends a
	// comment line, so proxies keep it open (15 s).
	KeepAlive time.Duration
	// MaxAge ends a stream; the client reconnects with a fresh token and
	// gets the latest snapshot (10 min).
	MaxAge time.Duration
	// WriteTimeout bounds each write to the client (10 s).
	WriteTimeout time.Duration
}

// idle is how long a stream may go without a write after one: the next
// write (at the latest a keep-alive) starts within KeepAlive + Poll and
// then has WriteTimeout. The write deadline must not lapse in between: an
// HTTP/2 stream is reset when it does, even with no write pending (T-023
// review I1).
func (c Stream) idle() time.Duration { return c.KeepAlive + c.Poll + c.WriteTimeout }

func (c Stream) withDefaults() Stream {
	if c.Poll <= 0 {
		c.Poll = time.Second
	}
	if c.KeepAlive <= 0 {
		c.KeepAlive = 15 * time.Second
	}
	if c.MaxAge <= 0 {
		c.MaxAge = 10 * time.Minute
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	return c
}

// Register adds the simulation routes to mux.
func Register(mux *http.ServeMux, log *slog.Logger, v auth.Verifier, users httpapi.Resolver, s *Service, stream Stream) {
	h := handlers{s: s, log: log, stream: stream.withDefaults()}
	for _, r := range Routes {
		var f func(http.ResponseWriter, *http.Request, tenant.Scope) error
		switch r.OperationID {
		case "createSimulation":
			f = h.create
		case "listSimulations":
			f = h.list
		case "getSimulation":
			f = h.get
		case "cancelSimulation":
			f = h.cancel
		case "getSimulationResults":
			f = h.results
		case "streamSimulationEvents":
			f = h.events
		case "downloadSimulationTrace":
			f = h.trace
		}
		mux.Handle(r.Method+" "+r.Path, httpapi.Handler(log, httpapi.Scoped(v, users, f)))
	}
}

type handlers struct {
	s      *Service
	log    *slog.Logger
	stream Stream
}

var modelIDs = regexp.MustCompile(`^mdl_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// pathID is the {id} of the request if it is an ID of kind, else 400.
func pathID(r *http.Request, ids *regexp.Regexp, kind string) (string, error) {
	id := r.PathValue("id")
	if !ids.MatchString(id) {
		return "", &httpapi.Error{Code: httpapi.InvalidRequest, Message: "id must be a " + kind + " ID",
			Details: []httpapi.Detail{{Path: "", Reason: "id"}}}
	}
	return id, nil
}

// maxRequestBytes bounds a run request: a scenario patch may be as large as
// the IR it edits (D-029), plus the rest of the request.
const maxRequestBytes = validate.MaxIRBytes + 64<<10

func details(issues []model.ValidationIssue) []httpapi.Detail {
	out := make([]httpapi.Detail, len(issues))
	for i, is := range issues {
		out[i] = httpapi.Detail{Path: is.Path, Reason: is.Message, Code: is.Code, Severity: string(is.Severity)}
	}
	return out
}

// Refused maps the service's errors to what the client sees (openapi error
// codes); tenant and transport errors pass to httpapi.
func Refused(err error) error {
	var re *RequestError
	var ie *InvalidError
	switch {
	case errors.As(err, &re):
		return &httpapi.Error{Code: httpapi.RequestValidationFailed, Message: "the run request is not acceptable", Details: details(re.Issues)}
	case errors.As(err, &ie):
		return &httpapi.Error{Code: httpapi.ModelValidationFailed, Message: "the run's effective model is not valid", Details: details(ie.Issues)}
	case errors.Is(err, ErrNotFound):
		return httpapi.Errorf(httpapi.NotFound, "not found")
	case errors.Is(err, ErrNotReady):
		return httpapi.Errorf(httpapi.ResultNotReady, "the run has no result: it is not completed")
	case errors.Is(err, ErrFinished):
		return httpapi.Errorf(httpapi.LifecycleConflict, "the run has finished and cannot be cancelled")
	case errors.Is(err, ErrNoTrace):
		return httpapi.Errorf(httpapi.NotFound, "the run has no sampled trace")
	case errors.Is(err, ErrStorageUnavailable):
		return httpapi.Errorf(httpapi.ServiceUnavailable, "object storage is not configured for this deployment")
	case errors.Is(err, ErrInvalidCursor):
		return httpapi.InvalidCursor()
	case errors.Is(err, ErrInvalidLimit):
		return &httpapi.Error{Code: httpapi.InvalidRequest, Message: "limit must be one integer from 1 to 100",
			Details: []httpapi.Detail{{Path: "", Reason: "limit"}}}
	case errors.Is(err, idempotency.ErrConflict), errors.Is(err, idempotency.ErrInProgress):
		return httpapi.Errorf(httpapi.IdempotencyKeyConflict, "this Idempotency-Key was used for a different request")
	}
	return err // ErrTraceCorrupt and the rest: INTERNAL_ERROR, logged
}

func (h handlers) create(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req CreateRequest
	body, err := httpapi.DecodeJSON(r, maxRequestBytes, "simulation.schema.json#/$defs/RunRequest", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Create(r.Context(), sc, httpapi.RequestID(r.Context()), key, body, req)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) list(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	modelID, err := pathID(r, modelIDs, "model")
	if err != nil {
		return err
	}
	cursor, limit, err := httpapi.PageQuery(r)
	if err != nil {
		return err
	}
	page, err := h.s.List(r.Context(), sc, httpapi.RequestID(r.Context()), modelID, cursor, limit)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, page)
	return nil
}

func (h handlers) get(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	run, err := h.s.Get(r.Context(), sc, httpapi.RequestID(r.Context()), id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteJSON(w, 200, run)
	return nil
}

func (h handlers) cancel(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	key, err := httpapi.IdempotencyKey(r)
	if err != nil {
		return err
	}
	var req struct{}
	body, err := httpapi.DecodeJSON(r, httpapi.MaxBodyBytes, "http.schema.json#/$defs/CancelSimulation", &req)
	if err != nil {
		return err
	}
	status, out, err := h.s.Cancel(r.Context(), sc, httpapi.RequestID(r.Context()), key, id, body)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, status, out)
	return nil
}

func (h handlers) results(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	out, err := h.s.Results(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	httpapi.WriteBody(w, 200, out)
	return nil
}

// trace serves the sampled trace artifact itself, after the service has
// authorized the caller and checked the stored bytes (D-044: no signed
// URL), as an attachment.
func (h handlers) trace(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	data, err := h.s.Trace(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-trace.json"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	httpapi.WriteBody(w, 200, data)
	return nil
}

// lastEventID is the revision a reconnecting client names (Last-Event-ID,
// "<run ID>:<revision>"), which must be of this run; -1 when absent.
func lastEventID(r *http.Request, id string) (int64, error) {
	values := r.Header.Values("Last-Event-ID")
	if len(values) == 0 {
		return -1, nil
	}
	bad := &httpapi.Error{Code: httpapi.InvalidRequest, Message: "Last-Event-ID must be an event ID of this run's stream (<run ID>:<revision>)",
		Details: []httpapi.Detail{{Path: "", Reason: "Last-Event-ID"}}}
	run, rev, ok := strings.Cut(values[0], ":")
	n, err := strconv.ParseInt(rev, 10, 64)
	if len(values) > 1 || !ok || run != id || err != nil || n < 0 || strconv.FormatInt(n, 10) != rev {
		return 0, bad
	}
	return n, nil
}

// events streams the run's progress snapshots (SSE; openapi
// streamSimulationEvents, API_DATABASE §6, TEST-026). The token comes in
// the Authorization header, never the URL. The first snapshot is always
// the latest (a reconnect gets no history). Before every event the
// service rereads the run in a transaction that checks the caller's
// membership and role again, so a member who was removed, or whose role
// may no longer read runs, gets nothing more: the stream ends. It also
// ends when the token expires, when the run is final, after MaxAge, and
// when the client goes away; the client then reconnects, and a refusal
// comes as an ordinary error response.
func (h handlers) events(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, runIDs, "simulation run")
	if err != nil {
		return err
	}
	if _, err := lastEventID(r, id); err != nil {
		return err
	}
	ctx := r.Context()
	snap, err := h.s.Snapshot(ctx, sc, id)
	if err != nil {
		return Refused(err)
	}
	identity, ok := auth.IdentityFrom(ctx)
	if !ok || identity.Expiry.IsZero() {
		return errors.New("runs: the stream has no verified token expiry")
	}
	end := time.Now().Add(h.stream.MaxAge)
	if identity.Expiry.Before(end) {
		end = identity.Expiry
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// From here on the response has begun: problems end the stream, they
	// are never written as an error envelope.
	// Each write gets WriteTimeout; after it, the deadline covers the
	// silence until the next one (idle).
	write := func(text string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(h.stream.WriteTimeout))
		if _, err := w.Write([]byte(text)); err != nil {
			return false
		}
		if rc.Flush() != nil {
			return false
		}
		_ = rc.SetWriteDeadline(time.Now().Add(h.stream.idle()))
		return true
	}
	send := func(s Snapshot) bool {
		data, err := json.Marshal(s)
		if err != nil {
			h.log.ErrorContext(ctx, "encoding a progress snapshot", "request_id", httpapi.RequestID(ctx), "error", err.Error())
			return false
		}
		return write(fmt.Sprintf("id: %s:%d\nevent: progress\ndata: %s\n\n", s.RunID, s.Revision, data))
	}
	if !send(snap) || snap.Final() {
		return nil
	}
	last := snap.Revision
	poll := time.NewTicker(h.stream.Poll)
	defer poll.Stop()
	expire := time.NewTimer(time.Until(end))
	defer expire.Stop()
	silent := time.Now()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-expire.C:
			return nil
		case <-poll.C:
		}
		if !time.Now().Before(end) {
			return nil
		}
		snap, err := h.s.Snapshot(ctx, sc, id)
		if err != nil {
			if !errors.Is(err, tenant.ErrNoMembership) && !errors.Is(err, tenant.ErrForbidden) && !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
				h.log.WarnContext(ctx, "the progress stream could not reread the run", "request_id", httpapi.RequestID(ctx), "error", err.Error())
			}
			return nil
		}
		switch {
		case snap.Revision != last:
			if !send(snap) {
				return nil
			}
			last, silent = snap.Revision, time.Now()
		case time.Since(silent) >= h.stream.KeepAlive:
			if !write(": keep-alive\n\n") {
				return nil
			}
			silent = time.Now()
		}
		if snap.Final() {
			return nil
		}
	}
}
