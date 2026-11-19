package apiserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

const (
	testBatch = "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44"
	jsonType  = "application/json"
)

// pingJSON builds a valid ping body at the harness clock; edit may change the decoded struct before encoding.
func pingJSON(t testing.TB, hn *harness, edit func(*domain.Ping)) []byte {
	t.Helper()
	now := hn.clock.Now().UnixMilli()
	p := domain.Ping{
		BatchID: testBatch, Source: "my-pc", Ruleset: domain.RulesetPvP, SentAt: now,
		Contacts: []domain.Contact{{
			GUID: "Player-1329-0A1B2C3D", Name: "Mc Cheeki", Raw: "Mc-Cheeki-PvP-US",
			Reaction: domain.ReactionHostile, LastSeen: now,
			Fixes: []domain.Fix{{T: now, MapID: 1411, X: 51.2, Y: 40.7, Kind: domain.FixExact}},
		}},
	}
	if edit != nil {
		edit(&p)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gz(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type postOpts struct {
	token, contentType, encoding string
}

// post sends body to /v1/pings from a fresh client IP (so the per-IP limiter never interferes).
func (hn *harness) post(body []byte, o postOpts) *httptest.ResponseRecorder {
	ipCounter++
	req := httptest.NewRequest("POST", "/v1/pings", bytes.NewReader(body))
	req.RemoteAddr = fmt.Sprintf("10.%d.%d.1:1", ipCounter/256%256, ipCounter%256)
	if o.token == "" {
		o.token = hn.tokens["ingest"]
	}
	if o.token != "-" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	if o.contentType != "-" {
		req.Header.Set("Content-Type", orDefault(o.contentType, jsonType))
	}
	if o.encoding != "" {
		req.Header.Set("Content-Encoding", o.encoding)
	}
	rec := httptest.NewRecorder()
	hn.h.ServeHTTP(rec, req)
	return rec
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// recordStore wires hn.store to accept every ping and remember what it received.
func recordStore(hn *harness, res store.ApplyResult) *[]domain.Ping {
	var got []domain.Ping
	hn.store.applyPing = func(_ context.Context, p domain.Ping, _ time.Time) (store.ApplyResult, error) {
		got = append(got, p)
		return res, nil
	}
	hn.store.charts = func(context.Context) ([]domain.Chart, error) { return nil, nil }
	return &got
}

var okResult = store.ApplyResult{Accepted: true, Contacts: 1, Fixes: 1}

func TestPing_Responses(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.MaxBodyBytes = 4096 })
	recordStore(hn, okResult)
	valid := pingJSON(t, hn, nil)
	atLimit := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), 4096-len(valid))...)
	rows := []struct {
		name   string
		body   []byte
		opts   postOpts
		status int
		code   string
		sub    string // text the body must contain
	}{
		{"valid plain", valid, postOpts{}, 200, "", `"accepted":true,"contacts":1,"fixes":1,"kills":0`},
		{"valid gzip", gz(t, valid), postOpts{encoding: "gzip"}, 200, "", `"accepted":true`},
		{"content type with charset", valid, postOpts{contentType: "Application/JSON; charset=UTF-8"}, 200, "", ""},
		{"text/plain", valid, postOpts{contentType: "text/plain"}, 415, "unsupported_media_type", ""},
		{"text/json", valid, postOpts{contentType: "text/json"}, 415, "unsupported_media_type", ""},
		{"no content type", valid, postOpts{contentType: "-"}, 415, "unsupported_media_type", ""},
		{"brotli", valid, postOpts{encoding: "br"}, 415, "unsupported_media_type", ""},
		{"gzip header on plain body", valid, postOpts{encoding: "gzip"}, 400, "invalid_request", ""},
		{"body at the limit", atLimit, postOpts{}, 200, "", ""},
		{"body over the limit", append(atLimit, ' '), postOpts{}, 413, "too_large", ""},
		{"gzip bomb", gz(t, append([]byte(`{"batchId":"`), bytes.Repeat([]byte("0"), 50<<20)...)), postOpts{encoding: "gzip"}, 413, "too_large", ""},
		{"two gzip members", append(gz(t, valid), gz(t, []byte(" "))...), postOpts{encoding: "gzip"}, 400, "invalid_request", ""},
		{"gzip then garbage", append(gz(t, valid), "junk"...), postOpts{encoding: "gzip"}, 400, "invalid_request", ""},
		{"truncated gzip", gz(t, valid)[:20], postOpts{encoding: "gzip"}, 400, "invalid_request", ""},
		{"not json", []byte("hello"), postOpts{}, 400, "invalid_ping", "not a valid ping"},
		{"unknown field", []byte(strings.Replace(string(valid), `"source"`, `"admin":true,"source"`, 1)), postOpts{}, 400, "invalid_ping", ""},
		{"bad batch id", pingJSON(t, hn, func(p *domain.Ping) { p.BatchID = "nope" }), postOpts{}, 400, "invalid_ping", "batchId"},
		{"coordinate out of range", pingJSON(t, hn, func(p *domain.Ping) { p.Contacts[0].Fixes[0].X = 101 }), postOpts{}, 400, "invalid_ping", "contacts[0].fixes[0].x"},
		{"view key", valid, postOpts{token: hn.tokens["view"]}, 403, "forbidden", ""},
		{"no key", valid, postOpts{token: "-"}, 401, "unauthorized", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			rec := hn.post(row.body, row.opts)
			if rec.Code != row.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, row.status, rec.Body)
			}
			if row.code != "" {
				if e := decodeErr(t, rec); e.Error.Code != row.code || e.Error.RequestID == "" {
					t.Errorf("error = %+v, want code %q", e, row.code)
				}
			}
			if !strings.Contains(rec.Body.String(), row.sub) {
				t.Errorf("body %q lacks %q", rec.Body, row.sub)
			}
		})
	}
}

