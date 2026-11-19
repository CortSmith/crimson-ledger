// Package kills finds player-versus-player kills in a stream of combat-log events.
//
// A kill is credited to a player (the owner, for pets and totems) who killed another player. The
// log either names the killer (PARTY_KILL, SPELL_INSTAKILL) or the killer is the source of the last
// damage the victim took just before UNIT_DIED. Feign death is not a death. Memory is bounded.
// A Detector is not safe for concurrent use.
package kills

import (
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/parser"
)

const (
	// killingBlowWindow: without an explicit killer, the last player hit counts only if it was this recent (5 s, as in the old bounty analyzer).
	killingBlowWindow = 5 * time.Second
	// duplicateWindow: UNIT_DIED, PARTY_KILL and SPELL_INSTAKILL for one victim inside this span are one death.
	duplicateWindow = 3 * time.Second
	// maxRemembered bounds each map; when full, it is cleared (a lost hit only loses one attribution).
	maxRemembered = 4096
)

type hit struct {
	at     time.Time
	killer string
}

// Detector turns events into kills.
type Detector struct {
	owners map[string]string // pet, guardian or totem GUID -> owner player GUID
	hits   map[string]hit    // victim player GUID -> last player damage
	deaths map[string]time.Time
	mapID  int
}

// New returns an empty Detector.
func New() *Detector {
	return &Detector{owners: map[string]string{}, hits: map[string]hit{}, deaths: map[string]time.Time{}}
}

// Apply consumes one event and returns the kill it completes, if any.
func (d *Detector) Apply(ev parser.Event) (domain.Kill, bool) {
	d.learn(ev)
	switch {
	case ev.Type == "UNIT_DIED":
		if ev.Unconscious || !isPlayer(ev.Dst.GUID) {
			return domain.Kill{}, false
		}
		h, ok := d.hits[ev.Dst.GUID]
		if !ok || ev.Time.Sub(h.at) > killingBlowWindow {
			return domain.Kill{}, false
		}
		return d.finish(ev, h.killer, ev.Dst.GUID)
	case ev.Type == "PARTY_KILL" || ev.Type == "SPELL_INSTAKILL":
		if !isPlayer(ev.Dst.GUID) {
			return domain.Kill{}, false
		}
		return d.finish(ev, d.creditOf(ev.Src.GUID), ev.Dst.GUID)
	case ev.IsDamage:
		d.recordHit(ev)
	}
	return domain.Kill{}, false
}

func (d *Detector) learn(ev parser.Event) {
	if ev.Info != nil && ev.Info.HasPos {
		d.mapID = ev.Info.MapID
	}
	if ev.Info != nil && ev.Info.Owner != "" && !isPlayer(ev.Info.GUID) {
		d.setOwner(ev.Info.GUID, ev.Info.Owner)
	}
	if ev.Type == "SPELL_SUMMON" && isPlayer(ev.Src.GUID) && ev.Dst.GUID != "" {
		d.setOwner(ev.Dst.GUID, ev.Src.GUID)
	}
}

func (d *Detector) setOwner(pet, owner string) {
	if len(d.owners) >= maxRemembered {
		d.owners = map[string]string{}
	}
	d.owners[pet] = owner
}

func (d *Detector) recordHit(ev parser.Event) {
	if !isPlayer(ev.Dst.GUID) {
		return
	}
	killer := d.creditOf(ev.Src.GUID)
	if killer == "" || killer == ev.Dst.GUID {
		return
	}
	if len(d.hits) >= maxRemembered {
		d.hits = map[string]hit{}
	}
	d.hits[ev.Dst.GUID] = hit{at: ev.Time, killer: killer}
}

// creditOf returns the player a unit's actions count for ("" when none).
func (d *Detector) creditOf(guid string) string {
	if isPlayer(guid) {
		return guid
	}
	return d.owners[guid]
}

func (d *Detector) finish(ev parser.Event, killer, victim string) (domain.Kill, bool) {
	if killer == "" || killer == victim || d.mapID <= 0 {
		return domain.Kill{}, false
	}
	if last, ok := d.deaths[victim]; ok && ev.Time.Sub(last) < duplicateWindow {
		return domain.Kill{}, false
	}
	if len(d.deaths) >= maxRemembered {
		d.deaths = map[string]time.Time{}
	}
	d.deaths[victim] = ev.Time
	delete(d.hits, victim)
	return domain.Kill{T: ev.Time.UnixMilli(), Killer: killer, Victim: victim, MapID: d.mapID}, true
}

func isPlayer(guid string) bool { return parser.KindOf(guid) == parser.KindPlayer }
