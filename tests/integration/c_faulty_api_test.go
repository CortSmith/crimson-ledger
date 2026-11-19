//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/apiclient"
	"crimsonledger/internal/domain"
	"crimsonledger/internal/testkit"
	"crimsonledger/internal/testkit/faultapi"
)

// C: a faulty or hostile API seen by the apps. Needs no deployment: faultapi runs in-process.
// Scope today: the shared `apiclient` (the layer both apps use for every call). The logger-pipeline and
// viewer-poller columns of the T18 table are F18-5 (they need the real-window and queue harnesses).
// Test names: TestC_<fault>_apiclient.

// fakeSleep records the waits the retry loop asks for instead of sleeping.
type fakeSleep struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (f *fakeSleep) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.waits = append(f.waits, d)
	f.mu.Unlock()
	return ctx.Err()
}

func clientFor(t *testing.T, url string, timeout time.Duration) (*apiclient.Client, *fakeSleep) {
	t.Helper()
	fs := &fakeSleep{}
	c, err := apiclient.New(apiclient.Options{BaseURL: url, APIKey: testkit.Canary, UserAgent: "crimson-ledger/it", Timeout: timeout, Sleep: fs.sleep})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c, fs
}

func sweepRP(c *apiclient.Client) (apiclient.SweepResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return c.Sweep(ctx, apiclient.SweepParams{Ruleset: domain.RulesetRP})
}

func TestC_5xxSequences_apiclient(t *testing.T) {
	for _, code := range []int{502, 503, 504} {
		srv := faultapi.New(t, faultapi.Status(code))
		c, fs := clientFor(t, srv.URL, time.Second)
		_, err := sweepRP(c)
		var ae *apiclient.APIError
		if !errors.As(err, &ae) || ae.Status != code || !apiclient.IsRetryable(err) {
			t.Fatalf("%d: want retryable APIError, got %v", code, err)
		}
		if srv.Requests() != 5 || len(fs.waits) != 4 {
			t.Fatalf("%d: want 5 attempts with 4 backoffs, got %d attempts %v", code, srv.Requests(), fs.waits)
		}
		for i := 1; i < len(fs.waits); i++ {
			if fs.waits[i] <= fs.waits[i-1] {
				t.Fatalf("%d: backoff not growing: %v", code, fs.waits)
			}
		}
	}
	srv := faultapi.New(t, faultapi.Status(500))
	c, _ := clientFor(t, srv.URL, time.Second)
	if _, err := sweepRP(c); err == nil || apiclient.IsRetryable(err) || srv.Requests() != 1 {
		t.Fatalf("500 must not be retried: err=%v requests=%d", err, srv.Requests())
	}
}

func TestC_ServerRecovers_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.Flaky(2, 3)) // 2 failures then success
	c, _ := clientFor(t, srv.URL, time.Second)
	if _, err := sweepRP(c); err != nil {
		t.Fatalf("client must ride out two 503s: %v", err)
	}
	if srv.Requests() != 3 {
		t.Fatalf("want 3 requests, got %d", srv.Requests())
	}
}

func TestC_RetryAfterHuge_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.RetryAfter(3600))
	c, fs := clientFor(t, srv.URL, time.Second)
	if _, err := sweepRP(c); err == nil {
		t.Fatal("want an error after the attempts are used up")
	}
	for _, w := range fs.waits {
		if w > 60*time.Second {
			t.Fatalf("Retry-After honoured beyond the 60 s cap: %v", fs.waits)
		}
	}
	// And it never blocks a cancel: Stop must return at once.
	c2, err := apiclient.New(apiclient.Options{BaseURL: srv.URL, APIKey: testkit.Canary, UserAgent: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c2.Sweep(ctx, apiclient.SweepParams{Ruleset: domain.RulesetRP})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancel did not interrupt the Retry-After wait: %v after %s", err, time.Since(start))
	}
}

func TestC_AuthRejected_apiclient(t *testing.T) {
	for _, code := range []int{401, 403} {
		srv := faultapi.New(t, faultapi.Status(code))
		c, _ := clientFor(t, srv.URL, time.Second)
		_, err := sweepRP(c)
		if !apiclient.IsAuth(err) || apiclient.IsRetryable(err) || srv.Requests() != 1 {
			t.Fatalf("%d: want a non-retryable auth error after one request, got %v (%d requests)", code, err, srv.Requests())
		}
	}
}

func TestC_SlowHeaders_apiclient(t *testing.T) {
	before := runtime.NumGoroutine()
	srv := faultapi.New(t, faultapi.SlowHeaders(5*time.Second))
	c, _ := clientFor(t, srv.URL, 300*time.Millisecond)
	start := time.Now()
	_, err := sweepRP(c)
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("per-attempt timeout did not fire: %v after %s", err, time.Since(start))
	}
	srv.Close()
	eventually(t, "goroutines to drain", 5*time.Second, func() bool { return runtime.NumGoroutine() <= before+3 })
}

