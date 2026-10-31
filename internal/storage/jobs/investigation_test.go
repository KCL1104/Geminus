package jobs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/storage/investigations"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	"github.com/kcl1104/geminus/internal/tenant"
)

// H-061 A3: an investigation job names its investigation and nothing else,
// and no other type names one; refused before the database.
func TestAnInvestigationJobNamesItsInvestigation(t *testing.T) {
	org, inv := ids.New("org"), ids.New("inv")
	for name, n := range map[string]jobs.New{
		"no investigation":             {Type: jobs.Investigation},
		"another resource":             {Type: jobs.Investigation, Resource: jobs.Resource{AIProposalID: ids.New("aip")}},
		"an investigation and another": {Type: jobs.Investigation, Resource: jobs.Resource{InvestigationID: inv, AIProposalID: ids.New("aip")}},
		"a proposal job naming one":    {Type: jobs.ModelProposal, Resource: jobs.Resource{InvestigationID: inv}},
		"a simulation naming one":      {Type: jobs.Simulation, Resource: jobs.Resource{InvestigationID: inv}},
	} {
		n.ID, n.OrganizationID, n.Payload = ids.New("job"), org, map[string]any{}
		if _, err := jobs.Enqueue(context.Background(), nil, n, fast); !errors.Is(err, jobs.ErrInvalidJob) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The API enqueues an investigation's one job with the investigation (5
// attempts, H-061 §5.1); only a worker loop that handles the type claims
// it, and the claim names the investigation.
func TestEnqueueAndClaimAnInvestigationJob(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	owner := e.org.Members[tenant.Owner]
	inv, job := ids.New("inv"), ids.New("job")
	err := e.api.Run(ctx, tenant.Scope{OrganizationID: e.org.ID, UserID: owner.UserID}, tenant.ReadWrite,
		func(ctx context.Context, tx tenant.Tx, _ tenant.Membership) error {
			if _, err := investigations.Insert(ctx, tx, investigations.Investigation{ID: inv, OrganizationID: e.org.ID, ModelID: e.model,
				BaseVersionID: e.base, Mode: investigations.Edit, Source: investigations.FromRequest, Intent: ptr("Cut the lead time"),
				OutputLocale: "en", ProposalModel: "muse-spark-1.3", PlannerModel: "muse-spark-1.3", PromptTemplateVersion: "investigator-0.1.0",
				Caps: investigations.CapsFor(investigations.Edit, false), CreatedBy: owner.UserID}); err != nil {
				return err
			}
			_, err := jobs.Enqueue(ctx, tx, jobs.New{ID: job, OrganizationID: e.org.ID, Type: jobs.Investigation,
				Resource: jobs.Resource{InvestigationID: inv}, Payload: map[string]any{"investigation_id": inv, "request_id": ids.New("req")},
				MaxAttempts: investigations.MaxAttempts}, fast)
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	got := e.get(t, e.org.ID, job)
	if got.Type != jobs.Investigation || got.Resource != (jobs.Resource{InvestigationID: inv}) || got.MaxAttempts != 5 || got.Status != jobs.Queued {
		t.Errorf("enqueued %+v", got)
	}
	// A loop of the other types leaves it queued; an agent lane's takes it.
	if _, _, ok := e.claim(t, e.org.ID, "w-rest"); ok {
		t.Fatal("a loop that does not handle investigations claimed one")
	}
	var claimed jobs.Job
	var ok bool
	if err := e.worker.Run(ctx, e.org.ID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		claimed, _, ok, err = jobs.Claim(ctx, tx, e.org.ID, "w-agent1", []jobs.Type{jobs.Investigation}, fast)
		return err
	}); err != nil || !ok {
		t.Fatalf("the agent lane's claim: %v %v", ok, err)
	}
	if claimed.ID != job || claimed.Resource.InvestigationID != inv || claimed.Attempt != 1 {
		t.Errorf("claimed %+v", claimed)
	}
}

func ptr[T any](v T) *T { return &v }
