package api_test

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/runs"
	"github.com/kcl1104/geminus/internal/runs/runner"
	"github.com/kcl1104/geminus/internal/tenant"
)

type sseEvent struct {
	id, event, data string
	comment         bool
}

type snapshotBody struct {
	RunID                 string `json:"run_id"`
	Status                string `json:"status"`
	Revision              int64  `json:"revision"`
	CompletedReplications int    `json:"completed_replications"`
	CancellationRequested bool   `json:"cancellation_requested"`
}

// stream is an open SSE response: its events arrive on events, which is
// closed when the server ends the stream.
type stream struct {
	status int
	header http.Header
	events chan sseEvent
	cancel context.CancelFunc
}

// openStream requests the run's events as subject with token (a fresh one
// if ""), and reads them until the server ends the stream.
func (e *runEnv) openStream(t *testing.T, subject, org, id, token string, headers map[string]string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/v1/simulations/"+id+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		token = e.token(subject)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Organization-ID", org)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	s := &stream{status: res.StatusCode, header: res.Header, events: make(chan sseEvent, 100), cancel: cancel}
	go func() {
		defer close(s.events)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev != (sseEvent{}) {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, ":"):
				s.events <- sseEvent{comment: true}
			case strings.HasPrefix(line, "id: "):
				ev.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			default:
				s.events <- sseEvent{event: "unexpected line " + line}
			}
		}
	}()
	return s
}

// next is the stream's next progress event (skipping keep-alive comments),
// or ok false when the stream ended; it fails the test after d.
func (s *stream) next(t *testing.T, d time.Duration) (snapshotBody, sseEvent, bool) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				return snapshotBody{}, sseEvent{}, false
			}
			if ev.comment {
				continue
			}
			if ev.event != "progress" {
				t.Fatalf("event %+v", ev)
			}
			issues, err := validate.Document([]byte(ev.data), "http.schema.json#/$defs/ProgressSnapshot", "EVENT")
			if err != nil || len(issues) > 0 {
				t.Fatalf("%s: %v %v", ev.data, issues, err)
			}
			var snap snapshotBody
			if err := json.Unmarshal([]byte(ev.data), &snap); err != nil {
				t.Fatal(err)
			}
			return snap, ev, true
		case <-timeout:
			t.Fatalf("no event within %v", d)
		}
	}
}

// ended waits for the server to end the stream, failing on any further
// progress event.
func (s *stream) ended(t *testing.T, d time.Duration) {
	t.Helper()
	if snap, _, ok := s.next(t, d); ok {
		t.Fatalf("an event after the stream should have ended: %+v", snap)
	}
}

