package apiclient

import (
	"encoding/json"
	"testing"

	"crimsonledger/internal/domain"
)

func BenchmarkSweepDecode_2000Contacts(b *testing.B) {
	cs := make([]domain.SweepContact, 2000)
	for i := range cs {
		cs[i] = domain.SweepContact{GUID: "Player-1329-0A1B2C3D", Name: "Mc Cheeki", Reaction: domain.ReactionHostile, LastSeen: 1, MapID: 1411, X: 51.2, Y: 40.7, Kind: domain.FixExact}
	}
	body, _ := json.Marshal(SweepResult{Contacts: cs})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r SweepResult
		if err := decodeJSON(body, &r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPostPingEncodeGzip_Max(b *testing.B) {
	p := domain.Ping{BatchID: "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44", Contacts: make([]domain.Contact, domain.MaxContacts)}
	for i := range p.Contacts {
		p.Contacts[i] = domain.Contact{GUID: "Player-1329-0A1B2C3D", Name: "Mc Cheeki", Fixes: make([]domain.Fix, 50)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw, _ := json.Marshal(p)
		if _, err := gzipBytes(raw); err != nil {
			b.Fatal(err)
		}
	}
}
