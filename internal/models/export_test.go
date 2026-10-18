package models

import (
	"context"
	"fmt"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	store "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/tenant"
)

// CommitVersion creates the model's next version from ir and manifest the
// way an approval will (T-026): not from the draft, then the draft rule of
// advance. It stands in for that path until it exists.
func (s *Service) CommitVersion(ctx context.Context, sc tenant.Scope, modelID string, ir []byte, manifest []model.DatasetBinding) (string, error) {
	canonical, err := hashing.Canonical(ir)
	if err != nil {
		return "", err
	}
	res, err := validate.IR(canonical)
	if err != nil || res.IR == nil {
		return "", fmt.Errorf("not a schema-valid IR: %v %v", err, res.Issues)
	}
	sorted, err := hashing.SortManifest(manifest)
	if err != nil {
		return "", err
	}
	var id string
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		mdl, err := store.LockModel(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		d, err := store.GetDraft(ctx, tx, m.OrganizationID, modelID)
		if err != nil {
			return err
		}
		base, err := store.GetVersion(ctx, tx, m.OrganizationID, mdl.LatestVersionID)
		if err != nil {
			return err
		}
		v, err := store.InsertVersion(ctx, tx, store.Version{ID: ids.New("mv"), OrganizationID: m.OrganizationID, ModelID: modelID,
			Version: base.Version + 1, SchemaVersion: "0.1", ParentVersionID: &base.ID, IR: canonical, Hash: hashing.Sum(canonical),
			DatasetBindings: sorted, CreatedBy: m.UserID})
		if err != nil {
			return err
		}
		id = v.ID
		return s.advance(ctx, tx, m, mdl, d, base, v, res.IR.Name)
	})
	return id, err
}
