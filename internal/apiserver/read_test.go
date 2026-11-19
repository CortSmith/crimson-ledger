package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

const (
	guidA = "Player-1329-0A1B2C3D"
	guidB = "Player-1329-0A1B2C3E"
)

func readHarness(t testing.TB) *harness {
	t.Helper()
	hn := newHarness(t, func(o *Options) { o.RateLimitPerMin = 100000 })
	hn.store.sweep = func(_ store.SweepQuery) []domain.SweepContact {
		return []domain.SweepContact{{GUID: guidA, Name: "Mc Cheeki", Reaction: domain.ReactionHostile, LastSeen: 5, MapID: 1411, X: 1, Y: 2, Kind: domain.FixExact}}
	}
	hn.store.players = func(domain.Ruleset, string, int) []store.PlayerSummary {
		return []store.PlayerSummary{{GUID: guidA, Name: "Mc Cheeki", Reaction: domain.ReactionHostile, Fixes: 3, FirstSeen: 1, LastSeen: 5}}
	}
	hn.store.trail = func(_ domain.Ruleset, g string, _ store.TrailFilter) (store.Trail, error) {
		if g != guidA {
			return store.Trail{}, store.ErrNotFound
		}
		return store.Trail{GUID: g, Name: "Mc Cheeki", Total: 1, Maps: []store.TrailMap{{MapID: 1411, Zone: "Durotar", Count: 1}},
			Fixes: []domain.Fix{{T: 5, MapID: 1411, X: 1, Y: 2, Kind: domain.FixExact}}}, nil
	}
	hn.store.bounty = func(domain.Ruleset, int) []domain.KillTotal {
		return []domain.KillTotal{{GUID: guidA, Name: "Mc Cheeki", Kills: 2, LastKill: 9}}
	}
	hn.store.charts = func(context.Context) ([]domain.Chart, error) {
		return []domain.Chart{{MapID: 1411, Zone: "Durotar", X1: 1, Y1: 1}}, nil
	}
	return hn
}

func TestReadGoldenShapes(t *testing.T) {
	hn := readHarness(t)
	rows := []struct{ path, want string }{
		{"/v1/sweep?ruleset=pvp", `{"serverTime":1731800000000,"ruleset":"pvp","contacts":[{"guid":"Player-1329-0A1B2C3D","name":"Mc Cheeki","reaction":"hostile","lastSeen":5,"mapId":1411,"x":1,"y":2,"kind":"exact"}]}`},
		{"/v1/players?ruleset=pvp&q=mc", `{"players":[{"guid":"Player-1329-0A1B2C3D","name":"Mc Cheeki","reaction":"hostile","fixes":3,"firstSeen":1,"lastSeen":5}]}`},
		{"/v1/players/" + guidA + "/trail?ruleset=pvp", `{"guid":"Player-1329-0A1B2C3D","name":"Mc Cheeki","total":1,"maps":[{"mapId":1411,"zone":"Durotar","count":1}],"fixes":[{"t":5,"mapId":1411,"x":1,"y":2,"kind":"exact"}]}`},
		{"/v1/bounty?ruleset=pvp", `{"ranking":[{"guid":"Player-1329-0A1B2C3D","name":"Mc Cheeki","kills":2,"lastKill":9}]}`},
		{"/v1/charts", `{"charts":[{"mapId":1411,"zone":"Durotar","x0":0,"y0":0,"x1":1,"y1":1}]}`},
	}
	for _, row := range rows {
		t.Run(row.path, func(t *testing.T) {
			rec := hn.get(row.path, "view")
			if rec.Code != 200 || rec.Body.String() != row.want {
				t.Fatalf("status %d body %s\nwant %s", rec.Code, rec.Body, row.want)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("no ETag")
			}
		})
	}
}

func TestReadEmptyListsAreArrays(t *testing.T) {
	hn := readHarness(t)
	hn.store.sweep = func(store.SweepQuery) []domain.SweepContact { return nil }
	hn.store.bounty = func(domain.Ruleset, int) []domain.KillTotal { return nil }
	for path, sub := range map[string]string{"/v1/sweep?ruleset=rp": `"contacts":[]`, "/v1/bounty?ruleset=rp": `"ranking":[]`} {
		if body := hn.get(path, "view").Body.String(); !strings.Contains(body, sub) {
			t.Errorf("%s: %s lacks %s", path, body, sub)
		}
	}
}

