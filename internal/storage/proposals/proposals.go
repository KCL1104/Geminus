// Package proposals is the SQL of twin.ai_proposals for their creation and
// generation (T-025, API_DATABASE §4, migrations 0001, 0002, 0011 and 0012).
// Every statement runs in a scoped transaction (tenant.DB.Run for the API,
// the worker's lease-fenced Checkpoint and Finish) and also filters by
// organization.
//
// The API inserts a proposal generating, with its request (base, mode,
// intent, output locale, selected entities) and its D-046 ID pool, none of
// which changes later, and its one model_proposal job. The worker writes
// the generated content once, moving it to ready, needs_input or invalid
// in the transaction that completes the job (Generate); when the job fails
// or is cancelled first, the proposal follows it to failed with the job's
// error (0012). Deciding a proposal is T-026's.
package proposals

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/model"
)

// Status is a proposal's state.
type Status string

const (
	Generating Status = "generating"
	NeedsInput Status = "needs_input"
	Ready      Status = "ready"
	Invalid    Status = "invalid"
	Failed     Status = "failed"
	Approved   Status = "approved"
	Rejected   Status = "rejected"
	Conflicted Status = "conflicted"
)

// Proposal is one row of twin.ai_proposals, with the request that created
// it.
type Proposal struct {
	ID, OrganizationID, ModelID, BaseVersionID string
	SourceImportID                             *string
	Mode                                       string
	Status                                     Status
	Intent                                     string
	OutputLocale                               string
	SelectedEntityIDs                          []string
	// IDPool is the stored ID allocation pool (JSON, D-046).
	IDPool []byte
	// The generated content; nil or empty until the proposal is generated.
	Summary                        *string
	Assumptions, ExpectedEffects   []string
	Questions                      []string
	Patch                          []byte // a JSON Patch array as stored
	PatchHash                      *string
	DatasetBindings                []model.DatasetBinding
	ServerRisk, ProviderRisk, Risk *string
	Confidence                     *float64
	Validation                     []byte // common.schema.json Validation as stored
	ProviderMetadata               []byte
	// Failure is why the proposal failed (the jobs.Failure JSON its job
	// recorded, 0012); nil otherwise.
	Failure                  []byte
	NewVersionID, ScenarioID *string
	CreatedBy                string
	CreatedAt                time.Time
	// RequestID is the request that created the proposal (its job's
	// payload).
	RequestID string
}

// ErrNotFound: no such proposal in the organization.
var ErrNotFound = errors.New("proposals: not found")

// ErrNotGenerating: the proposal is not generating any more; nothing was
// written.
var ErrNotGenerating = errors.New("proposals: the proposal is not generating")

// Insert stores p generating, with its request and pool.
func Insert(ctx context.Context, tx pgx.Tx, p Proposal) (Proposal, error) {
	selected := p.SelectedEntityIDs
	if selected == nil {
		selected = []string{}
	}
	ids, err := json.Marshal(selected)
	if err != nil {
		return Proposal{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO twin.ai_proposals(id,organization_id,model_id,base_version_id,source_import_id,mode,status,
		  intent,output_locale,selected_entity_ids,id_pool,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,'generating',$7,$8,$9,$10,$11) RETURNING created_at`,
		p.ID, p.OrganizationID, p.ModelID, p.BaseVersionID, p.SourceImportID, p.Mode, p.Intent, p.OutputLocale, string(ids),
		string(p.IDPool), p.CreatedBy).Scan(&p.CreatedAt)
	if err != nil {
		return Proposal{}, err
	}
	p.Status, p.SelectedEntityIDs = Generating, selected
	return p, nil
}

const columns = `p.id, p.model_id, p.base_version_id, p.source_import_id, p.mode, p.status, p.intent, p.output_locale,
	p.selected_entity_ids, p.id_pool, p.summary, p.assumptions, p.expected_effects, p.questions, p.patch, p.patch_hash,
	p.candidate_dataset_bindings, p.server_risk_level, p.provider_risk_claim, p.risk_level, p.confidence, p.validation,
	p.provider_metadata, p.error, p.new_version_id, p.scenario_id, p.created_by, p.created_at,
	coalesce(j.payload->>'request_id', '')`

// from joins each proposal with its job (one at most, 0001), whose payload
// holds the request that created it.
const from = ` FROM twin.ai_proposals p LEFT JOIN twin.jobs j
	  ON j.organization_id=p.organization_id AND j.ai_proposal_id=p.id`

func scan(row pgx.Row, org string) (Proposal, error) {
	p := Proposal{OrganizationID: org}
	var status string
	var selected, assumptions, effects, questions, bindings []byte
	err := row.Scan(&p.ID, &p.ModelID, &p.BaseVersionID, &p.SourceImportID, &p.Mode, &status, &p.Intent, &p.OutputLocale,
		&selected, &p.IDPool, &p.Summary, &assumptions, &effects, &questions, &p.Patch, &p.PatchHash,
		&bindings, &p.ServerRisk, &p.ProviderRisk, &p.Risk, &p.Confidence, &p.Validation,
		&p.ProviderMetadata, &p.Failure, &p.NewVersionID, &p.ScenarioID, &p.CreatedBy, &p.CreatedAt, &p.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	if err != nil {
		return Proposal{}, err
	}
	p.Status = Status(status)
	for _, c := range []struct {
		name string
		data []byte
		dst  any
	}{
		{"selected_entity_ids", selected, &p.SelectedEntityIDs}, {"assumptions", assumptions, &p.Assumptions},
		{"expected_effects", effects, &p.ExpectedEffects}, {"questions", questions, &p.Questions},
		{"candidate_dataset_bindings", bindings, &p.DatasetBindings},
	} {
		if err := json.Unmarshal(c.data, c.dst); err != nil {
			return Proposal{}, fmt.Errorf("proposal %s: stored %s: %w", p.ID, c.name, err)
		}
	}
	return p, nil
}

// Get returns the proposal id of organization org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Proposal, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE p.organization_id=$1 AND p.id=$2`, org, id), org)
}

