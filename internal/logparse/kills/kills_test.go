package kills

import (
	"testing"
	"time"

	"crimsonledger/internal/logparse/parser"
)

var t0 = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

const (
	killer = "Player-1-0000AAAA"
	victim = "Player-1-0000BBBB"
	pet    = "Pet-0-1-2-3-4-0000CCCC"
)

func dmg(at time.Duration, src, dst string) parser.Event {
	return parser.Event{Time: t0.Add(at), Type: "SPELL_DAMAGE", IsDamage: true,
		Src: parser.Unit{GUID: src}, Dst: parser.Unit{GUID: dst},
		Info: &parser.Info{GUID: dst, HasPos: true, MapID: 1426}}
}

func died(at time.Duration, dst string, feign bool) parser.Event {
	return parser.Event{Time: t0.Add(at), Type: "UNIT_DIED", Dst: parser.Unit{GUID: dst}, Unconscious: feign}
}

func TestApply_DeathAfterPlayerHitIsAKill(t *testing.T) {
	d := New()
	d.Apply(dmg(0, killer, victim))
	k, ok := d.Apply(died(time.Second, victim, false))
	if !ok || k.Killer != killer || k.Victim != victim || k.MapID != 1426 || k.T != t0.Add(time.Second).UnixMilli() {
		t.Fatalf("kill = %+v ok=%v", k, ok)
	}
}

func TestApply_NoKill(t *testing.T) {
	tests := []struct {
		name string
		run  func(d *Detector) (ok bool)
	}{
		{"feign death", func(d *Detector) bool {
			d.Apply(dmg(0, killer, victim))
			_, ok := d.Apply(died(time.Second, victim, true))
			return ok
		}},
		{"hit too old", func(d *Detector) bool {
			d.Apply(dmg(0, killer, victim))
			_, ok := d.Apply(died(6*time.Second, victim, false))
			return ok
		}},
		{"npc killed it", func(d *Detector) bool {
			d.Apply(dmg(0, "Creature-0-1-2-3-4-0000DDDD", victim))
			_, ok := d.Apply(died(time.Second, victim, false))
			return ok
		}},
		{"victim is an npc", func(d *Detector) bool {
			d.Apply(dmg(0, killer, "Creature-0-1-2-3-4-0000DDDD"))
			_, ok := d.Apply(died(time.Second, "Creature-0-1-2-3-4-0000DDDD", false))
			return ok
		}},
		{"self damage", func(d *Detector) bool {
			d.Apply(dmg(0, victim, victim))
			_, ok := d.Apply(died(time.Second, victim, false))
			return ok
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.run(New()) {
				t.Error("got a kill, want none")
			}
		})
	}
}

func TestApply_PetKillCreditsOwner(t *testing.T) {
	d := New()
	d.Apply(parser.Event{Time: t0, Type: "SPELL_SUMMON", Src: parser.Unit{GUID: killer}, Dst: parser.Unit{GUID: pet}})
	d.Apply(dmg(time.Second, pet, victim))
	k, ok := d.Apply(died(2*time.Second, victim, false))
	if !ok || k.Killer != killer {
		t.Fatalf("kill = %+v ok=%v, want credit to the owner", k, ok)
	}
}

func TestApply_PartyKillNamesKillerAndDuplicatesCollapse(t *testing.T) {
	d := New()
	d.Apply(dmg(0, killer, victim)) // sets the map
	pk := parser.Event{Time: t0.Add(time.Second), Type: "PARTY_KILL", Src: parser.Unit{GUID: killer}, Dst: parser.Unit{GUID: victim}}
	if _, ok := d.Apply(pk); !ok {
		t.Fatal("PARTY_KILL gave no kill")
	}
	if _, ok := d.Apply(died(time.Second+time.Millisecond, victim, false)); ok {
		t.Error("UNIT_DIED right after PARTY_KILL counted twice")
	}
}

func TestApply_NoKillWithoutKnownMap(t *testing.T) {
	d := New()
	e := dmg(0, killer, victim)
	e.Info = nil
	d.Apply(e)
	if _, ok := d.Apply(died(time.Second, victim, false)); ok {
		t.Error("kill reported with no map id (the server would reject it)")
	}
}

func TestApply_BoundsMemory(t *testing.T) {
	d := New()
	for i := 0; i < 3*maxRemembered; i++ {
		d.setOwner("Pet-"+string(rune(i)), killer)
	}
	if len(d.owners) > maxRemembered {
		t.Errorf("owners = %d, want <= %d", len(d.owners), maxRemembered)
	}
}
