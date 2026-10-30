package proposals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"regexp"
	"slices"
	"sync"

	"github.com/kcl1104/geminus/internal/ai/locale"
	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/model"
	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/model/validate"
	"github.com/kcl1104/geminus/internal/proposals/photo"
	"github.com/kcl1104/geminus/internal/storage/auditlog"
	"github.com/kcl1104/geminus/internal/storage/idempotency"
	"github.com/kcl1104/geminus/internal/storage/jobs"
	models "github.com/kcl1104/geminus/internal/storage/models"
	"github.com/kcl1104/geminus/internal/storage/objects"
	store "github.com/kcl1104/geminus/internal/storage/proposals"
	"github.com/kcl1104/geminus/internal/tenant"
)

// A proposal asked for with a photo or floor plan (T-056, H-029, H-033):
// createImageProposal. It is an edit proposal on the model's latest
// version, like createProposal's, without an intent: the photo is the
// request. Three steps, so that nothing is stored for a caller who may not
// ask and the file is read once:
//
//   - BeginImage checks, before the body is read, the caller's role (owner
//     or editor), that the photo path is on (Service.Photo, decided as the
//     worker decides it: the T-056 follow-up) and that photos can be
//     stored.
//   - CreateFromImage checks the fields, then whether the Idempotency-Key
//     was used before: a used key is only hashed, never decoded or stored
//     again, and replays the first answer (or is a conflict) whatever has
//     happened to the model since, as createProposal's (D-011; review
//     round 1, M1). For a new key it checks the base (404, 422, 409) before
//     anything is stored, then the image (internal/proposals/photo: PNG or
//     JPEG of the kinds it decodes, its size, its pixels; its pixels
//     encoded again, no metadata), stores that under a key the server makes
//     (tenant/<org>/assets/proposal-images/<proposal>/image), and commits
//     the proposal, its image record (migration 0017), its one
//     model_proposal job, its audit event and the idempotency response in
//     one transaction (D-011).
//
// An object stored by a request whose commit then fails is an orphan for
// the controlled maintenance D-044 leaves for later, as uploads' are. So is
// one stored by a request racing another with the same key in another API
// process: within a process, requests with one key run one after the
// other (imageKeys), so the later ones find the key used and store
// nothing (review round 1, L4). A request never deletes an object (D-044:
// only maintenance holds an objects.Maintainer).

const createImageOperation = "createImageProposal"

// keyLocks serialises, in this process, the photo requests of one
// Idempotency-Key from their first read to their commit. A request waits
// as long as its context allows; the map holds a key only while a request
// of it is running or waiting.
type keyLocks struct {
	mu   sync.Mutex
	held map[idempotency.Key]*keyLock
}

type keyLock struct {
	slot    chan struct{}
	waiting int
}

var imageKeys keyLocks

// lock takes k's slot, or returns ctx's error; release gives it back.
func (l *keyLocks) lock(ctx context.Context, k idempotency.Key) (release func(), err error) {
	l.mu.Lock()
	if l.held == nil {
		l.held = map[idempotency.Key]*keyLock{}
	}
	e := l.held[k]
	if e == nil {
		e = &keyLock{slot: make(chan struct{}, 1)}
		l.held[k] = e
	}
	e.waiting++
	l.mu.Unlock()
	done := func() {
		l.mu.Lock()
		if e.waiting--; e.waiting == 0 {
			delete(l.held, k)
		}
		l.mu.Unlock()
	}
	select {
	case e.slot <- struct{}{}:
		return func() { <-e.slot; done() }, nil
	case <-ctx.Done():
		done()
		return nil, ctx.Err()
	}
}

var (
	// ErrStorageUnavailable: no object storage is configured, so a photo
	// cannot be stored (503; D-044).
	ErrStorageUnavailable = errors.New("proposals: object storage is not configured")
	// ErrRetry: the key was used before this request read the file and is
	// free now (it expired in between): retry the request (503).
	ErrRetry = errors.New("proposals: retry the request")
)

// Codes of a refused photo, in the error's details (internal/proposals/http.go).
const (
	CodeImageFormatRejected = "IMAGE_FORMAT_REJECTED"
	CodeImageInvalid        = "IMAGE_INVALID"
	CodeImagePixels         = "IMAGE_PIXELS_EXCEEDED"
	// CodeImageUnsupported: the photo path is off (Service.Photo), so the
	// worker would not take the photo; the code it would fail the proposal
	// with (internal/proposals/generator's ImageUnsupported).
	CodeImageUnsupported = "AI_IMAGE_UNSUPPORTED"
)

