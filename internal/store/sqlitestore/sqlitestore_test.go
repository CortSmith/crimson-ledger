package sqlitestore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crimsonledger/internal/store"
	"crimsonledger/internal/store/storetest"
)

func newTestStore(t *testing.T, _ *storetest.FakeClock) store.Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func TestStore_Contract(t *testing.T) { storetest.Run(t, newTestStore) }

func TestStore_AfterMaxPing_DatabaseIsConsistent(t *testing.T) {
	s := newTestStore(t, nil).(*Store)
	now := time.Now()
	if _, err := s.ApplyPing(context.Background(), maxPing("consistency", now), now); err != nil {
		t.Fatalf("ApplyPing: %v", err)
	}
	var verdict string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&verdict); err != nil || verdict != "ok" {
		t.Errorf("integrity_check = %q, %v; want ok", verdict, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Error("foreign_key_check reported a violation")
	}
}
