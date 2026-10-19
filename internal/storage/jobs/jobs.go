// Package jobs is the PostgreSQL job queue (T-019; API_DATABASE §6–§7, D-007,
// D-011): the API enqueues a job in the same transaction as the resource it
// works on and its audit event; a worker claims one job at a time in an
// organization it is assigned (internal/tenant/workerscope), holds it with a
// lease it renews, and finishes it only while the lease is its own.
//
// A lease is fenced by (organization, job, worker, token): every later
// write names all four and needs the job still running with the lease
// unexpired, so a worker whose lease expired, or whose job another worker
// claimed since, changes nothing (ErrLeaseLost) and must stop. Work on the
// job's resource is the handler's (T-016, T-023, T-025): it locks the job
// with Lock in its finalizing transaction, so the result and the job's end
// commit together or not at all.
//
// Every statement filters by organization; RLS confines it as well.
package jobs

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Type is the kind of work (twin.jobs.type).
type Type string

const (
	Simulation     Type = "simulation"
	ModelProposal  Type = "model_proposal"
	ImportProfile  Type = "import_profile"
	ImportValidate Type = "import_validate"
	ImportMapping  Type = "import_mapping"
	Analysis       Type = "analysis"
)

// Status is a job's state.
type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Completed Status = "completed"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

var (
	// ErrInvalidJob: a job that cannot be enqueued (its type, resource or
	// payload); a caller's mistake, never sent to the database.
	ErrInvalidJob = errors.New("jobs: invalid job")
	// ErrLeaseLost: the lease is not this worker's any more (expired,
	// reaped, claimed again or finished). The worker stops; nothing was
	// written.
	ErrLeaseLost = errors.New("jobs: lease lost")
)

// Resource is the one row a job works on: exactly one ID is set, the one
// of the job's type.
type Resource struct {
	SimulationRunID, AIProposalID, ImportID, AIAnalysisID string
}

// New is a job to enqueue.
type New struct {
	ID, OrganizationID string
	Type               Type
	Resource           Resource
	// Payload is a JSON object: the frozen input reference the handler
	// needs, never secrets or raw customer data.
	Payload     map[string]any
	Priority    int
	MaxAttempts int // 1–10; 0 means Policy.MaxAttempts
}

// Job is a row of twin.jobs.
type Job struct {
	ID, OrganizationID string
	Type               Type
	Status             Status
	Resource           Resource
	Payload            []byte
	Priority           int
	Attempt            int
	MaxAttempts        int
	AvailableAt        time.Time
	LockedBy           string
	LeaseToken         int64
	LeaseExpiresAt     *time.Time
	Error              []byte
	CreatedAt          time.Time
}

// Lease is a worker's hold on a running job.
type Lease struct {
	OrganizationID, JobID, WorkerID string
	Token                           int64
}

// Policy is the queue's timing (ADR-024).
type Policy struct {
	// Lease is how long a claim or heartbeat holds a job.
	Lease time.Duration
	// MaxAttempts is a job's attempts unless New sets them.
	MaxAttempts int
	// BackoffBase and BackoffMax bound the wait before a retry: base,
	// doubling with each attempt, at most max.
	BackoffBase, BackoffMax time.Duration
}

// DefaultPolicy is ADR-024's: a 30 s lease renewed every 10 s by the
// worker, 3 attempts, retries after 5 s and 10 s (at most 5 min).
var DefaultPolicy = Policy{Lease: 30 * time.Second, MaxAttempts: 3, BackoffBase: 5 * time.Second, BackoffMax: 5 * time.Minute}

// Backoff is the wait before the retry that follows attempt (1-based).
func (p Policy) Backoff(attempt int) time.Duration {
	d := p.BackoffBase
	for i := 1; i < attempt && d < p.BackoffMax; i++ {
		d *= 2
	}
	return min(d, p.BackoffMax)
}

