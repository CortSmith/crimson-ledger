// Command seed feeds invented players into a running crimson-api so the viewer has something to show.
//
// It plays a simulated scouting session in real time: Alliance players (reaction "friendly") and Horde
// players (reaction "hostile") start in random zones; every tick each one wanders a little, and now and
// then hops to another zone. Occasionally a Horde and an Alliance player kill each other, which feeds the
// bounty ranking. Everything is made up.
//
//	cd App
//	./crimson-api keys create --scope ingest --label seed     # prints the key once
//	CRIMSON_INGEST_KEY=<key> go run ./scripts/seed            # 10 minutes, 40 players
//	CRIMSON_INGEST_KEY=<key> go run ./scripts/seed -duration 2m -players 100 -interval 2s
//
// The key is read from the environment only, never a flag.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"time"

	"crimsonledger/internal/domain"
)

// zone is a Classic uiMapID with its name; players move in percent coordinates (0-100) inside it.
type zone struct {
	id   int
	name string
}

var zones = []zone{
	{1411, "Durotar"}, {1412, "Mulgore"}, {1413, "The Barrens"}, {1416, "Alterac Mountains"},
	{1417, "Arathi Highlands"}, {1424, "Hillsbrad Foothills"}, {1426, "Dun Morogh"}, {1429, "Elwynn Forest"},
	{1431, "Duskwood"}, {1434, "Stranglethorn Vale"}, {1437, "Wetlands"}, {1438, "Teldrassil"},
	{1440, "Ashenvale"}, {1441, "Thousand Needles"}, {1444, "Feralas"}, {1446, "Tanaris"},
	{1453, "Stormwind City"}, {1454, "Orgrimmar"}, {1455, "Ironforge"},
}

type player struct {
	guid, name, raw string
	horde           bool
	zone            int
	x, y            float64
}

func (p *player) reaction() domain.Reaction {
	if p.horde {
		return domain.ReactionHostile
	}
	return domain.ReactionFriendly
}

