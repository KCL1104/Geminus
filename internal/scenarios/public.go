package scenarios

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/kcl1104/geminus/internal/model/hashing"
	store "github.com/kcl1104/geminus/internal/storage/scenarios"
)

// Scenario is scenario.schema.json: the resource getScenario and
// listScenarios show and createManualScenario answers, for a person's
// scenario (SourceProposalID nil, shown as null) and for one an approved
// scenario proposal created (T-026) alike.
type Scenario struct {
	ID               string         `json:"id"`
	OrganizationID   string         `json:"organization_id"`
	ModelID          string         `json:"model_id"`
	BaseVersionID    string         `json:"base_version_id"`
	Name             string         `json:"name"`
	Patch            jsontext.Value `json:"patch"`
	PatchHash        string         `json:"patch_hash"`
	SourceProposalID *string        `json:"source_proposal_id"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        string         `json:"created_at"`
}

// Page is http.schema.json#/$defs/Scenarios.
type Page struct {
	Items      []Scenario `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

// ErrCorrupt: a stored scenario's patch is not the one its patch_hash
// covers. It is a server fault (500), never shown (D-079 (f)).
var ErrCorrupt = errors.New("scenarios: a stored scenario's patch does not match its patch_hash")

var (
	scenarioIDs = regexp.MustCompile(`^scn_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	proposalIDs = regexp.MustCompile(`^aip_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	userIDs     = regexp.MustCompile(`^usr_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
	sha256s     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Public is the Scenario resource of row r: its patch in RFC 8785 form
// (the database returns jsonb's spelling), checked against patch_hash
// before it is shown, and its creation time in UTC. A row that is not a
// scenario the server could have written is an error.
func Public(r store.Scenario) (Scenario, error) {
	canonical, err := hashing.Canonical(r.Patch)
	if err != nil {
		return Scenario{}, fmt.Errorf("scenarios: scenario %s: the stored patch: %w", r.ID, err)
	}
	hash, err := hashing.Patch(canonical)
	if err != nil {
		return Scenario{}, fmt.Errorf("scenarios: scenario %s: the stored patch: %w", r.ID, err)
	}
	if !sha256s.MatchString(r.PatchHash) {
		return Scenario{}, fmt.Errorf("scenarios: scenario %s: the stored patch hash is not a hash", r.ID)
	}
	if hash != r.PatchHash {
		return Scenario{}, fmt.Errorf("%w (scenario %s)", ErrCorrupt, r.ID)
	}
	if !scenarioIDs.MatchString(r.ID) || !userIDs.MatchString(r.CreatedBy) || r.SourceProposalID != nil && !proposalIDs.MatchString(*r.SourceProposalID) {
		return Scenario{}, fmt.Errorf("scenarios: scenario %q is not a stored scenario", r.ID)
	}
	return Scenario{ID: r.ID, OrganizationID: r.OrganizationID, ModelID: r.ModelID, BaseVersionID: r.BaseVersionID, Name: r.Name,
		Patch: jsontext.Value(canonical), PatchHash: r.PatchHash, SourceProposalID: r.SourceProposalID, CreatedBy: r.CreatedBy,
		CreatedAt: timestamp(r.CreatedAt)}, nil
}
