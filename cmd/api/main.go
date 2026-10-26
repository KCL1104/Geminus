// Command api is the composition root of the Go REST API (docs/ARCHITECTURE.md
// §2). It owns configuration and lifecycle only, never business rules: it
// validates configuration, connects to PostgreSQL as the API runtime login
// (refusing an unsafe one), builds the token verifier and serves
// internal/api's handler, behind the access log (httpapi.AccessLog), until
// SIGINT or SIGTERM. Its log is observability's (T-035).
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
	"github.com/kcl1104/geminus/internal/httpapi"
	"github.com/kcl1104/geminus/internal/observability"
	"github.com/kcl1104/geminus/internal/observability/telemetry/export"
	"github.com/kcl1104/geminus/internal/proposals"
	"github.com/kcl1104/geminus/internal/storage/objects"
	"github.com/kcl1104/geminus/internal/tenant"
)

const (
	exitConfig   = 1
	exitDatabase = 3
	exitServe    = 4
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-probe" {
		// The container image's health check (T-039): the image has no
		// shell or HTTP client of its own.
		os.Exit(probe(os.Getenv))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Getenv, os.Stderr, nil))
}

// run serves until ctx ends. ready, if not nil, receives the listening
// address once the server accepts connections (for tests).
func run(ctx context.Context, getenv func(string) string, stderr io.Writer, ready chan<- string) int {
	log := newLogger(stderr)
	cfg, err := config.Load(getenv)
	authCfg, authErr := config.LoadAuth(getenv)
	srvCfg, srvErr := config.LoadServer(getenv)
	telCfg, telErr := config.LoadTelemetry(getenv)
	if err = errors.Join(err, authErr, srvErr, telErr); err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	// Traces and metrics leave the process only to an OTLP endpoint the
	// operator named (D-095 (b), ADR-028); otherwise this installs nothing.
	stopTelemetry, err := export.Setup(ctx, telCfg, "geminus-api", log)
	if err != nil {
		log.Error("invalid configuration", "error", "the telemetry exporter cannot be created: "+err.Error())
		return exitConfig
	}
	defer flush(stopTelemetry, log)
	verifier, err := auth.NewOIDCVerifier(authCfg)
	if err != nil {
		log.Error("invalid configuration", "error", err.Error())
		return exitConfig
	}
	opts := []api.Option{api.WithAI(aiAvailability(cfg))}
	store, err := objectStore(cfg)
	if err != nil {
		log.Error("invalid configuration", "error", "the object storage cannot be used: "+err.Error())
		return exitConfig
	}
	if store != nil {
		opts = append(opts, api.WithObjects(store))
	}
	poolCfg, err := poolConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("invalid configuration", "error", "DATABASE_URL is not a valid PostgreSQL connection string")
		return exitConfig
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("database unavailable", "error", "the connection pool could not be created")
		return exitDatabase
	}
	defer pool.Close()
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = tenant.CheckRuntimeRole(checkCtx, pool, tenant.APIRole)
	cancel()
	if errors.Is(err, tenant.ErrUnsafeRole) {
		// The error names roles and attributes, never the password.
		log.Error("refusing the database login", "error", err.Error())
		return exitDatabase
	}
	if err != nil {
		// pgx names the host, user and database, never the password.
		log.Error("database unavailable", "error", err.Error())
		return exitDatabase
	}
	handler, err := api.Handler(log, verifier, tenant.New(pool), opts...)
	if err != nil {
		log.Error("the domain packs are not valid", "error", err.Error())
		return exitConfig
	}
	ln, err := net.Listen("tcp", srvCfg.Addr)
	if err != nil {
		log.Error("cannot listen", "addr", srvCfg.Addr, "error", err.Error())
		return exitServe
	}
	// The platform's probes (T-039) sit outside the access log: a platform
	// polls them, and they carry no request of a user.
	srv := newServer(withProbes(httpapi.AccessLog(log, handler), pool.Ping), log)
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
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Error("shutdown", "error", err.Error())
		return exitServe
	}
	log.Info("stopped")
	return 0
}

