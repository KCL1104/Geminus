// Package snapshots reads frozen dataset snapshots from object storage
// (D-044, T-023): the datasets.Store a run's binding stage uses. The
// binding stage itself (internal/storage/datasets.Resolve) stays free of
// database and object I/O; this package is the adapter that gives it bytes.
package snapshots

import (
	"context"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/storage/datasets"
	"github.com/kcl1104/geminus/internal/storage/objects"
)

// Store is a datasets.Store over object storage: the snapshot of a dataset
// version is the object its row names (twin.dataset_versions.object_key).
// Keys holds those names, read with Keys in the organization's
// transaction, so the objects can be fetched after it ends (a run reads
// them outside its lease's transactions). A version without a key, or
// whose key is not one of the organization's, is datasets.ErrNotFound,
// never another tenant's object; Resolve still checks the bytes' hash.
type Store struct {
	Keys    map[string]string
	Objects objects.Store
}

var _ datasets.Store = Store{}

// Open reads the snapshot of dataset version datasetVersionID.
func (s Store) Open(ctx context.Context, organizationID, datasetVersionID string) (io.ReadCloser, error) {
	key, ok := s.Keys[datasetVersionID]
	if !ok || s.Objects == nil || !objects.ValidKey(organizationID, key) {
		return nil, datasets.ErrNotFound
	}
	rc, err := s.Objects.Get(ctx, key)
	if errors.Is(err, objects.ErrNotFound) {
		return nil, datasets.ErrNotFound
	}
	return rc, err
}

// Keys reads the object key of each dataset version manifest binds, in
// organization org. A version the organization cannot see is left out
// (Open then reports it missing).
func Keys(ctx context.Context, tx pgx.Tx, org string, manifest []model.DatasetBinding) (map[string]string, error) {
	keys := make(map[string]string, len(manifest))
	if len(manifest) == 0 {
		return keys, nil
	}
	versions := make([]string, len(manifest))
	for i, b := range manifest {
		versions[i] = b.DatasetVersionID
	}
	rows, err := tx.Query(ctx, `SELECT id, object_key FROM twin.dataset_versions WHERE organization_id=$1 AND id=ANY($2)`, org, versions)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		keys[id] = key
	}
	return keys, rows.Err()
}