// ImageFields are a photo request's form fields.
type ImageFields struct {
	BaseVersionID string
	OutputLocale  string
	// Model is the vision model the person chose (H-049); "" for the photo
	// path's.
	Model string
}

// checkModel reports a model that is not one of the contract's
// (ProposalModels) at /model; "" is the photo path's model.
func (f ImageFields) checkModel() error {
	if f.Model == "" || slices.Contains(ProposalModels, f.Model) {
		return nil
	}
	return fieldIssue("/model", "model is muse-spark-1.3 or muse-wonder-1.0")
}

var versionIDs = regexp.MustCompile(`^mv_[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// check reports what is wrong with f, and its locale.
func (f ImageFields) check() (locale.Locale, error) {
	var issues []model.ValidationIssue
	if !versionIDs.MatchString(f.BaseVersionID) {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/base_version_id", "base_version_id must be a model version ID", model.SeverityError))
	}
	loc, err := locale.Parse(f.OutputLocale)
	if err != nil {
		issues = append(issues, model.NewIssue(validate.ConstraintFailed, "/output_locale", "output_locale is en or zh-Hant", model.SeverityError))
	}
	var re *RequestError
	if errors.As(f.checkModel(), &re) {
		issues = append(issues, re.Issues...)
	}
	if len(issues) > 0 {
		return "", &RequestError{Issues: issues}
	}
	return loc, nil
}

// BeginImage checks, before the body is read, that the caller may ask for a
// proposal in the scope's organization, that the photo path is on and that
// photos can be stored. Nothing is stored.
func (s *Service) BeginImage(ctx context.Context, sc tenant.Scope) error {
	if err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		return m.Require(writeRoles...)
	}); err != nil {
		return err
	}
	if s.Photo != Available {
		return &UnavailableError{Availability: s.Photo, Photo: true}
	}
	if s.Objects == nil {
		return ErrStorageUnavailable
	}
	return nil
}

// checkBase reports whether baseVersionID is the latest version of the
// organization's model modelID (ErrNotFound, a RequestError at
// /base_version_id, ErrStaleBase).
func checkBase(ctx context.Context, tx tenant.Tx, org, modelID, baseVersionID string, lock bool) (models.Model, models.Version, error) {
	get := models.GetModel
	if lock {
		get = models.LockModel
	}
	mdl, err := get(ctx, tx, org, modelID)
	if errors.Is(err, models.ErrNotFound) {
		return models.Model{}, models.Version{}, ErrNotFound
	}
	if err != nil {
		return models.Model{}, models.Version{}, err
	}
	v, err := models.GetVersion(ctx, tx, org, baseVersionID)
	if errors.Is(err, models.ErrNotFound) {
		return models.Model{}, models.Version{}, ErrNotFound
	}
	if err != nil {
		return models.Model{}, models.Version{}, err
	}
	if v.ModelID != mdl.ID {
		return models.Model{}, models.Version{}, &RequestError{Issues: []model.ValidationIssue{model.NewIssue(validate.ConstraintFailed,
			"/base_version_id", "the version is not a version of this model", model.SeverityError)}}
	}
	if mdl.LatestVersionID != v.ID {
		return models.Model{}, models.Version{}, ErrStaleBase
	}
	return mdl, v, nil
}

// imageFingerprint is the request an Idempotency-Key stands for: the model,
// the fields and the uploaded file's SHA-256 and size (API_DATABASE §7). The
// model the person chose (H-049) is named only when it was sent, so a
// request without one has the fingerprint it had before.
func imageFingerprint(modelID string, f ImageFields, loc locale.Locale, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	fields := map[string]any{"model_id": modelID, "base_version_id": f.BaseVersionID, "output_locale": string(loc),
		"file_sha256": "sha256:" + hex.EncodeToString(sum[:]), "file_size": len(data)}
	if f.Model != "" {
		fields["model"] = f.Model
	}
	doc, err := json.Marshal(fields, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return hashing.Of(doc)
}

// CreateFromImage queues an edit proposal on modelID asked for with data,
// the file as the request carried it (at most photo.MaxBytes), or replays
// the response of the earlier request with the same Idempotency-Key (same
// model, fields and file: the same answer; anything else:
// idempotency.ErrConflict). It returns the HTTP status (202) and body.
func (s *Service) CreateFromImage(ctx context.Context, sc tenant.Scope, requestID, key, modelID string, f ImageFields, data []byte) (int, []byte, error) {
	if s.Objects == nil {
		return 0, nil, ErrStorageUnavailable
	}
	loc, err := f.check()
	if err != nil {
		return 0, nil, err
	}
	hash, err := imageFingerprint(modelID, f, loc, data)
	if err != nil {
		return 0, nil, err
	}
	k := idempotency.Key{Org: sc.OrganizationID, Actor: sc.UserID, Operation: createImageOperation, Key: key}
	// Requests with this key wait for each other here, in this process,
	// until the one before has committed or failed: the next then finds
	// the key used and only hashes the file (review round 1, L4).
	release, err := imageKeys.lock(ctx, k)
	if err != nil {
		return 0, nil, err
	}
	defer release()
	// Before anything is stored: the caller, whether the key was used
	// before (a replay or a conflict is decided from the hash, whatever has
	// happened to the model since: review round 1, M1), and, for a key not
	// used yet, the base.
	var seen bool
	if err := s.DB.Run(ctx, sc, tenant.ReadOnly, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		var err error
		if seen, err = idempotency.Seen(ctx, tx, k); err != nil || seen {
			return err
		}
		_, _, err = checkBase(ctx, tx, m.OrganizationID, modelID, f.BaseVersionID, false)
		return err
	}); err != nil {
		return 0, nil, err
	}
	var id string
	var stored *store.Image
	if !seen {
		im, err := photo.Sanitize(ctx, data)
		if err != nil {
			return 0, nil, err
		}
		id = ids.New("aip")
		objectKey, err := objects.NewKey(sc.OrganizationID, objects.Assets, "proposal-images", id, "image")
		if err != nil {
			return 0, nil, err
		}
		obj, err := s.Objects.Put(ctx, objectKey, bytes.NewReader(im.Data), photo.MaxBytes)
		if errors.Is(err, objects.ErrTooLarge) {
			return 0, nil, photo.ErrTooLarge
		}
		if err != nil {
			return 0, nil, err
		}
		if obj.SHA256 != im.SHA256 || obj.Size != im.Size() {
			return 0, nil, errors.New("proposals: the stored image is not the bytes the server wrote")
		}
		stored = &store.Image{ObjectKey: obj.Key, SHA256: obj.SHA256, Size: obj.Size, MediaType: im.MediaType, Width: im.Width, Height: im.Height}
	}
	var status int
	var out []byte
	err = s.DB.Run(ctx, sc, tenant.ReadWrite, func(ctx context.Context, tx tenant.Tx, m tenant.Membership) error {
		if err := m.Require(writeRoles...); err != nil {
			return err
		}
		replay, err := idempotency.Claim(ctx, tx, k, hash)
		if err != nil {
			return err
		}
		if replay != nil {
			status = replay.Status
			out, err = hashing.Canonical(replay.Body)
			return err
		}
		if stored == nil {
			return ErrRetry // only hashed, and the key is free after all
		}
		if s.Photo != Available {
			return &UnavailableError{Availability: s.Photo, Photo: true}
		}
		if err := s.checkModel(f.Model, true); err != nil {
			return err
		}
		// The model's row first, as every write about its versions takes
		// it (ADR-022).
		mdl, v, err := checkBase(ctx, tx, m.OrganizationID, modelID, f.BaseVersionID, true)
		if err != nil {
			return err
		}
		p, err := s.bind(m, mdl, v, store.Proposal{ID: id, Mode: "edit", OutputLocale: string(loc)})
		if err != nil {
			return err
		}
		if p, err = store.Insert(ctx, tx, p); err != nil {
			return err
		}
		if err := store.InsertImage(ctx, tx, m.OrganizationID, p.ID, *stored); err != nil {
			return err
		}
		if _, err := jobs.Enqueue(ctx, tx, proposalJob(m.OrganizationID, p.ID, requestID, false, f.Model), s.policy()); err != nil {
			return err
		}
		// IDs, the language and the stored image's hash and size: never the
		// file's name or content (SECURITY §9); the model the person chose
		// (H-049).
		meta := map[string]any{"model_id": p.ModelID, "base_version_id": p.BaseVersionID, "mode": p.Mode, "output_locale": p.OutputLocale,
			"selected_entities": 0, "source": "image", "image_content_hash": stored.SHA256, "image_size_bytes": stored.Size}
		if f.Model != "" {
			meta["requested_model"] = f.Model
		}
		if err := auditlog.AppendUser(ctx, tx, m.OrganizationID, m.UserID, auditlog.Event{
			Action: "proposal.create", TargetType: "ai_proposal", TargetID: p.ID, RequestID: requestID, Metadata: meta}); err != nil {
			return err
		}
		data, err := json.Marshal(Accepted{ProposalID: p.ID, Status: string(store.Generating)})
		if err != nil {
			return err
		}
		if out, err = hashing.Canonical(data); err != nil {
			return err
		}
		status = 202
		return idempotency.Complete(ctx, tx, k, idempotency.Response{Status: status, Body: out})
	})
	return status, out, err
}
