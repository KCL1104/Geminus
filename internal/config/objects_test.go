package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// s3Env is a complete S3-compatible bucket (T-039, ADR-029), named as
// Railway's bucket credentials name it, in production; kv changes it.
func s3Env(kv ...string) func(string) string {
	m := map[string]string{
		"DATABASE_URL": validDB, "APP_ENV": "production", "OBJECT_STORAGE_PROVIDER": "s3",
		"OBJECT_STORAGE_S3_ENDPOINT": "https://storage.example", "OBJECT_STORAGE_S3_BUCKET": "geminus-demo-1a2b3c",
		"OBJECT_STORAGE_S3_REGION": "auto", "OBJECT_STORAGE_S3_ACCESS_KEY_ID": "tid_ACCESS",
		"OBJECT_STORAGE_S3_SECRET_ACCESS_KEY": "tsec_SECRET-SENTINEL",
	}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return env(m)
}

// T-039: the S3-compatible adapter is the object storage of a deployment
// (any APP_ENV, production included); the whole bucket is configured or
// none of it is, and no message repeats a value.
func TestLoadS3(t *testing.T) {
	cfg, err := Load(s3Env())
	want := ObjectStorage{Provider: ObjectStorageS3, S3: S3Storage{Endpoint: "https://storage.example", Bucket: "geminus-demo-1a2b3c",
		Region: "auto", AccessKeyID: "tid_ACCESS", SecretAccessKey: "tsec_SECRET-SENTINEL"}}
	if err != nil || cfg.ObjectStorage != want {
		t.Fatalf("%+v %v", cfg.ObjectStorage, err)
	}
	for style, path := range map[string]bool{"virtual-hosted": false, "path": true} {
		cfg, err := Load(s3Env("OBJECT_STORAGE_S3_URL_STYLE", style))
		if err != nil || cfg.ObjectStorage.S3.PathStyle != path {
			t.Errorf("%s: %+v %v", style, cfg.ObjectStorage, err)
		}
	}
	for _, e := range []string{"development", "test", ""} {
		if _, err := Load(s3Env("APP_ENV", e)); err != nil {
			t.Errorf("APP_ENV %q: %v", e, err)
		}
	}
	if _, err := Load(s3Env("OBJECT_STORAGE_S3_ENDPOINT", "http://127.0.0.1:9000")); err != nil {
		t.Errorf("plain http on loopback: %v", err)
	}
	for _, c := range []struct {
		name string
		kv   []string
		want []string
	}{
		{"nothing but the provider", []string{"OBJECT_STORAGE_S3_ENDPOINT", "", "OBJECT_STORAGE_S3_BUCKET", "", "OBJECT_STORAGE_S3_REGION", "",
			"OBJECT_STORAGE_S3_ACCESS_KEY_ID", "", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", ""},
			[]string{"OBJECT_STORAGE_S3_ENDPOINT:required", "OBJECT_STORAGE_S3_BUCKET:required", "OBJECT_STORAGE_S3_REGION:required",
				"OBJECT_STORAGE_S3_ACCESS_KEY_ID:required", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY:required"}},
		{"plain http elsewhere", []string{"OBJECT_STORAGE_S3_ENDPOINT", "http://storage.example"}, []string{"OBJECT_STORAGE_S3_ENDPOINT:invalid"}},
		{"an endpoint with a path", []string{"OBJECT_STORAGE_S3_ENDPOINT", "https://storage.example/bucket"}, []string{"OBJECT_STORAGE_S3_ENDPOINT:invalid"}},
		{"an endpoint with credentials", []string{"OBJECT_STORAGE_S3_ENDPOINT", "https://k:SECRET-SENTINEL@storage.example"}, []string{"OBJECT_STORAGE_S3_ENDPOINT:invalid"}},
		{"a bucket that is not a DNS label", []string{"OBJECT_STORAGE_S3_BUCKET", "Geminus_Demo"}, []string{"OBJECT_STORAGE_S3_BUCKET:invalid"}},
		{"a region with a slash", []string{"OBJECT_STORAGE_S3_REGION", "us/east"}, []string{"OBJECT_STORAGE_S3_REGION:invalid"}},
		{"a key with a newline", []string{"OBJECT_STORAGE_S3_ACCESS_KEY_ID", "tid_ACCESS\n"}, []string{"OBJECT_STORAGE_S3_ACCESS_KEY_ID:invalid"}},
		{"a secret with a space before it", []string{"OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", " tsec_SECRET-SENTINEL"}, []string{"OBJECT_STORAGE_S3_SECRET_ACCESS_KEY:invalid"}},
		{"an unknown URL style", []string{"OBJECT_STORAGE_S3_URL_STYLE", "Path"}, []string{"OBJECT_STORAGE_S3_URL_STYLE:invalid"}},
		{"a root as well", []string{"OBJECT_STORAGE_ROOT", "/srv/objects"}, []string{"OBJECT_STORAGE_ROOT:invalid"}},
		// A bucket's variables without the provider are a half-made
		// configuration: refused, not ignored.
		{"bucket variables while disabled", []string{"OBJECT_STORAGE_PROVIDER", "", "OBJECT_STORAGE_S3_URL_STYLE", "path"},
			[]string{"OBJECT_STORAGE_S3_ENDPOINT:invalid", "OBJECT_STORAGE_S3_BUCKET:invalid", "OBJECT_STORAGE_S3_REGION:invalid",
				"OBJECT_STORAGE_S3_ACCESS_KEY_ID:invalid", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY:invalid", "OBJECT_STORAGE_S3_URL_STYLE:invalid"}},
		{"a bucket variable beside the filesystem", []string{"OBJECT_STORAGE_PROVIDER", "filesystem", "OBJECT_STORAGE_ROOT", "/srv/objects", "APP_ENV", "test",
			"OBJECT_STORAGE_S3_BUCKET", "", "OBJECT_STORAGE_S3_REGION", "", "OBJECT_STORAGE_S3_ACCESS_KEY_ID", "", "OBJECT_STORAGE_S3_SECRET_ACCESS_KEY", ""},
			[]string{"OBJECT_STORAGE_S3_ENDPOINT:invalid"}},
	} {
		cfg, err := Load(s3Env(c.kv...))
		if err == nil {
			t.Errorf("%s: accepted %+v", c.name, cfg)
			continue
		}
		if got := codes(t, err); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if strings.Contains(err.Error(), "SECRET-SENTINEL") || strings.Contains(err.Error(), "tid_ACCESS") {
			t.Errorf("%s: the error repeats a value: %v", c.name, err)
		}
	}
	// The secret prints as [redacted] wherever the configuration is printed.
	cfg, _ = Load(s3Env())
	if out := fmt.Sprintf("%v %+v %#v", cfg, cfg, cfg.ObjectStorage); strings.Contains(out, "SECRET-SENTINEL") {
		t.Errorf("the configuration prints the secret: %s", out)
	}
}
