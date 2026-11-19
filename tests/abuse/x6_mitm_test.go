//go:build integration

package abuse

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
)

// The ManInTheMiddle actor: a hostile server (or proxy) between the apps and the API. These run the real apiclient
// against throwaway servers, so they need no target, only the package's integration tag.

const canaryKey = "clk_v_CANARY000_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func mitmClient(t *testing.T, base string) *apiclient.Client {
	t.Helper()
	c, err := apiclient.New(apiclient.Options{BaseURL: base, APIKey: canaryKey, Timeout: 3 * time.Second,
		Sleep: func(context.Context, time.Duration) error { return context.Canceled }}) // never retry: one attempt each
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func noKey(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), canaryKey) {
		t.Errorf("error leaks the key: %v", err)
	}
}

// X601: a self-signed certificate is refused and the handler never sees a request (so no Authorization bytes).
func TestX601_UntrustedCertificateIsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, err := mitmClient(t, srv.URL).Charts(context.Background())
	if err == nil {
		t.Fatal("X601 self-signed certificate accepted")
	}
	noKey(t, err)
	if hits.Load() != 0 {
		t.Fatalf("X601 request reached the MITM server %d times", hits.Load())
	}
}

// X602: plain http:// to a non-loopback host is refused before anything is sent.
func TestX602_NoDowngradeToHTTP(t *testing.T) {
	for _, u := range []string{"http://api.example.com", "http://192.0.2.10:8443", "http://localhost.evil.example", "ftp://x", "https://user:pw@api.example.com"} {
		if _, err := apiclient.New(apiclient.Options{BaseURL: u, APIKey: canaryKey}); err == nil {
			t.Errorf("X602 %q accepted", u)
		}
	}
}

// X603: a redirect to another host, or from a listener to a different port/scheme, is not followed.
func TestX603_RedirectsAreNotFollowed(t *testing.T) {
	var attacker atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attacker.Add(1)
		if r.Header.Get("Authorization") != "" {
			attacker.Add(100)
		}
	}))
	defer evil.Close()
	for _, loc := range []string{evil.URL + "/v1/charts", "http://evil.example/v1/charts", "https://127.0.0.1:1/x", "//evil.example/x"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, loc, http.StatusTemporaryRedirect)
		}))
		_, err := mitmClient(t, srv.URL).Charts(context.Background())
		srv.Close()
		if err == nil {
			t.Errorf("X603 redirect to %q followed", loc)
		}
		noKey(t, err)
	}
	if attacker.Load() != 0 {
		t.Fatalf("X603 attacker host was contacted (%d)", attacker.Load())
	}
}

// X604: hostile response bodies are rejected without a crash.
func TestX604_HostileBodies(t *testing.T) {
	bodies := map[string]string{
		"huge":       `{"charts":[],"pad":"` + strings.Repeat("a", 5<<20) + `"}`,
		"deep":       strings.Repeat("[", 200000),
		"deep-obj":   strings.Repeat(`{"a":`, 100000),
		"nan":        `{"charts":NaN}`,
		"evil":       `{"charts":[{"mapId":"<script>alert(1)</script>","name":"‮\u0000"}]}`,
		"wrong-type": `{"charts":"x"}`,
		"empty":      ``,
		"bom":        "\xef\xbb\xbf{}",
	}
	for name, body := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		c := mitmClient(t, srv.URL)
		_, err := c.Charts(context.Background())
		_, serr := c.Sweep(context.Background(), apiclient.SweepParams{})
		srv.Close()
		if name != "evil" && name != "bom" && err == nil && serr == nil {
			t.Errorf("X604 %s: accepted by both Charts and Sweep", name)
		}
		noKey(t, err)
		noKey(t, serr)
	}
}

// X605: Set-Cookie and WWW-Authenticate are ignored: no cookie comes back, and a 401 challenge never makes the client
// send credentials differently.
func TestX605_CookiesAndChallengesIgnored(t *testing.T) {
	var cookies, auths atomic.Int32
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			cookies.Add(1)
		}
		if a := r.Header.Get("Authorization"); a != "Bearer "+canaryKey {
			auths.Add(1)
		}
		calls.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "evil"})
		w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthorized","message":"no","requestId":"r"}`))
	}))
	defer srv.Close()
	c := mitmClient(t, srv.URL)
	for i := 0; i < 3; i++ {
		_, err := c.Charts(context.Background())
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("X605 unexpected: %v", err)
		}
	}
	if cookies.Load() != 0 || auths.Load() != 0 {
		t.Fatalf("X605 cookies=%d odd-auth=%d over %d calls", cookies.Load(), auths.Load(), calls.Load())
	}
}
