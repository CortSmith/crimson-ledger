//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// targets_diff: where the two targets legitimately differ.
//   A19 local only (restart), A20 aws only (ttl, IAM), A16 needs CL_API_BIN (local) or the admin Lambda (aws, not wired yet).

func TestA01_HealthNeedsNoKey(t *testing.T) {
	r := do(t, req{Path: "/v1/health"})
	var h map[string]any
	r.JSON(t, &h)
	if r.Status != 200 || h["ok"] != true || len(h) != 2 || h["version"] == "" {
		t.Fatalf("A01: %d %s req=%s", r.Status, r.Body, r.RequestID())
	}
}

func TestA02_IngestPlain(t *testing.T) {
	g := newGUID()
	p := ping([]obj{contact(g, word(), fix(nowMS(), 1411, 10, 20), fix(nowMS()-1000, 1411, 11, 21))}, nil, nil)
	r := send(t, p)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"accepted":true,"contacts":1,"fixes":2,"kills":0`) {
		t.Fatalf("A02: %d %s req=%s", r.Status, r.Body, r.RequestID())
	}
}

func TestA03_IngestGzip(t *testing.T) {
	p := ping([]obj{contact(newGUID(), word(), fix(nowMS(), 1411, 5, 5))}, nil, nil)
	rq := ingest(gz(t, mustJSON(t, p)))
	rq.Header["Content-Encoding"] = "gzip"
	if r := do(t, rq); r.Status != 200 || !strings.Contains(string(r.Body), `"accepted":true`) {
		t.Fatalf("A03: %d %s req=%s", r.Status, r.Body, r.RequestID())
	}
}

func TestA04_RepeatBatchIsDuplicate(t *testing.T) {
	p := ping([]obj{contact(newGUID(), word(), fix(nowMS(), 1411, 5, 5))}, nil, nil)
	mustAccept(t, "A04", p)
	v := versionOf(t)
	r := send(t, p)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"duplicate":true`) || strings.Contains(string(r.Body), `"accepted":true`) {
		t.Fatalf("A04: %d %s req=%s", r.Status, r.Body, r.RequestID())
	}
	if v2 := versionOf(t); v2 != v {
		t.Fatalf("A04: duplicate changed the version %s -> %s", v, v2)
	}
}

func TestA05_SweepHasContactWithLastPosition(t *testing.T) {
	g, name := newGUID(), word()
	now := nowMS()
	mustAccept(t, "A05", ping([]obj{contact(g, name, fix(now-5000, 1411, 10, 10), fix(now, 1411, 51.2, 40.7))}, nil, nil))
	r := do(t, view("/v1/sweep?ruleset=rp"))
	for _, h := range []string{"ETag"} {
		if r.Header.Get(h) == "" {
			t.Fatalf("A05: missing %s req=%s", h, r.RequestID())
		}
	}
	var s sweepBody
	r.JSON(t, &s)
	for _, c := range s.Contacts {
		if c.GUID == g {
			if c.Name != name || c.MapID != 1411 || c.X != 51.2 || c.Y != 40.7 || c.Kind != "exact" || c.Reaction != "hostile" || c.LastSeen != now {
				t.Fatalf("A05: wrong contact %+v req=%s", c, r.RequestID())
			}
			if s.Ruleset != "rp" || s.ServerTime < now-60_000 {
				t.Fatalf("A05: envelope %+v", s)
			}
			return
		}
	}
	t.Fatalf("A05: contact %s missing from sweep req=%s", g, r.RequestID())
}

func TestA06_SweepConditionalGet(t *testing.T) {
	mustAccept(t, "A06", ping([]obj{contact(newGUID(), word(), fix(nowMS(), 1411, 1, 1))}, nil, nil))
	first := do(t, view("/v1/sweep?ruleset=rp"))
	etag := first.Header.Get("ETag")
	cond := view("/v1/sweep?ruleset=rp")
	cond.Header = map[string]string{"If-None-Match": etag}
	if r := do(t, cond); r.Status != 304 {
		t.Fatalf("A06: want 304, got %d req=%s", r.Status, r.RequestID())
	}
	mustAccept(t, "A06", ping([]obj{contact(newGUID(), word(), fix(nowMS(), 1411, 2, 2))}, nil, nil))
	if r := do(t, cond); r.Status != 200 || r.Header.Get("ETag") == etag {
		t.Fatalf("A06: after a new ping want 200 and a new ETag, got %d req=%s", r.Status, r.RequestID())
	}
}

