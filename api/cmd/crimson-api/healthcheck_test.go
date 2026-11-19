package main

import (
	"strings"
	"testing"

	"crimsonledger/internal/config"
)

func TestHealthcheck_UpAndDown(t *testing.T) {
	newHome(t, nil)
	base, stop, _ := startServer(t)
	if code, _, stderr := runCmd(t, "healthcheck", "--url", base+"/v1/health"); code != exitOK {
		t.Fatalf("healthy server: exit %d, %s", code, stderr)
	}
	stop()
	if code, _, _ := runCmd(t, "healthcheck", "--url", base+"/v1/health"); code != exitRuntime {
		t.Fatalf("stopped server: exit %d, want %d", code, exitRuntime)
	}
}

func TestHealthcheck_BadStatusFails(t *testing.T) {
	newHome(t, nil)
	base, _, _ := startServer(t)
	if code, _, _ := runCmd(t, "healthcheck", "--url", base+"/v1/nothing-here"); code != exitRuntime {
		t.Fatalf("404 must fail, exit %d", code)
	}
}

func TestHealthcheck_StrayArgument(t *testing.T) {
	newHome(t, nil)
	if code, _, stderr := runCmd(t, "healthcheck", "now"); code != exitUsage || !strings.Contains(stderr, "takes no arguments") {
		t.Fatalf("exit %d, %q", code, stderr)
	}
}

func TestHealthURL(t *testing.T) {
	tests := []struct {
		listen string
		tls    bool
		want   string
	}{
		{"127.0.0.1:8443", false, "http://127.0.0.1:8443/v1/health"},
		{"0.0.0.0:8443", false, "http://127.0.0.1:8443/v1/health"},
		{":8443", false, "http://127.0.0.1:8443/v1/health"},
		{"[::]:8443", false, "http://127.0.0.1:8443/v1/health"},
		{"192.168.1.20:8443", false, "http://192.168.1.20:8443/v1/health"},
		{"localhost:9", true, "https://localhost:9/v1/health"},
	}
	for _, tt := range tests {
		got, err := healthURL(config.APIConfig{Listen: tt.listen, TLSCert: map[bool]string{true: "c.pem"}[tt.tls]})
		if err != nil || got != tt.want {
			t.Errorf("%q: got %q, %v; want %q", tt.listen, got, err, tt.want)
		}
	}
	if _, err := healthURL(config.APIConfig{Listen: "nonsense"}); err == nil {
		t.Error("bad listen address must error")
	}
}
