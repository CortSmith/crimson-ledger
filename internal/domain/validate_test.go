package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var testNow = time.UnixMilli(1731800000000)

func ms(d time.Duration) int64 { return testNow.Add(d).UnixMilli() }

func guid(i int) string { return fmt.Sprintf("Player-1329-%08X", i) }

func goodPing() Ping {
	return Ping{
		BatchID: "6f1c2e0e-8b5a-4a9e-9d34-0a7c1d2f3b44", Source: "my-pc", Ruleset: RulesetPvP, SentAt: ms(0),
		Contacts: []Contact{{GUID: guid(1), Name: "Mc Cheeki", Raw: "Mc-Cheeki-PvP-US", Reaction: ReactionHostile,
			LastSeen: ms(-time.Second), Fixes: []Fix{{T: ms(-time.Second), MapID: 1411, X: 51.2, Y: 40.7, Kind: FixExact}}}},
		Charts: []Chart{{MapID: 1411, Zone: "Durotar", X0: 0, Y0: 0, X1: 1, Y1: 1}},
		Kills:  []Kill{{T: ms(-time.Minute), Killer: guid(1), Victim: guid(2), MapID: 1411}},
	}
}

func maxPing() Ping {
	p := goodPing()
	p.Contacts = nil
	for i := 0; i < MaxContacts; i++ {
		c := Contact{GUID: guid(i), Name: "N", Raw: "r", Reaction: ReactionNeutral, LastSeen: ms(0)}
		for j := 0; j < MaxFixesPerContact; j++ {
			c.Fixes = append(c.Fixes, Fix{T: ms(-time.Duration(j) * time.Second), MapID: 1, X: 50, Y: 50, Kind: FixApprox})
		}
		p.Contacts = append(p.Contacts, c)
	}
	p.Charts = nil
	for i := 1; i <= MaxCharts; i++ {
		p.Charts = append(p.Charts, Chart{MapID: i, Zone: "Z", X1: 1, Y1: 1})
	}
	p.Kills = nil
	for i := 0; i < MaxKills; i++ {
		p.Kills = append(p.Kills, Kill{T: ms(0), Killer: guid(i), Victim: guid(i + 1000), MapID: 1})
	}
	return p
}

