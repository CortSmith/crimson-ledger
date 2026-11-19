//go:build integration

package abuse

import "testing"

var readRoutes = []string{
	"/v1/sweep?ruleset=rp", "/v1/players?ruleset=rp&q=ab", "/v1/players/Player-1-ABCD/trail?ruleset=rp",
	"/v1/bounty?ruleset=rp", "/v1/charts",
}

func TestX201_ViewKeyCannotWrite(t *testing.T) {
	unchanged(t, "X201", func() {
		a := do(t, viewer(), "POST", "/v1/pings", js(t, pingBody([]obj{contactOf(guid(), "Viewonly", nowMS())}, nil)),
			map[string]string{"Content-Type": "application/json"})
		if a.Status != 403 {
			t.Errorf("X201: want 403, got %d %.120s", a.Status, a.Body)
		}
	})
}

func TestX202_IngestKeyCannotRead(t *testing.T) {
	for _, r := range readRoutes {
		if a := get(t, writer(), r); a.Status != 403 {
			t.Errorf("X202 %s: want 403, got %d", r, a.Status)
		}
	}
}

func TestX203_AdminAndDebugRoutesDoNotExist(t *testing.T) {
	paths := []string{"/admin", "/v1/keys", "/v1/admin/keys", "/internal", "/debug/pprof/", "/debug/vars", "/metrics",
		"/actuator", "/.env", "/.git/config", "/swagger", "/openapi.json", "/v1/export", "/v1/dump", "/v1/db",
		"/graphql", "/robots.txt", "/favicon.ico", "/", "/v1", "/v1/"}
	var first []byte
	for _, p := range paths {
		for _, a := range []Actor{anon(), viewer(), writer()} {
			ans := do(t, a, "GET", p, nil, nil)
			if ans.Status == 200 {
				t.Errorf("X203 %s as %s: 200 (%.80s)", p, a.Name, ans.Body)
			}
			if ans.Status == 404 {
				if !IsUniformError(ans) {
					t.Errorf("X203 %s as %s: 404 is not the uniform error body: %.120s", p, a.Name, ans.Body)
				}
				if s := ans.Header.Get("Server"); s != "" {
					t.Errorf("X203 %s: Server banner %q", p, s)
				}
				_ = first
			}
		}
	}
}

func TestX204_VerbTampering(t *testing.T) {
	unchanged(t, "X204", func() {
		for _, r := range readRoutes {
			for _, m := range []string{"PUT", "PATCH", "DELETE"} {
				if a := do(t, viewer(), m, r, nil, nil); a.Status != 405 && a.Status != 404 {
					t.Errorf("X204 %s %s: want 405, got %d", m, r, a.Status)
				}
			}
			a := do(t, viewer(), "GET", r, nil, map[string]string{"X-HTTP-Method-Override": "DELETE", "X-Method-Override": "POST"})
			if a.Status >= 400 && a.Status != 404 && a.Status != 400 {
				t.Errorf("X204 override header changed behaviour on %s: %d", r, a.Status)
			}
		}
		if a := do(t, viewer(), "POST", "/v1/sweep?ruleset=rp&_method=POST", nil, nil); a.Status == 200 {
			t.Errorf("X204: POST to a read route succeeded")
		}
		if a := do(t, viewer(), "GET", "/v1/pings", nil, nil); a.Status == 200 {
			t.Errorf("X204: GET on the ingest route returned data")
		}
	})
}

func TestX205_PathTricks(t *testing.T) {
	for _, p := range []string{"/v1/sweep/", "/v1//sweep", "/v1/./sweep", "/v1/%2e%2e/health", "/V1/SWEEP", "/v1/sweep%00",
		"/v1/sweep;x=1", "/v1/players/..%2fsweep/trail", "/v1/%73weep", "/v1/sweep%2f", "//v1/sweep", "/v1/health/../sweep"} {
		// Ingest key on a view route: any 2xx means a path trick escalated scope.
		a := raw(t, Raw{Target: p + qs(p), Headers: append([][2]string{{"Authorization", "Bearer " + tgt.IngestKey}}, hdrXFF()...)})
		if a.Status == 200 {
			t.Errorf("X205 %q: ingest key reached a read handler (200)", p)
		}
		if a.Status >= 300 && a.Status < 400 {
			t.Errorf("X205 %q: redirect %d", p, a.Status)
		}
	}
}

func qs(p string) string {
	if len(p) > 0 && (contains([]byte(p), "?")) {
		return ""
	}
	return "?ruleset=rp"
}

func TestX207_IngestKeyLearnsCountsOnly(t *testing.T) {
	g := guid()
	a := do(t, writer(), "POST", "/v1/pings", js(t, pingBody([]obj{contactOf(g, "Counted", nowMS())}, nil)),
		map[string]string{"Content-Type": "application/json"})
	if a.Status != 200 {
		t.Fatalf("setup ping: %d %.120s", a.Status, a.Body)
	}
	if contains(a.Body, g) || contains(a.Body, "Counted") {
		t.Errorf("X207: ingest response reflects submitted data: %s", a.Body)
	}
	if et := a.Header.Get("ETag"); et != "" {
		t.Errorf("X207: ingest response carries an ETag %q (state side channel)", et)
	}
}
