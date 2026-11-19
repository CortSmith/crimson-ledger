package testkit

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/store"
	"crimsonledger/internal/store/sqlitestore"
)

// Keys are the two bearer tokens NewTestAPI mints.
type Keys struct {
	Ingest string // scope ingest: may POST /v1/pings
	View   string // scope view: may read
}

// Pepper is the HMAC pepper of every test API.
var Pepper = []byte(strings.Repeat("t", 32))

// NewTestAPI starts the real apiserver on a loopback httptest server. With a nil store it opens a
// fresh SQLite database in the test's temp dir. Time comes from clock (nil: a new FakeClock).
func NewTestAPI(t testing.TB, s store.Store, clock *FakeClock) (*httptest.Server, Keys) {
	t.Helper()
	if clock == nil {
		clock = NewFakeClock()
	}
	if s == nil {
		db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatalf("open test database: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		s = db
	}
	var keys Keys
	for _, k := range []struct {
		scope apiserver.Scope
		dst   *string
	}{{apiserver.ScopeIngest, &keys.Ingest}, {apiserver.ScopeView, &keys.View}} {
		token, rec, err := apiserver.GenerateKey(k.scope, Pepper, rand.Reader, clock.Now(), 0)
		if err != nil {
			t.Fatalf("generate test key: %v", err)
		}
		if err := s.Keys().Put(context.Background(), rec); err != nil {
			t.Fatalf("store test key: %v", err)
		}
		*k.dst = token
	}
	opts := apiserver.DefaultOptions()
	opts.Version = "testkit"
	h, err := apiserver.New(apiserver.Deps{Store: s, Pepper: Pepper, Now: clock.Now, Log: slog.New(slog.DiscardHandler), Opts: opts})
	if err != nil {
		t.Fatalf("build test API: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, keys
}