func (p Policy) valid() bool {
	return p.Lease > 0 && p.MaxAttempts >= 1 && p.MaxAttempts <= 10 && p.BackoffBase > 0 && p.BackoffMax >= p.BackoffBase
}

// Failure is why an attempt ended without a result. Retryable failures
// (a lost dependency, a timeout) are tried again while attempts remain;
// others (invalid input, a budget exceeded) end the job at once.
type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Error makes a Failure an error a handler can return.
func (f *Failure) Error() string { return f.Code + ": " + f.Message }

// LeaseExpired is the failure the reaper records.
const LeaseExpired = "LEASE_EXPIRED"

func (r Resource) of(t Type) bool {
	set := 0
	for _, id := range []string{r.SimulationRunID, r.AIProposalID, r.ImportID, r.AIAnalysisID} {
		if id != "" {
			set++
		}
	}
	if set != 1 {
		return false
	}
	switch t {
	case Simulation:
		return r.SimulationRunID != ""
	case ModelProposal:
		return r.AIProposalID != ""
	case ImportProfile, ImportValidate, ImportMapping:
		return r.ImportID != ""
	case Analysis:
		return r.AIAnalysisID != ""
	}
	return false
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Enqueue adds a queued job in tx, the transaction that creates or updates
// its resource and writes the audit event (one commit for all three).
func Enqueue(ctx context.Context, tx pgx.Tx, j New, p Policy) (Job, error) {
	if !p.valid() || j.ID == "" || j.OrganizationID == "" || !j.Resource.of(j.Type) || j.Payload == nil {
		return Job{}, ErrInvalidJob
	}
	if j.MaxAttempts == 0 {
		j.MaxAttempts = p.MaxAttempts
	}
	if j.MaxAttempts < 1 || j.MaxAttempts > 10 {
		return Job{}, ErrInvalidJob
	}
	payload, err := json.Marshal(j.Payload, json.Deterministic(true))
	if err != nil {
		return Job{}, fmt.Errorf("%w: payload: %v", ErrInvalidJob, err)
	}
	r := j.Resource
	return scan(tx.QueryRow(ctx, `INSERT INTO twin.jobs(id,organization_id,type,status,payload,simulation_run_id,ai_proposal_id,import_id,ai_analysis_id,priority,max_attempts)
		VALUES ($1,$2,$3,'queued',$4,$5,$6,$7,$8,$9,$10) RETURNING `+columns,
		j.ID, j.OrganizationID, string(j.Type), payload, nullable(r.SimulationRunID), nullable(r.AIProposalID), nullable(r.ImportID), nullable(r.AIAnalysisID),
		j.Priority, j.MaxAttempts))
}

const columns = `id, organization_id, type, status, payload, coalesce(simulation_run_id,''), coalesce(ai_proposal_id,''), coalesce(import_id,''),
	coalesce(ai_analysis_id,''), priority, attempt, max_attempts, available_at, coalesce(locked_by,''), lease_token, lease_expires_at, error, created_at`

func scan(row pgx.Row) (Job, error) {
	var j Job
	var typ, status string
	err := row.Scan(&j.ID, &j.OrganizationID, &typ, &status, &j.Payload, &j.Resource.SimulationRunID, &j.Resource.AIProposalID, &j.Resource.ImportID,
		&j.Resource.AIAnalysisID, &j.Priority, &j.Attempt, &j.MaxAttempts, &j.AvailableAt, &j.LockedBy, &j.LeaseToken, &j.LeaseExpiresAt, &j.Error, &j.CreatedAt)
	j.Type, j.Status = Type(typ), Status(status)
	return j, err
}

func millis(d time.Duration) int64 { return d.Milliseconds() }

// Claim takes the next available job of org, of one of the types the
// worker handles: highest priority, then oldest, then by ID; one whose
// attempts are spent is never taken, and a type no worker handles stays
// queued.
// Jobs other workers hold locked are skipped (SKIP LOCKED), so concurrent
// claims never take the same job. ok is false when there is none. The
// claim raises the attempt and the lease token: a token names one attempt.
func Claim(ctx context.Context, tx pgx.Tx, org, worker string, types []Type, p Policy) (j Job, l Lease, ok bool, err error) {
	if !p.valid() || worker == "" || len(types) == 0 {
		return Job{}, Lease{}, false, fmt.Errorf("jobs: invalid claim")
	}
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = string(t)
	}
	j, err = scan(tx.QueryRow(ctx, `WITH next_job AS (
		  SELECT id FROM twin.jobs
		  WHERE organization_id=$1 AND status='queued' AND available_at<=now() AND attempt<max_attempts
		    AND type=ANY($4)
		  ORDER BY priority DESC, created_at, id
		  FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE twin.jobs j SET status='running', attempt=attempt+1, locked_at=now(), locked_by=$2,
		  lease_token=lease_token+1, lease_expires_at=now()+($3::bigint*interval '1 millisecond'), heartbeat_at=now()
		FROM next_job n WHERE j.organization_id=$1 AND j.id=n.id
		RETURNING `+qualified, org, worker, millis(p.Lease), names))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Lease{}, false, nil
	}
	if err != nil {
		return Job{}, Lease{}, false, err
	}
	return j, Lease{OrganizationID: org, JobID: j.ID, WorkerID: worker, Token: j.LeaseToken}, true, nil
}

