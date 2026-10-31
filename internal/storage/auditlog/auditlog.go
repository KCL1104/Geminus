// Package auditlog appends tenant-scoped audit events (REQ-018) inside the
// transaction of the action they record, so an action and its event commit
// or roll back together. Events are append-only: the API and worker roles
// have no UPDATE or DELETE on twin.audit_events, and a trigger requires a
// user event to name the scoped user (migrations/0002_rls.up.sql).
//
// It is the one place that writes twin.audit_events (T-035, D-091): every
// event is checked against internal/audit's catalogue before it is sent,
// so an action, target or metadata the catalogue does not list (a name, a
// comment, raw customer content, a prompt, a secret, chain-of-thought)
// fails the append, and with it the caller's transaction and the write it
// records.
package auditlog

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
)

// Event is one action by the scoped user.
type Event struct {
	Action     string // e.g. "project.create"
	TargetType string // e.g. "project"
	TargetID   string // the target's public ID
	RequestID  string
	Metadata   map[string]any // IDs and counts only
}

// MarkAI returns meta with the AI's mark when investigation is the AI
// investigation the transaction acts for (tenant.Membership.ActingFor;
// H-061 §6.2 step 7, §10): its ID and by_ai true, always together. A
// person's own event ("") is returned as it is, byte for byte. Inside an
// acting-for transaction the database refuses an unmarked event, and
// outside one a marked event (0019); the catalogue holds the mark to the
// actions the investigation's tools write.
func MarkAI(meta map[string]any, investigation string) map[string]any {
	if investigation == "" {
		return meta
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["investigation_id"], meta["by_ai"] = investigation, true
	return meta
}

// AppendUser records e as an action of the user tx is scoped to (its
// app.user_id), in organization org.
func AppendUser(ctx context.Context, tx pgx.Tx, org, user string, e Event) error {
	return appendEvent(ctx, tx, org, audit.User, user, e)
}

// AppendWorker records e as an action of the worker named worker (an
// actor reference, audit.ValidActor: a letter, then letters, digits, '_',
// '.', ':' or '-', at most 128 characters; D-092), in organization org:
// what a job did, under the request that queued it (T-023). The database
// lets only the worker role write worker events (0004).
func AppendWorker(ctx context.Context, tx pgx.Tx, org, worker string, e Event) error {
	return appendEvent(ctx, tx, org, audit.Worker, worker, e)
}

// AppendSystem records e as an action of the system actor named actor
// (provisioning, D-042), in organization org. The database lets only the
// worker and provisioning roles write system events (0004).
func AppendSystem(ctx context.Context, tx pgx.Tx, org, actor string, e Event) error {
	return appendEvent(ctx, tx, org, audit.System, actor, e)
}

func appendEvent(ctx context.Context, tx pgx.Tx, org string, actorType audit.ActorType, actor string, e Event) error {
	if err := audit.Check(audit.Event{ActorType: actorType, ActorID: actor, Action: e.Action, TargetType: e.TargetType,
		TargetID: e.TargetID, RequestID: e.RequestID, Metadata: e.Metadata}); err != nil {
		return err
	}
	data, err := json.Marshal(e.Metadata, json.Deterministic(true)) // a nil map is {} in json/v2
	if err != nil {
		return fmt.Errorf("audit: metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO twin.audit_events(id,organization_id,actor_type,actor_id,action,target_type,target_id,request_id,metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, ids.New("aud"), org, string(actorType), actor, e.Action, e.TargetType, e.TargetID, e.RequestID, string(data))
	return err
}