// aiAvailability is whether new AI proposals are accepted (T-025): with the
// deterministic fake (development and tests only, config refuses it
// elsewhere) or an approved live provider; otherwise they are refused with
// the code AI_SPEC §2 names, before anything is stored. The API never
// calls a provider itself, and reads no key. Both live providers, Anthropic
// and Meta (T-055), are treated alike.
func aiAvailability(cfg config.Config) proposals.Availability {
	live := cfg.AIProvider == config.AIProviderAnthropic || cfg.AIProvider == config.AIProviderMeta
	switch {
	case cfg.AIProvider == config.AIProviderFake:
		return proposals.Available
	case live && cfg.AILive:
		return proposals.Available
	case live:
		return proposals.PolicyNotApproved
	}
	return proposals.NotConfigured
}

// objectStore opens the configured object storage (D-044): nil when it is
// disabled; the filesystem adapter only for development and tests; the
// S3-compatible bucket of a deployment (T-039).
func objectStore(cfg config.Config) (objects.Store, error) {
	switch o := cfg.ObjectStorage; o.Provider {
	case config.ObjectStorageFilesystem:
		fs, err := objects.NewFS(o.Root, cfg.Env)
		if err != nil {
			return nil, err
		}
		return fs, nil
	case config.ObjectStorageS3:
		s3, err := objects.NewS3(objects.S3Config{Endpoint: o.S3.Endpoint, Bucket: o.S3.Bucket, Region: o.S3.Region,
			AccessKeyID: o.S3.AccessKeyID, SecretAccessKey: o.S3.SecretAccessKey, PathStyle: o.S3.PathStyle})
		if err != nil {
			return nil, err
		}
		return s3, nil
	}
	return nil, nil
}

// probe asks this machine's API whether it is ready (GET /readyz on
// API_LISTEN_ADDR's port, loopback unless the address names a host),
// within 3 s: 0 when it answers 200, 1 otherwise. It prints nothing.
func probe(getenv func(string) string) int {
	srv, err := config.LoadServer(getenv)
	if err != nil {
		return 1
	}
	host, port, err := net.SplitHostPort(srv.Addr)
	if err != nil {
		return 1
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		return 1
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// readyTimeout bounds the readiness probe's database check.
const readyTimeout = 2 * time.Second

// withProbes serves the platform's probes (T-039) and passes every other
// request to next: GET /healthz is 200 while the process serves (liveness);
// GET /readyz is 200 while ready answers within readyTimeout and 503
// otherwise (readiness: the API's database). Neither needs a token; they
// say only "ok", "ready" or "unavailable", never why. They are not /v1
// operations (the contract's operations run in an organization), so they
// are not in openapi/openapi.yaml.
func withProbes(next http.Handler, ready func(context.Context) error) http.Handler {
	answer := func(w http.ResponseWriter, status int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only these two exact GETs are probes; everything else (another
		// method or path) is the API's, as before.
		switch {
		case r.Method != http.MethodGet:
			next.ServeHTTP(w, r)
		case r.URL.Path == "/healthz":
			answer(w, http.StatusOK, `{"status":"ok"}`)
		case r.URL.Path == "/readyz":
			ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
			defer cancel()
			if err := ready(ctx); err != nil {
				answer(w, http.StatusServiceUnavailable, `{"status":"unavailable"}`)
				return
			}
			answer(w, http.StatusOK, `{"status":"ready"}`)
		default:
			next.ServeHTTP(w, r)
		}
	})
}

// flush flushes and stops the telemetry, within telemetryGrace.
func flush(stop func(context.Context) error, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), telemetryGrace)
	defer cancel()
	if err := stop(ctx); err != nil {
		log.Warn("flushing the telemetry failed", "error", err.Error())
	}
}

// telemetryGrace is how long a stopping process waits for its last spans
// and metrics to be sent.
const telemetryGrace = 5 * time.Second

// newLogger is the API's log (T-035, D-093, D-094): JSON lines on stderr,
// every attribute through the privacy filter, every line of a request with
// its request ID.
func newLogger(stderr io.Writer) *slog.Logger { return observability.NewLogger(stderr, "api") }

// shutdownGrace is how long a shutdown waits for requests in progress.
const shutdownGrace = 20 * time.Second

// poolConfig is the API's connection pool: every connection released to it
// is checked for leftover tenant scope (tenant.Guard).
func poolConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.AfterRelease = tenant.Guard
	return cfg, nil
}

// newServer is the API's listener with the limits of ADR-019: slow or
// oversized requests and idle connections are cut off.
func newServer(h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
