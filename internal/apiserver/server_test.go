package apiserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// fakeKeys is an in-memory store.KeyStore that counts calls.
type fakeKeys struct {
	mu      sync.Mutex
	recs    map[string]store.KeyRecord
	gets    int
	touches int
}

func (f *fakeKeys) Put(_ context.Context, k store.KeyRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs[k.ID] = k
	return nil
}
func (f *fakeKeys) Get(_ context.Context, id string) (store.KeyRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if k, ok := f.recs[id]; ok {
		return k, nil
	}
	return store.KeyRecord{}, store.ErrNotFound
}
func (f *fakeKeys) List(context.Context) ([]store.KeyRecord, error) { return nil, nil }
func (f *fakeKeys) SetDisabled(_ context.Context, id string, d bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := f.recs[id]
	k.Disabled = d
	f.recs[id] = k
	return nil
}
func (f *fakeKeys) Touch(context.Context, string, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touches++
	return nil
}

// fakeStore panics on every Store method except Keys, ApplyPing and Charts: the API core must not need
// data access. ApplyPing and Charts run the hooks a test sets and panic when it set none.
type fakeStore struct {
	store.Store
	keys      *fakeKeys
	applyPing func(context.Context, domain.Ping, time.Time) (store.ApplyResult, error)
	charts    func(context.Context) ([]domain.Chart, error)

	// Read hooks (T07); calls counts every data read so a 304 test can prove none happened.
	version func(domain.Ruleset) int64
	sweep   func(store.SweepQuery) []domain.SweepContact
	players func(domain.Ruleset, string, int) []store.PlayerSummary
	trail   func(domain.Ruleset, string, store.TrailFilter) (store.Trail, error)
	bounty  func(domain.Ruleset, int) []domain.KillTotal
	calls   int
}

func (f *fakeStore) Version(_ context.Context, rs domain.Ruleset) (int64, error) {
	if f.version == nil {
		return 1, nil
	}
	return f.version(rs), nil
}

func (f *fakeStore) Sweep(_ context.Context, q store.SweepQuery) ([]domain.SweepContact, error) {
	f.calls++
	return f.sweep(q), nil
}

func (f *fakeStore) SearchPlayers(_ context.Context, rs domain.Ruleset, q string, n int) ([]store.PlayerSummary, error) {
	f.calls++
	return f.players(rs, q, n), nil
}

func (f *fakeStore) Trail(_ context.Context, rs domain.Ruleset, g string, fl store.TrailFilter) (store.Trail, error) {
	f.calls++
	return f.trail(rs, g, fl)
}

func (f *fakeStore) Bounty(_ context.Context, rs domain.Ruleset, n int) ([]domain.KillTotal, error) {
	f.calls++
	return f.bounty(rs, n), nil
}

func (f *fakeStore) Keys() store.KeyStore { return f.keys }

func (f *fakeStore) ApplyPing(ctx context.Context, p domain.Ping, now time.Time) (store.ApplyResult, error) {
	return f.applyPing(ctx, p, now)
}

func (f *fakeStore) Charts(ctx context.Context) ([]domain.Chart, error) {
	f.calls++
	return f.charts(ctx)
}

type harness struct {
	h        http.Handler
	clock    *fakeClock
	keys     *fakeKeys
	tokens   map[string]string // name -> bearer token
	logs     *bytes.Buffer
	bodyRead int
	store    *fakeStore
}

