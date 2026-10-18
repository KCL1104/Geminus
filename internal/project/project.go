// Package project is the tenant project lifecycle (T-009): create, list and
// get, each in one tenant.DB.Run transaction that checks the caller's
// membership and role before touching data (D-008). A create is idempotent
// per Idempotency-Key (D-011) and writes its audit event in the same
// transaction (REQ-018). SQL lives in internal/storage.
package project

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"regexp"
	"time"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/projects"
	"github.com/kcl1104/geminus/internal/tenant"
)

// Project is http.schema.json#/$defs/Project.
type Project struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	CreatedAt      string `json:"created_at"` // RFC 3339, UTC
	CreatedBy      string `json:"created_by"`
}

// Page is http.schema.json#/$defs/Projects.
type Page struct {
	Items      []Project `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

// CreateRequest is http.schema.json#/$defs/CreateProject.
type CreateRequest struct {
	Name string `json:"name"`
}

var (
	// ErrInvalidCursor: the cursor is not one this service issued for this
	// organization's project list.
	ErrInvalidCursor = errors.New("project: invalid cursor")
	// ErrNotFound: no such project in the organization (or no access).
	ErrNotFound = errors.New("project: not found")
)

var projectID = regexp.MustCompile(`^prj_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// ValidID reports whether id is a D-001 project ID.
func ValidID(id string) bool { return projectID.MatchString(id) }

// Service runs project operations for a verified scope.
type Service struct {
	DB *tenant.DB
}

const createOperation = "createProject"

// Create creates a project named req.Name, or replays the response of the
// earlier request with the same Idempotency-Key. It returns the HTTP
// status and body to send. body is the request body as received; its
// canonical form is what a retry must repeat.
func (s *Service) Create(ctx context.Context, sc tenant.Scope, requestID, key string, body []byte, req CreateRequest) (int, []byte, error) {
	canonical, err := hashing.Canonical(body)
	if err != nil {
		return 0, nil, err
	}
	hash := hashing.Sum(canonical)
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: createOperation, Key: key}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(tenant.Writers...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			// jsonb keeps the value, not the bytes: both answers are sent
			// in canonical form, so a replay is byte-identical.
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		p, err := projects.Insert(ctx, tx, projects.Project{ID: ids.New("prj"), OrganizationID: m.OrganizationID, Name: req.Name, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "project.create", TargetType: "project", TargetID: p.ID, RequestID: requestID}); err != nil {
			return err
		}
		data, err := json.Marshal(public(p))
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 201
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}

// Get returns the project id.
func (s *Service) Get(ctx context.Context, sc tenant.Scope, id string) (Project, error) {
	var out Project
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(tenant.Members...); err != nil {
			return err
		}
		p, err := projects.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, projects.ErrNotFound) {
			return ErrNotFound
		}
		out = public(p)
		return err
	})
	return out, err
}

// List returns one page of the organization's projects, oldest first.
func (s *Service) List(ctx context.Context, sc tenant.Scope, cursor string, limit int) (Page, error) {
	var after *projects.Position
	if cursor != "" {
		pos, err := decodeCursor(cursor, sc.OrganizationID)
		if err != nil {
			return Page{}, err
		}
		after = &pos
	}
	page := Page{Items: []Project{}}
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(tenant.Members...); err != nil {
			return err
		}
		rows, err := projects.List(ctx, tx, m.OrganizationID, after, limit+1)
		if err != nil {
			return err
		}
		if len(rows) > limit {
			last := rows[limit-1]
			c := encodeCursor(m.OrganizationID, projects.Position{CreatedAt: last.CreatedAt, ID: last.ID})
			page.NextCursor = &c
			rows = rows[:limit]
		}
		for _, p := range rows {
			page.Items = append(page.Items, public(p))
		}
		return nil
	})
	return page, err
}

func public(p projects.Project) Project {
	return Project{ID: p.ID, OrganizationID: p.OrganizationID, Name: p.Name, CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339Nano), CreatedBy: p.CreatedBy}
}

// cursor is the position after which a page starts, bound to the
// organization and the list it came from (API_DATABASE §3). It is not
// signed: it can only move within the caller's own organization's list,
// which RLS and the organization filter confine.
type cursor struct {
	Version int    `json:"v"`
	Org     string `json:"o"`
	List    string `json:"l"`
	After   string `json:"t"` // RFC 3339 with nanoseconds
	ID      string `json:"i"`
}

const cursorList = "projects"

func encodeCursor(org string, pos projects.Position) string {
	data, _ := json.Marshal(cursor{Version: 1, Org: org, List: cursorList, After: pos.CreatedAt.UTC().Format(time.RFC3339Nano), ID: pos.ID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(s, org string) (projects.Position, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return projects.Position{}, ErrInvalidCursor
	}
	var c cursor
	if err := json.Unmarshal(data, &c, json.RejectUnknownMembers(true)); err != nil {
		return projects.Position{}, ErrInvalidCursor
	}
	t, err := time.Parse(time.RFC3339Nano, c.After)
	if err != nil || c.Version != 1 || c.Org != org || c.List != cursorList || !ValidID(c.ID) {
		return projects.Position{}, ErrInvalidCursor
	}
	return projects.Position{CreatedAt: t, ID: c.ID}, nil
}
