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
// error (0012). A person decides it through the API (T-026, 0013): Approve
// (with its one effect, created first in the same transaction), Reject, or
// Conflict when an approval finds that its base is no longer the model's
// latest version; each records the deciding user, whom 0001 holds to the
// transaction's scoped user, and the database's time.
package proposals

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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
	// The decision record (T-026): who decided (or found the conflict),
	// when, and the person's comment; nil before a decision.
	DecidedBy       *string
	DecidedAt       *time.Time
	DecisionComment *string
	CreatedBy       string
	CreatedAt       time.Time
	// RequestID is the request that created the proposal (its job's
	// payload).
	RequestID string
	// RequestedModel is the model that request named (its job's payload,
	// H-049; H-065 shows it), "" when it named none.
	RequestedModel string
	// Image is the photo the proposal was asked for with (T-056, 0017);
	// nil for a proposal asked for in words.
	Image *Image
}

// Image is what the server records of a photo proposal's image (T-056,
// twin.ai_proposal_images): the private object it stored (the upload's
// pixels encoded again, no metadata) and its SHA-256, size, media type and
// size in pixels. It is the proposal's request and never changes.
type Image struct {
	ObjectKey     string
	SHA256        string
	Size          int64
	MediaType     string
	Width, Height int
}

// InsertImage records im for the photo proposal id of org, which the same
// transaction created (0017 refuses any other).
func InsertImage(ctx context.Context, tx pgx.Tx, org, id string, im Image) error {
	_, err := tx.Exec(ctx, `INSERT INTO twin.ai_proposal_images(organization_id,proposal_id,object_key,content_hash,size_bytes,media_type,width,height)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, org, id, im.ObjectKey, im.SHA256, im.Size, im.MediaType, im.Width, im.Height)
	return err
}

// ErrNotFound: no such proposal in the organization.
var ErrNotFound = errors.New("proposals: not found")

// ErrNotGenerating: the proposal is not generating any more; nothing was
// written.
var ErrNotGenerating = errors.New("proposals: the proposal is not generating")

// ErrUnstorable: the database refused what the generated row would hold:
// text with U+0000 (SQLSTATE 22021), jsonb with \u0000 (22P05) or a content
// check of 0001 or 0012 (23514). Nothing was written, and the transaction
// is aborted. The server's message, which may quote the value, is not kept.
var ErrUnstorable = errors.New("proposals: the database cannot store the generated content")

// unstorable is err as ErrUnstorable when the database refused the row's
// content, else err.
func unstorable(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "22021", "22P05", "23514":
			return fmt.Errorf("%w (SQLSTATE %s, constraint %q)", ErrUnstorable, pg.Code, pg.ConstraintName)
		}
	}
	return err
}

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
	p.provider_metadata, p.error, p.new_version_id, p.scenario_id, p.decided_by, p.decided_at, p.decision_comment,
	p.created_by, p.created_at, coalesce(j.payload->>'request_id', ''), coalesce(j.payload->>'model', ''),
	i.object_key, i.content_hash, i.size_bytes, i.media_type, i.width, i.height`

// from joins each proposal with its job (one at most, 0001), whose payload
// holds the request that created it, and with its image (one at most,
// 0017), in the same organization.
const from = ` FROM twin.ai_proposals p LEFT JOIN twin.jobs j
	  ON j.organization_id=p.organization_id AND j.ai_proposal_id=p.id
	LEFT JOIN twin.ai_proposal_images i ON i.organization_id=p.organization_id AND i.proposal_id=p.id`

func scan(row pgx.Row, org string) (Proposal, error) {
	p := Proposal{OrganizationID: org}
	var status string
	var selected, assumptions, effects, questions, bindings []byte
	var imageKey, imageHash, imageType *string
	var imageSize *int64
	var imageWidth, imageHeight *int
	err := row.Scan(&p.ID, &p.ModelID, &p.BaseVersionID, &p.SourceImportID, &p.Mode, &status, &p.Intent, &p.OutputLocale,
		&selected, &p.IDPool, &p.Summary, &assumptions, &effects, &questions, &p.Patch, &p.PatchHash,
		&bindings, &p.ServerRisk, &p.ProviderRisk, &p.Risk, &p.Confidence, &p.Validation,
		&p.ProviderMetadata, &p.Failure, &p.NewVersionID, &p.ScenarioID, &p.DecidedBy, &p.DecidedAt, &p.DecisionComment,
		&p.CreatedBy, &p.CreatedAt, &p.RequestID, &p.RequestedModel,
		&imageKey, &imageHash, &imageSize, &imageType, &imageWidth, &imageHeight)
	if errors.Is(err, pgx.ErrNoRows) {
		return Proposal{}, ErrNotFound
	}
	if err != nil {
		return Proposal{}, err
	}
	p.Status = Status(status)
	if imageKey != nil {
		// 0017's columns are all NOT NULL: a joined row has every one.
		if imageHash == nil || imageSize == nil || imageType == nil || imageWidth == nil || imageHeight == nil {
			return Proposal{}, fmt.Errorf("proposal %s: its image row is incomplete", p.ID)
		}
		p.Image = &Image{ObjectKey: *imageKey, SHA256: *imageHash, Size: *imageSize, MediaType: *imageType, Width: *imageWidth, Height: *imageHeight}
	}
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
// Content the database refuses is ErrUnstorable.
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
		return unstorable(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotGenerating
	}
	return nil
}

// ErrNotDecidable: the proposal is not in a state the decision may leave
// (approving and conflicting need ready; rejecting needs ready or
// needs_input); nothing was written. The caller holds the proposal's row
// (Lock) and checked its state first, so this is a second line of defence.
var ErrNotDecidable = errors.New("proposals: the proposal is not in a state this decision may leave")

// decided is nil for one decided row, else ErrNotDecidable or err.
func decided(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotDecidable
	}
	return nil
}

// Approve records the ready proposal id as approved by user, with its one
// effect, newVersionID (edit) or scenarioID (scenario), which the caller
// created from this proposal earlier in the same transaction (0001 checks
// that the effect names this proposal; 0013 checks the effect when the
// transaction ends), and the person's comment (nil for none).
func Approve(ctx context.Context, tx pgx.Tx, org, id, user string, newVersionID, scenarioID, comment *string) error {
	return decided(tx.Exec(ctx, `UPDATE twin.ai_proposals SET status='approved', decided_by=$3, new_version_id=$4, scenario_id=$5,
		  decision_comment=$6
		WHERE organization_id=$1 AND id=$2 AND status='ready'`, org, id, user, newVersionID, scenarioID, comment))
}

// Reject records the ready or needs_input proposal id as rejected by user,
// with the person's comment (nil for none).
func Reject(ctx context.Context, tx pgx.Tx, org, id, user string, comment *string) error {
	return decided(tx.Exec(ctx, `UPDATE twin.ai_proposals SET status='rejected', decided_by=$3, decision_comment=$4
		WHERE organization_id=$1 AND id=$2 AND status IN ('ready','needs_input')`, org, id, user, comment))
}

// Conflict records that user's approval found the ready proposal id's base
// no longer the model's latest version: the proposal is conflicted for
// good (AI_SPEC §4, §6: no rebase).
func Conflict(ctx context.Context, tx pgx.Tx, org, id, user string) error {
	return decided(tx.Exec(ctx, `UPDATE twin.ai_proposals SET status='conflicted', decided_by=$3
		WHERE organization_id=$1 AND id=$2 AND status='ready'`, org, id, user))
}
