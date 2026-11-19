package testkit

import (
	"fmt"
	"sync/atomic"

	"crimsonledger/internal/domain"
)

var counter atomic.Uint64

// uuidV4 makes a unique, valid UUIDv4 from a process-wide counter (deterministic per test binary run order).
func uuidV4() string {
	n := counter.Add(1)
	return fmt.Sprintf("%08x-0000-4000-8000-%012x", n&0xffffffff, n)
}

// PingOption changes one field of a ping.
type PingOption func(*domain.Ping)

// ContactOption changes one field of a contact.
type ContactOption func(*domain.Contact)

// FixOption changes one field of a fix.
type FixOption func(*domain.Fix)

// APing builds a valid, empty ping stamped at Epoch.
func APing(opts ...PingOption) domain.Ping {
	p := domain.Ping{BatchID: uuidV4(), Source: "testkit", Ruleset: domain.RulesetPvP, SentAt: Epoch.UnixMilli()}
	for _, o := range opts {
		o(&p)
	}
	return p
}

// AContact builds a valid hostile player contact seen at Epoch with no fixes.
func AContact(opts ...ContactOption) domain.Contact {
	n := counter.Add(1)
	c := domain.Contact{
		GUID: fmt.Sprintf("Player-1329-%08X", n), Name: fmt.Sprintf("Scout%d", n), Raw: fmt.Sprintf("Scout%d-Realm", n),
		Reaction: domain.ReactionHostile, LastSeen: Epoch.UnixMilli(),
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// AFix builds a valid exact fix at Epoch in map 1411 (Durotar) at 50,50.
func AFix(opts ...FixOption) domain.Fix {
	f := domain.Fix{T: Epoch.UnixMilli(), MapID: 1411, X: 50, Y: 50, Kind: domain.FixExact}
	for _, o := range opts {
		o(&f)
	}
	return f
}

// AChart builds a valid map rectangle.
func AChart(mapID int, zone string) domain.Chart {
	return domain.Chart{MapID: mapID, Zone: zone, X0: 0, Y0: 0, X1: 1000, Y1: 1000}
}

// AKill builds a valid kill between two player GUIDs at Epoch.
func AKill(killer, victim string) domain.Kill {
	return domain.Kill{T: Epoch.UnixMilli(), Killer: killer, Victim: victim, MapID: 1411}
}

// WithBatchID sets the batch id (break it with a non-UUID).
func WithBatchID(id string) PingOption { return func(p *domain.Ping) { p.BatchID = id } }

// WithSource sets the source label.
func WithSource(s string) PingOption { return func(p *domain.Ping) { p.Source = s } }

// WithRuleset sets the ruleset.
func WithRuleset(r domain.Ruleset) PingOption { return func(p *domain.Ping) { p.Ruleset = r } }

// WithSentAt sets sentAt in epoch milliseconds.
func WithSentAt(ms int64) PingOption { return func(p *domain.Ping) { p.SentAt = ms } }

// WithContacts appends contacts.
func WithContacts(c ...domain.Contact) PingOption {
	return func(p *domain.Ping) { p.Contacts = append(p.Contacts, c...) }
}

// WithCharts appends charts.
func WithCharts(c ...domain.Chart) PingOption {
	return func(p *domain.Ping) { p.Charts = append(p.Charts, c...) }
}

// WithKills appends kills.
func WithKills(k ...domain.Kill) PingOption {
	return func(p *domain.Ping) { p.Kills = append(p.Kills, k...) }
}

// WithGUID sets the contact GUID.
func WithGUID(g string) ContactOption { return func(c *domain.Contact) { c.GUID = g } }

// WithName sets the contact name.
func WithName(n string) ContactOption { return func(c *domain.Contact) { c.Name = n } }

// WithRaw sets the raw log token.
func WithRaw(r string) ContactOption { return func(c *domain.Contact) { c.Raw = r } }

// WithReaction sets the reaction.
func WithReaction(r domain.Reaction) ContactOption { return func(c *domain.Contact) { c.Reaction = r } }

// WithLastSeen sets lastSeen in epoch milliseconds.
func WithLastSeen(ms int64) ContactOption { return func(c *domain.Contact) { c.LastSeen = ms } }

// WithFixes appends fixes.
func WithFixes(f ...domain.Fix) ContactOption {
	return func(c *domain.Contact) { c.Fixes = append(c.Fixes, f...) }
}

// WithT sets the fix time in epoch milliseconds.
func WithT(ms int64) FixOption { return func(f *domain.Fix) { f.T = ms } }

// WithMapID sets the fix map id.
func WithMapID(id int) FixOption { return func(f *domain.Fix) { f.MapID = id } }

// WithX sets the fix X coordinate (percent).
func WithX(x float64) FixOption { return func(f *domain.Fix) { f.X = x } }

// WithY sets the fix Y coordinate (percent).
func WithY(y float64) FixOption { return func(f *domain.Fix) { f.Y = y } }

// WithKind sets the fix kind.
func WithKind(k domain.FixKind) FixOption { return func(f *domain.Fix) { f.Kind = k } }
