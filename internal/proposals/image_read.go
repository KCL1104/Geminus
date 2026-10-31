package proposals

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// getProposalImage (H-052): the photo a proposal was asked for with, so the
// proposal page can show it with the boxes the segmenter found
// (generated_by.segmenter.boxes). Like the trace (D-044): any member of the
// proposal's organization, authorized in a transaction against the image's
// record (twin.ai_proposal_images, which the API role reads under RLS), then
// the object read after the transaction and served only when it is the
// record's (photo.Verify: digest, size, type, size in pixels; no pixel is
// decoded). No signed URL; the client names only the proposal.

var (
	// ErrNoImage: the proposal was asked for in words; it has no photo
	// (404, as a proposal that does not exist).
	ErrNoImage = errors.New("proposals: the proposal has no photo")
	// ErrImageCorrupt: the stored photo is missing, is not the one its
	// record names, or its key is not the organization's: nothing is
	// served (500, fail closed).
	ErrImageCorrupt = errors.New("proposals: the stored photo does not match its record")
	// ErrImageStorage: object storage could not be read while the request
	// was live (503: retry later); the cause is joined to it, never sent.
	ErrImageStorage = errors.New("proposals: the stored photo could not be read")
)

// Image returns the media type and bytes of the photo proposal id was asked
// for with, after checking the caller may read the proposal.
func (s *Service) Image(ctx context.Context, sc tenant.Scope, id string) (string, []byte, error) {
	var rec store.Image
	var org string
	err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(readRoles...); err != nil {
			return err
		}
		p, err := store.Get(ctx, tx, m.OrganizationID, id)
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if p.Image == nil {
			return ErrNoImage
		}
		rec, org = *p.Image, m.OrganizationID
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	data, err := readStoredImage(ctx, s.Objects, org, rec)
	if err != nil {
		return "", nil, err
	}
	return rec.MediaType, data, nil
}

// readStoredImage reads im's object of organization org from objs, no more
// than the record's size and one byte, and returns its bytes when they are
// the record's. A request that ended while it read gets its context's error
// as is (the client went away, or the deadline passed: not the storage's
// fault); any other read failure is ErrImageStorage (review round 1, L1, L2).
func readStoredImage(ctx context.Context, objs objects.Store, org string, im store.Image) ([]byte, error) {
	if objs == nil {
		return nil, ErrStorageUnavailable
	}
	if !objects.InArea(org, objects.Assets, im.ObjectKey) || im.Size < 1 || im.Size > photo.MaxBytes {
		return nil, ErrImageCorrupt
	}
	failed := func(err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Join(ErrImageStorage, err)
	}
	rc, err := objs.Get(ctx, im.ObjectKey)
	if errors.Is(err, objects.ErrNotFound) && ctx.Err() == nil {
		return nil, ErrImageCorrupt
	}
	if err != nil {
		return nil, failed(err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, im.Size+1))
	if err != nil {
		return nil, failed(err)
	}
	if photo.Verify(data, photo.Record{MediaType: im.MediaType, Width: im.Width, Height: im.Height, Size: im.Size, SHA256: im.SHA256}) != nil {
		return nil, ErrImageCorrupt
	}
	return data, nil
}

// image serves the photo itself (getProposalImage).
func (h handlers) image(w http.ResponseWriter, r *http.Request, sc tenant.Scope) error {
	id, err := pathID(r, proposalIDs, "proposal")
	if err != nil {
		return err
	}
	media, data, err := h.s.Image(r.Context(), sc, id)
	if err != nil {
		return Refused(err)
	}
	writeImage(w, media, data)
	return nil
}

// writeImage writes the stored bytes as their stored media type (image/png
// or image/jpeg), never sniffed and never kept by a cache: the photo is the
// organization's, and a member removed must not read it again from one.
func writeImage(w http.ResponseWriter, media string, data []byte) {
	h := w.Header()
	h.Set("Content-Type", media)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
