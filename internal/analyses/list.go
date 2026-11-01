package analyses

// listAnalyses (H-073 slice 1): the completed analyses of a run beside one
// baseline run, so a finished comparison shows its saved explanation
// instead of asking the AI again. A read: no provider, no audit event.

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"time"

	store "github.com/kcl1104/geminus/internal/storage/analyses"
	"github.com/kcl1104/geminus/internal/storage/runs"
	"github.com/kcl1104/geminus/internal/tenant"
)

var (
	// ErrInvalidCursor: a cursor this list did not issue (400).
	ErrInvalidCursor = errors.New("analyses: invalid cursor")
	// ErrInvalidLimit: a page size outside 1..100 (400; the handler's
	// PageQuery bounds it first).
	ErrInvalidLimit = errors.New("analyses: invalid limit")
)

// BaselineQuery is listAnalyses' baseline parameter, as a RunError names it.
const BaselineQuery = "baseline_run_id"

const maxLimit = 100

// Page is http.schema.json#/$defs/Analyses.
type Page struct {
	Items      []Record `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

// List returns one page of the completed analyses of run runID beside
// baseline run baselineID, newest first, each as Get shows it. Both runs
// must be the organization's (a RunError naming the parameter otherwise,
// the run first); a run that is not completed has none.
func (s *Service) List(ctx context.Context, sc tenant.Scope, requestID, runID, baselineID, cursor string, limit int) (Page, error) {
	if limit < 1 || limit > maxLimit {
		return Page{}, ErrInvalidLimit
	}
	var after *store.Position
	if cursor != "" {
		pos, err := decodeCursor(cursor, sc.OrganizationID, runID, baselineID)
		if err != nil {
			return Page{}, err
		}
		after = &pos
	}
	page := Page{Items: []Record{}}
	err := s.DB.Run(ctx, sc, tenant.ReadSnapshot, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		for _, r := range []struct{ id, param string }{{runID, RunParam}, {baselineID, BaselineQuery}} {
			if _, err := runs.Get(ctx, tx, m.OrganizationID, r.id); errors.Is(err, runs.ErrNotFound) {
				return &RunError{Param: r.param, Err: ErrRunNotFound}
			} else if err != nil {
				return err
			}
		}
		rows, err := store.ListCompleted(ctx, tx, m.OrganizationID, runID, baselineID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(m.OrganizationID, runID, baselineID, store.Position{CreatedAt: last.CreatedAt, ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, a := range rows {
			r, err := public(a, requestID)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, r)
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

// cursor is the position after which a page starts, bound to the
// organization, the pair and the list (API_DATABASE §3).
type cursor struct {
	Version  int    `json:"v"`
	Org      string `json:"o"`
	List     string `json:"l"`
	Run      string `json:"r"`
	Baseline string `json:"b"`
	After    string `json:"t"` // RFC 3339 with nanoseconds
	ID       string `json:"i"`
}

const cursorList = "analyses"

func encodeCursor(org, runID, baselineID string, pos store.Position) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, Run: runID, Baseline: baselineID,
		After: pos.CreatedAt.UTC().Format(time.RFC3339Nano), ID: pos.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org, runID, baselineID string) (store.Position, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return store.Position{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.After)
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || c.Run != runID || c.Baseline != baselineID ||
		!analysisIDs.MatchString(c.ID) {
		return store.Position{}, ErrInvalidCursor
	}
	return store.Position{CreatedAt: t, ID: c.ID}, nil
}