// qualified is columns for a statement where another table could make a
// name ambiguous.
const qualified = `j.id, j.organization_id, j.type, j.status, j.payload, coalesce(j.simulation_run_id,''), coalesce(j.ai_proposal_id,''),
	coalesce(j.import_id,''), coalesce(j.ai_analysis_id,''), j.priority, j.attempt, j.max_attempts, j.available_at, coalesce(j.locked_by,''),
	j.lease_token, j.lease_expires_at, j.error, j.created_at`

// fence is the condition every write under a lease needs.
const fence = `organization_id=$1 AND id=$2 AND locked_by=$3 AND lease_token=$4 AND status='running' AND lease_expires_at>now()`

// Heartbeat renews the lease for another Policy.Lease.
func Heartbeat(ctx context.Context, tx pgx.Tx, l Lease, p Policy) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.jobs SET heartbeat_at=now(), lease_expires_at=now()+($5::bigint*interval '1 millisecond')
		WHERE `+fence, l.OrganizationID, l.JobID, l.WorkerID, l.Token, millis(p.Lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Lock locks the job for the rest of tx if the lease is still the
// worker's: a finalizing transaction calls it first, writes the result,
// then Complete or Fail. The reaper skips a locked job, so it cannot take
// the job back between the check and the commit.
func Lock(ctx context.Context, tx pgx.Tx, l Lease) (Job, error) {
	j, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM twin.jobs WHERE `+fence+` FOR UPDATE`,
		l.OrganizationID, l.JobID, l.WorkerID, l.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrLeaseLost
	}
	return j, err
}

// Complete ends the job successfully.
func Complete(ctx context.Context, tx pgx.Tx, l Lease) error { return end(ctx, tx, l, Completed) }

// Cancel ends the job as cancelled: its handler stopped at a checkpoint
// because cancellation of its resource was requested (API_DATABASE §6).
func Cancel(ctx context.Context, tx pgx.Tx, l Lease) error { return end(ctx, tx, l, Cancelled) }

