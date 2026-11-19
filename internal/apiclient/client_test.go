package apiclient

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crimsonledger/internal/domain"
)

const canary = "clk_v_abcd1234_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type sleeps struct{ got []time.Duration }

func (s *sleeps) sleep(_ context.Context, d time.Duration) error {
	s.got = append(s.got, d)
	return nil
}

func newTest(t *testing.T, h http.Handler) (*Client, *sleeps) {
	t.Helper()
	srv := httptest.NewServer(h) // loopback http is allowed
	t.Cleanup(srv.Close)
	sl := &sleeps{}
	c, err := New(Options{BaseURL: srv.URL, APIKey: canary, UserAgent: "test/1", Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	c.rnd = func() float64 { return 0.5 } // zero jitter
	return c, sl
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestNew_RejectsBadURLs(t *testing.T) {
	for _, u := range []string{"", "http://example.com", "ftp://x", "https://user:pw@example.com", "https://", "https://h/?a=1"} {
		if _, err := New(Options{BaseURL: u}); err == nil {
			t.Errorf("New(%q) accepted", u)
		}
	}
	for _, u := range []string{"https://example.com", "http://127.0.0.1:8080", "http://localhost", "http://[::1]:1"} {
		if _, err := New(Options{BaseURL: u}); err != nil {
			t.Errorf("New(%q): %v", u, err)
		}
	}
}

func TestNew_BadCAFile(t *testing.T) {
	if _, err := New(Options{BaseURL: "https://x.example", CAFile: "/nonexistent.pem"}); err == nil {
		t.Error("missing CA file accepted")
	}
}

func TestRequests_Headers(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+canary || r.Header.Get("User-Agent") != "test/1" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("headers: %v", r.Header)
		}
		writeJSON(w, 200, map[string]any{"charts": []domain.Chart{{MapID: 1, Zone: "Durotar"}}, "future": 1})
	}))
	ch, err := c.Charts(context.Background())
	if err != nil || len(ch) != 1 || ch[0].Zone != "Durotar" {
		t.Fatalf("%v %v", ch, err)
	}
}

func TestRetry_Table(t *testing.T) {
	tests := []struct {
		name     string
		statuses []int
		attempts int
		wantErr  bool
		sleeps   []time.Duration
	}{
		{"ok first", []int{200}, 1, false, nil},
		{"503 then ok", []int{503, 200}, 2, false, []time.Duration{500 * time.Millisecond}},
		{"502 504 429 ok", []int{502, 504, 429, 200}, 4, false, []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}},
		{"always 503", []int{503, 503, 503, 503, 503}, 5, true, []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second}},
		{"400 never retried", []int{400}, 1, true, nil},
		{"401", []int{401}, 1, true, nil},
		{"403", []int{403}, 1, true, nil},
		{"404", []int{404}, 1, true, nil},
		{"413", []int{413}, 1, true, nil},
		{"500 not retried", []int{500}, 1, true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n atomic.Int32
			c, sl := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				i := int(n.Add(1)) - 1
				if tt.statuses[i] == 200 {
					writeJSON(w, 200, map[string]any{"charts": []any{}})
					return
				}
				writeJSON(w, tt.statuses[i], map[string]any{"error": map[string]string{"code": "x", "message": "m", "requestId": "r1"}})
			}))
			_, err := c.Charts(context.Background())
			if (err != nil) != tt.wantErr || int(n.Load()) != tt.attempts {
				t.Fatalf("err=%v attempts=%d", err, n.Load())
			}
			if fmt.Sprint(sl.got) != fmt.Sprint(tt.sleeps) {
				t.Errorf("sleeps %v want %v", sl.got, tt.sleeps)
			}
		})
	}
}

func TestRetryAfter_Capped(t *testing.T) {
	var n atomic.Int32
	c, sl := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			writeJSON(w, 429, map[string]any{})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	}))
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sl.got) != 1 || sl.got[0] != 60*time.Second {
		t.Errorf("sleeps %v", sl.got)
	}
}

func TestRetry_ReusesBatchID(t *testing.T) {
	var ids []string
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p domain.Ping
		_ = json.NewDecoder(r.Body).Decode(&p)
		ids = append(ids, p.BatchID)
		if len(ids) == 1 {
			writeJSON(w, 503, map[string]any{})
			return
		}
		writeJSON(w, 200, PingResult{Accepted: true, Contacts: 1})
	}))
	res, err := c.PostPing(context.Background(), domain.Ping{BatchID: "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44"})
	if err != nil || !res.Accepted || len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("%v %v %v", res, err, ids)
	}
}

func TestContextCancel_StopsRetrying(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { n.Add(1); writeJSON(w, 503, map[string]any{}) }))
	c.sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	if _, err := c.Charts(ctx); !errors.Is(err, context.Canceled) || n.Load() != 1 {
		t.Fatalf("%v attempts=%d", err, n.Load())
	}
}

func TestRedirect_Refused(t *testing.T) {
	var gotAuth atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			gotAuth.Add(1)
		}
	}))
	defer other.Close()
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	_, err := c.Charts(context.Background())
	if !errors.Is(err, ErrCrossHostRedirect) || gotAuth.Load() != 0 {
		t.Fatalf("err=%v auth=%d", err, gotAuth.Load())
	}
	if strings.Contains(err.Error(), other.URL) {
		t.Error("error leaks redirect target URL")
	}
}

func TestRedirect_TooMany(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/v1/charts", http.StatusFound) }))
	if _, err := c.Charts(context.Background()); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatal(err)
	}
}

