// Package aispend is the persistent spend ledger of live AI (T-053, H-018;
// PROPOSED D-110, D-111; migration 0016): a provider.Meter whose
// reservations and settlements are rows every worker process shares.
//
// Reserve, in one transaction scoped to the call's organization, adds the
// charge to the deployment's month and day (both provider organizations
// combined) and to the organization's day, each by a conditional UPDATE
// that holds only while the total stays within its limit, then opens the
// reservation and records ai_spend.reserve. The rows are locked in one
// order (month, day, organization day), so concurrent reservations
// serialize on them and none takes a limit past its value; a refused
// limit rolls everything back, and the refusal is recorded
// (ai_spend.refuse) in a transaction of its own. Settle locks the
// reservation, moves the same three totals by (settled − reserved), closes
// the reservation with what the call cost and its billed tokens, and
// records ai_spend.settle; a reservation is settled once. A settlement
// that never comes (the worker died) leaves the reservation counted at its
// bound, the safe side.
//
// Every write is audited in its own transaction (D-091); the events hold
// amounts, counts and labels, never the prompt, the answer, a key or a
// deployment-wide total. Metrics (D-095): geminus.ai.spend,
// geminus.ai.spend.refusals.
package aispend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ai/provider"
	"github.com/kcl1104/geminus/internal/ai/spend"
	"github.com/kcl1104/geminus/internal/audit"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/observability/telemetry"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Runner runs fn in one transaction scoped to org as the worker (cmd/worker
// gives the worker scope's Run; the tests a scoped login's).
type Runner func(ctx context.Context, org string, fn func(ctx context.Context, tx tenant.Tx) error) error

// Config is the meter's setup.
type Config struct {
	Run Runner
	// Actor names the worker in audit events (WORKER_ID).
	Actor  string
	Limits spend.Limits
	// Now is the clock of the periods (UTC); time.Now when nil.
	Now func() time.Time
}

// Meter is the ledger's provider.Meter.
type Meter struct{ cfg Config }

var _ provider.Meter = (*Meter)(nil)

// New checks cfg.
func New(cfg Config) (*Meter, error) {
	switch {
	case cfg.Run == nil:
		return nil, errors.New("aispend: a transaction runner is required")
	case !audit.ValidActor(cfg.Actor):
		return nil, errors.New("aispend: the worker's name must be an actor reference to name it in audit events")
	}
	if err := cfg.Limits.Validate(); err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Meter{cfg: cfg}, nil
}

// columns are the reservation's target column of each attribution target.
var columns = map[string]string{"ai_proposal": "ai_proposal_id", "ai_analysis": "ai_analysis_id", "import": "import_id"}

