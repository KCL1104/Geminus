package auditlog_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
)

// tx records the statements sent to it; the rest of pgx.Tx is never
// called (a nil embedded interface would panic).
type tx struct {
	pgx.Tx
	execs [][]any
	fail  error
}

func (t *tx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, append([]any{sql}, args...))
	return pgconn.NewCommandTag("INSERT 0 1"), t.fail
}

// The event is checked before anything is sent: one the catalogue refuses
// never reaches the database, and the caller's transaction (which gets
// the error) rolls back with the write it records (D-091 (c)).
func TestAnEventIsCheckedBeforeItIsWritten(t *testing.T) {
	org, user, req, prj := ids.New("org"), ids.New("usr"), ids.New("req"), ids.New("prj")
	var ok tx
	if err := auditlog.AppendUser(context.Background(), &ok, org, user, auditlog.Event{Action: "project.create", TargetType: "project",
		TargetID: prj, RequestID: req}); err != nil {
		t.Fatal(err)
	}
	if len(ok.execs) != 1 || !strings.Contains(ok.execs[0][0].(string), "INSERT INTO twin.audit_events") {
		t.Fatalf("%v", ok.execs)
	}
	got := ok.execs[0][1:]
	if !strings.HasPrefix(got[0].(string), "aud_") || got[1] != org || got[2] != "user" || got[3] != user || got[4] != "project.create" ||
		got[5] != "project" || got[6] != prj || got[7] != req || got[8] != "{}" {
		t.Errorf("%v", got)
	}
	for name, c := range map[string]struct {
		append func(context.Context, pgx.Tx, string, string, auditlog.Event) error
		actor  string
		e      auditlog.Event
	}{
		"a comment":                    {auditlog.AppendUser, user, auditlog.Event{Action: "proposal.reject", TargetType: "ai_proposal", TargetID: ids.New("aip"), RequestID: req, Metadata: map[string]any{"comment": "SENTINEL"}}},
		"an unknown action":            {auditlog.AppendUser, user, auditlog.Event{Action: "project.delete", TargetType: "project", TargetID: prj, RequestID: req}},
		"a user's event by the worker": {auditlog.AppendWorker, "worker-1", auditlog.Event{Action: "project.create", TargetType: "project", TargetID: prj, RequestID: req}},
		"a worker's event by a user":   {auditlog.AppendUser, user, auditlog.Event{Action: "simulation.complete", TargetType: "simulation", TargetID: ids.New("sim"), RequestID: req}},
		"a worker name that is not an actor": {auditlog.AppendWorker, "1-worker", auditlog.Event{Action: "simulation.cancelled", TargetType: "simulation",
			TargetID: ids.New("sim"), RequestID: req, Metadata: map[string]any{"attempt": 1}}},
		"a system event under another name": {auditlog.AppendSystem, "provision tenant", auditlog.Event{Action: "organization.provision", TargetType: "organization",
			TargetID: org, RequestID: req, Metadata: map[string]any{"owner_user_id": user}}},
	} {
		var refused tx
		err := c.append(context.Background(), &refused, org, c.actor, c.e)
		if !errors.Is(err, audit.ErrInvalid) || len(refused.execs) != 0 {
			t.Errorf("%s: %v, %d statements", name, err, len(refused.execs))
		}
		if err != nil && strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The database's error is the caller's.
	boom := errors.New("connection lost")
	failing := tx{fail: boom}
	if err := auditlog.AppendSystem(context.Background(), &failing, org, "provision-tenant", auditlog.Event{Action: "organization.provision",
		TargetType: "organization", TargetID: org, RequestID: req, Metadata: map[string]any{"owner_user_id": user}}); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	if got := failing.execs[0][1:]; got[2] != "system" || got[3] != "provision-tenant" || got[8] != `{"owner_user_id":"`+user+`"}` {
		t.Errorf("%v", got)
	}
}
