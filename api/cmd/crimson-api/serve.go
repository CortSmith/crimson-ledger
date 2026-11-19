package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/config"
	"crimsonledger/internal/store"
	"crimsonledger/internal/store/sqlitestore"
)

// Server limits from PLAN.md section 7.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 16 << 10
	defaultDrain      = 10 * time.Second // graceful shutdown budget (docs/tasks/T08)
	defaultPruneEvery = time.Hour
)

func runServe(ctx context.Context, args []string, io IO) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := fs.String("listen", "", "address to listen on (default: listen in api.json, 127.0.0.1:8443)")
	insecureLAN := fs.Bool("insecure-lan", false, "allow plain http on a non-loopback address (warns on every start)")
	if code, ok := parseFlags(fs, args, io.Stderr); !ok {
		return code
	}
	if fs.NArg() != 0 {
		return usageFail(io.Stderr, "serve", "takes no arguments, only flags")
	}
	cfg, err := config.Load("api.json", config.APIDefaults)
	if err != nil {
		return fail(io.Stderr, "serve", err)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if err := serve(ctx, cfg, *insecureLAN, io); err != nil {
		return fail(io.Stderr, "serve", err)
	}
	return exitOK
}

// serve opens the database, builds the handler and serves until ctx is cancelled.
func serve(ctx context.Context, cfg config.APIConfig, insecureLAN bool, io IO) error {
	log := slog.New(slog.NewTextHandler(io.Stderr, nil))
	mode, err := chooseBindMode(cfg.Listen, cfg.TLSCert != "", insecureLAN, os.Getenv(ContainerEnv) == "1")
	if err != nil {
		return err
	}
	tlsConf, err := loadTLS(cfg)
	if err != nil {
		return err
	}
	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	handler, err := buildHandler(st, cfg, io, log)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (is crimson-api already running?)", cfg.Listen, err)
	}
	if tlsConf != nil {
		ln = tls.NewListener(ln, tlsConf)
	}
	announce(ctx, st, ln, mode, io, log)
	go pruneLoop(ctx, st, cfg.RetentionDays, io, log)
	return serveUntilDone(ctx, newHTTPServer(handler), ln, io.drain(), log)
}

func (i IO) drain() time.Duration {
	if i.DrainBudget > 0 {
		return i.DrainBudget
	}
	return defaultDrain
}

func openStore(cfg config.APIConfig) (store.Store, error) {
	if cfg.Store != "sqlite" {
		return nil, fmt.Errorf("store %q is served by the AWS Lambda deployment, not crimson-api serve; set store to sqlite", cfg.Store)
	}
	path, err := cfg.ExpandedDBPath()
	if err != nil {
		return nil, err
	}
	st, err := sqlitestore.Open(path) // creates the file (0600) and runs the migrations
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return st, nil
}

func buildHandler(st store.Store, cfg config.APIConfig, io IO, log *slog.Logger) (http.Handler, error) {
	pepper, err := config.LoadOrCreatePepper()
	if err != nil {
		return nil, err
	}
	opts := apiserver.DefaultOptions()
	opts.MaxBodyBytes = cfg.MaxBodyBytes
	opts.RateLimitPerMin = cfg.RateLimitPerMin
	opts.TrustProxyHeaders = cfg.TrustProxyHeaders
	opts.Version = version
	if io.NoKeyCache {
		opts.KeyCacheTTL = 0
	}
	return apiserver.New(apiserver.Deps{Store: st, Pepper: pepper, Now: io.now, Log: log, Opts: opts})
}

func loadTLS(cfg config.APIConfig) (*tls.Config, error) {
	if cfg.TLSCert == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("load tls_cert/tls_key from api.json: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, nil
}

func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// announce prints the one-line banner and, when no key exists yet, how to make one.
func announce(ctx context.Context, st store.Store, ln net.Listener, mode bindMode, io IO, log *slog.Logger) {
	addr := ln.Addr().String()
	if mode == modeInsecureLAN {
		log.Warn("serving plain http beyond this machine: anyone on the network can read API keys in transit", "addr", addr)
	}
	scheme := "https"
	if mode != modeTLS {
		scheme = "http"
	}
	log.Info("crimson-api listening", "url", scheme+"://"+addr, "mode", string(mode), "version", version)
	if keys, err := st.Keys().List(ctx); err == nil && len(keys) == 0 {
		fmtLine(io, "no API keys yet. Create one with: crimson-api keys create --scope ingest --label my-pc")
	}
	if io.Ready != nil {
		io.Ready(addr)
	}
}

// serveUntilDone serves on ln and, when ctx ends, drains in-flight requests for up to drain.
func serveUntilDone(ctx context.Context, srv *http.Server, ln net.Listener, drain time.Duration, log *slog.Logger) error {
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down", "drain", drain.String())
	shutCtx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// pruneLoop deletes data older than the retention window on start and then on every tick.
func pruneLoop(ctx context.Context, st store.Store, retentionDays int, io IO, log *slog.Logger) {
	every := io.PruneEvery
	if every <= 0 {
		every = defaultPruneEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		cutoff := io.now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		if res, err := st.Prune(ctx, cutoff); err != nil {
			log.Error("prune failed", "err", err.Error())
		} else if res.Fixes+res.Kills+res.Batches > 0 {
			log.Info("pruned", "fixes", res.Fixes, "kills", res.Kills, "batches", res.Batches)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