func TestC_BadBodies_apiclient(t *testing.T) {
	cases := map[string]faultapi.Option{
		"truncated":       faultapi.Truncate(10),
		"closed mid body": faultapi.CloseMidBody(),
		"wrong type":      faultapi.WrongContentType(),
		"deep json":       faultapi.DeepJSON(100000),
		"nan coordinates": faultapi.NaNCoords(),
	}
	for name, opt := range cases {
		srv := faultapi.New(t, opt)
		c, _ := clientFor(t, srv.URL, time.Second)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: client panicked: %v", name, r)
				}
			}()
			res, err := sweepRP(c)
			if err == nil && name != "nan coordinates" {
				t.Fatalf("%s: want an error, got %+v", name, res)
			}
			if name == "nan coordinates" && err == nil {
				t.Fatalf("%s: NaN/Inf must not decode into a result: %+v", name, res)
			}
		}()
		if err := func() error { _, e := sweepRP(c); return e }(); err != nil && strings.Contains(err.Error(), testkit.Canary) {
			t.Fatalf("%s: error text leaks the key", name)
		}
	}
}

func TestC_SchemaDrift_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.SchemaDrift())
	c, _ := clientFor(t, srv.URL, time.Second)
	res, err := sweepRP(c)
	if err != nil {
		t.Fatalf("extra fields are allowed, got %v", err)
	}
	if len(res.Contacts) != 1 || res.Contacts[0].Name != "" {
		t.Fatalf("missing fields must stay empty, not be invented: %+v", res.Contacts)
	}
}

func TestC_HugeBody_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.HugeBody(100<<20))
	c, _ := clientFor(t, srv.URL, 10*time.Second)
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	_, err := sweepRP(c)
	runtime.ReadMemStats(&m1)
	if !errors.Is(err, apiclient.ErrResponseTooLarge) {
		t.Fatalf("want ErrResponseTooLarge, got %v", err)
	}
	if grew := int64(m1.TotalAlloc - m0.TotalAlloc); grew > 40<<20 {
		t.Fatalf("100 MB body allocated %d MB (limit 4 MiB + overhead)", grew>>20)
	}
}

func TestC_RedirectToAnotherHost_apiclient(t *testing.T) {
	var hits int
	var gotAuth string
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth = r.Header.Get("Authorization")
	}))
	t.Cleanup(other.Close)
	srv := faultapi.New(t, faultapi.Redirect(other.URL))
	c, _ := clientFor(t, srv.URL, time.Second)
	_, err := sweepRP(c)
	if !errors.Is(err, apiclient.ErrCrossHostRedirect) {
		t.Fatalf("want ErrCrossHostRedirect, got %v", err)
	}
	if hits != 0 || gotAuth != "" {
		t.Fatalf("the other host was contacted (%d hits) / received an Authorization header", hits)
	}
}

func TestC_EvilStrings_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.EvilStrings())
	c, _ := clientFor(t, srv.URL, time.Second)
	res, err := sweepRP(c)
	if err != nil {
		t.Fatalf("evil strings must decode as plain data: %v", err)
	}
	if len(res.Contacts) != 3 || !strings.Contains(res.Contacts[0].Name, "<img") {
		t.Fatalf("names must arrive verbatim as text (rendering escapes them, T14/T20): %+v", res.Contacts)
	}
}

func TestC_TLSBadCert_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.TLSBadCert())
	c, _ := clientFor(t, srv.URL, time.Second)
	_, err := sweepRP(c)
	if err == nil || apiclient.IsRetryable(err) {
		t.Fatalf("an untrusted certificate must fail and not be retried: %v", err)
	}
	if strings.Contains(err.Error(), testkit.Canary) {
		t.Fatal("certificate error leaks the key")
	}
	if srv.Requests() != 0 {
		t.Fatalf("a request (and so the key) reached a server with an untrusted certificate: %d", srv.Requests())
	}
}

func TestC_PlainHTTPNonLoopback_apiclient(t *testing.T) {
	for _, u := range []string{"http://203.0.113.9:8443", "http://example.com"} {
		if _, err := apiclient.New(apiclient.Options{BaseURL: u, APIKey: testkit.Canary}); err == nil {
			t.Fatalf("%s: http:// to a non-loopback host must be refused", u)
		}
	}
}

func TestC_HTTPSDowngrade_apiclient(t *testing.T) {
	srv := faultapi.New(t, faultapi.HTTPDowngrade())
	fs := &fakeSleep{}
	c, err := apiclient.New(apiclient.Options{BaseURL: srv.URL, APIKey: testkit.Canary, UserAgent: "x", Timeout: time.Second, Sleep: fs.sleep, CAFile: ""})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sweepRP(c); err == nil {
		t.Fatal("an https -> http redirect must not be followed")
	}
}
