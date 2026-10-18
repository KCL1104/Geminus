// Package auditlog appends tenant-scoped audit events (REQ-018) inside the
// transaction of the action they record, so an action and its event commit
// or roll back together. Events are append-only: the API and worker roles
// have no UPDATE or DELETE on twin.audit_events, and a trigger requires a
// user event to name the scoped user (migrations/0002_rls.up.sql).
// Metadata never holds raw customer content, prompts, secrets or
// chain-of-thought; callers pass IDs and counts.
package auditlog

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/jackc/pgx/v5"

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

// AppendUser records e as an action of the user tx is scoped to (its
// app.user_id), in organization org.
func AppendUser(ctx context.Context, tx pgx.Tx, org, user string, e Event) error {
	data, err := json.Marshal(e.Metadata, json.Deterministic(true)) // a nil map is {} in json/v2
	if err != nil {
		return fmt.Errorf("audit: metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO twin.audit_events(id,organization_id,actor_type,actor_id,action,target_type,target_id,request_id,metadata)
		VALUES ($1,$2,'user',$3,$4,$5,$6,$7,$8)`, ids.New("aud"), org, user, e.Action, e.TargetType, e.TargetID, e.RequestID, string(data))
	return err
}
