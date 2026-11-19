package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crimsonledger/internal/config"
)

func TestTLS_SelfsignWritesFiles0600(t *testing.T) {
	home := newHome(t, nil)
	code, _, stderr := runCmd(t, "tls-selfsign", "--host", "127.0.0.1", "--host", "my-pc.local")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, name := range []string{"server.crt", "server.key"} {
		st, err := os.Stat(filepath.Join(home, "tls", name))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %o", name, st.Mode().Perm())
		}
	}
	if code, _, stderr := runCmd(t, "tls-selfsign", "--host", "127.0.0.1"); code != exitRuntime || !strings.Contains(stderr, "--force") {
		t.Fatalf("overwrite must need --force: %d %q", code, stderr)
	}
	if code, _, _ := runCmd(t, "tls-selfsign", "--host", "127.0.0.1", "--force"); code != exitOK {
		t.Fatal("--force should replace")
	}
}

func TestTLS_SelfsignNeedsHost(t *testing.T) {
	newHome(t, nil)
	if code, _, _ := runCmd(t, "tls-selfsign"); code != exitUsage {
		t.Fatalf("code %d", code)
	}
	if code, _, _ := runCmd(t, "tls-selfsign", "--host", "bad host/x"); code != exitRuntime {
		t.Fatalf("bad host code %d", code)
	}
}

func TestTLS_ClientTrustsOnlyWithCA(t *testing.T) {
	home := newHome(t, nil)
	if code, _, stderr := runCmd(t, "tls-selfsign", "--host", "127.0.0.1"); code != exitOK {
		t.Fatal(stderr)
	}
	cfg := dbInHome(home)
	cfg.TLSCert, cfg.TLSKey = filepath.Join(home, "tls", "server.crt"), filepath.Join(home, "tls", "server.key")
	if err := config.Save("api.json", cfg); err != nil {
		t.Fatal(err)
	}
	base, _, _ := startServer(t, "--listen", "127.0.0.1:0")
	base = strings.Replace(base, "http://", "https://", 1)

	pem, err := os.ReadFile(cfg.TLSCert)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("certificate is not valid PEM")
	}
	trusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	if _, _, err := get(t, trusting, base+"/v1/health", ""); err != nil {
		t.Fatalf("client with the CA must connect: %v", err)
	}

	if _, _, err := get(t, http.DefaultClient, base+"/v1/health", ""); err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("client without the CA must fail with unknown authority, got %v", err)
	}
	wrongHost := strings.Replace(base, "127.0.0.1", "localhost", 1)
	if _, _, err := get(t, trusting, wrongHost+"/v1/health", ""); err == nil || !strings.Contains(err.Error(), "not valid for") && !strings.Contains(err.Error(), "doesn't contain any IP SANs") && !strings.Contains(err.Error(), "certificate is valid for") {
		t.Fatalf("hostname mismatch must fail, got %v", err)
	}
}