func TestValidatePing(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Ping)
		wantPath string // empty means valid
	}{
		{"valid minimal", func(p *Ping) { *p = Ping{BatchID: p.BatchID, Source: "s", Ruleset: RulesetNormal, SentAt: p.SentAt} }, ""},
		{"valid example", func(*Ping) {}, ""},
		{"valid max size", func(p *Ping) { *p = maxPing() }, ""},
		{"too many contacts", func(p *Ping) { *p = maxPing(); p.Contacts = append(p.Contacts, Contact{GUID: guid(999)}) }, "contacts"},
		{"too many fixes", func(p *Ping) {
			*p = maxPing()
			p.Contacts[7].Fixes = append(p.Contacts[7].Fixes, p.Contacts[7].Fixes[0])
		}, "contacts[7].fixes"},
		{"too many charts", func(p *Ping) { *p = maxPing(); p.Charts = append(p.Charts, p.Charts[0]) }, "charts"},
		{"too many kills", func(p *Ping) { *p = maxPing(); p.Kills = append(p.Kills, p.Kills[0]) }, "kills"},
		{"name 25 runes", func(p *Ping) { p.Contacts[0].Name = strings.Repeat("é", 25) }, "contacts[0].name"},
		{"name 24 runes", func(p *Ping) { p.Contacts[0].Name = strings.Repeat("é", 24) }, ""},
		{"empty name", func(p *Ping) { p.Contacts[0].Name = "" }, "contacts[0].name"},
		{"raw 65 runes", func(p *Ping) { p.Contacts[0].Raw = strings.Repeat("r", 65) }, "contacts[0].raw"},
		{"raw 64 runes", func(p *Ping) { p.Contacts[0].Raw = strings.Repeat("r", 64) }, ""},
		{"empty raw allowed", func(p *Ping) { p.Contacts[0].Raw = "" }, ""},
		{"source 33 chars", func(p *Ping) { p.Source = strings.Repeat("a", 33) }, "source"},
		{"source 32 chars", func(p *Ping) { p.Source = strings.Repeat("a", 32) }, ""},
		{"source bad char", func(p *Ping) { p.Source = "my pc" }, "source"},
		{"source empty", func(p *Ping) { p.Source = "" }, "source"},
		{"empty batchId", func(p *Ping) { p.BatchID = "" }, "batchId"},
		{"non-v4 uuid", func(p *Ping) { p.BatchID = "6f1c2e0e-8b5a-1a9e-9d34-0a7c1d2f3b44" }, "batchId"},
		{"bad uuid variant", func(p *Ping) { p.BatchID = "6f1c2e0e-8b5a-4a9e-cd34-0a7c1d2f3b44" }, "batchId"},
		{"bad ruleset", func(p *Ping) { p.Ruleset = "hardcore" }, "ruleset"},
		{"bad reaction", func(p *Ping) { p.Contacts[0].Reaction = "angry" }, "contacts[0].reaction"},
		{"bad kind", func(p *Ping) { p.Contacts[0].Fixes[0].Kind = "guess" }, "contacts[0].fixes[0].kind"},
		{"guid Foo-1-2", func(p *Ping) { p.Contacts[0].GUID = "Foo-1-2" }, "contacts[0].guid"},
		{"guid lowercase hex", func(p *Ping) { p.Contacts[0].GUID = "Player-1-abcd" }, "contacts[0].guid"},
		{"control char in name", func(p *Ping) { p.Contacts[0].Name = "Mc\nCheeki" }, "contacts[0].name"},
		{"bidi override in name", func(p *Ping) { p.Contacts[0].Name = "Mc\u202eCheeki" }, "contacts[0].name"},
		{"zero width in raw", func(p *Ping) { p.Contacts[0].Raw = "a\u200bb" }, "contacts[0].raw"},
		{"invalid utf8 in name", func(p *Ping) { p.Contacts[0].Name = "a\xffb" }, "contacts[0].name"},
		{"x just over", func(p *Ping) { p.Contacts[0].Fixes[0].X = 100.0001 }, "contacts[0].fixes[0].x"},
		{"x exactly 100", func(p *Ping) { p.Contacts[0].Fixes[0].X = 100 }, ""},
		{"x negative", func(p *Ping) { p.Contacts[0].Fixes[0].X = -0.1 }, "contacts[0].fixes[0].x"},
		{"x NaN", func(p *Ping) { p.Contacts[0].Fixes[0].X = math.NaN() }, "contacts[0].fixes[0].x"},
		{"y +Inf", func(p *Ping) { p.Contacts[0].Fixes[0].Y = math.Inf(1) }, "contacts[0].fixes[0].y"},
		{"y -Inf", func(p *Ping) { p.Contacts[0].Fixes[0].Y = math.Inf(-1) }, "contacts[0].fixes[0].y"},
		{"fix mapId zero", func(p *Ping) { p.Contacts[0].Fixes[0].MapID = 0 }, "contacts[0].fixes[0].mapId"},
		{"fix 25h old", func(p *Ping) { p.Contacts[0].Fixes[0].T = ms(-25 * time.Hour) }, "contacts[0].fixes[0].t"},
		{"fix 23h old", func(p *Ping) { p.Contacts[0].Fixes[0].T = ms(-23 * time.Hour) }, ""},
		{"fix 6min future", func(p *Ping) { p.Contacts[0].Fixes[0].T = ms(6 * time.Minute) }, "contacts[0].fixes[0].t"},
		{"fix 4min future", func(p *Ping) { p.Contacts[0].Fixes[0].T = ms(4 * time.Minute) }, ""},
		{"lastSeen stale", func(p *Ping) { p.Contacts[0].LastSeen = ms(-25 * time.Hour) }, "contacts[0].lastSeen"},
		{"duplicate guids", func(p *Ping) { p.Contacts = append(p.Contacts, p.Contacts[0]) }, "contacts[1].guid"},
		{"kill self", func(p *Ping) { p.Kills[0].Victim = p.Kills[0].Killer }, "kills[0].victim"},
		{"kill bad killer", func(p *Ping) { p.Kills[0].Killer = "x" }, "kills[0].killer"},
		{"kill bad victim", func(p *Ping) { p.Kills[0].Victim = "x" }, "kills[0].victim"},
		{"kill stale", func(p *Ping) { p.Kills[0].T = ms(-48 * time.Hour) }, "kills[0].t"},
		{"kill mapId zero", func(p *Ping) { p.Kills[0].MapID = 0 }, "kills[0].mapId"},
		{"chart mapId zero", func(p *Ping) { p.Charts[0].MapID = 0 }, "charts[0].mapId"},
		{"chart zone empty", func(p *Ping) { p.Charts[0].Zone = "" }, "charts[0].zone"},
		{"chart x1 NaN", func(p *Ping) { p.Charts[0].X1 = math.NaN() }, "charts[0].x1"},
		{"chart y0 range", func(p *Ping) { p.Charts[0].Y0 = 101 }, "charts[0].y0"},
		{"sentAt 11min ahead", func(p *Ping) { p.SentAt = ms(11 * time.Minute) }, "sentAt"},
		{"sentAt 11min behind", func(p *Ping) { p.SentAt = ms(-11 * time.Minute) }, "sentAt"},
		{"sentAt 9min behind", func(p *Ping) { p.SentAt = ms(-9 * time.Minute) }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := goodPing()
			tt.mutate(&p)
			err := ValidatePing(p, testNow)
			if tt.wantPath == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Path != tt.wantPath {
				t.Fatalf("want path %q, got %v", tt.wantPath, err)
			}
		})
	}
}

func TestValidationError_Path(t *testing.T) {
	p := goodPing()
	for i := 1; i <= 3; i++ {
		c := p.Contacts[0]
		c.GUID = guid(100 + i)
		p.Contacts = append(p.Contacts, c)
	}
	p.Contacts[3].Fixes = []Fix{{T: ms(0), MapID: 1, X: 100.0001, Y: 1, Kind: FixExact}}
	err := ValidatePing(p, testNow)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if ve.Path != "contacts[3].fixes[0].x" || ve.Error() != "contacts[3].fixes[0].x out of range [0,100]" {
		t.Fatalf("got %q / %q", ve.Path, ve.Error())
	}
}

func TestLimits_MatchPlan(t *testing.T) {
	got := []int64{MaxContacts, MaxFixesPerContact, MaxCharts, MaxKills, MaxNameRunes, MaxRawRunes, MaxSourceLen,
		int64(MaxFixAge), int64(MaxFixFuture), int64(MaxClockSkew)}
	want := []int64{200, 50, 50, 200, 24, 64, 32, int64(24 * time.Hour), int64(5 * time.Minute), int64(10 * time.Minute)}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("limit %d: got %d want %d", i, got[i], want[i])
		}
	}
}

func BenchmarkValidatePing_Max(b *testing.B) {
	p := maxPing()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := ValidatePing(p, testNow); err != nil {
			b.Fatal(err)
		}
	}
}
