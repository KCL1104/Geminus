// Command migrate applies the repository's migrations/ to a deployment's
// database (T-039, docs/runbooks/deploy.md), as its own step before the
// API and the worker start:
//
//	migrate [-dir DIR] up      apply what is pending
//	migrate [-dir DIR] status  report it (exit 6 while something is pending)
//
// The database comes from GEMINUS_MIGRATE_DATABASE_URL, never DATABASE_URL:
// a migration login that may act as twin_owner and is nothing else (no
// superuser, runtime or provisioning login; internal/storage/migrations).
// The runtime logins never migrate, and the migration login is given only
// to this step. The result is one JSON object on stdout: the versions
// applied now, the current version and what is still pending.
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

	"github.com/jackc/pgx/v5"

	"github.com/kcl1104/geminus/internal/storage/migrations"
)

const (
	exitUsage    = 2
	exitDatabase = 3
	exitRefused  = 4
	exitFailed   = 5
	exitPending  = 6
)

// timeout bounds one run, including the wait for another run's lock.
const timeout = 15 * time.Minute

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "migrations", "the directory of NNNN_name.up.sql files")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 || (fs.Arg(0) != "up" && fs.Arg(0) != "status") {
		fmt.Fprintln(stderr, "usage: migrate [-dir DIR] up|status")
		return exitUsage
	}
	ms, err := migrations.Load(os.DirFS(*dir))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	url := getenv("GEMINUS_MIGRATE_DATABASE_URL")
	if url == "" {
		fmt.Fprintln(stderr, "GEMINUS_MIGRATE_DATABASE_URL is not set (the migration login; never the runtime DATABASE_URL)")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		// pgx names the host, user and database, never the password.
		fmt.Fprintln(stderr, "database unavailable:", err)
		return exitDatabase
	}
	defer conn.Close(context.WithoutCancel(ctx))
	var done, applied []int
	if fs.Arg(0) == "up" {
		done, err = migrations.Apply(ctx, conn, ms)
		if err == nil {
			applied, err = migrations.Applied(ctx, conn, ms)
		}
	} else if err = migrations.CheckLogin(ctx, conn); err == nil {
		applied, err = migrations.Applied(ctx, conn, ms)
	}
	switch {
	case errors.Is(err, migrations.ErrUnsafeLogin):
		fmt.Fprintln(stderr, err)
		return exitRefused
	case err != nil:
		fmt.Fprintln(stderr, err)
		if len(done) > 0 {
			fmt.Fprintln(stderr, "applied before the failure:", done)
		}
		return exitFailed
	}
	pending := []int{}
	for _, m := range ms[len(applied):] {
		pending = append(pending, m.Version)
	}
	out, err := json.Marshal(map[string]any{"applied": nonNil(done), "current": len(applied), "pending": pending}, json.Deterministic(true))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailed
	}
	fmt.Fprintln(stdout, string(out))
	if len(pending) > 0 {
		return exitPending
	}
	return 0
}

func nonNil(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}