func TestQueryStrictness(t *testing.T) {
	hn := readHarness(t)
	long := strings.Repeat("a", 1024)
	rows := []struct{ name, path, sub string }{
		{"unknown param", "/v1/sweep?ruleset=pvp&x=1", `"x"`},
		{"duplicate param", "/v1/sweep?ruleset=pvp&ruleset=rp", "once"},
		{"missing ruleset", "/v1/sweep", "required"},
		{"bad ruleset", "/v1/sweep?ruleset=all", "must be"},
		{"limit zero", "/v1/bounty?ruleset=pvp&limit=0", "1 to 100"},
		{"limit negative", "/v1/bounty?ruleset=pvp&limit=-1", "1 to 100"},
		{"limit text", "/v1/bounty?ruleset=pvp&limit=abc", "1 to 100"},
		{"limit huge", "/v1/bounty?ruleset=pvp&limit=999999999999999999999", "1 to 100"},
		{"limit over max", "/v1/bounty?ruleset=pvp&limit=101", "1 to 100"},
		{"players limit over max", "/v1/players?ruleset=pvp&q=mc&limit=51", "1 to 50"},
		{"trail limit over max", "/v1/players/" + guidA + "/trail?ruleset=pvp&limit=2001", "1 to 2000"},
		{"nul byte", "/v1/players?ruleset=pvp&q=mc%00", "control"},
		{"long q", "/v1/players?ruleset=pvp&q=" + long, "longer"},
		{"short q", "/v1/players?ruleset=pvp&q=a", "2 to 24"},
		{"missing q", "/v1/players?ruleset=pvp", "required"},
		{"bad escape", "/v1/sweep?ruleset=pvp&zone=%zz", "malformed"},
		{"semicolon", "/v1/sweep?ruleset=pvp;zone=1", "malformed"},
		{"bad guid path", "/v1/players/nope/trail?ruleset=pvp", "guid"},
		{"bad since", "/v1/players/" + guidA + "/trail?ruleset=pvp&since=-5", "epoch"},
		{"maxAgeSec too big", "/v1/sweep?ruleset=pvp&maxAgeSec=86401", "1 to 86400"},
		{"charts takes none", "/v1/charts?ruleset=pvp", "unknown"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			rec := hn.get(row.path, "view")
			e := decodeErr(t, rec)
			if rec.Code != 400 || e.Error.Code != "invalid_request" || !strings.Contains(e.Error.Message, row.sub) {
				t.Fatalf("status %d %+v, want 400 containing %q", rec.Code, e.Error, row.sub)
			}
		})
	}
	if hn.store.calls != 0 {
		t.Errorf("a rejected query reached the store %d times", hn.store.calls)
	}
}

func TestSweep_PassesFiltersToStore(t *testing.T) {
	hn := readHarness(t)
	var got store.SweepQuery
	hn.store.sweep = func(q store.SweepQuery) []domain.SweepContact { got = q; return nil }
	hn.get("/v1/sweep?ruleset=rp&zone=1411&guid="+guidB+"&maxAgeSec=60", "view")
	if got.Ruleset != domain.RulesetRP || *got.MapID != 1411 || got.GUID != guidB || got.MaxAge != time.Minute || !got.Now.Equal(hn.clock.Now()) {
		t.Fatalf("store got %+v", got)
	}
}

func TestPlayers_NormalizesSearch(t *testing.T) {
	hn := readHarness(t)
	var text string
	var limit int
	hn.store.players = func(_ domain.Ruleset, q string, n int) []store.PlayerSummary { text, limit = q, n; return nil }
	hn.get("/v1/players?ruleset=pvp&q=%C3%89LAN", "view")
	if text != "elan" || limit != 20 {
		t.Fatalf("got %q limit %d", text, limit)
	}
}

func TestTrail(t *testing.T) {
	hn := readHarness(t)
	t.Run("unknown guid is 404", func(t *testing.T) {
		rec := hn.get("/v1/players/"+guidB+"/trail?ruleset=pvp", "view")
		if rec.Code != 404 || decodeErr(t, rec).Error.Code != "not_found" {
			t.Fatalf("status %d %s", rec.Code, rec.Body)
		}
	})
	t.Run("since is exclusive", func(t *testing.T) {
		var f store.TrailFilter
		hn.store.trail = func(_ domain.Ruleset, g string, fl store.TrailFilter) (store.Trail, error) {
			f = fl
			return store.Trail{GUID: g}, nil
		}
		hn.get("/v1/players/"+guidA+"/trail?ruleset=pvp&since=100&limit=7&map=1411", "view")
		if f.Since != 101 || f.Limit != 7 || f.MapID == nil || *f.MapID != 1411 {
			t.Fatalf("filter %+v", f)
		}
	})
}

