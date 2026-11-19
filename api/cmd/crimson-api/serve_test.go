package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/config"
)

func TestServe_RefusesPublicBindWithoutTLS(t *testing.T) {
	newHome(t, func(c *config.APIConfig) { c.Listen = "0.0.0.0:0" })
	code, _, stderr := runCmd(t, "serve")
	if code != exitRuntime || !strings.Contains(stderr, "without TLS") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServe_AllowsInsecureLANWithWarning(t *testing.T) {
	newHome(t, func(c *config.APIConfig) { c.Listen = "0.0.0.0:0" })
	_, stop, stderr := startServer(t, "--insecure-lan")
	if code := stop(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if got := stderr.String(); !strings.Contains(got, "level=WARN") || !strings.Contains(got, "plain http beyond this machine") {
		t.Fatalf("no insecure-lan warning in %q", got)
	}
}

func TestServe_CreatesDefaultsAndMigrates(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	// First run with no api.json: only the default db_path (the real home) is avoided by --listen being the sole override.
	if err := config.Save("api.json", dbInHome(home)); err != nil {
		t.Fatal(err)
	}
	base, _, stderr := startServer(t)
	if status, _, err := get(t, http.DefaultClient, base+"/v1/health", ""); err != nil || status != http.StatusOK {
		t.Fatalf("health %d, %v", status, err)
	}
	for _, f := range []string{filepath.Join("data", "ledger.db"), config.PepperFile} {
		if _, err := os.Stat(filepath.Join(home, f)); err != nil {
			t.Errorf("%s not created: %v", f, err)
		}
	}
	if !strings.Contains(stderr.String(), "crimson-api keys create --scope ingest") {
		t.Errorf("banner lacks the first-key hint: %q", stderr.String())
	}
}

func dbInHome(home string) config.APIConfig {
	c := config.APIDefaults()
	c.Listen = "127.0.0.1:0"
	c.DBPath = filepath.Join(home, "data", "ledger.db")
	return c
}

func TestServe_WhenStoreIsDynamo_Refuses(t *testing.T) {
	newHome(t, func(c *config.APIConfig) { c.Store = "dynamodb" })
	if code, _, stderr := runCmd(t, "serve"); code != exitRuntime || !strings.Contains(stderr, "Lambda") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServe_WhenConfigGroupReadable_Refuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	home := newHome(t, nil)
	if err := os.Chmod(filepath.Join(home, "api.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCmd(t, "serve"); code != exitRuntime || !strings.Contains(stderr, "chmod 600") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServe_WhenPortInUse_SaysSo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	newHome(t, func(c *config.APIConfig) { c.Listen = ln.Addr().String() })
	code, _, stderr := runCmd(t, "serve")
	if code != exitRuntime || !strings.Contains(stderr, "cannot listen on") || strings.Contains(stderr, "goroutine") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}

func TestServe_FilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	home := newHome(t, nil)
	_, stop, _ := startServer(t)
	stop()
	for path, want := range map[string]os.FileMode{
		home: 0o700, filepath.Join(home, "data"): 0o700,
		filepath.Join(home, "api.json"): 0o600, filepath.Join(home, config.PepperFile): 0o600,
		filepath.Join(home, "data", "ledger.db"): 0o600,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", path, st.Mode().Perm(), want)
		}
	}
}

func TestServer_Timeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 15*time.Second ||
		srv.WriteTimeout != 15*time.Second || srv.IdleTimeout != 60*time.Second || srv.MaxHeaderBytes != 16<<10 {
		t.Fatalf("limits differ from PLAN.md section 7: %+v", srv)
	}
}

func TestServer_SlowHeadersAreCut(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	srv.ReadHeaderTimeout = 200 * time.Millisecond
	addr, stop := listenAndServe(t, srv)
	defer stop()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")) // never finishes the header block
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("connection was not closed by the server in time: %v", err)
	}
}

func TestServer_MaxHeaderBytes(t *testing.T) {
	addr, stop := listenAndServe(t, newHTTPServer(http.NotFoundHandler()))
	defer stop()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Big", strings.Repeat("a", 32<<10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status %d, want 431", resp.StatusCode)
	}
}

func listenAndServe(t *testing.T, srv *http.Server) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() { _ = srv.Close() }
}

func TestGracefulShutdown_LetsInFlightRequestFinish(t *testing.T) {
	started := make(chan struct{})
	srv := newHTTPServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveUntilDone(ctx, srv, ln, 5*time.Second, testLogger()) }()

	body := make(chan string, 1)
	go func() {
		_, b, err := get(t, http.DefaultClient, "http://"+ln.Addr().String(), "")
		if err != nil {
			b = "error: " + err.Error()
		}
		body <- b
	}()
	<-started
	cancel()
	if got := <-body; got != "done" {
		t.Fatalf("in-flight request got %q", got)
	}
	if err := <-served; err != nil {
		t.Fatalf("serveUntilDone: %v", err)
	}
}
