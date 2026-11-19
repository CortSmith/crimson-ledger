package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/config"
)

// syncBuffer is a bytes.Buffer safe for the server goroutine and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newHome points the config home at a temp dir and writes an api.json whose database lives inside it.
func newHome(t *testing.T, edit func(*config.APIConfig)) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	cfg := config.APIDefaults()
	cfg.Listen = "127.0.0.1:0"
	cfg.DBPath = filepath.Join(home, "data", "ledger.db")
	if edit != nil {
		edit(&cfg)
	}
	if err := config.Save("api.json", cfg); err != nil {
		t.Fatal(err)
	}
	return home
}

func testIO() (IO, *syncBuffer, *syncBuffer) {
	out, errOut := &syncBuffer{}, &syncBuffer{}
	return IO{Stdout: out, Stderr: errOut}, out, errOut
}

// runCmd runs one short command and returns its exit code and captured output.
func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	io, out, errOut := testIO()
	code = run(context.Background(), args, io)
	return code, out.String(), errOut.String()
}

// startServer runs `serve` until the returned stop func is called and returns its base URL.
func startServer(t *testing.T, args ...string) (baseURL string, stop func() int, stderr *syncBuffer) {
	t.Helper()
	io, _, errOut := testIO()
	ready := make(chan string, 1)
	io.Ready = func(addr string) { ready <- addr }
	io.NoKeyCache = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- run(ctx, append([]string{"serve"}, args...), io) }()
	select {
	case addr := <-ready:
		scheme := "http://"
		if strings.Contains(strings.Join(args, " "), "tls") {
			scheme = "https://"
		}
		stopped := false
		stop = func() int {
			if stopped {
				return exitOK
			}
			stopped = true
			cancel()
			return <-done
		}
		t.Cleanup(func() { stop() })
		return scheme + addr, stop, errOut
	case code := <-done:
		cancel()
		t.Fatalf("serve exited early with %d: %s", code, errOut.String())
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("serve did not start in 10 s")
	}
	return "", nil, nil
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(&syncBuffer{}, nil)) }

// get performs a context-aware GET with an optional bearer token and returns the status and body.
func get(t *testing.T, client *http.Client, url, token string) (int, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}