func TestPing_TooManyContacts(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	body := pingJSON(t, hn, func(p *domain.Ping) {
		for i := range 200 {
			c := p.Contacts[0]
			c.GUID = fmt.Sprintf("Player-1-%08X", i+1)
			p.Contacts = append(p.Contacts, c)
		}
	})
	rec := hn.post(body, postOpts{})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "contacts") {
		t.Errorf("201 contacts = %d: %s", rec.Code, rec.Body)
	}
}

func TestPing_DuplicateBatch(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, store.ApplyResult{Duplicate: true})
	rec := hn.post(pingJSON(t, hn, nil), postOpts{})
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"accepted":false,"duplicate":true}` {
		t.Errorf("status %d body %q", rec.Code, rec.Body)
	}
}

func TestPing_StoreReceivesThePingAndTheServerClock(t *testing.T) {
	hn := newHarness(t, nil)
	got := recordStore(hn, okResult)
	hn.post(pingJSON(t, hn, nil), postOpts{})
	if len(*got) != 1 || (*got)[0].BatchID != testBatch || (*got)[0].Contacts[0].Name != "Mc Cheeki" {
		t.Errorf("store saw %+v", *got)
	}
}

func TestPing_StoreErrorIs500AndHidesTheCause(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	hn.store.applyPing = func(context.Context, domain.Ping, time.Time) (store.ApplyResult, error) {
		return store.ApplyResult{}, errors.New("disk exploded: secret-detail")
	}
	rec := hn.post(pingJSON(t, hn, nil), postOpts{})
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret-detail") || decodeErr(t, rec).Error.Code != "internal" {
		t.Errorf("status %d body %q", rec.Code, rec.Body)
	}
	if !strings.Contains(hn.logs.String(), "secret-detail") {
		t.Error("the cause must be logged with the request id")
	}
}

func TestPing_SlowStoreIs503WithRetryAfter(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	hn.store.applyPing = func(context.Context, domain.Ping, time.Time) (store.ApplyResult, error) {
		return store.ApplyResult{}, fmt.Errorf("apply: %w", context.DeadlineExceeded)
	}
	rec := hn.post(pingJSON(t, hn, nil), postOpts{})
	if rec.Code != 503 || rec.Header().Get("Retry-After") != "2" || decodeErr(t, rec).Error.Code != "unavailable" {
		t.Errorf("status %d retry %q body %q", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
}

func TestPing_StoreCallHasADeadline(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	var has bool
	hn.store.applyPing = func(ctx context.Context, _ domain.Ping, _ time.Time) (store.ApplyResult, error) {
		_, has = ctx.Deadline()
		return okResult, nil
	}
	hn.post(pingJSON(t, hn, nil), postOpts{})
	if !has {
		t.Error("ApplyPing ran without a timeout")
	}
}

func TestPing_LogsNoBody(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	hn.post(pingJSON(t, hn, nil), postOpts{})
	hn.post(pingJSON(t, hn, func(p *domain.Ping) { p.Contacts[0].Name = "Zzyzx"; p.Contacts[0].Fixes[0].X = 500 }), postOpts{})
	out := hn.logs.String()
	for _, bad := range []string{"Cheeki", "Zzyzx", "Player-1329", "my-pc"} {
		if strings.Contains(out, bad) {
			t.Errorf("log contains request data %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "ping accepted") || !strings.Contains(out, "keyId=") {
		t.Errorf("accepted ping must be logged with the key id:\n%s", out)
	}
}

func killsPing(n int, start int) func(*domain.Ping) {
	return func(p *domain.Ping) {
		for i := range n {
			p.Kills = append(p.Kills, domain.Kill{
				T: p.SentAt, Killer: fmt.Sprintf("Player-1-%08X", start+i), Victim: "Player-1-0000FFFF", MapID: 1411,
			})
		}
	}
}

func TestPing_KillRateLimitPerKey(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.RateLimitPerMin = 100000; o.MaxBodyBytes = 1 << 20 })
	recordStore(hn, okResult)
	for i := range MaxKillsPerKeyHour / 200 {
		body := pingJSON(t, hn, func(p *domain.Ping) {
			p.BatchID = fmt.Sprintf("6f1c2e0e-8b5a-4a9e-9d34-%012x", i)
			killsPing(200, i*200)(p)
		})
		if rec := hn.post(body, postOpts{}); rec.Code != 200 {
			t.Fatalf("batch %d = %d: %s", i, rec.Code, rec.Body)
		}
	}
	rec := hn.post(pingJSON(t, hn, killsPing(1, 9999)), postOpts{})
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || decodeErr(t, rec).Error.Code != "rate_limited" {
		t.Fatalf("over the hourly kill cap = %d: %s", rec.Code, rec.Body)
	}
	hn.clock.Advance(quotaWindow)
	if rec := hn.post(pingJSON(t, hn, killsPing(1, 9999)), postOpts{}); rec.Code != 200 {
		t.Errorf("after the window = %d, want 200", rec.Code)
	}
}

func TestPing_DistinctContactCapPerKey(t *testing.T) {
	hn := newHarness(t, func(o *Options) { o.RateLimitPerMin = 100000; o.MaxBodyBytes = 1 << 20 })
	recordStore(hn, okResult)
	send := func(batch, first, n int) int {
		body := pingJSON(t, hn, func(p *domain.Ping) {
			p.BatchID = fmt.Sprintf("6f1c2e0e-8b5a-4a9e-9d34-%012x", batch)
			p.Contacts = nil
			for i := range n {
				p.Contacts = append(p.Contacts, domain.Contact{
					GUID: fmt.Sprintf("Player-1-%08X", first+i), Name: "Abc", Raw: "Abc", Reaction: domain.ReactionHostile,
					LastSeen: p.SentAt, Fixes: []domain.Fix{{T: p.SentAt, MapID: 1, X: 1, Y: 1, Kind: domain.FixExact}},
				})
			}
		})
		return hn.post(body, postOpts{}).Code
	}
	for b := range MaxContactsPerKeyHour / 200 {
		if code := send(b, b*200, 200); code != 200 {
			t.Fatalf("batch %d = %d", b, code)
		}
	}
	if code := send(100, MaxContactsPerKeyHour, 1); code != 429 {
		t.Errorf("a new contact over the cap = %d, want 429", code)
	}
	if code := send(101, 0, 5); code != 200 {
		t.Errorf("contacts already counted must still be accepted, got %d", code)
	}
}

func TestPing_ChartPoisoningIsLoggedAndTheStoreDecides(t *testing.T) {
	hn := newHarness(t, nil)
	got := recordStore(hn, okResult)
	hn.store.charts = func(context.Context) ([]domain.Chart, error) {
		return []domain.Chart{{MapID: 1411, Zone: "Durotar", X0: 0, Y0: 0, X1: 1, Y1: 1}}, nil
	}
	body := pingJSON(t, hn, func(p *domain.Ping) {
		p.Charts = []domain.Chart{{MapID: 1411, Zone: "Hijacked", X0: 9, Y0: 9, X1: 10, Y1: 10}}
	})
	if rec := hn.post(body, postOpts{}); rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if len(*got) != 1 {
		t.Fatal("the ping must still reach the store, which keeps the first rectangle")
	}
	if out := hn.logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "mapId=1411") || !strings.Contains(out, "keyId=") {
		t.Errorf("want a WARN naming the map and key id:\n%s", out)
	}
	if strings.Contains(hn.logs.String(), "Hijacked") {
		t.Error("zone text from the body must not be logged")
	}
}

func TestPing_ChartsAreOnlyReadWhenThePingHasCharts(t *testing.T) {
	hn := newHarness(t, nil)
	recordStore(hn, okResult)
	hn.store.charts = func(context.Context) ([]domain.Chart, error) { panic("Charts must not be called") }
	if rec := hn.post(pingJSON(t, hn, nil), postOpts{}); rec.Code != 200 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestPing_GetIsNotAllowed(t *testing.T) {
	hn := newHarness(t, nil)
	if rec := hn.get("/v1/pings", "ingest"); rec.Code != 405 || rec.Header().Get("Allow") != "POST" {
		t.Errorf("status %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func FuzzIngestBody(f *testing.F) {
	hn := newHarness(f, func(o *Options) { o.RateLimitPerMin = 1_000_000 })
	recordStore(hn, okResult)
	valid := pingJSON(f, hn, nil)
	f.Add(valid, false)
	f.Add(gz(f, valid), true)
	f.Add([]byte(`{"batchId":`), false)
	f.Add([]byte("\x1f\x8b\x08"), true)
	f.Add([]byte(nil), true)
	f.Fuzz(func(t *testing.T, body []byte, zipped bool) {
		o := postOpts{}
		if zipped {
			o.encoding = "gzip"
		}
		switch code := hn.post(body, o).Code; code {
		case 200, 400, 413, 415:
		default:
			t.Fatalf("status %d for body %q", code, body)
		}
	})
}