func newHarness(t testing.TB, mod func(*Options)) *harness {
	t.Helper()
	hn := &harness{
		clock:  &fakeClock{t: time.Unix(1731800000, 0)},
		keys:   &fakeKeys{recs: map[string]store.KeyRecord{}},
		tokens: map[string]string{},
		logs:   &bytes.Buffer{},
	}
	addKey := func(name string, scope Scope, edit func(*store.KeyRecord)) {
		token, rec, err := GenerateKey(scope, testPepper, rand.Reader, hn.clock.Now(), 0)
		if err != nil {
			t.Fatal(err)
		}
		rec.Label = name
		if edit != nil {
			edit(&rec)
		}
		hn.keys.recs[rec.ID] = rec
		hn.tokens[name] = token
	}
	addKey("ingest", ScopeIngest, nil)
	addKey("view", ScopeView, nil)
	addKey("disabled", ScopeView, func(r *store.KeyRecord) { r.Disabled = true })
	addKey("expired", ScopeView, func(r *store.KeyRecord) {
		past := hn.clock.Now().Add(-time.Minute)
		r.ExpiresAt = &past
	})

	opts := DefaultOptions()
	opts.Version = "test"
	opts.KeyCacheTTL = 0
	if mod != nil {
		mod(&opts)
	}
	hn.store = &fakeStore{keys: hn.keys}
	srv, err := newServer(Deps{
		Store:  hn.store,
		Pepper: testPepper,
		Now:    hn.clock.Now,
		Log:    slog.New(slog.NewTextHandler(hn.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Opts:   opts,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		hn.bodyRead += int(n)
		if err != nil {
			writeBodyError(w, r, err)
			return
		}
		ok.ServeHTTP(w, r)
	})
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom " + hn.tokens["view"]) })
	hn.h = srv.handler(append(srv.routes(),
		route{"POST /v1/stub/ingest", ScopeIngest, ok},
		route{"GET /v1/stub/view", ScopeView, ok},
		route{"POST /v1/stub/echo", ScopeIngest, echo},
		route{"GET /v1/stub/panic", ScopeView, boom},
	))
	return hn
}

var ipCounter int

func (hn *harness) do(method, path, auth, remote string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if remote == "" {
		remote = "192.0.2.1:5000"
	}
	req.RemoteAddr = remote
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	hn.h.ServeHTTP(rec, req)
	return rec
}

func (hn *harness) get(path, name string) *httptest.ResponseRecorder {
	auth := ""
	if tok, ok := hn.tokens[name]; ok {
		auth = "Bearer " + tok
	}
	return hn.do("GET", path, auth, "", nil)
}

type errResp struct {
	Error struct{ Code, Message, RequestID string } `json:"error"`
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) errResp {
	t.Helper()
	var e errResp
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("body is not the error shape: %v: %q", err, rec.Body.String())
	}
	return e
}

func TestAuth(t *testing.T) {
	hn := newHarness(t, nil)
	view := hn.tokens["view"]
	rows := []struct {
		name, method, path, auth string
		status                   int
		code                     string
	}{
		{"no header", "GET", "/v1/stub/view", "", 401, "unauthorized"},
		{"basic scheme", "GET", "/v1/stub/view", "Basic x", 401, "unauthorized"},
		{"bearer garbage", "GET", "/v1/stub/view", "Bearer garbage", 401, "unauthorized"},
		{"bearer empty", "GET", "/v1/stub/view", "Bearer ", 401, "unauthorized"},
		{"unknown id", "GET", "/v1/stub/view", "Bearer clk_v_zzzzzzzz_" + strings.Repeat("A", 43), 401, "unauthorized"},
		{"wrong secret", "GET", "/v1/stub/view", "Bearer " + view[:15] + strings.Repeat("A", 43), 401, "unauthorized"},
		{"view key on ingest route", "POST", "/v1/stub/ingest", "Bearer " + view, 403, "forbidden"},
		{"ingest key on view route", "GET", "/v1/stub/view", "Bearer " + hn.tokens["ingest"], 403, "forbidden"},
		{"disabled key", "GET", "/v1/stub/view", "Bearer " + hn.tokens["disabled"], 403, "key_disabled"},
		{"expired key", "GET", "/v1/stub/view", "Bearer " + hn.tokens["expired"], 403, "key_disabled"},
		{"valid ingest", "POST", "/v1/stub/ingest", "Bearer " + hn.tokens["ingest"], 200, ""},
		{"valid view", "GET", "/v1/stub/view", "Bearer " + view, 200, ""},
		{"lowercase scheme", "GET", "/v1/stub/view", "bearer " + view, 200, ""},
	}
	for i, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			rec := hn.do(row.method, row.path, row.auth, fmt.Sprintf("198.51.100.%d:1", i+1), nil)
			if rec.Code != row.status {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, row.status, rec.Body)
			}
			if row.code != "" {
				if e := decodeErr(t, rec); e.Error.Code != row.code || e.Error.RequestID == "" {
					t.Errorf("error = %+v, want code %q and a requestId", e, row.code)
				}
			}
		})
	}
}

