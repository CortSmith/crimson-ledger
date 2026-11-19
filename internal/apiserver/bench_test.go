package apiserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store/sqlitestore"
)

// The two failure paths must cost the same (no timing oracle for key ids). Compare the ns/op of the pair:
//
//	go test -run xxx -bench Principal ./internal/apiserver/
func benchPrincipal(b *testing.B, token string) {
	hn := newHarness(b, nil)
	srv, _ := newServer(Deps{Store: &fakeStore{keys: hn.keys}, Pepper: testPepper, Opts: DefaultOptions()})
	if token == "" {
		token = hn.tokens["view"][:15] + strings.Repeat("A", 43)
	}
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		_, _ = srv.principal(ctx, token, true, ScopeView)
	}
}

func BenchmarkPrincipal_WrongSecret(b *testing.B) { benchPrincipal(b, "") }
func BenchmarkPrincipal_UnknownID(b *testing.B) {
	benchPrincipal(b, "clk_v_zzzzzzzz_"+strings.Repeat("A", 43))
}

// BenchmarkHandlePing_Max drives the largest ping the default 256 KiB limit admits (200 contacts, 15 fixes each)
// through the whole handler chain into a real SQLite store. Budget (PLAN.md section 13): < 160 ms per request.
func BenchmarkHandlePing_Max(b *testing.B) {
	st, err := sqlitestore.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	clock := &fakeClock{t: time.Unix(1731800000, 0)}
	token, rec, err := GenerateKey(ScopeIngest, testPepper, rand.Reader, clock.Now(), 0)
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Keys().Put(context.Background(), rec); err != nil {
		b.Fatal(err)
	}
	opts := DefaultOptions()
	opts.RateLimitPerMin = maxRateLimitPerMin
	h, err := New(Deps{Store: st, Pepper: testPepper, Now: clock.Now, Opts: opts})
	if err != nil {
		b.Fatal(err)
	}
	hn := &harness{clock: clock}
	bodies := make([][]byte, b.N)
	for i := range bodies {
		bodies[i] = pingJSON(b, hn, func(p *domain.Ping) {
			p.BatchID = fmt.Sprintf("6f1c2e0e-8b5a-4a9e-9d34-%012x", i)
			fixes := make([]domain.Fix, 15)
			for j := range fixes {
				fixes[j] = domain.Fix{T: p.SentAt - int64(j)*1000, MapID: 1411, X: 50.5, Y: 40.5, Kind: domain.FixExact}
			}
			p.Contacts = nil
			for c := range domain.MaxContacts {
				p.Contacts = append(p.Contacts, domain.Contact{
					GUID: fmt.Sprintf("Player-1-%08X", c+1), Name: "Mc Cheeki", Raw: "Mc-Cheeki-PvP-US",
					Reaction: domain.ReactionHostile, LastSeen: p.SentAt, Fixes: fixes,
				})
			}
		})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		req := httptest.NewRequest("POST", "/v1/pings", bytes.NewReader(bodies[i]))
		req.RemoteAddr = "192.0.2.1:1"
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", jsonType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			b.Fatalf("status %d: %s", w.Code, w.Body)
		}
	}
}
