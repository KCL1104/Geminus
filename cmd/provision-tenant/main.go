// Command provision-tenant is the operator's controlled bootstrap of tenants
// (D-008, T-010): it creates a user, or an organization with its first
// owner. It is not part of the API and is never exposed over HTTP.
//
//	provision-tenant user -issuer ISSUER -subject SUBJECT
//	provision-tenant organization -name NAME -owner-issuer ISSUER -owner-subject SUBJECT
//
// The database comes from GEMINUS_PROVISION_DATABASE_URL: a login that may
// SET ROLE twin_provisioner and is neither the schema owner, a runtime login
// nor a superuser (D-042, db/README.md). The result is one JSON object on stdout with the IDs;
// an owner then adds other users by ID through the membership API.
package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kcl1104/geminus/internal/storage/postgres"
	"github.com/kcl1104/geminus/internal/storage/provisioning"
)

const (
	exitUsage    = 2
	exitDatabase = 3
	exitRefused  = 4
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "user" && args[0] != "organization") {
		fmt.Fprintln(stderr, "usage: provision-tenant user -issuer ISSUER -subject SUBJECT\n       provision-tenant organization -name NAME -owner-issuer ISSUER -owner-subject SUBJECT")
		return exitUsage
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	var issuer, subject, name *string
	if args[0] == "user" {
		issuer, subject = fs.String("issuer", "", "OIDC issuer"), fs.String("subject", "", "OIDC subject")
	} else {
		name = fs.String("name", "", "organization name")
		issuer, subject = fs.String("owner-issuer", "", "the first owner's OIDC issuer"), fs.String("owner-subject", "", "the first owner's OIDC subject")
	}
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return exitUsage
	}
	url := getenv("GEMINUS_PROVISION_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(stderr, "GEMINUS_PROVISION_DATABASE_URL is not set")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	db, err := postgres.Open(ctx, url)
	if err != nil {
		// pgx names the host, user and database, never the password.
		fmt.Fprintln(stderr, "database unavailable:", err)
		return exitDatabase
	}
	defer db.Close()
	if err := provisioning.CheckOperator(ctx, db); errors.Is(err, provisioning.ErrNotOperator) {
		fmt.Fprintln(stderr, "refusing the database login: it must be able to SET ROLE twin_provisioner, must not be a member of twin_owner, twin_api or twin_worker, and must not have SUPERUSER, BYPASSRLS, CREATEROLE, CREATEDB or REPLICATION")
		return exitRefused
	} else if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return exitDatabase
	}
	var out map[string]any
	if args[0] == "user" {
		id, created, err := provisioning.User(ctx, db, *issuer, *subject)
		if err != nil {
			return fail(stderr, err)
		}
		out = map[string]any{"user_id": id, "created": created}
	} else {
		org, user, err := provisioning.Organization(ctx, db, *name, *issuer, *subject)
		if err != nil {
			return fail(stderr, err)
		}
		out = map[string]any{"organization_id": org, "owner_user_id": user}
	}
	data, err := json.Marshal(out, json.Deterministic(true))
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, err)
	if errors.Is(err, provisioning.ErrInvalidInput) {
		return exitUsage
	}
	return exitDatabase
}