// The stream (TEST-026) authenticates with the header, never the URL; it
// starts with the latest snapshot, follows the run's revisions as the
// worker runs it and ends with the final one; a reconnect gets the latest
// snapshot at once, with Last-Event-ID bound to the run.
func TestStreamFollowsTheRun(t *testing.T) {
	e := setupRuns(t)
	viewer, editor := e.a.Members[tenant.Viewer], e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-0001", runRequest(mv, 3, "none", nil))).ID
	other := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-0002", runRequest(mv, 1, "none", nil))).ID
	s := e.openStream(t, viewer.Subject, e.a.ID, id, "", nil)
	if s.status != 200 || s.header.Get("Content-Type") != "text/event-stream" || s.header.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(s.header.Get("X-Request-ID"), "req_") {
		t.Fatalf("%d %v", s.status, s.header)
	}
	snap, ev, ok := s.next(t, 5*time.Second)
	if !ok || snap.RunID != id || snap.Status != "queued" || snap.Revision != 0 || ev.id != id+":0" {
		t.Fatalf("first event %+v %+v", snap, ev)
	}
	// Silence brings a keep-alive comment, not a repeated snapshot.
	select {
	case ev := <-s.events:
		if !ev.comment {
			t.Errorf("while nothing changed: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Error("no keep-alive while the run waited")
	}
	go drain(t, e.loop(t, runner.Config{}, nil), 30*time.Second)
	last := snap
	for {
		snap, ev, ok := s.next(t, 20*time.Second)
		if !ok {
			break
		}
		if snap.Revision <= last.Revision || ev.id != snap.RunID+":"+itoa(snap.Revision) {
			t.Errorf("event %+v after %+v", snap, last)
		}
		last = snap
	}
	if last.Status != "completed" || last.CompletedReplications != 3 {
		t.Errorf("the stream ended at %+v", last)
	}
	// A reconnect gets the latest snapshot, whatever it last saw, and the
	// stream of a final run ends after it.
	r := e.openStream(t, viewer.Subject, e.a.ID, id, "", map[string]string{"Last-Event-ID": id + ":1"})
	if snap, _, ok := r.next(t, 5*time.Second); !ok || snap.Status != "completed" || snap.Revision != last.Revision {
		t.Errorf("reconnect: %+v", snap)
	}
	r.ended(t, 5*time.Second)
	for _, h := range []string{other + ":1", id, id + ":-1", id + ":01", id + ":x", "x" + id + ":1"} {
		if r := e.do("GET", "/v1/simulations/"+id+"/events", viewer.Subject, e.a.ID, map[string]string{"Last-Event-ID": h}, ""); r.status != 400 || code(t, r) != "INVALID_REQUEST" {
			t.Errorf("Last-Event-ID %q: %d %s", h, r.status, r.body)
		}
	}
	if r := e.do("GET", "/v1/simulations/"+id+"/events?access_token="+e.token(viewer.Subject), "", e.a.ID, nil, ""); r.status != 401 {
		t.Errorf("a token in the URL: %d %s", r.status, r.body)
	}
	if r := e.do("GET", "/v1/simulations/"+id+"/events", "", e.a.ID, nil, ""); r.status != 401 {
		t.Errorf("no token: %d %s", r.status, r.body)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TEST-026, T-010 review L6: before each event the stream checks the
// caller's membership and role again. A member removed meanwhile gets no
// event about a change made after the removal: the stream ends, and a
// reconnect is 404. A member demoted to viewer still reads runs (SECURITY
// §2), so that stream goes on.
func TestStreamEndsWhenTheMemberIsRemoved(t *testing.T) {
	e := setupRuns(t)
	owner, editor, viewer := e.a.Members[tenant.Owner], e.a.Members[tenant.Editor], e.a.Members[tenant.Viewer]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-rev1", runRequest(mv, 1, "none", nil))).ID
	removed := e.openStream(t, viewer.Subject, e.a.ID, id, "", nil)
	demoted := e.openStream(t, editor.Subject, e.a.ID, id, "", nil)
	kept := e.openStream(t, owner.Subject, e.a.ID, id, "", nil)
	for _, s := range []*stream{removed, demoted, kept} {
		if snap, _, ok := s.next(t, 5*time.Second); !ok || snap.Revision != 0 {
			t.Fatalf("first event %+v", snap)
		}
	}
	// The run moves (as an operator would; no worker runs here).
	bump := func(sql string) {
		e.ownerTx(t, e.a.ID, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, id)
			return err
		})
	}
	bump(`UPDATE twin.simulation_runs SET status='running', started_at=now(), execution_deadline_at=now()+interval '5 minutes', progress_revision=1 WHERE id=$1`)
	for _, s := range []*stream{removed, demoted, kept} {
		if snap, _, ok := s.next(t, 5*time.Second); !ok || snap.Revision != 1 || snap.Status != "running" {
			t.Fatalf("second event %+v", snap)
		}
	}
	if _, err := e.admin.Exec(context.Background(), `UPDATE twin.memberships SET role='viewer' WHERE organization_id=$1 AND user_id=$2`, e.a.ID, editor.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.Exec(context.Background(), `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, e.a.ID, viewer.UserID); err != nil {
		t.Fatal(err)
	}
	bump(`UPDATE twin.simulation_runs SET completed_replications=0, events_processed=50, progress_revision=2 WHERE id=$1`)
	removed.ended(t, 5*time.Second)
	for name, s := range map[string]*stream{"demoted": demoted, "kept": kept} {
		if snap, _, ok := s.next(t, 5*time.Second); !ok || snap.Revision != 2 {
			t.Errorf("%s: %+v %v", name, snap, ok)
		}
	}
	if r := e.do("GET", "/v1/simulations/"+id+"/events", viewer.Subject, e.a.ID, nil, ""); r.status != 404 || code(t, r) != "NOT_FOUND" {
		t.Errorf("the removed member reconnects: %d %s", r.status, r.body)
	}
}

// TEST-026: the recheck does not wait for the run to change. A member
// removed while the run stands still loses the stream at the next poll:
// it ends without another event (keep-alive comments are written only
// after a recheck succeeds).
func TestAnIdleStreamEndsWhenTheMemberIsRemoved(t *testing.T) {
	e := setupRuns(t)
	viewer, editor := e.a.Members[tenant.Viewer], e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-idle1", runRequest(mv, 1, "none", nil))).ID
	s := e.openStream(t, viewer.Subject, e.a.ID, id, "", nil)
	if snap, _, ok := s.next(t, 5*time.Second); !ok || snap.Revision != 0 {
		t.Fatalf("first event %+v", snap)
	}
	// The stream stays open while nothing changes: keep-alive comments.
	select {
	case ev, ok := <-s.events:
		if !ok || !ev.comment {
			t.Fatalf("while nothing changed: %+v %v", ev, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no keep-alive while the run waited")
	}
	if _, err := e.admin.Exec(context.Background(), `DELETE FROM twin.memberships WHERE organization_id=$1 AND user_id=$2`, e.a.ID, viewer.UserID); err != nil {
		t.Fatal(err)
	}
	removed := time.Now()
	s.ended(t, 5*time.Second)
	if took := time.Since(removed); took > time.Second {
		t.Errorf("the stream ended %v after the removal (poll 20 ms)", took)
	}
	if n := e.count(`SELECT progress_revision FROM twin.simulation_runs WHERE id=$1`, id); n != 0 {
		t.Errorf("the run changed (revision %d); this test needs it idle", n)
	}
}

// T-023 review I1: a write deadline that lapses while the stream is idle
// resets an HTTP/2 stream (HTTP/1.1 only notices at the next write). The
// deadline set before each write therefore covers the silence that may
// follow it, so a stream whose keep-alive is longer than its write timeout
// stays open over HTTP/2 and keeps sending keep-alive comments.
func TestAnIdleStreamOutlivesItsWriteTimeoutOverHTTP2(t *testing.T) {
	e := setupRuns(t, api.WithStream(runs.Stream{Poll: 20 * time.Millisecond, KeepAlive: 600 * time.Millisecond, WriteTimeout: 250 * time.Millisecond}))
	viewer, editor := e.a.Members[tenant.Viewer], e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-h2-1", runRequest(mv, 1, "none", nil))).ID
	h2 := httptest.NewUnstartedServer(e.srv.Config.Handler)
	h2.EnableHTTP2 = true
	h2.StartTLS()
	t.Cleanup(h2.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", h2.URL+"/v1/simulations/"+id+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token(viewer.Subject))
	req.Header.Set("X-Organization-ID", e.a.ID)
	res, err := h2.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ProtoMajor != 2 {
		t.Fatalf("%d over %s", res.StatusCode, res.Proto)
	}
	lines := make(chan string)
	ended := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		ended <- sc.Err()
	}()
	opened, comments := time.Now(), 0
	for time.Since(opened) < 2*time.Second {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, ":") {
				comments++
			}
		case err := <-ended:
			t.Fatalf("the idle stream ended after %v (write timeout 250 ms, keep-alive 600 ms): %v", time.Since(opened), err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if comments < 2 {
		t.Errorf("%d keep-alive comments in 2 s (keep-alive 600 ms)", comments)
	}
}

// The stream ends when its token expires, whether or not the run is
// final; the client then reconnects with a fresh token (SECURITY §3).
func TestStreamEndsWithItsToken(t *testing.T) {
	e := setupRuns(t)
	viewer, editor := e.a.Members[tenant.Viewer], e.a.Members[tenant.Editor]
	_, mv := e.version(t, e.a, golden(t, "deterministic-queue.json"), nil)
	id := decode[runBody](t, e.createRun(editor.Subject, e.a.ID, "run-sse-tok1", runRequest(mv, 1, "none", nil))).ID
	claims := e.p.Claims(viewer.Subject, time.Now())
	claims["exp"] = time.Now().Add(2 * time.Second).Unix()
	opened := time.Now()
	s := e.openStream(t, viewer.Subject, e.a.ID, id, e.p.Token(t, e.p.RSA, nil, claims), nil)
	if snap, _, ok := s.next(t, 5*time.Second); !ok || snap.Status != "queued" {
		t.Fatalf("first event %+v", snap)
	}
	s.ended(t, 10*time.Second)
	if took := time.Since(opened); took > 4*time.Second {
		t.Errorf("the stream outlived its token by %v", took-2*time.Second)
	}
}
