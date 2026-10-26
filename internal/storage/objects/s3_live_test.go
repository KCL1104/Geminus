package objects

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kcl1104/geminus/internal/ids"
	"github.com/kcl1104/geminus/internal/secret"
)

// The real bucket keeps the contract the fake server models (T-039): run
// by the owner (or a session the owner authorizes) against the deployment's
// bucket, with the same OBJECT_STORAGE_S3_* variables the API and worker
// get and GEMINUS_LIVE_S3=1:
//
//	GEMINUS_LIVE_S3=1 OBJECT_STORAGE_S3_ENDPOINT=… OBJECT_STORAGE_S3_BUCKET=… \
//	  OBJECT_STORAGE_S3_REGION=auto OBJECT_STORAGE_S3_ACCESS_KEY_ID=… \
//	  OBJECT_STORAGE_S3_SECRET_ACCESS_KEY=… [OBJECT_STORAGE_S3_URL_STYLE=path] \
//	  go test -count=1 -run TestLiveBucket -v ./internal/storage/objects/
//
// It writes two tiny synthetic objects under a fresh organization ID that no
// tenant has, checks that a second PUT of a key is refused by the bucket
// itself (no existence check first: If-None-Match: * must hold, or the
// adapter could replace an object), reads one back, and deletes both.
// Skipped without GEMINUS_LIVE_S3=1; never run in CI.
func TestLiveBucket(t *testing.T) {
	if os.Getenv("GEMINUS_LIVE_S3") != "1" {
		t.Skip("set GEMINUS_LIVE_S3=1 and OBJECT_STORAGE_S3_* to check a real bucket")
	}
	s, err := NewS3(S3Config{
		Endpoint: os.Getenv("OBJECT_STORAGE_S3_ENDPOINT"), Bucket: os.Getenv("OBJECT_STORAGE_S3_BUCKET"),
		Region: os.Getenv("OBJECT_STORAGE_S3_REGION"), AccessKeyID: os.Getenv("OBJECT_STORAGE_S3_ACCESS_KEY_ID"),
		SecretAccessKey: secret.Secret(os.Getenv("OBJECT_STORAGE_S3_SECRET_ACCESS_KEY")),
		PathStyle:       os.Getenv("OBJECT_STORAGE_S3_URL_STYLE") == "path",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	org := ids.New("org")
	key, _ := NewKey(org, Assets, "t039-live-check", ids.New("art")+".txt")
	other, _ := NewKey(org, Assets, "t039-live-check", ids.New("art")+".txt")
	t.Cleanup(func() {
		for _, k := range []string{key, other} {
			if err := s.Delete(context.Background(), k); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("cleanup %s: %v", k, err)
			}
		}
	})
	data := []byte("synthetic T-039 bucket check")
	if _, err := s.Put(ctx, key, bytes.NewReader(data), 1024); err != nil {
		t.Fatal(err)
	}
	s.skipHead = true
	if _, err := s.Put(ctx, key, strings.NewReader("replaced"), 1024); !errors.Is(err, ErrExists) {
		t.Fatalf("the bucket did not refuse a second PUT of a key (If-None-Match: *): %v; do not use this bucket", err)
	}
	s.skipHead = false
	rc, err := s.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %q %v", got, err)
	}
	if _, err := s.Get(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing key: %v", err)
	}
	if _, err := s.Put(ctx, other, bytes.NewReader(nil), 0); err != nil {
		t.Errorf("an empty object: %v", err)
	}
}
