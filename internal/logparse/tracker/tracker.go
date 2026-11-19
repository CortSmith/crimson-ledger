// Package tracker turns parsed combat-log events into the contacts and fixes the scout uploads.
//
// It keeps one small record per player, converts world positions to percent of the map rectangle
// (the coordinate space of domain.Fix), and hands out only what changed since the last Flush.
// It is not safe for concurrent use: one pipeline stage owns it.
package tracker

import (
	"math"
	"strings"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/parser"
)

const (
	// approxMaxAge is how old "my" last exact fix may be when it stands in for a nearby player (the same
	// 120 s as the old radar).
	approxMaxAge = 120 * time.Second
	// minMovePercent is the smallest move (percent of the map) worth sending again: 0.2 %.
	minMovePercent = 0.2
	// heartbeat is how long a motionless contact stays unsent before one refresh keeps lastSeen current.
	heartbeat = 30 * time.Second
	// maxUnits bounds memory: when exceeded, units unseen for the longest time are dropped.
	maxUnits = 4096
	// expireAfter is when an unseen unit is forgotten.
	expireAfter = 15 * time.Minute
	// mapMargin is how far outside a map rectangle a position may fall before it is ignored (instances reuse ids).
	mapMargin = 5.0
)

type unit struct {
	guid, raw string
	reaction  domain.Reaction
	hasReact  bool
	last      time.Time
	fix       domain.Fix
	hasFix    bool
	dirty     bool
	sent      domain.Fix
	sentAt    time.Time
	sentReact domain.Reaction
	everSent  bool
}

// Tracker accumulates events. The zero value is not usable; call New.
type Tracker struct {
	units  map[string]*unit
	maps   map[int]parser.MapBounds
	namer  *parser.Namer
	meGUID string
	meFix  domain.Fix
	hasMe  bool
	zone   string
	events int
}

// New returns a Tracker. realmSuffix may be empty to auto-detect.
func New(realmSuffix string) *Tracker {
	return &Tracker{units: map[string]*unit{}, maps: map[int]parser.MapBounds{}, namer: parser.NewNamer(realmSuffix)}
}

// Events is how many events were applied.
func (t *Tracker) Events() int { return t.events }

// Apply feeds one event.
func (t *Tracker) Apply(ev parser.Event) {
	t.events++
	switch ev.Type {
	case "ZONE_CHANGE":
		if ev.Zone != "" {
			t.zone = ev.Zone
		}
		return
	case "MAP_CHANGE":
		if ev.Map != nil {
			t.maps[ev.Map.ID] = *ev.Map
		}
		return
	}
	for _, u := range [...]parser.Unit{ev.Src, ev.Dst} {
		t.touch(u, ev.Time)
	}
	infoGUID := t.applyInfo(ev)
	t.applyApprox(ev, infoGUID)
	if len(t.units) > maxUnits {
		t.expire(ev.Time)
	}
}

func (t *Tracker) touch(pu parser.Unit, at time.Time) {
	if pu.GUID == "" || parser.KindOf(pu.GUID) != parser.KindPlayer {
		return
	}
	u := t.units[pu.GUID]
	if u == nil {
		u = &unit{guid: pu.GUID}
		t.units[pu.GUID] = u
	}
	if pu.Name != "" {
		u.raw = pu.Name
		t.namer.Observe(pu.Name)
	}
	if r := parser.ReactionOf(pu.Flags); r != parser.ReactionUnknown {
		u.reaction, u.hasReact = toDomain(r), true
		u.dirty = true
	}
	if parser.IsMine(pu.Flags) {
		t.meGUID = pu.GUID
	}
	if at.After(u.last) {
		u.last = at
		u.dirty = true
	}
}

func toDomain(r parser.Reaction) domain.Reaction {
	switch r {
	case parser.ReactionFriendly:
		return domain.ReactionFriendly
	case parser.ReactionHostile:
		return domain.ReactionHostile
	}
	return domain.ReactionNeutral
}

// applyInfo records the exact position carried by the advanced block and returns that unit's GUID.
func (t *Tracker) applyInfo(ev parser.Event) string {
	in := ev.Info
	if in == nil || !in.HasPos {
		return ""
	}
	u := t.units[in.GUID]
	if u == nil {
		return ""
	}
	f, ok := t.toFix(ev.Time, in.MapID, in.X, in.Y, domain.FixExact)
	if !ok {
		return ""
	}
	u.fix, u.hasFix, u.dirty = f, true, true
	if in.GUID == t.meGUID {
		t.meFix, t.hasMe = f, true
	}
	return in.GUID
}