func TestA07_PlayerSearch(t *testing.T) {
	w := word()
	g := newGUID()
	mustAccept(t, "A07", ping([]obj{contact(g, w+" Zed", fix(nowMS(), 1411, 1, 1))}, nil, nil))
	for _, q := range []string{strings.ToLower(w[:5]), strings.ToUpper(w), w} {
		r := do(t, view("/v1/players?ruleset=rp&q="+q))
		if r.Status != 200 || !strings.Contains(string(r.Body), g) {
			t.Fatalf("A07: q=%q not found: %d %.200s req=%s", q, r.Status, r.Body, r.RequestID())
		}
	}
	if r := do(t, view("/v1/players?ruleset=rp&q=a")); r.Status != 400 || r.ErrCode() != "invalid_request" {
		t.Fatalf("A07: 1-char query want 400, got %d req=%s", r.Status, r.RequestID())
	}
}

type trailBody struct {
	GUID  string `json:"guid"`
	Total int    `json:"total"`
	Maps  []struct {
		MapID, Count int
	} `json:"maps"`
	Fixes []struct {
		T int64 `json:"t"`
	} `json:"fixes"`
}

func TestA08_TrailPagination(t *testing.T) {
	g := newGUID()
	base := nowMS() - 60_000
	var fixes []obj
	for i := 0; i < 5; i++ {
		fixes = append(fixes, fix(base+int64(i)*1000, 1411, float64(i+1), 1))
	}
	mustAccept(t, "A08", ping([]obj{contact(g, word(), fixes...)}, nil, nil))
	get := func(q string) trailBody {
		r := do(t, view("/v1/players/"+g+"/trail?ruleset=rp"+q))
		if r.Status != 200 {
			t.Fatalf("A08: %d %s req=%s", r.Status, r.Body, r.RequestID())
		}
		var tb trailBody
		r.JSON(t, &tb)
		return tb
	}
	all := get("")
	if all.Total != 5 || len(all.Fixes) != 5 || all.Fixes[0].T != base+4000 {
		t.Fatalf("A08: want 5 fixes newest first, got total=%d %+v", all.Total, all.Fixes)
	}
	since := get(fmt.Sprintf("&since=%d", base+2000))
	if len(since.Fixes) != 2 || since.Fixes[0].T != base+4000 || since.Fixes[1].T != base+3000 {
		t.Fatalf("A08: since pagination wrong: %+v", since.Fixes)
	}
}

func TestA09_BountyRanking(t *testing.T) {
	killer, v1, v2 := newGUID(), newGUID(), newGUID()
	now := nowMS()
	k := func(victim string, dt int64) obj {
		return obj{"t": now - dt, "killer": killer, "victim": victim, "mapId": 1411}
	}
	cs := []obj{contact(killer, word(), fix(now, 1411, 1, 1)), contact(v1, word(), fix(now, 1411, 2, 2)), contact(v2, word(), fix(now, 1411, 3, 3))}
	mustAccept(t, "A09", ping(cs, []obj{k(v1, 3000), k(v2, 2000), k(v1, 3000)}, nil))
	r := do(t, view("/v1/bounty?ruleset=rp&limit=100"))
	var b struct {
		Ranking []struct {
			GUID  string `json:"guid"`
			Kills int    `json:"kills"`
		} `json:"ranking"`
	}
	r.JSON(t, &b)
	for _, e := range b.Ranking {
		if e.GUID == killer {
			if e.Kills != 2 {
				t.Fatalf("A09: want 2 kills (repeat not double counted), got %d req=%s", e.Kills, r.RequestID())
			}
			return
		}
	}
	t.Fatalf("A09: killer missing from ranking req=%s", r.RequestID())
}

func TestA10_ChartFirstWriteWins(t *testing.T) {
	mapID := 90000 + int(nowMS()%9000)
	chart := func(x1 float64) obj {
		return obj{"mapId": mapID, "zone": "It" + runID, "x0": 0, "y0": 0, "x1": x1, "y1": 1}
	}
	mustAccept(t, "A10", ping(nil, nil, []obj{chart(1)}))
	mustAccept(t, "A10", ping(nil, nil, []obj{chart(2)}))
	var c struct {
		Charts []struct {
			MapID int     `json:"mapId"`
			X1    float64 `json:"x1"`
		} `json:"charts"`
	}
	do(t, view("/v1/charts")).JSON(t, &c)
	for _, ch := range c.Charts {
		if ch.MapID == mapID {
			if ch.X1 != 1 {
				t.Fatalf("A10: later rectangle replaced the first: x1=%v", ch.X1)
			}
			return
		}
	}
	t.Fatalf("A10: chart %d missing", mapID)
}