func TestAuth_All401BodiesAreIdentical(t *testing.T) {
	hn := newHarness(t, nil)
	view := hn.tokens["view"]
	autos := []string{"", "Basic x", "Bearer garbage", "Bearer clk_v_zzzzzzzz_" + strings.Repeat("A", 43), "Bearer " + view[:15] + strings.Repeat("A", 43)}
	var first string
	for i, auth := range autos {
		rec := hn.do("GET", "/v1/stub/view", auth, fmt.Sprintf("198.51.100.%d:1", i+1), nil)
		if rec.Code != 401 {
			t.Fatalf("row %d status %d", i, rec.Code)
		}
		e := decodeErr(t, rec)
		e.Error.RequestID = ""
		got := fmt.Sprintf("%+v|%s", e, rec.Header().Get("WWW-Authenticate"))
		if i == 0 {
			first = got
		} else if got != first {
			t.Errorf("401 for row %d differs: %q vs %q", i, got, first)
		}
	}
}

func TestAuth_NoEndpointOtherThanHealthIsOpen(t *testing.T) {
	hn := newHarness(t, nil)
	for _, p := range []string{"/v1/stub/view", "/v1/stub/panic"} {
		if rec := hn.do("GET", p, "", "", nil); rec.Code != 401 {
			t.Errorf("GET %s without key = %d, want 401", p, rec.Code)
		}
	}
}

