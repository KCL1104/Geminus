package generator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/snapshots"
)

// copies is a datasets.Store over the snapshots an attempt read before it
// asked the provider (H-003 review L3): the binding stage after the call
// reads them from memory, never from storage. An attempt holds its base's
// snapshots for the length of the call, as a run holds its samples.
type copies struct {
	data    map[string][]byte // by dataset version ID
	missing map[string]bool   // datasets.ErrNotFound when read
}

var _ datasets.Store = copies{}

// errNotCopied: the binding stage asked for a snapshot the attempt did not
// read first, a server bug.
var errNotCopied = errors.New("generator: a dataset snapshot was not read before the provider call")

func (c copies) Open(_ context.Context, _, datasetVersionID string) (io.ReadCloser, error) {
	if data, ok := c.data[datasetVersionID]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	if c.missing[datasetVersionID] {
		return nil, datasets.ErrNotFound
	}
	return nil, errNotCopied
}

// preload reads the snapshot of every dataset version the base version's
// manifest binds, at most datasets.MaxSnapshotBytes+1 bytes of each (what
// the binding stage reads), before the provider is asked. A storage
// failure is the attempt's DATASET_STORAGE_FAILED, retried while attempts
// remain: no provider call was made, so a retry costs none (D-061 (c)).
func (h *Handler) preload(ctx context.Context, org string, st started) (copies, error) {
	c := copies{data: map[string][]byte{}, missing: map[string]bool{}}
	manifest := st.version.DatasetBindings
	if len(manifest) == 0 {
		return c, nil
	}
	if h.cfg.Objects == nil {
		return copies{}, failure(DatasetStorageFailed, "the base version binds datasets and this worker has no object storage", false)
	}
	store := snapshots.Store{Keys: st.keys, Objects: h.cfg.Objects}
	for _, b := range manifest {
		dsv := b.DatasetVersionID
		if _, done := c.data[dsv]; done || c.missing[dsv] {
			continue
		}
		data, err := read(ctx, store, org, dsv)
		switch {
		case errors.Is(err, datasets.ErrNotFound):
			c.missing[dsv] = true
		case err != nil:
			if ctx.Err() != nil {
				return copies{}, err
			}
			h.cfg.Log.WarnContext(ctx, "reading a proposal's dataset snapshots failed", "organization_id", org, "proposal_id", st.proposal.ID, "error", err.Error())
			return copies{}, failure(DatasetStorageFailed, "the dataset snapshots could not be read", true)
		default:
			c.data[dsv] = data
		}
	}
	return c, nil
}

// read returns at most datasets.MaxSnapshotBytes+1 bytes of a snapshot.
func read(ctx context.Context, store datasets.Store, org, dsv string) ([]byte, error) {
	rc, err := store.Open(ctx, org, dsv)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, datasets.MaxSnapshotBytes+1))
	if cerr := rc.Close(); err == nil {
		err = cerr
	}
	return data, err
}

// verify runs the dataset binding stage on the base version itself, from
// the copies, before the provider is asked, and returns the manifest it
// verified (D-034 order). The base was verified when it was published; a
// snapshot that no longer verifies (missing, changed, another tenant's)
// would make every candidate invalid, so the attempt fails for good
// instead of asking the provider.
func (h *Handler) verify(ctx context.Context, org, proposal string, c copies, base *model.ModelIR, manifest []model.DatasetBinding) ([]model.DatasetBinding, error) {
	res, err := datasets.Resolve(ctx, c, org, base, manifest)
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, err
	case err != nil:
		h.cfg.Log.ErrorContext(ctx, "verifying a proposal's base datasets failed", "organization_id", org, "proposal_id", proposal, "error", err.Error())
		return nil, failure(DatasetStorageFailed, "the base version's dataset snapshots could not be verified", false)
	case !res.Valid():
		i := slices.IndexFunc(res.Issues, func(is model.ValidationIssue) bool { return is.Severity == model.SeverityError })
		h.cfg.Log.ErrorContext(ctx, "a proposal's base datasets do not verify", "organization_id", org, "proposal_id", proposal,
			"code", res.Issues[i].Code, "path", res.Issues[i].Path)
		return nil, failure(DatasetStorageFailed, "the base version's dataset snapshots do not verify", false)
	}
	return res.Manifest, nil
}
