package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(func(string) string { return "" }, &stderr); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Fatalf("stderr does not name the missing variable: %s", stderr.String())
	}
}

func TestRunRefusesToStartUntilImplemented(t *testing.T) {
	var stderr bytes.Buffer
	getenv := func(k string) string {
		if k == "DATABASE_URL" {
			return "postgresql://twin_worker:db-pass-123@db.internal/twin"
		}
		return ""
	}
	if code := run(getenv, &stderr); code != exitNotImplemented {
		t.Fatalf("exit = %d, want %d", code, exitNotImplemented)
	}
	out := stderr.String()
	if !strings.Contains(out, "NOT IMPLEMENTED") || !strings.Contains(out, "T-019") {
		t.Fatalf("stderr does not explain what is missing: %s", out)
	}
	if strings.Contains(out, "db-pass-123") {
		t.Fatalf("stderr leaks the database password: %s", out)
	}
}