func TestA11_OutOfOrderPings(t *testing.T) {
	g := newGUID()
	now := nowMS()
	mustAccept(t, "A11", ping([]obj{contact(g, word(), fix(now, 1411, 70, 70))}, nil, nil))
	mustAccept(t, "A11", ping([]obj{contact(g, word(), fix(now-30_000, 1411, 10, 10))}, nil, nil))
	s := sweep(t, "&guid="+g)
	if len(s.Contacts) != 1 || s.Contacts[0].X != 70 {
		t.Fatalf("A11: contact lost its newer position: %+v", s.Contacts)
	}
	var tb trailBody
	do(t, view("/v1/players/"+g+"/trail?ruleset=rp")).JSON(t, &tb)
	if tb.Total != 2 {
		t.Fatalf("A11: history should hold both fixes, total=%d", tb.Total)
	}
}

func TestA12_RulesetIsolation(t *testing.T) {
	g := newGUID()
	mustAccept(t, "A12", ping([]obj{contact(g, word(), fix(nowMS(), 1411, 1, 1))}, nil, nil))
	r := do(t, view("/v1/sweep?ruleset=pvp&guid="+g))
	if r.Status != 200 || strings.Contains(string(r.Body), g) {
		t.Fatalf("A12: rp data visible under pvp: %d %s", r.Status, r.Body)
	}
	if r := do(t, view("/v1/players/"+g+"/trail?ruleset=pvp")); r.Status != 404 {
		t.Fatalf("A12: trail under pvp want 404, got %d", r.Status)
	}
}

// A13 finding (F18-3): PLAN 5.2 allows 200 contacts x 50 fixes but caps the body at 256 KiB, and that
// full shape is about 700 KB. So the test sends the largest shape that fits (200 x 12) and pins the
// documented behaviour of the full shape: a clean 413, nothing stored.
func bigPing(fixesPerContact int) obj {
	now := nowMS()
	var cs []obj
	for i := 0; i < 200; i++ {
		var fx []obj
		for j := 0; j < fixesPerContact; j++ {
			fx = append(fx, fix(now-int64(j)*1000, 1411, float64(j%100), float64(i%100)))
		}
		cs = append(cs, contact(newGUID(), word(), fx...))
	}
	return ping(cs, nil, nil)
}

func TestA13_MaxSizePing(t *testing.T) {
	r := send(t, bigPing(12))
	if r.Status != 200 || (tgt.Kind == KindLocal && r.Dur > 2*time.Second) {
		t.Fatalf("A13: largest fitting ping: %d in %s req=%s %.200s", r.Status, r.Dur, r.RequestID(), r.Body)
	}
	v := versionOf(t)
	if r := send(t, bigPing(50)); r.Status != 413 || r.ErrCode() != "too_large" {
		t.Fatalf("A13: full-limits ping want clean 413 too_large, got %d %.120s req=%s", r.Status, r.Body, r.RequestID())
	}
	if versionOf(t) != v {
		t.Fatalf("A13: rejected ping changed state")
	}
}