// applyApprox places other players named by the event at my position when my own fix is recent.
func (t *Tracker) applyApprox(ev parser.Event, infoGUID string) {
	if !t.hasMe || absDur(ev.Time.Sub(time.UnixMilli(t.meFix.T))) >= approxMaxAge {
		return
	}
	for _, pu := range [...]parser.Unit{ev.Src, ev.Dst} {
		u := t.units[pu.GUID]
		if u == nil || pu.GUID == t.meGUID || pu.GUID == infoGUID {
			continue
		}
		f := t.meFix
		f.T, f.Kind = ev.Time.UnixMilli(), domain.FixApprox
		if u.hasFix && u.fix.Kind == domain.FixExact && f.T-u.fix.T < approxMaxAge.Milliseconds()/4 {
			continue // a recent exact fix beats a guess
		}
		u.fix, u.hasFix, u.dirty = f, true, true
	}
}

// toFix converts a world position to percent of the map. ok is false for unknown maps or far-off points.
func (t *Tracker) toFix(at time.Time, mapID int, x, y float64, kind domain.FixKind) (domain.Fix, bool) {
	m, ok := t.maps[mapID]
	if !ok || m.X1 == m.X0 || m.Y1 == m.Y0 {
		return domain.Fix{}, false
	}
	px := (m.Y1 - y) / (m.Y1 - m.Y0) * 100
	py := (m.X1 - x) / (m.X1 - m.X0) * 100
	if math.IsNaN(px) || math.IsNaN(py) || px < -mapMargin || px > 100+mapMargin || py < -mapMargin || py > 100+mapMargin {
		return domain.Fix{}, false
	}
	return domain.Fix{T: at.UnixMilli(), MapID: mapID, X: clamp(px), Y: clamp(py), Kind: kind}, true
}

func clamp(v float64) float64 { return math.Round(math.Min(100, math.Max(0, v))*100) / 100 }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (t *Tracker) expire(now time.Time) {
	for g, u := range t.units {
		if now.Sub(u.last) > expireAfter && g != t.meGUID {
			delete(t.units, g)
		}
	}
	for len(t.units) > maxUnits { // still too many: drop the oldest
		var oldest *unit
		for _, u := range t.units {
			if oldest == nil || u.last.Before(oldest.last) {
				oldest = u
			}
		}
		delete(t.units, oldest.guid)
	}
}

// Charts returns the map rectangles for the given ids, in the percent space fixes use. The server accepts
// only 0..100 values in a chart, so the rectangle is the whole 0..100 square under the map's zone name.
func (t *Tracker) Charts(ids map[int]struct{}) []domain.Chart {
	var out []domain.Chart
	for id := range ids {
		m, ok := t.maps[id]
		if !ok {
			continue
		}
		zone := m.Name
		if zone == "" {
			zone = t.zone
		}
		if zone == "" {
			zone = "Unknown"
		}
		out = append(out, domain.Chart{MapID: id, Zone: truncate(zone, domain.MaxZoneRunes), X0: 0, Y0: 0, X1: 100, Y1: 100})
	}
	return out
}

func truncate(s string, runes int) string {
	r := []rune(s)
	if len(r) > runes {
		return string(r[:runes])
	}
	return s
}

// Flush returns the contacts worth sending now, at most one fix each, and marks them sent.
// A contact is skipped when it has not moved by minMovePercent, kept its reaction, and was sent
// less than heartbeat ago. Output is sorted by GUID so pings are deterministic.
func (t *Tracker) Flush(now time.Time) []domain.Contact {
	var out []domain.Contact
	for _, u := range t.units {
		if !u.dirty || !u.hasFix || !u.hasReact {
			continue
		}
		u.dirty = false
		name := truncate(t.namer.Display(u.raw), domain.MaxNameRunes)
		if strings.TrimSpace(name) == "" || u.guid == "" {
			continue
		}
		if u.everSent && u.reaction == u.sentReact && u.fix.MapID == u.sent.MapID &&
			moved(u.sent, u.fix) < minMovePercent && now.Sub(u.sentAt) < heartbeat {
			continue
		}
		u.sent, u.sentAt, u.sentReact, u.everSent = u.fix, now, u.reaction, true
		out = append(out, domain.Contact{GUID: u.guid, Name: name, Raw: truncate(u.raw, domain.MaxRawRunes),
			Reaction: u.reaction, LastSeen: u.last.UnixMilli(), Fixes: []domain.Fix{u.fix}})
	}
	sortContacts(out)
	return out
}

func moved(a, b domain.Fix) float64 { return math.Max(math.Abs(a.X-b.X), math.Abs(a.Y-b.Y)) }

// Realm returns the detected realm suffix ("" until known).
func (t *Tracker) Realm() string { return t.namer.Suffix() }