func TestResponse_TooLarge(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"charts":[],"pad":"` + strings.Repeat("a", maxResponseBytes) + `"}`))
	}))
	if _, err := c.Charts(context.Background()); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatal(err)
	}
}

func TestResponse_WrongContentTypeAndShape(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>")
	}))
	if _, err := c.Charts(context.Background()); !errors.Is(err, ErrNotJSON) {
		t.Fatal(err)
	}
	c2, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"charts": "nope"}) }))
	if _, err := c2.Charts(context.Background()); err == nil {
		t.Fatal("wrong field type accepted")
	}
}

func TestSweep_304ServesCache(t *testing.T) {
	var sawINM atomic.Value
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			sawINM.Store(inm)
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		writeJSON(w, 200, SweepResult{ServerTime: 5, Contacts: []domain.SweepContact{{GUID: "g", Name: "A"}}})
	}))
	zone := 1411
	p := SweepParams{Ruleset: domain.RulesetPvP, Zone: &zone}
	first, err := c.Sweep(context.Background(), p)
	if err != nil || first.NotModified {
		t.Fatalf("%v %v", first, err)
	}
	second, err := c.Sweep(context.Background(), p)
	if err != nil || !second.NotModified || len(second.Contacts) != 1 || sawINM.Load() != `W/"v1"` {
		t.Fatalf("%+v %v inm=%v", second, err, sawINM.Load())
	}
}

func TestSweep_CacheBounded(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `W/"x"`)
		writeJSON(w, 200, SweepResult{})
	}))
	for i := 0; i < etagCacheSize+10; i++ {
		z := i
		if _, err := c.Sweep(context.Background(), SweepParams{Zone: &z}); err != nil {
			t.Fatal(err)
		}
	}
	if c.cache.len() != etagCacheSize {
		t.Errorf("cache holds %d", c.cache.len())
	}
}

func TestReadEndpoints_Queries(t *testing.T) {
	var got []string
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		writeJSON(w, 200, map[string]any{"players": []any{}, "ranking": []any{}, "fixes": []any{}})
	}))
	ctx := context.Background()
	m := 7
	_, _ = c.Players(ctx, domain.RulesetPvP, "mc", 5)
	_, _ = c.Trail(ctx, domain.RulesetPvP, "Player-1-0A1B", TrailParams{MapID: &m, Limit: 9, Since: 3})
	_, _ = c.Bounty(ctx, domain.RulesetRP, 10)
	want := []string{"/v1/players?limit=5&q=mc&ruleset=pvp", "/v1/players/Player-1-0A1B/trail?limit=9&map=7&ruleset=pvp&since=3", "/v1/bounty?limit=10&ruleset=rp"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v", got)
	}
}

func TestGzipThreshold(t *testing.T) {
	var enc []string
	var contacts []int
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc = append(enc, r.Header.Get("Content-Encoding"))
		var rd io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			rd = zr
		}
		var p domain.Ping
		if err := json.NewDecoder(rd).Decode(&p); err != nil {
			t.Error(err)
		}
		contacts = append(contacts, len(p.Contacts))
		writeJSON(w, 200, PingResult{Accepted: true})
	}))
	small := domain.Ping{BatchID: "b"}
	big := domain.Ping{BatchID: "b", Contacts: make([]domain.Contact, 40)}
	_, _ = c.PostPing(context.Background(), small)
	_, _ = c.PostPing(context.Background(), big)
	if enc[0] != "" || enc[1] != "gzip" || contacts[1] != 40 {
		t.Errorf("enc=%v contacts=%v", enc, contacts)
	}
}

func TestError_Format_NoKey(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "bad " + canary + "\x1b[31m", "requestId": "a1"}})
	}))
	_, err := c.Charts(context.Background())
	if !IsAuth(err) || IsRetryable(err) {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), canary) || strings.ContainsRune(err.Error(), 0x1b) || !strings.HasPrefix(err.Error(), "server said unauthorized: ") || !strings.HasSuffix(err.Error(), "(request a1)") {
		t.Errorf("%q", err)
	}
}

func TestNoKeyInOutput(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{}) }))
	o := Options{BaseURL: "https://h.example", APIKey: canary}
	for _, s := range []string{fmt.Sprintf("%v %+v %#v %s", c, c, c, c), fmt.Sprintf("%v %+v %#v", o, o, o), fmt.Sprint(c.LogValue(), o.LogValue())} {
		if strings.Contains(s, canary) {
			t.Errorf("key leaked: %s", s)
		}
	}
	// Network failure: the error must not contain the key or the URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c2, _ := New(Options{BaseURL: dead.URL, APIKey: canary, Sleep: (&sleeps{}).sleep})
	_, err := c2.Charts(context.Background())
	if err == nil || strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), dead.URL) {
		t.Errorf("%v", err)
	}
}

func TestCheck(t *testing.T) {
	c, _ := newTest(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"ok": false}) }))
	if c.Check(context.Background()) == nil {
		t.Error("unhealthy accepted")
	}
}

func TestTLS_CAFile(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) }))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL, Sleep: (&sleeps{}).sleep})
	if err := c.Check(context.Background()); err == nil {
		t.Fatal("self-signed accepted without CA")
	}
	pem := t.TempDir() + "/ca.pem"
	writePEM(t, pem, srv)
	c2, err := New(Options{BaseURL: srv.URL, CAFile: pem})
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// hostname mismatch is rejected even with the CA
	c3, _ := New(Options{BaseURL: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), CAFile: pem, Sleep: (&sleeps{}).sleep})
	if err := c3.Check(context.Background()); err == nil {
		t.Error("hostname mismatch accepted")
	}
}

func FuzzDecodeResponse(f *testing.F) {
	f.Add([]byte(`{"serverTime":1,"contacts":[{"guid":"g"}]}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(_ *testing.T, b []byte) {
		var r SweepResult
		_ = decodeJSON(b, &r)
		var tr Trail
		_ = decodeJSON(b, &tr)
	})
}