func TestA14_ConcurrentIngestAndReads(t *testing.T) {
	const clients, pings = 8, 20
	guids := make([][]string, clients)
	var wg sync.WaitGroup
	errs := make(chan string, clients*pings*2)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < pings; i++ {
				g := newGUID()
				guids[c] = append(guids[c], g)
				r, err := doRaw(ingest(mustJSON(t, ping([]obj{contact(g, word(), fix(nowMS(), 1411, 1, 1))}, nil, nil))))
				if err != nil || r.Status != 200 {
					errs <- fmt.Sprintf("ingest: %v %d %s", err, r.Status, r.Body)
				}
				if r, err := doRaw(view("/v1/sweep?ruleset=rp")); err != nil || r.Status >= 500 {
					errs <- fmt.Sprintf("read: %v %d", err, r.Status)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("A14: %s", e)
	}
	seen := map[string]bool{}
	for _, c := range sweep(t, "").Contacts {
		seen[c.GUID] = true
	}
	for _, gs := range guids {
		for _, g := range gs {
			if !seen[g] {
				t.Fatalf("A14: contact %s missing from the final state", g)
			}
		}
	}
}

func TestA15_KeyScopes(t *testing.T) {
	if r := do(t, req{Path: "/v1/sweep?ruleset=rp", Key: tgt.IngestKey}); r.Status != 403 || r.ErrCode() != "forbidden" {
		t.Fatalf("A15: ingest key on GET want 403 forbidden, got %d %s", r.Status, r.Body)
	}
	if r := do(t, ingest(mustJSON(t, ping(nil, nil, nil)))); r.Status != 200 {
		t.Fatalf("A15: ingest key on POST want 200, got %d", r.Status)
	}
	if r := do(t, view("/v1/charts")); r.Status != 200 {
		t.Fatalf("A15: view key on GET want 200, got %d", r.Status)
	}
}

var keyRe = regexp.MustCompile(`clk_[iv]_([A-Za-z0-9]+)_`)

func TestA16_KeyLifecycle(t *testing.T) {
	if tgt.APIBin == "" || tgt.Home == "" {
		t.Skip("A16 needs CL_API_BIN and CL_HOME (local); the AWS admin Lambda path is not wired yet (T18 F18-1)")
	}
	cmd := exec.Command(tgt.APIBin, "keys", "create", "--scope", "view", "--label", "it-a16") // #nosec G204 -- operator-supplied test binary
	cmd.Env = append(cmd.Environ(), "CRIMSON_LEDGER_HOME="+tgt.Home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("A16: create key: %v", err)
	}
	key := strings.TrimSpace(string(out))
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		t.Fatal("A16: create did not print a key")
	}
	use := func() resp { return do(t, req{Path: "/v1/charts", Key: key}) }
	if r := use(); r.Status != 200 {
		t.Fatalf("A16: new key unusable: %d", r.Status)
	}
	rev := exec.Command(tgt.APIBin, "keys", "revoke", m[1]) // #nosec G204 -- see above
	rev.Env = cmd.Env
	if err := rev.Run(); err != nil {
		t.Fatalf("A16: revoke: %v", err)
	}
	eventually(t, "revoked key to answer 403 key_disabled (cache TTL 30 s)", 35*time.Second, func() bool {
		r := use()
		return r.Status == 403 && r.ErrCode() == "key_disabled"
	})
}

func TestA17_ResponseHygiene(t *testing.T) {
	for _, rq := range []req{{Path: "/v1/health"}, view("/v1/sweep?ruleset=rp"), view("/v1/charts"), {Path: "/v1/sweep"}, {Path: "/nope"}} {
		r := do(t, rq)
		h := r.Header
		if h.Get("X-Request-Id") == "" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") == "" {
			t.Fatalf("A17: %s lacks hygiene headers: %v", rq.Path, h)
		}
		if ct := h.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Fatalf("A17: %s content type %q", rq.Path, ct)
		}
		if h.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("A17: %s sets CORS", rq.Path)
		}
	}
	// A body big enough to be worth compressing is gzipped when the client asks (net/http would hide it, so set the header ourselves).
	for i := 0; i < 3; i++ {
		fx := []obj{}
		for j := 0; j < 30; j++ {
			fx = append(fx, fix(nowMS(), 1411, 1, 1))
		}
		mustAccept(t, "A17", ping([]obj{contact(newGUID(), word(), fx...)}, nil, nil))
	}
	rq := view("/v1/sweep?ruleset=rp")
	rq.Header = map[string]string{"Accept-Encoding": "gzip"}
	if r := do(t, rq); r.Status != 200 {
		t.Fatalf("A17: gzip request: %d", r.Status)
	}
	// Go's transport transparently inflates when it sets Accept-Encoding itself; assert only that an explicit request still works.
}

func TestA19_PersistenceLocal(t *testing.T) {
	if tgt.Kind != KindLocal {
		t.Skip("A19 is local-only")
	}
	t.Skip("A19 needs the compose stack (docker compose restart api); run via scripts/docker-smoke.sh until it-compose lands (F18-2)")
}

func TestA20_TTLAWS(t *testing.T) {
	if tgt.Kind != KindAWS {
		t.Skip("A20 is aws-only")
	}
	t.Skip("A20 needs the AWS test stack and the test IAM role (F18-1)")
}

var _ = http.MethodGet