func main() {
	var (
		apiURL   = flag.String("api-url", "http://127.0.0.1:8443", "crimson-api address")
		ruleset  = flag.String("ruleset", "pvp", "normal|pvp|rp|rppvp")
		players  = flag.Int("players", 40, "number of fake players (half Alliance, half Horde)")
		duration = flag.Duration("duration", 10*time.Minute, "how long to keep feeding, starting now")
		interval = flag.Duration("interval", 5*time.Second, "time between pings")
		seed     = flag.Uint64("seed", uint64(time.Now().UnixNano()), "random seed (same seed, same cast)")
	)
	flag.Parse()
	if *players < 2 || *players > domain.MaxContacts || *interval < time.Second || *duration < *interval {
		fatalf("need 2 <= players <= %d, interval >= 1s, duration >= interval", domain.MaxContacts)
	}
	key := os.Getenv("CRIMSON_INGEST_KEY")
	if key == "" {
		fatalf("set CRIMSON_INGEST_KEY to an ingest key (crimson-api keys create --scope ingest --label seed)")
	}
	rs := domain.Ruleset(*ruleset)
	if !rs.Valid() {
		fatalf("unknown ruleset %q", *ruleset)
	}

	rng := rand.New(rand.NewPCG(*seed, 0x5eed))
	cast := make([]*player, *players)
	for i := range cast {
		cast[i] = newPlayer(rng, i)
	}
	charts := make([]domain.Chart, len(zones))
	for i, z := range zones {
		charts[i] = domain.Chart{MapID: z.id, Zone: z.name, X0: 0, Y0: 0, X1: 100, Y1: 100}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	end := time.Now().Add(*duration)
	fmt.Printf("seeding %d players (%d Alliance, %d Horde) into %s for %s via %s\n",
		*players, *players/2, *players-*players/2, rs, *duration, *apiURL)

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	for n := 0; ; n++ {
		now := time.Now()
		ping := buildPing(rng, rs, cast, charts, now)
		if err := post(ctx, *apiURL, key, ping); err != nil {
			fatalf("tick %d: %v", n, err)
		}
		fmt.Printf("%s tick %d: %d contacts, %d kills\n", now.Format("15:04:05"), n, len(ping.Contacts), len(ping.Kills))
		if !now.Add(*interval).Before(end) {
			break
		}
		select {
		case <-ctx.Done():
			fmt.Println("interrupted")
			return
		case <-tick.C:
		}
	}
	fmt.Println("done")
}

var (
	starts = []string{"Ar", "Bel", "Cor", "Dra", "El", "Fen", "Gor", "Hal", "Is", "Jun", "Kel", "Lor", "Mor", "Nyx", "Pel", "Ral", "Syl", "Tor", "Vor", "Wyn"}
	ends   = []string{"a", "an", "dor", "en", "is", "on", "us", "wyn", "ak", "iel"}
)

// newPlayer makes player i; odd indexes are Horde. Names stay unique through the numeric tail of the GUID.
func newPlayer(rng *rand.Rand, i int) *player {
	name := starts[rng.IntN(len(starts))] + ends[rng.IntN(len(ends))]
	return &player{
		guid:  fmt.Sprintf("Player-9999-%08X", 0x5EED0000+i),
		name:  name,
		raw:   name + "-Seedrealm",
		horde: i%2 == 1,
		zone:  rng.IntN(len(zones)),
		x:     5 + rng.Float64()*90,
		y:     5 + rng.Float64()*90,
	}
}

func buildPing(rng *rand.Rand, rs domain.Ruleset, cast []*player, charts []domain.Chart, now time.Time) domain.Ping {
	t := now.UnixMilli()
	p := domain.Ping{
		BatchID: uuidV4(rng), Source: "seed", Ruleset: rs, SentAt: t,
		Contacts: make([]domain.Contact, 0, len(cast)), Charts: charts, Kills: []domain.Kill{},
	}
	for _, pl := range cast {
		if rng.Float64() < 0.04 { // hop to another zone
			pl.zone = rng.IntN(len(zones))
			pl.x, pl.y = 5+rng.Float64()*90, 5+rng.Float64()*90
		} else { // wander up to ~2% of the map
			a, d := rng.Float64()*2*math.Pi, rng.Float64()*2
			pl.x, pl.y = clamp(pl.x+math.Cos(a)*d), clamp(pl.y+math.Sin(a)*d)
		}
		p.Contacts = append(p.Contacts, domain.Contact{
			GUID: pl.guid, Name: pl.name, Raw: pl.raw, Reaction: pl.reaction(), LastSeen: t,
			Fixes: []domain.Fix{{T: t, MapID: zones[pl.zone].id, X: round1(pl.x), Y: round1(pl.y), Kind: domain.FixExact}},
		})
	}
	if rng.Float64() < 0.3 { // one cross-faction kill, for the bounty ranking
		a, b := cast[rng.IntN(len(cast))], cast[rng.IntN(len(cast))]
		if a.horde != b.horde {
			p.Kills = append(p.Kills, domain.Kill{T: t, Killer: a.guid, Victim: b.guid, MapID: zones[a.zone].id})
		}
	}
	return p
}

func clamp(v float64) float64  { return math.Max(1, math.Min(99, v)) }
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// uuidV4 draws a random version-4 UUID from rng.
func uuidV4(rng *rand.Rand) string {
	hi, lo := rng.Uint64(), rng.Uint64()
	hi = hi&^0xF000 | 0x4000     // version 4
	lo = lo&^(0xC<<60) | 0x8<<60 // variant 10xx
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", hi>>32, hi>>16&0xffff, hi&0xffff, lo>>48, lo&0xffffffffffff)
}

func post(ctx context.Context, base, key string, ping domain.Ping) error {
	body, err := json.Marshal(ping)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/pings", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("api answered %s: %s", resp.Status, b)
	}
	return nil
}

func fatalf(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed: "+f+"\n", a...)
	os.Exit(1)
}