var (
	reservationID = regexp.MustCompile(`^asr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	credential    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// errRefused rolls a reservation back; limit says which.
type errRefused struct{ limit string }

func (e *errRefused) Error() string { return "aispend: the " + e.limit + " limit refuses the charge" }

// event is an audit event of the ledger on the call's target.
func (m *Meter) event(ctx context.Context, tx tenant.Tx, org, action string, a provider.Attribution, meta map[string]any) error {
	return auditlog.AppendWorker(ctx, tx, org, m.cfg.Actor, auditlog.Event{Action: action, TargetType: a.TargetType,
		TargetID: a.TargetID, RequestID: a.RequestID, Metadata: meta})
}

// Reserve opens a reservation for c if no limit refuses it.
func (m *Meter) Reserve(ctx context.Context, c provider.Charge) (provider.Reservation, error) {
	col, ok := columns[c.Attribution.TargetType]
	if !ok || c.NanoUSD <= 0 {
		return provider.Reservation{}, errors.New("aispend: the charge names no target the ledger keeps, or no amount")
	}
	now := m.cfg.Now()
	day, month := spend.Day(now), spend.Month(now)
	l := m.cfg.Limits
	refuse := func(limit string) (provider.Reservation, error) {
		telemetry.Current().SpendRefused(ctx, c.Model, string(c.Role), limit)
		// Best effort: the call is refused whether or not this commits.
		_ = m.cfg.Run(ctx, c.OrganizationID, func(ctx context.Context, tx tenant.Tx) error {
			return m.event(ctx, tx, c.OrganizationID, "ai_spend.refuse", c.Attribution, map[string]any{"role": string(c.Role),
				"model": audit.Label(c.Model), "charge_nano_usd": c.NanoUSD, "limit": limit})
		})
		return provider.Reservation{}, &provider.Error{Code: provider.CodeQuotaExceeded, Limit: limit}
	}
	if c.NanoUSD > l.OrganizationDay {
		return refuse(provider.LimitOrganizationDay) // no total can hold it, and no sum can overflow
	}
	id := ids.New("asr")
	err := m.cfg.Run(ctx, c.OrganizationID, func(ctx context.Context, tx tenant.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO twin.ai_spend_periods(period,starts_on) VALUES ('month',$1),('day',$2) ON CONFLICT DO NOTHING`,
			month, day); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO twin.ai_spend_days(organization_id,day) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			c.OrganizationID, day); err != nil {
			return err
		}
		// One lock order for every writer: month, day, organization day.
		for _, step := range []struct {
			limit string
			sql   string
			args  []any
		}{
			{provider.LimitMonth, `UPDATE twin.ai_spend_periods SET committed_nano_usd = committed_nano_usd + $1
				WHERE period = 'month' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, month, l.Month}},
			{provider.LimitDay, `UPDATE twin.ai_spend_periods SET committed_nano_usd = committed_nano_usd + $1
				WHERE period = 'day' AND starts_on = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.Day}},
			{provider.LimitOrganizationDay, `UPDATE twin.ai_spend_days SET committed_nano_usd = committed_nano_usd + $1
				WHERE organization_id = $4 AND day = $2 AND committed_nano_usd + $1 <= $3`, []any{c.NanoUSD, day, l.OrganizationDay, c.OrganizationID}},
		} {
			tag, err := tx.Exec(ctx, step.sql, step.args...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return &errRefused{step.limit}
			}
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`INSERT INTO twin.ai_spend_reservations(id,organization_id,day,role,model,%s,request_id,reserved_nano_usd)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, col), id, c.OrganizationID, day, string(c.Role), c.Model, c.Attribution.TargetID,
			c.Attribution.RequestID, c.NanoUSD); err != nil {
			return err
		}
		var held int64
		if err := tx.QueryRow(ctx, `SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $1 AND day = $2`,
			c.OrganizationID, day).Scan(&held); err != nil {
			return err
		}
		return m.event(ctx, tx, c.OrganizationID, "ai_spend.reserve", c.Attribution, map[string]any{"reservation_id": id,
			"role": string(c.Role), "model": audit.Label(c.Model), "reserved_nano_usd": c.NanoUSD, "organization_day_committed_nano_usd": held})
	})
	var r *errRefused
	if errors.As(err, &r) {
		return refuse(r.limit)
	}
	if err != nil {
		return provider.Reservation{}, err
	}
	return provider.Reservation{ID: id, Charge: c}, nil
}

// ErrSettled: the reservation is already settled; nothing changed.
var ErrSettled = errors.New("aispend: the reservation is already settled")

// ErrNotFound: the organization has no such reservation.
var ErrNotFound = errors.New("aispend: no such reservation")

// Settle closes r with what the call cost.
func (m *Meter) Settle(ctx context.Context, r provider.Reservation, s provider.Settlement) error {
	u := s.Usage
	switch {
	case !reservationID.MatchString(r.ID):
		return ErrNotFound
	case s.NanoUSD < 0 || u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheCreationInputTokens < 0 || u.CacheReadInputTokens < 0 ||
		s.UnknownAttempts < 0:
		return errors.New("aispend: a settlement has no negative amount or count")
	case s.Outcome != provider.SpendUsage && s.Outcome != provider.SpendNone && s.Outcome != provider.SpendUnknown:
		return errors.New("aispend: a settlement says how its amount was found")
	case s.Credential != "" && !credential.MatchString(s.Credential):
		return errors.New("aispend: a credential is named by its label")
	}
	org := r.Charge.OrganizationID
	var label any
	if s.Credential != "" {
		label = s.Credential
	}
	var role, model string
	err := m.cfg.Run(ctx, org, func(ctx context.Context, tx tenant.Tx) error {
		var day time.Time
		var reserved int64
		var state, request string
		var aip, ana, imp *string
		err := tx.QueryRow(ctx, `SELECT day, reserved_nano_usd, state, role, model, request_id, ai_proposal_id, ai_analysis_id, import_id
			FROM twin.ai_spend_reservations WHERE organization_id = $1 AND id = $2 FOR UPDATE`, org, r.ID).
			Scan(&day, &reserved, &state, &role, &model, &request, &aip, &ana, &imp)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "reserved" {
			return ErrSettled
		}
		delta := s.NanoUSD - reserved
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE twin.ai_spend_periods SET committed_nano_usd = committed_nano_usd + $1 WHERE period = 'month' AND starts_on = $2`,
				[]any{delta, spend.Month(day)}},
			{`UPDATE twin.ai_spend_periods SET committed_nano_usd = committed_nano_usd + $1 WHERE period = 'day' AND starts_on = $2`,
				[]any{delta, day}},
			{`UPDATE twin.ai_spend_days SET committed_nano_usd = committed_nano_usd + $1 WHERE organization_id = $3 AND day = $2`,
				[]any{delta, day, org}},
			{`UPDATE twin.ai_spend_reservations SET state = 'settled', outcome = $3, settled_nano_usd = $4, input_tokens = $5,
				output_tokens = $6, cache_write_tokens = $7, cache_read_tokens = $8, unknown_attempts = $9, credential_label = $10,
				settled_at = now() WHERE organization_id = $1 AND id = $2`,
				[]any{org, r.ID, string(s.Outcome), s.NanoUSD, u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens,
					u.CacheReadInputTokens, s.UnknownAttempts, label}},
		} {
			tag, err := tx.Exec(ctx, q.sql, q.args...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return errors.New("aispend: the ledger lacks a total the reservation counts in")
			}
		}
		target := provider.Attribution{RequestID: request}
		switch {
		case aip != nil:
			target.TargetType, target.TargetID = "ai_proposal", *aip
		case ana != nil:
			target.TargetType, target.TargetID = "ai_analysis", *ana
		case imp != nil:
			target.TargetType, target.TargetID = "import", *imp
		}
		return m.event(ctx, tx, org, "ai_spend.settle", target, map[string]any{"reservation_id": r.ID, "role": role,
			"model": audit.Label(model), "reserved_nano_usd": reserved, "settled_nano_usd": s.NanoUSD, "outcome": string(s.Outcome),
			"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "cache_write_tokens": u.CacheCreationInputTokens,
			"cache_read_tokens": u.CacheReadInputTokens, "unknown_attempts": s.UnknownAttempts, "credential_label": s.Credential})
	})
	if err != nil {
		return err
	}
	telemetry.Current().Spend(ctx, model, role, string(s.Outcome), s.NanoUSD)
	return nil
}

// Totals are what a UTC day holds: the deployment's day and month and one
// organization's day.
type Totals struct {
	Month, Day, OrganizationDay int64
}

// TotalsAt reads the totals of the day and month of t, in tx's
// organization (zero for a period no call has reserved in yet).
func TotalsAt(ctx context.Context, tx tenant.Tx, org string, t time.Time) (Totals, error) {
	var out Totals
	err := tx.QueryRow(ctx, `SELECT
		coalesce((SELECT committed_nano_usd FROM twin.ai_spend_periods WHERE period = 'month' AND starts_on = $1), 0),
		coalesce((SELECT committed_nano_usd FROM twin.ai_spend_periods WHERE period = 'day' AND starts_on = $2), 0),
		coalesce((SELECT committed_nano_usd FROM twin.ai_spend_days WHERE organization_id = $3 AND day = $2), 0)`,
		spend.Month(t), spend.Day(t), org).Scan(&out.Month, &out.Day, &out.OrganizationDay)
	return out, err
}
