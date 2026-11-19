package testkit

import (
	"fmt"
	"math/rand/v2"
	"time"

	"crimsonledger/internal/domain"
)

// SessionOptions shapes a generated session; zero values give 3 players over 2 minutes.
type SessionOptions struct {
	Players int // hostile players seen
	Minutes int // length of the session, one ping per minute
}

// Player is one generated hostile player.
type Player struct {
	GUID string
	Name string
}

// Session is a deterministic fake play session in both of its forms.
type Session struct {
	Lines   []string      // combat-log lines, oldest first
	Pings   []domain.Ping // what a perfect logger would upload, one per minute
	Players []Player
}

const (
	hostileFlags = 0x548 // player, hostile (0x40) reaction bit set
	sessionMapID = 1411
	castsPerMin  = 4 // log lines per player per minute
)

// GenerateSession returns the same session for the same seed: log lines the parser accepts and the
// pings they should turn into. Every ping passes domain.ValidatePing at the end of the session.
func GenerateSession(seed uint64, o SessionOptions) Session {
	if o.Players <= 0 {
		o.Players = 3
	}
	if o.Minutes <= 0 {
		o.Minutes = 2
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) // #nosec G404 -- deterministic test data
	var s Session
	for i := 0; i < o.Players; i++ {
		name := fmt.Sprintf("Raider%c%02d", 'A'+rune(rng.IntN(26)), i)
		s.Players = append(s.Players, Player{GUID: fmt.Sprintf("Player-1329-%08X", rng.Uint32()|1<<28), Name: name})
	}
	x, y := make([]float64, o.Players), make([]float64, o.Players)
	for i := range x {
		x[i], y[i] = 20+rng.Float64()*60, 20+rng.Float64()*60
	}
	for m := 0; m < o.Minutes; m++ {
		minute := Epoch.Add(time.Duration(m) * time.Minute)
		ping := domain.Ping{BatchID: fmt.Sprintf("%08x-0000-4000-8000-%012x", seed&0xffffffff, m+1), Source: "session", Ruleset: domain.RulesetPvP}
		for i, pl := range s.Players {
			c := domain.Contact{GUID: pl.GUID, Name: pl.Name, Raw: pl.Name + "-Realm", Reaction: domain.ReactionHostile}
			for k := 0; k < castsPerMin; k++ {
				at := minute.Add(time.Duration(k*10+i) * time.Second)
				x[i] = clamp(x[i] + rng.Float64()*2 - 1)
				y[i] = clamp(y[i] + rng.Float64()*2 - 1)
				s.Lines = append(s.Lines, castLine(at, pl, x[i], y[i]))
				c.LastSeen = at.UnixMilli()
				c.Fixes = append(c.Fixes, domain.Fix{T: at.UnixMilli(), MapID: sessionMapID, X: round2(x[i]), Y: round2(y[i]), Kind: domain.FixExact})
			}
			ping.Contacts = append(ping.Contacts, c)
		}
		ping.SentAt = minute.Add(time.Minute).UnixMilli()
		s.Pings = append(s.Pings, ping)
	}
	return s
}

// castLine renders a SPELL_CAST_SUCCESS line for a hostile player at world position (x, y percent).
func castLine(at time.Time, p Player, x, y float64) string {
	return fmt.Sprintf(`%s  SPELL_CAST_SUCCESS,%s,"%s-Realm-PvP-US",0x%x,0x80000000,0000000000000000,nil,0x80000000,0x80000000,2963,"Bolt",0x1,%s,0000000000000000,67,67,20,0,47,0,0,0,0,184,184,0,%.2f,%.2f,%d,0.6938,0`,
		at.Format("1/2/2006 15:04:05.000")+"-0", p.GUID, p.Name, hostileFlags, p.GUID, x*10, y*10, sessionMapID)
}

func clamp(v float64) float64 { return min(max(v, 1), 99) }

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