func TestETag(t *testing.T) {
	hn := readHarness(t)
	path := "/v1/sweep?ruleset=pvp"
	tag := hn.get(path, "view").Header().Get("ETag")
	cond := func(path, tag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "192.0.2.1:5000"
		req.Header.Set("Authorization", "Bearer "+hn.tokens["view"])
		req.Header.Set("If-None-Match", tag)
		rec := httptest.NewRecorder()
		hn.h.ServeHTTP(rec, req)
		return rec
	}
	t.Run("304 skips the data query", func(t *testing.T) {
		hn.store.calls = 0
		rec := cond(path, tag)
		if rec.Code != 304 || rec.Body.Len() != 0 || hn.store.calls != 0 || rec.Header().Get("ETag") != tag {
			t.Fatalf("status %d body %q calls %d", rec.Code, rec.Body, hn.store.calls)
		}
	})
	t.Run("changes after a ping", func(t *testing.T) {
		hn.store.version = func(domain.Ruleset) int64 { return 2 }
		defer func() { hn.store.version = nil }()
		if rec := cond(path, tag); rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("changes with the query", func(t *testing.T) {
		if rec := cond("/v1/sweep?ruleset=pvp&maxAgeSec=60", tag); rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("changes across the time bucket", func(t *testing.T) {
		hn.clock.Advance(2 * etagBucket)
		if rec := cond(path, tag); rec.Code != 200 {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("star and lists match", func(t *testing.T) {
		tag := hn.get(path, "view").Header().Get("ETag")
		for _, h := range []string{"*", `W/"x", ` + tag, strings.TrimPrefix(tag, "W/")} {
			if rec := cond(path, h); rec.Code != 304 {
				t.Errorf("If-None-Match %q: status %d", h, rec.Code)
			}
		}
	})
}

func TestScopes(t *testing.T) {
	hn := readHarness(t)
	for _, path := range []string{"/v1/sweep?ruleset=pvp", "/v1/players?ruleset=pvp&q=mc", "/v1/bounty?ruleset=pvp", "/v1/charts", "/v1/players/" + guidA + "/trail?ruleset=pvp"} {
		if rec := hn.get(path, "ingest"); rec.Code != 403 {
			t.Errorf("ingest key on %s: %d", path, rec.Code)
		}
		if rec := hn.get(path, ""); rec.Code != 401 {
			t.Errorf("no key on %s: %d", path, rec.Code)
		}
	}
	if rec := hn.do("POST", "/v1/sweep?ruleset=pvp", "Bearer "+hn.tokens["view"], "", nil); rec.Code != 405 {
		t.Errorf("POST sweep: %d", rec.Code)
	}
}

func TestCharts_LongCacheAndGzip(t *testing.T) {
	hn := readHarness(t)
	if cc := hn.get("/v1/charts", "view").Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("Cache-Control %q", cc)
	}
	big := make([]domain.Chart, 100)
	hn.store.charts = func(context.Context) ([]domain.Chart, error) { return big, nil }
	req := httptest.NewRequest("GET", "/v1/charts", nil)
	req.RemoteAddr = "192.0.2.1:5000"
	req.Header.Set("Authorization", "Bearer "+hn.tokens["view"])
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	hn.h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("headers %v", rec.Header())
	}
}

func TestStoreFailureIs500WithoutDetail(t *testing.T) {
	hn := readHarness(t)
	hn.store.trail = func(domain.Ruleset, string, store.TrailFilter) (store.Trail, error) {
		return store.Trail{}, http.ErrAbortHandler
	}
	rec := hn.get("/v1/players/"+guidA+"/trail?ruleset=pvp", "view")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "abort") {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
}

func FuzzQuery(f *testing.F) {
	for _, s := range []string{"ruleset=pvp", "ruleset=pvp&ruleset=rp", "q=%C3%A9", "ruleset=pvp%00", "limit=-1", "%zz", ";;"} {
		f.Add(s)
	}
	hn := readHarness(f)
	f.Fuzz(func(t *testing.T, raw string) {
		for _, p := range []string{"/v1/sweep", "/v1/players", "/v1/players/" + guidA + "/trail", "/v1/bounty", "/v1/charts"} {
			req := httptest.NewRequest("GET", "/", nil)
			req.URL.Path, req.URL.RawQuery = p, raw
			req.RemoteAddr = "192.0.2.1:5000"
			req.Header.Set("Authorization", "Bearer "+hn.tokens["view"])
			rec := httptest.NewRecorder()
			hn.h.ServeHTTP(rec, req)
			switch rec.Code {
			case 200, 304, 400, 429:
			default:
				t.Fatalf("%s?%q: status %d", p, raw, rec.Code)
			}
		}
	})
}