// Lock returns the proposal and holds its row until the transaction ends.
func Lock(ctx context.Context, tx pgx.Tx, org, id string) (Proposal, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+from+` WHERE p.organization_id=$1 AND p.id=$2 FOR UPDATE OF p`, org, id), org)
}

// Position is a place in the (created_at, id) order of a model's proposals.
type Position struct {
	CreatedAt time.Time
	ID        string
}

// List returns up to limit proposals of the model in (created_at, id)
// order, after the position if one is given.
func List(ctx context.Context, tx pgx.Tx, org, modelID string, after *Position, limit int) ([]Proposal, error) {
	var rows pgx.Rows
	var err error
	if after == nil {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE p.organization_id=$1 AND p.model_id=$2
			ORDER BY p.created_at, p.id LIMIT $3`, org, modelID, limit)
	} else {
		rows, err = tx.Query(ctx, `SELECT `+columns+from+` WHERE p.organization_id=$1 AND p.model_id=$2
			AND (p.created_at, p.id) > ($3, $4) ORDER BY p.created_at, p.id LIMIT $5`, org, modelID, after.CreatedAt, after.ID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		p, err := scan(rows, org)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Generated is what a proposal's job writes when it is generated: the
// status (ready, needs_input or invalid) and the content that status holds.
type Generated struct {
	Status                         Status
	Summary                        *string
	Assumptions, ExpectedEffects   []string
	Questions                      []string
	Patch                          []byte
	PatchHash                      *string
	DatasetBindings                []model.DatasetBinding
	ServerRisk, ProviderRisk, Risk *string
	Confidence                     *float64
	Validation                     []byte
	ProviderMetadata               []byte
}

func jsonArray[T any](s []T) (string, error) {
	if s == nil {
		s = []T{}
	}
	data, err := json.Marshal(s)
	return string(data), err
}

func nullableJSON(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

// Generate writes g to the generating proposal id (the worker, in the
// transaction that completes the proposal's job; 0012 refuses any other).
func Generate(ctx context.Context, tx pgx.Tx, org, id string, g Generated) error {
	switch g.Status {
	case Ready, NeedsInput, Invalid:
	default:
		return fmt.Errorf("proposals: a proposal is generated ready, needs_input or invalid, not %q", g.Status)
	}
	assumptions, err := jsonArray(g.Assumptions)
	if err != nil {
		return err
	}
	effects, err := jsonArray(g.ExpectedEffects)
	if err != nil {
		return err
	}
	questions, err := jsonArray(g.Questions)
	if err != nil {
		return err
	}
	bindings, err := jsonArray(g.DatasetBindings)
	if err != nil {
		return err
	}
	metadata := g.ProviderMetadata
	if metadata == nil {
		metadata = []byte("{}")
	}
	tag, err := tx.Exec(ctx, `UPDATE twin.ai_proposals SET status=$3, summary=$4, assumptions=$5, expected_effects=$6, questions=$7,
		  patch=$8, patch_hash=$9, candidate_dataset_bindings=$10, server_risk_level=$11, provider_risk_claim=$12, risk_level=$13,
		  confidence=$14, validation=$15, provider_metadata=$16
		WHERE organization_id=$1 AND id=$2 AND status='generating'`,
		org, id, string(g.Status), g.Summary, assumptions, effects, questions, nullableJSON(g.Patch), g.PatchHash, bindings,
		g.ServerRisk, g.ProviderRisk, g.Risk, g.Confidence, nullableJSON(g.Validation), string(metadata))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotGenerating
	}
	return nil
}
