package tracker

import (
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/parser"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

const (
	me    = "Player-1-0000AAAA"
	enemy = "Player-1-0000BBBB"
)

func mapEvent() parser.Event {
	return parser.Event{Type: "MAP_CHANGE", Map: &parser.MapBounds{ID: 1426, Name: "Dun Morogh", X0: -6000, X1: -4000, Y0: -1000, Y1: 1000}}
}

// castBy is an event where src acts and the advanced block describes src at (x, y).
func castBy(at time.Duration, guid, raw string, flags uint32, x, y float64) parser.Event {
	return parser.Event{Time: t0.Add(at), Type: "SPELL_CAST_SUCCESS",
		Src:  parser.Unit{GUID: guid, Name: raw, Flags: flags},
		Info: &parser.Info{GUID: guid, X: x, Y: y, MapID: 1426, HasPos: true}}
}

func TestFlush_ExactFixIsConvertedToPercent(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	got := tr.Flush(t0)
	if len(got) != 1 {
		t.Fatalf("contacts = %d, want 1", len(got))
	}
	c := got[0]
	if c.Reaction != domain.ReactionHostile || len(c.Fixes) != 1 {
		t.Fatalf("contact = %+v", c)
	}
	f := c.Fixes[0]
	if f.X != 50 || f.Y != 50 || f.Kind != domain.FixExact || f.MapID != 1426 {
		t.Errorf("fix = %+v, want 50/50 exact on 1426", f)
	}
}

func TestFlush_UnknownMapGivesNoContact(t *testing.T) {
	tr := New("")
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	if got := tr.Flush(t0); len(got) != 0 {
		t.Errorf("got %d contacts without a known map", len(got))
	}
}

func TestFlush_ApproxFixNearMe(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, me, "Alpha-Realm-PvP-US", 0x511, -5000, 0))
	tr.Apply(parser.Event{Time: t0.Add(2 * time.Second), Type: "SPELL_CAST_START",
		Src: parser.Unit{GUID: enemy, Name: "Beta-Realm-PvP-US", Flags: 0x548}})
	got := tr.Flush(t0.Add(2 * time.Second))
	var e *domain.Contact
	for i := range got {
		if got[i].GUID == enemy {
			e = &got[i]
		}
	}
	if e == nil || e.Fixes[0].Kind != domain.FixApprox {
		t.Fatalf("enemy = %+v, want an approx fix", e)
	}
}

func TestFlush_StaleMeGivesNoApprox(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, me, "Alpha-Realm-PvP-US", 0x511, -5000, 0))
	tr.Apply(parser.Event{Time: t0.Add(5 * time.Minute), Type: "SPELL_CAST_START",
		Src: parser.Unit{GUID: enemy, Name: "Beta-Realm-PvP-US", Flags: 0x548}})
	for _, c := range tr.Flush(t0.Add(5 * time.Minute)) {
		if c.GUID == enemy {
			t.Errorf("enemy got a fix from a 5 minute old position: %+v", c)
		}
	}
}

func TestFlush_CoalescesTinyMovesButHeartbeats(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	if len(tr.Flush(t0)) != 1 {
		t.Fatal("first flush must send")
	}
	tr.Apply(castBy(time.Second, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 1)) // 0.05 %
	if got := tr.Flush(t0.Add(time.Second)); len(got) != 0 {
		t.Errorf("tiny move was sent: %+v", got)
	}
	tr.Apply(castBy(2*time.Second, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 40)) // 2 %
	if len(tr.Flush(t0.Add(2*time.Second))) != 1 {
		t.Error("real move was not sent")
	}
	tr.Apply(castBy(40*time.Second, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 40))
	if len(tr.Flush(t0.Add(40*time.Second))) != 1 {
		t.Error("heartbeat refresh was not sent")
	}
}

func TestFlush_ReactionChangeIsSent(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	tr.Flush(t0)
	tr.Apply(castBy(time.Second, enemy, "Beta-Realm-PvP-US", 0x511, -5000, 0))
	got := tr.Flush(t0.Add(time.Second))
	if len(got) != 1 || got[0].Reaction != domain.ReactionFriendly {
		t.Errorf("got %+v, want one friendly contact", got)
	}
}

func TestFlush_IgnoresNPCsAndIsSorted(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, "Creature-0-6835-0-11-1397-0000F38640", "Boar", 0xa48, -5000, 0))
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	tr.Apply(castBy(0, me, "Alpha-Realm-PvP-US", 0x511, -5100, 0))
	got := tr.Flush(t0)
	if len(got) != 2 || got[0].GUID != me || got[1].GUID != enemy {
		t.Errorf("got %+v, want [me enemy] only", got)
	}
}

func TestFlush_ResultsPassValidation(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	tr.Apply(castBy(0, enemy, "Beta-Realm-PvP-US", 0x548, -5000, 0))
	p := domain.Ping{BatchID: "11111111-1111-4111-8111-111111111111", Source: "t", Ruleset: domain.RulesetPvP,
		SentAt: t0.UnixMilli(), Contacts: tr.Flush(t0), Charts: tr.Charts(map[int]struct{}{1426: {}})}
	if err := domain.ValidatePing(p, t0); err != nil {
		t.Fatalf("ping rejected: %v", err)
	}
}

func TestApply_BoundsMemory(t *testing.T) {
	tr := New("")
	tr.Apply(mapEvent())
	for i := 0; i < maxUnits+50; i++ {
		g := "Player-1-" + hex(i)
		tr.Apply(castBy(time.Duration(i)*time.Millisecond, g, "N-Realm", 0x548, -5000, 0))
	}
	if len(tr.units) > maxUnits {
		t.Errorf("units = %d, want <= %d", len(tr.units), maxUnits)
	}
}

func hex(i int) string {
	const d = "0123456789ABCDEF"
	b := []byte("0000AAAA")
	for k := 7; k >= 4 && i > 0; k-- {
		b[k] = d[i%16]
		i /= 16
	}
	return string(b)
}