func end(ctx context.Context, tx pgx.Tx, l Lease, s Status) error {
	tag, err := tx.Exec(ctx, `UPDATE twin.jobs SET status=$5, locked_at=NULL, locked_by=NULL, lease_expires_at=NULL, error=NULL
		WHERE `+fence, l.OrganizationID, l.JobID, l.WorkerID, l.Token, string(s))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// CancelQueued cancels job id of org if it is still queued, in the
// transaction that cancels its resource: a queued job ends at once
// (API_DATABASE §6). It reports whether it did. A running job is stopped by
// its handler at a checkpoint instead (Cancel), and a finished one stays.
func CancelQueued(ctx context.Context, tx pgx.Tx, org, id string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE twin.jobs SET status='cancelled' WHERE organization_id=$1 AND id=$2 AND status='queued'`, org, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Fail ends the attempt with f. A retryable failure with attempts left
// queues the job again after Policy.Backoff; otherwise the job fails.
// retried says which.
func Fail(ctx context.Context, tx pgx.Tx, l Lease, f Failure, p Policy) (retried bool, err error) {
	if !p.valid() || f.Code == "" {
		return false, fmt.Errorf("jobs: invalid failure")
	}
	detail, err := json.Marshal(f, json.Deterministic(true))
	if err != nil {
		return false, err
	}
	var status string
	err = tx.QueryRow(ctx, `UPDATE twin.jobs SET
		  status=CASE WHEN $5 AND attempt<max_attempts THEN 'queued' ELSE 'failed' END,
		  available_at=CASE WHEN $5 AND attempt<max_attempts THEN now()+(($6::bigint[])[attempt]*interval '1 millisecond') ELSE available_at END,
		  locked_at=NULL, locked_by=NULL, lease_expires_at=NULL, error=$7
		WHERE `+fence+` RETURNING status`,
		l.OrganizationID, l.JobID, l.WorkerID, l.Token, f.Retryable, backoffs(p), detail).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrLeaseLost
	}
	return status == string(Queued), err
}

// backoffs is Policy.Backoff for attempts 1–10, in milliseconds, for SQL
// to index by the job's attempt.
func backoffs(p Policy) []int64 {
	out := make([]int64, 10)
	for i := range out {
		out[i] = millis(p.Backoff(i + 1))
	}
	return out
}

// Reap takes back the running jobs of org whose lease expired: the worker
// crashed or lost the database. A job with attempts left is queued again
// after Policy.Backoff, the others fail; both record LEASE_EXPIRED. At most
// limit jobs per call; jobs a finalizing transaction holds are skipped.
// The expired worker's token stays behind, so it can write nothing more.
func Reap(ctx context.Context, tx pgx.Tx, org string, limit int, p Policy) (requeued, failed int, err error) {
	if !p.valid() || limit < 1 {
		return 0, 0, fmt.Errorf("jobs: invalid reap")
	}
	detail, err := json.Marshal(Failure{Code: LeaseExpired, Message: "the worker's lease expired before the job finished", Retryable: true}, json.Deterministic(true))
	if err != nil {
		return 0, 0, err
	}
	rows, err := tx.Query(ctx, `WITH expired AS (
		  SELECT id FROM twin.jobs
		  WHERE organization_id=$1 AND status='running' AND lease_expires_at<=now()
		  ORDER BY lease_expires_at, id
		  FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE twin.jobs j SET
		  status=CASE WHEN j.attempt<j.max_attempts THEN 'queued' ELSE 'failed' END,
		  available_at=CASE WHEN j.attempt<j.max_attempts THEN now()+(($3::bigint[])[j.attempt]*interval '1 millisecond') ELSE j.available_at END,
		  locked_at=NULL, locked_by=NULL, lease_expires_at=NULL, error=$4
		FROM expired e WHERE j.organization_id=$1 AND j.id=e.id
		RETURNING j.status`, org, limit, backoffs(p), detail)
	if err != nil {
		return 0, 0, err
	}
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, s := range statuses {
		if s == string(Queued) {
			requeued++
		} else {
			failed++
		}
	}
	return requeued, failed, err
}

// Get returns the job id of org.
func Get(ctx context.Context, tx pgx.Tx, org, id string) (Job, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM twin.jobs WHERE organization_id=$1 AND id=$2`, org, id))
}
