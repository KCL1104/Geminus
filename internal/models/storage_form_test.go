package models_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/model/hashing"
	"github.com/kcl1104/geminus/internal/models"
	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/tenant"
)

// storedIR is a draft's or a version's IR as PostgreSQL holds it, and the
// hash stored with it.
func (e *env) storedIR(t *testing.T, query string, arg string) ([]byte, string) {
	t.Helper()
	var ir []byte
	var hash string
	err := postgres.OpenPool(e.admin).Tx(context.Background(), pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, query, arg).Scan(&ir, &hash)
	})
	if err != nil {
		t.Fatal(err)
	}
	return ir, hash
}

// D-040: the IR is stored as the canonical JSON text its hash covers, so
// what is read back is byte for byte what was checked and hashed, at any
// size, and nothing grows on the way (T-012 review M4: jsonb printed this
// 1.9 MB draft as 83.6 MB; the reviewer's probe R11c).
func TestStoredIRIsTheCanonicalBytes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	rev := int64(1)
	for batch := range 4 {
		d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, rev, sinks(fmt.Sprintf("b%d_", batch), 30, 2300, "1e300"))
		if err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
		rev = d.Revision
	}
	d, err := e.svc.GetDraft(ctx, e.scope(tenant.Viewer), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.IR) < 1_800_000 {
		t.Fatalf("fixture: the draft is only %d bytes", len(d.IR))
	}
	stored, hash := e.storedIR(t, `SELECT ir, hash FROM twin.model_drafts WHERE model_id=$1`, m.ID)
	if !bytes.Equal(stored, d.IR) {
		t.Errorf("the stored draft is %d bytes, its canonical form %d", len(stored), len(d.IR))
	}
	if canonical, err := hashing.Canonical(stored); err != nil || !bytes.Equal(canonical, stored) {
		t.Errorf("the stored draft is not in canonical form (%v)", err)
	}
	if hashing.Sum(stored) != hash || hash != d.Hash {
		t.Errorf("the stored hash %s is not the stored bytes' %s (draft %s)", hash, hashing.Sum(stored), d.Hash)
	}

	_, out, err := e.publish(sc, m.ID, rev, m.LatestVersion.ID, key())
	if err != nil {
		t.Fatal(err)
	}
	var v models.Version
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	stored, hash = e.storedIR(t, `SELECT ir, hash FROM twin.model_versions WHERE id=$1`, v.ID)
	if !bytes.Equal(stored, d.IR) || hashing.Sum(stored) != hash {
		t.Errorf("the stored version is %d bytes with hash %s; the published IR is %d bytes with hash %s", len(stored), hash, len(d.IR), hashing.Sum(d.IR))
	}
}

// The database checks the hash on every write (migration 0005); the service
// checks it again on every read, so bytes the hash does not cover (a column
// that does not keep them, such as jsonb before 0005) are an error rather
// than served as the hashed IR.
func TestStoredIRMustMatchItsHash(t *testing.T) {
	ir := []byte(`{"model_id":"mdl_01M4YDQK00TA00000000000001","schema_version":"0.1"}`)
	got, err := models.StoredIR(ir, hashing.Sum(ir))
	if err != nil || !bytes.Equal(got, ir) {
		t.Errorf("the IR with its own hash: %s, %v", got, err)
	}
	spaced := bytes.ReplaceAll(ir, []byte(`,`), []byte(`, `))
	if _, err := models.StoredIR(spaced, hashing.Sum(ir)); err == nil {
		t.Error("the same value in other bytes was accepted under the original hash")
	}
}

// A patch whose result Go's deterministic encoding writes differently from
// RFC 8785 (-0; member names in U+E000-U+FFFF beside ones above U+FFFF,
// such as a fullwidth colon and a CJK Extension B character) is saved in
// canonical form with its hash, not refused by the hash check (T-013
// review M1).
func TestPatchedDraftIsCanonical(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	sc := e.scope(tenant.Editor)
	m := e.create(t, sc, createBody)
	rev := int64(1)
	for name, value := range map[string]string{
		"negative zero":       `{"z":-0}`,
		"UTF-16 member order": `{"\uff1a":1,"\ud840\udc00":2}`,
		"U+FFFD and an emoji": `{"\ufffd":1,"\ud83d\ude00":2}`,
	} {
		d, err := e.svc.PatchDraft(ctx, sc, requestID(), m.ID, rev, []byte(`[{"op":"add","path":"/metadata","value":`+value+`}]`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rev = d.Revision
		stored, hash := e.storedIR(t, `SELECT ir, hash FROM twin.model_drafts WHERE model_id=$1`, m.ID)
		canonical, err := hashing.Canonical(stored)
		if err != nil || !bytes.Equal(canonical, stored) || hashing.Sum(stored) != hash || !bytes.Equal(stored, d.IR) {
			t.Errorf("%s: the stored draft is not the canonical bytes of its hash (%v)", name, err)
		}
	}
}
