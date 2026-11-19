package sqlitestore

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// maxPing is the largest legal ping: 200 contacts of 50 fixes each.
func maxPing(batch string, now time.Time) domain.Ping {
	p := domain.Ping{BatchID: batch, Source: "bench", Ruleset: domain.RulesetPvP, SentAt: now.UnixMilli()}
	for c := 0; c < domain.MaxContacts; c++ {
		ct := domain.Contact{GUID: fmt.Sprintf("Player-1-%08X", c), Name: fmt.Sprintf("Name%d", c), Raw: "r", Reaction: domain.ReactionHostile, LastSeen: now.UnixMilli()}
		for f := 0; f < domain.MaxFixesPerContact; f++ {
			ct.Fixes = append(ct.Fixes, domain.Fix{T: now.UnixMilli() - int64(f), MapID: 1411, X: 1, Y: 1, Kind: domain.FixExact})
		}
		p.Contacts = append(p.Contacts, ct)
	}
	return p
}

func BenchmarkApplyPing_Max(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "b.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	for i := 0; b.Loop(); i++ {
		if _, err := s.ApplyPing(context.Background(), maxPing(fmt.Sprintf("b%d", i), now.Add(time.Duration(i)*time.Minute)), now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSweep_10kContacts(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "b.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now()
	for i := 0; i < 50; i++ { // 50 pings x 200 contacts = 10 000 distinct contacts
		p := maxPing(fmt.Sprintf("b%d", i), now)
		for c := range p.Contacts {
			p.Contacts[c].GUID = fmt.Sprintf("Player-1-%08X", i*1000+c)
			p.Contacts[c].Fixes = p.Contacts[c].Fixes[:1]
		}
		if _, err := s.ApplyPing(context.Background(), p, now); err != nil {
			b.Fatal(err)
		}
	}
	q := store.SweepQuery{Ruleset: domain.RulesetPvP, Now: now}
	b.ResetTimer()
	for b.Loop() {
		if _, err := s.Sweep(context.Background(), q); err != nil {
			b.Fatal(err)
		}
	}
}