func TestAuth_RunsBeforeBodyIsRead(t *testing.T) {
	hn := newHarness(t, nil)
	counter := &countingReader{r: io.LimitReader(zeros{}, 100<<20)}
	req := httptest.NewRequest("POST", "/v1/stub/echo", counter)
	req.ContentLength = -1
	req.Header.Set("Authorization", "Bearer clk_i_zzzzzzzz_"+strings.Repeat("A", 43))
	rec := httptest.NewRecorder()
	hn.h.ServeHTTP(rec, req)
	if rec.Code != 401 || counter.n >= 1024 {
		t.Errorf("status %d, body bytes read %d: auth must reject before reading the body", rec.Code, counter.n)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) { n, err := c.r.Read(p); c.n += n; return n, err }

func TestRateLimit_PerIP(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.RateLimitPerMin = 60 }) // burst 10
	for i := range 10 {
		if rec := hn.do("GET", "/v1/health", "", "203.0.113.5:1", nil); rec.Code != 200 {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := hn.do("GET", "/v1/health", "", "203.0.113.5:1", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || decodeErr(t, rec).Error.Code != "rate_limited" {
		t.Fatalf("11th request = %d retry %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := hn.do("GET", "/v1/health", "", "203.0.113.6:1", nil); rec.Code != 200 {
		t.Errorf("another IP = %d, want 200", rec.Code)
	}
}

func TestRateLimit_PerKey(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.RateLimitPerMin = 60 })
	auth := "Bearer " + hn.tokens["view"]
	for i := range 10 {
		if rec := hn.do("GET", "/v1/stub/view", auth, fmt.Sprintf("203.0.113.%d:1", i+10), nil); rec.Code != 200 {
			t.Fatalf("request %d = %d", i, rec.Code)
		}
	}
	rec := hn.do("GET", "/v1/stub/view", auth, "203.0.113.99:1", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("11th request on one key from fresh IPs = %d, want 429 with Retry-After", rec.Code)
	}
}

func TestFailedAuthLockout(t *testing.T) {
	hn := newHarness(t, nil)
	for i := range failedAuthMax {
		if rec := hn.do("GET", "/v1/stub/view", "Bearer garbage", "203.0.113.5:1", nil); rec.Code != 401 {
			t.Fatalf("failure %d = %d", i, rec.Code)
		}
	}
	good := "Bearer " + hn.tokens["view"]
	rec := hn.do("GET", "/v1/stub/view", good, "203.0.113.5:1", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("correct key from a locked IP = %d, want 429 with Retry-After", rec.Code)
	}
	if rec := hn.do("GET", "/v1/stub/view", good, "203.0.113.6:1", nil); rec.Code != 200 {
		t.Errorf("other IP = %d, want 200", rec.Code)
	}
}

func TestFailedAuthLockout_NotBypassedByForwardedFor(t *testing.T) {
	hn := newHarness(t, nil) // TrustProxyHeaders is false
	for i := range failedAuthMax {
		req := httptest.NewRequest("GET", "/v1/stub/view", nil)
		req.RemoteAddr = "203.0.113.5:1"
		req.Header.Set("Authorization", "Bearer garbage")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		hn.h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if rec := hn.do("GET", "/v1/health", "", "203.0.113.5:1", nil); rec.Code != 429 {
		t.Errorf("lockout bypassed through X-Forwarded-For: status %d", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	rows := []struct {
		name, remote string
		xff          []string
		trust        bool
		want         string
	}{
		{"remote addr only", "198.51.100.7:4000", nil, false, "198.51.100.7"},
		{"spoofed header ignored when untrusted", "198.51.100.7:4000", []string{"1.2.3.4"}, false, "198.51.100.7"},
		{"trusted uses last entry", "198.51.100.7:4000", []string{"1.1.1.1, 2.2.2.2"}, true, "2.2.2.2"},
		{"trusted uses last header line", "198.51.100.7:4000", []string{"1.1.1.1", "3.3.3.3"}, true, "3.3.3.3"},
		{"trusted without header", "198.51.100.7:4000", nil, true, "198.51.100.7"},
		{"trusted garbage entry", "198.51.100.7:4000", []string{"1.1.1.1, not-an-ip"}, true, "198.51.100.7"},
		{"ipv6 remote", "[2001:db8::1]:4000", nil, false, "2001:db8::1"},
		{"remote without port", "198.51.100.7", nil, false, "198.51.100.7"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = row.remote
			for _, v := range row.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIPOf(req, row.trust); got != row.want {
				t.Errorf("clientIPOf = %q, want %q", got, row.want)
			}
		})
	}
}

func TestHeaders_OnEveryResponse(t *testing.T) {
	hn := newHarness(t, nil)
	good := "Bearer " + hn.tokens["view"]
	reqs := []struct{ name, method, path, auth string }{
		{"health", "GET", "/v1/health", ""},
		{"401", "GET", "/v1/stub/view", ""},
		{"404", "GET", "/v1/nope", ""},
		{"405", "POST", "/v1/health", ""},
		{"ok with key", "GET", "/v1/stub/view", good},
		{"500", "GET", "/v1/stub/panic", good},
	}
	for _, r := range reqs {
		t.Run(r.name, func(t *testing.T) {
			rec := hn.do(r.method, r.path, r.auth, "", nil)
			h := rec.Header()
			want := map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "no-store",
				"Content-Type":           "application/json; charset=utf-8",
			}
			for k, v := range want {
				if h.Get(k) != v {
					t.Errorf("%s = %q, want %q", k, h.Get(k), v)
				}
			}
			if h.Get("X-Request-Id") == "" {
				t.Error("missing X-Request-Id")
			}
			for _, k := range []string{"Server", "X-Powered-By", "Access-Control-Allow-Origin"} {
				if h.Get(k) != "" {
					t.Errorf("%s must not be set, got %q", k, h.Get(k))
				}
			}
		})
	}
}

func TestRouting_ErrorsUseTheStandardShape(t *testing.T) {
	hn := newHarness(t, nil)
	rows := []struct {
		name, method, path string
		status             int
		code, allow        string
	}{
		{"unknown path", "GET", "/v1/nope", 404, "not_found", ""},
		{"wrong method", "POST", "/v1/health", 405, "method_not_allowed", "GET"},
		{"HEAD", "HEAD", "/v1/health", 405, "method_not_allowed", "GET"},
		{"OPTIONS", "OPTIONS", "/v1/health", 405, "method_not_allowed", "GET"},
		{"OPTIONS unknown", "OPTIONS", "/v1/nope", 404, "not_found", ""},
		{"unclean path", "GET", "/v1//health", 404, "not_found", ""},
		{"trailing slash", "GET", "/v1/health/", 404, "not_found", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			rec := hn.do(row.method, row.path, "", "", nil)
			if rec.Code != row.status {
				t.Fatalf("status = %d, want %d", rec.Code, row.status)
			}
			if row.method == "HEAD" {
				return // a HEAD response has no body
			}
			if e := decodeErr(t, rec); e.Error.Code != row.code {
				t.Errorf("code = %q, want %q", e.Error.Code, row.code)
			}
			if rec.Header().Get("Allow") != row.allow {
				t.Errorf("Allow = %q, want %q", rec.Header().Get("Allow"), row.allow)
			}
		})
	}
}

func TestPanicBecomes500(t *testing.T) {
	hn := newHarness(t, nil)
	rec := hn.get("/v1/stub/panic", "view")
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if e := decodeErr(t, rec); e.Error.Code != "internal" || e.Error.RequestID == "" {
		t.Errorf("error = %+v", e)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("the panic value leaked to the client")
	}
	if strings.Contains(hn.logs.String(), hn.tokens["view"]) {
		t.Error("the panic value leaked a key into the log")
	}
	if rec := hn.do("GET", "/v1/health", "", "", nil); rec.Code != 200 {
		t.Errorf("server stopped serving after a panic: %d", rec.Code)
	}
}

func TestBodyTooLarge413(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.MaxBodyBytes = 1024 })
	auth := "Bearer " + hn.tokens["ingest"]
	t.Run("declared length", func(t *testing.T) {
		rec := hn.do("POST", "/v1/stub/echo", auth, "", strings.NewReader(strings.Repeat("x", 2000)))
		if rec.Code != 413 || decodeErr(t, rec).Error.Code != "too_large" {
			t.Errorf("status %d: %s", rec.Code, rec.Body)
		}
	})
	t.Run("unknown length", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/v1/stub/echo", strings.NewReader(strings.Repeat("x", 2000)))
		req.ContentLength = -1
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		hn.h.ServeHTTP(rec, req)
		if rec.Code != 413 {
			t.Errorf("status %d: %s", rec.Code, rec.Body)
		}
	})
	t.Run("within limit", func(t *testing.T) {
		if rec := hn.do("POST", "/v1/stub/echo", auth, "", strings.NewReader("{}")); rec.Code != 200 {
			t.Errorf("status %d", rec.Code)
		}
	})
}

