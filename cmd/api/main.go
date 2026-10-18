// Command api is the composition root of the Go REST API (docs/ARCHITECTURE.md
// §2). It owns configuration and lifecycle only, never business rules: it
// validates configuration, connects to PostgreSQL as the API runtime login
// (refusing an unsafe one), builds the token verifier and serves
// internal/api's handler until SIGINT or SIGTERM.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kcl1104/geminus/internal/api"
	"github.com/kcl1104/geminus/internal/auth"
	"github.com/kcl1104/geminus/internal/config"
	"github.com/kcl1104/geminus/internal/tenant"
)

const (
	exitConfig   = 1
	exitDatabase = 3
	exitServe    = 4
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stderr, nil))
}

// run serves until ctx ends. ready, if not nil, receives the listening
// address once the server accepts connections (for tests).
func run(ctx context.Context, getenv func(string) string, stderr io.Writer, ready chan<- string) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil)).With("component", "api")
	cfg, err := config.Load(getenv)
	authCfg, authErr := config.LoadAuth(getenv)
	srvCfg, srvErr := config.LoadServer(getenv)
	if err = errors.Join(err, authErr, srvErr); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	verifier, err := auth.NewOIDCVerifier(authCfg)
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("invalid configuration", "error", "DATABASE_URL is not a valid PostgreSQL connection string")
		return exitConfig
	}
	poolCfg.AfterRelease = tenant.Guard
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("database unavailable", "error", "the connection pool could not be created")
		return exitDatabase
	}
	defer pool.Close()
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = tenant.CheckRuntimeRole(checkCtx, pool, tenant.APIRole)
	cancel()
	if err != nil {
		// The error names roles and attributes, never the URL or password.
		log.Error("refusing the database login", "error", err.Error())
		return exitDatabase
	}
	ln, err := net.Listen("tcp", srvCfg.Addr)
	if err != nil {
		log.Error("cannot listen", "addr", srvCfg.Addr, "error", err.Error())
		return exitServe
	}
	srv := &http.Server{
		Handler:           api.Handler(log, verifier, tenant.New(pool)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.Info("serving", "addr", ln.Addr().String())
	if ready != nil {
		ready <- ln.Addr().String()
	}
	select {
	case err := <-served:
		log.Error("server stopped", "error", err.Error())
		return exitServe
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Error("shutdown", "error", err.Error())
		return exitServe
	}
	log.Info("stopped")
	return 0
}