func TestHealth_NoAuth_NoStoreAccess(t *testing.T) {
	hn := newHarness(t, nil)
	rec := hn.do("GET", "/v1/health", "", "", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"ok":true,"version":"test"}` {
		t.Errorf("status %d body %q", rec.Code, rec.Body)
	}
	if hn.keys.gets != 0 {
		t.Errorf("health read the key store %d times", hn.keys.gets)
	}
}

func TestKeyCache_ServesRepeatReadsAndExpires(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.KeyCacheTTL = 30 * time.Second })
	hn.get("/v1/stub/view", "view")
	hn.get("/v1/stub/view", "view")
	if hn.keys.gets != 1 {
		t.Errorf("key store reads = %d, want 1 (second one cached)", hn.keys.gets)
	}
	hn.clock.Advance(31 * time.Second)
	hn.get("/v1/stub/view", "view")
	if hn.keys.gets != 2 {
		t.Errorf("key store reads = %d after the TTL, want 2", hn.keys.gets)
	}
}

func TestKeyCache_RevocationTakesEffectAfterTTL(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.KeyCacheTTL = 30 * time.Second })
	hn.get("/v1/stub/view", "view")
	_, id, _, _ := ParseKey(hn.tokens["view"])
	_ = hn.keys.SetDisabled(context.Background(), id, true)
	if rec := hn.get("/v1/stub/view", "view"); rec.Code != 200 {
		t.Errorf("within the TTL a cached key still works, got %d", rec.Code)
	}
	hn.clock.Advance(31 * time.Second)
	if rec := hn.get("/v1/stub/view", "view"); rec.Code != 403 {
		t.Errorf("after the TTL a revoked key = %d, want 403", rec.Code)
	}
}

func TestTouch_IsThrottled(t *testing.T) {
	hn := newHarness(t, nil)
	for range 3 {
		hn.get("/v1/stub/view", "view")
	}
	if hn.keys.touches != 1 {
		t.Errorf("touches = %d, want 1", hn.keys.touches)
	}
	hn.clock.Advance(touchEvery + time.Second)
	hn.get("/v1/stub/view", "view")
	if hn.keys.touches != 2 {
		t.Errorf("touches = %d after the interval, want 2", hn.keys.touches)
	}
}

func TestLogs_NeverContainKey(t *testing.T) {
	hn := newHarness(t, nil)
	token := hn.tokens["view"]
	_, _, secret, _ := ParseKey(token)
	wrong := token[:15] + strings.Repeat("A", 43)
	hn.do("GET", "/v1/stub/view", "Bearer "+wrong, "", nil)
	hn.do("GET", "/v1/stub/view?key="+token, "Bearer "+token, "", nil) // a key in the URL must be redacted
	hn.get("/v1/stub/panic", "view")
	out := hn.logs.String()
	if out == "" {
		t.Fatal("expected access log lines")
	}
	for _, bad := range []string{token, wrong, secret, strings.Repeat("A", 43)} {
		if strings.Contains(out, bad) {
			t.Errorf("log contains secret material %q", bad[:12]+"...")
		}
	}
}

func TestRedactingHandler_RedactsMessageAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	token, _, _ := GenerateKey(ScopeView, testPepper, rand.Reader, time.Now(), 0)
	log := slog.New(newRedactingHandler(slog.NewTextHandler(&buf, nil))).With("pre", token)
	log.Error("bad "+token, "k", token, slog.Group("g", "inner", token), "err", fmt.Errorf("wrapped %s", token))
	if strings.Contains(buf.String(), token[15:]) {
		t.Errorf("secret survived redaction: %s", buf.String())
	}
}

func TestNew_ValidatesDeps(t *testing.T) {
	good := Deps{Store: &fakeStore{keys: &fakeKeys{}}, Pepper: testPepper, Opts: DefaultOptions()}
	if _, err := New(good); err != nil {
		t.Fatalf("valid deps: %v", err)
	}
	short := good
	short.Pepper = []byte("short")
	if _, err := New(short); err == nil {
		t.Error("a short pepper must be refused")
	}
	noStore := good
	noStore.Store = nil
	if _, err := New(noStore); err == nil {
		t.Error("a missing store must be refused")
	}
	badOpts := good
	badOpts.Opts.MaxBodyBytes = 0
	if _, err := New(badOpts); err == nil {
		t.Error("invalid options must be refused")
	}
}

func FuzzAuthHeader(f *testing.F) {
	hn := newHarness(f, nil)
	f.Add("")
	f.Add("Bearer " + hn.tokens["view"])
	f.Add("Bearer clk_v_zzzzzzzz_" + strings.Repeat("A", 43))
	f.Add("Bearer \x00\xff")
	f.Fuzz(func(t *testing.T, header string) {
		ipCounter++
		rec := hn.do("GET", "/v1/stub/view", header, fmt.Sprintf("10.%d.%d.1:1", ipCounter/256%256, ipCounter%256), nil)
		if rec.Code != 200 && rec.Code != 401 && rec.Code != 403 && rec.Code != 429 {
			t.Fatalf("header %q gave status %d", header, rec.Code)
		}
	})
}
